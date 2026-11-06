package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/ai-proxy/ai-portal-go/internal/config"
	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/middleware"
	"github.com/ai-proxy/ai-portal-go/internal/routes"
	"github.com/ai-proxy/ai-portal-go/internal/services"
	"github.com/ai-proxy/ai-portal-go/pkg/audit"
	"github.com/ai-proxy/ai-portal-go/pkg/i18n"
	"github.com/ai-proxy/ai-portal-go/pkg/mail"
	"github.com/ai-proxy/ai-portal-go/web"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Handler handles all web page rendering
type Handler struct {
	db            *db.DB
	cfg           *config.Config
	logger        *zap.Logger
	auth          *middleware.Auth
	mail          *mail.Sender
	audit         *audit.Logger
	rewardSvc     *services.RewardService
	notifSvc      *services.NotificationService
	exchange      *services.ExchangeService
	authRoutes    *routes.AuthRoutes
	i18n          i18n.I18n
	pages         map[string]*template.Template
	partials      *template.Template
	currentLocale string
	renderMu      sync.Mutex
}

// PageData is the common data passed to all templates
type PageData struct {
	Title          string
	Page           string
	Locale         string
	Theme          string
	Currency       string
	CurrencySymbol string
	Timezone       string
	ExchangeRate   float64
	CSRFToken      string
	IsAuth         bool
	UserID         string
	UserEmail      string
	Role           string
	EnterpriseID   bool
	Toast          *ToastMessage
	Data           interface{}

	// SEO (public pages only). Indexable defaults to false in RenderPage so
	// every page is noindex unless a public handler explicitly opts in. When
	// Indexable is true the layout emits index,follow + canonical + OG/Twitter
	// + optional JSON-LD; otherwise it emits noindex,nofollow.
	Indexable   bool
	Canonical   string
	SEOTitle    string
	SEODesc     string
	SEOKeywords string
	SEOImage    string
	JSONLD      template.HTML
}

// ToastMessage represents a toast notification
type ToastMessage struct {
	Type    string // success, error, warning
	Message string
}

// NewHandler creates a new web handler
func NewHandler(database *db.DB, cfg *config.Config, logger *zap.Logger, auth *middleware.Auth, mailSender *mail.Sender, auditLog *audit.Logger, exchange *services.ExchangeService) (*Handler, error) {
	i18n, err := LoadTranslations()
	if err != nil {
		return nil, err
	}

	h := &Handler{
		db:         database,
		cfg:        cfg,
		logger:     logger,
		auth:       auth,
		mail:       mailSender,
		audit:      auditLog,
		rewardSvc:  services.NewRewardService(database, logger, auditLog),
		notifSvc:   services.NewNotificationService(database, logger, i18n),
		exchange:   exchange,
		authRoutes: routes.NewAuthRoutes(database, cfg, logger, auth, auditLog, mailSender, services.NewRewardService(database, logger, auditLog)),
		i18n:       i18n,
		pages:      make(map[string]*template.Template),
	}

	// Browser flows write the session cookie through the shared issuer.
	h.authRoutes.SetCookieWriter(SetJWTWebCookie)

	funcs := h.TemplateFuncs()

	// Parse each page template independently (layout + page content)
	// This avoids name collisions between pages
	pageNames := []string{
		"landing", "login", "login-mfa", "register", "forgot-password",
		"models-gallery",
		"dashboard", "apikeys", "billing", "invoices", "profile",
		"subscriptions", "my-subscriptions", "my-group",
		"plan-and-group", "usage-logs", "status", "user-upstream-ops",
		"notifications",
		"support", "admin-tickets",
		"admin", "admin-users", "admin-models-general", "admin-models-aws", "admin-pricing", "admin-keypool",
		"admin-providers", "admin-subscriptions", "admin-rate-limits",
		"admin-audit-logs", "admin-settings", "admin-enterprise",
		"admin-compliance",
		"admin-keys",
		"admin-groups",
		"admin-announcements", "admin-vouchers", "admin-reconciliation",
		"admin-monitor-upstream", "admin-monitor-downstream",
		"admin-provider-health", "admin-upstream-ops", "admin-reconciliation-ops",
		"admin-upstream-pricing", "admin-usage-explorer",
		// Enterprise admin pages
		"enterprise/dashboard", "enterprise/users", "enterprise/member-detail", "enterprise/keys", "enterprise/key-detail",
		"enterprise/billing", "enterprise/groups",
		"enterprise/usage", "enterprise/audit", "enterprise/alerts", "enterprise/settings",
		"enterprise-accept", "enterprise-apply",
		"admin-enterprise-applications",
		// Super admin pages
		"super/admin-management", "super/payment",
		// Billing pages
		"billing-details", "voucher",
	}

	for _, name := range pageNames {
		t, err := template.New("").Funcs(funcs).ParseFS(web.FS,
			"templates/layout.html",
			"templates/partials/*.html",
			fmt.Sprintf("templates/%s.html", name),
		)
		if err != nil {
			logger.Warn("Page template not found, skipping", zap.String("page", name), zap.Error(err))
			continue
		}
		h.pages[name] = t
	}

	// Parse partials (HTMX fragments, no layout)
	partials, err := template.New("").Funcs(funcs).ParseFS(web.FS,
		"templates/partials/*.html",
	)
	if err != nil {
		logger.Warn("No partials found", zap.Error(err))
	} else {
		h.partials = partials
	}

	return h, nil
}

// Render renders a template with the given name and data
func (h *Handler) Render(c *gin.Context, name string, data PageData) {
	data.CSRFToken = c.GetString("csrf_token")
	if data.Locale == "" {
		data.Locale = h.getLocale(c)
	}
	if data.Theme == "" {
		data.Theme = h.getTheme(c)
	}
	data.IsAuth = c.GetString("user_id") != ""
	data.UserID = c.GetString("user_id")
	data.UserEmail = c.GetString("user_email")
	data.Role = c.GetString("user_role")
	// Enterprise membership flag for layout/sidebar gating (e.g. hide the
	// enterprise registration entry from users who already belong to one).
	// Handlers may pre-set it; otherwise resolve it here for plain users.
	if data.IsAuth && !data.EnterpriseID && data.Role == "user" && h.db != nil {
		var hasEnterprise bool
		_ = h.db.QueryRow(c.Request.Context(),
			`SELECT enterprise_id IS NOT NULL FROM users WHERE id = $1::uuid AND deleted_at IS NULL`,
			data.UserID,
		).Scan(&hasEnterprise)
		data.EnterpriseID = hasEnterprise
	}
	data.Currency = h.getCurrency(c)
	data.CurrencySymbol = config.CurrencySymbol(data.Currency)
	data.Timezone = h.getTimezone(c)
	data.ExchangeRate = h.getExchangeRate(c)
	if t := c.GetString("toast_type"); t != "" {
		data.Toast = &ToastMessage{Type: t, Message: c.GetString("toast_message")}
	} else if t := c.Query("toast"); t != "" {
		msg := template.HTMLEscapeString(c.Query("message"))
		data.Toast = &ToastMessage{Type: template.HTMLEscapeString(t), Message: msg}
	}

	h.renderMu.Lock()
	h.currentLocale = data.Locale

	t, ok := h.pages[name]
	if !ok {
		h.renderMu.Unlock()
		h.logger.Error("Page template not found", zap.String("page", name))
		c.String(http.StatusNotFound, "Page not found")
		return
	}

	c.Header("Content-Type", "text/html; charset=utf-8")
	err := t.ExecuteTemplate(c.Writer, "layout", data)
	h.renderMu.Unlock()
	if err != nil {
		h.logger.Error("Template render error", zap.String("template", name), zap.Error(err), zap.Any("data", data))
		c.String(http.StatusInternalServerError, "Internal Server Error")
	}
}

// RenderPage renders a page using the layout template
func (h *Handler) RenderPage(c *gin.Context, pageName string, data PageData) {
	data.Page = pageName
	h.Render(c, pageName, data)
}

// RenderPartial renders a partial template (for HTMX responses)
func (h *Handler) RenderPartial(c *gin.Context, name string, data interface{}) {
	if h.partials == nil {
		c.String(http.StatusInternalServerError, "Partials not loaded")
		return
	}

	locale := ""
	// Add common fields if data is a map
	if m, ok := data.(gin.H); ok {
		if _, exists := m["CSRFToken"]; !exists {
			m["CSRFToken"] = c.GetString("csrf_token")
		}
		if _, exists := m["Timezone"]; !exists {
			m["Timezone"] = h.getTimezone(c)
		}
		if _, exists := m["Locale"]; !exists {
			m["Locale"] = h.getLocale(c)
		}
		if _, exists := m["Currency"]; !exists {
			m["Currency"] = h.getCurrency(c)
		}
		if _, exists := m["ExchangeRate"]; !exists {
			m["ExchangeRate"] = h.getExchangeRate(c)
		}
		if l, ok := m["Locale"].(string); ok {
			locale = l
		}
	}

	// Hold the lock for the entire render so a concurrent request cannot
	// mutate currentLocale (read by the "t"/"role" template funcs) mid-render.
	h.renderMu.Lock()
	if locale != "" {
		h.currentLocale = locale
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	err := h.partials.ExecuteTemplate(c.Writer, name, data)
	h.renderMu.Unlock()
	if err != nil {
		h.logger.Error("Partial render error", zap.String("template", name), zap.Error(err))
		c.String(http.StatusInternalServerError, "Internal Server Error")
	}
}

// SetToast sets a toast notification.
// For HTMX requests: sends HX-Trigger header (message URL-encoded to avoid
// non-ASCII chars in HTTP header per RFC 7230).
// For full page loads: stores in gin context for PageData.
func (h *Handler) SetToast(c *gin.Context, toastType, message string) {
	if c.GetHeader("HX-Request") == "true" {
		trigger := map[string]interface{}{
			"showToast": map[string]string{
				"type":    toastType,
				"message": url.PathEscape(message),
			},
		}
		data, _ := json.Marshal(trigger)
		c.Header("HX-Trigger", string(data))
	} else {
		c.Set("toast_type", toastType)
		c.Set("toast_message", message)
	}
}

// Partial renders an HTMX partial (no layout)
func (h *Handler) Partial(c *gin.Context, name string, data interface{}) {
	if h.partials == nil {
		c.String(http.StatusNotFound, "Partial not found")
		return
	}
	locale := ""
	// Add common fields if data is a map
	if m, ok := data.(gin.H); ok {
		if _, exists := m["CSRFToken"]; !exists {
			m["CSRFToken"] = c.GetString("csrf_token")
		}
		if _, exists := m["Timezone"]; !exists {
			m["Timezone"] = h.getTimezone(c)
		}
		if _, exists := m["Locale"]; !exists {
			m["Locale"] = h.getLocale(c)
		}
		if _, exists := m["Currency"]; !exists {
			m["Currency"] = h.getCurrency(c)
		}
		if _, exists := m["ExchangeRate"]; !exists {
			m["ExchangeRate"] = h.getExchangeRate(c)
		}
		if l, ok := m["Locale"].(string); ok {
			locale = l
		}
	}
	// Hold the lock for the entire render so a concurrent request cannot
	// mutate currentLocale (read by the "t"/"role" template funcs) mid-render.
	h.renderMu.Lock()
	if locale != "" {
		h.currentLocale = locale
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	err := h.partials.ExecuteTemplate(c.Writer, name, data)
	h.renderMu.Unlock()
	if err != nil {
		h.logger.Error("Partial render error", zap.String("template", name), zap.Error(err))
		c.String(http.StatusInternalServerError, "Internal Server Error")
	}
}

func (h *Handler) getLocale(c *gin.Context) string {
	// 1. Check cookie
	if cookie, err := c.Cookie("locale"); err == nil && (cookie == "zh" || cookie == "en") {
		return cookie
	}
	// 2. Check Accept-Language header
	lang := c.GetHeader("Accept-Language")
	if len(lang) >= 2 && strings.ToLower(lang[:2]) == "zh" {
		return "zh"
	}
	// 3. Default to English
	return "en"
}

func (h *Handler) getTheme(c *gin.Context) string {
	if cookie, err := c.Cookie("theme"); err == nil {
		return cookie
	}
	return "system"
}

func (h *Handler) getCurrency(c *gin.Context) string {
	// 1. Check cookie
	if cookie, err := c.Cookie("currency"); err == nil && config.IsSupportedCurrency(cookie) {
		return cookie
	}
	// 2. Check DB for logged-in user
	userID := c.GetString("user_id")
	if userID != "" && h.db != nil {
		var currency string
		_ = h.db.QueryRow(c.Request.Context(),
			`SELECT COALESCE(preferred_currency, 'USD') FROM users WHERE id = $1::uuid`,
			userID,
		).Scan(&currency)
		if config.IsSupportedCurrency(currency) {
			return currency
		}
	}
	// 3. Default
	return config.DefaultCurrency
}

func (h *Handler) getTimezone(c *gin.Context) string {
	// 1. Check cookie
	if cookie, err := c.Cookie("timezone"); err == nil && cookie != "" {
		return cookie
	}
	// 2. Check DB for logged-in user
	userID := c.GetString("user_id")
	if userID != "" && h.db != nil {
		var tz string
		_ = h.db.QueryRow(c.Request.Context(),
			`SELECT COALESCE(timezone, 'UTC') FROM users WHERE id = $1::uuid`,
			userID,
		).Scan(&tz)
		if tz != "" {
			return tz
		}
	}
	// 3. Default
	return "UTC"
}

func (h *Handler) getExchangeRate(c *gin.Context) float64 {
	var rateStr string
	if h.db != nil {
		_ = h.db.QueryRow(c.Request.Context(),
			`SELECT value FROM system_settings WHERE key = 'usd_exchange_rate'`,
		).Scan(&rateStr)
	}
	if rateStr != "" {
		rateCents, _ := strconv.Atoi(rateStr)
		if rateCents > 0 {
			return float64(rateCents) / 100.0
		}
	}
	return 1.0
}

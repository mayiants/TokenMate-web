package web

import (
	"database/sql"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mayiants/ai-portal-go/internal/middleware"
	"github.com/mayiants/ai-portal-go/internal/query"
	"github.com/mayiants/ai-portal-go/internal/routes"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// RegisterRoutes registers all web page routes
func (h *Handler) RegisterRoutes(r *gin.RouterGroup, auth *middleware.Auth) {
	// All pages get JWT context from cookie (optional)
	allPages := r.Group("/")
	allPages.Use(JWTMiddlewareWeb(auth, h.logger))
	{
		// Public pages
		allPages.GET("", h.handleLanding)
		allPages.GET("/home", h.handleLanding)
		allPages.GET("/login", h.handleLogin)
		allPages.GET("/login/mfa", h.handleLoginMFA)
		allPages.GET("/register", h.handleRegister)
		allPages.GET("/forgot-password", h.handleForgotPassword)
		allPages.GET("/models", h.handleModelsGallery)

		// Language switcher
		allPages.POST("/lang", h.handleLangSwitch)
		allPages.POST("/theme", h.handleThemeSwitch)

		// Web auth actions (form POSTs)
		allPages.POST("/login", h.HandleLogin)
		allPages.POST("/login/mfa", h.HandleLoginMFA)
		allPages.POST("/register", h.HandleRegister)
		allPages.POST("/logout", h.HandleLogout)
		// Also allow GET logout so direct URL navigation (e.g. browser address bar) works cleanly.
		allPages.GET("/logout", h.HandleLogout)

		// Authenticated pages (require login)
		authPages := allPages.Group("/")
		authPages.Use(RequireAuthWeb())
		{
			authPages.GET("/dashboard", h.handleDashboard)
			authPages.GET("/dashboard/recent-keys", h.handleRecentKeys)
			authPages.GET("/apikeys", h.handleAPIKeys)
			authPages.GET("/apikeys/:id/view", h.HandleViewEnterpriseMemberKey)

			authPages.GET("/billing", h.handleBilling)
			authPages.GET("/billing-details", h.handleBillingDetails)
			authPages.GET("/voucher", h.handleVoucher)

			authPages.GET("/invoices", h.handleInvoices)

			authPages.GET("/profile", h.handleProfile)
			authPages.POST("/profile/settings", h.HandleUpdateProfileSettings)

			authPages.GET("/my-group", h.handleMyGroup)

			// Plan & Group (merged subscriptions + group)
			authPages.GET("/plan-and-group", h.handlePlanAndGroup)

			// Usage & Logs
			authPages.GET("/usage-logs", h.handleUsageLogs)

			// Unified notification inbox (topbar bell target)
			authPages.GET("/notifications", h.handleNotifications)

			// Support tickets (user surface)
			authPages.GET("/support", h.handleSupport)

			// Service status page (login required)
			authPages.GET("/status", h.handleStatus)

			// Enterprise self-registration
			authPages.GET("/enterprise-apply", h.handleEnterpriseApply)

			// Read-only upstream operations view for non-admin users
			authPages.GET("/upstream-ops", h.handleUserUpstreamOps)

			// Subscription pages
			authPages.GET("/subscriptions", h.handleSubscriptions)
			authPages.GET("/subscriptions/my", h.handleMySubscriptions)

			// Admin pages
			admin := authPages.Group("/admin")
			admin.Use(h.requireRole("system_admin", "super_admin"))
			{
				admin.GET("", h.handleAdminDashboard)
				admin.GET("/users", h.handleAdminUsers)
				admin.GET("/models", h.handleAdminModels)
				admin.GET("/models-aws", h.handleAdminModelsAWS)
				admin.GET("/pricing", h.handleAdminPricing)
				admin.GET("/keypool", h.handleAdminKeyPool)
				admin.GET("/providers", h.handleAdminProviders)
				admin.GET("/subscriptions", h.handleAdminSubscriptions)
				admin.GET("/subscriptions/:id/detail", h.HandleGetPlanDetail)
				admin.GET("/rate-limits", h.handleAdminRateLimits)
				admin.GET("/audit-logs", h.handleAdminAuditLogs)
				admin.GET("/audit-logs/export", h.handleAdminAuditLogsExport)
				admin.GET("/audit-logs/:id/detail", h.HandleAuditLogDetail)
				admin.GET("/compliance", h.handleAdminCompliance)
				admin.GET("/settings", h.handleAdminSettings)
				admin.GET("/tickets", h.handleAdminTickets)
				admin.GET("/settings/:key/json", h.HandleGetSettingJSON)
				admin.GET("/enterprise", h.handleAdminEnterprise)
				admin.GET("/enterprise/:id/detail", h.HandleEnterpriseDetail)
				admin.GET("/monitor-upstream", h.handleMonitorUpstream)
				admin.GET("/monitor-downstream", h.handleMonitorDownstream)
				admin.GET("/provider-health", h.handleAdminProviderHealth)
				admin.GET("/upstream-ops", h.handleAdminUpstreamOps)
				admin.GET("/reconciliation-ops", h.handleAdminReconciliationOps)
				admin.GET("/upstream-pricing", h.handleAdminUpstreamPricing)
				admin.GET("/usage-explorer", h.handleAdminUsageExplorer)
				admin.GET("/announcements", h.handleAdminAnnouncements)
				admin.GET("/vouchers", h.handleAdminVouchers)
				admin.GET("/reconciliation", h.handleAdminReconciliation)

				// Admin key management (S-K1)
				admin.GET("/keys", h.handleAdminKeys)

				// Group management (Phase 2)
				admin.GET("/groups", h.handleAdminGroups)
				admin.GET("/enterprise-applications", h.handleAdminEnterpriseApplications)
			}

			// Enterprise self-management pages (enterprise_admin only).
			// System/super admins manage enterprises via /admin/enterprise endpoints, not here.
			enterprise := authPages.Group("/enterprise")
			enterprise.Use(h.requireRole("enterprise_admin"))
			enterprise.Use(h.setEnterpriseContext())
			{
				enterprise.GET("", h.handleEnterpriseDashboard)
				enterprise.GET("/users", h.handleEnterpriseUsers)
				enterprise.GET("/users/:id", h.handleEnterpriseMemberDetail)
				enterprise.GET("/keys", h.handleEnterpriseKeys)
				enterprise.GET("/keys/:id", h.handleEnterpriseKeyDetail)
				enterprise.GET("/keys/:id/view", h.HandleViewEnterpriseKey)
				enterprise.GET("/billing", h.handleEnterpriseBilling)
				enterprise.GET("/groups", h.handleEnterpriseGroups)
				enterprise.GET("/usage", h.handleEnterpriseUsage)
				enterprise.GET("/audit", h.handleEnterpriseAudit)
				enterprise.GET("/alerts", h.handleEnterpriseAlerts)
				enterprise.GET("/settings", h.handleEnterpriseSettings)
			}

			// Super admin pages (super_admin role only)
			super := authPages.Group("/super")
			super.Use(h.requireRole("super_admin"))
			{
				super.GET("/admin-management", h.handleSuperAdminManagement)
				super.GET("/payment", h.handleSuperPayment)
			}
		}
	}
}

// --- Public Handlers ---

func (h *Handler) handleLanding(c *gin.Context) {
	if c.GetString("user_id") != "" {
		c.Redirect(http.StatusFound, "/dashboard")
		return
	}
	base := h.baseURL(c)
	h.RenderPage(c, "landing", PageData{
		Title:       h.i18n.T("app.name", h.getLocale(c)),
		Indexable:   true,
		Canonical:   base + "/",
		SEOTitle:    "WOODGIT — Unified AI API Gateway",
		SEODesc:     "One OpenAI-compatible API for many LLM, vision, image and audio models across providers. Usage-based billing, per-key rate limits, and enterprise management.",
		SEOKeywords: "AI API, LLM gateway, OpenAI-compatible API, unified AI API, AI model aggregator, chat completions API, image generation API, enterprise AI",
		SEOImage:    base + "/static/favicon.svg",
		JSONLD: template.HTML(`{"@context":"https://schema.org","@type":"WebSite","name":"WOODGIT",` +
			`"url":"` + base + `/","description":"Unified, OpenAI-compatible AI API gateway aggregating LLM, vision, image and audio models from multiple providers.",` +
			`"publisher":{"@type":"Organization","name":"WOODGIT"}}`),
	})
}

func (h *Handler) handleModelsGallery(c *gin.Context) {
	base := h.baseURL(c)
	h.RenderPage(c, "models-gallery", PageData{
		Title:       h.i18n.T("Models Gallery", h.getLocale(c)),
		Indexable:   true,
		Canonical:   base + "/models",
		SEOTitle:    "AI Models & Pricing — WOODGIT",
		SEODesc:     "Browse the AI models available through WOODGIT — chat, vision, image, audio and embedding models from many providers, with transparent per-token and per-request pricing.",
		SEOKeywords: "AI models, LLM pricing, model catalog, GPT, Claude, Gemini, image models, embedding models, AI API pricing",
		SEOImage:    base + "/static/favicon.svg",
		JSONLD: template.HTML(`{"@context":"https://schema.org","@type":"CollectionPage",` +
			`"name":"AI Models & Pricing","url":"` + base + `/models",` +
			`"description":"Catalog of AI models accessible via the WOODGIT unified API, with pricing."}`),
	})
}

func (h *Handler) handleLogin(c *gin.Context) {
	if c.GetString("user_id") != "" {
		c.Redirect(http.StatusFound, "/dashboard")
		return
	}
	h.RenderPage(c, "login", PageData{Title: h.i18n.T("auth.login", h.getLocale(c))})
}

func (h *Handler) handleRegister(c *gin.Context) {
	if c.GetString("user_id") != "" {
		c.Redirect(http.StatusFound, "/dashboard")
		return
	}
	h.RenderPage(c, "register", PageData{Title: h.i18n.T("auth.register", h.getLocale(c))})
}

func (h *Handler) handleForgotPassword(c *gin.Context) {
	h.RenderPage(c, "forgot-password", PageData{Title: h.i18n.T("auth.forgot_password", h.getLocale(c))})
}

func (h *Handler) handleLangSwitch(c *gin.Context) {
	lang := c.PostForm("lang")
	if lang != "zh" && lang != "en" {
		lang = "en"
	}
	c.SetCookie("locale", lang, 86400*365, "/", "", isSecure(c), false)
	// Save to DB if logged in
	if userID := c.GetString("user_id"); userID != "" {
		if err := routes.UpdateUserPreferences(h.db, c.Request.Context(), userID, lang, "", "", ""); err != nil {
			h.logger.Error("Failed to update user locale", zap.String("user_id", userID), zap.Error(err))
		}
	}
	c.Redirect(http.StatusFound, c.GetHeader("Referer"))
}

func (h *Handler) handleThemeSwitch(c *gin.Context) {
	theme := c.PostForm("theme")
	if theme != "light" && theme != "dark" && theme != "system" {
		theme = "system"
	}
	c.SetCookie("theme", theme, 86400*365, "/", "", isSecure(c), false)
	// Save to DB if logged in
	if userID := c.GetString("user_id"); userID != "" {
		if err := routes.UpdateUserPreferences(h.db, c.Request.Context(), userID, "", theme, "", ""); err != nil {
			h.logger.Error("Failed to update user theme", zap.String("user_id", userID), zap.Error(err))
		}
	}
	c.Status(http.StatusNoContent)
}

// --- Authenticated Handlers ---

func (h *Handler) handleDashboard(c *gin.Context) {
	data, _ := h.GetDashboardData(c.Request.Context(), c.GetString("user_id"))
	h.RenderPage(c, "dashboard", PageData{
		Title: h.i18n.T("nav.dashboard", h.getLocale(c)),
		Data:  data,
	})
}

func (h *Handler) handleRecentKeys(c *gin.Context) {
	keys := h.GetRecentKeys(c.Request.Context(), c.GetString("user_id"), h.getTimezone(c), h.getLocale(c))
	h.RenderPartial(c, "recent-keys-partial", gin.H{"Keys": keys})
}

func (h *Handler) handleAPIKeys(c *gin.Context) {
	personalKeys, _ := h.GetAPIKeys(c.Request.Context(), c.GetString("user_id"), getPage(c), 50)
	var hasEnterprise bool
	var enterpriseKeys *query.AssignedEnterpriseKeysData
	_ = h.db.QueryRow(c.Request.Context(),
		`SELECT enterprise_id IS NOT NULL FROM users WHERE id = $1::uuid AND deleted_at IS NULL`,
		c.GetString("user_id"),
	).Scan(&hasEnterprise)

	if hasEnterprise {
		enterpriseKeys, _ = h.GetAssignedEnterpriseKeys(c.Request.Context(), c.GetString("user_id"), getPageParam(c, "epage"), 50)
	}

	// Apply system defaults for enterprise keys with 0 limit
	if enterpriseKeys != nil {
		for i := range enterpriseKeys.Items {
			if enterpriseKeys.Items[i].RateLimitRPM <= 0 {
				enterpriseKeys.Items[i].RateLimitRPM = routes.DefaultUserRPM(h.db)
			}
		}
	}

	// Compute key-type creation permissions.
	role := c.GetString("user_role")
	isAdmin := role == "super_admin" || role == "system_admin"

	// Subscription key type is visible when the system has active subscription plans,
	// so users know the option exists. Actual creation is gated on the backend by
	// checking if the user has an active subscription — if not, a prompt to subscribe is returned.
	var hasSubscriptionPlans bool
	_ = h.db.QueryRow(c.Request.Context(),
		`SELECT EXISTS(SELECT 1 FROM subscription_plans WHERE status = 'active')`).Scan(&hasSubscriptionPlans)

	canCreateSubscription := isAdmin || hasSubscriptionPlans
	// Enterprise key type should only be visible to users who actually belong to an enterprise or are admins.
	canCreateEnterprise := isAdmin || hasEnterprise

	// Compute accurate total active/inactive counts for personal keys (not just current page).
	// A key past its expires_at no longer works (the API rejects it as expired)
	// even though status stays 'active', so it counts as inactive here.
	var activeCount, inactiveCount int
	_ = h.db.QueryRow(c.Request.Context(),
		`SELECT
			COUNT(*) FILTER (WHERE status = 'active' AND (expires_at IS NULL OR expires_at > NOW())),
			COUNT(*) FILTER (WHERE NOT (status = 'active' AND (expires_at IS NULL OR expires_at > NOW())))
		 FROM api_keys
		 WHERE user_id = $1::uuid AND deleted_at IS NULL AND key_type = 'general'`,
		c.GetString("user_id"),
	).Scan(&activeCount, &inactiveCount)

	data := gin.H{
		"PersonalKeys":             personalKeys,
		"PersonalActiveCount":      activeCount,
		"PersonalInactiveCount":    inactiveCount,
		"EnterpriseKeys":           enterpriseKeys,
		"HasEnterprise":            hasEnterprise,
		"CanCreateSubscriptionKey": canCreateSubscription,
		"CanCreateEnterpriseKey":   canCreateEnterprise,
	}

	h.RenderPage(c, "apikeys", PageData{
		Title:        h.i18n.T("nav.apikeys", h.getLocale(c)),
		Data:         data,
		EnterpriseID: hasEnterprise,
	})
}

func (h *Handler) handleBilling(c *gin.Context) {
	data, _ := h.GetBillingData(c.Request.Context(), c.GetString("user_id"), 20)

	// Check if any payment gateway is enabled or simulation mode is on
	var simulationMode string
	_ = h.db.QueryRow(c.Request.Context(),
		`SELECT COALESCE(value, 'false') FROM system_settings WHERE key = 'payment_simulation_mode'`).Scan(&simulationMode)
	simulate := simulationMode == "true"

	// Per-gateway enabled state so the UI can grey out unavailable methods.
	// Simulation mode bypasses real gateways, so all methods stay usable.
	gatewayEnabled := map[string]bool{}
	rows, err := h.db.Query(c.Request.Context(),
		`SELECT gateway FROM payment_gateways WHERE enabled = true`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var gw string
			if rows.Scan(&gw) == nil {
				gatewayEnabled[gw] = true
			}
		}
	}
	for _, gw := range []string{"wechat", "alipay", "stripe", "paypal"} {
		if simulate {
			gatewayEnabled[gw] = true
		}
	}
	enabledCount := 0
	var defaultMethod string
	for _, gw := range []string{"wechat", "alipay", "stripe", "paypal"} {
		if gatewayEnabled[gw] {
			enabledCount++
			if defaultMethod == "" {
				defaultMethod = gw
			}
		}
	}
	paymentAvailable := simulate || enabledCount > 0

	h.RenderPage(c, "billing", PageData{
		Title: h.i18n.T("nav.billing", h.getLocale(c)),
		Data: map[string]interface{}{
			"BillingData":      data,
			"PaymentAvailable": paymentAvailable,
			"GatewayEnabled":   gatewayEnabled,
			"DefaultMethod":    defaultMethod,
		},
	})
}

func (h *Handler) handleBillingDetails(c *gin.Context) {
	h.RenderPage(c, "billing-details", PageData{
		Title: h.i18n.T("nav.billing_details", h.getLocale(c)),
	})
}

func (h *Handler) handleVoucher(c *gin.Context) {
	h.RenderPage(c, "voucher", PageData{
		Title: h.i18n.T("nav.voucher", h.getLocale(c)),
	})
}

func (h *Handler) handleAdminVouchers(c *gin.Context) {
	h.RenderPage(c, "admin-vouchers", PageData{
		Title: h.i18n.T("nav.admin.vouchers", h.getLocale(c)),
	})
}

func (h *Handler) handleAdminReconciliation(c *gin.Context) {
	h.RenderPage(c, "admin-reconciliation", PageData{
		Title: h.i18n.T("nav.reconciliation", h.getLocale(c)),
	})
}

func (h *Handler) handleAdminKeys(c *gin.Context) {
	h.RenderPage(c, "admin-keys", PageData{
		Title: h.i18n.T("nav.admin.keys", h.getLocale(c)),
	})
}

func (h *Handler) handleInvoices(c *gin.Context) {
	data, _ := h.GetInvoiceData(c.Request.Context(), c.GetString("user_id"), getPage(c), 50)
	h.RenderPage(c, "invoices", PageData{
		Title: h.i18n.T("nav.invoices", h.getLocale(c)),
		Data:  data,
	})
}

func (h *Handler) handleProfile(c *gin.Context) {
	data, err := h.GetProfileData(c.Request.Context(), c.GetString("user_id"))
	if err != nil || data == nil {
		// If profile data cannot be fetched, create a minimal default
		data = &ProfileData{
			Email: c.GetString("user_email"),
			Role:  c.GetString("user_role"),
		}
	}
	pageData := PageData{
		Title: h.i18n.T("nav.profile", h.getLocale(c)),
		Data:  data,
	}
	// Resolve the effective locale for the profile page. Only let an explicitly
	// saved DB preference override the request locale; an unsaved profile (empty
	// DB locale) must keep the cookie / Accept-Language / default locale so the
	// page does not silently flip to English. Theme/timezone still honor saved
	// values because their defaults are benign.
	pageData.Locale = h.getLocale(c)
	if data.Locale != "" {
		pageData.Locale = data.Locale
	}
	if data.Theme != "" {
		pageData.Theme = data.Theme
	}
	if data.Timezone != "" {
		pageData.Timezone = data.Timezone
	}
	h.RenderPage(c, "profile", pageData)
}

// --- Subscription Handlers ---

func (h *Handler) handleSubscriptions(c *gin.Context) {
	h.RenderPage(c, "subscriptions", PageData{
		Title: h.i18n.T("nav.subscriptions", h.getLocale(c)),
	})
}

func (h *Handler) handleMySubscriptions(c *gin.Context) {
	h.RenderPage(c, "my-subscriptions", PageData{
		Title: h.i18n.T("nav.my_subscriptions", h.getLocale(c)),
	})
}

func (h *Handler) handleMyGroup(c *gin.Context) {
	data, _ := h.GetMyGroupData(c.Request.Context(), c.GetString("user_id"))
	h.RenderPage(c, "my-group", PageData{
		Title: h.i18n.T("nav.my_group", h.getLocale(c)),
		Data:  data,
	})
}

func (h *Handler) handlePlanAndGroup(c *gin.Context) {
	data, _ := h.GetMyGroupData(c.Request.Context(), c.GetString("user_id"))
	h.RenderPage(c, "plan-and-group", PageData{
		Title: h.i18n.T("nav.plan_and_group", h.getLocale(c)),
		Data: map[string]interface{}{
			"Group": data,
		},
	})
}

func (h *Handler) handleUsageLogs(c *gin.Context) {
	h.RenderPage(c, "usage-logs", PageData{
		Title: h.i18n.T("nav.usage_logs", h.getLocale(c)),
	})
}

func (h *Handler) handleStatus(c *gin.Context) {
	h.RenderPage(c, "status", PageData{
		Title: h.i18n.T("status.title", h.getLocale(c)),
	})
}

func (h *Handler) handleNotifications(c *gin.Context) {
	h.RenderPage(c, "notifications", PageData{
		Title: h.i18n.T("nav.notifications", h.getLocale(c)),
	})
}

func (h *Handler) handleSupport(c *gin.Context) {
	h.RenderPage(c, "support", PageData{
		Title: h.i18n.T("Support Tickets", h.getLocale(c)),
	})
}

func (h *Handler) handleAdminTickets(c *gin.Context) {
	h.RenderPage(c, "admin-tickets", PageData{
		Title: h.i18n.T("Support Tickets", h.getLocale(c)),
	})
}

func (h *Handler) handleUserUpstreamOps(c *gin.Context) {
	h.RenderPage(c, "user-upstream-ops", PageData{
		Title: h.i18n.T("upstream_ops.title", h.getLocale(c)),
	})
}

// --- Admin Handlers ---

func (h *Handler) handleAdminDashboard(c *gin.Context) {
	data, _ := h.GetAdminDashboardData(c.Request.Context())
	h.RenderPage(c, "admin", PageData{
		Title: h.i18n.T("admin.title", h.getLocale(c)),
		Data:  data,
	})
}

func (h *Handler) handleAdminUsers(c *gin.Context) {
	data, _ := h.GetAdminUsers(c.Request.Context(), getPage(c), 50)
	h.RenderPage(c, "admin-users", PageData{
		Title: h.i18n.T("admin.users", h.getLocale(c)),
		Data:  data,
	})
}

func (h *Handler) handleAdminModels(c *gin.Context) {
	data, _ := h.GetAdminModels(c.Request.Context(), getPage(c), 50, "!amazon")
	// Convert PaginatedData to map to avoid Go template interface{} slice access bug
	pageData := gin.H{}
	if data != nil {
		pageData["Total"] = data.Total
		pageData["Page"] = data.Page
		pageData["PageSize"] = data.PageSize
		pageData["TotalPages"] = data.TotalPages
		if items, ok := data.Items.(*query.AdminModelsData); ok {
			pageData["Models"] = items.Models
			pageData["Providers"] = items.Providers
			pageData["ActiveCount"] = items.ActiveCount
			pageData["PendingCount"] = items.PendingCount
			pageData["PausedCount"] = items.PausedCount
			pageData["EOLPendingCount"] = items.EOLPendingCount
			pageData["EOLCount"] = items.EOLCount
			pageData["DiscontinuedCount"] = items.DiscontinuedCount
		}
	}
	h.RenderPage(c, "admin-models-general", PageData{
		Title: h.i18n.T("admin.models", h.getLocale(c)),
		Data:  pageData,
	})
}

func (h *Handler) handleAdminModelsAWS(c *gin.Context) {
	data, _ := h.GetAdminModels(c.Request.Context(), getPage(c), 50, "amazon")
	pageData := gin.H{}
	if data != nil {
		pageData["Total"] = data.Total
		pageData["Page"] = data.Page
		pageData["PageSize"] = data.PageSize
		pageData["TotalPages"] = data.TotalPages
		if items, ok := data.Items.(*query.AdminModelsData); ok {
			pageData["Models"] = items.Models
			pageData["Providers"] = items.Providers
			pageData["ActiveCount"] = items.ActiveCount
			pageData["PendingCount"] = items.PendingCount
			pageData["PausedCount"] = items.PausedCount
			pageData["EOLPendingCount"] = items.EOLPendingCount
			pageData["EOLCount"] = items.EOLCount
			pageData["DiscontinuedCount"] = items.DiscontinuedCount
		}
	}
	h.RenderPage(c, "admin-models-aws", PageData{
		Title: h.i18n.T("nav.admin.models_aws", h.getLocale(c)),
		Data:  pageData,
	})
}

func (h *Handler) handleAdminPricing(c *gin.Context) {
	h.RenderPage(c, "admin-pricing", PageData{
		Title: h.i18n.T("admin.pricing", h.getLocale(c)),
		Data:  gin.H{},
	})
}

func (h *Handler) handleAdminKeyPool(c *gin.Context) {
	data, _ := h.GetAdminUpstreamKeys(c.Request.Context(), getPage(c), 50)
	h.RenderPage(c, "admin-keypool", PageData{
		Title: h.i18n.T("admin.keypool", h.getLocale(c)),
		Data:  data,
	})
}

func (h *Handler) handleAdminProviders(c *gin.Context) {
	providers, _ := h.GetAdminProviders(c.Request.Context())
	h.RenderPage(c, "admin-providers", PageData{
		Title: h.i18n.T("admin.providers", h.getLocale(c)),
		Data:  providers,
	})
}

func (h *Handler) handleAdminSubscriptions(c *gin.Context) {
	plans, _ := h.GetAdminPlans(c.Request.Context())
	h.RenderPage(c, "admin-subscriptions", PageData{
		Title: h.i18n.T("admin.subscriptions", h.getLocale(c)),
		Data:  plans,
	})
}

func (h *Handler) handleAdminRateLimits(c *gin.Context) {
	h.RenderPage(c, "admin-rate-limits", PageData{
		Title: h.i18n.T("admin.rate_limits", h.getLocale(c)),
		Data:  nil,
	})
}

func (h *Handler) handleAdminAuditLogs(c *gin.Context) {
	search := c.Query("search")
	category := c.Query("category")
	level := c.Query("level")
	data, _ := h.GetAdminAuditLogs(c.Request.Context(), getPage(c), 50, search, category, level)
	h.RenderPage(c, "admin-audit-logs", PageData{
		Title: h.i18n.T("admin.audit_logs", h.getLocale(c)),
		Data:  data,
	})
}

func (h *Handler) handleAdminCompliance(c *gin.Context) {
	h.RenderPage(c, "admin-compliance", PageData{
		Title: h.i18n.T("admin.compliance", h.getLocale(c)),
		Data:  nil,
	})
}

func (h *Handler) handleAdminAuditLogsExport(c *gin.Context) {
	csv, err := h.ExportAdminAuditLogs(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Export failed"}})
		return
	}
	if h.audit != nil {
		h.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("user_email"), "admin", "audit_logs_exported", "info", "Admin exported audit logs to CSV")
	}
	c.Header("Content-Disposition", "attachment; filename=\"audit-logs-"+time.Now().Format("20060102-150405")+".csv\"")
	c.Data(http.StatusOK, "text/csv; charset=utf-8", []byte(csv))
}

func (h *Handler) handleAdminSettings(c *gin.Context) {
	settings, _ := h.GetAdminSettings(c.Request.Context())
	h.RenderPage(c, "admin-settings", PageData{
		Title: h.i18n.T("admin.settings", h.getLocale(c)),
		Data:  settings,
	})
}

func (h *Handler) handleAdminEnterprise(c *gin.Context) {
	data, err := h.GetAdminEnterprises(c.Request.Context(), getPage(c), 50, c.Query("search"))
	if err != nil {
		h.logger.Error("GetAdminEnterprises failed", zap.Error(err))
		data = &query.AdminEnterpriseListData{Items: []query.AdminEnterprise{}, TotalPages: 1}
	}
	if data == nil {
		data = &query.AdminEnterpriseListData{Items: []query.AdminEnterprise{}, TotalPages: 1}
	}
	data.Search = c.Query("search")
	h.RenderPage(c, "admin-enterprise", PageData{
		Title: h.i18n.T("admin.enterprise", h.getLocale(c)),
		Data:  data,
	})
}

func (h *Handler) handleAdminGroups(c *gin.Context) {
	groups, err := h.GetAdminGroups(c.Request.Context())
	if err != nil {
		h.logger.Error("GetAdminGroups failed", zap.Error(err))
		groups = &query.AdminGroupsData{}
	}
	if groups == nil {
		groups = &query.AdminGroupsData{}
	}
	h.RenderPage(c, "admin-groups", PageData{
		Title: h.i18n.T("admin.groups", h.getLocale(c)),
		Data:  groups,
	})
}

// --- Enterprise Admin Handlers ---

func (h *Handler) handleEnterpriseDashboard(c *gin.Context) {
	rng := c.Query("range")
	if rng == "" {
		rng = query.DefaultRange
	}
	data, _ := h.GetEnterpriseDashboardData(c.Request.Context(), c.GetString("user_id"), rng)
	if data != nil && data.DisplayCurrency != "" && data.DisplayCurrency != "USD" && h.exchange != nil {
		data.ExchangeRate = h.exchange.GetRateOrDefault(c.Request.Context(), "USD", data.DisplayCurrency, data.ExchangeRate)
		if data.ExchangeRate > 0 {
			data.CreditTotalLocal = data.CreditTotalUSD * data.ExchangeRate
			data.CreditUsedLocal = data.CreditUsedUSD * data.ExchangeRate
			data.CreditRemainLocal = data.CreditRemainUSD * data.ExchangeRate
		}
	}
	h.RenderPage(c, "enterprise/dashboard", PageData{
		Title: h.i18n.T("enterprise.title", h.getLocale(c)),
		Data:  data,
	})
}

func (h *Handler) handleEnterpriseUsers(c *gin.Context) {
	userID := c.GetString("user_id")
	users, _ := h.GetEnterpriseUsers(c.Request.Context(), userID)
	invitations, _ := h.GetEnterpriseInvitations(c.Request.Context(), userID)
	data := gin.H{
		"Members":       users,
		"Invitations":   invitations,
		"CurrentUserID": userID,
	}
	h.RenderPage(c, "enterprise/users", PageData{
		Title: h.i18n.T("enterprise.users", h.getLocale(c)),
		Data:  data,
	})
}

// handleEnterpriseMemberDetail renders the enterprise member detail page.
func (h *Handler) handleEnterpriseMemberDetail(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	memberID := c.Param("id")
	ctx := c.Request.Context()

	// Verify member belongs to this enterprise.
	var email, name, status, role string
	var quotaLimit, creditLimit, quotaUsed, creditUsed int64
	var joinedAt interface{}
	err := h.db.QueryRow(ctx, `
		SELECT u.email, COALESCE(u.name, ''), u.status, COALESCE(em.role, 'member'),
		       COALESCE(em.quota_limit, 0), COALESCE(em.credit_limit, 0),
		       COALESCE(u.quota_used, 0), COALESCE(u.credit_used, 0),
		       em.joined_at
		FROM users u
		JOIN enterprise_members em ON u.id = em.user_id AND em.enterprise_id = u.enterprise_id AND em.removed_at IS NULL
		WHERE u.id = $1::uuid AND u.enterprise_id = $2::uuid AND u.deleted_at IS NULL`,
		memberID, enterpriseID).Scan(&email, &name, &status, &role, &quotaLimit, &creditLimit, &quotaUsed, &creditUsed, &joinedAt)
	if err != nil {
		h.logger.Warn("Enterprise member detail not found", zap.String("member_id", memberID), zap.String("enterprise_id", enterpriseID), zap.Error(err))
		c.Redirect(http.StatusFound, "/enterprise/users")
		return
	}

	// Monthly usage summary.
	startOfMonth := time.Now().UTC().AddDate(0, 0, -time.Now().UTC().Day()+1).Format("2006-01-02") + " 00:00:00"
	endOfMonth := time.Now().UTC().Format("2006-01-02") + " 23:59:59"
	var monthRequests int
	var monthTokens, monthCost int64
	_ = h.db.QueryRow(ctx, `
		SELECT COALESCE(COUNT(*), 0), COALESCE(SUM(ur.input_tokens + ur.output_tokens), 0), COALESCE(SUM(ur.total_cost), 0)
		FROM usage_records ur
		JOIN api_keys k ON ur.user_api_key_id = k.id
		WHERE k.enterprise_id = $1::uuid AND k.assigned_user_id = $2::uuid
		  AND ur.created_at >= date_trunc('month', CURRENT_DATE)`,
		enterpriseID, memberID).Scan(&monthRequests, &monthTokens, &monthCost)

	// Keys assigned to member.
	var keys []gin.H
	keyRows, err := h.db.Query(ctx, `
		SELECT id::text, name, key_prefix, status, created_at, COALESCE(expires_at, NULL)
		FROM api_keys
		WHERE enterprise_id = $1::uuid AND assigned_user_id = $2::uuid AND deleted_at IS NULL
		ORDER BY created_at DESC LIMIT 50`,
		enterpriseID, memberID)
	if err == nil {
		defer keyRows.Close()
		for keyRows.Next() {
			var id, keyName, keyPrefix, keyStatus string
			var createdAt, expiresAt interface{}
			if err := keyRows.Scan(&id, &keyName, &keyPrefix, &keyStatus, &createdAt, &expiresAt); err == nil {
				keys = append(keys, gin.H{
					"id": id, "name": keyName, "key_prefix": keyPrefix, "status": keyStatus,
					"created_at": createdAt, "expires_at": expiresAt,
				})
			}
		}
	}

	// Audit events for this member.
	var auditEvents []gin.H
	auditRows, err := h.db.Query(ctx, `
		SELECT cl.category, cl.change_type, cl.field_name, cl.old_value, cl.new_value, cl.delta,
		       COALESCE(cl.reason, ''), cl.created_at, COALESCE(operator.email, 'system')
		FROM enterprise_audit_log cl
		LEFT JOIN users operator ON cl.changed_by = operator.id
		WHERE cl.enterprise_id = $1::uuid AND cl.user_id = $2::uuid
		ORDER BY cl.created_at DESC LIMIT 50`,
		enterpriseID, memberID)
	if err == nil {
		defer auditRows.Close()
		for auditRows.Next() {
			var category, changeType, fieldName, reason, changedBy string
			var oldVal, newVal, delta sql.NullInt64
			var createdAt interface{}
			if err := auditRows.Scan(&category, &changeType, &fieldName, &oldVal, &newVal, &delta, &reason, &createdAt, &changedBy); err == nil {
				auditEvents = append(auditEvents, gin.H{
					"category": category, "change_type": changeType, "field_name": fieldName,
					"old_value": nullInt64(oldVal), "new_value": nullInt64(newVal),
					"delta": nullInt64(delta), "reason": reason,
					"created_at": createdAt, "changed_by": changedBy,
				})
			}
		}
	}

	// Recent API logs for this member, filterable via ?from=&to=&limit=.
	// Defaults: current month, 50 rows (cap 200).
	normDate := func(s, suffix string) string {
		if len(s) == 10 {
			return s + suffix
		}
		return s
	}
	logFrom := normDate(c.DefaultQuery("from", startOfMonth), " 00:00:00")
	logTo := normDate(c.DefaultQuery("to", endOfMonth), " 23:59:59")
	logLimit := 50
	if v, err := strconv.Atoi(c.DefaultQuery("limit", "50")); err == nil {
		switch v {
		case 50, 100, 200:
			logLimit = v
		}
	}
	recentLogs, _, _ := query.GetUsageRecords(ctx, h.db, "", query.UsageRecordFilter{
		IsAdmin:      true,
		EnterpriseID: enterpriseID,
		UserID:       memberID,
		StartDate:    logFrom,
		EndDate:      logTo,
		Limit:        logLimit,
	})

	h.RenderPage(c, "enterprise/member-detail", PageData{
		Title: h.i18n.T("enterprise.member_detail", h.getLocale(c)),
		Data: gin.H{
			"MemberID":      memberID,
			"Email":         email,
			"Name":          name,
			"Status":        status,
			"Role":          role,
			"QuotaLimit":    quotaLimit,
			"QuotaUsed":     quotaUsed,
			"CreditLimit":   creditLimit,
			"CreditUsed":    creditUsed,
			"JoinedAt":      joinedAt,
			"MonthRequests": monthRequests,
			"MonthTokens":   monthTokens,
			"MonthCost":     monthCost,
			"Keys":          keys,
			"AuditEvents":   auditEvents,
			"RecentLogs":    recentLogs,
			"LogFrom":       logFrom[:10],
			"LogTo":         logTo[:10],
			"LogLimit":      logLimit,
		},
	})
}

func (h *Handler) handleEnterpriseKeys(c *gin.Context) {
	keys, _ := h.GetEnterpriseKeys(c.Request.Context(), c.GetString("user_id"))
	members, _ := h.GetEnterpriseUsers(c.Request.Context(), c.GetString("user_id"))
	groups, _ := h.GetEnterpriseGroups(c.Request.Context(), c.GetString("enterprise_id"))

	var displayKeys []EnterpriseKeyData
	now := time.Now()
	for _, k := range keys {
		displayKeys = append(displayKeys, EnterpriseKeyData{
			ID:               k.ID,
			Name:             k.Name,
			KeyPrefix:        k.KeyPrefix,
			Status:           k.Status,
			AssignedUserName: k.AssignedUserName,
			AssignedUserID:   k.AssignedUserID,
			GroupName:        k.GroupName,
			GroupID:          k.GroupID,
			CreatedAt:        k.CreatedAt,
			MonthRequests:    k.MonthRequests,
			MonthTokens:      k.MonthTokens,
			RateLimitRPM:     k.RateLimitRPM,
			ModelRoutingMode: derefStr(k.ModelRoutingMode),
			CurrentModel:     derefStr(k.CurrentModel),
			SelectedModels:   toStringSlice(k.SelectedModels),
			AllowedModels:    unmarshalStringSlice(k.AllowedModels),
			ExpiresAt:        k.ExpiresAt,
			Expired:          k.ExpiresAt != nil && k.ExpiresAt.Before(now),
		})
	}

	data := gin.H{
		"Keys":    displayKeys,
		"Members": members,
		"Groups":  groups,
	}
	h.RenderPage(c, "enterprise/keys", PageData{
		Title: h.i18n.T("enterprise.keys", h.getLocale(c)),
		Data:  data,
	})
}

// handleEnterpriseKeyDetail renders the enterprise key detail page.
func (h *Handler) handleEnterpriseKeyDetail(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	keyID := c.Param("id")
	ctx := c.Request.Context()

	var keyName, keyPrefix, status, assignedUserID, assignedUserName, groupName string
	var createdAt, expiresAt interface{}
	var rpm int
	var tpm int64
	var allowedModels interface{}
	err := h.db.QueryRow(ctx, `
		SELECT k.name, k.key_prefix, k.status,
		       COALESCE(k.assigned_user_id::text, ''), COALESCE(u.name, u.email, ''),
		       COALESCE(g.name, ''), k.created_at, k.expires_at,
		       COALESCE(k.rate_limit_rpm, 0), COALESCE(k.rate_limit_tpm, 0), k.allowed_models
		FROM api_keys k
		LEFT JOIN users u ON k.assigned_user_id = u.id
		LEFT JOIN user_groups g ON k.group_id = g.id
		WHERE k.id = $1::uuid AND k.enterprise_id = $2::uuid AND k.deleted_at IS NULL`,
		keyID, enterpriseID).Scan(&keyName, &keyPrefix, &status, &assignedUserID, &assignedUserName, &groupName, &createdAt, &expiresAt, &rpm, &tpm, &allowedModels)
	if err != nil {
		h.logger.Warn("Enterprise key detail not found", zap.String("key_id", keyID), zap.String("enterprise_id", enterpriseID), zap.Error(err))
		c.Redirect(http.StatusFound, "/enterprise/keys")
		return
	}

	// Monthly usage summary for this key.
	startOfMonth := time.Now().UTC().AddDate(0, 0, -time.Now().UTC().Day()+1).Format("2006-01-02") + " 00:00:00"
	endOfMonth := time.Now().UTC().Format("2006-01-02") + " 23:59:59"
	var monthRequests int
	var monthTokens, monthCost int64
	_ = h.db.QueryRow(ctx, `
		SELECT COALESCE(COUNT(*), 0), COALESCE(SUM(ur.input_tokens + ur.output_tokens), 0), COALESCE(SUM(ur.total_cost), 0)
		FROM usage_records ur
		WHERE ur.user_api_key_id = $1::uuid`,
		keyID).Scan(&monthRequests, &monthTokens, &monthCost)

	// Audit events for this key.
	var auditEvents []gin.H
	auditRows, err := h.db.Query(ctx, `
		SELECT cl.category, cl.change_type, cl.field_name, cl.old_value, cl.new_value, cl.delta,
		       COALESCE(cl.reason, ''), cl.created_at, COALESCE(operator.email, 'system')
		FROM enterprise_audit_log cl
		LEFT JOIN users operator ON cl.changed_by = operator.id
		WHERE cl.enterprise_id = $1::uuid AND cl.target_type = 'key' AND cl.target_id = $2
		ORDER BY cl.created_at DESC LIMIT 50`,
		enterpriseID, keyID)
	if err == nil {
		defer auditRows.Close()
		for auditRows.Next() {
			var category, changeType, fieldName, reason, changedBy string
			var oldVal, newVal, delta sql.NullInt64
			var createdAtEvent interface{}
			if err := auditRows.Scan(&category, &changeType, &fieldName, &oldVal, &newVal, &delta, &reason, &createdAtEvent, &changedBy); err == nil {
				auditEvents = append(auditEvents, gin.H{
					"category": category, "change_type": changeType, "field_name": fieldName,
					"old_value": nullInt64(oldVal), "new_value": nullInt64(newVal),
					"delta": nullInt64(delta), "reason": reason,
					"created_at": createdAtEvent, "changed_by": changedBy,
				})
			}
		}
	}

	// Recent API logs for this key, filterable via ?from=&to=&limit=.
	// Defaults: current month, 50 rows (cap 200).
	normDate := func(s, suffix string) string {
		if len(s) == 10 {
			return s + suffix
		}
		return s
	}
	logFrom := normDate(c.DefaultQuery("from", startOfMonth), " 00:00:00")
	logTo := normDate(c.DefaultQuery("to", endOfMonth), " 23:59:59")
	logLimit := 50
	if v, err := strconv.Atoi(c.DefaultQuery("limit", "50")); err == nil {
		switch v {
		case 50, 100, 200:
			logLimit = v
		}
	}
	recentLogs, _, _ := query.GetUsageRecords(ctx, h.db, "", query.UsageRecordFilter{
		IsAdmin:      true,
		EnterpriseID: enterpriseID,
		APIKeyID:     keyID,
		StartDate:    logFrom,
		EndDate:      logTo,
		Limit:        logLimit,
	})

	h.RenderPage(c, "enterprise/key-detail", PageData{
		Title: h.i18n.T("enterprise.key_detail", h.getLocale(c)),
		Data: gin.H{
			"KeyID":            keyID,
			"Name":             keyName,
			"KeyPrefix":        keyPrefix,
			"Status":           status,
			"AssignedUserID":   assignedUserID,
			"AssignedUserName": assignedUserName,
			"GroupName":        groupName,
			"CreatedAt":        createdAt,
			"ExpiresAt":        expiresAt,
			"RPM":              rpm,
			"TPM":              tpm,
			"AllowedModels":    allowedModels,
			"MonthRequests":    monthRequests,
			"MonthTokens":      monthTokens,
			"MonthCost":        monthCost,
			"AuditEvents":      auditEvents,
			"RecentLogs":       recentLogs,
			"LogFrom":          logFrom[:10],
			"LogTo":            logTo[:10],
			"LogLimit":         logLimit,
		},
	})
}

func (h *Handler) handleEnterpriseBilling(c *gin.Context) {
	data, _ := h.GetEnterpriseBillingData(c.Request.Context(), c.GetString("user_id"))
	if data != nil && data.DisplayCurrency != "" && data.DisplayCurrency != "USD" && h.exchange != nil {
		data.ExchangeRate = h.exchange.GetRateOrDefault(c.Request.Context(), "USD", data.DisplayCurrency, data.ExchangeRate)
		if data.ExchangeRate > 0 {
			data.CreditTotalLocal = data.CreditTotalUSD * data.ExchangeRate
			data.CreditUsedLocal = data.CreditUsedUSD * data.ExchangeRate
			data.CreditRemainingLocal = data.CreditRemainingUSD * data.ExchangeRate
			for i := range data.Invoices {
				data.Invoices[i].DisplayCurrency = data.DisplayCurrency
				data.Invoices[i].AmountLocal = (float64(data.Invoices[i].Amount) / 100.0) * data.ExchangeRate
			}
		}
	}
	h.RenderPage(c, "enterprise/billing", PageData{
		Title: h.i18n.T("enterprise.billing", h.getLocale(c)),
		Data:  data,
	})
}

func (h *Handler) handleEnterpriseGroups(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	groups, _ := h.GetEnterpriseGroups(c.Request.Context(), enterpriseID)
	h.RenderPage(c, "enterprise/groups", PageData{
		Title: h.i18n.T("enterprise.groups", h.getLocale(c)),
		Data:  groups,
	})
}

func (h *Handler) handleEnterpriseUsage(c *gin.Context) {
	h.RenderPage(c, "enterprise/usage", PageData{
		Title: h.i18n.T("enterprise.usage", h.getLocale(c)),
		Data:  gin.H{},
	})
}

func (h *Handler) handleEnterpriseAudit(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	selectedCategory := c.DefaultQuery("category", "")
	validCategories := map[string]bool{"billing": true, "member": true, "key": true, "group": true, "settings": true, "alert": true, "role": true}
	if !validCategories[selectedCategory] {
		selectedCategory = ""
	}
	fromDate := c.Query("from")
	toDate := c.Query("to")
	// Single fuzzy search across operator and member (email or name).
	searchQuery := strings.TrimSpace(c.Query("q"))

	// Unified enterprise audit logs
	var auditLogs []gin.H
	var categoryCounts = gin.H{"billing": 0, "member": 0, "key": 0, "group": 0, "settings": 0, "alert": 0, "role": 0}
	query := `
		SELECT cl.id::text, cl.category, cl.change_type, cl.field_name, cl.old_value, cl.new_value, cl.delta,
		       COALESCE(cl.reason, ''), cl.created_at, COALESCE(operator.email, 'system'),
		       COALESCE(member.email, ''), COALESCE(member.name, ''), COALESCE(cl.user_id::text, '')
		FROM enterprise_audit_log cl
		LEFT JOIN users operator ON cl.changed_by = operator.id
		LEFT JOIN users member ON cl.user_id = member.id
		WHERE cl.enterprise_id = $1::uuid`
	args := []interface{}{enterpriseID}
	argIdx := 1
	if selectedCategory != "" {
		argIdx++
		query += fmt.Sprintf(` AND cl.category = $%d`, argIdx)
		args = append(args, selectedCategory)
	}
	if fromDate != "" {
		argIdx++
		query += fmt.Sprintf(` AND cl.created_at >= $%d::date`, argIdx)
		args = append(args, fromDate)
	}
	if toDate != "" {
		argIdx++
		query += fmt.Sprintf(` AND cl.created_at < ($%d::date + INTERVAL '1 day')`, argIdx)
		args = append(args, toDate)
	}
	if searchQuery != "" {
		argIdx++
		query += fmt.Sprintf(` AND (operator.email ILIKE $%[1]d OR operator.name ILIKE $%[1]d OR member.email ILIKE $%[1]d OR member.name ILIKE $%[1]d)`, argIdx)
		args = append(args, "%"+searchQuery+"%")
	}
	query += ` ORDER BY cl.created_at DESC LIMIT 100`

	auditRows, err := h.db.Query(c.Request.Context(), query, args...)
	if err == nil {
		defer auditRows.Close()
		for auditRows.Next() {
			var id, category, changeType, fieldName, reason, changedBy string
			var memberEmail, memberName, memberUserID string
			var oldVal, newVal, delta sql.NullInt64
			var createdAt interface{}
			if err := auditRows.Scan(&id, &category, &changeType, &fieldName, &oldVal, &newVal, &delta, &reason, &createdAt, &changedBy, &memberEmail, &memberName, &memberUserID); err == nil {
				auditLogs = append(auditLogs, gin.H{
					"id": id, "category": category, "change_type": changeType, "field_name": fieldName,
					"old_value": nullInt64(oldVal), "new_value": nullInt64(newVal),
					"delta": nullInt64(delta), "reason": reason,
					"created_at": createdAt, "changed_by": changedBy,
					"user_id": memberUserID, "user_email": memberEmail, "user_name": memberName,
				})
			}
		}
	}

	// Count audit events by category (all categories, ignoring filter) for stat cards.
	countRows, err := h.db.Query(c.Request.Context(), `
		SELECT category, COUNT(*) FROM enterprise_audit_log
		WHERE enterprise_id = $1::uuid
		GROUP BY category`,
		enterpriseID)
	if err == nil {
		defer countRows.Close()
		for countRows.Next() {
			var category string
			var count int
			if err := countRows.Scan(&category, &count); err == nil {
				categoryCounts[category] = count
			}
		}
	}

	h.RenderPage(c, "enterprise/audit", PageData{
		Title: h.i18n.T("enterprise.audit", h.getLocale(c)),
		Data: gin.H{
			"AuditLogs":        auditLogs,
			"CategoryCounts":   categoryCounts,
			"SelectedCategory": selectedCategory,
			"Categories":       []string{"billing", "member", "key", "group", "settings", "alert", "role"},
			"FromDate":         fromDate,
			"ToDate":           toDate,
			"SearchQuery":      searchQuery,
		},
	})
}

func (h *Handler) handleEnterpriseAlerts(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	ctx := c.Request.Context()
	statusFilter := c.DefaultQuery("status", "all")
	if statusFilter != "open" && statusFilter != "acknowledged" {
		statusFilter = "all"
	}
	alertTypeFilter := c.DefaultQuery("type", "all")

	where := "WHERE enterprise_id = $1::uuid"
	args := []interface{}{enterpriseID}
	argIdx := 1
	switch statusFilter {
	case "open":
		where += " AND acknowledged_at IS NULL"
	case "acknowledged":
		where += " AND acknowledged_at IS NOT NULL"
	}
	if alertTypeFilter != "" && alertTypeFilter != "all" {
		argIdx++
		where += fmt.Sprintf(" AND alert_type = $%d", argIdx)
		args = append(args, alertTypeFilter)
	}

	var alerts []gin.H
	query := fmt.Sprintf(`SELECT id::text, alert_type, threshold_percent, current_value, limit_value,
						 notified_at, acknowledged_at,
						 COALESCE((SELECT email FROM users WHERE id = acknowledged_by), '')
				  FROM enterprise_alerts %s ORDER BY notified_at DESC LIMIT 100`, where)
	rows, err := h.db.Query(ctx, query, args...)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var id, alertType, ackBy string
			var threshold int
			var current, limit float64
			var notified, acked interface{}
			if err := rows.Scan(&id, &alertType, &threshold, &current, &limit, &notified, &acked, &ackBy); err == nil {
				alerts = append(alerts, gin.H{
					"id": id, "alert_type": alertType, "threshold_percent": threshold,
					"current_value": current, "limit_value": limit,
					"notified_at": notified, "acknowledged_at": acked, "acknowledged_by": ackBy,
				})
			}
		}
	}

	var total, openCount, ackCount int
	_ = h.db.QueryRow(ctx,
		`SELECT COUNT(*), COUNT(*) FILTER (WHERE acknowledged_at IS NULL), COUNT(*) FILTER (WHERE acknowledged_at IS NOT NULL)
		 FROM enterprise_alerts WHERE enterprise_id = $1::uuid`, enterpriseID).Scan(&total, &openCount, &ackCount)

	// Effective settings: missing row = enabled.
	configured := make(map[string]bool)
	settingRows, err := h.db.Query(ctx,
		`SELECT alert_type, threshold_percent, COALESCE(enabled, true)
		 FROM enterprise_alert_settings WHERE enterprise_id = $1::uuid`, enterpriseID)
	if err == nil {
		defer settingRows.Close()
		for settingRows.Next() {
			var at string
			var th int
			var en bool
			if err := settingRows.Scan(&at, &th, &en); err == nil {
				configured[fmt.Sprintf("%s:%d", at, th)] = en
			}
		}
	}
	defaultSettings := []gin.H{
		{"alert_type": "quota_warning", "threshold_percent": 80, "enabled": true},
		{"alert_type": "quota_warning", "threshold_percent": 90, "enabled": true},
		{"alert_type": "quota_exceeded", "threshold_percent": 100, "enabled": true},
		{"alert_type": "credit_warning", "threshold_percent": 80, "enabled": true},
		{"alert_type": "credit_warning", "threshold_percent": 90, "enabled": true},
		{"alert_type": "credit_exceeded", "threshold_percent": 100, "enabled": true},
	}
	var settings []gin.H
	for _, s := range defaultSettings {
		key := fmt.Sprintf("%s:%d", s["alert_type"], s["threshold_percent"])
		if en, ok := configured[key]; ok {
			s["enabled"] = en
		}
		settings = append(settings, s)
	}

	h.RenderPage(c, "enterprise/alerts", PageData{
		Title: h.i18n.T("enterprise.alerts", h.getLocale(c)),
		Data: gin.H{
			"Alerts":          alerts,
			"Total":           total,
			"OpenCount":       openCount,
			"AckCount":        ackCount,
			"Settings":        settings,
			"StatusFilter":    statusFilter,
			"AlertTypeFilter": alertTypeFilter,
			"AlertTypes":      []string{"quota_warning", "quota_exceeded", "credit_warning", "credit_exceeded"},
		},
	})
}

func (h *Handler) handleEnterpriseSettings(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	ctx := c.Request.Context()

	type settingsData struct {
		ID                string // enterprise id; the purge confirmation echoes it
		Name              string
		LegalName         string
		ContactEmail      string
		ContactName       string
		ContactPhone      string
		Country           string
		TaxNumber         string
		RegisteredAddress string
		BankName          string
		BankAccount       string
		CreatedAt         interface{}
		VerifiedAt        *time.Time
		MemberCount       int
		MaxMembers        int
		PricingRatio      float64
		BillingCycle      string
		QuotaTotal        int64
		CreditTotalUSD    float64
	}
	d := &settingsData{ID: enterpriseID}
	var verifiedAt sql.NullTime
	_ = h.db.QueryRow(ctx,
		`SELECT e.name, COALESCE(e.legal_name,''), COALESCE(e.contact_email,''), COALESCE(e.contact_name,''), COALESCE(e.contact_phone,''),
			COALESCE(e.country,''), COALESCE(e.tax_number,''), COALESCE(e.registered_address,''),
			COALESCE(e.bank_name,''), COALESCE(e.bank_account,''),
			e.created_at, e.verified_at,
			COALESCE(e.max_members, 50), COALESCE(e.pricing_ratio, 1.0),
			COALESCE(e.billing_cycle, 'monthly'), COALESCE(e.quota_total, 0), COALESCE(e.credit_total, 0)
		 FROM enterprises e
		 WHERE e.id = $1::uuid`, enterpriseID,
	).Scan(&d.Name, &d.LegalName, &d.ContactEmail, &d.ContactName, &d.ContactPhone,
		&d.Country, &d.TaxNumber, &d.RegisteredAddress,
		&d.BankName, &d.BankAccount,
		&d.CreatedAt, &verifiedAt,
		&d.MaxMembers, &d.PricingRatio,
		&d.BillingCycle, &d.QuotaTotal, &d.CreditTotalUSD)
	if verifiedAt.Valid {
		d.VerifiedAt = &verifiedAt.Time
	}
	d.CreditTotalUSD = d.CreditTotalUSD / 1000000
	_ = h.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM users WHERE enterprise_id = $1::uuid AND deleted_at IS NULL`, enterpriseID,
	).Scan(&d.MemberCount)

	h.RenderPage(c, "enterprise/settings", PageData{
		Title: h.i18n.T("Enterprise Settings", h.getLocale(c)),
		Data:  d,
	})
}

func (h *Handler) handleEnterpriseApply(c *gin.Context) {
	h.RenderPage(c, "enterprise-apply", PageData{
		Title: h.i18n.T("Enterprise Registration", h.getLocale(c)),
	})
}

func (h *Handler) handleAdminEnterpriseApplications(c *gin.Context) {
	h.RenderPage(c, "admin-enterprise-applications", PageData{
		Title: h.i18n.T("Enterprise Applications", h.getLocale(c)),
	})
}

func nullInt64(v sql.NullInt64) int64 {
	if v.Valid {
		return v.Int64
	}
	return 0
}

// --- Super Admin Handlers ---

func (h *Handler) handleSuperAdminManagement(c *gin.Context) {
	admins, _ := h.GetSuperAdmins(c.Request.Context())
	h.RenderPage(c, "super/admin-management", PageData{
		Title: h.i18n.T("super.title", h.getLocale(c)),
		Data:  admins,
	})
}

func (h *Handler) handleSuperPayment(c *gin.Context) {
	config, _ := h.GetPaymentConfig(c.Request.Context())
	var enabledCount int
	_ = h.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM payment_gateways WHERE enabled = true`).Scan(&enabledCount)
	h.RenderPage(c, "super/payment", PageData{
		Title: h.i18n.T("super.payment", h.getLocale(c)),
		Data: map[string]interface{}{
			"Config":         config,
			"GatewayEnabled": enabledCount > 0,
		},
	})
}

// --- Monitoring Handlers ---

func (h *Handler) handleMonitorUpstream(c *gin.Context) {
	data, _ := h.GetUpstreamKeyMonitorData(c.Request.Context(), getPage(c), 50)
	h.RenderPage(c, "admin-monitor-upstream", PageData{
		Title: h.i18n.T("admin.monitor_upstream", h.getLocale(c)),
		Data:  data,
	})
}

func (h *Handler) handleMonitorDownstream(c *gin.Context) {
	period := c.DefaultQuery("period", "7d")
	if period != "1d" && period != "24h" && period != "7d" && period != "15d" && period != "30d" && period != "quarter" && period != "90d" {
		period = "7d"
	}
	search := c.Query("search")
	status := c.Query("status")
	keyType := c.Query("type")
	sort := c.Query("sort")
	groupBy := c.DefaultQuery("group_by", "key")
	var data *PaginatedData
	switch groupBy {
	case "user":
		data, _ = h.GetDownstreamUserMonitorData(c.Request.Context(), period, search, status, keyType, sort, getPage(c), 50)
	case "enterprise":
		data, _ = h.GetDownstreamEnterpriseMonitorData(c.Request.Context(), period, search, status, keyType, sort, getPage(c), 50)
	default:
		data, _ = h.GetDownstreamKeyMonitorData(c.Request.Context(), period, search, status, keyType, sort, getPage(c), 50)
	}
	h.RenderPage(c, "admin-monitor-downstream", PageData{
		Title: h.i18n.T("admin.monitor_downstream", h.getLocale(c)),
		Data:  data,
	})
}

func (h *Handler) handleAdminProviderHealth(c *gin.Context) {
	data, _ := h.GetAdminProviderHealthData(c.Request.Context())
	h.RenderPage(c, "admin-provider-health", PageData{
		Title: h.i18n.T("admin.provider_health", h.getLocale(c)),
		Data:  data,
	})
}

func (h *Handler) handleAdminUpstreamOps(c *gin.Context) {
	h.RenderPage(c, "admin-upstream-ops", PageData{
		Title: h.i18n.T("admin.upstream_ops", h.getLocale(c)),
	})
}

func (h *Handler) handleAdminReconciliationOps(c *gin.Context) {
	h.RenderPage(c, "admin-reconciliation-ops", PageData{
		Title: h.i18n.T("admin.upstream_reconciliation", h.getLocale(c)),
	})
}

func (h *Handler) handleAdminUpstreamPricing(c *gin.Context) {
	h.RenderPage(c, "admin-upstream-pricing", PageData{
		Title: h.i18n.T("admin.upstream_pricing", h.getLocale(c)),
	})
}

func (h *Handler) handleAdminUsageExplorer(c *gin.Context) {
	h.RenderPage(c, "admin-usage-explorer", PageData{
		Title: h.i18n.T("admin.usage_explorer", h.getLocale(c)),
	})
}

// --- Middleware helpers ---

func getPage(c *gin.Context) int {
	return getPageParam(c, "page")
}

func getPageParam(c *gin.Context, key string) int {
	page := 1
	if p := c.Query(key); p != "" {
		if parsed, err := strconv.Atoi(p); err == nil && parsed > 0 {
			page = parsed
		}
	}
	return page
}

func (h *Handler) requireRole(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userRole := c.GetString("user_role")
		for _, r := range roles {
			if userRole == r {
				c.Next()
				return
			}
		}
		q := url.Values{}
		q.Set("toast", "error")
		q.Set("message", h.i18n.T("error.insufficient_permission", h.getLocale(c)))
		c.Redirect(http.StatusFound, "/dashboard?"+q.Encode())
		c.Abort()
	}
}

func (h *Handler) handleAdminAnnouncements(c *gin.Context) {
	h.RenderPage(c, "admin-announcements", PageData{
		Title: h.i18n.T("admin.announcements", h.getLocale(c)),
	})
}

// setEnterpriseContext loads the user's enterprise_id into the gin context
func (h *Handler) setEnterpriseContext() gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")
		var enterpriseID string
		err := h.db.QueryRow(c.Request.Context(),
			`SELECT enterprise_id::text FROM users WHERE id = $1::uuid AND deleted_at IS NULL`, userID,
		).Scan(&enterpriseID)
		if err == nil && enterpriseID != "" {
			c.Set("enterprise_id", enterpriseID)
		}
		c.Next()
	}
}

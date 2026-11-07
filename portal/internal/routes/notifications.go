package routes

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/internal/middleware"
	"github.com/mayiants/ai-portal-go/internal/services"
	"github.com/gin-gonic/gin"
)

// NotificationRoutes exposes the per-user inbox powering the topbar bell,
// the /notifications page, and the email-preference toggles. All endpoints
// require authentication; there are no admin-only endpoints here because
// scoping is per-recipient at insert time (see NotificationService).
type NotificationRoutes struct {
	db    *db.DB
	auth  *middleware.Auth
	notif *services.NotificationService
}

func NewNotificationRoutes(database *db.DB, auth *middleware.Auth, notif *services.NotificationService) *NotificationRoutes {
	return &NotificationRoutes{db: database, auth: auth, notif: notif}
}

func (r *NotificationRoutes) Register(rg *gin.RouterGroup) {
	authed := rg.Group("/notifications")
	authed.Use(r.auth.RequireAuth())
	authed.GET("/unread-count", r.unreadCount)
	authed.GET("/recent", r.recent)
	authed.GET("", r.list)
	authed.POST("/:id/read", r.markRead)
	authed.POST("/read-all", r.markAllRead)
	authed.GET("/preferences", r.getPreferences)
	authed.PUT("/preferences", r.putPreference)

	authed.GET("/channels", r.listChannels)
	authed.POST("/channels", r.createChannel)
	authed.DELETE("/channels/:id", r.deleteChannel)
	authed.POST("/channels/:id/toggle", r.toggleChannel)
	authed.POST("/channels/:id/test", r.testChannel)
}

func (r *NotificationRoutes) unreadCount(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"count": r.notif.UnreadCount(c.Request.Context(), c.GetString("user_id"))})
}

func (r *NotificationRoutes) recent(c *gin.Context) {
	items, unread, err := r.notif.List(c.Request.Context(), c.GetString("user_id"), 10, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "failed to load notifications"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "unread": unread})
}

func (r *NotificationRoutes) list(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	perPage := 20
	items, unread, err := r.notif.List(c.Request.Context(), c.GetString("user_id"), perPage, (page-1)*perPage)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "failed to load notifications"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "unread": unread, "page": page, "per_page": perPage})
}

func (r *NotificationRoutes) markRead(c *gin.Context) {
	r.notif.MarkRead(c.Request.Context(), c.GetString("user_id"), c.Param("id"))
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (r *NotificationRoutes) markAllRead(c *gin.Context) {
	r.notif.MarkAllRead(c.Request.Context(), c.GetString("user_id"))
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// preferenceTypesForRole returns the event types relevant to the caller. The
// preference row (channel='email', historical name) is the master delivery
// switch for a type: disabled means no in-app notification AND no email.
// Notification scopes are fixed at insert time: balance_low/quota_warning go
// to the user themselves; enterprise_* go to enterprise admins;
// invoice_request and enterprise_join_confirm go to system admins. Showing a
// user toggles for events they can never receive is misleading, so the list
// is filtered here (and in putPreference) by role.
func preferenceTypesForRole(role string) []string {
	personal := []string{"balance_low", "quota_warning"}
	switch role {
	case "enterprise_admin":
		return append(append([]string{}, personal...),
			"enterprise_member_joined", "enterprise_member_pending_ea",
			"enterprise_quota_warning", "enterprise_quota_exceeded",
			"enterprise_credit_warning", "enterprise_credit_exceeded",
			"enterprise_invoice_request", "enterprise_monthly_statement",
		)
	case "system_admin", "super_admin":
		return append(append([]string{}, personal...),
			"invoice_request", "enterprise_join_confirm",
		)
	default:
		return personal
	}
}

func (r *NotificationRoutes) getPreferences(c *gin.Context) {
	userID := c.GetString("user_id")
	types := preferenceTypesForRole(c.GetString("role"))
	c.JSON(http.StatusOK, gin.H{
		"types":       types,
		"preferences": r.notif.EmailPreferenceSummary(c.Request.Context(), userID, types),
	})
}

type putPreferenceRequest struct {
	Type    string `json:"type" binding:"required"`
	Enabled bool   `json:"enabled"`
}

func (r *NotificationRoutes) putPreference(c *gin.Context) {
	var req putPreferenceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}
	allowed := false
	for _, t := range preferenceTypesForRole(c.GetString("role")) {
		if req.Type == t {
			allowed = true
			break
		}
	}
	if !allowed {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "unknown preference type"}})
		return
	}
	if err := r.notif.SetEmailPreference(c.Request.Context(), c.GetString("user_id"), req.Type, req.Enabled); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "failed to save preference"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---- IM webhook channels (proposal §6) ----

func (r *NotificationRoutes) listChannels(c *gin.Context) {
	channels, err := r.notif.ListChannels(c.Request.Context(), c.GetString("user_id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "failed to load channels"}})
		return
	}
	if channels == nil {
		channels = []services.NotificationChannel{}
	}
	c.JSON(http.StatusOK, gin.H{"items": channels})
}

type createChannelRequest struct {
	Type       string   `json:"type" binding:"required,oneof=feishu dingtalk wecom"`
	WebhookURL string   `json:"webhook_url" binding:"required"`
	Secret     string   `json:"secret"`
	Filters    []string `json:"filters"`
}

func (r *NotificationRoutes) createChannel(c *gin.Context) {
	var req createChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}
	if len(req.WebhookURL) > 1024 || (!strings.HasPrefix(req.WebhookURL, "https://") && !strings.HasPrefix(req.WebhookURL, "http://")) {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "invalid webhook URL"}})
		return
	}
	ch, err := r.notif.CreateChannel(c.Request.Context(), c.GetString("user_id"), req.Type, req.WebhookURL, req.Secret, req.Filters)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "failed to create channel"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"item": ch})
}

func (r *NotificationRoutes) deleteChannel(c *gin.Context) {
	if err := r.notif.DeleteChannel(c.Request.Context(), c.GetString("user_id"), c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "failed to delete channel"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type toggleChannelRequest struct {
	Enabled bool `json:"enabled"`
}

func (r *NotificationRoutes) toggleChannel(c *gin.Context) {
	var req toggleChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}
	if err := r.notif.SetChannelEnabled(c.Request.Context(), c.GetString("user_id"), c.Param("id"), req.Enabled); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "failed to update channel"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (r *NotificationRoutes) testChannel(c *gin.Context) {
	locale := GetLocale(c)
	if locale == "" {
		locale = "en"
	}
	if err := r.notif.TestChannel(c.Request.Context(), c.GetString("user_id"), c.Param("id"), locale); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": gin.H{"message": err.Error()}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

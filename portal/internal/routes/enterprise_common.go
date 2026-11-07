package routes

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mayiants/ai-portal-go/internal/config"
	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/internal/middleware"
	"github.com/mayiants/ai-portal-go/internal/services"
	"github.com/mayiants/ai-portal-go/internal/sso"
	"github.com/mayiants/ai-portal-go/pkg/audit"
	"github.com/mayiants/ai-portal-go/pkg/mail"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

type EnterpriseRoutes struct {
	db       *db.DB
	cfg      *config.Config
	logger   *zap.Logger
	auth     *middleware.Auth
	audit    *audit.Logger
	mail     *mail.Sender
	exchange *services.ExchangeService
	reward   *services.RewardService
	notif    *services.NotificationService
	// ssoClient is created lazily (see sso()); tests and main may inject one.
	ssoClient *sso.Client
}

func NewEnterpriseRoutes(db *db.DB, cfg *config.Config, logger *zap.Logger, auth *middleware.Auth, auditLog *audit.Logger, mailSender *mail.Sender, exchange *services.ExchangeService, reward *services.RewardService, notif *services.NotificationService) *EnterpriseRoutes {
	return &EnterpriseRoutes{db: db, cfg: cfg, logger: logger, auth: auth, audit: auditLog, mail: mailSender, exchange: exchange, reward: reward, notif: notif}
}

// enterpriseAuditEntry holds a single enterprise-scoped management event.
// Numeric and string old/new values are optional; use whichever fits the event.
type enterpriseAuditEntry struct {
	EnterpriseID string
	Category     string // billing | member | key | group | settings | alert | role
	ChangeType   string
	FieldName    string
	UserID       *string // subject user (e.g. member whose quota/key changed)
	ChangedBy    string  // operator/admin user ID
	OldValue     *int64
	NewValue     *int64
	OldValueStr  *string
	NewValueStr  *string
	TargetType   string // user | key | group | enterprise | alert | invitation
	TargetID     string // target object ID
	Delta        *int64
	Reason       string
	// Action is the coarse verb shown in the audit UI. Optional: when empty the
	// writer falls back to ChangeType so `action` is never blank.
	Action string
}

// logEnterpriseAudit records an enterprise-scoped management event to enterprise_audit_log.
func (r *EnterpriseRoutes) logEnterpriseAudit(ctx context.Context, entry enterpriseAuditEntry) {
	writeEnterpriseAudit(ctx, r.db, r.logger, entry)
}

// writeEnterpriseAudit records an enterprise-scoped management event to
// enterprise_audit_log. Package-level so any route handler (not just
// EnterpriseRoutes) can emit enterprise audit events — e.g. the user-facing
// key delete path, which must log enterprise-key deletion to the same log as
// creation so the enterprise/audit page shows both.
func writeEnterpriseAudit(ctx context.Context, database *db.DB, logger *zap.Logger, entry enterpriseAuditEntry) {
	if database == nil {
		return
	}
	var userIDArg interface{}
	if entry.UserID != nil && *entry.UserID != "" {
		userIDArg = *entry.UserID
	}
	var changedByArg interface{}
	if entry.ChangedBy != "" {
		changedByArg = entry.ChangedBy
	}
	// Most callers only set ChangeType; leaving `action` empty made ~78% of
	// production rows unreadable in views that group by action.
	action := entry.Action
	if action == "" {
		action = entry.ChangeType
	}
	// IP / UA come from the request context (see middleware.AuditClientInfo);
	// invalid IPs are stored as NULL so the INET cast cannot fail the insert.
	var ipArg, uaArg interface{}
	if ip := audit.NormalizeIP(audit.ClientIPFromContext(ctx)); ip != "" {
		ipArg = ip
	}
	if ua := audit.NormalizeUserAgent(audit.UserAgentFromContext(ctx)); ua != "" {
		uaArg = ua
	}
	if _, err := database.Pool.Exec(ctx, `
		INSERT INTO enterprise_audit_log (enterprise_id, category, change_type, field_name, user_id, changed_by, old_value, new_value, old_value_str, new_value_str, target_type, target_id, delta, reason, action, ip_address, user_agent)
		VALUES ($1::uuid, $2, $3, $4, $5::uuid, $6::uuid, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16::inet, $17)`,
		entry.EnterpriseID, entry.Category, entry.ChangeType, entry.FieldName, userIDArg, changedByArg,
		entry.OldValue, entry.NewValue, entry.OldValueStr, entry.NewValueStr, entry.TargetType, entry.TargetID, entry.Delta, entry.Reason,
		action, ipArg, uaArg); err != nil {
		if logger != nil {
			logger.Warn("Failed to log enterprise audit event",
				zap.String("category", entry.Category),
				zap.String("change_type", entry.ChangeType),
				zap.Error(err))
		}
	}
}

func (r *EnterpriseRoutes) Register(rg *gin.RouterGroup) {
	// Admin enterprise management
	admin := rg.Group("/admin/enterprise")
	admin.Use(r.auth.RequireAuth(), r.auth.RequireAdmin())
	{
		admin.GET("", r.listEnterprises)
		admin.POST("", r.createEnterprise)
		admin.GET("/:id", r.getEnterprise)
		admin.PUT("/:id", r.updateEnterprise)
		admin.DELETE("/:id", r.deleteEnterprise)
		admin.POST("/:id/restore", r.restoreEnterprise)
		admin.POST("/:id/suspend", r.suspendEnterprise)
		admin.POST("/:id/activate", r.activateEnterprise)
		admin.POST("/:id/verify", r.verifyEnterprise)
		admin.POST("/:id/topup", r.topupEnterprisePool)
		admin.GET("/change-requests", r.listEnterpriseChangeRequests)
		admin.POST("/change-requests/:id/review", r.reviewEnterpriseChangeRequest)
		admin.GET("/:id/members", r.listMembers)
		admin.POST("/:id/invite", r.inviteMember)
		admin.DELETE("/:id/members/:userId", r.removeMember)
		// Force-reassign a user's enterprise (SA recovery path). Registered under
		// a static /reassign-user segment so it never collides with the /:id routes.
		admin.POST("/reassign-user", r.reassignUserEnterprise)
		admin.GET("/invitations", r.listInvitations)
		admin.DELETE("/invitations/:id", r.cancelInvitation)
		admin.POST("/:id/invitations/:invId/approve", r.approveInvitationAdmin)
		admin.POST("/:id/invitations/:invId/reject", r.rejectInvitationAdmin)
		admin.PUT("/:id/appoint-admin", r.appointAdmin)
	}

	// Enterprise self-management (viewable by members OR enterprise/system/super admins).
	// Mutating actions additionally require requireEnterpriseContext() so admins without
	// an enterprise cannot create members/keys/etc. in a non-existent enterprise.
	enterprise := rg.Group("/enterprise")
	enterprise.Use(r.auth.RequireAuth(), r.requireEnterpriseAccess())
	{
		enterprise.GET("", r.getMyEnterprise)
		enterprise.GET("/members", r.getMyEnterpriseMembers)
		enterprise.GET("/usage", r.getMyEnterpriseUsage)
		enterprise.GET("/groups/public", r.getMyEnterprisePublicGroups)
		enterprise.POST("/members", r.requireEnterpriseAdmin(), r.requireEnterpriseContext(), r.addEnterpriseMember)
		enterprise.DELETE("/members/:userId", r.requireEnterpriseAdmin(), r.requireEnterpriseContext(), r.removeMyEnterpriseMember)
		enterprise.POST("/members/:userId/topup", r.requireEnterpriseAdmin(), r.requireEnterpriseContext(), r.topupMemberQuota)
		enterprise.PUT("/members/:userId/quota-limit", r.requireEnterpriseAdmin(), r.requireEnterpriseContext(), r.setMemberQuotaLimit)
		enterprise.PUT("/members/:userId/credit-limit", r.requireEnterpriseAdmin(), r.requireEnterpriseContext(), r.setMemberCreditLimit)
		enterprise.POST("/members/:userId/toggle", r.requireEnterpriseAdmin(), r.requireEnterpriseContext(), r.toggleMemberStatus)
		enterprise.PUT("/members/:userId/role", r.requireEnterpriseAdmin(), r.requireEnterpriseContext(), r.promoteDemoteMember)
		enterprise.PUT("/members/:userId/name", r.requireEnterpriseAdmin(), r.requireEnterpriseContext(), r.updateMemberName)
		enterprise.POST("/quit", r.requireEnterpriseContext(), r.quitEnterprise)
		enterprise.POST("/members/import", r.requireEnterpriseAdmin(), r.requireEnterpriseContext(), r.importEnterpriseMembers)
		enterprise.GET("/members/export", r.requireEnterpriseAdmin(), r.exportEnterpriseMembers)
		enterprise.POST("/members/batch-quota", r.requireEnterpriseAdmin(), r.requireEnterpriseContext(), r.batchSetMemberQuota)
		enterprise.POST("/members/batch-credit", r.requireEnterpriseAdmin(), r.requireEnterpriseContext(), r.batchSetMemberCreditLimit)
		enterprise.GET("/keys", r.listEnterpriseKeys)
		enterprise.POST("/keys", r.requireEnterpriseAdmin(), r.requireEnterpriseContext(), r.createEnterpriseKey)
		enterprise.PUT("/keys/:keyId", r.requireEnterpriseAdmin(), r.requireEnterpriseContext(), r.reassignKey)
		enterprise.POST("/keys/:keyId/revoke", r.requireEnterpriseAdmin(), r.requireEnterpriseContext(), r.revokeEnterpriseKey)
		enterprise.POST("/keys/:keyId/reactivate", r.requireEnterpriseAdmin(), r.requireEnterpriseContext(), r.reactivateEnterpriseKey)
		enterprise.DELETE("/keys/:keyId", r.requireEnterpriseAdmin(), r.requireEnterpriseContext(), r.deleteEnterpriseKey)
		enterprise.GET("/audit", r.requireEnterpriseAdmin(), r.listEnterpriseAuditLog)
		enterprise.GET("/audit/export", r.requireEnterpriseAdmin(), r.exportAuditCSV)
		enterprise.GET("/alerts", r.requireEnterpriseAdmin(), r.requireEnterpriseContext(), r.listEnterpriseAlerts)
		enterprise.GET("/alert-settings", r.requireEnterpriseAdmin(), r.requireEnterpriseContext(), r.getEnterpriseAlertSettings)
		enterprise.PUT("/alert-settings", r.requireEnterpriseAdmin(), r.requireEnterpriseContext(), r.updateEnterpriseAlertSetting)
		enterprise.POST("/alerts/:id/ack", r.requireEnterpriseAdmin(), r.requireEnterpriseContext(), r.acknowledgeAlert)
	}

	// Enterprise self-service invitation management + billing + groups
	enterpriseAdmin := rg.Group("/enterprise")
	enterpriseAdmin.Use(r.auth.RequireAuth(), r.requireEnterpriseAdmin())
	{
		enterpriseAdmin.GET("/invitations", r.listMyEnterpriseInvitations)
		enterpriseAdmin.PUT("/invitations/:invId/approve", r.requireEnterpriseContext(), r.approveInvitation)
		enterpriseAdmin.PUT("/invitations/:invId/reject", r.requireEnterpriseContext(), r.rejectInvitation)
		enterpriseAdmin.DELETE("/invitations/:invId", r.requireEnterpriseContext(), r.cancelMyEnterpriseInvitation)
		enterpriseAdmin.POST("/invitations/:invId/resend", r.requireEnterpriseContext(), r.resendMyEnterpriseInvitation)
		enterpriseAdmin.POST("/invitations/:invId/copy-link", r.requireEnterpriseContext(), r.copyMyEnterpriseInvitationLink)
		enterpriseAdmin.GET("/billing/details", r.getEnterpriseBillingDetails)
		enterpriseAdmin.GET("/groups", r.listEnterpriseGroups)
		enterpriseAdmin.POST("/groups", r.requireEnterpriseContext(), r.createEnterpriseGroup)
		enterpriseAdmin.PUT("/groups/:groupId", r.requireEnterpriseContext(), r.updateEnterpriseGroup)
		enterpriseAdmin.DELETE("/groups/:groupId", r.requireEnterpriseContext(), r.deleteEnterpriseGroup)
		enterpriseAdmin.GET("/available-models", r.getAvailableModels)
		enterpriseAdmin.GET("/platform-groups", r.getPlatformGroups)
		enterpriseAdmin.PUT("/profile", r.requireEnterpriseContext(), r.updateMyEnterprise)
		enterpriseAdmin.POST("/settings/change-request", r.requireEnterpriseContext(), r.submitEnterpriseChangeRequest)
		enterpriseAdmin.GET("/settings/change-requests", r.listMyChangeRequests)
		enterpriseAdmin.GET("/reconciliation", r.getEnterpriseReconciliation)
		enterpriseAdmin.GET("/reconciliation/export", r.exportEnterpriseReconciliationCSV)
		enterpriseAdmin.GET("/reconciliation/member/:userId", r.getEnterpriseMemberReconciliationDetail)
		enterpriseAdmin.GET("/reconciliation/search", r.searchEnterpriseMember)
		enterpriseAdmin.GET("/usage/trend", r.getEnterpriseUsageTrend)
		enterpriseAdmin.GET("/usage/member-logs", r.getEnterpriseMemberLogs)
		enterpriseAdmin.GET("/usage/member-logs/export", r.exportEnterpriseMemberLogsCSV)
		enterpriseAdmin.GET("/usage/details", r.getEnterpriseUsageDetails)
		enterpriseAdmin.GET("/usage/export", r.exportEnterpriseUsageCSV)
		enterpriseAdmin.GET("/usage/errors", r.getEnterpriseUsageErrors)
		enterpriseAdmin.GET("/usage/error-logs", r.getEnterpriseErrorLogs)
		enterpriseAdmin.GET("/usage/rate-limits", r.getEnterpriseRateLimitHits)
		enterpriseAdmin.GET("/billing/trend", r.getEnterpriseBillingTrend)
	}

	// Data-retention policy (enterprise_admin of the enterprise or super_admin)
	r.RegisterRetention(rg)
	r.RegisterSecurity(rg)
	r.RegisterSSO(rg)

	// Public invitation acceptance
	rg.GET("/enterprise/accept", r.acceptInvitationPage)
	rg.POST("/enterprise/accept", r.acceptInvitation)
}

// requireEnterpriseAdmin ensures the user is an enterprise admin of a specific enterprise.
// System/super admins do NOT bypass this check for self-management endpoints.
func (r *EnterpriseRoutes) requireEnterpriseAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")
		var role string
		var enterpriseID *string
		err := r.db.QueryRow(c.Request.Context(),
			`SELECT role, enterprise_id::text FROM users WHERE id = $1::uuid AND deleted_at IS NULL`,
			userID,
		).Scan(&role, &enterpriseID)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Failed to verify permissions"}})
			return
		}
		if role != "enterprise_admin" || enterpriseID == nil || *enterpriseID == "" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Enterprise admin access required"}})
			return
		}
		c.Set("enterprise_id", *enterpriseID)
		c.Next()
	}
}

// requireEnterpriseAccess allows only enterprise members to view enterprise self-management pages.
// System/super admins must use the /api/admin/enterprise management endpoints instead.
func (r *EnterpriseRoutes) requireEnterpriseAccess() gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")
		var enterpriseID *string
		err := r.db.QueryRow(c.Request.Context(),
			`SELECT enterprise_id::text FROM users WHERE id = $1::uuid AND deleted_at IS NULL`,
			userID,
		).Scan(&enterpriseID)
		if err != nil || enterpriseID == nil || *enterpriseID == "" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{"message": T("error.not_enterprise_member", c)}})
			return
		}

		c.Set("enterprise_id", *enterpriseID)
		c.Next()
	}
}

// requireEnterpriseContext ensures an enterprise_id has been set. Use this on
// mutating enterprise routes so admins without an enterprise cannot create data
// in a non-existent enterprise.
func (r *EnterpriseRoutes) requireEnterpriseContext() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetString("enterprise_id") == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.enterprise_not_found", c)}})
			return
		}
		c.Next()
	}
}

// getMyEnterprise returns the current user's enterprise details
func (r *EnterpriseRoutes) getMyEnterprise(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")

	var id, name, contactEmail, status, country, displayCurrency string
	var quotaTotal, quotaUsed, creditTotal, creditUsed, prepaidBalance int64
	var pricingRatio, exchangeRate float64
	var memberCount int
	var createdAt, verifiedAt interface{}

	err := r.db.QueryRow(c.Request.Context(),
		`SELECT e.id::text, e.name, COALESCE(e.contact_email, ''), e.status,
		        COALESCE(e.quota_total, 0), COALESCE(e.quota_used, 0),
		        COALESCE(e.credit_total, 0), COALESCE(e.credit_used, 0), COALESCE(e.prepaid_balance, 0),
		        COALESCE(e.pricing_ratio, 1.0),
		        (SELECT COUNT(*) FROM users WHERE enterprise_id = e.id AND deleted_at IS NULL),
		        e.created_at, e.verified_at, COALESCE(e.country, ''),
		        COALESCE(e.display_currency, 'USD'), COALESCE(e.exchange_rate, 1.0)
		 FROM enterprises e WHERE e.id = $1::uuid`, enterpriseID,
	).Scan(&id, &name, &contactEmail, &status, &quotaTotal, &quotaUsed, &creditTotal, &creditUsed, &prepaidBalance, &pricingRatio, &memberCount, &createdAt, &verifiedAt, &country, &displayCurrency, &exchangeRate)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Enterprise not found"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"enterprise": gin.H{
			"id": id, "name": name, "contact_email": contactEmail, "status": status,
			"country": country, "verified_at": verifiedAt,
			"quota_total": quotaTotal, "quota_used": quotaUsed,
			"credit_total": creditTotal, "credit_used": creditUsed,
			"prepaid_balance":  prepaidBalance,
			"pricing_ratio":    pricingRatio,
			"display_currency": displayCurrency, "exchange_rate": exchangeRate,
			"member_count": memberCount, "created_at": createdAt,
		},
	})
}

// updateMyEnterprise allows an enterprise admin to update their own enterprise name/contact email.
// Once an enterprise is verified, these fields must be changed via change request instead.
func (r *EnterpriseRoutes) updateMyEnterprise(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	var req struct {
		Name         *string `json:"name"`
		ContactEmail *string `json:"contact_email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	ctx := c.Request.Context()

	// Verified enterprises cannot directly edit protected info.
	var verifiedAt interface{}
	_ = r.db.QueryRow(ctx, `SELECT verified_at FROM enterprises WHERE id = $1::uuid`, enterpriseID).Scan(&verifiedAt)
	if verifiedAt != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Verified enterprise information cannot be edited directly. Please submit a change request."}})
		return
	}

	var setClauses []string
	var args []interface{}
	argIdx := 1

	var oldName, oldContactEmail string
	_ = r.db.QueryRow(ctx, `SELECT COALESCE(name, ''), COALESCE(contact_email, '') FROM enterprises WHERE id = $1::uuid`, enterpriseID).Scan(&oldName, &oldContactEmail)

	if req.Name != nil {
		var dupCount int
		_ = r.db.QueryRow(ctx, `SELECT COUNT(*) FROM enterprises WHERE name = $1 AND id != $2::uuid AND deleted_at IS NULL`, *req.Name, enterpriseID).Scan(&dupCount)
		if dupCount > 0 {
			c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "Enterprise name already exists"}})
			return
		}
		setClauses = append(setClauses, fmt.Sprintf("name = $%d", argIdx))
		args = append(args, *req.Name)
		argIdx++
	}
	if req.ContactEmail != nil {
		email := strings.TrimSpace(*req.ContactEmail)
		if email != "" {
			var dupCount int
			_ = r.db.QueryRow(ctx, `SELECT COUNT(*) FROM enterprises WHERE contact_email = $1 AND id != $2::uuid AND deleted_at IS NULL`, email, enterpriseID).Scan(&dupCount)
			if dupCount > 0 {
				c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "Contact email already used by another enterprise"}})
				return
			}
		}
		setClauses = append(setClauses, fmt.Sprintf("contact_email = $%d", argIdx))
		args = append(args, email)
		argIdx++
	}

	if len(setClauses) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "No fields to update"}})
		return
	}

	setClauses = append(setClauses, fmt.Sprintf("updated_at = $%d", argIdx))
	args = append(args, time.Now())
	argIdx++

	query := fmt.Sprintf("UPDATE enterprises SET %s WHERE id = $%d::uuid AND deleted_at IS NULL", strings.Join(setClauses, ", "), argIdx)
	args = append(args, enterpriseID)

	if err := r.db.Exec(ctx, query, args...); err != nil {
		r.logger.Error("Failed to update enterprise profile", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update enterprise"}})
		return
	}

	if req.Name != nil && *req.Name != oldName {
		newName := *req.Name
		r.logEnterpriseAudit(ctx, enterpriseAuditEntry{
			EnterpriseID: enterpriseID,
			Category:     "settings",
			ChangeType:   "enterprise_profile_updated",
			FieldName:    "name",
			ChangedBy:    c.GetString("user_id"),
			OldValueStr:  &oldName,
			NewValueStr:  &newName,
			Reason:       "Enterprise name updated",
		})
	}
	if req.ContactEmail != nil {
		email := strings.TrimSpace(*req.ContactEmail)
		if email != oldContactEmail {
			r.logEnterpriseAudit(ctx, enterpriseAuditEntry{
				EnterpriseID: enterpriseID,
				Category:     "settings",
				ChangeType:   "enterprise_profile_updated",
				FieldName:    "contact_email",
				ChangedBy:    c.GetString("user_id"),
				OldValueStr:  &oldContactEmail,
				NewValueStr:  &email,
				Reason:       "Enterprise contact email updated",
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// getMyEnterpriseMembers returns members of the current user's enterprise
func (r *EnterpriseRoutes) getMyEnterpriseMembers(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")

	rows, err := r.db.Query(c.Request.Context(),
		`SELECT u.id::text, u.email, COALESCE(u.name, '') as name, u.role, u.status, u.created_at,
		        COALESCE(em.quota_limit, 0)
		 FROM users u
		 JOIN enterprise_members em ON u.id = em.user_id AND em.enterprise_id = u.enterprise_id AND em.removed_at IS NULL
		 WHERE u.enterprise_id = $1::uuid AND u.deleted_at IS NULL
		 ORDER BY u.created_at DESC`, enterpriseID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list members"}})
		return
	}
	defer rows.Close()

	members := []gin.H{}
	for rows.Next() {
		var id, email, name, role, status string
		var createdAt interface{}
		var quotaLimit int64
		if err := rows.Scan(&id, &email, &name, &role, &status, &createdAt, &quotaLimit); err != nil {
			continue
		}
		members = append(members, gin.H{
			"id": id, "email": email, "name": name, "role": role, "status": status,
			"quota_limit": quotaLimit, "created_at": createdAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"members": members})
}

// getMyEnterpriseUsage returns usage statistics for the current user's enterprise
func (r *EnterpriseRoutes) getMyEnterpriseUsage(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")

	var totalRequests int
	var totalTokens int64
	var totalCost float64 // micro-USD (1e6 = 1 USD)
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*), COALESCE(SUM(ur.input_tokens + ur.output_tokens), 0), COALESCE(SUM(ur.total_cost), 0)
		 FROM usage_records ur
		 JOIN api_keys k ON ur.user_api_key_id = k.id
		 WHERE k.enterprise_id = $1::uuid AND k.key_type = 'enterprise'`, enterpriseID,
	).Scan(&totalRequests, &totalTokens, &totalCost)

	// Per-model breakdown
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT ur.model, COUNT(*) as requests, SUM(ur.input_tokens + ur.output_tokens) as tokens, SUM(ur.total_cost) as cost
		 FROM usage_records ur
		 JOIN api_keys k ON ur.user_api_key_id = k.id
		 WHERE k.enterprise_id = $1::uuid AND k.key_type = 'enterprise'
		 GROUP BY ur.model ORDER BY SUM(ur.total_cost) DESC LIMIT 10`, enterpriseID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to get usage"}})
		return
	}
	defer rows.Close()

	modelStats := []gin.H{}
	for rows.Next() {
		var model string
		var requests int
		var tokens int64
		var cost float64
		if err := rows.Scan(&model, &requests, &tokens, &cost); err != nil {
			continue
		}
		modelStats = append(modelStats, gin.H{
			"model": model, "requests": requests, "tokens": tokens, "cost": cost, // cost is micro-USD
		})
	}

	// Per-user breakdown
	userRows, err := r.db.Query(c.Request.Context(),
		`SELECT u.id::text, COALESCE(u.name, u.email) as user_name, COUNT(*) as requests,
		        SUM(ur.input_tokens + ur.output_tokens) as tokens, SUM(ur.total_cost) as cost
		 FROM usage_records ur
		 JOIN api_keys k ON ur.user_api_key_id = k.id
		 JOIN users u ON ur.user_id = u.id
		 WHERE k.enterprise_id = $1::uuid AND k.key_type = 'enterprise'
		 GROUP BY u.id, u.name, u.email
		 ORDER BY SUM(ur.total_cost) DESC LIMIT 10`, enterpriseID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to get user usage"}})
		return
	}
	defer userRows.Close()

	userStats := []gin.H{}
	for userRows.Next() {
		var id, userName string
		var requests int
		var tokens int64
		var cost float64
		if err := userRows.Scan(&id, &userName, &requests, &tokens, &cost); err != nil {
			continue
		}
		userStats = append(userStats, gin.H{
			"id": id, "name": userName, "requests": requests, "tokens": tokens, "cost": cost, // cost is micro-USD
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"total_requests": totalRequests,
		"total_tokens":   totalTokens,
		"total_cost":     totalCost, // micro-USD (1e6 = 1 USD)
		"models":         modelStats,
		"users":          userStats,
	})
}

// getEnterpriseUsageDetails returns detailed usage with date range and dimension support
func (r *EnterpriseRoutes) getEnterpriseUsageDetails(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	dimension := c.DefaultQuery("dimension", "user") // user, model, key
	dateFrom := c.DefaultQuery("from", "")
	dateTo := c.DefaultQuery("to", "")

	// Build WHERE clause - only usage from enterprise-owned keys (sk-wgit-en-*)
	whereClause := "k.enterprise_id = $1::uuid AND k.key_type = 'enterprise'"
	args := []interface{}{enterpriseID}
	argIdx := 2

	if dateFrom != "" {
		whereClause += fmt.Sprintf(" AND ur.created_at >= $%d::date", argIdx)
		args = append(args, dateFrom)
		argIdx++
	} else {
		whereClause += " AND ur.created_at >= DATE_TRUNC('month', CURRENT_DATE)"
	}
	if dateTo != "" {
		whereClause += fmt.Sprintf(" AND ur.created_at < ($%d::date + INTERVAL '1 day')", argIdx)
		args = append(args, dateTo)
	}

	var query string
	switch dimension {
	case "model":
		query = fmt.Sprintf(`
			SELECT COALESCE(ur.model, 'unknown') as dimension_value,
			       COUNT(*) as requests,
			       SUM(ur.input_tokens + ur.output_tokens) as tokens,
			       SUM(ur.input_tokens) as input_tokens,
			       SUM(ur.cached_input_tokens) as cached_input_tokens,
			       SUM(ur.output_tokens) as output_tokens,
			       SUM(ur.total_cost) as cost,
			       COALESCE(AVG(ur.latency_ms), 0)::int as avg_latency_ms,
			       CASE WHEN COALESCE(SUM(ur.latency_ms), 0) > 0
			            THEN COALESCE(SUM(ur.input_tokens + ur.output_tokens), 0)::numeric * 1000.0 / SUM(ur.latency_ms)
			            ELSE 0
			       END as avg_tps
			FROM usage_records ur
			JOIN api_keys k ON ur.user_api_key_id = k.id
			WHERE %s
			GROUP BY ur.model
			ORDER BY cost DESC`, whereClause)
	case "key":
		query = fmt.Sprintf(`
			SELECT COALESCE(k.key_prefix, 'unknown') as dimension_value,
			       COUNT(*) as requests,
			       SUM(ur.input_tokens + ur.output_tokens) as tokens,
			       SUM(ur.input_tokens) as input_tokens,
			       SUM(ur.cached_input_tokens) as cached_input_tokens,
			       SUM(ur.output_tokens) as output_tokens,
			       SUM(ur.total_cost) as cost,
			       COALESCE(AVG(ur.latency_ms), 0)::int as avg_latency_ms,
			       CASE WHEN COALESCE(SUM(ur.latency_ms), 0) > 0
			            THEN COALESCE(SUM(ur.input_tokens + ur.output_tokens), 0)::numeric * 1000.0 / SUM(ur.latency_ms)
			            ELSE 0
			       END as avg_tps
			FROM usage_records ur
			JOIN api_keys k ON ur.user_api_key_id = k.id
			WHERE %s
			GROUP BY k.id, k.key_prefix
			ORDER BY cost DESC`, whereClause)
	default: // user
		query = fmt.Sprintf(`
			SELECT COALESCE(u.name, u.email, 'unknown') as dimension_value,
			       COUNT(*) as requests,
			       SUM(ur.input_tokens + ur.output_tokens) as tokens,
			       SUM(ur.input_tokens) as input_tokens,
			       SUM(ur.cached_input_tokens) as cached_input_tokens,
			       SUM(ur.output_tokens) as output_tokens,
			       SUM(ur.total_cost) as cost,
			       COALESCE(AVG(ur.latency_ms), 0)::int as avg_latency_ms,
			       CASE WHEN COALESCE(SUM(ur.latency_ms), 0) > 0
			            THEN COALESCE(SUM(ur.input_tokens + ur.output_tokens), 0)::numeric * 1000.0 / SUM(ur.latency_ms)
			            ELSE 0
			       END as avg_tps
			FROM usage_records ur
			JOIN api_keys k ON ur.user_api_key_id = k.id
			JOIN users u ON ur.user_id = u.id
			WHERE %s
			GROUP BY u.id, u.name, u.email
			ORDER BY cost DESC`, whereClause)
	}

	rows, err := r.db.Query(c.Request.Context(), query, args...)
	if err != nil {
		r.logger.Error("Failed to query usage details", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to get usage details"}})
		return
	}
	defer rows.Close()

	type UsageRow struct {
		DimensionValue    string  `json:"dimension_value"`
		Requests          int     `json:"requests"`
		Tokens            int64   `json:"tokens"`
		InputTokens       int64   `json:"input_tokens"`
		CachedInputTokens int64   `json:"cached_input_tokens"`
		OutputTokens      int64   `json:"output_tokens"`
		Cost              float64 `json:"cost"` // micro-USD (1e6 = 1 USD)
		AvgLatencyMs      int     `json:"avg_latency_ms"`
		AvgTps            float64 `json:"avg_tps"`
	}

	var results []UsageRow
	var totalRequests int
	var totalTokens, totalInput, totalCachedInput, totalOutput int64
	var totalCost float64

	for rows.Next() {
		var row UsageRow
		if err := rows.Scan(&row.DimensionValue, &row.Requests, &row.Tokens, &row.InputTokens, &row.CachedInputTokens, &row.OutputTokens, &row.Cost, &row.AvgLatencyMs, &row.AvgTps); err != nil {
			continue
		}
		results = append(results, row)
		totalRequests += row.Requests
		totalTokens += row.Tokens
		totalInput += row.InputTokens
		totalCachedInput += row.CachedInputTokens
		totalOutput += row.OutputTokens
		totalCost += row.Cost
	}

	c.JSON(http.StatusOK, gin.H{
		"dimension": dimension,
		"from":      dateFrom,
		"to":        dateTo,
		"details":   results,
		"total": gin.H{
			"requests":            totalRequests,
			"tokens":              totalTokens,
			"input_tokens":        totalInput,
			"cached_input_tokens": totalCachedInput,
			"output_tokens":       totalOutput,
			"cost":                totalCost, // micro-USD (1e6 = 1 USD)
		},
	})
}

// exportEnterpriseUsageCSV exports usage details as CSV
func (r *EnterpriseRoutes) exportEnterpriseUsageCSV(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	dimension := c.DefaultQuery("dimension", "user")
	dateFrom := c.DefaultQuery("from", "")
	dateTo := c.DefaultQuery("to", "")

	whereClause := "k.enterprise_id = $1::uuid AND k.key_type = 'enterprise'"
	args := []interface{}{enterpriseID}
	argIdx := 2

	if dateFrom != "" {
		whereClause += fmt.Sprintf(" AND ur.created_at >= $%d::date", argIdx)
		args = append(args, dateFrom)
		argIdx++
	} else {
		whereClause += " AND ur.created_at >= DATE_TRUNC('month', CURRENT_DATE)"
	}
	if dateTo != "" {
		whereClause += fmt.Sprintf(" AND ur.created_at < ($%d::date + INTERVAL '1 day')", argIdx)
		args = append(args, dateTo)
	}

	var query string
	switch dimension {
	case "model":
		query = fmt.Sprintf(`
			SELECT COALESCE(ur.model, 'unknown'),
			       COUNT(*), SUM(ur.input_tokens + ur.output_tokens),
			       SUM(ur.input_tokens), SUM(ur.cached_input_tokens), SUM(ur.output_tokens), SUM(ur.total_cost)
			FROM usage_records ur
			JOIN api_keys k ON ur.user_api_key_id = k.id
			WHERE %s
			GROUP BY ur.model ORDER BY SUM(ur.total_cost) DESC`, whereClause)
	case "key":
		query = fmt.Sprintf(`
			SELECT COALESCE(k.key_prefix, 'unknown'),
			       COUNT(*), SUM(ur.input_tokens + ur.output_tokens),
			       SUM(ur.input_tokens), SUM(ur.cached_input_tokens), SUM(ur.output_tokens), SUM(ur.total_cost)
			FROM usage_records ur
			JOIN api_keys k ON ur.user_api_key_id = k.id
			WHERE %s
			GROUP BY k.id, k.key_prefix
			ORDER BY SUM(ur.total_cost) DESC`, whereClause)
	default:
		query = fmt.Sprintf(`
			SELECT COALESCE(u.name, u.email, 'unknown'),
			       COUNT(*), SUM(ur.input_tokens + ur.output_tokens),
			       SUM(ur.input_tokens), SUM(ur.cached_input_tokens), SUM(ur.output_tokens), SUM(ur.total_cost)
			FROM usage_records ur
			JOIN api_keys k ON ur.user_api_key_id = k.id
			JOIN users u ON ur.user_id = u.id
			WHERE %s
			GROUP BY u.id, u.name, u.email ORDER BY SUM(ur.total_cost) DESC`, whereClause)
	}

	rows, err := r.db.Query(c.Request.Context(), query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to export"}})
		return
	}
	defer rows.Close()

	// Build CSV
	var header string
	switch dimension {
	case "model":
		header = "Model,Requests,Total Tokens,Input Tokens,Cached Input Tokens,Output Tokens,Cost (micro-USD)\n"
	case "key":
		header = "Key Name,Requests,Total Tokens,Input Tokens,Cached Input Tokens,Output Tokens,Cost (micro-USD)\n"
	default:
		header = "User,Requests,Total Tokens,Input Tokens,Cached Input Tokens,Output Tokens,Cost (micro-USD)\n"
	}

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=enterprise-usage-%s-%s.csv", dimension, time.Now().UTC().Format("200601021504")))
	_, _ = c.Writer.WriteString(header)

	for rows.Next() {
		var name string
		var requests int
		var tokens, inputT, cachedInputT, outputT int64
		var cost float64
		if err := rows.Scan(&name, &requests, &tokens, &inputT, &cachedInputT, &outputT, &cost); err != nil {
			continue
		}
		_, _ = fmt.Fprintf(c.Writer, "%s,%d,%d,%d,%d,%d,%.0f\n",
			strings.ReplaceAll(name, ",", ";"), requests, tokens, inputT, cachedInputT, outputT, cost)
	}
}

// listEnterprises returns all non-deleted enterprises
func (r *EnterpriseRoutes) listEnterprises(c *gin.Context) {
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT e.id::text, e.name, COALESCE(e.contact_email, ''), e.status,
		        COALESCE(e.quota_total, 0), COALESCE(e.quota_used, 0),
		        COALESCE(e.display_currency, 'USD'), COALESCE(e.exchange_rate, 1.0),
		        COALESCE(e.country, ''), COALESCE(e.tax_number, ''),
		        (SELECT COUNT(*) FROM users WHERE enterprise_id = e.id AND deleted_at IS NULL) as member_count,
		        e.created_at
		 FROM enterprises e WHERE e.deleted_at IS NULL ORDER BY e.created_at DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list enterprises"}})
		return
	}
	defer rows.Close()

	enterprises := []gin.H{}
	for rows.Next() {
		var id, name, contactEmail, status, displayCurrency, country, taxNumber string
		var quotaTotal, quotaUsed int64
		var exchangeRate float64
		var memberCount int
		var createdAt interface{}
		if err := rows.Scan(&id, &name, &contactEmail, &status, &quotaTotal, &quotaUsed, &displayCurrency, &exchangeRate, &country, &taxNumber, &memberCount, &createdAt); err != nil {
			continue
		}
		enterprises = append(enterprises, gin.H{
			"id": id, "name": name, "contact_email": contactEmail, "status": status,
			"quota_total": quotaTotal, "quota_used": quotaUsed,
			"display_currency": displayCurrency, "exchange_rate": exchangeRate,
			"country": country, "tax_number": taxNumber,
			"member_count": memberCount, "created_at": createdAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"enterprises": enterprises})
}

// createEnterprise creates a new enterprise
func (r *EnterpriseRoutes) createEnterprise(c *gin.Context) {
	var req struct {
		Name            string  `json:"name" binding:"required"`
		ContactEmail    string  `json:"contact_email"`
		QuotaTotal      int64   `json:"quota_total"`
		CreditTotal     int64   `json:"credit_total"`
		MaxMembers      int     `json:"max_members"`
		MaxAdmins       int     `json:"max_admins"`
		AllowedDomains  string  `json:"allowed_domains"`
		BillingCycle    string  `json:"billing_cycle"`
		PricingRatio    float64 `json:"pricing_ratio"`
		DisplayCurrency string  `json:"display_currency"`
		ExchangeRate    float64 `json:"exchange_rate"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Name is required"}})
		return
	}

	if req.MaxMembers == 0 {
		req.MaxMembers = 50
	}
	if req.MaxAdmins == 0 {
		req.MaxAdmins = 3
	}
	if req.BillingCycle == "" {
		req.BillingCycle = "monthly"
	}
	if req.PricingRatio == 0 {
		req.PricingRatio = 1.0
	}
	if req.DisplayCurrency == "" {
		req.DisplayCurrency = "USD"
	}
	if req.ExchangeRate <= 0 {
		req.ExchangeRate = 1.0
	}

	ctx := c.Request.Context()

	// Check for duplicate name or contact_email
	var dupCount int
	_ = r.db.QueryRow(ctx, `SELECT COUNT(*) FROM enterprises WHERE name = $1 AND deleted_at IS NULL`, req.Name).Scan(&dupCount)
	if dupCount > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "Enterprise name already exists"}})
		return
	}
	if req.ContactEmail != "" {
		_ = r.db.QueryRow(ctx, `SELECT COUNT(*) FROM enterprises WHERE contact_email = $1 AND deleted_at IS NULL`, req.ContactEmail).Scan(&dupCount)
		if dupCount > 0 {
			c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "Contact email already used by another enterprise"}})
			return
		}
	}

	var id string
	err := r.db.QueryRow(ctx,
		`INSERT INTO enterprises (name, contact_email, quota_total, credit_total, max_members, max_admins, allowed_domains, billing_cycle, pricing_ratio, display_currency, exchange_rate, status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 'active') RETURNING id::text`,
		req.Name, req.ContactEmail, req.QuotaTotal, req.CreditTotal, req.MaxMembers, req.MaxAdmins, req.AllowedDomains, req.BillingCycle, req.PricingRatio, req.DisplayCurrency, req.ExchangeRate,
	).Scan(&id)
	if err != nil {
		r.logger.Error("Failed to create enterprise", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to create enterprise"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "enterprise": gin.H{"id": id, "name": req.Name}})
}

// getEnterprise returns enterprise details
func (r *EnterpriseRoutes) getEnterprise(c *gin.Context) {
	enterpriseID := c.Param("id")

	var id, name, contactEmail, status, billingCycle, displayCurrency string
	var quotaTotal, quotaUsed, creditTotal, creditUsed, prepaidBalance int64
	var pricingRatio, exchangeRate float64
	var maxMembers, maxAdmins int
	var allowedDomains string
	var memberCount int
	var createdAt, verifiedAt interface{}
	var legalName, country, taxNumber, contactName, contactPhone string
	var registeredAddress, bankName, bankAccount string

	err := r.db.QueryRow(c.Request.Context(),
		`SELECT e.id::text, e.name, COALESCE(e.contact_email, ''), e.status,
		        COALESCE(e.quota_total, 0), COALESCE(e.quota_used, 0),
		        COALESCE(e.credit_total, 0), COALESCE(e.credit_used, 0), COALESCE(e.prepaid_balance, 0),
		        COALESCE(e.pricing_ratio, 1.0),
		        COALESCE(e.max_members, 50), COALESCE(e.max_admins, 3), COALESCE(e.allowed_domains, ''),
		        COALESCE(e.billing_cycle, 'monthly'),
		        COALESCE(e.display_currency, 'USD'), COALESCE(e.exchange_rate, 1.0),
		        (SELECT COUNT(*) FROM users WHERE enterprise_id = e.id AND deleted_at IS NULL),
		        e.created_at, e.verified_at,
		        COALESCE(e.legal_name, ''), COALESCE(e.country, ''), COALESCE(e.tax_number, ''),
		        COALESCE(e.contact_name, ''), COALESCE(e.contact_phone, ''),
		        COALESCE(e.registered_address, ''), COALESCE(e.bank_name, ''), COALESCE(e.bank_account, '')
		 FROM enterprises e WHERE e.id = $1::uuid`, enterpriseID,
	).Scan(&id, &name, &contactEmail, &status, &quotaTotal, &quotaUsed, &creditTotal, &creditUsed, &prepaidBalance, &pricingRatio, &maxMembers, &maxAdmins, &allowedDomains, &billingCycle, &displayCurrency, &exchangeRate, &memberCount, &createdAt, &verifiedAt,
		&legalName, &country, &taxNumber, &contactName, &contactPhone, &registeredAddress, &bankName, &bankAccount)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Enterprise not found"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"enterprise": gin.H{
			"id": id, "name": name, "contact_email": contactEmail, "status": status,
			"quota_total": quotaTotal, "quota_used": quotaUsed,
			"credit_total": creditTotal, "credit_used": creditUsed,
			"prepaid_balance": prepaidBalance,
			"pricing_ratio":   pricingRatio,
			"max_members":     maxMembers, "max_admins": maxAdmins, "allowed_domains": allowedDomains,
			"billing_cycle":    billingCycle,
			"display_currency": displayCurrency, "exchange_rate": exchangeRate,
			"member_count": memberCount, "created_at": createdAt,
			"verified_at": verifiedAt,
			"legal_name":  legalName, "country": country, "tax_number": taxNumber,
			"contact_name": contactName, "contact_phone": contactPhone,
			"registered_address": registeredAddress, "bank_name": bankName, "bank_account": bankAccount,
		},
	})
}

// updateEnterprise updates enterprise details
func (r *EnterpriseRoutes) updateEnterprise(c *gin.Context) {
	enterpriseID := c.Param("id")
	var req struct {
		Name            *string  `json:"name"`
		ContactEmail    *string  `json:"contact_email"`
		QuotaTotal      *int64   `json:"quota_total"`
		CreditTotal     *int64   `json:"credit_total"`
		PrepaidBalance  *int64   `json:"prepaid_balance"`
		PricingRatio    *float64 `json:"pricing_ratio"`
		MaxMembers      *int     `json:"max_members"`
		MaxAdmins       *int     `json:"max_admins"`
		AllowedDomains  *string  `json:"allowed_domains"`
		BillingCycle    *string  `json:"billing_cycle"`
		DisplayCurrency *string  `json:"display_currency"`
		ExchangeRate    *float64 `json:"exchange_rate"`
		Country         *string  `json:"country"`    // billing/GST jurisdiction (enterprise/settings mirror)
		TaxNumber       *string  `json:"tax_number"` // tax id shown on invoices
		// Registration info (mirrors enterprise/settings + application data)
		LegalName         *string `json:"legal_name"`
		ContactName       *string `json:"contact_name"`
		ContactPhone      *string `json:"contact_phone"`
		RegisteredAddress *string `json:"registered_address"`
		BankName          *string `json:"bank_name"`
		BankAccount       *string `json:"bank_account"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	// prepaid_balance and credit_total are money the customer paid for. Editing
	// them straight from the form left no ledger row (this is why Intel's wallet
	// had no audit trail). They now move only through the recharge flow
	// (quota/credit/prepaid top-up or record_only), which writes a tamper-proof
	// enterprise_audit_log entry. Reject a direct edit instead of silently
	// ignoring it, so an admin is told where to go.
	if req.PrepaidBalance != nil || req.CreditTotal != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "prepaid_balance and credit_total cannot be edited directly; use the Recharge action (or record_only to book a deposit that is already in the wallet)"}})
		return
	}

	ctx := c.Request.Context()
	var setClauses []string
	var args []interface{}
	argIdx := 1

	if req.Name != nil {
		var dupCount int
		_ = r.db.QueryRow(ctx, `SELECT COUNT(*) FROM enterprises WHERE name = $1 AND id != $2::uuid AND deleted_at IS NULL`, *req.Name, enterpriseID).Scan(&dupCount)
		if dupCount > 0 {
			c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "Enterprise name already exists"}})
			return
		}
		setClauses = append(setClauses, fmt.Sprintf("name = $%d", argIdx))
		args = append(args, *req.Name)
		argIdx++
	}
	if req.ContactEmail != nil && *req.ContactEmail != "" {
		var dupCount int
		_ = r.db.QueryRow(ctx, `SELECT COUNT(*) FROM enterprises WHERE contact_email = $1 AND id != $2::uuid AND deleted_at IS NULL`, *req.ContactEmail, enterpriseID).Scan(&dupCount)
		if dupCount > 0 {
			c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "Contact email already used by another enterprise"}})
			return
		}
		setClauses = append(setClauses, fmt.Sprintf("contact_email = $%d", argIdx))
		args = append(args, *req.ContactEmail)
		argIdx++
	}
	if req.QuotaTotal != nil {
		setClauses = append(setClauses, fmt.Sprintf("quota_total = $%d", argIdx))
		args = append(args, *req.QuotaTotal)
		argIdx++
	}
	if req.MaxMembers != nil {
		setClauses = append(setClauses, fmt.Sprintf("max_members = $%d", argIdx))
		args = append(args, *req.MaxMembers)
		argIdx++
	}
	if req.MaxAdmins != nil {
		setClauses = append(setClauses, fmt.Sprintf("max_admins = $%d", argIdx))
		args = append(args, *req.MaxAdmins)
		argIdx++
	}
	if req.AllowedDomains != nil {
		setClauses = append(setClauses, fmt.Sprintf("allowed_domains = $%d", argIdx))
		args = append(args, *req.AllowedDomains)
		argIdx++
	}
	if req.BillingCycle != nil {
		setClauses = append(setClauses, fmt.Sprintf("billing_cycle = $%d", argIdx))
		args = append(args, *req.BillingCycle)
		argIdx++
	}
	if req.PricingRatio != nil {
		setClauses = append(setClauses, fmt.Sprintf("pricing_ratio = $%d", argIdx))
		args = append(args, *req.PricingRatio)
		argIdx++
	}
	if req.DisplayCurrency != nil && *req.DisplayCurrency != "" {
		setClauses = append(setClauses, fmt.Sprintf("display_currency = $%d", argIdx))
		args = append(args, *req.DisplayCurrency)
		argIdx++
	}
	if req.ExchangeRate != nil && *req.ExchangeRate > 0 {
		setClauses = append(setClauses, fmt.Sprintf("exchange_rate = $%d", argIdx))
		args = append(args, *req.ExchangeRate)
		argIdx++
	}
	if req.Country != nil {
		setClauses = append(setClauses, fmt.Sprintf("country = $%d", argIdx))
		args = append(args, *req.Country)
		argIdx++
	}
	if req.TaxNumber != nil {
		setClauses = append(setClauses, fmt.Sprintf("tax_number = $%d", argIdx))
		args = append(args, *req.TaxNumber)
		argIdx++
	}
	if req.LegalName != nil {
		setClauses = append(setClauses, fmt.Sprintf("legal_name = $%d", argIdx))
		args = append(args, *req.LegalName)
		argIdx++
	}
	if req.ContactName != nil {
		setClauses = append(setClauses, fmt.Sprintf("contact_name = $%d", argIdx))
		args = append(args, *req.ContactName)
		argIdx++
	}
	if req.ContactPhone != nil {
		setClauses = append(setClauses, fmt.Sprintf("contact_phone = $%d", argIdx))
		args = append(args, *req.ContactPhone)
		argIdx++
	}
	if req.RegisteredAddress != nil {
		setClauses = append(setClauses, fmt.Sprintf("registered_address = $%d", argIdx))
		args = append(args, *req.RegisteredAddress)
		argIdx++
	}
	if req.BankName != nil {
		setClauses = append(setClauses, fmt.Sprintf("bank_name = $%d", argIdx))
		args = append(args, *req.BankName)
		argIdx++
	}
	if req.BankAccount != nil {
		setClauses = append(setClauses, fmt.Sprintf("bank_account = $%d", argIdx))
		args = append(args, *req.BankAccount)
		argIdx++
	}

	if len(setClauses) > 0 {
		setClauses = append(setClauses, "updated_at = NOW()")
		sql := fmt.Sprintf("UPDATE enterprises SET %s WHERE id = $%d::uuid",
			strings.Join(setClauses, ", "), argIdx)
		args = append(args, enterpriseID)
		if err := r.db.Exec(ctx, sql, args...); err != nil {
			r.logger.Error("Failed to update enterprise", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update enterprise"}})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// suspendEnterprise suspends an enterprise and, in the same transaction,
// disables all active enterprise keys (same cascade pattern as group disable).
// Reactivating the enterprise does NOT restore the keys automatically.
func (r *EnterpriseRoutes) suspendEnterprise(c *gin.Context) {
	enterpriseID := c.Param("id")
	var disabledKeyHashes []string
	err := r.db.WithTx(c.Request.Context(), func(tx pgx.Tx) error {
		if _, txErr := tx.Exec(c.Request.Context(),
			`UPDATE enterprises SET status = 'suspended', updated_at = NOW() WHERE id = $1::uuid`,
			enterpriseID); txErr != nil {
			return txErr
		}
		hashRows, txErr := tx.Query(c.Request.Context(),
			`SELECT key_hash FROM api_keys WHERE enterprise_id = $1::uuid AND deleted_at IS NULL AND status = 'active'`,
			enterpriseID)
		if txErr != nil {
			return txErr
		}
		for hashRows.Next() {
			var h string
			if scanErr := hashRows.Scan(&h); scanErr == nil {
				disabledKeyHashes = append(disabledKeyHashes, h)
			}
		}
		hashRows.Close()
		if _, txErr := tx.Exec(c.Request.Context(),
			`UPDATE api_keys SET status = 'disabled', updated_at = NOW()
			 WHERE enterprise_id = $1::uuid AND deleted_at IS NULL AND status = 'active'`,
			enterpriseID); txErr != nil {
			return txErr
		}
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to suspend enterprise"}})
		return
	}
	for _, h := range disabledKeyHashes {
		invalidateValidateCache(c.Request.Context(), r.db, r.logger, h)
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// activateEnterprise activates an enterprise
func (r *EnterpriseRoutes) activateEnterprise(c *gin.Context) {
	enterpriseID := c.Param("id")
	err := r.db.Exec(c.Request.Context(),
		`UPDATE enterprises SET status = 'active', updated_at = NOW() WHERE id = $1::uuid`,
		enterpriseID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to activate enterprise"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// verifyEnterprise marks an enterprise as verified (system admin action).
// Enterprises created directly by an admin never got verified_at; this is the manual path.
func (r *EnterpriseRoutes) verifyEnterprise(c *gin.Context) {
	enterpriseID := c.Param("id")
	adminUserID := c.GetString("user_id")
	ctx := c.Request.Context()

	var verifiedAt interface{}
	err := r.db.QueryRow(ctx,
		`UPDATE enterprises SET verified_at = NOW(), updated_at = NOW()
		 WHERE id = $1::uuid AND deleted_at IS NULL AND verified_at IS NULL
		 RETURNING verified_at`,
		enterpriseID).Scan(&verifiedAt)
	if err == pgx.ErrNoRows {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Enterprise not found or already verified"}})
		return
	}
	if err != nil {
		r.logger.Error("Failed to verify enterprise", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to verify enterprise"}})
		return
	}

	if r.audit != nil {
		r.audit.Log(ctx, adminUserID, c.GetString("email"), "admin", "enterprise_verified", "info",
			fmt.Sprintf("Enterprise %s marked as verified", enterpriseID))
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// deleteEnterprise soft-deletes an enterprise (marks as deleted, preserves data for audit)
func (r *EnterpriseRoutes) deleteEnterprise(c *gin.Context) {
	enterpriseID := c.Param("id")
	currentUserID := c.GetString("user_id")

	if err := r.db.WithTx(c.Request.Context(), func(tx pgx.Tx) error {
		// Soft-remove all members
		if _, err := tx.Exec(c.Request.Context(),
			`UPDATE enterprise_members SET removed_at = NOW(), removed_by = $1::uuid
			 WHERE enterprise_id = $2::uuid AND removed_at IS NULL`,
			currentUserID, enterpriseID); err != nil {
			return err
		}
		// Clear enterprise association from users
		if _, err := tx.Exec(c.Request.Context(),
			`UPDATE users SET enterprise_id = NULL, role = 'user', updated_at = NOW()
			 WHERE enterprise_id = $1::uuid`,
			enterpriseID); err != nil {
			return err
		}
		// Disable all enterprise keys
		if _, err := tx.Exec(c.Request.Context(),
			`UPDATE api_keys SET status = 'disabled', assigned_user_id = NULL, updated_at = NOW()
			 WHERE enterprise_id = $1::uuid`,
			enterpriseID); err != nil {
			return err
		}
		// Cancel all pending invitations
		if _, err := tx.Exec(c.Request.Context(),
			`UPDATE enterprise_invitations SET status = 'cancelled', updated_at = NOW()
			 WHERE enterprise_id = $1::uuid AND status = 'pending'`,
			enterpriseID); err != nil {
			return err
		}
		// Soft delete the enterprise
		if _, err := tx.Exec(c.Request.Context(),
			`UPDATE enterprises SET deleted_at = NOW(), deleted_by = $1::uuid, updated_at = NOW()
			 WHERE id = $2::uuid AND deleted_at IS NULL`,
			currentUserID, enterpriseID); err != nil {
			return err
		}
		return nil
	}); err != nil {
		r.logger.Error("Failed to delete enterprise", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to delete enterprise"}})
		return
	}

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), currentUserID, c.GetString("email"), "enterprise", "enterprise_deleted", "warning",
			fmt.Sprintf("Enterprise soft-deleted: %s", enterpriseID))
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// restoreEnterprise restores a soft-deleted enterprise
func (r *EnterpriseRoutes) restoreEnterprise(c *gin.Context) {
	enterpriseID := c.Param("id")
	currentUserID := c.GetString("user_id")

	err := r.db.Exec(c.Request.Context(),
		`UPDATE enterprises SET status = 'active', deleted_at = NULL, deleted_by = NULL, delete_reason = NULL, updated_at = NOW()
		 WHERE id = $1::uuid AND deleted_at IS NOT NULL`,
		enterpriseID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to restore enterprise"}})
		return
	}

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), currentUserID, c.GetString("email"), "enterprise", "enterprise_restored", "info",
			fmt.Sprintf("Enterprise restored: %s", enterpriseID))
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}
func (r *EnterpriseRoutes) CheckAdminVacantEnterprises(ctx context.Context) {
	rows, err := r.db.Query(ctx, `
		SELECT e.id::text, e.name, e.admin_vacant_since,
		       (SELECT COUNT(*) FROM enterprise_members em WHERE em.enterprise_id = e.id AND em.removed_at IS NULL)
		FROM enterprises e
		WHERE e.status = 'admin_vacant' AND e.deleted_at IS NULL`)
	if err != nil {
		r.logger.Error("Failed to query admin_vacant enterprises", zap.Error(err))
		return
	}
	defer rows.Close()

	type vacantEnterprise struct {
		id            string
		name          string
		vacantSince   time.Time
		activeMembers int
	}
	var enterprises []vacantEnterprise

	for rows.Next() {
		var v vacantEnterprise
		if err := rows.Scan(&v.id, &v.name, &v.vacantSince, &v.activeMembers); err != nil {
			continue
		}
		enterprises = append(enterprises, v)
	}

	if len(enterprises) == 0 {
		return
	}

	// Build summary
	var lines []string
	for _, e := range enterprises {
		days := int(time.Since(e.vacantSince).Hours() / 24)
		lines = append(lines, fmt.Sprintf("- %s (vacant for %d days, %d members affected)", e.name, days, e.activeMembers))
	}

	summary := fmt.Sprintf("The following enterprises have no admin:\n\n%s\n\nPlease appoint an admin at %s/admin/enterprises",
		strings.Join(lines, "\n"), r.cfg.App.BaseURL)

	// Send to all super_admin and system_admin users
	if r.mail != nil && r.mail.IsConfigured() {
		adminRows, err := r.db.Query(ctx, `
			SELECT email FROM users WHERE role IN ('super_admin', 'system_admin') AND deleted_at IS NULL`)
		if err != nil {
			r.logger.Error("Failed to query system admins", zap.Error(err))
		} else {
			defer adminRows.Close()
			for adminRows.Next() {
				var email string
				if err := adminRows.Scan(&email); err != nil {
					continue
				}
				go func(to string) {
					if err := r.mail.SendAdminVacantReminder(to, summary, r.cfg.App.BaseURL, "zh"); err != nil {
						r.logger.Warn("Failed to send admin_vacant reminder", zap.String("to", to), zap.Error(err))
					}
				}(email)
			}
		}
	}

	// Auto-suspend after 7 days
	for _, e := range enterprises {
		if time.Since(e.vacantSince) > 7*24*time.Hour {
			err := r.db.Exec(ctx, `UPDATE enterprises SET status = 'suspended' WHERE id = $1::uuid AND status = 'admin_vacant'`, e.id)
			if err != nil {
				r.logger.Error("Failed to auto-suspend enterprise", zap.String("id", e.id), zap.Error(err))
			} else {
				r.logger.Info("Auto-suspended enterprise (admin_vacant > 7 days)", zap.String("id", e.id), zap.String("name", e.name))
			}
		}
	}

	r.logger.Info("Admin vacant check completed", zap.Int("vacant_enterprises", len(enterprises)))
}

// listEnterpriseAuditLog returns audit log for the enterprise
func (r *EnterpriseRoutes) listEnterpriseAuditLog(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	categoryFilter := c.DefaultQuery("category", "")
	validCategories := map[string]bool{"billing": true, "member": true, "key": true, "group": true, "settings": true, "alert": true, "role": true}
	if !validCategories[categoryFilter] {
		categoryFilter = ""
	}

	// Role change log
	logs := []gin.H{}
	if categoryFilter == "" || categoryFilter == "role" {
		rows, err := r.db.Query(c.Request.Context(), `
			SELECT r.id::text, r.user_id::text, r.old_role, r.new_role, r.changed_by::text, r.created_at as changed_at, r.reason,
			       COALESCE(cb.email, '') as changed_by_email
			FROM enterprise_member_role_log r
			LEFT JOIN users cb ON r.changed_by = cb.id
			WHERE r.enterprise_id = $1::uuid
			ORDER BY r.created_at DESC LIMIT 50`,
			enterpriseID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list audit log"}})
			return
		}
		defer rows.Close()

		for rows.Next() {
			var id, userID, oldRole, newRole, changedBy, reason, changedByEmail string
			var changedAt interface{}
			if err := rows.Scan(&id, &userID, &oldRole, &newRole, &changedBy, &changedAt, &reason, &changedByEmail); err != nil {
				continue
			}
			logs = append(logs, gin.H{
				"id": id, "user_id": userID, "old_role": oldRole, "new_role": newRole,
				"changed_by": changedBy, "changed_by_email": changedByEmail,
				"changed_at": changedAt, "reason": reason,
			})
		}
	}

	// Unified enterprise audit log
	creditLogs := []gin.H{}
	query := `
		SELECT cl.id::text, cl.category, cl.change_type, cl.field_name, cl.old_value, cl.new_value, cl.delta,
		       COALESCE(cl.reason, ''), cl.created_at, COALESCE(operator.email, 'system'),
		       COALESCE(member.email, ''), COALESCE(member.name, ''), cl.user_id::text
		FROM enterprise_audit_log cl
		LEFT JOIN users operator ON cl.changed_by = operator.id
		LEFT JOIN users member ON cl.user_id = member.id
		WHERE cl.enterprise_id = $1::uuid`
	args := []interface{}{enterpriseID}
	if categoryFilter != "" {
		query += ` AND cl.category = $2`
		args = append(args, categoryFilter)
	}
	query += ` ORDER BY cl.created_at DESC LIMIT 50`

	creditRows, err := r.db.Query(c.Request.Context(), query, args...)
	if err == nil {
		defer creditRows.Close()
		for creditRows.Next() {
			var id, category, changeType, fieldName, reason, changedBy string
			var memberEmail, memberName, memberUserID string
			var oldVal, newVal, delta *int64
			var createdAt interface{}
			if err := creditRows.Scan(&id, &category, &changeType, &fieldName, &oldVal, &newVal, &delta, &reason, &createdAt, &changedBy, &memberEmail, &memberName, &memberUserID); err == nil {
				creditLogs = append(creditLogs, gin.H{
					"id": id, "category": category, "change_type": changeType, "field_name": fieldName,
					"old_value": oldVal, "new_value": newVal, "delta": delta,
					"reason": reason, "created_at": createdAt, "changed_by": changedBy,
					"user_id": memberUserID, "user_email": memberEmail, "user_name": memberName,
				})
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"audit_log": logs, "credit_log": creditLogs, "category": categoryFilter})
}

// exportAuditCSV exports audit logs as CSV
func (r *EnterpriseRoutes) exportAuditCSV(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	logType := c.DefaultQuery("type", "roles")
	categoryFilter := c.DefaultQuery("category", "")
	validCategories := map[string]bool{"billing": true, "member": true, "key": true, "group": true, "settings": true, "alert": true, "role": true}
	if !validCategories[categoryFilter] {
		categoryFilter = ""
	}
	fromDate := strings.TrimSpace(c.Query("from"))
	toDate := strings.TrimSpace(c.Query("to"))
	operatorQuery := strings.TrimSpace(c.Query("operator"))
	memberQuery := strings.TrimSpace(c.Query("member"))
	searchQuery := strings.TrimSpace(c.Query("q"))

	c.Header("Content-Type", "text/csv; charset=utf-8")

	switch logType {
	case "credits":
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=enterprise-audit-log-%s.csv", time.Now().UTC().Format("200601021504")))
		_, _ = c.Writer.WriteString("Time,Category,Type,Member,Field,Delta,Operator,Reason\n")

		query := `
			SELECT cl.created_at, cl.category, cl.change_type, COALESCE(member.email, ''),
			       cl.field_name, COALESCE(cl.delta, 0), cl.old_value, cl.new_value,
			       COALESCE(operator.email, 'system'), COALESCE(cl.reason, '')
			FROM enterprise_audit_log cl
			LEFT JOIN users operator ON cl.changed_by = operator.id
			LEFT JOIN users member ON cl.user_id = member.id
			WHERE cl.enterprise_id = $1::uuid`
		args := []interface{}{enterpriseID}
		argIdx := 1
		if categoryFilter != "" {
			argIdx++
			query += fmt.Sprintf(` AND cl.category = $%d`, argIdx)
			args = append(args, categoryFilter)
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
		if operatorQuery != "" {
			argIdx++
			query += fmt.Sprintf(` AND (operator.email ILIKE $%d OR operator.name ILIKE $%d)`, argIdx, argIdx)
			args = append(args, "%"+operatorQuery+"%")
		}
		if memberQuery != "" {
			argIdx++
			query += fmt.Sprintf(` AND (member.email ILIKE $%d OR member.name ILIKE $%d)`, argIdx, argIdx)
			args = append(args, "%"+memberQuery+"%")
		}
		if searchQuery != "" {
			argIdx++
			query += fmt.Sprintf(` AND (operator.email ILIKE $%[1]d OR operator.name ILIKE $%[1]d OR member.email ILIKE $%[1]d OR member.name ILIKE $%[1]d)`, argIdx)
			args = append(args, "%"+searchQuery+"%")
		}
		query += ` ORDER BY cl.created_at DESC LIMIT 500`

		rows, err := r.db.Query(c.Request.Context(), query, args...)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Export failed"}})
			return
		}
		defer rows.Close()

		for rows.Next() {
			var createdAt interface{}
			var category, changeType, memberEmail, fieldName, reason, changedBy string
			var delta int64
			var oldVal, newVal sql.NullInt64
			if err := rows.Scan(&createdAt, &category, &changeType, &memberEmail, &fieldName, &delta, &oldVal, &newVal, &changedBy, &reason); err == nil {
				// Role rows store member=0/admin=1; emit the transition instead
				// of a meaningless 0 delta.
				deltaStr := fmt.Sprintf("%d", delta)
				if category == "role" {
					roleName := func(v sql.NullInt64) string {
						if v.Valid && v.Int64 == 1 {
							return "admin"
						}
						return "member"
					}
					deltaStr = roleName(oldVal) + "->" + roleName(newVal)
				}
				_, _ = fmt.Fprintf(c.Writer, "%v,%s,%s,%s,%s,%s,%s,%s\n",
					createdAt, category, changeType, strings.ReplaceAll(memberEmail, ",", ";"), fieldName, deltaStr, changedBy, strings.ReplaceAll(reason, ",", ";"))
			}
		}

	default: // roles
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=enterprise-role-log-%s.csv", time.Now().UTC().Format("200601021504")))
		_, _ = c.Writer.WriteString("Time,Member,Old Role,New Role,Operator,Reason\n")

		rows, err := r.db.Query(c.Request.Context(), `
			SELECT r.created_at as changed_at, COALESCE(u.name, u.email, ''), r.old_role, r.new_role,
			       COALESCE(cb.email, ''), COALESCE(r.reason, '')
			FROM enterprise_member_role_log r
			LEFT JOIN users u ON r.user_id = u.id
			LEFT JOIN users cb ON r.changed_by = cb.id
			WHERE r.enterprise_id = $1::uuid
			ORDER BY r.created_at DESC LIMIT 500`,
			enterpriseID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Export failed"}})
			return
		}
		defer rows.Close()

		for rows.Next() {
			var changedAt interface{}
			var userName, oldRole, newRole, changedBy, reason string
			if err := rows.Scan(&changedAt, &userName, &oldRole, &newRole, &changedBy, &reason); err == nil {
				_, _ = fmt.Fprintf(c.Writer, "%v,%s,%s,%s,%s,%s\n",
					changedAt, strings.ReplaceAll(userName, ",", ";"), oldRole, newRole, changedBy, strings.ReplaceAll(reason, ",", ";"))
			}
		}
	}
}

// submitEnterpriseChangeRequest allows enterprise admin to request info changes.
// For verified enterprises, direct edits are disabled; changes go through this request queue.
func (r *EnterpriseRoutes) submitEnterpriseChangeRequest(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	userID := c.GetString("user_id")
	ctx := c.Request.Context()

	var req struct {
		LegalName         string `json:"legal_name"`
		Name              string `json:"name"`
		Country           string `json:"country"`
		TaxNumber         string `json:"tax_number"`
		RegisteredAddress string `json:"registered_address"`
		BankName          string `json:"bank_name"`
		BankAccount       string `json:"bank_account"`
		ContactEmail      string `json:"contact_email"`
		ContactName       string `json:"contact_name"`
		ContactPhone      string `json:"contact_phone"`
		Reason            string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	// Build list of changed fields by comparing to current values
	type fieldChange struct {
		field, col, newVal string
	}
	fields := []fieldChange{
		{"legal_name", "legal_name", req.LegalName},
		{"name", "name", req.Name},
		{"country", "country", req.Country},
		{"tax_number", "tax_number", req.TaxNumber},
		{"registered_address", "registered_address", req.RegisteredAddress},
		{"bank_name", "bank_name", req.BankName},
		{"bank_account", "bank_account", req.BankAccount},
		{"contact_email", "contact_email", req.ContactEmail},
		{"contact_name", "contact_name", req.ContactName},
		{"contact_phone", "contact_phone", req.ContactPhone},
	}

	inserted := 0
	for _, f := range fields {
		var oldValue string
		_ = r.db.QueryRow(ctx, fmt.Sprintf(`SELECT COALESCE(%s, '') FROM enterprises WHERE id = $1::uuid`, f.col), enterpriseID).Scan(&oldValue)
		if oldValue == f.newVal {
			continue // No change
		}
		_ = r.db.Exec(ctx, `
			INSERT INTO enterprise_change_requests
			(enterprise_id, requested_by, field, old_value, new_value, reason, status, created_at, updated_at)
			VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, 'pending', NOW(), NOW())`,
			enterpriseID, userID, f.field, oldValue, f.newVal, req.Reason)
		inserted++

		fieldName := f.field
		newValue := f.newVal
		r.logEnterpriseAudit(ctx, enterpriseAuditEntry{
			EnterpriseID: enterpriseID,
			Category:     "settings",
			ChangeType:   "change_request_submitted",
			FieldName:    fieldName,
			ChangedBy:    userID,
			OldValueStr:  &oldValue,
			NewValueStr:  &newValue,
			Reason:       req.Reason,
		})
	}

	if inserted == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "No changes detected"}})
		return
	}

	if r.audit != nil {
		r.audit.Log(ctx, userID, c.GetString("email"), "enterprise", "info_change_requested", "warning",
			fmt.Sprintf("Enterprise %s: %d field change(s) requested", enterpriseID, inserted))
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "changes": inserted})
}

// listEnterpriseChangeRequests lists pending enterprise info change requests for system admin review.
func (r *EnterpriseRoutes) listEnterpriseChangeRequests(c *gin.Context) {
	ctx := c.Request.Context()
	status := c.DefaultQuery("status", "pending")

	rows, err := r.db.Query(ctx, `
		SELECT cr.id::text, cr.enterprise_id::text, e.name as enterprise_name, cr.requested_by::text,
		       COALESCE(u.email, '') as requester_email, cr.field, cr.old_value, cr.new_value,
		       cr.reason, cr.status, cr.created_at::text
		FROM enterprise_change_requests cr
		JOIN enterprises e ON cr.enterprise_id = e.id
		JOIN users u ON cr.requested_by = u.id
		WHERE cr.status = $1
		ORDER BY cr.created_at DESC`, status)
	if err != nil {
		r.logger.Error("Failed to list change requests", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list change requests"}})
		return
	}
	defer rows.Close()

	var requests []gin.H
	for rows.Next() {
		var id, entID, entName, reqBy, reqEmail, field, oldValue, newValue, reason, stat, createdAt string
		if err := rows.Scan(&id, &entID, &entName, &reqBy, &reqEmail, &field, &oldValue, &newValue, &reason, &stat, &createdAt); err != nil {
			continue
		}
		requests = append(requests, gin.H{
			"id": id, "enterprise_id": entID, "enterprise_name": entName,
			"requested_by": reqBy, "requester_email": reqEmail,
			"field": field, "old_value": oldValue, "new_value": newValue,
			"reason": reason, "status": stat, "created_at": createdAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"requests": requests})
}

// reviewEnterpriseChangeRequest approves or rejects a change request.
func (r *EnterpriseRoutes) reviewEnterpriseChangeRequest(c *gin.Context) {
	reqID := c.Param("id")
	adminUserID := c.GetString("user_id")
	ctx := c.Request.Context()

	var req struct {
		Action string `json:"action"` // "approve" or "reject"
	}
	if err := c.ShouldBindJSON(&req); err != nil || (req.Action != "approve" && req.Action != "reject") {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "action must be approve or reject"}})
		return
	}

	var cr struct {
		EnterpriseID string
		Field        string
		OldValue     string
		NewValue     string
	}
	err := r.db.QueryRow(ctx, `
		SELECT enterprise_id::text, field, old_value, new_value
		FROM enterprise_change_requests
		WHERE id = $1::uuid AND status = 'pending'`, reqID).Scan(&cr.EnterpriseID, &cr.Field, &cr.OldValue, &cr.NewValue)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Change request not found or already reviewed"}})
		return
	}

	newStatus := "rejected"
	if req.Action == "approve" {
		newStatus = "approved"
		colMap := map[string]string{
			"legal_name":         "legal_name",
			"name":               "name",
			"country":            "country",
			"tax_number":         "tax_number",
			"registered_address": "registered_address",
			"bank_name":          "bank_name",
			"bank_account":       "bank_account",
			"contact_email":      "contact_email",
			"contact_name":       "contact_name",
			"contact_phone":      "contact_phone",
		}
		col, ok := colMap[cr.Field]
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Unsupported change field"}})
			return
		}
		if err := r.db.Exec(ctx,
			fmt.Sprintf(`UPDATE enterprises SET %s = $1, updated_at = NOW() WHERE id = $2::uuid`, col),
			cr.NewValue, cr.EnterpriseID); err != nil {
			r.logger.Error("Failed to apply approved change", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to apply change"}})
			return
		}
	}

	if err := r.db.Exec(ctx, `
		UPDATE enterprise_change_requests
		SET status = $1, reviewed_by = $2::uuid, reviewed_at = NOW(), updated_at = NOW()
		WHERE id = $3::uuid`, newStatus, adminUserID, reqID); err != nil {
		r.logger.Error("Failed to update change request status", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update request status"}})
		return
	}

	if r.audit != nil {
		r.audit.Log(ctx, adminUserID, c.GetString("email"), "admin", fmt.Sprintf("enterprise_change_request_%s", req.Action), "info",
			fmt.Sprintf("Change request %s %s for enterprise %s", reqID, req.Action, cr.EnterpriseID))
	}

	changeType := "change_request_" + req.Action
	r.logEnterpriseAudit(ctx, enterpriseAuditEntry{
		EnterpriseID: cr.EnterpriseID,
		Category:     "settings",
		ChangeType:   changeType,
		FieldName:    cr.Field,
		ChangedBy:    adminUserID,
		OldValueStr:  &cr.OldValue,
		NewValueStr:  &cr.NewValue,
		Reason:       fmt.Sprintf("Change request %s %s", reqID, req.Action),
	})

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// listMyChangeRequests returns change requests for the current enterprise
func (r *EnterpriseRoutes) listMyChangeRequests(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT field, new_value, status, created_at::text
		 FROM enterprise_change_requests
		 WHERE enterprise_id = $1::uuid
		 ORDER BY created_at DESC LIMIT 20`, enterpriseID)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"requests": []interface{}{}})
		return
	}
	defer rows.Close()
	type row struct {
		Field     string `json:"field"`
		NewValue  string `json:"new_value"`
		Status    string `json:"status"`
		CreatedAt string `json:"created_at"`
	}
	var requests []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.Field, &r.NewValue, &r.Status, &r.CreatedAt); err == nil {
			requests = append(requests, r)
		}
	}
	c.JSON(http.StatusOK, gin.H{"requests": requests})
}

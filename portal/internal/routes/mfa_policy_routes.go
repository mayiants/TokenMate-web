package routes

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/ai-proxy/ai-portal-go/internal/middleware"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ---- enterprise policy: PUT /api/enterprise/security ----------------------

// RegisterSecurity wires the per-enterprise MFA policy API. Like the retention
// API it is a separate group because the member-facing /enterprise routes
// reject platform admins; the fine-grained check is resolveRetentionTarget
// (own enterprise_admin, or super_admin naming the enterprise).
func (r *EnterpriseRoutes) RegisterSecurity(rg *gin.RouterGroup) {
	g := rg.Group("/enterprise/security")
	g.Use(r.auth.RequireAuth())
	{
		g.GET("", r.getEnterpriseSecurity)
		g.PUT("", r.updateEnterpriseSecurity)
	}
}

// GET /api/enterprise/security[?enterprise_id=<uuid>]
func (r *EnterpriseRoutes) getEnterpriseSecurity(c *gin.Context) {
	entID, ok := r.resolveRetentionTarget(c, c.Query("enterprise_id"))
	if !ok {
		return
	}
	var require bool
	var enrolled, members int
	if err := r.db.Pool.QueryRow(c.Request.Context(),
		`SELECT e.require_mfa,
		        (SELECT COUNT(*) FROM users u WHERE u.enterprise_id = e.id AND u.deleted_at IS NULL AND u.totp_enabled),
		        (SELECT COUNT(*) FROM users u WHERE u.enterprise_id = e.id AND u.deleted_at IS NULL)
		   FROM enterprises e WHERE e.id = $1::uuid AND e.deleted_at IS NULL`, entID,
	).Scan(&require, &enrolled, &members); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Enterprise not found"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"enterprise_id": entID, "require_mfa": require,
		"members_total": members, "members_mfa_enrolled": enrolled,
	})
}

// PUT /api/enterprise/security {require_mfa: bool[, enterprise_id]}
func (r *EnterpriseRoutes) updateEnterpriseSecurity(c *gin.Context) {
	var req struct {
		EnterpriseID string `json:"enterprise_id"`
		// Pointer: a missing field must not silently switch the policy off.
		RequireMFA *bool `json:"require_mfa"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.RequireMFA == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "require_mfa (boolean) is required"}})
		return
	}
	entID, ok := r.resolveRetentionTarget(c, req.EnterpriseID)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	var old bool
	if err := r.db.Pool.QueryRow(ctx,
		`SELECT require_mfa FROM enterprises WHERE id = $1::uuid AND deleted_at IS NULL`, entID).Scan(&old); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Enterprise not found"}})
		return
	}
	changed := old != *req.RequireMFA
	if changed {
		if _, err := r.db.Pool.Exec(ctx,
			`UPDATE enterprises SET require_mfa = $2, updated_at = NOW() WHERE id = $1::uuid`, entID, *req.RequireMFA); err != nil {
			r.logger.Error("Failed to update enterprise MFA policy", zap.String("enterprise_id", entID), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update security policy"}})
			return
		}
		userID := c.GetString("user_id")
		oldS, newS := fmt.Sprintf("%t", old), fmt.Sprintf("%t", *req.RequireMFA)
		msg := fmt.Sprintf("Enterprise MFA requirement changed from %s to %s", oldS, newS)
		if r.audit != nil {
			r.audit.LogStructured(ctx, userID, c.GetString("email"), "security", "enterprise_mfa_policy_updated", "warning", msg,
				"enterprise", entID, map[string]interface{}{"require_mfa": old}, map[string]interface{}{"require_mfa": *req.RequireMFA})
		}
		r.logEnterpriseAudit(ctx, enterpriseAuditEntry{
			EnterpriseID: entID, Category: "settings", ChangeType: "mfa_policy_updated", FieldName: "require_mfa",
			ChangedBy: userID, TargetType: "enterprise", TargetID: entID, Reason: msg,
			OldValueStr: &oldS, NewValueStr: &newS,
		})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "changed": changed, "enterprise_id": entID, "require_mfa": *req.RequireMFA})
}

// ---- platform policy: /api/admin/settings/mfa-required-roles ---------------

func (r *SettingsRoutes) loadMFARequiredRoles(c *gin.Context) []string {
	var v string
	_ = r.db.Pool.QueryRow(c.Request.Context(),
		`SELECT COALESCE(value, '') FROM system_settings WHERE key = $1`, middleware.SettingMFARequiredRoles).Scan(&v)
	roles := middleware.ParseMFARoles(v)
	if roles == nil {
		roles = []string{}
	}
	return roles
}

// GET /api/admin/settings/mfa-required-roles (super_admin)
func (r *SettingsRoutes) getMFARequiredRoles(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"roles": r.loadMFARequiredRoles(c), "valid_roles": middleware.ValidMFARoles})
}

// PUT /api/admin/settings/mfa-required-roles {roles: ["enterprise_admin", ...]}
// An empty list means nobody is forced to use MFA.
func (r *SettingsRoutes) updateMFARequiredRoles(c *gin.Context) {
	var req struct {
		Roles *[]string `json:"roles"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Roles == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "roles (array) is required"}})
		return
	}
	valid := map[string]bool{}
	for _, v := range middleware.ValidMFARoles {
		valid[v] = true
	}
	want := map[string]bool{}
	for _, role := range *req.Roles {
		role = strings.ToLower(strings.TrimSpace(role))
		if role == "" {
			continue
		}
		if !valid[role] {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf("Unknown role %q", role)}})
			return
		}
		want[role] = true
	}
	// Stable order (privilege order) so the stored value and audit trail are deterministic.
	var ordered []string
	for _, role := range middleware.ValidMFARoles {
		if want[role] {
			ordered = append(ordered, role)
		}
	}
	newVal := strings.Join(ordered, ",")
	oldVal := strings.Join(r.loadMFARequiredRoles(c), ",")

	ctx := c.Request.Context()
	userID := c.GetString("user_id")
	if _, err := r.db.Pool.Exec(ctx,
		`INSERT INTO system_settings (key, value, category, updated_by, updated_at, managed_by)
		 VALUES ($1, $2, 'security', $3::uuid, NOW(), 'admin')
		 ON CONFLICT (key) DO UPDATE SET value = $2, updated_by = $3::uuid, updated_at = NOW(), managed_by = 'admin'`,
		middleware.SettingMFARequiredRoles, newVal, userID); err != nil {
		r.logger.Error("Failed to update mfa_required_roles", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update setting"}})
		return
	}
	changed := oldVal != newVal
	if changed && r.audit != nil {
		r.audit.LogStructured(ctx, userID, c.GetString("email"), "security", "mfa_required_roles_updated", "warning",
			fmt.Sprintf("Platform MFA-required roles changed from [%s] to [%s]", oldVal, newVal),
			"system_settings", middleware.SettingMFARequiredRoles,
			map[string]interface{}{"roles": oldVal}, map[string]interface{}{"roles": newVal})
	}
	roles := ordered
	if roles == nil {
		roles = []string{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "changed": changed, "roles": roles})
}

// ---- admin recovery: POST /api/admin/users/:id/mfa/reset -------------------

// resetUserMFA removes a user's MFA (lost device and lost recovery codes).
//
//   - super_admin: any other user.
//   - enterprise_admin: members (role "user") of their own enterprise only, so
//     an enterprise admin can neither touch other admins nor other tenants.
//   - everyone else: 403.
//
// Nobody can reset their own MFA here: that would let a hijacked session drop
// the second factor without proving it. Every reset is audit-logged and bumps
// the target's token_version (all sessions end).
func (r *AdminRoutes) resetUserMFA(c *gin.Context) {
	ctx := c.Request.Context()
	actorID := c.GetString("user_id")
	targetID := c.Param("id")

	var actorRole string
	var actorEnt *string
	if err := r.db.Pool.QueryRow(ctx,
		`SELECT COALESCE(role, 'user'), enterprise_id::text FROM users WHERE id = $1::uuid AND deleted_at IS NULL`, actorID,
	).Scan(&actorRole, &actorEnt); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Failed to verify permissions"}})
		return
	}
	if actorRole != "super_admin" && actorRole != "enterprise_admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Insufficient permissions"}})
		return
	}

	var targetRole, targetEmail string
	var targetEnt *string
	var hadMFA bool
	if err := r.db.Pool.QueryRow(ctx,
		`SELECT COALESCE(role, 'user'), email, enterprise_id::text, COALESCE(totp_enabled, false)
		   FROM users WHERE id::text = $1 AND deleted_at IS NULL`, targetID,
	).Scan(&targetRole, &targetEmail, &targetEnt, &hadMFA); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "User not found"}})
		return
	}
	if targetID == actorID {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Use the profile security settings to change your own MFA"}})
		return
	}
	if actorRole == "enterprise_admin" {
		inScope := actorEnt != nil && targetEnt != nil && *actorEnt == *targetEnt && targetRole == "user"
		if !inScope {
			c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Not permitted for this user"}})
			return
		}
	}

	svc := NewMFAService(r.db, encryptionKeyOf(r.cfg), r.logger, r.audit)
	if err := svc.AdminReset(ctx, actorID, targetID); err != nil {
		r.logger.Error("Failed to reset user MFA", zap.String("target", targetID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to reset MFA"}})
		return
	}
	if targetEnt != nil && *targetEnt != "" {
		oldS, newS := "enabled", "reset"
		if !hadMFA {
			oldS = "not_enrolled"
		}
		writeEnterpriseAudit(ctx, r.db, r.logger, enterpriseAuditEntry{
			EnterpriseID: *targetEnt, Category: "member", ChangeType: "mfa_reset", FieldName: "mfa",
			UserID: &targetID, ChangedBy: actorID, TargetType: "user", TargetID: targetID,
			OldValueStr: &oldS, NewValueStr: &newS, Reason: "MFA reset by administrator",
		})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "had_mfa": hadMFA})
}

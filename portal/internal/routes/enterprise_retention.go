package routes

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// RegisterRetention wires the per-enterprise data-retention API. It is a
// separate group from the member-facing /enterprise routes because those reject
// system/super admins (requireEnterpriseAccess), while this API must accept an
// enterprise_admin of the target enterprise OR a super_admin; the fine-grained
// check lives in resolveRetentionTarget so it is covered by unit tests.
func (r *EnterpriseRoutes) RegisterRetention(rg *gin.RouterGroup) {
	g := rg.Group("/enterprise/retention")
	g.Use(r.auth.RequireAuth())
	{
		g.GET("", r.getEnterpriseRetention)
		g.PUT("", r.updateEnterpriseRetention)
		g.POST("/purge", r.purgeEnterpriseRetention)
	}
}

// retentionPolicyView is the JSON shape of GET /api/enterprise/retention.
type retentionPolicyView struct {
	EnterpriseID string     `json:"enterprise_id"`
	Mode         string     `json:"mode"`
	Days         int        `json:"days"`
	UpdatedAt    *time.Time `json:"updated_at"`
	UpdatedBy    string     `json:"updated_by"`
	LastPurge    struct {
		At     *time.Time `json:"at"`
		By     string     `json:"by"`
		Status string     `json:"status"`
	} `json:"last_purge"`
	MinDays int `json:"min_days"`
	MaxDays int `json:"max_days"`
}

// resolveRetentionTarget authorises the caller and returns the enterprise the
// request acts on.
//
//   - enterprise_admin: only their own enterprise. A different enterprise_id in
//     the request is rejected (403) instead of silently ignored so a probing
//     admin gets a clear signal and cannot be confused about what was changed.
//   - super_admin: any (non-deleted) enterprise, named explicitly.
//   - everyone else (members, system_admin): 403.
//
// The role is read from the database, not the JWT claim, so a demoted admin
// loses access immediately. On failure the response has been written.
func (r *EnterpriseRoutes) resolveRetentionTarget(c *gin.Context, requested string) (string, bool) {
	ctx := c.Request.Context()
	userID := c.GetString("user_id")
	var role string
	var ownEnterprise *string
	if err := r.db.Pool.QueryRow(ctx,
		`SELECT COALESCE(role, 'user'), enterprise_id::text FROM users WHERE id = $1::uuid AND deleted_at IS NULL`,
		userID).Scan(&role, &ownEnterprise); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Failed to verify permissions"}})
		return "", false
	}
	requested = strings.TrimSpace(requested)
	if requested != "" {
		if _, err := uuid.Parse(requested); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid enterprise_id"}})
			return "", false
		}
	}

	var target string
	switch role {
	case "super_admin":
		if requested == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "enterprise_id is required for platform administrators"}})
			return "", false
		}
		target = strings.ToLower(requested)
	case "enterprise_admin":
		if ownEnterprise == nil || *ownEnterprise == "" {
			c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Enterprise admin access required"}})
			return "", false
		}
		if requested != "" && !strings.EqualFold(requested, *ownEnterprise) {
			c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Not permitted for this enterprise"}})
			return "", false
		}
		target = *ownEnterprise
	default:
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Enterprise admin access required"}})
		return "", false
	}

	var exists bool
	if err := r.db.Pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM enterprises WHERE id = $1::uuid AND deleted_at IS NULL)`,
		target).Scan(&exists); err != nil || !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Enterprise not found"}})
		return "", false
	}
	return target, true
}

// loadRetentionPolicy reads the stored policy and last-purge info.
func (r *EnterpriseRoutes) loadRetentionPolicy(ctx context.Context, enterpriseID string) (*retentionPolicyView, error) {
	v := &retentionPolicyView{EnterpriseID: enterpriseID, MinDays: retentionDaysMin, MaxDays: retentionDaysMax}
	var updatedBy, purgeBy, purgeStatus *string
	err := r.db.Pool.QueryRow(ctx,
		`SELECT retention_mode, retention_days, retention_updated_at, retention_updated_by::text,
		        retention_last_purge_at, retention_last_purge_by::text, retention_last_purge_status
		   FROM enterprises WHERE id = $1::uuid AND deleted_at IS NULL`, enterpriseID,
	).Scan(&v.Mode, &v.Days, &v.UpdatedAt, &updatedBy, &v.LastPurge.At, &purgeBy, &purgeStatus)
	if err != nil {
		return nil, err
	}
	if updatedBy != nil {
		v.UpdatedBy = *updatedBy
	}
	if purgeBy != nil {
		v.LastPurge.By = *purgeBy
	}
	if purgeStatus != nil {
		v.LastPurge.Status = *purgeStatus
	}
	return v, nil
}

// getEnterpriseRetention returns the current policy plus last purge info.
// GET /api/enterprise/retention[?enterprise_id=<uuid>] (enterprise_id: super_admin only)
func (r *EnterpriseRoutes) getEnterpriseRetention(c *gin.Context) {
	entID, ok := r.resolveRetentionTarget(c, c.Query("enterprise_id"))
	if !ok {
		return
	}
	v, err := r.loadRetentionPolicy(c.Request.Context(), entID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Enterprise not found"}})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to load retention policy"}})
		return
	}
	c.JSON(http.StatusOK, v)
}

type updateRetentionRequest struct {
	EnterpriseID string `json:"enterprise_id"`
	Mode         string `json:"mode"`
	// Days is a pointer so "omitted" (keep the current value) is distinguishable
	// from an invalid 0.
	Days *int `json:"days"`
}

// updateEnterpriseRetention changes the policy.
// PUT /api/enterprise/retention {mode, days}
func (r *EnterpriseRoutes) updateEnterpriseRetention(c *gin.Context) {
	var req updateRetentionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}
	entID, ok := r.resolveRetentionTarget(c, req.EnterpriseID)
	if !ok {
		return
	}
	// Validate after authorisation so unauthorised callers learn nothing about
	// accepted values.
	if !validRetentionMode(req.Mode) {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "mode must be one of: off, metadata, standard"}})
		return
	}
	if req.Days != nil && !validRetentionDays(*req.Days) {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf("days must be between %d and %d", retentionDaysMin, retentionDaysMax)}})
		return
	}

	ctx := c.Request.Context()
	userID := c.GetString("user_id")

	var oldMode string
	var oldDays, newDays int
	changed := false
	err := r.db.WithTx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`SELECT retention_mode, retention_days FROM enterprises WHERE id = $1::uuid AND deleted_at IS NULL FOR UPDATE`,
			entID).Scan(&oldMode, &oldDays); err != nil {
			return err
		}
		newDays = oldDays
		if req.Days != nil {
			newDays = *req.Days
		}
		if oldMode == req.Mode && oldDays == newDays {
			return nil
		}
		changed = true
		_, err := tx.Exec(ctx,
			`UPDATE enterprises
			    SET retention_mode = $2, retention_days = $3,
			        retention_updated_at = NOW(), retention_updated_by = $4::uuid, updated_at = NOW()
			  WHERE id = $1::uuid`,
			entID, req.Mode, newDays, userID)
		return err
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Enterprise not found"}})
			return
		}
		r.logger.Error("Failed to update retention policy", zap.String("enterprise_id", entID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update retention policy"}})
		return
	}

	if changed {
		// Cached validate responses carry the policy; flush them for all of the
		// enterprise's keys (including the Proxy's in-process copy) so the new
		// policy applies to the next request instead of after the ~5 min TTL.
		invalidateEnterpriseValidateCaches(ctx, r.db, r.logger, entID)
		r.auditRetentionChange(c, entID, "retention_policy_updated", "info",
			fmt.Sprintf("Retention policy changed from %s/%dd to %s/%dd", oldMode, oldDays, req.Mode, newDays),
			map[string]interface{}{"mode": oldMode, "days": oldDays},
			map[string]interface{}{"mode": req.Mode, "days": newDays},
			fmt.Sprintf("%s/%d", oldMode, oldDays), fmt.Sprintf("%s/%d", req.Mode, newDays))
	}

	v, err := r.loadRetentionPolicy(ctx, entID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to load retention policy"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "changed": changed, "policy": v})
}

// auditRetentionChange writes the event to both audit_logs (platform view) and
// enterprise_audit_log (enterprise's own audit timeline).
func (r *EnterpriseRoutes) auditRetentionChange(c *gin.Context, entID, action, level, message string,
	oldValue, newValue interface{}, oldStr, newStr string) {
	ctx := c.Request.Context()
	userID := c.GetString("user_id")
	if r.audit != nil {
		r.audit.LogStructured(ctx, userID, c.GetString("email"), "enterprise", action, level, message,
			"enterprise", entID, oldValue, newValue)
	}
	entry := enterpriseAuditEntry{
		EnterpriseID: entID,
		Category:     "settings",
		ChangeType:   action,
		FieldName:    "retention_policy",
		ChangedBy:    userID,
		TargetType:   "enterprise",
		TargetID:     entID,
		Reason:       message,
	}
	if oldStr != "" {
		entry.OldValueStr = &oldStr
	}
	if newStr != "" {
		entry.NewValueStr = &newStr
	}
	writeEnterpriseAudit(ctx, r.db, r.logger, entry)
}

type purgeRetentionRequest struct {
	EnterpriseID string `json:"enterprise_id"`
	Confirm      string `json:"confirm"`
}

// purgeEnterpriseRetention asks the Proxy(ies) to delete the enterprise's
// stored payloads. The caller must echo the enterprise id in `confirm` — the
// same guard the Proxy endpoint applies — so an accidental or replayed request
// cannot wipe data.
// POST /api/enterprise/retention/purge {confirm: "<enterprise_id>"}
func (r *EnterpriseRoutes) purgeEnterpriseRetention(c *gin.Context) {
	var req purgeRetentionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}
	requested := req.EnterpriseID
	if requested == "" {
		// Platform admins have no enterprise of their own; `confirm` names it.
		requested = req.Confirm
	}
	entID, ok := r.resolveRetentionTarget(c, requested)
	if !ok {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(req.Confirm), entID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Confirmation does not match the enterprise id"}})
		return
	}

	ctx := c.Request.Context()
	userID := c.GetString("user_id")

	// Audit the intent first: if the process dies mid-call there is still a
	// record that a purge was requested.
	r.auditRetentionChange(c, entID, "retention_purge_requested", "warning",
		"Enterprise payload purge requested", nil, nil, "", "")

	res, purgeErr := r.db.PurgeEnterpriseRetention(ctx, entID)

	status := "ok"
	if purgeErr != nil {
		status = "failed"
	}
	// Record the outcome on the enterprise (shown as "last purge" in the UI).
	if _, err := r.db.Pool.Exec(ctx,
		`UPDATE enterprises SET retention_last_purge_at = NOW(), retention_last_purge_by = $2::uuid,
		        retention_last_purge_status = $3 WHERE id = $1::uuid`,
		entID, userID, status); err != nil {
		r.logger.Warn("Failed to record retention purge outcome", zap.String("enterprise_id", entID), zap.Error(err))
	}

	if purgeErr != nil {
		r.logger.Warn("Enterprise retention purge failed", zap.String("enterprise_id", entID), zap.Error(purgeErr))
		r.auditRetentionChange(c, entID, "retention_purge_failed", "error",
			"Enterprise payload purge failed: "+purgeErr.Error(), nil, nil, "", "failed")
		code := http.StatusBadGateway
		msg := "The proxy could not be reached or rejected the purge request. Nothing may have been deleted; try again later."
		switch {
		case errors.Is(purgeErr, db.ErrProxyNotConfigured), errors.Is(purgeErr, db.ErrProxySecretMissing):
			code = http.StatusServiceUnavailable
			msg = "Proxy connection is not configured on this Portal; purge is unavailable."
		}
		c.JSON(code, gin.H{"error": gin.H{"message": msg}, "success": false})
		return
	}

	r.auditRetentionChange(c, entID, "retention_purged", "warning",
		fmt.Sprintf("Enterprise payloads purged (%d files, %d bytes)", res.DeletedFiles, res.DeletedBytes),
		nil, map[string]interface{}{"deleted_files": res.DeletedFiles, "deleted_bytes": res.DeletedBytes}, "", "ok")
	c.JSON(http.StatusOK, gin.H{
		"success":       true,
		"deleted_files": res.DeletedFiles,
		"deleted_bytes": res.DeletedBytes,
		"proxies":       len(res.Targets),
	})
}

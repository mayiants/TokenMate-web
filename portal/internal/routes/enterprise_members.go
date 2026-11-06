package routes

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"github.com/ai-proxy/ai-portal-go/pkg/crypto"
	"github.com/ai-proxy/ai-portal-go/web"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

// reclaimMemberEnterpriseQuota returns the unused portion of a member's
// enterprise-quota grant to the enterprise pool when the member leaves the
// enterprise (S-U6). It must run inside the same transaction as
// enterprise_members / api_keys / users updates so the quota can never be
// double-counted across the boundary.
//
// Allocation path (topupMemberQuota):
//
//	enterprises.quota_used += N
//	users.quota_total += N; users.quota_remaining += N
//
// Reclaim path (this):
//
//	unused := max(users.quota_total - users.quota_used, 0)
//	enterprises.quota_used = max(quota_used - unused, 0)
//	users.quota_total = 0; users.quota_remaining = 0
//
// Already-spent quota (`users.quota_used`) is not moved — the enterprise
// pool was debited up-front for the entire grant, so reusing the unused
// portion keeps the pool from being inflated by repeated topup→remove
// cycles. The member's `quota_used`/`credit_used` are zeroed too: drop them
// once the member has no enterprise relationship so the monthly reset gate
// cannot accidentally re-apply an enterprise cap to a now-standalone user.
func reclaimMemberEnterpriseQuota(ctx context.Context, tx pgx.Tx, userID, enterpriseID string) (reclaimed int64, err error) {
	var total, used int64
	if qerr := tx.QueryRow(ctx,
		`SELECT COALESCE(quota_total, 0), COALESCE(quota_used, 0)
		  FROM users WHERE id = $1::uuid`,
		userID,
	).Scan(&total, &used); qerr != nil {
		// A scan error here means we cannot safely reclaim — fail loudly so
		// the transaction aborts rather than silently leaking pool space.
		return 0, fmt.Errorf("select member quota: %w", qerr)
	}
	unused := total - used
	if unused < 0 {
		unused = 0
	}
	if unused > 0 {
		if _, err = tx.Exec(ctx,
			`UPDATE enterprises SET quota_used = GREATEST(quota_used - $1, 0), updated_at = NOW()
			 WHERE id = $2::uuid`,
			unused, enterpriseID,
		); err != nil {
			return 0, fmt.Errorf("reclaim pool quota_used: %w", err)
		}
	}
	if _, err = tx.Exec(ctx,
		`UPDATE users SET quota_total = 0, quota_remaining = 0,
		                  quota_used = 0, credit_used = 0, updated_at = NOW()
		 WHERE id = $1::uuid`,
		userID,
	); err != nil {
		return 0, fmt.Errorf("zero member quota fields: %w", err)
	}
	return unused, nil
}

// removeMyEnterpriseMember removes a member from the current user's enterprise
// notifyEnterpriseAdmins is a thin nil-safe wrapper that enriches params with
// the enterprise name and forwards to the notification service.
func (r *EnterpriseRoutes) notifyEnterpriseAdmins(c *gin.Context, enterpriseID, ntype, priority, titleKey, bodyKey string, params map[string]string, link, refType, refID string) {
	if r.notif == nil {
		return
	}
	if params == nil {
		params = map[string]string{}
	}
	if _, ok := params["enterprise"]; !ok {
		var name string
		if err := r.db.QueryRow(c.Request.Context(), `SELECT name FROM enterprises WHERE id = $1::uuid`, enterpriseID).Scan(&name); err == nil && name != "" {
			params["enterprise"] = name
		}
	}
	r.notif.NotifyEnterpriseAdmins(c.Request.Context(), enterpriseID, ntype, priority, titleKey, bodyKey, params, link, refType, refID)
}

func (r *EnterpriseRoutes) removeMyEnterpriseMember(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	userID := c.Param("userId")
	currentUserID := c.GetString("user_id")

	if userID == currentUserID {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Cannot remove yourself"}})
		return
	}

	// Collect key hashes before the transaction so the cache can be
	// invalidated after commit (the goroutine closure below must not
	// touch the gin context).
	var recalledKeyHashes []string
	if hashRows, qErr := r.db.Query(c.Request.Context(),
		`SELECT key_hash FROM api_keys
		 WHERE enterprise_id = $1::uuid AND assigned_user_id = $2::uuid AND deleted_at IS NULL AND status = 'active'`,
		enterpriseID, userID); qErr == nil {
		for hashRows.Next() {
			var h string
			if scanErr := hashRows.Scan(&h); scanErr == nil {
				recalledKeyHashes = append(recalledKeyHashes, h)
			}
		}
		hashRows.Close()
	}

	if err := r.db.WithTx(c.Request.Context(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(c.Request.Context(),
			`UPDATE enterprise_members SET removed_at = NOW(), removed_by = $1::uuid
			 WHERE enterprise_id = $2::uuid AND user_id = $3::uuid AND removed_at IS NULL`,
			currentUserID, enterpriseID, userID); err != nil {
			return err
		}
		if _, err := tx.Exec(c.Request.Context(),
			`UPDATE users SET enterprise_id = NULL, role = 'user', updated_at = NOW()
			 WHERE id = $1::uuid AND enterprise_id = $2::uuid`,
			userID, enterpriseID); err != nil {
			return err
		}
		if _, err := tx.Exec(c.Request.Context(),
			`UPDATE api_keys SET status = 'disabled', assigned_user_id = NULL, updated_at = NOW()
			 WHERE enterprise_id = $1::uuid AND assigned_user_id = $2::uuid`,
			enterpriseID, userID); err != nil {
			return err
		}
		// Return the member's unused enterprise-quota grant to the pool
		// before they leave the enterprise context (S-U6).
		if _, err := reclaimMemberEnterpriseQuota(c.Request.Context(), tx, userID, enterpriseID); err != nil {
			return err
		}
		return nil
	}); err != nil {
		r.logger.Error("Failed to remove enterprise member", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to remove member"}})
		return
	}
	for _, h := range recalledKeyHashes {
		invalidateValidateCache(c.Request.Context(), r.db, r.logger, h)
	}
	// The member's personal keys lose their enterprise context
	// (users.enterprise_id cleared above); drop their cached validate
	// results too so the change takes effect immediately.
	invalidateUserValidateCaches(c.Request.Context(), r.db, r.logger, userID)

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), currentUserID, c.GetString("email"), "enterprise", "member_removed", "warning",
			fmt.Sprintf("Member removed from enterprise %s: %s", enterpriseID, userID))
	}

	oldStatus := "active"
	newStatus := "removed"
	r.logEnterpriseAudit(c.Request.Context(), enterpriseAuditEntry{
		EnterpriseID: enterpriseID,
		Category:     "member",
		ChangeType:   "member_removed",
		FieldName:    "membership",
		UserID:       &userID,
		ChangedBy:    currentUserID,
		OldValueStr:  &oldStatus,
		NewValueStr:  &newStatus,
		Reason:       "Member removed from enterprise",
	})

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// topupMemberQuota adds quota to a specific enterprise member from the enterprise pool
func (r *EnterpriseRoutes) topupMemberQuota(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	userID := c.Param("userId")

	var req struct {
		Quota int64 `json:"quota" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "quota is required"}})
		return
	}

	if req.Quota <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "quota must be positive"}})
		return
	}

	// Verify member belongs to this enterprise
	var memberEnterpriseID *string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT enterprise_id::text FROM users WHERE id = $1::uuid AND deleted_at IS NULL`, userID,
	).Scan(&memberEnterpriseID)
	if err != nil || memberEnterpriseID == nil || *memberEnterpriseID != enterpriseID {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Member not found in your enterprise"}})
		return
	}

	// Transaction: deduct from enterprise pool + add to member
	tx, err := r.db.Pool.Begin(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Transaction failed"}})
		return
	}
	defer func() { _ = tx.Rollback(c.Request.Context()) }()

	// Lock and check enterprise quota pool
	var quotaTotal, quotaUsed int64
	err = tx.QueryRow(c.Request.Context(),
		`SELECT COALESCE(quota_total, 0), COALESCE(quota_used, 0)
		 FROM enterprises WHERE id = $1::uuid FOR UPDATE`,
		enterpriseID,
	).Scan(&quotaTotal, &quotaUsed)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to read enterprise quota"}})
		return
	}

	available := quotaTotal - quotaUsed
	if req.Quota > available {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message":   "Enterprise quota insufficient",
				"available": available,
				"requested": req.Quota,
			},
		})
		return
	}

	// Deduct from enterprise pool (pre-allocate)
	_, err = tx.Exec(c.Request.Context(),
		`UPDATE enterprises SET quota_used = quota_used + $1, updated_at = NOW()
		 WHERE id = $2::uuid`,
		req.Quota, enterpriseID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to reserve enterprise quota"}})
		return
	}

	// Add to member
	_, err = tx.Exec(c.Request.Context(),
		`UPDATE users SET quota_total = COALESCE(quota_total, 0) + $1, quota_remaining = COALESCE(quota_remaining, 0) + $1, updated_at = NOW()
		 WHERE id = $2::uuid`,
		req.Quota, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to top up quota"}})
		return
	}

	if err := tx.Commit(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to commit"}})
		return
	}

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "enterprise", "member_topup", "info",
			fmt.Sprintf("Allocated %d quota to member %s from enterprise pool %s (remaining: %d)", req.Quota, userID, enterpriseID, available-req.Quota))
	}

	// Enterprise-scoped audit: a top-up moves quota from the enterprise pool to
	// a member (a financial allocation), so it must appear on the enterprise
	// audit page alongside quota-limit changes.
	delta := req.Quota
	r.logEnterpriseAudit(c.Request.Context(), enterpriseAuditEntry{
		EnterpriseID: enterpriseID,
		Category:     "billing",
		ChangeType:   "member_topup",
		FieldName:    "quota_total",
		UserID:       &userID,
		ChangedBy:    c.GetString("user_id"),
		Delta:        &delta,
		TargetType:   "user",
		TargetID:     userID,
		Reason:       fmt.Sprintf("Allocated %d quota to member from enterprise pool", req.Quota),
	})

	c.JSON(http.StatusOK, gin.H{"success": true, "added_quota": req.Quota, "enterprise_remaining": available - req.Quota})
}

// setMemberQuotaLimit sets a per-member token quota cap for an enterprise member.
// 0 = unlimited. Enterprise admin can use this to restrict individual member usage.
func (r *EnterpriseRoutes) setMemberQuotaLimit(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	userID := c.Param("userId")

	var req struct {
		QuotaLimit int64 `json:"quota_limit"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	if req.QuotaLimit < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "quota_limit cannot be negative"}})
		return
	}

	// Verify member belongs to this enterprise
	var memberEnterpriseID *string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT enterprise_id::text FROM users WHERE id = $1::uuid AND deleted_at IS NULL`, userID,
	).Scan(&memberEnterpriseID)
	if err != nil || memberEnterpriseID == nil || *memberEnterpriseID != enterpriseID {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Member not found in your enterprise"}})
		return
	}

	var oldQuotaLimit int64
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COALESCE(quota_limit, 0) FROM enterprise_members WHERE enterprise_id = $1::uuid AND user_id = $2::uuid AND removed_at IS NULL`,
		enterpriseID, userID).Scan(&oldQuotaLimit)

	_, err = r.db.Pool.Exec(c.Request.Context(),
		`UPDATE enterprise_members SET quota_limit = $1
		 WHERE enterprise_id = $2::uuid AND user_id = $3::uuid AND removed_at IS NULL`,
		req.QuotaLimit, enterpriseID, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "An internal error occurred"}})
		return
	}

	operatorID := c.GetString("user_id")
	if oldQuotaLimit != req.QuotaLimit {
		if _, err := r.db.Pool.Exec(c.Request.Context(),
			`INSERT INTO enterprise_audit_log (enterprise_id, category, change_type, field_name, action, amount, delta, old_value, new_value, reason, changed_by, user_id)
			 VALUES ($1, 'billing', 'member_quota_limit_set', 'quota_limit', 'set', 0, $2, $3, $4, 'Member quota limit updated', $5::uuid, $6::uuid)`,
			enterpriseID, req.QuotaLimit-oldQuotaLimit, oldQuotaLimit, req.QuotaLimit, operatorID, userID); err != nil {
			r.logger.Warn("Failed to log member quota limit change", zap.Error(err))
		}
	}

	if r.audit != nil {
		limitStr := "unlimited"
		if req.QuotaLimit > 0 {
			limitStr = fmt.Sprintf("%d", req.QuotaLimit)
		}
		r.audit.Log(c.Request.Context(), operatorID, c.GetString("email"), "enterprise", "quota_limit_set", "info",
			fmt.Sprintf("Set member %s quota limit to %s tokens in enterprise %s", userID, limitStr, enterpriseID))
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "quota_limit": req.QuotaLimit})
}

// setMemberCreditLimit sets a per-member credit cap for an enterprise member.
// 0 = unlimited. The unit is micro-USD to match enterprises.credit_total/used.
func (r *EnterpriseRoutes) setMemberCreditLimit(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	userID := c.Param("userId")

	var req struct {
		CreditLimit int64 `json:"credit_limit"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	if req.CreditLimit < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "credit_limit cannot be negative"}})
		return
	}

	// Verify member belongs to this enterprise
	var memberEnterpriseID *string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT enterprise_id::text FROM users WHERE id = $1::uuid AND deleted_at IS NULL`, userID,
	).Scan(&memberEnterpriseID)
	if err != nil || memberEnterpriseID == nil || *memberEnterpriseID != enterpriseID {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Member not found in your enterprise"}})
		return
	}

	var oldCreditLimit int64
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COALESCE(credit_limit, 0) FROM enterprise_members WHERE enterprise_id = $1::uuid AND user_id = $2::uuid AND removed_at IS NULL`,
		enterpriseID, userID).Scan(&oldCreditLimit)

	_, err = r.db.Pool.Exec(c.Request.Context(),
		`UPDATE enterprise_members SET credit_limit = $1
		 WHERE enterprise_id = $2::uuid AND user_id = $3::uuid AND removed_at IS NULL`,
		req.CreditLimit, enterpriseID, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "An internal error occurred"}})
		return
	}

	operatorID := c.GetString("user_id")
	if oldCreditLimit != req.CreditLimit {
		if _, err := r.db.Pool.Exec(c.Request.Context(),
			`INSERT INTO enterprise_audit_log (enterprise_id, category, change_type, field_name, action, amount, delta, old_value, new_value, reason, changed_by, user_id)
			 VALUES ($1, 'billing', 'member_credit_limit_set', 'credit_limit', 'set', 0, $2, $3, $4, 'Member credit limit updated', $5::uuid, $6::uuid)`,
			enterpriseID, req.CreditLimit-oldCreditLimit, oldCreditLimit, req.CreditLimit, operatorID, userID); err != nil {
			r.logger.Warn("Failed to log member credit limit change", zap.Error(err))
		}
	}

	if r.audit != nil {
		limitStr := "unlimited"
		if req.CreditLimit > 0 {
			limitStr = fmt.Sprintf("%d", req.CreditLimit)
		}
		r.audit.Log(c.Request.Context(), operatorID, c.GetString("email"), "enterprise", "credit_limit_set", "info",
			fmt.Sprintf("Set member %s credit limit to %s micro-USD in enterprise %s", userID, limitStr, enterpriseID))
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "credit_limit": req.CreditLimit})
}

// updateMemberName updates the display name of an enterprise member.
func (r *EnterpriseRoutes) updateMemberName(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	userID := c.Param("userId")
	currentUserID := c.GetString("user_id")

	if userID == currentUserID {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Cannot change your own name here"}})
		return
	}

	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "name is required"}})
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "name cannot be empty"}})
		return
	}

	// Verify member belongs to this enterprise
	var memberEnterpriseID *string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT enterprise_id::text FROM users WHERE id = $1::uuid AND deleted_at IS NULL`, userID,
	).Scan(&memberEnterpriseID)
	if err != nil || memberEnterpriseID == nil || *memberEnterpriseID != enterpriseID {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Member not found in your enterprise"}})
		return
	}

	if err := r.db.Exec(c.Request.Context(),
		`UPDATE users SET name = $1, updated_at = NOW() WHERE id = $2::uuid`,
		name, userID); err != nil {
		r.logger.Error("Failed to update member name", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update name"}})
		return
	}

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), currentUserID, c.GetString("email"), "enterprise", "member_name_updated", "info",
			fmt.Sprintf("Member %s name updated in enterprise %s", userID, enterpriseID))
	}

	r.logEnterpriseAudit(c.Request.Context(), enterpriseAuditEntry{
		EnterpriseID: enterpriseID,
		Category:     "member",
		ChangeType:   "member_name_updated",
		FieldName:    "name",
		UserID:       &userID,
		ChangedBy:    currentUserID,
		NewValueStr:  &name,
		TargetType:   "user",
		TargetID:     userID,
		Reason:       "Member display name updated",
	})

	c.JSON(http.StatusOK, gin.H{"success": true, "name": name})
}

// toggleMemberStatus toggles a member's status between active and suspended
func (r *EnterpriseRoutes) toggleMemberStatus(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	userID := c.Param("userId")
	currentUserID := c.GetString("user_id")

	if userID == currentUserID {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Cannot toggle your own status"}})
		return
	}

	var currentStatus string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT status FROM users WHERE id = $1::uuid AND enterprise_id = $2::uuid AND deleted_at IS NULL`,
		userID, enterpriseID,
	).Scan(&currentStatus)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Member not found"}})
		return
	}

	newStatus := "suspended"
	if currentStatus == "suspended" {
		newStatus = "active"
	}

	err = r.db.Exec(c.Request.Context(),
		`UPDATE users SET status = $1, updated_at = NOW() WHERE id = $2::uuid`,
		newStatus, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to toggle status"}})
		return
	}

	// A suspended member's keys are rejected by validateKey; drop the cached
	// positive validation results so the gate takes effect immediately.
	invalidateUserValidateCaches(c.Request.Context(), r.db, r.logger, userID)

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), currentUserID, c.GetString("email"), "enterprise", "member_toggled", "info",
			fmt.Sprintf("Member %s status changed to %s in enterprise %s", userID, newStatus, enterpriseID))
	}

	r.logEnterpriseAudit(c.Request.Context(), enterpriseAuditEntry{
		EnterpriseID: enterpriseID,
		Category:     "member",
		ChangeType:   "member_status_changed",
		FieldName:    "status",
		UserID:       &userID,
		ChangedBy:    currentUserID,
		OldValueStr:  &currentStatus,
		NewValueStr:  &newStatus,
		Reason:       fmt.Sprintf("Member status changed to %s", newStatus),
	})

	c.JSON(http.StatusOK, gin.H{"success": true, "status": newStatus})
}

// listMembers returns enterprise members and pending invitations
func (r *EnterpriseRoutes) listMembers(c *gin.Context) {
	enterpriseID := c.Param("id")

	// Active members
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT u.id::text, u.email, COALESCE(u.name, ''), em.role, em.joined_at
		 FROM enterprise_members em
		 JOIN users u ON em.user_id = u.id
		 WHERE em.enterprise_id = $1::uuid AND em.removed_at IS NULL
		 ORDER BY em.joined_at DESC`, enterpriseID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list members"}})
		return
	}
	defer rows.Close()

	members := []gin.H{}
	for rows.Next() {
		var id, email, name, role string
		var createdAt interface{}
		if err := rows.Scan(&id, &email, &name, &role, &createdAt); err != nil {
			continue
		}
		members = append(members, gin.H{
			"id": id, "email": email, "name": name, "role": role, "created_at": createdAt,
		})
	}

	// Pending invitations
	invRows, err := r.db.Query(c.Request.Context(),
		`SELECT id::text, email, role, status, created_at
		 FROM enterprise_invitations
		 WHERE enterprise_id = $1::uuid AND status IN ('pending', 'email_confirmed', 'ea_approved')
		 ORDER BY created_at DESC`, enterpriseID)
	if err == nil {
		defer invRows.Close()
		invitations := []gin.H{}
		for invRows.Next() {
			var id, email, role, status string
			var createdAt interface{}
			if err := invRows.Scan(&id, &email, &role, &status, &createdAt); err == nil {
				invitations = append(invitations, gin.H{
					"id": id, "email": email, "role": role, "status": status, "created_at": createdAt,
				})
			}
		}
		c.JSON(http.StatusOK, gin.H{"members": members, "invitations": invitations})
		return
	}

	c.JSON(http.StatusOK, gin.H{"members": members})
}

// inviteMember sends an invitation email
func (r *EnterpriseRoutes) inviteMember(c *gin.Context) {
	enterpriseID := c.Param("id")
	adminUserID := c.GetString("user_id")

	var req struct {
		Email string `json:"email" binding:"required"`
		Role  string `json:"role"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Email is required"}})
		return
	}
	if req.Role == "" {
		req.Role = "member"
	}

	// Check enterprise exists and capacity
	var enterpriseName string
	var maxMembers int
	var allowedDomains string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT name, COALESCE(max_members, 50), COALESCE(allowed_domains, '') FROM enterprises WHERE id = $1::uuid AND deleted_at IS NULL`,
		enterpriseID,
	).Scan(&enterpriseName, &maxMembers, &allowedDomains)
	_ = allowedDomains // reserved for future domain-based branching
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Enterprise not found"}})
		return
	}

	// Check max_members limit (active members + pending invitations that don't already have a member)
	var currentMemberCount int
	var pendingInviteCount int
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM enterprise_members WHERE enterprise_id = $1::uuid AND removed_at IS NULL`,
		enterpriseID,
	).Scan(&currentMemberCount)
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM enterprise_invitations ei
		 WHERE ei.enterprise_id = $1::uuid
		   AND ei.status IN ('pending', 'email_confirmed', 'ea_approved')
		   AND NOT EXISTS (
		     SELECT 1 FROM enterprise_members em
		     JOIN users u ON em.user_id = u.id
		     WHERE em.enterprise_id = ei.enterprise_id
		       AND em.removed_at IS NULL
		       AND u.email = ei.email
		       AND u.deleted_at IS NULL
		   )`,
		enterpriseID,
	).Scan(&pendingInviteCount)
	total := currentMemberCount + pendingInviteCount
	if total >= maxMembers {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf("Enterprise member limit reached (%d active + %d pending = %d/%d)", currentMemberCount, pendingInviteCount, total, maxMembers)}})
		return
	}

	// Prevent duplicate pending invitation for the same email.
	var existingPendingID *string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT id::text FROM enterprise_invitations
		 WHERE enterprise_id = $1::uuid AND email = $2 AND status IN ('pending', 'email_confirmed', 'ea_approved')`,
		enterpriseID, req.Email,
	).Scan(&existingPendingID)
	if existingPendingID != nil {
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "An invitation is already pending for this email"}})
		return
	}

	// Check if user already exists (inviteMember)
	var existingUserID *string
	err = r.db.QueryRow(c.Request.Context(),
		`SELECT id::text FROM users WHERE email = $1 AND deleted_at IS NULL`, req.Email,
	).Scan(&existingUserID)
	if err == nil && existingUserID != nil {
		// User exists — create an invitation in email_confirmed state.
		// SA invitations pre-approve nothing: the enterprise admin must
		// approve first, regardless of domain match.
		existingToken := generateRandomCode("ABCDEFGHJKLMNPQRSTUVWXYZ23456789", 32)
		tokenHash := crypto.HashKeySHA256(existingToken)
		if err := r.db.Exec(c.Request.Context(),
			`INSERT INTO enterprise_invitations (enterprise_id, email, name, role, token, status, email_confirmed, email_confirmed_at, expires_at, invited_by)
			 VALUES ($1::uuid, $2, NULLIF($3, ''), $4, $5, 'email_confirmed', TRUE, NOW(), NOW() + INTERVAL '14 days', $6::uuid)`,
			enterpriseID, req.Email, "", req.Role, tokenHash, adminUserID); err != nil {
			r.logger.Error("Failed to create invitation for existing user", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to create invitation"}})
			return
		}

		if r.audit != nil {
			r.audit.Log(c.Request.Context(), adminUserID, c.GetString("email"), "enterprise", "member_invited", "info",
				fmt.Sprintf("Member invited to enterprise %s: %s (awaiting EA approval)", enterpriseName, req.Email))
		}

		// Actionable: the member sits in email_confirmed awaiting EA approval.
		r.notifyEnterpriseAdmins(c, enterpriseID, "enterprise_member_pending_ea", "high",
			"notif.enterprise_member_pending_ea.title", "notif.enterprise_member_pending_ea.body",
			map[string]string{"email": req.Email, "enterprise": enterpriseName},
			"/enterprise/members", "invitation", req.Email)

		role := req.Role
		r.logEnterpriseAudit(c.Request.Context(), enterpriseAuditEntry{
			EnterpriseID: enterpriseID,
			Category:     "member",
			ChangeType:   "member_invited",
			FieldName:    "invitation",
			ChangedBy:    adminUserID,
			NewValueStr:  &role,
			Reason:       fmt.Sprintf("Invitation sent to %s (existing user, awaiting EA approval)", req.Email),
		})

		c.JSON(http.StatusOK, gin.H{"success": true, "pending_ea_approval": true, "message": T("msg.invitation_pending_ea", c)})
		return
	}

	// Generate invitation token
	tokenBytes := make([]byte, 32)
	_, _ = rand.Read(tokenBytes)
	token := hex.EncodeToString(tokenBytes)
	tokenHash := crypto.HashKeySHA256(token)

	// Create invitation
	var inviteID string
	err = r.db.QueryRow(c.Request.Context(),
		`INSERT INTO enterprise_invitations (enterprise_id, email, token, role, invited_by, expires_at)
		 VALUES ($1::uuid, $2, $3, $4, $5::uuid, NOW() + INTERVAL '14 days')
		 RETURNING id::text`,
		enterpriseID, req.Email, tokenHash, req.Role, adminUserID,
	).Scan(&inviteID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to create invitation"}})
		return
	}

	// Send invitation email
	acceptURL := fmt.Sprintf("%s/api/enterprise/accept?token=%s", r.cfg.App.BaseURL, token)

	if r.mail != nil && r.mail.IsConfigured() {
		locale := GetLocale(c)
		go func() {
			if err := r.mail.SendEnterpriseInvitation(req.Email, enterpriseName, acceptURL, locale); err != nil {
				r.logger.Warn("Failed to send invitation email",
					zap.String("email", req.Email),
					zap.Error(err))
			}
		}()
	}

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), adminUserID, c.GetString("email"), "enterprise", "member_invited", "info",
			fmt.Sprintf("Member invited to enterprise %s: %s", enterpriseName, req.Email))
	}

	role := req.Role
	r.logEnterpriseAudit(c.Request.Context(), enterpriseAuditEntry{
		EnterpriseID: enterpriseID,
		Category:     "member",
		ChangeType:   "member_invited",
		FieldName:    "invitation",
		ChangedBy:    adminUserID,
		NewValueStr:  &role,
		Reason:       fmt.Sprintf("Invitation sent to %s (new user)", req.Email),
	})

	c.JSON(http.StatusOK, gin.H{
		"success":    true,
		"invitation": gin.H{"id": inviteID, "email": req.Email, "token": token, "accept_url": acceptURL},
	})
}

// listInvitations lists all invitations for an enterprise
func (r *EnterpriseRoutes) listInvitations(c *gin.Context) {
	enterpriseID := c.Query("enterprise_id")

	query := `SELECT i.id::text, i.email, i.role, i.status, i.expires_at, i.created_at,
	                 e.name as enterprise_name
	          FROM enterprise_invitations i
	          JOIN enterprises e ON i.enterprise_id = e.id`
	args := []interface{}{}

	if enterpriseID != "" {
		query += " WHERE i.enterprise_id = $1::uuid"
		args = append(args, enterpriseID)
	}
	query += " ORDER BY i.created_at DESC LIMIT 50"

	rows, err := r.db.Query(c.Request.Context(), query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list invitations"}})
		return
	}
	defer rows.Close()

	invitations := []gin.H{}
	for rows.Next() {
		var id, email, role, status, enterpriseName string
		var expiresAt, createdAt interface{}
		if err := rows.Scan(&id, &email, &role, &status, &expiresAt, &createdAt, &enterpriseName); err != nil {
			continue
		}
		invitations = append(invitations, gin.H{
			"id": id, "email": email, "role": role, "status": status,
			"expires_at": expiresAt, "created_at": createdAt, "enterprise_name": enterpriseName,
		})
	}
	c.JSON(http.StatusOK, gin.H{"invitations": invitations})
}

// cancelInvitation cancels an invitation (super admin route)
func (r *EnterpriseRoutes) cancelInvitation(c *gin.Context) {
	inviteID := c.Param("id")
	err := r.db.Exec(c.Request.Context(),
		`UPDATE enterprise_invitations SET status = 'cancelled' WHERE id = $1::uuid AND status = 'pending'`,
		inviteID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to cancel invitation"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// cancelMyEnterpriseInvitation cancels a pending invitation for the current enterprise.
func (r *EnterpriseRoutes) cancelMyEnterpriseInvitation(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	inviteID := c.Param("invId")

	res, err := r.db.Pool.Exec(c.Request.Context(),
		`UPDATE enterprise_invitations SET status = 'cancelled'
		 WHERE id = $1::uuid AND enterprise_id = $2::uuid AND status IN ('pending', 'email_confirmed', 'ea_approved')`,
		inviteID, enterpriseID)
	if err != nil {
		r.logger.Error("Failed to cancel enterprise invitation", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to cancel invitation"}})
		return
	}
	if res.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Invitation not found or already cancelled"}})
		return
	}

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "enterprise", "invitation_cancelled", "info",
			fmt.Sprintf("Invitation %s cancelled in enterprise %s", inviteID, enterpriseID))
	}

	oldStatus := "pending"
	newStatus := "cancelled"
	r.logEnterpriseAudit(c.Request.Context(), enterpriseAuditEntry{
		EnterpriseID: enterpriseID,
		Category:     "member",
		ChangeType:   "invitation_cancelled",
		FieldName:    "invitation_status",
		ChangedBy:    c.GetString("user_id"),
		OldValueStr:  &oldStatus,
		NewValueStr:  &newStatus,
		Reason:       fmt.Sprintf("Invitation %s cancelled", inviteID),
	})

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// resendMyEnterpriseInvitation regenerates the token and refreshes the expiry
// for a pending (email-link) invitation, then re-sends the invitation email.
// Only 'pending' invitations have an email acceptance link that can expire;
// 'email_confirmed'/'ea_approved' invitations are existing users who accept
// in-app (awaiting approval), so resend does not apply to them.
func (r *EnterpriseRoutes) resendMyEnterpriseInvitation(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	inviteID := c.Param("invId")
	ctx := c.Request.Context()

	// Only pending (email-link) invitations are resendable.
	var email, enterpriseName string
	err := r.db.QueryRow(ctx,
		`SELECT i.email, e.name
		 FROM enterprise_invitations i
		 JOIN enterprises e ON i.enterprise_id = e.id
		 WHERE i.id = $1::uuid AND i.enterprise_id = $2::uuid AND i.status = 'pending'`,
		inviteID, enterpriseID,
	).Scan(&email, &enterpriseName)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": T("error.invitation_not_resendable", c)}})
		return
	}

	// New token + fresh 14-day expiry.
	tokenBytes := make([]byte, 32)
	_, _ = rand.Read(tokenBytes)
	token := hex.EncodeToString(tokenBytes)
	tokenHash := crypto.HashKeySHA256(token)

	if err := r.db.Exec(ctx,
		`UPDATE enterprise_invitations
		 SET token = $1, expires_at = NOW() + INTERVAL '14 days', updated_at = NOW()
		 WHERE id = $2::uuid AND enterprise_id = $3::uuid AND status = 'pending'`,
		tokenHash, inviteID, enterpriseID); err != nil {
		r.logger.Error("Failed to refresh invitation token", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to resend invitation"}})
		return
	}

	acceptURL := fmt.Sprintf("%s/api/enterprise/accept?token=%s", r.cfg.App.BaseURL, token)
	if r.mail != nil && r.mail.IsConfigured() {
		locale := GetLocale(c)
		go func() {
			if err := r.mail.SendEnterpriseInvitation(email, enterpriseName, acceptURL, locale); err != nil {
				r.logger.Warn("Failed to resend invitation email", zap.String("email", email), zap.Error(err))
			}
		}()
	}

	if r.audit != nil {
		r.audit.Log(ctx, c.GetString("user_id"), c.GetString("email"), "enterprise", "invitation_resent", "info",
			fmt.Sprintf("Invitation resent to %s in enterprise %s", email, enterpriseID))
	}
	reason := fmt.Sprintf("Invitation resent to %s (new link, 14-day expiry)", email)
	r.logEnterpriseAudit(ctx, enterpriseAuditEntry{
		EnterpriseID: enterpriseID,
		Category:     "member",
		ChangeType:   "invitation_resent",
		FieldName:    "invitation",
		ChangedBy:    c.GetString("user_id"),
		Reason:       reason,
	})

	c.JSON(http.StatusOK, gin.H{"success": true, "message": T("msg.invitation_resent", c)})
}

// copyMyEnterpriseInvitationLink rotates the acceptance token for a pending
// invitation and returns a fresh shareable URL without sending an email.
// The raw token is never persisted (only its SHA-256 hash), so a working link
// can only be produced by rotating; this also extends an expired link's expiry.
func (r *EnterpriseRoutes) copyMyEnterpriseInvitationLink(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	inviteID := c.Param("invId")
	ctx := c.Request.Context()

	var email string
	err := r.db.QueryRow(ctx,
		`SELECT i.email
		 FROM enterprise_invitations i
		 WHERE i.id = $1::uuid AND i.enterprise_id = $2::uuid AND i.status = 'pending'`,
		inviteID, enterpriseID,
	).Scan(&email)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": T("error.invitation_no_link", c)}})
		return
	}

	// New token + fresh 14-day expiry, same policy as resend.
	tokenBytes := make([]byte, 32)
	_, _ = rand.Read(tokenBytes)
	token := hex.EncodeToString(tokenBytes)
	tokenHash := crypto.HashKeySHA256(token)

	if err := r.db.Exec(ctx,
		`UPDATE enterprise_invitations
		 SET token = $1, expires_at = NOW() + INTERVAL '14 days', updated_at = NOW()
		 WHERE id = $2::uuid AND enterprise_id = $3::uuid AND status = 'pending'`,
		tokenHash, inviteID, enterpriseID); err != nil {
		r.logger.Error("Failed to rotate invitation token", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to generate invite link"}})
		return
	}

	if r.audit != nil {
		r.audit.Log(ctx, c.GetString("user_id"), c.GetString("email"), "enterprise", "invitation_link_copied", "info",
			fmt.Sprintf("Invite link rotated for %s in enterprise %s", email, enterpriseID))
	}
	reason := fmt.Sprintf("Invite link rotated for %s (new link, 14-day expiry)", email)
	r.logEnterpriseAudit(ctx, enterpriseAuditEntry{
		EnterpriseID: enterpriseID,
		Category:     "member",
		ChangeType:   "invitation_link_rotated",
		FieldName:    "invitation",
		ChangedBy:    c.GetString("user_id"),
		Reason:       reason,
	})

	c.JSON(http.StatusOK, gin.H{
		"success":    true,
		"accept_url": fmt.Sprintf("%s/api/enterprise/accept?token=%s", r.cfg.App.BaseURL, token),
	})
}

// Exception: when the enterprise has no enterprise admin yet (fresh enterprise),
// nobody can play the EA role, so the system admin may approve an email_confirmed
// invitation directly to bootstrap the first member/admin.
func (r *EnterpriseRoutes) approveInvitationAdmin(c *gin.Context) {
	enterpriseID := c.Param("id")
	inviteID := c.Param("invId")
	approverID := c.GetString("user_id")
	ctx := c.Request.Context()

	var email, role, status string
	err := r.db.QueryRow(ctx,
		`SELECT email, role, status FROM enterprise_invitations
		 WHERE id = $1::uuid AND enterprise_id = $2::uuid AND status IN ('email_confirmed', 'ea_approved')`,
		inviteID, enterpriseID,
	).Scan(&email, &role, &status)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.invitation_needs_ea_approval", c)}})
		return
	}

	if status == "email_confirmed" {
		var adminCount int
		_ = r.db.QueryRow(ctx,
			`SELECT COUNT(*) FROM enterprise_members WHERE enterprise_id = $1::uuid AND role = 'admin' AND removed_at IS NULL`,
			enterpriseID,
		).Scan(&adminCount)
		if adminCount > 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.invitation_needs_ea_approval", c)}})
			return
		}
	}

	memberAdded := false

	// Check max_admins before proceeding with admin role
	if role == "admin" {
		var maxAdmins, currentAdmins int
		_ = r.db.QueryRow(ctx,
			`SELECT COALESCE(max_admins, 3),
			        (SELECT COUNT(*) FROM enterprise_members WHERE enterprise_id = $1::uuid AND role = 'admin' AND removed_at IS NULL)
			 FROM enterprises WHERE id = $1::uuid`,
			enterpriseID,
		).Scan(&maxAdmins, &currentAdmins)
		if currentAdmins >= maxAdmins {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf("Max admins (%d) reached", maxAdmins)}})
			return
		}
	}

	err = r.db.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE enterprise_invitations SET status = 'approved', admin_approved = TRUE, admin_approved_by = $1::uuid, admin_approved_at = NOW()
			 WHERE id = $2::uuid`,
			approverID, inviteID); err != nil {
			return err
		}

		var existingUserID *string
		if err := tx.QueryRow(ctx,
			`SELECT id::text FROM users WHERE email = $1 AND deleted_at IS NULL`, email,
		).Scan(&existingUserID); err != nil && err != pgx.ErrNoRows {
			return err
		}

		if existingUserID != nil {
			mappedRole := "user"
			if role == "admin" {
				mappedRole = "enterprise_admin"
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO enterprise_members (enterprise_id, user_id, role, joined_at) VALUES ($1, $2, $3, NOW()) ON CONFLICT (enterprise_id, user_id) DO UPDATE SET role = $3, removed_at = NULL, removed_by = NULL`,
				enterpriseID, *existingUserID, role); err != nil {
				return err
			}
			ct, err := tx.Exec(ctx,
				`UPDATE users SET enterprise_id = $1::uuid, role = $2, updated_at = NOW()
				 WHERE id = $3::uuid AND (enterprise_id IS NULL OR enterprise_id = $1::uuid)`,
				enterpriseID, mappedRole, *existingUserID)
			if err != nil {
				return err
			}
			if ct.RowsAffected() == 0 {
				return fmt.Errorf("user %s already belongs to another enterprise", *existingUserID)
			}
			// Clean up any duplicate pending invitations for this email
			if _, err := tx.Exec(ctx,
				`UPDATE enterprise_invitations SET status = 'approved', admin_approved = TRUE, admin_approved_by = $1::uuid, admin_approved_at = NOW()
				 WHERE enterprise_id = $2::uuid AND email = $3 AND status IN ('pending', 'email_confirmed', 'ea_approved')`,
				approverID, enterpriseID, email); err != nil {
				return err
			}
			memberAdded = true
		}
		return nil
	})

	if err != nil {
		r.logger.Error("Failed to approve invitation", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to approve invitation"}})
		return
	}

	// Notify an already-registered user that they have joined the enterprise
	// (same pattern as addEnterpriseMember).
	if memberAdded && r.mail != nil && r.mail.IsConfigured() {
		var enterpriseName string
		_ = r.db.QueryRow(ctx,
			`SELECT name FROM enterprises WHERE id = $1::uuid AND deleted_at IS NULL`, enterpriseID,
		).Scan(&enterpriseName)
		locale := GetLocale(c)
		go func() {
			if err := r.mail.SendEnterpriseAddedNotification(email, enterpriseName, locale); err != nil {
				r.logger.Warn("Failed to send enterprise add notification",
					zap.String("email", email),
					zap.Error(err))
			}
		}()
	}

	if r.audit != nil {
		r.audit.Log(ctx, approverID, c.GetString("email"), "enterprise", "invitation_approved", "info",
			fmt.Sprintf("Invitation %s for %s given final approval in enterprise %s", inviteID, email, enterpriseID))
	}

	oldStatus := status
	newStatus := "approved"
	if memberAdded {
		var existingUserID string
		_ = r.db.QueryRow(ctx, `SELECT id::text FROM users WHERE email = $1 AND deleted_at IS NULL`, email).Scan(&existingUserID)
		r.logEnterpriseAudit(ctx, enterpriseAuditEntry{
			EnterpriseID: enterpriseID,
			Category:     "member",
			ChangeType:   "member_added",
			FieldName:    "membership",
			UserID:       &existingUserID,
			ChangedBy:    approverID,
			OldValueStr:  &oldStatus,
			NewValueStr:  &newStatus,
			Reason:       fmt.Sprintf("Invitation approved and member added (%s)", email),
		})
	} else {
		r.logEnterpriseAudit(ctx, enterpriseAuditEntry{
			EnterpriseID: enterpriseID,
			Category:     "member",
			ChangeType:   "invitation_approved",
			FieldName:    "invitation_status",
			ChangedBy:    approverID,
			OldValueStr:  &oldStatus,
			NewValueStr:  &newStatus,
			Reason:       fmt.Sprintf("Invitation %s approved (%s)", inviteID, email),
		})
	}

	// Member is now fully approved — let the enterprise admins know.
	var entNameForNotif string
	_ = r.db.QueryRow(c.Request.Context(), `SELECT name FROM enterprises WHERE id = $1::uuid`, enterpriseID).Scan(&entNameForNotif)
	if r.notif != nil {
		r.notif.NotifyEnterpriseAdmins(c.Request.Context(), enterpriseID, "enterprise_member_joined", "normal",
			"notif.enterprise_member_joined.title", "notif.enterprise_member_joined.body",
			map[string]string{"email": email, "enterprise": entNameForNotif},
			"", "invitation", inviteID)
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// rejectInvitationAdmin allows super admin to reject an invitation for any enterprise
func (r *EnterpriseRoutes) rejectInvitationAdmin(c *gin.Context) {
	enterpriseID := c.Param("id")
	inviteID := c.Param("invId")
	rejecterID := c.GetString("user_id")

	var email, oldStatus string
	err := r.db.QueryRow(c.Request.Context(),
		`UPDATE enterprise_invitations SET status = 'rejected'
		 WHERE id = $1::uuid AND enterprise_id = $2::uuid AND status IN ('pending', 'email_confirmed', 'ea_approved')
		 RETURNING email, status`,
		inviteID, enterpriseID).Scan(&email, &oldStatus)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to reject invitation"}})
		return
	}

	newStatus := "rejected"
	r.logEnterpriseAudit(c.Request.Context(), enterpriseAuditEntry{
		EnterpriseID: enterpriseID,
		Category:     "member",
		ChangeType:   "invitation_rejected",
		FieldName:    "invitation_status",
		ChangedBy:    rejecterID,
		OldValueStr:  &oldStatus,
		NewValueStr:  &newStatus,
		Reason:       fmt.Sprintf("Invitation %s rejected (%s)", inviteID, email),
	})

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// acceptInvitationPage shows the acceptance page
func (r *EnterpriseRoutes) acceptInvitationPage(c *gin.Context) {
	token := c.Query("token")
	if token == "" {
		c.String(http.StatusOK, "<html><body><h2>"+T("error.invalid_invitation_link", c)+"</h2></body></html>")
		return
	}

	// Verify token
	var enterpriseName, email, inviteName string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT e.name, i.email, COALESCE(i.name, '')
		 FROM enterprise_invitations i
		 JOIN enterprises e ON i.enterprise_id = e.id
		 WHERE i.token = $1 AND i.status = 'pending' AND i.expires_at > NOW()`,
		crypto.HashKeySHA256(token),
	).Scan(&enterpriseName, &email, &inviteName)
	if err != nil {
		c.String(http.StatusOK, "<html><body><h2>"+T("error.invitation_expired", c)+"</h2></body></html>")
		return
	}

	// Render standalone template (self-contained CSS, no layout needed)
	locale := GetLocale(c)
	tmpl, tmplErr := template.New("accept").Funcs(template.FuncMap{
		"t": func(key string) string {
			if routeI18n == nil {
				return key
			}
			return routeI18n.T(key, locale)
		},
	}).ParseFS(web.FS, "templates/enterprise-accept.html")
	if tmplErr != nil {
		c.String(http.StatusInternalServerError, "Template error: "+tmplErr.Error())
		return
	}

	c.Header("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprint(c.Writer, "<!DOCTYPE html><html lang=\""+locale+"\"><head><meta charset=\"UTF-8\"/><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"/><title>"+T("page.accept_invitation_title", c)+"</title><link rel=\"stylesheet\" href=\"/static/css/tailwind.min.css\"/><link rel=\"stylesheet\" href=\"/static/css/app.css\"/><script src=\"/static/js/app-layout.js\"></script></head><body>")
	_ = tmpl.ExecuteTemplate(c.Writer, "content", gin.H{
		"Token":          token,
		"EnterpriseName": enterpriseName,
		"Email":          email,
		"Name":           inviteName,
	})
	_, _ = fmt.Fprint(c.Writer, "</body></html>")
}

// rowQuerier is satisfied by *db.DB and pgx.Tx.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row
}

// enterpriseSeats is the capacity picture of an enterprise used by every path
// that adds a member.
type enterpriseSeats struct {
	Members        int // active members
	Pending        int // open invitations whose e-mail is not yet a member
	MaxMembers     int
	Admins         int
	MaxAdmins      int
	AllowedDomains string // platform-approved auto-join domains (comma separated)
}

// loadEnterpriseSeats counts effective members = active members + pending
// invitations that don't already have an active member. A failed query leaves
// the zero value, matching the previous inline behaviour of addEnterpriseMember
// (MaxMembers 0 then blocks adding, i.e. it fails closed).
func loadEnterpriseSeats(ctx context.Context, q rowQuerier, enterpriseID string) enterpriseSeats {
	var s enterpriseSeats
	_ = q.QueryRow(ctx,
		`SELECT
			(SELECT COUNT(*) FROM enterprise_members WHERE enterprise_id = e.id AND removed_at IS NULL),
			(SELECT COUNT(*) FROM enterprise_invitations ei
			 WHERE ei.enterprise_id = e.id
			   AND ei.status IN ('pending', 'email_confirmed', 'ea_approved')
			   AND NOT EXISTS (
			     SELECT 1 FROM enterprise_members em
			     JOIN users u ON em.user_id = u.id
			     WHERE em.enterprise_id = ei.enterprise_id
			       AND em.removed_at IS NULL
			       AND u.email = ei.email
			       AND u.deleted_at IS NULL
			   )),
			COALESCE(e.max_members, 50),
			(SELECT COUNT(*) FROM enterprise_members WHERE enterprise_id = e.id AND role = 'admin' AND removed_at IS NULL),
			COALESCE(e.max_admins, 3),
			COALESCE(e.allowed_domains, '')
		 FROM enterprises e WHERE e.id = $1::uuid AND e.deleted_at IS NULL`, enterpriseID,
	).Scan(&s.Members, &s.Pending, &s.MaxMembers, &s.Admins, &s.MaxAdmins, &s.AllowedDomains)
	return s
}

// domainMatches checks if an email domain is in the allowed domains list
func domainMatches(email, allowedDomains string) bool {
	if allowedDomains == "" {
		return false
	}
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return false
	}
	emailDomain := strings.ToLower(parts[1])
	for _, d := range strings.Split(allowedDomains, ",") {
		if strings.TrimSpace(strings.ToLower(d)) == emailDomain {
			return true
		}
	}
	return false
}

// acceptInvitation accepts an invitation
func (r *EnterpriseRoutes) acceptInvitation(c *gin.Context) {
	token := c.PostForm("token")
	password := c.PostForm("password")
	name := c.PostForm("name")

	if token == "" || password == "" {
		q := url.Values{}
		q.Set("toast", "error")
		q.Set("message", T("error.all_fields_required", c))
		c.Redirect(http.StatusFound, "/api/enterprise/accept?token="+token+"&"+q.Encode())
		return
	}

	// Verify token (join inviter to learn whether the inviter is an EA)
	var inviteID, enterpriseID, email, role, inviterRole string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT i.id::text, i.enterprise_id::text, i.email, i.role, COALESCE(u.role, '')
		 FROM enterprise_invitations i
		 LEFT JOIN users u ON i.invited_by = u.id
		 WHERE i.token = $1 AND i.status = 'pending' AND i.expires_at > NOW()`,
		crypto.HashKeySHA256(token),
	).Scan(&inviteID, &enterpriseID, &email, &role, &inviterRole)
	if err != nil {
		q := url.Values{}
		q.Set("toast", "error")
		q.Set("message", T("error.invitation_invalid_or_expired", c))
		c.Redirect(http.StatusFound, "/api/enterprise/accept?"+q.Encode())
		return
	}

	// Get enterprise allowed domains
	var allowedDomains string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COALESCE(allowed_domains, '') FROM enterprises WHERE id = $1::uuid`, enterpriseID,
	).Scan(&allowedDomains)

	isDomainMatched := domainMatches(email, allowedDomains)
	inviterIsEA := inviterRole == "enterprise_admin"
	// Auto-join only when the domain is whitelisted AND the inviter is an EA
	// (an EA invitation counts as implicit EA approval).
	autoJoin := isDomainMatched && inviterIsEA

	// Map invitation role to users.role (different enums)
	invRole := "user"
	if role == "admin" {
		invRole = "enterprise_admin"
	}

	// Check if user already exists
	var existingUserID *string
	err = r.db.QueryRow(c.Request.Context(),
		`SELECT id::text FROM users WHERE email = $1 AND deleted_at IS NULL`, email,
	).Scan(&existingUserID)

	var memberUserID string
	if err == nil && existingUserID != nil {
		memberUserID = *existingUserID
	} else {
		// Create new user (without enterprise_id for non-domain — will be set on approval)
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			q := url.Values{}
			q.Set("toast", "error")
			q.Set("message", T("error.register_encryption_failed", c))
			c.Redirect(http.StatusFound, "/api/enterprise/accept?token="+token+"&"+q.Encode())
			return
		}
		passwordHash := string(hash)

		if autoJoin {
			err = r.db.QueryRow(c.Request.Context(),
				`INSERT INTO users (email, name, password_hash, role, enterprise_id)
				 VALUES ($1, $2, $3, $4, $5::uuid) RETURNING id::text`,
				email, name, passwordHash, invRole, enterpriseID,
			).Scan(&memberUserID)
		} else {
			// Pending approval: the enterprise role is only granted when the
			// invitation is approved (EA or SA), never at registration time.
			err = r.db.QueryRow(c.Request.Context(),
				`INSERT INTO users (email, name, password_hash, role)
				 VALUES ($1, $2, $3, 'user') RETURNING id::text`,
				email, name, passwordHash,
			).Scan(&memberUserID)
		}
		if err != nil {
			q := url.Values{}
			q.Set("toast", "error")
			q.Set("message", "Registration failed")
			c.Redirect(http.StatusFound, "/api/enterprise/accept?token="+token+"&"+q.Encode())
			return
		}

		// New users registering via an enterprise invitation get the same
		// welcome bonus as the regular registration paths.
		if r.reward != nil {
			r.reward.ApplyWelcomeBonus(c.Request.Context(), memberUserID, email)
		}
	}

	if autoJoin {
		// Check max_admins before auto-joining with admin role
		if role == "admin" {
			var maxAdmins, currentAdmins int
			_ = r.db.QueryRow(c.Request.Context(),
				`SELECT COALESCE(max_admins, 3),
				        (SELECT COUNT(*) FROM enterprise_members WHERE enterprise_id = $1::uuid AND role = 'admin' AND removed_at IS NULL)`,
				enterpriseID,
			).Scan(&maxAdmins, &currentAdmins)
			if currentAdmins >= maxAdmins {
				q := url.Values{}
				q.Set("toast", "error")
				q.Set("message", fmt.Sprintf("Max admins (%d) reached", maxAdmins))
				c.Redirect(http.StatusFound, "/api/enterprise/accept?token="+token+"&"+q.Encode())
				return
			}
		}

		// Domain matched and invited by an EA — auto-join.
		// Guard against hijacking existing users who already belong to a
		// different enterprise: the UPDATE only touches rows whose
		// enterprise_id is NULL or already points at this enterprise.
		if existingUserID != nil {
			if err := r.db.Exec(c.Request.Context(),
				`UPDATE users SET enterprise_id = $1::uuid, role = $2, updated_at = NOW()
				 WHERE id = $3::uuid AND (enterprise_id IS NULL OR enterprise_id = $1::uuid)`,
				enterpriseID, invRole, memberUserID); err != nil {
				r.logger.Error("Failed to update user enterprise", zap.Error(err))
			}
		}
		if err := r.db.Exec(c.Request.Context(),
			`INSERT INTO enterprise_members (enterprise_id, user_id, role, joined_at)
			 VALUES ($1::uuid, $2::uuid, $3, NOW())
			 ON CONFLICT (enterprise_id, user_id) DO UPDATE SET role = $3, removed_at = NULL, removed_by = NULL`,
			enterpriseID, memberUserID, role); err != nil {
			r.logger.Error("Failed to add enterprise member", zap.Error(err))
		}
		if err := r.db.Exec(c.Request.Context(),
			`UPDATE enterprise_invitations SET status = 'accepted', accepted_at = NOW()
			 WHERE enterprise_id = $1::uuid AND email = $2 AND status IN ('pending', 'email_confirmed', 'ea_approved')`,
			enterpriseID, email); err != nil {
			r.logger.Error("Failed to update invitation status", zap.Error(err))
		}

		oldStatus := "invited"
		newStatus := "joined"
		r.logEnterpriseAudit(c.Request.Context(), enterpriseAuditEntry{
			EnterpriseID: enterpriseID,
			Category:     "member",
			ChangeType:   "member_joined",
			FieldName:    "membership",
			UserID:       &memberUserID,
			ChangedBy:    memberUserID,
			OldValueStr:  &oldStatus,
			NewValueStr:  &newStatus,
			Reason:       fmt.Sprintf("Invitation accepted and auto-joined (%s)", email),
		})

		r.notifyEnterpriseAdmins(c, enterpriseID, "enterprise_member_joined", "normal",
			"notif.enterprise_member_joined.title", "notif.enterprise_member_joined.body",
			map[string]string{"email": email},
			"", "member", memberUserID)

		q := url.Values{}
		q.Set("toast", "success")
		q.Set("message", T("msg.register_success_login_prompt", c))
		c.Redirect(http.StatusFound, "/login?"+q.Encode())
	} else {
		// No auto-join — registration proves the email works; wait for approval.
		// An EA invitation counts as implicit EA approval, so a non-matching
		// domain skips straight to ea_approved (awaiting SA final approval).
		newStatus := "email_confirmed"
		if inviterIsEA && !isDomainMatched {
			newStatus = "ea_approved"
		}
		if err := r.db.Exec(c.Request.Context(),
			`UPDATE enterprise_invitations SET status = $2, email_confirmed = TRUE, email_confirmed_at = NOW() WHERE id = $1::uuid`,
			inviteID, newStatus); err != nil {
			r.logger.Error("Failed to update invitation status", zap.Error(err))
		}

		oldStatus := "pending"
		r.logEnterpriseAudit(c.Request.Context(), enterpriseAuditEntry{
			EnterpriseID: enterpriseID,
			Category:     "member",
			ChangeType:   "invitation_accepted",
			FieldName:    "invitation_status",
			UserID:       &memberUserID,
			ChangedBy:    memberUserID,
			OldValueStr:  &oldStatus,
			NewValueStr:  &newStatus,
			Reason:       fmt.Sprintf("Invitation accepted by user (%s)", email),
		})

		if r.notif != nil {
			if newStatus == "ea_approved" {
				// Non-domain email invited by EA: jumps straight to awaiting SA.
				var entName string
				_ = r.db.QueryRow(c.Request.Context(), `SELECT name FROM enterprises WHERE id = $1::uuid`, enterpriseID).Scan(&entName)
				r.notif.NotifySystemAdmins(c.Request.Context(), "enterprise_join_confirm", "high",
					"notif.enterprise_join_confirm.title", "notif.enterprise_join_confirm.body",
					map[string]string{"email": email, "enterprise": entName},
					"/admin/enterprise", "invitation", inviteID)
			} else {
				r.notifyEnterpriseAdmins(c, enterpriseID, "enterprise_member_pending_ea", "high",
					"notif.enterprise_member_pending_ea.title", "notif.enterprise_member_pending_ea.body",
					map[string]string{"email": email},
					"/enterprise/members", "invitation", inviteID)
			}
		}

		q := url.Values{}
		q.Set("toast", "success")
		if newStatus == "ea_approved" {
			q.Set("message", T("msg.invitation_pending_sa", c))
		} else {
			q.Set("message", T("msg.email_confirmed_wait_approval", c))
		}
		c.Redirect(http.StatusFound, "/login?"+q.Encode())
	}
}

// removeMember removes a member from enterprise (admin route)
func (r *EnterpriseRoutes) removeMember(c *gin.Context) {
	enterpriseID := c.Param("id")
	userID := c.Param("userId")
	currentUserID := c.GetString("user_id")

	// Collect key hashes before the transaction so the cache can be
	// invalidated after commit.
	var recalledKeyHashes []string
	if hashRows, qErr := r.db.Query(c.Request.Context(),
		`SELECT key_hash FROM api_keys
		 WHERE enterprise_id = $1::uuid AND assigned_user_id = $2::uuid AND deleted_at IS NULL AND status = 'active'`,
		enterpriseID, userID); qErr == nil {
		for hashRows.Next() {
			var h string
			if scanErr := hashRows.Scan(&h); scanErr == nil {
				recalledKeyHashes = append(recalledKeyHashes, h)
			}
		}
		hashRows.Close()
	}

	if err := r.db.WithTx(c.Request.Context(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(c.Request.Context(),
			`UPDATE enterprise_members SET removed_at = NOW(), removed_by = $1::uuid
			 WHERE enterprise_id = $2::uuid AND user_id = $3::uuid AND removed_at IS NULL`,
			currentUserID, enterpriseID, userID); err != nil {
			return err
		}
		if _, err := tx.Exec(c.Request.Context(),
			`UPDATE users SET enterprise_id = NULL, role = 'user', updated_at = NOW()
		 WHERE id = $1::uuid AND enterprise_id = $2::uuid`,
			userID, enterpriseID); err != nil {
			return err
		}
		if _, err := tx.Exec(c.Request.Context(),
			`UPDATE api_keys SET status = 'disabled', assigned_user_id = NULL, updated_at = NOW()
			 WHERE enterprise_id = $1::uuid AND assigned_user_id = $2::uuid`,
			enterpriseID, userID); err != nil {
			return err
		}
		// Return the member's unused enterprise-quota grant to the pool
		// before they leave the enterprise context (S-U6).
		if _, err := reclaimMemberEnterpriseQuota(c.Request.Context(), tx, userID, enterpriseID); err != nil {
			return err
		}
		return nil
	}); err != nil {
		r.logger.Error("Failed to remove member", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to remove member"}})
		return
	}
	for _, h := range recalledKeyHashes {
		invalidateValidateCache(c.Request.Context(), r.db, r.logger, h)
	}
	// The member's personal keys lose their enterprise context
	// (users.enterprise_id cleared above); drop their cached validate
	// results too so the change takes effect immediately.
	invalidateUserValidateCaches(c.Request.Context(), r.db, r.logger, userID)

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), currentUserID, c.GetString("email"), "enterprise", "member_removed", "warning",
			fmt.Sprintf("Member removed from enterprise %s: %s", enterpriseID, userID))
	}

	oldStatus := "active"
	newStatus := "removed"
	r.logEnterpriseAudit(c.Request.Context(), enterpriseAuditEntry{
		EnterpriseID: enterpriseID,
		Category:     "member",
		ChangeType:   "member_removed",
		FieldName:    "membership",
		UserID:       &userID,
		ChangedBy:    currentUserID,
		OldValueStr:  &oldStatus,
		NewValueStr:  &newStatus,
		Reason:       "Member removed from enterprise",
	})

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// promoteDemoteMember changes a member's role with audit logging
func (r *EnterpriseRoutes) promoteDemoteMember(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	userID := c.Param("userId")
	currentUserID := c.GetString("user_id")

	var req struct {
		Role string `json:"role" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "role is required"}})
		return
	}

	if req.Role != "member" && req.Role != "admin" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "role must be 'member' or 'admin'"}})
		return
	}

	if userID == currentUserID {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Cannot change your own role"}})
		return
	}

	// Get current role
	var oldRole string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT role FROM enterprise_members WHERE enterprise_id = $1::uuid AND user_id = $2::uuid AND removed_at IS NULL`,
		enterpriseID, userID,
	).Scan(&oldRole)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Member not found"}})
		return
	}

	if oldRole == req.Role {
		// Idempotent: no role change needed.
		c.JSON(http.StatusOK, gin.H{"success": true, "role": req.Role})
		return
	}

	// Check max_admins if promoting to admin
	if req.Role == "admin" {
		var maxAdmins, currentAdmins int
		err := r.db.QueryRow(c.Request.Context(),
			`SELECT COALESCE(max_admins, 3),
			        (SELECT COUNT(*) FROM enterprise_members WHERE enterprise_id = $1::uuid AND role = 'admin' AND removed_at IS NULL)`,
			enterpriseID,
		).Scan(&maxAdmins, &currentAdmins)
		if err == nil && currentAdmins >= maxAdmins {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf("Max admins (%d) reached", maxAdmins)}})
			return
		}
	}

	// Map to users.role
	mappedRole := "user"
	if req.Role == "admin" {
		mappedRole = "enterprise_admin"
	}

	// Update enterprise_members.role
	err = r.db.Exec(c.Request.Context(),
		`UPDATE enterprise_members SET role = $1 WHERE enterprise_id = $2::uuid AND user_id = $3::uuid AND removed_at IS NULL`,
		req.Role, enterpriseID, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update role"}})
		return
	}

	// Sync users.role
	err = r.db.Exec(c.Request.Context(),
		`UPDATE users SET role = $1, updated_at = NOW() WHERE id = $2::uuid`,
		mappedRole, userID)
	if err != nil {
		r.logger.Error("Failed to sync user role", zap.Error(err))
	}

	// Log role change
	if err := r.db.Exec(c.Request.Context(),
		`INSERT INTO enterprise_member_role_log (enterprise_id, user_id, old_role, new_role, changed_by, reason)
		 VALUES ($1::uuid, $2::uuid, $3, $4, $5::uuid, 'role_change')`,
		enterpriseID, userID, oldRole, req.Role, currentUserID); err != nil {
		r.logger.Error("Failed to log role change", zap.Error(err))
	}
	// Also log to enterprise_audit_log for unified enterprise audit timeline.
	oldVal := int64(0)
	newVal := int64(0)
	if oldRole == "admin" {
		oldVal = 1
	}
	if req.Role == "admin" {
		newVal = 1
	}
	r.logEnterpriseAudit(c.Request.Context(), enterpriseAuditEntry{
		EnterpriseID: enterpriseID,
		Category:     "role",
		ChangeType:   "member_role_changed",
		FieldName:    "role",
		UserID:       &userID,
		ChangedBy:    currentUserID,
		OldValue:     &oldVal,
		NewValue:     &newVal,
		Reason:       "Member role changed",
	})

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), currentUserID, c.GetString("email"), "enterprise", "role_changed", "info",
			fmt.Sprintf("Member %s role changed from %s to %s in enterprise %s", userID, oldRole, req.Role, enterpriseID))
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "old_role": oldRole, "new_role": req.Role})
}

// quitEnterprise allows a member to quit their enterprise
func (r *EnterpriseRoutes) quitEnterprise(c *gin.Context) {
	userID := c.GetString("user_id")
	enterpriseID := c.GetString("enterprise_id")

	// Check if this is the last admin
	var adminRole string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT role FROM enterprise_members WHERE enterprise_id = $1::uuid AND user_id = $2::uuid AND removed_at IS NULL`,
		enterpriseID, userID,
	).Scan(&adminRole)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Not a member of this enterprise"}})
		return
	}

	if adminRole == "admin" {
		// Check if this is the last admin
		var adminCount int
		_ = r.db.QueryRow(c.Request.Context(),
			`SELECT COUNT(*) FROM enterprise_members WHERE enterprise_id = $1::uuid AND role = 'admin' AND removed_at IS NULL`,
			enterpriseID,
		).Scan(&adminCount)

		if adminCount <= 1 {
			// Last admin quitting — set enterprise to admin_vacant
			if err := r.db.Exec(c.Request.Context(),
				`UPDATE enterprises SET status = 'admin_vacant', admin_vacant_since = NOW() WHERE id = $1::uuid`,
				enterpriseID); err != nil {
				r.logger.Error("Failed to set admin_vacant status", zap.Error(err))
			}
		}
	}

	// Collect key hashes before the transaction so they can be invalidated afterwards.
	var keyHashes []string
	hashRows, err := r.db.Query(c.Request.Context(),
		`SELECT key_hash FROM api_keys WHERE enterprise_id = $1::uuid AND assigned_user_id = $2::uuid AND deleted_at IS NULL`,
		enterpriseID, userID)
	if err == nil {
		defer hashRows.Close()
		for hashRows.Next() {
			var h string
			if hashRows.Scan(&h) == nil {
				keyHashes = append(keyHashes, h)
			}
		}
	}

	if err := r.db.WithTx(c.Request.Context(), func(tx pgx.Tx) error {
		// Soft-delete member
		if _, err := tx.Exec(c.Request.Context(),
			`UPDATE enterprise_members SET removed_at = NOW(), removed_by = $1::uuid WHERE enterprise_id = $2::uuid AND user_id = $1::uuid AND removed_at IS NULL`,
			userID, enterpriseID); err != nil {
			return err
		}
		// Update user
		if _, err := tx.Exec(c.Request.Context(),
			`UPDATE users SET enterprise_id = NULL, role = 'user', updated_at = NOW() WHERE id = $1::uuid`,
			userID); err != nil {
			return err
		}
		// Recall enterprise keys
		if _, err := tx.Exec(c.Request.Context(),
			`UPDATE api_keys SET status = 'disabled', assigned_user_id = NULL WHERE enterprise_id = $1::uuid AND assigned_user_id = $2::uuid`,
			enterpriseID, userID); err != nil {
			return err
		}
		// Return the member's unused enterprise-quota grant to the pool
		// before they leave the enterprise context (S-U6). The member is
		// quitting voluntarily — same reclaim logic as the admin-driven
		// removal path below.
		if _, err := reclaimMemberEnterpriseQuota(c.Request.Context(), tx, userID, enterpriseID); err != nil {
			return err
		}
		return nil
	}); err != nil {
		r.logger.Error("Failed to quit enterprise", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to quit enterprise"}})
		return
	}

	for _, h := range keyHashes {
		invalidateValidateCache(c.Request.Context(), r.db, r.logger, h)
	}

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), userID, c.GetString("email"), "enterprise", "member_quit", "info",
			fmt.Sprintf("Member quit enterprise %s", enterpriseID))
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// approveInvitation approves a pending invitation (for non-domain emails).
// The invitation update, user assignment, and enterprise_members sync run in one transaction.
func (r *EnterpriseRoutes) approveInvitation(c *gin.Context) {
	inviteID := c.Param("invId")
	approverID := c.GetString("user_id")
	ctx := c.Request.Context()

	var enterpriseID, email, role string
	err := r.db.QueryRow(ctx,
		`SELECT enterprise_id::text, email, role FROM enterprise_invitations
		 WHERE id = $1::uuid AND status = 'email_confirmed'`,
		inviteID,
	).Scan(&enterpriseID, &email, &role)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invitation not found or not in email_confirmed state"}})
		return
	}

	// Enterprise admins may only approve invitations for their own enterprise.
	callerEnterpriseID := c.GetString("enterprise_id")
	if callerEnterpriseID == "" || callerEnterpriseID != enterpriseID {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "You can only approve invitations for your own enterprise"}})
		return
	}

	// Emails outside the enterprise's allowed domains need a second
	// approval level: EA approval only moves the invitation to ea_approved,
	// a system admin gives the final approval.
	var enterpriseName, allowedDomains string
	_ = r.db.QueryRow(ctx,
		`SELECT name, COALESCE(allowed_domains, '') FROM enterprises WHERE id = $1::uuid`, enterpriseID,
	).Scan(&enterpriseName, &allowedDomains)
	if !domainMatches(email, allowedDomains) {
		if err := r.db.Exec(ctx,
			`UPDATE enterprise_invitations SET status = 'ea_approved', updated_at = NOW() WHERE id = $1::uuid`,
			inviteID); err != nil {
			r.logger.Error("Failed to mark invitation ea_approved", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to approve invitation"}})
			return
		}
		if r.audit != nil {
			r.audit.Log(ctx, approverID, c.GetString("email"), "enterprise", "invitation_ea_approved", "warning",
				fmt.Sprintf("Invitation %s for %s approved by EA, awaiting system admin final approval", inviteID, email))
		}

		oldStatus := "email_confirmed"
		newStatus := "ea_approved"
		r.logEnterpriseAudit(ctx, enterpriseAuditEntry{
			EnterpriseID: enterpriseID,
			Category:     "member",
			ChangeType:   "invitation_ea_approved",
			FieldName:    "invitation_status",
			ChangedBy:    approverID,
			OldValueStr:  &oldStatus,
			NewValueStr:  &newStatus,
			Reason:       fmt.Sprintf("Invitation %s approved by EA, awaiting system admin (%s)", inviteID, email),
		})

		if r.notif != nil {
			r.notif.NotifySystemAdmins(ctx, "enterprise_join_confirm", "high",
				"notif.enterprise_join_confirm.title", "notif.enterprise_join_confirm.body",
				map[string]string{"email": email, "enterprise": enterpriseName},
				"/admin/enterprise", "invitation", inviteID)
		}

		c.JSON(http.StatusOK, gin.H{"success": true, "pending_sa_approval": true, "message": T("msg.invitation_pending_sa", c)})
		return
	}

	memberAdded := false
	err = r.db.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE enterprise_invitations SET status = 'approved', admin_approved = TRUE, admin_approved_by = $1::uuid, admin_approved_at = NOW()
			 WHERE id = $2::uuid`,
			approverID, inviteID); err != nil {
			return err
		}

		var existingUserID *string
		if err := tx.QueryRow(ctx,
			`SELECT id::text FROM users WHERE email = $1 AND deleted_at IS NULL`, email,
		).Scan(&existingUserID); err != nil && err != pgx.ErrNoRows {
			return err
		}

		if existingUserID != nil {
			mappedRole := "user"
			if role == "admin" {
				mappedRole = "enterprise_admin"
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO enterprise_members (enterprise_id, user_id, role, joined_at) VALUES ($1::uuid, $2::uuid, $3, NOW()) ON CONFLICT (enterprise_id, user_id) WHERE removed_at IS NULL DO UPDATE SET role = $3, removed_at = NULL, removed_by = NULL`,
				enterpriseID, *existingUserID, role); err != nil {
				return err
			}
			ct, err := tx.Exec(ctx,
				`UPDATE users SET enterprise_id = $1::uuid, role = $2, updated_at = NOW()
				 WHERE id = $3::uuid AND (enterprise_id IS NULL OR enterprise_id = $1::uuid)`,
				enterpriseID, mappedRole, *existingUserID)
			if err != nil {
				return err
			}
			if ct.RowsAffected() == 0 {
				return fmt.Errorf("user %s already belongs to another enterprise", *existingUserID)
			}
			if _, err := tx.Exec(ctx,
				`UPDATE enterprise_invitations SET status = 'approved', admin_approved = TRUE, admin_approved_by = $1::uuid, admin_approved_at = NOW()
				 WHERE enterprise_id = $2::uuid AND email = $3 AND status IN ('pending', 'email_confirmed', 'ea_approved')`,
				approverID, enterpriseID, email); err != nil {
				return err
			}
			memberAdded = true
		}
		return nil
	})

	if err != nil {
		r.logger.Error("Failed to approve invitation", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to approve invitation"}})
		return
	}

	// Notify an already-registered user that they have joined the enterprise
	// (same pattern as addEnterpriseMember).
	if memberAdded && r.mail != nil && r.mail.IsConfigured() {
		locale := GetLocale(c)
		go func() {
			if err := r.mail.SendEnterpriseAddedNotification(email, enterpriseName, locale); err != nil {
				r.logger.Warn("Failed to send enterprise add notification",
					zap.String("email", email),
					zap.Error(err))
			}
		}()
	}

	if r.audit != nil {
		r.audit.Log(ctx, approverID, c.GetString("email"), "enterprise", "invitation_approved", "info",
			fmt.Sprintf("Invitation %s approved for %s", inviteID, email))
	}

	oldStatus := "email_confirmed"
	newStatus := "approved"
	if memberAdded {
		var existingUserID string
		_ = r.db.QueryRow(ctx, `SELECT id::text FROM users WHERE email = $1 AND deleted_at IS NULL`, email).Scan(&existingUserID)
		r.logEnterpriseAudit(ctx, enterpriseAuditEntry{
			EnterpriseID: enterpriseID,
			Category:     "member",
			ChangeType:   "member_added",
			FieldName:    "membership",
			UserID:       &existingUserID,
			ChangedBy:    approverID,
			OldValueStr:  &oldStatus,
			NewValueStr:  &newStatus,
			Reason:       fmt.Sprintf("Invitation approved and member added (%s)", email),
		})
	} else {
		r.logEnterpriseAudit(ctx, enterpriseAuditEntry{
			EnterpriseID: enterpriseID,
			Category:     "member",
			ChangeType:   "invitation_approved",
			FieldName:    "invitation_status",
			ChangedBy:    approverID,
			OldValueStr:  &oldStatus,
			NewValueStr:  &newStatus,
			Reason:       fmt.Sprintf("Invitation %s approved (%s)", inviteID, email),
		})
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// rejectInvitation rejects a pending invitation and records an audit log.
func (r *EnterpriseRoutes) rejectInvitation(c *gin.Context) {
	inviteID := c.Param("invId")
	rejecterID := c.GetString("user_id")
	ctx := c.Request.Context()

	// Enterprise admins may only reject invitations for their own enterprise.
	// The enterprise_id filter in the WHERE clause ensures a cross-tenant
	// rejection silently matches zero rows (returned as "not found").
	callerEnterpriseID := c.GetString("enterprise_id")
	if callerEnterpriseID == "" {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "You can only reject invitations for your own enterprise"}})
		return
	}

	var email, oldStatus string
	err := r.db.QueryRow(ctx,
		`UPDATE enterprise_invitations SET status = 'rejected'
		 WHERE id = $1::uuid AND enterprise_id = $2::uuid AND status IN ('pending', 'email_confirmed', 'ea_approved')
		 RETURNING email, status`,
		inviteID, callerEnterpriseID).Scan(&email, &oldStatus)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Invitation not found"}})
		return
	}

	if r.audit != nil {
		r.audit.Log(ctx, rejecterID, c.GetString("email"), "enterprise", "invitation_rejected", "warning",
			fmt.Sprintf("Invitation %s for %s rejected", inviteID, email))
	}

	newStatus := "rejected"
	r.logEnterpriseAudit(ctx, enterpriseAuditEntry{
		EnterpriseID: callerEnterpriseID,
		Category:     "member",
		ChangeType:   "invitation_rejected",
		FieldName:    "invitation_status",
		ChangedBy:    rejecterID,
		OldValueStr:  &oldStatus,
		NewValueStr:  &newStatus,
		Reason:       fmt.Sprintf("Invitation %s rejected (%s)", inviteID, email),
	})

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// appointAdmin appoints a new enterprise admin (for admin_vacant enterprises)
func (r *EnterpriseRoutes) appointAdmin(c *gin.Context) {
	enterpriseID := c.Param("id")

	var req struct {
		UserID string `json:"user_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "user_id is required"}})
		return
	}

	// Verify enterprise is admin_vacant
	var status string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT status FROM enterprises WHERE id = $1::uuid AND deleted_at IS NULL`,
		enterpriseID,
	).Scan(&status)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Enterprise not found"}})
		return
	}
	if status != "admin_vacant" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Enterprise is not admin_vacant"}})
		return
	}

	// Verify user is a member
	var memberRole string
	err = r.db.QueryRow(c.Request.Context(),
		`SELECT role FROM enterprise_members WHERE enterprise_id = $1::uuid AND user_id = $2::uuid AND removed_at IS NULL`,
		enterpriseID, req.UserID,
	).Scan(&memberRole)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "User is not a member of this enterprise"}})
		return
	}

	// Promote to admin
	err = r.db.Exec(c.Request.Context(),
		`UPDATE enterprise_members SET role = 'admin' WHERE enterprise_id = $1::uuid AND user_id = $2::uuid AND removed_at IS NULL`,
		enterpriseID, req.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to appoint admin"}})
		return
	}

	// Sync users.role
	err = r.db.Exec(c.Request.Context(),
		`UPDATE users SET role = 'enterprise_admin', updated_at = NOW() WHERE id = $1::uuid`,
		req.UserID)
	if err != nil {
		r.logger.Error("Failed to sync user role", zap.Error(err))
	}

	// Resolve admin_vacant
	err = r.db.Exec(c.Request.Context(),
		`UPDATE enterprises SET status = 'active', admin_vacant_since = NULL WHERE id = $1::uuid`,
		enterpriseID)
	if err != nil {
		r.logger.Error("Failed to resolve admin_vacant", zap.Error(err))
	}

	// Log role change
	// Log role change
	if err := r.db.Exec(c.Request.Context(),
		`INSERT INTO enterprise_member_role_log (enterprise_id, user_id, old_role, new_role, changed_by, reason)
		 VALUES ($1::uuid, $2::uuid, $3, 'admin', $4::uuid, 'appointed_admin')`,
		enterpriseID, req.UserID, memberRole, c.GetString("user_id")); err != nil {
		r.logger.Error("Failed to log admin appointment", zap.Error(err))
	}

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "enterprise", "admin_appointed", "info",
			fmt.Sprintf("Admin appointed for enterprise %s: user %s", enterpriseID, req.UserID))
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// listMyEnterpriseInvitations lists pending invitations for the current user's enterprise
func (r *EnterpriseRoutes) listMyEnterpriseInvitations(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")

	rows, err := r.db.Query(c.Request.Context(), `
		SELECT i.id::text, i.email, COALESCE(i.name, ''), i.role, i.status, i.expires_at, i.created_at,
		       i.email_confirmed, i.admin_approved
		FROM enterprise_invitations i
		WHERE i.enterprise_id = $1::uuid
		ORDER BY i.created_at DESC LIMIT 50`,
		enterpriseID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list invitations"}})
		return
	}
	defer rows.Close()

	invitations := []gin.H{}
	for rows.Next() {
		var id, email, name, role, status string
		var expiresAt, createdAt interface{}
		var emailConfirmed, adminApproved bool
		if err := rows.Scan(&id, &email, &name, &role, &status, &expiresAt, &createdAt, &emailConfirmed, &adminApproved); err != nil {
			continue
		}
		invitations = append(invitations, gin.H{
			"id": id, "email": email, "name": name, "role": role, "status": status,
			"expires_at": expiresAt, "created_at": createdAt,
			"email_confirmed": emailConfirmed, "admin_approved": adminApproved,
		})
	}
	c.JSON(http.StatusOK, gin.H{"invitations": invitations})
}

// addEnterpriseMember adds a user to enterprise by email
func (r *EnterpriseRoutes) addEnterpriseMember(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	currentUserID := c.GetString("user_id")

	var req struct {
		Email string `json:"email" binding:"required"`
		Name  string `json:"name"`
		Role  string `json:"role"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Email is required"}})
		return
	}

	email := strings.ToLower(strings.TrimSpace(req.Email))
	name := strings.TrimSpace(req.Name)
	role := strings.TrimSpace(req.Role)
	if role == "" {
		role = "member"
	}
	if role != "member" && role != "admin" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Role must be member or admin"}})
		return
	}

	// Map enterprise_members.role → users.role
	usersRole := "user"
	if role == "admin" {
		usersRole = "enterprise_admin"
	}

	// Check enterprise limits (shared with SSO just-in-time provisioning so both
	// entry points count seats the same way).
	seats := loadEnterpriseSeats(c.Request.Context(), r.db, enterpriseID)
	memberCount, pendingCount, maxMembers := seats.Members, seats.Pending, seats.MaxMembers
	adminCount, maxAdmins, allowedDomains := seats.Admins, seats.MaxAdmins, seats.AllowedDomains
	effectiveMembers := memberCount + pendingCount
	if effectiveMembers >= maxMembers {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf("Enterprise member limit reached (%d active + %d pending = %d/%d)", memberCount, pendingCount, effectiveMembers, maxMembers)}})
		return
	}
	if role == "admin" && adminCount >= maxAdmins {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Enterprise admin limit reached"}})
		return
	}

	// Load enterprise name for emails
	var enterpriseName string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT name FROM enterprises WHERE id = $1::uuid AND deleted_at IS NULL`, enterpriseID,
	).Scan(&enterpriseName)

	// Prevent duplicate pending invitation for the same email.
	var existingPendingID *string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT id::text FROM enterprise_invitations
		 WHERE enterprise_id = $1::uuid AND email = $2 AND status IN ('pending', 'email_confirmed', 'ea_approved')`,
		enterpriseID, email,
	).Scan(&existingPendingID)
	if existingPendingID != nil {
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "An invitation is already pending for this email"}})
		return
	}

	// Find user by email
	var userID string
	var existingEnterpriseID *string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT id::text, enterprise_id::text FROM users WHERE email = $1 AND deleted_at IS NULL`, email,
	).Scan(&userID, &existingEnterpriseID)
	if err != nil {
		// User doesn't exist — create invitation
		token := generateRandomCode("ABCDEFGHJKLMNPQRSTUVWXYZ23456789", 32)
		tokenHash := crypto.HashKeySHA256(token)
		if err := r.db.Exec(c.Request.Context(),
			`INSERT INTO enterprise_invitations (enterprise_id, email, name, role, token, expires_at, invited_by)
			 VALUES ($1::uuid, $2, NULLIF($3, ''), $4, $5, NOW() + INTERVAL '14 days', $6::uuid)`,
			enterpriseID, email, name, role, tokenHash, currentUserID); err != nil {
			r.logger.Error("Failed to create invitation", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to create invitation"}})
			return
		}

		acceptURL := fmt.Sprintf("%s/api/enterprise/accept?token=%s", r.cfg.App.BaseURL, token)
		if r.mail != nil && r.mail.IsConfigured() {
			locale := GetLocale(c)
			go func() {
				if err := r.mail.SendEnterpriseInvitation(email, enterpriseName, acceptURL, locale); err != nil {
					r.logger.Warn("Failed to send invitation email",
						zap.String("email", email),
						zap.Error(err))
				}
			}()
		}

		if r.audit != nil {
			r.audit.Log(c.Request.Context(), currentUserID, c.GetString("email"), "enterprise", "member_invited", "info",
				fmt.Sprintf("Member invited to enterprise %s: %s", enterpriseName, email))
		}

		r.logEnterpriseAudit(c.Request.Context(), enterpriseAuditEntry{
			EnterpriseID: enterpriseID,
			Category:     "member",
			ChangeType:   "member_invited",
			FieldName:    "invitation",
			ChangedBy:    currentUserID,
			NewValueStr:  &role,
			Reason:       fmt.Sprintf("Invitation sent to %s (new user)", email),
		})

		c.JSON(http.StatusOK, gin.H{"success": true, "invited": true, "accept_url": acceptURL, "message": "Invitation sent"})
		return
	}
	if existingEnterpriseID != nil && *existingEnterpriseID != "" {
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "User already belongs to another enterprise"}})
		return
	}

	// Existing user whose email domain is NOT whitelisted: don't add directly.
	// An EA add counts as implicit EA approval, so create an ea_approved
	// invitation awaiting system admin final approval.
	if !domainMatches(email, allowedDomains) {
		token := generateRandomCode("ABCDEFGHJKLMNPQRSTUVWXYZ23456789", 32)
		tokenHash := crypto.HashKeySHA256(token)
		if err := r.db.Exec(c.Request.Context(),
			`INSERT INTO enterprise_invitations (enterprise_id, email, name, role, token, status, email_confirmed, email_confirmed_at, expires_at, invited_by)
			 VALUES ($1, $2, NULLIF($3, ''), $4, $5, 'ea_approved', TRUE, NOW(), NOW() + INTERVAL '14 days', $6::uuid)`,
			enterpriseID, email, name, role, tokenHash, currentUserID); err != nil {
			r.logger.Error("Failed to create invitation", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to create invitation"}})
			return
		}

		if r.audit != nil {
			r.audit.Log(c.Request.Context(), currentUserID, c.GetString("email"), "enterprise", "member_invited", "info",
				fmt.Sprintf("Member invited to enterprise %s: %s (awaiting system admin approval)", enterpriseName, email))
		}

		r.logEnterpriseAudit(c.Request.Context(), enterpriseAuditEntry{
			EnterpriseID: enterpriseID,
			Category:     "member",
			ChangeType:   "member_invited",
			FieldName:    "invitation",
			ChangedBy:    currentUserID,
			NewValueStr:  &role,
			Reason:       fmt.Sprintf("Invitation sent to %s (awaiting system admin approval)", email),
		})

		c.JSON(http.StatusOK, gin.H{"success": true, "pending_sa_approval": true, "message": T("msg.invitation_pending_sa", c)})
		return
	}

	// Apply optional name update to existing user
	if name != "" {
		_ = r.db.Exec(c.Request.Context(),
			`UPDATE users SET name = $1, updated_at = NOW() WHERE id = $2::uuid`,
			name, userID)
	}

	// Add user to enterprise with synced role. Guard against hijacking a user who
	// already belongs to another enterprise.
	var currentEntID *string
	if err := r.db.QueryRow(c.Request.Context(),
		`SELECT enterprise_id::text FROM users WHERE id = $1::uuid AND deleted_at IS NULL`,
		userID,
	).Scan(&currentEntID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to look up user"}})
		return
	}
	if currentEntID != nil && *currentEntID != enterpriseID {
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "User already belongs to another enterprise"}})
		return
	}

	err = r.db.Exec(c.Request.Context(),
		`UPDATE users SET enterprise_id = $1::uuid, role = $2, updated_at = NOW()
		 WHERE id = $3::uuid AND deleted_at IS NULL AND (enterprise_id IS NULL OR enterprise_id = $1::uuid)`,
		enterpriseID, usersRole, userID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to add user"}})
		return
	}

	// Add to enterprise_members
	_ = r.db.Exec(c.Request.Context(),
		`INSERT INTO enterprise_members (enterprise_id, user_id, role, created_at)
		 VALUES ($1::uuid, $2::uuid, $3, NOW())
		 ON CONFLICT (enterprise_id, user_id) DO UPDATE SET role = $3, removed_at = NULL, removed_by = NULL`,
		enterpriseID, userID, role,
	)

	// Clean up stale invitations for this email now that the user is a member
	_ = r.db.Exec(c.Request.Context(),
		`UPDATE enterprise_invitations SET status = 'accepted', admin_approved = TRUE, admin_approved_at = NOW()
		 WHERE enterprise_id = $1::uuid AND email = $2 AND status IN ('pending', 'email_confirmed', 'ea_approved')`,
		enterpriseID, email)

	// Notify existing user they were added
	if r.mail != nil && r.mail.IsConfigured() {
		locale := GetLocale(c)
		go func() {
			if err := r.mail.SendEnterpriseAddedNotification(email, enterpriseName, locale); err != nil {
				r.logger.Warn("Failed to send enterprise add notification",
					zap.String("email", email),
					zap.Error(err))
			}
		}()
	}

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), currentUserID, c.GetString("email"), "enterprise", "member_added", "info",
			fmt.Sprintf("Member added to enterprise %s: %s", enterpriseName, email))
	}

	oldStatus := "none"
	newStatus := "added"
	r.logEnterpriseAudit(c.Request.Context(), enterpriseAuditEntry{
		EnterpriseID: enterpriseID,
		Category:     "member",
		ChangeType:   "member_added",
		FieldName:    "membership",
		UserID:       &userID,
		ChangedBy:    currentUserID,
		OldValueStr:  &oldStatus,
		NewValueStr:  &newStatus,
		Reason:       fmt.Sprintf("Member added directly (%s)", email),
	})

	c.JSON(http.StatusOK, gin.H{"success": true, "user_id": userID})
}

// reassignUserEnterprise is a system/super admin-only operation that force-moves
// a user out of their current enterprise (if any) and, optionally, into a target
// enterprise. Unlike the enterprise-admin add/invite paths, it bypasses the
// anti-hijack guard (users.enterprise_id IS NULL) because the caller is a trusted
// platform admin — this is the recovery path for a user who was mistakenly added
// to the wrong enterprise and cannot otherwise be moved.
//
// Body: { "user_id": "<uuid>", "target_enterprise_id": "<uuid|empty>", "role": "member|admin", "reason": "<text>" }
//
// If target_enterprise_id is empty the user is only detached (left standalone).
// Detach reuses the same cleanup as removeMember: soft-delete enterprise_members,
// clear users.enterprise_id/role, disable + unassign the member's enterprise keys,
// and reclaim the unused enterprise-quota grant to the source pool (S-U6).
func (r *EnterpriseRoutes) reassignUserEnterprise(c *gin.Context) {
	operatorID := c.GetString("user_id")
	ctx := c.Request.Context()

	var req struct {
		UserID             string `json:"user_id" binding:"required"`
		TargetEnterpriseID string `json:"target_enterprise_id"`
		Role               string `json:"role"`
		Reason             string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "user_id is required"}})
		return
	}

	userID := strings.TrimSpace(req.UserID)
	targetEnterpriseID := strings.TrimSpace(req.TargetEnterpriseID)
	role := strings.TrimSpace(req.Role)
	if role == "" {
		role = "member"
	}
	if role != "member" && role != "admin" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "role must be 'member' or 'admin'"}})
		return
	}
	reason := strings.TrimSpace(req.Reason)

	// Never let an admin reassign their own enterprise through this endpoint —
	// it would let them silently detach from / hijack into any enterprise and
	// sidesteps the quit/appoint flows that protect the last-admin invariant.
	if userID == operatorID {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Cannot reassign your own enterprise via this endpoint"}})
		return
	}

	// Resolve the user and their current enterprise.
	var sourceEnterpriseID *string
	if err := r.db.QueryRow(ctx,
		`SELECT enterprise_id::text FROM users WHERE id = $1::uuid AND deleted_at IS NULL`, userID,
	).Scan(&sourceEnterpriseID); err != nil {
		if err == pgx.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "User not found"}})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to look up user"}})
		return
	}

	sourceID := ""
	if sourceEnterpriseID != nil {
		sourceID = *sourceEnterpriseID
	}

	// No-op guard: user already in the requested target (or already standalone
	// when no target is given).
	if sourceID == targetEnterpriseID {
		if targetEnterpriseID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "User is not in any enterprise"}})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "User already belongs to the target enterprise"}})
		return
	}

	// Validate the target enterprise (when moving into one) and check capacity.
	usersRole := "user"
	if role == "admin" {
		usersRole = "enterprise_admin"
	}
	if targetEnterpriseID != "" {
		var maxMembers, memberCount, maxAdmins, adminCount int
		if err := r.db.QueryRow(ctx,
			`SELECT COALESCE(max_members, 50),
			        (SELECT COUNT(*) FROM enterprise_members WHERE enterprise_id = e.id AND removed_at IS NULL),
			        COALESCE(max_admins, 3),
			        (SELECT COUNT(*) FROM enterprise_members WHERE enterprise_id = e.id AND role = 'admin' AND removed_at IS NULL)
			 FROM enterprises e WHERE e.id = $1::uuid AND e.deleted_at IS NULL`,
			targetEnterpriseID,
		).Scan(&maxMembers, &memberCount, &maxAdmins, &adminCount); err != nil {
			if err == pgx.ErrNoRows {
				c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Target enterprise not found"}})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to look up target enterprise"}})
			return
		}
		if memberCount >= maxMembers {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf("Target enterprise member limit reached (%d/%d)", memberCount, maxMembers)}})
			return
		}
		if role == "admin" && adminCount >= maxAdmins {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf("Target enterprise admin limit reached (%d/%d)", adminCount, maxAdmins)}})
			return
		}
	}

	// Collect the source enterprise's keys assigned to this user so their
	// validate cache can be dropped after commit.
	var recalledKeyHashes []string
	if sourceID != "" {
		if hashRows, qErr := r.db.Query(ctx,
			`SELECT key_hash FROM api_keys
			 WHERE enterprise_id = $1::uuid AND assigned_user_id = $2::uuid AND deleted_at IS NULL AND status = 'active'`,
			sourceID, userID); qErr == nil {
			for hashRows.Next() {
				var h string
				if scanErr := hashRows.Scan(&h); scanErr == nil {
					recalledKeyHashes = append(recalledKeyHashes, h)
				}
			}
			hashRows.Close()
		}
	}

	if err := r.db.WithTx(ctx, func(tx pgx.Tx) error {
		// 1. Detach from the current enterprise (if any) — same cleanup as removeMember.
		if sourceID != "" {
			if _, err := tx.Exec(ctx,
				`UPDATE enterprise_members SET removed_at = NOW(), removed_by = $1::uuid
				 WHERE enterprise_id = $2::uuid AND user_id = $3::uuid AND removed_at IS NULL`,
				operatorID, sourceID, userID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx,
				`UPDATE api_keys SET status = 'disabled', assigned_user_id = NULL, updated_at = NOW()
				 WHERE enterprise_id = $1::uuid AND assigned_user_id = $2::uuid`,
				sourceID, userID); err != nil {
				return err
			}
			// Return the member's unused enterprise-quota grant to the source pool
			// before they leave the enterprise context (S-U6). Must run before the
			// users row is repointed so the reclaim math reads the right grant.
			if _, err := reclaimMemberEnterpriseQuota(ctx, tx, userID, sourceID); err != nil {
				return err
			}
		}

		// 2. Attach to the target enterprise, or leave standalone.
		if targetEnterpriseID == "" {
			if _, err := tx.Exec(ctx,
				`UPDATE users SET enterprise_id = NULL, role = 'user', updated_at = NOW() WHERE id = $1::uuid`,
				userID); err != nil {
				return err
			}
			return nil
		}

		if _, err := tx.Exec(ctx,
			`UPDATE users SET enterprise_id = $1::uuid, role = $2, updated_at = NOW() WHERE id = $3::uuid`,
			targetEnterpriseID, usersRole, userID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO enterprise_members (enterprise_id, user_id, role, joined_at)
			 VALUES ($1::uuid, $2::uuid, $3, NOW())
			 ON CONFLICT (enterprise_id, user_id) DO UPDATE SET role = $3, removed_at = NULL, removed_by = NULL`,
			targetEnterpriseID, userID, role); err != nil {
			return err
		}
		// Clean up any stale invitations for this user in the target enterprise.
		if _, err := tx.Exec(ctx,
			`UPDATE enterprise_invitations SET status = 'accepted', admin_approved = TRUE, admin_approved_at = NOW()
			 WHERE enterprise_id = $1::uuid AND status IN ('pending', 'email_confirmed', 'ea_approved')
			   AND email = (SELECT email FROM users WHERE id = $2::uuid)`,
			targetEnterpriseID, userID); err != nil {
			return err
		}
		return nil
	}); err != nil {
		r.logger.Error("Failed to reassign user enterprise", zap.Error(err), zap.String("user_id", userID))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to reassign user"}})
		return
	}

	// Invalidate caches after commit: the source keys were disabled and the
	// user's enterprise context changed, so any cached validate result is stale.
	for _, h := range recalledKeyHashes {
		invalidateValidateCache(ctx, r.db, r.logger, h)
	}
	invalidateUserValidateCaches(ctx, r.db, r.logger, userID)

	// Global admin audit (both source and target captured in the message).
	if r.audit != nil {
		r.audit.Log(ctx, operatorID, c.GetString("email"), "admin", "user_enterprise_reassigned", "warning",
			fmt.Sprintf("Admin reassigned user %s from enterprise %q to %q (reason: %s)", userID, sourceID, targetEnterpriseID, reason))
	}

	// Per-enterprise audit trail: one entry on the source (removed) and one on
	// the target (added) so each enterprise's timeline is complete.
	if sourceID != "" {
		oldStatus := "active"
		newStatus := "removed"
		detachReason := reason
		if detachReason == "" {
			detachReason = "Reassigned by platform admin"
		}
		r.logEnterpriseAudit(ctx, enterpriseAuditEntry{
			EnterpriseID: sourceID,
			Category:     "member",
			ChangeType:   "member_removed",
			FieldName:    "membership",
			UserID:       &userID,
			ChangedBy:    operatorID,
			OldValueStr:  &oldStatus,
			NewValueStr:  &newStatus,
			Reason:       fmt.Sprintf("Member reassigned out by platform admin (%s)", detachReason),
		})
	}
	if targetEnterpriseID != "" {
		oldStatus := "none"
		newStatus := "added"
		attachReason := reason
		if attachReason == "" {
			attachReason = "Reassigned by platform admin"
		}
		r.logEnterpriseAudit(ctx, enterpriseAuditEntry{
			EnterpriseID: targetEnterpriseID,
			Category:     "member",
			ChangeType:   "member_added",
			FieldName:    "membership",
			UserID:       &userID,
			ChangedBy:    operatorID,
			OldValueStr:  &oldStatus,
			NewValueStr:  &newStatus,
			Reason:       fmt.Sprintf("Member reassigned in by platform admin (%s)", attachReason),
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success":              true,
		"user_id":              userID,
		"source_enterprise_id": sourceID,
		"target_enterprise_id": targetEnterpriseID,
	})
}

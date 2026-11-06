package routes

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/ai-proxy/ai-portal-go/pkg/crypto"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// P2 (manage:keys) and P3 (read:members / manage:members) endpoints consumed by
// a management token. Every query is scoped to the token's enterprise
// (mgmt_enterprise_id) so a token can never touch another enterprise's data.

// ---- P2: manage:keys ----

// mgmtListKeys lists the enterprise's keys.
func (r *ManagementTokenRoutes) mgmtListKeys(c *gin.Context) {
	entID := c.GetString("mgmt_enterprise_id")
	if entID == "" {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "token is not bound to an enterprise"}})
		return
	}
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT k.id::text, k.name, k.key_prefix, k.status, k.key_type,
		        COALESCE(k.assigned_user_id::text,''), COALESCE(k.group_id::text,''),
		        k.created_at, k.expires_at
		 FROM api_keys k
		 WHERE k.enterprise_id = $1::uuid AND k.deleted_at IS NULL
		 ORDER BY k.created_at DESC LIMIT 200`, entID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list keys"}})
		return
	}
	defer rows.Close()
	keys := []gin.H{}
	for rows.Next() {
		var id, name, prefix, status, keyType, assigned, groupID string
		var createdAt, expiresAt interface{}
		if rows.Scan(&id, &name, &prefix, &status, &keyType, &assigned, &groupID, &createdAt, &expiresAt) != nil {
			continue
		}
		keys = append(keys, gin.H{
			"id": id, "name": name, "key_prefix": prefix, "status": status, "key_type": keyType,
			"assigned_user_id": assigned, "group_id": groupID, "created_at": createdAt, "expires_at": expiresAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"keys": keys})
}

// mgmtCreateKey creates an enterprise key bound to a member-visible (public)
// group of the token's enterprise. Mirrors the self-service constraints:
// group must belong to the enterprise; selected models must be a subset of the
// group's models. assigned_user_id is the token's owner (the enterprise admin).
func (r *ManagementTokenRoutes) mgmtCreateKey(c *gin.Context) {
	entID := c.GetString("mgmt_enterprise_id")
	ownerID := c.GetString("mgmt_user_id")
	if entID == "" {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "token is not bound to an enterprise"}})
		return
	}
	var req struct {
		Name    string   `json:"name"`
		GroupID string   `json:"group_id"`
		Models  []string `json:"models"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Name) == "" || req.GroupID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "name and group_id are required"}})
		return
	}

	// Group must be an enterprise group of THIS enterprise.
	var groupModelsRaw []byte
	var rpm, tpm int
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT COALESCE(models,'[]'::jsonb), COALESCE(rate_limit_rpm,0), COALESCE(rate_limit_tpm,0)
		 FROM user_groups
		 WHERE id = $1::uuid AND enterprise_id = $2::uuid AND group_type = 'enterprise'
		   AND deleted_at IS NULL AND status = 'active'`,
		req.GroupID, entID,
	).Scan(&groupModelsRaw, &rpm, &tpm)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "group not found in your enterprise"}})
		return
	}
	var groupModels []string
	_ = json.Unmarshal(groupModelsRaw, &groupModels)

	// Selected models must be a subset of the group's models (when restricted).
	selected := req.Models
	if len(selected) > 0 && len(groupModels) > 0 {
		set := map[string]bool{}
		for _, m := range groupModels {
			set[m] = true
		}
		for _, m := range selected {
			if !set[m] {
				c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf("model '%s' is not in the selected group", m)}})
				return
			}
		}
	}
	if len(selected) == 0 {
		selected = groupModels
	}

	rawKey := "sk-wgit-en-" + uuid.NewString()
	keyHash := crypto.HashKeySHA256(rawKey)
	keyPrefix := rawKey[:apiKeyPrefixDisplayLength] + "..."
	keyEncrypted, encErr := crypto.Encrypt(rawKey, r.cfgEncryptionKey())
	if encErr != nil {
		r.logger.Error("mgmt create key encrypt failed", zap.Error(encErr))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to create key"}})
		return
	}
	var allowedJSON interface{}
	if len(selected) > 0 {
		allowedJSON = selected
	}
	var currentModel *string
	if len(selected) > 0 {
		m := selected[0]
		currentModel = &m
	}

	var keyID string
	err = r.db.QueryRow(c.Request.Context(),
		`INSERT INTO api_keys (user_id, name, key_hash, key_prefix, key_encrypted, key_type, status,
		   enterprise_id, assigned_user_id, group_id, access_mode, allowed_models, rate_limit_rpm, rate_limit_tpm,
		   model_routing_mode, current_model, selected_models)
		 VALUES ($1::uuid, $2, $3, $4, $5, 'enterprise', 'active',
		   $6::uuid, $1::uuid, $7::uuid, 'enterprise', $8, $9, $10, 'single', $11, $12)
		 RETURNING id::text`,
		ownerID, strings.TrimSpace(req.Name), keyHash, keyPrefix, keyEncrypted,
		entID, req.GroupID, allowedJSON, rpm, tpm, currentModel, selected,
	).Scan(&keyID)
	if err != nil {
		r.logger.Error("mgmt create key failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to create key"}})
		return
	}
	if r.audit != nil {
		r.audit.Log(c.Request.Context(), ownerID, "mgmt-token", "management_token", "key_created", "warning",
			fmt.Sprintf("Enterprise key '%s' created via management token (%s)", req.Name, c.GetString("mgmt_token_id")))
	}
	writeEnterpriseAudit(c.Request.Context(), r.db, r.logger, enterpriseAuditEntry{
		EnterpriseID: entID, Category: "key", ChangeType: "key_created", FieldName: "key",
		UserID: &ownerID, ChangedBy: ownerID, TargetType: "key", TargetID: keyID,
		Reason: fmt.Sprintf("Enterprise key '%s' created via management token", req.Name),
	})
	c.JSON(http.StatusOK, gin.H{"id": keyID, "key": rawKey, "key_prefix": keyPrefix, "name": req.Name})
}

// mgmtDisableKey revokes an enterprise key (scoped to the token's enterprise).
func (r *ManagementTokenRoutes) mgmtDisableKey(c *gin.Context) {
	entID := c.GetString("mgmt_enterprise_id")
	keyID := c.Param("id")
	var keyHash string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT key_hash FROM api_keys WHERE id = $1::uuid AND enterprise_id = $2::uuid AND deleted_at IS NULL`,
		keyID, entID).Scan(&keyHash)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Key not found"}})
		return
	}
	if err := r.db.Exec(c.Request.Context(),
		`UPDATE api_keys SET status = 'disabled', updated_at = NOW()
		 WHERE id = $1::uuid AND enterprise_id = $2::uuid`, keyID, entID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to disable key"}})
		return
	}
	invalidateValidateCache(c.Request.Context(), r.db, r.logger, keyHash)
	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("mgmt_user_id"), "mgmt-token", "management_token", "key_disabled", "warning",
			"Enterprise key "+keyID+" disabled via management token")
	}
	writeEnterpriseAudit(c.Request.Context(), r.db, r.logger, enterpriseAuditEntry{
		EnterpriseID: entID, Category: "key", ChangeType: "key_revoked", FieldName: "status",
		ChangedBy: c.GetString("mgmt_user_id"), TargetType: "key", TargetID: keyID,
		Reason: "Enterprise key disabled via management token",
	})
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// mgmtDeleteKey soft-deletes an enterprise key (scoped to the token's enterprise).
func (r *ManagementTokenRoutes) mgmtDeleteKey(c *gin.Context) {
	entID := c.GetString("mgmt_enterprise_id")
	keyID := c.Param("id")
	var keyHash string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT key_hash FROM api_keys WHERE id = $1::uuid AND enterprise_id = $2::uuid AND deleted_at IS NULL`,
		keyID, entID).Scan(&keyHash)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Key not found"}})
		return
	}
	if err := r.db.Exec(c.Request.Context(),
		`UPDATE api_keys SET deleted_at = NOW(), status = 'revoked', updated_at = NOW()
		 WHERE id = $1::uuid AND enterprise_id = $2::uuid`, keyID, entID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to delete key"}})
		return
	}
	invalidateValidateCache(c.Request.Context(), r.db, r.logger, keyHash)
	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("mgmt_user_id"), "mgmt-token", "management_token", "key_deleted", "warning",
			"Enterprise key "+keyID+" deleted via management token")
	}
	writeEnterpriseAudit(c.Request.Context(), r.db, r.logger, enterpriseAuditEntry{
		EnterpriseID: entID, Category: "key", ChangeType: "key_deleted", FieldName: "status",
		ChangedBy: c.GetString("mgmt_user_id"), TargetType: "key", TargetID: keyID,
		Reason: "Enterprise key deleted via management token",
	})
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ---- P3: read:members / manage:members ----

// mgmtListMembers lists the enterprise's members.
func (r *ManagementTokenRoutes) mgmtListMembers(c *gin.Context) {
	entID := c.GetString("mgmt_enterprise_id")
	if entID == "" {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "token is not bound to an enterprise"}})
		return
	}
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT u.id::text, u.email, COALESCE(u.name,''), COALESCE(em.role,'member'),
		        COALESCE(em.quota_limit,0), COALESCE(em.credit_limit,0),
		        COALESCE(u.quota_used,0), COALESCE(u.credit_used,0)
		 FROM users u
		 JOIN enterprise_members em ON em.user_id = u.id AND em.enterprise_id = $1::uuid AND em.removed_at IS NULL
		 WHERE u.deleted_at IS NULL
		 ORDER BY u.created_at`, entID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list members"}})
		return
	}
	defer rows.Close()
	members := []gin.H{}
	for rows.Next() {
		var id, email, name, role string
		var quotaLimit, creditLimit, quotaUsed, creditUsed int64
		if rows.Scan(&id, &email, &name, &role, &quotaLimit, &creditLimit, &quotaUsed, &creditUsed) != nil {
			continue
		}
		members = append(members, gin.H{
			"user_id": id, "email": email, "name": name, "role": role,
			"quota_limit": quotaLimit, "credit_limit": creditLimit,
			"quota_used": quotaUsed, "credit_used": creditUsed,
		})
	}
	c.JSON(http.StatusOK, gin.H{"members": members})
}

// mgmtSetMemberQuota sets a member's quota_limit (micro-USD). 0 = unlimited.
func (r *ManagementTokenRoutes) mgmtSetMemberQuota(c *gin.Context) {
	r.updateMemberLimit(c, "quota_limit")
}

// mgmtSetMemberCredit sets a member's credit_limit (micro-USD). 0 = unlimited.
func (r *ManagementTokenRoutes) mgmtSetMemberCredit(c *gin.Context) {
	r.updateMemberLimit(c, "credit_limit")
}

func (r *ManagementTokenRoutes) updateMemberLimit(c *gin.Context, column string) {
	entID := c.GetString("mgmt_enterprise_id")
	targetUser := c.Param("userId")
	var req struct {
		Limit int64 `json:"limit"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Limit < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "limit (>= 0) is required"}})
		return
	}
	// column is a fixed internal constant (never user input) — safe to interpolate.
	ct, err := r.db.Pool.Exec(c.Request.Context(),
		fmt.Sprintf(`UPDATE enterprise_members SET %s = $1
		 WHERE enterprise_id = $2::uuid AND user_id = $3::uuid AND removed_at IS NULL`, column),
		req.Limit, entID, targetUser)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update member"}})
		return
	}
	if ct.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Member not found in your enterprise"}})
		return
	}
	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("mgmt_user_id"), "mgmt-token", "management_token", "member_"+column+"_set", "warning",
			fmt.Sprintf("Member %s %s set to %d via management token", targetUser, column, req.Limit))
	}
	// mirror to enterprise_audit_log (same change_type the admin UI path uses).
	ct2 := "member_quota_limit_set"
	if column == "credit_limit" {
		ct2 = "member_credit_limit_set"
	}
	nv := req.Limit
	writeEnterpriseAudit(c.Request.Context(), r.db, r.logger, enterpriseAuditEntry{
		EnterpriseID: entID, Category: "billing", ChangeType: ct2, FieldName: column,
		UserID: &targetUser, ChangedBy: c.GetString("mgmt_user_id"), NewValue: &nv,
		TargetType: "user", TargetID: targetUser, Reason: "Member " + column + " set via management token",
	})
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// mgmtRemoveMember removes a member from the enterprise and disables their
// enterprise keys (mirrors the admin remove-member behavior).
func (r *ManagementTokenRoutes) mgmtRemoveMember(c *gin.Context) {
	entID := c.GetString("mgmt_enterprise_id")
	targetUser := c.Param("userId")
	actor := c.GetString("mgmt_user_id")
	ctx := c.Request.Context()
	if targetUser == actor {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "cannot remove the token owner"}})
		return
	}

	// Last-admin guard: if the target is an active admin and the only one left,
	// refuse — removing them would leave the enterprise with no UI-capable
	// admin while the token keeps operating (lockout). Mirrors quitEnterprise.
	var targetRole string
	var adminCount int
	if err := r.db.Pool.QueryRow(ctx,
		`SELECT COALESCE((SELECT role FROM enterprise_members WHERE enterprise_id=$1::uuid AND user_id=$2::uuid AND removed_at IS NULL), ''),
		        (SELECT COUNT(*) FROM enterprise_members WHERE enterprise_id=$1::uuid AND role='admin' AND removed_at IS NULL)`,
		entID, targetUser).Scan(&targetRole, &adminCount); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to look up member"}})
		return
	}
	if targetRole == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Member not found in your enterprise"}})
		return
	}
	if targetRole == "admin" && adminCount <= 1 {
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "Cannot remove the last enterprise admin"}})
		return
	}

	// Collect the member's active key hashes before the tx so caches can be
	// busted after commit.
	var recalledKeyHashes []string
	if hashRows, qErr := r.db.Query(ctx,
		`SELECT key_hash FROM api_keys
		 WHERE enterprise_id = $1::uuid AND assigned_user_id = $2::uuid AND deleted_at IS NULL AND status = 'active'`,
		entID, targetUser); qErr == nil {
		for hashRows.Next() {
			var h string
			if scanErr := hashRows.Scan(&h); scanErr == nil {
				recalledKeyHashes = append(recalledKeyHashes, h)
			}
		}
		hashRows.Close()
	}

	// Full removal in one transaction, matching removeMyEnterpriseMember: mark
	// removed, detach the user from the enterprise (so their personal keys stop
	// billing the enterprise), disable assigned keys, and reclaim unused quota.
	if err := r.db.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE enterprise_members SET removed_at = NOW(), removed_by = $1::uuid
			 WHERE enterprise_id = $2::uuid AND user_id = $3::uuid AND removed_at IS NULL`,
			actor, entID, targetUser); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE users SET enterprise_id = NULL, role = 'user', updated_at = NOW()
			 WHERE id = $1::uuid AND enterprise_id = $2::uuid`,
			targetUser, entID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE api_keys SET status = 'disabled', assigned_user_id = NULL, updated_at = NOW()
			 WHERE enterprise_id = $1::uuid AND assigned_user_id = $2::uuid`,
			entID, targetUser); err != nil {
			return err
		}
		if _, err := reclaimMemberEnterpriseQuota(ctx, tx, targetUser, entID); err != nil {
			return err
		}
		return nil
	}); err != nil {
		r.logger.Error("Failed to remove member via management token", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to remove member"}})
		return
	}

	// Bust validate caches after commit (recalled enterprise keys + the user's
	// personal keys that just lost enterprise context).
	for _, h := range recalledKeyHashes {
		invalidateValidateCache(ctx, r.db, r.logger, h)
	}
	invalidateUserValidateCaches(ctx, r.db, r.logger, targetUser)

	if r.audit != nil {
		r.audit.Log(ctx, actor, "mgmt-token", "management_token", "member_removed", "warning",
			"Member "+targetUser+" removed via management token")
	}
	writeEnterpriseAudit(ctx, r.db, r.logger, enterpriseAuditEntry{
		EnterpriseID: entID, Category: "member", ChangeType: "member_removed", FieldName: "status",
		UserID: &targetUser, ChangedBy: actor, TargetType: "user", TargetID: targetUser,
		Reason: "Member removed via management token",
	})
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// cfgEncryptionKey returns the AES key used to encrypt stored key material.
func (r *ManagementTokenRoutes) cfgEncryptionKey() string {
	if r.cfg == nil {
		return ""
	}
	return r.cfg.Auth.EncryptionKey
}

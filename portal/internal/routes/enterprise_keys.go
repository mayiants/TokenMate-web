package routes

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/mayiants/ai-portal-go/pkg/crypto"
	"github.com/mayiants/ai-portal-go/pkg/metrics"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// listEnterpriseKeys lists all enterprise keys (admin sees all, member sees own)
func (r *EnterpriseRoutes) listEnterpriseKeys(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	userRole := c.GetString("role")
	userID := c.GetString("user_id")

	query := `SELECT k.id::text, k.name, k.key_prefix, k.status, k.enterprise_id::text,
	                  k.assigned_user_id::text, k.key_type, k.created_at,
	                  k.expires_at, k.model_routing_mode, k.allowed_models, k.group_id::text
	           FROM api_keys k
	           WHERE k.enterprise_id = $1::uuid AND k.deleted_at IS NULL`
	args := []interface{}{enterpriseID}

	// Non-admin members only see their own keys
	if userRole != "enterprise_admin" && userRole != "system_admin" && userRole != "super_admin" {
		query += " AND k.assigned_user_id = $2::uuid"
		args = append(args, userID)
	}
	query += " ORDER BY k.created_at DESC LIMIT 100"

	rows, err := r.db.Query(c.Request.Context(), query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list keys"}})
		return
	}
	defer rows.Close()

	keys := []gin.H{}
	for rows.Next() {
		var id, name, keyPrefix, status, keyType string
		var enterpriseIDVal, assignedUserID *string
		var createdAt, expiresAt interface{}
		var modelRoutingMode, groupID *string
		var allowedModels interface{}
		if err := rows.Scan(&id, &name, &keyPrefix, &status, &enterpriseIDVal, &assignedUserID, &keyType, &createdAt,
			&expiresAt, &modelRoutingMode, &allowedModels, &groupID); err != nil {
			continue
		}
		keys = append(keys, gin.H{
			"id": id, "name": name, "key_prefix": keyPrefix, "status": status,
			"enterprise_id": enterpriseIDVal, "assigned_user_id": assignedUserID,
			"key_type": keyType, "created_at": createdAt,
			"expires_at": expiresAt, "model_routing_mode": modelRoutingMode,
			"allowed_models": allowedModels, "group_id": groupID,
		})
	}
	c.JSON(http.StatusOK, gin.H{"keys": keys})
}

// validKeyExpiresInDays lists the allowed key lifetime options (0 = never expires).
var validKeyExpiresInDays = map[int]bool{0: true, 30: true, 90: true, 365: true}

// keyExpiresAt converts an expires_in day count into a timestamp (nil = never).
func keyExpiresAt(expiresIn int) *time.Time {
	if expiresIn <= 0 {
		return nil
	}
	t := time.Now().Add(time.Duration(expiresIn) * 24 * time.Hour)
	return &t
}

// createEnterpriseKey creates a new enterprise key for a member
func (r *EnterpriseRoutes) createEnterpriseKey(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")

	var req struct {
		Name             string   `json:"name" binding:"required"`
		AssignedTo       string   `json:"assigned_to" binding:"required"`
		GroupID          string   `json:"group_id"`
		Models           []string `json:"models"`
		ModelRoutingMode string   `json:"model_routing_mode"`
		ExpiresIn        int      `json:"expires_in"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "name and assigned_to are required"}})
		return
	}
	if !validKeyExpiresInDays[req.ExpiresIn] {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "expires_in must be one of 0, 30, 90, 365"}})
		return
	}
	switch req.ModelRoutingMode {
	case "single", "round_robin", "priority":
	case "":
		req.ModelRoutingMode = "single"
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "model_routing_mode must be single, round_robin or priority"}})
		return
	}

	// Verify assigned_to belongs to this enterprise (either via users.enterprise_id or enterprise_members)
	var isMember bool
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT EXISTS (
			SELECT 1 FROM users u WHERE u.id = $1::uuid AND u.enterprise_id = $2::uuid AND u.deleted_at IS NULL
			UNION
			SELECT 1 FROM enterprise_members em WHERE em.user_id = $1::uuid AND em.enterprise_id = $2::uuid AND em.removed_at IS NULL
		)`,
		req.AssignedTo, enterpriseID,
	).Scan(&isMember)
	if err != nil || !isMember {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Assigned user is not a member of this enterprise"}})
		return
	}

	// Generate key (same UUID format as regular keys — see apikeys.go)
	key := "sk-wgit-en-" + uuid.New().String()
	keyPrefix := key
	if len(key) > apiKeyPrefixDisplayLength {
		keyPrefix = key[:apiKeyPrefixDisplayLength] + "..."
	}

	// Hash key for authentication, encrypt for later retrieval
	keyHash := crypto.HashKeySHA256(key)
	keyEncrypted, encErr := crypto.Encrypt(key, r.cfg.Auth.EncryptionKey)
	if encErr != nil {
		r.logger.Error("Failed to encrypt enterprise key", zap.Error(encErr))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Key encryption failed"}})
		return
	}

	// Resolve routing group: enterprise group → platform group (for proxy priority + key pool)
	var finalGroupID string
	var allowedModels interface{}
	var keyRPM, keyTPM int64
	var selectedModels []string

	if req.GroupID != "" {
		// Query enterprise group for rate limits. Inherited platform template
		// groups (inherit_to_enterprises) may be used directly without a local
		// enterprise copy.
		var entModels []byte
		var entRPM int
		var entTPM int64
		err = r.db.QueryRow(c.Request.Context(),
			`SELECT COALESCE(g.models::text, '[]')::bytea, COALESCE(g.rpm, 0), COALESCE(g.tpm, 0)
			 FROM user_groups g WHERE g.id = $1::uuid AND g.deleted_at IS NULL
			   AND (g.enterprise_id = $2::uuid
			        OR (g.enterprise_id IS NULL AND g.group_type = 'enterprise' AND g.inherit_to_enterprises))`,
			req.GroupID, enterpriseID,
		).Scan(&entModels, &entRPM, &entTPM)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Group not found in your enterprise"}})
			return
		}

		// Apply enterprise group restrictions to the key
		var groupModels []string
		if len(entModels) > 0 && string(entModels) != "[]" && string(entModels) != "null" {
			_ = json.Unmarshal(entModels, &groupModels)
		}
		if len(req.Models) > 0 {
			// Admin picked a subset — every model must belong to the group
			// (an empty group model list means unrestricted, so accept as-is).
			if len(groupModels) > 0 {
				for _, m := range req.Models {
					if !containsString(groupModels, m) {
						c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf("Model %s is not in the selected group", m)}})
						return
					}
				}
			}
			selectedModels = req.Models
		} else {
			selectedModels = groupModels
		}
		if len(selectedModels) > 0 {
			allowedModels = selectedModels
		}
		if entRPM > 0 {
			keyRPM = int64(entRPM)
		}
		if entTPM > 0 {
			keyTPM = entTPM
		}

		// Always use enterprise group as group_id (model/rate limit reference);
		// platform parent group routing resolved at validation time.
		finalGroupID = req.GroupID
	}

	// Routing fields power the wgit-maas-latest alias resolution on the proxy
	// (single → current_model; round_robin/priority → selected_models order).
	var currentModel *string
	if len(selectedModels) > 0 {
		m := selectedModels[0]
		currentModel = &m
	}
	expiresAt := keyExpiresAt(req.ExpiresIn)

	var keyID string
	creatorUserID := c.GetString("user_id") // the enterprise admin creating this key
	if finalGroupID != "" {
		err = r.db.QueryRow(c.Request.Context(),
			`INSERT INTO api_keys (user_id, name, key_hash, key_prefix, key_encrypted, key_type, status,
			   enterprise_id, assigned_user_id, group_id, access_mode, allowed_models, rate_limit_rpm, rate_limit_tpm,
			   model_routing_mode, current_model, selected_models, expires_at)
			 VALUES ($1::uuid, $2, $3, $4, $5, 'enterprise', 'active',
			   $6::uuid, $7::uuid, $8::uuid, 'enterprise', $9, $10, $11, $12, $13, $14, $15)
			 RETURNING id::text`,
			creatorUserID, req.Name, keyHash, keyPrefix, keyEncrypted, enterpriseID, req.AssignedTo,
			finalGroupID, allowedModels, keyRPM, keyTPM,
			req.ModelRoutingMode, currentModel, selectedModels, expiresAt,
		).Scan(&keyID)
	} else {
		err = r.db.QueryRow(c.Request.Context(),
			`INSERT INTO api_keys (user_id, name, key_hash, key_prefix, key_encrypted, key_type, status,
			   enterprise_id, assigned_user_id, access_mode, allowed_models, rate_limit_rpm, rate_limit_tpm,
			   model_routing_mode, current_model, selected_models, expires_at)
			 VALUES ($1::uuid, $2, $3, $4, $5, 'enterprise', 'active',
			   $6::uuid, $7::uuid, 'enterprise', $8, $9, $10, $11, $12, $13, $14)
			 RETURNING id::text`,
			creatorUserID, req.Name, keyHash, keyPrefix, keyEncrypted, enterpriseID, req.AssignedTo,
			allowedModels, keyRPM, keyTPM,
			req.ModelRoutingMode, currentModel, selectedModels, expiresAt,
		).Scan(&keyID)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "An internal error occurred"}})
		return
	}
	// Best-effort active key gauge: increment when a new active key is created.
	// This is not decremented on deletion/disable; a periodic refresh would be needed for accuracy.
	metrics.GetMetrics().ActiveKeys.Add(1)

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "enterprise", "key_created", "info",
			fmt.Sprintf("Enterprise key created for member %s in enterprise %s", req.AssignedTo, enterpriseID))
	}

	r.logEnterpriseAudit(c.Request.Context(), enterpriseAuditEntry{
		EnterpriseID: enterpriseID,
		Category:     "key",
		ChangeType:   "key_created",
		FieldName:    "key",
		UserID:       &req.AssignedTo,
		ChangedBy:    creatorUserID,
		NewValueStr:  &req.Name,
		TargetType:   "key",
		TargetID:     keyID,
		Reason:       fmt.Sprintf("Enterprise key '%s' created", req.Name),
	})

	invalidateValidateCache(c.Request.Context(), r.db, r.logger, keyHash)

	c.JSON(http.StatusOK, gin.H{"success": true, "key_id": keyID, "key": key})
}

// reassignKey reassigns a recalled enterprise key to another member
func (r *EnterpriseRoutes) reassignKey(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	keyID := c.Param("keyId")

	// Fetch the key hash before updating so we can invalidate the validation cache.
	var keyHash string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT key_hash FROM api_keys WHERE id = $1::uuid AND enterprise_id = $2::uuid AND deleted_at IS NULL`,
		keyID, enterpriseID,
	).Scan(&keyHash)

	var req struct {
		Name             string   `json:"name"`
		AssignedTo       string   `json:"assigned_to"`
		GroupID          string   `json:"group_id"`
		Models           []string `json:"models"`
		ModelRoutingMode string   `json:"model_routing_mode"`
		ExpiresIn        *int     `json:"expires_in"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	// Verify key belongs to this enterprise and snapshot current state for audit.
	var keyStatus, keyName, oldAssignedUserID string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT status, name, COALESCE(assigned_user_id::text, '') FROM api_keys WHERE id = $1::uuid AND enterprise_id = $2::uuid`,
		keyID, enterpriseID,
	).Scan(&keyStatus, &keyName, &oldAssignedUserID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Key not found in this enterprise"}})
		return
	}

	// Update name if provided
	if req.Name != "" {
		err = r.db.Exec(c.Request.Context(),
			`UPDATE api_keys SET name = $1, updated_at = NOW() WHERE id = $2::uuid`,
			req.Name, keyID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update name"}})
			return
		}
	}

	// Allow editing group/model for active keys; reassign for disabled keys
	if req.AssignedTo != "" {
		var isMember bool
		err = r.db.QueryRow(c.Request.Context(),
			`SELECT EXISTS (
				SELECT 1 FROM users u WHERE u.id = $1::uuid AND u.enterprise_id = $2::uuid AND u.deleted_at IS NULL
				UNION
				SELECT 1 FROM enterprise_members em WHERE em.user_id = $1::uuid AND em.enterprise_id = $2::uuid AND em.removed_at IS NULL
			)`,
			req.AssignedTo, enterpriseID,
		).Scan(&isMember)
		if err != nil || !isMember {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "User is not a member of this enterprise"}})
			return
		}
		if keyStatus != "disabled" {
			// Just update assignee for active keys
			err = r.db.Exec(c.Request.Context(),
				`UPDATE api_keys SET assigned_user_id = $1::uuid, updated_at = NOW() WHERE id = $2::uuid`,
				req.AssignedTo, keyID)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update assignee"}})
				return
			}
		} else {
			// Reassign disabled key (reactivate + assign)
			err = r.db.Exec(c.Request.Context(),
				`UPDATE api_keys SET assigned_user_id = $1::uuid, status = 'active', updated_at = NOW() WHERE id = $2::uuid`,
				req.AssignedTo, keyID)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to reassign key"}})
				return
			}
		}
	}

	// Update group_id for active keys or disabled keys being reassigned
	if req.GroupID != "" {
		// Validate group belongs to this enterprise
		var groupEnterpriseID string
		err = r.db.QueryRow(c.Request.Context(),
			`SELECT enterprise_id::text FROM user_groups WHERE id = $1::uuid`,
			req.GroupID,
		).Scan(&groupEnterpriseID)
		if err != nil || groupEnterpriseID != enterpriseID {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Group not found in your enterprise"}})
			return
		}

		// Store the enterprise group id directly (same semantics as key
		// creation); platform parent group routing is resolved at validation time.
		err = r.db.Exec(c.Request.Context(),
			`UPDATE api_keys SET group_id = $1::uuid, updated_at = NOW() WHERE id = $2::uuid`,
			req.GroupID, keyID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update group"}})
			return
		}
	}

	// Update models / routing for active or reassigned keys. Every model must
	// belong to the key's effective group (new group if changed above, else current group).
	if len(req.Models) > 0 {
		effGroupID := req.GroupID
		if effGroupID == "" {
			_ = r.db.QueryRow(c.Request.Context(),
				`SELECT COALESCE(group_id::text, '') FROM api_keys WHERE id = $1::uuid`, keyID,
			).Scan(&effGroupID)
		}
		var groupModels []string
		if effGroupID != "" {
			var entModels []byte
			if err := r.db.QueryRow(c.Request.Context(),
				`SELECT COALESCE(models::text, '[]')::bytea FROM user_groups WHERE id = $1::uuid AND enterprise_id = $2::uuid`,
				effGroupID, enterpriseID,
			).Scan(&entModels); err == nil {
				_ = json.Unmarshal(entModels, &groupModels)
			}
		}
		if len(groupModels) > 0 {
			for _, m := range req.Models {
				if !containsString(groupModels, m) {
					c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf("Model %s is not in the selected group", m)}})
					return
				}
			}
		}
		routingMode := req.ModelRoutingMode
		if routingMode == "" {
			routingMode = "single"
		}
		switch routingMode {
		case "single", "round_robin", "priority":
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "model_routing_mode must be single, round_robin or priority"}})
			return
		}
		currentModel := req.Models[0]
		err = r.db.Exec(c.Request.Context(),
			`UPDATE api_keys SET allowed_models = $1, selected_models = $2, current_model = $3, model_routing_mode = $4, updated_at = NOW() WHERE id = $5::uuid`,
			req.Models, req.Models, currentModel, routingMode, keyID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update models"}})
			return
		}
	}

	// Update expiry (renewal). expires_in present: 0 = never, else days from now.
	if req.ExpiresIn != nil {
		if !validKeyExpiresInDays[*req.ExpiresIn] {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "expires_in must be one of 0, 30, 90, 365"}})
			return
		}
		err = r.db.Exec(c.Request.Context(),
			`UPDATE api_keys SET expires_at = $1, updated_at = NOW() WHERE id = $2::uuid`,
			keyExpiresAt(*req.ExpiresIn), keyID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update expiry"}})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})

	if req.AssignedTo != "" && req.AssignedTo != oldAssignedUserID {
		r.logEnterpriseAudit(c.Request.Context(), enterpriseAuditEntry{
			EnterpriseID: enterpriseID,
			Category:     "key",
			ChangeType:   "key_reassigned",
			FieldName:    "assigned_user_id",
			UserID:       &req.AssignedTo,
			ChangedBy:    c.GetString("user_id"),
			OldValueStr:  &oldAssignedUserID,
			NewValueStr:  &req.AssignedTo,
			TargetType:   "key",
			TargetID:     keyID,
			Reason:       fmt.Sprintf("Key '%s' reassigned", keyName),
		})
	}

	if keyHash != "" {
		invalidateValidateCache(c.Request.Context(), r.db, r.logger, keyHash)
	}
}

// revokeEnterpriseKey revokes an enterprise API key
func (r *EnterpriseRoutes) revokeEnterpriseKey(c *gin.Context) {
	keyID := c.Param("keyId")
	enterpriseID := c.GetString("enterprise_id")

	var keyHash, keyStatus, keyName, assignedUserID string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT key_hash, status, name, COALESCE(assigned_user_id::text, '') FROM api_keys WHERE id = $1::uuid AND enterprise_id = $2::uuid AND deleted_at IS NULL`,
		keyID, enterpriseID,
	).Scan(&keyHash, &keyStatus, &keyName, &assignedUserID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Key not found"}})
		return
	}
	if keyStatus != "active" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Only active keys can be revoked"}})
		return
	}

	err = r.db.Exec(c.Request.Context(),
		`UPDATE api_keys SET status = 'revoked', updated_at = NOW()
		 WHERE id = $1::uuid AND enterprise_id = $2::uuid AND status = 'active'`,
		keyID, enterpriseID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to revoke key"}})
		return
	}

	oldStatus := "active"
	newStatus := "revoked"
	r.logEnterpriseAudit(c.Request.Context(), enterpriseAuditEntry{
		EnterpriseID: enterpriseID,
		Category:     "key",
		ChangeType:   "key_revoked",
		FieldName:    "status",
		UserID:       &assignedUserID,
		ChangedBy:    c.GetString("user_id"),
		OldValueStr:  &oldStatus,
		NewValueStr:  &newStatus,
		TargetType:   "key",
		TargetID:     keyID,
		Reason:       fmt.Sprintf("Key '%s' revoked", keyName),
	})

	c.JSON(http.StatusOK, gin.H{"success": true})

	invalidateValidateCache(c.Request.Context(), r.db, r.logger, keyHash)
}

// reactivateEnterpriseKey reactivates a revoked enterprise API key
func (r *EnterpriseRoutes) reactivateEnterpriseKey(c *gin.Context) {
	keyID := c.Param("keyId")
	enterpriseID := c.GetString("enterprise_id")

	var keyHash, keyStatus, keyName, assignedUserID string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT key_hash, status, name, COALESCE(assigned_user_id::text, '') FROM api_keys WHERE id = $1::uuid AND enterprise_id = $2::uuid AND deleted_at IS NULL`,
		keyID, enterpriseID,
	).Scan(&keyHash, &keyStatus, &keyName, &assignedUserID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Key not found"}})
		return
	}
	if keyStatus != "revoked" && keyStatus != "disabled" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Only revoked or disabled keys can be reactivated"}})
		return
	}

	err = r.db.Exec(c.Request.Context(),
		`UPDATE api_keys SET status = 'active', updated_at = NOW()
		 WHERE id = $1::uuid AND enterprise_id = $2::uuid AND status IN ('revoked', 'disabled')`,
		keyID, enterpriseID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to reactivate key"}})
		return
	}

	newStatus := "active"
	r.logEnterpriseAudit(c.Request.Context(), enterpriseAuditEntry{
		EnterpriseID: enterpriseID,
		Category:     "key",
		ChangeType:   "key_reactivated",
		FieldName:    "status",
		UserID:       &assignedUserID,
		ChangedBy:    c.GetString("user_id"),
		OldValueStr:  &keyStatus,
		NewValueStr:  &newStatus,
		TargetType:   "key",
		TargetID:     keyID,
		Reason:       fmt.Sprintf("Key '%s' reactivated", keyName),
	})

	c.JSON(http.StatusOK, gin.H{"success": true})

	invalidateValidateCache(c.Request.Context(), r.db, r.logger, keyHash)
}

// deleteEnterpriseKey soft-deletes an enterprise API key
func (r *EnterpriseRoutes) deleteEnterpriseKey(c *gin.Context) {
	keyID := c.Param("keyId")
	enterpriseID := c.GetString("enterprise_id")

	var keyHash, keyName, keyStatus, assignedUserID string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT key_hash, name, status, COALESCE(assigned_user_id::text, '') FROM api_keys WHERE id = $1::uuid AND enterprise_id = $2::uuid AND deleted_at IS NULL`,
		keyID, enterpriseID,
	).Scan(&keyHash, &keyName, &keyStatus, &assignedUserID)

	err := r.db.Exec(c.Request.Context(),
		`UPDATE api_keys SET deleted_at = NOW(), updated_at = NOW()
		 WHERE id = $1::uuid AND enterprise_id = $2::uuid`,
		keyID, enterpriseID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to delete key"}})
		return
	}

	newStatus := "deleted"
	r.logEnterpriseAudit(c.Request.Context(), enterpriseAuditEntry{
		EnterpriseID: enterpriseID,
		Category:     "key",
		ChangeType:   "key_deleted",
		FieldName:    "status",
		UserID:       &assignedUserID,
		ChangedBy:    c.GetString("user_id"),
		OldValueStr:  &keyStatus,
		NewValueStr:  &newStatus,
		TargetType:   "key",
		TargetID:     keyID,
		Reason:       fmt.Sprintf("Key '%s' deleted", keyName),
	})

	c.JSON(http.StatusOK, gin.H{"success": true})

	invalidateValidateCache(c.Request.Context(), r.db, r.logger, keyHash)
}

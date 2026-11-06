package routes

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// listEnterpriseGroups returns groups owned by this enterprise
func (r *EnterpriseRoutes) listEnterpriseGroups(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")

	rows, err := r.db.Query(c.Request.Context(),
		`SELECT g.id::text, g.name, g.group_type, g.models, g.rpm, g.rph, g.tpm, g.tph, g.tpmonth,
		        COALESCE(g.concurrent_limit, 0), g.created_at::text,
		        COALESCE(pg.name, ''), COALESCE(g.visibility, 'public'), FALSE AS inherited
		 FROM user_groups g
		 LEFT JOIN user_groups pg ON g.parent_group_id = pg.id
		 WHERE g.enterprise_id = $1::uuid AND g.deleted_at IS NULL
		 UNION ALL
		 -- Inherited platform template groups: visible to every enterprise,
		 -- read-only for enterprise admins (no local row exists).
		 SELECT g.id::text, g.name, g.group_type, g.models, g.rpm, g.rph, g.tpm, g.tph, g.tpmonth,
		        COALESCE(g.concurrent_limit, 0), g.created_at::text,
		        '' AS parent_name, 'public' AS visibility, TRUE AS inherited
		 FROM user_groups g
		 WHERE g.enterprise_id IS NULL AND g.group_type = 'enterprise' AND g.inherit_to_enterprises AND g.deleted_at IS NULL
		 ORDER BY name`,
		enterpriseID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list groups"}})
		return
	}
	defer rows.Close()

	type groupItem struct {
		ID              string      `json:"id"`
		Name            string      `json:"name"`
		GroupType       string      `json:"group_type"`
		Models          interface{} `json:"models"`
		RPM             int         `json:"rpm"`
		RPH             int64       `json:"rph"`
		TPM             int64       `json:"tpm"`
		TPH             int64       `json:"tph"`
		TPMonth         int64       `json:"tpmonth"`
		ConcurrentLimit int         `json:"concurrent_limit"`
		ParentGroupName string      `json:"parent_group_name"`
		Visibility      string      `json:"visibility"`
		CreatedAt       string      `json:"created_at"`
		Inherited       bool        `json:"inherited"`
	}

	var groups []groupItem
	for rows.Next() {
		var g groupItem
		if err := rows.Scan(&g.ID, &g.Name, &g.GroupType, &g.Models, &g.RPM, &g.RPH, &g.TPM, &g.TPH, &g.TPMonth,
			&g.ConcurrentLimit, &g.CreatedAt, &g.ParentGroupName, &g.Visibility, &g.Inherited); err != nil {
			continue
		}
		groups = append(groups, g)
	}
	if groups == nil {
		groups = []groupItem{}
	}
	c.JSON(http.StatusOK, gin.H{"groups": groups})
}

// createEnterpriseGroup creates a new group for this enterprise
func (r *EnterpriseRoutes) createEnterpriseGroup(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")

	var req struct {
		Name            string   `json:"name" binding:"required"`
		PlatformGroupID string   `json:"platform_group_id" binding:"required"`
		Models          []string `json:"models"`
		RPM             int      `json:"rpm"`
		TPM             int64    `json:"tpm"`
		ConcurrentLimit *int     `json:"concurrent_limit"`
		Visibility      string   `json:"visibility"`
		SyncMode        string   `json:"sync_mode"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "name and platform_group_id are required"}})
		return
	}

	// Validate platform group exists and is a platform enterprise group
	var platformGroupType string
	var platformModels []byte
	var platformRPM int
	var platformTPM int64
	var allowedEnterpriseIDs []byte
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT group_type, COALESCE(models::text, '[]')::bytea, COALESCE(rpm, 0), COALESCE(tpm, 0),
		        COALESCE(allowed_enterprise_ids::text, '[]')::bytea
		 FROM user_groups WHERE id = $1::uuid AND enterprise_id IS NULL AND deleted_at IS NULL`,
		req.PlatformGroupID,
	).Scan(&platformGroupType, &platformModels, &platformRPM, &platformTPM, &allowedEnterpriseIDs)
	if err != nil || platformGroupType != "enterprise" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Platform group not found or not an enterprise group"}})
		return
	}

	// Check enterprise is allowed to use this platform group
	if len(allowedEnterpriseIDs) > 0 {
		var allowedIDs []string
		if jsonErr := json.Unmarshal(allowedEnterpriseIDs, &allowedIDs); jsonErr == nil && len(allowedIDs) > 0 {
			allowed := false
			for _, id := range allowedIDs {
				if id == enterpriseID {
					allowed = true
					break
				}
			}
			if !allowed {
				c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": T("error.platform_group_not_available", c)}})
				return
			}
		}
	}

	// Validate that requested models are a subset of the platform group's models.
	// An enterprise group must never expose models its parent platform group does not.
	// Follow-mode groups skip the subset check entirely: their model list is the
	// platform group's, re-materialized on every platform-side change.
	var platformModelList []string
	if len(platformModels) > 0 {
		if err := json.Unmarshal(platformModels, &platformModelList); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to parse platform group models"}})
			return
		}
	}
	if req.SyncMode == "follow" {
		req.Models = platformModelList
	} else if req.SyncMode == "" {
		req.SyncMode = "manual"
	} else if req.SyncMode != "manual" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "sync_mode must be 'manual' or 'follow'"}})
		return
	}
	if req.SyncMode == "manual" && len(req.Models) > 0 {
		platformSet := make(map[string]struct{}, len(platformModelList))
		for _, m := range platformModelList {
			platformSet[m] = struct{}{}
		}
		for _, m := range req.Models {
			if _, ok := platformSet[m]; !ok {
				c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf("Model '%s' is not available in the platform group", m)}})
				return
			}
		}
	}

	// Validate enterprise RPM ≤ platform RPM
	if req.RPM <= 0 {
		req.RPM = 500
	}
	if platformRPM > 0 && req.RPM > platformRPM {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf(T("error.rpm_exceeds_platform_limit", c), platformRPM)}})
		return
	}

	// Validate enterprise TPM against platform TPM (0 = unlimited: a child group
	// may only be unlimited when the platform group has no TPM limit).
	if req.TPM < 0 || (platformTPM > 0 && (req.TPM == 0 || req.TPM > platformTPM)) {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf(T("error.tpm_exceeds_platform_limit", c), platformTPM)}})
		return
	}

	modelsJSON, _ := json.Marshal(req.Models)
	if req.RPM <= 0 {
		req.RPM = 500
	}
	if req.Visibility == "" {
		req.Visibility = "public"
	}
	if req.Visibility != "public" && req.Visibility != "admin_only" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.invalid_visibility", c)}})
		return
	}

	concurrentLimit := 10
	if req.ConcurrentLimit != nil {
		concurrentLimit = *req.ConcurrentLimit
	}

	var groupID string
	err = r.db.QueryRow(c.Request.Context(),
		`INSERT INTO user_groups (name, group_type, models, rpm, tpm, enterprise_id, parent_group_id, visibility, concurrent_limit, sync_mode)
		 VALUES ($1, 'enterprise', $2::jsonb, $3, $4, $5::uuid, $6::uuid, $7, $8, $9)
		 RETURNING id::text`,
		req.Name, string(modelsJSON), req.RPM, req.TPM, enterpriseID, req.PlatformGroupID, req.Visibility, concurrentLimit, req.SyncMode,
	).Scan(&groupID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to create group"}})
		return
	}

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"),
			"enterprise", "group_created", "info",
			fmt.Sprintf("Enterprise group '%s' created for enterprise %s", req.Name, enterpriseID))
	}

	groupName := req.Name
	r.logEnterpriseAudit(c.Request.Context(), enterpriseAuditEntry{
		EnterpriseID: enterpriseID,
		Category:     "group",
		ChangeType:   "group_created",
		FieldName:    "group",
		ChangedBy:    c.GetString("user_id"),
		NewValueStr:  &groupName,
		Reason:       fmt.Sprintf("Enterprise group '%s' created", req.Name),
	})

	c.JSON(http.StatusOK, gin.H{"success": true, "group_id": groupID})
}

// updateEnterpriseGroup updates an enterprise group
func (r *EnterpriseRoutes) updateEnterpriseGroup(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	groupID := c.Param("groupId")

	var ownerID string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT enterprise_id::text FROM user_groups WHERE id = $1::uuid AND deleted_at IS NULL`,
		groupID).Scan(&ownerID)
	if err != nil || ownerID != enterpriseID {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Group not found"}})
		return
	}

	var req struct {
		Name            *string   `json:"name"`
		Models          *[]string `json:"models"`
		RPM             *int      `json:"rpm"`
		TPM             *int64    `json:"tpm"`
		ConcurrentLimit *int      `json:"concurrent_limit"`
		Visibility      *string   `json:"visibility"`
		Status          *string   `json:"status"`
		SyncMode        *string   `json:"sync_mode"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}
	if req.SyncMode != nil && *req.SyncMode != "manual" && *req.SyncMode != "follow" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "sync_mode must be 'manual' or 'follow'"}})
		return
	}
	// Switching to follow: the model list is the parent platform group's,
	// re-materialized now; the caller's Models field is ignored in that case.
	if req.SyncMode != nil && *req.SyncMode == "follow" {
		req.Models = nil
		if err := r.db.Exec(c.Request.Context(),
			`UPDATE user_groups child SET models = parent.models, updated_at = NOW()
			 FROM user_groups parent
			 WHERE parent.id = child.parent_group_id AND child.id = $1::uuid
			   AND child.deleted_at IS NULL`, groupID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to sync models from platform group"}})
			return
		}
	}

	// Validate RPM/TPM against the parent platform group limits (same rules as
	// group creation: RPM must not exceed the platform limit; TPM 0 = unlimited
	// is only allowed when the platform group has no TPM limit).
	if req.RPM != nil || req.TPM != nil {
		var platformRPM int
		var platformTPM int64
		err = r.db.QueryRow(c.Request.Context(),
			`SELECT COALESCE(pg.rpm, 0), COALESCE(pg.tpm, 0)
			 FROM user_groups g
			 LEFT JOIN user_groups pg ON g.parent_group_id = pg.id
			 WHERE g.id = $1::uuid`,
			groupID,
		).Scan(&platformRPM, &platformTPM)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to load platform group limits"}})
			return
		}
		if req.RPM != nil && platformRPM > 0 && *req.RPM > platformRPM {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf(T("error.rpm_exceeds_platform_limit", c), platformRPM)}})
			return
		}
		if req.TPM != nil && (*req.TPM < 0 || (platformTPM > 0 && (*req.TPM == 0 || *req.TPM > platformTPM))) {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf(T("error.tpm_exceeds_platform_limit", c), platformTPM)}})
			return
		}
	}

	if req.Visibility != nil && *req.Visibility != "public" && *req.Visibility != "admin_only" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.invalid_visibility", c)}})
		return
	}

	// Validate that requested models are a subset of the parent platform
	// group's models (same rule as group creation).
	if req.Models != nil {
		var platformModelsJSON []byte
		err = r.db.QueryRow(c.Request.Context(),
			`SELECT COALESCE(pg.models::text, '[]')::bytea
			 FROM user_groups g
			 LEFT JOIN user_groups pg ON g.parent_group_id = pg.id
			 WHERE g.id = $1::uuid`,
			groupID,
		).Scan(&platformModelsJSON)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to load platform group models"}})
			return
		}
		var platformModelList []string
		if len(platformModelsJSON) > 0 {
			if err := json.Unmarshal(platformModelsJSON, &platformModelList); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to parse platform group models"}})
				return
			}
		}
		platformSet := make(map[string]struct{}, len(platformModelList))
		for _, m := range platformModelList {
			platformSet[m] = struct{}{}
		}
		for _, m := range *req.Models {
			if _, ok := platformSet[m]; !ok {
				c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf("Model '%s' is not available in the platform group", m)}})
				return
			}
		}
	}

	// Validate status against a whitelist before applying.
	// NOTE: user_groups.status has a DB CHECK constraint allowing only
	// 'active' and 'inactive' (see schema.go). The whitelist must match.
	validStatuses := map[string]bool{"active": true, "inactive": true}
	if req.Status != nil && !validStatuses[*req.Status] {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "status must be one of: active, inactive"}})
		return
	}

	// Build a single dynamic UPDATE for all non-status fields, always
	// setting updated_at. Errors are returned to the client instead of
	// being swallowed.
	var setClauses []string
	var args []interface{}
	var changedFields []string
	argIdx := 1
	if req.Name != nil {
		setClauses = append(setClauses, fmt.Sprintf("name = $%d", argIdx))
		args = append(args, *req.Name)
		argIdx++
		changedFields = append(changedFields, "name")
	}
	if req.Models != nil {
		modelsJSON, _ := json.Marshal(*req.Models)
		setClauses = append(setClauses, fmt.Sprintf("models = $%d::jsonb", argIdx))
		args = append(args, string(modelsJSON))
		argIdx++
		changedFields = append(changedFields, "models")
	}
	if req.RPM != nil {
		setClauses = append(setClauses, fmt.Sprintf("rpm = $%d", argIdx))
		args = append(args, *req.RPM)
		argIdx++
		changedFields = append(changedFields, "rpm")
	}
	if req.TPM != nil {
		setClauses = append(setClauses, fmt.Sprintf("tpm = $%d", argIdx))
		args = append(args, *req.TPM)
		argIdx++
		changedFields = append(changedFields, "tpm")
	}
	if req.ConcurrentLimit != nil {
		setClauses = append(setClauses, fmt.Sprintf("concurrent_limit = $%d", argIdx))
		args = append(args, *req.ConcurrentLimit)
		argIdx++
		changedFields = append(changedFields, "concurrent_limit")
	}
	if req.Visibility != nil {
		setClauses = append(setClauses, fmt.Sprintf("visibility = $%d", argIdx))
		args = append(args, *req.Visibility)
		argIdx++
		changedFields = append(changedFields, "visibility")
	}
	if req.SyncMode != nil {
		setClauses = append(setClauses, fmt.Sprintf("sync_mode = $%d", argIdx))
		args = append(args, *req.SyncMode)
		argIdx++
		changedFields = append(changedFields, "sync_mode")
	}
	if len(setClauses) > 0 {
		setClauses = append(setClauses, "updated_at = NOW()")
		query := fmt.Sprintf("UPDATE user_groups SET %s WHERE id = $%d::uuid", strings.Join(setClauses, ", "), argIdx)
		args = append(args, groupID)
		if err := r.db.Exec(c.Request.Context(), query, args...); err != nil {
			r.logger.Error("Failed to update enterprise group", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update group"}})
			return
		}
	}

	if req.Status != nil {
		// Changing group status and cascading key disable must be atomic.
		var disabledKeyHashes []string
		err = r.db.WithTx(c.Request.Context(), func(tx pgx.Tx) error {
			if _, txErr := tx.Exec(c.Request.Context(),
				`UPDATE user_groups SET status = $1, updated_at = NOW() WHERE id = $2::uuid`, *req.Status, groupID); txErr != nil {
				return txErr
			}
			// Disabling the group disables its active keys (they can be
			// re-enabled individually later); re-enabling does NOT restore them.
			if *req.Status != "active" {
				hashRows, txErr := tx.Query(c.Request.Context(),
					`SELECT key_hash FROM api_keys WHERE group_id = $1::uuid AND deleted_at IS NULL AND status = 'active'`,
					groupID)
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
					 WHERE group_id = $1::uuid AND deleted_at IS NULL AND status = 'active'`,
					groupID); txErr != nil {
					return txErr
				}
			}
			return nil
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update group status"}})
			return
		}
		for _, h := range disabledKeyHashes {
			invalidateValidateCache(c.Request.Context(), r.db, r.logger, h)
		}
		if r.audit != nil {
			r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"),
				"enterprise", "group_status_changed", "warning",
				fmt.Sprintf("Enterprise group %s status changed to %s (cascaded disable to %d key(s))", groupID, *req.Status, len(disabledKeyHashes)))
		}

		oldStatus := "active"
		if *req.Status == "active" {
			oldStatus = "inactive"
		}
		newStatus := *req.Status
		r.logEnterpriseAudit(c.Request.Context(), enterpriseAuditEntry{
			EnterpriseID: enterpriseID,
			Category:     "group",
			ChangeType:   "group_status_changed",
			FieldName:    "status",
			ChangedBy:    c.GetString("user_id"),
			OldValueStr:  &oldStatus,
			NewValueStr:  &newStatus,
			Reason:       fmt.Sprintf("Group status changed to %s (%d key(s) disabled)", newStatus, len(disabledKeyHashes)),
		})
	}

	if len(changedFields) > 0 {
		fieldsStr := strings.Join(changedFields, ",")
		r.logEnterpriseAudit(c.Request.Context(), enterpriseAuditEntry{
			EnterpriseID: enterpriseID,
			Category:     "group",
			ChangeType:   "group_updated",
			FieldName:    "group",
			ChangedBy:    c.GetString("user_id"),
			NewValueStr:  &fieldsStr,
			Reason:       fmt.Sprintf("Group updated: %s", fieldsStr),
		})
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// getAvailableModels returns all active models from the platform's model registry
func (r *EnterpriseRoutes) getAvailableModels(c *gin.Context) {
	platformGroupID := c.Query("platform_group_id")

	if platformGroupID != "" {
		var modelsJSON string
		err := r.db.QueryRow(c.Request.Context(),
			`SELECT COALESCE(models::text, '[]') FROM user_groups
			 WHERE id = $1::uuid AND enterprise_id IS NULL AND group_type = 'enterprise' AND deleted_at IS NULL`,
			platformGroupID,
		).Scan(&modelsJSON)

		var models []string
		if err == nil && modelsJSON != "" && modelsJSON != "[]" && modelsJSON != "null" {
			_ = json.Unmarshal([]byte(modelsJSON), &models)
		}
		// The parent platform group defines the ceiling of what an enterprise
		// group may expose. If it has no models configured, the enterprise
		// group must see none — never fall back to the full platform catalog
		// (that would let a child group expose models its parent does not).
		if models == nil {
			models = []string{}
		}

		// Look up validation status so the UI can disable models that failed
		// validation. Only echo back the names as they appear in the group's
		// model list — never expose product_id to enterprise users, since it
		// would reveal the upstream provider/model identifier.
		invalidModels := []string{}
		if len(models) > 0 {
			vRows, vErr := r.db.Query(c.Request.Context(),
				`SELECT DISTINCT m.name
				 FROM unnest($1::text[]) AS m(name)
				 JOIN service_models sm ON sm.product_id = m.name OR sm.api_model_name = m.name
				 WHERE sm.status IN ('active', 'eol_pending') AND sm.validation_status = 'invalid'`, models)
			if vErr == nil {
				defer vRows.Close()
				for vRows.Next() {
					var name string
					if err := vRows.Scan(&name); err == nil && name != "" {
						invalidModels = append(invalidModels, name)
					}
				}
			}
		}
		c.JSON(http.StatusOK, gin.H{"models": models, "invalid_models": invalidModels})
		return
	}
	c.JSON(http.StatusOK, gin.H{"models": []string{}, "invalid_models": []string{}})
}

// getPlatformGroups returns platform enterprise groups available for selection
func (r *EnterpriseRoutes) getPlatformGroups(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")

	rows, err := r.db.Query(c.Request.Context(),
		`SELECT id::text, name, COALESCE(rpm, 0), COALESCE(rph, 0), COALESCE(rp5h, 0),
		        COALESCE(rpw, 0), COALESCE(rpmonth, 0),
		        COALESCE(tpm, 0), COALESCE(tph, 0), COALESCE(tpd, 0),
		        COALESCE(tpw, 0), COALESCE(tpmonth, 0),
		        COALESCE(pricing_ratio, 1.0), COALESCE(models::text, '[]')
		 FROM user_groups WHERE group_type = 'enterprise' AND enterprise_id IS NULL AND deleted_at IS NULL
		   AND (allowed_enterprise_ids IS NULL OR allowed_enterprise_ids = '[]'::jsonb OR allowed_enterprise_ids @> to_jsonb($1::text)
		        OR inherit_to_enterprises)
		 ORDER BY name`, enterpriseID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to load platform groups"}})
		return
	}
	defer rows.Close()

	type pg struct {
		ID           string  `json:"id"`
		Name         string  `json:"name"`
		RPM          int     `json:"rpm"`
		RPH          int64   `json:"rph"`
		RP5H         int64   `json:"rp5h"`
		RPW          int64   `json:"rpw"`
		RPMonth      int64   `json:"rpmonth"`
		TPM          int64   `json:"tpm"`
		TPH          int64   `json:"tph"`
		TPD          int64   `json:"tpd"`
		TPW          int64   `json:"tpw"`
		TPMonth      int64   `json:"tpmonth"`
		PricingRatio float64 `json:"pricing_ratio"`
		ModelCount   int     `json:"model_count"`
	}
	var groups []pg
	for rows.Next() {
		var g pg
		var modelsJSON string
		if err := rows.Scan(&g.ID, &g.Name, &g.RPM, &g.RPH, &g.RP5H, &g.RPW, &g.RPMonth,
			&g.TPM, &g.TPH, &g.TPD, &g.TPW, &g.TPMonth, &g.PricingRatio, &modelsJSON); err != nil {
			continue
		}
		var models []string
		if err := json.Unmarshal([]byte(modelsJSON), &models); err == nil {
			g.ModelCount = len(models)
		}
		groups = append(groups, g)
	}
	if groups == nil {
		groups = []pg{}
	}
	c.JSON(http.StatusOK, gin.H{"groups": groups})
}

// deleteEnterpriseGroup deletes an enterprise group — old name preserved below
func (r *EnterpriseRoutes) deleteEnterpriseGroup(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	groupID := c.Param("groupId")

	var ownerID string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT enterprise_id::text FROM user_groups WHERE id = $1::uuid AND deleted_at IS NULL`,
		groupID).Scan(&ownerID)
	if err != nil || ownerID != enterpriseID {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Group not found"}})
		return
	}

	// Block deletion while active keys still reference this group (aligns with
	// the system admin group deletion protection in groups.go). Disabled keys
	// cannot authenticate, so they don't block; they are detached below.
	var keyCount int
	err = r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM api_keys WHERE group_id = $1::uuid AND deleted_at IS NULL AND status = 'active'`, groupID,
	).Scan(&keyCount)
	if err == nil && keyCount > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf(T("error.group_has_active_keys", c), keyCount)}})
		return
	}

	// Detach non-active and soft-deleted keys so that reactivating them later
	// falls back to default routing instead of a deleted group.
	if err := r.db.Exec(c.Request.Context(),
		`UPDATE api_keys SET group_id = NULL WHERE group_id = $1::uuid AND (deleted_at IS NOT NULL OR status <> 'active')`, groupID); err != nil {
		r.logger.Error("Failed to detach keys from enterprise group", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to delete group"}})
		return
	}

	// Soft delete: mark group as deleted (hidden from UI)
	if err := r.db.Exec(c.Request.Context(),
		`UPDATE user_groups SET deleted_at = NOW(), updated_at = NOW() WHERE id = $1::uuid`, groupID); err != nil {
		r.logger.Error("Failed to delete enterprise group", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to delete group"}})
		return
	}

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"),
			"enterprise", "group_deleted", "warning",
			fmt.Sprintf("Enterprise group deleted from enterprise %s", enterpriseID))
	}

	deletedStatus := "deleted"
	r.logEnterpriseAudit(c.Request.Context(), enterpriseAuditEntry{
		EnterpriseID: enterpriseID,
		Category:     "group",
		ChangeType:   "group_deleted",
		FieldName:    "status",
		ChangedBy:    c.GetString("user_id"),
		NewValueStr:  &deletedStatus,
		Reason:       "Enterprise group deleted",
	})

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// getMyEnterprisePublicGroups returns public enterprise groups (member-accessible)
func (r *EnterpriseRoutes) getMyEnterprisePublicGroups(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT id::text, name, COALESCE(rpm, 0), COALESCE(tpm, 0)
		 FROM user_groups WHERE enterprise_id = $1::uuid AND visibility = 'public'
		 ORDER BY name`,
		enterpriseID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to load groups"}})
		return
	}
	defer rows.Close()

	type g struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		RPM  int    `json:"rpm"`
		TPM  int64  `json:"tpm"`
	}
	var groups []g
	for rows.Next() {
		var item g
		if err := rows.Scan(&item.ID, &item.Name, &item.RPM, &item.TPM); err == nil {
			groups = append(groups, item)
		}
	}
	if groups == nil {
		groups = []g{}
	}
	c.JSON(http.StatusOK, gin.H{"groups": groups})
}

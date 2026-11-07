package routes

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/internal/middleware"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

type GroupRoutes struct {
	db   *db.DB
	auth *middleware.Auth
}

func NewGroupRoutes(db *db.DB, auth *middleware.Auth) *GroupRoutes {
	return &GroupRoutes{db: db, auth: auth}
}

func (r *GroupRoutes) Register(rg *gin.RouterGroup) {
	rg.Use(r.auth.RequireAuth(), r.auth.RequireAdmin())
	rg.GET("", r.listGroups)
	rg.GET("/:id", r.getGroup)
	rg.POST("", r.createGroup)
	rg.PUT("/:id", r.updateGroup)
	rg.DELETE("/:id", r.deleteGroup)
	rg.POST("/:id/members", r.addMembers)
	rg.DELETE("/:id/members/:userId", r.removeMember)
	rg.GET("/:id/models-with-status", r.getGroupModelsWithStatus)
	rg.POST("/:id/sync-follow-children", r.syncFollowChildrenHandler)
}

// syncFollowChildren re-materializes the model lists of every follow-mode
// enterprise child of the given platform group, and invalidates the validate
// cache of keys bound to those children. Drift behind platform-side model
// maintenance is the failure mode this exists for: enterprise admins rarely
// revisit their groups, so without it new models stay unreachable and retired
// models linger.
func (r *GroupRoutes) syncFollowChildren(c *gin.Context, platformGroupID string) int64 {
	tag, err := r.db.Pool.Exec(c.Request.Context(),
		`UPDATE user_groups child SET models = parent.models, updated_at = NOW()
		 FROM user_groups parent
		 WHERE parent.id = child.parent_group_id
		   AND parent.id = $1::uuid
		   AND child.sync_mode = 'follow'
		   AND child.deleted_at IS NULL`,
		platformGroupID)
	if err != nil {
		zap.L().Warn("follow-group sync failed", zap.String("platform_group", platformGroupID), zap.Error(err))
		return 0
	}
	hashRows, err := r.db.Query(c.Request.Context(),
		`SELECT ak.key_hash FROM api_keys ak
		 JOIN user_groups g ON g.id = ak.group_id
		 WHERE g.parent_group_id = $1::uuid AND g.sync_mode = 'follow'
		   AND ak.deleted_at IS NULL`,
		platformGroupID)
	if err == nil {
		for hashRows.Next() {
			var h string
			if scanErr := hashRows.Scan(&h); scanErr == nil {
				invalidateValidateCache(c.Request.Context(), r.db, zap.L(), h)
			}
		}
		hashRows.Close()
	}
	return tag.RowsAffected()
}

// syncFollowChildrenHandler exposes the sync to admins (monitoring page button
// or manual repair after bulk model operations).
func (r *GroupRoutes) syncFollowChildrenHandler(c *gin.Context) {
	n := r.syncFollowChildren(c, c.Param("id"))
	c.JSON(http.StatusOK, gin.H{"success": true, "synced": n})
}

// RegisterPublic registers public group routes (no admin required, filtered by user role)
func (r *GroupRoutes) RegisterPublic(rg *gin.RouterGroup) {
	rg.Use(r.auth.RequireAuth())
	rg.GET("", r.listUserGroups)
	rg.GET("/:id/models-with-status", r.getGroupModelsWithStatus)
	rg.GET("/:id/pricing", r.getGroupModelPricing)
}

// listUserGroups returns groups that the current user is allowed to see based on their role
func (r *GroupRoutes) listUserGroups(c *gin.Context) {
	userID := c.GetString("user_id")
	userRole := c.GetString("role")

	switch userRole {
	case "super_admin", "system_admin":
		// Admins see platform groups (non-enterprise) + ALL enterprise groups (for test/validation)
		rows, err := r.db.Query(c.Request.Context(),
			`SELECT g.id::text, g.name, g.description, g.group_type, g.billing_mode,
			        g.pricing_ratio, g.models, g.rate_limit_rpm, g.concurrent_limit, g.status, g.created_at, g.updated_at,
			        g.inherit_to_enterprises
			 FROM user_groups g
			 WHERE g.deleted_at IS NULL AND g.status = 'active'
			   AND ((g.enterprise_id IS NULL AND g.group_type != 'enterprise')
			        OR g.group_type = 'enterprise')
			 ORDER BY g.is_default DESC, g.created_at DESC`)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list groups"}})
			return
		}
		defer rows.Close()
		c.JSON(http.StatusOK, gin.H{"groups": r.scanGroups(rows)})
		return

	case "enterprise_admin":
		// Enterprise admins see platform groups (non-enterprise) + ALL their own enterprise's groups (public + admin_only)
		// + platform template groups marked inherit_to_enterprises.
		rows, err := r.db.Query(c.Request.Context(),
			`SELECT g.id::text, g.name, g.description, g.group_type, g.billing_mode,
			        g.pricing_ratio, g.models, g.rate_limit_rpm, g.concurrent_limit, g.status, g.created_at, g.updated_at,
			        g.inherit_to_enterprises
			 FROM user_groups g
			 WHERE g.deleted_at IS NULL AND g.status = 'active'
			   AND ((g.enterprise_id IS NULL AND g.group_type != 'enterprise')
			        OR (g.enterprise_id IS NULL AND g.group_type = 'enterprise' AND g.inherit_to_enterprises)
			        OR g.enterprise_id = (
			            SELECT u.enterprise_id FROM users u WHERE u.id = $1::uuid
			        ))
			 ORDER BY g.is_default DESC, g.created_at DESC`,
			userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list groups"}})
			return
		}
		defer rows.Close()
		c.JSON(http.StatusOK, gin.H{"groups": r.scanGroups(rows)})
		return

	default:
		// Regular users: platform groups (non-enterprise) + own enterprise's public groups
		// + inherited platform template groups (enterprise members only).
		rows, err := r.db.Query(c.Request.Context(),
			`SELECT g.id::text, g.name, g.description, g.group_type, g.billing_mode,
			        g.pricing_ratio, g.models, g.rate_limit_rpm, g.concurrent_limit, g.status, g.created_at, g.updated_at,
			        g.inherit_to_enterprises
			 FROM user_groups g
			 WHERE g.deleted_at IS NULL AND g.status = 'active'
			   AND ((g.enterprise_id IS NULL AND g.group_type != 'enterprise')
			        OR (g.enterprise_id IS NULL AND g.group_type = 'enterprise' AND g.inherit_to_enterprises
			            AND (EXISTS(SELECT 1 FROM users u WHERE u.id = $1::uuid AND u.enterprise_id IS NOT NULL)
			                 OR EXISTS(SELECT 1 FROM enterprise_members em WHERE em.user_id = $1::uuid AND em.removed_at IS NULL)))
			        OR (g.visibility = 'public' AND g.enterprise_id = (
			            SELECT u.enterprise_id FROM users u WHERE u.id = $1::uuid
			        )))
			 ORDER BY g.is_default DESC, g.created_at DESC`,
			userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list groups"}})
			return
		}
		defer rows.Close()
		c.JSON(http.StatusOK, gin.H{"groups": r.scanGroups(rows)})
		return
	}
}

func (r *GroupRoutes) scanGroups(rows pgx.Rows) []gin.H {
	groups := []gin.H{}
	for rows.Next() {
		var id, name, groupType, billingMode, status string
		var description *string
		var pricingRatio float64
		var models interface{}
		var rateLimitRPM, concurrentLimit int
		var createdAt, updatedAt interface{}
		var inherit bool
		_ = rows.Scan(&id, &name, &description, &groupType, &billingMode,
			&pricingRatio, &models, &rateLimitRPM, &concurrentLimit, &status, &createdAt, &updatedAt, &inherit)
		groups = append(groups, gin.H{
			"id": id, "name": name, "description": description,
			"group_type":       groupType,
			"billing_mode":     billingMode,
			"pricing_ratio":    pricingRatio,
			"models":           models,
			"rate_limit_rpm":   rateLimitRPM,
			"concurrent_limit": concurrentLimit,
			"status":           status,
			"created_at":       createdAt,
			"updated_at":       updatedAt,
			"inherited":        inherit,
		})
	}
	return groups
}

func (r *GroupRoutes) listGroups(c *gin.Context) {
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT g.id::text, g.name, g.description, g.group_type, g.billing_mode,
	        g.pricing_ratio, g.routing_track, g.models, g.rate_limit_rpm, g.concurrent_limit, g.status, g.created_at, g.updated_at,
	        (SELECT COUNT(*) FROM users WHERE group_id = g.id AND deleted_at IS NULL) as member_count,
	        (SELECT COUNT(*) FROM api_keys WHERE group_id = g.id AND deleted_at IS NULL) as key_count,
	        g.enterprise_id::text, COALESCE(e.name, ''), g.parent_group_id::text, COALESCE(pg.name, ''),
	        g.sync_mode,
	        -- Models the parent platform group serves that this group lacks (drift behind).
	        (SELECT COUNT(*) FROM jsonb_array_elements_text(COALESCE(pg.models, '[]'::jsonb)) pm(name)
	         WHERE pm.name NOT IN (SELECT jsonb_array_elements_text(COALESCE(g.models, '[]'::jsonb)))) AS missing_models,
	        -- Models this group lists that are no longer servable (retired upstream).
	        (SELECT COUNT(*) FROM jsonb_array_elements_text(COALESCE(g.models, '[]'::jsonb)) cm(name)
	         WHERE cm.name NOT IN (
	           SELECT DISTINCT COALESCE(api_model_name, product_id) FROM service_models
	           WHERE status IN ('active', 'eol_pending', 'eol') AND deleted_at IS NULL)) AS stale_models
	 FROM user_groups g
	 LEFT JOIN enterprises e ON e.id = g.enterprise_id
	 LEFT JOIN user_groups pg ON pg.id = g.parent_group_id
	 WHERE g.deleted_at IS NULL ORDER BY g.created_at DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list groups"}})
		return
	}
	defer rows.Close()

	groups := []gin.H{}
	for rows.Next() {
		var id, name, groupType, billingMode, status, routingTrack string
		var description *string
		var pricingRatio float64
		var models interface{}
		var rateLimitRPM, concurrentLimit int
		var memberCount, keyCount int
		var enterpriseName, parentName, syncMode string
		var enterpriseID, parentID *string
		var missingModels, staleModels int
		var createdAt, updatedAt interface{}
		if scanErr := rows.Scan(&id, &name, &description, &groupType, &billingMode,
			&pricingRatio, &routingTrack, &models, &rateLimitRPM, &concurrentLimit, &status, &createdAt, &updatedAt, &memberCount, &keyCount,
			&enterpriseID, &enterpriseName, &parentID, &parentName, &syncMode, &missingModels, &staleModels); scanErr != nil {
			zap.L().Warn("listGroups: scan failed", zap.Error(scanErr))
			continue
		}
		groups = append(groups, gin.H{
			"id": id, "name": name, "description": description,
			"group_type":       groupType,
			"billing_mode":     billingMode,
			"pricing_ratio":    pricingRatio,
			"models":           models,
			"rate_limit_rpm":   rateLimitRPM,
			"concurrent_limit": concurrentLimit,
			"status":           status,
			"created_at":       createdAt,
			"updated_at":       updatedAt,
			"member_count":     memberCount,
			"key_count":        keyCount,
			"routing_track":    routingTrack,
			"enterprise_id":    enterpriseID,
			"enterprise_name":  enterpriseName,
			"parent_group_id":  parentID,
			"parent_name":      parentName,
			"sync_mode":        syncMode,
			"missing_models":   missingModels,
			"stale_models":     staleModels,
		})
	}
	c.JSON(http.StatusOK, gin.H{"groups": groups})
}

func (r *GroupRoutes) getGroup(c *gin.Context) {
	groupID := c.Param("id")

	var id, name, status string
	var description *string
	var pricingRatio float64
	var modelPermissions interface{}
	var rateLimitRPM, rateLimitTPM *int
	var concurrentLimit int
	var groupType, billingMode string
	var models interface{}
	var createdAt, updatedAt interface{}
	var routingTrack string
	var rpm, rph, rp5h, rpw, rpmonth *int
	var tpm, tph, tpd, tpw, tpmonth *int
	var allowedEnterpriseIDs interface{}
	var inheritToEnterprises bool
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT id, name, description, pricing_ratio, model_permissions, rate_limit_rpm, rate_limit_tpm, concurrent_limit, status,
		        group_type, billing_mode, models, created_at, updated_at, routing_track,
		        rpm, rph, rp5h, rpw, rpmonth, tpm, tph, tpd, tpw, tpmonth,
		        COALESCE(allowed_enterprise_ids, '[]'::jsonb), inherit_to_enterprises
		 FROM user_groups WHERE id = $1::uuid`, groupID,
	).Scan(&id, &name, &description, &pricingRatio, &modelPermissions, &rateLimitRPM, &rateLimitTPM, &concurrentLimit, &status,
		&groupType, &billingMode, &models, &createdAt, &updatedAt, &routingTrack,
		&rpm, &rph, &rp5h, &rpw, &rpmonth, &tpm, &tph, &tpd, &tpw, &tpmonth,
		&allowedEnterpriseIDs, &inheritToEnterprises)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Group not found"}})
		return
	}

	// Get members
	memberRows, err := r.db.Query(c.Request.Context(),
		`SELECT u.id, u.name, u.email, u.role FROM users u
		 WHERE u.group_id = $1::uuid AND u.deleted_at IS NULL LIMIT 100`, groupID)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"group": gin.H{
				"id": id, "name": name, "description": description,
				"pricing_ratio": pricingRatio, "model_permissions": modelPermissions,
				"rate_limit_rpm": rateLimitRPM, "rate_limit_tpm": rateLimitTPM, "concurrent_limit": concurrentLimit, "status": status,
				"group_type":    groupType,
				"billing_mode":  billingMode,
				"models":        models,
				"routing_track": routingTrack,
				"created_at":    createdAt, "updated_at": updatedAt,
				"rpm": rpm, "rph": rph, "rp5h": rp5h, "rpw": rpw, "rpmonth": rpmonth,
				"tpm": tpm, "tph": tph, "tpd": tpd, "tpw": tpw, "tpmonth": tpmonth,
				"allowed_enterprise_ids": allowedEnterpriseIDs,
				"inherit_to_enterprises": inheritToEnterprises,
				"member_count":           0, "members": []gin.H{},
			},
		})
		return
	}
	defer memberRows.Close()

	members := []gin.H{}
	for memberRows.Next() {
		var uid, uname, uemail, urole string
		_ = memberRows.Scan(&uid, &uname, &uemail, &urole)
		members = append(members, gin.H{"id": uid, "name": uname, "email": uemail, "role": urole})
	}

	c.JSON(http.StatusOK, gin.H{
		"group": gin.H{
			"id": id, "name": name, "description": description,
			"pricing_ratio": pricingRatio, "model_permissions": modelPermissions,
			"rate_limit_rpm": rateLimitRPM, "rate_limit_tpm": rateLimitTPM, "concurrent_limit": concurrentLimit, "status": status,
			"group_type":    groupType,
			"billing_mode":  billingMode,
			"models":        models,
			"routing_track": routingTrack,
			"created_at":    createdAt, "updated_at": updatedAt,
			"rpm": rpm, "rph": rph, "rp5h": rp5h, "rpw": rpw, "rpmonth": rpmonth,
			"tpm": tpm, "tph": tph, "tpd": tpd, "tpw": tpw, "tpmonth": tpmonth,
			"allowed_enterprise_ids": allowedEnterpriseIDs,
			"inherit_to_enterprises": inheritToEnterprises,
			"member_count":           len(members), "members": members,
		},
	})
}

func (r *GroupRoutes) createGroup(c *gin.Context) {
	var req struct {
		Name             string      `json:"name" binding:"required"`
		Description      string      `json:"description"`
		PricingRatio     float64     `json:"pricing_ratio"`
		ModelPermissions interface{} `json:"model_permissions"`
		RateLimitRPM     int         `json:"rate_limit_rpm"`
		RateLimitTPM     int         `json:"rate_limit_tpm"`
		ConcurrentLimit  *int        `json:"concurrent_limit"`
		GroupType        string      `json:"group_type"`
		BillingMode      string      `json:"billing_mode"`
		Models           interface{} `json:"models"`
		RoutingTrack     string      `json:"routing_track"`
		// InheritToEnterprises marks a platform enterprise template group as
		// directly usable by every enterprise's members (no local copy needed).
		InheritToEnterprises bool `json:"inherit_to_enterprises"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Name required"}})
		return
	}
	if req.GroupType == "" {
		req.GroupType = "general"
	}
	// Inheritance only makes sense on platform enterprise template groups.
	if req.InheritToEnterprises && req.GroupType != "enterprise" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "inherit_to_enterprises requires group_type 'enterprise'"}})
		return
	}
	if req.BillingMode == "" {
		req.BillingMode = "token"
	}
	if req.RoutingTrack == "" {
		req.RoutingTrack = "standard"
	}
	concurrentLimit := 10
	if req.ConcurrentLimit != nil {
		concurrentLimit = *req.ConcurrentLimit
	}

	var id string
	err := r.db.QueryRow(c.Request.Context(),
		`INSERT INTO user_groups (name, description, pricing_ratio, model_permissions, rate_limit_rpm, rate_limit_tpm, concurrent_limit,
		                         group_type, billing_mode, models, routing_track, inherit_to_enterprises)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) RETURNING id`,
		req.Name, req.Description, req.PricingRatio, req.ModelPermissions, req.RateLimitRPM, req.RateLimitTPM, concurrentLimit,
		req.GroupType, req.BillingMode, req.Models, req.RoutingTrack, req.InheritToEnterprises,
	).Scan(&id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to create group"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "group": gin.H{"id": id, "name": req.Name}})
}

func (r *GroupRoutes) updateGroup(c *gin.Context) {
	groupID := c.Param("id")
	var req struct {
		Name                 string      `json:"name"`
		Description          string      `json:"description"`
		PricingRatio         *float64    `json:"pricing_ratio"`
		ModelPermissions     interface{} `json:"model_permissions"`
		RPM                  *int        `json:"rpm"`
		RPH                  *int        `json:"rph"`
		RP5H                 *int        `json:"rp5h"`
		RPW                  *int        `json:"rpw"`
		RPMonth              *int        `json:"rpmonth"`
		TPM                  *int        `json:"tpm"`
		TPH                  *int        `json:"tph"`
		TPD                  *int        `json:"tpd"`
		TPW                  *int        `json:"tpw"`
		TPMonth              *int        `json:"tpmonth"`
		ConcurrentLimit      *int        `json:"concurrent_limit"`
		Status               string      `json:"status"`
		GroupType            string      `json:"group_type"`
		BillingMode          string      `json:"billing_mode"`
		Models               interface{} `json:"models"`
		RoutingTrack         string      `json:"routing_track"`
		AllowedEnterpriseIDs *[]string   `json:"allowed_enterprise_ids"`
		SyncMode             *string     `json:"sync_mode"`
		InheritToEnterprises *bool       `json:"inherit_to_enterprises"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	if req.Name != "" {
		if err := r.db.Exec(c.Request.Context(), `UPDATE user_groups SET name = $1, updated_at = NOW() WHERE id = $2::uuid`, req.Name, groupID); err != nil {
			zap.L().Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}
	if req.Description != "" {
		if err := r.db.Exec(c.Request.Context(), `UPDATE user_groups SET description = $1, updated_at = NOW() WHERE id = $2::uuid`, req.Description, groupID); err != nil {
			zap.L().Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}
	if req.PricingRatio != nil {
		if err := r.db.Exec(c.Request.Context(), `UPDATE user_groups SET pricing_ratio = $1, updated_at = NOW() WHERE id = $2::uuid`, *req.PricingRatio, groupID); err != nil {
			zap.L().Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}
	if req.Status != "" {
		// Changing group status cascades to disable active keys (mirrors
		// the enterprise group logic). The status update and key disable
		// run in a single transaction; cache is invalidated after commit.
		var disabledKeyHashes []string
		if err := r.db.WithTx(c.Request.Context(), func(tx pgx.Tx) error {
			if _, txErr := tx.Exec(c.Request.Context(),
				`UPDATE user_groups SET status = $1, updated_at = NOW() WHERE id = $2::uuid`, req.Status, groupID); txErr != nil {
				return txErr
			}
			// Disabling the group disables its active keys; re-enabling
			// does NOT restore them.
			if req.Status != "active" {
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
		}); err != nil {
			zap.L().Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
		for _, h := range disabledKeyHashes {
			invalidateValidateCache(c.Request.Context(), r.db, zap.L(), h)
		}
	}
	if req.GroupType != "" {
		if err := r.db.Exec(c.Request.Context(), `UPDATE user_groups SET group_type = $1, updated_at = NOW() WHERE id = $2::uuid`, req.GroupType, groupID); err != nil {
			zap.L().Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}
	if req.BillingMode != "" {
		if err := r.db.Exec(c.Request.Context(), `UPDATE user_groups SET billing_mode = $1, updated_at = NOW() WHERE id = $2::uuid`, req.BillingMode, groupID); err != nil {
			zap.L().Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}
	if req.Models != nil {
		// Collect key hashes in the group before updating, so we can
		// invalidate their validate cache entries afterwards.
		var affectedKeyHashes []string
		hashRows, hashErr := r.db.Query(c.Request.Context(),
			`SELECT key_hash FROM api_keys WHERE group_id = $1::uuid AND deleted_at IS NULL`,
			groupID)
		if hashErr == nil {
			for hashRows.Next() {
				var h string
				if scanErr := hashRows.Scan(&h); scanErr == nil {
					affectedKeyHashes = append(affectedKeyHashes, h)
				}
			}
			hashRows.Close()
		}

		if err := r.db.Exec(c.Request.Context(), `UPDATE user_groups SET models = $1, updated_at = NOW() WHERE id = $2::uuid`, req.Models, groupID); err != nil {
			zap.L().Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}

		// Propagate model changes to all keys in the group so the key-card
		// display stays consistent with validate-time enforcement.
		//
		// Keys without explicit allowed_models inherit the new group set.
		// Keys with explicit allowed_models are intersected (models removed
		// from the group are revoked from the key as well).
		// selected_models and current_model are cleaned up if they reference
		// models no longer in the group. current_model falls back to the
		// first available group model rather than NULL so that single-mode
		// keys do not break proxy routing (proxy returns 400 when
		// current_model is empty and the client uses "wgit-maas-latest").
		_ = r.db.Exec(c.Request.Context(),
			`UPDATE api_keys SET
				allowed_models = $1::jsonb,
				selected_models = (
					SELECT COALESCE(jsonb_agg(elem), '[]'::jsonb)
					FROM jsonb_array_elements_text(CASE WHEN api_keys.selected_models IS NULL THEN '[]'::jsonb ELSE api_keys.selected_models END) AS se(elem)
					WHERE se.elem IN (SELECT jsonb_array_elements_text($1::jsonb))
				),
				current_model = CASE
					WHEN api_keys.current_model IS NOT NULL
					     AND api_keys.current_model::text NOT IN (SELECT jsonb_array_elements_text($1::jsonb))
					THEN (SELECT jsonb_array_elements_text($1::jsonb) LIMIT 1)
					ELSE api_keys.current_model
				END,
				updated_at = NOW()
			WHERE group_id = $2::uuid AND deleted_at IS NULL
			  AND (allowed_models IS NULL OR allowed_models = '[]'::jsonb OR allowed_models = 'null'::jsonb)`,
			req.Models, groupID)

		_ = r.db.Exec(c.Request.Context(),
			`UPDATE api_keys SET
				allowed_models = (
					SELECT COALESCE(jsonb_agg(elem), '[]'::jsonb)
					FROM jsonb_array_elements_text(api_keys.allowed_models) AS ae(elem)
					WHERE ae.elem IN (SELECT jsonb_array_elements_text($1::jsonb))
				),
				selected_models = (
					SELECT COALESCE(jsonb_agg(elem), '[]'::jsonb)
					FROM jsonb_array_elements_text(CASE WHEN api_keys.selected_models IS NULL THEN '[]'::jsonb ELSE api_keys.selected_models END) AS se(elem)
					WHERE se.elem IN (SELECT jsonb_array_elements_text($1::jsonb))
				),
				current_model = CASE
					WHEN api_keys.current_model IS NOT NULL
					     AND api_keys.current_model::text NOT IN (SELECT jsonb_array_elements_text($1::jsonb))
					THEN (SELECT jsonb_array_elements_text($1::jsonb) LIMIT 1)
					ELSE api_keys.current_model
				END,
				updated_at = NOW()
			WHERE group_id = $2::uuid AND deleted_at IS NULL
			  AND allowed_models IS NOT NULL AND allowed_models != '[]'::jsonb AND allowed_models != 'null'::jsonb`,
			req.Models, groupID)

		// Invalidate validate cache so the next proxy request picks up the
		// updated model intersection.
		for _, h := range affectedKeyHashes {
			invalidateValidateCache(c.Request.Context(), r.db, zap.L(), h)
		}

		// Directly editing a follow-mode group's models demotes it to manual:
		// the admin clearly wants bespoke control. Editing a platform group's
		// models re-materializes every follow-mode enterprise child from it.
		_ = r.db.Exec(c.Request.Context(),
			`UPDATE user_groups SET sync_mode = 'manual', updated_at = NOW()
			 WHERE id = $1::uuid AND sync_mode = 'follow'`, groupID)
		r.syncFollowChildren(c, groupID)
	}
	if req.SyncMode != nil && (*req.SyncMode == "manual" || *req.SyncMode == "follow") {
		if err := r.db.Exec(c.Request.Context(),
			`UPDATE user_groups SET sync_mode = $1, updated_at = NOW() WHERE id = $2::uuid`, *req.SyncMode, groupID); err != nil {
			zap.L().Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
		// Switching a group to follow immediately materializes the parent's
		// current model list into THIS group.
		if *req.SyncMode == "follow" {
			_ = r.db.Exec(c.Request.Context(),
				`UPDATE user_groups child SET models = parent.models, updated_at = NOW()
				 FROM user_groups parent
				 WHERE parent.id = child.parent_group_id AND child.id = $1::uuid
				   AND child.deleted_at IS NULL`, groupID)
		}
	}
	if req.RoutingTrack != "" {
		if err := r.db.Exec(c.Request.Context(), `UPDATE user_groups SET routing_track = $1, updated_at = NOW() WHERE id = $2::uuid`, req.RoutingTrack, groupID); err != nil {
			zap.L().Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}

	// Update rate limits only when explicitly provided
	if req.RPM != nil {
		if err := r.db.Exec(c.Request.Context(), `UPDATE user_groups SET rpm = $1, rate_limit_rpm = $1, updated_at = NOW() WHERE id = $2::uuid`, *req.RPM, groupID); err != nil {
			zap.L().Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}
	if req.RPH != nil {
		if err := r.db.Exec(c.Request.Context(), `UPDATE user_groups SET rph = $1, updated_at = NOW() WHERE id = $2::uuid`, *req.RPH, groupID); err != nil {
			zap.L().Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}
	if req.RP5H != nil {
		if err := r.db.Exec(c.Request.Context(), `UPDATE user_groups SET rp5h = $1, updated_at = NOW() WHERE id = $2::uuid`, *req.RP5H, groupID); err != nil {
			zap.L().Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}
	if req.RPW != nil {
		if err := r.db.Exec(c.Request.Context(), `UPDATE user_groups SET rpw = $1, updated_at = NOW() WHERE id = $2::uuid`, *req.RPW, groupID); err != nil {
			zap.L().Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}
	if req.RPMonth != nil {
		if err := r.db.Exec(c.Request.Context(), `UPDATE user_groups SET rpmonth = $1, updated_at = NOW() WHERE id = $2::uuid`, *req.RPMonth, groupID); err != nil {
			zap.L().Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}
	if req.TPM != nil {
		if err := r.db.Exec(c.Request.Context(), `UPDATE user_groups SET tpm = $1, rate_limit_tpm = $2, updated_at = NOW() WHERE id = $3::uuid`, *req.TPM, *req.TPM, groupID); err != nil {
			zap.L().Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}
	if req.TPH != nil {
		if err := r.db.Exec(c.Request.Context(), `UPDATE user_groups SET tph = $1, updated_at = NOW() WHERE id = $2::uuid`, *req.TPH, groupID); err != nil {
			zap.L().Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}
	if req.TPD != nil {
		if err := r.db.Exec(c.Request.Context(), `UPDATE user_groups SET tpd = $1, updated_at = NOW() WHERE id = $2::uuid`, *req.TPD, groupID); err != nil {
			zap.L().Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}
	if req.TPW != nil {
		if err := r.db.Exec(c.Request.Context(), `UPDATE user_groups SET tpw = $1, updated_at = NOW() WHERE id = $2::uuid`, *req.TPW, groupID); err != nil {
			zap.L().Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}
	if req.TPMonth != nil {
		if err := r.db.Exec(c.Request.Context(), `UPDATE user_groups SET tpmonth = $1, updated_at = NOW() WHERE id = $2::uuid`, *req.TPMonth, groupID); err != nil {
			zap.L().Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}
	if req.ConcurrentLimit != nil {
		if err := r.db.Exec(c.Request.Context(), `UPDATE user_groups SET concurrent_limit = $1, updated_at = NOW() WHERE id = $2::uuid`, *req.ConcurrentLimit, groupID); err != nil {
			zap.L().Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}
	if req.AllowedEnterpriseIDs != nil {
		// Mutually exclusive with inheritance: a group inherited by all
		// enterprises cannot carry a per-enterprise allowlist.
		var inherited bool
		_ = r.db.QueryRow(c.Request.Context(),
			`SELECT inherit_to_enterprises FROM user_groups WHERE id = $1::uuid AND deleted_at IS NULL`, groupID,
		).Scan(&inherited)
		if inherited && len(*req.AllowedEnterpriseIDs) > 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "This group is inherited by all enterprises; the allowed-enterprises list is not editable. Disable inheritance first."}})
			return
		}
		aeJSON, _ := json.Marshal(*req.AllowedEnterpriseIDs)
		if err := r.db.Exec(c.Request.Context(), `UPDATE user_groups SET allowed_enterprise_ids = $1::jsonb, updated_at = NOW() WHERE id = $2::uuid`, string(aeJSON), groupID); err != nil {
			zap.L().Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}
	if req.InheritToEnterprises != nil {
		if *req.InheritToEnterprises {
			// Only platform enterprise template groups can be inherited.
			var eligible bool
			_ = r.db.QueryRow(c.Request.Context(),
				`SELECT group_type = 'enterprise' AND enterprise_id IS NULL FROM user_groups WHERE id = $1::uuid AND deleted_at IS NULL`, groupID,
			).Scan(&eligible)
			if !eligible {
				c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "inherit_to_enterprises requires a platform enterprise template group"}})
				return
			}
			// Enabling inheritance clears the per-enterprise allowlist.
			if err := r.db.Exec(c.Request.Context(),
				`UPDATE user_groups SET inherit_to_enterprises = TRUE, allowed_enterprise_ids = '[]'::jsonb, updated_at = NOW() WHERE id = $1::uuid`, groupID); err != nil {
				zap.L().Error("database operation failed", zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
				return
			}
		} else {
			if err := r.db.Exec(c.Request.Context(),
				`UPDATE user_groups SET inherit_to_enterprises = FALSE, updated_at = NOW() WHERE id = $1::uuid`, groupID); err != nil {
				zap.L().Error("database operation failed", zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
				return
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *GroupRoutes) deleteGroup(c *gin.Context) {
	groupID := c.Param("id")

	// Check for active members — block deletion if any exist
	var memberCount int
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM users WHERE group_id = $1::uuid AND deleted_at IS NULL`, groupID,
	).Scan(&memberCount)
	if err == nil && memberCount > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf("Group has %d active member(s). Please reassign them before deleting.", memberCount)}})
		return
	}

	// Check for active API keys referencing this group — block deletion if any exist.
	// Disabled keys cannot authenticate, so they don't block; they are detached below.
	var keyCount int
	err = r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM api_keys WHERE group_id = $1::uuid AND deleted_at IS NULL AND status = 'active'`, groupID,
	).Scan(&keyCount)
	if err == nil && keyCount > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf("Group has %d active API key(s). Please reassign them before deleting.", keyCount)}})
		return
	}

	// Detach non-active and soft-deleted keys from this group so that a later
	// reactivation falls back to default routing instead of a deleted group.
	if err := r.db.Exec(c.Request.Context(), `UPDATE api_keys SET group_id = NULL WHERE group_id = $1::uuid AND (deleted_at IS NOT NULL OR status <> 'active')`, groupID); err != nil {
		zap.L().Error("database operation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
		return
	}

	// Detach soft-deleted users from this group
	if err := r.db.Exec(c.Request.Context(), `UPDATE users SET group_id = NULL WHERE group_id = $1::uuid AND deleted_at IS NOT NULL`, groupID); err != nil {
		zap.L().Error("database operation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
		return
	}

	// Now soft-delete the group
	_, err = r.db.Pool.Exec(c.Request.Context(),
		`UPDATE user_groups SET deleted_at = NOW(), updated_at = NOW() WHERE id = $1::uuid AND deleted_at IS NULL`, groupID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "An internal error occurred"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *GroupRoutes) addMembers(c *gin.Context) {
	groupID := c.Param("id")
	var req struct {
		UserIDs []string `json:"user_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	added := 0
	for _, uid := range req.UserIDs {
		_, err := r.db.Pool.Exec(c.Request.Context(),
			`UPDATE users SET group_id = $1::uuid, updated_at = NOW() WHERE id = $2::uuid`,
			groupID, uid)
		if err == nil {
			added++
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "added": added})
}

func (r *GroupRoutes) removeMember(c *gin.Context) {
	groupID := c.Param("id")
	userID := c.Param("userId")

	if err := r.db.Exec(c.Request.Context(), `UPDATE users SET group_id = NULL, updated_at = NOW() WHERE id = $1::uuid AND group_id = $2::uuid`, userID, groupID); err != nil {
		zap.L().Error("database operation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// getGroupModelsWithStatus returns group models with their lifecycle status (active/eol_pending) and EOL info
func (r *GroupRoutes) getGroupModelsWithStatus(c *gin.Context) {
	groupID := c.Param("id")

	// Get group models
	var modelsJSON interface{}
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT models FROM user_groups WHERE id = $1::uuid`, groupID,
	).Scan(&modelsJSON)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Group not found"}})
		return
	}

	var modelNames []string
	switch v := modelsJSON.(type) {
	case []byte:
		_ = json.Unmarshal(v, &modelNames)
	case string:
		_ = json.Unmarshal([]byte(v), &modelNames)
	}

	if len(modelNames) == 0 {
		c.JSON(http.StatusOK, gin.H{"models": []gin.H{}})
		return
	}

	// Get status and EOL info for each model. Only return the display name
	// and lifecycle status — never expose product_id or pricing, which would
	// reveal upstream identifiers and cost prices (see enterprise_groups.go).
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT sm.status, sm.eol_service_at,
		        sm.aimodels_data->>'name' as name
		 FROM service_models sm
		 WHERE sm.product_id = ANY($1)
		   AND sm.status IN ('active', 'eol_pending')
		 ORDER BY sm.provider, sm.sort_order, sm.product_id`, modelNames)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to query models"}})
		return
	}
	defer rows.Close()

	modelsWithStatus := []gin.H{}
	for rows.Next() {
		var status string
		var eolServiceAt *time.Time
		var name *string
		_ = rows.Scan(&status, &eolServiceAt, &name)

		entry := gin.H{
			"name":   name,
			"status": status,
		}
		if eolServiceAt != nil {
			entry["eol_service_at"] = eolServiceAt.Format("2006-01-02")
			daysRemaining := int(time.Until(*eolServiceAt).Hours() / 24)
			if daysRemaining > 0 {
				entry["eol_days_remaining"] = daysRemaining
			}
		}
		modelsWithStatus = append(modelsWithStatus, entry)
	}

	c.JSON(http.StatusOK, gin.H{"models": modelsWithStatus})
}

// getGroupModelPricing returns base model prices (from model_pricing_rules)
// for the models in a specific group. No ratio is applied — the raw base
// price is returned so the user can see what they're paying. The group's
// pricing_ratio is included separately for reference.
func (r *GroupRoutes) getGroupModelPricing(c *gin.Context) {
	groupID := c.Param("id")

	// Get group info + model list
	var groupName, groupType string
	var pricingRatio float64
	var modelsJSON []byte
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT name, group_type, COALESCE(pricing_ratio, 1.0), models::text
		 FROM user_groups WHERE id = $1::uuid AND status = 'active'`,
		groupID,
	).Scan(&groupName, &groupType, &pricingRatio, &modelsJSON)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Group not found"}})
		return
	}

	var modelNames []string
	_ = json.Unmarshal(modelsJSON, &modelNames)

	// Query base prices — use LEFT JOIN so models without pricing rules still
	// appear (with null prices). Deduplicate by api_model_name, keeping the
	// one with the lowest input price.
	query := `SELECT * FROM (
		SELECT DISTINCT ON (sm.api_model_name)
			sm.api_model_name, sm.vendor,
			sm.aimodels_data->>'name' as name,
			COALESCE(NULLIF(sm.aimodels_data->>'type', ''), sm.capabilities->>'type', 'text') as model_type,
			mpr.billing_mode,
			mpr.prices->>'input' as input_price,
			mpr.prices->>'output' as output_price,
			mpr.prices->>'cache_hit' as cache_hit_price,
			mpr.prices->>'reasoning' as reasoning_price,
			mpr.prices->>'fixed_request' as fixed_request_price,
			mpr.prices->'variants' as price_variants
		 FROM service_models sm
		 LEFT JOIN model_pricing_rules mpr ON mpr.status = 'active'
		   AND (mpr.effective_to IS NULL OR mpr.effective_to > NOW())
		   AND sm.provider = mpr.provider
		   AND (sm.api_model_name = mpr.api_model_name OR sm.product_id = mpr.product_id)
		 WHERE sm.status IN ('active', 'eol_pending')
		   AND sm.show_in_store = true`
	args := []interface{}{}
	if len(modelNames) > 0 {
		query += ` AND sm.api_model_name = ANY($1)`
		args = append(args, modelNames)
	}
	query += ` ORDER BY sm.api_model_name, COALESCE((mpr.prices->>'input')::numeric, 999999) ASC`
	query += `) AS dedup ORDER BY api_model_name`

	rows, err := r.db.Query(c.Request.Context(), query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to query pricing"}})
		return
	}
	defer rows.Close()

	models := []gin.H{}
	for rows.Next() {
		var apiModelName, modelType string
		var vendor, name *string
		var billingMode *string
		var inputPrice, outputPrice, cacheHitPrice, reasoningPrice *string
		var fixedRequestPrice *string
		var priceVariants []byte
		if err := rows.Scan(&apiModelName, &vendor, &name, &modelType,
			&billingMode,
			&inputPrice, &outputPrice, &cacheHitPrice, &reasoningPrice,
			&fixedRequestPrice, &priceVariants); err != nil {
			continue
		}
		entry := gin.H{
			"api_model_name": apiModelName,
			"name":           name,
			"vendor":         vendor,
			"model_type":     modelType,
			"input_price":    parsePriceStr(inputPrice),
			"output_price":   parsePriceStr(outputPrice),
		}
		if billingMode != nil && *billingMode != "" {
			entry["billing_mode"] = *billingMode
		}
		// Media/flat-per-request rules carry their real price in
		// fixed_request + variants (per image/second) — without these the
		// price dialog shows blank token columns for every media model.
		if fr := parsePriceStr(fixedRequestPrice); fr != nil {
			entry["fixed_request_price"] = fr
		}
		if len(priceVariants) > 0 && string(priceVariants) != "null" {
			var variants []gin.H
			if json.Unmarshal(priceVariants, &variants) == nil && len(variants) > 0 {
				entry["price_variants"] = variants
			}
		}
		if cacheHitPrice != nil {
			entry["cache_hit_price"] = parsePriceStr(cacheHitPrice)
		}
		if reasoningPrice != nil {
			entry["reasoning_price"] = parsePriceStr(reasoningPrice)
		}
		models = append(models, entry)
	}

	c.JSON(http.StatusOK, gin.H{
		"group_id":      groupID,
		"group_name":    groupName,
		"group_type":    groupType,
		"pricing_ratio": pricingRatio,
		"models":        models,
	})
}

// parsePriceStr parses a string price (JSONB numeric) to float64.
func parsePriceStr(s *string) interface{} {
	if s == nil || *s == "" {
		return nil
	}
	var f float64
	if _, err := fmt.Sscanf(*s, "%f", &f); err != nil {
		return nil
	}
	return f
}

package routes

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// setEOLPending sets model status to eol_pending (EOL announced but still serving)
func (r *ModelRoutes) setEOLPending(c *gin.Context) {
	modelID := c.Param("id")
	var req struct {
		EOLServiceAt string `json:"eol_service_at"`
		EOLNotes     string `json:"eol_notes"`
	}
	_ = c.ShouldBindJSON(&req)

	var err error
	if req.EOLServiceAt != "" {
		err = r.db.Exec(c.Request.Context(),
			`UPDATE service_models SET status = 'eol_pending', eol_announced_at = NOW(),
			 eol_service_at = $2, eol_notes = $3, updated_at = NOW() WHERE id = $1::uuid`,
			modelID, req.EOLServiceAt, req.EOLNotes)
	} else {
		err = r.db.Exec(c.Request.Context(),
			`UPDATE service_models SET status = 'eol_pending', eol_announced_at = NOW(),
			 eol_notes = $2, updated_at = NOW() WHERE id = $1::uuid`,
			modelID, req.EOLNotes)
	}
	if err != nil {
		r.logger.Error("Failed to set EOL pending", zap.String("model_id", modelID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update EOL status"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// updateEOL updates EOL fields for a model
func (r *ModelRoutes) updateEOL(c *gin.Context) {
	modelID := c.Param("id")
	var req struct {
		EOLServiceAt *string `json:"eol_service_at"`
		EOLPurgeAt   *string `json:"eol_purge_at"`
		EOLNotes     *string `json:"eol_notes"`
	}
	_ = c.ShouldBindJSON(&req)

	var updateErr error
	if req.EOLServiceAt != nil {
		if err := r.db.Exec(c.Request.Context(),
			`UPDATE service_models SET eol_service_at = $1, updated_at = NOW() WHERE id = $2::uuid`,
			*req.EOLServiceAt, modelID); err != nil {
			updateErr = err
		}
	}
	if req.EOLPurgeAt != nil {
		if err := r.db.Exec(c.Request.Context(),
			`UPDATE service_models SET eol_purge_at = $1, updated_at = NOW() WHERE id = $2::uuid`,
			*req.EOLPurgeAt, modelID); err != nil {
			updateErr = err
		}
	}
	if req.EOLNotes != nil {
		if err := r.db.Exec(c.Request.Context(),
			`UPDATE service_models SET eol_notes = $1, updated_at = NOW() WHERE id = $2::uuid`,
			*req.EOLNotes, modelID); err != nil {
			updateErr = err
		}
	}
	if updateErr != nil {
		r.logger.Error("Failed to update EOL fields", zap.String("model_id", modelID), zap.Error(updateErr))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update EOL fields"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// cancelEOL cancels EOL pending and returns model to active status
func (r *ModelRoutes) cancelEOL(c *gin.Context) {
	modelID := c.Param("id")
	if err := r.db.Exec(c.Request.Context(),
		`UPDATE service_models SET status = 'active', eol_announced_at = NULL, eol_service_at = NULL, updated_at = NOW() WHERE id = $1::uuid`, modelID); err != nil {
		r.logger.Error("Failed to cancel EOL", zap.String("model_id", modelID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to cancel EOL"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *ModelRoutes) confirmEOL(c *gin.Context) {
	modelID := c.Param("id")
	// Enter the EOL sunset period: existing keys/groups keep the model for 30 days,
	// after which it is automatically discontinued and removed. New groups/keys cannot
	// select it because store/group queries exclude status='eol'.
	if err := r.db.Exec(c.Request.Context(),
		`UPDATE service_models SET status = 'eol', eol_service_at = NOW() + INTERVAL '30 days', eol_purge_at = NOW() + INTERVAL '30 days', updated_at = NOW() WHERE id = $1::uuid`, modelID); err != nil {
		r.logger.Error("Failed to confirm EOL", zap.String("model_id", modelID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to confirm EOL"}})
		return
	}
	invalidateActiveServiceModelsCache(c.Request.Context(), r.db, r.logger)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *ModelRoutes) discontinue(c *gin.Context) {
	modelID := c.Param("id")
	// Optional {"silent": true} body: delist without emailing affected users.
	var req struct {
		Silent bool `json:"silent"`
	}
	_ = c.ShouldBindJSON(&req)
	if err := r.discontinueModel(c.Request.Context(), modelID, c.GetString("user_id"), c.GetString("email"), !req.Silent); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to discontinue model"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// discontinueModel is the internal implementation used by both the admin HTTP handler
// and the background auto-discontinue job. It updates status, cleans up groups/keys,
// syncs upstream keys, and optionally logs an audit event. notify controls whether
// affected users receive a deprecation email (false = silent delist).
// It returns an error so the admin handler can surface failures (the auto job logs and continues).
func (r *ModelRoutes) discontinueModel(ctx context.Context, modelID, userID, email string, notify bool) error {
	var provider string
	_ = r.db.QueryRow(ctx,
		`SELECT provider FROM service_models WHERE id = $1::uuid`, modelID,
	).Scan(&provider)
	if err := r.db.Exec(ctx,
		`UPDATE service_models SET status = 'discontinued', discontinued_at = NOW(), updated_at = NOW() WHERE id = $1::uuid`, modelID); err != nil {
		r.logger.Error("Failed to discontinue model", zap.String("model_id", modelID), zap.Error(err))
		return err
	}
	// Trigger cleanup: remove this model from all groups and keys — but only
	// when no other provider row still serves this model name. Discontinuing
	// one provider of a multi-provider model must not strip the model from
	// groups or email users: the model is still callable via the other rows.
	if r.modelAvailableElsewhere(ctx, modelID) {
		r.logger.Info("Model still served by another provider row; skipping group cleanup and user notification",
			zap.String("model_id", modelID))
	} else {
		r.cleanupSingleModelFromGroups(ctx, modelID, notify)
	}
	// Disable smart routing entries for this model so the proxy does not try to
	// route requests to a discontinued provider/model.
	r.disableModelProviderRouting(ctx, modelID)
	// Auto-sync
	if provider != "" {
		go func(p string) {
			if n, err := syncServiceModelsToKeys(r.db, context.Background(), p); err != nil {
				r.logger.Warn("Auto-sync after discontinue failed", zap.String("provider", p), zap.Error(err))
			} else {
				r.logger.Info("Auto-sync after discontinue", zap.String("provider", p), zap.Int64("keys_updated", n))
			}
			r.db.PublishSyncEvent(context.Background())
		}(provider)
	}
	if userID != "" && r.audit != nil {
		r.audit.Log(ctx, userID, email, "admin", "model_discontinued", "warning", fmt.Sprintf("Model discontinued: %s", modelID))
	}
	return nil
}

// disableModelProviderRouting disables all enabled model_provider_routing entries
// that match the given service model. It matches against both product_id and
// api_model_name since routing rules may be stored under either value.
//
// It returns the IDs of the rows it flipped so callers (pause) can record them
// in the pause marker; the delete/discontinue paths ignore the return value.
func (r *ModelRoutes) disableModelProviderRouting(ctx context.Context, modelID string) []string {
	var provider, productID, apiModelName string
	err := r.db.QueryRow(ctx,
		`SELECT provider, product_id, COALESCE(api_model_name, '') FROM service_models WHERE id = $1::uuid`, modelID,
	).Scan(&provider, &productID, &apiModelName)
	if err != nil {
		r.logger.Warn("Failed to get model info for routing disable", zap.String("model_id", modelID), zap.Error(err))
		return nil
	}

	names := []string{productID}
	if apiModelName != "" && apiModelName != productID {
		names = append(names, apiModelName)
	}

	rows, err := r.db.Pool.Query(ctx,
		`UPDATE model_provider_routing SET enabled = false, updated_at = NOW()
		 WHERE provider = $1 AND model_name = ANY($2::text[]) AND enabled = true
		 RETURNING id::text`,
		provider, names)
	if err != nil {
		r.logger.Warn("Failed to disable model provider routing", zap.String("model_id", modelID), zap.Error(err))
		return nil
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	if len(ids) > 0 {
		r.logger.Info("Disabled model provider routing",
			zap.String("model_id", modelID),
			zap.Int("rows", len(ids)))
	}
	return ids
}

// enableModelProviderRouting re-enables the routing rows recorded in the pause
// marker. Rows an admin disabled manually stay disabled because only
// pause-flipped IDs are passed in.
func (r *ModelRoutes) enableModelProviderRouting(ctx context.Context, routingIDs []string) {
	if len(routingIDs) == 0 {
		return
	}
	tag, err := r.db.Pool.Exec(ctx,
		`UPDATE model_provider_routing SET enabled = true, updated_at = NOW()
		 WHERE id = ANY($1::uuid[]) AND enabled = false`,
		routingIDs)
	if err != nil {
		r.logger.Warn("Failed to enable model provider routing", zap.Strings("routing_ids", routingIDs), zap.Error(err))
		return
	}
	if tag.RowsAffected() > 0 {
		r.logger.Info("Enabled model provider routing", zap.Int64("rows", tag.RowsAffected()))
	}
}

// enableModelProviderRoutingLegacy is the pre-marker fallback for models that
// were paused before the cascade marker existed: re-enable by model name.
func (r *ModelRoutes) enableModelProviderRoutingLegacy(ctx context.Context, modelID string) {
	var provider, productID, apiModelName string
	err := r.db.QueryRow(ctx,
		`SELECT provider, product_id, COALESCE(api_model_name, '') FROM service_models WHERE id = $1::uuid`, modelID,
	).Scan(&provider, &productID, &apiModelName)
	if err != nil {
		r.logger.Warn("Failed to get model info for routing enable", zap.String("model_id", modelID), zap.Error(err))
		return
	}

	names := []string{productID}
	if apiModelName != "" && apiModelName != productID {
		names = append(names, apiModelName)
	}

	tag, err := r.db.Pool.Exec(ctx,
		`UPDATE model_provider_routing SET enabled = true, updated_at = NOW()
		 WHERE provider = $1 AND model_name = ANY($2::text[]) AND enabled = false`,
		provider, names)
	if err != nil {
		r.logger.Warn("Failed to enable model provider routing", zap.String("model_id", modelID), zap.Error(err))
		return
	}
	if tag.RowsAffected() > 0 {
		r.logger.Info("Enabled model provider routing",
			zap.String("model_id", modelID),
			zap.Int64("rows", tag.RowsAffected()))
	}
}

// AutoDiscontinueExpiredEOLModels transitions EOL models whose sunset period has
// expired (eol_purge_at <= NOW()) to 'discontinued' and removes them from groups/keys.
// This is invoked by the daily model-lifecycle background job.
func (r *ModelRoutes) AutoDiscontinueExpiredEOLModels(ctx context.Context) {
	rows, err := r.db.Query(ctx,
		`SELECT id::text, product_id FROM service_models
		 WHERE status = 'eol'
		   AND eol_purge_at IS NOT NULL
		   AND eol_purge_at <= NOW()`)
	if err != nil {
		r.logger.Warn("Failed to query expired EOL models", zap.Error(err))
		return
	}
	defer rows.Close()

	for rows.Next() {
		var modelID, productID string
		if err := rows.Scan(&modelID, &productID); err != nil {
			continue
		}
		if err := r.discontinueModel(ctx, modelID, "", "", true); err != nil {
			r.logger.Warn("Auto-discontinue failed", zap.String("product_id", productID), zap.Error(err))
			continue
		}
		r.logger.Info("Auto-discontinued expired EOL model", zap.String("product_id", productID))
	}
}

// notifyAffectedUsers sends email to users whose keys lost a model
func (r *ModelRoutes) notifyAffectedUsers(ctx context.Context, productID string) {
	if r.mailer == nil || !r.mailer.IsConfigured() {
		r.logger.Info("Mailer not configured, skipping notification", zap.String("productId", productID))
		return
	}

	// Find users whose keys had this model removed
	rows, err := r.db.Query(ctx,
		`SELECT DISTINCT u.email, u.name, ak.name
		 FROM api_keys ak
		 JOIN users u ON ak.user_id = u.id
		 WHERE ak.deleted_at IS NULL
		   AND u.deleted_at IS NULL
		   AND u.email IS NOT NULL
		   AND ak.group_id IN (
		     SELECT g.id FROM user_groups g
		     WHERE g.models::text LIKE '%' || $1 || '%'
		   )`,
		productID)
	if err != nil {
		r.logger.Warn("Failed to query affected users", zap.Error(err))
		return
	}
	defer rows.Close()

	dashboardURL := r.cfg.App.BaseURL
	if dashboardURL == "" {
		dashboardURL = ""
	}

	for rows.Next() {
		var email, userName, keyName string
		_ = rows.Scan(&email, &userName, &keyName)
		if err := r.mailer.SendModelDiscontinuedNotification(email, productID, dashboardURL, "zh"); err != nil {
			r.logger.Warn("Failed to send model discontinued notification",
				zap.String("email", email), zap.Error(err))
		}
	}
}

// sendKeyFallbackNotification notifies a user that their key was auto-adjusted
func (r *ModelRoutes) sendKeyFallbackNotification(ctx context.Context, userID, keyName, fallbackModel string) {
	if r.mailer == nil || !r.mailer.IsConfigured() {
		return
	}

	var email string
	err := r.db.QueryRow(ctx,
		`SELECT email FROM users WHERE id = $1::uuid AND deleted_at IS NULL`, userID,
	).Scan(&email)
	if err != nil || email == "" {
		return
	}

	dashboardURL := r.cfg.App.BaseURL
	if dashboardURL == "" {
		dashboardURL = ""
	}
	if dashboardURL != "" {
		dashboardURL = dashboardURL + "/dashboard"
	}

	if err := r.mailer.SendKeyFallbackNotification(email, keyName, fallbackModel, dashboardURL, "zh"); err != nil {
		r.logger.Warn("Failed to send key fallback notification",
			zap.String("email", email), zap.Error(err))
	}
}

// CheckAndSendEOLReminders checks eol_pending models and sends reminders at 7 and 1 days before service end.
func (r *ModelRoutes) CheckAndSendEOLReminders(ctx context.Context) {
	if r.mailer == nil || !r.mailer.IsConfigured() {
		return
	}

	// Find eol_pending models within 7 days of service end
	rows, err := r.db.Query(ctx,
		`SELECT sm.id::text, sm.product_id, sm.eol_service_at,
		        EXTRACT(DAY FROM sm.eol_service_at - NOW())::int as days_remaining
		 FROM service_models sm
		 WHERE sm.status = 'eol_pending'
		   AND sm.eol_service_at IS NOT NULL
		   AND sm.eol_service_at > NOW()
		   AND sm.eol_service_at - NOW() <= INTERVAL '7 days'
		   AND (sm.last_eol_notified_at IS NULL
		        OR sm.last_eol_notified_at < NOW() - INTERVAL '12 hours')`)
	if err != nil {
		r.logger.Warn("Failed to query eol_pending models for reminders", zap.Error(err))
		return
	}
	defer rows.Close()

	for rows.Next() {
		var modelID, productID string
		var eolServiceAt time.Time
		var daysRemaining int
		_ = rows.Scan(&modelID, &productID, &eolServiceAt, &daysRemaining)

		// Only send at 7 and 1 day milestones
		if daysRemaining != 7 && daysRemaining != 1 {
			continue
		}

		r.logger.Info("Sending EOL reminder",
			zap.String("productId", productID),
			zap.Int("daysRemaining", daysRemaining))

		// Find users with keys in groups that include this model
		userRows, err := r.db.Query(ctx,
			`SELECT DISTINCT u.email, u.name
			 FROM api_keys ak
			 JOIN users u ON ak.user_id = u.id
			 JOIN user_groups ug ON ak.group_id = ug.id
			 WHERE ak.deleted_at IS NULL
			   AND u.deleted_at IS NULL
			   AND u.email IS NOT NULL
			   AND ug.models::text LIKE '%' || $1 || '%'`,
			productID)
		if err != nil {
			continue
		}

		dashboardURL := r.cfg.App.BaseURL
		if dashboardURL == "" {
			dashboardURL = ""
		}
		if dashboardURL != "" {
			dashboardURL = dashboardURL + "/dashboard"
		}

		for userRows.Next() {
			var email, userName string
			_ = userRows.Scan(&email, &userName)
			if err := r.mailer.SendEOLReminderNotification(email, productID, daysRemaining,
				eolServiceAt.Format("2006-01-02"), dashboardURL, "zh"); err != nil {
				r.logger.Warn("Failed to send EOL reminder",
					zap.String("email", email), zap.Error(err))
			}
		}
		userRows.Close()

		// Update last_eol_notified_at
		if err := r.db.Exec(ctx,
			`UPDATE service_models SET last_eol_notified_at = NOW() WHERE id = $1::uuid`, modelID); err != nil {
			r.logger.Warn("Failed to update last_eol_notified_at",
				zap.String("model_id", modelID), zap.Error(err))
		}
	}
}

// cleanupInvalidModelsHandler - Admin manual trigger for cleaning up invalid models from groups
func (r *ModelRoutes) cleanupInvalidModelsHandler(c *gin.Context) {
	r.CleanupAllInvalidModels(c.Request.Context())
	r.CheckAndSendEOLReminders(c.Request.Context())
	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "admin", "models_cleanup_triggered", "info", "Admin triggered model cleanup")
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Cleanup completed"})
}

// modelAvailableElsewhere reports whether another non-deleted service_models
// row offers the same callable model (matched on api_model_name/product_id,
// both directions) in a serving-or-announced state (active/eol_pending/eol).
// Paused and ignored rows do not count: the model is not servable through
// them right now.
func (r *ModelRoutes) modelAvailableElsewhere(ctx context.Context, modelID string) bool {
	var apiName, productID string
	if err := r.db.QueryRow(ctx,
		`SELECT COALESCE(api_model_name, product_id), product_id FROM service_models WHERE id = $1::uuid`, modelID,
	).Scan(&apiName, &productID); err != nil {
		return false
	}
	var exists bool
	err := r.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM service_models
			 WHERE deleted_at IS NULL AND id != $1::uuid
			   AND status IN ('active', 'eol_pending', 'eol')
			   AND (api_model_name = $2 OR product_id = $2 OR api_model_name = $3 OR product_id = $3))`,
		modelID, apiName, productID).Scan(&exists)
	return err == nil && exists
}

package routes

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/ai-proxy/ai-portal-go/internal/config"
	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/middleware"
	"github.com/ai-proxy/ai-portal-go/internal/services"
	"github.com/ai-proxy/ai-portal-go/pkg/audit"
	"github.com/ai-proxy/ai-portal-go/pkg/mail"
	"github.com/ai-proxy/ai-portal-go/pkg/validation"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type ModelRoutes struct {
	db         *db.DB
	auth       *middleware.Auth
	cfg        *config.Config
	logger     *zap.Logger
	validator  *validation.ModelValidator
	mailer     *mail.Sender
	audit      *audit.Logger
	pricingSvc *services.PricingService
}

func NewModelRoutes(db *db.DB, auth *middleware.Auth, cfg *config.Config, logger *zap.Logger, mailer *mail.Sender, auditLog *audit.Logger) *ModelRoutes {
	return &ModelRoutes{
		db:         db,
		auth:       auth,
		cfg:        cfg,
		logger:     logger,
		validator:  validation.NewModelValidator(db, cfg, logger),
		mailer:     mailer,
		audit:      auditLog,
		pricingSvc: services.NewPricingService(db, logger),
	}
}

func (r *ModelRoutes) Register(rg *gin.RouterGroup) {
	// Public - no auth needed
	rg.GET("/models/public", r.listPublicModels)
	rg.GET("/service-models/public", r.listServiceModelsPublic)

	// Admin service models - CRUD operations
	// Note: GET /admin/service-models and /admin/openrouter-models are registered in admin.go
	serviceGroup := rg.Group("/admin/service-models")
	serviceGroup.Use(r.auth.RequireAuth(), r.auth.RequireAdmin())
	serviceGroup.POST("", r.createServiceModel)
	serviceGroup.GET("/:id", r.getServiceModel)
	serviceGroup.PATCH("/:id", r.updateServiceModel)
	serviceGroup.POST("/:id/confirm-eol", r.confirmEOL)
	serviceGroup.POST("/:id/cancel-eol", r.cancelEOL)
	serviceGroup.POST("/:id/set-eol-pending", r.setEOLPending)
	serviceGroup.POST("/:id/update-eol", r.updateEOL)
	serviceGroup.POST("/:id/discontinue", r.discontinue)
	serviceGroup.POST("/:id/toggle-hidden", r.toggleHidden)
	serviceGroup.POST("/:id/pause", r.pauseModel)
	serviceGroup.POST("/:id/resume", r.resumeModel)
	serviceGroup.DELETE("/:id", r.deleteModel)
	serviceGroup.POST("/:id/confirm", r.confirmModel)
	serviceGroup.POST("/:id/ignore", r.ignoreModel)
	serviceGroup.POST("/:id/accept-changes", r.acceptChanges)
	serviceGroup.POST("/sync-from-aimodels", r.syncFromAIModels)
	serviceGroup.POST("/cleanup-invalid-models", r.cleanupInvalidModelsHandler)
	serviceGroup.POST("/resync-to-keys", r.resyncServiceModelsToKeys)

	// Model validation endpoints
	serviceGroup.POST("/:id/validate", r.validateModel)
	serviceGroup.POST("/:id/auto-confirm", r.autoConfirmModel)
	// Note: batch-validate is registered in admin.go to avoid duplicate route

	// OpenRouter models - add to service
	rg.POST("/admin/openrouter-models/add-to-service", r.auth.RequireAuth(), r.auth.RequireAdmin(), r.addFromOpenRouter)

	// Model approvals
	approvalsGroup := rg.Group("/admin/service-models/approvals")
	approvalsGroup.Use(r.auth.RequireAuth(), r.auth.RequireAdmin())
	approvalsGroup.GET("/pending", r.getPendingApprovals)
	approvalsGroup.POST("/:id/review", r.reviewApproval)

	// Model aliases
	aliasGroup := rg.Group("/admin/service-models/:id/aliases")
	aliasGroup.Use(r.auth.RequireAuth(), r.auth.RequireAdmin())
	aliasGroup.GET("", r.getAliases)
	aliasGroup.POST("", r.addAlias)
	aliasGroup.DELETE("/:alias", r.removeAlias)

	// Model provider routing - Admin-configured provider fallback order per model
	routingGroup := rg.Group("/admin/model-routing")
	routingGroup.Use(r.auth.RequireAuth(), r.auth.RequireAdmin())
	routingGroup.GET("", r.listModelRouting)
	routingGroup.POST("", r.createModelRouting)
	routingGroup.PATCH("/:id", r.updateModelRouting)
	routingGroup.DELETE("/:id", r.deleteModelRouting)
	routingGroup.POST("/reorder", r.reorderModelRouting)

	// Provider models - fetch and sync from provider /v1/models API
	providerGroup := rg.Group("/admin/provider-models")
	providerGroup.Use(r.auth.RequireAuth(), r.auth.RequireAdmin())
	providerGroup.GET("/fetch", r.fetchProviderModels)
	providerGroup.POST("/sync-key-models", r.syncKeyModels)
	providerGroup.POST("/add-to-service", r.addProviderModelsToService)
}

// --- Model Lifecycle Cleanup & EOL Notifications ---

// cleanupSingleModelFromGroups removes a specific model from all group.models and affected keys' selected_models.
// notify=false performs a silent cleanup (no user emails) for admin-initiated
// silent delists; the email recipient query must still run before removal.
func (r *ModelRoutes) cleanupSingleModelFromGroups(ctx context.Context, modelID string, notify bool) {
	// Get the product_id and api_model_name for this model
	var productID, apiModelName string
	err := r.db.QueryRow(ctx,
		`SELECT product_id, COALESCE(api_model_name, product_id) FROM service_models WHERE id = $1::uuid`, modelID,
	).Scan(&productID, &apiModelName)
	if err != nil {
		r.logger.Warn("Failed to get product_id for cleanup", zap.String("modelId", modelID), zap.Error(err))
		return
	}

	r.logger.Info("Cleaning up model from groups", zap.String("productId", productID), zap.String("apiModelName", apiModelName))

	// Step 1: Notify affected users BEFORE removing the model from group.models.
	// notifyAffectedUsers finds recipients by querying user_groups.models for
	// the product_id; if we run cleanup first, the model is gone from every
	// group and the recipient query returns no one — so EOL/deprecation
	// emails never reach anyone. Notify must precede the delete.
	// Silent delists (notify=false) skip the email entirely.
	if notify {
		r.notifyAffectedUsers(ctx, productID)
	}

	// Step 2: Remove from all group.models (match both product_id and api_model_name)
	_, err = r.db.Pool.Exec(ctx,
		`UPDATE user_groups SET models = (
		    SELECT COALESCE(jsonb_agg(elem), '[]'::jsonb)
		    FROM jsonb_array_elements_text(COALESCE(models, '[]'::jsonb)) elem
		    WHERE elem != $1 AND elem != $2
		  ), updated_at = NOW()
		  WHERE models IS NOT NULL AND models::text != '[]'`,
		productID, apiModelName)
	if err != nil {
		r.logger.Warn("Failed to remove model from group.models", zap.Error(err))
	}

	// Step 3: Remove from affected keys' selected_models (match both product_id and api_model_name)
	_, err = r.db.Pool.Exec(ctx,
		`UPDATE api_keys SET selected_models = (
		    SELECT COALESCE(jsonb_agg(elem), '[]'::jsonb)
		    FROM jsonb_array_elements_text(COALESCE(selected_models, '[]'::jsonb)) elem
		    WHERE elem != $1 AND elem != $2
		  ), updated_at = NOW()
		  WHERE selected_models IS NOT NULL AND selected_models::text != '[]'`,
		productID, apiModelName)
	if err != nil {
		r.logger.Warn("Failed to remove model from keys' selected_models", zap.Error(err))
	}

	// Step 4: Fallback keys whose selected_models became empty to their group's first model
	r.fallbackEmptySelectedModels(ctx)

	// Step 5: Update cleanup timestamp
	if err := r.db.Exec(ctx, `UPDATE service_models SET last_group_cleanup_at = NOW() WHERE id = $1::uuid`, modelID); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
	}
}
func (r *ModelRoutes) CleanupAllInvalidModels(ctx context.Context) {
	r.logger.Info("Running full cleanup of invalid models from groups")

	// Remove discontinued/ignored (non-protected) models from group.models immediately.
	// EOL models are kept in groups/keys until their sunset period expires (eol_purge_at).
	// Match both product_id and api_model_name since groups may store either format.
	//
	// CRITICAL: a model name is only removed if NO still-servable row (active /
	// eol_pending / eol-before-purge) shares that product_id or api_model_name.
	// Many models exist under the same name on several providers (e.g.
	// deepseek-v4-flash active on deepseek+laozhang but discontinued on
	// huawei/tokenrouter); without the "servedNames" exclusion the discontinued
	// row would strip the still-active name from every group on each background
	// cleanup pass — the cause of models repeatedly vanishing from groups.
	_, err := r.db.Pool.Exec(ctx,
		`WITH served AS (
		    SELECT product_id AS n FROM service_models
		    WHERE deleted_at IS NULL AND protected = false
		      AND (status IN ('active','eol_pending')
		           OR (status = 'eol' AND (eol_purge_at IS NULL OR eol_purge_at > NOW())))
		    UNION
		    SELECT COALESCE(api_model_name, product_id) FROM service_models
		    WHERE deleted_at IS NULL AND protected = false
		      AND (status IN ('active','eol_pending')
		           OR (status = 'eol' AND (eol_purge_at IS NULL OR eol_purge_at > NOW())))
		 )
		 UPDATE user_groups SET models = (
		    SELECT COALESCE(jsonb_agg(elem), '[]'::jsonb)
		    FROM jsonb_array_elements_text(COALESCE(models, '[]'::jsonb)) elem
		    WHERE elem IN (SELECT n FROM served)
		       OR (elem NOT IN (
		      SELECT sm.product_id FROM service_models sm
		      WHERE sm.status IN ('discontinued', 'ignored')
		        AND sm.protected = false
		    )
		    AND elem NOT IN (
		      SELECT COALESCE(sm.api_model_name, sm.product_id) FROM service_models sm
		      WHERE sm.status IN ('discontinued', 'ignored')
		        AND sm.protected = false
		    )
		    AND elem NOT IN (
		      SELECT sm.product_id FROM service_models sm
		      WHERE sm.status = 'eol'
		        AND sm.protected = false
		        AND sm.eol_purge_at IS NOT NULL
		        AND sm.eol_purge_at <= NOW()
		    )
		    AND elem NOT IN (
		      SELECT COALESCE(sm.api_model_name, sm.product_id) FROM service_models sm
		      WHERE sm.status = 'eol'
		        AND sm.protected = false
		        AND sm.eol_purge_at IS NOT NULL
		        AND sm.eol_purge_at <= NOW()
		    ))
		  ), updated_at = NOW()
		  WHERE models IS NOT NULL AND models::text != '[]'`)
	if err != nil {
		r.logger.Warn("Failed to cleanup group.models", zap.Error(err))
	}

	// Remove from keys' selected_models (match both product_id and api_model_name).
	// Same sunset-period rule for EOL models, and the same multi-provider
	// protection: never strip a name that is still servable on another provider.
	_, err = r.db.Pool.Exec(ctx,
		`WITH served AS (
		    SELECT product_id AS n FROM service_models
		    WHERE deleted_at IS NULL AND protected = false
		      AND (status IN ('active','eol_pending')
		           OR (status = 'eol' AND (eol_purge_at IS NULL OR eol_purge_at > NOW())))
		    UNION
		    SELECT COALESCE(api_model_name, product_id) FROM service_models
		    WHERE deleted_at IS NULL AND protected = false
		      AND (status IN ('active','eol_pending')
		           OR (status = 'eol' AND (eol_purge_at IS NULL OR eol_purge_at > NOW())))
		 )
		 UPDATE api_keys SET selected_models = (
		    SELECT COALESCE(jsonb_agg(elem), '[]'::jsonb)
		    FROM jsonb_array_elements_text(COALESCE(selected_models, '[]'::jsonb)) elem
		    WHERE elem IN (SELECT n FROM served)
		       OR (elem NOT IN (
		      SELECT sm.product_id FROM service_models sm
		      WHERE sm.status IN ('discontinued', 'ignored')
		        AND sm.protected = false
		    )
		    AND elem NOT IN (
		      SELECT COALESCE(sm.api_model_name, sm.product_id) FROM service_models sm
		      WHERE sm.status IN ('discontinued', 'ignored')
		        AND sm.protected = false
		    )
		    AND elem NOT IN (
		      SELECT sm.product_id FROM service_models sm
		      WHERE sm.status = 'eol'
		        AND sm.protected = false
		        AND sm.eol_purge_at IS NOT NULL
		        AND sm.eol_purge_at <= NOW()
		    )
		    AND elem NOT IN (
		      SELECT COALESCE(sm.api_model_name, sm.product_id) FROM service_models sm
		      WHERE sm.status = 'eol'
		        AND sm.protected = false
		        AND sm.eol_purge_at IS NOT NULL
		        AND sm.eol_purge_at <= NOW()
		    ))
		  ), updated_at = NOW()
		  WHERE selected_models IS NOT NULL AND selected_models::text != '[]'`)
	if err != nil {
		r.logger.Warn("Failed to cleanup keys' selected_models", zap.Error(err))
	}

	// Fallback empty selected_models
	r.fallbackEmptySelectedModels(ctx)

	// Update cleanup timestamps for models that are fully expired/discontinued now.
	if err := r.db.Exec(ctx, `UPDATE service_models SET last_group_cleanup_at = NOW()
			  WHERE (
			    status IN ('discontinued', 'ignored')
			    OR (status = 'eol' AND eol_purge_at IS NOT NULL AND eol_purge_at <= NOW())
			  )
			  AND protected = false`); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
	}
}

// fallbackEmptySelectedModels sets keys with empty selected_models to their group's first model
func (r *ModelRoutes) fallbackEmptySelectedModels(ctx context.Context) {
	// Find keys whose selected_models became empty or null but still have a group
	rows, err := r.db.Query(ctx,
		`SELECT ak.id::text, ak.name, ak.user_id::text, ak.group_id::text, ug.models
		 FROM api_keys ak
		 JOIN user_groups ug ON ak.group_id = ug.id
		 WHERE ak.deleted_at IS NULL
		   AND (ak.selected_models IS NULL OR ak.selected_models::text IN ('[]', 'null', ''))
		   AND ug.models IS NOT NULL AND ug.models::text NOT IN ('[]', 'null', '')`)
	if err != nil {
		r.logger.Warn("Failed to query keys with empty selected_models", zap.Error(err))
		return
	}
	defer rows.Close()

	for rows.Next() {
		var keyID, keyName, userID, groupID string
		var groupModelsJSON interface{}
		_ = rows.Scan(&keyID, &keyName, &userID, &groupID, &groupModelsJSON)

		// Parse group models to get the first model
		var models []string
		switch v := groupModelsJSON.(type) {
		case []byte:
			_ = json.Unmarshal(v, &models)
		case string:
			_ = json.Unmarshal([]byte(v), &models)
		}

		if len(models) > 0 {
			// Set selected_models to group's first model as fallback
			fallbackJSON, _ := json.Marshal(models[:1])
			_, err := r.db.Pool.Exec(ctx,
				`UPDATE api_keys SET selected_models = $1, updated_at = NOW() WHERE id = $2::uuid`,
				fallbackJSON, keyID)
			if err != nil {
				r.logger.Warn("Failed to fallback key selected_models",
					zap.String("keyId", keyID), zap.Error(err))
			} else {
				r.logger.Info("Fallback key selected_models to group first model",
					zap.String("keyId", keyID), zap.String("keyName", keyName),
					zap.String("fallbackModel", models[0]))
				// Send notification email
				r.sendKeyFallbackNotification(ctx, userID, keyName, models[0])
			}
		}
	}
}

// generateAPIModelName creates a simplified api_model_name from a product_id.
// For multi-segment IDs like "Pro/moonshotai/Kimi-K2.6" or "deepseek-ai/DeepSeek-V4-Flash",
// it takes the last segment and lowercases it: "kimi-k2.6", "deepseek-v4-flash".
// For simple IDs like "gpt-4o", it just lowercases them.
func generateAPIModelName(productID string) string {
	parts := strings.Split(productID, "/")
	name := parts[len(parts)-1]
	return strings.ToLower(name)
}

// awsInfraProviders lists AWS Bedrock infrastructure-level providers whose prefix
// should be stripped from model IDs. Models from other providers (deepseek, qwen,
// minimax, etc.) keep their full product_id as the api_model_name — otherwise
// users see confusing names like "v3.1" instead of "deepseek.v3.1".
var awsInfraProviders = map[string]bool{
	"amazon":    true, // Amazon Nova, Titan
	"anthropic": true, // Anthropic Claude
}

// generateBedrockAPIModelName generates a user-facing API model name from a Bedrock product ID.
// Only strips the prefix for AWS infrastructure providers (amazon, anthropic).
// For third-party models on Bedrock (deepseek, qwen, mistral, etc.), keeps the full ID.
// Examples:
//
//	"anthropic.claude-sonnet-4-6" → "claude-sonnet-4-6"
//	"amazon.nova-lite-v1:0"       → "nova-lite-v1:0"
//	"deepseek.v3.1"               → "deepseek.v3.1"  (not "v3.1"!)
//	"qwen.qwen3-32b"              → "qwen.qwen3-32b"  (not "qwen3-32b"!)
//	"claude-opus-4-6" (no prefix) → "claude-opus-4-6"
func generateBedrockAPIModelName(productID string) string {
	if !strings.Contains(productID, ".") {
		return productID
	}
	idx := strings.Index(productID, ".")
	prefix := productID[:idx]
	// Only strip for known infra providers; keep full ID for third-party models
	if awsInfraProviders[prefix] {
		return productID[idx+1:]
	}
	return productID
}

// filterSelectedModels returns the subset of fetched that appears in selected,
// preserving fetched order. An empty selection returns fetched unchanged.
func filterSelectedModels(fetched []string, selected []string) []string {
	if len(selected) == 0 {
		return fetched
	}
	want := make(map[string]bool, len(selected))
	for _, m := range selected {
		want[m] = true
	}
	out := make([]string, 0, len(selected))
	for _, m := range fetched {
		if want[m] {
			out = append(out, m)
		}
	}
	return out
}

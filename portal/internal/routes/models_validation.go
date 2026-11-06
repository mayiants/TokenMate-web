package routes

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// validateModel validates a single model's API name
// POST /admin/service-models/:id/validate
func (r *ModelRoutes) validateModel(c *gin.Context) {
	modelID := c.Param("id")

	// Get model info
	var provider, productID, modelType string
	var apiModelName *string
	var supportsThinking bool
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT provider, product_id, api_model_name,
		        COALESCE(aimodels_data->>'type', 'text'),
		        COALESCE(capabilities->>'supports_thinking', 'false') = 'true'
		 FROM service_models WHERE id = $1::uuid`,
		modelID,
	).Scan(&provider, &productID, &apiModelName, &modelType, &supportsThinking)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Model not found"}})
		return
	}

	apiName := ""
	if apiModelName != nil {
		apiName = *apiModelName
	}

	// Run validation
	result, err := r.validator.ValidateModel(c.Request.Context(), provider, productID, apiName, modelType, supportsThinking)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "An internal error occurred"}})
		return
	}

	// Update model validation status
	if err := r.validator.UpdateModelValidation(c.Request.Context(), modelID, result); err != nil {
		r.logger.Warn("Failed to update model validation status", zap.Error(err))
	}

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "admin", "model_validated", "info",
			fmt.Sprintf("Model validated: %s (available=%v)", modelID, result.Available))
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"result":  result,
	})
}

// autoConfirmModel validates and auto-confirms a model if valid
// POST /admin/service-models/:id/auto-confirm
func (r *ModelRoutes) autoConfirmModel(c *gin.Context) {
	modelID := c.Param("id")

	// Get model info
	var provider, productID, status, modelType string
	var apiModelName *string
	var supportsThinking bool
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT provider, product_id, api_model_name, status,
		        COALESCE(aimodels_data->>'type', 'text'),
		        COALESCE(capabilities->>'supports_thinking', 'false') = 'true'
		 FROM service_models WHERE id = $1::uuid`,
		modelID,
	).Scan(&provider, &productID, &apiModelName, &status, &modelType, &supportsThinking)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Model not found"}})
		return
	}

	apiName := ""
	if apiModelName != nil {
		apiName = *apiModelName
	}

	// Run validation
	result, err := r.validator.ValidateModel(c.Request.Context(), provider, productID, apiName, modelType, supportsThinking)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "An internal error occurred"}})
		return
	}

	// Update validation status
	if err := r.validator.UpdateModelValidation(c.Request.Context(), modelID, result); err != nil {
		r.logger.Warn("Failed to update model validation status", zap.Error(err))
	}

	// Auto-confirm if valid
	if result.Available {
		newAPIName := result.SuggestedName
		if newAPIName == "" {
			newAPIName = apiName
		}
		_, err = r.db.Pool.Exec(c.Request.Context(),
			`UPDATE service_models SET status = 'active', api_model_name = $1, confirmed_at = NOW(), updated_at = NOW() WHERE id = $2::uuid`,
			newAPIName, modelID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "An internal error occurred"}})
			return
		}
		// Same confirm semantics as confirmModel: pricing rule + key sync.
		if newAPIName != "" && provider != "" {
			r.ensurePricingRule(c.Request.Context(), provider, productID, newAPIName)
		}
		if provider != "" {
			go func(p string) {
				if n, err := syncServiceModelsToKeys(r.db, context.Background(), p); err != nil {
					r.logger.Warn("Auto-sync after auto-confirm failed", zap.String("provider", p), zap.Error(err))
				} else {
					r.logger.Info("Auto-sync after auto-confirm", zap.String("provider", p), zap.Int64("keys_updated", n))
				}
				r.db.PublishSyncEvent(context.Background())
			}(provider)
		}
		if r.audit != nil {
			r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "admin", "model_auto_confirmed", "info",
				fmt.Sprintf("Model auto-confirmed: %s", modelID))
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success":       true,
		"result":        result,
		"autoConfirmed": result.Available,
	})
}

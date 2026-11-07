package server

import (
	"net/http"

	"github.com/mayiants/ai-proxy-go/pkg/models"
	"github.com/gin-gonic/gin"
)

// handleValidateModel validates a single model
// POST /api/internal/validate-model
func (s *HandlerService) handleValidateModel(c *gin.Context) {
	var req struct {
		Provider  string `json:"provider" binding:"required"`
		ProductID string `json:"product_id" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request: " + err.Error()}})
		return
	}

	if s.modelValidator == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Model validator not initialized"}})
		return
	}

	result := s.modelValidator.ValidateModel(c.Request.Context(), req.Provider, req.ProductID)

	c.JSON(http.StatusOK, result)
}

// handleBatchValidate validates multiple models in parallel
// POST /api/internal/batch-validate
func (s *HandlerService) handleBatchValidate(c *gin.Context) {
	var req struct {
		Models []struct {
			Provider  string `json:"provider" binding:"required"`
			ProductID string `json:"product_id" binding:"required"`
		} `json:"models" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request: " + err.Error()}})
		return
	}

	if s.modelValidator == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Model validator not initialized"}})
		return
	}

	// Convert to ServiceModel slice
	modelsToValidate := make([]models.ServiceModel, len(req.Models))
	for i, m := range req.Models {
		modelsToValidate[i] = models.ServiceModel{
			Provider:  m.Provider,
			ProductID: m.ProductID,
		}
	}

	results := s.modelValidator.BatchValidate(c.Request.Context(), modelsToValidate)

	c.JSON(http.StatusOK, gin.H{
		"results": results,
		"total":   len(results),
	})
}

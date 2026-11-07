package routes

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/mayiants/ai-portal-go/internal/query"
	"github.com/mayiants/ai-portal-go/pkg/crypto"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (r *AdminRoutes) listProviders(c *gin.Context) {
	data, err := query.GetAdminProvidersData(c.Request.Context(), r.db)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list providers"}})
		return
	}

	providers := []gin.H{}
	for _, p := range data.Providers {
		var apiKeyMasked interface{} = nil
		if p.APIKey != nil && *p.APIKey != "" {
			raw := *p.APIKey
			if r.cfg.Auth.EncryptionKey != "" {
				if decrypted, err := crypto.Decrypt(raw, r.cfg.Auth.EncryptionKey); err == nil {
					raw = decrypted
				}
			}
			if len(raw) > 4 {
				masked := raw[:4] + "****"
				apiKeyMasked = masked
			} else {
				apiKeyMasked = "****"
			}
		}
		providers = append(providers, gin.H{
			"id": p.ID, "name": p.Name, "type": p.Type, "base_url": p.BaseURL,
			"sync_source": p.SyncSource, "api_key": apiKeyMasked, "description": p.Description,
			"enabled": p.Enabled, "sort_order": p.SortOrder,
		})
	}

	c.JSON(http.StatusOK, gin.H{"providers": providers})
}
func (r *AdminRoutes) createProvider(c *gin.Context) {
	var req struct {
		ID          string  `json:"id" binding:"required"`
		Name        string  `json:"name" binding:"required"`
		Type        string  `json:"type"`
		BaseURL     string  `json:"base_url"`
		SyncSource  string  `json:"sync_source"`
		APIKey      *string `json:"api_key"`
		Description *string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	if req.Type == "" {
		req.Type = "international"
	}
	if req.SyncSource == "" {
		req.SyncSource = "manual"
	}

	// Encrypt api_key with AES-256-GCM (same as upstream_keys)
	var apiKeyStored *string
	if req.APIKey != nil && *req.APIKey != "" {
		if r.cfg.Auth.EncryptionKey != "" {
			encrypted, err := crypto.Encrypt(*req.APIKey, r.cfg.Auth.EncryptionKey)
			if err != nil {
				r.logger.Error("Failed to encrypt provider api_key", zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to encrypt api_key"}})
				return
			}
			s := encrypted
			apiKeyStored = &s
		} else {
			apiKeyStored = req.APIKey
		}
	}

	if err := r.db.Exec(c.Request.Context(),
		`INSERT INTO providers (id, name, type, base_url, sync_source, api_key, description) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		req.ID, req.Name, req.Type, req.BaseURL, req.SyncSource, apiKeyStored, req.Description); err != nil {
		r.logger.Error("Failed to create provider", zap.Error(err), zap.String("id", req.ID))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to create provider"}})
		return
	}

	go r.db.PublishSyncEvent(context.Background())
	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "admin", "provider_created", "info", fmt.Sprintf("Provider created: %s", req.Name))
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
func (r *AdminRoutes) updateProvider(c *gin.Context) {
	providerID := c.Param("id")

	var req struct {
		Name        string  `json:"name"`
		Enabled     *bool   `json:"enabled"`
		BaseURL     string  `json:"base_url"`
		Type        string  `json:"type"`
		APIKey      *string `json:"api_key"`
		Description *string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	ctx := c.Request.Context()

	// Build a single dynamic UPDATE statement
	setClauses := []string{"updated_at = NOW()"}
	args := []interface{}{}
	argIdx := 1

	if req.Name != "" {
		setClauses = append(setClauses, fmt.Sprintf("name = $%d", argIdx))
		args = append(args, req.Name)
		argIdx++
	}
	if req.Enabled != nil {
		setClauses = append(setClauses, fmt.Sprintf("enabled = $%d", argIdx))
		args = append(args, *req.Enabled)
		argIdx++
	}
	if req.BaseURL != "" {
		setClauses = append(setClauses, fmt.Sprintf("base_url = $%d", argIdx))
		args = append(args, req.BaseURL)
		argIdx++
	}
	if req.Type != "" {
		setClauses = append(setClauses, fmt.Sprintf("type = $%d", argIdx))
		args = append(args, req.Type)
		argIdx++
	}
	if req.APIKey != nil {
		// Encrypt api_key with AES-256-GCM (same as upstream_keys)
		var apiKeyVal string
		if *req.APIKey != "" && r.cfg.Auth.EncryptionKey != "" {
			encrypted, err := crypto.Encrypt(*req.APIKey, r.cfg.Auth.EncryptionKey)
			if err != nil {
				r.logger.Error("Failed to encrypt provider api_key", zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to encrypt api_key"}})
				return
			}
			apiKeyVal = encrypted
		} else {
			apiKeyVal = *req.APIKey
		}
		setClauses = append(setClauses, fmt.Sprintf("api_key = $%d", argIdx))
		args = append(args, apiKeyVal)
		argIdx++
	}
	if req.Description != nil {
		setClauses = append(setClauses, fmt.Sprintf("description = $%d", argIdx))
		args = append(args, *req.Description)
		argIdx++
	}

	query := fmt.Sprintf("UPDATE providers SET %s WHERE id = $%d", strings.Join(setClauses, ", "), argIdx)
	args = append(args, providerID)

	result, err := r.db.Pool.Exec(ctx, query, args...)
	if err != nil {
		r.logger.Error("Failed to update provider", zap.Error(err), zap.String("provider_id", providerID))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update provider"}})
		return
	}
	if result.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Provider not found"}})
		return
	}

	// Sync base_url to all upstream keys under this provider that don't have a custom base_url
	if req.BaseURL != "" {
		if err := r.db.Exec(ctx,
			`UPDATE upstream_keys SET base_url = $1 WHERE provider = $2 AND (base_url IS NULL OR base_url = '')`,
			req.BaseURL, providerID); err != nil {
			r.logger.Error("Failed to sync base_url to upstream_keys", zap.Error(err), zap.String("provider_id", providerID))
		}
	}

	go r.db.PublishSyncEvent(context.Background())
	if r.audit != nil {
		r.audit.Log(ctx, c.GetString("user_id"), c.GetString("email"), "admin", "provider_updated", "info", fmt.Sprintf("Provider updated: %s", req.Name))
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
func (r *AdminRoutes) toggleProvider(c *gin.Context) {
	providerID := c.Param("id")

	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	result, err := r.db.Pool.Exec(c.Request.Context(),
		`UPDATE providers SET enabled = $1, updated_at = NOW() WHERE id = $2`,
		req.Enabled, providerID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update provider"}})
		return
	}

	if result.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Provider not found"}})
		return
	}

	go r.db.PublishSyncEvent(context.Background())
	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "admin", "provider_toggled", "info", fmt.Sprintf("Provider %s status toggled to %v", providerID, req.Enabled))
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
func (r *AdminRoutes) deleteProvider(c *gin.Context) {
	providerID := c.Param("id")
	if err := r.db.Exec(c.Request.Context(), `DELETE FROM providers WHERE id = $1`, providerID); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
		return
	}
	go r.db.PublishSyncEvent(context.Background())
	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "admin", "provider_deleted", "info", fmt.Sprintf("Provider deleted: %s", providerID))
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/pkg/crypto"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

// isBedrockBearerKey reports whether an upstream key value is a Bedrock API
// Key (Bearer token, e.g. "ABSK...") rather than an "access_key:secret_key"
// pair. Bedrock API keys are a single opaque token with no ':' separator. The
// proxy authenticates these with an Authorization: Bearer header (no SigV4),
// so they are valid on both runtime (amazon-{region}) and Mantle providers.
func isBedrockBearerKey(key string) bool {
	if strings.Contains(key, ":") {
		return false
	}
	return strings.HasPrefix(key, "ABSK") || strings.HasPrefix(key, "bedrock-api-key-")
}

func (r *AdminRoutes) listUpstreamKeys(c *gin.Context) {
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT id, provider, key_preview, tier, base_url, models, enabled, assignment_mode, assigned_proxy, created_at
		 FROM upstream_keys WHERE deleted_at IS NULL ORDER BY provider, created_at`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list keys"}})
		return
	}
	defer rows.Close()

	keys := []gin.H{}
	for rows.Next() {
		var id, provider, preview, tier, assignmentMode string
		var baseURL, assignedProxy *string
		var models interface{}
		var enabled bool
		var createdAt time.Time
		_ = rows.Scan(&id, &provider, &preview, &tier, &baseURL, &models, &enabled, &assignmentMode, &assignedProxy, &createdAt)
		keys = append(keys, gin.H{
			"id": id, "provider": provider, "key_preview": preview, "tier": tier,
			"base_url": baseURL, "models": models, "enabled": enabled,
			"assignment_mode": assignmentMode, "assigned_proxy": assignedProxy,
			"created_at": createdAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{"keys": keys})
}
func (r *AdminRoutes) createUpstreamKey(c *gin.Context) {
	var req struct {
		Provider       string   `json:"provider" binding:"required"`
		Key            string   `json:"key" binding:"required"`
		Tier           string   `json:"tier"`
		BaseURL        string   `json:"base_url"`
		Models         []string `json:"models"`
		AssignmentMode string   `json:"assignment_mode"`
		AssignedProxy  string   `json:"assigned_proxy"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	keyPreview := req.Key
	if len(keyPreview) > 12 {
		keyPreview = keyPreview[:8] + "..."
	}

	// Encrypt key with AES-256-GCM
	var keyEncrypted string
	if r.cfg.Auth.EncryptionKey != "" {
		encrypted, err := crypto.Encrypt(req.Key, r.cfg.Auth.EncryptionKey)
		if err != nil {
			r.logger.Error("Failed to encrypt key", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to encrypt key"}})
			return
		}
		keyEncrypted = encrypted
	} else if r.cfg.Auth.AdminMode == "production" {
		// In production mode, require encryption
		r.logger.Error("ENCRYPTION_KEY not set, refusing to store key in production mode")
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "ENCRYPTION_KEY must be configured to store upstream keys in production mode"}})
		return
	} else {
		// Fallback: store as plaintext if no encryption key configured (only for MVP/dev mode)
		r.logger.Warn("No encryption key configured, storing key as plaintext (MVP mode)")
		keyEncrypted = req.Key
	}

	if req.AssignmentMode == "" {
		req.AssignmentMode = "exclusive"
	}

	var keyID string
	err := r.db.QueryRow(c.Request.Context(),
		`INSERT INTO upstream_keys (provider, key_encrypted, key_preview, tier, base_url, models, assignment_mode, assigned_proxy)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		req.Provider, keyEncrypted, keyPreview, req.Tier, req.BaseURL, req.Models, req.AssignmentMode, req.AssignedProxy,
	).Scan(&keyID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to create key"}})
		return
	}

	go r.db.PublishSyncEvent(context.Background())
	c.JSON(http.StatusOK, gin.H{"key": gin.H{"id": keyID}})
}

// ============================================
// Key Pool Management (matches Node.js frontend)
// ============================================

// getKeyPool returns upstream keys from DB (with optional proxy enrichment)
func (r *AdminRoutes) getKeyPool(c *gin.Context) {
	providerFilter := c.Query("provider")

	// Query upstream_keys from DB
	query := `SELECT id::text, provider, key_preview, rpm_limit, tpm_limit, model_limits, quota_limit, tier, base_url, models,
	         enabled, validation_status, assignment_mode, assigned_proxy, created_at
	         FROM upstream_keys WHERE deleted_at IS NULL`
	args := []interface{}{}

	if providerFilter != "" && providerFilter != "all" {
		query += " AND provider = $" + strconv.Itoa(len(args)+1)
		args = append(args, providerFilter)
	}

	query += " ORDER BY provider, created_at DESC"

	rows, err := r.db.Query(c.Request.Context(), query, args...)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"keys": []gin.H{}, "stats": []gin.H{}})
		return
	}
	defer rows.Close()

	keys := []gin.H{}
	providerStats := make(map[string]map[string]interface{})

	for rows.Next() {
		var id, provider, keyPreview, assignmentMode string
		var rpmLimit, tpmLimit *int
		var modelLimitsRaw interface{}
		var quotaLimit *int64
		var tier *string
		var baseURL, assignedProxy *string
		var validationStatus *string
		var models interface{}
		var enabled bool
		var createdAt time.Time
		_ = rows.Scan(&id, &provider, &keyPreview, &rpmLimit, &tpmLimit, &modelLimitsRaw, &quotaLimit, &tier, &baseURL, &models,
			&enabled, &validationStatus, &assignmentMode, &assignedProxy, &createdAt)

		status := "active"
		if !enabled {
			status = "disabled"
		}

		vStatus := ""
		if validationStatus != nil {
			vStatus = *validationStatus
		}

		keys = append(keys, gin.H{
			"id":                id,
			"key_id":            id,
			"provider":          provider,
			"key_preview":       keyPreview,
			"keyPreview":        keyPreview,
			"Preview":           keyPreview,
			"rpm_limit":         rpmLimit,
			"RpmLimit":          rpmLimit,
			"tpm_limit":         tpmLimit,
			"TpmLimit":          tpmLimit,
			"model_limits":      db.ParseModelLimits(modelLimitsRaw),
			"ModelLimits":       db.ParseModelLimits(modelLimitsRaw),
			"quota_limit":       quotaLimit,
			"QuotaLimit":        quotaLimit,
			"tier":              tier,
			"Tier":              tier,
			"base_url":          baseURL,
			"baseUrl":           baseURL,
			"BaseURL":           baseURL,
			"models":            models,
			"enabled":           enabled,
			"status":            status,
			"validation_status": vStatus,
			"ValidationStatus":  vStatus,
			"assignment_mode":   assignmentMode,
			"assignmentMode":    assignmentMode,
			"assigned_proxy":    assignedProxy,
			"assignedProxy":     assignedProxy,
			"created_at":        createdAt,
			"createdAt":         createdAt,
		})

		// Count per provider
		if _, ok := providerStats[provider]; !ok {
			providerStats[provider] = map[string]interface{}{
				"provider":       provider,
				"total":          0,
				"active":         0,
				"total_requests": 0,
				"total_tokens":   0,
			}
		}
		psMap := providerStats[provider]
		psMap["total"] = psMap["total"].(int) + 1
		if enabled {
			psMap["active"] = psMap["active"].(int) + 1
		}
	}

	// Convert providerStats map to slice
	stats := []gin.H{}
	for _, ps := range providerStats {
		stats = append(stats, gin.H{
			"provider":       ps["provider"],
			"total":          ps["total"],
			"active":         ps["active"],
			"total_requests": ps["total_requests"],
			"total_tokens":   ps["total_tokens"],
		})
	}

	c.JSON(http.StatusOK, gin.H{"keys": keys, "stats": stats})
}
func (r *AdminRoutes) getKeyHistory(c *gin.Context) {
	// Stub - return empty history for now
	c.JSON(http.StatusOK, gin.H{"history": []gin.H{}})
}
func (r *AdminRoutes) addKeyPoolKey(c *gin.Context) {
	var req struct {
		Provider   string   `json:"provider" binding:"required"`
		Key        string   `json:"key" binding:"required"`
		RpmLimit   *int     `json:"rpm_limit"`
		TpmLimit   *int     `json:"tpm_limit"`
		QuotaLimit *int64   `json:"quota_limit"`
		Tier       string   `json:"tier"`
		Notes      string   `json:"notes"`
		BaseURL    string   `json:"base_url"`
		Models     []string `json:"models"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Provider and key required"}})
		return
	}

	// Validate key format for AWS Bedrock Runtime providers. Two credential
	// forms are accepted: an AK/SK pair ("ACCESS_KEY_ID:SECRET_ACCESS_KEY")
	// or a Bedrock API Key (ABSK... bearer token, a single opaque string with
	// no ':'). The proxy authenticates AK/SK with SigV4 and ABSK with an
	// Authorization: Bearer header, so the same ABSK key can be registered on
	// both the runtime (amazon-{region}) and Mantle (amazon-mantle-{region})
	// providers.
	isAWSRuntime := req.Provider == "amazon" || (strings.HasPrefix(req.Provider, "amazon-") && !strings.HasPrefix(req.Provider, "amazon-mantle-"))
	if isAWSRuntime && !strings.Contains(req.Key, ":") && !isBedrockBearerKey(req.Key) {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "AWS Bedrock Runtime requires ACCESS_KEY_ID:SECRET_ACCESS_KEY format or a Bedrock API key (ABSK...)"}})
		return
	}

	keyPreview := req.Key
	if len(req.Key) > 12 {
		keyPreview = req.Key[:8] + "..." + req.Key[len(req.Key)-4:]
	}

	// Encrypt key with AES-256-GCM
	var keyEncrypted string
	if r.cfg.Auth.EncryptionKey != "" {
		encrypted, err := crypto.Encrypt(req.Key, r.cfg.Auth.EncryptionKey)
		if err != nil {
			r.logger.Error("Failed to encrypt key", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to encrypt key"}})
			return
		}
		keyEncrypted = encrypted
	} else if r.cfg.Auth.AdminMode == "production" {
		r.logger.Error("ENCRYPTION_KEY not set, refusing to store key in production mode")
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "ENCRYPTION_KEY must be configured to store upstream keys in production mode"}})
		return
	} else {
		r.logger.Warn("No encryption key configured, storing key as plaintext (MVP mode)")
		keyEncrypted = req.Key
	}

	if req.Tier == "" {
		req.Tier = "default"
	}

	// Resolve default base URL from providers table if not provided
	if req.BaseURL == "" {
		_ = r.db.QueryRow(c.Request.Context(),
			`SELECT COALESCE(base_url, '') FROM providers WHERE id = $1`, req.Provider,
		).Scan(&req.BaseURL)
	}

	var keyID string
	err := r.db.QueryRow(c.Request.Context(),
		`INSERT INTO upstream_keys (provider, key_encrypted, key_preview, rpm_limit, tpm_limit, quota_limit, tier, base_url, models, validation_status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'pending') RETURNING id::text`,
		req.Provider, keyEncrypted, keyPreview, req.RpmLimit, req.TpmLimit, req.QuotaLimit, req.Tier, req.BaseURL, req.Models,
	).Scan(&keyID)
	if err != nil {
		r.logger.Error("Failed to create upstream key", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to add key"}})
		return
	}

	// Trigger async validation
	go r.validateKeyAsync(keyID, req.Provider, req.Key, req.BaseURL)

	go r.db.PublishSyncEvent(context.Background())
	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "admin", "keypool_created", "info", fmt.Sprintf("Keypool key added for provider %s", req.Provider))
	}
	c.JSON(http.StatusOK, gin.H{"key": gin.H{"id": keyID, "key_preview": keyPreview}})
}

// updateKeyPoolKey updates an existing upstream key's fields
func (r *AdminRoutes) updateKeyPoolKey(c *gin.Context) {
	keyID := c.Param("keyId")

	var req struct {
		Provider    *string                   `json:"provider"`
		Key         *string                   `json:"key"`
		RpmLimit    *int                      `json:"rpm_limit"`
		TpmLimit    *int                      `json:"tpm_limit"`
		ModelLimits *map[string]db.ModelLimit `json:"model_limits"`
		QuotaLimit  *int64                    `json:"quota_limit"`
		Tier        *string                   `json:"tier"`
		BaseURL     *string                   `json:"base_url"`
		Models      []string                  `json:"models"`
		Enabled     *bool                     `json:"enabled"`
		Notes       *string                   `json:"notes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	// Build dynamic UPDATE
	sets := []string{}
	args := []interface{}{}
	argIdx := 1

	if req.Provider != nil {
		sets = append(sets, fmt.Sprintf("provider = $%d", argIdx))
		args = append(args, *req.Provider)
		argIdx++
	}
	if req.Key != nil && *req.Key != "" {
		keyPreview := *req.Key
		if len(keyPreview) > 12 {
			keyPreview = keyPreview[:8] + "..." + keyPreview[len(keyPreview)-4:]
		}
		var keyEncrypted string
		if r.cfg.Auth.EncryptionKey != "" {
			encrypted, err := crypto.Encrypt(*req.Key, r.cfg.Auth.EncryptionKey)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to encrypt key"}})
				return
			}
			keyEncrypted = encrypted
		} else {
			keyEncrypted = *req.Key
		}
		sets = append(sets, fmt.Sprintf("key_encrypted = $%d", argIdx))
		args = append(args, keyEncrypted)
		argIdx++
		sets = append(sets, fmt.Sprintf("key_preview = $%d", argIdx))
		args = append(args, keyPreview)
		argIdx++
	}
	if req.RpmLimit != nil {
		sets = append(sets, fmt.Sprintf("rpm_limit = $%d", argIdx))
		args = append(args, *req.RpmLimit)
		argIdx++
	}
	if req.TpmLimit != nil {
		sets = append(sets, fmt.Sprintf("tpm_limit = $%d", argIdx))
		args = append(args, *req.TpmLimit)
		argIdx++
	}
	if req.ModelLimits != nil {
		encoded, err := json.Marshal(*req.ModelLimits)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid model_limits"}})
			return
		}
		sets = append(sets, fmt.Sprintf("model_limits = $%d", argIdx))
		args = append(args, encoded)
		argIdx++
	}
	if req.QuotaLimit != nil {
		sets = append(sets, fmt.Sprintf("quota_limit = $%d", argIdx))
		args = append(args, *req.QuotaLimit)
		argIdx++
	}
	if req.Tier != nil {
		sets = append(sets, fmt.Sprintf("tier = $%d", argIdx))
		args = append(args, *req.Tier)
		argIdx++
	}
	if req.BaseURL != nil {
		sets = append(sets, fmt.Sprintf("base_url = $%d", argIdx))
		args = append(args, *req.BaseURL)
		argIdx++
	}
	if req.Models != nil {
		sets = append(sets, fmt.Sprintf("models = $%d", argIdx))
		args = append(args, req.Models)
		argIdx++
	}
	if req.Enabled != nil {
		sets = append(sets, fmt.Sprintf("enabled = $%d", argIdx))
		args = append(args, *req.Enabled)
		argIdx++
	}
	if req.Notes != nil {
		sets = append(sets, fmt.Sprintf("notes = $%d", argIdx))
		args = append(args, *req.Notes)
		argIdx++
	}

	if len(sets) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "No fields to update"}})
		return
	}

	sets = append(sets, "updated_at = NOW()")
	args = append(args, keyID)
	query := fmt.Sprintf("UPDATE upstream_keys SET %s WHERE id = $%d::uuid",
		strings.Join(sets, ", "), argIdx)

	_, err := r.db.Pool.Exec(c.Request.Context(), query, args...)
	if err != nil {
		r.logger.Error("Failed to update upstream key", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update key"}})
		return
	}

	go r.db.PublishSyncEvent(context.Background())
	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "admin", "keypool_updated", "info", fmt.Sprintf("Keypool key updated: %s", keyID))
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// validateKeyAsync validates an upstream key in the background
func (r *AdminRoutes) validateKeyAsync(keyID, provider, apiKey, baseURL string) {
	r.logger.Info("Starting async key validation",
		zap.String("key_id", keyID),
		zap.String("provider", provider),
		zap.String("base_url", baseURL))

	// Resolve base URL: custom > providers table > hardcoded fallback map
	url := baseURL
	if url == "" {
		var providerURL string
		_ = r.db.QueryRow(context.Background(),
			`SELECT COALESCE(base_url, '') FROM providers WHERE id = $1`,
			provider,
		).Scan(&providerURL)
		if providerURL != "" {
			url = providerURL
		}
	}
	if url == "" {
		url = getProviderDefaultURL(provider)
	}
	if url == "" {
		r.logger.Warn("No base URL for validation", zap.String("provider", provider))
		_ = r.db.Exec(context.Background(),
			`UPDATE upstream_keys SET validation_status = 'invalid', validation_at = NOW() WHERE id = $1::uuid`, keyID)
		return
	}

	// Use shared validation function for consistent URL building and error handling
	status, fetchedModels, err := validateUpstreamKey(provider, apiKey, url)

	if status == "valid" && len(fetchedModels) > 0 {
		modelsJSON, _ := json.Marshal(fetchedModels)
		_ = r.db.Exec(context.Background(),
			`UPDATE upstream_keys SET validation_status = $1, validation_at = NOW(), models = $2 WHERE id = $3::uuid`,
			status, modelsJSON, keyID)
	} else {
		_ = r.db.Exec(context.Background(),
			`UPDATE upstream_keys SET validation_status = $1, validation_at = NOW() WHERE id = $2::uuid`,
			status, keyID)
	}

	if err != nil {
		r.logger.Warn("Key validation error", zap.String("provider", provider), zap.String("key_id", keyID), zap.Error(err))
	} else {
		r.logger.Info("Key validation completed", zap.String("provider", provider), zap.String("key_id", keyID), zap.String("status", status), zap.Int("models_count", len(fetchedModels)))
	}
}
func (r *AdminRoutes) deleteKeyPoolKey(c *gin.Context) {
	provider := c.Param("provider")
	keyID := c.Param("keyId")

	// Delete by exact UUID only (LIKE removed for security)
	_, err := r.db.Pool.Exec(c.Request.Context(),
		`UPDATE upstream_keys SET deleted_at = NOW() WHERE provider = $1 AND id = $2::uuid AND deleted_at IS NULL`,
		provider, keyID)
	if err != nil {
		r.logger.Error("Failed to delete key pool key", zap.Error(err), zap.String("provider", provider), zap.String("key_id", keyID))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to delete key"}})
		return
	}

	go r.db.PublishSyncEvent(context.Background())
	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "admin", "keypool_deleted", "info", fmt.Sprintf("Keypool key deleted: %s", keyID))
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// deleteKeyPoolKeySimple - Delete key by ID only (for frontend compatibility)
func (r *AdminRoutes) deleteKeyPoolKeySimple(c *gin.Context) {
	keyID := c.Param("keyId")

	result, err := r.db.Pool.Exec(c.Request.Context(),
		`UPDATE upstream_keys SET deleted_at = NOW() WHERE id = $1::uuid AND deleted_at IS NULL`, keyID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": T("error.delete_failed", c)}})
		return
	}

	if result.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": T("error.key_not_found", c)}})
		return
	}

	go r.db.PublishSyncEvent(context.Background())
	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "admin", "keypool_deleted", "info", fmt.Sprintf("Keypool key deleted: %s", keyID))
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
func (r *AdminRoutes) toggleKeyPoolKey(c *gin.Context) {
	provider := c.Param("provider")
	keyID := c.Param("keyId")

	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	if req.Enabled != nil {
		if _, err := uuid.Parse(keyID); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid key ID"}})
			return
		}
		_, err := r.db.Pool.Exec(c.Request.Context(),
			`UPDATE upstream_keys SET enabled = $1, updated_at = NOW()
			 WHERE provider = $2 AND id = $3::uuid`,
			*req.Enabled, provider, keyID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to toggle key"}})
			return
		}
	}

	go r.db.PublishSyncEvent(context.Background())
	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "admin", "keypool_toggled", "info", fmt.Sprintf("Keypool key %s toggled", keyID))
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// toggleKeyPoolKeySimple - Toggle key by ID only (for frontend compatibility)
func (r *AdminRoutes) toggleKeyPoolKeySimple(c *gin.Context) {
	keyID := c.Param("keyId")

	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	if req.Enabled != nil {
		_, err := r.db.Pool.Exec(c.Request.Context(),
			`UPDATE upstream_keys SET enabled = $1, updated_at = NOW() WHERE id = $2::uuid`,
			*req.Enabled, keyID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": T("error.toggle_failed", c)}})
			return
		}
	}

	go r.db.PublishSyncEvent(context.Background())
	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "admin", "keypool_toggled", "info", fmt.Sprintf("Keypool key %s toggled", keyID))
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

const maxImportKeys = 100

func (r *AdminRoutes) importKeyPoolKeys(c *gin.Context) {
	// Support both single provider and mixed provider modes
	var singleReq struct {
		Provider string   `json:"provider"`
		Keys     []string `json:"keys"`
		RpmLimit *int     `json:"rpm_limit"`
		TpmLimit *int     `json:"tpm_limit"`
		BaseURL  string   `json:"base_url"`
		Models   []string `json:"models"`
	}

	var mixedReq struct {
		Keys []struct {
			Provider string `json:"provider"`
			Key      string `json:"key"`
			BaseURL  string `json:"base_url"`
			RpmLimit int    `json:"rpm_limit"`
			TpmLimit int    `json:"tpm_limit"`
			Enabled  bool   `json:"enabled"`
		} `json:"keys"`
	}

	// Try to detect which format
	body, _ := c.GetRawData()
	c.Request.Body = io.NopCloser(bytes.NewBuffer(body))

	// Try single provider format first
	if err := c.ShouldBindJSON(&singleReq); err == nil && singleReq.Provider != "" && len(singleReq.Keys) > 0 {
		if len(singleReq.Keys) > maxImportKeys {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf("Too many keys: max %d per batch", maxImportKeys)}})
			return
		}

		imported, skipped, err := r.importSingleProviderKeys(c.Request.Context(), singleReq)
		if err != nil {
			r.logger.Error("Single provider key import failed", zap.String("provider", singleReq.Provider), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to import keys"}})
			return
		}

		if r.audit != nil {
			r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "admin", "keypool_imported", "info", fmt.Sprintf("Imported %d keys for provider %s", imported, singleReq.Provider))
		}
		c.JSON(http.StatusOK, gin.H{"imported": imported, "skipped": skipped})
		// Auto-sync active service_models to keys for this provider
		go func() {
			if n, err := syncServiceModelsToKeys(r.db, context.Background(), singleReq.Provider); err != nil {
				r.logger.Warn("Auto-sync after import failed", zap.String("provider", singleReq.Provider), zap.Error(err))
			} else {
				r.logger.Info("Auto-sync after import", zap.String("provider", singleReq.Provider), zap.Int64("keys_updated", n))
			}
			r.db.PublishSyncEvent(context.Background())
		}()
		return
	}

	// Try mixed provider format
	c.Request.Body = io.NopCloser(bytes.NewBuffer(body))
	if err := c.ShouldBindJSON(&mixedReq); err == nil && len(mixedReq.Keys) > 0 {
		if len(mixedReq.Keys) > maxImportKeys {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf("Too many keys: max %d per batch", maxImportKeys)}})
			return
		}

		imported, skipped, err := r.importMixedProviderKeys(c.Request.Context(), mixedReq)
		if err != nil {
			r.logger.Error("Mixed provider key import failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to import keys"}})
			return
		}

		if r.audit != nil {
			r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "admin", "keypool_imported", "info", fmt.Sprintf("Imported %d keys for provider %s", imported, "multiple"))
		}
		c.JSON(http.StatusOK, gin.H{"imported": imported, "skipped": skipped})
		// Auto-sync active service_models to keys for all involved providers
		go func() {
			if n, err := syncServiceModelsToKeys(r.db, context.Background(), ""); err != nil {
				r.logger.Warn("Auto-sync after import failed", zap.Error(err))
			} else {
				r.logger.Info("Auto-sync after import", zap.Int64("keys_updated", n))
			}
			r.db.PublishSyncEvent(context.Background())
		}()
		return
	}

	c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request format"}})
}

// importSingleProviderKeys imports keys for a single provider inside a transaction.
// Duplicates are counted as skipped. DB errors roll back the entire transaction.
func (r *AdminRoutes) importSingleProviderKeys(ctx context.Context, req struct {
	Provider string   `json:"provider"`
	Keys     []string `json:"keys"`
	RpmLimit *int     `json:"rpm_limit"`
	TpmLimit *int     `json:"tpm_limit"`
	BaseURL  string   `json:"base_url"`
	Models   []string `json:"models"`
}) (imported, skipped int, err error) {
	err = r.db.WithTx(ctx, func(tx pgx.Tx) error {
		for _, key := range req.Keys {
			key = trimSpace(key)
			if key == "" {
				continue
			}

			keyPreview := key
			if len(key) > 12 {
				keyPreview = key[:8] + "..." + key[len(key)-4:]
			}

			keyEncrypted, encryptErr := r.encryptUpstreamKey(key)
			if encryptErr != nil {
				r.logger.Warn("Skipping key due to encryption failure", zap.Error(encryptErr))
				skipped++
				continue
			}

			result, execErr := tx.Exec(ctx,
				`INSERT INTO upstream_keys (provider, key_encrypted, key_preview, rpm_limit, tpm_limit, base_url, models)
				 VALUES ($1, $2, $3, $4, $5, $6, $7)
				 ON CONFLICT (provider, key_encrypted) DO NOTHING`,
				req.Provider, keyEncrypted, keyPreview, req.RpmLimit, req.TpmLimit, req.BaseURL, req.Models)
			if execErr != nil {
				return execErr
			}
			if result.RowsAffected() == 0 {
				skipped++
			} else {
				imported++
			}
		}
		return nil
	})
	return imported, skipped, err
}

// importMixedProviderKeys imports keys with per-key providers inside a transaction.
// Duplicates are counted as skipped. DB errors roll back the entire transaction.
func (r *AdminRoutes) importMixedProviderKeys(ctx context.Context, req struct {
	Keys []struct {
		Provider string `json:"provider"`
		Key      string `json:"key"`
		BaseURL  string `json:"base_url"`
		RpmLimit int    `json:"rpm_limit"`
		TpmLimit int    `json:"tpm_limit"`
		Enabled  bool   `json:"enabled"`
	} `json:"keys"`
}) (imported, skipped int, err error) {
	err = r.db.WithTx(ctx, func(tx pgx.Tx) error {
		for _, k := range req.Keys {
			if k.Provider == "" || k.Key == "" {
				skipped++
				continue
			}

			keyPreview := k.Key
			if len(k.Key) > 12 {
				keyPreview = k.Key[:8] + "..." + k.Key[len(k.Key)-4:]
			}

			keyEncrypted, encryptErr := r.encryptUpstreamKey(k.Key)
			if encryptErr != nil {
				r.logger.Warn("Skipping key due to encryption failure", zap.String("provider", k.Provider), zap.Error(encryptErr))
				skipped++
				continue
			}

			var rpmLimit, tpmLimit *int
			if k.RpmLimit > 0 {
				rpmLimit = &k.RpmLimit
			}
			if k.TpmLimit > 0 {
				tpmLimit = &k.TpmLimit
			}

			result, execErr := tx.Exec(ctx,
				`INSERT INTO upstream_keys (provider, key_encrypted, key_preview, rpm_limit, tpm_limit, base_url)
				 VALUES ($1, $2, $3, $4, $5, $6)
				 ON CONFLICT (provider, key_encrypted) DO NOTHING`,
				k.Provider, keyEncrypted, keyPreview, rpmLimit, tpmLimit, k.BaseURL)
			if execErr != nil {
				return execErr
			}
			if result.RowsAffected() == 0 {
				skipped++
			} else {
				imported++
			}
		}
		return nil
	})
	return imported, skipped, err
}

// encryptUpstreamKey encrypts an upstream key if an encryption key is configured.
func (r *AdminRoutes) encryptUpstreamKey(key string) (string, error) {
	if r.cfg.Auth.EncryptionKey == "" {
		return key, nil
	}
	return crypto.Encrypt(key, r.cfg.Auth.EncryptionKey)
}
func (r *AdminRoutes) exportKeyPoolKeys(c *gin.Context) {
	// 1. Rate limiting: max 10 exports per user per hour
	userID := c.GetString("user_id")
	if checkExportLimit(userID) {
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
			"error": gin.H{"message": "Export rate limit exceeded. Maximum 10 exports per hour."},
		})
		return
	}

	// 2. Secondary password verification (operation password)
	var req struct {
		Password string `json:"password"`
	}
	_ = c.ShouldBindJSON(&req)
	if req.Password == "" {
		req.Password = c.PostForm("password")
	}
	if req.Password == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"message": "Operation password required for export"},
		})
		return
	}

	var passwordHash string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT password_hash FROM users WHERE id = $1::uuid`, userID,
	).Scan(&passwordHash)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password)) != nil {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": gin.H{"message": "Invalid operation password"},
		})
		if r.audit != nil {
			r.audit.Log(c.Request.Context(), userID, c.GetString("email"), "security", "keypool_export_failed", "warning", "Keypool export failed: invalid operation password")
		}
		return
	}

	provider := c.Query("provider")

	query := `SELECT id::text, provider, key_preview, key_encrypted, rpm_limit, tpm_limit, base_url, models, enabled, created_at
	          FROM upstream_keys WHERE deleted_at IS NULL`
	args := []interface{}{}

	if provider != "" && provider != "all" {
		query += " AND provider = $" + strconv.Itoa(len(args)+1)
		args = append(args, provider)
	}

	query += " ORDER BY provider, created_at"

	rows, err := r.db.Query(c.Request.Context(), query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to export keys"}})
		return
	}
	defer rows.Close()

	keys := []gin.H{}
	for rows.Next() {
		var id, provider, keyPreview, keyEncrypted string
		var rpmLimit, tpmLimit *int
		var baseURL *string
		var models interface{}
		var enabled bool
		var createdAt time.Time
		_ = rows.Scan(&id, &provider, &keyPreview, &keyEncrypted, &rpmLimit, &tpmLimit, &baseURL, &models, &enabled, &createdAt)

		// Decrypt key for export
		var keyDecrypted string
		if r.cfg.Auth.EncryptionKey != "" {
			decrypted, err := crypto.Decrypt(keyEncrypted, r.cfg.Auth.EncryptionKey)
			if err != nil {
				r.logger.Warn("Failed to decrypt key for export, skipping",
					zap.String("key_id", id),
					zap.Error(err))
				continue
			}
			keyDecrypted = decrypted
		} else {
			keyDecrypted = keyEncrypted // no encryption
		}

		keys = append(keys, gin.H{
			"id": id, "provider": provider, "key": keyDecrypted, "key_preview": keyPreview,
			"rpm_limit": rpmLimit, "tpm_limit": tpmLimit, "base_url": baseURL, "models": models,
			"enabled": enabled, "created_at": createdAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{"keys": keys})
}

// validateKeyPoolKey validates a single upstream key
func (r *AdminRoutes) validateKeyPoolKey(c *gin.Context) {
	keyID := c.Param("keyId")

	// Get key from database
	var provider, keyEncrypted string
	var baseURL, models *string

	err := r.db.QueryRow(c.Request.Context(),
		`SELECT provider, key_encrypted, base_url, models FROM upstream_keys WHERE id = $1::uuid`,
		keyID,
	).Scan(&provider, &keyEncrypted, &baseURL, &models)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Key not found"}})
		return
	}

	// Decrypt key if encrypted
	var apiKey string
	if r.cfg.Auth.EncryptionKey != "" {
		decrypted, err := crypto.Decrypt(keyEncrypted, r.cfg.Auth.EncryptionKey)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to decrypt key"}})
			return
		}
		apiKey = decrypted
	} else {
		apiKey = keyEncrypted
	}

	// Get base URL: key's custom URL > provider's URL > provider default URL
	url := ""
	if baseURL != nil && *baseURL != "" {
		url = *baseURL
	}
	if url == "" {
		// Try provider's configured base_url from providers table
		var providerURL string
		_ = r.db.QueryRow(c.Request.Context(),
			`SELECT COALESCE(base_url, '') FROM providers WHERE id = $1`,
			provider,
		).Scan(&providerURL)
		if providerURL != "" {
			url = providerURL
		}
	}
	if url == "" {
		url = getProviderDefaultURL(provider)
	}
	if url == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "No base URL configured for provider"}})
		return
	}

	// Validate key by calling provider's /models endpoint
	status, fetchedModels, err := validateUpstreamKey(provider, apiKey, url)

	// Update validation status in database
	now := time.Now()
	if status == "valid" && len(fetchedModels) > 0 {
		modelsJSON, _ := json.Marshal(fetchedModels)
		if err := r.db.Exec(c.Request.Context(), `UPDATE upstream_keys SET validation_status = $1, validation_at = $2, models = $3, updated_at = NOW() WHERE id = $4::uuid`, status, now, modelsJSON, keyID); err != nil {
			r.logger.Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	} else {
		if err := r.db.Exec(c.Request.Context(), `UPDATE upstream_keys SET validation_status = $1, validation_at = $2, updated_at = NOW() WHERE id = $3::uuid`, status, now, keyID); err != nil {
			r.logger.Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}

	if err != nil {
		r.logger.Error("Upstream key validation failed", zap.Error(err), zap.String("provider", provider))
		c.JSON(http.StatusInternalServerError, gin.H{"status": status, "error": "Validation failed. Check logs for details."})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": status, "models": fetchedModels})
}

// validateKeyPoolKeysBatch validates multiple upstream keys
func (r *AdminRoutes) validateKeyPoolKeysBatch(c *gin.Context) {
	var req struct {
		Provider string `json:"provider"`
		Limit    int    `json:"limit"`
	}
	_ = c.ShouldBindJSON(&req)

	if req.Limit == 0 {
		req.Limit = 20
	}

	// Cap the overall operation at 5 minutes to avoid blocking the request indefinitely
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()

	// Get keys to validate
	query := `SELECT id::text, provider, key_encrypted, key_preview, base_url FROM upstream_keys WHERE enabled = true AND deleted_at IS NULL AND (validation_status != 'valid' OR validation_at IS NULL OR validation_at < NOW() - INTERVAL '24 hours')`
	args := []interface{}{}
	if req.Provider != "" {
		query += " AND provider = $" + strconv.Itoa(len(args)+1)
		args = append(args, req.Provider)
	}
	query += " LIMIT $" + strconv.Itoa(len(args)+1)
	args = append(args, req.Limit)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to get keys"}})
		return
	}
	defer rows.Close()

	validated := 0
	invalid := 0

	for rows.Next() {
		// Stop if the overall timeout has expired
		if ctx.Err() != nil {
			break
		}

		var id, provider, keyEncrypted, keyPreview string
		var baseURL *string
		_ = rows.Scan(&id, &provider, &keyEncrypted, &keyPreview, &baseURL)

		// Decrypt key
		var apiKey string
		if r.cfg.Auth.EncryptionKey != "" {
			decrypted, err := crypto.Decrypt(keyEncrypted, r.cfg.Auth.EncryptionKey)
			if err != nil {
				invalid++
				if err := r.db.Exec(ctx, `UPDATE upstream_keys SET validation_status = 'invalid', validation_at = NOW() WHERE id = $1::uuid`, id); err != nil {
					r.logger.Error("database operation failed", zap.Error(err))
					c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
					return
				}
				continue
			}
			apiKey = decrypted
		} else {
			apiKey = keyEncrypted
		}

		// Resolve base URL: custom > providers table > hardcoded fallback map
		url := ""
		if baseURL != nil {
			url = *baseURL
		}
		if url == "" {
			var providerURL string
			_ = r.db.QueryRow(ctx,
				`SELECT COALESCE(base_url, '') FROM providers WHERE id = $1`,
				provider,
			).Scan(&providerURL)
			if providerURL != "" {
				url = providerURL
			}
		}
		if url == "" {
			url = getProviderDefaultURL(provider)
		}
		if url == "" {
			invalid++
			logUpstreamKeyValidation(r.db, id, provider, keyPreview, "batch", "no_base_url", "no base_url configured for provider and no fallback", 0, 0)
			if err := r.db.Exec(ctx, `UPDATE upstream_keys SET validation_status = 'unknown', validation_at = NOW() WHERE id = $1::uuid`, id); err != nil {
				r.logger.Error("database operation failed", zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
				return
			}
			continue
		}

		// Validate
		vStart := time.Now()
		status, fetchedModels, vErr := validateUpstreamKey(provider, apiKey, url)
		vDurationMs := int(time.Since(vStart).Milliseconds())
		errMsg := ""
		if vErr != nil {
			errMsg = vErr.Error()
		}
		logUpstreamKeyValidation(r.db, id, provider, keyPreview, "batch", status, errMsg, len(fetchedModels), vDurationMs)

		now := time.Now()
		if status == "valid" && len(fetchedModels) > 0 {
			// Seed upstream-source capability rows for newly discovered model
			// names (name-based inference; manual overrides always win).
			upsertUpstreamCapabilityDefaults(ctx, r.db, fetchedModels)
			modelsJSON, _ := json.Marshal(fetchedModels)
			if err := r.db.Exec(ctx, `UPDATE upstream_keys SET validation_status = $1, validation_at = $2, models = $3 WHERE id = $4::uuid`, status, now, modelsJSON, id); err != nil {
				r.logger.Error("database operation failed", zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
				return
			}
		} else if status == "valid" || status == "invalid" || status == "pending" {
			if err := r.db.Exec(ctx, `UPDATE upstream_keys SET validation_status = $1, validation_at = $2 WHERE id = $3::uuid`, status, now, id); err != nil {
				r.logger.Error("database operation failed", zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
				return
			}
		} else {
			// Network-level probe failure: keep the stored status (sync only ships
			// valid/pending keys — demoting to unknown here would evict a healthy
			// key from all proxies), just stamp the attempt.
			if err := r.db.Exec(ctx, `UPDATE upstream_keys SET validation_at = $1 WHERE id = $2::uuid`, now, id); err != nil {
				r.logger.Error("database operation failed", zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
				return
			}
		}

		if status == "valid" {
			validated++
		} else {
			invalid++
		}
	}

	c.JSON(http.StatusOK, gin.H{"validated": validated, "invalid": invalid})
	go r.db.PublishSyncEvent(context.Background())
}

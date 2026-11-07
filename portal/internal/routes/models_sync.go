package routes

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mayiants/ai-portal-go/pkg/crypto"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (r *ModelRoutes) syncFromAIModels(c *gin.Context) {
	stats, err := syncAIModels(r.db, c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok": false, "message": "An internal error occurred",
		})
		return
	}
	// Run cleanup after sync
	r.CleanupAllInvalidModels(c.Request.Context())

	// Auto-sync active service_models to all keys
	go func() {
		if n, err := syncServiceModelsToKeys(r.db, context.Background(), ""); err != nil {
			r.logger.Warn("Auto-sync after aiModels sync failed", zap.Error(err))
		} else {
			r.logger.Info("Auto-sync after aiModels sync", zap.Int64("keys_updated", n))
		}
		r.db.PublishSyncEvent(context.Background())
	}()

	addedCount := 0
	skippedCount := 0
	if v, ok := stats["added"].(int); ok {
		addedCount = v
	}
	if v, ok := stats["skipped"].(int); ok {
		skippedCount = v
	}

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "admin", "models_synced", "info", fmt.Sprintf("Models synced from AI models: %d added, %d skipped", addedCount, skippedCount))
	}

	c.JSON(http.StatusOK, gin.H{
		"ok": true, "message": "Sync completed!",
		"added":      stats["added"],
		"hasChanges": stats["hasChanges"],
		"skipped":    stats["skipped"],
	})
}

// resyncServiceModelsToKeys manually triggers sync of all active service_models to upstream_keys
// POST /api/admin/service-models/resync-to-keys
func (r *ModelRoutes) resyncServiceModelsToKeys(c *gin.Context) {
	n, err := syncServiceModelsToKeys(r.db, c.Request.Context(), "")
	if err != nil {
		r.logger.Warn("Manual resync failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "An internal error occurred"})
		return
	}
	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "admin", "models_resynced_to_keys", "info",
			fmt.Sprintf("Manual resync completed: %d keys updated", n))
	}
	go r.db.PublishSyncEvent(c.Request.Context())
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Resync completed", "keys_updated": n})
}

// addFromOpenRouter adds models from openrouter_models to service_models
func (r *ModelRoutes) addFromOpenRouter(c *gin.Context) {
	var req struct {
		OpenRouterIDs []string `json:"openrouter_ids"` // IDs from openrouter_models
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.OpenRouterIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "openrouter_ids required"}})
		return
	}

	// Check A: reject if no providers configured at all
	var providerCount int
	_ = r.db.QueryRow(c.Request.Context(), `SELECT COUNT(*) FROM providers WHERE enabled = true`).Scan(&providerCount)
	if providerCount == 0 {
		c.JSON(http.StatusPreconditionFailed, gin.H{"error": gin.H{"message": T("error.configure_provider_first", c)}})
		return
	}

	// Pre-load valid provider names for check B
	validProviders := make(map[string]bool)
	provRows, _ := r.db.Query(c.Request.Context(), `SELECT id FROM providers WHERE enabled = true`)
	if provRows != nil {
		defer provRows.Close()
		for provRows.Next() {
			var id string
			if provRows.Scan(&id) == nil {
				validProviders[id] = true
			}
		}
	}

	added := 0
	skipped := 0
	errors := []string{}
	syncedProviders := make(map[string]bool)

	for _, orID := range req.OpenRouterIDs {
		// Get model from openrouter_models
		var openrouterID, provider, name string
		var inputPrice, outputPrice *float64
		var contextWindow *int
		var rawData interface{}

		err := r.db.QueryRow(c.Request.Context(),
			`SELECT openrouter_id, provider, name, input_price, output_price, context_window, raw_data
			 FROM openrouter_models WHERE id = $1::uuid`, orID,
		).Scan(&openrouterID, &provider, &name, &inputPrice, &outputPrice, &contextWindow, &rawData)
		if err != nil {
			skipped++
			errors = append(errors, "Model not found: "+orID)
			continue
		}

		// Check B: model's provider must exist in providers table
		if !validProviders[provider] {
			skipped++
			errors = append(errors, "Provider '"+provider+"' not configured, please add the provider first")
			continue
		}

		// Extract product_id from openrouter_id (e.g., "openai/gpt-4o" -> "gpt-4o")
		productID := openrouterID
		if idx := len(provider); idx > 0 && len(openrouterID) > idx+1 {
			// Try to extract after provider name
			parts := []byte(openrouterID)
			for i := 0; i < len(parts); i++ {
				if parts[i] == '/' && i+1 < len(parts) {
					productID = string(parts[i+1:])
					break
				}
			}
		}

		// Insert into service_models with status='pending'
		var inputPriceVal, outputPriceVal float64
		if inputPrice != nil {
			inputPriceVal = *inputPrice
		}
		if outputPrice != nil {
			outputPriceVal = *outputPrice
		}

		_, err = r.db.Pool.Exec(c.Request.Context(),
			`INSERT INTO service_models (provider, product_id, api_model_name,
			 input_price, output_price, input_price_cache_miss, aimodels_data, status)
			 VALUES ($1, $2, $3, $4, $5, $4, $6, 'pending')
			 ON CONFLICT (provider, product_id) DO UPDATE SET
			  input_price = EXCLUDED.input_price,
			  output_price = EXCLUDED.output_price,
			  input_price_cache_miss = EXCLUDED.input_price_cache_miss,
			  aimodels_data = EXCLUDED.aimodels_data,
			  updated_at = NOW()`,
			provider, productID, productID, inputPriceVal, outputPriceVal, rawData,
		)
		if err != nil {
			skipped++
			errors = append(errors, "Failed to add "+openrouterID)
			continue
		}

		// Check if actually inserted
		var exists bool
		_ = r.db.QueryRow(c.Request.Context(),
			`SELECT EXISTS(SELECT 1 FROM service_models WHERE provider = $1 AND product_id = $2 AND status = 'pending')`,
			provider, productID,
		).Scan(&exists)
		if exists {
			added++
			syncedProviders[provider] = true
		} else {
			skipped++
		}
	}

	// Auto-sync active service_models to keys for affected providers
	go func() {
		for p := range syncedProviders {
			if n, err := syncServiceModelsToKeys(r.db, context.Background(), p); err != nil {
				r.logger.Warn("Auto-sync after openrouter add failed", zap.String("provider", p), zap.Error(err))
			} else {
				r.logger.Info("Auto-sync after openrouter add", zap.String("provider", p), zap.Int64("keys_updated", n))
			}
		}
		r.db.PublishSyncEvent(context.Background())
	}()

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "admin", "models_added_from_openrouter", "info",
			fmt.Sprintf("Added %d models from OpenRouter", added))
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"added":   added,
		"skipped": skipped,
		"errors":  errors,
	})
}

// fetchProviderModels calls a provider's /v1/models API and returns the model list.
// GET /api/admin/provider-models/fetch?provider=anthropic&base_url=...&api_key=...
func (r *ModelRoutes) fetchProviderModels(c *gin.Context) {
	provider := c.Query("provider")
	if provider == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "provider parameter required"}})
		return
	}

	// Provider-specific models URL overrides
	// Some providers use non-standard paths (e.g. xunfei uses /v2/models)
	// Note: xunfei /v2/models returns empty data — models must be added manually
	providerModelsPath := map[string]string{
		"xunfei": "/v2/models",
	}

	upstreamKeyID := c.Query("upstream_key_id")

	// Resolve base URL: explicit param > selected upstream key > providers table > default.
	// A caller-supplied base_url is treated as untrusted: when set, we will
	// NOT fall back to decrypting a DB-stored upstream key (otherwise an
	// admin could point the request at an arbitrary URL and exfiltrate the
	// decrypted secret). A custom base_url must come with an explicit
	// X-Provider-API-Key header.
	baseURL := c.Query("base_url")
	customBaseURL := baseURL != ""
	var keyEncrypted, keyBaseURL string
	if !customBaseURL {
		if upstreamKeyID != "" {
			err := r.db.QueryRow(c.Request.Context(),
				`SELECT key_encrypted, COALESCE(base_url, '') FROM upstream_keys
				 WHERE id = $1::uuid AND provider = $2 AND enabled = true AND deleted_at IS NULL`,
				upstreamKeyID, provider,
			).Scan(&keyEncrypted, &keyBaseURL)
			if err != nil {
				if err == sql.ErrNoRows {
					c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Selected upstream key not found or not enabled"}})
				} else {
					r.logger.Warn("Error fetching selected upstream key",
						zap.String("provider", provider), zap.String("upstream_key_id", upstreamKeyID), zap.Error(err))
					c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Error fetching selected upstream key"}})
				}
				return
			}
			if keyBaseURL != "" {
				baseURL = keyBaseURL
			}
		}
		if baseURL == "" {
			var providerBaseURL *string
			err := r.db.QueryRow(c.Request.Context(),
				`SELECT base_url FROM providers WHERE id = $1`, provider,
			).Scan(&providerBaseURL)
			if err == nil && providerBaseURL != nil {
				baseURL = *providerBaseURL
			}
		}
	}
	if baseURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "No base URL found for provider. Set it in providers table or pass base_url parameter."}})
		return
	}

	// Resolve API key: header > query param (query is deprecated for security) > selected DB key > any DB key.
	// DB key decryption is only allowed when using the DB-configured base_url;
	// a custom base_url requires an explicit header key (see comment above).
	apiKey := c.GetHeader("X-Provider-API-Key")
	if apiKey == "" {
		apiKey = c.Query("api_key")
		if apiKey != "" {
			r.logger.Warn("api_key passed via query string — this is deprecated and will be removed. Use X-Provider-API-Key header instead.")
		}
	}
	if apiKey == "" && !customBaseURL {
		if keyEncrypted == "" {
			err := r.db.QueryRow(c.Request.Context(),
				`SELECT key_encrypted FROM upstream_keys WHERE provider = $1 AND enabled = true AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 1`, provider,
			).Scan(&keyEncrypted)
			if err != nil && err != sql.ErrNoRows {
				r.logger.Warn("Error fetching provider API key for model list",
					zap.String("provider", provider), zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Error fetching provider API key"}})
				return
			}
		}
		if keyEncrypted != "" {
			if r.cfg.Auth.EncryptionKey != "" {
				decrypted, err := crypto.Decrypt(keyEncrypted, r.cfg.Auth.EncryptionKey)
				if err != nil {
					r.logger.Warn("Failed to decrypt provider API key, skipping model fetch",
						zap.String("provider", provider), zap.Error(err))
					c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to decrypt provider API key"}})
					return
				}
				apiKey = decrypted
			} else {
				apiKey = keyEncrypted
			}
		}
	}
	if apiKey == "" {
		if customBaseURL {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "A custom base_url requires an explicit X-Provider-API-Key header (DB-stored keys are not sent to caller-supplied URLs)."}})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "No API key found for provider. Add an upstream key or pass api_key parameter."}})
		return
	}

	// Call provider models API
	// Build models URL: use provider-specific path if defined, otherwise baseURL + /models
	modelsPath, hasCustomPath := providerModelsPath[provider]
	var modelsURL string
	if hasCustomPath {
		// For providers with custom paths, append directly to the base domain
		// Strip any trailing /v1, /v2, /v4 etc. from baseURL to avoid double path
		cleanBase := strings.TrimRight(baseURL, "/")
		for _, suffix := range []string{"/v1", "/v2", "/v4", "/v1beta"} {
			if strings.HasSuffix(cleanBase, suffix) {
				cleanBase = cleanBase[:len(cleanBase)-len(suffix)]
				break
			}
		}
		modelsURL = cleanBase + modelsPath
	} else {
		// Most providers: baseURL already contains the version path, just append /models
		modelsURL = strings.TrimRight(baseURL, "/") + "/models"
	}

	// Set auth header based on provider
	var authHeader string
	var authValue string
	switch provider {
	case "anthropic":
		authHeader = "x-api-key"
		authValue = apiKey
	case "google":
		// Use the x-goog-api-key header instead of appending ?key=... to the
		// URL. Putting the secret in the URL leaks it via logs, referer
		// headers, and error messages returned to the caller.
		authHeader = "x-goog-api-key"
		authValue = apiKey
	case "xunfei":
		// XunFei uses Bearer APIKey:APISecret format
		authHeader = "Authorization"
		authValue = "Bearer " + apiKey
	default:
		authHeader = "Authorization"
		authValue = "Bearer " + apiKey
	}

	req, err := http.NewRequestWithContext(c.Request.Context(), "GET", modelsURL, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to create request"}})
		return
	}
	if authHeader != "" {
		req.Header.Set(authHeader, authValue)
	}
	if provider == "anthropic" {
		req.Header.Set("anthropic-version", "2023-06-01")
	}

	// TLS verification is enabled by default. It can be disabled only via an explicit
	// environment variable for emergency debugging; this is strongly discouraged in production.
	tlsConfig := &tls.Config{
		InsecureSkipVerify: false,
		MinVersion:         tls.VersionTLS12,
	}
	if os.Getenv("SKIP_UPSTREAM_TLS_VERIFY") == "true" {
		tlsConfig.InsecureSkipVerify = true
	}
	transport := &http.Transport{
		TLSClientConfig:   tlsConfig,
		ForceAttemptHTTP2: true,
	}
	client := &http.Client{Timeout: 30 * time.Second, Transport: transport}

	// Retry up to 3 times for TLS/network errors (CDN backends can be intermittent)
	var resp *http.Response
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		resp, err = client.Do(req)
		if err == nil {
			// Success: clear the sticky error from a prior failed attempt,
			// otherwise the post-loop `if lastErr != nil` check would return
			// 502 even though this attempt succeeded (first-fail-then-succeed).
			lastErr = nil
			break
		}
		lastErr = err
		r.logger.Warn("Provider models fetch attempt failed",
			zap.String("provider", provider),
			zap.Int("attempt", attempt),
			zap.Error(err))
		time.Sleep(time.Duration(attempt) * time.Second)
		// Re-create request since body was consumed
		req, err = http.NewRequestWithContext(c.Request.Context(), "GET", modelsURL, nil)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to create request"}})
			return
		}
		if authHeader != "" {
			req.Header.Set(authHeader, authValue)
		}
		if provider == "anthropic" {
			req.Header.Set("anthropic-version", "2023-06-01")
		}
	}
	if lastErr != nil {
		r.logger.Error("Failed to fetch provider models after retries", zap.String("provider", provider), zap.Error(lastErr))
		// Do not echo lastErr.Error() to the client: for google it used to
		// contain the full URL with the API key in the query string, and for
		// other providers it can leak internal hostnames.
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": "Failed to reach provider after retries"}})
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		c.JSON(resp.StatusCode, gin.H{"error": gin.H{"message": "Provider returned status " + strconv.Itoa(resp.StatusCode)}})
		return
	}

	var result struct {
		Data []struct {
			ID     string `json:"id"`
			Object string `json:"object"`
			Owned  bool   `json:"owned"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to parse provider response"}})
		return
	}

	// Get current service_models for this provider to show which are already mapped
	existingRows, err := r.db.Query(c.Request.Context(),
		`SELECT product_id, api_model_name FROM service_models WHERE provider = $1`, provider)
	if err == nil {
		defer existingRows.Close()
	}
	existing := map[string]string{}
	if existingRows != nil {
		for existingRows.Next() {
			var productID, apiModelName string
			if existingRows.Scan(&productID, &apiModelName) == nil {
				existing[strings.ToLower(productID)] = apiModelName
			}
		}
	}

	// Build response with mapping status
	type ModelInfo struct {
		ID            string `json:"id"`
		AlreadyMapped bool   `json:"already_mapped"`
		APIModelName  string `json:"api_model_name,omitempty"`
	}
	models := make([]ModelInfo, 0, len(result.Data))
	seen := make(map[string]bool, len(result.Data))
	for _, m := range result.Data {
		if m.Object != "" && m.Object != "model" && m.Object != "list" {
			continue
		}
		// Deduplicate by exact ID: some providers (e.g. zimaocloud) return the
		// same model ID multiple times with identical metadata. Keep the first.
		if seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		info := ModelInfo{ID: m.ID}
		if apiName, ok := existing[strings.ToLower(m.ID)]; ok {
			info.AlreadyMapped = true
			info.APIModelName = apiName
		}
		models = append(models, info)
	}

	r.logger.Info("Fetched provider models", zap.String("provider", provider), zap.Int("count", len(models)))

	// Hint for providers that don't return model lists via API
	hint := ""
	if len(models) == 0 {
		switch provider {
		case "xunfei":
			hint = T("hint.xunfei_models", c)
		case "baidu":
			hint = T("hint.baidu_models", c)
		default:
			hint = T("hint.empty_model_list", c)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"provider": provider,
		"base_url": baseURL,
		"models":   models,
		"total":    len(models),
		"hint":     hint,
	})
}

// syncKeyModels updates upstream_keys.models for all keys of a provider
// with the model list from the provider's /v1/models API.
// POST /api/admin/provider-models/sync-key-models
func (r *ModelRoutes) syncKeyModels(c *gin.Context) {
	var req struct {
		Provider string   `json:"provider" binding:"required"`
		Models   []string `json:"models" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "provider and models required"}})
		return
	}

	modelsJSON, err := json.Marshal(req.Models)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to marshal models"}})
		return
	}

	// Explicit admin action: force-update ALL enabled, non-deleted keys for this provider.
	// Unlike syncServiceModelsToKeys (auto-triggered, gentle), this is admin-initiated
	// and should apply the selected model list to every key regardless of current state.
	tag, err := r.db.Pool.Exec(c.Request.Context(),
		`UPDATE upstream_keys SET models = $1, updated_at = NOW() WHERE provider = $2 AND deleted_at IS NULL AND enabled = TRUE`,
		modelsJSON, req.Provider)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "An internal error occurred"}})
		return
	}

	r.logger.Info("Synced key models from provider",
		zap.String("provider", req.Provider),
		zap.Int("models", len(req.Models)),
		zap.Int64("keys_updated", tag.RowsAffected()),
	)
	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "admin", "key_models_synced", "info",
			fmt.Sprintf("Synced key models for provider %s (%d models, %d keys updated)", req.Provider, len(req.Models), tag.RowsAffected()))
	}

	c.JSON(http.StatusOK, gin.H{
		"success":      true,
		"keys_updated": tag.RowsAffected(),
		"models_count": len(req.Models),
	})
}

// addProviderModelsToService adds selected provider models to service_models table.
// For multi-segment product_ids (e.g. "Pro/moonshotai/Kimi-K2.6"), it generates
// a simplified api_model_name from the last segment (e.g. "kimi-k2.6").
// Capabilities are pre-filled from OpenRouter candidate data when a match is found.
// POST /api/admin/provider-models/add-to-service
func (r *ModelRoutes) addProviderModelsToService(c *gin.Context) {
	var req struct {
		Provider string   `json:"provider" binding:"required"`
		Models   []string `json:"models" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "provider and models required"}})
		return
	}

	added := 0
	skipped := 0
	errs := []string{}

	for _, modelID := range req.Models {
		apiModelName := generateAPIModelName(modelID)

		var exists bool
		_ = r.db.QueryRow(c.Request.Context(),
			`SELECT EXISTS(SELECT 1 FROM service_models WHERE provider = $1 AND product_id = $2)`,
			req.Provider, modelID,
		).Scan(&exists)

		if exists {
			skipped++
			continue
		}

		// Try to pre-fill capabilities from OpenRouter candidate table.
		contextWindow, maxOutputTokens, capabilities := r.matchOpenRouterCapabilities(c.Request.Context(), req.Provider, modelID, apiModelName)

		capsJSON, _ := json.Marshal(capabilities)

		err := r.db.Exec(c.Request.Context(),
			`INSERT INTO service_models (provider, product_id, api_model_name, status, context_window, max_output_tokens, capabilities)
			 VALUES ($1, $2, $3, 'pending', $4, $5, $6)
			 ON CONFLICT (provider, product_id) DO NOTHING`,
			req.Provider, modelID, apiModelName, contextWindow, maxOutputTokens, capsJSON,
		)
		if err != nil {
			errs = append(errs, "Failed to add "+modelID)
			continue
		}

		var reallyAdded bool
		_ = r.db.QueryRow(c.Request.Context(),
			`SELECT EXISTS(SELECT 1 FROM service_models WHERE provider = $1 AND product_id = $2 AND status = 'pending')`,
			req.Provider, modelID,
		).Scan(&reallyAdded)
		if reallyAdded {
			added++
		} else {
			skipped++
		}
	}

	r.logger.Info("Added provider models to service",
		zap.String("provider", req.Provider),
		zap.Int("added", added),
		zap.Int("skipped", skipped),
	)
	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "admin", "provider_models_added_to_service", "info",
			fmt.Sprintf("Added %d provider models to service for %s", added, req.Provider))
	}

	// Auto-sync active service_models to keys for this provider
	go func(p string) {
		if n, err := syncServiceModelsToKeys(r.db, context.Background(), p); err != nil {
			r.logger.Warn("Auto-sync after provider model add failed", zap.String("provider", p), zap.Error(err))
		} else {
			r.logger.Info("Auto-sync after provider model add", zap.String("provider", p), zap.Int64("keys_updated", n))
		}
		r.db.PublishSyncEvent(context.Background())
	}(req.Provider)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"added":   added,
		"skipped": skipped,
		"errors":  errs,
	})
}

// BackfillOpenRouterCapabilities iterates over existing service_models with empty
// capabilities and tries to pre-fill them from the OpenRouter candidate table.
// It is intended to run once at startup (idempotent: rows already filled are
// skipped on the next run).
func (r *ModelRoutes) BackfillOpenRouterCapabilities(ctx context.Context) (updated int, err error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, provider, product_id, COALESCE(api_model_name, '')
		 FROM service_models
		 WHERE deleted_at IS NULL
		   AND (capabilities IS NULL OR capabilities = '{}'::jsonb OR capabilities = 'null'::jsonb)`)
	if err != nil {
		return 0, fmt.Errorf("query service_models for backfill: %w", err)
	}
	defer rows.Close()

	type modelRef struct {
		id           string
		provider     string
		productID    string
		apiModelName string
	}
	var refs []modelRef
	for rows.Next() {
		var id, provider, productID, apiModelName string
		if err := rows.Scan(&id, &provider, &productID, &apiModelName); err != nil {
			continue
		}
		refs = append(refs, modelRef{id: id, provider: provider, productID: productID, apiModelName: apiModelName})
	}

	for _, m := range refs {
		ctxWin, maxOut, caps := r.matchOpenRouterCapabilities(ctx, m.provider, m.productID, m.apiModelName)
		if ctxWin == 0 && maxOut == 0 && len(caps) == 0 {
			continue
		}

		// Ensure top-level context fields live in the JSONB object too.
		if ctxWin > 0 {
			caps["context_window"] = ctxWin
		}
		if maxOut > 0 {
			caps["max_output_tokens"] = maxOut
		}
		capsJSON, _ := json.Marshal(caps)

		_, err := r.db.Pool.Exec(ctx,
			`UPDATE service_models
			 SET capabilities = $1,
			     context_window = CASE WHEN $2 > 0 THEN $2 ELSE context_window END,
			     max_output_tokens = CASE WHEN $3 > 0 THEN $3 ELSE max_output_tokens END,
			     updated_at = NOW()
			 WHERE id = $4`,
			capsJSON, ctxWin, maxOut, m.id)
		if err != nil {
			r.logger.Warn("Failed to backfill capabilities",
				zap.String("model_id", m.id),
				zap.Error(err))
			continue
		}
		updated++
	}

	return updated, nil
}

// matchOpenRouterCapabilities tries to find a matching OpenRouter candidate record
// and derive capabilities. It returns (context_window, max_output_tokens, capabilities_map).
// Matching is best-effort because naming conventions differ between OpenRouter and
// our provider/product_id/api_model_name scheme.
func (r *ModelRoutes) matchOpenRouterCapabilities(ctx context.Context, provider, productID, apiModelName string) (int, int, map[string]interface{}) {
	caps := make(map[string]interface{})

	orProvider := resolveOpenRouterProvider(provider, productID)
	name := apiModelName
	if name == "" {
		name = productID
	}

	// Original prefix embedded in product_id (e.g. "z-ai/glm-5.1" -> "z-ai").
	var originalPrefix string
	if idx := strings.Index(productID, "/"); idx > 0 {
		originalPrefix = productID[:idx]
	}

	// Strategy 1: direct openrouter_id.
	var candidates []string
	if originalPrefix != "" {
		// The product_id itself may already be a valid OpenRouter id.
		candidates = append(candidates, productID)
		candidates = append(candidates, originalPrefix+"/"+name)
	}
	if orProvider != "" {
		candidates = append(candidates, orProvider+"/"+name)
		if !strings.Contains(productID, "/") {
			candidates = append(candidates, orProvider+"/"+productID)
		}
		if idx := strings.LastIndex(productID, "/"); idx >= 0 {
			candidates = append(candidates, orProvider+"/"+productID[idx+1:])
		}
	}

	for _, openrouterID := range candidates {
		var contextWindow, maxOutputTokens int
		var capsRaw []byte
		err := r.db.QueryRow(ctx,
			`SELECT context_window, max_output_tokens, capabilities
			 FROM openrouter_models WHERE openrouter_id = $1`,
			openrouterID,
		).Scan(&contextWindow, &maxOutputTokens, &capsRaw)
		if err == nil {
			if len(capsRaw) > 0 && string(capsRaw) != "{}" && string(capsRaw) != "null" {
				_ = json.Unmarshal(capsRaw, &caps)
			}
			return contextWindow, maxOutputTokens, caps
		}
	}

	// Strategy 2: normalized name fuzzy match.
	normalized := normalizeModelName(name)
	var query string
	var args []interface{}
	if orProvider != "" {
		query = `SELECT openrouter_id, context_window, max_output_tokens, capabilities
				 FROM openrouter_models WHERE provider = $1`
		args = append(args, orProvider)
	} else {
		query = `SELECT openrouter_id, context_window, max_output_tokens, capabilities
				 FROM openrouter_models`
	}
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return 0, 0, caps
	}
	defer rows.Close()

	var bestMatch struct {
		score           int
		contextWindow   int
		maxOutputTokens int
		caps            map[string]interface{}
	}
	for rows.Next() {
		var openrouterID string
		var contextWindow, maxOutputTokens int
		var capsRaw []byte
		if err := rows.Scan(&openrouterID, &contextWindow, &maxOutputTokens, &capsRaw); err != nil {
			continue
		}
		orName := normalizeModelName(openrouterID)
		score := modelNameSimilarity(normalized, orName)
		if score > bestMatch.score {
			bestMatch.score = score
			bestMatch.contextWindow = contextWindow
			bestMatch.maxOutputTokens = maxOutputTokens
			bestMatch.caps = make(map[string]interface{})
			if len(capsRaw) > 0 && string(capsRaw) != "{}" && string(capsRaw) != "null" {
				_ = json.Unmarshal(capsRaw, &bestMatch.caps)
			}
		}
	}
	if bestMatch.score >= 2 {
		return bestMatch.contextWindow, bestMatch.maxOutputTokens, bestMatch.caps
	}

	return 0, 0, caps
}

// normalizeModelName prepares a model name for fuzzy matching.
func normalizeModelName(s string) string {
	s = strings.ToLower(s)
	// Strip common suffixes/prefixes and normalize separators.
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, ".", "")
	s = strings.ReplaceAll(s, "/", "")
	s = strings.ReplaceAll(s, " ", "")
	return s
}

// modelNameSimilarity returns a simple matching score. Higher is better.
func modelNameSimilarity(a, b string) int {
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 100
	}
	if strings.Contains(a, b) || strings.Contains(b, a) {
		return len(minLen(a, b))
	}
	return 0
}

func minLen(a, b string) string {
	if len(a) < len(b) {
		return a
	}
	return b
}

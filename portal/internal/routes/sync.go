package routes

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/mayiants/ai-portal-go/internal/config"
	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/internal/services"
	"github.com/mayiants/ai-portal-go/pkg/crypto"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type SyncRoutes struct {
	db         *db.DB
	cfg        *config.Config
	logger     *zap.Logger
	pricingSvc *services.PricingService
}

func NewSyncRoutes(db *db.DB, cfg *config.Config, logger *zap.Logger, pricingSvc *services.PricingService) *SyncRoutes {
	return &SyncRoutes{db: db, cfg: cfg, logger: logger, pricingSvc: pricingSvc}
}

func (r *SyncRoutes) Register(rg *gin.RouterGroup) {
	rg.GET("/key-pool/version", r.syncKeyPoolVersion)
	rg.GET("/key-pool/sync", r.syncKeyPool)
	rg.GET("/models/sync", r.syncModels)
	rg.GET("/pricing/sync", r.syncPricing)
	rg.GET("/retention/policies/sync", r.syncRetentionPolicies)
}

// requireSyncSecret validates the shared secret used by the Proxy and other
// internal callers. It accepts either the legacy X-Admin-Secret header or the
// newer x-proxy-secret header.
func (r *SyncRoutes) requireSyncSecret(c *gin.Context) bool {
	// Fail-closed: an empty configured secret must never authenticate a
	// request, otherwise any caller (including an unauthenticated one) would
	// match and could fetch upstream key material in plaintext.
	if r.cfg.Portal.SyncSecret == "" {
		r.logger.Error("sync secret is not configured; refusing all sync requests")
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "Unauthorized"}})
		return false
	}
	secret := c.GetHeader("X-Admin-Secret")
	if secret == "" {
		secret = c.GetHeader("x-proxy-secret")
	}
	// Constant-time compare: this secret guards an endpoint that returns
	// DECRYPTED upstream provider keys, so a timing side-channel here is
	// high-value. (webhook_common.go already uses hmac.Equal.)
	if subtle.ConstantTimeCompare([]byte(secret), []byte(r.cfg.Portal.SyncSecret)) != 1 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "Unauthorized"}})
		return false
	}
	return true
}

// SyncKeyResponse is kept as an alias for backwards compatibility.
// The canonical definition now lives in the db package so that PublishSyncEvent
// can compute the same deterministic config_version.
type SyncKeyResponse = db.SyncKeyResponse

// syncKeyPool - Sync upstream keys to proxy
// GET /api/admin/key-pool/sync
func (r *SyncRoutes) syncKeyPool(c *gin.Context) {
	if !r.requireSyncSecret(c) {
		return
	}

	// Get proxy ID for filtering (optional)
	proxyID := c.Query("proxyId")
	region := c.Query("region")

	// Query all enabled upstream keys (include pending for immediate availability)
	rows, err := r.db.Query(c.Request.Context(), `
		SELECT id::text, provider, key_encrypted, key_preview, tier, base_url, models,
		       enabled, assignment_mode, assigned_proxy, rpm_limit, tpm_limit, model_limits, quota_limit, quota_used
		FROM upstream_keys
		WHERE enabled = true AND validation_status IN ('valid', 'pending') AND deleted_at IS NULL
		ORDER BY provider, created_at
	`)
	if err != nil {
		r.logger.Error("Failed to query upstream keys", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database error"}})
		return
	}
	defer rows.Close()

	keys := []SyncKeyResponse{}
	exclusiveCount := 0
	sharedCount := 0

	for rows.Next() {
		var id, provider, keyEncrypted, keyPreview, tier string
		var baseURL *string
		var models interface{}
		var enabled bool
		var assignmentMode string
		var assignedProxy *string
		var rpmLimit, tpmLimit *int
		var modelLimitsRaw interface{}
		var quotaLimit *int64

		var quotaUsed *int64
		if err := rows.Scan(&id, &provider, &keyEncrypted, &keyPreview, &tier, &baseURL, &models,
			&enabled, &assignmentMode, &assignedProxy, &rpmLimit, &tpmLimit, &modelLimitsRaw, &quotaLimit, &quotaUsed); err != nil {
			r.logger.Warn("Failed to scan upstream key", zap.Error(err))
			continue
		}

		// Parse models from JSONB
		var modelList []string
		if models != nil {
			switch v := models.(type) {
			case []byte:
				_ = json.Unmarshal(v, &modelList)
			case string:
				_ = json.Unmarshal([]byte(v), &modelList)
			case []interface{}:
				for _, item := range v {
					if s, ok := item.(string); ok {
						modelList = append(modelList, s)
					}
				}
			}
		}

		// Get rpm/tpm values
		rpmVal := 0
		if rpmLimit != nil {
			rpmVal = *rpmLimit
		}
		tpmVal := 0
		if tpmLimit != nil {
			tpmVal = *tpmLimit
		}
		modelLimits := db.ParseModelLimits(modelLimitsRaw)

		quotaVal := int64(0)
		if quotaLimit != nil {
			quotaVal = *quotaLimit
		}
		quotaUsedVal := int64(0)
		if quotaUsed != nil {
			quotaUsedVal = *quotaUsed
		}

		// Filter by proxy assignment
		if assignmentMode == "exclusive" && assignedProxy != nil {
			if proxyID != "" && *assignedProxy != proxyID {
				continue // Skip keys assigned to other proxies
			}
			exclusiveCount++
		} else {
			sharedCount++
		}

		// Determine priority based on tier
		priority := "normal"
		switch tier {
		case "premium", "enterprise":
			priority = "high"
		case "economy":
			priority = "economy"
		}

		baseURLStr := ""
		if baseURL != nil {
			baseURLStr = *baseURL
		}

		// Decrypt key if encryption is configured
		var actualKey string
		if r.cfg.Auth.EncryptionKey != "" {
			decrypted, err := crypto.Decrypt(keyEncrypted, r.cfg.Auth.EncryptionKey)
			if err != nil {
				r.logger.Warn("Failed to decrypt upstream key, skipping",
					zap.String("key_id", id),
					zap.Error(err))
				continue
			}
			actualKey = decrypted
		} else {
			// No encryption key configured, use as-is
			actualKey = keyEncrypted
		}

		keys = append(keys, SyncKeyResponse{
			ID:             id,
			Provider:       provider,
			Key:            actualKey,
			KeyPreview:     keyPreview,
			Tier:           tier,
			Priority:       priority,
			BaseURL:        baseURLStr,
			Models:         modelList,
			Enabled:        enabled,
			AssignmentMode: assignmentMode,
			IsShared:       assignmentMode != "exclusive",
			RPMLimit:       rpmVal,
			TPMLimit:       tpmVal,
			ModelLimits:    modelLimits,
			QuotaLimit:     quotaVal,
			QuotaUsed:      quotaUsedVal,
		})
	}

	// Get list of providers
	providerRows, err := r.db.Query(c.Request.Context(), `
		SELECT id, name, base_url, enabled
		FROM providers
		WHERE enabled = true
		ORDER BY sort_order, name
	`)
	if err == nil {
		defer providerRows.Close()
	}

	providers := []gin.H{}
	if providerRows != nil {
		for providerRows.Next() {
			var id, name string
			var baseURL *string
			var enabled bool
			if err := providerRows.Scan(&id, &name, &baseURL, &enabled); err == nil {
				baseURLStr := ""
				if baseURL != nil {
					baseURLStr = *baseURL
				}
				providers = append(providers, gin.H{
					"id":      id,
					"name":    name,
					"baseUrl": baseURLStr,
					"enabled": enabled,
				})
			}
		}
	}

	// Get smart routing toggles from system_settings
	smartRouting := map[string]bool{
		"health": true, "tier": true, "rpm": true, "latency": true, "quota": true,
	}
	settingRows, err := r.db.Query(c.Request.Context(),
		`SELECT key, value FROM system_settings WHERE key LIKE 'routing_smart_%'`)
	if err == nil {
		defer settingRows.Close()
		for settingRows.Next() {
			var key, value string
			if settingRows.Scan(&key, &value) == nil {
				if strings.HasPrefix(key, "routing_smart_") {
					ruleName := strings.TrimPrefix(key, "routing_smart_")
					smartRouting[ruleName] = value == "true"
				}
			}
		}
	}

	// Get model provider routing rules
	routingRows, err := r.db.Query(c.Request.Context(), `
		SELECT id::text, model_name, provider, tier, priority, enabled
		FROM model_provider_routing
		WHERE enabled = true
		ORDER BY model_name,
		  CASE tier WHEN 'high' THEN 1 WHEN 'normal' THEN 2 WHEN 'economy' THEN 3 ELSE 4 END,
		  priority, provider
	`)
	modelRouting := []gin.H{}
	if err == nil {
		defer routingRows.Close()
		for routingRows.Next() {
			var id, modelName, provider, tier string
			var priority int
			var enabled bool
			if routingRows.Scan(&id, &modelName, &provider, &tier, &priority, &enabled) == nil {
				modelRouting = append(modelRouting, gin.H{
					"id": id, "model_name": modelName, "provider": provider,
					"tier": tier, "priority": priority, "enabled": enabled,
				})
			}
		}
	}

	modelNameMap := r.buildModelNameMap(c.Request.Context())

	// Compute a deterministic version for the proxy's incremental hot-reload.
	// Use the shared db.ComputeKeyPoolConfigVersion so the version endpoint,
	// sync endpoint, and sync notifications all agree on the same string —
	// inlining a separate md5 here caused drift that made the proxy repeatedly
	// full-sync or never update.
	configVersion, err := r.db.ComputeKeyPoolConfigVersion(c.Request.Context(), proxyID, region)
	if err != nil {
		r.logger.Error("Failed to compute key-pool config version", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database error"}})
		return
	}

	// Incremental sync: if the client already has this version, return 304.
	requestVersion := c.GetHeader("X-Config-Version")
	if requestVersion == "" {
		requestVersion = c.GetHeader("If-None-Match")
	}
	if requestVersion != "" && requestVersion == configVersion {
		c.AbortWithStatus(http.StatusNotModified)
		return
	}

	// Get provider health for smart routing
	healthRows, err := r.db.Query(c.Request.Context(), `
		SELECT provider,
			COALESCE(SUM(request_count), 0),
			COALESCE(SUM(failure_count), 0),
			CASE WHEN COALESCE(SUM(request_count), 0) > 0
			     THEN ROUND(COALESCE(SUM(total_latency_ms), 0)::numeric / SUM(request_count), 0)
			     ELSE 0
			END,
			COALESCE(CASE MAX(CASE status WHEN 'unhealthy' THEN 3 WHEN 'degraded' THEN 2 WHEN 'healthy' THEN 1 ELSE 0 END)
				WHEN 3 THEN 'unhealthy' WHEN 2 THEN 'degraded' WHEN 1 THEN 'healthy' END, 'unknown')
		FROM provider_health_stats
		WHERE bucket >= NOW() - INTERVAL '15 minutes'
		GROUP BY provider`)
	providerHealth := []gin.H{}
	if err == nil {
		defer healthRows.Close()
		for healthRows.Next() {
			var p, status string
			var reqCount, failCount int
			var avgLatency int64
			if healthRows.Scan(&p, &reqCount, &failCount, &avgLatency, &status) == nil {
				errorRate := 0.0
				if reqCount > 0 {
					errorRate = float64(failCount) / float64(reqCount)
				}
				providerHealth = append(providerHealth, gin.H{
					"provider": p, "status": status,
					"request_count": reqCount, "failure_count": failCount,
					"error_rate": errorRate, "avg_latency_ms": avgLatency,
				})
			}
		}
	}

	// Get per-key health for smart routing (today's stats from upstream_key_usage)
	keyHealthRows, err := r.db.Query(c.Request.Context(), `
		SELECT upstream_key_id::text, provider,
			COALESCE(SUM(request_count), 0),
			COALESCE(SUM(error_count), 0),
			CASE WHEN COALESCE(SUM(request_count), 0) > 0
				 THEN ROUND(COALESCE(SUM(total_latency_ms), 0)::numeric / SUM(request_count), 0)
				 ELSE 0
			END
		FROM upstream_key_usage
		WHERE date = CURRENT_DATE
		GROUP BY upstream_key_id, provider`)
	keyHealth := []gin.H{}
	if err == nil {
		defer keyHealthRows.Close()
		for keyHealthRows.Next() {
			var keyID, p string
			var reqCount, failCount int
			var avgLatency int64
			if keyHealthRows.Scan(&keyID, &p, &reqCount, &failCount, &avgLatency) == nil {
				errorRate := 0.0
				if reqCount > 0 {
					errorRate = float64(failCount) / float64(reqCount)
				}
				status := "healthy"
				if errorRate >= 0.80 {
					status = "unhealthy"
				} else if errorRate >= 0.50 {
					status = "degraded"
				}
				keyHealth = append(keyHealth, gin.H{
					"key_id": keyID, "provider": p, "status": status,
					"error_rate": errorRate, "avg_latency_ms": avgLatency,
				})
			}
		}
	}

	r.logger.Info("Key pool sync completed",
		zap.Int("keys", len(keys)),
		zap.Int("exclusive", exclusiveCount),
		zap.Int("shared", sharedCount),
		zap.String("proxyId", proxyID),
		zap.String("region", region),
	)

	resp := gin.H{
		"keys":            keys,
		"providers":       providers,
		"exclusiveCount":  exclusiveCount,
		"sharedCount":     sharedCount,
		"proxyId":         proxyID,
		"region":          region,
		"model_routing":   modelRouting,
		"model_name_map":  modelNameMap,
		"provider_health": providerHealth,
		"key_health":      keyHealth,
		"smart_routing":   smartRouting,
	}

	resp["config_version"] = configVersion
	resp["syncedAt"] = time.Now().Format(time.RFC3339)

	c.JSON(http.StatusOK, resp)
}

// syncKeyPoolVersion returns the current deterministic config version and the
// last sync timestamp. Proxies poll this lightweight endpoint before deciding
// whether to download the full key-pool snapshot.
// GET /api/admin/key-pool/version
func (r *SyncRoutes) syncKeyPoolVersion(c *gin.Context) {
	if !r.requireSyncSecret(c) {
		return
	}

	proxyID := c.Query("proxyId")
	region := c.Query("region")

	configVersion, err := r.db.ComputeKeyPoolConfigVersion(c.Request.Context(), proxyID, region)
	if err != nil {
		r.logger.Error("Failed to compute key-pool config version", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database error"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"config_version": configVersion,
		"synced_at":      time.Now().Format(time.RFC3339),
	})
}

// syncModels - Sync service models to proxy
// GET /api/admin/models/sync
func (r *SyncRoutes) syncModels(c *gin.Context) {
	if !r.requireSyncSecret(c) {
		return
	}

	// Query all servable service models (active + EOL lifecycle)
	rows, err := r.db.Query(c.Request.Context(), `
		SELECT id::text, provider, product_id, api_model_name, status,
		       input_price, output_price,
		       input_price_cache_hit, input_price_cache_miss, reasoning_price,
		       aimodels_data, show_in_store, capabilities, context_window, max_output_tokens
		FROM service_models
		WHERE status IN ('active', 'eol_pending', 'eol')
		ORDER BY provider, sort_order, product_id
	`)
	if err != nil {
		r.logger.Error("Failed to query service models", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database error"}})
		return
	}
	defer rows.Close()

	models := []gin.H{}
	for rows.Next() {
		var id, provider, productID, status string
		var apiModelName *string
		var inputPrice, outputPrice *float64
		var inputPriceCacheHit, inputPriceCacheMiss, reasoningPrice *float64
		var aimodelsData interface{}
		var capabilitiesRaw interface{}
		var contextWindow *int
		var maxOutputTokens *int
		var showInStore bool

		if err := rows.Scan(&id, &provider, &productID, &apiModelName, &status,
			&inputPrice, &outputPrice,
			&inputPriceCacheHit, &inputPriceCacheMiss, &reasoningPrice,
			&aimodelsData, &showInStore, &capabilitiesRaw, &contextWindow, &maxOutputTokens); err != nil {
			continue
		}

		apiModelNameStr := ""
		if apiModelName != nil {
			apiModelNameStr = *apiModelName
		}

		// Normalize capabilities: merge explicit context_window / max_output_tokens
		// columns into the JSONB capabilities sent to the proxy.
		capabilities := r.normalizeCapabilities(capabilitiesRaw, contextWindow, maxOutputTokens)

		models = append(models, gin.H{
			"id":                  id,
			"provider":            provider,
			"productId":           productID,
			"apiModelName":        apiModelNameStr,
			"status":              status,
			"inputPrice":          inputPrice,
			"outputPrice":         outputPrice,
			"inputPriceCacheHit":  inputPriceCacheHit,
			"inputPriceCacheMiss": inputPriceCacheMiss,
			"reasoningPrice":      reasoningPrice,
			"aimodelsData":        aimodelsData,
			"showInStore":         showInStore,
			"capabilities":        capabilities,
		})
	}

	r.logger.Info("Models sync completed", zap.Int("models", len(models)))

	c.JSON(http.StatusOK, gin.H{
		"models":   models,
		"syncedAt": time.Now().Format(time.RFC3339),
	})
}

// buildModelNameMap returns a list of (provider, product_id, api_model_name) tuples
// from all servable service_models. Servable = in-service ('active') plus the
// still-serving EOL lifecycle states ('eol_pending' EOL notice, 'eol' EOL
// service in progress) — mirrors the activeServiceModelsCache definition.
// The proxy uses this to translate user-facing api_model_name to
// provider-specific product_id when forwarding requests.
func (r *SyncRoutes) buildModelNameMap(ctx context.Context) []gin.H {
	rows, err := r.db.Query(ctx, `
		SELECT provider, product_id, api_model_name, capabilities
		FROM service_models
		WHERE status IN ('active', 'eol_pending', 'eol') AND api_model_name IS NOT NULL AND api_model_name != ''
		ORDER BY provider, api_model_name
	`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var result []gin.H
	for rows.Next() {
		var provider, productID, apiModelName string
		var capsRaw []byte
		if rows.Scan(&provider, &productID, &apiModelName, &capsRaw) == nil {
			entry := gin.H{
				"provider":       provider,
				"product_id":     productID,
				"api_model_name": apiModelName,
			}
			// model_type lets the proxy reject wrong-endpoint calls (e.g. a
			// video model on /v1/chat/completions) before dispatching. The
			// capabilities map itself stays typed for the /models/sync view.
			var capsAny map[string]interface{}
			if json.Unmarshal(capsRaw, &capsAny) == nil {
				if t, ok := capsAny["type"].(string); ok && t != "" {
					entry["model_type"] = t
				}
			}
			if len(capsRaw) > 0 && string(capsRaw) != "{}" && string(capsRaw) != "null" {
				var caps map[string]bool
				if json.Unmarshal(capsRaw, &caps) == nil {
					entry["capabilities"] = caps
				}
			}
			result = append(result, entry)
		}
	}
	return result
}

// normalizeCapabilities merges the explicit context_window / max_output_tokens
// columns into the capabilities JSONB so the proxy receives a single,
// self-describing object. Any fields already present in the JSONB are preserved.
func (r *SyncRoutes) normalizeCapabilities(raw interface{}, contextWindow, maxOutputTokens *int) map[string]interface{} {
	caps := make(map[string]interface{})
	if raw != nil {
		switch v := raw.(type) {
		case []byte:
			if len(v) > 0 && string(v) != "{}" && string(v) != "null" {
				_ = json.Unmarshal(v, &caps)
			}
		case string:
			if v != "" && v != "{}" && v != "null" {
				_ = json.Unmarshal([]byte(v), &caps)
			}
		case map[string]interface{}:
			for k, val := range v {
				caps[k] = val
			}
		}
	}
	if contextWindow != nil && *contextWindow > 0 {
		caps["context_window"] = *contextWindow
	}
	if maxOutputTokens != nil && *maxOutputTokens > 0 {
		caps["max_output_tokens"] = *maxOutputTokens
	}
	return caps
}

// syncPricing returns the simplified pricing view used by the Proxy to pre-charge
// quota before forwarding requests. This endpoint is intentionally NOT protected
// by admin JWT — it is called by the Proxy using the shared x-proxy-secret, the
// same mechanism used for /key-pool/sync and /models/sync.
// GET /api/admin/pricing/sync
func (r *SyncRoutes) syncPricing(c *gin.Context) {
	if !r.requireSyncSecret(c) {
		return
	}

	views, err := r.pricingSvc.SyncViewForProxy(c.Request.Context())
	if err != nil {
		r.logger.Error("failed to build pricing sync view", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to build pricing sync view"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"pricing":  views,
		"syncedAt": time.Now().UTC().Format(time.RFC3339),
	})
}

// syncRetentionPolicies publishes the retention windows currently in force so
// the Proxy's cleanup follows policy changes for data already on disk (the
// <N>d directory a file was written under is only a fallback there).
// GET /api/admin/retention/policies/sync
func (r *SyncRoutes) syncRetentionPolicies(c *gin.Context) {
	if !r.requireSyncSecret(c) {
		return
	}
	ctx := c.Request.Context()

	_, platformDays := loadPlatformRetentionDefaults(ctx, r.db)

	rows, err := r.db.Query(ctx,
		`SELECT id::text, retention_days FROM enterprises WHERE deleted_at IS NULL`)
	if err != nil {
		r.logger.Error("failed to load enterprise retention policies", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to load retention policies"}})
		return
	}
	defer rows.Close()

	type entPolicy struct {
		ID   string `json:"id"`
		Days int    `json:"days"`
	}
	ents := make([]entPolicy, 0)
	for rows.Next() {
		var e entPolicy
		if err := rows.Scan(&e.ID, &e.Days); err != nil {
			r.logger.Error("failed to scan enterprise retention policy", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to load retention policies"}})
			return
		}
		ents = append(ents, e)
	}
	if err := rows.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to load retention policies"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"platform":    gin.H{"days": platformDays},
		"enterprises": ents,
		"syncedAt":    time.Now().UTC().Format(time.RFC3339),
	})
}

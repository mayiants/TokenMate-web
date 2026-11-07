package server

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mayiants/ai-proxy-go/internal/audit"
	"github.com/mayiants/ai-proxy-go/pkg/keypool"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// validateBaseURL checks if a URL is safe to use (prevents SSRF).
// Validates both literal IPs and resolves hostnames to catch DNS-based bypasses.
func validateBaseURL(baseURL string) error {
	if baseURL == "" {
		return nil
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("URL scheme must be http or https")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL must have a host")
	}
	if host == "localhost" || host == "127.0.0.1" || host == "0.0.0.0" || host == "::1" {
		return fmt.Errorf("localhost is not allowed")
	}
	if strings.HasPrefix(host, "169.254.") {
		return fmt.Errorf("metadata service is not allowed")
	}
	isPrivateIP := func(ip net.IP) bool {
		return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsPrivate()
	}
	// Check literal IP
	if ip := net.ParseIP(host); ip != nil {
		if isPrivateIP(ip) {
			return fmt.Errorf("private IP is not allowed")
		}
		return nil
	}
	// Resolve hostname to catch DNS-based bypasses (e.g. internal.corp → 10.0.0.1)
	addrs, err := net.LookupHost(host)
	if err != nil {
		return fmt.Errorf("unable to resolve host: %w", err)
	}
	for _, addr := range addrs {
		if ip := net.ParseIP(addr); ip != nil && isPrivateIP(ip) {
			return fmt.Errorf("host resolves to private IP, not allowed")
		}
	}
	return nil
}

// handleGetStats - Get system statistics
func (s *HandlerService) handleGetStats(c *gin.Context) {
	stats := gin.H{
		"timestamp": time.Now().Unix(),
		"version":   "1.0.0",
	}

	if s.handlerDeps.AffinityCache != nil {
		stats["cache"] = s.handlerDeps.AffinityCache.Stats()
	}

	c.JSON(http.StatusOK, stats)
}

// handleGetAffinityStats - Get channel affinity cache statistics
func (s *HandlerService) handleGetAffinityStats(c *gin.Context) {
	if s.handlerDeps.AffinityCache == nil {
		c.JSON(http.StatusOK, gin.H{"enabled": false})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"enabled": true,
		"stats":   s.handlerDeps.AffinityCache.Stats(),
	})
}

// handleClearAffinityCache - Clear channel affinity cache
func (s *HandlerService) handleClearAffinityCache(c *gin.Context) {
	if s.handlerDeps.AffinityCache == nil {
		c.JSON(http.StatusOK, gin.H{"message": "Cache not enabled"})
		return
	}

	s.handlerDeps.AffinityCache.Clear()
	c.JSON(http.StatusOK, gin.H{"message": "Cache cleared"})
}

// handleAuditSearch - Search audit logs
func (s *HandlerService) handleAuditSearch(c *gin.Context) {
	if s.handlerDeps.AuditLogger == nil {
		c.JSON(http.StatusOK, gin.H{"results": []interface{}{}})
		return
	}

	// Parse query parameters
	query := audit.AuditQuery{
		KeyID:    c.Query("key_id"),
		UserID:   c.Query("user_id"),
		Provider: c.Query("provider"),
		Model:    c.Query("model"),
	}

	if startStr := c.Query("start"); startStr != "" {
		if t, err := time.Parse(time.RFC3339, startStr); err == nil {
			query.Start = t
		}
	}

	if endStr := c.Query("end"); endStr != "" {
		if t, err := time.Parse(time.RFC3339, endStr); err == nil {
			query.End = t
		}
	}

	if limitStr := c.Query("limit"); limitStr != "" {
		_, _ = fmt.Sscanf(limitStr, "%d", &query.Limit)
	}
	if query.Limit == 0 {
		query.Limit = 100
	}

	results, err := s.handlerDeps.AuditLogger.Search(query)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"results": results})
}

// handleGetKeyHistory returns history for a specific key
// GET /admin/keys/:provider/:keyId/history
func (s *HandlerService) handleGetKeyHistory(c *gin.Context) {
	provider := c.Param("provider")
	keyID := c.Param("keyId")

	// For now, return empty history (would need time-series storage for actual data)
	c.JSON(http.StatusOK, gin.H{
		"history":  []interface{}{},
		"provider": provider,
		"keyId":    keyID,
	})
}

// POST /admin/keys
func (s *HandlerService) handleAddKey(c *gin.Context) {
	var req struct {
		Provider     string   `json:"provider"`
		Key          string   `json:"key"`
		Tier         string   `json:"tier"`
		Notes        string   `json:"notes"`
		BaseURL      string   `json:"baseUrl"`
		Models       []string `json:"models"`
		KeyID        string   `json:"keyId"`
		EconomyTiers []string `json:"economyTiers"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "Invalid request: " + err.Error(),
		}})
		return
	}

	if s.keyPoolManager == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"message": "Key pool manager not initialized",
		}})
		return
	}

	// SSRF protection: validate BaseURL
	if err := validateBaseURL(req.BaseURL); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "Invalid baseUrl: " + err.Error(),
		}})
		return
	}

	// Create key preview
	keyPreview := ""
	if len(req.Key) > 12 {
		keyPreview = req.Key[:8] + "..." + req.Key[len(req.Key)-4:]
	} else if len(req.Key) > 4 {
		keyPreview = req.Key[:4] + "..."
	}

	key := &keypool.Key{
		KeyStatic: keypool.KeyStatic{
			ID:             req.KeyID,
			Provider:       req.Provider,
			Key:            req.Key,
			KeyPreview:     keyPreview,
			Tier:           req.Tier,
			BaseURL:        req.BaseURL,
			Models:         req.Models,
			Notes:          req.Notes,
			EconomyTiers:   req.EconomyTiers,
			AssignmentMode: "shared",
			IsShared:       true,
		},
	}

	if err := s.keyPoolManager.AddKey(key); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"message": "Failed to add key: " + err.Error(),
		}})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"message": "Key added successfully",
		"keyId":   req.KeyID,
	})
}

// DELETE /admin/keys/:provider/:keyId
func (s *HandlerService) handleDeleteKey(c *gin.Context) {
	provider := c.Param("provider")
	keyID := c.Param("keyId")

	if s.keyPoolManager == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"message": "Key pool manager not initialized",
		}})
		return
	}

	deleted := s.keyPoolManager.RemoveKey(provider, keyID)

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"deleted": deleted,
	})
}

// PATCH /admin/keys/:provider/:keyId
func (s *HandlerService) handleToggleKey(c *gin.Context) {
	provider := c.Param("provider")
	keyID := c.Param("keyId")

	var req struct {
		Enabled bool `json:"enabled"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "Invalid request: " + err.Error(),
		}})
		return
	}

	if s.keyPoolManager == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"message": "Key pool manager not initialized",
		}})
		return
	}

	updated := s.keyPoolManager.ToggleKey(provider, keyID, req.Enabled)

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"updated": updated,
	})
}

// POST /admin/keys/:provider/:keyId/reset-runtime
func (s *HandlerService) handleResetKeyRuntime(c *gin.Context) {
	provider := c.Param("provider")
	keyID := c.Param("keyId")

	if s.keyPoolManager == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"message": "Key pool manager not initialized",
		}})
		return
	}

	if !s.keyPoolManager.ResetKeyRuntimeState(provider, keyID) {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{
			"message": "Key not found for provider",
		}})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":       true,
		"reset":    keyID,
		"provider": provider,
	})
}

// GET /admin/keypool/selection-state?provider=<name>
// Read-only dump of every runtime signal that gates key selection (cooldowns,
// failure streaks, RPM/TPM windows, model-level state, live health tier).
// Built for routing forensics after the 2026-10-28 incident where a provider
// silently served zero traffic for 32h and only a restart recovered it.
func (s *HandlerService) handleGetSelectionState(c *gin.Context) {
	if s.keyPoolManager == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"message": "Key pool manager not initialized",
		}})
		return
	}

	provider := c.Query("provider")
	states := s.keyPoolManager.DumpSelectionState(provider)
	c.JSON(http.StatusOK, gin.H{
		"providers":        states,
		"count":            len(states),
		"circuit_breakers": providerBreakers.Status(),
	})
}

// POST /admin/circuit-breakers/:provider/reset
// Force-closes a provider's circuit breaker. Recovery without a proxy
// restart when a breaker got stuck open (e.g. transport wedge where every
// half-open probe dies before reaching the upstream — see the 2026-11
// tokenrouter 31h silence). If the provider is still failing, the breaker
// re-trips immediately and the logs show it.
func (s *HandlerService) handleResetCircuitBreaker(c *gin.Context) {
	provider := c.Param("provider")
	if provider == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "provider is required"}})
		return
	}
	providerBreakers.Reset(provider)
	s.logger.Info("circuit breaker reset via admin API",
		zap.String("provider", provider))
	c.JSON(http.StatusOK, gin.H{
		"success":          true,
		"provider":         provider,
		"circuit_breakers": providerBreakers.Status(),
	})
}

// POST /admin/keys/import
func (s *HandlerService) handleImportKeys(c *gin.Context) {
	var req struct {
		Provider string                   `json:"provider"`
		Keys     []map[string]interface{} `json:"keys"`
		Tier     string                   `json:"tier"`
		BaseURL  string                   `json:"baseUrl"`
		Models   []string                 `json:"models"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "Invalid request: " + err.Error(),
		}})
		return
	}

	if s.keyPoolManager == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"message": "Key pool manager not initialized",
		}})
		return
	}

	// SSRF protection: validate BaseURL
	if err := validateBaseURL(req.BaseURL); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "Invalid baseUrl: " + err.Error(),
		}})
		return
	}

	added := 0
	for _, keyData := range req.Keys {
		keyStr, ok := keyData["key"].(string)
		if !ok {
			continue
		}

		economyTiers := []string{}
		// JSON arrays decode as []interface{}, not []string, so assert
		// each element individually to avoid silently losing economyTiers.
		if et, ok := keyData["economyTiers"].([]interface{}); ok {
			for _, e := range et {
				if s, ok := e.(string); ok {
					economyTiers = append(economyTiers, s)
				}
			}
		}

		keyPreview := ""
		if len(keyStr) > 12 {
			keyPreview = keyStr[:8] + "..." + keyStr[len(keyStr)-4:]
		}

		key := &keypool.Key{
			KeyStatic: keypool.KeyStatic{
				Provider:       req.Provider,
				Key:            keyStr,
				KeyPreview:     keyPreview,
				Tier:           req.Tier,
				BaseURL:        req.BaseURL,
				Models:         req.Models,
				EconomyTiers:   economyTiers,
				AssignmentMode: "shared",
				IsShared:       true,
			},
		}

		if err := s.keyPoolManager.AddKey(key); err == nil {
			added++
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":    true,
		"added": added,
		"total": len(req.Keys),
	})
}

// POST /api/webhook/usage
func (s *HandlerService) handleUsageReport(c *gin.Context) {
	var req struct {
		TraceID      string  `json:"traceId"`
		APIKeyHash   string  `json:"apiKeyHash"`
		Model        string  `json:"model"`
		Provider     string  `json:"provider"`
		InputTokens  int     `json:"inputTokens"`
		OutputTokens int     `json:"outputTokens"`
		Cost         float64 `json:"cost"`
		LatencyMs    int     `json:"latencyMs"`
		Timestamp    string  `json:"timestamp"`
		KeyID        string  `json:"keyId"`
		ServerID     string  `json:"serverId"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request body"}})
		return
	}

	// Process usage report (would store in database)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// POST /api/webhook/usage-batch
func (s *HandlerService) handleBatchUsageReport(c *gin.Context) {
	var req struct {
		Records  []interface{} `json:"records"`
		ServerID string        `json:"serverId"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request body"}})
		return
	}

	// Process batch usage report
	c.JSON(http.StatusOK, gin.H{
		"ok":        true,
		"processed": len(req.Records),
	})
}

// POST /api/webhook/key-pool-status
func (s *HandlerService) handleKeyPoolStatus(c *gin.Context) {
	var req struct {
		Provider string `json:"provider"`
		Keys     []struct {
			ID      string `json:"id"`
			Preview string `json:"preview"`
			RPM     int    `json:"rpm"`
			TPM     int    `json:"tpm"`
			Status  string `json:"status"`
		} `json:"keys"`
		Timestamp string `json:"timestamp"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request body"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// POST /api/webhook/sync
// Triggered by Portal to notify this Proxy that key/model/pricing config changed.
func (s *HandlerService) handleWebhookSync(c *gin.Context) {
	if s.keyPoolManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "Key pool manager not initialized"}})
		return
	}

	var payload struct {
		ConfigVersion string `json:"config_version"`
	}
	// Best-effort parse: an empty/missing config_version falls back to full sync.
	_ = c.ShouldBindJSON(&payload)

	if payload.ConfigVersion != "" && payload.ConfigVersion == s.keyPoolManager.GetConfigVersion() {
		s.logger.Debug("Webhook sync skipped: config version unchanged",
			zap.String("config_version", payload.ConfigVersion))
		c.JSON(http.StatusOK, gin.H{"ok": true, "skipped": true})
		return
	}

	go func() {
		if err := s.keyPoolManager.SyncFromPortalWithContext(context.Background()); err != nil {
			s.logger.Warn("Webhook sync failed", zap.Error(err))
		}
	}()

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// POST /api/webhook/balance-update
// Triggered by Portal to invalidate this Proxy's local balance cache for a user.
func (s *HandlerService) handleWebhookBalanceUpdate(c *gin.Context) {
	var req struct {
		Type      string  `json:"type"`
		UserID    string  `json:"user_id"`
		Balance   float64 `json:"balance"`
		Threshold float64 `json:"threshold"`
		KeyHash   string  `json:"key_hash"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request body"}})
		return
	}

	if s.authMiddleware == nil {
		c.JSON(http.StatusOK, gin.H{"ok": true})
		return
	}

	// This endpoint is the cross-host (HTTPS) sibling of the Redis pub/sub
	// subscriber: Portal Redis and Proxy Redis are separate instances across
	// EC2s, so revocation/balance events reach this Proxy via this callback.
	switch req.Type {
	case "balance_update":
		if req.UserID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Missing user_id"}})
			return
		}
		if req.Balance <= 0 {
			s.authMiddleware.SetBalance(req.UserID, 0)
		} else {
			s.authMiddleware.InvalidateBalance(req.UserID)
		}
	case "key_revoked":
		if req.KeyHash == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Missing key_hash"}})
			return
		}
		s.authMiddleware.InvalidateKeyHash(req.KeyHash)
	case "user_banned":
		if req.UserID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Missing user_id"}})
			return
		}
		s.authMiddleware.InvalidateUser(req.UserID)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Unknown type"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// GET /api/admin/key-pool/sync
func (s *HandlerService) handleSyncKeys(c *gin.Context) {
	proxySecret := c.GetHeader("x-proxy-secret")
	if proxySecret == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "Missing x-proxy-secret header"}})
		return
	}

	if s.cfg == nil || s.cfg.Portal.SyncSecret == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
			"message": "Server not configured (SyncSecret missing)",
			"type":    "server_error",
		}})
		return
	}
	if subtle.ConstantTimeCompare([]byte(proxySecret), []byte(s.cfg.Portal.SyncSecret)) != 1 {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{
			"message": "Invalid proxy secret",
			"type":    "permission_error",
			"code":    "INVALID_SECRET",
		}})
		return
	}

	proxyID := c.GetHeader("x-proxy-id")
	if proxyID == "" {
		proxyID = c.Query("proxy_id")
	}
	region := c.GetHeader("x-proxy-region")
	if region == "" {
		region = c.Query("region")
	}

	if s.keyPoolManager == nil {
		c.JSON(http.StatusOK, gin.H{
			"proxyId":        proxyID,
			"region":         region,
			"keys":           []interface{}{},
			"providers":      []interface{}{},
			"exclusiveCount": 0,
			"sharedCount":    0,
			"syncedAt":       time.Now().Format(time.RFC3339),
		})
		return
	}

	// Trigger sync from Portal before returning
	if syncErr := s.keyPoolManager.SyncFromPortalWithContext(c.Request.Context()); syncErr != nil {
		c.JSON(http.StatusOK, gin.H{
			"proxyId":        proxyID,
			"region":         region,
			"keys":           []interface{}{},
			"providers":      []interface{}{},
			"exclusiveCount": 0,
			"sharedCount":    0,
			"syncedAt":       time.Now().Format(time.RFC3339),
			"syncError":      syncErr.Error(),
		})
		return
	}

	stats := s.keyPoolManager.GetAllStats()
	var allKeys []keypool.Key
	exclusiveCount := 0
	sharedCount := 0

	for _, pool := range stats {
		for _, key := range pool.Keys {
			allKeys = append(allKeys, *key)
			if key.IsShared {
				sharedCount++
			} else {
				exclusiveCount++
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"proxyId":        proxyID,
		"region":         region,
		"keys":           allKeys,
		"providers":      []interface{}{},
		"exclusiveCount": exclusiveCount,
		"sharedCount":    sharedCount,
		"configVersion":  s.keyPoolManager.GetConfigVersion(),
		"syncedAt":       time.Now().Format(time.RFC3339),
	})
}

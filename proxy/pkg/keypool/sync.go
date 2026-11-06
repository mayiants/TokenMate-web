package keypool

import (
	"context"
	"encoding/json"
	"fmt"
	mathrand "math/rand"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ai-proxy/ai-proxy-go/pkg/telemetry"
	"go.uber.org/zap"
)

// SyncKey represents a key received from Portal sync (includes actual key value)
type SyncKey struct {
	ID             string                `json:"id"`
	Provider       string                `json:"provider"`
	Key            string                `json:"key"` // Actual key value from sync
	KeyPreview     string                `json:"key_preview"`
	Tier           string                `json:"tier"`
	Priority       string                `json:"priority"` // "high", "normal", "economy"
	BaseURL        string                `json:"baseUrl,omitempty"`
	Models         []string              `json:"models,omitempty"`
	Notes          string                `json:"notes,omitempty"`
	Enabled        bool                  `json:"enabled"`
	AssignmentMode string                `json:"assignmentMode"`
	IsShared       bool                  `json:"isShared"`
	EconomyTiers   []string              `json:"economyTiers,omitempty"`
	RPMLimit       int                   `json:"rpm_limit,omitempty"`
	TPMLimit       int                   `json:"tpm_limit,omitempty"`
	ModelLimits    map[string]ModelLimit `json:"model_limits,omitempty"`
	QuotaLimit     int64                 `json:"quota_limit,omitempty"`
	QuotaUsed      int64                 `json:"quota_used,omitempty"`
}

// SyncProvider represents a provider configuration from Portal sync
type SyncProvider struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	BaseURL string `json:"baseUrl"`
	Enabled bool   `json:"enabled"`
}

// ModelRoutingRule represents an admin-configured provider fallback order for a model
type ModelRoutingRule struct {
	ModelName string `json:"model_name"`
	Provider  string `json:"provider"`
	Tier      string `json:"tier"`
	Priority  int    `json:"priority"`
	Enabled   bool   `json:"enabled"`
}

// ModelNameMapEntry represents a (provider, api_model_name) → product_id mapping
type ModelNameMapEntry struct {
	Provider     string          `json:"provider"`
	ProductID    string          `json:"product_id"`
	APIModelName string          `json:"api_model_name"`
	ModelType    string          `json:"model_type,omitempty"` // "text", "image", "video", "audio"
	Capabilities map[string]bool `json:"capabilities,omitempty"`
}

// ProviderHealthEntry represents per-provider health from Portal sync
type ProviderHealthEntry struct {
	Provider     string  `json:"provider"`
	Status       string  `json:"status"`
	ErrorRate    float64 `json:"error_rate"`
	AvgLatencyMs int64   `json:"avg_latency_ms"`
}

// syncBackoffDelay returns the next backoff duration with jitter, capped at syncInterval.
func (m *Manager) syncBackoffDelay() time.Duration {
	syncInterval := time.Duration(atomic.LoadInt64(&m.syncInterval))
	if syncInterval == 0 {
		syncInterval = 60 * time.Second
		atomic.StoreInt64(&m.syncInterval, int64(syncInterval))
	}
	base := time.Duration(1<<min(m.syncConsecutiveFailures, 6)) * time.Second
	if base > syncInterval {
		base = syncInterval
	}
	jitter := time.Duration(float64(base) * 0.2)
	return base + time.Duration(float64(jitter)*mathrand.Float64())
}

// recordSyncSuccess resets sync failure backoff.
func (m *Manager) recordSyncSuccess() {
	m.syncBackoffMu.Lock()
	m.syncConsecutiveFailures = 0
	m.syncNextAllowed = time.Time{}
	m.syncBackoffMu.Unlock()
}

// recordSyncFailure increments consecutive failures and sets next allowed sync time.
func (m *Manager) recordSyncFailure() {
	m.syncBackoffMu.Lock()
	m.syncConsecutiveFailures++
	m.syncNextAllowed = time.Now().Add(m.syncBackoffDelay())
	m.syncBackoffMu.Unlock()
}

// syncAllowed returns true if enough backoff time has passed since the last failure.
func (m *Manager) syncAllowed() bool {
	m.syncBackoffMu.Lock()
	defer m.syncBackoffMu.Unlock()
	return time.Now().After(m.syncNextAllowed)
}

// ProviderHealthInfo holds per-provider health metrics for smart routing
type ProviderHealthInfo struct {
	Status       string  `json:"status"`
	ErrorRate    float64 `json:"error_rate"`
	AvgLatencyMs int64   `json:"avg_latency_ms"`
}

// KeyHealthInfo holds per-key health metrics for smart routing
type KeyHealthInfo struct {
	Status       string  `json:"status"`
	ErrorRate    float64 `json:"error_rate"`
	AvgLatencyMs int64   `json:"avg_latency_ms"`
}

// KeyHealthEntry represents per-key health data received from Portal sync
type KeyHealthEntry struct {
	KeyID        string  `json:"key_id"`
	Provider     string  `json:"provider"`
	Status       string  `json:"status"`
	ErrorRate    float64 `json:"error_rate"`
	AvgLatencyMs int64   `json:"avg_latency_ms"`
}

// SyncVersionResult is the lightweight response from /key-pool/version.
type SyncVersionResult struct {
	ConfigVersion string `json:"config_version"`
	SyncedAt      string `json:"synced_at"`
}

// SyncVersionFromPortal polls the Portal version endpoint and returns the
// current config version. It returns an error if the request fails or the
// response cannot be decoded.
func (m *Manager) SyncVersionFromPortal(ctx context.Context) (SyncVersionResult, error) {
	var result SyncVersionResult
	if m.portalURL == "" || m.portalSecret == "" {
		return result, fmt.Errorf("portal URL or secret not configured")
	}

	url := m.portalURL + "/api/admin/key-pool/version"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return result, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("x-proxy-secret", m.portalSecret)
	req.Header.Set("x-proxy-id", m.getProxyID())
	req.Header.Set("x-proxy-region", m.getProxyRegion())
	telemetry.InjectTraceparent(ctx, req)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return result, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return result, fmt.Errorf("portal returned status %d", resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return result, fmt.Errorf("decode response: %w", err)
	}
	return result, nil
}

// SyncFromPortal syncs keys from the Portal with context support
func (m *Manager) SyncFromPortal() error {
	return m.SyncFromPortalWithContext(context.Background())
}

// SyncFromPortalWithContext syncs keys from the Portal with context
func (m *Manager) SyncFromPortalWithContext(ctx context.Context) (err error) {
	defer func() {
		if err != nil {
			m.recordSyncFailure()
		} else {
			m.recordSyncSuccess()
		}
	}()

	if m.portalURL == "" || m.portalSecret == "" {
		m.logger.Warn("Portal URL or secret not configured, skipping sync")
		return nil
	}

	// Warn if the portal URL does not use HTTPS. The sync response contains all
	// upstream real API keys, so an unencrypted transport is a security risk.
	if !strings.HasPrefix(m.portalURL, "https://") {
		m.logger.Warn("Portal URL does not use HTTPS; upstream keys are at risk in transit",
			zap.String("portal_url", m.portalURL))
	}

	url := m.portalURL + "/api/admin/key-pool/sync"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("x-proxy-secret", m.portalSecret)
	req.Header.Set("x-proxy-id", m.getProxyID())
	req.Header.Set("x-proxy-region", m.getProxyRegion())
	telemetry.InjectTraceparent(ctx, req)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return fmt.Errorf("portal returned status %d", resp.StatusCode)
	}

	var result struct {
		Keys           []SyncKey             `json:"keys"`
		Providers      []SyncProvider        `json:"providers"`
		ModelRouting   []ModelRoutingRule    `json:"model_routing"`
		ModelNameMap   []ModelNameMapEntry   `json:"model_name_map"`
		ProviderHealth []ProviderHealthEntry `json:"provider_health"`
		KeyHealth      []KeyHealthEntry      `json:"key_health"`
		SmartRouting   map[string]bool       `json:"smart_routing"`
		ConfigVersion  string                `json:"config_version"`
	}

	// ... (decoding omitted for brevity, same as before)

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	// Incremental hot-reload: skip applying the full snapshot when the portal
	// reports the same config version we already have.
	if result.ConfigVersion != "" && result.ConfigVersion == m.GetConfigVersion() {
		m.setLastSync(time.Now())
		m.logger.Debug("Portal config unchanged, skipping sync",
			zap.String("config_version", result.ConfigVersion),
		)
		return nil
	}

	// Update smart routing toggles from sync
	if len(result.SmartRouting) > 0 {
		m.smartMu.Lock()
		m.smartRouting = result.SmartRouting
		m.smartMu.Unlock()
	}

	// Update provider health from sync
	if len(result.ProviderHealth) > 0 {
		healthMap := make(map[string]ProviderHealthInfo, len(result.ProviderHealth))
		for _, h := range result.ProviderHealth {
			healthMap[h.Provider] = ProviderHealthInfo{
				Status:       h.Status,
				ErrorRate:    h.ErrorRate,
				AvgLatencyMs: h.AvgLatencyMs,
			}
		}
		m.healthMu.Lock()
		m.providerHealth = healthMap
		m.healthMu.Unlock()
	}

	// Update per-key health from sync
	if len(result.KeyHealth) > 0 {
		khMap := make(map[string]KeyHealthInfo, len(result.KeyHealth))
		for _, h := range result.KeyHealth {
			k := h.Provider + ":" + h.KeyID
			khMap[k] = KeyHealthInfo{
				Status:       h.Status,
				ErrorRate:    h.ErrorRate,
				AvgLatencyMs: h.AvgLatencyMs,
			}
		}
		m.keyHealthMu.Lock()
		m.keyHealth = khMap
		m.keyHealthMu.Unlock()
	}

	// Update provider configs from sync
	if len(result.Providers) > 0 {
		m.updateProviderConfigs(result.Providers)
	}

	// Convert SyncKey to Key (static fields only; runtime state merged later)
	keys := make([]Key, len(result.Keys))
	for i, sk := range result.Keys {
		keys[i] = Key{
			KeyStatic: KeyStatic{
				ID:             sk.ID,
				Provider:       sk.Provider,
				Key:            sk.Key, // Preserve actual key value
				KeyPreview:     sk.KeyPreview,
				Tier:           sk.Tier,
				Priority:       sk.Priority,
				BaseURL:        sk.BaseURL,
				Models:         sk.Models,
				Notes:          sk.Notes,
				Enabled:        sk.Enabled,
				AssignmentMode: sk.AssignmentMode,
				IsShared:       sk.IsShared,
				EconomyTiers:   sk.EconomyTiers,
				RPMLimit:       sk.RPMLimit,
				TPMLimit:       sk.TPMLimit,
				ModelLimits:    sk.ModelLimits,
				QuotaLimit:     sk.QuotaLimit,
			},
		}
	}

	// Update pools with new keys
	m.updatePoolsFromSync(keys)

	// Update model routing rules from sync
	m.updateModelRouting(result.ModelRouting)

	// Update model name map from sync
	m.updateModelNameMap(result.ModelNameMap)

	// Persist to Redis
	if m.redis != nil {
		if err := m.persistToRedis(ctx); err != nil {
			m.logger.Warn("Failed to persist keys to Redis", zap.Error(err))
		}
	}

	m.setConfigVersion(result.ConfigVersion)
	m.setLastSync(time.Now())
	m.logger.Info("Synced keys from portal",
		zap.Int("total_keys", len(result.Keys)),
		zap.Int("providers", len(result.Providers)),
		zap.String("config_version", result.ConfigVersion),
	)

	return nil
}

// updateProviderConfigs updates runtime provider configs from Portal sync
func (m *Manager) updateProviderConfigs(providers []SyncProvider) {
	m.providerConfigsMu.Lock()
	defer m.providerConfigsMu.Unlock()

	// Only update providers that have a non-empty base URL
	for _, p := range providers {
		if p.BaseURL != "" {
			m.providerConfigs[p.ID] = ProviderConfigEntry{
				BaseURL: p.BaseURL,
			}
			m.logger.Debug("Updated provider config from sync",
				zap.String("provider", p.ID),
				zap.String("base_url", p.BaseURL),
			)
		}
	}
}

// updateModelRouting rebuilds the model→provider order map from sync data
func (m *Manager) updateModelRouting(rules []ModelRoutingRule) {
	m.routingMu.Lock()
	defer m.routingMu.Unlock()

	routing := make(map[string][]string)
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		routing[rule.ModelName] = append(routing[rule.ModelName], rule.Provider)
	}
	m.modelRouting = routing
}

// GetRoutingOrder returns the admin-configured provider order for a model.
// Returns nil if no rules configured (caller should fall back to default behavior).
func (m *Manager) GetRoutingOrder(model string) []string {
	m.routingMu.RLock()
	defer m.routingMu.RUnlock()

	if m.modelRouting == nil {
		return nil
	}
	providers, ok := m.modelRouting[model]
	if !ok {
		return nil
	}
	result := make([]string, len(providers))
	copy(result, providers)
	return result
}

// updateModelNameMap rebuilds the provider→api_model_name→product_id map from sync data
func (m *Manager) updateModelNameMap(entries []ModelNameMapEntry) {
	m.modelNameMapMu.Lock()
	defer m.modelNameMapMu.Unlock()

	mm := make(map[string]map[string]string)
	mc := make(map[string]map[string]map[string]bool)
	mt := make(map[string]map[string]string)
	for _, e := range entries {
		if mm[e.Provider] == nil {
			mm[e.Provider] = make(map[string]string)
			mc[e.Provider] = make(map[string]map[string]bool)
			mt[e.Provider] = make(map[string]string)
		}
		mm[e.Provider][e.APIModelName] = e.ProductID
		if len(e.Capabilities) > 0 {
			mc[e.Provider][e.APIModelName] = e.Capabilities
		}
		if e.ModelType != "" {
			mt[e.Provider][e.APIModelName] = e.ModelType
		}
	}
	m.modelNameMap = mm
	m.modelCapabilityMu.Lock()
	m.modelCapability = mc
	m.modelType = mt
	m.modelCapabilityMu.Unlock()
	m.logger.Info("Updated model name map",
		zap.Int("entries", len(entries)),
		zap.Int("providers", len(mm)),
	)
}

// GetModelCapability returns the capability map for a given (provider, apiModelName).
// Returns nil if no capabilities are recorded.
func (m *Manager) GetModelCapability(provider, apiModelName string) map[string]bool {
	m.modelCapabilityMu.RLock()
	defer m.modelCapabilityMu.RUnlock()
	if m.modelCapability == nil {
		return nil
	}
	providerMap, ok := m.modelCapability[provider]
	if !ok {
		return nil
	}
	return providerMap[apiModelName]
}

// GetModelType returns the model type ("text", "image", "video", "audio")
// synced from Portal, or "" when unknown.
func (m *Manager) GetModelType(provider, apiModelName string) string {
	m.modelCapabilityMu.RLock()
	defer m.modelCapabilityMu.RUnlock()
	if m.modelType == nil {
		return ""
	}
	return m.modelType[provider][apiModelName]
}

// GetModelTypeAny returns the model type for apiModelName across any synced
// provider, or "" when unknown. Used before a provider is chosen (e.g. alias
// resolution on the chat endpoint).
func (m *Manager) GetModelTypeAny(apiModelName string) string {
	m.modelCapabilityMu.RLock()
	defer m.modelCapabilityMu.RUnlock()
	if m.modelType == nil {
		return ""
	}
	for _, models := range m.modelType {
		if t, ok := models[apiModelName]; ok {
			return t
		}
	}
	return ""
}

// IsMediaModelType reports whether the synced model type is a media
// generation type that requires a dedicated endpoint.
func IsMediaModelType(t string) bool {
	return t == "image" || t == "video" || t == "audio"
}

// ResolveUpstreamModelName returns the provider-specific product_id for a given
// (provider, apiModelName) pair. Returns empty string if no mapping exists.
func (m *Manager) ResolveUpstreamModelName(provider, apiModelName string) string {
	m.modelNameMapMu.RLock()
	defer m.modelNameMapMu.RUnlock()

	if m.modelNameMap == nil {
		return ""
	}
	providerMap, ok := m.modelNameMap[provider]
	if !ok {
		return ""
	}
	return providerMap[apiModelName]
}

// GetProviderConfig returns the base URL for a provider from Portal sync.
// If the provider was not synced, returns ("", false).
func (m *Manager) GetProviderConfig(provider string) (string, bool) {
	m.providerConfigsMu.RLock()
	defer m.providerConfigsMu.RUnlock()
	cfg, ok := m.providerConfigs[provider]
	return cfg.BaseURL, ok
}

// GetSmartRouting returns the smart routing toggle configuration from Portal.
func (m *Manager) GetSmartRouting() map[string]bool {
	m.smartMu.RLock()
	defer m.smartMu.RUnlock()
	if m.smartRouting == nil {
		return map[string]bool{"health": true, "tier": true, "rpm": true, "latency": true, "quota": true}
	}
	result := make(map[string]bool, len(m.smartRouting))
	for k, v := range m.smartRouting {
		result[k] = v
	}
	return result
}

// GetProviderHealth returns per-provider health metrics from Portal sync.
func (m *Manager) GetProviderHealth() map[string]ProviderHealthInfo {
	m.healthMu.RLock()
	defer m.healthMu.RUnlock()
	if m.providerHealth == nil {
		return map[string]ProviderHealthInfo{}
	}
	result := make(map[string]ProviderHealthInfo, len(m.providerHealth))
	for k, v := range m.providerHealth {
		result[k] = v
	}
	return result
}

// GetKeyHealth returns per-key health metrics from Portal sync.
func (m *Manager) GetKeyHealth() map[string]KeyHealthInfo {
	m.keyHealthMu.RLock()
	defer m.keyHealthMu.RUnlock()
	if m.keyHealth == nil {
		return map[string]KeyHealthInfo{}
	}
	result := make(map[string]KeyHealthInfo, len(m.keyHealth))
	for k, v := range m.keyHealth {
		result[k] = v
	}
	return result
}

// updatePoolsFromSync merges synced keys into existing pools, preserving
// runtime state (failures, cooldown, usage stats) for keys that already exist.
func (m *Manager) updatePoolsFromSync(keys []Key) {
	// Guard against empty snapshots: if portal returns zero keys (bug,
	// outage, or transient parse error), applying the sync would delete
	// every provider pool below and the gateway would instantly have no
	// keys to route to. Refuse to apply an empty snapshot when we already
	// hold non-empty local pools — stale keys are strictly safer than none.
	if len(keys) == 0 {
		m.poolsMu.RLock()
		localNonEmpty := len(m.pools) > 0
		m.poolsMu.RUnlock()
		if localNonEmpty {
			m.logger.Warn("Sync returned empty key snapshot; refusing to wipe non-empty local pools")
			return
		}
		return
	}

	// Group keys by provider and by ID within each provider
	providerKeys := make(map[string]map[string]*Key)
	for i := range keys {
		key := &keys[i]
		if key.Status == "" {
			key.Status = "active"
		}
		// Use Portal-provided limits if available; fallback to tier defaults
		if key.RPMLimit == 0 && key.TPMLimit == 0 {
			key.RPMLimit, key.TPMLimit = getTierLimits(key.Tier)
		}
		if providerKeys[key.Provider] == nil {
			providerKeys[key.Provider] = make(map[string]*Key)
		}
		providerKeys[key.Provider][key.ID] = key
	}

	newProviders := make(map[string]bool)
	for provider := range providerKeys {
		newProviders[provider] = true
	}

	// Merge each provider's pool
	for provider, idMap := range providerKeys {
		lock := m.getLock(provider)

		lock.Lock()

		// Check if pool already exists (protected by per-provider lock)
		existingPool, hasPool := m.getPool(provider)

		if hasPool {
			// Build lookup of existing keys by ID
			existingByID := make(map[string]*KeyInternal)
			for _, ik := range existingPool.internalKeys {
				existingByID[ik.ID] = ik
			}

			newKeyList := make([]*Key, 0, len(idMap))
			newInternalList := make([]*KeyInternal, 0, len(idMap))
			existingPool.TotalKeys = 0
			existingPool.ActiveKeys = 0

			for id, newKey := range idMap {
				if existingInternal, ok := existingByID[id]; ok {
					// Key exists: create new KeyInternal to avoid torn reads
					// on KeyStatic fields (slice headers are not atomically written
					// by struct copy). Pointer swap is atomic in Go.
					merged := *existingInternal.Key     // preserve runtime fields (quotaUsed, lastUsed)
					merged.KeyStatic = newKey.KeyStatic // overwrite static fields
					// Detect credential rotation: if the operator rotated the
					// secret in portal, a locally-killed key (inactive/purged for
					// the OLD credential) deserves a fresh start. Otherwise the
					// local kill verdict stands and must NOT be revived by the
					// portal-supplied Enabled (handled by IsKeyAvailable's
					// Status check, plus keeping Enabled false here so the
					// ActiveKeys stat stays accurate).
					credentialRotated := existingInternal.ActualKey != "" && existingInternal.ActualKey != newKey.Key
					if credentialRotated && (merged.Status == "inactive" || merged.Status == "purged") {
						merged.Status = "active"
						merged.Enabled = true
						merged.Failures = 0
						merged.FailureHistory = nil
						merged.InactiveInfo = nil
						merged.CooldownUntil = time.Time{}
						merged.PurgedFromPool = false
						merged.PurgedAt = time.Time{}
						merged.StatusHistory = append(merged.StatusHistory, StatusEntry{
							Status: "active",
							At:     time.Now(),
							Reason: "CREDENTIAL_ROTATED",
						})
						m.logger.Info("Key re-activated after credential rotation",
							zap.String("provider", provider),
							zap.String("key_id", id),
						)
					} else if merged.Status == "inactive" || merged.Status == "purged" {
						// Credential unchanged but locally killed: keep disabled
						// regardless of portal's Enabled to avoid a 401 flap loop.
						// IsKeyAvailable's Status check is the real gate; this
						// keeps the ActiveKeys stat accurate.
						merged.Enabled = false
					}
					newInternal := newKeyInternal(&merged, newKey.Key)
					newInternal.quotaUsed.Store(existingInternal.quotaUsed.Load())
					newInternal.lastUsed.Store(existingInternal.lastUsed.Load())
					newKeyList = append(newKeyList, newInternal.Key)
					newInternalList = append(newInternalList, newInternal)
				} else {
					// New key
					newKeyList = append(newKeyList, newKey)
					internal := newKeyInternal(newKey, newKey.Key)
					internal.quotaUsed.Store(newKey.QuotaUsed)
					internal.lastUsed.Store(newKey.LastUsed.UnixNano())
					newInternalList = append(newInternalList, internal)
				}
				existingPool.TotalKeys++
				if newKeyList[len(newKeyList)-1].Enabled {
					existingPool.ActiveKeys++
				}
			}
			existingPool.Keys = newKeyList
			existingPool.internalKeys = newInternalList
		} else {
			// New provider: create fresh pool
			pool := &ProviderPool{
				Provider:     provider,
				Keys:         make([]*Key, 0, len(idMap)),
				internalKeys: make([]*KeyInternal, 0, len(idMap)),
			}
			for _, newKey := range idMap {
				if newKey.Status == "" {
					newKey.Status = "active"
				}
				pool.Keys = append(pool.Keys, newKey)
				internal := newKeyInternal(newKey, newKey.Key)
				internal.quotaUsed.Store(newKey.QuotaUsed)
				internal.lastUsed.Store(newKey.LastUsed.UnixNano())
				pool.internalKeys = append(pool.internalKeys, internal)
				pool.TotalKeys++
				if newKey.Enabled {
					pool.ActiveKeys++
				}
			}
			lock.Unlock()
			m.setPool(provider, pool)
			continue
		}

		lock.Unlock()
	}

	// Remove pools for providers that no longer have keys
	m.poolsMu.Lock()
	for provider := range m.pools {
		if !newProviders[provider] {
			delete(m.pools, provider)
		}
	}
	m.poolsMu.Unlock()
}

// GetAllStats returns stats for all providers
func (m *Manager) GetAllStats() map[string]*ProviderPool {
	m.poolsMu.RLock()
	providers := make([]string, 0, len(m.pools))
	for k := range m.pools {
		providers = append(providers, k)
	}
	m.poolsMu.RUnlock()

	result := make(map[string]*ProviderPool)
	for _, provider := range providers {
		lock := m.getLock(provider)
		lock.RLock()

		pool, ok := m.getPool(provider)
		if !ok {
			lock.RUnlock()
			continue
		}

		// Create copy without sensitive data
		poolCopy := &ProviderPool{
			Provider:      pool.Provider,
			TotalKeys:     pool.TotalKeys,
			ActiveKeys:    pool.ActiveKeys,
			TotalRequests: pool.TotalRequests,
			TotalTokens:   pool.TotalTokens,
			Keys:          make([]*Key, 0, len(pool.Keys)),
		}

		for _, key := range pool.Keys {
			poolCopy.Keys = append(poolCopy.Keys, &Key{
				KeyStatic: KeyStatic{
					ID:             key.ID,
					Provider:       key.Provider,
					KeyPreview:     key.KeyPreview,
					Tier:           key.Tier,
					Priority:       key.Priority,
					BaseURL:        key.BaseURL,
					Models:         key.Models,
					Notes:          key.Notes,
					Enabled:        key.Enabled,
					AssignmentMode: key.AssignmentMode,
					IsShared:       key.IsShared,
					RPMLimit:       key.RPMLimit,
					TPMLimit:       key.TPMLimit,
					QuotaLimit:     key.QuotaLimit,
				},
				KeyRuntime: KeyRuntime{
					RPM:           key.RPM,
					TPM:           key.TPM,
					TotalRequests: key.TotalRequests,
					AvgLatencyMs:  key.AvgLatencyMs,
					Failures:      key.Failures,
					Status:        key.Status,
				},
			})
		}

		result[provider] = poolCopy
		lock.RUnlock()
	}

	return result
}

// AddKey adds a key to the pool
func (m *Manager) AddKey(key *Key) error {
	return m.AddKeyWithContext(context.Background(), key)
}

// AddKeyWithContext adds a key with context support
func (m *Manager) AddKeyWithContext(ctx context.Context, key *Key) error {
	provider := key.Provider
	lock := m.getLock(provider)
	lock.Lock()
	defer lock.Unlock()

	pool, ok := m.getPool(provider)
	if !ok {
		pool = &ProviderPool{
			Provider:     provider,
			Keys:         make([]*Key, 0),
			internalKeys: make([]*KeyInternal, 0),
		}
		m.setPool(provider, pool)
	}

	// Set defaults
	key.Enabled = true
	key.Status = "active"
	key.RPMLimit, key.TPMLimit = getTierLimits(key.Tier)

	// Store internal key
	internal := newKeyInternal(key, key.Key)
	internal.quotaUsed.Store(key.QuotaUsed)
	internal.lastUsed.Store(key.LastUsed.UnixNano())

	pool.Keys = append(pool.Keys, key)
	pool.internalKeys = append(pool.internalKeys, internal)
	pool.TotalKeys++
	pool.ActiveKeys++

	// Persist to Redis
	if m.redis != nil {
		if err := m.persistToRedis(ctx); err != nil {
			m.logger.Warn("Failed to persist key to Redis", zap.Error(err))
		}
	}

	return nil
}

// RemoveKey removes a key from the pool
func (m *Manager) RemoveKey(provider, keyID string) bool {
	return m.RemoveKeyWithContext(context.Background(), provider, keyID)
}

// RemoveKeyWithContext removes a key with context support
func (m *Manager) RemoveKeyWithContext(ctx context.Context, provider, keyID string) bool {
	lock := m.getLock(provider)
	lock.Lock()
	defer lock.Unlock()

	pool, ok := m.getPool(provider)
	if !ok {
		return false
	}

	for i, key := range pool.Keys {
		if key.ID == keyID || strings.Contains(key.KeyPreview, keyID) {
			pool.Keys = append(pool.Keys[:i], pool.Keys[i+1:]...)
			pool.internalKeys = append(pool.internalKeys[:i], pool.internalKeys[i+1:]...)
			pool.TotalKeys--
			if key.Enabled {
				pool.ActiveKeys--
			}

			// Persist to Redis
			if m.redis != nil {
				_ = m.persistToRedis(ctx)
			}
			return true
		}
	}

	return false
}

// ToggleKey enables or disables a key
func (m *Manager) ToggleKey(provider, keyID string, enabled bool) bool {
	return m.ToggleKeyWithContext(context.Background(), provider, keyID, enabled)
}

// ToggleKeyWithContext toggles key with context support
func (m *Manager) ToggleKeyWithContext(ctx context.Context, provider, keyID string, enabled bool) bool {
	lock := m.getLock(provider)
	lock.Lock()
	defer lock.Unlock()

	pool, ok := m.getPool(provider)
	if !ok {
		return false
	}

	for _, key := range pool.Keys {
		if key.ID == keyID || strings.Contains(key.KeyPreview, keyID) {
			if key.Enabled != enabled {
				key.Enabled = enabled
				if enabled {
					key.Status = "active"
					pool.ActiveKeys++
				} else {
					key.Status = "inactive"
					pool.ActiveKeys--
				}

				// Persist to Redis
				if m.redis != nil {
					_ = m.persistToRedis(ctx)
				}
			}
			return true
		}
	}

	return false
}

// UpdateKeyUsage updates key usage stats
func (m *Manager) UpdateKeyUsage(provider, keyID string, latencyMs int, success bool) {
	lock := m.getLock(provider)
	lock.Lock()
	defer lock.Unlock()

	pool, ok := m.getPool(provider)
	if !ok {
		return
	}

	for _, key := range pool.Keys {
		if key.ID == keyID {
			key.TotalRequests++
			pool.TotalRequests++
			// Note: Failures are managed by ReportKeyFailure, not here
			// Update average latency using exponential moving average
			if key.AvgLatencyMs == 0 {
				key.AvgLatencyMs = latencyMs
			} else {
				// Exponential moving average: weight recent by 0.1, history by 0.9
				// This prevents a single outlier from skewing the metric by 50%
				key.AvgLatencyMs = (key.AvgLatencyMs*9 + latencyMs) / 10
			}
			break
		}
	}
}

// getTierLimits returns RPM and TPM limits for a tier
// getTierLimits returns RPM and TPM limits for a tier. The default branch is
// getTierLimits returns RPM and TPM limits for a tier. These are fallbacks
// applied when an upstream key has no explicit RPM/TPM configured (both 0).
// They are intentionally generous so that keys for long-context /
// high-throughput models (e.g. kimi-k3, where a single request can be tens of
// thousands of tokens) are not throttled into cooldown by a small default.
// Set explicit per-key limits on the upstream key to override. Note: an
// unrecognized tier falls through to default.
func getTierLimits(tier string) (rpmLimit, tpmLimit int) {
	switch tier {
	case "enterprise":
		return 1200, 10000000
	case "premium":
		return 600, 3000000
	case "budget":
		return 60, 1500000
	default:
		return 300, 1500000
	}
}

// StartSyncLoop starts periodic sync with Portal
func (m *Manager) StartSyncLoop(ctx context.Context) {
	syncInterval := time.Duration(atomic.LoadInt64(&m.syncInterval))
	if syncInterval == 0 {
		syncInterval = 60 * time.Second
		atomic.StoreInt64(&m.syncInterval, int64(syncInterval))
	}

	go func() {
		ticker := time.NewTicker(syncInterval)
		defer ticker.Stop()

		// Initial sync
		initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		if err := m.SyncFromPortalWithContext(initCtx); err != nil {
			m.logger.Error("Initial sync failed", zap.Error(err))
			// Try to load from Redis as fallback
			if m.redis != nil {
				if loadErr := m.loadFromRedis(initCtx); loadErr != nil {
					m.logger.Error("Failed to load from Redis", zap.Error(loadErr))
				} else {
					m.logger.Info("Loaded keys from Redis cache")
				}
			}
		}
		cancel()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !m.syncAllowed() {
					m.logger.Debug("Skipping periodic sync due to failure backoff")
					continue
				}

				syncCtx, syncCancel := context.WithTimeout(ctx, 30*time.Second)
				// Poll the lightweight version endpoint first; only fetch the full
				// snapshot when the Portal reports a different config version.
				version, err := m.SyncVersionFromPortal(syncCtx)
				if err != nil {
					m.logger.Error("Failed to poll portal config version", zap.Error(err))
					m.recordSyncFailure()
					syncCancel()
					continue
				}
				if version.ConfigVersion != "" && version.ConfigVersion == m.GetConfigVersion() {
					m.setLastSync(time.Now())
					m.logger.Debug("Portal config unchanged, skipping full sync",
						zap.String("config_version", version.ConfigVersion))
					syncCancel()
					continue
				}

				if err := m.SyncFromPortalWithContext(syncCtx); err != nil {
					m.logger.Error("Sync failed", zap.Error(err))
				}
				syncCancel()
			}
		}
	}()
}

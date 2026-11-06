package keypool

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// containsMatch checks if a string slice contains a given string.
// Returns (matched bool, matchedName string) — the actual name from the slice that matched.
// Matching priority: exact > normalized (dot/hyphen) > prefix (with date suffixes).
func containsMatch(slice []string, item string) (bool, string) {
	normalized := normalizeModelName(item)

	// Fast path: single pass to build exact + normalized lookup maps.
	// This avoids repeated O(L) normalization scans when the slice is large.
	exactMap := make(map[string]string, len(slice))
	normMap := make(map[string]string, len(slice))
	for _, s := range slice {
		if s == "" {
			continue
		}
		if _, ok := exactMap[s]; !ok {
			exactMap[s] = s
		}
		norm := normalizeModelName(s)
		if _, ok := normMap[norm]; !ok {
			normMap[norm] = s
		}
	}

	if s, ok := exactMap[item]; ok {
		return true, s
	}
	if s, ok := normMap[normalized]; ok {
		return true, s
	}

	// Prefix match (item is prefix of slice element, e.g. "claude-opus-4-6" → "claude-opus-4-6-20260512")
	// Pick the shortest prefix match to prefer base model over dated/thinking variants.
	// Require hyphen boundary so "gpt-4" does not match "gpt-4o".
	var shortest string
	for _, s := range slice {
		normS := normalizeModelName(s)
		if normS == normalized {
			// Already handled by exact/normalized above; skip
			continue
		}
		if strings.HasPrefix(normS, normalized+"-") {
			if shortest == "" || len(s) < len(shortest) {
				shortest = s
			}
		}
	}
	if shortest != "" {
		return true, shortest
	}

	return false, ""
}

// contains is a convenience wrapper that returns only the bool from containsMatch.
func contains(slice []string, item string) bool {
	matched, _ := containsMatch(slice, item)
	return matched
}

// normalizeModelName replaces dots with hyphens for fuzzy model name matching.
func normalizeModelName(name string) string {
	return strings.ReplaceAll(name, ".", "-")
}

// ParseKeyType - Parse key type from key format per V3 spec
// Returns: "general", "subscription", "enterprise", or error
func ParseKeyType(key string) (string, error) {
	if !strings.HasPrefix(key, "sk-wgit-") {
		return "", fmt.Errorf("invalid key format: must start with sk-wgit-")
	}

	remainder := strings.TrimPrefix(key, "sk-wgit-")

	if strings.HasPrefix(remainder, "en-") {
		return "enterprise", nil
	}
	if strings.HasPrefix(remainder, "tp-") {
		return "subscription", nil
	}
	// sk-wgit-xxxx → general (economy/general/single_model)
	return "general", nil
}

// IsValidKeyFormat - Check if key has valid format
func IsValidKeyFormat(key string) bool {
	return strings.HasPrefix(key, "sk-wgit-") && len(key) > 8
}

// GetKeyPriority - Get priority level from access mode
func GetKeyPriority(accessMode string) string {
	switch accessMode {
	case "enterprise", "subscription", "token_plan", "single_model":
		return "high"
	case "economy":
		return "economy"
	default:
		return "normal"
	}
}

// GetKeyByPriority selects a key based on user priority level and requested model.
// If reqModel is empty, model filtering is skipped.
// If preferredKeyID is set and the key is in the candidate set, it is prioritized.
func (m *Manager) GetKeyByPriority(ctx context.Context, provider string, userPriority string, reqModel string, preferredKeyID string) *KeyInternal {
	pool, ok := m.getPool(provider)
	if !ok || len(pool.internalKeys) == 0 {
		// Try to load from Redis
		if m.redis != nil {
			if err := m.loadFromRedis(ctx); err != nil {
				m.logger.Warn("Failed to load key pool from Redis",
					zap.String("provider", provider),
					zap.Error(err),
				)
				return nil
			}
			pool, ok = m.getPool(provider)
			if !ok || len(pool.internalKeys) == 0 {
				return nil
			}
		} else {
			return nil
		}
	}

	lock := m.getLock(provider)
	lock.RLock()
	defer lock.RUnlock()

	// Filter keys by priority based on user type
	// Priority levels: high > normal > economy
	// - Enterprise/Subscription/Single_product users: can use high + normal keys
	// - General users: use normal keys
	// - Economy users: use economy keys first, fallback to normal
	var filtered []*KeyInternal

	switch userPriority {
	case "high":
		// Enterprise/Subscription/Single_product: prefer high priority, then normal
		for _, key := range pool.internalKeys {
			if !m.IsKeyAvailable(key.Key) {
				continue
			}
			if key.Priority == "high" || key.Priority == "normal" || key.Priority == "" {
				filtered = append(filtered, key)
			}
		}
	case "economy":
		// Economy users: prefer economy keys, fallback to normal
		for _, key := range pool.internalKeys {
			if !m.IsKeyAvailable(key.Key) {
				continue
			}
			if key.Priority == "economy" {
				filtered = append(filtered, key)
			}
		}
		// Fallback to normal if no economy keys
		if len(filtered) == 0 {
			for _, key := range pool.internalKeys {
				if !m.IsKeyAvailable(key.Key) {
					continue
				}
				if key.Priority == "normal" || key.Priority == "" {
					filtered = append(filtered, key)
				}
			}
		}
	default:
		// General users: use normal priority keys
		for _, key := range pool.internalKeys {
			if !m.IsKeyAvailable(key.Key) {
				continue
			}
			if key.Priority == "normal" || key.Priority == "" {
				filtered = append(filtered, key)
			}
		}
	}

	// Filter by model support (if requested model is specified)
	if reqModel != "" {
		var modelFiltered []*KeyInternal
		for _, key := range filtered {
			if len(key.Models) == 0 || contains(key.Models, reqModel) {
				modelFiltered = append(modelFiltered, key)
			}
		}
		filtered = modelFiltered
	}

	// Filter by upstream key rate limit (RPM check)
	var rateFiltered []*KeyInternal
	for _, key := range filtered {
		if m.checkUpstreamAvailable(key, reqModel) {
			rateFiltered = append(rateFiltered, key)
		}
	}
	filtered = rateFiltered

	// If no filtered keys remain, fall back to any available key that still
	// matches the requested model. This relaxes priority tier and RPM/TPM
	// limits but NEVER bypasses model matching — routing a request to a key
	// that does not support the model would fail upstream. If no model-matching
	// key exists, return nil so the caller can try another provider.
	if len(filtered) == 0 {
		filtered = make([]*KeyInternal, 0)
		for _, key := range pool.internalKeys {
			if !m.IsKeyAvailable(key.Key) {
				continue
			}
			if reqModel != "" && len(key.Models) > 0 && !contains(key.Models, reqModel) {
				continue
			}
			filtered = append(filtered, key)
		}
	}

	if len(filtered) == 0 {
		return nil
	}

	// If preferred key ID is set and the key is in the filtered set, prioritize it
	if preferredKeyID != "" {
		for _, key := range filtered {
			if key.ID == preferredKeyID {
				key.SetLastUsed(time.Now())
				return key
			}
		}
	}

	// Find least recently used key from filtered set
	var selected *KeyInternal
	for _, key := range filtered {
		if selected == nil || key.GetLastUsed().Before(selected.GetLastUsed()) {
			selected = key
		}
	}

	if selected != nil {
		selected.SetLastUsed(time.Now())
	}

	return selected
}

// checkUpstreamAvailable checks if an upstream key has RPM budget remaining.
// Uses a fixed 60-second window. If key has no RPM/TPM limits configured,
// it is always available. When model is non-empty and the key carries
// ModelLimits (per-model budgets synced from the Portal), the usage counter
// for that specific model is checked against the model's entry, falling back
// to the "*" entry when no exact match exists.
func (m *Manager) checkUpstreamAvailable(key *KeyInternal, model string) bool {
	if key.RPMLimit <= 0 && key.TPMLimit <= 0 && len(key.ModelLimits) == 0 {
		return true // No limits configured
	}

	k := key.Provider + ":" + key.ID
	usage := m.getUpstreamUsage(k)
	if usage == nil {
		return true // No usage recorded yet
	}

	usage.mu.Lock()
	defer usage.mu.Unlock()

	now := time.Now()
	if now.After(usage.windowEnd) {
		// Window expired, reset
		usage.rpmUsed = 0
		usage.tpmUsed = 0
		usage.models = nil
		usage.windowEnd = now.Add(time.Minute)
		return true
	}

	if key.RPMLimit > 0 && usage.rpmUsed >= key.RPMLimit {
		return false
	}
	if key.TPMLimit > 0 && usage.tpmUsed >= int64(key.TPMLimit) {
		return false
	}

	if model != "" && len(key.ModelLimits) > 0 {
		limit, ok := key.ModelLimits[model]
		if !ok {
			limit, ok = key.ModelLimits["*"]
		}
		if ok && (limit.RPM > 0 || limit.TPM > 0) {
			mc := usage.models[model]
			if mc == nil {
				return true // No usage for this model yet
			}
			if limit.RPM > 0 && mc.rpm >= limit.RPM {
				return false
			}
			if limit.TPM > 0 && mc.tpm >= limit.TPM {
				return false
			}
		}
	}

	return true
}

// SelectRoundRobinModel selects the next model in rotation using Redis counter.
// Falls back to in-memory counter if Redis is not available.
// The counter has a 24-hour TTL to auto-expire.
func (m *Manager) SelectRoundRobinModel(keyID string, models []string) string {
	if len(models) == 0 {
		return ""
	}
	if len(models) == 1 {
		return models[0]
	}

	// Use Redis if available for distributed consistency
	if m.redis != nil {
		ctx := context.Background()
		redisKey := "rr:" + keyID

		count, err := m.redis.Incr(ctx, redisKey).Result()
		if err != nil {
			m.logger.Warn("Redis round robin failed, using fallback",
				zap.Error(err),
				zap.String("key_id", keyID))
			// Fallback to first model on error
			return models[0]
		}

		// Set TTL on first use or refresh
		if count == 1 {
			m.redis.Expire(ctx, redisKey, 24*time.Hour)
		}

		index := int(count-1) % len(models)
		return models[index]
	}

	// Fallback to in-memory counter (for single-instance deployments)
	// Use Manager-level sync.Map for thread-safe access across calls
	type rrCounter struct {
		value int64
		mu    sync.Mutex
	}

	actual, _ := m.roundRobinCounters.LoadOrStore(keyID, &rrCounter{value: 0})
	counter := actual.(*rrCounter)

	counter.mu.Lock()
	counter.value++
	count := counter.value
	counter.mu.Unlock()

	index := int(count-1) % len(models)
	return models[index]
}

// CandidateKey is a key candidate with its provider for cross-provider selection.
type CandidateKey struct {
	Provider          string
	Key               *KeyInternal
	UpstreamModelName string // the model name to send to this provider
}

// GetAllKeysForModel collects candidate keys across all providers for a given model.
// Only keys from providers not in the excluded set are returned.
//
// Candidacy is gated by the Portal-synced model name map (built from active
// service_models): a key is eligible only when its (provider, model) pair is
// activated in the Portal catalog. A key advertising the model is NOT
// sufficient — provider consoles may list models that are not activated on
// our platform, and some providers never return a model list at all. Before
// the first successful sync (map not loaded), the legacy advertisement-based
// matching is used as a fallback.
//
// Keys are grouped by user priority tier so the caller can apply unified
// priority-based selection.
func (m *Manager) GetAllKeysForModel(model string, excludedProviders map[string]bool, tierFilter string) (high, normal, economy []*CandidateKey) {
	// Catalog gate: provider → upstream product_id for this model.
	m.modelNameMapMu.RLock()
	providerProductID := make(map[string]string)
	catalogReady := m.modelNameMap != nil
	if catalogReady {
		norm := normalizeModelName(model)
		for prov, provMap := range m.modelNameMap {
			if pid, ok := provMap[model]; ok {
				providerProductID[prov] = pid
				continue
			}
			// dot/hyphen-normalized fallback
			for name, pid := range provMap {
				if normalizeModelName(name) == norm {
					providerProductID[prov] = pid
					break
				}
			}
		}
	}
	m.modelNameMapMu.RUnlock()

	// Snapshot the provider list so we can release the global pool map lock
	// and iterate under per-provider read locks.
	m.poolsMu.RLock()
	type poolSnap struct {
		provider string
		pool     *ProviderPool
	}
	snaps := make([]poolSnap, 0, len(m.pools))
	for provider, pool := range m.pools {
		if excludedProviders[provider] {
			continue
		}
		if len(pool.internalKeys) == 0 {
			continue
		}
		snaps = append(snaps, poolSnap{provider: provider, pool: pool})
	}
	m.poolsMu.RUnlock()

	for _, snap := range snaps {
		provider := snap.provider
		pool := snap.pool
		lock := m.getLock(provider)
		lock.RLock()

		mappedProductID := providerProductID[provider]

		for _, key := range pool.internalKeys {
			if !m.IsKeyAvailable(key.Key) {
				continue
			}
			if catalogReady {
				// Portal catalog is the single source of truth: only
				// activated (provider, model) pairs are routable.
				if mappedProductID == "" {
					continue
				}
			} else if len(key.Models) > 0 {
				// Pre-sync fallback: legacy key-advertised model matching.
				matched, _ := key.MatchModel(model)
				if !matched {
					continue
				}
			}
			// Tier filter (new)
			if tierFilter != "" && key.Priority != tierFilter {
				continue
			}
			if !m.checkUpstreamAvailable(key, model) {
				continue
			}
			// Model-level cooldown: skip if this (key, model) was rate-limited upstream.
			// Unlike key-level cooldown, this only blocks the specific model.
			if m.IsModelOnCooldown(key.ID, model) {
				continue
			}
			// Model-level health check: skip if this model is disabled on this key
			if m.modelHealth != nil && !m.modelHealth.IsModelAvailable(key.ID, model) {
				continue
			}
			ck := &CandidateKey{Provider: provider, Key: key}
			// Upstream model name comes from the catalog mapping when present.
			if mappedProductID != "" {
				ck.UpstreamModelName = mappedProductID
			} else if len(key.Models) > 0 {
				if matched, matchedName := key.MatchModel(model); matched {
					ck.UpstreamModelName = matchedName
				}
			}
			switch key.Priority {
			case "high":
				high = append(high, ck)
			case "economy":
				economy = append(economy, ck)
			default:
				normal = append(normal, ck)
			}
		}
		lock.RUnlock()
	}
	return
}

// GetProvidersForModel returns all providers that have available keys for the given model.
// Like GetAllKeysForModel, candidacy is gated by the Portal-synced model name
// map (active service_models) once it has been loaded.
func (m *Manager) GetProvidersForModel(model string) []string {
	// Catalog gate (same source of truth as GetAllKeysForModel).
	m.modelNameMapMu.RLock()
	catalogProviders := make(map[string]bool)
	catalogReady := m.modelNameMap != nil
	if catalogReady {
		norm := normalizeModelName(model)
		for prov, provMap := range m.modelNameMap {
			if _, ok := provMap[model]; ok {
				catalogProviders[prov] = true
				continue
			}
			for name := range provMap {
				if normalizeModelName(name) == norm {
					catalogProviders[prov] = true
					break
				}
			}
		}
	}
	m.modelNameMapMu.RUnlock()

	m.poolsMu.RLock()
	type poolSnap struct {
		provider string
		pool     *ProviderPool
	}
	snaps := make([]poolSnap, 0, len(m.pools))
	for provider, pool := range m.pools {
		if len(pool.internalKeys) == 0 {
			continue
		}
		snaps = append(snaps, poolSnap{provider: provider, pool: pool})
	}
	m.poolsMu.RUnlock()

	var providers []string
	for _, snap := range snaps {
		provider := snap.provider
		pool := snap.pool
		if catalogReady && !catalogProviders[provider] {
			continue
		}
		lock := m.getLock(provider)
		lock.RLock()
		found := false
		for _, key := range pool.internalKeys {
			if !m.IsKeyAvailable(key.Key) {
				continue
			}
			if catalogReady || len(key.Models) == 0 || contains(key.Models, model) {
				found = true
				break
			}
		}
		lock.RUnlock()
		if found {
			providers = append(providers, provider)
		}
	}
	return providers
}

// GetKeysForProvider returns all available keys for a specific provider, grouped by priority.
// Used by the Bedrock dual-track routing to isolate Bedrock keys from the standard pool.
func (m *Manager) GetKeysForProvider(provider string) (high, normal, economy []*CandidateKey) {
	m.poolsMu.RLock()
	pool, ok := m.pools[provider]
	m.poolsMu.RUnlock()
	if !ok || len(pool.internalKeys) == 0 {
		return
	}

	lock := m.getLock(provider)
	lock.RLock()
	defer lock.RUnlock()

	for _, key := range pool.internalKeys {
		if !m.IsKeyAvailable(key.Key) {
			continue
		}
		if !m.checkUpstreamAvailable(key, "") {
			continue
		}
		ck := &CandidateKey{Provider: provider, Key: key, UpstreamModelName: ""}
		switch key.Priority {
		case "high":
			high = append(high, ck)
		case "economy":
			economy = append(economy, ck)
		default:
			normal = append(normal, ck)
		}
	}
	return
}

// GetProviderStats returns statistics for providers that support a model
func (m *Manager) GetProviderStats(model string) map[string]ProviderStats {
	m.poolsMu.RLock()
	type poolSnap struct {
		provider string
		pool     *ProviderPool
	}
	snaps := make([]poolSnap, 0, len(m.pools))
	for provider, pool := range m.pools {
		if len(pool.internalKeys) == 0 {
			continue
		}
		snaps = append(snaps, poolSnap{provider: provider, pool: pool})
	}
	m.poolsMu.RUnlock()

	stats := make(map[string]ProviderStats)
	for _, snap := range snaps {
		provider := snap.provider
		pool := snap.pool
		lock := m.getLock(provider)
		lock.RLock()

		var availableKeys, totalKeys int
		var totalLatencyMs, totalRequests int64
		var totalSuccessRate float64

		for _, key := range pool.internalKeys {
			totalKeys++
			if m.IsKeyAvailable(key.Key) {
				if len(key.Models) == 0 || contains(key.Models, model) {
					availableKeys++
					totalLatencyMs += int64(key.AvgLatencyMs)
					totalRequests += key.TotalRequests
					// Success rate approximation
					if key.TotalRequests > 0 {
						failRate := float64(key.Failures) / float64(key.TotalRequests)
						totalSuccessRate += (1.0 - failRate)
					} else {
						totalSuccessRate += 1.0 // Assume good for unused keys
					}
				}
			}
		}
		lock.RUnlock()

		if availableKeys > 0 {
			stats[provider] = ProviderStats{
				AvailableKeys: availableKeys,
				TotalKeys:     totalKeys,
				AvgLatencyMs:  int(totalLatencyMs / int64(availableKeys)),
				TotalRequests: totalRequests,
				SuccessRate:   totalSuccessRate / float64(availableKeys),
			}
		}
	}
	return stats
}

// ProviderStats holds statistics for a provider
type ProviderStats struct {
	AvailableKeys int     `json:"available_keys"`
	TotalKeys     int     `json:"total_keys"`
	AvgLatencyMs  int     `json:"avg_latency_ms"`
	TotalRequests int64   `json:"total_requests"`
	SuccessRate   float64 `json:"success_rate"`
}

// HasActiveKeysForModel checks if there are any active upstream keys that support the given model.
// Used by scene classifier to verify model availability before recommending it.
func (m *Manager) HasActiveKeysForModel(model string) bool {
	if m == nil {
		return false
	}
	excluded := make(map[string]bool)
	high, normal, economy := m.GetAllKeysForModel(model, excluded, "")
	return len(high)+len(normal)+len(economy) > 0
}

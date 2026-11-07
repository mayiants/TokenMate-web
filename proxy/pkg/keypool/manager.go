package keypool

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mayiants/ai-proxy-go/internal/config"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// ProviderConfigEntry holds runtime provider config (from sync or fallback)
type ProviderConfigEntry struct {
	BaseURL  string
	AuthType string
}

// ModelLimit is a per-model RPM/TPM budget for one upstream key, synced from
// the Portal. The map key is a model name; "*" is the fallback entry that
// applies to any model without a more specific entry.
type ModelLimit struct {
	RPM int   `json:"rpm"`
	TPM int64 `json:"tpm"`
}

// KeyStatic holds fields sourced from Portal sync (read-only at runtime).
// These fields are overwritten on every sync merge.
type KeyStatic struct {
	ID             string                `json:"id"`
	Provider       string                `json:"provider"`
	Key            string                `json:"-"` // Sensitive, never serialize to JSON
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
	RPMLimit       int                   `json:"rpm_limit"`
	TPMLimit       int                   `json:"tpm_limit"`
	ModelLimits    map[string]ModelLimit `json:"model_limits,omitempty"`
	QuotaLimit     int64                 `json:"quota_limit"`
}

// KeyRuntime holds fields maintained locally by the Proxy.
// These fields are preserved across sync merges.
type KeyRuntime struct {
	// Status tracking
	Status        string        `json:"status"` // "active", "cooldown", "inactive", "purged"
	StatusHistory []StatusEntry `json:"status_history,omitempty"`

	// Failure tracking
	Failures       int            `json:"failures"`
	FailureHistory []FailureEntry `json:"failure_history,omitempty"`
	LastFailure    time.Time      `json:"last_failure,omitempty"`

	// Cooldown tracking
	CooldownUntil  time.Time `json:"cooldown_until,omitempty"`
	RateLimitUntil time.Time `json:"rate_limit_until,omitempty"`

	// Inactive tracking (for 24h purge)
	InactiveInfo   *InactiveInfo `json:"inactive_info,omitempty"`
	PurgedFromPool bool          `json:"purged_from_pool"`
	PurgedAt       time.Time     `json:"purged_at,omitempty"`

	// Runtime stats
	RPM           int       `json:"rpm"`
	TPM           int       `json:"tpm"`
	TotalRequests int64     `json:"total_requests"`
	AvgLatencyMs  int       `json:"avg_latency_ms"`
	LastUsed      time.Time `json:"last_used"`
	QuotaUsed     int64     `json:"quota_used"`
}

// Key represents an upstream API key (for internal use).
// It embeds KeyStatic and KeyRuntime for cache-friendly access while
// keeping static/runtime fields logically separated for sync merge.
type Key struct {
	KeyStatic
	KeyRuntime
}

// StatusEntry represents a status change event
type StatusEntry struct {
	Status string    `json:"status"`
	At     time.Time `json:"at"`
	Reason string    `json:"reason"`
}

// FailureEntry represents a failure event
type FailureEntry struct {
	Time      time.Time `json:"time"`
	Status    int       `json:"status"`
	Error     string    `json:"error"`
	ErrorType string    `json:"error_type"`
}
type InactiveInfo struct {
	At           time.Time `json:"at"`
	Reason       string    `json:"reason"`
	FailureCount int       `json:"failure_count"`
	LastError    string    `json:"last_error,omitempty"`
}

// KeyInternal is used for internal storage with sensitive key
type KeyInternal struct {
	*Key
	ActualKey string // The actual API key, stored separately
	// Atomic runtime fields to prevent data races on concurrent access
	quotaUsed atomic.Int64
	lastUsed  atomic.Int64 // UnixNano
	// lastSuccess is the UnixNano of the last SUCCESSFUL upstream call on this
	// key. Used by smartRoute's success-aware LRU so a never-successful key
	// (lastSuccess==0) is not preferred by the "oldest first" tie-break.
	lastSuccess atomic.Int64

	// Precomputed model matching sets. Built once from key.Models so the hot
	// path in GetKeyByPriority does not allocate maps on every lookup.
	exactModelSet map[string]string
	normModelSet  map[string]string
}

// newKeyInternal creates a KeyInternal and precomputes model matching sets.
func newKeyInternal(key *Key, actualKey string) *KeyInternal {
	if key == nil {
		return nil
	}
	exact, norm := buildModelSets(key.Models)
	return &KeyInternal{
		Key:           key,
		ActualKey:     actualKey,
		exactModelSet: exact,
		normModelSet:  norm,
	}
}

// buildModelSets builds exact and normalized lookup maps from a model list.
func buildModelSets(models []string) (map[string]string, map[string]string) {
	exact := make(map[string]string, len(models))
	norm := make(map[string]string, len(models))
	for _, s := range models {
		if s == "" {
			continue
		}
		if _, ok := exact[s]; !ok {
			exact[s] = s
		}
		n := normalizeModelName(s)
		if _, ok := norm[n]; !ok {
			norm[n] = s
		}
	}
	return exact, norm
}

// MatchModel reports whether the key supports the requested model.
// Matching priority: exact > normalized (dot/hyphen) > prefix (with date suffixes).
func (k *KeyInternal) MatchModel(model string) (bool, string) {
	if k == nil {
		return false, ""
	}

	// Fallback to the generic helper if no precomputed sets are available.
	if k.exactModelSet == nil || k.normModelSet == nil {
		return containsMatch(k.Models, model)
	}

	normalized := normalizeModelName(model)
	if s, ok := k.exactModelSet[model]; ok {
		return true, s
	}
	if s, ok := k.normModelSet[normalized]; ok {
		return true, s
	}

	// Prefix match (model is prefix of a supported model, e.g. "claude-opus-4-6" → "claude-opus-4-6-20260512")
	// Require hyphen boundary so "gpt-4" does not match "gpt-4o".
	var shortest string
	for _, s := range k.Models {
		if s == "" {
			continue
		}
		normS := normalizeModelName(s)
		if normS == normalized {
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

// AddQuotaUsed atomically adds n to the key's quota used counter.
func (k *KeyInternal) AddQuotaUsed(n int64) {
	if k != nil {
		k.quotaUsed.Add(n)
	}
}

// GetQuotaUsed returns the atomic quota used counter.
func (k *KeyInternal) GetQuotaUsed() int64 {
	if k == nil {
		return 0
	}
	return k.quotaUsed.Load()
}

// SetLastUsed atomically sets the key's last used timestamp.
func (k *KeyInternal) SetLastUsed(t time.Time) {
	if k != nil {
		k.lastUsed.Store(t.UnixNano())
	}
}

// GetLastUsed returns the atomic last used timestamp.
func (k *KeyInternal) GetLastUsed() time.Time {
	if k == nil {
		return time.Time{}
	}
	return time.Unix(0, k.lastUsed.Load())
}

// SetLastSuccess atomically records the time of a successful upstream call.
func (k *KeyInternal) SetLastSuccess(t time.Time) {
	if k != nil {
		k.lastSuccess.Store(t.UnixNano())
	}
}

// GetLastSuccess returns the last successful-call timestamp (zero if never).
func (k *KeyInternal) GetLastSuccess() time.Time {
	if k == nil {
		return time.Time{}
	}
	ns := k.lastSuccess.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

// GetFailures returns the current consecutive-failure counter (reset on success).
func (k *KeyInternal) GetFailures() int {
	if k == nil || k.Key == nil {
		return 0
	}
	return k.Failures
}

// GetLastFailure returns the last failure timestamp (zero if none).
func (k *KeyInternal) GetLastFailure() time.Time {
	if k == nil || k.Key == nil {
		return time.Time{}
	}
	return k.LastFailure
}

// upstreamUsageEntry tracks per-key RPM/TPM usage in a fixed 60s window.
// models holds per-model counters used to enforce ModelLimits; the counters
// share the entry's window and are reset together with it.
type upstreamUsageEntry struct {
	rpmUsed   int
	tpmUsed   int64
	windowEnd time.Time
	models    map[string]*modelUsageCounter
	mu        sync.Mutex
}

// modelUsageCounter tracks per-(key, model) usage within the parent entry's window.
type modelUsageCounter struct {
	rpm int
	tpm int64
}

// upstreamUsageShardCount is the number of sharded maps for upstream usage.
// Must be a power of two for the fast modulo below.
const upstreamUsageShardCount = 32

// upstreamUsageShard holds a subset of upstream usage entries with its own lock.
type upstreamUsageShard struct {
	mu      sync.RWMutex
	entries map[string]*upstreamUsageEntry
}

// ProviderPool represents a pool of keys for a provider
type ProviderPool struct {
	Provider string `json:"provider"`
	Keys     []*Key `json:"keys"`

	// Stats
	TotalKeys     int   `json:"totalKeys"`
	ActiveKeys    int   `json:"activeKeys"`
	TotalRequests int64 `json:"totalRequests"`
	TotalTokens   int64 `json:"totalTokens"`

	// Internal keys with actual values
	internalKeys []*KeyInternal
}

// providerLock provides per-provider locking for sharded concurrency
type providerLock struct {
	sync.RWMutex
}

// Manager manages the key pool with sharded locks and Redis persistence
type Manager struct {
	// Sharded locks - one per provider for better concurrency
	locksMu sync.RWMutex
	locks   map[string]*providerLock

	// Global lock for pool map operations
	poolsMu sync.RWMutex
	pools   map[string]*ProviderPool

	cfg    *config.Config
	logger *zap.Logger

	// Portal sync
	portalURL    string
	portalSecret string
	// proxyID/proxyRegion/syncInterval are written by SetProxyInfo (startup/config)
	// and read by sync/pubsub goroutines. Protect with proxyInfoMu.
	proxyID             atomic.Value // string
	proxyRegion         atomic.Value // string
	syncInterval        int64        // nanoseconds, accessed atomically
	healthCheckInterval time.Duration
	syncStateMu         sync.RWMutex
	lastSync            time.Time
	configVersion       string

	// Sync failure backoff state
	syncBackoffMu           sync.Mutex
	syncConsecutiveFailures int
	syncNextAllowed         time.Time

	// Provider configs from Portal sync (id -> config)
	providerConfigsMu sync.RWMutex
	providerConfigs   map[string]ProviderConfigEntry

	// Upstream key rate limiting (provider:keyID -> usage)
	// Sharded into multiple maps to reduce lock contention under high throughput.
	upstreamUsageShards [upstreamUsageShardCount]upstreamUsageShard

	// Redis for persistence
	redis         *redis.Client
	redisKey      string
	encryptionKey string // For encrypting actual keys in Redis

	// Model routing from Portal sync (model_name → ordered provider list)
	routingMu    sync.RWMutex
	modelRouting map[string][]string

	// Model name map from Portal sync (provider → api_model_name → product_id)
	modelNameMapMu sync.RWMutex
	modelNameMap   map[string]map[string]string

	// Model capability map from Portal sync (provider → api_model_name → capabilities)
	modelCapabilityMu sync.RWMutex
	modelCapability   map[string]map[string]map[string]bool

	// Model type map from Portal sync (provider → api_model_name → "text"|"image"|"video"|"audio")
	modelType map[string]map[string]string

	// Provider health from Portal sync (for smart routing)
	healthMu       sync.RWMutex
	providerHealth map[string]ProviderHealthInfo

	// Per-key health from Portal sync (for smart routing)
	keyHealthMu sync.RWMutex
	keyHealth   map[string]KeyHealthInfo

	// Smart routing toggles from Portal
	smartMu      sync.RWMutex
	smartRouting map[string]bool

	// Per-model cooldown: keyID → model → cooldown_until
	// When upstream rate-limits a specific model, only that (key,model) is cooled,
	// not the entire key. Other models on the same key remain usable.
	modelCooldownsMu sync.RWMutex
	modelCooldowns   map[string]map[string]time.Time

	// Per-model consecutive failure counts feeding the 3-strike cooldown:
	// keyID → model → count. Unlike key.Failures (rolling, synced to portal,
	// used for scoring and the 10-min excessive-failures kill), this counter
	// is what triggers the short max-failures cooldown, scoped to one model
	// so a flaky model cannot take down sibling models on the same key.
	modelFailures map[string]map[string]int

	// Round-robin counters for model selection (in-memory fallback when Redis unavailable)
	roundRobinCounters sync.Map

	// Model-level health tracking: disables specific models on specific keys
	// when they repeatedly fail (e.g. upstream returns 401 for unsupported model).
	modelHealth *ModelHealth

	// Per-provider upstream dispatch queues.
	queueEnabled            bool
	queueWorkersPerProvider int
	queueSizePerProvider    int
	queueSyncFallback       bool
	queuesMu                sync.RWMutex
	queues                  map[string]*ProviderQueue
}

// NewManager creates a new key pool manager
func NewManager(cfg *config.Config, logger *zap.Logger) *Manager {
	encKey := cfg.Auth.EncryptionKey
	if encKey == "" {
		logger.Fatal("ENCRYPTION_KEY not set. Set ENCRYPTION_KEY environment variable for keypool encryption. Falling back to JWT_SECRET is not allowed for security.")
	}

	m := &Manager{
		locks:           make(map[string]*providerLock),
		pools:           make(map[string]*ProviderPool),
		providerConfigs: make(map[string]ProviderConfigEntry),
		// upstreamUsageShards is an array; each shard's map is initialized lazily.
		cfg:                     cfg,
		logger:                  logger,
		portalURL:               cfg.Portal.URL,
		portalSecret:            cfg.Portal.SyncSecret,
		syncInterval:            int64(cfg.Portal.SyncInterval),
		healthCheckInterval:     cfg.Portal.HealthCheckInterval,
		redisKey:                "ai-proxy:keypool:keys",
		encryptionKey:           encKey,
		modelHealth:             NewModelHealth(logger),
		queueEnabled:            cfg.Queue.Enabled,
		queueWorkersPerProvider: cfg.Queue.WorkersPerProvider,
		queueSizePerProvider:    cfg.Queue.SizePerProvider,
		queueSyncFallback:       cfg.Queue.SyncFallback,
		queues:                  make(map[string]*ProviderQueue),
	}
	m.proxyID.Store("go-proxy-1")
	m.proxyRegion.Store("default")

	// Initialize Redis client only if configured
	if cfg.Redis.Host != "" {
		m.redis = redis.NewClient(cfg.Redis.Options())
		logger.Info("Key pool Redis client initialized")
	} else {
		logger.Info("Key pool running without Redis (local memory only)")
	}

	return m
}

// GetLastSync returns the timestamp of the last successful Portal sync.
func (m *Manager) GetLastSync() time.Time {
	m.syncStateMu.RLock()
	defer m.syncStateMu.RUnlock()
	return m.lastSync
}

// GetConfigVersion returns the config version from the last successful Portal sync.
func (m *Manager) GetConfigVersion() string {
	m.syncStateMu.RLock()
	defer m.syncStateMu.RUnlock()
	return m.configVersion
}

func (m *Manager) setConfigVersion(v string) {
	m.syncStateMu.Lock()
	defer m.syncStateMu.Unlock()
	m.configVersion = v
}

func (m *Manager) setLastSync(t time.Time) {
	m.syncStateMu.Lock()
	defer m.syncStateMu.Unlock()
	m.lastSync = t
}

// fnv64a hashes a string using the FNV-1a 64-bit algorithm.
func fnv64a(s string) uint64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	h := uint64(offset64)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime64
	}
	return h
}

// upstreamUsageShardFor returns the shard that owns the given provider:keyID key.
func (m *Manager) upstreamUsageShardFor(k string) *upstreamUsageShard {
	return &m.upstreamUsageShards[fnv64a(k)&(upstreamUsageShardCount-1)]
}

// getUpstreamUsage returns the existing usage entry for k, or nil if absent.
// The caller must NOT hold the shard lock.
func (m *Manager) getUpstreamUsage(k string) *upstreamUsageEntry {
	shard := m.upstreamUsageShardFor(k)
	shard.mu.RLock()
	usage := shard.entries[k]
	shard.mu.RUnlock()
	return usage
}

// getOrCreateUpstreamUsage returns the usage entry for k, creating it if absent.
// The caller must NOT hold the shard lock.
func (m *Manager) getOrCreateUpstreamUsage(k string) *upstreamUsageEntry {
	shard := m.upstreamUsageShardFor(k)
	shard.mu.RLock()
	if shard.entries != nil {
		usage := shard.entries[k]
		shard.mu.RUnlock()
		if usage != nil {
			return usage
		}
	} else {
		shard.mu.RUnlock()
	}

	shard.mu.Lock()
	if shard.entries == nil {
		shard.entries = make(map[string]*upstreamUsageEntry)
	}
	usage := shard.entries[k]
	if usage == nil {
		usage = &upstreamUsageEntry{windowEnd: time.Now().Add(time.Minute)}
		shard.entries[k] = usage
	}
	shard.mu.Unlock()
	return usage
}

// getLock returns the lock for a specific provider (creates if not exists)
func (m *Manager) getLock(provider string) *providerLock {
	m.locksMu.RLock()
	lock, ok := m.locks[provider]
	m.locksMu.RUnlock()

	if ok {
		return lock
	}

	// Create new lock
	m.locksMu.Lock()
	defer m.locksMu.Unlock()

	// Double check after acquiring write lock
	if lock, ok := m.locks[provider]; ok {
		return lock
	}

	lock = &providerLock{}
	m.locks[provider] = lock
	return lock
}

// getPool returns the pool for a provider (thread-safe)
func (m *Manager) getPool(provider string) (*ProviderPool, bool) {
	m.poolsMu.RLock()
	defer m.poolsMu.RUnlock()
	pool, ok := m.pools[provider]
	return pool, ok
}

// setPool sets the pool for a provider (thread-safe)
func (m *Manager) setPool(provider string, pool *ProviderPool) {
	m.poolsMu.Lock()
	defer m.poolsMu.Unlock()
	m.pools[provider] = pool
}

// getProxyID returns the proxy ID (configurable)
func (m *Manager) getProxyID() string {
	if v, ok := m.proxyID.Load().(string); ok && v != "" {
		return v
	}
	return "go-proxy-1"
}

// getProxyRegion returns the proxy region (configurable)
func (m *Manager) getProxyRegion() string {
	if v, ok := m.proxyRegion.Load().(string); ok && v != "" {
		return v
	}
	return "default"
}

// SetProxyInfo sets proxy ID and region
func (m *Manager) SetProxyInfo(id, region string) {
	m.proxyID.Store(id)
	m.proxyRegion.Store(region)
}

// GetKey selects a key for a provider using LRU strategy
func (m *Manager) GetKey(provider string) *KeyInternal {
	return m.GetKeyWithContext(context.Background(), provider)
}

// GetKeyWithContext selects a key with context support
func (m *Manager) GetKeyWithContext(ctx context.Context, provider string) *KeyInternal {
	return m.GetKeyByPriority(ctx, provider, "normal", "", "")
}

// RecordModelSuccess records a successful request for a model on a specific key.
// This clears any accumulated model-level failures.
func (m *Manager) RecordModelSuccess(keyID, model string) {
	if m.modelHealth != nil {
		m.modelHealth.RecordSuccess(keyID, model)
	}
}

// ReportModelFailure records a model-level failure (e.g. 400/401/404 indicating
// the model is not supported on this key). After repeated failures, the model
// is temporarily disabled on this key without affecting other models.
// Returns true if the model was just disabled.
func (m *Manager) ReportModelFailure(keyID, model, errorMsg string) bool {
	if m.modelHealth != nil {
		return m.modelHealth.RecordFailure(keyID, model, errorMsg)
	}
	return false
}

// IsModelHealthy checks if a specific model is available on a key.
func (m *Manager) IsModelHealthy(keyID, model string) bool {
	if m.modelHealth == nil {
		return true
	}
	return m.modelHealth.IsModelAvailable(keyID, model)
}

// GetModelHealthStats returns model health summary stats.
func (m *Manager) GetModelHealthStats() (total, disabled int) {
	if m.modelHealth == nil {
		return 0, 0
	}
	return m.modelHealth.Stats()
}

// GetDisabledModelsForKey returns models currently disabled on a specific key.
func (m *Manager) GetDisabledModelsForKey(keyID string) []string {
	if m.modelHealth == nil {
		return nil
	}
	return m.modelHealth.DisabledModels(keyID)
}

// ResetModelHealth clears model health state for a specific key+model combination.
func (m *Manager) ResetModelHealth(keyID, model string) {
	if m.modelHealth != nil {
		m.modelHealth.Reset(keyID, model)
	}
}

// ResetKeyRuntimeState clears all runtime penalty state for a single key:
// key-level cooldown/rate-limit timers and failure counters, per-model
// cooldowns, and per-model health disable flags. Locally killed keys are
// revived only when the key itself is still enabled (an operator-disabled
// key stays disabled). This lets operators recover a pool after an upstream
// incident (e.g. an RPM probe that tripped every key) without restarting the
// proxy — hot config syncs only merge static fields and never clear this state.
func (m *Manager) ResetKeyRuntimeState(provider, keyID string) bool {
	lock := m.getLock(provider)
	lock.Lock()
	defer lock.Unlock()

	pool, ok := m.getPool(provider)
	if !ok {
		return false
	}

	found := false
	var matchedIDs []string
	for _, ik := range pool.internalKeys {
		if ik.ID != keyID && !strings.Contains(ik.KeyPreview, keyID) {
			continue
		}
		found = true
		matchedIDs = append(matchedIDs, ik.ID)
		ik.CooldownUntil = time.Time{}
		ik.RateLimitUntil = time.Time{}
		ik.Failures = 0
		ik.FailureHistory = nil
		ik.InactiveInfo = nil
		if ik.Enabled && (ik.Status == "cooldown" || ik.Status == "inactive") {
			ik.Status = "active"
			ik.StatusHistory = append(ik.StatusHistory, StatusEntry{
				Status: "active",
				At:     time.Now(),
				Reason: "RUNTIME_RESET",
			})
		}
	}
	if !found {
		return false
	}

	m.modelCooldownsMu.Lock()
	for _, id := range matchedIDs {
		delete(m.modelCooldowns, id)
		delete(m.modelFailures, id)
	}
	m.modelCooldownsMu.Unlock()

	// Also drop the RPM/TPM rate-window entries so the key starts with a fresh
	// budget. The counters only feed checkUpstreamAvailable (rate gating) — not
	// billing or usage reporting — and the window would self-expire within 60s
	// anyway; an operator reset is meant to restore the key immediately.
	for _, id := range matchedIDs {
		usageKey := provider + ":" + id
		shard := m.upstreamUsageShardFor(usageKey)
		shard.mu.Lock()
		delete(shard.entries, usageKey)
		shard.mu.Unlock()
	}
	if m.modelHealth != nil {
		m.modelHealth.ResetKey(keyID)
	}

	m.logger.Info("Key runtime state reset",
		zap.String("provider", provider),
		zap.String("key_id", keyID),
	)
	return true
}

// StartPubSubListener starts a Redis Pub/Sub listener for real-time sync notifications.
// When a message is received on the sync channel, it triggers an immediate sync from Portal.
// This complements the periodic HTTP polling with near-instant updates.
func (m *Manager) StartPubSubListener(ctx context.Context) {
	if m.redis == nil {
		m.logger.Info("Redis Pub/Sub listener not started: no Redis configured")
		return
	}

	go func() {
		m.logger.Info("Starting Redis Pub/Sub listener", zap.String("channel", "ai-proxy:sync"))

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			pubsub := m.redis.Subscribe(ctx, "ai-proxy:sync")

			// Wait for confirmation that subscription is created
			_, err := pubsub.Receive(ctx)
			if err != nil {
				m.logger.Error("Pub/Sub subscription failed, retrying in 5s", zap.Error(err))
				_ = pubsub.Close()
				select {
				case <-ctx.Done():
					return
				case <-time.After(5 * time.Second):
				}
				continue
			}

			m.logger.Info("Pub/Sub subscription active")

			// Consume messages
			ch := pubsub.Channel()
			for msg := range ch {
				m.logger.Info("Received sync notification from Pub/Sub",
					zap.String("payload", msg.Payload),
				)

				syncCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				if err := m.SyncFromPortalWithContext(syncCtx); err != nil {
					m.logger.Error("Pub/Sub triggered sync failed", zap.Error(err))
				}
				cancel()
			}

			// Channel closed, retry connection
			m.logger.Warn("Pub/Sub channel closed, reconnecting in 5s")
			_ = pubsub.Close()
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}()
}

// QueueEnabled reports whether per-provider upstream queues are enabled.
func (m *Manager) QueueEnabled() bool {
	return m.queueEnabled
}

// Dispatch enqueues a job for the given provider. If the queue is disabled,
// this method returns an error and the caller should fall back to inline
// dispatch.
func (m *Manager) Dispatch(ctx context.Context, provider string, job ProviderJob) (ProviderResult, error) {
	q := m.getOrCreateQueue(provider)
	return q.Dispatch(ctx, job)
}

func (m *Manager) getOrCreateQueue(provider string) *ProviderQueue {
	m.queuesMu.RLock()
	q, ok := m.queues[provider]
	m.queuesMu.RUnlock()
	if ok {
		return q
	}

	m.queuesMu.Lock()
	defer m.queuesMu.Unlock()
	if q, ok := m.queues[provider]; ok {
		return q
	}

	q = NewProviderQueue(provider, m.queueWorkersPerProvider, m.queueSizePerProvider, m.queueSyncFallback, m.logger)
	m.queues[provider] = q
	return q
}

// StopQueues stops all provider dispatch queues.
func (m *Manager) StopQueues() {
	m.queuesMu.Lock()
	defer m.queuesMu.Unlock()
	for _, q := range m.queues {
		q.Stop()
	}
}

// Close cleans up resources
func (m *Manager) Close() error {
	m.StopQueues()
	if m.redis != nil {
		return m.redis.Close()
	}
	return nil
}

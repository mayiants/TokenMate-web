package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ai-proxy/ai-proxy-go/dto"
	"github.com/ai-proxy/ai-proxy-go/pkg/metrics"
	"github.com/ai-proxy/ai-proxy-go/types"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// AffinityCache - Channel affinity cache for cache optimization
// Routes similar requests to same upstream key for better prompt cache hits
type AffinityCache struct {
	redis       *redis.Client
	redisPrefix string
	maxEntries  int
	defaultTTL  time.Duration
	localCache  sync.Map
	stats       sync.Map // map[string]*usageCacheStatsEntry
}

// usageCacheStatsEntry - Per-key usage cache statistics
type usageCacheStatsEntry struct {
	mu                   sync.Mutex
	RuleName             string
	UsingGroup           string
	KeyFingerprint       string
	CachedTokenRateMode  string
	Hit                  int64
	Total                int64
	WindowSeconds        int64
	PromptTokens         int64
	CompletionTokens     int64
	TotalTokens          int64
	CachedTokens         int64
	PromptCacheHitTokens int64
	LastSeenAt           time.Time
}

// AffinityConfig - Cache configuration
type AffinityConfig struct {
	Redis       *redis.Client
	RedisPrefix string
	MaxEntries  int
	DefaultTTL  time.Duration
}

// NewAffinityCache - Create affinity cache
func NewAffinityCache(ttl time.Duration) *AffinityCache {
	if ttl <= 0 {
		ttl = time.Hour
	}

	return &AffinityCache{
		redisPrefix: "ai-proxy:affinity:v2",
		maxEntries:  100000,
		defaultTTL:  ttl,
	}
}

// NewAffinityCacheWithConfig - Create affinity cache with full config
func NewAffinityCacheWithConfig(cfg *AffinityConfig) *AffinityCache {
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = 100000
	}
	if cfg.DefaultTTL <= 0 {
		cfg.DefaultTTL = time.Hour
	}
	if cfg.RedisPrefix == "" {
		cfg.RedisPrefix = "ai-proxy:affinity:v2"
	}

	return &AffinityCache{
		redis:       cfg.Redis,
		redisPrefix: cfg.RedisPrefix,
		maxEntries:  cfg.MaxEntries,
		defaultTTL:  cfg.DefaultTTL,
	}
}

// StartEviction starts a background goroutine that periodically trims the
// in-memory localCache and stats maps when they exceed maxEntries. Redis keys
// already have TTLs and are not managed here. The goroutine exits when ctx is
// cancelled.
func (c *AffinityCache) StartEviction(ctx context.Context, logger *zap.Logger) {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.evictExcess(ctx, logger)
			}
		}
	}()
}

// evictExcess removes random entries from localCache and stats when they grow
// beyond maxEntries. sync.Map does not support LRU, so random eviction is used
// as a simple memory bound.
func (c *AffinityCache) evictExcess(ctx context.Context, logger *zap.Logger) {
	evictedLocal := c.evictMap(&c.localCache, c.maxEntries)
	evictedStats := c.evictMap(&c.stats, c.maxEntries)
	if evictedLocal > 0 || evictedStats > 0 {
		logger.Debug("Affinity cache eviction completed",
			zap.Int("local_evicted", evictedLocal),
			zap.Int("stats_evicted", evictedStats))
	}
}

func (c *AffinityCache) evictMap(m *sync.Map, limit int) int {
	var count int
	m.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	if count <= limit {
		return 0
	}

	// Trim down to 90% of the limit to avoid evicting on every tick.
	target := int(float64(limit) * 0.9)
	if target < 0 {
		target = 0
	}
	toDelete := count - target
	if toDelete <= 0 {
		return 0
	}

	deleted := 0
	m.Range(func(key, _ interface{}) bool {
		if deleted >= toDelete {
			return false
		}
		m.Delete(key)
		deleted++
		return true
	})
	return deleted
}

// AffinityRule - Rule for channel affinity
type AffinityRule struct {
	Name            string
	ModelRegex      []string
	PathRegex       []string
	KeySourcePath   string
	TTLSeconds      int
	SkipRetryOnFail bool
}

// buildAffinityKey - Build unified cache key:
// track:priority:model:group:fingerprint, where the fingerprint is the
// conversation seed (system prompt + first message) so a session sticks
// to one upstream key; falls back to a body hash for non-chat requests.
func (c *AffinityCache) buildAffinityKey(ctx *gin.Context, modelName string, usingGroup string, priority string) string {
	fingerprint := c.getRequestFingerprint(ctx)
	if fingerprint == "" {
		return ""
	}
	// Include routing track for dual-track cache isolation
	track := ctx.GetString("routing_track")
	if track == "" {
		track = "standard"
	}
	return fmt.Sprintf("%s:%s:%s:%s:%s", track, priority, modelName, usingGroup, fingerprint)
}

// getRequestFingerprint returns a stable fingerprint for affinity routing.
// Prefers the conversation seed (system prompt + first message) so every
// turn of the same session maps to the same upstream key; falls back to a
// full-body hash when the body has no parseable messages (e.g. non-chat
// endpoints).
func (c *AffinityCache) getRequestFingerprint(ctx *gin.Context) string {
	body, exists := ctx.Get("request_body")
	if !exists {
		return ""
	}
	var bodyBytes []byte
	switch v := body.(type) {
	case []byte:
		bodyBytes = v
	case string:
		bodyBytes = []byte(v)
	default:
		return ""
	}
	if fp := conversationPrefixFingerprint(bodyBytes); fp != "" {
		return fp
	}
	hash := sha256.Sum256(bodyBytes)
	return hex.EncodeToString(hash[:])[:8]
}

// conversationPrefixFingerprint hashes the conversation seed: the system
// prompt (top-level "system" field, Anthropic-style, or a leading system
// message) plus the first message. Coding agents resend a growing history
// each turn, but this prefix is stable within a session, so the session
// sticks to one upstream key and prompt caches stay warm. Works for both
// OpenAI and Anthropic request shapes.
func conversationPrefixFingerprint(body []byte) string {
	var probe struct {
		System   json.RawMessage   `json:"system"`
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(body, &probe); err != nil || len(probe.Messages) == 0 {
		return ""
	}

	var sb strings.Builder
	if len(probe.System) > 0 {
		sb.WriteString(truncateForFingerprint(extractTextForFingerprint(probe.System)))
	}

	// Leading system messages join the system part; the first non-system
	// message anchors the session. This makes OpenAI-shaped
	// (system inside messages) and Anthropic-shaped (top-level system)
	// requests of the same conversation fingerprint identically.
	var firstRole string
	var firstContent json.RawMessage
	for _, raw := range probe.Messages {
		var m struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			return ""
		}
		if m.Role == "system" {
			sb.WriteString(truncateForFingerprint(extractTextForFingerprint(m.Content)))
			continue
		}
		firstRole, firstContent = m.Role, m.Content
		break
	}
	if firstRole == "" {
		return ""
	}
	sb.WriteString("|")
	sb.WriteString(firstRole)
	sb.WriteString(":")
	sb.WriteString(truncateForFingerprint(extractTextForFingerprint(firstContent)))

	if sb.String() == "|"+firstRole+":" {
		return ""
	}
	return affinityFingerprint(sb.String())
}

// extractTextForFingerprint pulls text out of a message/system content
// field, which may be a plain string or an array of content parts.
func extractTextForFingerprint(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		var sb strings.Builder
		for _, p := range parts {
			sb.WriteString(p.Text)
		}
		return sb.String()
	}
	return ""
}

func truncateForFingerprint(s string) string {
	if len(s) > 2048 {
		return s[:2048]
	}
	return s
}

// GetPreferredKeyID returns the preferred upstream key ID for affinity routing.
// Uses unified key format: priority:model:group:sha1(body)[:8]
func (c *AffinityCache) GetPreferredKeyID(ctx *gin.Context, modelName string, usingGroup string, priority string) string {
	cacheKey := c.buildAffinityKey(ctx, modelName, usingGroup, priority)
	if cacheKey == "" {
		return ""
	}
	// Stash the resolved key so RecordPreferredKeyID reuses the exact same
	// key for this request (see the note there about model/track mutation).
	ctx.Set(GinKeyAffinityCacheKey, cacheKey)

	// Check local cache first
	if val, ok := c.localCache.Load(cacheKey); ok {
		if keyID, ok := val.(string); ok && keyID != "" {
			metrics.GetMetrics().IncrementCacheHits(1)
			return keyID
		}
	}

	// Check Redis if available
	if c.redis != nil {
		result, err := c.redis.Get(context.Background(), c.redisPrefix+":"+cacheKey).Result()
		if err == nil && result != "" {
			c.localCache.Store(cacheKey, result)
			metrics.GetMetrics().IncrementCacheHits(1)
			return result
		}
	}

	metrics.GetMetrics().IncrementCacheMisses(1)
	return ""
}

// RecordPreferredKeyID records a successful upstream key ID for future affinity.
// Uses unified key format: priority:model:group:sha1(body)[:8]
func (c *AffinityCache) RecordPreferredKeyID(ctx *gin.Context, modelName string, usingGroup string, keyID string, priority string) {
	if keyID == "" {
		return
	}

	// Reuse the exact key GetPreferredKeyID computed for this request so the
	// two always agree. The handler replaces req.Model with the upstream model
	// ID and only sets the routing track *after* the Get call, so rebuilding
	// the key here from the mutated model/track never matched the Get lookup —
	// affinity never stuck and sessions fell back to round-robin. Fall back to
	// rebuilding only when Get never ran (no stashed key).
	cacheKey := ctx.GetString(GinKeyAffinityCacheKey)
	if cacheKey == "" {
		cacheKey = c.buildAffinityKey(ctx, modelName, usingGroup, priority)
	}
	if cacheKey == "" {
		return
	}

	c.localCache.Store(cacheKey, keyID)

	if c.redis != nil {
		c.redis.Set(context.Background(), c.redisPrefix+":"+cacheKey, keyID, c.defaultTTL)
	}
}

// === Stats & utilities ===

// UsageCacheStats - Usage cache statistics
type UsageCacheStats struct {
	RuleName            string `json:"rule_name"`
	UsingGroup          string `json:"using_group"`
	KeyFingerprint      string `json:"key_fp"`
	CachedTokenRateMode string `json:"cached_token_rate_mode"`

	Hit           int64 `json:"hit"`
	Total         int64 `json:"total"`
	WindowSeconds int64 `json:"window_seconds"`

	PromptTokens         int64 `json:"prompt_tokens"`
	CompletionTokens     int64 `json:"completion_tokens"`
	TotalTokens          int64 `json:"total_tokens"`
	CachedTokens         int64 `json:"cached_tokens"`
	PromptCacheHitTokens int64 `json:"prompt_cache_hit_tokens"`
	LastSeenAt           int64 `json:"last_seen_at"`
}

// ObserveUsageCache - Track cache hit rates
func (c *AffinityCache) ObserveUsageCache(ctx *gin.Context, usage *dto.Usage, relayFormat types.RelayFormat) {
	if usage == nil {
		return
	}

	mode := cachedTokenRateModeByRelayFormat(relayFormat)

	cachedTokens := int64(usage.PromptTokensDetails.CachedTokens)
	if cachedTokens == 0 && usage.PromptCacheHitTokens > 0 {
		cachedTokens = int64(usage.PromptCacheHitTokens)
	}

	ruleName := ctx.GetString(GinKeyAffinityMeta)
	if ruleName == "" {
		ruleName = "default"
	}
	usingGroup := "default"
	keyFingerprint := ctx.GetString("request_fingerprint")
	if keyFingerprint == "" {
		keyFingerprint = "unknown"
	}

	statsKey := fmt.Sprintf("%s:%s:%s", ruleName, usingGroup, keyFingerprint)

	val, _ := c.stats.LoadOrStore(statsKey, &usageCacheStatsEntry{
		RuleName:            ruleName,
		UsingGroup:          usingGroup,
		KeyFingerprint:      keyFingerprint,
		CachedTokenRateMode: mode,
	})

	entry := val.(*usageCacheStatsEntry)
	entry.mu.Lock()
	defer entry.mu.Unlock()

	entry.Total++
	if cachedTokens > 0 {
		entry.Hit++
	}
	entry.PromptTokens += int64(usage.PromptTokens)
	entry.CompletionTokens += int64(usage.CompletionTokens)
	entry.TotalTokens += int64(usage.TotalTokens)
	entry.CachedTokens += cachedTokens
	entry.PromptCacheHitTokens += int64(usage.PromptCacheHitTokens)
	entry.LastSeenAt = time.Now()
}

// cachedTokenRateModeByRelayFormat - Get cache mode by relay format
func cachedTokenRateModeByRelayFormat(format types.RelayFormat) string {
	switch format {
	case types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses:
		return "cached_over_prompt"
	case types.RelayFormatClaude:
		return "cached_over_prompt_plus_cached"
	default:
		return ""
	}
}

// Clear - Clear all affinity cache
func (c *AffinityCache) Clear() int {
	count := 0
	c.localCache.Range(func(key, value interface{}) bool {
		c.localCache.Delete(key)
		count++
		return true
	})
	if c.redis != nil {
		iter := c.redis.Scan(context.Background(), 0, c.redisPrefix+":*", 0).Iterator()
		for iter.Next(context.Background()) {
			c.redis.Del(context.Background(), iter.Val())
			count++
		}
	}
	return count
}

// Stats - Get cache statistics
func (c *AffinityCache) Stats() map[string]interface{} {
	stats := map[string]interface{}{
		"max_entries": c.maxEntries,
		"default_ttl": c.defaultTTL.String(),
	}

	localCount := 0
	c.localCache.Range(func(key, value interface{}) bool {
		localCount++
		return true
	})
	stats["local_entries"] = localCount

	if c.redis != nil {
		var count int64
		iter := c.redis.Scan(context.Background(), 0, c.redisPrefix+":*", 100).Iterator()
		for iter.Next(context.Background()) {
			count++
		}
		if iter.Err() == nil {
			stats["redis_entries"] = count
		}
	}

	var totalHits, totalRequests, totalCachedTokens int64
	c.stats.Range(func(key, value interface{}) bool {
		entry := value.(*usageCacheStatsEntry)
		entry.mu.Lock()
		totalHits += entry.Hit
		totalRequests += entry.Total
		totalCachedTokens += entry.CachedTokens
		entry.mu.Unlock()
		return true
	})

	if totalRequests > 0 {
		stats["cache_hit_rate"] = float64(totalHits) / float64(totalRequests)
		stats["total_cache_requests"] = totalRequests
		stats["total_cached_tokens"] = totalCachedTokens
	}

	return stats
}

// messagesFingerprint - Create fingerprint from messages
// affinityFingerprint - Create short fingerprint
func affinityFingerprint(s string) string {
	if s == "" {
		return ""
	}
	hash := sha256.Sum256([]byte(s))
	h := hex.EncodeToString(hash[:])
	if len(h) >= 8 {
		return h[:8]
	}
	return h
}

// Context keys for storing affinity info
const (
	GinKeyAffinityCacheKey   = "affinity_cache_key"
	GinKeyAffinityTTLSeconds = "affinity_ttl_seconds"
	GinKeyAffinityMeta       = "affinity_meta"
	GinKeyAffinitySkipRetry  = "affinity_skip_retry"
)

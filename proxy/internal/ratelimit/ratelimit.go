package ratelimit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ai-proxy/ai-proxy-go/dto"
	"github.com/ai-proxy/ai-proxy-go/internal/config"
	"github.com/ai-proxy/ai-proxy-go/pkg/convert"
	"github.com/ai-proxy/ai-proxy-go/pkg/token"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// maxLocalCounterIdleAge is the absolute maximum time an inactive local
// counter will be retained. Even weekly counters are evicted after this long
// without activity; Redis (if configured) remains the durable source of truth.
const maxLocalCounterIdleAge = 24 * time.Hour

// lockFreeBucket - Local token bucket protected by a mutex.
// The previous CAS-based implementation could over-consume tokens under contention
// because the token check was performed before acquiring the update lock.
type lockFreeBucket struct {
	mu         sync.Mutex
	tokens     float64
	lastUpdate int64
}

// newLockFreeBucket creates a new bucket initialized to maxTokens.
func newLockFreeBucket(maxTokens float64) *lockFreeBucket {
	return &lockFreeBucket{
		tokens:     maxTokens,
		lastUpdate: time.Now().UnixNano(),
	}
}

// AllowN checks if the request is allowed, consuming N tokens.
func (b *lockFreeBucket) AllowN(rate, maxTokens float64, n int) bool {
	if n <= 0 {
		return true
	}

	requestTokens := float64(n)

	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now().UnixNano()
	elapsedSeconds := float64(now-b.lastUpdate) / 1e9
	b.tokens = min(maxTokens, b.tokens+elapsedSeconds*rate)
	b.lastUpdate = now

	if b.tokens < requestTokens {
		return false
	}

	b.tokens -= requestTokens
	return true
}

// localCounter - Local token bucket counter for fast path (lock-free)
type localCounter struct {
	buckets   sync.Map // key -> *lockFreeBucket (concurrent safe)
	rate      float64  // tokens per second
	maxTokens float64
	window    time.Duration
	lastUsed  atomic.Int64 // unix nano of last AllowN call
}

func newLocalCounter(maxTokens int, windowSize time.Duration) *localCounter {
	// Calculate tokens per second
	rate := float64(maxTokens) / windowSize.Seconds()
	lc := &localCounter{
		rate:      rate,
		maxTokens: float64(maxTokens),
		window:    windowSize,
	}
	lc.touch()
	return lc
}

// touch marks the counter as recently used.
func (lc *localCounter) touch() {
	lc.lastUsed.Store(time.Now().UnixNano())
}

// stale reports whether the counter has had no activity for longer than
// twice its window (capped at maxLocalCounterIdleAge).
func (lc *localCounter) stale() bool {
	idle := time.Duration(time.Now().UnixNano() - lc.lastUsed.Load())
	limit := 2 * lc.window
	if limit > maxLocalCounterIdleAge {
		limit = maxLocalCounterIdleAge
	}
	return idle > limit
}

// AllowN - Check if request is allowed consuming N tokens (lock-free)
func (lc *localCounter) AllowN(key string, n int) bool {
	if n <= 0 {
		return true
	}

	lc.touch()

	// LoadOrStore is atomic, no global lock needed
	v, _ := lc.buckets.LoadOrStore(key, newLockFreeBucket(lc.maxTokens))
	bucket := v.(*lockFreeBucket)

	return bucket.AllowN(lc.rate, lc.maxTokens, n)
}

// Allow - Check if request is allowed consuming 1 token
func (lc *localCounter) Allow(key string) bool {
	return lc.AllowN(key, 1)
}

// Middleware - Rate limiting middleware
type Middleware struct {
	cfg    *config.Config
	logger *zap.Logger
	redis  *redis.Client

	// Local counters keyed by "rpm:<value>" or "tpm:<value>" (created on-demand)
	localCounters sync.Map // string -> *localCounter

	// Concurrent request counters per key hash
	concurrentCounters sync.Map // string -> *atomic.Int32

	// Async sync channel
	syncChan chan syncRequest

	// Token counter for request-body token estimation.
	tokenCounter *token.Counter
}

type syncRequest struct {
	key         string
	limit       int
	delta       int64
	counterType string // "rpm", "tpm", "rph", "tph", "rp5h", "rpw", "rpd", "tpd", "tpw", "tpmonth"
}

// NewMiddleware - Create rate limit middleware
func NewMiddleware(cfg *config.Config, logger *zap.Logger) *Middleware {
	// Initialize Redis client (only if configured)
	var redisClient *redis.Client
	if cfg.Redis.Host != "" {
		redisClient = redis.NewClient(cfg.Redis.Options())
	}

	m := &Middleware{
		cfg:          cfg,
		logger:       logger,
		redis:        redisClient,
		syncChan:     make(chan syncRequest, 10000),
		tokenCounter: token.NewCounter(),
	}

	// Start async sync goroutine (only if Redis is available)
	if redisClient != nil {
		go m.asyncSyncLoop()
	}

	// Start concurrent counter cleanup loop
	go m.concurrentCleanupLoop()

	// Start local token-bucket counter cleanup loop to bound memory growth.
	go m.localCounterCleanupLoop()

	logger.Info("Rate limit middleware initialized",
		zap.Int("default_rpm", cfg.RateLimit.DefaultRPM),
		zap.Int("default_tpm", cfg.RateLimit.DefaultTPM),
		zap.Bool("redis_enabled", redisClient != nil),
		zap.Int("sync_chan_size", 10000),
	)

	return m
}

// getOrCreateCounter - Get or lazily create a local counter for given limit
func (m *Middleware) getOrCreateCounter(counterKey string, limit int, window time.Duration) *localCounter {
	if v, ok := m.localCounters.Load(counterKey); ok {
		return v.(*localCounter)
	}
	counter := newLocalCounter(limit, window)
	v, _ := m.localCounters.LoadOrStore(counterKey, counter)
	return v.(*localCounter)
}

// minPositive returns the smallest positive (non-zero) value among the
// provided integers. If all are zero, it returns 0.
func minPositive(values ...int) int {
	result := 0
	for _, v := range values {
		if v > 0 && (result == 0 || v < result) {
			result = v
		}
	}
	return result
}

// estimateRequestTokens reads the request body and returns a conservative
// estimate of tokens this request may consume. Returns 0 on parse failure so
// that estimation never blocks a request by itself.
func (m *Middleware) estimateRequestTokens(c *gin.Context) int64 {
	if c.Request == nil || c.Request.Body == nil {
		return 0
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return 0
	}
	// Restore body so downstream handlers can read it again.
	c.Request.Body = io.NopCloser(bytes.NewBuffer(body))

	if len(body) == 0 {
		return 0
	}

	path := c.Request.URL.Path
	switch {
	case strings.HasSuffix(path, "/chat/completions"):
		var req dto.GeneralOpenAIRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return 0
		}
		var estimated int64
		if len(req.Messages) > 0 {
			estimated = int64(m.tokenCounter.EstimateMessages(req.Messages, req.Model))
		}
		if req.MaxTokens != nil && *req.MaxTokens > 0 {
			estimated += int64(*req.MaxTokens)
		}
		return estimated

	case strings.HasSuffix(path, "/responses"):
		var req convert.ResponsesRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return 0
		}
		hubReq, err := convert.ResponsesToOpenAI(&req)
		if err != nil {
			return 0
		}
		var estimated int64
		if len(hubReq.Messages) > 0 {
			estimated = int64(m.tokenCounter.EstimateMessages(hubReq.Messages, req.Model))
		}
		if hubReq.MaxTokens != nil && *hubReq.MaxTokens > 0 {
			estimated += int64(*hubReq.MaxTokens)
		}
		return estimated

	case strings.HasSuffix(path, "/messages"):
		// Anthropic Messages API — Claude clients. Convert to the OpenAI
		// hub format and estimate with the regular OpenAI estimator so the
		// pre-request TPM/TPH/TPMonth pre-checks are enforced; previously
		// /v1/messages and /v1/completions fell through to default 0, which
		// let Claude clients bypass the TPM hard block (S-P5).
		var req convert.AnthropicRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return 0
		}
		hubReq, err := convert.AnthropicToOpenAI(&req)
		if err != nil {
			return 0
		}
		var estimated int64
		if hubReq != nil && len(hubReq.Messages) > 0 {
			estimated = int64(m.tokenCounter.EstimateMessages(hubReq.Messages, req.Model))
		}
		if req.MaxTokens != nil && *req.MaxTokens > 0 {
			estimated += int64(*req.MaxTokens)
		}
		return estimated

	case strings.HasSuffix(path, "/completions"):
		// Legacy OpenAI /v1/completions (text completions). The handler is
		// deprecated today, but still key the token estimator here so a
		// re-enablement cannot bypass TPM hard pre-checks (S-P5).
		var req struct {
			Model     string      `json:"model"`
			Prompt    interface{} `json:"prompt"`
			MaxTokens *int        `json:"max_tokens"`
			Stop      []string    `json:"stop"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			return 0
		}
		var text string
		switch v := req.Prompt.(type) {
		case string:
			text = v
		case []interface{}:
			for _, item := range v {
				if s, ok := item.(string); ok {
					text += s + " "
				}
			}
		}
		var estimated int64
		if text != "" {
			estimated = int64(token.CountTextToken(text, req.Model))
		}
		if req.MaxTokens != nil && *req.MaxTokens > 0 {
			estimated += int64(*req.MaxTokens)
		}
		return estimated

	case strings.HasSuffix(path, "/embeddings"):
		var req struct {
			Model string      `json:"model"`
			Input interface{} `json:"input"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			return 0
		}
		var text string
		switch v := req.Input.(type) {
		case string:
			text = v
		case []interface{}:
			for _, item := range v {
				if s, ok := item.(string); ok {
					text += s + " "
				}
			}
		}
		if text == "" {
			return 0
		}
		return int64(token.CountTextToken(text, req.Model))

	case strings.HasSuffix(path, "/images/generations"):
		return 1000

	case strings.Contains(path, "/audio/"):
		return 256

	default:
		return 0
	}
}

// Handler - Rate limiting middleware handler.
//
// Enforcement matrix (product-approved):
//
//	Hard (request blocked before upstream dispatch):
//	  RPM, RPH, RP5H, RPW, TPM, TPH, TPMonth, Concurrent
//	Soft (counted for admin utilization, never blocks):
//	  RPD, TPD, TPW, RPMonth, upstream quota
//
// Token dimensions use a pre-request estimate to hard-block; actual usage is
// reconciled after the request so utilization counters remain accurate.
//
// Dimension model (F5 fix):
//
//	Counter keys are partitioned by *dimension* so the enforced scope of
//	each limit matches its semantics — swapping an API key can no longer
//	reset subscription/group quotas:
//	  key    — counted per individual key (keyHash); applies to the
//	           key-level limits set in RateLimit{RPM,TPM} (default fallback).
//	  sub    — counted per user (claims.UserID); applies to all
//	           subscription_* limits a plan enforces across all keys owned
//	           by that user.
//	  group  — counted per group (claims.GroupID); shared by every key in
//	           the group.
//	Each non-zero limit is checked independently and the request is
//	rejected if *any* dimension blocks it. A key's own counter is still
//	incremented before a stricter sub/group check runs — that is correct:
//	the counter records "this key attempted this request", not "this
//	request was relayed upstream".
func (m *Middleware) Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !m.cfg.RateLimit.Enabled {
			c.Next()
			return
		}

		userID := c.GetString("user_id")
		apiKeyID := c.GetString("api_key_id")
		keyHash := c.GetString("key_hash")
		keyType := c.GetString("key_type")
		groupID := c.GetString("group_id")

		prefix := m.cfg.RateLimit.RedisPrefix
		keyRedisKey := prefix + "key:" + keyHash
		subRedisKey := prefix + "sub:" + userID
		groupRedisKey := prefix + "group:" + groupID

		block := func(counterType, code, msg string, limit int, dim string) {
			m.logger.Warn("Rate limit exceeded",
				zap.String("user_id", userID),
				zap.String("api_key_id", apiKeyID),
				zap.String("key_hash", keyHash),
				zap.String("key_type", keyType),
				zap.String("group_id", groupID),
				zap.String("counter_type", counterType),
				zap.Int("limit", limit),
				zap.String("dimension", dim),
			)
			c.Header("Retry-After", m.retryAfter(counterType))
			// Exposed for the failure-audit middleware, which runs outside this handler.
			c.Set("error_message", msg)
			c.Set("error_code", code)
			c.AbortWithStatusJSON(429, gin.H{
				"error": gin.H{
					"message": msg,
					"type":    "rate_limit_error",
					"code":    code,
				},
			})
		}

		// Key-level limits (per individual key). Defaults apply when the
		// Portal did not attach a per-key value.
		keyRPM := c.GetInt("rate_limit_rpm")
		if keyRPM <= 0 {
			keyRPM = m.cfg.RateLimit.DefaultRPM
		}
		keyTPM := c.GetInt("rate_limit_tpm")
		if keyTPM <= 0 {
			keyTPM = m.cfg.RateLimit.DefaultTPM
		}

		// Subscription limits (0 when the plan does not enforce them).
		subRPM := c.GetInt("sub_rpm_limit")
		subRPH := c.GetInt("sub_rph_limit")
		subRP5H := c.GetInt("sub_rp5h_limit")
		subRPW := c.GetInt("sub_rpw_limit")
		subRPD := c.GetInt("sub_rpd_limit")
		subTPM := c.GetInt("sub_tpm_limit")
		subTPH := c.GetInt("sub_tph_limit")
		subTPD := c.GetInt("sub_tpd_limit")
		subTPW := c.GetInt("sub_tpw_limit")
		subTPMonth := c.GetInt("sub_tpmonth_limit")
		subConcurrent := c.GetInt("sub_concurrent_limit")

		// Group limits (0 when not configured for this group).
		groupRPM := c.GetInt("group_rpm")
		groupRPH := c.GetInt("group_rph")
		groupRP5H := c.GetInt("group_rp5h")
		groupRPW := c.GetInt("group_rpw")
		groupTPM := c.GetInt("group_tpm")
		groupTPH := c.GetInt("group_tph")
		groupTPD := c.GetInt("group_tpd")
		groupTPW := c.GetInt("group_tpw")
		groupTPMonth := c.GetInt("group_tpmonth")
		groupConcurrent := c.GetInt("group_concurrent_limit")

		// --- Hard request counters (pre-request) ---

		// RPM — key, subscription, group (each independently enforced).
		if keyRPM > 0 && !m.AllowRPM(keyRedisKey, keyRPM) {
			block("rpm", "RATE_LIMIT_EXCEEDED",
				fmt.Sprintf("Rate limit exceeded (%d RPM). Please slow down your requests.", keyRPM),
				keyRPM, "key")
			return
		}
		if subRPM > 0 && !m.AllowRPM(subRedisKey, subRPM) {
			block("rpm", "RATE_LIMIT_EXCEEDED",
				fmt.Sprintf("Rate limit exceeded (%d RPM, subscription). Please slow down your requests.", subRPM),
				subRPM, "subscription")
			return
		}
		if groupRPM > 0 && !m.AllowRPM(groupRedisKey, groupRPM) {
			block("rpm", "RATE_LIMIT_EXCEEDED",
				fmt.Sprintf("Rate limit exceeded (%d RPM, group). Please slow down your requests.", groupRPM),
				groupRPM, "group")
			return
		}

		// RPH — subscription, group.
		if subRPH > 0 && !m.AllowRPH(subRedisKey, subRPH) {
			block("rph", "HOURLY_RATE_LIMIT_EXCEEDED",
				fmt.Sprintf("Hourly rate limit exceeded (%d RPH). Please try again later.", subRPH),
				subRPH, "subscription")
			return
		}
		if groupRPH > 0 && !m.AllowRPH(groupRedisKey, groupRPH) {
			block("rph", "HOURLY_RATE_LIMIT_EXCEEDED",
				fmt.Sprintf("Hourly rate limit exceeded (%d RPH, group). Please try again later.", groupRPH),
				groupRPH, "group")
			return
		}

		// RP5H — subscription, group.
		if subRP5H > 0 && !m.AllowRP5H(subRedisKey, subRP5H) {
			block("rp5h", "RATE_LIMIT_EXCEEDED",
				fmt.Sprintf("Rate limit exceeded (%d requests per 5 hours). Please try again later.", subRP5H),
				subRP5H, "subscription")
			return
		}
		if groupRP5H > 0 && !m.AllowRP5H(groupRedisKey, groupRP5H) {
			block("rp5h", "RATE_LIMIT_EXCEEDED",
				fmt.Sprintf("Rate limit exceeded (%d requests per 5 hours, group).", groupRP5H),
				groupRP5H, "group")
			return
		}

		// RPW — subscription, group.
		if subRPW > 0 && !m.AllowRPW(subRedisKey, subRPW) {
			block("rpw", "RATE_LIMIT_EXCEEDED",
				fmt.Sprintf("Rate limit exceeded (%d requests per week). Please try again later.", subRPW),
				subRPW, "subscription")
			return
		}
		if groupRPW > 0 && !m.AllowRPW(groupRedisKey, groupRPW) {
			block("rpw", "RATE_LIMIT_EXCEEDED",
				fmt.Sprintf("Rate limit exceeded (%d requests per week, group).", groupRPW),
				groupRPW, "group")
			return
		}

		// RPD is soft (subscription-only): record but never block.
		if subRPD > 0 && !m.AllowRPD(subRedisKey, subRPD) {
			m.logger.Warn("Daily request limit exceeded (soft, not blocking)",
				zap.String("user_id", userID),
				zap.String("api_key_id", apiKeyID),
				zap.String("key_hash", keyHash),
				zap.Int("sub_rpd", subRPD),
				zap.String("dimension", "subscription"),
			)
		}

		// --- Token estimation for hard pre-checks ---
		anyTokenHard := keyTPM > 0 || subTPM > 0 || groupTPM > 0 ||
			subTPH > 0 || groupTPH > 0 ||
			subTPMonth > 0 || groupTPMonth > 0
		var estimatedTokens int64
		if anyTokenHard {
			estimatedTokens = m.estimateRequestTokens(c)
		}

		// TPM — key, subscription, group.
		if keyTPM > 0 && estimatedTokens > 0 && !m.AllowTPM(keyRedisKey, keyTPM, estimatedTokens) {
			block("tpm", "TOKEN_RATE_LIMIT_EXCEEDED",
				fmt.Sprintf("Token rate limit exceeded (%d TPM). Please reduce request size or wait.", keyTPM),
				keyTPM, "key")
			return
		}
		if subTPM > 0 && estimatedTokens > 0 && !m.AllowTPM(subRedisKey, subTPM, estimatedTokens) {
			block("tpm", "TOKEN_RATE_LIMIT_EXCEEDED",
				fmt.Sprintf("Token rate limit exceeded (%d TPM, subscription). Please reduce request size or wait.", subTPM),
				subTPM, "subscription")
			return
		}
		if groupTPM > 0 && estimatedTokens > 0 && !m.AllowTPM(groupRedisKey, groupTPM, estimatedTokens) {
			block("tpm", "TOKEN_RATE_LIMIT_EXCEEDED",
				fmt.Sprintf("Token rate limit exceeded (%d TPM, group). Please reduce request size or wait.", groupTPM),
				groupTPM, "group")
			return
		}

		// TPH — subscription, group.
		if subTPH > 0 && estimatedTokens > 0 && !m.AllowTPH(subRedisKey, subTPH, estimatedTokens) {
			block("tph", "TOKEN_RATE_LIMIT_EXCEEDED",
				fmt.Sprintf("Hourly token rate limit exceeded (%d TPH, subscription). Please reduce request size or wait.", subTPH),
				subTPH, "subscription")
			return
		}
		if groupTPH > 0 && estimatedTokens > 0 && !m.AllowTPH(groupRedisKey, groupTPH, estimatedTokens) {
			block("tph", "TOKEN_RATE_LIMIT_EXCEEDED",
				fmt.Sprintf("Hourly token rate limit exceeded (%d TPH, group).", groupTPH),
				groupTPH, "group")
			return
		}

		// TPMonth — subscription, group.
		if subTPMonth > 0 && estimatedTokens > 0 && !m.AllowTPMonth(subRedisKey, subTPMonth, estimatedTokens) {
			block("tpmonth", "TOKEN_RATE_LIMIT_EXCEEDED",
				fmt.Sprintf("Monthly token rate limit exceeded (%d TPMonth, subscription). Please reduce request size or wait.", subTPMonth),
				subTPMonth, "subscription")
			return
		}
		if groupTPMonth > 0 && estimatedTokens > 0 && !m.AllowTPMonth(groupRedisKey, groupTPMonth, estimatedTokens) {
			block("tpmonth", "TOKEN_RATE_LIMIT_EXCEEDED",
				fmt.Sprintf("Monthly token rate limit exceeded (%d TPMonth, group).", groupTPMonth),
				groupTPMonth, "group")
			return
		}

		// --- Concurrent limit (hard) ---
		// Both subscription (per user) and group (per group) buckets may apply
		// at once. If the group bucket rejects a request that already acquired
		// the sub bucket, release the sub slot before returning so the count
		// does not leak.
		var acquiredConcurrent []string
		releaseAllConcurrent := func() {
			for _, k := range acquiredConcurrent {
				m.ReleaseConcurrent(k)
			}
			acquiredConcurrent = nil
		}
		if subConcurrent > 0 && keyType == "subscription" {
			if !m.AllowConcurrent(subRedisKey, subConcurrent) {
				block("concurrent", "CONCURRENT_LIMIT_EXCEEDED",
					fmt.Sprintf("Too many concurrent requests (%d max). Please wait and retry.", subConcurrent),
					subConcurrent, "subscription")
				return
			}
			acquiredConcurrent = append(acquiredConcurrent, subRedisKey)
		}
		if groupConcurrent > 0 {
			if !m.AllowConcurrent(groupRedisKey, groupConcurrent) {
				releaseAllConcurrent()
				block("concurrent", "CONCURRENT_LIMIT_EXCEEDED",
					fmt.Sprintf("Too many concurrent requests (%d max, group). Please wait and retry.", groupConcurrent),
					groupConcurrent, "group")
				return
			}
			acquiredConcurrent = append(acquiredConcurrent, groupRedisKey)
		}
		// Release every acquired concurrent slot via defer so a panic in a
		// downstream handler cannot leak any of them.
		if len(acquiredConcurrent) > 0 {
			defer releaseAllConcurrent()
		}

		c.Next()

		// --- Post-request reconciliation ---
		tokens := c.GetInt64("tokens_consumed")
		if tokens <= 0 {
			return
		}
		extra := tokens - estimatedTokens
		// Hard token counters were reserved with the estimate; consume the
		// delta when the actual usage exceeded the estimate.
		if extra > 0 {
			if keyTPM > 0 {
				m.RecordTokens(keyRedisKey, keyTPM, extra)
			}
			if subTPM > 0 {
				m.RecordTokens(subRedisKey, subTPM, extra)
			}
			if groupTPM > 0 {
				m.RecordTokens(groupRedisKey, groupTPM, extra)
			}
			if subTPH > 0 {
				m.CheckTPH(subRedisKey, subTPH, extra)
			}
			if groupTPH > 0 {
				m.CheckTPH(groupRedisKey, groupTPH, extra)
			}
			if subTPMonth > 0 {
				m.CheckTPMonth(subRedisKey, subTPMonth, extra)
			}
			if groupTPMonth > 0 {
				m.CheckTPMonth(groupRedisKey, groupTPMonth, extra)
			}
		}
		// Soft token counters use the actual token count.
		if subTPD > 0 {
			m.CheckTPD(subRedisKey, subTPD, tokens)
		}
		if groupTPD > 0 {
			m.CheckTPD(groupRedisKey, groupTPD, tokens)
		}
		if subTPW > 0 {
			m.CheckTPW(subRedisKey, subTPW, tokens)
		}
		if groupTPW > 0 {
			m.CheckTPW(groupRedisKey, groupTPW, tokens)
		}
	}
}

// AllowRPM - Check and record RPM usage. Returns false if limit exceeded.
func (m *Middleware) AllowRPM(redisKey string, rpmLimit int) bool {
	if rpmLimit <= 0 {
		return true
	}

	counterKey := fmt.Sprintf("rpm:%d", rpmLimit)
	counter := m.getOrCreateCounter(counterKey, rpmLimit, time.Minute)

	if !counter.Allow(redisKey + ":rpm") {
		return false
	}

	// Async sync to Redis (non-blocking, only if Redis is available)
	if m.redis != nil {
		select {
		case m.syncChan <- syncRequest{key: redisKey + ":rpm", limit: rpmLimit, delta: 1, counterType: "rpm"}:
		default:
			// Channel full, skip sync (Redis will catch up later)
		}
	}

	return true
}

// RecordTokens - Record token usage and check TPM limit. Called after request completes.
// Returns false if TPM limit was exceeded (for metrics/logging only; request already completed).
func (m *Middleware) RecordTokens(redisKey string, tpmLimit int, tokens int64) bool {
	if tpmLimit <= 0 || tokens <= 0 {
		return true
	}

	counterKey := fmt.Sprintf("tpm:%d", tpmLimit)
	counter := m.getOrCreateCounter(counterKey, tpmLimit, time.Minute)

	allowed := counter.AllowN(redisKey+":tpm", int(tokens))

	// Async sync to Redis (non-blocking, only if Redis is available)
	if m.redis != nil {
		select {
		case m.syncChan <- syncRequest{key: redisKey + ":tpm", limit: tpmLimit, delta: tokens, counterType: "tpm"}:
		default:
		}
	}

	if !allowed {
		m.logger.Warn("User TPM limit exceeded",
			zap.String("redis_key", redisKey),
			zap.Int("tpm_limit", tpmLimit),
			zap.Int64("tokens_consumed", tokens),
		)
	}

	return allowed
}

// CheckAndRecordTokens - Check and record token usage with API key, subscription and group limits.
// Uses the MORE RESTRICTIVE (smaller) non-zero limit.
// Returns the effective TPM limit used and whether the request was allowed.
func (m *Middleware) CheckAndRecordTokens(redisKey string, keyTPM, subTPM, groupTPM int, tokens int64) (effectiveTPM int, allowed bool) {
	effectiveTPM = minPositive(keyTPM, subTPM, groupTPM)

	if effectiveTPM <= 0 || tokens <= 0 {
		return effectiveTPM, true
	}

	allowed = m.RecordTokens(redisKey, effectiveTPM, tokens)
	return effectiveTPM, allowed
}

// AllowRPH - Check and record RPH (requests per hour) usage.
func (m *Middleware) AllowRPH(redisKey string, rphLimit int) bool {
	if rphLimit <= 0 {
		return true
	}

	counterKey := fmt.Sprintf("rph:%d", rphLimit)
	counter := m.getOrCreateCounter(counterKey, rphLimit, time.Hour)

	if !counter.Allow(redisKey + ":rph") {
		return false
	}

	// Async sync to Redis (non-blocking, only if Redis is available)
	if m.redis != nil {
		select {
		case m.syncChan <- syncRequest{key: redisKey + ":rph", limit: rphLimit, delta: 1, counterType: "rph"}:
		default:
		}
	}

	return true
}

// CheckTPH - Check and record TPH (tokens per hour) usage.
func (m *Middleware) CheckTPH(redisKey string, tphLimit int, tokens int64) bool {
	if tphLimit <= 0 || tokens <= 0 {
		return true
	}

	counterKey := fmt.Sprintf("tph:%d", tphLimit)
	counter := m.getOrCreateCounter(counterKey, tphLimit, time.Hour)

	allowed := counter.AllowN(redisKey+":tph", int(tokens))

	// Async sync to Redis (non-blocking, only if Redis is available)
	if m.redis != nil {
		select {
		case m.syncChan <- syncRequest{key: redisKey + ":tph", limit: tphLimit, delta: tokens, counterType: "tph"}:
		default:
		}
	}

	return allowed
}

// AllowRP5H - Check and record RP5H (requests per 5 hours) usage.
func (m *Middleware) AllowRP5H(redisKey string, rp5hLimit int) bool {
	if rp5hLimit <= 0 {
		return true
	}

	counterKey := fmt.Sprintf("rp5h:%d", rp5hLimit)
	counter := m.getOrCreateCounter(counterKey, rp5hLimit, 5*time.Hour)

	if !counter.Allow(redisKey + ":rp5h") {
		return false
	}

	// Async sync to Redis
	if m.redis != nil {
		select {
		case m.syncChan <- syncRequest{key: redisKey + ":rp5h", limit: rp5hLimit, delta: 1, counterType: "rp5h"}:
		default:
		}
	}

	return true
}

// AllowRPW - Check and record RPW (requests per week) usage.
func (m *Middleware) AllowRPW(redisKey string, rpwLimit int) bool {
	if rpwLimit <= 0 {
		return true
	}

	counterKey := fmt.Sprintf("rpw:%d", rpwLimit)
	counter := m.getOrCreateCounter(counterKey, rpwLimit, 7*24*time.Hour)

	if !counter.Allow(redisKey + ":rpw") {
		return false
	}

	// Async sync to Redis
	if m.redis != nil {
		select {
		case m.syncChan <- syncRequest{key: redisKey + ":rpw", limit: rpwLimit, delta: 1, counterType: "rpw"}:
		default:
		}
	}

	return true
}

// RateLimitInfo - Rate limit information from context
type RateLimitInfo struct {
	KeyRPM  int
	KeyTPM  int
	SubRPM  int
	SubTPM  int
	SubRPH  int
	SubTPH  int
	SubRP5H int
	SubRPW  int
	// Group limits (added to keep helper consistent with Handler logic)
	GroupRPM     int
	GroupRPH     int
	GroupRP5H    int
	GroupRPW     int
	GroupTPM     int
	GroupTPH     int
	GroupTPMonth int
}

// GetRateLimitInfo - Extract rate limit info from gin context
func GetRateLimitInfo(c *gin.Context) RateLimitInfo {
	return RateLimitInfo{
		KeyRPM:       c.GetInt("rate_limit_rpm"),
		KeyTPM:       c.GetInt("rate_limit_tpm"),
		SubRPM:       c.GetInt("sub_rpm_limit"),
		SubTPM:       c.GetInt("sub_tpm_limit"),
		SubRPH:       c.GetInt("sub_rph_limit"),
		SubTPH:       c.GetInt("sub_tph_limit"),
		SubRP5H:      c.GetInt("sub_rp5h_limit"),
		SubRPW:       c.GetInt("sub_rpw_limit"),
		GroupRPM:     c.GetInt("group_rpm"),
		GroupRPH:     c.GetInt("group_rph"),
		GroupRP5H:    c.GetInt("group_rp5h"),
		GroupRPW:     c.GetInt("group_rpw"),
		GroupTPM:     c.GetInt("group_tpm"),
		GroupTPH:     c.GetInt("group_tph"),
		GroupTPMonth: c.GetInt("group_tpmonth"),
	}
}

// GetEffectiveLimits - Calculate effective limits (minimum of key, subscription and group limits).
// Note: this helper is only used by tests today; Handler() uses minPositive inline.
func GetEffectiveLimits(info RateLimitInfo) (effectiveRPM, effectiveTPM, effectiveRPH, effectiveTPH, effectiveRP5H, effectiveRPW, effectiveTPMonth int) {
	effectiveRPM = minPositive(info.KeyRPM, info.SubRPM, info.GroupRPM)
	effectiveTPM = minPositive(info.KeyTPM, info.SubTPM, info.GroupTPM)
	effectiveRPH = minPositive(info.SubRPH, info.GroupRPH)
	effectiveTPH = minPositive(info.SubTPH, info.GroupTPH)
	effectiveRP5H = minPositive(info.SubRP5H, info.GroupRP5H)
	effectiveRPW = minPositive(info.SubRPW, info.GroupRPW)
	effectiveTPMonth = minPositive(0, info.GroupTPMonth) // subscription tpmonth not exposed yet

	return
}

// AllowRPD - Check and record RPD (requests per day) usage.
func (m *Middleware) AllowRPD(redisKey string, rpdLimit int) bool {
	if rpdLimit <= 0 {
		return true
	}

	counterKey := fmt.Sprintf("rpd:%d", rpdLimit)
	counter := m.getOrCreateCounter(counterKey, rpdLimit, 24*time.Hour)

	if !counter.Allow(redisKey + ":rpd") {
		return false
	}

	// Async sync to Redis
	if m.redis != nil {
		select {
		case m.syncChan <- syncRequest{key: redisKey + ":rpd", limit: rpdLimit, delta: 1, counterType: "rpd"}:
		default:
		}
	}

	return true
}

// CheckTPD - Check and record TPD (tokens per day) usage.
func (m *Middleware) CheckTPD(redisKey string, tpdLimit int, tokens int64) bool {
	if tpdLimit <= 0 || tokens <= 0 {
		return true
	}

	counterKey := fmt.Sprintf("tpd:%d", tpdLimit)
	counter := m.getOrCreateCounter(counterKey, tpdLimit, 24*time.Hour)

	allowed := counter.AllowN(redisKey+":tpd", int(tokens))

	// Async sync to Redis
	if m.redis != nil {
		select {
		case m.syncChan <- syncRequest{key: redisKey + ":tpd", limit: tpdLimit, delta: tokens, counterType: "tpd"}:
		default:
		}
	}

	if !allowed {
		m.logger.Warn("User TPD limit exceeded",
			zap.String("redis_key", redisKey),
			zap.Int("tpd_limit", tpdLimit),
			zap.Int64("tokens_consumed", tokens),
		)
	}

	return allowed
}

// CheckTPW - Check and record TPW (tokens per week) usage.
func (m *Middleware) CheckTPW(redisKey string, tpwLimit int, tokens int64) bool {
	if tpwLimit <= 0 || tokens <= 0 {
		return true
	}

	counterKey := fmt.Sprintf("tpw:%d", tpwLimit)
	counter := m.getOrCreateCounter(counterKey, tpwLimit, 7*24*time.Hour)

	allowed := counter.AllowN(redisKey+":tpw", int(tokens))

	// Async sync to Redis
	if m.redis != nil {
		select {
		case m.syncChan <- syncRequest{key: redisKey + ":tpw", limit: tpwLimit, delta: tokens, counterType: "tpw"}:
		default:
		}
	}

	if !allowed {
		m.logger.Warn("User TPW limit exceeded",
			zap.String("redis_key", redisKey),
			zap.Int("tpw_limit", tpwLimit),
			zap.Int64("tokens_consumed", tokens),
		)
	}

	return allowed
}

// AllowTPM - Check and record TPM (tokens per minute) usage. Returns false if limit exceeded.
func (m *Middleware) AllowTPM(redisKey string, tpmLimit int, tokens int64) bool {
	if tpmLimit <= 0 || tokens <= 0 {
		return true
	}

	counterKey := fmt.Sprintf("tpm:%d", tpmLimit)
	counter := m.getOrCreateCounter(counterKey, tpmLimit, time.Minute)

	allowed := counter.AllowN(redisKey+":tpm", int(tokens))

	if m.redis != nil {
		select {
		case m.syncChan <- syncRequest{key: redisKey + ":tpm", limit: tpmLimit, delta: tokens, counterType: "tpm"}:
		default:
		}
	}

	return allowed
}

// AllowTPH - Check and record TPH (tokens per hour) usage. Returns false if limit exceeded.
func (m *Middleware) AllowTPH(redisKey string, tphLimit int, tokens int64) bool {
	if tphLimit <= 0 || tokens <= 0 {
		return true
	}

	counterKey := fmt.Sprintf("tph:%d", tphLimit)
	counter := m.getOrCreateCounter(counterKey, tphLimit, time.Hour)

	allowed := counter.AllowN(redisKey+":tph", int(tokens))

	if m.redis != nil {
		select {
		case m.syncChan <- syncRequest{key: redisKey + ":tph", limit: tphLimit, delta: tokens, counterType: "tph"}:
		default:
		}
	}

	return allowed
}

// AllowTPMonth - Check and record TPMonth (tokens per month) usage. Returns false if limit exceeded.
func (m *Middleware) AllowTPMonth(redisKey string, tpmonthLimit int, tokens int64) bool {
	if tpmonthLimit <= 0 || tokens <= 0 {
		return true
	}

	counterKey := fmt.Sprintf("tpmonth:%d", tpmonthLimit)
	counter := m.getOrCreateCounter(counterKey, tpmonthLimit, 30*24*time.Hour)

	allowed := counter.AllowN(redisKey+":tpmonth", int(tokens))

	if m.redis != nil {
		select {
		case m.syncChan <- syncRequest{key: redisKey + ":tpmonth", limit: tpmonthLimit, delta: tokens, counterType: "tpmonth"}:
		default:
		}
	}

	return allowed
}

// CheckTPMonth - Check and record TPMonth (tokens per month) usage.
func (m *Middleware) CheckTPMonth(redisKey string, tpmonthLimit int, tokens int64) bool {
	if tpmonthLimit <= 0 || tokens <= 0 {
		return true
	}

	counterKey := fmt.Sprintf("tpmonth:%d", tpmonthLimit)
	counter := m.getOrCreateCounter(counterKey, tpmonthLimit, 30*24*time.Hour)

	allowed := counter.AllowN(redisKey+":tpmonth", int(tokens))

	if m.redis != nil {
		select {
		case m.syncChan <- syncRequest{key: redisKey + ":tpmonth", limit: tpmonthLimit, delta: tokens, counterType: "tpmonth"}:
		default:
		}
	}

	if !allowed {
		m.logger.Warn("User TPMonth limit exceeded",
			zap.String("redis_key", redisKey),
			zap.Int("tpmonth_limit", tpmonthLimit),
			zap.Int64("tokens_consumed", tokens),
		)
	}

	return allowed
}

// AllowConcurrent - Increment concurrent request counter. Returns false if limit exceeded.
func (m *Middleware) AllowConcurrent(redisKey string, limit int) bool {
	if limit <= 0 {
		return true
	}

	key := redisKey + ":concurrent"
	v, _ := m.concurrentCounters.LoadOrStore(key, &atomic.Int32{})
	counter := v.(*atomic.Int32)

	current := counter.Add(1)
	if int(current) > limit {
		counter.Add(-1)
		return false
	}
	return true
}

// ReleaseConcurrent - Decrement concurrent request counter.
func (m *Middleware) ReleaseConcurrent(redisKey string) {
	key := redisKey + ":concurrent"
	if v, ok := m.concurrentCounters.Load(key); ok {
		counter := v.(*atomic.Int32)
		for {
			current := counter.Load()
			if current <= 0 {
				break
			}
			if counter.CompareAndSwap(current, current-1) {
				break
			}
		}
	}
}

// concurrentCleanupLoop periodically cleans up zero concurrent counters
func (m *Middleware) concurrentCleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		m.concurrentCounters.Range(func(key, value interface{}) bool {
			counter := value.(*atomic.Int32)
			if counter.Load() <= 0 {
				m.concurrentCounters.Delete(key)
			}
			return true
		})
	}
}

// localCounterCleanupLoop periodically removes stale local token-bucket
// counters to prevent unbounded memory growth.
func (m *Middleware) localCounterCleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		m.localCounters.Range(func(key, value interface{}) bool {
			counter := value.(*localCounter)
			if counter.stale() {
				m.localCounters.Delete(key)
			}
			return true
		})
	}
}

// retryAfter returns approximate seconds until the next window reset
func (m *Middleware) retryAfter(counterType string) string {
	now := time.Now()
	switch counterType {
	case "rpm":
		return fmt.Sprintf("%d", 60-now.Second())
	case "rph", "tph":
		return fmt.Sprintf("%d", int((time.Hour - time.Duration(now.Minute())*time.Minute - time.Duration(now.Second())*time.Second).Seconds()))
	case "tpm":
		return fmt.Sprintf("%d", int((time.Minute - time.Duration(now.Second())*time.Second).Seconds()))
	case "rp5h":
		hourBlock := now.Hour() / 5
		nextHour := (hourBlock + 1) * 5
		var nextReset time.Time
		if nextHour >= 24 {
			// crosses midnight: reset at 00:00 next day
			nextReset = time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
		} else {
			nextReset = time.Date(now.Year(), now.Month(), now.Day(), nextHour, 0, 0, 0, now.Location())
		}
		return fmt.Sprintf("%d", int(nextReset.Sub(now).Seconds()))
	case "rpd", "tpd":
		nextReset := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
		return fmt.Sprintf("%d", int(nextReset.Sub(now).Seconds()))
	case "tpmonth":
		// Approximate: until the first day of next month.
		nextReset := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location())
		return fmt.Sprintf("%d", int(nextReset.Sub(now).Seconds()))
	case "rpw", "tpw":
		// Approximate: until end of week (Sunday midnight)
		daysUntilSunday := (7 - int(now.Weekday())) % 7
		if daysUntilSunday == 0 {
			daysUntilSunday = 7
		}
		nextReset := time.Date(now.Year(), now.Month(), now.Day()+daysUntilSunday, 0, 0, 0, 0, now.Location())
		return fmt.Sprintf("%d", int(nextReset.Sub(now).Seconds()))
	default:
		return "60"
	}
}

// asyncSyncLoop - Periodically sync local counters to Redis
func (m *Middleware) asyncSyncLoop() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case req := <-m.syncChan:
			ctx := context.Background()
			err := m.syncToRedis(ctx, req)
			if err != nil {
				m.logger.Debug("Redis sync failed", zap.Error(err))
			}
		case <-ticker.C:
			// Batch process any pending syncs
		}
	}
}

// syncToRedis - Sync counter increment to Redis
func (m *Middleware) syncToRedis(ctx context.Context, req syncRequest) error {
	if m.redis == nil {
		return nil
	}

	// Determine TTL based on counter type
	ttl := time.Minute
	switch req.counterType {
	case "rph", "tph":
		ttl = time.Hour
	case "rp5h":
		ttl = 5 * time.Hour
	case "rpw":
		ttl = 7 * 24 * time.Hour
	case "rpd", "tpd":
		ttl = 24 * time.Hour
	case "tpmonth":
		ttl = 30 * 24 * time.Hour
	}

	pipe := m.redis.Pipeline()
	pipe.IncrBy(ctx, req.key, req.delta)
	pipe.Expire(ctx, req.key, ttl)
	_, err := pipe.Exec(ctx)
	if err != nil {
		return err
	}

	return nil
}

// ErrRateLimitExceeded - Rate limit error
var ErrRateLimitExceeded = &RateLimitError{
	Message: "Rate limit exceeded",
	Type:    "rate_limit_error",
	Code:    "RATE_LIMIT_EXCEEDED",
}

// RateLimitError - Rate limit error type
type RateLimitError struct {
	Message string
	Type    string
	Code    string
}

func (e *RateLimitError) Error() string {
	return e.Message
}

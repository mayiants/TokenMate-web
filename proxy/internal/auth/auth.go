package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mayiants/ai-proxy-go/internal/config"
	"github.com/mayiants/ai-proxy-go/pkg/telemetry"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const (
	// Request timestamp tolerance (5 minutes)
	timestampTolerance = 5 * time.Minute
	// Replay attack prevention window
	replayWindow = 5 * time.Minute
	// Default cache TTL
	defaultCacheTTL = 5 * time.Minute
	// Default max cache entries
	defaultMaxCacheEntries = 10000
	// Graceful degradation TTL (use cached data longer when Portal is down)
	gracefulTTL = 30 * time.Minute
	// Balance cache TTL (independent of auth cache, much shorter)
	balanceCacheTTL = 10 * time.Second
	// Negative cache TTL for disabled/unknown keys (shorter since status can change)
	negativeCacheTTL = 1 * time.Minute
)

// cacheEntry - Cached validation result (claims nil = negative cache for disabled keys)
type cacheEntry struct {
	claims    *Claims
	expiresAt time.Time
	cachedAt  time.Time
}

// balanceEntry - Cached balance for a user (independent of auth cache)
type balanceEntry struct {
	balance   int64 // micro-USD (1e6 = 1 USD)
	updatedAt time.Time
}

// Middleware - Authentication middleware
type Middleware struct {
	cfg    *config.Config
	logger *zap.Logger

	// Replay attack prevention - store used nonces
	nonceCache map[string]time.Time // fallback when Redis unavailable
	nonceMu    sync.RWMutex
	redis      *redis.Client // for distributed nonce storage

	// API Key validation cache (using sync.Map for better concurrency)
	keyCache   sync.Map // key: string (keyHash), value: *cacheEntry
	maxEntries int
	cacheTTL   time.Duration

	// Independent balance cache (10s TTL, separate from 5min auth cache)
	// Keyed by user_id, invalidated by Redis pub/sub balance_update messages
	balanceCache   map[string]*balanceEntry // user_id → balance entry
	balanceCacheMu sync.RWMutex

	// Graceful degradation
	portalHealthy atomic.Bool
	lastErrorTime atomic.Value // stores time.Time

	// Shared HTTP client for Portal API calls (connection pooling)
	portalClient *http.Client

	// Stop channel for graceful shutdown of background goroutines
	stopCh chan struct{}
}

// NewMiddleware - Create auth middleware
func NewMiddleware(cfg *config.Config, logger *zap.Logger) *Middleware {
	m := &Middleware{
		cfg:          cfg,
		logger:       logger,
		nonceCache:   make(map[string]time.Time),
		balanceCache: make(map[string]*balanceEntry),
		maxEntries:   defaultMaxCacheEntries,
		cacheTTL:     defaultCacheTTL,
		stopCh:       make(chan struct{}),
		// Shared HTTP client with connection pooling
		portalClient: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 50,
				IdleConnTimeout:     90 * time.Second,
				DisableCompression:  false,
			},
		},
	}
	m.portalHealthy.Store(true)

	// Start cleanup goroutine for old nonces and cache entries
	go m.cleanupLoop()

	logger.Info("Auth middleware initialized",
		zap.Duration("cache_ttl", m.cacheTTL),
		zap.Int("max_cache_entries", m.maxEntries),
	)

	return m
}

// Claims - JWT claims structure
type Claims struct {
	UserID        string   `json:"user_id"`
	APIKeyID      string   `json:"api_key_id"`
	KeyHash       string   `json:"key_hash"`
	Role          string   `json:"role"`
	AccessMode    string   `json:"access_mode"`
	AllowedModels []string `json:"allowed_models"`
	RateLimitRPM  int      `json:"rate_limit_rpm"`
	RateLimitTPM  int      `json:"rate_limit_tpm"`
	Balance       int64    `json:"balance"` // cached balance for pre-check (micro-USD)
	// Group-level fields (Phase 1)
	GroupID       string   `json:"group_id"`
	KeyType       string   `json:"key_type"`
	CurrentModel  string   `json:"current_model"`
	BillingMode   string   `json:"billing_mode"`
	PricingRatio  float64  `json:"pricing_ratio"`
	GroupPriority []string `json:"group_priority"`
	Models        []string `json:"models"`
	// Phase 2: model routing
	ModelRoutingMode string   `json:"model_routing_mode"`
	SelectedModels   []string `json:"selected_models"`
	// Subscription rate limits (enforced locally by Proxy)
	SubTpmLimit        int   `json:"sub_tpm_limit"`
	SubTphLimit        int   `json:"sub_tph_limit"`
	SubRpmLimit        int   `json:"sub_rpm_limit"`
	SubRphLimit        int   `json:"sub_rph_limit"`
	SubConcurrentLimit int   `json:"sub_concurrent_limit"`
	SubRp5hLimit       int   `json:"sub_rp5h_limit"`
	SubRpdLimit        int   `json:"sub_rpd_limit"`
	SubRpwLimit        int   `json:"sub_rpw_limit"`
	SubTpdLimit        int   `json:"sub_tpd_limit"`
	SubTpwLimit        int   `json:"sub_tpw_limit"`
	SubTPMonthLimit    int   `json:"sub_tpmonth_limit"`
	SubDailyLimit      int64 `json:"sub_daily_limit"`
	SubMonthlyQuota    int64 `json:"sub_monthly_quota"`
	// Group rate limits
	GroupRPM             int `json:"group_rpm"`
	GroupRPH             int `json:"group_rph"`
	GroupRP5H            int `json:"group_rp5h"`
	GroupRPW             int `json:"group_rpw"`
	GroupRPMonth         int `json:"group_rpmonth"`
	GroupTPM             int `json:"group_tpm"`
	GroupTPH             int `json:"group_tph"`
	GroupTPD             int `json:"group_tpd"`
	GroupTPW             int `json:"group_tpw"`
	GroupTPMonth         int `json:"group_tpmonth"`
	GroupConcurrentLimit int `json:"group_concurrent_limit"`
	// Dual-track routing
	RoutingTrack string `json:"routing_track"`
	// Per-enterprise retention policy (from Portal; normalised in validateWithPortalAPI)
	EnterpriseID  string `json:"enterprise_id"`
	RetentionMode string `json:"retention_mode"`
	RetentionDays int    `json:"retention_days"`
	jwt.RegisteredClaims
}

// abortAuth rejects the request and records the reason in the gin context so
// the failure-audit middleware can log it without re-parsing the response body.
func abortAuth(c *gin.Context, status int, message, errType, code string) {
	c.Set("error_message", message)
	c.Set("error_code", code)
	c.AbortWithStatusJSON(status, gin.H{
		"error": gin.H{
			"message": message,
			"type":    errType,
			"code":    code,
		},
	})
}

// Handler - Auth middleware handler
func (m *Middleware) Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Extract API key from header
		apiKey := extractAPIKey(c)
		if apiKey == "" {
			abortAuth(c, 401, "Missing API key", "authentication_error", "MISSING_API_KEY")
			return
		}

		// Validate API key format
		if !strings.HasPrefix(apiKey, "sk-") || len(apiKey) < 16 {
			abortAuth(c, 401, "Invalid API key format", "authentication_error", "INVALID_API_KEY_FORMAT")
			return
		}

		// Validate with Portal
		claims, err := m.validateWithPortal(c.Request.Context(), apiKey)
		if err != nil {
			// Check for specific error codes
			errMsg := err.Error()

			// BALANCE_EXHAUSTED / QUOTA_EXCEEDED mean the key itself was
			// recognised; expose its hash (never the raw key) so failure audits
			// can attribute the rejection.
			if strings.HasPrefix(errMsg, "BALANCE_EXHAUSTED") || strings.HasPrefix(errMsg, "QUOTA_EXCEEDED") {
				c.Set("key_hash", hashKeySHA256(apiKey))
			}

			if strings.HasPrefix(errMsg, "BALANCE_EXHAUSTED") {
				// Balance exhausted - permanent until top-up
				abortAuth(c, 402, "Insufficient balance. Please top up your account.", "billing_error", "BALANCE_EXHAUSTED")
				return
			}

			if strings.HasPrefix(errMsg, "QUOTA_EXCEEDED") {
				// Quota exhausted - temporary, will reset after window
				abortAuth(c, 429, "Rate limit quota exceeded for current time window. Please try again later.", "rate_limit_error", "QUOTA_EXCEEDED")
				return
			}

			if strings.HasPrefix(errMsg, "PORTAL_UNAVAILABLE") {
				// Portal is down/unreachable and no cache available
				abortAuth(c, 503, "Authentication service temporarily unavailable. Please try again later.", "service_unavailable", "PORTAL_UNAVAILABLE")
				return
			}

			// Generic auth error
			m.logger.Warn("API key validation failed",
				zap.Error(err),
				zap.String("key_prefix", maskKey(apiKey)),
			)
			abortAuth(c, 401, "Invalid API key", "authentication_error", "INVALID_API_KEY")
			return
		}

		// Set user info in context
		c.Set("user_id", claims.UserID)
		c.Set("api_key_id", claims.APIKeyID)
		c.Set("key_hash", claims.KeyHash)
		c.Set("role", claims.Role)
		c.Set("access_mode", claims.AccessMode)
		c.Set("api_key", apiKey)
		c.Set("allowed_models", claims.AllowedModels)
		c.Set("rate_limit_rpm", claims.RateLimitRPM)
		c.Set("rate_limit_tpm", claims.RateLimitTPM)
		// Phase 1: group-level fields
		c.Set("group_id", claims.GroupID)
		c.Set("key_type", claims.KeyType)
		c.Set("current_model", claims.CurrentModel)
		c.Set("billing_mode", claims.BillingMode)
		c.Set("pricing_ratio", claims.PricingRatio)
		c.Set("group_priority", claims.GroupPriority)
		c.Set("models", claims.Models)
		// Phase 2: model routing
		c.Set("model_routing_mode", claims.ModelRoutingMode)
		c.Set("selected_models", claims.SelectedModels)
		// Subscription rate limits
		c.Set("sub_rpm_limit", claims.SubRpmLimit)
		c.Set("sub_tpm_limit", claims.SubTpmLimit)
		c.Set("sub_rph_limit", claims.SubRphLimit)
		c.Set("sub_tph_limit", claims.SubTphLimit)
		c.Set("sub_concurrent_limit", claims.SubConcurrentLimit)
		c.Set("sub_rp5h_limit", claims.SubRp5hLimit)
		c.Set("sub_rpd_limit", claims.SubRpdLimit)
		c.Set("sub_rpw_limit", claims.SubRpwLimit)
		c.Set("sub_tpd_limit", claims.SubTpdLimit)
		c.Set("sub_tpw_limit", claims.SubTpwLimit)
		c.Set("sub_tpmonth_limit", claims.SubTPMonthLimit)
		// Group rate limits
		c.Set("group_rpm", claims.GroupRPM)
		c.Set("group_rph", claims.GroupRPH)
		c.Set("group_rp5h", claims.GroupRP5H)
		c.Set("group_rpw", claims.GroupRPW)
		c.Set("group_rpmonth", claims.GroupRPMonth)
		c.Set("group_tpm", claims.GroupTPM)
		c.Set("group_tph", claims.GroupTPH)
		c.Set("group_tpd", claims.GroupTPD)
		c.Set("group_tpw", claims.GroupTPW)
		c.Set("group_tpmonth", claims.GroupTPMonth)
		c.Set("group_concurrent_limit", claims.GroupConcurrentLimit)
		c.Set("routing_track", claims.RoutingTrack)
		// Retention policy rides the same 5-minute key cache as the rest of the claims.
		c.Set("enterprise_id", claims.EnterpriseID)
		c.Set("retention_mode", claims.RetentionMode)
		c.Set("retention_days", claims.RetentionDays)

		// Balance pre-check: use independent balance cache (10s TTL) first,
		// then fall back to claims.Balance from auth cache (5min TTL).
		// The balance cache is invalidated by Redis pub/sub balance_update messages,
		// giving near-instant rejection when Portal detects low balance.
		// Enterprise keys use enterprise credit, not personal balance;
		// subscription keys are gated by the portal on subscription quota.
		// Both skip the personal-balance check.
		if claims.KeyType != "enterprise" && claims.KeyType != "subscription" {
			cachedBalance := m.getBalance(claims.UserID)
			if cachedBalance >= 0 {
				// Balance cache hit (10s TTL) — use this authoritative value
				if cachedBalance <= 0 {
					abortAuth(c, 402, "Insufficient balance. Please top up your account.", "billing_error", "BALANCE_EXHAUSTED")
					return
				}
			} else if claims.Balance <= 0 {
				// Balance cache miss but auth cache says balance ≤ 0
				abortAuth(c, 402, "Insufficient balance. Please top up your account.", "billing_error", "BALANCE_EXHAUSTED")
				return
			}
		}

		c.Next()
	}
}

// AdminHandler - Admin auth middleware with timestamp and signature validation
func (m *Middleware) AdminHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Timestamp and nonce are always required for admin endpoints to prevent replay attacks
		timestampStr := c.GetHeader("X-Timestamp")
		nonce := c.GetHeader("X-Nonce")
		signature := c.GetHeader("X-Signature")

		// 1. Validate timestamp (always required)
		if timestampStr == "" {
			c.AbortWithStatusJSON(403, gin.H{
				"error": gin.H{
					"message": "Timestamp header is required",
					"type":    "permission_error",
					"code":    "TIMESTAMP_REQUIRED",
				},
			})
			return
		}
		timestamp, err := strconv.ParseInt(timestampStr, 10, 64)
		if err != nil {
			c.AbortWithStatusJSON(403, gin.H{
				"error": gin.H{
					"message": "Invalid timestamp format",
					"type":    "permission_error",
					"code":    "INVALID_TIMESTAMP",
				},
			})
			return
		}
		requestTime := time.Unix(timestamp, 0)
		now := time.Now()
		if now.Sub(requestTime) > timestampTolerance || requestTime.Sub(now) > timestampTolerance {
			c.AbortWithStatusJSON(403, gin.H{
				"error": gin.H{
					"message": "Timestamp expired or in future",
					"type":    "permission_error",
					"code":    "TIMESTAMP_EXPIRED",
				},
			})
			return
		}

		// 2. Validate nonce (always required for replay prevention)
		if nonce == "" {
			c.AbortWithStatusJSON(403, gin.H{
				"error": gin.H{
					"message": "Nonce header is required",
					"type":    "permission_error",
					"code":    "NONCE_REQUIRED",
				},
			})
			return
		}
		if m.isNonceUsed(nonce) {
			c.AbortWithStatusJSON(403, gin.H{
				"error": gin.H{
					"message": "Nonce already used (replay attack)",
					"type":    "permission_error",
					"code":    "NONCE_REPLAY",
				},
			})
			return
		}
		m.markNonceUsed(nonce)

		// 3. Validate HMAC signature (required — simple admin secret fallback removed)
		if signature == "" {
			c.AbortWithStatusJSON(403, gin.H{
				"error": gin.H{
					"message": "Signature header is required",
					"type":    "permission_error",
					"code":    "SIGNATURE_REQUIRED",
				},
			})
			return
		}
		if err := m.validateSignatureHMAC(c, timestampStr, nonce, signature); err != nil {
			m.logger.Warn("Admin signature validation failed",
				zap.Error(err),
				zap.String("path", c.Request.URL.Path),
			)
			c.AbortWithStatusJSON(403, gin.H{
				"error": gin.H{
					"message": "Invalid request signature",
					"type":    "permission_error",
					"code":    "INVALID_SIGNATURE",
				},
			})
			return
		}

		c.Set("is_admin", true)
		c.Next()
	}
}

// validateSignatureHMAC validates only the HMAC signature component.
// Caller must validate timestamp and nonce before calling this function.
func (m *Middleware) validateSignatureHMAC(c *gin.Context, timestampStr, nonce, signature string) error {
	// Compute expected signature
	// Signature = HMAC-SHA256(secret, method + path + timestamp + nonce)
	// Admin.Secret is required; no fallback to Portal.SyncSecret is allowed.
	signingKey := m.cfg.Admin.Secret
	if signingKey == "" {
		return fmt.Errorf("admin secret is not configured")
	}
	message := fmt.Sprintf("%s%s%s%s",
		c.Request.Method,
		c.Request.URL.Path,
		timestampStr,
		nonce,
	)
	expectedSig := computeHMAC(signingKey, message)
	if !hmac.Equal([]byte(signature), []byte(expectedSig)) {
		return fmt.Errorf("signature mismatch")
	}
	return nil
}

// isNonceUsed checks if a nonce has been used
// SetRedisClient sets the Redis client for distributed nonce storage
func (m *Middleware) SetRedisClient(rdb *redis.Client) {
	m.redis = rdb
}

func (m *Middleware) isNonceUsed(nonce string) bool {
	if m.redis != nil {
		// Atomic check-and-mark via SETNX: returns true if key was set (nonce is fresh)
		set, err := m.redis.SetNX(context.Background(), "nonce:"+nonce, "1", replayWindow).Result()
		if err != nil {
			// Redis unavailable — cannot verify nonce, assume used to prevent replay
			m.logger.Warn("Redis unavailable for nonce check, rejecting request", zap.Error(err))
			return true
		}
		if !set {
			// Key already existed → nonce was already used
			return true
		}
		// Nonce is fresh and now marked in Redis; also mark in local cache
		m.nonceMu.Lock()
		m.nonceCache[nonce] = time.Now()
		m.nonceMu.Unlock()
		return false
	}
	// No Redis: use local cache only
	m.nonceMu.RLock()
	_, exists := m.nonceCache[nonce]
	m.nonceMu.RUnlock()
	return exists
}

// markNonceUsed marks a nonce as used (called only when Redis is unavailable or as fallback)
func (m *Middleware) markNonceUsed(nonce string) {
	if m.redis != nil {
		// Already marked atomically in isNonceUsed via SETNX
		return
	}
	m.nonceMu.Lock()
	defer m.nonceMu.Unlock()
	m.nonceCache[nonce] = time.Now()
}

// cleanupLoop periodically removes old nonces and cache entries
func (m *Middleware) cleanupLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case now := <-ticker.C:
			// Clean nonces
			m.nonceMu.Lock()
			for nonce, usedAt := range m.nonceCache {
				if now.Sub(usedAt) > replayWindow {
					delete(m.nonceCache, nonce)
				}
			}
			m.nonceMu.Unlock()

			// Clean key cache (sync.Map iteration)
			m.keyCache.Range(func(key, value interface{}) bool {
				entry := value.(*cacheEntry)
				if now.After(entry.expiresAt) {
					// Only evict expired entries when portal is healthy or the
					// entry has exceeded gracefulTTL. This prevents the cleanup
					// loop from deleting entries that getCachedClaims would still
					// return under graceful degradation, making gracefulTTL effective.
					if m.portalHealthy.Load() || now.After(entry.cachedAt.Add(gracefulTTL)) {
						m.keyCache.Delete(key)
					}
				}
				return true
			})

			// Clean stale balance cache entries.
			m.cleanupStaleBalanceEntries(now)
		case <-m.stopCh:
			return
		}
	}
}

// cleanupStaleBalanceEntries removes balance entries that have not been
// refreshed for more than 5x the balance TTL. Active users refresh every TTL
// period, so this only removes users that are no longer making requests.
func (m *Middleware) cleanupStaleBalanceEntries(now time.Time) {
	m.balanceCacheMu.Lock()
	defer m.balanceCacheMu.Unlock()
	for userID, entry := range m.balanceCache {
		if now.Sub(entry.updatedAt) > 5*balanceCacheTTL {
			delete(m.balanceCache, userID)
		}
	}
}

// Stop gracefully shuts down the middleware background goroutines.
func (m *Middleware) Stop() {
	close(m.stopCh)
}

const balanceRedisPrefix = "ai-proxy:balance:"

func balanceRedisKey(userID string) string {
	return balanceRedisPrefix + userID
}

func floatToMicro(usd float64) int64 {
	return int64(math.Round(usd * 1_000_000))
}

func (m *Middleware) balanceCacheEnabled() bool {
	return m.cfg != nil && m.cfg.Billing.LocalBalanceCacheEnabled
}

func (m *Middleware) balanceCacheTTL() time.Duration {
	if m.cfg != nil && m.cfg.Billing.LocalBalanceCacheTTL > 0 {
		return m.cfg.Billing.LocalBalanceCacheTTL
	}
	return balanceCacheTTL
}

// getBalance returns cached balance for a user (micro-USD), or -1 if not cached/expired.
// This cache is independent of the auth cache (10s TTL vs 5min TTL).
func (m *Middleware) getBalance(userID string) int64 {
	if m.balanceCacheEnabled() && m.redis != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		val, err := m.redis.Get(ctx, balanceRedisKey(userID)).Result()
		if err == nil && val != "" {
			if micro, err := strconv.ParseInt(val, 10, 64); err == nil {
				m.setBalanceMemory(userID, micro)
				return micro
			}
		}
	}

	m.balanceCacheMu.RLock()
	if entry, ok := m.balanceCache[userID]; ok {
		if time.Since(entry.updatedAt) < m.balanceCacheTTL() {
			m.balanceCacheMu.RUnlock()
			return entry.balance
		}
	}
	m.balanceCacheMu.RUnlock()
	return -1 // not cached or expired
}

// setBalanceMemory updates only the in-memory balance entry.
func (m *Middleware) setBalanceMemory(userID string, balanceMicro int64) {
	m.balanceCacheMu.Lock()
	m.balanceCache[userID] = &balanceEntry{
		balance:   balanceMicro,
		updatedAt: time.Now(),
	}
	m.balanceCacheMu.Unlock()
}

// setBalance updates the cached balance for a user (Redis + in-memory).
func (m *Middleware) setBalance(userID string, balanceMicro int64) {
	m.setBalanceMemory(userID, balanceMicro)
	if m.balanceCacheEnabled() && m.redis != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_ = m.redis.Set(ctx, balanceRedisKey(userID), balanceMicro, m.balanceCacheTTL()).Err()
	}
}

// SetBalance sets a user's cached balance directly (micro-USD). Exported for webhook updates.
func (m *Middleware) SetBalance(userID string, balanceMicro int64) {
	m.setBalance(userID, balanceMicro)
}

// InvalidateBalance removes a user's balance cache entry.
// Called when a balance_update message is received from Redis pub/sub.
func (m *Middleware) InvalidateBalance(userID string) {
	m.balanceCacheMu.Lock()
	delete(m.balanceCache, userID)
	m.balanceCacheMu.Unlock()
	if m.balanceCacheEnabled() && m.redis != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_ = m.redis.Del(ctx, balanceRedisKey(userID)).Err()
	}
}

// InvalidateKeyHash removes a single key's cached validation result so the next
// request re-validates against the Portal. Called when a key is disabled/deleted
// (key_revoked), via either the Redis pub/sub subscriber or the HTTPS webhook.
func (m *Middleware) InvalidateKeyHash(keyHash string) {
	if keyHash == "" {
		return
	}
	m.keyCache.Delete(keyHash)
	m.logger.Info("Key cache invalidated (key_revoked)", zap.String("key_hash", keyHash))
}

// InvalidateUser drops all cached state for a user: balance cache plus every
// cached key-validation entry owned by that user. Called when a user or their
// enterprise is suspended/deleted (user_banned), via either the Redis pub/sub
// subscriber or the HTTPS webhook.
func (m *Middleware) InvalidateUser(userID string) {
	if userID == "" {
		return
	}
	m.InvalidateBalance(userID)
	m.balanceCacheMu.Lock()
	delete(m.balanceCache, userID)
	m.balanceCacheMu.Unlock()
	m.keyCache.Range(func(key, value interface{}) bool {
		entry, ok := value.(*cacheEntry)
		if ok && entry.claims != nil && entry.claims.UserID == userID {
			m.keyCache.Delete(key)
		}
		return true
	})
	m.logger.Info("All caches invalidated for user (user_banned)", zap.String("user_id", userID))
}

// PublishKeyRevoked publishes a key_revoked message to the shared Redis channel
// so all proxy instances invalidate the cached validation result for that key hash.
func (m *Middleware) PublishKeyRevoked(ctx context.Context, keyHash string) {
	if m.redis == nil {
		return
	}
	data, _ := json.Marshal(map[string]string{
		"type":     "key_revoked",
		"key_hash": keyHash,
	})
	if err := m.redis.Publish(ctx, "ai-proxy:balance", string(data)).Err(); err != nil {
		m.logger.Warn("Failed to publish key_revoked message",
			zap.String("key_hash", keyHash), zap.Error(err))
	}
}

// PublishUserBanned publishes a user_banned message to the shared Redis channel
// so all proxy instances invalidate all caches for that user.
func (m *Middleware) PublishUserBanned(ctx context.Context, userID string) {
	if m.redis == nil {
		return
	}
	data, _ := json.Marshal(map[string]string{
		"type":    "user_banned",
		"user_id": userID,
	})
	if err := m.redis.Publish(ctx, "ai-proxy:balance", string(data)).Err(); err != nil {
		m.logger.Warn("Failed to publish user_banned message",
			zap.String("user_id", userID), zap.Error(err))
	}
}

// PreConsumeLocal atomically deducts amountMicro from the cached balance.
// It returns (remainingMicro, ok, handled):
//   - handled=false: no usable cache entry exists (or the feature is disabled),
//     so nothing was deducted locally and the caller must fall back to the
//     portal pre-consume path (fail-open).
//   - handled=true, ok=true: the amount was deducted; remainingMicro is the
//     post-deduction balance (which may legitimately be 0).
//   - handled=true, ok=false: the cached balance is insufficient;
//     remainingMicro is the current balance.
func (m *Middleware) PreConsumeLocal(userID string, amountMicro int64) (remainingMicro int64, ok bool, handled bool) {
	if !m.balanceCacheEnabled() || amountMicro <= 0 {
		return 0, true, false
	}

	if m.redis != nil {
		// Sentinel values distinguish the three outcomes unambiguously:
		// a post-deduction balance of exactly 0 must not be confused with a
		// missing key, or the caller would double-charge via portal pre-consume.
		lua := `local bal = redis.call('GET', KEYS[1])
if bal == false then return {-2, 0} end
local needed = tonumber(ARGV[1])
local current = tonumber(bal)
if current < needed then return {-1, current} end
local new = redis.call('DECRBY', KEYS[1], needed)
redis.call('EXPIRE', KEYS[1], ARGV[2])
return {new, new}`
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		res, err := m.redis.Eval(ctx, lua, []string{balanceRedisKey(userID)}, amountMicro, int64(m.balanceCacheTTL().Seconds())).Result()
		if err != nil {
			return 0, true, false // fail-open on Redis error
		}
		arr, isArr := res.([]interface{})
		if !isArr || len(arr) != 2 {
			return 0, true, false
		}
		newMicro, _ := strconv.ParseInt(fmt.Sprintf("%v", arr[0]), 10, 64)
		switch newMicro {
		case -2: // no cached balance entry
			return 0, true, false
		case -1: // insufficient balance
			current, _ := strconv.ParseInt(fmt.Sprintf("%v", arr[1]), 10, 64)
			return current, false, true
		}
		m.setBalanceMemory(userID, newMicro)
		return newMicro, true, true
	}

	// In-memory fallback.
	m.balanceCacheMu.Lock()
	defer m.balanceCacheMu.Unlock()
	entry, exists := m.balanceCache[userID]
	if !exists || time.Since(entry.updatedAt) >= m.balanceCacheTTL() {
		return 0, true, false // fail-open
	}
	current := entry.balance
	if current < amountMicro {
		return current, false, true
	}
	newMicro := current - amountMicro
	entry.balance = newMicro
	entry.updatedAt = time.Now()
	return newMicro, true, true
}

// SettleLocal adjusts the cached balance after the actual cost is known.
// delta = preConsumedMicro - actualMicro. A positive delta refunds the
// overcharge; a negative delta charges the difference.
func (m *Middleware) SettleLocal(userID string, preConsumedMicro, actualMicro int64) {
	if !m.balanceCacheEnabled() || preConsumedMicro == 0 {
		return
	}
	delta := preConsumedMicro - actualMicro
	if delta == 0 {
		return
	}

	if m.redis != nil {
		// Adjust only an existing key: a bare INCRBY on an expired key would
		// create a phantom balance holding just this delta. When the key is
		// gone the portal ledger remains the source of truth via usage reports,
		// so skipping the Redis write is safe.
		lua := `if redis.call('EXISTS', KEYS[1]) == 1 then
	redis.call('INCRBY', KEYS[1], ARGV[1])
	redis.call('EXPIRE', KEYS[1], ARGV[2])
	return 1
end
return 0`
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_ = m.redis.Eval(ctx, lua, []string{balanceRedisKey(userID)}, delta, int64(m.balanceCacheTTL().Seconds())).Err()
	}

	m.balanceCacheMu.Lock()
	if entry, ok := m.balanceCache[userID]; ok {
		entry.balance = entry.balance + delta
		entry.updatedAt = time.Now()
	}
	m.balanceCacheMu.Unlock()
}

// ChargeLocal confirms a pre-consumed charge without refunding it.
// The balance was already decremented by PreConsumeLocal, so this only
// refreshes the Redis TTL when Redis is in use.
func (m *Middleware) ChargeLocal(userID string, amountMicro int64) {
	if !m.balanceCacheEnabled() || amountMicro <= 0 {
		return
	}
	if m.redis != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_ = m.redis.Expire(ctx, balanceRedisKey(userID), m.balanceCacheTTL()).Err()
	}
}

// StartBalanceListener subscribes to the ai-proxy:balance Redis channel
// and invalidates local balance cache when a balance_update message arrives.
// This enables near-instant rejection when Portal detects low balance.
func (m *Middleware) StartBalanceListener(ctx context.Context, rdb *redis.Client) {
	if rdb == nil {
		m.logger.Warn("No Redis client, balance pub/sub listener not started")
		return
	}

	go func() {
		channel := "ai-proxy:balance"
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			sub := rdb.Subscribe(ctx, channel)
			m.logger.Info("Balance pub/sub listener started", zap.String("channel", channel))

			ch := sub.Channel()
			for msg := range ch {
				var data struct {
					Type      string  `json:"type"`
					UserID    string  `json:"user_id"`
					Balance   float64 `json:"balance"`
					Threshold float64 `json:"threshold"`
					KeyHash   string  `json:"key_hash"`
				}
				if err := json.Unmarshal([]byte(msg.Payload), &data); err != nil {
					m.logger.Warn("Failed to parse balance_update message", zap.Error(err))
					continue
				}
				switch data.Type {
				case "balance_update":
					if data.UserID == "" {
						continue
					}
					if data.Balance <= 0 {
						m.setBalance(data.UserID, 0)
					} else {
						m.InvalidateBalance(data.UserID)
					}
					m.logger.Debug("Balance cache updated from pub/sub",
						zap.String("user_id", data.UserID),
						zap.Float64("balance", data.Balance))
				case "key_revoked":
					m.InvalidateKeyHash(data.KeyHash)
				case "user_banned":
					m.InvalidateUser(data.UserID)
				default:
					m.logger.Warn("Unknown pub/sub message type",
						zap.String("type", data.Type))
				}
			}

			// Subscription dropped, retry after a short delay
			if err := sub.Close(); err != nil {
				m.logger.Warn("Balance pub/sub subscription close error", zap.Error(err))
			}
			m.logger.Warn("Balance pub/sub subscription dropped, reconnecting in 5s...")
			select {
			case <-time.After(5 * time.Second):
			case <-ctx.Done():
				return
			}
		}
	}()
}

// getCachedClaims - Get cached claims if valid
func (m *Middleware) getCachedClaims(keyHash string) *Claims {
	if v, ok := m.keyCache.Load(keyHash); ok {
		entry := v.(*cacheEntry)
		// Negative cache entries (claims == nil) are handled by isNegativelyCached
		if entry.claims == nil {
			return nil
		}
		// Normal case: cache is still valid
		if time.Now().Before(entry.expiresAt) {
			return entry.claims
		}

		// Graceful degradation: Portal is unhealthy, use extended TTL
		if !m.portalHealthy.Load() && time.Since(entry.cachedAt) < gracefulTTL {
			m.logger.Debug("Using gracefully degraded cache",
				zap.Duration("age", time.Since(entry.cachedAt)),
			)
			return entry.claims
		}
	}
	return nil
}

// isNegativelyCached - Check if key is negatively cached (disabled/unknown)
func (m *Middleware) isNegativelyCached(keyHash string) bool {
	if v, ok := m.keyCache.Load(keyHash); ok {
		entry := v.(*cacheEntry)
		if entry.claims == nil && time.Now().Before(entry.expiresAt) {
			return true
		}
	}
	return false
}

// cacheNegative - Cache a negative result (disabled/unknown key)
func (m *Middleware) cacheNegative(keyHash string) {
	now := time.Now()
	m.keyCache.Store(keyHash, &cacheEntry{
		claims:    nil,
		expiresAt: now.Add(negativeCacheTTL),
		cachedAt:  now,
	})
}

// cacheClaims - Cache validation result
func (m *Middleware) cacheClaims(keyHash string, claims *Claims) {
	now := time.Now()
	m.keyCache.Store(keyHash, &cacheEntry{
		claims:    claims,
		expiresAt: now.Add(m.cacheTTL),
		cachedAt:  now,
	})

	// Mark portal as healthy after successful validation
	m.portalHealthy.Store(true)
}

// markPortalUnhealthy - Mark portal as unhealthy for graceful degradation
func (m *Middleware) markPortalUnhealthy() {
	m.portalHealthy.Store(false)
	m.lastErrorTime.Store(time.Now())
	m.logger.Warn("Portal marked as unhealthy, enabling graceful degradation")
}

// computeHMAC computes HMAC-SHA256
func computeHMAC(secret, message string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(message))
	return hex.EncodeToString(h.Sum(nil))
}

// extractAPIKey - Extract API key from request
func extractAPIKey(c *gin.Context) string {
	// Check Authorization header
	authHeader := c.GetHeader("Authorization")
	if authHeader != "" {
		if strings.HasPrefix(authHeader, "Bearer ") {
			return strings.TrimPrefix(authHeader, "Bearer ")
		}
	}

	// Check X-API-Key header
	apiKey := c.GetHeader("X-API-Key")
	if apiKey != "" {
		return apiKey
	}

	return ""
}

// validateWithPortal - Validate API key with Portal
func (m *Middleware) validateWithPortal(ctx context.Context, apiKey string) (*Claims, error) {
	// All keys validate against the Portal API. There is no local JWT bypass
	// path: a self-signed token could carry forged balance/quota claims.
	claims, err := m.validateWithPortalAPI(ctx, apiKey)
	if err != nil {
		return nil, err
	}

	return claims, nil
}

// validateWithPortalAPI - Validate API key via Portal HTTP API
func (m *Middleware) validateWithPortalAPI(ctx context.Context, apiKey string) (*Claims, error) {
	// Hash API key for cache key to avoid storing raw keys in memory
	cacheKey := hashCacheKey(apiKey)

	// 1. Check local cache first (both positive and negative)
	if cached := m.getCachedClaims(cacheKey); cached != nil {
		m.logger.Debug("API key validation cache hit",
			zap.String("key_prefix", maskKey(apiKey)),
		)
		return cached, nil
	}

	// 1a. Check negative cache (disabled/unknown keys)
	if m.isNegativelyCached(cacheKey) {
		m.logger.Debug("API key validation negative cache hit",
			zap.String("key_prefix", maskKey(apiKey)),
		)
		return nil, fmt.Errorf("api key not valid")
	}

	// 2. Portal not configured - refuse authentication (no dev mode bypass)
	if m.cfg.Portal.URL == "" {
		return nil, fmt.Errorf("portal URL not configured")
	}

	// 3. Call Portal validation endpoint
	url := m.cfg.Portal.URL + "/api/apikeys/validate"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	telemetry.InjectTraceparent(ctx, req)

	// Use shared HTTP client for connection pooling
	resp, err := m.portalClient.Do(req)
	if err != nil {
		m.logger.Warn("Portal validation failed",
			zap.Error(err),
			zap.String("key_prefix", maskKey(apiKey)),
		)

		// Mark portal as unhealthy for graceful degradation
		m.markPortalUnhealthy()

		// Try graceful degradation: use expired cache if available
		if cached := m.getCachedClaims(cacheKey); cached != nil {
			m.logger.Info("Using gracefully degraded cache after Portal failure",
				zap.String("key_prefix", maskKey(apiKey)),
			)
			return cached, nil
		}

		return nil, fmt.Errorf("portal request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// Mark portal as unhealthy for 5xx errors
		if resp.StatusCode >= 500 {
			m.markPortalUnhealthy()

			// Try graceful degradation
			if cached := m.getCachedClaims(cacheKey); cached != nil {
				m.logger.Info("Using gracefully degraded cache after Portal error",
					zap.String("key_prefix", maskKey(apiKey)),
					zap.Int("status", resp.StatusCode),
				)
				return cached, nil
			}
			return nil, fmt.Errorf("PORTAL_UNAVAILABLE: portal returned status %d", resp.StatusCode)
		}

		// 4xx: classify by portal's error_code instead of collapsing everything
		// into a generic auth failure, so the client gets a meaningful error.
		var errBody struct {
			ErrorCode string `json:"error_code"`
			Error     string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		switch errBody.ErrorCode {
		case "BALANCE_EXHAUSTED", "ENTERPRISE_CREDIT_EXHAUSTED", "MEMBER_QUOTA_EXCEEDED":
			return nil, fmt.Errorf("BALANCE_EXHAUSTED: %s", errBody.Error)
		case "QUOTA_EXCEEDED":
			return nil, fmt.Errorf("QUOTA_EXCEEDED: %s", errBody.Error)
		}
		return nil, fmt.Errorf("portal returned status %d", resp.StatusCode)
	}

	var result struct {
		Valid         bool     `json:"valid"`
		UserID        string   `json:"user_id"`
		APIKeyID      string   `json:"api_key_id"`
		Role          string   `json:"role"`
		AccessMode    string   `json:"access_mode"`
		AllowedModels []string `json:"allowed_models"`
		RateLimitRPM  int      `json:"rate_limit_rpm"`
		RateLimitTPM  int      `json:"rate_limit_tpm"`
		Balance       float64  `json:"balance"`
		QuotaExceeded bool     `json:"quota_exceeded"`
		QuotaWindow   string   `json:"quota_window"`
		// Phase 1: group fields
		GroupID       string   `json:"group_id"`
		KeyType       string   `json:"key_type"`
		CurrentModel  string   `json:"current_model"`
		BillingMode   string   `json:"billing_mode"`
		PricingRatio  float64  `json:"pricing_ratio"`
		GroupPriority []string `json:"group_priority"`
		Models        []string `json:"models"`
		// Phase 2: model routing
		ModelRoutingMode string   `json:"model_routing_mode"`
		SelectedModels   []string `json:"selected_models"`
		// Subscription rate limits
		SubRpmLimit int `json:"sub_rpm_limit"`
		SubTpmLimit int `json:"sub_tpm_limit"`
		SubRphLimit int `json:"sub_rph_limit"`
		SubTphLimit int `json:"sub_tph_limit"`
		// Phase 2 subscription limits (previously dropped — caused quota escape)
		SubConcurrentLimit int   `json:"sub_concurrent_limit"`
		SubRp5hLimit       int   `json:"sub_rp5h_limit"`
		SubRpdLimit        int   `json:"sub_rpd_limit"`
		SubRpwLimit        int   `json:"sub_rpw_limit"`
		SubTpdLimit        int   `json:"sub_tpd_limit"`
		SubTpwLimit        int   `json:"sub_tpw_limit"`
		SubTPMonthLimit    int   `json:"sub_tpmonth_limit"`
		SubDailyLimit      int64 `json:"sub_daily_limit"`
		SubMonthlyQuota    int64 `json:"sub_monthly_quota"`
		// Group rate limits
		GroupRPM             int `json:"group_rpm"`
		GroupRPH             int `json:"group_rph"`
		GroupRP5H            int `json:"group_rp5h"`
		GroupRPW             int `json:"group_rpw"`
		GroupRPMonth         int `json:"group_rpmonth"`
		GroupTPM             int `json:"group_tpm"`
		GroupTPH             int `json:"group_tph"`
		GroupTPD             int `json:"group_tpd"`
		GroupTPW             int `json:"group_tpw"`
		GroupTPMonth         int `json:"group_tpmonth"`
		GroupConcurrentLimit int `json:"group_concurrent_limit"`
		// Retention policy (absent on older Portals)
		EnterpriseID  string `json:"enterprise_id"`
		RetentionMode string `json:"retention_mode"`
		RetentionDays int    `json:"retention_days"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if !result.Valid {
		// Cache negative result (disabled/unknown key) so repeated lookups
		// skip the portal round-trip within negativeCacheTTL.
		m.cacheNegative(cacheKey)
		return nil, fmt.Errorf("api key not valid")
	}

	retMode, retDays := NormalizeRetention(result.RetentionMode, result.RetentionDays, m.cfg.Retention.MaxAge)

	// 4. Cache the result
	claims := &Claims{
		UserID:        result.UserID,
		APIKeyID:      result.APIKeyID,
		KeyHash:       hashKeySHA256(apiKey),
		Role:          result.Role,
		AccessMode:    result.AccessMode,
		AllowedModels: result.AllowedModels,
		RateLimitRPM:  result.RateLimitRPM,
		RateLimitTPM:  result.RateLimitTPM,
		Balance:       floatToMicro(result.Balance),
		// Phase 1: group fields
		GroupID:       result.GroupID,
		KeyType:       result.KeyType,
		CurrentModel:  result.CurrentModel,
		BillingMode:   result.BillingMode,
		PricingRatio:  result.PricingRatio,
		GroupPriority: result.GroupPriority,
		Models:        result.Models,
		// Phase 2: model routing
		ModelRoutingMode: result.ModelRoutingMode,
		SelectedModels:   result.SelectedModels,
		// Subscription rate limits
		SubRpmLimit:        result.SubRpmLimit,
		SubTpmLimit:        result.SubTpmLimit,
		SubRphLimit:        result.SubRphLimit,
		SubTphLimit:        result.SubTphLimit,
		SubConcurrentLimit: result.SubConcurrentLimit,
		SubRp5hLimit:       result.SubRp5hLimit,
		SubRpdLimit:        result.SubRpdLimit,
		SubRpwLimit:        result.SubRpwLimit,
		SubTpdLimit:        result.SubTpdLimit,
		SubTpwLimit:        result.SubTpwLimit,
		SubTPMonthLimit:    result.SubTPMonthLimit,
		SubDailyLimit:      result.SubDailyLimit,
		SubMonthlyQuota:    result.SubMonthlyQuota,
		// Group rate limits
		GroupRPM:             result.GroupRPM,
		GroupRPH:             result.GroupRPH,
		GroupRP5H:            result.GroupRP5H,
		GroupRPW:             result.GroupRPW,
		GroupRPMonth:         result.GroupRPMonth,
		GroupTPM:             result.GroupTPM,
		GroupTPH:             result.GroupTPH,
		GroupTPD:             result.GroupTPD,
		GroupTPW:             result.GroupTPW,
		GroupTPMonth:         result.GroupTPMonth,
		GroupConcurrentLimit: result.GroupConcurrentLimit,
		EnterpriseID:         result.EnterpriseID,
		RetentionMode:        retMode,
		RetentionDays:        retDays,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
		},
	}
	// Check for balance/quota issues before caching — prevents bypass via cache on next request
	// (fix: cacheClaims must come after these checks, not before)
	// Enterprise keys use enterprise credit; subscription keys are gated by the
	// portal on subscription quota — neither is bound by personal balance.
	if result.Balance <= 0 && result.KeyType != "enterprise" && result.KeyType != "subscription" {
		// Balance exhausted - permanent until top-up (non-enterprise keys only)
		// Enterprise keys use enterprise credit, not personal balance
		return nil, fmt.Errorf("BALANCE_EXHAUSTED: user %s has zero balance", result.UserID)
	}
	if result.QuotaExceeded {
		// Quota exhausted - temporary, will reset after window
		return nil, fmt.Errorf("QUOTA_EXCEEDED: user %s quota exceeded in window %s", result.UserID, result.QuotaWindow)
	}

	// Seed the local balance cache from the portal response.
	// Enterprise keys use enterprise credit; subscription keys draw on
	// subscription quota — neither spends from the personal balance cache.
	if result.KeyType != "enterprise" && result.KeyType != "subscription" {
		m.setBalance(result.UserID, floatToMicro(result.Balance))
	}

	m.cacheClaims(cacheKey, claims)

	m.logger.Debug("API key validated and cached",
		zap.String("key_prefix", maskKey(apiKey)),
		zap.String("user_id", result.UserID),
	)

	return claims, nil
}

// hashKeySHA256 - Hash API key with SHA256 for cache key
func hashKeySHA256(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

// maskKey - Mask API key for safe logging
func maskKey(key string) string {
	if len(key) < 8 {
		return "****"
	}
	return key[:4] + "..." + key[len(key)-2:]
}

// hashCacheKey - SHA-256 hash of API key for cache key (no raw key in memory map)
func hashCacheKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

// Retention policy modes carried in the key-validation response.
const (
	RetentionModeOff      = "off"
	RetentionModeMetadata = "metadata"
	RetentionModeStandard = "standard"

	maxRetentionDays = 365
)

// NormalizeRetention maps the Portal-supplied policy onto safe values. Missing
// or unknown fields (older Portal) fall back to the pre-existing behaviour:
// full capture for the configured max age. Unknown modes become "standard"
// rather than "off" so a typo cannot silently disable retention for a tenant
// that is contractually required to keep it.
func NormalizeRetention(mode string, days int, defaultMaxAge time.Duration) (string, int) {
	switch mode {
	case RetentionModeOff, RetentionModeMetadata, RetentionModeStandard:
	default:
		mode = RetentionModeStandard
	}
	if days <= 0 {
		days = int((defaultMaxAge + 24*time.Hour - 1) / (24 * time.Hour))
		if days <= 0 {
			days = 90
		}
	}
	if days > maxRetentionDays {
		days = maxRetentionDays
	}
	return mode, days
}

package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mayiants/ai-proxy-go/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestLockFreeBucket_BasicAllow(t *testing.T) {
	b := newLockFreeBucket(10)
	// Should allow requests within limit
	for i := 0; i < 10; i++ {
		assert.True(t, b.AllowN(1, 10, 1), "request %d should be allowed", i)
	}
	// 11th request should be denied
	assert.False(t, b.AllowN(1, 10, 1), "11th request should be denied")
}

func TestLockFreeBucket_ZeroOrNegativeN(t *testing.T) {
	b := newLockFreeBucket(10)
	assert.True(t, b.AllowN(1, 10, 0), "zero tokens should always be allowed")
	assert.True(t, b.AllowN(1, 10, -1), "negative tokens should always be allowed")
}

func TestLockFreeBucket_RefillOverTime(t *testing.T) {
	b := newLockFreeBucket(5)
	// Consume all tokens
	for i := 0; i < 5; i++ {
		assert.True(t, b.AllowN(10, 5, 1), "request %d should be allowed", i)
	}
	assert.False(t, b.AllowN(10, 5, 1), "should be denied immediately after exhaustion")

	// Wait for refill (rate=10 tokens/sec, so 0.2s = 2 tokens)
	time.Sleep(250 * time.Millisecond)
	assert.True(t, b.AllowN(10, 5, 1), "should be allowed after partial refill")
	assert.True(t, b.AllowN(10, 5, 1), "second request should be allowed")
	assert.False(t, b.AllowN(10, 5, 1), "third request should be denied (only 2 refilled)")
}

func TestLockFreeBucket_MaxTokensCap(t *testing.T) {
	b := newLockFreeBucket(3)
	// Consume all
	for i := 0; i < 3; i++ {
		assert.True(t, b.AllowN(1, 3, 1))
	}
	// Wait long enough to refill way beyond max
	time.Sleep(5 * time.Second)
	// Should still only have maxTokens = 3
	assert.True(t, b.AllowN(1, 3, 1))
	assert.True(t, b.AllowN(1, 3, 1))
	assert.True(t, b.AllowN(1, 3, 1))
	assert.False(t, b.AllowN(1, 3, 1), "should not exceed maxTokens cap")
}

func TestLockFreeBucket_ConcurrentAccess(t *testing.T) {
	b := newLockFreeBucket(1000)
	var wg sync.WaitGroup
	var allowed atomic.Int64

	// rate=0 prevents token refill during the concurrent burst so the test is deterministic.
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if b.AllowN(0, 1000, 1) {
					allowed.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	// The bucket must not have allowed more than maxTokens requests.
	assert.LessOrEqual(t, allowed.Load(), int64(1000), "concurrent requests should not exceed bucket capacity")
	// With rate=0 the bucket should be empty after the burst.
	assert.False(t, b.AllowN(0, 1000, 1), "all tokens should have been consumed by concurrent goroutines")
}

func TestLocalCounter_AllowAndAllowN(t *testing.T) {
	lc := newLocalCounter(5, time.Minute)
	key := "test-key"

	// Allow 5 requests
	for i := 0; i < 5; i++ {
		assert.True(t, lc.Allow(key), "request %d should be allowed", i)
	}
	assert.False(t, lc.Allow(key), "6th request should be denied")

	// AllowN with n=2
	lc2 := newLocalCounter(5, time.Minute)
	assert.True(t, lc2.AllowN(key, 2), "AllowN(2) should be allowed")
	assert.True(t, lc2.AllowN(key, 2), "AllowN(2) should be allowed (2 remaining)")
	assert.False(t, lc2.AllowN(key, 2), "AllowN(2) should be denied (0 remaining)")
	assert.True(t, lc2.AllowN(key, 1), "AllowN(1) should be allowed")
}

func TestLocalCounter_DifferentKeys(t *testing.T) {
	lc := newLocalCounter(3, time.Minute)
	assert.True(t, lc.Allow("key-a"))
	assert.True(t, lc.Allow("key-a"))
	assert.True(t, lc.Allow("key-a"))
	assert.False(t, lc.Allow("key-a"))

	// key-b should have its own bucket
	assert.True(t, lc.Allow("key-b"))
	assert.True(t, lc.Allow("key-b"))
	assert.True(t, lc.Allow("key-b"))
	assert.False(t, lc.Allow("key-b"))
}

func TestMiddleware_AllowRPM(t *testing.T) {
	cfg := &config.Config{
		RateLimit: config.RateLimitConfig{
			Enabled:     true,
			DefaultRPM:  60,
			DefaultTPM:  100000,
			RedisPrefix: "rl:test:",
		},
	}
	logger, _ := zap.NewDevelopment()
	m := NewMiddleware(cfg, logger)

	key := "user:test-key"
	// Allow 5 RPM
	for i := 0; i < 5; i++ {
		assert.True(t, m.AllowRPM(key, 5), "request %d should be allowed", i)
	}
	assert.False(t, m.AllowRPM(key, 5), "6th request should be denied")
}

func TestMiddleware_AllowRPH(t *testing.T) {
	cfg := &config.Config{
		RateLimit: config.RateLimitConfig{
			Enabled:     true,
			DefaultRPM:  60,
			DefaultTPM:  100000,
			RedisPrefix: "rl:test:",
		},
	}
	logger, _ := zap.NewDevelopment()
	m := NewMiddleware(cfg, logger)

	key := "user:test-key"
	// Allow 3 RPH
	for i := 0; i < 3; i++ {
		assert.True(t, m.AllowRPH(key, 3), "request %d should be allowed", i)
	}
	assert.False(t, m.AllowRPH(key, 3), "4th request should be denied")
}

func TestMiddleware_RecordTokens(t *testing.T) {
	cfg := &config.Config{
		RateLimit: config.RateLimitConfig{
			Enabled:     true,
			DefaultRPM:  60,
			DefaultTPM:  100000,
			RedisPrefix: "rl:test:",
		},
	}
	logger, _ := zap.NewDevelopment()
	m := NewMiddleware(cfg, logger)

	key := "user:test-key"
	// TPM limit of 10
	assert.True(t, m.RecordTokens(key, 10, 5), "5 tokens should be allowed")
	assert.True(t, m.RecordTokens(key, 10, 5), "5 more tokens should be allowed")
	assert.False(t, m.RecordTokens(key, 10, 1), "1 more token should be denied")
}

func TestMiddleware_CheckTPH(t *testing.T) {
	cfg := &config.Config{
		RateLimit: config.RateLimitConfig{
			Enabled:     true,
			DefaultRPM:  60,
			DefaultTPM:  100000,
			RedisPrefix: "rl:test:",
		},
	}
	logger, _ := zap.NewDevelopment()
	m := NewMiddleware(cfg, logger)

	key := "user:test-key"
	// TPH limit of 15
	assert.True(t, m.CheckTPH(key, 15, 10), "10 tokens should be allowed")
	assert.True(t, m.CheckTPH(key, 15, 5), "5 more tokens should be allowed")
	assert.False(t, m.CheckTPH(key, 15, 1), "1 more token should be denied")
}

func TestMiddleware_AllowRP5H(t *testing.T) {
	cfg := &config.Config{
		RateLimit: config.RateLimitConfig{
			Enabled:     true,
			DefaultRPM:  60,
			DefaultTPM:  100000,
			RedisPrefix: "rl:test:",
		},
	}
	logger, _ := zap.NewDevelopment()
	m := NewMiddleware(cfg, logger)

	key := "user:test-key"
	// Allow 3 RP5H
	for i := 0; i < 3; i++ {
		assert.True(t, m.AllowRP5H(key, 3), "request %d should be allowed", i)
	}
	assert.False(t, m.AllowRP5H(key, 3), "4th request should be denied")
	// Zero limit should always allow
	assert.True(t, m.AllowRP5H(key, 0), "RP5H=0 should allow")
}

func TestMiddleware_AllowRPW(t *testing.T) {
	cfg := &config.Config{
		RateLimit: config.RateLimitConfig{
			Enabled:     true,
			DefaultRPM:  60,
			DefaultTPM:  100000,
			RedisPrefix: "rl:test:",
		},
	}
	logger, _ := zap.NewDevelopment()
	m := NewMiddleware(cfg, logger)

	key := "user:test-key"
	// Allow 2 RPW
	assert.True(t, m.AllowRPW(key, 2), "request 1 should be allowed")
	assert.True(t, m.AllowRPW(key, 2), "request 2 should be allowed")
	assert.False(t, m.AllowRPW(key, 2), "3rd request should be denied")
	// Zero limit should always allow
	assert.True(t, m.AllowRPW(key, 0), "RPW=0 should allow")
}

func TestMiddleware_CheckAndRecordTokens(t *testing.T) {
	cfg := &config.Config{
		RateLimit: config.RateLimitConfig{
			Enabled:     true,
			DefaultRPM:  60,
			DefaultTPM:  100000,
			RedisPrefix: "rl:test:",
		},
	}
	logger, _ := zap.NewDevelopment()
	m := NewMiddleware(cfg, logger)

	key := "user:test-key"
	// keyTPM=100, subTPM=10, groupTPM=0 → effective should be 10
	effective, allowed := m.CheckAndRecordTokens(key, 100, 10, 0, 5)
	assert.Equal(t, 10, effective)
	assert.True(t, allowed)

	// Second call: 5 more tokens (at limit)
	effective, allowed = m.CheckAndRecordTokens(key, 100, 10, 0, 5)
	assert.Equal(t, 10, effective)
	assert.True(t, allowed)

	// Third call: exceeded
	effective, allowed = m.CheckAndRecordTokens(key, 100, 10, 0, 1)
	assert.Equal(t, 10, effective)
	assert.False(t, allowed)

	// Group limit is the strictest
	effective, allowed = m.CheckAndRecordTokens(key, 100, 10, 5, 1)
	assert.Equal(t, 5, effective)
	assert.True(t, allowed)
}

func TestMiddleware_ZeroLimits(t *testing.T) {
	cfg := &config.Config{
		RateLimit: config.RateLimitConfig{
			Enabled:     true,
			DefaultRPM:  60,
			DefaultTPM:  100000,
			RedisPrefix: "rl:test:",
		},
	}
	logger, _ := zap.NewDevelopment()
	m := NewMiddleware(cfg, logger)

	key := "user:test-key"
	// Zero or negative limits should always allow
	assert.True(t, m.AllowRPM(key, 0), "RPM=0 should allow")
	assert.True(t, m.AllowRPM(key, -1), "RPM=-1 should allow")
	assert.True(t, m.AllowRPH(key, 0), "RPH=0 should allow")
	assert.True(t, m.RecordTokens(key, 0, 100), "TPM=0 should allow")
	assert.True(t, m.RecordTokens(key, -1, 100), "TPM=-1 should allow")
	assert.True(t, m.CheckTPH(key, 0, 100), "TPH=0 should allow")
}

func TestGetEffectiveLimits(t *testing.T) {
	// Key limits only
	info := RateLimitInfo{KeyRPM: 60, KeyTPM: 100000}
	rpm, tpm, rph, tph, rp5h, rpw, tpmonth := GetEffectiveLimits(info)
	assert.Equal(t, 60, rpm)
	assert.Equal(t, 100000, tpm)
	assert.Equal(t, 0, rph)
	assert.Equal(t, 0, tph)
	assert.Equal(t, 0, rp5h)
	assert.Equal(t, 0, rpw)
	assert.Equal(t, 0, tpmonth)

	// Subscription is more restrictive
	info = RateLimitInfo{KeyRPM: 60, KeyTPM: 100000, SubRPM: 30, SubTPM: 50000, SubRPH: 1000, SubTPH: 200000, SubRP5H: 50, SubRPW: 200}
	rpm, tpm, rph, tph, rp5h, rpw, tpmonth = GetEffectiveLimits(info)
	assert.Equal(t, 30, rpm)
	assert.Equal(t, 50000, tpm)
	assert.Equal(t, 1000, rph)
	assert.Equal(t, 200000, tph)
	assert.Equal(t, 50, rp5h)
	assert.Equal(t, 200, rpw)
	assert.Equal(t, 0, tpmonth)

	// Key limit is more restrictive (subscription is 0 or higher)
	info = RateLimitInfo{KeyRPM: 60, KeyTPM: 100000, SubRPM: 120, SubTPM: 200000}
	rpm, tpm, _, _, rp5h, rpw, tpmonth = GetEffectiveLimits(info)
	assert.Equal(t, 60, rpm)
	assert.Equal(t, 100000, tpm)
	assert.Equal(t, 0, rp5h)
	assert.Equal(t, 0, rpw)
	assert.Equal(t, 0, tpmonth)

	// Group limit is the strictest
	info = RateLimitInfo{KeyRPM: 60, KeyTPM: 100000, SubRPM: 30, SubTPM: 50000, GroupRPM: 10, GroupTPM: 10000, GroupTPMonth: 1000000}
	rpm, tpm, rph, tph, rp5h, rpw, tpmonth = GetEffectiveLimits(info)
	assert.Equal(t, 10, rpm)
	assert.Equal(t, 10000, tpm)
	assert.Equal(t, 0, rph)
	assert.Equal(t, 0, tph)
	assert.Equal(t, 0, rp5h)
	assert.Equal(t, 0, rpw)
	assert.Equal(t, 1000000, tpmonth)
}

func TestMiddleware_ConcurrentRPM(t *testing.T) {
	cfg := &config.Config{
		RateLimit: config.RateLimitConfig{
			Enabled:     true,
			DefaultRPM:  60,
			DefaultTPM:  100000,
			RedisPrefix: "rl:test:",
		},
	}
	logger, _ := zap.NewDevelopment()
	m := NewMiddleware(cfg, logger)

	key := "user:concurrent"
	limit := 1000

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 30; j++ {
				_ = m.AllowRPM(key, limit)
			}
		}()
	}
	wg.Wait()

	// Count remaining capacity
	remaining := 0
	for i := 0; i < limit*2; i++ {
		if m.AllowRPM(key, limit) {
			remaining++
		} else {
			break
		}
	}
	// Due to CAS contention, some requests may fall through when retries are exhausted,
	// so remaining should be small (ideally 0, but allow some tolerance)
	assert.Less(t, remaining, limit/10, "remaining capacity should be small after concurrent burst")
}

func TestMiddleware_AllowRPD(t *testing.T) {
	cfg := &config.Config{
		RateLimit: config.RateLimitConfig{
			Enabled:     true,
			DefaultRPM:  60,
			DefaultTPM:  100000,
			RedisPrefix: "rl:test:",
		},
	}
	logger, _ := zap.NewDevelopment()
	m := NewMiddleware(cfg, logger)

	key := "user:test-key"
	// Allow 3 RPD
	for i := 0; i < 3; i++ {
		assert.True(t, m.AllowRPD(key, 3), "request %d should be allowed", i)
	}
	assert.False(t, m.AllowRPD(key, 3), "4th request should be denied")
	// Zero limit should always allow
	assert.True(t, m.AllowRPD(key, 0), "RPD=0 should allow")
}

func TestMiddleware_CheckTPD(t *testing.T) {
	cfg := &config.Config{
		RateLimit: config.RateLimitConfig{
			Enabled:     true,
			DefaultRPM:  60,
			DefaultTPM:  100000,
			RedisPrefix: "rl:test:",
		},
	}
	logger, _ := zap.NewDevelopment()
	m := NewMiddleware(cfg, logger)

	key := "user:test-key"
	// TPD limit of 15
	assert.True(t, m.CheckTPD(key, 15, 10), "10 tokens should be allowed")
	assert.True(t, m.CheckTPD(key, 15, 5), "5 more tokens should be allowed")
	assert.False(t, m.CheckTPD(key, 15, 1), "1 more token should be denied")
}

func TestMiddleware_AllowConcurrent(t *testing.T) {
	cfg := &config.Config{
		RateLimit: config.RateLimitConfig{
			Enabled:     true,
			DefaultRPM:  60,
			DefaultTPM:  100000,
			RedisPrefix: "rl:test:",
		},
	}
	logger, _ := zap.NewDevelopment()
	m := NewMiddleware(cfg, logger)

	key := "user:test-key"
	// Allow 3 concurrent
	assert.True(t, m.AllowConcurrent(key, 3), "1st concurrent should be allowed")
	assert.True(t, m.AllowConcurrent(key, 3), "2nd concurrent should be allowed")
	assert.True(t, m.AllowConcurrent(key, 3), "3rd concurrent should be allowed")
	assert.False(t, m.AllowConcurrent(key, 3), "4th concurrent should be denied")

	// Release one
	m.ReleaseConcurrent(key)
	assert.True(t, m.AllowConcurrent(key, 3), "should allow after release")

	// Zero limit should always allow
	assert.True(t, m.AllowConcurrent(key, 0), "concurrent=0 should allow")

	// Cleanup: release all
	m.ReleaseConcurrent(key)
	m.ReleaseConcurrent(key)
	m.ReleaseConcurrent(key)
	m.ReleaseConcurrent(key)
}

func TestMiddleware_RetryAfter(t *testing.T) {
	cfg := &config.Config{
		RateLimit: config.RateLimitConfig{
			Enabled:     true,
			DefaultRPM:  60,
			DefaultTPM:  100000,
			RedisPrefix: "rl:test:",
		},
	}
	logger, _ := zap.NewDevelopment()
	m := NewMiddleware(cfg, logger)

	// retryAfter should return a positive integer string for all types
	assert.NotEmpty(t, m.retryAfter("rpm"))
	assert.NotEmpty(t, m.retryAfter("rph"))
	assert.NotEmpty(t, m.retryAfter("rp5h"))
	assert.NotEmpty(t, m.retryAfter("rpd"))
	assert.NotEmpty(t, m.retryAfter("rpw"))
	assert.Equal(t, "60", m.retryAfter("unknown"))
}

// TestMiddleware_ConcurrentSlotReleasedOnPanic pins the fix for the leaked
// concurrent slot: when a downstream handler panics, the deferred release
// must still run (previously the post-Next release was skipped, permanently
// inflating the counter until process restart).
func TestMiddleware_ConcurrentSlotReleasedOnPanic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{
		RateLimit: config.RateLimitConfig{
			Enabled:     true,
			DefaultRPM:  60,
			DefaultTPM:  100000,
			RedisPrefix: "rl:test:",
		},
	}
	m := NewMiddleware(cfg, zap.NewNop())

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		c.Set("user_id", "user-1")
		c.Set("key_hash", "panic-key")
		c.Set("key_type", "general")
		c.Set("group_id", "panic-group")
		c.Set("group_concurrent_limit", 1)
		c.Next()
	})
	r.Use(m.Handler())
	r.POST("/panic", func(c *gin.Context) { panic("boom") })

	req := httptest.NewRequest(http.MethodPost, "/panic", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)

	// The slot must have been released despite the panic. Post-F5 the
	// concurrent counter is keyed by the group dimension (claims.GroupID),
	// not the legacy user:keyHash dimension.
	assert.True(t, m.AllowConcurrent("rl:test:group:panic-group", 1),
		"concurrent slot leaked after downstream panic")
}

// TestMiddleware_F5_DimensionSplit guards the F5 fix: RPM/TPM limits set at
// the subscription and group dimensions must be counted on a per-user /
// per-group redis key, not on the per-key keyHash dimension. Otherwise a user
// holding multiple API keys could escape a plan's RPM/TPM quota simply by
// rotating keys.
func TestMiddleware_F5_DimensionSplit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{
		RateLimit: config.RateLimitConfig{
			Enabled:     true,
			DefaultRPM:  10000,
			DefaultTPM:  1000000,
			RedisPrefix: "rl:test:",
		},
	}
	m := NewMiddleware(cfg, zap.NewNop())

	// Two real handlers (no upstream work) — we only care about whether the
	// second request is blocked by shared subscription quota.
	r := gin.New()
	r.Use(func(c *gin.Context) {
		// Emulate auth: same user, two different keys, no group.
		switch c.GetHeader("X-Test-Key") {
		case "A":
			c.Set("key_hash", "keyA")
		case "B":
			c.Set("key_hash", "keyB")
		}
		c.Set("user_id", "user-shared")
		c.Set("key_type", "subscription")
		c.Set("rate_limit_rpm", 0) // disable per-key default
		c.Set("rate_limit_tpm", 0)
		c.Set("sub_rpm_limit", 1) // plan's RPM quota = 1
		c.Set("group_rpm", 0)
		c.Next()
	})
	r.Use(m.Handler())
	r.POST("/v1/chat/completions", func(c *gin.Context) { c.Status(http.StatusOK) })

	do := func(key string) int {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		req.Header.Set("X-Test-Key", key)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	// First request (key A) consumes the single RPM slot from the shared
	// subscription bucket and must be allowed.
	assert.Equal(t, http.StatusOK, do("A"), "first request via key A must be allowed")

	// Second request replays the same subscription bucket via a different
	// key. Previously this was keyed on keyHash so key B's counter would be
	// fresh and the request would slip through (the F5 escape). Now it must
	// be rejected with 429.
	assert.Equal(t, http.StatusTooManyRequests, do("B"),
		"second request via a different key must still honor the shared subscription RPM bucket")
}

// TestMiddleware_F5_GroupSharedAcrossUsers verifies that a group-level RPM
// limit applies across all users in the group, regardless of their keys.
func TestMiddleware_F5_GroupSharedAcrossUsers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{
		RateLimit: config.RateLimitConfig{
			Enabled:     true,
			DefaultRPM:  10000,
			DefaultTPM:  1000000,
			RedisPrefix: "rl:test:",
		},
	}
	m := NewMiddleware(cfg, zap.NewNop())

	r := gin.New()
	r.Use(func(c *gin.Context) {
		// Two distinct users with distinct keys but in the same group.
		switch c.GetHeader("X-Test-User") {
		case "u1":
			c.Set("user_id", "user-1")
			c.Set("key_hash", "k1")
		case "u2":
			c.Set("user_id", "user-2")
			c.Set("key_hash", "k2")
		}
		c.Set("key_type", "general")
		c.Set("group_id", "group-shared")
		c.Set("rate_limit_rpm", 0)
		c.Set("rate_limit_tpm", 0)
		c.Set("sub_rpm_limit", 0)
		c.Set("group_rpm", 1) // group limit is 1 RPM
		c.Next()
	})
	r.Use(m.Handler())
	r.POST("/v1/chat/completions", func(c *gin.Context) { c.Status(http.StatusOK) })

	do := func(user string) int {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		req.Header.Set("X-Test-User", user)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	assert.Equal(t, http.StatusOK, do("u1"), "u1's first request consumes the only slot")
	// u2 in the same group should be rejected — the group RPM bucket is
	// shared by everyone in the group.
	assert.Equal(t, http.StatusTooManyRequests, do("u2"),
		"u2 in the same group must be rejected because the group RPM bucket is shared")
}

// TestMiddleware_SP5_MessagesAndCompletionsEstimate pins S-P5: the
// pre-request token estimate must produce a positive number for the
// Anthropic /v1/messages and the legacy /v1/completions paths, otherwise
// TPM/TPH/TPMonth hard pre-checks are silently bypassed for both client
// formats.
func TestMiddleware_SP5_MessagesAndCompletionsEstimate(t *testing.T) {
	cfg := &config.Config{
		RateLimit: config.RateLimitConfig{
			Enabled:     true,
			DefaultRPM:  60,
			DefaultTPM:  100000,
			RedisPrefix: "rl:test:",
		},
	}
	m := NewMiddleware(cfg, zap.NewNop())

	mkCtx := func(body string) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
		return c
	}

	messagesCtx := mkCtx(`{"model":"claude-3-5-sonnet","max_tokens":128,"messages":[{"role":"user","content":"Hello world"}]}`)
	tok := m.estimateRequestTokens(messagesCtx)
	assert.Greater(t, tok, int64(0), "/v1/messages must produce a token estimate")

	completionsCtx := mkCtx(`{"model":"gpt-4o","max_tokens":64,"prompt":"Once upon a time"}`)
	completionsCtx.Request.URL.Path = "/v1/completions"
	tok = m.estimateRequestTokens(completionsCtx)
	assert.Greater(t, tok, int64(0), "/v1/completions must produce a token estimate")
}

package cache

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/ai-proxy/ai-proxy-go/dto"
	"github.com/ai-proxy/ai-proxy-go/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestNewAffinityCache(t *testing.T) {
	t.Run("default TTL", func(t *testing.T) {
		cache := NewAffinityCache(0)
		assert.NotNil(t, cache)
		assert.Equal(t, time.Hour, cache.defaultTTL)
		assert.Equal(t, 100000, cache.maxEntries)
		assert.Equal(t, "ai-proxy:affinity:v2", cache.redisPrefix)
	})

	t.Run("custom TTL", func(t *testing.T) {
		cache := NewAffinityCache(30 * time.Minute)
		assert.NotNil(t, cache)
		assert.Equal(t, 30*time.Minute, cache.defaultTTL)
	})
}

func TestNewAffinityCacheWithConfig(t *testing.T) {
	t.Run("full config", func(t *testing.T) {
		cfg := &AffinityConfig{
			RedisPrefix: "test-prefix",
			MaxEntries:  500,
			DefaultTTL:  10 * time.Minute,
		}
		cache := NewAffinityCacheWithConfig(cfg)
		assert.NotNil(t, cache)
		assert.Equal(t, "test-prefix", cache.redisPrefix)
		assert.Equal(t, 500, cache.maxEntries)
		assert.Equal(t, 10*time.Minute, cache.defaultTTL)
	})

	t.Run("empty config uses defaults", func(t *testing.T) {
		cfg := &AffinityConfig{}
		cache := NewAffinityCacheWithConfig(cfg)
		assert.NotNil(t, cache)
		assert.Equal(t, 100000, cache.maxEntries)
		assert.Equal(t, time.Hour, cache.defaultTTL)
		assert.Equal(t, "ai-proxy:affinity:v2", cache.redisPrefix)
	})
}

func TestAffinityCache_GetPreferredKeyID(t *testing.T) {
	cache := NewAffinityCache(time.Hour)

	t.Run("empty cache key returns empty", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		w := &mockResponseWriter{}
		c, _ := gin.CreateTestContext(w)

		keyID := cache.GetPreferredKeyID(c, "gpt-4", "openai", "high")
		assert.Empty(t, keyID)
	})

	t.Run("returns stored key from local cache", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		w := &mockResponseWriter{}
		c, _ := gin.CreateTestContext(w)
		c.Set("request_body", []byte("test request body"))

		// First record a key
		cache.RecordPreferredKeyID(c, "gpt-4", "openai", "key-123", "high")

		// Then retrieve it
		keyID := cache.GetPreferredKeyID(c, "gpt-4", "openai", "high")
		assert.Equal(t, "key-123", keyID)
	})

	t.Run("different model returns different key", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		w := &mockResponseWriter{}
		c1, _ := gin.CreateTestContext(w)
		c1.Set("request_body", []byte("test"))

		cache.RecordPreferredKeyID(c1, "gpt-4", "openai", "key-gpt4", "high")

		c2, _ := gin.CreateTestContext(w)
		c2.Set("request_body", []byte("test"))

		keyID := cache.GetPreferredKeyID(c2, "claude-3", "anthropic", "high")
		assert.Empty(t, keyID)
	})
}

func TestAffinityCache_RecordPreferredKeyID(t *testing.T) {
	cache := NewAffinityCache(time.Hour)

	t.Run("empty key ID is ignored", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		w := &mockResponseWriter{}
		c, _ := gin.CreateTestContext(w)
		c.Set("request_body", []byte("test"))

		// Should not panic
		cache.RecordPreferredKeyID(c, "gpt-4", "openai", "", "high")

		keyID := cache.GetPreferredKeyID(c, "gpt-4", "openai", "high")
		assert.Empty(t, keyID)
	})

	t.Run("stores and retrieves key correctly", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		w := &mockResponseWriter{}
		c, _ := gin.CreateTestContext(w)
		c.Set("request_body", []byte("test body"))

		cache.RecordPreferredKeyID(c, "gpt-4", "openai", "key-abc", "high")

		c2, _ := gin.CreateTestContext(w)
		c2.Set("request_body", []byte("test body"))

		keyID := cache.GetPreferredKeyID(c2, "gpt-4", "openai", "high")
		assert.Equal(t, "key-abc", keyID)
	})

	// Regression: the handler resolves the routing track and swaps the logical
	// model for the upstream model ID only AFTER the GetPreferredKeyID call, so
	// RecordPreferredKeyID runs with a different model/track. Get and Record
	// must still agree on the affinity key or sessions never stick.
	t.Run("get and record stay consistent across model and track mutation", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		w := &mockResponseWriter{}
		body := []byte(`{"model":"claude-sonnet-4-5","system":"You are a probe.","messages":[{"role":"user","content":"hello"}]}`)

		// Request 1: Get with the logical model and no routing track set yet.
		c1, _ := gin.CreateTestContext(w)
		c1.Set("request_body", body)
		assert.Empty(t, cache.GetPreferredKeyID(c1, "claude-sonnet-4-5", "default", "high"))

		// Handler sets the track and swaps in the upstream model ID, then records.
		c1.Set("routing_track", "bedrock")
		cache.RecordPreferredKeyID(c1, "anthropic.claude-sonnet-4-5-20250929-v1:0", "default", "key-A", "high")

		// Request 2 (same conversation): Get with the logical model must hit key-A.
		c2, _ := gin.CreateTestContext(w)
		c2.Set("request_body", body)
		assert.Equal(t, "key-A", cache.GetPreferredKeyID(c2, "claude-sonnet-4-5", "default", "high"))
	})
}

func TestAffinityCache_Clear(t *testing.T) {
	cache := NewAffinityCache(time.Hour)

	t.Run("clears local cache", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		w := &mockResponseWriter{}
		c, _ := gin.CreateTestContext(w)
		c.Set("request_body", []byte("test"))

		cache.RecordPreferredKeyID(c, "gpt-4", "openai", "key-123", "high")

		// Verify it's there
		keyID := cache.GetPreferredKeyID(c, "gpt-4", "openai", "high")
		assert.Equal(t, "key-123", keyID)

		// Clear
		count := cache.Clear()
		assert.GreaterOrEqual(t, count, 1)

		// Verify it's gone
		c2, _ := gin.CreateTestContext(w)
		c2.Set("request_body", []byte("test"))
		keyID = cache.GetPreferredKeyID(c2, "gpt-4", "openai", "high")
		assert.Empty(t, keyID)
	})
}

func TestAffinityCache_Stats(t *testing.T) {
	cache := NewAffinityCache(time.Hour)

	t.Run("returns stats with no entries", func(t *testing.T) {
		stats := cache.Stats()
		assert.NotNil(t, stats)
		assert.Equal(t, 100000, stats["max_entries"])
		assert.Equal(t, "1h0m0s", stats["default_ttl"])
		assert.Equal(t, 0, stats["local_entries"])
	})

	t.Run("returns stats with entries", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		w := &mockResponseWriter{}
		c, _ := gin.CreateTestContext(w)
		c.Set("request_body", []byte("test"))

		cache.RecordPreferredKeyID(c, "gpt-4", "openai", "key-123", "high")

		stats := cache.Stats()
		assert.NotNil(t, stats)
		assert.Equal(t, 1, stats["local_entries"])
	})
}

func TestAffinityCache_ObserveUsageCache(t *testing.T) {
	cache := NewAffinityCache(time.Hour)

	t.Run("nil usage is ignored", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		w := &mockResponseWriter{}
		c, _ := gin.CreateTestContext(w)

		// Should not panic
		cache.ObserveUsageCache(c, nil, types.RelayFormatOpenAI)
	})

	t.Run("records usage stats", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		w := &mockResponseWriter{}
		c, _ := gin.CreateTestContext(w)
		c.Set(GinKeyAffinityMeta, "test-rule")
		c.Set("request_fingerprint", "fp-123")

		usage := &dto.Usage{
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      150,
		}

		cache.ObserveUsageCache(c, usage, types.RelayFormatOpenAI)

		// Verify stats were recorded
		stats := cache.Stats()
		assert.NotNil(t, stats)
		assert.Equal(t, int64(1), stats["total_cache_requests"])
	})
}

func TestAffinityFingerprint(t *testing.T) {
	t.Run("empty string", func(t *testing.T) {
		assert.Empty(t, affinityFingerprint(""))
	})

	t.Run("returns 8 char hash", func(t *testing.T) {
		result := affinityFingerprint("test")
		assert.Len(t, result, 8)
	})
}

func TestGinKeyConstants(t *testing.T) {
	// Verify constants are defined and non-empty
	require.NotEmpty(t, GinKeyAffinityCacheKey)
	require.NotEmpty(t, GinKeyAffinityTTLSeconds)
	require.NotEmpty(t, GinKeyAffinityMeta)
	require.NotEmpty(t, GinKeyAffinitySkipRetry)
}

// mockResponseWriter is a minimal mock for gin.ResponseWriter
type mockResponseWriter struct{}

func (m *mockResponseWriter) WriteHeaderNow()                              {}
func (m *mockResponseWriter) Written() bool                                { return false }
func (m *mockResponseWriter) WriteString(s string) (int, error)            { return len(s), nil }
func (m *mockResponseWriter) Status() int                                  { return 200 }
func (m *mockResponseWriter) Size() int                                    { return 0 }
func (m *mockResponseWriter) WriteHeader(code int)                         {}
func (m *mockResponseWriter) Header() http.Header                          { return http.Header{} }
func (m *mockResponseWriter) Write([]byte) (int, error)                    { return 0, nil }
func (m *mockResponseWriter) WriteHeaderString(s string)                   {}
func (m *mockResponseWriter) Pusher() http.Pusher                          { return nil }
func (m *mockResponseWriter) Flush()                                       {}
func (m *mockResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) { return nil, nil, nil }
func (m *mockResponseWriter) CloseNotify() <-chan bool                     { return nil }
func (m *mockResponseWriter) Error() error                                 { return nil }

// Ensure mockResponseWriter implements gin.ResponseWriter
var _ gin.ResponseWriter = (*mockResponseWriter)(nil)

func countSyncMap(m *sync.Map) int {
	var n int
	m.Range(func(_, _ interface{}) bool {
		n++
		return true
	})
	return n
}

func TestAffinityCache_Eviction(t *testing.T) {
	cache := NewAffinityCacheWithConfig(&AffinityConfig{
		MaxEntries: 100,
		DefaultTTL: time.Hour,
	})

	// Fill local cache and stats beyond maxEntries.
	for i := 0; i < 200; i++ {
		cache.localCache.Store(fmt.Sprintf("key-%d", i), fmt.Sprintf("value-%d", i))
		cache.stats.Store(fmt.Sprintf("stats-%d", i), &usageCacheStatsEntry{})
	}
	require.Equal(t, 200, countSyncMap(&cache.localCache))
	require.Equal(t, 200, countSyncMap(&cache.stats))

	cache.evictExcess(context.Background(), zap.NewNop())

	assert.LessOrEqual(t, countSyncMap(&cache.localCache), 90)
	assert.LessOrEqual(t, countSyncMap(&cache.stats), 90)
}

func TestConversationPrefixFingerprint(t *testing.T) {
	// Same session seed (system + first message), growing history:
	// fingerprint must be identical so the session sticks to one key.
	turn1 := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"system","content":"You are a coding agent."},{"role":"user","content":"fix the bug"}]}`)
	turn2 := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"system","content":"You are a coding agent."},{"role":"user","content":"fix the bug"},{"role":"assistant","content":"ok"},{"role":"user","content":"more"}]}`)
	fp1 := conversationPrefixFingerprint(turn1)
	fp2 := conversationPrefixFingerprint(turn2)
	assert.NotEmpty(t, fp1)
	assert.Equal(t, fp1, fp2, "same session seed must produce identical fingerprint across turns")

	// Different first message → different session → different fingerprint.
	other := []byte(`{"messages":[{"role":"system","content":"You are a coding agent."},{"role":"user","content":"write tests"}]}`)
	assert.NotEqual(t, fp1, conversationPrefixFingerprint(other))

	// Anthropic shape: top-level system field + content parts array.
	anthropic := []byte(`{"system":[{"type":"text","text":"You are a coding agent."}],"messages":[{"role":"user","content":[{"type":"text","text":"fix the bug"}]}]}`)
	assert.Equal(t, fp1, conversationPrefixFingerprint(anthropic), "anthropic-shaped equivalent request must match")

	// No messages / unparseable → empty (caller falls back to body hash).
	assert.Empty(t, conversationPrefixFingerprint([]byte(`{"prompt":"hello"}`)))
	assert.Empty(t, conversationPrefixFingerprint([]byte(`not json`)))
	assert.Empty(t, conversationPrefixFingerprint([]byte(`{"messages":[]}`)))
}

package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mayiants/ai-proxy-go/dto"
	"github.com/mayiants/ai-proxy-go/internal/config"
	"github.com/mayiants/ai-proxy-go/internal/retention"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRetentionCapture_CreatesRecord(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mock := &mockRetentionStore{}
	cfg := &config.Config{Server: config.ServerConfig{Mode: "test"}}
	svc := NewHandlerService(cfg, noopLogger())
	svc.SetRetentionStore(mock)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/v1/chat/completions", bytes.NewReader([]byte(`{"model":"gpt-4o"}`)))
	c.Set("user_id", "user-1")
	c.Set("key_hash", "hash-1")
	c.Set("trace_id", "trace-abc")
	c.Set("routing_track", "standard")
	c.Set("client_format", "openai")
	c.Set("upstream_format", "anthropic")
	c.Set("vendor_model_name", "claude-3-5-sonnet-20241022")
	c.Set("first_token_latency_ms", int64(45))
	c.Set("is_converted", true)

	usage := &dto.Usage{PromptTokens: 10, CompletionTokens: 20}
	svc.captureRetention(c, []byte(`{"model":"gpt-4o"}`), []byte(`{"choices":[]}`), "openai", "gpt-4o", "upstream-key-1", usage, 123, 200, false, "chat_completions")

	require.Len(t, mock.records, 1)
	r := mock.records[0]
	assert.Equal(t, "trace-abc", r.TraceID)
	assert.Equal(t, "user-1", r.Metadata.UserID)
	assert.Equal(t, "hash-1", r.Metadata.APIKeyHash)
	assert.Equal(t, "openai", r.Metadata.Provider)
	assert.Equal(t, "gpt-4o", r.Metadata.Model)
	assert.Equal(t, "claude-3-5-sonnet-20241022", r.Metadata.VendorModelName)
	assert.Equal(t, "openai", r.Metadata.ClientFormat)
	assert.Equal(t, "anthropic", r.Metadata.UpstreamFormat)
	assert.Equal(t, "upstream-key-1", r.Metadata.UpstreamKeyID)
	assert.Equal(t, 10, r.Metadata.InputTokens)
	assert.Equal(t, 20, r.Metadata.OutputTokens)
	assert.Equal(t, 200, r.Metadata.StatusCode)
	assert.Equal(t, int64(123), r.Metadata.LatencyMs)
	assert.Equal(t, int64(45), r.Metadata.FirstTokenLatency)
	assert.False(t, r.Metadata.IsStream)
	assert.True(t, r.Metadata.IsConverted)
	assert.Equal(t, "chat_completions", r.Metadata.RequestType)
	assert.Equal(t, `{"model":"gpt-4o"}`, string(r.Request.Body))
	assert.Equal(t, `{"choices":[]}`, string(r.Response.Body))
}

func TestRetentionCapture_DisabledWhenStoreNil(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{Server: config.ServerConfig{Mode: "test"}}
	svc := NewHandlerService(cfg, noopLogger())
	// retentionStore is nil

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/v1/chat/completions", nil)

	// Should not panic
	svc.captureRetention(c, []byte("req"), []byte("resp"), "openai", "gpt-4o", "key-1", nil, 0, 200, false, "chat_completions")
}

func TestRetentionCapture_CaptureAllHeadersRedactsSensitiveValues(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mock := &mockRetentionStore{}
	cfg := &config.Config{
		Server:    config.ServerConfig{Mode: "test"},
		Retention: config.RetentionConfig{CaptureAllHeaders: true},
	}
	svc := NewHandlerService(cfg, noopLogger())
	svc.SetRetentionStore(mock)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/v1/chat/completions", nil)
	c.Request.Header.Set("X-Request-ID", "req-123")
	c.Request.Header.Set("Authorization", "Bearer secret-token")
	c.Request.Header.Set("X-API-Key", "secret-key")
	c.Request.Header.Set("User-Agent", "test-client/1.0")

	svc.captureRetention(c, []byte("req"), []byte("resp"), "openai", "gpt-4o", "key-1", nil, 0, 200, false, "chat_completions")

	require.Len(t, mock.records, 1)
	h := mock.records[0].Request.Headers
	assert.Equal(t, "req-123", h[http.CanonicalHeaderKey("X-Request-ID")])
	assert.Equal(t, "test-client/1.0", h["User-Agent"])
	assert.Equal(t, "***", h["Authorization"])
	assert.Equal(t, "***", h[http.CanonicalHeaderKey("X-API-Key")])
}

func TestHandlerGetRetentionObject(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mock := &mockRetentionStore{
		records: []*retention.Record{{
			TraceID: "trace-xyz",
			Metadata: retention.Metadata{
				UserID:   "user-2",
				Provider: "anthropic",
				Model:    "claude-sonnet-4",
			},
		}},
	}

	cfg := &config.Config{
		Server:    config.ServerConfig{Mode: "test"},
		RateLimit: config.RateLimitConfig{Enabled: false},
		Admin:     config.AdminConfig{Secret: "admin-secret"},
	}
	svc := NewHandlerService(cfg, noopLogger())
	svc.authMiddleware = noopAuthMiddleware()
	svc.rateLimitMiddleware = noopRateLimitMiddleware()
	svc.SetRetentionStore(mock)
	svc.router = gin.New()
	setupRoutes(svc)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/admin/retention/objects/trace-xyz", nil)
	svc.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code, "admin endpoint requires admin auth")
}

type mockRetentionStore struct {
	records []*retention.Record
}

func (m *mockRetentionStore) Record(r *retention.Record) {
	m.records = append(m.records, r)
}

func (m *mockRetentionStore) Flush() {}

func (m *mockRetentionStore) Close() error { return nil }

func (m *mockRetentionStore) Get(traceID string) (*retention.Record, error) {
	for _, r := range m.records {
		if r.TraceID == traceID {
			return r, nil
		}
	}
	return nil, nil
}

func (m *mockRetentionStore) Cleanup() error { return nil }

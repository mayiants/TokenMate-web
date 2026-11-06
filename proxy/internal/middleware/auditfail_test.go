package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/ai-proxy/ai-proxy-go/internal/audit"
	"github.com/ai-proxy/ai-proxy-go/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// portalSink captures usage-batch posts so tests can inspect audited rows.
type portalSink struct {
	mu   sync.Mutex
	rows []audit.UsageReport
}

func newSinkLogger(t *testing.T) (*audit.Logger, *portalSink) {
	t.Helper()
	sink := &portalSink{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b audit.BatchUsageReport
		_ = json.NewDecoder(r.Body).Decode(&b)
		sink.mu.Lock()
		sink.rows = append(sink.rows, b.Records...)
		sink.mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	l, err := audit.NewLogger(&config.Config{
		Portal: config.PortalConfig{URL: srv.URL},
		Audit:  config.AuditConfig{Enabled: true, BatchSize: 1000, FlushInterval: time.Hour},
	}, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	return l, sink
}

func (s *portalSink) flushed(t *testing.T, l *audit.Logger) []audit.UsageReport {
	t.Helper()
	require.NoError(t, l.Flush())
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]audit.UsageReport(nil), s.rows...)
}

func newFailRouter(l *audit.Logger, h gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestID())
	r.Use(AuditFailures(func() *audit.Logger { return l }))
	r.Any("/v1/x", h)
	return r
}

func do(r *gin.Engine, ip string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/x", nil)
	req.RemoteAddr = ip + ":1234"
	req.Header.Set("User-Agent", "test-agent/1")
	r.ServeHTTP(w, req)
	return w
}

func TestAuditFailures_Table(t *testing.T) {
	tests := []struct {
		name        string
		handler     gin.HandlerFunc
		wantRows    int
		wantStatus  int
		wantErr     string
		wantKeyHash string
		wantModel   string
	}{
		{
			name:     "success is not audited",
			handler:  func(c *gin.Context) { c.Status(200) },
			wantRows: 0,
		},
		{
			name: "anonymous 401 uses status text",
			handler: func(c *gin.Context) {
				c.AbortWithStatus(401)
			},
			wantRows: 1, wantStatus: 401, wantErr: "Unauthorized",
		},
		{
			name: "handler supplied error message and code",
			handler: func(c *gin.Context) {
				c.Set("error_message", "Rate limit exceeded")
				c.AbortWithStatus(429)
			},
			wantRows: 1, wantStatus: 429, wantErr: "Rate limit exceeded",
		},
		{
			name: "error code appended to status text",
			handler: func(c *gin.Context) {
				c.Set("error_code", "UPSTREAM_TIMEOUT")
				c.AbortWithStatus(504)
			},
			wantRows: 1, wantStatus: 504, wantErr: "Gateway Timeout (UPSTREAM_TIMEOUT)",
		},
		{
			name: "resolved key hash and model are recorded",
			handler: func(c *gin.Context) {
				c.Set("key_hash", "hash-abc")
				c.Set("logical_model", "gpt-4o")
				c.AbortWithStatus(502)
			},
			wantRows: 1, wantStatus: 502, wantErr: "Bad Gateway", wantKeyHash: "hash-abc", wantModel: "gpt-4o",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, sink := newSinkLogger(t)
			w := do(newFailRouter(l, tt.handler), "10.0.0.1")
			traceHeader := w.Header().Get(TraceIDHeader)
			require.NotEmpty(t, traceHeader)

			rows := sink.flushed(t, l)
			require.Len(t, rows, tt.wantRows)
			if tt.wantRows == 0 {
				return
			}
			got := rows[0]
			assert.Equal(t, traceHeader, got.TraceID, "audit row must carry the customer-visible trace id")
			assert.Equal(t, tt.wantStatus, got.StatusCode)
			assert.Equal(t, tt.wantErr, got.Error)
			assert.Equal(t, tt.wantKeyHash, got.APIKeyHash)
			assert.Equal(t, tt.wantModel, got.Model)
			assert.Equal(t, "10.0.0.1", got.ClientIP)
			assert.Equal(t, "test-agent/1", got.UserAgent)
			assert.Equal(t, "/v1/x", got.Endpoint)
			// Failure rows must never be billable.
			assert.Zero(t, got.Cost)
			assert.Zero(t, got.InputTokens+got.OutputTokens)
		})
	}
}

func TestAuditFailures_DedupeWithHandlerAudit(t *testing.T) {
	l, sink := newSinkLogger(t)
	r := newFailRouter(l, func(c *gin.Context) {
		// A handler that already audited its own failure.
		l.Log(audit.AuditEntry{TraceID: c.GetString("trace_id"), StatusCode: 500, Error: "from handler", Timestamp: time.Now()})
		c.AbortWithStatus(500)
	})
	do(r, "10.0.0.2")

	rows := sink.flushed(t, l)
	require.Len(t, rows, 1)
	assert.Equal(t, "from handler", rows[0].Error)
}

func TestAuditFailures_AnonymousFloodLimit(t *testing.T) {
	l, sink := newSinkLogger(t)
	r := newFailRouter(l, func(c *gin.Context) { c.AbortWithStatus(401) })

	before := audit.DroppedAnonymousFailures()
	for i := 0; i < 30; i++ {
		do(r, "10.0.0.3")
	}
	// A different scanner IP still gets its own budget.
	do(r, "10.0.0.4")

	rows := sink.flushed(t, l)
	assert.Len(t, rows, 21, "20 for the flooding IP + 1 for the other IP")
	assert.Equal(t, int64(10), audit.DroppedAnonymousFailures()-before)
}

func TestAuditFailures_KeyedFailuresAreNotRateLimited(t *testing.T) {
	l, sink := newSinkLogger(t)
	r := newFailRouter(l, func(c *gin.Context) {
		c.Set("key_hash", "hash-1")
		c.AbortWithStatus(429)
	})
	for i := 0; i < 30; i++ {
		do(r, "10.0.0.5")
	}
	assert.Len(t, sink.flushed(t, l), 30)
}

func TestAuditFailures_NilLoggerIsNoop(t *testing.T) {
	r := newFailRouter(nil, func(c *gin.Context) { c.AbortWithStatus(500) })
	assert.Equal(t, 500, do(r, "10.0.0.6").Code)
}

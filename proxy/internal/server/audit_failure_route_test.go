package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mayiants/ai-proxy-go/internal/audit"
	"github.com/mayiants/ai-proxy-go/internal/auth"
	"github.com/mayiants/ai-proxy-go/internal/config"
	"github.com/mayiants/ai-proxy-go/internal/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// Auth rejections on /v1 must be audited with the same trace id the customer
// sees in X-Trace-Id.
func TestV1AuthRejectionIsAudited(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var (
		mu   sync.Mutex
		rows []audit.UsageReport
	)
	portal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b audit.BatchUsageReport
		_ = json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		rows = append(rows, b.Records...)
		mu.Unlock()
	}))
	defer portal.Close()

	cfg := &config.Config{
		Server:    config.ServerConfig{Mode: "test"},
		Portal:    config.PortalConfig{URL: portal.URL},
		Audit:     config.AuditConfig{Enabled: true, BatchSize: 100, FlushInterval: time.Hour},
		RateLimit: config.RateLimitConfig{Enabled: false},
	}
	al, err := audit.NewLogger(cfg, zap.NewNop())
	require.NoError(t, err)
	defer al.Close()

	svc := NewHandlerService(cfg, noopLogger())
	svc.handlerDeps = &HandlerDependencies{AuditLogger: al}
	svc.authMiddleware = auth.NewMiddleware(cfg, zap.NewNop())
	svc.rateLimitMiddleware = noopRateLimitMiddleware()
	svc.router = gin.New()
	svc.router.Use(middleware.RequestID())
	setupRoutes(svc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("User-Agent", "scanner/1")
	svc.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	trace := w.Header().Get("X-Trace-Id")
	require.NotEmpty(t, trace)

	require.NoError(t, al.Flush())
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, rows, 1)
	assert.Equal(t, trace, rows[0].TraceID)
	assert.Equal(t, 401, rows[0].StatusCode)
	assert.Equal(t, "Missing API key", rows[0].Error)
	assert.Equal(t, "/v1/chat/completions", rows[0].Endpoint)
	assert.Equal(t, "scanner/1", rows[0].UserAgent)
	assert.Zero(t, rows[0].Cost)
}

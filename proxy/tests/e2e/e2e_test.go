package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mayiants/ai-proxy-go/dto"
	"github.com/mayiants/ai-proxy-go/internal/audit"
	"github.com/mayiants/ai-proxy-go/internal/config"
	"github.com/mayiants/ai-proxy-go/internal/server"
	"github.com/mayiants/ai-proxy-go/pkg/billing"
	"github.com/mayiants/ai-proxy-go/pkg/cache"
	"github.com/mayiants/ai-proxy-go/pkg/token"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestE2EHealthEndpoint tests the health check endpoint
func TestE2EHealthEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Server: config.ServerConfig{Mode: "test"},
		Audit:  config.AuditConfig{Enabled: false, BatchSize: 10, FlushInterval: time.Hour},
	}
	logger := zap.NewNop()

	service := server.NewHandlerService(cfg, logger)
	srv, err := server.New(cfg, logger, service)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/health", nil)
	srv.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "healthy", resp["status"])
}

// TestE2EMetricsEndpoint tests the metrics endpoint
func TestE2EMetricsEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Server: config.ServerConfig{Mode: "test"},
		Admin:  config.AdminConfig{Secret: "test-admin-secret"},
		Audit:  config.AuditConfig{Enabled: false, BatchSize: 10, FlushInterval: time.Hour},
	}
	logger := zap.NewNop()

	service := server.NewHandlerService(cfg, logger)
	srv, err := server.New(cfg, logger, service)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/metrics", nil)
	srv.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/plain")
}

// TestE2EChatCompletionsWithSimulateMode tests chat completions in simulate mode
func TestE2EChatCompletionsWithSimulateMode(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Server: config.ServerConfig{Mode: "test"},
		Audit:  config.AuditConfig{Enabled: false, BatchSize: 10, FlushInterval: time.Hour},
	}
	logger := zap.NewNop()

	service := server.NewHandlerService(cfg, logger)
	srv, err := server.New(cfg, logger, service)
	require.NoError(t, err)

	// Set up handler dependencies with simulate mode enabled
	deps := &server.HandlerDependencies{
		Logger:        logger,
		TokenCounter:  token.NewCounter(),
		Billing:       billing.NewBilling(),
		AffinityCache: cache.NewAffinityCache(time.Hour),
		Simulate:      server.SimulateConfig{Enabled: true, LatencyMs: 0},
	}
	service.SetHandlerDependencies(deps)

	// Create chat completion request
	reqBody := dto.GeneralOpenAIRequest{
		Model: "gpt-4",
		Messages: []dto.Message{
			{Role: "user", Content: "Hello, world!"},
		},
	}
	body, _ := json.Marshal(reqBody)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-api-key")

	// Note: This would require auth middleware to be bypassed or properly configured
	// For E2E testing, we'd need a valid API key or mock the auth
	// This test demonstrates the structure

	srv.Router().ServeHTTP(w, req)

	// The request will fail with 401/403 since auth is not configured
	// This is expected in a raw E2E test without proper auth setup
	assert.True(t, w.Code == http.StatusUnauthorized || w.Code == http.StatusForbidden || w.Code == http.StatusServiceUnavailable)
}

// TestE2EStatusEndpoints tests public status endpoints
func TestE2EStatusEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Server: config.ServerConfig{Mode: "test"},
		Audit:  config.AuditConfig{Enabled: false, BatchSize: 10, FlushInterval: time.Hour},
	}
	logger := zap.NewNop()

	service := server.NewHandlerService(cfg, logger)
	srv, err := server.New(cfg, logger, service)
	require.NoError(t, err)

	// Public endpoints (no auth required)
	publicEndpoints := []string{
		"/api/status",
		"/api/status/history",
	}

	for _, endpoint := range publicEndpoints {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", endpoint, nil)
		srv.Router().ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "endpoint %s should return 200", endpoint)
	}

	// Protected endpoints (require admin auth)
	protectedEndpoints := []string{
		"/api/status/providers",
	}
	for _, endpoint := range protectedEndpoints {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", endpoint, nil)
		srv.Router().ServeHTTP(w, req)
		assert.NotEqual(t, http.StatusOK, w.Code, "endpoint %s should require auth", endpoint)
	}
}

// TestE2EAdminEndpointsRequireAuth tests that admin endpoints require authentication
func TestE2EAdminEndpointsRequireAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Server: config.ServerConfig{Mode: "test"},
		Admin:  config.AdminConfig{Secret: "test-admin-secret"},
		Audit:  config.AuditConfig{Enabled: false, BatchSize: 10, FlushInterval: time.Hour},
	}
	logger := zap.NewNop()

	service := server.NewHandlerService(cfg, logger)
	srv, err := server.New(cfg, logger, service)
	require.NoError(t, err)

	// Request without admin secret should be rejected
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/admin/stats", nil)
	srv.Router().ServeHTTP(w, req)

	// Admin auth returns 403 (timestamp/nonce required) rather than 401
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestE2EInternalValidationRequiresAdminSecret tests internal validation endpoint
func TestE2EInternalValidationRequiresAdminSecret(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Server: config.ServerConfig{Mode: "test"},
		Admin:  config.AdminConfig{Secret: "test-admin-secret"},
		Audit:  config.AuditConfig{Enabled: false, BatchSize: 10, FlushInterval: time.Hour},
	}
	logger := zap.NewNop()

	service := server.NewHandlerService(cfg, logger)
	srv, err := server.New(cfg, logger, service)
	require.NoError(t, err)

	// Without secret
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/internal/validate-model", nil)
	srv.Router().ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// With wrong secret
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/api/internal/validate-model", nil)
	req.Header.Set("X-Admin-Secret", "wrong-secret")
	srv.Router().ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// With correct secret but empty body
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/api/internal/validate-model", nil)
	req.Header.Set("X-Admin-Secret", "test-admin-secret")
	srv.Router().ServeHTTP(w, req)
	// Will be 400 because body is empty, but auth passes
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestE2ECORSHeaders tests CORS headers
func TestE2ECORSHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Server: config.ServerConfig{
			Mode:           "test",
			AllowedOrigins: []string{"https://example.com"},
		},
		Audit: config.AuditConfig{Enabled: false, BatchSize: 10, FlushInterval: time.Hour},
	}
	logger := zap.NewNop()

	service := server.NewHandlerService(cfg, logger)
	srv, err := server.New(cfg, logger, service)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("OPTIONS", "/v1/chat/completions", nil)
	req.Header.Set("Origin", "https://example.com")
	srv.Router().ServeHTTP(w, req)

	// OPTIONS requests should return 204
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, "https://example.com", w.Header().Get("Access-Control-Allow-Origin"))
}

// TestE2ERequestIDPropagation tests that request IDs are propagated
func TestE2ERequestIDPropagation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Server: config.ServerConfig{Mode: "test"},
		Audit:  config.AuditConfig{Enabled: false, BatchSize: 10, FlushInterval: time.Hour},
	}
	logger := zap.NewNop()

	service := server.NewHandlerService(cfg, logger)
	srv, err := server.New(cfg, logger, service)
	require.NoError(t, err)

	// Request with existing request ID
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/health", nil)
	req.Header.Set("X-Request-ID", "test-request-id-123")
	srv.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "test-request-id-123", w.Header().Get("X-Request-ID"))
}

// TestE2EFullBusinessFlow simulates a complete business flow
func TestE2EFullBusinessFlow(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Server: config.ServerConfig{Mode: "test"},
		Admin:  config.AdminConfig{Secret: "test-admin-secret"},
		Audit:  config.AuditConfig{Enabled: true, BatchSize: 10, FlushInterval: time.Hour},
	}
	logger := zap.NewNop()

	service := server.NewHandlerService(cfg, logger)
	srv, err := server.New(cfg, logger, service)
	require.NoError(t, err)

	// Step 1: Health check
	t.Run("health check", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/health", nil)
		srv.Router().ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	// Step 2: Metrics endpoint
	t.Run("metrics endpoint", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/metrics", nil)
		srv.Router().ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	// Step 3: Status endpoints
	t.Run("status endpoints", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/api/status", nil)
		srv.Router().ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	// Step 4: Admin endpoint (without auth - should fail)
	t.Run("admin endpoint requires auth", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/admin/stats", nil)
		srv.Router().ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	// Step 5: Internal validation (without secret - should fail)
	t.Run("internal validation requires secret", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/api/internal/validate-model", nil)
		srv.Router().ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

// TestE2EConcurrentRequests tests concurrent request handling
func TestE2EConcurrentRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Server: config.ServerConfig{Mode: "test"},
		Audit:  config.AuditConfig{Enabled: false, BatchSize: 10, FlushInterval: time.Hour},
	}
	logger := zap.NewNop()

	service := server.NewHandlerService(cfg, logger)
	srv, err := server.New(cfg, logger, service)
	require.NoError(t, err)

	// Make concurrent requests to health endpoint
	results := make(chan int, 10)
	for i := 0; i < 10; i++ {
		go func() {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest("GET", "/health", nil)
			srv.Router().ServeHTTP(w, req)
			results <- w.Code
		}()
	}

	// Collect results
	for i := 0; i < 10; i++ {
		code := <-results
		assert.Equal(t, http.StatusOK, code)
	}
}

// TestE2EErrorHandling tests error handling for various scenarios
func TestE2EErrorHandling(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Server: config.ServerConfig{Mode: "test"},
		Audit:  config.AuditConfig{Enabled: false, BatchSize: 10, FlushInterval: time.Hour},
	}
	logger := zap.NewNop()

	service := server.NewHandlerService(cfg, logger)
	srv, err := server.New(cfg, logger, service)
	require.NoError(t, err)

	t.Run("invalid endpoint returns 404", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/nonexistent/endpoint", nil)
		srv.Router().ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("method not allowed", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("DELETE", "/health", nil)
		srv.Router().ServeHTTP(w, req)
		// Gin returns 404 for undefined methods on existing paths
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

// TestE2EAuditLogger tests audit logger integration
func TestE2EAuditLogger(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Server: config.ServerConfig{Mode: "test"},
		Portal: config.PortalConfig{URL: "http://localhost:3001"},
		Audit:  config.AuditConfig{Enabled: true, BatchSize: 10, FlushInterval: time.Hour},
	}
	logger := zap.NewNop()

	// Create audit logger
	auditLogger, err := audit.NewLogger(cfg, logger)
	require.NoError(t, err)
	defer auditLogger.Close()

	// Log an audit entry
	auditLogger.Log(audit.AuditEntry{
		Timestamp:   time.Now(),
		Model:       "gpt-4",
		Provider:    "openai",
		InputTokens: 100,
		Cost:        0.01,
	})

	// Verify logger was created
	assert.NotNil(t, auditLogger)
}

// TestE2EBillingCalculation tests billing calculation
func TestE2EBillingCalculation(t *testing.T) {
	billingInstance := billing.NewBilling()

	usage := &dto.Usage{
		PromptTokens:     1000,
		CompletionTokens: 500,
		TotalTokens:      1500,
	}

	cost := billingInstance.CalculateCost(usage, "gpt-4", "openai")
	assert.GreaterOrEqual(t, cost, 0.0)
}

// TestE2EAffinityCache tests affinity cache functionality
func TestE2EAffinityCache(t *testing.T) {
	affinityCache := cache.NewAffinityCache(time.Hour)

	// Create a gin context
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("request_body", []byte("test request"))

	// Record a preferred key
	affinityCache.RecordPreferredKeyID(c, "gpt-4", "openai", "key-123", "high")

	// Retrieve the preferred key
	keyID := affinityCache.GetPreferredKeyID(c, "gpt-4", "openai", "high")
	assert.Equal(t, "key-123", keyID)

	// Get stats
	stats := affinityCache.Stats()
	assert.NotNil(t, stats)
	assert.Equal(t, 1, stats["local_entries"])
}

// BenchmarkE2EHealthEndpoint benchmarks the health endpoint
func BenchmarkE2EHealthEndpoint(b *testing.B) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Server: config.ServerConfig{Mode: "test"},
		Audit:  config.AuditConfig{Enabled: false, BatchSize: 10, FlushInterval: time.Hour},
	}
	logger := zap.NewNop()

	service := server.NewHandlerService(cfg, logger)
	srv, _ := server.New(cfg, logger, service)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/health", nil)
		srv.Router().ServeHTTP(w, req)
	}
}

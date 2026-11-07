package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mayiants/ai-proxy-go/pkg/metrics"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestMetricsMiddleware_SkipsHealthPath(t *testing.T) {
	router := gin.New()
	router.Use(MetricsMiddleware())
	router.GET("/health", func(c *gin.Context) {
		c.String(200, "ok")
	})

	before := metrics.GetMetrics().TotalRequests.Load()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/health", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	// Should not have incremented total requests
	assert.Equal(t, before, metrics.GetMetrics().TotalRequests.Load())
}

func TestMetricsMiddleware_SkipsMetricsPath(t *testing.T) {
	router := gin.New()
	router.Use(MetricsMiddleware())
	router.GET("/metrics", func(c *gin.Context) {
		c.String(200, "# HELP test")
	})

	before := metrics.GetMetrics().TotalRequests.Load()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/metrics", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, before, metrics.GetMetrics().TotalRequests.Load())
}

func TestMetricsMiddleware_SkipsAPIStatusPath(t *testing.T) {
	router := gin.New()
	router.Use(MetricsMiddleware())
	router.GET("/api/status", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	before := metrics.GetMetrics().TotalRequests.Load()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/status", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, before, metrics.GetMetrics().TotalRequests.Load())
}

func TestMetricsMiddleware_RecordsRequest(t *testing.T) {
	m := metrics.GetMetrics()
	beforeTotal := m.TotalRequests.Load()
	beforeSuccess := m.SuccessfulRequests.Load()
	beforeNonStream := m.NonStreamingRequests.Load()

	router := gin.New()
	router.Use(MetricsMiddleware())
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		c.String(200, `{"choices":[]}`)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, beforeTotal+1, m.TotalRequests.Load())
	assert.Equal(t, beforeSuccess+1, m.SuccessfulRequests.Load())
	assert.Equal(t, beforeNonStream+1, m.NonStreamingRequests.Load())
}

func TestMetricsMiddleware_RecordsStreamingRequest(t *testing.T) {
	m := metrics.GetMetrics()
	beforeStream := m.StreamingRequests.Load()

	router := gin.New()
	router.Use(MetricsMiddleware())
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		c.String(200, `data: [DONE]`)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("Accept", "text/event-stream")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, beforeStream+1, m.StreamingRequests.Load())
}

func TestMetricsMiddleware_RecordsFailedRequest(t *testing.T) {
	m := metrics.GetMetrics()
	beforeFailed := m.FailedRequests.Load()

	router := gin.New()
	router.Use(MetricsMiddleware())
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		c.String(500, "error")
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, beforeFailed+1, m.FailedRequests.Load())
}

func TestMetricsMiddleware_RecordsProviderAndModel(t *testing.T) {
	// Verify metrics accepts provider/model recording without error
	// (actual count tested indirectly since sync.Map doesn't expose count easily)
	router := gin.New()
	router.Use(MetricsMiddleware())
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		c.Set("provider", "openai")
		c.Set("model", "gpt-4o")
		c.String(200, `{"choices":[]}`)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", bytes.NewReader([]byte(`{"model":"gpt-4o"}`)))
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	// Provider and model are recorded via sync.Map - verify no panic occurred
}

func TestMetricsMiddleware_RecordsTokens(t *testing.T) {
	m := metrics.GetMetrics()
	beforeTokens := m.TotalOutputTokens.Load()

	router := gin.New()
	router.Use(MetricsMiddleware())
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		c.Set("tokens_consumed", int64(150))
		c.String(200, `{"choices":[]}`)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, beforeTokens+150, m.TotalOutputTokens.Load())
}

func TestLoggerWithRequestID_WithID(t *testing.T) {
	service := NewHandlerService(nil, nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("request_id", "req-12345")

	logger := service.LoggerWithRequestID(c)
	assert.NotNil(t, logger)
	// Logger should be from handlerDeps
}

func TestLoggerWithRequestID_WithoutID(t *testing.T) {
	service := NewHandlerService(nil, nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	logger := service.LoggerWithRequestID(c)
	assert.NotNil(t, logger)
}

func TestLoggerWithRequestID_NilDeps(t *testing.T) {
	service := NewHandlerService(nil, nil)
	// handlerDeps is nil by default in test
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("request_id", "req-12345")

	logger := service.LoggerWithRequestID(c)
	// Should return nop logger when handlerDeps is nil
	assert.Equal(t, zap.NewNop(), logger)
}

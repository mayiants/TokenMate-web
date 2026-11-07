package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mayiants/ai-proxy-go/internal/config"
	"github.com/mayiants/ai-proxy-go/pkg/metrics"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestNew_WithConfig(t *testing.T) {
	cfg := &config.Config{
		Server:    config.ServerConfig{Mode: "test"},
		RateLimit: config.RateLimitConfig{Enabled: false},
		Admin:     config.AdminConfig{Secret: "test-secret"},
	}

	srv, err := New(cfg, noopLogger(), NewHandlerService(cfg, noopLogger()))
	assert.NoError(t, err)
	assert.NotNil(t, srv)
	assert.NotNil(t, srv.cfg)
	assert.NotNil(t, srv.logger)
	assert.NotNil(t, srv.router)
	assert.NotNil(t, srv.http)
	assert.NotNil(t, srv.authMiddleware)
}

func TestNew_ReleaseMode(t *testing.T) {
	// Save and restore mode
	origMode := gin.Mode()
	defer gin.SetMode(origMode)

	cfg := &config.Config{
		Server:    config.ServerConfig{Mode: "release"},
		RateLimit: config.RateLimitConfig{Enabled: false},
		Admin:     config.AdminConfig{Secret: "test-secret"},
	}

	srv, err := New(cfg, noopLogger(), NewHandlerService(cfg, noopLogger()))
	assert.NoError(t, err)
	assert.NotNil(t, srv)
	assert.Equal(t, gin.ReleaseMode, gin.Mode())
}

func TestServer_Router(t *testing.T) {
	cfg := &config.Config{
		Server:    config.ServerConfig{Mode: "test"},
		RateLimit: config.RateLimitConfig{Enabled: false},
		Admin:     config.AdminConfig{Secret: "test-secret"},
	}

	srv, err := New(cfg, noopLogger(), NewHandlerService(cfg, noopLogger()))
	assert.NoError(t, err)
	assert.NotNil(t, srv.Router())
}

func TestServer_Router_HealthEndpoint(t *testing.T) {
	cfg := &config.Config{
		Server:    config.ServerConfig{Mode: "test"},
		RateLimit: config.RateLimitConfig{Enabled: false},
		Admin:     config.AdminConfig{Secret: "test-secret"},
	}

	srv, _ := New(cfg, noopLogger(), NewHandlerService(cfg, noopLogger()))
	router := srv.Router()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/health", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestCorsMiddleware_EmptyListDeniesCrossOrigin(t *testing.T) {
	router := gin.New()
	router.Use(corsMiddleware([]string{}))
	router.GET("/test", func(c *gin.Context) {
		c.String(200, "ok")
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://example.com")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Methods"))
}

func TestCorsMiddleware_WildcardOrigin(t *testing.T) {
	router := gin.New()
	router.Use(corsMiddleware([]string{"*"}))
	router.GET("/test", func(c *gin.Context) {
		c.String(200, "ok")
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://example.com")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Credentials"))
}

func TestCorsMiddleware_SpecificOrigin_Allowed(t *testing.T) {
	router := gin.New()
	router.Use(corsMiddleware([]string{"https://trusted.com"}))
	router.GET("/test", func(c *gin.Context) {
		c.String(200, "ok")
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://trusted.com")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "https://trusted.com", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", w.Header().Get("Access-Control-Allow-Credentials"))
}

func TestCorsMiddleware_SpecificOrigin_Denied(t *testing.T) {
	router := gin.New()
	router.Use(corsMiddleware([]string{"https://trusted.com"}))
	router.GET("/test", func(c *gin.Context) {
		c.String(200, "ok")
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://evil.com")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
}

func TestCorsMiddleware_NoOrigin(t *testing.T) {
	router := gin.New()
	router.Use(corsMiddleware([]string{"*"}))
	router.GET("/test", func(c *gin.Context) {
		c.String(200, "ok")
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
}

func TestCorsMiddleware_OptionsRequest(t *testing.T) {
	router := gin.New()
	router.Use(corsMiddleware([]string{"*"}))
	router.OPTIONS("/test", func(c *gin.Context) {
		c.String(200, "ok")
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("OPTIONS", "/test", nil)
	req.Header.Set("Origin", "https://example.com")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
	assert.NotEmpty(t, w.Header().Get("Access-Control-Allow-Methods"))
	assert.NotEmpty(t, w.Header().Get("Access-Control-Allow-Headers"))
}

func TestServer_AuthMiddleware(t *testing.T) {
	cfg := &config.Config{
		Server:    config.ServerConfig{Mode: "test"},
		RateLimit: config.RateLimitConfig{Enabled: false},
		Admin:     config.AdminConfig{Secret: "test-secret"},
	}

	srv, err := New(cfg, noopLogger(), NewHandlerService(cfg, noopLogger()))
	assert.NoError(t, err)
	assert.NotNil(t, srv.AuthMiddleware())
}

func TestServer_Router_MetricsEndpoint(t *testing.T) {
	cfg := &config.Config{
		Server:    config.ServerConfig{Mode: "test"},
		RateLimit: config.RateLimitConfig{Enabled: false},
		Admin:     config.AdminConfig{Secret: "test-secret"},
	}

	srv, _ := New(cfg, noopLogger(), NewHandlerService(cfg, noopLogger()))
	router := srv.Router()

	// Make a sample observation so Prometheus histograms are emitted.
	metrics.GetMetrics().ObserveRequestDuration("openai", "gpt-4o", 200, false, false, 100*time.Millisecond)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/metrics", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/plain")
	body := w.Body.String()
	assert.Contains(t, body, "proxy_requests_total")
	assert.Contains(t, body, "proxy_request_duration_seconds")
}

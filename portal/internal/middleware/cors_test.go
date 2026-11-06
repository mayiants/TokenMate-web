package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func setupCORSGinContext(origin string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/test", nil)
	if origin != "" {
		c.Request.Header.Set("Origin", origin)
	}
	return c, w
}

func TestNewCORS_NoOrigin_Passes(t *testing.T) {
	handler := NewCORS([]string{})
	c, w := setupCORSGinContext("")
	handler(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestNewCORS_AllowAll_AnyOriginPasses(t *testing.T) {
	handler := NewCORS([]string{"*"})
	c, w := setupCORSGinContext("https://evil.example.com")
	handler(c)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "https://evil.example.com", w.Header().Get("Access-Control-Allow-Origin"))
}

func TestNewCORS_AllowAll_SetsHeaders(t *testing.T) {
	handler := NewCORS([]string{"*"})
	c, w := setupCORSGinContext("https://myapp.com")
	handler(c)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "https://myapp.com", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "GET, POST, PUT, PATCH, DELETE, OPTIONS", w.Header().Get("Access-Control-Allow-Methods"))
	assert.Equal(t, "Content-Type, Authorization, X-API-Key", w.Header().Get("Access-Control-Allow-Headers"))
}

func TestNewCORS_AllowedOrigin_Passes(t *testing.T) {
	handler := NewCORS([]string{"https://myapp.com", "https://otherapp.com"})
	c, w := setupCORSGinContext("https://myapp.com")
	handler(c)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "https://myapp.com", w.Header().Get("Access-Control-Allow-Origin"))
}

func TestNewCORS_NotAllowedOrigin_Rejects(t *testing.T) {
	handler := NewCORS([]string{"https://allowed.com"})
	c, w := setupCORSGinContext("https://evil.com")
	handler(c)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestNewCORS_EmptyAllowlist_NoOrigin_Rejected(t *testing.T) {
	handler := NewCORS([]string{})
	c, w := setupCORSGinContext("https://unknown.com")
	handler(c)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestNewCORS_AllowAll_InList(t *testing.T) {
	handler := NewCORS([]string{"https://a.com", "*", "https://b.com"})
	c, w := setupCORSGinContext("https://any-origin.com")
	handler(c)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "https://any-origin.com", w.Header().Get("Access-Control-Allow-Origin"))
}

func TestNewCORS_Options_Preflight(t *testing.T) {
	handler := NewCORS([]string{"https://myapp.com"})
	c, w := setupCORSGinContext("https://myapp.com")
	c.Request.Method = http.MethodOptions
	handler(c)
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestNewCORS_CacheHeaders_NonStatic(t *testing.T) {
	handler := NewCORS([]string{"*"})
	c, w := setupCORSGinContext("https://myapp.com")
	c.Request.URL.Path = "/api/data"
	handler(c)
	assert.Equal(t, "no-store, no-cache, must-revalidate, proxy-revalidate", w.Header().Get("Cache-Control"))
	assert.Equal(t, "no-cache", w.Header().Get("Pragma"))
	assert.Equal(t, "0", w.Header().Get("Expires"))
}

func TestNewCORS_NoCacheHeaders_StaticPath(t *testing.T) {
	handler := NewCORS([]string{"*"})
	c, w := setupCORSGinContext("https://myapp.com")
	c.Request.URL.Path = "/static/app.js"
	handler(c)
	assert.Empty(t, w.Header().Get("Cache-Control"))
	assert.Empty(t, w.Header().Get("Pragma"))
	assert.Empty(t, w.Header().Get("Expires"))
}

func TestNewCORS_SameOrigin_Passes(t *testing.T) {
	handler := NewCORS([]string{})
	c, w := setupCORSGinContext("")
	// Simulate same-origin by setting Host
	c.Request.URL.Scheme = "https"
	c.Request.Host = "myapp.com"
	c.Request.Header.Set("Origin", "https://myapp.com")
	handler(c)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "https://myapp.com", w.Header().Get("Access-Control-Allow-Origin"))
}

func TestNewCORS_SameOrigin_WithForwardedHeaders(t *testing.T) {
	handler := NewCORS([]string{})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/test", nil)
	// X-Forwarded-* are only honored when the direct peer is a trusted
	// (loopback/private) reverse proxy. Simulate that here.
	c.Request.RemoteAddr = "127.0.0.1:1234"
	c.Request.Host = "original.example.com"
	c.Request.Header.Set("X-Forwarded-Proto", "https")
	c.Request.Header.Set("X-Forwarded-Host", "proxy.example.com")
	c.Request.Header.Set("Origin", "https://proxy.example.com")
	handler(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestNewCORS_SameOrigin_NoSchemeURL(t *testing.T) {
	// When scheme is not in URL, falls back to TLS check or http
	handler := NewCORS([]string{})
	c, w := setupCORSGinContext("http://localhost")
	c.Request.URL.Scheme = ""
	c.Request.Host = "localhost"
	handler(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestSameOrigin_Helper(t *testing.T) {
	tests := []struct {
		name       string
		scheme     string
		host       string
		fwdProto   string
		fwdHost    string
		remoteAddr string // direct TCP peer; loopback/private trusts forwarded headers
		wantOrigin string
	}{
		{"basic http", "http", "example.com", "", "", "192.0.2.1:1", "http://example.com"},
		{"basic https", "https", "example.com", "", "", "192.0.2.1:1", "https://example.com"},
		{"forwarded proto (trusted proxy)", "", "example.com", "https", "", "127.0.0.1:1", "https://example.com"},
		{"forwarded host (trusted proxy)", "", "original.com", "", "proxy.com", "10.0.0.1:1", "http://proxy.com"},
		{"forwarded headers ignored (public peer)", "", "example.com", "https", "evil.com", "203.0.113.5:1", "http://example.com"},
		{"no scheme defaults to http", "", "secure.com", "", "", "192.0.2.1:1", "http://secure.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := setupCORSGinContext("")
			c.Request.RemoteAddr = tt.remoteAddr
			c.Request.URL.Scheme = tt.scheme
			c.Request.Host = tt.host
			if tt.fwdProto != "" {
				c.Request.Header.Set("X-Forwarded-Proto", tt.fwdProto)
			}
			if tt.fwdHost != "" {
				c.Request.Header.Set("X-Forwarded-Host", tt.fwdHost)
			}
			got := sameOrigin(c)
			assert.Equal(t, tt.wantOrigin, got)
		})
	}
}

package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestIsSecure tests HTTPS detection.
func TestIsSecure(t *testing.T) {
	tests := []struct {
		name           string
		forwardedProto string
		want           bool
	}{
		{"HTTP with X-Forwarded-Proto https", "https", true},
		{"HTTP with X-Forwarded-Proto http", "http", false},
		{"plain HTTP", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			req := httptest.NewRequest("GET", "/test", nil)
			if tt.forwardedProto != "" {
				req.Header.Set("X-Forwarded-Proto", tt.forwardedProto)
			}
			c.Request = req

			got := isSecure(c)
			if got != tt.want {
				t.Errorf("isSecure() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestGetRequestHost tests host extraction.
func TestGetRequestHost(t *testing.T) {
	tests := []struct {
		name          string
		requestHost   string
		forwardedHost string
		want          string
	}{
		{"use request host", "example.com", "", "example.com"},
		{"use forwarded host", "internal", "example.com", "example.com"},
		{"forwarded host with port", "internal", "example.com:8080", "example.com:8080"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			req := httptest.NewRequest("GET", "/test", nil)
			req.Host = tt.requestHost
			if tt.forwardedHost != "" {
				req.Header.Set("X-Forwarded-Host", tt.forwardedHost)
			}
			c.Request = req

			got := getRequestHost(c)
			if got != tt.want {
				t.Errorf("getRequestHost() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestIsIPHost tests IP host detection.
func TestIsIPHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"192.168.1.1", true},
		{"127.0.0.1", true},
		{"::1", true},
		{"example.com", false},
		{"localhost", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			got := isIPHost(tt.host)
			if got != tt.want {
				t.Errorf("isIPHost(%q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}

// TestGetCookieDomain tests cookie domain extraction.
func TestGetCookieDomain(t *testing.T) {
	tests := []struct {
		name          string
		requestHost   string
		forwardedHost string
		want          string
	}{
		{"domain host", "example.com", "", "example.com"},
		{"domain host with port", "example.com:8080", "", "example.com"},
		{"IP host", "192.168.1.1", "", ""},
		{"forwarded domain", "", "proxy.example.com", "proxy.example.com"},
		{"forwarded IP", "", "10.0.0.1", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			req := httptest.NewRequest("GET", "/test", nil)
			if tt.requestHost != "" {
				req.Host = tt.requestHost
			}
			if tt.forwardedHost != "" {
				req.Header.Set("X-Forwarded-Host", tt.forwardedHost)
			}
			c.Request = req

			got := getCookieDomain(c)
			if got != tt.want {
				t.Errorf("getCookieDomain() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSetJWTWebCookie tests that the JWT cookie is set correctly.
func TestSetJWTWebCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest("GET", "/test", nil)
	req.Host = "example.com"
	c.Request = req

	SetJWTWebCookie(c, "test-token-123", false)

	cookies := w.Result().Cookies()
	var jwtCookie *http.Cookie
	for i := range cookies {
		if cookies[i].Name == JWTCookieName {
			jwtCookie = cookies[i]
			break
		}
	}
	if jwtCookie == nil {
		t.Fatal("JWT cookie not set")
	}
	if jwtCookie.Value != "test-token-123" {
		t.Errorf("JWT cookie value = %q, want %q", jwtCookie.Value, "test-token-123")
	}
	if !jwtCookie.HttpOnly {
		t.Error("JWT cookie should be HttpOnly")
	}
	if jwtCookie.Path != "/" {
		t.Errorf("JWT cookie path = %q, want %q", jwtCookie.Path, "/")
	}

	// Test with remember me
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	req2 := httptest.NewRequest("GET", "/test", nil)
	req2.Host = "example.com"
	c2.Request = req2
	SetJWTWebCookie(c2, "test-token-remember", true)

	cookies2 := w2.Result().Cookies()
	for i := range cookies2 {
		if cookies2[i].Name == JWTCookieName {
			if cookies2[i].MaxAge <= 86400*7 {
				t.Errorf("Remember me cookie maxAge = %d, should be > %d", cookies2[i].MaxAge, 86400*7)
			}
			break
		}
	}
}

// TestClearJWTWebCookie tests that the JWT cookie is cleared.
func TestClearJWTWebCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest("GET", "/test", nil)
	req.Host = "example.com"
	c.Request = req

	ClearJWTWebCookie(c)

	cookies := w.Result().Cookies()
	var jwtCookie *http.Cookie
	for i := range cookies {
		if cookies[i].Name == JWTCookieName {
			jwtCookie = cookies[i]
			break
		}
	}
	if jwtCookie == nil {
		t.Fatal("Clear JWT cookie not found")
	}
	if jwtCookie.MaxAge != -1 {
		t.Errorf("Clear JWT cookie MaxAge = %d, want -1", jwtCookie.MaxAge)
	}
	if jwtCookie.Value != "" {
		t.Errorf("Clear JWT cookie value = %q, want empty", jwtCookie.Value)
	}
}

// TestRequireAuthWebRedirect tests that unauthenticated users get redirected.
func TestRequireAuthWebRedirect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequireAuthWeb())
	router.GET("/dashboard", func(c *gin.Context) {
		c.String(200, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/dashboard", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Errorf("expected 302 redirect, got %d", w.Code)
	}
}

// TestRequireAuthWebPass tests that authenticated users pass through.
func TestRequireAuthWebPass(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", "test-user-id")
		c.Next()
	})
	router.Use(RequireAuthWeb())
	router.GET("/dashboard", func(c *gin.Context) {
		c.String(200, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/dashboard", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

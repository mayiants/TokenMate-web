package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// TestGenerateCSRFToken tests that tokens are the expected length.
func TestGenerateCSRFToken(t *testing.T) {
	token := generateCSRFToken()
	if len(token) != 32 {
		t.Errorf("generateCSRFToken() length = %d, want 32", len(token))
	}

	// Multiple tokens should be different
	token2 := generateCSRFToken()
	if token == token2 {
		t.Errorf("generateCSRFToken() produced identical tokens")
	}
}

// TestSecurityHeaders verifies security headers are set.
func TestSecurityHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(SecurityHeaders())
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	if v := w.Header().Get("X-Frame-Options"); v != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", v)
	}
	if v := w.Header().Get("X-Content-Type-Options"); v != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", v)
	}
	if v := w.Header().Get("Referrer-Policy"); v != "strict-origin-when-cross-origin" {
		t.Errorf("Referrer-Policy = %q, want strict-origin-when-cross-origin", v)
	}
	csp := w.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Error("Content-Security-Policy header is empty")
	}
	if !strings.Contains(csp, "default-src") {
		t.Error("CSP missing default-src directive")
	}
}

// TestCSRFMiddlewareGet sets a token for GET requests.
func TestCSRFMiddlewareGet(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(CSRFMiddleware())
	router.GET("/test", func(c *gin.Context) {
		token := c.GetString("csrf_token")
		c.String(http.StatusOK, token)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	// Token should be set in context and as cookie
	cookies := w.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == "csrf_token" {
			found = true
			if len(c.Value) != 32 {
				t.Errorf("csrf_token cookie length = %d, want 32", len(c.Value))
			}
			break
		}
	}
	if !found {
		t.Error("csrf_token cookie not set")
	}
}

// TestCSRFMiddlewarePostValid tests CSRF with matching token.
func TestCSRFMiddlewarePostValid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(CSRFMiddleware())
	router.POST("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	// First GET to obtain a token
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	router.ServeHTTP(w, req)

	var csrfToken string
	for _, c := range w.Result().Cookies() {
		if c.Name == "csrf_token" {
			csrfToken = c.Value
			break
		}
	}
	if csrfToken == "" {
		t.Fatal("could not obtain csrf token")
	}

	// POST with valid CSRF token
	body := "csrf_token=" + csrfToken + "&field=value"
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.AddCookie(&http.Cookie{Name: "csrf_token", Value: csrfToken})
	router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w2.Code)
	}
}

// TestCSRFMiddlewarePostNoToken tests CSRF with missing token.
func TestCSRFMiddlewarePostNoToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(CSRFMiddleware())
	router.POST("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader("field=value"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

// TestCSRFMiddlewareAPIRoutesSkipped tests that API routes skip CSRF.
func TestCSRFMiddlewareAPIRoutesSkipped(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(CSRFMiddleware())
	router.POST("/api/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/test", strings.NewReader("field=value"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("API routes should skip CSRF, got %d", w.Code)
	}
}

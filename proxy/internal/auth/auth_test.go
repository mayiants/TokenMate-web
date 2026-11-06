package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ai-proxy/ai-proxy-go/internal/config"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestNewMiddleware(t *testing.T) {
	cfg := &config.Config{
		Auth: config.AuthConfig{
			JWTSecret: "test-secret",
		},
		Portal: config.PortalConfig{
			SyncSecret: "admin-secret",
		},
	}

	logger, _ := zap.NewDevelopment()
	m := NewMiddleware(cfg, logger)

	if m == nil {
		t.Fatal("NewMiddleware returned nil")
	}
}

func TestExtractAPIKey(t *testing.T) {
	tests := []struct {
		name  string
		setup func(c *gin.Context)
		want  string
	}{
		{
			name: "from Authorization header",
			setup: func(c *gin.Context) {
				c.Request.Header.Set("Authorization", "Bearer sk-test-key")
			},
			want: "sk-test-key",
		},
		{
			name: "from X-API-Key header",
			setup: func(c *gin.Context) {
				c.Request.Header.Set("X-API-Key", "sk-test-key")
			},
			want: "sk-test-key",
		},
		{
			name: "from query parameter (disabled for security)",
			setup: func(c *gin.Context) {
				c.Request.URL.RawQuery = "api_key=sk-test-key"
			},
			want: "",
		},
		{
			name:  "no API key",
			setup: func(c *gin.Context) {},
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
			tt.setup(c)

			got := extractAPIKey(c)
			if got != tt.want {
				t.Errorf("extractAPIKey() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestMaskKey(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"sk", "****"},
		{"sk-test", "****"},
		{"sk-test-key", "sk-t...ey"},
		{"sk-very-long-api-key", "sk-v...ey"},
		{"sk-test-key-123", "sk-t...23"},
	}

	for _, tt := range tests {
		got := maskKey(tt.key)
		if got != tt.want {
			t.Errorf("maskKey(%s) = %s, want %s", tt.key, got, tt.want)
		}
	}
}

func TestComputeHMAC(t *testing.T) {
	secret := "test-secret"
	message := "GET/api/admin/keys1234567890nonce123"

	// Same inputs should produce same output
	sig1 := computeHMAC(secret, message)
	sig2 := computeHMAC(secret, message)

	if sig1 != sig2 {
		t.Error("same inputs should produce same HMAC")
	}

	// Different inputs should produce different output
	sig3 := computeHMAC(secret, "different-message")

	if sig1 == sig3 {
		t.Error("different inputs should produce different HMAC")
	}

	// Different secret should produce different output
	sig4 := computeHMAC("different-secret", message)

	if sig1 == sig4 {
		t.Error("different secrets should produce different HMAC")
	}
}

func TestNonceReplayProtection(t *testing.T) {
	cfg := &config.Config{
		Portal: config.PortalConfig{
			SyncSecret: "test-secret",
		},
	}
	logger, _ := zap.NewDevelopment()
	m := NewMiddleware(cfg, logger)

	nonce := "test-nonce-123"

	// First use should not be detected as replay
	if m.isNonceUsed(nonce) {
		t.Error("nonce should not be marked as used initially")
	}

	// Mark as used
	m.markNonceUsed(nonce)

	// Second use should be detected as replay
	if !m.isNonceUsed(nonce) {
		t.Error("nonce should be marked as used after markNonceUsed")
	}
}

func TestAdminHandler_RequiresHMACSignature(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{
		Admin: config.AdminConfig{Secret: "test-admin-secret-32-characters"},
	}
	logger, _ := zap.NewDevelopment()
	m := NewMiddleware(cfg, logger)

	router := gin.New()
	router.Use(m.AdminHandler())
	router.GET("/admin/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/admin/test", nil)
	req.Header.Set("X-Timestamp", fmt.Sprintf("%d", time.Now().Unix()))
	req.Header.Set("X-Nonce", "nonce123")
	req.Header.Set("X-Admin-Secret", cfg.Admin.Secret)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("simple secret fallback should be rejected, got %d", w.Code)
	}
}

func TestAdminHandler_ValidSignature(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{
		Admin: config.AdminConfig{Secret: "test-admin-secret-32-characters"},
	}
	logger, _ := zap.NewDevelopment()
	m := NewMiddleware(cfg, logger)

	router := gin.New()
	router.Use(m.AdminHandler())
	router.GET("/admin/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	nonce := "nonce123"
	message := fmt.Sprintf("%s%s%s%s", http.MethodGet, "/admin/test", timestamp, nonce)
	signature := computeHMAC(cfg.Admin.Secret, message)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/admin/test", nil)
	req.Header.Set("X-Timestamp", timestamp)
	req.Header.Set("X-Nonce", nonce)
	req.Header.Set("X-Signature", signature)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("valid HMAC signature should pass, got %d: %s", w.Code, w.Body.String())
	}
}

func TestBalanceCacheCleanup(t *testing.T) {
	cfg := &config.Config{
		Auth:   config.AuthConfig{JWTSecret: "test-secret"},
		Portal: config.PortalConfig{SyncSecret: "test-sync-secret-must-be-at-least-32-characters"},
	}
	logger, _ := zap.NewDevelopment()
	m := NewMiddleware(cfg, logger)

	now := time.Now()
	stale := now.Add(-6 * balanceCacheTTL)
	fresh := now.Add(-balanceCacheTTL)

	m.balanceCacheMu.Lock()
	m.balanceCache["stale-user"] = &balanceEntry{balance: 0, updatedAt: stale}
	m.balanceCache["fresh-user"] = &balanceEntry{balance: 100, updatedAt: fresh}
	m.balanceCacheMu.Unlock()

	m.cleanupStaleBalanceEntries(now)

	m.balanceCacheMu.RLock()
	_, staleExists := m.balanceCache["stale-user"]
	_, freshExists := m.balanceCache["fresh-user"]
	m.balanceCacheMu.RUnlock()

	if staleExists {
		t.Error("stale balance cache entry should have been removed")
	}
	if !freshExists {
		t.Error("fresh balance cache entry should still exist")
	}
}

func TestMiddleware_Handler_MissingKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{}
	m := NewMiddleware(cfg, zap.NewNop())
	defer m.Stop()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/", nil)

	m.Handler()(c)

	if !c.IsAborted() {
		t.Error("expected request to be aborted")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestMiddleware_Handler_InvalidFormat(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{}
	m := NewMiddleware(cfg, zap.NewNop())
	defer m.Stop()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/", nil)
	c.Request.Header.Set("Authorization", "Bearer not-an-sk-key")

	m.Handler()(c)

	if !c.IsAborted() {
		t.Error("expected request to be aborted")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

// newPortalMock starts a fake Portal whose /api/apikeys/validate endpoint is
// handled by validateHandler, and returns a config pointing at it.
func newPortalMock(t *testing.T, validateHandler http.HandlerFunc) (*config.Config, func()) {
	t.Helper()
	srv := httptest.NewServer(validateHandler)
	cfg := &config.Config{Portal: config.PortalConfig{URL: srv.URL}}
	return cfg, srv.Close
}

func TestMiddleware_Handler_PortalKeySuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg, cleanup := newPortalMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"valid":true,"user_id":"user-1","api_key_id":"key-1","role":"user","access_mode":"general","balance":10,"key_type":"general"}`))
	})
	defer cleanup()

	m := NewMiddleware(cfg, zap.NewNop())
	defer m.Stop()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/", nil)
	c.Request.Header.Set("Authorization", "Bearer sk-wgit-testkey123456")

	m.Handler()(c)

	if c.IsAborted() {
		t.Error("expected request to pass auth")
	}
	if c.GetString("user_id") != "user-1" {
		t.Errorf("user_id = %s, want user-1", c.GetString("user_id"))
	}
	if c.GetString("api_key_id") != "key-1" {
		t.Errorf("api_key_id = %s, want key-1", c.GetString("api_key_id"))
	}
}

func TestMiddleware_Handler_PortalBalanceExhausted(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Portal rejects with 402 + error_code — proxy must surface 402, not a
	// generic 401.
	cfg, cleanup := newPortalMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"valid":false,"error":"Insufficient balance","error_code":"BALANCE_EXHAUSTED"}`))
	})
	defer cleanup()

	m := NewMiddleware(cfg, zap.NewNop())
	defer m.Stop()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/", nil)
	c.Request.Header.Set("Authorization", "Bearer sk-wgit-testkey123456")

	m.Handler()(c)

	if !c.IsAborted() {
		t.Error("expected request to be aborted")
	}
	if w.Code != http.StatusPaymentRequired {
		t.Errorf("expected 402, got %d", w.Code)
	}
}

func TestMiddleware_Handler_PortalDownReturns503(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg, cleanup := newPortalMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	defer cleanup()

	m := NewMiddleware(cfg, zap.NewNop())
	defer m.Stop()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/", nil)
	c.Request.Header.Set("Authorization", "Bearer sk-wgit-testkey123456")

	m.Handler()(c)

	if !c.IsAborted() {
		t.Error("expected request to be aborted")
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestMiddleware_Handler_BalanceCacheBlocksRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg, cleanup := newPortalMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"valid":true,"user_id":"user-zero","api_key_id":"key-1","role":"user","access_mode":"general","balance":10,"key_type":"general"}`))
	})
	defer cleanup()

	m := NewMiddleware(cfg, zap.NewNop())
	defer m.Stop()

	doRequest := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/", nil)
		c.Request.Header.Set("Authorization", "Bearer sk-wgit-testkey123456")
		m.Handler()(c)
		return w
	}

	// First request passes and seeds the balance cache from the portal response.
	if w := doRequest(); w.Code == http.StatusPaymentRequired || w.Code == http.StatusUnauthorized {
		t.Fatalf("first request should pass auth, got %d", w.Code)
	}

	// Simulate a balance_update pub/sub dropping the balance to zero.
	m.setBalance("user-zero", 0)

	// Second request must be rejected even though cached claims say balance 10.
	if w := doRequest(); w.Code != http.StatusPaymentRequired {
		t.Errorf("expected 402 after balance drop, got %d", w.Code)
	}
}

func TestMiddleware_Handler_EnterpriseBypassesBalanceCheck(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg, cleanup := newPortalMock(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"valid":true,"user_id":"user-ent","api_key_id":"key-ent","role":"user","access_mode":"general","balance":0,"key_type":"enterprise"}`))
	})
	defer cleanup()

	m := NewMiddleware(cfg, zap.NewNop())
	defer m.Stop()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/", nil)
	c.Request.Header.Set("Authorization", "Bearer sk-wgit-testkey123456")

	m.Handler()(c)

	if c.IsAborted() {
		t.Error("enterprise key should not be aborted for zero personal balance")
	}
}

func TestMiddleware_Handler_JWTPrefixHasNoLocalBypass(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// The sk-jwt- prefix must NOT trigger any local signature validation —
	// the key goes to the Portal like any other, and is rejected there.
	portalCalled := false
	cfg, cleanup := newPortalMock(t, func(w http.ResponseWriter, r *http.Request) {
		portalCalled = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"valid":false}`))
	})
	defer cleanup()

	m := NewMiddleware(cfg, zap.NewNop())
	defer m.Stop()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/", nil)
	c.Request.Header.Set("Authorization", "Bearer sk-jwt-eyJhbGciOiJIUzI1NiJ9.fake.sig")

	m.Handler()(c)

	if !portalCalled {
		t.Error("sk-jwt- key must be forwarded to Portal, not validated locally")
	}
	if !c.IsAborted() || w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for portal-rejected sk-jwt key, got %d", w.Code)
	}
}

func TestMiddleware_CacheClaims(t *testing.T) {
	cfg := &config.Config{}
	m := NewMiddleware(cfg, zap.NewNop())
	defer m.Stop()

	claims := &Claims{UserID: "user-cached"}
	m.cacheClaims("key-1", claims)

	got := m.getCachedClaims("key-1")
	if got == nil {
		t.Fatal("expected cached claims")
	}
	if got.UserID != "user-cached" {
		t.Errorf("UserID = %s, want user-cached", got.UserID)
	}

	if m.getCachedClaims("key-unknown") != nil {
		t.Error("unknown key should return nil")
	}
}

func TestMiddleware_CacheClaimsGracefulDegradation(t *testing.T) {
	cfg := &config.Config{}
	m := NewMiddleware(cfg, zap.NewNop())
	defer m.Stop()

	claims := &Claims{UserID: "user-degraded"}
	m.cacheClaims("key-1", claims)

	// Mark portal unhealthy; expired cache should still be returned within gracefulTTL.
	m.markPortalUnhealthy()

	// Manually expire the entry but keep cachedAt recent.
	m.keyCache.Store("key-1", &cacheEntry{
		claims:    claims,
		expiresAt: time.Now().Add(-time.Second),
		cachedAt:  time.Now(),
	})

	got := m.getCachedClaims("key-1")
	if got == nil {
		t.Fatal("expected degraded cache hit")
	}
	if got.UserID != "user-degraded" {
		t.Errorf("UserID = %s, want user-degraded", got.UserID)
	}
}

func TestValidateWithPortal_PortalNotConfigured(t *testing.T) {
	cfg := &config.Config{}
	m := NewMiddleware(cfg, zap.NewNop())
	defer m.Stop()

	_, err := m.validateWithPortal(context.Background(), "sk-test-key")
	if err == nil {
		t.Fatal("expected error when portal URL not configured")
	}
	if !strings.Contains(err.Error(), "portal URL not configured") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateWithPortal_CacheHit(t *testing.T) {
	cfg := &config.Config{}
	m := NewMiddleware(cfg, zap.NewNop())
	defer m.Stop()

	claims := &Claims{UserID: "cached-user"}
	m.cacheClaims(hashCacheKey("sk-test-key"), claims)

	got, err := m.validateWithPortal(context.Background(), "sk-test-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.UserID != "cached-user" {
		t.Errorf("UserID = %s, want cached-user", got.UserID)
	}
}

func TestValidateWithPortalAPI_GracefulDegradation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Start a server that always fails.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "portal down", http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := &config.Config{Portal: config.PortalConfig{URL: server.URL}}
	m := NewMiddleware(cfg, zap.NewNop())
	defer m.Stop()

	claims := &Claims{UserID: "degraded-user"}
	m.cacheClaims(hashCacheKey("sk-test-key"), claims)

	got, err := m.validateWithPortalAPI(context.Background(), "sk-test-key")
	if err != nil {
		t.Fatalf("expected graceful degradation, got error: %v", err)
	}
	if got.UserID != "degraded-user" {
		t.Errorf("UserID = %s, want degraded-user", got.UserID)
	}
}

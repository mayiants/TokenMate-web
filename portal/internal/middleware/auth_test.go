package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mayiants/ai-portal-go/internal/config"
	"github.com/mayiants/ai-portal-go/pkg/jwt"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newTestAuth(adminMode string) *Auth {
	cfg := &config.Config{
		Auth: config.AuthConfig{
			JWTSecret:  "test-secret-key-32-chars-long!!",
			JWTExpires: time.Hour,
			AdminMode:  adminMode,
		},
	}
	return NewAuth(cfg, nil)
}

func newTestJWTManager() *jwt.Manager {
	return jwt.NewManager("test-secret-key-32-chars-long!!", time.Hour)
}

func TestExtractToken(t *testing.T) {
	tests := []struct {
		name      string
		headerVal string
		want      string
	}{
		{"bearer token", "Bearer sk-abc123", "sk-abc123"},
		{"bearer with spaces", "Bearer   token-with-spaces", "  token-with-spaces"},
		{"no header", "", ""},
		{"basic auth", "Basic dXNlcjpwYXNz", ""},
		{"bearer lowercase", "bearer sk-abc123", ""},
		{"just bearer no space", "Bearer", ""},
		{"empty bearer", "Bearer ", ""},
		{"bearer with special chars", "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.headerVal != "" {
				c.Request.Header.Set("Authorization", tt.headerVal)
			}
			got := extractToken(c)
			if got != tt.want {
				t.Errorf("extractToken() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRoleHierarchy(t *testing.T) {
	tests := []struct {
		name string
		role string
		want int
	}{
		{"user level", "user", 1},
		{"enterprise_admin level", "enterprise_admin", 2},
		{"system_admin level", "system_admin", 10},
		{"super_admin level", "super_admin", 100},
		{"unknown role", "unknown", 0},
		{"empty role", "", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := roleHierarchy[tt.role]
			if got != tt.want {
				t.Errorf("roleHierarchy[%q] = %d, want %d", tt.role, got, tt.want)
			}
		})
	}
}

func TestRequireAuth(t *testing.T) {
	tests := []struct {
		name       string
		authHeader string
		wantCode   int
		wantAbort  bool
	}{
		{"valid token", "valid", http.StatusOK, false},
		{"no header", "", http.StatusUnauthorized, true},
		{"invalid token", "Bearer invalid-token", http.StatusUnauthorized, true},
		{"basic auth scheme", "Basic dXNlcjpwYXNz", http.StatusUnauthorized, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestAuth("production")
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

			if tt.authHeader == "valid" {
				jm := newTestJWTManager()
				token, _ := jm.Generate("user-123", "test@example.com", "user", 0)
				c.Request.Header.Set("Authorization", "Bearer "+token)
			} else if tt.authHeader != "" {
				c.Request.Header.Set("Authorization", tt.authHeader)
			}

			handler := a.RequireAuth()
			handler(c)

			if w.Code != tt.wantCode {
				t.Errorf("got status %d, want %d", w.Code, tt.wantCode)
			}
			if tt.wantAbort && !c.IsAborted() {
				t.Error("expected context to be aborted")
			}
			if !tt.wantAbort && c.IsAborted() {
				t.Error("expected context NOT to be aborted")
			}
		})
	}
}

func TestRequireAuthSetsClaims(t *testing.T) {
	a := newTestAuth("production")
	jm := newTestJWTManager()
	token, _ := jm.Generate("user-456", "admin@example.com", "system_admin", 0)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.Header.Set("Authorization", "Bearer "+token)

	handler := a.RequireAuth()
	handler(c)

	if c.GetString("user_id") != "user-456" {
		t.Errorf("user_id = %q, want %q", c.GetString("user_id"), "user-456")
	}
	if c.GetString("email") != "admin@example.com" {
		t.Errorf("email = %q, want %q", c.GetString("email"), "admin@example.com")
	}
	if c.GetString("role") != "system_admin" {
		t.Errorf("role = %q, want %q", c.GetString("role"), "system_admin")
	}
}

func TestOptionalAuth(t *testing.T) {
	tests := []struct {
		name       string
		authHeader string
		wantCode   int
		wantAbort  bool
	}{
		{"valid token", "valid", http.StatusOK, false},
		{"no header - ok", "", http.StatusOK, false},
		{"invalid token - continues", "Bearer bad", http.StatusOK, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestAuth("production")
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

			if tt.authHeader == "valid" {
				jm := newTestJWTManager()
				token, _ := jm.Generate("user-123", "test@example.com", "user", 0)
				c.Request.Header.Set("Authorization", "Bearer "+token)
			} else if tt.authHeader != "" {
				c.Request.Header.Set("Authorization", tt.authHeader)
			}

			handler := a.OptionalAuth()
			handler(c)

			if w.Code != tt.wantCode {
				t.Errorf("got status %d, want %d", w.Code, tt.wantCode)
			}
			if c.IsAborted() {
				t.Error("OptionalAuth should never abort")
			}
		})
	}
}

func TestOptionalAuthSetsClaimsWhenValid(t *testing.T) {
	a := newTestAuth("production")
	jm := newTestJWTManager()
	token, _ := jm.Generate("user-789", "user@example.com", "user", 0)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.Header.Set("Authorization", "Bearer "+token)

	handler := a.OptionalAuth()
	handler(c)

	if c.GetString("user_id") != "user-789" {
		t.Errorf("user_id = %q, want %q", c.GetString("user_id"), "user-789")
	}
}

func TestRequireRole(t *testing.T) {
	tests := []struct {
		name     string
		userRole string
		reqRoles []string
		wantCode int
	}{
		{"admin matches system_admin", "system_admin", []string{"system_admin", "super_admin"}, http.StatusOK},
		{"admin matches super_admin", "super_admin", []string{"system_admin", "super_admin"}, http.StatusOK},
		{"user rejected for admin", "user", []string{"system_admin", "super_admin"}, http.StatusForbidden},
		{"enterprise_admin rejected for admin", "enterprise_admin", []string{"system_admin", "super_admin"}, http.StatusForbidden},
		{"empty role unauthorized", "", []string{"system_admin"}, http.StatusUnauthorized},
		{"single role match", "super_admin", []string{"super_admin"}, http.StatusOK},
		{"single role mismatch", "user", []string{"super_admin"}, http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestAuth("production")
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.userRole != "" {
				c.Set("role", tt.userRole)
			}

			handler := a.requireRole(tt.reqRoles...)
			handler(c)

			if w.Code != tt.wantCode {
				t.Errorf("got status %d, want %d", w.Code, tt.wantCode)
			}
		})
	}
}

func TestRequireMinRole(t *testing.T) {
	tests := []struct {
		name     string
		userRole string
		minRole  string
		wantCode int
	}{
		{"super_admin >= enterprise_admin", "super_admin", "enterprise_admin", http.StatusOK},
		{"system_admin >= enterprise_admin", "system_admin", "enterprise_admin", http.StatusOK},
		{"enterprise_admin >= enterprise_admin", "enterprise_admin", "enterprise_admin", http.StatusOK},
		{"user < enterprise_admin", "user", "enterprise_admin", http.StatusForbidden},
		{"super_admin >= system_admin", "super_admin", "system_admin", http.StatusOK},
		{"system_admin >= system_admin", "system_admin", "system_admin", http.StatusOK},
		{"enterprise_admin < system_admin", "enterprise_admin", "system_admin", http.StatusForbidden},
		{"user < system_admin", "user", "system_admin", http.StatusForbidden},
		{"empty role unauthorized", "", "enterprise_admin", http.StatusUnauthorized},
		{"unknown role forbidden", "guest", "user", http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestAuth("production")
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.userRole != "" {
				c.Set("role", tt.userRole)
			}

			handler := a.requireMinRole(tt.minRole)
			handler(c)

			if w.Code != tt.wantCode {
				t.Errorf("got status %d, want %d", w.Code, tt.wantCode)
			}
		})
	}
}

func TestCanPerformSensitiveOps(t *testing.T) {
	tests := []struct {
		name      string
		role      string
		adminMode string
		want      bool
	}{
		{"super_admin in production", "super_admin", "production", true},
		{"super_admin in mvp", "super_admin", "mvp", true},
		{"system_admin in mvp", "system_admin", "mvp", true},
		{"system_admin in production", "system_admin", "production", false},
		{"user in mvp", "user", "mvp", false},
		{"user in production", "user", "production", false},
		{"enterprise_admin in mvp", "enterprise_admin", "mvp", false},
		{"enterprise_admin in production", "enterprise_admin", "production", false},
		{"empty role", "", "production", false},
		{"empty role mvp", "", "mvp", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestAuth(tt.adminMode)
			got := a.CanPerformSensitiveOps(tt.role)
			if got != tt.want {
				t.Errorf("CanPerformSensitiveOps(%q) with adminMode=%q = %v, want %v", tt.role, tt.adminMode, got, tt.want)
			}
		})
	}
}

func TestRequireAdmin(t *testing.T) {
	tests := []struct {
		name     string
		userRole string
		wantCode int
	}{
		{"system_admin allowed", "system_admin", http.StatusOK},
		{"super_admin allowed", "super_admin", http.StatusOK},
		{"user denied", "user", http.StatusForbidden},
		{"enterprise_admin denied", "enterprise_admin", http.StatusForbidden},
		{"no role unauthorized", "", http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestAuth("production")
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.userRole != "" {
				c.Set("role", tt.userRole)
			}

			handler := a.RequireAdmin()
			handler(c)

			if w.Code != tt.wantCode {
				t.Errorf("got status %d, want %d", w.Code, tt.wantCode)
			}
		})
	}
}

func TestRequireSuperAdmin(t *testing.T) {
	tests := []struct {
		name     string
		userRole string
		wantCode int
	}{
		{"super_admin allowed", "super_admin", http.StatusOK},
		{"system_admin denied", "system_admin", http.StatusForbidden},
		{"user denied", "user", http.StatusForbidden},
		{"no role unauthorized", "", http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestAuth("production")
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.userRole != "" {
				c.Set("role", tt.userRole)
			}

			handler := a.RequireSuperAdmin()
			handler(c)

			if w.Code != tt.wantCode {
				t.Errorf("got status %d, want %d", w.Code, tt.wantCode)
			}
		})
	}
}

func TestRequireEnterpriseAdmin(t *testing.T) {
	tests := []struct {
		name     string
		userRole string
		wantCode int
	}{
		{"super_admin allowed", "super_admin", http.StatusOK},
		{"system_admin allowed", "system_admin", http.StatusOK},
		{"enterprise_admin allowed", "enterprise_admin", http.StatusOK},
		{"user denied", "user", http.StatusForbidden},
		{"no role unauthorized", "", http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestAuth("production")
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.userRole != "" {
				c.Set("role", tt.userRole)
			}

			handler := a.RequireEnterpriseAdmin()
			handler(c)

			if w.Code != tt.wantCode {
				t.Errorf("got status %d, want %d", w.Code, tt.wantCode)
			}
		})
	}
}

func TestJWTManager(t *testing.T) {
	a := newTestAuth("production")
	jm := a.JWTManager()
	if jm == nil {
		t.Fatal("JWTManager() returned nil")
	}

	token, err := jm.Generate("uid", "e@x.com", "user", 0)
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	claims, err := jm.Verify(token)
	if err != nil {
		t.Fatalf("Verify() error: %v", err)
	}
	if claims.UserID != "uid" || claims.Email != "e@x.com" || claims.Role != "user" {
		t.Errorf("claims mismatch: %+v", claims)
	}
}

func TestExpiredToken(t *testing.T) {
	mgr := jwt.NewManager("test-secret-key-32-chars-long!!", -time.Hour)
	token, _ := mgr.Generate("uid", "e@x.com", "user", 0)

	a := newTestAuth("production")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.Header.Set("Authorization", "Bearer "+token)

	handler := a.RequireAuth()
	handler(c)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expired token: got status %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestWrongSecretToken(t *testing.T) {
	otherMgr := jwt.NewManager("different-secret-key-32-chars-long", time.Hour)
	token, _ := otherMgr.Generate("uid", "e@x.com", "user", 0)

	a := newTestAuth("production")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.Header.Set("Authorization", "Bearer "+token)

	handler := a.RequireAuth()
	handler(c)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("wrong secret: got status %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

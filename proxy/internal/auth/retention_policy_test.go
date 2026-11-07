package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mayiants/ai-proxy-go/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestNormalizeRetention(t *testing.T) {
	const day = 24 * time.Hour
	tests := []struct {
		name     string
		mode     string
		days     int
		maxAge   time.Duration
		wantMode string
		wantDays int
	}{
		{"missing fields default to standard + configured max age", "", 0, 90 * day, "standard", 90},
		{"off is preserved", "off", 30, 90 * day, "off", 30},
		{"metadata is preserved", "metadata", 7, 90 * day, "metadata", 7},
		{"unknown mode fails safe to standard", "bogus", 7, 90 * day, "standard", 7},
		{"days above 365 are clamped", "standard", 9999, 90 * day, "standard", 365},
		{"negative days use default", "standard", -1, 30 * day, "standard", 30},
		{"partial day rounds up", "", 0, 36 * time.Hour, "standard", 2},
		{"zero max age falls back to 90", "", 0, 0, "standard", 90},
		{"huge default max age is clamped", "", 0, 1000 * day, "standard", 365},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, d := NormalizeRetention(tt.mode, tt.days, tt.maxAge)
			assert.Equal(t, tt.wantMode, m)
			assert.Equal(t, tt.wantDays, d)
		})
	}
}

func TestHandler_RetentionPolicyReachesContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name     string
		body     string
		wantEnt  string
		wantMode string
		wantDays int
	}{
		{"older portal without fields", `{"valid":true,"user_id":"u","balance":10,"key_type":"general"}`, "", "standard", 90},
		{"policy supplied", `{"valid":true,"user_id":"u","balance":10,"key_type":"enterprise","enterprise_id":"3f2b8c1e-5d6a-4e7f-9a0b-1c2d3e4f5a6b","retention_mode":"metadata","retention_days":14}`,
			"3f2b8c1e-5d6a-4e7f-9a0b-1c2d3e4f5a6b", "metadata", 14},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			cfg := &config.Config{
				Portal:    config.PortalConfig{URL: srv.URL},
				Retention: config.RetentionConfig{MaxAge: 90 * 24 * time.Hour},
			}
			m := NewMiddleware(cfg, zap.NewNop())
			defer m.Stop()

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request, _ = http.NewRequest("GET", "/", nil)
			c.Request.Header.Set("Authorization", "Bearer sk-wgit-testkey123456")
			m.Handler()(c)

			assert.False(t, c.IsAborted())
			assert.Equal(t, tt.wantEnt, c.GetString("enterprise_id"))
			assert.Equal(t, tt.wantMode, c.GetString("retention_mode"))
			assert.Equal(t, tt.wantDays, c.GetInt("retention_days"))
		})
	}
}

func TestHandler_RejectionsExposeAuditContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name         string
		authHeader   string
		portalBody   string
		portalStatus int
		wantStatus   int
		wantCode     string
		wantKeyHash  bool
	}{
		{"missing key", "", "", 200, 401, "MISSING_API_KEY", false},
		{"bad format", "Bearer nope", "", 200, 401, "INVALID_API_KEY_FORMAT", false},
		{"unknown key", "Bearer sk-wgit-testkey123456", `{"valid":false}`, 200, 401, "INVALID_API_KEY", false},
		{"zero balance resolves key hash", "Bearer sk-wgit-testkey123456", `{"valid":true,"user_id":"u","balance":0,"key_type":"general"}`, 200, 402, "BALANCE_EXHAUSTED", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.portalStatus)
				_, _ = w.Write([]byte(tt.portalBody))
			}))
			defer srv.Close()
			m := NewMiddleware(&config.Config{Portal: config.PortalConfig{URL: srv.URL}}, zap.NewNop())
			defer m.Stop()

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request, _ = http.NewRequest("GET", "/", nil)
			if tt.authHeader != "" {
				c.Request.Header.Set("Authorization", tt.authHeader)
			}
			m.Handler()(c)

			assert.True(t, c.IsAborted())
			assert.Equal(t, tt.wantStatus, w.Code)
			assert.Equal(t, tt.wantCode, c.GetString("error_code"))
			assert.NotEmpty(t, c.GetString("error_message"))
			if tt.wantKeyHash {
				assert.Equal(t, hashKeySHA256("sk-wgit-testkey123456"), c.GetString("key_hash"))
				assert.NotContains(t, c.GetString("key_hash"), "sk-")
			} else {
				assert.Empty(t, c.GetString("key_hash"))
			}
		})
	}
}

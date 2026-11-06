package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// TestParseHealthWindow tests the window parsing helper.
func TestParseHealthWindow(t *testing.T) {
	tests := []struct {
		name   string
		window string
		want   string
	}{
		{"default (empty)", "", "1 hour"},
		{"15 minutes", "15m", "15 minutes"},
		{"30 minutes", "30m", "30 minutes"},
		{"1 hour", "1h", "1 hour"},
		{"12 hours", "12h", "12 hours"},
		{"24 hours", "24h", "24 hours"},
		{"unknown value", "unknown", "1 hour"},
		{"garbage", "abc", "1 hour"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/?window="+tt.window, nil)

			got := parseHealthWindow(c)
			if got != tt.want {
				t.Errorf("parseHealthWindow() with window=%q = %q, want %q", tt.window, got, tt.want)
			}
		})
	}
}

// TestAdminRoutesUsageMethodsExist is a compile-time check.
func TestAdminRoutesUsageMethodsExist(t *testing.T) {
	_ = (*AdminRoutes).getRateLimitUsage
	_ = (*AdminRoutes).listUpstreamUsage
	_ = (*AdminRoutes).getUpstreamUsageSummary
	_ = (*AdminRoutes).exportUpstreamUsage
	_ = (*AdminRoutes).listProviderHealth
	_ = (*AdminRoutes).exportProviderHealth
	_ = (*AdminRoutes).getProviderHealthSummary
	_ = (*AdminRoutes).getProviderHealthDetail
}

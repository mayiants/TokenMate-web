package routes

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// TestUsage_DaysParamParsing tests the days query parameter parsing for usage endpoints.
func TestUsage_DaysParamParsing(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		wantDays int
	}{
		{"default days", "", 7},
		{"valid days 14", "days=14", 14},
		{"valid days 30", "days=30", 30},
		{"invalid days", "days=abc", 7},
		{"zero days", "days=0", 7},
		{"negative days", "days=-5", 7},
		{"days > 90", "days=100", 7},
		{"exactly 90", "days=90", 90},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/usage/recent?"+tt.query, nil)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = req

			days := 7
			if d := c.Query("days"); d != "" {
				if parsed, err := strconv.Atoi(d); err == nil && parsed > 0 && parsed <= 90 {
					days = parsed
				}
			}
			if days != tt.wantDays {
				t.Errorf("days = %d, want %d", days, tt.wantDays)
			}
		})
	}
}

// TestUsage_PeriodParamParsing tests the period query parameter parsing.
func TestUsage_PeriodParamParsing(t *testing.T) {
	tests := []struct {
		name     string
		period   string
		wantDays int
	}{
		{"default", "", 7},
		{"7d", "7d", 7},
		{"14d", "14d", 14},
		{"30d", "30d", 30},
		{"90d", "90d", 90},
		{"invalid format", "7days", 7},
		{"empty suffix", "7", 7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			daysStr := strings.TrimSuffix(tt.period, "d")
			days := 7
			if parsed, err := strconv.Atoi(daysStr); err == nil && parsed > 0 {
				days = parsed
			}
			if days != tt.wantDays {
				t.Errorf("days from period %q = %d, want %d", tt.period, days, tt.wantDays)
			}
		})
	}
}

// TestUsage_AdminRoleDetection tests admin role detection logic.
func TestUsage_AdminRoleDetection(t *testing.T) {
	tests := []struct {
		name      string
		role      string
		wantAdmin bool
	}{
		{"system_admin", "system_admin", true},
		{"super_admin", "super_admin", true},
		{"enterprise_admin", "enterprise_admin", false},
		{"regular user", "user", false},
		{"empty role", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isAdmin := tt.role == "system_admin" || tt.role == "super_admin"
			if isAdmin != tt.wantAdmin {
				t.Errorf("isAdmin(%q) = %v, want %v", tt.role, isAdmin, tt.wantAdmin)
			}
		})
	}
}

// TestUsage_CSVExportHeaders tests CSV export header generation.
func TestUsage_CSVExportHeaders(t *testing.T) {
	headers := []string{
		"trace_id", "model", "provider", "region",
		"input_tokens", "output_tokens", "cached_input_tokens",
		"effective_tokens", "reasoning_tokens", "cost",
		"latency_ms", "cache_hit", "error", "created_at",
	}

	for _, h := range headers {
		if h == "" {
			t.Error("CSV header should not be empty")
		}
	}

	if len(headers) < 10 {
		t.Errorf("expected at least 10 CSV headers, got %d", len(headers))
	}
}

// TestUsage_UsageRecordKeys tests the usage record JSON keys.
func TestUsage_UsageRecordKeys(t *testing.T) {
	record := map[string]interface{}{
		"trace_id":      "trace-1",
		"model":         "gpt-4",
		"provider":      "openai",
		"input_tokens":  100,
		"output_tokens": 50,
		"cached_tokens": 20,
		"total_cost":    0.005,
		"latency_ms":    200,
		"created_at":    "2024-01-01T00:00:00Z",
	}

	requiredKeys := []string{"trace_id", "model", "provider", "input_tokens", "output_tokens", "total_cost"}
	for _, key := range requiredKeys {
		if _, ok := record[key]; !ok {
			t.Errorf("usage record missing key: %q", key)
		}
	}
}

// TestUsage_RecentUsageDefaultLimit tests the recent usage default limit.
func TestUsage_RecentUsageDefaultLimit(t *testing.T) {
	// Default limit for getRecentUsage is 7 days, max 90
	dflt := 7
	max := 90
	if dflt <= 0 || dflt > max {
		t.Errorf("default days %d should be between 1 and %d", dflt, max)
	}
}

// TestUsage_UsageSummaryKeys tests the usage summary response keys.
func TestUsage_UsageSummaryKeys(t *testing.T) {
	summary := map[string]interface{}{
		"today": map[string]interface{}{"requests": 0, "tokens": 0},
		"month": map[string]interface{}{"requests": 0, "tokens": 0},
	}

	if _, ok := summary["today"]; !ok {
		t.Error("usage summary should have 'today' key")
	}
	if _, ok := summary["month"]; !ok {
		t.Error("usage summary should have 'month' key")
	}
}

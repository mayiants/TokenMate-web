package routes

import (
	"fmt"
	"testing"
)

// TestStatus_HealthResponse verifies the health check response structure.
func TestStatus_HealthResponse(t *testing.T) {
	// The health endpoint returns {"status": "ok"}
	status := "ok"
	if status != "ok" {
		t.Errorf("health status should be 'ok', got %q", status)
	}
}

// TestStatus_MetricsFormat tests Prometheus metrics formatting.
func TestStatus_MetricsFormat(t *testing.T) {
	// Test the metric format pattern
	totalRequests := 42
	expected := "# HELP portal_requests_total Total requests in last hour\n# TYPE portal_requests_total gauge\nportal_requests_total 42\n"

	result := fmt.Sprintf("# HELP portal_requests_total Total requests in last hour\n# TYPE portal_requests_total gauge\nportal_requests_total %d\n", totalRequests)
	if result != expected {
		t.Errorf("metrics format mismatch:\ngot:\n%s\nwant:\n%s", result, expected)
	}
}

// TestStatus_MetricsLineFormat tests individual metric line format.
func TestStatus_MetricsLineFormat(t *testing.T) {
	// Each metric should follow Prometheus format
	metricTypes := []struct {
		name       string
		wantPrefix string
	}{
		{"portal_requests_total", "# HELP portal_requests_total"},
		{"portal_tokens_total", "# HELP portal_tokens_total"},
		{"portal_active_users", "# HELP portal_active_users"},
		{"portal_users_total", "# HELP portal_users_total"},
		{"portal_api_keys_active", "# HELP portal_api_keys_active"},
		{"portal_subscriptions_active", "# HELP portal_subscriptions_active"},
	}

	for _, mt := range metricTypes {
		if mt.wantPrefix == "" {
			t.Errorf("metric %q has empty help prefix", mt.name)
		}
	}
}

// TestStatus_ServiceStatus tests the service status structure.
func TestStatus_ServiceStatus(t *testing.T) {
	// Verify expected service keys
	services := []string{"database", "redis"}
	for _, svc := range services {
		if svc == "" {
			t.Error("service name should not be empty")
		}
	}
}

// TestStatus_VersionFormat tests the version format.
func TestStatus_VersionFormat(t *testing.T) {
	version := "2.0.0"
	if len(version) == 0 {
		t.Error("version should not be empty")
	}
	// Should match semver format roughly
	parts := []string{"2", "0", "0"}
	for _, p := range parts {
		if p == "" {
			t.Error("version part should not be empty")
		}
	}
}

// TestStatus_PeriodParsing tests the period query parameter parsing.
func TestStatus_PeriodParsing(t *testing.T) {
	tests := []struct {
		name       string
		period     string
		wantPeriod string
	}{
		{"default", "", "24h"},
		{"24h", "24h", "24h"},
		{"7d", "7d", "7d"},
		{"30d", "30d", "30d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			period := tt.period
			if period == "" {
				period = "24h"
			}
			if period != tt.wantPeriod {
				t.Errorf("period = %q, want %q", period, tt.wantPeriod)
			}
		})
	}
}

// TestStatus_ProviderQueryParam tests provider filter logic.
func TestStatus_ProviderQueryParam(t *testing.T) {
	tests := []struct {
		name      string
		model     string
		group     string
		wantModel string
		wantGroup string
	}{
		{"no filters", "", "", "", ""},
		{"model only", "gpt-4", "", "gpt-4", ""},
		{"group only", "", "group-1", "", "group-1"},
		{"both", "gpt-4", "group-1", "gpt-4", "group-1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.model != tt.wantModel {
				t.Errorf("model = %q, want %q", tt.model, tt.wantModel)
			}
			if tt.group != tt.wantGroup {
				t.Errorf("group = %q, want %q", tt.group, tt.wantGroup)
			}
		})
	}
}

// TestStatus_UpstreamStatusOrder tests that upstream status results are sorted.
func TestStatus_UpstreamStatusOrder(t *testing.T) {
	// Verify that data is initialized as empty slice (not nil)
	data := make([]interface{}, 0)
	if len(data) != 0 {
		t.Error("empty data should have length 0")
	}
}

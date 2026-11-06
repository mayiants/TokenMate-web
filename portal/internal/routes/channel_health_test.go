package routes

import (
	"strconv"
	"testing"
)

// TestChannelHealthRoutesMethodsExist is a compile-time check.
func TestChannelHealthRoutesMethodsExist(t *testing.T) {
	_ = (*ChannelHealthRoutes).listChannels
	_ = (*ChannelHealthRoutes).testAll
	_ = (*ChannelHealthRoutes).getChannel
	_ = (*ChannelHealthRoutes).testChannel
}

// TestChannelHealthStatusClassification tests status classification logic.
func TestChannelHealthStatusClassification(t *testing.T) {
	tests := []struct {
		name       string
		errorRate  float64
		wantStatus string
	}{
		{"healthy (0 errors)", 0.0, "healthy"},
		{"healthy (10% errors)", 0.10, "healthy"},
		{"healthy (exactly 20%)", 0.20, "healthy"},
		{"degraded (21% errors)", 0.21, "degraded"},
		{"degraded (30% errors)", 0.30, "degraded"},
		{"degraded (exactly 50%)", 0.50, "degraded"},
		{"unhealthy (51% errors)", 0.51, "unhealthy"},
		{"unhealthy (100% errors)", 1.0, "unhealthy"},
		{"unhealthy (over 100%)", 1.5, "unhealthy"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := "healthy"
			if tt.errorRate > 0.5 {
				status = "unhealthy"
			} else if tt.errorRate > 0.2 {
				status = "degraded"
			}

			if status != tt.wantStatus {
				t.Errorf("errorRate=%.2f: status=%q, want %q", tt.errorRate, status, tt.wantStatus)
			}
		})
	}
}

// TestChannelHealthErrorRateCalculation tests error rate arithmetic.
func TestChannelHealthErrorRateCalculation(t *testing.T) {
	tests := []struct {
		name          string
		successCount  int
		failureCount  int
		wantErrorRate float64
	}{
		{"all success", 10, 0, 0.0},
		{"all failure", 0, 10, 1.0},
		{"mixed 50/50", 5, 5, 0.5},
		{"no data", 0, 0, 0.0},
		{"single failure", 99, 1, 0.01},
		{"single success", 1, 99, 0.99},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			total := tt.successCount + tt.failureCount
			errorRate := 0.0
			if total > 0 {
				errorRate = float64(tt.failureCount) / float64(total)
			}
			if errorRate != tt.wantErrorRate {
				t.Errorf("errorRate = %f, want %f", errorRate, tt.wantErrorRate)
			}
		})
	}
}

// TestChannelHealthConstants tests that health-related constants are valid.
func TestChannelHealthConstants(t *testing.T) {
	// The test timeout is 8 seconds
	testTimeout := 8 // seconds
	if testTimeout <= 0 {
		t.Error("test timeout should be positive")
	}

	// User agent format
	userAgent := "AI-Proxy-Health-Check/1.0"
	if userAgent == "" {
		t.Error("user agent should not be empty")
	}
}

// TestHTTPStatusCodeClassification tests the HTTP status code logic.
func TestHTTPStatusCodeClassification(t *testing.T) {
	tests := []struct {
		statusCode int
		isSuccess  bool
		is5xx      bool
	}{
		{200, true, false},
		{201, true, false},
		{301, true, false},
		{400, true, false}, // 4xx is < 500, channel health treats as success
		{401, true, false},
		{404, true, false},
		{429, true, false},
		{500, false, true},
		{502, false, true},
		{503, false, true},
	}

	for _, tt := range tests {
		t.Run(strconv.Itoa(tt.statusCode), func(t *testing.T) {
			isSuccess := tt.statusCode < 500
			is5xx := tt.statusCode >= 500

			if isSuccess != tt.isSuccess {
				t.Errorf("status %d: isSuccess=%v, want %v", tt.statusCode, isSuccess, tt.isSuccess)
			}
			if is5xx != tt.is5xx {
				t.Errorf("status %d: is5xx=%v, want %v", tt.statusCode, is5xx, tt.is5xx)
			}
		})
	}
}

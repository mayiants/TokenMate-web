package routes

import (
	"math"
	"testing"
)

// TestRound2 tests the round-to-2-decimal-places helper.
func TestRound2(t *testing.T) {
	tests := []struct {
		name  string
		input float64
		want  float64
	}{
		{"zero", 0, 0},
		{"integer", 5.0, 5.0},
		{"one decimal", 3.14, 3.14},
		{"two decimals", 3.141, 3.14},
		{"round up", 3.145, 3.15},
		{"round down", 3.144, 3.14},
		{"negative", -3.145, -3.15}, // Go's math.Round rounds half away from zero
		{"large number", 12345.6789, 12345.68},
		{"very small", 0.001, 0.0},
		{"very small round up", 0.005, 0.01},
		{"NaN", math.NaN(), math.NaN()}, // NaN comparison is special
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := round2(tt.input)
			if math.IsNaN(tt.want) {
				if !math.IsNaN(got) {
					t.Errorf("round2(%v) = %v, want NaN", tt.input, got)
				}
				return
			}
			if got != tt.want {
				t.Errorf("round2(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// TestRound2Precision verifies that output always has at most 2 decimal places.
func TestRound2Precision(t *testing.T) {
	testValues := []float64{
		0.333, 0.666, 0.999, 1.234, 5.678, 100.005,
	}
	for _, v := range testValues {
		got := round2(v)
		// Multiply by 100 and check no sub-cent precision
		scaled := got * 100
		if math.Abs(scaled-math.Round(scaled)) > 1e-9 {
			t.Errorf("round2(%v) = %v has more than 2 decimal places", v, got)
		}
	}
}

// TestMapInternalStatus tests the status mapping function.
func TestMapInternalStatus(t *testing.T) {
	tests := []struct {
		name     string
		internal string
		want     string
	}{
		{"healthy → available", "healthy", "available"},
		{"degraded → degraded", "degraded", "degraded"},
		{"unhealthy → unavailable", "unhealthy", "unavailable"},
		{"unknown → unknown (insufficient data, distinct from missing)", "unknown", "unknown"},
		{"empty → missing", "", "missing"},
		{"random → missing", "random_value", "missing"},
		{"HEALTHY uppercase → missing", "HEALTHY", "missing"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapInternalStatus(tt.internal)
			if got != tt.want {
				t.Errorf("mapInternalStatus(%q) = %q, want %q", tt.internal, got, tt.want)
			}
		})
	}
}

// TestMapInternalStatusKnownValues verifies all known statuses map correctly.
func TestMapInternalStatusKnownValues(t *testing.T) {
	// Assert the mapping is exhaustive for known values
	knownMappings := map[string]string{
		"healthy":   "available",
		"degraded":  "degraded",
		"unhealthy": "unavailable",
	}
	for internal, expected := range knownMappings {
		got := mapInternalStatus(internal)
		if got != expected {
			t.Errorf("mapInternalStatus(%q) = %q, want %q", internal, got, expected)
		}
	}
}

// TestAdminRoutesUpstreamOpsMethodsExist is a compile-time check.
func TestAdminRoutesUpstreamOpsMethodsExist(t *testing.T) {
	_ = (*AdminRoutes).listUpstreamOpsHealth
	_ = (*AdminRoutes).listUpstreamOpsProviderHealth
	_ = (*AdminRoutes).getUpstreamOpsHealthSummary
	_ = (*AdminRoutes).listUpstreamOpsErrors
	_ = (*AdminRoutes).exportUpstreamOpsHealth
	_ = (*AdminRoutes).listUpstreamOpsMonitorKeys
	_ = (*AdminRoutes).exportUpstreamMonitorKeys
	_ = (*AdminRoutes).exportDownstreamMonitor
	_ = (*AdminRoutes).getUpstreamOpsHistory
}

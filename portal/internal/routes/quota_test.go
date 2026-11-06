package routes

import (
	"math"
	"testing"
)

// TestQuotaToUSD tests the micro-USD to USD string conversion.
func TestQuotaToUSD(t *testing.T) {
	tests := []struct {
		name    string
		quota   int64
		wantUSD string
	}{
		{"zero", 0, "$0.000000"},
		{"one dollar", 1_000_000, "$1.000000"},
		{"half dollar", 500_000, "$0.500000"},
		{"one cent", 10_000, "$0.010000"},
		{"micro dollar", 1, "$0.000001"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := QuotaToUSD(tt.quota)
			if got != tt.wantUSD {
				t.Errorf("QuotaToUSD(%d) = %q, want %q", tt.quota, got, tt.wantUSD)
			}
		})
	}
}

// TestQuotaToUSDFloat tests the micro-USD to float64 conversion.
func TestQuotaToUSDFloat(t *testing.T) {
	tests := []struct {
		name    string
		quota   int64
		wantUSD float64
	}{
		{"zero", 0, 0.0},
		{"one dollar", 1_000_000, 1.0},
		{"half dollar", 500_000, 0.5},
		{"one cent", 10_000, 0.01},
		{"micro dollar", 1, 0.000001},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := QuotaToUSDFloat(tt.quota)
			if math.Abs(got-tt.wantUSD) > 1e-9 {
				t.Errorf("QuotaToUSDFloat(%d) = %f, want %f", tt.quota, got, tt.wantUSD)
			}
		})
	}
}

// TestQuotaToRMB tests the micro-USD to RMB string conversion.
func TestQuotaToRMB(t *testing.T) {
	tests := []struct {
		name    string
		quota   int64
		rate    float64
		wantRMB string
	}{
		{"zero", 0, 7.2, "¥0.00"},
		{"one dollar", 1_000_000, 7.2, "¥7.20"},
		{"half dollar", 500_000, 7.2, "¥3.60"},
		{"rate 7.0", 1_000_000, 7.0, "¥7.00"},
		{"rate 7.5", 1_000_000, 7.5, "¥7.50"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := QuotaToRMB(tt.quota, tt.rate)
			if got != tt.wantRMB {
				t.Errorf("QuotaToRMB(%d, %f) = %q, want %q", tt.quota, tt.rate, got, tt.wantRMB)
			}
		})
	}
}

// TestUSDtoQuota tests the USD to micro-USD conversion.
func TestUSDtoQuota(t *testing.T) {
	tests := []struct {
		name      string
		usd       float64
		wantQuota int64
	}{
		{"zero", 0.0, 0},
		{"one dollar", 1.0, 1_000_000},
		{"half dollar", 0.5, 500_000},
		{"one cent", 0.01, 10_000},
		{"small amount", 0.000001, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := USDtoQuota(tt.usd)
			if got != tt.wantQuota {
				t.Errorf("USDtoQuota(%f) = %d, want %d", tt.usd, got, tt.wantQuota)
			}
		})
	}
}

// TestRMBYuanToQuota tests the RMB yuan to micro-USD conversion.
func TestRMBYuanToQuota(t *testing.T) {
	tests := []struct {
		name      string
		yuan      float64
		rate      float64
		wantQuota int64
	}{
		{"zero yuan", 0, 7.2, 0},
		{"7.2 yuan = 1 USD", 7.2, 7.2, 1_000_000},
		{"1 yuan at 7.2", 1.0, 7.2, 138888},
		{"10 yuan at 7.0", 10.0, 7.0, 1_428_571},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RMBYuanToQuota(tt.yuan, tt.rate)
			if got != tt.wantQuota {
				t.Errorf("RMBYuanToQuota(%f, %f) = %d, want %d", tt.yuan, tt.rate, got, tt.wantQuota)
			}
		})
	}
}

// TestFormatQuota tests the quota formatting for display.
func TestFormatQuota(t *testing.T) {
	tests := []struct {
		name    string
		quota   int64
		curr    string
		rate    float64
		wantStr string
	}{
		{"USD display", 1_000_000, "USD", 7.2, "$1.00"},
		{"CNY display", 1_000_000, "CNY", 7.2, "¥7.20"},
		{"zero USD", 0, "USD", 7.2, "$0.00"},
		{"zero CNY", 0, "CNY", 7.2, "¥0.00"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatQuota(tt.quota, tt.curr, tt.rate)
			if got != tt.wantStr {
				t.Errorf("FormatQuota(%d, %q, %f) = %q, want %q", tt.quota, tt.curr, tt.rate, got, tt.wantStr)
			}
		})
	}
}

// TestQuotaPerUSD tests the quota per USD constant.
func TestQuotaPerUSD(t *testing.T) {
	if QuotaPerUSD != 1_000_000 {
		t.Errorf("QuotaPerUSD = %d, want 1_000_000", QuotaPerUSD)
	}
}

// TestQuotaRoundTrip tests that converting USD -> quota -> USD gives the same result.
func TestQuotaRoundTrip_USD(t *testing.T) {
	tests := []float64{0.0, 1.0, 0.5, 0.01, 100.0, 0.000001}

	for _, usd := range tests {
		quota := USDtoQuota(usd)
		result := QuotaToUSDFloat(quota)
		if math.Abs(result-usd) > 1e-5 {
			t.Errorf("round trip: USD %f -> quota %d -> USD %f (diff %f)", usd, quota, result, math.Abs(result-usd))
		}
	}
}

// TestQuotaRoundTrip_RMB tests the RMB round trip.
func TestQuotaRoundTrip_RMB(t *testing.T) {
	tests := []struct {
		yuan float64
		rate float64
	}{
		{7.2, 7.2},
		{1.0, 7.2},
		{100.0, 7.0},
	}

	for _, tt := range tests {
		quota := RMBYuanToQuota(tt.yuan, tt.rate)
		result := QuotaToRMBFloat(quota, tt.rate)
		if math.Abs(result-tt.yuan) > 1e-4 {
			t.Errorf("round trip: RMB %f @ %f -> quota %d -> RMB %f (diff %f)", tt.yuan, tt.rate, quota, result, math.Abs(result-tt.yuan))
		}
	}
}

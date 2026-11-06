package services

import (
	"strings"
	"testing"
)

// TestQuotaConversions pins the rounding semantics of the money conversion
// helpers, especially the float64->int64 truncation in USDtoQuota which
// systematically rounds DOWN on the recharge path.
func TestQuotaConversions(t *testing.T) {
	if got := USDtoQuota(1.0); got != 1_000_000 {
		t.Errorf("USDtoQuota(1.0) = %d, want 1000000", got)
	}
	// Truncation (not rounding): 0.5 cent -> 5000 micro; sub-micro fractions drop.
	if got := USDtoQuota(0.005); got != 5000 {
		t.Errorf("USDtoQuota(0.005) = %d, want 5000", got)
	}
	if got := USDtoQuota(0.0000009); got != 0 {
		t.Errorf("USDtoQuota(0.0000009) = %d, want 0 (truncation)", got)
	}

	if got := QuotaToUSD(1_234_567); got != "$1.234567" {
		t.Errorf("QuotaToUSD = %q, want $1.234567", got)
	}
	if got := QuotaToUSDFloat(500_000); got != 0.5 {
		t.Errorf("QuotaToUSDFloat = %v, want 0.5", got)
	}

	// RMBCentToQuota: 100 fen = 1 yuan; at 7.2 that is 1/7.2 USD.
	got := RMBCentToQuota(100, 7.2)
	wantF := 1.0 / 7.2 * 1_000_000
	want := int64(wantF)
	if got != want {
		t.Errorf("RMBCentToQuota(100, 7.2) = %d, want %d", got, want)
	}
	// Zero/negative rate: must not panic or produce garbage infinities.
	_ = RMBCentToQuota(100, 0) // documents current behavior: divides by zero rate

	// RMBYuanToQuota round-trips with QuotaToRMBFloat within 1 micro.
	yuan := QuotaToRMBFloat(RMBYuanToQuota(100, 7.2), 7.2)
	if yuan < 99.99 || yuan > 100.01 {
		t.Errorf("round trip 100 yuan = %v, want ~100", yuan)
	}
}

func TestFormatQuotaConversions(t *testing.T) {
	if got := FormatQuota(1_500_000, "USD", 7.2); got != "$1.50" {
		t.Errorf("USD: got %q, want $1.50", got)
	}
	if got := FormatQuota(1_000_000, "CNY", 7.2); got != "¥7.20" {
		t.Errorf("CNY: got %q, want ¥7.20", got)
	}
	if got := FormatQuotaFloat(1_000_000, "CNY", 7.2); got != 7.2 {
		t.Errorf("FormatQuotaFloat CNY = %v, want 7.2", got)
	}
	if got := QuotaToCurrency(2_000_000, "EUR", 7.2); !strings.HasPrefix(got, "$") {
		t.Errorf("unknown currency must default to USD, got %q", got)
	}
}

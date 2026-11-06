package web

import (
	"strings"
	"testing"
)

func TestActionsUsdToQuota(t *testing.T) {
	tests := []struct {
		usd  float64
		want int64
	}{
		{1.0, 1000000},
		{0.5, 500000},
		{0, 0},
		{10.0, 10000000},
	}

	for _, tt := range tests {
		got := usdToQuota(tt.usd)
		if got != tt.want {
			t.Errorf("usdToQuota(%v) = %d, want %d", tt.usd, got, tt.want)
		}
	}
}

func TestActionsRmbYuanToQuota(t *testing.T) {
	tests := []struct {
		yuan float64
		rate float64
		want int64
	}{
		{7.20, 7.20, 1000000},
		{14.40, 7.20, 2000000},
		{72.00, 7.20, 10000000},
	}

	for _, tt := range tests {
		got := rmbYuanToQuota(tt.yuan, tt.rate)
		if got != tt.want {
			t.Errorf("rmbYuanToQuota(%v, %v) = %d, want %d", tt.yuan, tt.rate, got, tt.want)
		}
	}
}

func TestActionsQuotaPerUSDConstant(t *testing.T) {
	if quotaPerUSD != 1000000 {
		t.Errorf("quotaPerUSD = %d, want 1000000", quotaPerUSD)
	}
}

func TestActionsKeyPreviewTruncation(t *testing.T) {
	tests := []struct {
		key string
	}{
		{"sk-wgit-test-key-123"},
		{"short"},
		{"sk-very-long-key-with-many-chars"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			preview := tt.key
			if len(tt.key) > 12 {
				preview = tt.key[:8] + "..." + tt.key[len(tt.key)-4:]
			}
			if len(tt.key) <= 12 && preview != tt.key {
				t.Errorf("short key should not be truncated: %q -> %q", tt.key, preview)
			}
			if len(tt.key) > 12 && !strings.Contains(preview, "...") {
				t.Errorf("long key preview should contain '...': %q", preview)
			}
		})
	}
}

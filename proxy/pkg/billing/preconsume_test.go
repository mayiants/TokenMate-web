package billing

import (
	"testing"

	"github.com/mayiants/ai-proxy-go/types"
	"github.com/stretchr/testify/assert"
)

func TestEstimateInputCost(t *testing.T) {
	b := NewBilling()

	// Set up pricing: gpt-4o ModelRatio = $2.50/M input
	b.SetPricing("gpt-4o", "openai", &types.PriceData{
		ModelRatio: 2.5,
	})

	tests := []struct {
		name         string
		model        string
		provider     string
		inputTokens  int
		margin       float64
		wantMicroUSD int64
	}{
		{
			name:         "1000 tokens at $2.50/M",
			model:        "gpt-4o",
			provider:     "openai",
			inputTokens:  1000,
			margin:       1.0,
			wantMicroUSD: 2500,
		},
		{
			name:         "1M tokens at $2.50/M",
			model:        "gpt-4o",
			provider:     "openai",
			inputTokens:  1_000_000,
			margin:       1.0,
			wantMicroUSD: 2_500_000,
		},
		{
			name:         "with 1.2x margin",
			model:        "gpt-4o",
			provider:     "openai",
			inputTokens:  1000,
			margin:       1.2,
			wantMicroUSD: 3000,
		},
		{
			name:         "zero tokens",
			model:        "gpt-4o",
			provider:     "openai",
			inputTokens:  0,
			margin:       1.0,
			wantMicroUSD: 0,
		},
		{
			name:         "unknown model uses $1/M fallback",
			model:        "unknown-model",
			provider:     "unknown",
			inputTokens:  1000,
			margin:       1.0,
			wantMicroUSD: 1000,
		},
		{
			name:         "very small request — rounds to nearest μ$",
			model:        "gpt-4o",
			provider:     "openai",
			inputTokens:  1,
			margin:       1.0,
			wantMicroUSD: 2, // 1/1M * 2.5 * 1M = 2.5 → int64 = 2
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := b.EstimateInputCost(tt.model, tt.provider, tt.inputTokens, tt.margin)
			assert.Equal(t, tt.wantMicroUSD, got)
		})
	}
}

func TestDetermineSettleAction(t *testing.T) {
	tests := []struct {
		name       string
		success    bool
		statusCode int
		errorType  string
		wantAction SettleAction
	}{
		// Success → unfreeze (normal billing queue handles actual cost)
		{"success 200", true, 200, "", SettleActionUnfreeze},
		{"success with usage", true, 200, "", SettleActionUnfreeze},

		// Timeout → charge (upstream consumed input tokens)
		{"timeout statusCode=0", false, 0, "timeout", SettleActionCharge},
		{"timeout explicit", false, 0, "timeout", SettleActionCharge},

		// Success without usage data → charge (request must not be free)
		{"usage missing", false, 200, "usage_missing", SettleActionCharge},

		// 4xx → unfreeze (upstream rejected before processing)
		{"400 bad request", false, 400, "4xx", SettleActionUnfreeze},
		{"401 auth error", false, 401, "4xx", SettleActionUnfreeze},
		{"404 not found", false, 404, "4xx", SettleActionUnfreeze},

		// 5xx → unfreeze (favor user; reconciliation covers discrepancies)
		{"500 internal", false, 500, "5xx", SettleActionUnfreeze},
		{"502 bad gateway", false, 502, "5xx", SettleActionUnfreeze},
		{"503 overloaded", false, 503, "5xx", SettleActionUnfreeze},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetermineSettleAction(tt.success, tt.statusCode, tt.errorType)
			assert.Equal(t, tt.wantAction, got)
		})
	}
}

func TestPreConsume_NoPortal(t *testing.T) {
	b := NewBilling()
	result := b.PreConsume(nil, PreConsumeRequest{
		UserID:      "user1",
		Model:       "gpt-4o",
		InputTokens: 1000,
	})
	assert.True(t, result.Success)
	assert.False(t, result.Rejected)
	assert.Equal(t, int64(0), result.Amount)
}

func TestSettle_NoPortal(t *testing.T) {
	b := NewBilling()
	result := b.Settle(nil, SettleRequest{
		RequestID:   "req-1",
		PreConsumed: 1000,
		Success:     true,
	})
	assert.True(t, result.Success)
}

func TestSettle_ZeroPreConsumed(t *testing.T) {
	b := NewBillingWithPortal("http://localhost:9999", "secret", 0)
	result := b.Settle(nil, SettleRequest{
		RequestID:   "req-1",
		PreConsumed: 0,
		Success:     true,
	})
	assert.True(t, result.Success)
	assert.Equal(t, int64(0), result.Charged)
}

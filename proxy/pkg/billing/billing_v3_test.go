package billing

import (
	"math"
	"testing"

	"github.com/mayiants/ai-proxy-go/dto"
	"github.com/mayiants/ai-proxy-go/types"
)

// TestCalculateCostExact - Test with exact expected values (not ranges)
func TestCalculateCostExact(t *testing.T) {
	tests := []struct {
		name       string
		usage      *dto.Usage
		pricing    *types.PriceData
		wantInput  float64
		wantOutput float64
		wantCache  float64
		wantTotal  float64
	}{
		{
			name: "basic - no cache, no reasoning",
			usage: &dto.Usage{
				PromptTokens:     1000,
				CompletionTokens: 500,
			},
			pricing: &types.PriceData{
				ModelRatio:      1.0,
				CompletionRatio: 3.0,
				CacheRatio:      0.1,
			},
			wantInput:  0.001,  // 1000/1e6 * 1.0
			wantOutput: 0.0015, // 500/1e6 * 1.0 * 3.0
			wantCache:  0.0,    // no cached tokens
			wantTotal:  0.0025, // sum
		},
		{
			name: "with cache hit",
			usage: &dto.Usage{
				PromptTokens:     1000,
				CompletionTokens: 500,
				PromptTokensDetails: dto.InputTokenDetails{
					CachedTokens: 400,
				},
			},
			pricing: &types.PriceData{
				ModelRatio:      1.0,
				CompletionRatio: 3.0,
				CacheRatio:      0.1,
			},
			wantInput:  0.0006,  // (1000-400)/1e6 * 1.0 = 600/1e6
			wantOutput: 0.0015,  // 500/1e6 * 1.0 * 3.0
			wantCache:  0.00004, // 400/1e6 * 1.0 * 0.1
			wantTotal:  0.00214, // sum
		},
		{
			name: "gpt-4o pricing",
			usage: &dto.Usage{
				PromptTokens:     10000,
				CompletionTokens: 5000,
			},
			pricing: &types.PriceData{
				ModelRatio:      5.0, // $5/1M tokens
				CompletionRatio: 3.0, // output is 3x input
				CacheRatio:      0.1,
			},
			wantInput:  0.05,  // 10000/1e6 * 5.0
			wantOutput: 0.075, // 5000/1e6 * 5.0 * 3.0
			wantCache:  0.0,
			wantTotal:  0.125,
		},
		{
			name: "claude-sonnet with reasoning",
			usage: &dto.Usage{
				PromptTokens:     1000,
				CompletionTokens: 500,
				CompletionTokenDetails: dto.OutputTokenDetails{
					ReasoningTokens: 200,
				},
			},
			pricing: &types.PriceData{
				ModelRatio:      3.0, // $3/1M tokens
				CompletionRatio: 5.0,
				CacheRatio:      0.1,
			},
			wantInput:  0.003,  // 1000/1e6 * 3.0
			wantOutput: 0.0075, // 500/1e6 * 3.0 * 5.0
			wantCache:  0.0,
			wantTotal:  0.0111, // 0.003 + 0.0075 + (200/1e6 * 3.0)
		},
		{
			name: "deepseek pricing",
			usage: &dto.Usage{
				PromptTokens:     100000,
				CompletionTokens: 50000,
			},
			pricing: &types.PriceData{
				ModelRatio:      0.14, // $0.14/1M tokens
				CompletionRatio: 2.0,
				CacheRatio:      0.1,
			},
			wantInput:  0.014, // 100000/1e6 * 0.14
			wantOutput: 0.014, // 50000/1e6 * 0.14 * 2.0
			wantCache:  0.0,
			wantTotal:  0.028,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateCost(tt.usage, tt.pricing)
			if result == nil {
				t.Fatal("CalculateCost returned nil")
			}

			if !floatEquals(result.InputCost, tt.wantInput, 1e-10) {
				t.Errorf("InputCost = %v, want %v", result.InputCost, tt.wantInput)
			}
			if !floatEquals(result.OutputCost, tt.wantOutput, 1e-10) {
				t.Errorf("OutputCost = %v, want %v", result.OutputCost, tt.wantOutput)
			}
			if !floatEquals(result.CacheReadCost, tt.wantCache, 1e-10) {
				t.Errorf("CacheReadCost = %v, want %v", result.CacheReadCost, tt.wantCache)
			}
			if !floatEquals(result.TotalCost, tt.wantTotal, 1e-6) {
				t.Errorf("TotalCost = %v, want %v", result.TotalCost, tt.wantTotal)
			}
		})
	}
}

// TestDefaultPricingExact - Verify all model pricing ratios
func TestDefaultPricingExact(t *testing.T) {
	tests := []struct {
		model          string
		wantModelRatio float64
		wantCompletion float64
	}{
		{"claude-3-opus-20240229", 15.0, 5.0},
		{"claude-3-5-sonnet-20241022", 3.0, 5.0},
		{"claude-3-haiku-20240307", 0.25, 5.0},
		{"gpt-4o", 5.0, 3.0},
		{"gpt-4o-mini", 5.0, 3.0},
		{"gpt-4-turbo", 10.0, 3.0},
		{"gpt-4", 30.0, 2.0},
		{"gpt-3.5-turbo", 0.5, 3.0},
		{"deepseek-chat", 0.14, 2.0},
		{"deepseek-coder", 0.14, 2.0},
		{"qwen-turbo", 0.2, 2.0},
		{"unknown-model", 1.0, 3.0}, // default
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			pricing := DefaultPricing(tt.model)
			if pricing.ModelRatio != tt.wantModelRatio {
				t.Errorf("ModelRatio = %v, want %v", pricing.ModelRatio, tt.wantModelRatio)
			}
			if pricing.CompletionRatio != tt.wantCompletion {
				t.Errorf("CompletionRatio = %v, want %v", pricing.CompletionRatio, tt.wantCompletion)
			}
		})
	}
}

// TestCostBreakdownComponents - Verify cost breakdown sums to total
func TestCostBreakdownComponents(t *testing.T) {
	usage := &dto.Usage{
		PromptTokens:     5000,
		CompletionTokens: 2000,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         1000,
			CachedCreationTokens: 500,
		},
		CompletionTokenDetails: dto.OutputTokenDetails{
			ReasoningTokens: 300,
		},
	}
	pricing := &types.PriceData{
		ModelRatio:         3.0,
		CompletionRatio:    5.0,
		CacheRatio:         0.1,
		CacheCreationRatio: 0.25,
	}

	result := CalculateCost(usage, pricing)
	if result == nil {
		t.Fatal("CalculateCost returned nil")
	}

	// Verify components sum to total
	sum := result.InputCost + result.OutputCost + result.CacheReadCost + result.CacheWriteCost + result.ReasoningCost
	if !floatEquals(result.TotalCost, sum, 1e-10) {
		t.Errorf("TotalCost (%v) != sum of components (%v)", result.TotalCost, sum)
	}

	// Verify individual components
	effectiveInput := 5000 - 1000 // 4000
	wantInput := float64(effectiveInput) / 1e6 * 3.0
	if !floatEquals(result.InputCost, wantInput, 1e-10) {
		t.Errorf("InputCost = %v, want %v", result.InputCost, wantInput)
	}

	wantOutput := float64(2000) / 1e6 * 3.0 * 5.0
	if !floatEquals(result.OutputCost, wantOutput, 1e-10) {
		t.Errorf("OutputCost = %v, want %v", result.OutputCost, wantOutput)
	}

	wantCacheRead := float64(1000) / 1e6 * 3.0 * 0.1
	if !floatEquals(result.CacheReadCost, wantCacheRead, 1e-10) {
		t.Errorf("CacheReadCost = %v, want %v", result.CacheReadCost, wantCacheRead)
	}

	wantCacheWrite := float64(500) / 1e6 * 3.0 * 0.25
	if !floatEquals(result.CacheWriteCost, wantCacheWrite, 1e-10) {
		t.Errorf("CacheWriteCost = %v, want %v", result.CacheWriteCost, wantCacheWrite)
	}

	wantReasoning := float64(300) / 1e6 * 3.0
	if !floatEquals(result.ReasoningCost, wantReasoning, 1e-10) {
		t.Errorf("ReasoningCost = %v, want %v", result.ReasoningCost, wantReasoning)
	}
}

// TestCacheHitRate - Verify cache hit rate calculation
func TestCacheHitRate(t *testing.T) {
	tests := []struct {
		name     string
		usage    *dto.Usage
		wantRate float64
	}{
		{
			name:     "nil usage",
			usage:    nil,
			wantRate: 0,
		},
		{
			name:     "zero tokens",
			usage:    &dto.Usage{PromptTokens: 0},
			wantRate: 0,
		},
		{
			name: "no cache",
			usage: &dto.Usage{
				PromptTokens: 1000,
			},
			wantRate: 0,
		},
		{
			name: "50% cache hit via PromptTokensDetails",
			usage: &dto.Usage{
				PromptTokens: 1000,
				PromptTokensDetails: dto.InputTokenDetails{
					CachedTokens: 500,
				},
			},
			wantRate: 0.5,
		},
		{
			name: "30% cache hit via PromptCacheHitTokens",
			usage: &dto.Usage{
				PromptTokens:         1000,
				PromptCacheHitTokens: 300,
			},
			wantRate: 0.3,
		},
		{
			name: "100% cache hit",
			usage: &dto.Usage{
				PromptTokens: 1000,
				PromptTokensDetails: dto.InputTokenDetails{
					CachedTokens: 1000,
				},
			},
			wantRate: 1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rate := GetCacheHitRate(tt.usage)
			if !floatEquals(rate, tt.wantRate, 1e-10) {
				t.Errorf("GetCacheHitRate() = %v, want %v", rate, tt.wantRate)
			}
		})
	}
}

// TestCalculateQuotaExact - Verify quota calculation with exact values
func TestCalculateQuotaExact(t *testing.T) {
	usage := &dto.Usage{
		PromptTokens:     1000,
		CompletionTokens: 500,
	}
	pricing := &types.PriceData{
		ModelRatio:      1.0,
		CompletionRatio: 3.0,
	}

	quota := CalculateQuota(usage, pricing)

	// Cost = (1000/1e6 * 1.0) + (500/1e6 * 1.0 * 3.0) = 0.001 + 0.0015 = 0.0025
	// Quota = 0.0025 * 500000 = 1250
	expectedQuota := 1250
	if quota != expectedQuota {
		t.Errorf("CalculateQuota() = %v, want %v", quota, expectedQuota)
	}
}

// TestEdgeCases - Edge cases for billing
func TestEdgeCases(t *testing.T) {
	t.Run("zero tokens", func(t *testing.T) {
		usage := &dto.Usage{
			PromptTokens:     0,
			CompletionTokens: 0,
		}
		pricing := &types.PriceData{
			ModelRatio:      1.0,
			CompletionRatio: 3.0,
		}
		result := CalculateCost(usage, pricing)
		if result.TotalCost != 0 {
			t.Errorf("TotalCost = %v, want 0", result.TotalCost)
		}
	})

	t.Run("cached tokens exceed prompt tokens", func(t *testing.T) {
		usage := &dto.Usage{
			PromptTokens:     100,
			CompletionTokens: 50,
			PromptTokensDetails: dto.InputTokenDetails{
				CachedTokens: 200, // more than prompt tokens
			},
		}
		pricing := &types.PriceData{
			ModelRatio:      1.0,
			CompletionRatio: 3.0,
			CacheRatio:      0.1,
		}
		result := CalculateCost(usage, pricing)
		// effectiveInput should be 0 (not negative)
		if result.InputCost < 0 {
			t.Errorf("InputCost = %v, want >= 0", result.InputCost)
		}
	})

	t.Run("very large token count", func(t *testing.T) {
		usage := &dto.Usage{
			PromptTokens:     10000000, // 10M tokens
			CompletionTokens: 5000000,
		}
		pricing := &types.PriceData{
			ModelRatio:      5.0,
			CompletionRatio: 3.0,
		}
		result := CalculateCost(usage, pricing)
		// 10M/1M * 5.0 = 50.0
		// 5M/1M * 5.0 * 3.0 = 75.0
		// Total = 125.0
		if !floatEquals(result.TotalCost, 125.0, 0.01) {
			t.Errorf("TotalCost = %v, want ~125.0", result.TotalCost)
		}
	})
}

// TestPricingCache - Verify pricing cache works
func TestPricingCache(t *testing.T) {
	b := NewBilling()

	// First call should return non-nil default pricing.
	p1 := b.GetPricing("gpt-4o", "openai")
	if p1 == nil {
		t.Fatal("GetPricing returned nil")
	}

	// Second call should return equivalent default pricing.
	// Fallback pricing is intentionally NOT cached so that Portal sync can
	// replace it later; only SetPricing values are cached.
	p2 := b.GetPricing("gpt-4o", "openai")
	if p2 == nil || p2.ModelRatio != p1.ModelRatio {
		t.Error("GetPricing should return consistent default pricing")
	}

	// Different model should return different pricing
	p3 := b.GetPricing("claude-3-opus", "anthropic")
	if p1.ModelRatio == p3.ModelRatio {
		t.Error("Different models should have different pricing")
	}

	// Set custom pricing and verify it is cached
	customPricing := &types.PriceData{ModelRatio: 99.0}
	b.SetPricing("custom-model", "custom", customPricing)
	p4 := b.GetPricing("custom-model", "custom")
	if p4 != customPricing {
		t.Error("SetPricing should cache the provided pricing pointer")
	}
	if p4.ModelRatio != 99.0 {
		t.Errorf("Custom pricing ModelRatio = %v, want 99.0", p4.ModelRatio)
	}
}

// TestImageCost - Verify image generation cost
func TestImageCost(t *testing.T) {
	b := NewBilling()

	tests := []struct {
		name     string
		model    string
		size     string
		quality  string
		n        int
		expected float64
	}{
		{"dall-e-3 standard", "dall-e-3", "1024x1024", "standard", 1, 0.04},
		{"dall-e-3 hd", "dall-e-3", "1024x1024", "hd", 1, 0.08},
		{"dall-e-3 hd large", "dall-e-3", "1792x1024", "hd", 1, 0.12},
		{"dall-e-2 standard", "dall-e-2", "1024x1024", "standard", 1, 0.02},
		{"multiple", "dall-e-3", "1024x1024", "standard", 3, 0.12},
		{"n=0 becomes 1", "dall-e-3", "1024x1024", "standard", 0, 0.04},
		{"unknown model", "unknown", "1024x1024", "standard", 1, 0.04},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cost := b.CalculateImageCost(tt.model, tt.size, tt.quality, tt.n)
			if !floatEquals(cost, tt.expected, 1e-10) {
				t.Errorf("CalculateImageCost() = %v, want %v", cost, tt.expected)
			}
		})
	}
}

// floatEquals - Compare floats with epsilon
func floatEquals(a, b, epsilon float64) bool {
	return math.Abs(a-b) < epsilon
}

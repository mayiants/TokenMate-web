package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ai-proxy/ai-proxy-go/dto"
	"github.com/ai-proxy/ai-proxy-go/types"
)

func TestCalculateCost(t *testing.T) {
	tests := []struct {
		name         string
		usage        *dto.Usage
		pricing      *types.PriceData
		minTotalCost float64
		maxTotalCost float64
	}{
		{
			name:    "nil usage",
			usage:   nil,
			pricing: &types.PriceData{ModelRatio: 1.0},
		},
		{
			name:    "nil pricing",
			usage:   &dto.Usage{PromptTokens: 100, CompletionTokens: 50},
			pricing: nil,
		},
		{
			name:         "basic usage",
			usage:        &dto.Usage{PromptTokens: 1000, CompletionTokens: 500},
			pricing:      &types.PriceData{ModelRatio: 1.0, CompletionRatio: 3.0},
			minTotalCost: 0.001,
			maxTotalCost: 0.01,
		},
		{
			name: "usage with cache",
			usage: &dto.Usage{
				PromptTokens:     1000,
				CompletionTokens: 500,
				PromptTokensDetails: dto.InputTokenDetails{
					CachedTokens: 500,
				},
			},
			pricing:      &types.PriceData{ModelRatio: 1.0, CompletionRatio: 3.0, CacheRatio: 0.1},
			minTotalCost: 0.001,
			maxTotalCost: 0.01,
		},
		{
			name: "usage with cache creation",
			usage: &dto.Usage{
				PromptTokens:     1000,
				CompletionTokens: 500,
				PromptTokensDetails: dto.InputTokenDetails{
					CachedCreationTokens: 200,
				},
			},
			pricing:      &types.PriceData{ModelRatio: 1.0, CompletionRatio: 3.0, CacheCreationRatio: 0.25},
			minTotalCost: 0.001,
			maxTotalCost: 0.01,
		},
		{
			name: "claude cache tokens",
			usage: &dto.Usage{
				PromptTokens:                1000,
				CompletionTokens:            500,
				ClaudeCacheCreation5mTokens: 200,
				ClaudeCacheCreation1hTokens: 100,
			},
			pricing:      &types.PriceData{ModelRatio: 3.0, CompletionRatio: 5.0, CacheCreation5mRatio: 0.25, CacheCreation1hRatio: 0.30},
			minTotalCost: 0.001,
			maxTotalCost: 0.1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateCost(tt.usage, tt.pricing)
			if result == nil {
				if tt.usage != nil && tt.pricing != nil {
					t.Error("CalculateCost() returned nil for non-nil inputs")
				}
				return
			}

			if tt.minTotalCost > 0 && result.TotalCost < tt.minTotalCost {
				t.Errorf("CalculateCost() total = %v, want at least %v", result.TotalCost, tt.minTotalCost)
			}
			if tt.maxTotalCost > 0 && result.TotalCost > tt.maxTotalCost {
				t.Errorf("CalculateCost() total = %v, want at most %v", result.TotalCost, tt.maxTotalCost)
			}
		})
	}
}

func TestBilling_CalculateCost(t *testing.T) {
	b := NewBilling()

	tests := []struct {
		name     string
		usage    *dto.Usage
		model    string
		provider string
		wantCost bool
	}{
		{
			name:     "gpt-4 basic",
			usage:    &dto.Usage{PromptTokens: 1000, CompletionTokens: 500},
			model:    "gpt-4",
			provider: "openai",
			wantCost: true,
		},
		{
			name:     "claude-3-opus",
			usage:    &dto.Usage{PromptTokens: 1000, CompletionTokens: 500},
			model:    "claude-3-opus",
			provider: "anthropic",
			wantCost: true,
		},
		{
			name:     "unknown model",
			usage:    &dto.Usage{PromptTokens: 1000, CompletionTokens: 500},
			model:    "unknown-model",
			provider: "unknown",
			wantCost: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cost := b.CalculateCost(tt.usage, tt.model, tt.provider)
			if tt.wantCost && cost <= 0 {
				t.Errorf("CalculateCost() = %v, want > 0", cost)
			}
		})
	}
}

func TestBilling_CalculateImageCost(t *testing.T) {
	b := NewBilling()

	tests := []struct {
		name    string
		model   string
		size    string
		quality string
		n       int
		minCost float64
		maxCost float64
	}{
		{
			name:    "dall-e-3 standard 1024x1024",
			model:   "dall-e-3",
			size:    "1024x1024",
			quality: "standard",
			n:       1,
			minCost: 0.03,
			maxCost: 0.05,
		},
		{
			name:    "dall-e-3 hd 1024x1024",
			model:   "dall-e-3",
			size:    "1024x1024",
			quality: "hd",
			n:       1,
			minCost: 0.07,
			maxCost: 0.09,
		},
		{
			name:    "dall-e-3 hd 1792x1024",
			model:   "dall-e-3",
			size:    "1792x1024",
			quality: "hd",
			n:       1,
			minCost: 0.10,
			maxCost: 0.15,
		},
		{
			name:    "dall-e-2 1024x1024",
			model:   "dall-e-2",
			size:    "1024x1024",
			quality: "standard",
			n:       1,
			minCost: 0.01,
			maxCost: 0.03,
		},
		{
			name:    "multiple images",
			model:   "dall-e-3",
			size:    "1024x1024",
			quality: "standard",
			n:       3,
			minCost: 0.10,
			maxCost: 0.15,
		},
		{
			name:    "default n=0 becomes 1",
			model:   "dall-e-3",
			size:    "1024x1024",
			quality: "standard",
			n:       0,
			minCost: 0.03,
			maxCost: 0.05,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cost := b.CalculateImageCost(tt.model, tt.size, tt.quality, tt.n)
			if cost < tt.minCost || cost > tt.maxCost {
				t.Errorf("CalculateImageCost() = %v, want between %v and %v", cost, tt.minCost, tt.maxCost)
			}
		})
	}
}

func TestGetCacheHitRate(t *testing.T) {
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
			name:     "zero prompt tokens",
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
			name: "50% cache hit",
			usage: &dto.Usage{
				PromptTokens: 1000,
				PromptTokensDetails: dto.InputTokenDetails{
					CachedTokens: 500,
				},
			},
			wantRate: 0.5,
		},
		{
			name: "cache hit via PromptCacheHitTokens",
			usage: &dto.Usage{
				PromptTokens:         1000,
				PromptCacheHitTokens: 300,
			},
			wantRate: 0.3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rate := GetCacheHitRate(tt.usage)
			if rate != tt.wantRate {
				t.Errorf("GetCacheHitRate() = %v, want %v", rate, tt.wantRate)
			}
		})
	}
}

func TestDefaultPricing(t *testing.T) {
	tests := []struct {
		name           string
		model          string
		wantModelRatio float64
		wantCompletion float64
	}{
		{"claude-opus", "claude-3-opus-20240229", 15.0, 5.0},
		{"claude-sonnet", "claude-3-sonnet-20240229", 3.0, 5.0},
		{"claude-haiku", "claude-3-haiku-20240307", 0.25, 5.0},
		{"gpt-4o", "gpt-4o", 5.0, 3.0},
		{"gpt-4-turbo", "gpt-4-turbo", 10.0, 3.0},
		{"gpt-4", "gpt-4", 30.0, 2.0},
		{"gpt-3.5", "gpt-3.5-turbo", 0.5, 3.0},
		{"deepseek", "deepseek-chat", 0.14, 2.0},
		{"qwen", "qwen-turbo", 0.2, 2.0},
		{"unknown", "unknown-model", 1.0, 3.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pricing := DefaultPricing(tt.model)
			if pricing.ModelRatio != tt.wantModelRatio {
				t.Errorf("DefaultPricing(%v).ModelRatio = %v, want %v", tt.model, pricing.ModelRatio, tt.wantModelRatio)
			}
			if pricing.CompletionRatio != tt.wantCompletion {
				t.Errorf("DefaultPricing(%v).CompletionRatio = %v, want %v", tt.model, pricing.CompletionRatio, tt.wantCompletion)
			}
		})
	}
}

func TestCalculateQuota(t *testing.T) {
	usage := &dto.Usage{
		PromptTokens:     1000,
		CompletionTokens: 500,
	}
	pricing := &types.PriceData{
		ModelRatio:      1.0,
		CompletionRatio: 3.0,
	}

	quota := CalculateQuota(usage, pricing)
	if quota <= 0 {
		t.Errorf("CalculateQuota() = %v, want > 0", quota)
	}
}

func TestClassifyProviderError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		transport  error
		want       ProviderErrorType
	}{
		{"success 200", 200, nil, ProviderErrorNone},
		{"success 204", 204, nil, ProviderErrorNone},
		{"rate limit 429", 429, nil, ProviderErrorRateLimit},
		{"auth 401", 401, nil, ProviderErrorAuth},
		{"auth 403", 403, nil, ProviderErrorAuth},
		{"invalid request 400", 400, nil, ProviderErrorInvalidRequest},
		{"client 404", 404, nil, ProviderError4xx},
		{"server 500", 500, nil, ProviderError5xx},
		{"server 502", 502, nil, ProviderError5xx},
		{"network timeout", 0, context.DeadlineExceeded, ProviderErrorTimeout},
		{"network error", 0, &net.OpError{Op: "dial", Err: fmt.Errorf("connection refused")}, ProviderErrorNetwork},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyProviderError(tt.statusCode, tt.transport, "")
			if got != tt.want {
				t.Errorf("ClassifyProviderError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidProviderErrorType(t *testing.T) {
	if !ValidProviderErrorType("rate_limit") {
		t.Error("expected rate_limit to be valid")
	}
	if ValidProviderErrorType("unknown_error") {
		t.Error("expected unknown_error to be invalid")
	}
}

func TestSyncPricingFromPortal_AbsolutePrices(t *testing.T) {
	secret := "test-secret"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-proxy-secret") != secret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(PortalPricingSyncResponse{
			Pricing: []PortalPricing{
				{
					Provider:             "openai",
					Region:               "us",
					Model:                "gpt-test",
					InputPricePer1M:      5.0,
					OutputPricePer1M:     15.0,
					CacheHitPricePer1M:   0.5,
					CacheWritePricePer1M: 1.25,
					ReasoningPricePer1M:  5.0,
					FixedPrice:           0.001,
					BillingMode:          "flat",
				},
			},
		})
	}))
	defer srv.Close()

	b := NewBillingWithPortal(srv.URL, secret, time.Hour)
	if err := b.SyncPricingFromPortal(context.Background()); err != nil {
		t.Fatalf("sync pricing: %v", err)
	}

	p := b.GetPricing("gpt-test", "openai")
	if p == nil {
		t.Fatal("expected pricing")
	}
	if p.InputPricePer1M != 5.0 {
		t.Errorf("input = %v, want 5.0", p.InputPricePer1M)
	}
	if p.OutputPricePer1M != 15.0 {
		t.Errorf("output = %v, want 15.0", p.OutputPricePer1M)
	}
	if p.ModelRatio != 5.0 {
		t.Errorf("modelRatio = %v, want 5.0", p.ModelRatio)
	}
	if p.CompletionRatio != 3.0 {
		t.Errorf("completionRatio = %v, want 3.0", p.CompletionRatio)
	}
	if p.FixedPrice != 0.001 {
		t.Errorf("fixedPrice = %v, want 0.001", p.FixedPrice)
	}

	usage := &dto.Usage{PromptTokens: 1000, CompletionTokens: 500}
	cost := b.CalculateCost(usage, "gpt-test", "openai")
	if cost <= 0 {
		t.Errorf("expected positive cost, got %v", cost)
	}
}

func TestGetPricing_CacheTTL(t *testing.T) {
	b := NewBilling()
	b.SetPricing("ttl-model", "openai", &types.PriceData{
		InputPricePer1M:  9.0,
		OutputPricePer1M: 9.0,
	})

	// Immediately after set, cache is valid.
	p := b.GetPricing("ttl-model", "openai")
	if p == nil || p.InputPricePer1M != 9.0 {
		t.Fatalf("expected cached pricing 9.0, got %v", p)
	}

	// Manually expire the entry by rewinding its timestamp.
	b.syncMutex.Lock()
	entry := b.pricingCache["openai:ttl-model"]
	entry.at = time.Now().Add(-2 * pricingCacheTTL)
	b.pricingCache["openai:ttl-model"] = entry
	b.syncMutex.Unlock()

	// Expired entries should fall back to default pricing, not the cached 9.0.
	p = b.GetPricing("ttl-model", "openai")
	if p == nil || p.InputPricePer1M == 9.0 {
		t.Fatalf("expected fallback pricing after expiry, got %v", p)
	}
}

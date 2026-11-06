package services

import (
	"testing"
	"time"
)

func i64(v int64) *int64 { return &v }

// TestCalculateTieredComponentCost pins the tiered billing arithmetic with
// absolute expected values (the existing batch-vs-single test is
// self-referential and cannot catch a wrong formula).
func TestCalculateTieredComponentCost(t *testing.T) {
	tier1 := TokenTier{MaxTokens: i64(1_000_000), Prices: ComponentPrices{Input: 2.0, Output: 4.0}}
	tier2 := TokenTier{MaxTokens: nil, Prices: ComponentPrices{Input: 1.0, Output: 2.0}}

	tests := []struct {
		name      string
		tokens    int
		tiers     []TokenTier
		component string
		want      float64
	}{
		{"no tiers", 1_000_000, nil, "input", 0},
		{"zero tokens", 0, []TokenTier{tier1, tier2}, "input", 0},
		{"negative tokens", -5, []TokenTier{tier1, tier2}, "input", 0},
		{"single unlimited tier", 2_000_000, []TokenTier{tier2}, "input", 2.0},
		// Exactly at the boundary: everything bills at the first tier.
		{"exact boundary", 1_000_000, []TokenTier{tier1, tier2}, "input", 2.0},
		// 1M @ 2.0 + 1M @ 1.0 = 3.0
		{"crosses boundary", 2_000_000, []TokenTier{tier1, tier2}, "input", 3.0},
		// Output component uses its own price: 1M @ 4.0 + 500k @ 2.0 = 5.0
		{"output component", 1_500_000, []TokenTier{tier1, tier2}, "output", 5.0},
		// Unsorted tier definitions must be sorted (unlimited last).
		{"unsorted tiers", 2_000_000, []TokenTier{tier2, tier1}, "input", 3.0},
		{"unknown component", 1_000_000, []TokenTier{tier1}, "bogus", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateTieredComponentCost(tt.tokens, tt.tiers, tt.component)
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// TestResolveTimeBasedPrices covers day/window matching, cross-midnight
// windows, boundary inclusivity and the base-price fallback.
func TestResolveTimeBasedPrices(t *testing.T) {
	s := &PricingService{}
	base := ComponentPrices{Input: 1.0}
	night := ComponentPrices{Input: 0.5}

	rule := &PricingRule{
		Prices:   base,
		Timezone: "UTC",
		TimeRules: []TimeRule{
			{Days: []int{1, 2, 3, 4, 5}, Start: "22:00", End: "08:00", Prices: night}, // weekday nights, crosses midnight
		},
	}

	at := func(day time.Weekday, hh, mm int) time.Time {
		// 2026-07-19 is a Sunday; add days to reach the wanted weekday.
		d := time.Date(2026, 7, 19, hh, mm, 0, 0, time.UTC).AddDate(0, 0, int(day))
		return d
	}

	tests := []struct {
		name string
		when time.Time
		want float64
	}{
		{"weekday night in window", at(time.Monday, 23, 30), 0.5},
		{"weekday early morning in window", at(time.Tuesday, 7, 59), 0.5},
		{"window start inclusive", at(time.Monday, 22, 0), 0.5},
		{"window end exclusive", at(time.Tuesday, 8, 0), 1.0},
		{"weekday daytime", at(time.Wednesday, 12, 0), 1.0},
		{"weekend night excluded by days", at(time.Saturday, 23, 30), 1.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.resolveTimeBasedPrices(rule, tt.when)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Input != tt.want {
				t.Errorf("price = %v, want %v", got.Input, tt.want)
			}
		})
	}

	// Invalid timezone falls back to UTC instead of erroring.
	got, err := s.resolveTimeBasedPrices(&PricingRule{
		Prices: base, Timezone: "Not/AZone",
		TimeRules: []TimeRule{{Days: []int{1}, Start: "00:00", End: "23:59", Prices: night}},
	}, at(time.Monday, 12, 0))
	if err != nil || got.Input != 0.5 {
		t.Errorf("invalid timezone: got %v, err %v; want 0.5, nil", got.Input, err)
	}
}

// TestMatchScore pins the exact-match scoring used to pick between candidate
// rules (exact provider/region/model +100 each, priority x10).
func TestMatchScore(t *testing.T) {
	rule := &PricingRule{Provider: "openai", Region: "us", ProductID: "gpt-4o", Priority: 3}
	if got := matchScore(rule, "openai", "us", "gpt-4o"); got != 330 {
		t.Errorf("full match: got %d, want 330", got)
	}
	if got := matchScore(rule, "openai", "*", "gpt-4o"); got != 230 {
		t.Errorf("no region match: got %d, want 230", got)
	}
	wildcard := &PricingRule{Provider: "*", Region: "*", Priority: 0}
	if got := matchScore(wildcard, "openai", "us", "gpt-4o"); got != 0 {
		t.Errorf("wildcard rule: got %d, want 0", got)
	}
	// APIModelName match counts the same as ProductID.
	alias := &PricingRule{Provider: "openai", Region: "*", APIModelName: "gpt-4o"}
	if got := matchScore(alias, "openai", "us", "gpt-4o"); got != 200 {
		t.Errorf("api-model-name match: got %d, want 200", got)
	}
}

// TestCalculateTieredCost_Absolute asserts absolute per-component costs and
// the per-request fixed fee on the full tiered path.
func TestCalculateTieredCost_Absolute(t *testing.T) {
	s := &PricingService{}
	tiers := []TokenTier{
		{MaxTokens: i64(1_000_000), Prices: ComponentPrices{Input: 2.0, CacheHit: 0.2}},
		{MaxTokens: nil, Prices: ComponentPrices{Input: 1.0, CacheHit: 0.1}},
	}
	prices := ComponentPrices{FixedRequest: 0.5}

	rec := UsageRecord{
		InputTokens:          2_000_000,
		CachedTokens:         500_000,
		OutputTokens:         0,
		RequestCount:         2,
		EffectiveInputTokens: 1_500_000,
	}
	got := s.calculateTieredCost(rec, tiers, prices)

	// effective input 1.5M: 1M @ 2.0 + 0.5M @ 1.0 = 2.5
	if got.Input != 2.5 {
		t.Errorf("input = %v, want 2.5", got.Input)
	}
	// cached 0.5M @ 0.2 (first tier) = 0.1
	if got.CacheHit != 0.1 {
		t.Errorf("cache hit = %v, want 0.1", got.CacheHit)
	}
	// fixed fee per request: 0.5 * 2 = 1.0
	if got.Fixed != 1.0 {
		t.Errorf("fixed = %v, want 1.0", got.Fixed)
	}

	// RequestCount 0 still charges one fixed fee.
	rec.RequestCount = 0
	if got := s.calculateTieredCost(rec, tiers, prices); got.Fixed != 0.5 {
		t.Errorf("fixed with zero request count = %v, want 0.5", got.Fixed)
	}
}

// TestSelectContextTier pins tier selection for tiered_context billing:
// first tier whose max_tokens covers the total length wins; nil max_tokens
// is the catch-all; empty tiers or no coverage returns nil (flat fallback).
func TestSelectContextTier(t *testing.T) {
	tiers := []TokenTier{
		{MaxTokens: i64(204_800), Prices: ComponentPrices{Input: 2.0, Output: 6.0}},
		{MaxTokens: nil, Prices: ComponentPrices{Input: 4.0, Output: 12.0}},
	}
	tests := []struct {
		name      string
		total     int64
		tiers     []TokenTier
		wantInput float64 // 0 means want nil
	}{
		{"empty tiers", 100, nil, 0},
		{"below threshold", 100_000, tiers, 2.0},
		{"exact boundary bills low tier", 204_800, tiers, 2.0},
		{"one over boundary bills high tier", 204_801, tiers, 4.0},
		{"catch-all for far over", 10_000_000, tiers, 4.0},
		{"no catch-all and over last max falls back", 10_000_000, tiers[:1], 0},
		{"unsorted tiers still resolve", 100_000, []TokenTier{tiers[1], tiers[0]}, 2.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := selectContextTier(tt.total, tt.tiers)
			if tt.wantInput == 0 {
				if got != nil {
					t.Errorf("selectContextTier(%d) = %+v, want nil", tt.total, got)
				}
				return
			}
			if got == nil || got.Prices.Input != tt.wantInput {
				t.Errorf("selectContextTier(%d) = %+v, want input price %v", tt.total, got, tt.wantInput)
			}
		})
	}
}

// TestCalculateContextTierCost_Absolute verifies whole-request tier pricing:
// every component bills at the selected tier's prices (laozhang tiered_expr
// semantics), not marginal brackets.
func TestCalculateContextTierCost_Absolute(t *testing.T) {
	s := &PricingService{}
	tiers := []TokenTier{
		{MaxTokens: i64(272_000), Prices: ComponentPrices{Input: 10.0, Output: 50.0, CacheHit: 1.0, CacheWrite: 12.5, FixedRequest: 0.005}},
		{MaxTokens: nil, Prices: ComponentPrices{Input: 20.0, Output: 75.0, CacheHit: 2.0, CacheWrite: 25.0, FixedRequest: 0.005}},
	}
	base := ComponentPrices{Input: 1.0, Output: 1.0, FixedRequest: 0.005}

	// Below threshold: 200K in (100K cached) + 50K out, all at low tier.
	low := UsageRecord{
		InputTokens: 200_000, CachedTokens: 100_000, OutputTokens: 50_000,
		RequestCount: 1, EffectiveInputTokens: 100_000,
	}
	got := s.calculateContextTierCost(low, tiers, base)
	// input: 100K eff @ 10.0 = 1.0; output: 50K @ 50.0 = 2.5;
	// cache hit: 100K @ 1.0 = 0.1; fixed: 0.005
	if got.Input != 1.0 || got.Output != 2.5 || got.CacheHit != 0.1 || got.Fixed != 0.005 {
		t.Errorf("low tier costs = %+v, want input 1.0 output 2.5 cache 0.1 fixed 0.005", *got)
	}

	// Over threshold: ALL tokens (including the 272K base segment) bill high.
	high := UsageRecord{
		InputTokens: 300_000, CachedTokens: 0, OutputTokens: 10_000,
		RequestCount: 1, EffectiveInputTokens: 300_000,
	}
	got = s.calculateContextTierCost(high, tiers, base)
	// input: 300K @ 20.0 = 6.0; output: 10K @ 75.0 = 0.75
	if got.Input != 6.0 || got.Output != 0.75 {
		t.Errorf("high tier costs = %+v, want input 6.0 output 0.75", *got)
	}

	// Empty tiers fall back to base flat prices.
	got = s.calculateContextTierCost(low, nil, base)
	if got.Input != 0.1 || got.Output != 0.05 { // 100K @ 1.0, 50K @ 1.0
		t.Errorf("fallback flat costs = %+v, want input 0.1 output 0.05", *got)
	}
}

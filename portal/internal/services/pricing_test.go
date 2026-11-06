package services

import (
	"bytes"
	"context"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

func TestUSDtoQuota(t *testing.T) {
	cases := []struct {
		usd  float64
		want int64
	}{
		{0, 0},
		{1, 1_000_000},
		{1.5, 1_500_000},
		{0.000001, 1},
	}
	for _, tc := range cases {
		got := USDtoQuota(tc.usd)
		if got != tc.want {
			t.Errorf("USDtoQuota(%v) = %d, want %d", tc.usd, got, tc.want)
		}
	}
}

func TestQuotaToUSD(t *testing.T) {
	if got := QuotaToUSD(1_500_000); got != "$1.500000" {
		t.Errorf("QuotaToUSD(1500000) = %q, want $1.500000", got)
	}
}

func TestQuotaToUSDFloat(t *testing.T) {
	if got := QuotaToUSDFloat(1_500_000); math.Abs(got-1.5) > 1e-9 {
		t.Errorf("QuotaToUSDFloat(1500000) = %v, want 1.5", got)
	}
}

func TestRMBYuanToQuota(t *testing.T) {
	// 10 yuan / 7.20 = 1.388... USD → 1_388_888 micro-USD after int64 truncation
	got := RMBYuanToQuota(10.0, 7.20)
	want := int64(1_388_888)
	if got != want {
		t.Errorf("RMBYuanToQuota(10, 7.20) = %d, want %d", got, want)
	}
}

func TestFormatQuota(t *testing.T) {
	if got := FormatQuota(1_500_000, "USD", 7.20); got != "$1.50" {
		t.Errorf("FormatQuota USD = %q, want $1.50", got)
	}
	if got := FormatQuota(1_500_000, "CNY", 7.20); got != "¥10.80" {
		t.Errorf("FormatQuota CNY = %q, want ¥10.80", got)
	}
}

func TestPricingService_ExportImportCSV(t *testing.T) {
	d := testBillingDB(t)
	defer d.Close()

	ctx := context.Background()
	svc := NewPricingService(d, zap.NewNop())
	model := "csv-model-" + uuid.NewString()

	_, err := svc.CreateRule(ctx, &PricingRule{
		Provider:     "openai",
		Region:       "us",
		APIModelName: model,
		BillingMode:  "flat",
		Prices:       ComponentPrices{Input: 1.0, Output: 2.0, CacheHit: 0.1, CacheWrite: 0.25, Reasoning: 1.5},
		Currency:     "USD",
		Timezone:     "UTC",
		Priority:     10,
		Status:       "active",
		RuleName:     "csv-test",
	})
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}

	csvData, err := svc.ExportRulesToCSV(ctx, "openai", "us", model, "", "active")
	if err != nil {
		t.Fatalf("export csv: %v", err)
	}
	if len(csvData) == 0 {
		t.Fatal("expected non-empty CSV")
	}
	if !bytes.Contains(csvData, []byte(model)) {
		t.Fatalf("exported CSV missing model: %s", string(csvData))
	}

	created, updated, failed, _, err := svc.ImportRulesFromCSV(ctx, csvData)
	if err != nil {
		t.Fatalf("import csv: %v", err)
	}
	if failed != 0 {
		t.Fatalf("import failed count = %d", failed)
	}
	if created+updated != 1 {
		t.Fatalf("expected 1 row imported, created=%d updated=%d", created, updated)
	}

	rules, err := svc.ListRules(ctx, "openai", "us", model, "flat", "active")
	if err != nil {
		t.Fatalf("list rules: %v", err)
	}
	if len(rules) < 1 {
		t.Fatalf("expected at least 1 rule after import, got %d", len(rules))
	}
}

func TestPricingService_CalculateCostBatch(t *testing.T) {
	d := testBillingDB(t)
	defer d.Close()

	ctx := context.Background()
	svc := NewPricingService(d, zap.NewNop())
	provider := "openai"
	model := "batch-model-" + uuid.NewString()

	insertPricingRule(t, d, &PricingRule{
		Provider:     provider,
		Region:       "*",
		APIModelName: model,
		BillingMode:  "flat",
		Prices: ComponentPrices{
			Input:      1.0,
			Output:     2.0,
			CacheHit:   0.5,
			CacheWrite: 1.0,
			Reasoning:  0.5,
		},
		Status:        "active",
		RuleName:      "batch test",
		EffectiveFrom: time.Now().Add(-time.Hour),
	})

	now := time.Now().Format(time.RFC3339)
	records := []UsageRecord{
		{Provider: provider, Region: "us", Model: model, InputTokens: 1_000_000, OutputTokens: 500_000, Timestamp: now},
		{Provider: provider, Region: "us", Model: model, InputTokens: 2_000_000, OutputTokens: 1_000_000, Timestamp: now},
		{Provider: provider, Region: "eu", Model: model, InputTokens: 500_000, OutputTokens: 250_000, Timestamp: now},
	}

	want := make([]*ComponentCosts, len(records))
	for i, r := range records {
		c, err := svc.CalculateCost(ctx, r)
		if err != nil {
			t.Fatalf("CalculateCost index %d: %v", i, err)
		}
		want[i] = c
	}

	got, err := svc.CalculateCostBatch(ctx, records)
	if err != nil {
		t.Fatalf("CalculateCostBatch: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d results, got %d", len(want), len(got))
	}
	for i := range want {
		if math.Abs(got[i].Total()-want[i].Total()) > 1e-9 {
			t.Errorf("index %d: batch total %v != single total %v", i, got[i].Total(), want[i].Total())
		}
	}
}

func TestPricingService_SyncViewForProxy_WorstCase(t *testing.T) {
	d := testBillingDB(t)
	defer d.Close()

	ctx := context.Background()
	svc := NewPricingService(d, zap.NewNop())
	model := "proxy-worst-" + uuid.NewString()

	max := int64(1000)
	_, err := svc.CreateRule(ctx, &PricingRule{
		Provider:     "aws",
		Region:       "*",
		APIModelName: model,
		BillingMode:  "tiered",
		Tiers: []TokenTier{
			{MaxTokens: &max, Prices: ComponentPrices{Input: 1.0, Output: 2.0, CacheHit: 0.1, CacheWrite: 0.25, Reasoning: 1.0}},
			{Prices: ComponentPrices{Input: 0.5, Output: 5.0, CacheHit: 0.2, CacheWrite: 0.5, Reasoning: 2.0}},
		},
		Currency: "USD",
		Timezone: "UTC",
		Status:   "active",
	})
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}

	views, err := svc.SyncViewForProxy(ctx)
	if err != nil {
		t.Fatalf("sync view: %v", err)
	}

	for _, v := range views {
		if v.Provider == "aws" && v.Model == model {
			if v.InputPricePer1M != 1.0 {
				t.Errorf("input worst-case = %v, want 1.0", v.InputPricePer1M)
			}
			if v.OutputPricePer1M != 5.0 {
				t.Errorf("output worst-case = %v, want 5.0", v.OutputPricePer1M)
			}
			if v.ReasoningPricePer1M != 2.0 {
				t.Errorf("reasoning worst-case = %v, want 2.0", v.ReasoningPricePer1M)
			}
			return
		}
	}
	t.Fatalf("expected view for aws/%s not found", model)
}

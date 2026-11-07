package services

import (
	"context"
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/internal/testutil"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

func TestMain(m *testing.M) {
	testutil.LoadEnv()
	ctx := context.Background()
	if err := db.InitTestSchema(ctx, testutil.PostgresDSN(), zap.NewNop()); err != nil {
		fmt.Fprintf(os.Stderr, "init schema in services TestMain: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

func testBillingDB(t *testing.T) *db.DB {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), testutil.PostgresDSN())
	if err != nil {
		t.Fatalf("connect to test db: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("ping test db: %v", err)
	}
	return &db.DB{Pool: pool, Redis: nil}
}

func insertPricingRule(t *testing.T, d *db.DB, rule *PricingRule) string {
	t.Helper()
	ctx := context.Background()
	ps, ts, tr, err := marshalPricingRuleJSON(rule)
	if err != nil {
		t.Fatalf("marshal pricing rule: %v", err)
	}
	var id string
	err = d.Pool.QueryRow(ctx,
		`INSERT INTO model_pricing_rules
		 (provider, region, product_id, api_model_name, billing_mode,
		  prices, tiers, time_rules, timezone, currency,
		  effective_from, effective_to, priority, status, rule_name, note)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		 RETURNING id::text`,
		rule.Provider, rule.Region, strPtr(rule.ProductID), strPtr(rule.APIModelName), rule.BillingMode,
		ps, ts, tr, rule.Timezone, rule.Currency,
		rule.EffectiveFrom, rule.EffectiveTo, rule.Priority, rule.Status, rule.RuleName, rule.Note,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert pricing rule: %v", err)
	}
	return id
}

func TestBillingService_CalculateCost_PerToken(t *testing.T) {
	d := testBillingDB(t)
	defer d.Close()

	ctx := context.Background()
	provider := "openai"
	model := "gpt-test-" + fmt.Sprintf("%d", time.Now().UnixNano())

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
		RuleName:      "test per token",
		EffectiveFrom: time.Now().Add(-time.Hour),
	})

	svc := NewBillingService(d, zap.NewNop())
	record := UsageRecord{
		Provider:             provider,
		Model:                model,
		EffectiveInputTokens: 1_000_000,
		OutputTokens:         500_000,
	}
	got := svc.CalculateCost(ctx, &record)

	// input: 1M * 1.0 = $1.0, output: 500k * 2.0 = $1.0 → total $2.0 exact
	want := int64(2_000_000)
	if got != want {
		t.Errorf("CalculateCost = %d, want %d", got, want)
	}
}

func TestBillingService_CalculateCost_PerRequest(t *testing.T) {
	d := testBillingDB(t)
	defer d.Close()

	ctx := context.Background()
	provider := "openai"
	model := "gpt-fixed-" + fmt.Sprintf("%d", time.Now().UnixNano())

	insertPricingRule(t, d, &PricingRule{
		Provider:     provider,
		Region:       "*",
		APIModelName: model,
		BillingMode:  "flat",
		Prices: ComponentPrices{
			FixedRequest: 0.25,
		},
		Status:        "active",
		RuleName:      "test per request",
		EffectiveFrom: time.Now().Add(-time.Hour),
	})

	svc := NewBillingService(d, zap.NewNop())
	got := svc.CalculateCost(ctx, &UsageRecord{Provider: provider, Model: model, InputTokens: 1_000_000})
	want := USDtoQuota(0.25)
	if got != want {
		t.Errorf("CalculateCost = %d, want %d", got, want)
	}
}

func TestBillingService_GetPricingRatioBatch(t *testing.T) {
	d := testBillingDB(t)
	defer d.Close()

	ctx := context.Background()
	groupA := uuid.New().String()
	groupB := uuid.New().String()

	_, _ = d.Pool.Exec(ctx, "DELETE FROM user_groups WHERE id = ANY($1::uuid[])", []string{groupA, groupB})
	_, err := d.Pool.Exec(ctx,
		`INSERT INTO user_groups (id, name, pricing_ratio, status, created_at, updated_at)
		 VALUES ($1::uuid, 'group-a', 0.85, 'active', NOW(), NOW()),
		        ($2::uuid, 'group-b', 1.25, 'active', NOW(), NOW())`,
		groupA, groupB,
	)
	if err != nil {
		t.Fatalf("insert user groups: %v", err)
	}
	defer func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM user_groups WHERE id = ANY($1::uuid[])", []string{groupA, groupB})
	}()

	svc := NewBillingService(d, zap.NewNop())

	got := svc.GetPricingRatioBatch(ctx, []string{groupA, groupB, "", uuid.New().String()})
	if got[groupA] != 0.85 {
		t.Errorf("groupA ratio = %v, want 0.85", got[groupA])
	}
	if got[groupB] != 1.25 {
		t.Errorf("groupB ratio = %v, want 1.25", got[groupB])
	}
	if got[""] != 1.0 {
		t.Errorf("empty group ratio = %v, want 1.0", got[""])
	}
	if got[groupA] != svc.GetPricingRatio(ctx, groupA) {
		t.Errorf("batch result differs from single lookup")
	}
}

func TestBillingService_GetPricingRatioBatch_FollowsParent(t *testing.T) {
	d := testBillingDB(t)
	defer d.Close()

	ctx := context.Background()
	parent := uuid.New().String()
	follow := uuid.New().String()

	_, _ = d.Pool.Exec(ctx, "DELETE FROM user_groups WHERE id = ANY($1::uuid[])", []string{parent, follow})
	_, err := d.Pool.Exec(ctx,
		`INSERT INTO user_groups (id, name, pricing_ratio, sync_mode, status, created_at, updated_at)
		 VALUES ($1::uuid, 'platform-parent', 1.015, 'manual', 'active', NOW(), NOW()),
		        ($2::uuid, 'enterprise-follow', 1.01, 'follow', 'active', NOW(), NOW())`,
		parent, follow,
	)
	if err != nil {
		t.Fatalf("insert user groups: %v", err)
	}
	_, err = d.Pool.Exec(ctx,
		`UPDATE user_groups SET parent_group_id = $1::uuid WHERE id = $2::uuid`, parent, follow)
	if err != nil {
		t.Fatalf("set parent_group_id: %v", err)
	}
	defer func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM user_groups WHERE id = ANY($1::uuid[])", []string{parent, follow})
	}()

	svc := NewBillingService(d, zap.NewNop())

	// Ratio is multiplicative along the chain: 1.01 × 1.015.
	if got := svc.GetPricingRatio(ctx, follow); math.Abs(got-1.02515) > 1e-9 {
		t.Errorf("follow-group ratio = %v, want 1.02515 (1.01 × 1.015)", got)
	}
	if got := svc.GetPricingRatio(ctx, parent); got != 1.015 {
		t.Errorf("parent ratio = %v, want 1.015", got)
	}
}

func TestBillingService_GetPricingRatio(t *testing.T) {
	d := testBillingDB(t)
	defer d.Close()

	ctx := context.Background()
	groupID := uuid.New().String()

	_, _ = d.Pool.Exec(ctx, "DELETE FROM user_groups WHERE id = $1::uuid", groupID)
	_, err := d.Pool.Exec(ctx,
		`INSERT INTO user_groups (id, name, pricing_ratio, status, created_at, updated_at)
		 VALUES ($1::uuid, 'test-group', 0.85, 'active', NOW(), NOW())`,
		groupID,
	)
	if err != nil {
		t.Fatalf("insert user group: %v", err)
	}
	defer func() { _, _ = d.Pool.Exec(ctx, "DELETE FROM user_groups WHERE id = $1::uuid", groupID) }()

	svc := NewBillingService(d, zap.NewNop())
	if got := svc.GetPricingRatio(ctx, groupID); got != 0.85 {
		t.Errorf("GetPricingRatio = %v, want 0.85", got)
	}
	if got := svc.GetPricingRatio(ctx, ""); got != 1.0 {
		t.Errorf("GetPricingRatio empty = %v, want 1.0", got)
	}
	if got := svc.GetPricingRatio(ctx, uuid.New().String()); got != 1.0 {
		t.Errorf("GetPricingRatio missing = %v, want 1.0", got)
	}
}

func TestPricingService_MatchRule_NormalizedModelName(t *testing.T) {
	d := testBillingDB(t)
	defer d.Close()

	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	provider := "matchnorm-" + suffix

	// Same-provider rule registered with dash-style name (Bedrock profile
	// style); clients request the dot-style name (upstream catalog style).
	dashID := insertPricingRule(t, d, &PricingRule{
		Provider:      provider,
		Region:        "*",
		APIModelName:  "claude-opus-4-6",
		BillingMode:   "flat",
		Prices:        ComponentPrices{Input: 15, Output: 75},
		Status:        "active",
		RuleName:      "dash-name rule",
		EffectiveFrom: time.Now().Add(-time.Hour),
	})
	// Exact dot-name rule for a DIFFERENT provider must not win.
	otherID := insertPricingRule(t, d, &PricingRule{
		Provider:      "other-" + suffix,
		Region:        "*",
		APIModelName:  "claude-opus-4.6",
		BillingMode:   "flat",
		Prices:        ComponentPrices{Input: 5, Output: 25},
		Status:        "active",
		RuleName:      "other-provider dot rule",
		EffectiveFrom: time.Now().Add(-time.Hour),
	})
	// Nameless global fallback.
	globalID := insertPricingRule(t, d, &PricingRule{
		Provider:      "*",
		Region:        "*",
		BillingMode:   "flat",
		Prices:        ComponentPrices{Input: 1, Output: 3},
		Status:        "active",
		RuleName:      "global fallback",
		EffectiveFrom: time.Now().Add(-time.Hour),
	})
	defer func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM model_pricing_rules WHERE provider IN ($1,$2,'*') AND rule_name LIKE '%' || $3", provider, "other-"+suffix, suffix)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM model_pricing_rules WHERE provider='*' AND rule_name LIKE '%' || $1", suffix)
	}()

	svc := NewPricingService(d, zap.NewNop())
	now := time.Now()

	rule, err := svc.MatchRule(ctx, provider, "*", "claude-opus-4.6", now)
	if err != nil {
		t.Fatalf("MatchRule dot-name: %v", err)
	}
	if rule == nil || rule.ID != dashID {
		t.Errorf("MatchRule dot-name = %+v, want same-provider dash rule %s (other=%s global=%s)", rule, dashID, otherID, globalID)
	}

	rule, err = svc.MatchRule(ctx, provider, "*", "claude-opus-4-6", now)
	if err != nil {
		t.Fatalf("MatchRule dash-name: %v", err)
	}
	if rule == nil || rule.ID != dashID {
		t.Errorf("MatchRule dash-name = %+v, want exact dash rule %s", rule, dashID)
	}
}

func TestPricingService_MatchRule_ExactNameBeatsNormalized(t *testing.T) {
	d := testBillingDB(t)
	defer d.Close()

	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	provider := "matchexact-" + suffix

	dotID := insertPricingRule(t, d, &PricingRule{
		Provider:      provider,
		Region:        "*",
		APIModelName:  "claude-opus-4.6",
		BillingMode:   "flat",
		Prices:        ComponentPrices{Input: 15, Output: 75},
		Status:        "active",
		RuleName:      "exact dot rule " + suffix,
		EffectiveFrom: time.Now().Add(-time.Hour),
	})
	dashID := insertPricingRule(t, d, &PricingRule{
		Provider:      provider,
		Region:        "*",
		APIModelName:  "claude-opus-4-6",
		BillingMode:   "flat",
		Prices:        ComponentPrices{Input: 5, Output: 25},
		Status:        "active",
		RuleName:      "dash rule " + suffix,
		EffectiveFrom: time.Now().Add(-time.Hour),
	})
	defer func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM model_pricing_rules WHERE provider=$1", provider)
	}()

	svc := NewPricingService(d, zap.NewNop())
	rule, err := svc.MatchRule(ctx, provider, "*", "claude-opus-4.6", time.Now())
	if err != nil {
		t.Fatalf("MatchRule: %v", err)
	}
	if rule == nil || rule.ID != dotID {
		t.Errorf("exact dot rule %s should beat normalized dash rule %s, got %+v", dotID, dashID, rule)
	}
}

// TestBillingService_GetPricingRatioBatch_CycleGuard verifies the recursive CTE
// does not inflate the ratio when parent_group_id forms a cycle (only reachable
// via a manual DB edit; the app only ever points parent to a top-level platform
// group). Without the path-based cycle guard a 2-node loop of 1.5-ratio groups
// would multiply up to 1.5^10; with it the product is bounded (stops at the
// first repeat) and never explodes.
func TestBillingService_GetPricingRatioBatch_CycleGuard(t *testing.T) {
	d := testBillingDB(t)
	defer d.Close()

	ctx := context.Background()
	a := uuid.New().String()
	b := uuid.New().String()
	_, _ = d.Pool.Exec(ctx, "DELETE FROM user_groups WHERE id = ANY($1::uuid[])", []string{a, b})
	_, err := d.Pool.Exec(ctx,
		`INSERT INTO user_groups (id, name, pricing_ratio, status, created_at, updated_at)
		 VALUES ($1::uuid, 'cycle-a', 1.5, 'active', NOW(), NOW()),
		        ($2::uuid, 'cycle-b', 1.5, 'active', NOW(), NOW())`, a, b)
	if err != nil {
		t.Fatalf("insert groups: %v", err)
	}
	// Form a cycle a -> b -> a (manual edit; app never does this).
	_, _ = d.Pool.Exec(ctx, `UPDATE user_groups SET parent_group_id = $2::uuid WHERE id = $1::uuid`, a, b)
	_, _ = d.Pool.Exec(ctx, `UPDATE user_groups SET parent_group_id = $1::uuid WHERE id = $2::uuid`, a, b)
	defer func() {
		_, _ = d.Pool.Exec(ctx, `UPDATE user_groups SET parent_group_id = NULL WHERE id = ANY($1::uuid[])`, []string{a, b})
		_, _ = d.Pool.Exec(ctx, "DELETE FROM user_groups WHERE id = ANY($1::uuid[])", []string{a, b})
	}()

	svc := NewBillingService(d, zap.NewNop())
	got := svc.GetPricingRatio(ctx, a)
	// Must terminate and stay bounded — never 1.5^10 (~57.7). The cycle stops
	// at the first repeated node, so the product is a small finite value.
	if got <= 0 || got > 5.0 {
		t.Errorf("cycle ratio = %v, want a bounded small value (cycle guard must stop runaway multiplication)", got)
	}
}

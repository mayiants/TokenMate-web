package routes

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/config"
	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/services"
	"github.com/ai-proxy/ai-portal-go/internal/testutil"
	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

func testWebhookBillingDB(t *testing.T) *db.DB {
	t.Helper()
	ctx := context.Background()
	if err := db.InitTestSchema(ctx, testutil.PostgresDSN(), zap.NewNop()); err != nil {
		t.Fatalf("init test schema: %v", err)
	}
	pool, err := pgxpool.New(ctx, testutil.PostgresDSN())
	if err != nil {
		t.Fatalf("connect to test db: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping test db: %v", err)
	}
	// Ensure api_keys schema matches current code (name, status columns)
	_, _ = pool.Exec(ctx, `ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS name VARCHAR(255)`)
	_, _ = pool.Exec(ctx, `ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS status VARCHAR(20) DEFAULT 'active'`)
	return &db.DB{Pool: pool, Redis: nil}
}

func newTestWebhookRoutes(t *testing.T, database *db.DB) *WebhookRoutes {
	t.Helper()
	cfg := &config.Config{
		Portal: config.PortalConfig{SyncSecret: "test-secret"},
	}
	logger := zap.NewNop()
	exchangeSvc := services.NewExchangeService(database, logger)
	return NewWebhookRoutes(database, cfg, logger, nil, exchangeSvc, nil)
}

// TestProcessBillingBatch_DailyQuotaReset verifies daily quota reset in batch path.
func TestProcessBillingBatch_DailyQuotaReset(t *testing.T) {
	d := testWebhookBillingDB(t)
	defer d.Close()
	r := newTestWebhookRoutes(t, d)
	ctx := context.Background()

	userID := uuid.NewString()
	email := fmt.Sprintf("daily-%d@example.com", time.Now().UnixNano())
	keyHash := "test-daily-key-" + fmt.Sprintf("%d", time.Now().UnixNano())
	planID := fmt.Sprintf("2f820af6-7d60-4c48-a4d3-%012d", time.Now().UnixNano()%1000000000000)

	_, _ = d.Pool.Exec(ctx, "DELETE FROM api_keys WHERE key_hash = $1", keyHash)
	_, _ = d.Pool.Exec(ctx, "DELETE FROM user_subscriptions WHERE user_id = $1::uuid", userID)
	_, _ = d.Pool.Exec(ctx, "DELETE FROM users WHERE id = $1::uuid", userID)
	_, _ = d.Pool.Exec(ctx, "DELETE FROM subscription_plans WHERE id = $1::uuid", planID)
	defer func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM api_keys WHERE key_hash = $1", keyHash)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM user_subscriptions WHERE user_id = $1::uuid", userID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM balance_transactions WHERE user_id = $1::uuid", userID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM users WHERE id = $1::uuid", userID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM subscription_plans WHERE id = $1::uuid", planID)
	}()

	_, err := d.Pool.Exec(ctx,
		`INSERT INTO subscription_plans (id, name, plan_type, price, quota, duration_days, daily_limit, status, tpm_limit, tph_limit, rpm_limit, rph_limit, created_at, updated_at)
		 VALUES ($1::uuid, 'Test Plan', 'token', 1000, 100000, 30, 5000, 'active', 100000, 1000000, 60, 3600, NOW(), NOW())`,
		planID)
	if err != nil {
		t.Fatalf("insert plan: %v", err)
	}

	_, err = d.Pool.Exec(ctx,
		"INSERT INTO users (id, name, email, password_hash, role, balance, created_at, updated_at) VALUES ($1::uuid, $2, $3, $4, $5, $6, NOW(), NOW())",
		userID, "Daily User", email, "hash", "user", 1000000)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	_, err = d.Pool.Exec(ctx,
		`INSERT INTO api_keys (id, user_id, key_hash, key_prefix, name, status, key_type, created_at, updated_at)
		 VALUES (gen_random_uuid(), $1::uuid, $2, 'sk-daily', 'Daily Key', 'active', 'subscription', NOW(), NOW())`,
		userID, keyHash)
	if err != nil {
		t.Fatalf("insert api key: %v", err)
	}

	pastReset := time.Now().UTC().Add(-24 * time.Hour)
	_, err = d.Pool.Exec(ctx,
		`INSERT INTO user_subscriptions (user_id, plan_id, status, quota_total, quota_used, quota_remaining, daily_limit, daily_quota_used, daily_quota_reset, tpm_used, tph_used, rpm_used, rph_used, expires_at, created_at, updated_at)
		 VALUES ($1::uuid, $2::uuid, 'active', 10000, 0, 10000, 500, 999, $3, 0, 0, 0, 0, NOW() + INTERVAL '1 year', NOW(), NOW())`,
		userID, planID, pastReset)
	if err != nil {
		t.Fatalf("insert subscription: %v", err)
	}

	records := []*UsageRecord{
		{TraceID: "550e8400-e29b-41d4-a716-446655440030", APIKeyHash: keyHash, Model: "gpt-4", Provider: "openai", InputTokens: 10, OutputTokens: 5, RequestCount: 1, Cost: 0.001},
	}

	err = r.processBillingBatch(ctx, userID, records)
	if err != nil {
		t.Fatalf("processBillingBatch failed: %v", err)
	}

	var dailyUsed int64
	err = d.Pool.QueryRow(ctx,
		"SELECT daily_quota_used FROM user_subscriptions WHERE user_id = $1::uuid AND status = 'active'",
		userID).Scan(&dailyUsed)
	if err != nil {
		t.Fatalf("select daily_quota_used: %v", err)
	}
	expectedDaily := int64(10 + 5)
	if dailyUsed != expectedDaily {
		t.Errorf("expected daily_quota_used = %d (reset + new), got %d", expectedDaily, dailyUsed)
	}
}

// --- Phase 4: Enterprise hard stop tests ---
// These tests verify that processBillingBatch does NOT fall back to personal balance
// for enterprise keys when enterprise credit is exhausted (110% cap).

// setupEnterpriseTestData creates an enterprise, a user linked to it, and an enterprise API key.
// Returns (enterpriseID, userID, keyHash, modelProvider, modelName, cleanup func).
func setupEnterpriseTestData(t *testing.T, d *db.DB, r *WebhookRoutes, ctx context.Context,
	userBalance, giftBalance, creditTotal, creditUsed int64,
) (string, string, string, string, string, func()) {
	t.Helper()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	entName := "test-ent-" + suffix
	entEmail := "ent-" + suffix + "@test.com"
	userEmail := "entuser-" + suffix + "@test.com"
	keyHash := "ent-key-" + suffix
	modelProvider := "test-provider-" + suffix
	modelName := "test-model-" + suffix

	// 1. Insert a pricing rule with known pricing: input=$2/M, output=$4/M
	_, err := d.Pool.Exec(ctx,
		`INSERT INTO model_pricing_rules (provider, region, product_id, api_model_name, billing_mode, prices, status, rule_name, priority, effective_from)
		 VALUES ($1, '*', $2, $3, 'flat', $4, 'active', 'test rule', 0, NOW())`,
		modelProvider, modelName, modelName,
		`{"input": 2.0, "output": 4.0, "cache_hit": 0.2, "cache_write": 2.0, "reasoning": 2.0, "fixed_request": 0}`,
	)
	if err != nil {
		t.Fatalf("insert pricing rule: %v", err)
	}

	// 2. Insert enterprise
	var enterpriseID string
	err = d.Pool.QueryRow(ctx,
		`INSERT INTO enterprises (name, contact_email, status, credit_total, credit_used, billing_cycle)
		 VALUES ($1, $2, 'active', $3, $4, 'monthly')
		 RETURNING id::text`,
		entName, entEmail, creditTotal, creditUsed).Scan(&enterpriseID)
	if err != nil {
		t.Fatalf("insert enterprise: %v", err)
	}

	// 3. Insert user linked to enterprise
	var userID string
	err = d.Pool.QueryRow(ctx,
		`INSERT INTO users (name, email, password_hash, role, balance, gift_balance, enterprise_id)
		 VALUES ('Ent User', $1, 'hash', 'user', $2, $3, $4::uuid)
		 RETURNING id::text`,
		userEmail, userBalance, giftBalance, enterpriseID).Scan(&userID)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	// 4. Insert enterprise API key
	_, err = d.Pool.Exec(ctx,
		`INSERT INTO api_keys (id, user_id, key_hash, key_prefix, name, status, key_type, total_requests, total_tokens)
		 VALUES (gen_random_uuid(), $1::uuid, $2, 'sk-ent', 'Ent Key', 'active', 'enterprise', 0, 0)`,
		userID, keyHash)
	if err != nil {
		t.Fatalf("insert api key: %v", err)
	}

	cleanup := func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM usage_records WHERE user_id = $1::uuid", userID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM balance_transactions WHERE user_id = $1::uuid", userID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM enterprise_audit_log WHERE enterprise_id = $1::uuid", enterpriseID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM enterprise_alerts WHERE enterprise_id = $1::uuid", enterpriseID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM api_keys WHERE key_hash = $1", keyHash)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM users WHERE id = $1::uuid", userID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM enterprises WHERE id = $1::uuid", enterpriseID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM model_pricing_rules WHERE provider = $1 AND product_id = $2", modelProvider, modelName)
	}

	return enterpriseID, userID, keyHash, modelProvider, modelName, cleanup
}

// TestProcessBillingBatch_EnterpriseHardStop_NoPersonalBalanceFallback verifies that when
// enterprise credit is at 110% cap, processBillingBatch does NOT deduct from personal balance.
func TestProcessBillingBatch_EnterpriseHardStop_NoPersonalBalanceFallback(t *testing.T) {
	d := testWebhookBillingDB(t)
	defer d.Close()
	r := newTestWebhookRoutes(t, d)
	ctx := context.Background()

	// Enterprise with credit_total=500000 ($0.50), credit_used=550000 (110% — at cap)
	// User has balance=1000000 ($1.00) as personal funds
	enterpriseID, userID, keyHash, modelProvider, modelName, cleanup := setupEnterpriseTestData(t, d, r, ctx,
		1_000_000, /* balance */
		0,         /* gift_balance */
		500_000,   /* credit_total */
		550_000,   /* credit_used = 110% of credit_total */
	)
	defer cleanup()

	// Usage: 1M input + 1M output tokens → raw cost = $6.0 → with PricingRatio 0.85 = $5.10
	records := []*UsageRecord{
		{
			TraceID:    uuid.New().String(),
			APIKeyHash: keyHash, Model: modelName,
			Provider:    modelProvider,
			InputTokens: 1_000_000, OutputTokens: 1_000_000,
			RequestCount: 1, Cost: 5.10, PricingRatio: 0.85,
		},
	}

	err := r.processBillingBatch(ctx, userID, records)
	if err != nil {
		t.Fatalf("processBillingBatch failed: %v", err)
	}

	// Verify: personal balance should NOT be deducted
	var balanceAfter, giftAfter int64
	err = d.Pool.QueryRow(ctx,
		"SELECT COALESCE(balance, 0), COALESCE(gift_balance, 0) FROM users WHERE id = $1::uuid", userID,
	).Scan(&balanceAfter, &giftAfter)
	if err != nil {
		t.Fatalf("select user balance: %v", err)
	}
	if balanceAfter != 1_000_000 {
		t.Errorf("personal balance should NOT be deducted: expected 1000000, got %d", balanceAfter)
	}
	if giftAfter != 0 {
		t.Errorf("gift_balance should remain 0: got %d", giftAfter)
	}

	// Verify: enterprise credit_used should NOT change (already at 110% cap, no deduction)
	var creditUsedAfter int64
	err = d.Pool.QueryRow(ctx,
		"SELECT COALESCE(credit_used, 0) FROM enterprises WHERE id = $1::uuid", enterpriseID,
	).Scan(&creditUsedAfter)
	if err != nil {
		t.Fatalf("select enterprise credit_used: %v", err)
	}
	if creditUsedAfter != 550_000 {
		t.Errorf("enterprise credit_used should not change when at 110%% cap: expected 550000, got %d", creditUsedAfter)
	}

}

// TestProcessBillingBatch_EnterpriseCreditWithin110_Allowed verifies that enterprise credit
// deduction works correctly when within the 110% cap.
func TestProcessBillingBatch_EnterpriseCreditWithin110_Allowed(t *testing.T) {
	d := testWebhookBillingDB(t)
	defer d.Close()
	r := newTestWebhookRoutes(t, d)
	ctx := context.Background()

	// Enterprise with credit_total=10_000_000 ($10), credit_used=5_000_000 (50%)
	// User has balance=0, gift_balance=0
	enterpriseID, userID, keyHash, modelProvider, modelName, cleanup := setupEnterpriseTestData(t, d, r, ctx,
		0,          /* balance */
		0,          /* gift_balance */
		10_000_000, /* credit_total */
		5_000_000,  /* credit_used = 50% */
	)
	defer cleanup()

	// Usage: 1M input + 1M output tokens → raw cost = $6.0 → with PricingRatio 0.85 = $5.10 = 5,100,000 micro-USD
	records := []*UsageRecord{
		{
			TraceID:    uuid.New().String(),
			APIKeyHash: keyHash, Model: modelName,
			Provider:    modelProvider,
			InputTokens: 1_000_000, OutputTokens: 1_000_000,
			RequestCount: 1, Cost: 5.10, PricingRatio: 0.85,
		},
	}

	err := r.processBillingBatch(ctx, userID, records)
	if err != nil {
		t.Fatalf("processBillingBatch failed: %v", err)
	}

	// Verify: enterprise credit_used should increase by ~5,100,000
	var creditUsedAfter int64
	err = d.Pool.QueryRow(ctx,
		"SELECT COALESCE(credit_used, 0) FROM enterprises WHERE id = $1::uuid", enterpriseID,
	).Scan(&creditUsedAfter)
	if err != nil {
		t.Fatalf("select enterprise credit_used: %v", err)
	}
	if creditUsedAfter <= 5_000_000 {
		t.Errorf("enterprise credit_used should increase from 5000000, got %d", creditUsedAfter)
	}

	// Verify: personal balance should remain 0
	var balanceAfter int64
	err = d.Pool.QueryRow(ctx,
		"SELECT COALESCE(balance, 0) FROM users WHERE id = $1::uuid", userID,
	).Scan(&balanceAfter)
	if err != nil {
		t.Fatalf("select user balance: %v", err)
	}
	if balanceAfter != 0 {
		t.Errorf("personal balance should remain 0 when credit covers cost: got %d", balanceAfter)
	}
}

// TestProcessBillingBatch_EnterpriseCreditOnly_NoGiftNoBalance verifies that
// enterprise-key usage bills the enterprise credit ONLY: personal gift and
// balance are never touched, and over-cap overage is left unpaid (hard stop).
func TestProcessBillingBatch_EnterpriseCreditOnly_NoGiftNoBalance(t *testing.T) {
	d := testWebhookBillingDB(t)
	defer d.Close()
	r := newTestWebhookRoutes(t, d)
	ctx := context.Background()

	// Enterprise with credit_total=3_000_000 ($3), credit_used=0
	// User has gift_balance=1_000_000 ($1), balance=5_000_000 ($5)
	enterpriseID, userID, keyHash, modelProvider, modelName, cleanup := setupEnterpriseTestData(t, d, r, ctx,
		5_000_000, /* balance */
		1_000_000, /* gift_balance */
		3_000_000, /* credit_total */
		0,         /* credit_used */
	)
	defer cleanup()

	// Usage: 1M input + 1M output tokens → raw cost = $6.0 → with PricingRatio 0.85 = $5.10 = 5,100,000 micro-USD
	// Deduction: enterprise credit ONLY (cap = 3,000,000 * 1.1 = 3,300,000);
	// gift/balance untouched, remaining 1,800,000 unpaid (hard stop).
	records := []*UsageRecord{
		{
			TraceID:    uuid.New().String(),
			APIKeyHash: keyHash, Model: modelName,
			Provider:    modelProvider,
			InputTokens: 1_000_000, OutputTokens: 1_000_000,
			RequestCount: 1, Cost: 5.10, PricingRatio: 0.85,
		},
	}

	err := r.processBillingBatch(ctx, userID, records)
	if err != nil {
		t.Fatalf("processBillingBatch failed: %v", err)
	}

	// Verify: gift_balance NOT deducted (enterprise keys never touch personal funds)
	var giftAfter int64
	err = d.Pool.QueryRow(ctx,
		"SELECT COALESCE(gift_balance, 0) FROM users WHERE id = $1::uuid", userID,
	).Scan(&giftAfter)
	if err != nil {
		t.Fatalf("select gift_balance: %v", err)
	}
	if giftAfter != 1_000_000 {
		t.Errorf("gift_balance should NOT be deducted by enterprise-key usage: expected 1000000, got %d", giftAfter)
	}

	// Verify: enterprise credit_used should be 3,300,000 (110% cap)
	var creditUsedAfter int64
	err = d.Pool.QueryRow(ctx,
		"SELECT COALESCE(credit_used, 0) FROM enterprises WHERE id = $1::uuid", enterpriseID,
	).Scan(&creditUsedAfter)
	if err != nil {
		t.Fatalf("select enterprise credit_used: %v", err)
	}
	if creditUsedAfter != 3_300_000 {
		t.Errorf("enterprise credit_used should be 3300000 (110%% cap), got %d", creditUsedAfter)
	}

	// Verify: personal balance should NOT be deducted (hard stop at 110%)
	var balanceAfter int64
	err = d.Pool.QueryRow(ctx,
		"SELECT COALESCE(balance, 0) FROM users WHERE id = $1::uuid", userID,
	).Scan(&balanceAfter)
	if err != nil {
		t.Fatalf("select user balance: %v", err)
	}
	if balanceAfter != 5_000_000 {
		t.Errorf("personal balance should NOT be deducted (hard stop): expected 5000000, got %d", balanceAfter)
	}
}

// TestProcessBillingBatch_MixedKeyTypes verifies per-key-hash key type
// resolution: in one batch, enterprise-key records bill enterprise credit
// while general-key records deduct personal gift. This is the regression
// test for the previous "first key of user LIMIT 1" misrouting.
func TestProcessBillingBatch_MixedKeyTypes(t *testing.T) {
	d := testWebhookBillingDB(t)
	defer d.Close()
	r := newTestWebhookRoutes(t, d)
	ctx := context.Background()

	// Enterprise credit 10M, user gift 10M, balance 0
	enterpriseID, userID, entKeyHash, modelProvider, modelName, cleanup := setupEnterpriseTestData(t, d, r, ctx,
		0,          /* balance */
		10_000_000, /* gift_balance */
		10_000_000, /* credit_total */
		0,          /* credit_used */
	)
	defer cleanup()

	// Add a GENERAL key for the same user. Critically, insert it so that a
	// naive "first key of user" lookup would return the general key (the
	// previous bug: enterprise usage then billed personal funds).
	genKeyHash := "gen-key-" + entKeyHash
	_, err := d.Pool.Exec(ctx,
		`INSERT INTO api_keys (id, user_id, key_hash, key_prefix, name, status, key_type, total_requests, total_tokens)
		 VALUES (gen_random_uuid(), $1::uuid, $2, 'sk-gen', 'Gen Key', 'active', 'general', 0, 0)`,
		userID, genKeyHash)
	if err != nil {
		t.Fatalf("insert general key: %v", err)
	}
	defer func() { _, _ = d.Pool.Exec(ctx, "DELETE FROM api_keys WHERE key_hash = $1", genKeyHash) }()

	// Each record: 1M in + 1M out → $5.10 after ratio 0.85 = 5,100,000 micro-USD
	mkRec := func(hash string) *UsageRecord {
		return &UsageRecord{
			TraceID:    uuid.New().String(),
			APIKeyHash: hash, Model: modelName,
			Provider:    modelProvider,
			InputTokens: 1_000_000, OutputTokens: 1_000_000,
			RequestCount: 1, Cost: 5.10, PricingRatio: 0.85,
		}
	}
	records := []*UsageRecord{mkRec(genKeyHash), mkRec(entKeyHash)}

	if err := r.processBillingBatch(ctx, userID, records); err != nil {
		t.Fatalf("processBillingBatch failed: %v", err)
	}

	// Enterprise record (5.1M) → credit_used; general record (5.1M) → gift
	var creditUsedAfter, giftAfter int64
	if err := d.Pool.QueryRow(ctx,
		"SELECT COALESCE(credit_used, 0) FROM enterprises WHERE id = $1::uuid", enterpriseID,
	).Scan(&creditUsedAfter); err != nil {
		t.Fatalf("select credit_used: %v", err)
	}
	if creditUsedAfter != 5_100_000 {
		t.Errorf("enterprise credit_used should be 5100000, got %d", creditUsedAfter)
	}
	if err := d.Pool.QueryRow(ctx,
		"SELECT COALESCE(gift_balance, 0) FROM users WHERE id = $1::uuid", userID,
	).Scan(&giftAfter); err != nil {
		t.Fatalf("select gift_balance: %v", err)
	}
	if giftAfter != 10_000_000-5_100_000 {
		t.Errorf("gift_balance should be 4900000 (general record only), got %d", giftAfter)
	}
}

// TestProcessStats_InTransaction verifies processStats wraps writes in a transaction.
func TestProcessStats_InTransaction(t *testing.T) {
	d := testWebhookBillingDB(t)
	defer d.Close()
	r := newTestWebhookRoutes(t, d)
	ctx := context.Background()

	userID := "550e8400-e29b-41d4-a716-446655440006"
	keyHash := "test-stats-key-" + fmt.Sprintf("%d", time.Now().UnixNano())
	traceID := "550e8400-e29b-41d4-a716-446655440040"

	_, _ = d.Pool.Exec(ctx, "DELETE FROM usage_records WHERE trace_id = $1", traceID)
	_, _ = d.Pool.Exec(ctx, "DELETE FROM api_keys WHERE key_hash = $1", keyHash)
	_, _ = d.Pool.Exec(ctx, "DELETE FROM users WHERE id = $1::uuid", userID)
	defer func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM usage_records WHERE trace_id = $1", traceID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM api_keys WHERE key_hash = $1", keyHash)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM users WHERE id = $1::uuid", userID)
	}()

	_, err := d.Pool.Exec(ctx,
		"INSERT INTO users (id, name, email, password_hash, role, balance, created_at, updated_at) VALUES ($1::uuid, $2, $3, $4, $5, $6, NOW(), NOW())",
		userID, "Stats User", "stats@example.com", "hash", "user", 1000000)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	_, err = d.Pool.Exec(ctx,
		`INSERT INTO api_keys (id, user_id, key_hash, key_prefix, name, status, total_requests, total_tokens, created_at, updated_at)
		 VALUES (gen_random_uuid(), $1::uuid, $2, 'sk-stats', 'Stats Key', 'active', 0, 0, NOW(), NOW())`,
		userID, keyHash)
	if err != nil {
		t.Fatalf("insert api key: %v", err)
	}

	record := &UsageRecord{
		TraceID: traceID, APIKeyHash: keyHash, Model: "gpt-4", Provider: "openai",
		InputTokens: 100, OutputTokens: 50, RequestCount: 1, Cost: 0.005, RoutingTrack: "standard",
	}

	err = r.processStats(ctx, record)
	if err != nil {
		t.Fatalf("processStats failed: %v", err)
	}

	var count int
	err = d.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM usage_records WHERE trace_id = $1", traceID).Scan(&count)
	if err != nil {
		t.Fatalf("select usage_records: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 usage_record, got %d", count)
	}

	var totalReqs, totalTokens int64
	err = d.Pool.QueryRow(ctx, "SELECT total_requests, total_tokens FROM api_keys WHERE key_hash = $1", keyHash).Scan(&totalReqs, &totalTokens)
	if err != nil {
		t.Fatalf("select api_keys: %v", err)
	}
	if totalReqs != 1 {
		t.Errorf("expected total_requests = 1, got %d", totalReqs)
	}
	if totalTokens != 150 {
		t.Errorf("expected total_tokens = 150, got %d", totalTokens)
	}
}

// TestProcessBillingBatch_MultipleActiveSubscriptions pins the fix for the
// double-deduction bug: when a user holds more than one active subscription,
// quota must be deducted only from the selected (newest) subscription — not
// from every active row.
func TestProcessBillingBatch_MultipleActiveSubscriptions(t *testing.T) {
	d := testWebhookBillingDB(t)
	defer d.Close()
	r := newTestWebhookRoutes(t, d)
	ctx := context.Background()

	userID := uuid.NewString()
	email := fmt.Sprintf("multisub-%d@example.com", time.Now().UnixNano())
	keyHash := fmt.Sprintf("test-multisub-key-%d", time.Now().UnixNano())
	planID := fmt.Sprintf("2f820af6-7d60-4c48-a4d3-%012d", time.Now().UnixNano()%1000000000000)

	_, _ = d.Pool.Exec(ctx, "DELETE FROM api_keys WHERE key_hash = $1", keyHash)
	_, _ = d.Pool.Exec(ctx, "DELETE FROM user_subscriptions WHERE user_id = $1::uuid", userID)
	_, _ = d.Pool.Exec(ctx, "DELETE FROM users WHERE id = $1::uuid", userID)
	_, _ = d.Pool.Exec(ctx, "DELETE FROM subscription_plans WHERE id = $1::uuid", planID)
	defer func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM api_keys WHERE key_hash = $1", keyHash)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM user_subscriptions WHERE user_id = $1::uuid", userID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM balance_transactions WHERE user_id = $1::uuid", userID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM users WHERE id = $1::uuid", userID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM subscription_plans WHERE id = $1::uuid", planID)
	}()

	if _, err := d.Pool.Exec(ctx,
		`INSERT INTO subscription_plans (id, name, plan_type, price, quota, duration_days, daily_limit, status, tpm_limit, tph_limit, rpm_limit, rph_limit, created_at, updated_at)
		 VALUES ($1::uuid, 'Test Plan', 'token', 1000, 100000, 30, 5000, 'active', 100000, 1000000, 60, 3600, NOW(), NOW())`,
		planID); err != nil {
		t.Fatalf("insert plan: %v", err)
	}
	if _, err := d.Pool.Exec(ctx,
		"INSERT INTO users (id, name, email, password_hash, role, balance, created_at, updated_at) VALUES ($1::uuid, $2, $3, $4, $5, $6, NOW(), NOW())",
		userID, "Multi Sub User", email, "hash", "user", 1000000); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := d.Pool.Exec(ctx,
		`INSERT INTO api_keys (id, user_id, key_hash, key_prefix, name, status, key_type, created_at, updated_at)
		 VALUES (gen_random_uuid(), $1::uuid, $2, 'sk-multi', 'Multi Key', 'active', 'subscription', NOW(), NOW())`,
		userID, keyHash); err != nil {
		t.Fatalf("insert api key: %v", err)
	}

	// Older subscription with a large quota, newer one with a small quota.
	if _, err := d.Pool.Exec(ctx,
		`INSERT INTO user_subscriptions (user_id, plan_id, status, quota_total, quota_used, quota_remaining, daily_limit, daily_quota_used, daily_quota_reset, expires_at, created_at, updated_at)
		 VALUES ($1::uuid, $2::uuid, 'active', 50000, 0, 50000, 0, 0, NOW() + INTERVAL '1 day', NOW() + INTERVAL '1 year', NOW() - INTERVAL '10 days', NOW())`,
		userID, planID); err != nil {
		t.Fatalf("insert old subscription: %v", err)
	}
	if _, err := d.Pool.Exec(ctx,
		`INSERT INTO user_subscriptions (user_id, plan_id, status, quota_total, quota_used, quota_remaining, daily_limit, daily_quota_used, daily_quota_reset, expires_at, created_at, updated_at)
		 VALUES ($1::uuid, $2::uuid, 'active', 10000, 0, 10000, 0, 0, NOW() + INTERVAL '1 day', NOW() + INTERVAL '1 year', NOW(), NOW())`,
		userID, planID); err != nil {
		t.Fatalf("insert new subscription: %v", err)
	}

	records := []*UsageRecord{
		{TraceID: "550e8400-e29b-41d4-a716-446655440031", APIKeyHash: keyHash, Model: "gpt-4", Provider: "openai", InputTokens: 10, OutputTokens: 5, RequestCount: 1, Cost: 0.001},
	}
	if err := r.processBillingBatch(ctx, userID, records); err != nil {
		t.Fatalf("processBillingBatch failed: %v", err)
	}

	rows, err := d.Pool.Query(ctx,
		`SELECT quota_remaining, daily_quota_used FROM user_subscriptions
		 WHERE user_id = $1::uuid AND status = 'active' ORDER BY created_at ASC`, userID)
	if err != nil {
		t.Fatalf("query subscriptions: %v", err)
	}
	defer rows.Close()
	var remainders []int64
	for rows.Next() {
		var rem, daily int64
		if err := rows.Scan(&rem, &daily); err != nil {
			t.Fatalf("scan: %v", err)
		}
		remainders = append(remainders, rem)
		if daily != 0 && daily != 15 {
			t.Errorf("unexpected daily_quota_used = %d", daily)
		}
	}
	if len(remainders) != 2 {
		t.Fatalf("expected 2 subscriptions, got %d", len(remainders))
	}
	if remainders[0] != 50000 {
		t.Errorf("older subscription must be untouched: quota_remaining = %d, want 50000", remainders[0])
	}
	if remainders[1] != 10000-15 {
		t.Errorf("newest subscription should be deducted: quota_remaining = %d, want %d", remainders[1], 10000-15)
	}
}

// TestMarkBillingRecords_DedupAndRelease verifies trace-level idempotency:
// a record with an already-billed trace_id is skipped, and releasing the
// marker (on failure re-queue) lets the retry through.
func TestMarkBillingRecords_DedupAndRelease(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = rdb.Close() }()

	database := &db.DB{Redis: rdb}
	r := newTestWebhookRoutes(t, database)
	ctx := context.Background()

	rec1 := &UsageRecord{TraceID: "dedup-trace-1", APIKeyHash: "hash-1"}
	rec2 := &UsageRecord{TraceID: "dedup-trace-1", APIKeyHash: "hash-1"} // replay of rec1
	rec3 := &UsageRecord{TraceID: "dedup-trace-2", APIKeyHash: "hash-1"}
	rec4 := &UsageRecord{TraceID: "", APIKeyHash: "hash-1"} // no trace: always kept

	kept, markers := r.markBillingRecords(ctx, []*UsageRecord{rec1, rec2, rec3, rec4})
	if len(kept) != 3 {
		t.Fatalf("expected 3 records kept (duplicate skipped), got %d", len(kept))
	}
	for _, k := range kept {
		if k == rec2 {
			t.Fatal("duplicate trace_id record was not skipped")
		}
	}

	// Re-marking without release must skip (duplicate).
	kept2, _ := r.markBillingRecords(ctx, []*UsageRecord{rec1})
	if len(kept2) != 0 {
		t.Fatalf("expected re-mark of billed trace to be skipped, kept %d", len(kept2))
	}

	// After release (failure re-queue), the retry must be billable again.
	r.releaseBillingMarker(ctx, markers, rec1)
	kept3, _ := r.markBillingRecords(ctx, []*UsageRecord{rec1})
	if len(kept3) != 1 {
		t.Fatalf("expected retry to be billable after marker release, kept %d", len(kept3))
	}
}

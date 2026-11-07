package routes

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/google/uuid"
)

// setupSubscriptionBillingData seeds a user + subscription key + plan + a pricing
// rule ($1/M input, $2/M output) and returns the identifiers. planType is "token"
// or "code". quotaRemaining is the subscription quota bucket (tokens for a token
// plan, request-count for a code plan). balance/gift are the user's wallet.
//
// It returns a cleanup func the caller must defer.
func setupSubscriptionBillingData(t *testing.T, d *db.DB, r *WebhookRoutes, ctx context.Context,
	planType string, quotaRemaining, balance, gift int64) (userID, keyHash, provider, model string, cleanup func()) {
	t.Helper()
	userID = uuid.NewString()
	keyHash = "test-sub-bill-" + fmt.Sprintf("%d", time.Now().UnixNano())
	planID := uuid.NewString()
	provider = "openai"
	model = "sub-bill-model-" + fmt.Sprintf("%d", time.Now().UnixNano()%100000)
	email := fmt.Sprintf("sub-bill-%d@example.com", time.Now().UnixNano())

	mustExec := func(sql string, args ...interface{}) {
		if _, err := d.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("setup exec failed (%s): %v", sql, err)
		}
	}

	mustExec(`INSERT INTO subscription_plans (id, name, plan_type, price, quota, duration_days, daily_limit, status, created_at, updated_at)
		 VALUES ($1::uuid, 'SubBill Plan', $2, 1000, 100000, 30, 0, 'active', NOW(), NOW())`, planID, planType)
	mustExec(`INSERT INTO users (id, name, email, password_hash, role, balance, gift_balance, created_at, updated_at)
		 VALUES ($1::uuid, 'SubBill User', $2, 'hash', 'user', $3, $4, NOW(), NOW())`, userID, email, balance, gift)
	mustExec(`INSERT INTO api_keys (id, user_id, key_hash, key_prefix, name, status, key_type, created_at, updated_at)
		 VALUES (gen_random_uuid(), $1::uuid, $2, 'sk-subbill', 'SubBill Key', 'active', 'subscription', NOW(), NOW())`, userID, keyHash)
	mustExec(`INSERT INTO user_subscriptions (user_id, plan_id, plan_name, plan_type, status, quota_total, quota_used, quota_remaining, expires_at, created_at, updated_at)
		 VALUES ($1::uuid, $2::uuid, 'SubBill Plan', $3, 'active', 100000, 0, $4, NOW() + INTERVAL '1 year', NOW(), NOW())`,
		userID, planID, planType, quotaRemaining)
	// $1/M input, $2/M output.
	mustExec(`INSERT INTO model_pricing_rules (provider, region, product_id, api_model_name, billing_mode, prices, status, rule_name, priority, effective_from)
		 VALUES ($1, '*', $2, $2, 'flat', $3, 'active', 'subbill rule', 0, NOW())`,
		provider, model, `{"input": 1.0, "output": 2.0, "cache_hit": 0.2, "cache_write": 2.0, "reasoning": 2.0, "fixed_request": 0}`)

	cleanup = func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM model_pricing_rules WHERE provider = $1 AND product_id = $2", provider, model)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM balance_transactions WHERE user_id = $1::uuid", userID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM usage_records WHERE user_id = $1::uuid", userID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM api_keys WHERE key_hash = $1", keyHash)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM user_subscriptions WHERE user_id = $1::uuid", userID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM users WHERE id = $1::uuid", userID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM subscription_plans WHERE id = $1::uuid", planID)
	}
	return userID, keyHash, provider, model, cleanup
}

func walletOf(t *testing.T, d *db.DB, ctx context.Context, userID string) (balance, gift int64) {
	t.Helper()
	if err := d.Pool.QueryRow(ctx,
		`SELECT COALESCE(balance,0), COALESCE(gift_balance,0) FROM users WHERE id = $1::uuid`, userID,
	).Scan(&balance, &gift); err != nil {
		t.Fatalf("select wallet: %v", err)
	}
	return balance, gift
}

func subQuotaOf(t *testing.T, d *db.DB, ctx context.Context, userID string) (remaining, used int64) {
	t.Helper()
	if err := d.Pool.QueryRow(ctx,
		`SELECT quota_remaining, quota_used FROM user_subscriptions WHERE user_id = $1::uuid AND status='active'`, userID,
	).Scan(&remaining, &used); err != nil {
		t.Fatalf("select sub quota: %v", err)
	}
	return remaining, used
}

// TestSubscription_CodePlanDeductsRequests: a code plan deducts REQUEST COUNT
// from the quota bucket, not tokens. One request with 300 tokens must deduct 1,
// not 300. (single path)
func TestSubscription_CodePlanDeductsRequests(t *testing.T) {
	d := testWebhookBillingDB(t)
	defer d.Close()
	r := newTestWebhookRoutes(t, d)
	ctx := context.Background()

	userID, keyHash, provider, model, cleanup := setupSubscriptionBillingData(t, d, r, ctx,
		"code", 100 /*quota=100 requests*/, 0 /*balance*/, 0 /*gift*/)
	defer cleanup()

	rec := &UsageRecord{
		TraceID: uuid.NewString(), APIKeyHash: keyHash, Model: model, Provider: provider,
		InputTokens: 200, OutputTokens: 100, RequestCount: 1, PricingRatio: 1.0, RoutingTrack: "standard",
	}
	if err := r.processRecord(ctx, rec); err != nil {
		t.Fatalf("processRecord: %v", err)
	}

	remaining, used := subQuotaOf(t, d, ctx, userID)
	if used != 1 {
		t.Errorf("code plan quota_used = %d, want 1 (one request)", used)
	}
	if remaining != 99 {
		t.Errorf("code plan quota_remaining = %d, want 99", remaining)
	}
}

// TestSubscription_TokenPlanDeductsTokens: a token plan still deducts input+output.
func TestSubscription_TokenPlanDeductsTokens(t *testing.T) {
	d := testWebhookBillingDB(t)
	defer d.Close()
	r := newTestWebhookRoutes(t, d)
	ctx := context.Background()

	userID, keyHash, provider, model, cleanup := setupSubscriptionBillingData(t, d, r, ctx,
		"token", 100000, 0, 0)
	defer cleanup()

	rec := &UsageRecord{
		TraceID: uuid.NewString(), APIKeyHash: keyHash, Model: model, Provider: provider,
		InputTokens: 200, OutputTokens: 100, RequestCount: 1, PricingRatio: 1.0, RoutingTrack: "standard",
	}
	if err := r.processRecord(ctx, rec); err != nil {
		t.Fatalf("processRecord: %v", err)
	}
	_, used := subQuotaOf(t, d, ctx, userID)
	if used != 300 {
		t.Errorf("token plan quota_used = %d, want 300 (tokens)", used)
	}
}

// TestSubscription_NoDoubleCharge: a request fully covered by subscription quota
// must NOT also deduct the dollar cost from gift/balance. (single path)
func TestSubscription_NoDoubleCharge(t *testing.T) {
	d := testWebhookBillingDB(t)
	defer d.Close()
	r := newTestWebhookRoutes(t, d)
	ctx := context.Background()

	userID, keyHash, provider, model, cleanup := setupSubscriptionBillingData(t, d, r, ctx,
		"token", 100000 /*ample quota*/, 1000000 /*balance*/, 500000 /*gift*/)
	defer cleanup()

	rec := &UsageRecord{
		TraceID: uuid.NewString(), APIKeyHash: keyHash, Model: model, Provider: provider,
		InputTokens: 200, OutputTokens: 100, RequestCount: 1, PricingRatio: 1.0, RoutingTrack: "standard",
	}
	if err := r.processRecord(ctx, rec); err != nil {
		t.Fatalf("processRecord: %v", err)
	}

	balance, gift := walletOf(t, d, ctx, userID)
	if balance != 1000000 || gift != 500000 {
		t.Errorf("wallet changed on a sub-covered request: balance=%d gift=%d, want 1000000/500000 (no double charge)", balance, gift)
	}
	_, used := subQuotaOf(t, d, ctx, userID)
	if used != 300 {
		t.Errorf("quota_used = %d, want 300", used)
	}
}

// TestSubscription_ExhaustedNoWalletFallback: when the subscription quota is
// exhausted, the billing path must NOT fall back to gift/balance. Any uncovered
// cost is simply not charged to the wallet (the proxy gate rejects such requests
// up front; billing never silently drains the wallet). (single path)
func TestSubscription_ExhaustedNoWalletFallback(t *testing.T) {
	d := testWebhookBillingDB(t)
	defer d.Close()
	r := newTestWebhookRoutes(t, d)
	ctx := context.Background()

	userID, keyHash, provider, model, cleanup := setupSubscriptionBillingData(t, d, r, ctx,
		"token", 0 /*exhausted*/, 1000000 /*balance available*/, 500000 /*gift available*/)
	defer cleanup()

	rec := &UsageRecord{
		TraceID: uuid.NewString(), APIKeyHash: keyHash, Model: model, Provider: provider,
		InputTokens: 200, OutputTokens: 100, RequestCount: 1, PricingRatio: 1.0, RoutingTrack: "standard",
	}
	if err := r.processRecord(ctx, rec); err != nil {
		t.Fatalf("processRecord: %v", err)
	}

	balance, gift := walletOf(t, d, ctx, userID)
	if balance != 1000000 || gift != 500000 {
		t.Errorf("wallet drained on exhausted subscription: balance=%d gift=%d, want 1000000/500000 (no fallback)", balance, gift)
	}
}

// TestSubscription_ExhaustedNoWalletFallback_Batch: same guarantee on the batch
// path (the live primary billing path).
func TestSubscription_ExhaustedNoWalletFallback_Batch(t *testing.T) {
	d := testWebhookBillingDB(t)
	defer d.Close()
	r := newTestWebhookRoutes(t, d)
	ctx := context.Background()

	userID, keyHash, provider, model, cleanup := setupSubscriptionBillingData(t, d, r, ctx,
		"token", 0 /*exhausted*/, 1000000, 500000)
	defer cleanup()

	records := []*UsageRecord{{
		TraceID: uuid.NewString(), APIKeyHash: keyHash, Model: model, Provider: provider,
		InputTokens: 200, OutputTokens: 100, RequestCount: 1, PricingRatio: 1.0, RoutingTrack: "standard",
	}}
	if err := r.processBillingBatch(ctx, userID, records); err != nil {
		t.Fatalf("processBillingBatch: %v", err)
	}

	balance, gift := walletOf(t, d, ctx, userID)
	if balance != 1000000 || gift != 500000 {
		t.Errorf("batch: wallet drained on exhausted subscription: balance=%d gift=%d, want 1000000/500000", balance, gift)
	}
}

// TestSubscription_CodePlanDeductsRequests_Batch: batch path also deducts
// request count for code plans.
func TestSubscription_CodePlanDeductsRequests_Batch(t *testing.T) {
	d := testWebhookBillingDB(t)
	defer d.Close()
	r := newTestWebhookRoutes(t, d)
	ctx := context.Background()

	userID, keyHash, provider, model, cleanup := setupSubscriptionBillingData(t, d, r, ctx,
		"code", 100, 0, 0)
	defer cleanup()

	records := []*UsageRecord{
		{TraceID: uuid.NewString(), APIKeyHash: keyHash, Model: model, Provider: provider,
			InputTokens: 200, OutputTokens: 100, RequestCount: 1, PricingRatio: 1.0, RoutingTrack: "standard"},
		{TraceID: uuid.NewString(), APIKeyHash: keyHash, Model: model, Provider: provider,
			InputTokens: 500, OutputTokens: 400, RequestCount: 1, PricingRatio: 1.0, RoutingTrack: "standard"},
	}
	if err := r.processBillingBatch(ctx, userID, records); err != nil {
		t.Fatalf("processBillingBatch: %v", err)
	}
	_, used := subQuotaOf(t, d, ctx, userID)
	if used != 2 {
		t.Errorf("batch code plan quota_used = %d, want 2 (two requests)", used)
	}
}

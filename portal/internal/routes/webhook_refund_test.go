package routes

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestProcessBillingBatch_RefundNetZero verifies the hot billing path for
// charge-on-dispatch refunds: a dispatch charge followed by a status=refund
// record (fresh trace_id, same billing metadata) must net the user's balance
// back to the pre-charge value, with both the charge and the refund audited
// in usage_records and balance_transactions.
func TestProcessBillingBatch_RefundNetZero(t *testing.T) {
	d := testWebhookBillingDB(t)
	defer d.Close()
	r := newTestWebhookRoutes(t, d)
	ctx := context.Background()

	userID := uuid.NewString()
	keyHash := "test-refund-batch-" + fmt.Sprintf("%d", time.Now().UnixNano())
	cleanup := func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM usage_records WHERE user_id = $1::uuid", userID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM balance_transactions WHERE user_id = $1::uuid", userID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM api_keys WHERE key_hash = $1", keyHash)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM users WHERE id = $1::uuid", userID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM model_pricing_rules WHERE provider = 'tokenrouter' AND api_model_name = 'kling-v3'")
	}
	cleanup()
	defer cleanup()

	_, err := d.Pool.Exec(ctx,
		`INSERT INTO model_pricing_rules (provider, region, api_model_name, billing_mode, prices, status, created_at, updated_at)
		 VALUES ('tokenrouter', '*', 'kling-v3', 'flat', '{"fixed_request": 0.56}', 'active', NOW(), NOW())`)
	if err != nil {
		t.Fatalf("insert pricing rule: %v", err)
	}

	const startBalance = int64(2_000_000)
	_, err = d.Pool.Exec(ctx,
		"INSERT INTO users (id, name, email, password_hash, role, balance, created_at, updated_at) VALUES ($1::uuid, $2, $3, $4, $5, $6, NOW(), NOW())",
		userID, "Refund User", fmt.Sprintf("refund-%d@example.com", time.Now().UnixNano()), "hash", "user", startBalance)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = d.Pool.Exec(ctx,
		`INSERT INTO api_keys (id, user_id, key_hash, key_prefix, name, status, key_type, created_at, updated_at)
		 VALUES (gen_random_uuid(), $1::uuid, $2, 'sk-refund', 'Refund Key', 'active', 'general', NOW(), NOW())`,
		userID, keyHash)
	if err != nil {
		t.Fatalf("insert api key: %v", err)
	}

	// Dispatch charge then terminal-failure refund (fresh trace_id — the
	// portal billing queue dedups by trace_id and would drop the refund
	// if it reused the dispatch trace_id).
	chargeTrace := uuid.NewString()
	refundTrace := uuid.NewString()

	// The stats pipeline normally persists the usage rows before billing;
	// mimic that so the batch billing updates real rows and the dbCosts
	// shortcut sees the stored total_cost.
	_, err = d.Pool.Exec(ctx,
		`INSERT INTO usage_records (trace_id, user_id, model, provider, total_cost, request_count, status, created_at)
		 VALUES ($1, $2::uuid, 'kling-v3', 'tokenrouter', 560000, 1, 'success', NOW())`,
		chargeTrace, userID)
	if err != nil {
		t.Fatalf("insert charge usage row: %v", err)
	}
	_, err = d.Pool.Exec(ctx,
		`INSERT INTO usage_records (trace_id, user_id, model, provider, total_cost, request_count, status, created_at)
		 VALUES ($1, $2::uuid, 'kling-v3', 'tokenrouter', -560000, 1, 'refund', NOW())`,
		refundTrace, userID)
	if err != nil {
		t.Fatalf("insert refund usage row: %v", err)
	}

	records := []*UsageRecord{
		{TraceID: chargeTrace, APIKeyHash: keyHash, Model: "kling-v3", Provider: "tokenrouter", RequestCount: 1},
		{TraceID: refundTrace, APIKeyHash: keyHash, Model: "kling-v3", Provider: "tokenrouter", RequestCount: 1, Status: "refund"},
	}
	if err := r.processBillingBatch(ctx, userID, records); err != nil {
		t.Fatalf("processBillingBatch: %v", err)
	}

	var balance int64
	if err := d.Pool.QueryRow(ctx, "SELECT COALESCE(balance, 0) FROM users WHERE id = $1::uuid", userID).Scan(&balance); err != nil {
		t.Fatalf("select balance: %v", err)
	}
	if balance != startBalance {
		t.Errorf("balance = %d, want %d (charge and refund must net to zero)", balance, startBalance)
	}

	// Audit rows: charge positive, refund negative with status 'refund'.
	var chargeCost, refundCost int64
	var refundStatus string
	if err := d.Pool.QueryRow(ctx,
		"SELECT total_cost FROM usage_records WHERE trace_id = $1", chargeTrace).Scan(&chargeCost); err != nil {
		t.Fatalf("select charge row: %v", err)
	}
	if err := d.Pool.QueryRow(ctx,
		"SELECT total_cost, status FROM usage_records WHERE trace_id = $1", refundTrace).Scan(&refundCost, &refundStatus); err != nil {
		t.Fatalf("select refund row: %v", err)
	}
	if chargeCost != 560_000 {
		t.Errorf("charge total_cost = %d, want 560000 ($0.56 flat)", chargeCost)
	}
	if refundCost != -560_000 {
		t.Errorf("refund total_cost = %d, want -560000", refundCost)
	}
	if refundStatus != "refund" {
		t.Errorf("refund status = %q, want %q", refundStatus, "refund")
	}

	// Ledger: one usage deduction and one refund credit.
	var usageTx, refundTx int64
	if err := d.Pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount), 0) FROM balance_transactions WHERE user_id = $1::uuid AND transaction_type = 'usage' AND balance_type = 'balance'`,
		userID).Scan(&usageTx); err != nil {
		t.Fatalf("select usage transactions: %v", err)
	}
	if err := d.Pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount), 0) FROM balance_transactions WHERE user_id = $1::uuid AND transaction_type = 'refund'`,
		userID).Scan(&refundTx); err != nil {
		t.Fatalf("select refund transactions: %v", err)
	}
	if usageTx != -560_000 {
		t.Errorf("usage transaction amount = %d, want -560000", usageTx)
	}
	if refundTx != 560_000 {
		t.Errorf("refund transaction amount = %d, want 560000", refundTx)
	}
}

// TestProcessRecord_RefundCreditsBalance verifies the direct (non-queued)
// path: a negative-cost status=refund record credits the user's balance via
// applyBalanceRefund instead of being silently dropped by the >0 guards.
func TestProcessRecord_RefundCreditsBalance(t *testing.T) {
	d := testWebhookBillingDB(t)
	defer d.Close()
	r := newTestWebhookRoutes(t, d)
	ctx := context.Background()

	userID := uuid.NewString()
	keyHash := "test-refund-direct-" + fmt.Sprintf("%d", time.Now().UnixNano())
	cleanup := func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM usage_records WHERE user_id = $1::uuid", userID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM balance_transactions WHERE user_id = $1::uuid", userID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM api_keys WHERE key_hash = $1", keyHash)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM users WHERE id = $1::uuid", userID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM model_pricing_rules WHERE provider = 'tokenrouter' AND api_model_name = 'kling-v3'")
	}
	cleanup()
	defer cleanup()

	_, err := d.Pool.Exec(ctx,
		`INSERT INTO model_pricing_rules (provider, region, api_model_name, billing_mode, prices, status, created_at, updated_at)
		 VALUES ('tokenrouter', '*', 'kling-v3', 'flat', '{"fixed_request": 0.56}', 'active', NOW(), NOW())`)
	if err != nil {
		t.Fatalf("insert pricing rule: %v", err)
	}

	const startBalance = int64(2_000_000)
	_, err = d.Pool.Exec(ctx,
		"INSERT INTO users (id, name, email, password_hash, role, balance, created_at, updated_at) VALUES ($1::uuid, $2, $3, $4, $5, $6, NOW(), NOW())",
		userID, "Refund Direct User", fmt.Sprintf("refund-direct-%d@example.com", time.Now().UnixNano()), "hash", "user", startBalance)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = d.Pool.Exec(ctx,
		`INSERT INTO api_keys (id, user_id, key_hash, key_prefix, name, status, key_type, created_at, updated_at)
		 VALUES (gen_random_uuid(), $1::uuid, $2, 'sk-refund', 'Refund Key', 'active', 'general', NOW(), NOW())`,
		userID, keyHash)
	if err != nil {
		t.Fatalf("insert api key: %v", err)
	}

	// 1. Dispatch charge.
	charge := &UsageRecord{
		TraceID: uuid.NewString(), APIKeyHash: keyHash, Model: "kling-v3", Provider: "tokenrouter", RequestCount: 1,
	}
	if err := r.processRecord(ctx, charge); err != nil {
		t.Fatalf("processRecord charge: %v", err)
	}
	var balance int64
	if err := d.Pool.QueryRow(ctx, "SELECT COALESCE(balance, 0) FROM users WHERE id = $1::uuid", userID).Scan(&balance); err != nil {
		t.Fatalf("select balance after charge: %v", err)
	}
	if balance != startBalance-560_000 {
		t.Fatalf("balance after charge = %d, want %d", balance, startBalance-560_000)
	}

	// 2. Refund record: balance must come back, refund audited.
	refund := &UsageRecord{
		TraceID: uuid.NewString(), APIKeyHash: keyHash, Model: "kling-v3", Provider: "tokenrouter",
		RequestCount: 1, Status: "refund",
	}
	if err := r.processRecord(ctx, refund); err != nil {
		t.Fatalf("processRecord refund: %v", err)
	}
	if err := d.Pool.QueryRow(ctx, "SELECT COALESCE(balance, 0) FROM users WHERE id = $1::uuid", userID).Scan(&balance); err != nil {
		t.Fatalf("select balance after refund: %v", err)
	}
	if balance != startBalance {
		t.Errorf("balance after refund = %d, want %d", balance, startBalance)
	}

	var refundTxCount int
	var refundAmount int64
	if err := d.Pool.QueryRow(ctx,
		`SELECT COUNT(*), COALESCE(SUM(amount), 0) FROM balance_transactions
		 WHERE user_id = $1::uuid AND transaction_type = 'refund' AND balance_type = 'balance'`, userID,
	).Scan(&refundTxCount, &refundAmount); err != nil {
		t.Fatalf("select refund transactions: %v", err)
	}
	if refundTxCount != 1 || refundAmount != 560_000 {
		t.Errorf("refund transactions = count %d amount %d, want 1 / 560000", refundTxCount, refundAmount)
	}

	var refundCost int64
	if err := d.Pool.QueryRow(ctx,
		"SELECT total_cost FROM usage_records WHERE trace_id = $1", refund.TraceID).Scan(&refundCost); err != nil {
		t.Fatalf("select refund usage row: %v", err)
	}
	if refundCost != -560_000 {
		t.Errorf("refund usage total_cost = %d, want -560000", refundCost)
	}
}

// TestProcessRecord_RefundIdempotentReplay verifies the usage_records
// ON CONFLICT boundary protects the refund path: replaying the same refund
// record (same trace_id + created_at) must not double-credit the balance.
func TestProcessRecord_RefundIdempotentReplay(t *testing.T) {
	d := testWebhookBillingDB(t)
	defer d.Close()
	r := newTestWebhookRoutes(t, d)
	ctx := context.Background()

	userID := uuid.NewString()
	keyHash := "test-refund-replay-" + fmt.Sprintf("%d", time.Now().UnixNano())
	cleanup := func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM usage_records WHERE user_id = $1::uuid", userID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM balance_transactions WHERE user_id = $1::uuid", userID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM api_keys WHERE key_hash = $1", keyHash)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM users WHERE id = $1::uuid", userID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM model_pricing_rules WHERE provider = 'tokenrouter' AND api_model_name = 'kling-v3'")
	}
	cleanup()
	defer cleanup()

	_, err := d.Pool.Exec(ctx,
		`INSERT INTO model_pricing_rules (provider, region, api_model_name, billing_mode, prices, status, created_at, updated_at)
		 VALUES ('tokenrouter', '*', 'kling-v3', 'flat', '{"fixed_request": 0.56}', 'active', NOW(), NOW())`)
	if err != nil {
		t.Fatalf("insert pricing rule: %v", err)
	}

	const startBalance = int64(2_000_000)
	_, err = d.Pool.Exec(ctx,
		"INSERT INTO users (id, name, email, password_hash, role, balance, created_at, updated_at) VALUES ($1::uuid, $2, $3, $4, $5, $6, NOW(), NOW())",
		userID, "Refund Replay User", fmt.Sprintf("refund-replay-%d@example.com", time.Now().UnixNano()), "hash", "user", startBalance)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = d.Pool.Exec(ctx,
		`INSERT INTO api_keys (id, user_id, key_hash, key_prefix, name, status, key_type, created_at, updated_at)
		 VALUES (gen_random_uuid(), $1::uuid, $2, 'sk-refund', 'Refund Key', 'active', 'general', NOW(), NOW())`,
		userID, keyHash)
	if err != nil {
		t.Fatalf("insert api key: %v", err)
	}

	// Timestamp is second-precision; add a margin so it is safely AFTER the
	// rule's effective_from (inserted above), otherwise the effective-window
	// filter excludes the rule for backdated records.
	ts := time.Now().UTC().Add(2 * time.Second).Format(time.RFC3339)
	refund := &UsageRecord{
		TraceID: uuid.NewString(), APIKeyHash: keyHash, Model: "kling-v3", Provider: "tokenrouter",
		RequestCount: 1, Status: "refund", Timestamp: ts,
	}
	if err := r.processRecord(ctx, refund); err != nil {
		t.Fatalf("processRecord refund first: %v", err)
	}
	// Replay the exact same record (webhook retry): must be a no-op.
	if err := r.processRecord(ctx, refund); err != nil {
		t.Fatalf("processRecord refund replay: %v", err)
	}

	var balance int64
	if err := d.Pool.QueryRow(ctx, "SELECT COALESCE(balance, 0) FROM users WHERE id = $1::uuid", userID).Scan(&balance); err != nil {
		t.Fatalf("select balance: %v", err)
	}
	if balance != startBalance+560_000 {
		t.Errorf("balance = %d, want %d (replay must not double-refund)", balance, startBalance+560_000)
	}

	var refundTxCount int
	if err := d.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM balance_transactions WHERE user_id = $1::uuid AND transaction_type = 'refund'`, userID,
	).Scan(&refundTxCount); err != nil {
		t.Fatalf("select refund transactions: %v", err)
	}
	if refundTxCount != 1 {
		t.Errorf("refund transaction count = %d, want 1", refundTxCount)
	}
}

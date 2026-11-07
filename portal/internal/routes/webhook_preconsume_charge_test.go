package routes

import (
	"context"
	"testing"

	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// TestChargeFromFreeze_Personal verifies the timeout pre-consume charge path
// for personal keys: the frozen amount is deducted (gift first, then balance)
// and the audit rows (usage_records + balance_transactions) are written in the
// same transaction. Regression test for the F1 fix — the INSERT previously
// referenced non-existent columns (api_key_hash, error_message) so every
// timeout charge rolled back and never landed.
func TestChargeFromFreeze_Personal(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), testutil.PostgresDSN())
	if err != nil {
		t.Fatalf("connect to test db: %v", err)
	}
	defer pool.Close()

	routes := &WebhookRoutes{db: &db.DB{Pool: pool}, logger: zap.NewNop()}
	ctx := context.Background()

	userID := "550e8400-e29b-41d4-a716-4466554400f1"
	cleanup := func() {
		_, _ = pool.Exec(ctx, `DELETE FROM usage_records WHERE user_id = $1::uuid`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM balance_transactions WHERE user_id = $1::uuid`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, userID)
	}
	cleanup()
	defer cleanup()

	_, err = pool.Exec(ctx,
		`INSERT INTO users (id, name, email, password_hash, role, balance, gift_balance, created_at, updated_at)
		 VALUES ($1::uuid, 'Timeout Charge User', 'timeout-charge-f1@example.com', 'hash', 'user', 1000, 300, NOW(), NOW())`,
		userID)
	if err != nil {
		t.Fatalf("user insert failed: %v", err)
	}

	var requestID string
	if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&requestID); err != nil {
		t.Fatalf("generate request id: %v", err)
	}

	fd := &freezeData{
		UserID:   userID,
		KeyHash:  "test-key-hash-f1",
		Model:    "gpt-4o",
		Provider: "openai",
		Amount:   500,
	}
	charged, err := routes.chargeFromFreeze(ctx, fd, requestID, 123)
	if err != nil {
		t.Fatalf("chargeFromFreeze failed: %v", err)
	}
	if charged != 500 {
		t.Errorf("charged = %d, want 500", charged)
	}

	// Gift deducted first (300), then main balance (200).
	var giftBalance, balance int64
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(gift_balance, 0), COALESCE(balance, 0) FROM users WHERE id = $1::uuid`, userID,
	).Scan(&giftBalance, &balance); err != nil {
		t.Fatalf("select balances: %v", err)
	}
	if giftBalance != 0 || balance != 800 {
		t.Errorf("balances after charge: gift=%d balance=%d, want gift=0 balance=800", giftBalance, balance)
	}

	// The usage_records audit row must have landed with the real column names.
	var totalCost, latencyMs, requestCount int64
	var giftDeduction, balanceDeduction float64 // DECIMAL(18,10) columns
	var errText string
	err = pool.QueryRow(ctx,
		`SELECT total_cost, gift_deduction, balance_deduction, request_count, COALESCE(latency_ms, 0), COALESCE(error, '')
		 FROM usage_records WHERE trace_id = $1::uuid`, requestID,
	).Scan(&totalCost, &giftDeduction, &balanceDeduction, &requestCount, &latencyMs, &errText)
	if err != nil {
		t.Fatalf("usage_records row missing (timeout charge did not land): %v", err)
	}
	if totalCost != 500 || giftDeduction != 300 || balanceDeduction != 200 || requestCount != 1 || latencyMs != 123 {
		t.Errorf("usage_records row = cost:%d gift:%v balance:%v count:%d latency:%d, want 500/300/200/1/123",
			totalCost, giftDeduction, balanceDeduction, requestCount, latencyMs)
	}
	if errText != "timeout_pre_consume_charge" {
		t.Errorf("usage_records.error = %q, want %q", errText, "timeout_pre_consume_charge")
	}

	// Balance transaction log: one gift row and one balance row.
	var txCount int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM balance_transactions WHERE reference_id = $1 AND transaction_type = 'usage'`, requestID,
	).Scan(&txCount); err != nil {
		t.Fatalf("select balance_transactions: %v", err)
	}
	if txCount != 2 {
		t.Errorf("balance_transactions rows = %d, want 2 (gift + balance)", txCount)
	}
}

// TestChargeFromFreeze_Enterprise verifies the enterprise branch of the
// timeout charge: enterprise credit is consumed, member credit_used is
// tracked, and the audit rows land in the same transaction.
func TestChargeFromFreeze_Enterprise(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), testutil.PostgresDSN())
	if err != nil {
		t.Fatalf("connect to test db: %v", err)
	}
	defer pool.Close()

	routes := &WebhookRoutes{db: &db.DB{Pool: pool}, logger: zap.NewNop()}
	ctx := context.Background()

	userID := "550e8400-e29b-41d4-a716-4466554400f2"
	enterpriseID := "550e8400-e29b-41d4-a716-4466554400f3"
	cleanup := func() {
		_, _ = pool.Exec(ctx, `DELETE FROM usage_records WHERE user_id = $1::uuid`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM balance_transactions WHERE user_id = $1::uuid`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM enterprises WHERE id = $1::uuid`, enterpriseID)
	}
	cleanup()
	defer cleanup()

	_, err = pool.Exec(ctx,
		`INSERT INTO enterprises (id, name, credit_total, credit_used, created_at, updated_at)
		 VALUES ($1::uuid, 'Timeout Charge Ent', 10000, 0, NOW(), NOW())`,
		enterpriseID)
	if err != nil {
		t.Fatalf("enterprise insert failed: %v", err)
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO users (id, name, email, password_hash, role, balance, created_at, updated_at)
		 VALUES ($1::uuid, 'Timeout Charge Member', 'timeout-charge-f1-ent@example.com', 'hash', 'user', 0, NOW(), NOW())`,
		userID)
	if err != nil {
		t.Fatalf("user insert failed: %v", err)
	}

	var requestID string
	if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&requestID); err != nil {
		t.Fatalf("generate request id: %v", err)
	}

	fd := &freezeData{
		UserID:       userID,
		KeyHash:      "test-key-hash-f1-ent",
		Model:        "gpt-4o",
		Provider:     "openai",
		Amount:       400,
		KeyType:      "enterprise",
		EnterpriseID: enterpriseID,
	}
	charged, err := routes.chargeFromFreeze(ctx, fd, requestID, 250)
	if err != nil {
		t.Fatalf("chargeFromFreeze failed: %v", err)
	}
	if charged != 400 {
		t.Errorf("charged = %d, want 400", charged)
	}

	var creditUsed int64
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(credit_used, 0) FROM enterprises WHERE id = $1::uuid`, enterpriseID,
	).Scan(&creditUsed); err != nil {
		t.Fatalf("select enterprise credit: %v", err)
	}
	if creditUsed != 400 {
		t.Errorf("enterprise credit_used = %d, want 400", creditUsed)
	}

	var memberCreditUsed int64
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(credit_used, 0) FROM users WHERE id = $1::uuid`, userID,
	).Scan(&memberCreditUsed); err != nil {
		t.Fatalf("select member credit_used: %v", err)
	}
	if memberCreditUsed != 400 {
		t.Errorf("member credit_used = %d, want 400", memberCreditUsed)
	}

	var totalCost, latencyMs int64
	var creditDeduction float64 // DECIMAL(18,10) column
	var errText string
	err = pool.QueryRow(ctx,
		`SELECT total_cost, credit_deduction, COALESCE(latency_ms, 0), COALESCE(error, '')
		 FROM usage_records WHERE trace_id = $1::uuid`, requestID,
	).Scan(&totalCost, &creditDeduction, &latencyMs, &errText)
	if err != nil {
		t.Fatalf("usage_records row missing (timeout charge did not land): %v", err)
	}
	if totalCost != 400 || creditDeduction != 400 || latencyMs != 250 {
		t.Errorf("usage_records row = cost:%d credit:%v latency:%d, want 400/400/250",
			totalCost, creditDeduction, latencyMs)
	}
	if errText != "timeout_pre_consume_charge" {
		t.Errorf("usage_records.error = %q, want %q", errText, "timeout_pre_consume_charge")
	}

	var txCount int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM balance_transactions WHERE reference_id = $1 AND balance_type = 'credit'`, requestID,
	).Scan(&txCount); err != nil {
		t.Fatalf("select balance_transactions: %v", err)
	}
	if txCount != 1 {
		t.Errorf("balance_transactions credit rows = %d, want 1", txCount)
	}
}

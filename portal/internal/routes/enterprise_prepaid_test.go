package routes

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// TestEnterprisePrepaidFirst_Deduction verifies the prepaid-first deduction
// order in the pre-consume batch path: the prepaid wallet is drained before
// the monthly allowance (credit_total/credit_used with the 110% buffer).
func TestEnterprisePrepaidFirst_Deduction(t *testing.T) {
	d := testWebhookBillingDB(t)
	defer d.Close()
	r := newTestWebhookRoutes(t, d)
	ctx := context.Background()

	// Pricing rule from setup: input=$2/M, output=$4/M → 1 input token = 2 micro-USD.
	// Enterprise: prepaid=1000µ$, credit_total=1000µ$, credit_used=0.
	enterpriseID, userID, keyHash, modelProvider, modelName, cleanup := setupEnterpriseTestData(t, d, r, ctx,
		0,    /* balance */
		0,    /* gift_balance */
		1000, /* credit_total */
		0,    /* credit_used */
	)
	defer cleanup()

	if _, err := d.Pool.Exec(ctx,
		`UPDATE enterprises SET prepaid_balance = 1000 WHERE id = $1::uuid`, enterpriseID); err != nil {
		t.Fatalf("set prepaid_balance: %v", err)
	}

	// First deduction: 750 input tokens × $2/M = 1500µ$ → prepaid covers 1000,
	// allowance covers the remaining 500.
	records := []*UsageRecord{{
		TraceID:      uuid.NewString(),
		APIKeyHash:   keyHash,
		Model:        modelName,
		Provider:     modelProvider,
		InputTokens:  750,
		OutputTokens: 0,
		RequestCount: 1,
		PricingRatio: 1.0,
	}}
	if err := r.processBillingBatch(ctx, userID, records); err != nil {
		t.Fatalf("processBillingBatch failed: %v", err)
	}

	var prepaid, creditUsed int64
	if err := d.Pool.QueryRow(ctx,
		`SELECT COALESCE(prepaid_balance, 0), COALESCE(credit_used, 0) FROM enterprises WHERE id = $1::uuid`,
		enterpriseID).Scan(&prepaid, &creditUsed); err != nil {
		t.Fatalf("select enterprise balances: %v", err)
	}
	if prepaid != 0 {
		t.Errorf("prepaid_balance = %d, want 0 (fully drained)", prepaid)
	}
	if creditUsed != 500 {
		t.Errorf("credit_used = %d, want 500 (1500 cost - 1000 prepaid)", creditUsed)
	}

	// Second deduction: 350 input tokens = 700µ$; prepaid is empty so the
	// allowance applies with the 110% buffer: headroom = 1000 + 100 - 500 = 600,
	// the remaining 100 is absorbed by the platform (existing semantics).
	records2 := []*UsageRecord{{
		TraceID:      uuid.NewString(),
		APIKeyHash:   keyHash,
		Model:        modelName,
		Provider:     modelProvider,
		InputTokens:  350,
		OutputTokens: 0,
		RequestCount: 1,
		PricingRatio: 1.0,
	}}
	if err := r.processBillingBatch(ctx, userID, records2); err != nil {
		t.Fatalf("processBillingBatch (second) failed: %v", err)
	}

	if err := d.Pool.QueryRow(ctx,
		`SELECT COALESCE(prepaid_balance, 0), COALESCE(credit_used, 0) FROM enterprises WHERE id = $1::uuid`,
		enterpriseID).Scan(&prepaid, &creditUsed); err != nil {
		t.Fatalf("select enterprise balances (second): %v", err)
	}
	if prepaid != 0 {
		t.Errorf("prepaid_balance = %d, want 0", prepaid)
	}
	if creditUsed != 1100 {
		t.Errorf("credit_used = %d, want 1100 (500 + 600 headroom, 100 overflow absorbed)", creditUsed)
	}
}

// TestEnterpriseRefundLIFO verifies refunds unwind in the reverse of the
// deduction order: the monthly allowance is repaid first, then the prepaid wallet.
// The record leaves Timestamp empty on purpose: parseUsageTimestamp falls back to
// time.Now(), while a second-truncated RFC3339 value can land before the pricing
// rule's effective_from and fall through to the global fallback rule.
func TestEnterpriseRefundLIFO(t *testing.T) {
	d := testWebhookBillingDB(t)
	defer d.Close()
	r := newTestWebhookRoutes(t, d)
	ctx := context.Background()

	enterpriseID, _, keyHash, modelProvider, modelName, cleanup := setupEnterpriseTestData(t, d, r, ctx,
		0,    /* balance */
		0,    /* gift_balance */
		1000, /* credit_total */
		0,    /* credit_used */
	)
	defer cleanup()

	// Simulate prior usage: allowance used 300µ$, prepaid empty.
	if _, err := d.Pool.Exec(ctx,
		`UPDATE enterprises SET credit_used = 300, prepaid_balance = 0 WHERE id = $1::uuid`, enterpriseID); err != nil {
		t.Fatalf("set initial balances: %v", err)
	}

	// Refund record: -250 input tokens × $2/M = -500µ$ (negative cost), status=refund.
	record := &UsageRecord{
		TraceID:      uuid.NewString(),
		APIKeyHash:   keyHash,
		Model:        modelName,
		Provider:     modelProvider,
		InputTokens:  -250,
		OutputTokens: 0,
		RequestCount: 1,
		PricingRatio: 1.0,
		Status:       "refund",
	}
	if err := r.processRecord(ctx, record); err != nil {
		t.Fatalf("processRecord refund failed: %v", err)
	}

	var prepaid, creditUsed int64
	if err := d.Pool.QueryRow(ctx,
		`SELECT COALESCE(prepaid_balance, 0), COALESCE(credit_used, 0) FROM enterprises WHERE id = $1::uuid`,
		enterpriseID).Scan(&prepaid, &creditUsed); err != nil {
		t.Fatalf("select enterprise balances: %v", err)
	}
	if creditUsed != 0 {
		t.Errorf("credit_used = %d, want 0 (allowance repaid first: 300 of 500)", creditUsed)
	}
	if prepaid != 200 {
		t.Errorf("prepaid_balance = %d, want 200 (refund remainder lands in the wallet)", prepaid)
	}
}

// TestEnterpriseRefundLIFO_BatchPath is the P1-1 regression guard. The batch
// path (processBillingBatch) is the LIVE primary billing path. Before the fix it
// refunded enterprise credit with `credit_used = GREATEST(credit_used - $1, 0)`,
// which silently discarded the portion that should return to prepaid_balance —
// permanently losing real prepaid money on failed charge-on-dispatch media tasks.
// This test charges entirely from prepaid, then refunds via the batch path and
// asserts the prepaid wallet is made whole.
func TestEnterpriseRefundLIFO_BatchPath(t *testing.T) {
	d := testWebhookBillingDB(t)
	defer d.Close()
	r := newTestWebhookRoutes(t, d)
	ctx := context.Background()

	enterpriseID, userID, keyHash, modelProvider, modelName, cleanup := setupEnterpriseTestData(t, d, r, ctx,
		0,    /* balance */
		0,    /* gift_balance */
		1000, /* credit_total */
		0,    /* credit_used */
	)
	defer cleanup()

	// Charge-on-dispatch state: the whole 500µ$ came from prepaid (allowance
	// untouched). prepaid already drained to 0, credit_used still 0.
	if _, err := d.Pool.Exec(ctx,
		`UPDATE enterprises SET credit_used = 0, prepaid_balance = 0 WHERE id = $1::uuid`, enterpriseID); err != nil {
		t.Fatalf("set initial balances: %v", err)
	}

	// Task fails upstream → batch refund of the full dispatch charge (-500µ$).
	records := []*UsageRecord{{
		TraceID:      uuid.NewString(),
		APIKeyHash:   keyHash,
		Model:        modelName,
		Provider:     modelProvider,
		InputTokens:  250, // recomputed cost ×$2/M = 500µ$; Status=refund negates it
		OutputTokens: 0,
		RequestCount: 1,
		PricingRatio: 1.0,
		Status:       "refund",
	}}
	if err := r.processBillingBatch(ctx, userID, records); err != nil {
		t.Fatalf("processBillingBatch refund failed: %v", err)
	}

	var prepaid, creditUsed int64
	if err := d.Pool.QueryRow(ctx,
		`SELECT COALESCE(prepaid_balance, 0), COALESCE(credit_used, 0) FROM enterprises WHERE id = $1::uuid`,
		enterpriseID).Scan(&prepaid, &creditUsed); err != nil {
		t.Fatalf("select enterprise balances: %v", err)
	}
	// credit_used was 0, so the LIFO split sends the entire refund to prepaid.
	if creditUsed != 0 {
		t.Errorf("credit_used = %d, want 0", creditUsed)
	}
	if prepaid != 500 {
		t.Errorf("prepaid_balance = %d, want 500 (full refund restored to the wallet, not lost)", prepaid)
	}
}

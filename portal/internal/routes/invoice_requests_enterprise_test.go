package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// invoiceTestDB connects to the shared test database (schema initialized by TestMain).
func invoiceTestDB(t *testing.T) (*db.DB, *pgxpool.Pool) {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), testutil.PostgresDSN())
	if err != nil {
		t.Fatalf("connect to test db: %v", err)
	}
	t.Cleanup(pool.Close)
	return &db.DB{Pool: pool}, pool
}

// invoiceTestContext builds a gin context with an optional JSON body for handler tests.
func invoiceTestContext(method, path string, body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, w
}

// seedEnterpriseInvoiceFixture creates an enterprise, a member user and an admin user.
func seedEnterpriseInvoiceFixture(t *testing.T, pool *pgxpool.Pool) (entID, memberID, adminID, plainID string) {
	t.Helper()
	ctx := context.Background()
	entID = uuid.New().String()
	memberID = uuid.New().String()
	adminID = uuid.New().String()
	plainID = uuid.New().String()

	if _, err := pool.Exec(ctx,
		`INSERT INTO enterprises (id, name, status) VALUES ($1::uuid, $2, 'active')`,
		entID, "InvoiceTest Ent "+entID[:8]); err != nil {
		t.Fatalf("insert enterprise: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (id, name, email, password_hash, role, balance, enterprise_id, created_at, updated_at)
		 VALUES ($1::uuid, $2, $3, 'hash', 'enterprise_admin', 0, $4::uuid, NOW(), NOW())`,
		memberID, "Ent Admin", "ent-admin-"+memberID[:8]+"@test.com", entID); err != nil {
		t.Fatalf("insert enterprise admin: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (id, name, email, password_hash, role, balance, enterprise_id, created_at, updated_at)
		 VALUES ($1::uuid, $2, $3, 'hash', 'user', 0, $4::uuid, NOW(), NOW())`,
		plainID, "Plain Member", "ent-plain-"+plainID[:8]+"@test.com", entID); err != nil {
		t.Fatalf("insert plain member: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (id, name, email, password_hash, role, balance, created_at, updated_at)
		 VALUES ($1::uuid, $2, $3, 'hash', 'system_admin', 0, NOW(), NOW())`,
		adminID, "Sys Admin", "sys-admin-"+adminID[:8]+"@test.com"); err != nil {
		t.Fatalf("insert admin: %v", err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM invoice_requests WHERE enterprise_id = $1::uuid OR user_id = ANY($2::uuid[])`,
			entID, []string{memberID, adminID, plainID})
		_, _ = pool.Exec(context.Background(), `DELETE FROM enterprise_audit_log WHERE enterprise_id = $1::uuid`, entID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1::uuid[])`, []string{memberID, adminID, plainID})
		_, _ = pool.Exec(context.Background(), `DELETE FROM enterprises WHERE id = $1::uuid`, entID)
	})
	return entID, memberID, adminID, plainID
}

func postInvoiceRequest(t *testing.T, ir *InvoiceRoutes, userID string, body string) (int, map[string]interface{}) {
	t.Helper()
	c, w := invoiceTestContext(http.MethodPost, "/api/invoices/requests", []byte(body))
	c.Set("user_id", userID)
	ir.createRequest(c)
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w.Code, resp
}

func getInvoiceEligibility(t *testing.T, ir *InvoiceRoutes, userID string) (int, map[string]interface{}) {
	t.Helper()
	c, w := invoiceTestContext(http.MethodGet, "/api/invoices/eligibility", nil)
	c.Set("user_id", userID)
	ir.getEligibility(c)
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w.Code, resp
}

// TestEnterpriseInvoiceRequestFlow covers the full enterprise invoice lifecycle:
// submit (no profile_id) → over-eligible rejected → admin approve+complete
// (invoice_number visible) → second request rejected (rejection_reason visible).
func TestEnterpriseInvoiceRequestFlow(t *testing.T) {
	database, pool := invoiceTestDB(t)
	ctx := context.Background()
	entID, memberID, _, plainID := seedEnterpriseInvoiceFixture(t, pool)

	ir := &InvoiceRoutes{db: database, logger: zap.NewNop()}
	admin := &AdminRoutes{db: database, logger: zap.NewNop()}

	// $300 + $100 monthly statements (micro-USD) and one already-invoiced $50
	// request against the 2026-09 period (cents).
	for _, st := range []struct {
		period string
		credit int64
	}{
		{"2026-09", 300_000_000},
		{"2026-10", 100_000_000},
		{"2026-11", 250_000_000},
	} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO enterprise_statements (enterprise_id, period, credit_used_micro)
			 VALUES ($1::uuid, $2, $3)`, entID, st.period, st.credit); err != nil {
			t.Fatalf("seed statement %s: %v", st.period, err)
		}
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO invoice_requests (user_id, enterprise_id, amount, title, status, pool, period)
		 VALUES ($1::uuid, $2::uuid, 5000, 'Old invoice', 'completed', 'postpaid', '2026-09')`,
		memberID, entID); err != nil {
		t.Fatalf("seed completed invoice request: %v", err)
	}
	// eligible = un-invoiced 2026-10 statement ($10000) = 10000 cents

	// 0. Regular enterprise members no longer hit the enterprise path: without
	// profile_id the personal path rejects the request (400, not 403).
	code, _ := postInvoiceRequest(t, ir, plainID, `{"amount_cents":10000}`)
	if code != http.StatusBadRequest {
		t.Fatalf("plain member request without profile: got status %d, want 400", code)
	}

	// 1. Over-eligible request is rejected (eligible is $600 across statements).
	code, _ = postInvoiceRequest(t, ir, memberID, `{"amount_cents":70000,"title":"Too big"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("over-eligible request: got status %d, want 400", code)
	}

	// 2. Valid request without profile_id succeeds (enterprise path).
	code, resp := postInvoiceRequest(t, ir, memberID, `{"amount_cents":10000,"delivery_email":"fin@test.com"}`)
	if code != http.StatusOK {
		t.Fatalf("enterprise request without profile_id: got status %d, resp %v", code, resp)
	}
	reqID, _ := resp["id"].(string)
	if reqID == "" {
		t.Fatalf("expected id in response, got %v", resp)
	}
	// Title defaults to the verified enterprise name; enterprise_id is recorded.
	var title, storedEntID string
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(title, ''), COALESCE(enterprise_id::text, '') FROM invoice_requests WHERE id = $1::uuid`,
		reqID).Scan(&title, &storedEntID); err != nil {
		t.Fatalf("query stored request: %v", err)
	}
	if storedEntID != entID {
		t.Errorf("stored enterprise_id = %q, want %q", storedEntID, entID)
	}
	if title != "InvoiceTest Ent "+entID[:8] {
		t.Errorf("title = %q, want enterprise name %q", title, "InvoiceTest Ent "+entID[:8])
	}

	// 3. Admin sees the request with enterprise identification.
	c, w := invoiceTestContext(http.MethodGet, "/api/admin/invoice-requests", nil)
	admin.listInvoiceRequests(c)
	if w.Code != http.StatusOK {
		t.Fatalf("admin list: got status %d", w.Code)
	}
	var listResp struct {
		Requests []map[string]interface{} `json:"requests"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode admin list: %v", err)
	}
	var found map[string]interface{}
	for _, r := range listResp.Requests {
		if r["id"] == reqID {
			found = r
		}
	}
	if found == nil {
		t.Fatalf("admin list does not contain request %s", reqID)
	}
	if found["enterprise_id"] != entID || found["enterprise_name"] == "" {
		t.Errorf("admin list entry missing enterprise identification: %v", found)
	}
	if found["amount_cents"].(float64) != 10000 {
		t.Errorf("amount_cents = %v, want 10000", found["amount_cents"])
	}

	// 4. Admin approves (pending → processing) then completes with invoice number.
	c, w = invoiceTestContext(http.MethodPost, "/approve", nil)
	c.Params = gin.Params{{Key: "id", Value: reqID}}
	admin.approveInvoiceRequest(c)
	if w.Code != http.StatusOK {
		t.Fatalf("approve: got status %d", w.Code)
	}
	c, w = invoiceTestContext(http.MethodPost, "/complete", []byte(fmt.Sprintf(`{"invoice_number":"INV-TEST-%s"}`, entID[:8])))
	c.Params = gin.Params{{Key: "id", Value: reqID}}
	admin.completeInvoiceRequest(c)
	if w.Code != http.StatusOK {
		t.Fatalf("complete: got status %d", w.Code)
	}

	// 5. Member listRequests exposes the invoice number after issuance.
	invoiceNumber := listRequestField(t, ir, memberID, reqID, "invoice_number")
	if invoiceNumber != "INV-TEST-"+entID[:8] {
		t.Errorf("listRequests invoice_number = %q, want INV-TEST-%s", invoiceNumber, entID[:8])
	}

	// 6. Second request: the 2026-10 statement is now invoiced; the un-invoiced
	// 2026-11 statement ($250) still allows a $50 request, which admin rejects.
	code, resp = postInvoiceRequest(t, ir, memberID, `{"amount_cents":5000}`)
	if code != http.StatusOK {
		t.Fatalf("second request: got status %d, resp %v", code, resp)
	}
	reqID2, _ := resp["id"].(string)
	if reqID2 == "" {
		t.Fatalf("expected id for second request, got %v", resp)
	}
	c, w = invoiceTestContext(http.MethodPost, "/reject", []byte(`{"reason":"amount mismatch with contract"}`))
	c.Params = gin.Params{{Key: "id", Value: reqID2}}
	admin.rejectInvoiceRequest(c)
	if w.Code != http.StatusOK {
		t.Fatalf("reject: got status %d", w.Code)
	}

	// 7. Member listRequests exposes the rejection reason.
	reason := listRequestField(t, ir, memberID, reqID2, "rejection_reason")
	if reason != "amount mismatch with contract" {
		t.Errorf("listRequests rejection_reason = %q, want %q", reason, "amount mismatch with contract")
	}
}

// listRequestField returns a field of a specific request from the member's listRequests.
func listRequestField(t *testing.T, ir *InvoiceRoutes, userID, reqID, field string) string {
	t.Helper()
	c, w := invoiceTestContext(http.MethodGet, "/api/invoices/requests?scope=enterprise", nil)
	c.Set("user_id", userID)
	ir.listRequests(c)
	if w.Code != http.StatusOK {
		t.Fatalf("listRequests: got status %d", w.Code)
	}
	var resp struct {
		Requests []map[string]interface{} `json:"requests"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode listRequests: %v", err)
	}
	for _, r := range resp.Requests {
		if r["id"] == reqID {
			v, _ := r[field].(string)
			return v
		}
	}
	t.Fatalf("request %s not found in listRequests", reqID)
	return ""
}

// TestEnterpriseInvoiceEligibility verifies the two-pool formula:
// postpaid = un-invoiced monthly statements minus pending; prepaid =
// consumed share of cumulative prepaid top-ups (top-ups − wallet balance)
// minus invoiced. Personal-balance usage must not count in either pool.
func TestEnterpriseInvoiceEligibility(t *testing.T) {
	database, pool := invoiceTestDB(t)
	ctx := context.Background()
	entID, memberID, _, _ := seedEnterpriseInvoiceFixture(t, pool)

	ir := &InvoiceRoutes{db: database, logger: zap.NewNop()}

	// Postpaid statements: 2026-09 ($200, already invoiced) and 2026-10
	// ($100, not invoiced). Only the un-invoiced period counts.
	for _, st := range []struct {
		period string
		credit int64
	}{
		{"2026-09", 200_000_000},
		{"2026-10", 100_000_000},
	} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO enterprise_statements (enterprise_id, period, credit_used_micro)
			 VALUES ($1::uuid, $2, $3)`, entID, st.period, st.credit); err != nil {
			t.Fatalf("seed statement %s: %v", st.period, err)
		}
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO invoice_requests (user_id, enterprise_id, amount, title, status, pool, period)
		 VALUES ($1::uuid, $2::uuid, 8000, 'Issued', 'completed', 'postpaid', '2026-09')`,
		memberID, entID); err != nil {
		t.Fatalf("seed completed invoice request: %v", err)
	}

	// Prepaid: $300 topped up, $120 left in the wallet → $180 consumed.
	if _, err := pool.Exec(ctx,
		`INSERT INTO enterprise_audit_log (enterprise_id, category, change_type, field_name, action, delta, amount)
		 VALUES ($1::uuid, 'billing', 'prepaid_topup', 'prepaid_balance', 'topup', $2, $2)`,
		entID, 300_000_000); err != nil {
		t.Fatalf("seed prepaid top-up: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE enterprises SET prepaid_balance = $2 WHERE id = $1::uuid`, entID, 120_000_000); err != nil {
		t.Fatalf("set prepaid_balance: %v", err)
	}

	el := ir.enterpriseInvoiceEligibility(ctx, entID)
	// 2026-09 net $12000 ($200 − $80 invoiced) + 2026-10 net $10000.
	if el.PostpaidEligibleCents != 22000 {
		t.Errorf("postpaidEligible = %d, want 22000", el.PostpaidEligibleCents)
	}
	if el.PrepaidEligibleCents != 18000 {
		t.Errorf("prepaidEligible = %d, want 18000", el.PrepaidEligibleCents)
	}
	if el.EligibleCents != 40000 {
		t.Errorf("eligibleCents = %d, want 40000", el.EligibleCents)
	}
	if el.AlreadyRequestedCents != 8000 {
		t.Errorf("alreadyRequestedCents = %d, want 8000", el.AlreadyRequestedCents)
	}

	// A pending postpaid request also consumes eligibility.
	if _, err := pool.Exec(ctx,
		`INSERT INTO invoice_requests (user_id, enterprise_id, amount, title, status, pool, period)
		 VALUES ($1::uuid, $2::uuid, 2000, 'Pending', 'pending', 'postpaid', '2026-10')`,
		memberID, entID); err != nil {
		t.Fatalf("seed pending invoice request: %v", err)
	}
	el = ir.enterpriseInvoiceEligibility(ctx, entID)
	if el.PostpaidEligibleCents != 20000 {
		t.Errorf("postpaidEligible after pending = %d, want 20000", el.PostpaidEligibleCents)
	}
	if el.EligibleCents != 38000 {
		t.Errorf("eligibleCents after pending = %d, want 38000", el.EligibleCents)
	}

	// Per-period check: 2026-10 has $10000 − $2000 pending = $8000 left.
	if got := ir.statementPeriodEligible(ctx, entID, "2026-10"); got != 8000 {
		t.Errorf("statementPeriodEligible(2026-10) = %d, want 8000", got)
	}
	// Partially invoiced period keeps its remainder open.
	if got := ir.statementPeriodEligible(ctx, entID, "2026-09"); got != 12000 {
		t.Errorf("statementPeriodEligible(2026-09) = %d, want 12000", got)
	}

	// Personal-balance usage must NOT count toward enterprise eligibility.
	if _, err := pool.Exec(ctx,
		`INSERT INTO balance_transactions (user_id, transaction_type, amount, balance_type, description)
		 VALUES ($1::uuid, 'usage', -50000000, 'balance', 'member personal usage')`,
		memberID); err != nil {
		t.Fatalf("seed balance transaction: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM balance_transactions WHERE user_id = $1::uuid`, memberID)
	})
	el = ir.enterpriseInvoiceEligibility(ctx, entID)
	if el.EligibleCents != 38000 {
		t.Errorf("eligibleCents affected by personal balance usage: got %d, want 38000", el.EligibleCents)
	}

	// Regression: a request WITHOUT a period must still consume eligibility.
	// The global (non-period-scoped) deduction exists precisely because a
	// period JOIN would let these slip through and allow duplicate requests.
	if _, err := pool.Exec(ctx,
		`INSERT INTO invoice_requests (user_id, enterprise_id, amount, title, status, pool, period)
		 VALUES ($1::uuid, $2::uuid, 500, 'No-period request', 'pending', 'postpaid', NULL)`,
		memberID, entID); err != nil {
		t.Fatalf("seed no-period request: %v", err)
	}
	el = ir.enterpriseInvoiceEligibility(ctx, entID)
	if el.PostpaidEligibleCents != 19500 {
		t.Errorf("postpaidEligible with no-period request = %d, want 19500", el.PostpaidEligibleCents)
	}
}

// TestEnterpriseMemberPersonalInvoice covers regular enterprise members
// (role='user' with enterprise_id set) invoicing their personal real-fund
// consumption: success within personal eligible, rejection beyond it, and no
// consumption of the enterprise invoice quota in either direction.
func TestEnterpriseMemberPersonalInvoice(t *testing.T) {
	database, pool := invoiceTestDB(t)
	ctx := context.Background()
	entID, memberID, _, plainID := seedEnterpriseInvoiceFixture(t, pool)

	ir := &InvoiceRoutes{db: database, logger: zap.NewNop()}

	// Enterprise pool has a $200 un-invoiced monthly statement; the member
	// personally consumed $50 of real-fund balance (micro-USD usage against
	// balance_type='balance').
	if _, err := pool.Exec(ctx,
		`INSERT INTO enterprise_statements (enterprise_id, period, credit_used_micro)
		 VALUES ($1::uuid, '2026-10', $2)`, entID, 200_000_000); err != nil {
		t.Fatalf("seed enterprise statement: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO balance_transactions (user_id, transaction_type, amount, balance_type, description)
		 VALUES ($1::uuid, 'usage', -50000000, 'balance', 'member personal usage')`,
		plainID); err != nil {
		t.Fatalf("seed balance transaction: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM balance_transactions WHERE user_id = $1::uuid`, plainID)
	})

	// Personal invoice profile for the member (personal path requires profile_id).
	var profileID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO invoice_profiles (user_id, name, type)
		 VALUES ($1::uuid, 'Member Personal', 'personal') RETURNING id::text`,
		plainID).Scan(&profileID); err != nil {
		t.Fatalf("insert invoice profile: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM invoice_profiles WHERE id = $1::uuid`, profileID)
	})

	// 1. Over personal eligible ($50 = 5000 cents) is rejected.
	code, _ := postInvoiceRequest(t, ir, plainID,
		fmt.Sprintf(`{"amount_cents":6000,"profile_id":%q,"delivery_email":"m@test.com"}`, profileID))
	if code != http.StatusBadRequest {
		t.Fatalf("over personal eligible: got status %d, want 400", code)
	}

	// 2. Within personal eligible succeeds and is stored as a personal request
	// (enterprise_id stays NULL).
	code, resp := postInvoiceRequest(t, ir, plainID,
		fmt.Sprintf(`{"amount_cents":3000,"profile_id":%q,"delivery_email":"m@test.com"}`, profileID))
	if code != http.StatusOK {
		t.Fatalf("member personal request: got status %d, resp %v", code, resp)
	}
	reqID, _ := resp["id"].(string)
	if reqID == "" {
		t.Fatalf("expected id in response, got %v", resp)
	}
	var storedEntID string
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(enterprise_id::text, '') FROM invoice_requests WHERE id = $1::uuid`,
		reqID).Scan(&storedEntID); err != nil {
		t.Fatalf("query stored request: %v", err)
	}
	if storedEntID != "" {
		t.Errorf("personal request stored enterprise_id = %q, want empty", storedEntID)
	}

	// 3. The personal request does not consume the enterprise invoice quota:
	// enterprise eligible is still the full $200 statement and the EA can
	// request it.
	el := ir.enterpriseInvoiceEligibility(ctx, entID)
	if el.EligibleCents != 20000 {
		t.Errorf("enterprise eligible after member personal request = %d, want 20000", el.EligibleCents)
	}
	code, resp = postInvoiceRequest(t, ir, memberID, `{"amount_cents":20000,"delivery_email":"fin@test.com"}`)
	if code != http.StatusOK {
		t.Fatalf("enterprise request after member personal request: got status %d, resp %v", code, resp)
	}

	// 4. The EA's enterprise request does not consume the member's personal
	// eligible either: the remaining $20 (5000 - 3000) is still requestable.
	code, resp = postInvoiceRequest(t, ir, plainID,
		fmt.Sprintf(`{"amount_cents":2000,"profile_id":%q,"delivery_email":"m@test.com"}`, profileID))
	if code != http.StatusOK {
		t.Fatalf("member remaining personal request: got status %d, resp %v", code, resp)
	}
}

// TestGetInvoiceEligibility verifies the /api/invoices/eligibility endpoint
// returns personal eligibility for regular members and enterprise eligibility
// for enterprise admins.
func TestGetInvoiceEligibility(t *testing.T) {
	database, pool := invoiceTestDB(t)
	ctx := context.Background()
	entID, memberID, _, plainID := seedEnterpriseInvoiceFixture(t, pool)

	ir := &InvoiceRoutes{db: database, logger: zap.NewNop()}

	// Enterprise pool has a $200 un-invoiced monthly statement; the member
	// personally consumed $50.
	if _, err := pool.Exec(ctx,
		`INSERT INTO enterprise_statements (enterprise_id, period, credit_used_micro)
		 VALUES ($1::uuid, '2026-10', $2)`, entID, 200_000_000); err != nil {
		t.Fatalf("seed enterprise statement: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO balance_transactions (user_id, transaction_type, amount, balance_type, description)
		 VALUES ($1::uuid, 'usage', -50000000, 'balance', 'member personal usage')`,
		plainID); err != nil {
		t.Fatalf("seed balance transaction: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM balance_transactions WHERE user_id = $1::uuid`, plainID)
	})

	// Regular member sees personal eligibility: $50 consumption = 5000 cents.
	code, resp := getInvoiceEligibility(t, ir, plainID)
	if code != http.StatusOK {
		t.Fatalf("member eligibility: got status %d, resp %v", code, resp)
	}
	if resp["type"] != "personal" {
		t.Errorf("member eligibility type = %v, want personal", resp["type"])
	}
	if resp["eligible_cents"] != float64(5000) {
		t.Errorf("member eligible_cents = %v, want 5000", resp["eligible_cents"])
	}

	// Enterprise admin sees enterprise eligibility: $200 un-invoiced statement
	// = 20000 cents (postpaid pool).
	code, resp = getInvoiceEligibility(t, ir, memberID)
	if code != http.StatusOK {
		t.Fatalf("admin eligibility: got status %d, resp %v", code, resp)
	}
	if resp["type"] != "enterprise" {
		t.Errorf("admin eligibility type = %v, want enterprise", resp["type"])
	}
	if resp["eligible_cents"] != float64(20000) {
		t.Errorf("admin eligible_cents = %v, want 20000", resp["eligible_cents"])
	}
	if resp["postpaid_eligible_cents"] != float64(20000) {
		t.Errorf("admin postpaid_eligible_cents = %v, want 20000", resp["postpaid_eligible_cents"])
	}
}

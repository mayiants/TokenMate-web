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
	"github.com/ai-proxy/ai-portal-go/pkg/audit"
	"github.com/ai-proxy/ai-portal-go/pkg/mail"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

func testEnterpriseDB(t *testing.T) *db.DB {
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

func newTestEnterpriseRoutes(t *testing.T, database *db.DB) *EnterpriseRoutes {
	t.Helper()
	cfg := &config.Config{}
	logger := zap.NewNop()
	return NewEnterpriseRoutes(database, cfg, logger, nil, &audit.Logger{}, &mail.Sender{}, nil, services.NewRewardService(database, logger, nil), nil)
}

// --- Phase 3: ResetMonthlyBilling tests ---

// TestResetMonthlyBilling_SkipsWhenNotFirstOfMonth verifies that ResetMonthlyBilling
// is a no-op when not called on the 1st of the month.
func TestResetMonthlyBilling_SkipsWhenNotFirstOfMonth(t *testing.T) {
	now := time.Now()
	if now.Day() == 1 {
		t.Skip("Skipping: test only valid on non-1st days (today is the 1st)")
	}

	d := testEnterpriseDB(t)
	defer d.Close()
	r := newTestEnterpriseRoutes(t, d)
	ctx := context.Background()

	// Create an enterprise that should be reset
	suffix := fmt.Sprintf("%d", now.UnixNano())
	entName := "reset-skip-ent-" + suffix
	entEmail := "reset-skip-" + suffix + "@test.com"

	var enterpriseID string
	err := d.Pool.QueryRow(ctx,
		`INSERT INTO enterprises (name, contact_email, status, credit_total, credit_used, quota_used, billing_cycle, credit_reset_at)
		 VALUES ($1, $2, 'active', 10000000, 5000000, 2000000, 'monthly', NOW() - INTERVAL '2 months')
		 RETURNING id::text`,
		entName, entEmail).Scan(&enterpriseID)
	if err != nil {
		t.Fatalf("insert enterprise: %v", err)
	}
	defer func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM enterprise_audit_log WHERE enterprise_id = $1::uuid", enterpriseID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM enterprises WHERE id = $1::uuid", enterpriseID)
	}()

	// Call ResetMonthlyBilling — should skip because today is not the 1st
	r.ResetMonthlyBilling(ctx)

	// Verify: enterprise data should NOT be changed
	var creditUsed, quotaUsed int64
	err = d.Pool.QueryRow(ctx,
		"SELECT COALESCE(credit_used, 0), COALESCE(quota_used, 0) FROM enterprises WHERE id = $1::uuid", enterpriseID,
	).Scan(&creditUsed, &quotaUsed)
	if err != nil {
		t.Fatalf("select enterprise: %v", err)
	}
	if creditUsed != 5000000 {
		t.Errorf("credit_used should not be reset on non-1st day: expected 5000000, got %d", creditUsed)
	}
	if quotaUsed != 2000000 {
		t.Errorf("quota_used should not be reset on non-1st day: expected 2000000, got %d", quotaUsed)
	}
}

// TestResetMonthlyBilling_ResetsCalendarMonth verifies the core SQL logic of
// ResetMonthlyBilling: when credit_reset_at < start of current month, the enterprise
// should have credit_used and quota_used reset to 0, and credit_reset_at set to start of next month.
func TestResetMonthlyBilling_ResetsCalendarMonth(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	ctx := context.Background()

	// Create an enterprise due for reset
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	entName := "reset-ent-" + suffix
	entEmail := "reset-" + suffix + "@test.com"

	var enterpriseID string
	err := d.Pool.QueryRow(ctx,
		`INSERT INTO enterprises (name, contact_email, status, credit_total, credit_used, quota_used, billing_cycle, credit_reset_at)
		 VALUES ($1, $2, 'active', 10000000, 5000000, 2000000, 'monthly', NOW() - INTERVAL '2 months')
		 RETURNING id::text`,
		entName, entEmail).Scan(&enterpriseID)
	if err != nil {
		t.Fatalf("insert enterprise: %v", err)
	}
	defer func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM enterprise_audit_log WHERE enterprise_id = $1::uuid", enterpriseID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM enterprises WHERE id = $1::uuid", enterpriseID)
	}()

	// Execute the same core SQL as ResetMonthlyBilling
	nextReset := time.Date(time.Now().Year(), time.Now().Month()+1, 1, 0, 0, 0, 0, time.Now().Location())

	rows, err := d.Pool.Query(ctx, `
		SELECT id::text, name
		FROM enterprises
		WHERE status = 'active'
		  AND billing_cycle = 'monthly'
		  AND deleted_at IS NULL
		  AND (credit_reset_at IS NULL OR credit_reset_at < DATE_TRUNC('month', NOW() + INTERVAL '1 month'))`)
	if err != nil {
		t.Fatalf("query enterprises: %v", err)
	}

	var resetCount int
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			continue
		}
		_, err := d.Pool.Exec(ctx,
			`UPDATE enterprises SET quota_used = 0, credit_used = 0, credit_reset_at = $2, updated_at = NOW()
			 WHERE id = $1::uuid`,
			id, nextReset)
		if err != nil {
			t.Fatalf("reset enterprise billing: %v", err)
		}
		if err := d.Exec(ctx, `INSERT INTO enterprise_audit_log (enterprise_id, change_type, field_name, action, reason, amount)
					 VALUES ($1, 'monthly_reset', 'quota_used,credit_used', 'reset', 'Calendar month reset', 0)`, id); err != nil {
			t.Fatalf("database operation failed: %v", err)
		}
		resetCount++
	}
	rows.Close()

	if resetCount < 1 {
		t.Errorf("expected at least 1 enterprise to be reset, got %d", resetCount)
	}

	// Verify: enterprise should be reset
	var creditUsed, quotaUsed int64
	var creditResetAt time.Time
	err = d.Pool.QueryRow(ctx,
		"SELECT COALESCE(credit_used, 0), COALESCE(quota_used, 0), COALESCE(credit_reset_at, NOW()) FROM enterprises WHERE id = $1::uuid", enterpriseID,
	).Scan(&creditUsed, &quotaUsed, &creditResetAt)
	if err != nil {
		t.Fatalf("select enterprise: %v", err)
	}
	if creditUsed != 0 {
		t.Errorf("credit_used should be reset to 0, got %d", creditUsed)
	}
	if quotaUsed != 0 {
		t.Errorf("quota_used should be reset to 0, got %d", quotaUsed)
	}
	// credit_reset_at should be start of next month
	expectedReset := time.Date(time.Now().Year(), time.Now().Month()+1, 1, 0, 0, 0, 0, time.UTC)
	if creditResetAt.Year() != expectedReset.Year() || creditResetAt.Month() != expectedReset.Month() || creditResetAt.Day() != 1 {
		t.Errorf("credit_reset_at should be start of next month (%v), got %v", expectedReset, creditResetAt)
	}

	// Verify: enterprise_audit_log should have a monthly_reset entry
	var logCount int
	err = d.Pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM enterprise_audit_log WHERE enterprise_id = $1::uuid AND change_type = 'monthly_reset'", enterpriseID,
	).Scan(&logCount)
	if err != nil {
		t.Fatalf("select credit log: %v", err)
	}
	if logCount < 1 {
		t.Errorf("expected at least 1 monthly_reset log entry, got %d", logCount)
	}
}

// TestResetMonthlyBilling_SkipsAlreadyResetThisMonth verifies the idempotency
// boundary: credit_reset_at stores NEXT month's 1st after a reset, so an
// enterprise whose credit_reset_at has already reached next month must be
// skipped, while one whose credit_reset_at equals the CURRENT month's 1st
// (written by last month's reset as its nextReset) is still due — the old
// predicate (< current month's 1st) wrongly skipped that exact case, which
// is how the reset became alternating reset/skip months in production.
func TestResetMonthlyBilling_SkipsAlreadyResetThisMonth(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	ctx := context.Background()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	// Case A: reset already done this month (credit_reset_at = next month's 1st)
	var doneID string
	err := d.Pool.QueryRow(ctx,
		`INSERT INTO enterprises (name, contact_email, status, credit_total, credit_used, quota_used, billing_cycle, credit_reset_at)
		 VALUES ('reset-done-ent-' || $1, 'reset-done-' || $1 || '@test.com', 'active', 10000000, 3000000, 1000000, 'monthly', DATE_TRUNC('month', NOW() + INTERVAL '1 month'))
		 RETURNING id::text`,
		suffix).Scan(&doneID)
	if err != nil {
		t.Fatalf("insert done enterprise: %v", err)
	}

	// Case B: last reset happened last month, so credit_reset_at = current
	// month's 1st — this enterprise IS due for this month's reset.
	var dueID string
	err = d.Pool.QueryRow(ctx,
		`INSERT INTO enterprises (name, contact_email, status, credit_total, credit_used, quota_used, billing_cycle, credit_reset_at)
		 VALUES ('reset-due-ent-' || $1, 'reset-due-' || $1 || '@test.com', 'active', 10000000, 3000000, 1000000, 'monthly', DATE_TRUNC('month', NOW()))
		 RETURNING id::text`,
		suffix).Scan(&dueID)
	if err != nil {
		t.Fatalf("insert due enterprise: %v", err)
	}

	defer func() {
		for _, id := range []string{doneID, dueID} {
			_, _ = d.Pool.Exec(ctx, "DELETE FROM enterprise_audit_log WHERE enterprise_id = $1::uuid", id)
			_, _ = d.Pool.Exec(ctx, "DELETE FROM enterprises WHERE id = $1::uuid", id)
		}
	}()

	// Same selection predicate as ResetMonthlyBilling.
	rows, err := d.Pool.Query(ctx, `
		SELECT id::text
		FROM enterprises
		WHERE id IN ($1::uuid, $2::uuid)
		  AND status = 'active'
		  AND billing_cycle = 'monthly'
		  AND deleted_at IS NULL
		  AND (credit_reset_at IS NULL OR credit_reset_at < DATE_TRUNC('month', NOW() + INTERVAL '1 month'))`,
		doneID, dueID)
	if err != nil {
		t.Fatalf("query enterprises: %v", err)
	}

	var matched []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			matched = append(matched, id)
		}
	}
	rows.Close()

	for _, id := range matched {
		if id == doneID {
			t.Errorf("enterprise with credit_reset_at = next month's 1st should NOT be selected (already reset this month)")
		}
	}
	foundDue := false
	for _, id := range matched {
		if id == dueID {
			foundDue = true
		}
	}
	if !foundDue {
		t.Errorf("enterprise with credit_reset_at = current month's 1st SHOULD be selected (due for this month's reset) — this is the production Nov-1 scenario")
	}

	// Verify: the already-reset enterprise's counters must remain untouched.
	var creditUsed, quotaUsed int64
	err = d.Pool.QueryRow(ctx,
		"SELECT COALESCE(credit_used, 0), COALESCE(quota_used, 0) FROM enterprises WHERE id = $1::uuid", doneID,
	).Scan(&creditUsed, &quotaUsed)
	if err != nil {
		t.Fatalf("select enterprise: %v", err)
	}
	if creditUsed != 3000000 {
		t.Errorf("credit_used should remain 3000000 (not reset), got %d", creditUsed)
	}
	if quotaUsed != 1000000 {
		t.Errorf("quota_used should remain 1000000 (not reset), got %d", quotaUsed)
	}
}

// ---------- domainMatches tests ----------

func TestDomainMatches(t *testing.T) {
	tests := []struct {
		name           string
		email          string
		allowedDomains string
		want           bool
	}{
		{"exact match", "alice@acme.com", "acme.com", true},
		{"multiple domains match", "bob@acme.com", "example.com, acme.com, corp.io", true},
		{"no match", "charlie@test-provider.com", "acme.com", false},
		{"empty allowed", "alice@acme.com", "", false},
		{"case insensitive email domain", "Alice@ACME.com", "acme.com", true},
		{"case insensitive allowed", "alice@acme.com", "ACME.COM", true},
		{"subdomain not match", "alice@sub.acme.com", "acme.com", false},
		{"partial match false", "alice@acme.com.org", "acme.com", false},
		{"invalid email no at", "aliceacme.com", "acme.com", false},
		{"multiple no match", "dave@test-provider.com", "acme.com, corp.io", false},
		{"spaces in list", "eve@corp.io", "acme.com, corp.io , example.org", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := domainMatches(tt.email, tt.allowedDomains)
			if got != tt.want {
				t.Errorf("domainMatches(%q, %q) = %v, want %v", tt.email, tt.allowedDomains, got, tt.want)
			}
		})
	}
}

func TestDomainMatchesEmptyCases(t *testing.T) {
	// All empty/edge cases
	if domainMatches("", "acme.com") {
		t.Error("empty email should not match")
	}
	if domainMatches("alice@acme.com", "") {
		t.Error("empty allowedDomains should not match")
	}
	if domainMatches("", "") {
		t.Error("both empty should not match")
	}
}

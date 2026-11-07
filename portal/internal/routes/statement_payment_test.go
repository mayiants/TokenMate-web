package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// TestRecordStatementPayment exercises accounts-receivable tracking on a
// post-paid monthly statement: partial then full payment, over-payment and
// no-receivable rejections, and that no balance column is touched.
func TestRecordStatementPayment(t *testing.T) {
	d := testEnterpriseDB(t)
	t.Cleanup(d.Close)
	r := newTestAdminPricingRoutes(t, d) // same DB-backed AdminRoutes builder
	ctx := context.Background()

	var adminID, entID string
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	if err := d.Pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, name, role) VALUES ($1, 'x', 'ar', 'super_admin') RETURNING id::text`,
		"ar-"+suffix+"@test.com").Scan(&adminID); err != nil {
		t.Fatalf("user: %v", err)
	}
	if err := d.Pool.QueryRow(ctx,
		`INSERT INTO enterprises (name, credit_total) VALUES ($1, 100000000) RETURNING id::text`,
		"ar-ent-"+suffix).Scan(&entID); err != nil {
		t.Fatalf("enterprise: %v", err)
	}
	// A post-paid statement: $100 consumed (100,000,000 micro).
	if _, err := d.Pool.Exec(ctx,
		`INSERT INTO enterprise_statements (enterprise_id, period, credit_used_micro) VALUES ($1::uuid, '2026-10', 100000000)`,
		entID); err != nil {
		t.Fatalf("statement: %v", err)
	}
	// A prepaid-only statement: no consumption → no receivable.
	if _, err := d.Pool.Exec(ctx,
		`INSERT INTO enterprise_statements (enterprise_id, period, credit_used_micro, prepaid_topup_micro) VALUES ($1::uuid, '2026-09', 0, 50000000)`,
		entID); err != nil {
		t.Fatalf("statement2: %v", err)
	}
	t.Cleanup(func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM enterprise_audit_log WHERE enterprise_id = $1::uuid`, entID)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM enterprise_statements WHERE enterprise_id = $1::uuid`, entID)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM enterprises WHERE id = $1::uuid`, entID)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, adminID)
	})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/:id/statements/:period/payment", func(c *gin.Context) { c.Set("user_id", adminID); r.recordStatementPayment(c) })
	post := func(period string, body map[string]any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/"+entID+"/statements/"+period+"/payment", bytes.NewReader(b)))
		return w
	}
	paid := func(period string) int64 {
		var v int64
		_ = d.Pool.QueryRow(ctx, `SELECT paid_micro FROM enterprise_statements WHERE enterprise_id = $1::uuid AND period = $2`, entID, period).Scan(&v)
		return v
	}

	// Partial payment $40.
	if w := post("2026-10", map[string]any{"amount_cents": 4000, "note": "wire 1"}); w.Code != http.StatusOK {
		t.Fatalf("partial: %d %s", w.Code, w.Body.String())
	}
	if paid("2026-10") != 40000000 {
		t.Errorf("paid after partial = %d", paid("2026-10"))
	}
	// Remaining $60 → fully paid.
	if w := post("2026-10", map[string]any{"amount_cents": 6000, "note": "wire 2"}); w.Code != http.StatusOK {
		t.Fatalf("final: %d %s", w.Code, w.Body.String())
	}
	if paid("2026-10") != 100000000 {
		t.Errorf("paid after final = %d", paid("2026-10"))
	}
	// Over-payment rejected (would exceed statement).
	if w := post("2026-10", map[string]any{"amount_cents": 1, "note": "x"}); w.Code != http.StatusConflict {
		t.Errorf("overpay: got %d, want 409", w.Code)
	}
	// Prepaid-only period has no receivable.
	if w := post("2026-09", map[string]any{"amount_cents": 100, "note": "x"}); w.Code != http.StatusConflict {
		t.Errorf("no-receivable: got %d, want 409 (%s)", w.Code, w.Body.String())
	}
	// replace=true corrects the recorded total.
	if w := post("2026-10", map[string]any{"amount_cents": 2500, "note": "correction", "replace": true}); w.Code != http.StatusOK {
		t.Fatalf("replace: %d %s", w.Code, w.Body.String())
	}
	if paid("2026-10") != 25000000 {
		t.Errorf("paid after replace = %d", paid("2026-10"))
	}

	// Balance column untouched.
	var creditUsed int64
	_ = d.Pool.QueryRow(ctx, `SELECT COALESCE(credit_used,0) FROM enterprises WHERE id = $1::uuid`, entID).Scan(&creditUsed)
	if creditUsed != 0 {
		t.Errorf("credit_used changed to %d (AR must not move balances)", creditUsed)
	}
}

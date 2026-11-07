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

func TestTopupPrepaidRecordOnly(t *testing.T) {
	d := testEnterpriseDB(t)
	t.Cleanup(d.Close) // registered first => runs last, after the data cleanups below
	r := newTestEnterpriseRoutes(t, d)
	ctx := context.Background()

	var adminID, entID string
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	if err := d.Pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, name, role) VALUES ($1, 'x', 'prepaid-rec', 'super_admin') RETURNING id::text`,
		"prepaid-rec-"+suffix+"@test.com").Scan(&adminID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if err := d.Pool.QueryRow(ctx,
		`INSERT INTO enterprises (name, prepaid_balance) VALUES ($1, 4111243848) RETURNING id::text`,
		"prepaid-rec-ent-"+suffix).Scan(&entID); err != nil {
		t.Fatalf("insert enterprise: %v", err)
	}
	t.Cleanup(func() {
		if _, err := d.Pool.Exec(ctx, `DELETE FROM enterprise_audit_log WHERE enterprise_id = $1::uuid`, entID); err != nil {
			t.Errorf("cleanup audit: %v", err)
		}
		if _, err := d.Pool.Exec(ctx, `DELETE FROM enterprises WHERE id = $1::uuid`, entID); err != nil {
			t.Errorf("cleanup enterprise: %v", err)
		}
		if _, err := d.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, adminID); err != nil {
			t.Errorf("cleanup user: %v", err)
		}
	})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/:id/topup", func(c *gin.Context) { c.Set("user_id", adminID); r.topupEnterprisePool(c) })
	post := func(body map[string]any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/"+entID+"/topup", bytes.NewReader(b)))
		return w
	}
	balance := func() int64 {
		var v int64
		if err := d.Pool.QueryRow(ctx, `SELECT prepaid_balance FROM enterprises WHERE id = $1::uuid`, entID).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	eff := "2026-09-30T00:00:00Z"
	good := func() map[string]any {
		return map[string]any{"prepaid_balance": 4587000000, "record_only": true, "effective_at": eff, "reason": "opening deposit"}
	}

	// Validation failures: 400 and no ledger/balance change.
	for name, mut := range map[string]func(map[string]any){
		"no reason":    func(m map[string]any) { m["reason"] = " " },
		"future date":  func(m map[string]any) { m["effective_at"] = time.Now().Add(time.Hour).UTC().Format(time.RFC3339) },
		"no date":      func(m map[string]any) { delete(m, "effective_at") },
		"with invoice": func(m map[string]any) { m["issue_invoice"] = true; m["invoice_title"] = "x" },
		"with quota":   func(m map[string]any) { m["quota_total"] = 5 },
	} {
		m := good()
		mut(m)
		if w := post(m); w.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400 (%s)", name, w.Code, w.Body.String())
		}
	}

	// Below the wallet balance: refused.
	low := good()
	low["prepaid_balance"] = 1000000000
	if w := post(low); w.Code != http.StatusConflict {
		t.Errorf("below balance: got %d, want 409 (%s)", w.Code, w.Body.String())
	}

	var n int
	_ = d.Pool.QueryRow(ctx, `SELECT count(*) FROM enterprise_audit_log WHERE enterprise_id = $1::uuid`, entID).Scan(&n)
	if n != 0 || balance() != 4111243848 {
		t.Fatalf("rejected requests changed state: rows=%d balance=%d", n, balance())
	}

	// Happy path: row dated at effective_at, balance untouched.
	if w := post(good()); w.Code != http.StatusOK {
		t.Fatalf("record_only: got %d: %s", w.Code, w.Body.String())
	}
	if b := balance(); b != 4111243848 {
		t.Errorf("balance changed to %d", b)
	}
	var delta, oldV, newV int64
	var created time.Time
	var changeType string
	if err := d.Pool.QueryRow(ctx,
		`SELECT change_type, delta, old_value, new_value, created_at FROM enterprise_audit_log WHERE enterprise_id = $1::uuid`,
		entID).Scan(&changeType, &delta, &oldV, &newV, &created); err != nil {
		t.Fatal(err)
	}
	if changeType != "prepaid_topup" || delta != 4587000000 || oldV != 0 || newV != 4587000000 {
		t.Errorf("row = %s delta=%d old=%d new=%d", changeType, delta, oldV, newV)
	}
	if !created.Equal(time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("created_at = %v, want 2026-09-30", created)
	}

	// Replay is rejected so the deposit cannot be counted twice.
	if w := post(good()); w.Code != http.StatusConflict {
		t.Errorf("duplicate: got %d, want 409 (%s)", w.Code, w.Body.String())
	}
	_ = d.Pool.QueryRow(ctx, `SELECT count(*) FROM enterprise_audit_log WHERE enterprise_id = $1::uuid`, entID).Scan(&n)
	if n != 1 {
		t.Errorf("rows after duplicate = %d, want 1", n)
	}
}

func TestTopupCreditRecordOnlyAndEditRejection(t *testing.T) {
	d := testEnterpriseDB(t)
	t.Cleanup(d.Close)
	r := newTestEnterpriseRoutes(t, d)
	ctx := context.Background()

	var adminID, entID string
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	if err := d.Pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, name, role) VALUES ($1, 'x', 'cred-rec', 'super_admin') RETURNING id::text`,
		"cred-rec-"+suffix+"@test.com").Scan(&adminID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if err := d.Pool.QueryRow(ctx,
		`INSERT INTO enterprises (name, credit_total, prepaid_balance) VALUES ($1, 2000000, 0) RETURNING id::text`,
		"cred-rec-ent-"+suffix).Scan(&entID); err != nil {
		t.Fatalf("insert enterprise: %v", err)
	}

	gin.SetMode(gin.TestMode)
	topup := gin.New()
	topup.POST("/:id/topup", func(c *gin.Context) { c.Set("user_id", adminID); r.topupEnterprisePool(c) })
	edit := gin.New()
	edit.PUT("/:id", func(c *gin.Context) { c.Set("user_id", adminID); r.updateEnterprise(c) })

	do := func(router *gin.Engine, method, path string, body map[string]any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(method, path, bytes.NewReader(b)))
		return w
	}
	creditTotal := func() int64 {
		var v int64
		_ = d.Pool.QueryRow(ctx, `SELECT credit_total FROM enterprises WHERE id = $1::uuid`, entID).Scan(&v)
		return v
	}

	// credit record_only: ledger row, balance unchanged.
	w := do(topup, http.MethodPost, "/"+entID+"/topup", map[string]any{
		"credit_total": 2000000, "record_only": true,
		"effective_at": "2026-09-30T00:00:00Z", "reason": "opening credit",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("credit record_only: got %d: %s", w.Code, w.Body.String())
	}
	if creditTotal() != 2000000 {
		t.Errorf("credit_total changed to %d", creditTotal())
	}
	var ct string
	var delta int64
	if err := d.Pool.QueryRow(ctx,
		`SELECT change_type, delta FROM enterprise_audit_log WHERE enterprise_id = $1::uuid AND change_type = 'credit_topup'`,
		entID).Scan(&ct, &delta); err != nil {
		t.Fatalf("expected credit_topup ledger row: %v", err)
	}
	if delta != 2000000 {
		t.Errorf("credit ledger delta = %d", delta)
	}

	// record_only with both credit and prepaid is rejected.
	if w := do(topup, http.MethodPost, "/"+entID+"/topup", map[string]any{
		"credit_total": 1, "prepaid_balance": 1, "record_only": true,
		"effective_at": "2026-09-30T00:00:00Z", "reason": "both",
	}); w.Code != http.StatusBadRequest {
		t.Errorf("both pools: got %d, want 400", w.Code)
	}

	// Direct edit of credit_total / prepaid_balance is refused.
	for _, body := range []map[string]any{{"credit_total": 5}, {"prepaid_balance": 5}, {"credit_total": 5, "name": "x"}} {
		if w := do(edit, http.MethodPut, "/"+entID, body); w.Code != http.StatusBadRequest {
			t.Errorf("direct edit %v: got %d, want 400 (%s)", body, w.Code, w.Body.String())
		}
	}
	if creditTotal() != 2000000 {
		t.Errorf("credit_total mutated by a rejected edit: %d", creditTotal())
	}

	// A non-money edit still works.
	if w := do(edit, http.MethodPut, "/"+entID, map[string]any{"max_members": 7}); w.Code != http.StatusOK {
		t.Errorf("name edit: got %d (%s)", w.Code, w.Body.String())
	}

	t.Cleanup(func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM enterprise_audit_log WHERE enterprise_id = $1::uuid`, entID)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM enterprises WHERE id = $1::uuid`, entID)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, adminID)
	})
}

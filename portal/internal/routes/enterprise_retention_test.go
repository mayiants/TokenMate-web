package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/config"
	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/middleware"
	"github.com/ai-proxy/ai-portal-go/internal/services"
	"github.com/ai-proxy/ai-portal-go/pkg/audit"
	"github.com/ai-proxy/ai-portal-go/pkg/crypto"
	"github.com/ai-proxy/ai-portal-go/pkg/mail"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// retentionFixture holds two enterprises and one user per role of interest.
type retentionFixture struct {
	d      *db.DB
	r      *EnterpriseRoutes
	logger *audit.Logger

	entA, entB                                  string
	adminA, adminB, memberA, sysAdmin, superAdm string
	suffix                                      string
}

func newRetentionFixture(t *testing.T) *retentionFixture {
	t.Helper()
	resetPlatformRetentionCache()
	d := testEnterpriseDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	al := audit.NewLogger(d, zap.NewNop())
	f := &retentionFixture{
		d: d, suffix: suffix, logger: al,
		r: NewEnterpriseRoutes(d, &config.Config{}, zap.NewNop(), nil, al, &mail.Sender{},
			nil, services.NewRewardService(d, zap.NewNop(), nil), nil),
	}
	ctx := context.Background()
	mkEnt := func(label string) string {
		var id string
		if err := d.Pool.QueryRow(ctx,
			`INSERT INTO enterprises (name, contact_email, status) VALUES ($1, $2, 'active') RETURNING id::text`,
			"ret-"+label+"-"+suffix, "ret-"+label+"-"+suffix+"@test.com").Scan(&id); err != nil {
			t.Fatalf("insert enterprise: %v", err)
		}
		return id
	}
	mkUser := func(label, role, ent string) string {
		var id string
		if err := d.Pool.QueryRow(ctx,
			`INSERT INTO users (name, email, password_hash, role, enterprise_id)
			 VALUES ($1, $2, 'x', $3, NULLIF($4, '')::uuid) RETURNING id::text`,
			"Ret "+label, "ret-"+suffix+"-"+label+"@test.com", role, ent).Scan(&id); err != nil {
			t.Fatalf("insert user %s: %v", label, err)
		}
		return id
	}
	f.entA, f.entB = mkEnt("a"), mkEnt("b")
	f.adminA = mkUser("adminA", "enterprise_admin", f.entA)
	f.adminB = mkUser("adminB", "enterprise_admin", f.entB)
	f.memberA = mkUser("memberA", "user", f.entA)
	f.sysAdmin = mkUser("sys", "system_admin", "")
	f.superAdm = mkUser("super", "super_admin", "")

	t.Cleanup(func() {
		f.logger.Stop(ctx)
		// Order matters: children before users/enterprises (FKs). Failures are
		// logged, not ignored: a leaked super_admin breaks unrelated tests that
		// assert on the number of super admins.
		for _, st := range []struct {
			sql  string
			args []interface{}
		}{
			// system_settings.updated_by references users, so it goes first.
			{`DELETE FROM system_settings WHERE key IN ('retention_default_mode','retention_default_days')`, nil},
			{`DELETE FROM api_keys WHERE user_id IN (SELECT id FROM users WHERE email LIKE $1)`, []interface{}{"ret-" + suffix + "-%"}},
			{`DELETE FROM audit_logs WHERE target_id = ANY($1) OR user_id IN (SELECT id FROM users WHERE email LIKE $2)`, []interface{}{[]string{f.entA, f.entB}, "ret-" + suffix + "-%"}},
			{`DELETE FROM enterprise_audit_log WHERE enterprise_id = ANY($1::uuid[])`, []interface{}{[]string{f.entA, f.entB}}},
			{`DELETE FROM users WHERE email LIKE $1`, []interface{}{"ret-" + suffix + "-%"}},
			{`DELETE FROM enterprises WHERE id = ANY($1::uuid[])`, []interface{}{[]string{f.entA, f.entB}}},
		} {
			if _, err := d.Pool.Exec(ctx, st.sql, st.args...); err != nil {
				t.Errorf("cleanup %q: %v", st.sql, err)
			}
		}
		resetPlatformRetentionCache()
		d.Close()
	})
	return f
}

// call invokes a retention handler as userID and decodes the JSON response.
func (f *retentionFixture) call(t *testing.T, h gin.HandlerFunc, method, userID, query string, body interface{}) (int, map[string]interface{}) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	var rdr *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rdr = bytes.NewReader(raw)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, "/api/enterprise/retention"+query, rdr)
	req.Header.Set("Content-Type", "application/json")
	// What middleware.AuditClientInfo does in production.
	req = req.WithContext(audit.WithClientInfo(req.Context(), "198.51.100.77", "RetentionTest/1.0"))
	c.Request = req
	c.Set("user_id", userID)
	c.Set("email", "actor@test.com")
	h(c)
	var out map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func (f *retentionFixture) policy(t *testing.T, ent string) (string, int) {
	t.Helper()
	var mode string
	var days int
	if err := f.d.Pool.QueryRow(context.Background(),
		`SELECT retention_mode, retention_days FROM enterprises WHERE id = $1::uuid`, ent).Scan(&mode, &days); err != nil {
		t.Fatalf("read policy: %v", err)
	}
	return mode, days
}

func (f *retentionFixture) flushAudit(t *testing.T) {
	t.Helper()
	// Stop drains the async buffer; the logger is single-use afterwards, so
	// swap in a fresh one for any later calls in the same test.
	f.logger.Stop(context.Background())
	f.logger = audit.NewLogger(f.d, zap.NewNop())
	f.r.audit = f.logger
}

func (f *retentionFixture) countAudit(t *testing.T, ent, action string) int {
	t.Helper()
	var n int
	if err := f.d.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM audit_logs WHERE target_id = $1 AND action = $2`, ent, action).Scan(&n); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	return n
}

func (f *retentionFixture) countEntAudit(t *testing.T, ent, changeType string) int {
	t.Helper()
	var n int
	if err := f.d.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM enterprise_audit_log WHERE enterprise_id = $1::uuid AND change_type = $2`, ent, changeType).Scan(&n); err != nil {
		t.Fatalf("count enterprise audit: %v", err)
	}
	return n
}

// ---------------------------------------------------------------------------
// Permission matrix
// ---------------------------------------------------------------------------

func TestRetentionAPI_PermissionMatrix(t *testing.T) {
	f := newRetentionFixture(t)
	put := map[string]interface{}{"mode": "metadata", "days": 45}

	type tc struct {
		name   string
		user   string
		query  string
		body   map[string]interface{} // PUT body (enterprise_id merged in when set)
		entID  string                 // enterprise_id sent (query/body); "" = none
		want   int
		target string // enterprise expected to change on success
	}
	cases := []tc{
		{"member denied", f.memberA, "", put, "", http.StatusForbidden, ""},
		{"member denied even with own enterprise_id", f.memberA, "", put, f.entA, http.StatusForbidden, ""},
		{"system_admin denied", f.sysAdmin, "", put, f.entA, http.StatusForbidden, ""},
		{"other-enterprise admin denied", f.adminB, "", put, f.entA, http.StatusForbidden, ""},
		{"own admin allowed (implicit enterprise)", f.adminA, "", put, "", http.StatusOK, f.entA},
		{"own admin allowed (explicit own id)", f.adminA, "", put, f.entA, http.StatusOK, f.entA},
		{"super_admin allowed with enterprise_id", f.superAdm, "", put, f.entB, http.StatusOK, f.entB},
		{"super_admin must name an enterprise", f.superAdm, "", put, "", http.StatusBadRequest, ""},
		{"invalid enterprise_id", f.superAdm, "", put, "not-a-uuid", http.StatusBadRequest, ""},
		{"unknown enterprise", f.superAdm, "", put, "99999999-9999-9999-9999-999999999999", http.StatusNotFound, ""},
	}
	for _, c := range cases {
		t.Run("PUT "+c.name, func(t *testing.T) {
			beforeA, beforeAd := f.policy(t, f.entA)
			beforeB, beforeBd := f.policy(t, f.entB)
			body := map[string]interface{}{}
			for k, v := range c.body {
				body[k] = v
			}
			if c.entID != "" {
				body["enterprise_id"] = c.entID
			}
			code, resp := f.call(t, f.r.updateEnterpriseRetention, http.MethodPut, c.user, "", body)
			if code != c.want {
				t.Fatalf("status = %d, want %d (resp %v)", code, c.want, resp)
			}
			gotAm, gotAd := f.policy(t, f.entA)
			gotBm, gotBd := f.policy(t, f.entB)
			changedA := gotAm != beforeA || gotAd != beforeAd
			changedB := gotBm != beforeB || gotBd != beforeBd
			switch c.target {
			case "":
				if changedA || changedB {
					t.Errorf("denied/invalid request changed a policy (A: %s/%d, B: %s/%d)", gotAm, gotAd, gotBm, gotBd)
				}
			case f.entA:
				if gotAm != "metadata" || gotAd != 45 || changedB {
					t.Errorf("expected only A to change to metadata/45, got A=%s/%d changedB=%v", gotAm, gotAd, changedB)
				}
			case f.entB:
				if gotBm != "metadata" || gotBd != 45 {
					t.Errorf("expected B to change to metadata/45, got B=%s/%d", gotBm, gotBd)
				}
			}
			// Reset both to a known state for the next case.
			_, _ = f.d.Pool.Exec(context.Background(),
				`UPDATE enterprises SET retention_mode='standard', retention_days=90 WHERE id = ANY($1::uuid[])`,
				[]string{f.entA, f.entB})
		})
	}

	getCases := []struct {
		name  string
		user  string
		query string
		want  int
	}{
		{"member denied", f.memberA, "", http.StatusForbidden},
		{"system_admin denied", f.sysAdmin, "?enterprise_id=" + f.entA, http.StatusForbidden},
		{"other-enterprise admin denied", f.adminB, "?enterprise_id=" + f.entA, http.StatusForbidden},
		{"own admin allowed", f.adminA, "", http.StatusOK},
		{"super_admin allowed", f.superAdm, "?enterprise_id=" + f.entA, http.StatusOK},
		{"super_admin without enterprise_id", f.superAdm, "", http.StatusBadRequest},
	}
	for _, c := range getCases {
		t.Run("GET "+c.name, func(t *testing.T) {
			code, resp := f.call(t, f.r.getEnterpriseRetention, http.MethodGet, c.user, c.query, nil)
			if code != c.want {
				t.Fatalf("status = %d, want %d (resp %v)", code, c.want, resp)
			}
			if code == http.StatusOK {
				if resp["mode"] != "standard" || resp["days"] != float64(90) {
					t.Errorf("unexpected policy %v", resp)
				}
				if resp["min_days"] != float64(1) || resp["max_days"] != float64(365) {
					t.Errorf("limits missing: %v", resp)
				}
			}
		})
	}

	// A demoted admin loses access immediately: the role is read from the DB,
	// not from the (long-lived) JWT claim.
	t.Run("demoted admin denied", func(t *testing.T) {
		_, _ = f.d.Pool.Exec(context.Background(), `UPDATE users SET role='user' WHERE id=$1::uuid`, f.adminA)
		defer f.d.Pool.Exec(context.Background(), `UPDATE users SET role='enterprise_admin' WHERE id=$1::uuid`, f.adminA) //nolint:errcheck
		if code, _ := f.call(t, f.r.getEnterpriseRetention, http.MethodGet, f.adminA, "", nil); code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", code)
		}
	})
}

// ---------------------------------------------------------------------------
// Validation boundaries
// ---------------------------------------------------------------------------

func TestRetentionAPI_ValidationBoundaries(t *testing.T) {
	f := newRetentionFixture(t)

	bad := []struct {
		name string
		body map[string]interface{}
	}{
		{"days 0", map[string]interface{}{"mode": "standard", "days": 0}},
		{"days -1", map[string]interface{}{"mode": "standard", "days": -1}},
		{"days 366", map[string]interface{}{"mode": "standard", "days": 366}},
		{"days huge", map[string]interface{}{"mode": "standard", "days": 1 << 40}},
		{"days string", map[string]interface{}{"mode": "standard", "days": "30"}},
		{"mode empty", map[string]interface{}{"days": 30}},
		{"mode unknown", map[string]interface{}{"mode": "full", "days": 30}},
		{"mode wrong case", map[string]interface{}{"mode": "Standard", "days": 30}},
		{"mode sql", map[string]interface{}{"mode": "off'; DROP TABLE enterprises;--", "days": 30}},
	}
	for _, c := range bad {
		t.Run("rejects "+c.name, func(t *testing.T) {
			code, _ := f.call(t, f.r.updateEnterpriseRetention, http.MethodPut, f.adminA, "", c.body)
			if code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", code)
			}
			if m, d := f.policy(t, f.entA); m != "standard" || d != 90 {
				t.Errorf("policy changed to %s/%d by an invalid request", m, d)
			}
		})
	}

	good := []struct {
		mode string
		days int
	}{{"standard", 1}, {"standard", 365}, {"metadata", 30}, {"off", 7}}
	for _, c := range good {
		t.Run(fmt.Sprintf("accepts %s/%d", c.mode, c.days), func(t *testing.T) {
			code, resp := f.call(t, f.r.updateEnterpriseRetention, http.MethodPut, f.adminA, "",
				map[string]interface{}{"mode": c.mode, "days": c.days})
			if code != http.StatusOK {
				t.Fatalf("status = %d (%v)", code, resp)
			}
			if m, d := f.policy(t, f.entA); m != c.mode || d != c.days {
				t.Errorf("stored %s/%d, want %s/%d", m, d, c.mode, c.days)
			}
		})
	}

	t.Run("omitted days keeps the current value", func(t *testing.T) {
		_, _ = f.d.Pool.Exec(context.Background(), `UPDATE enterprises SET retention_mode='standard', retention_days=120 WHERE id=$1::uuid`, f.entA)
		code, _ := f.call(t, f.r.updateEnterpriseRetention, http.MethodPut, f.adminA, "", map[string]interface{}{"mode": "off"})
		if code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		if m, d := f.policy(t, f.entA); m != "off" || d != 120 {
			t.Errorf("stored %s/%d, want off/120", m, d)
		}
	})

	t.Run("database CHECK constraints back the API validation", func(t *testing.T) {
		for _, q := range []string{
			`UPDATE enterprises SET retention_mode='bogus' WHERE id=$1::uuid`,
			`UPDATE enterprises SET retention_days=0 WHERE id=$1::uuid`,
			`UPDATE enterprises SET retention_days=366 WHERE id=$1::uuid`,
		} {
			if _, err := f.d.Pool.Exec(context.Background(), q, f.entA); err == nil {
				t.Errorf("expected CHECK violation for %q", q)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Audit trail
// ---------------------------------------------------------------------------

func TestRetentionAPI_UpdateIsAuditedToBothLogs(t *testing.T) {
	f := newRetentionFixture(t)
	ctx := context.Background()

	code, resp := f.call(t, f.r.updateEnterpriseRetention, http.MethodPut, f.adminA, "",
		map[string]interface{}{"mode": "metadata", "days": 14})
	if code != http.StatusOK || resp["changed"] != true {
		t.Fatalf("status=%d resp=%v", code, resp)
	}
	// A no-op write must not produce audit noise.
	code, resp = f.call(t, f.r.updateEnterpriseRetention, http.MethodPut, f.adminA, "",
		map[string]interface{}{"mode": "metadata", "days": 14})
	if code != http.StatusOK || resp["changed"] != false {
		t.Fatalf("no-op: status=%d resp=%v", code, resp)
	}
	f.flushAudit(t)

	if n := f.countAudit(t, f.entA, "retention_policy_updated"); n != 1 {
		t.Errorf("audit_logs rows = %d, want exactly 1", n)
	}
	if n := f.countEntAudit(t, f.entA, "retention_policy_updated"); n != 1 {
		t.Errorf("enterprise_audit_log rows = %d, want exactly 1", n)
	}

	var ip, ua, oldV, newV, action *string
	var changedBy *string
	if err := f.d.Pool.QueryRow(ctx,
		`SELECT host(ip_address), user_agent, old_value::text, new_value::text
		   FROM audit_logs WHERE target_id = $1 AND action = 'retention_policy_updated'`, f.entA).
		Scan(&ip, &ua, &oldV, &newV); err != nil {
		t.Fatalf("read audit_logs: %v", err)
	}
	if ip == nil || *ip != "198.51.100.77" || ua == nil || *ua != "RetentionTest/1.0" {
		t.Errorf("audit_logs client info = %v / %v, want request ip/ua", retDeref(ip), retDeref(ua))
	}
	if oldV == nil || !strings.Contains(*oldV, `"standard"`) || newV == nil || !strings.Contains(*newV, `"metadata"`) {
		t.Errorf("old/new values = %v / %v", retDeref(oldV), retDeref(newV))
	}
	if err := f.d.Pool.QueryRow(ctx,
		`SELECT action, host(ip_address), user_agent, changed_by::text, old_value_str, new_value_str
		   FROM enterprise_audit_log WHERE enterprise_id = $1::uuid AND change_type = 'retention_policy_updated'`, f.entA).
		Scan(&action, &ip, &ua, &changedBy, &oldV, &newV); err != nil {
		t.Fatalf("read enterprise_audit_log: %v", err)
	}
	if action == nil || *action != "retention_policy_updated" {
		t.Errorf("enterprise_audit_log.action = %v, want it normalised to change_type", retDeref(action))
	}
	if ip == nil || *ip != "198.51.100.77" || ua == nil || *ua != "RetentionTest/1.0" {
		t.Errorf("enterprise_audit_log client info = %v / %v", retDeref(ip), retDeref(ua))
	}
	if changedBy == nil || *changedBy != f.adminA || retDeref(oldV) != "standard/90" || retDeref(newV) != "metadata/14" {
		t.Errorf("changed_by/old/new = %v / %v / %v", retDeref(changedBy), retDeref(oldV), retDeref(newV))
	}

	// The policy row also records who/when.
	var by *string
	var at *time.Time
	_ = f.d.Pool.QueryRow(ctx, `SELECT retention_updated_by::text, retention_updated_at FROM enterprises WHERE id=$1::uuid`, f.entA).Scan(&by, &at)
	if by == nil || *by != f.adminA || at == nil {
		t.Errorf("retention_updated_by/at = %v / %v", retDeref(by), at)
	}
}

// TestWriteEnterpriseAudit_ActionDefaultsToChangeType covers the normalisation
// of enterprise_audit_log.action at the writer level (222/286 production rows
// had it empty because only change_type was set).
func TestWriteEnterpriseAudit_ActionDefaultsToChangeType(t *testing.T) {
	f := newRetentionFixture(t)
	ctx := audit.WithClientInfo(context.Background(), "not-an-ip", "UA/9")

	writeEnterpriseAudit(ctx, f.d, zap.NewNop(), enterpriseAuditEntry{
		EnterpriseID: f.entA, Category: "settings", ChangeType: "defaulted_action", FieldName: "x"})
	writeEnterpriseAudit(ctx, f.d, zap.NewNop(), enterpriseAuditEntry{
		EnterpriseID: f.entA, Category: "settings", ChangeType: "explicit_action_ct", Action: "explicit", FieldName: "x"})

	rows := map[string][2]*string{}
	r, err := f.d.Pool.Query(context.Background(),
		`SELECT change_type, action, host(ip_address) FROM enterprise_audit_log
		  WHERE enterprise_id = $1::uuid AND change_type IN ('defaulted_action','explicit_action_ct')`, f.entA)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for r.Next() {
		var ct string
		var act, ip *string
		if err := r.Scan(&ct, &act, &ip); err != nil {
			t.Fatal(err)
		}
		rows[ct] = [2]*string{act, ip}
	}
	if a := rows["defaulted_action"][0]; a == nil || *a != "defaulted_action" {
		t.Errorf("action = %v, want change_type fallback", retDeref(a))
	}
	if a := rows["explicit_action_ct"][0]; a == nil || *a != "explicit" {
		t.Errorf("explicit action overwritten: %v", retDeref(a))
	}
	// Invalid IP in the context is stored as NULL rather than failing the insert.
	if len(rows) != 2 || rows["defaulted_action"][1] != nil {
		t.Errorf("expected both rows inserted with NULL ip, got %v", rows)
	}
}

// ---------------------------------------------------------------------------
// Purge
// ---------------------------------------------------------------------------

type purgeProxy struct {
	*httptest.Server
	mu    sync.Mutex
	calls []string
}

func newPurgeProxy(t *testing.T, status int, body string) *purgeProxy {
	t.Helper()
	p := &purgeProxy{}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.calls = append(p.calls, r.Method+" "+r.URL.RequestURI()+" sig="+fmt.Sprint(r.Header.Get("X-Signature") != ""))
		p.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(p.Close)
	return p
}

func TestRetentionAPI_Purge(t *testing.T) {
	f := newRetentionFixture(t)
	ctx := context.Background()
	t.Setenv("PROXY_SYNC_SECRET", "purge-test-secret")
	t.Setenv("PROXY_ADMIN_SECRET", "")
	t.Setenv("PROXY_CALLBACK_URLS", "")

	t.Run("confirm must equal the enterprise id", func(t *testing.T) {
		px := newPurgeProxy(t, http.StatusOK, `{}`)
		t.Setenv("PROXY_URL", px.URL)
		for _, confirm := range []string{"", "yes", f.entB, strings.ToUpper(f.entA) + "x"} {
			code, _ := f.call(t, f.r.purgeEnterpriseRetention, http.MethodPost, f.adminA, "/purge",
				map[string]interface{}{"confirm": confirm})
			// A different, valid enterprise id is an authorisation failure for a
			// non-super admin; everything else is a failed confirmation.
			want := http.StatusBadRequest
			if confirm == f.entB {
				want = http.StatusForbidden
			}
			if code != want {
				t.Errorf("confirm %q: status = %d, want %d", confirm, code, want)
			}
		}
		if len(px.calls) != 0 {
			t.Errorf("proxy was called despite a bad confirmation: %v", px.calls)
		}
	})

	t.Run("member and other-enterprise admin cannot purge", func(t *testing.T) {
		px := newPurgeProxy(t, http.StatusOK, `{}`)
		t.Setenv("PROXY_URL", px.URL)
		for _, u := range []string{f.memberA, f.adminB, f.sysAdmin} {
			code, _ := f.call(t, f.r.purgeEnterpriseRetention, http.MethodPost, u, "/purge",
				map[string]interface{}{"confirm": f.entA})
			if code != http.StatusForbidden {
				t.Errorf("user %s: status = %d, want 403", u, code)
			}
		}
		if len(px.calls) != 0 {
			t.Errorf("proxy was called by an unauthorised user: %v", px.calls)
		}
	})

	t.Run("enterprise admin purge calls the proxy and is audited", func(t *testing.T) {
		px := newPurgeProxy(t, http.StatusOK, `{"deleted_files":5,"deleted_bytes":2048}`)
		t.Setenv("PROXY_URL", px.URL)
		code, resp := f.call(t, f.r.purgeEnterpriseRetention, http.MethodPost, f.adminA, "/purge",
			map[string]interface{}{"confirm": f.entA})
		if code != http.StatusOK || resp["success"] != true || resp["deleted_files"] != float64(5) {
			t.Fatalf("status=%d resp=%v", code, resp)
		}
		want := fmt.Sprintf("DELETE /admin/retention/enterprises/%s?confirm=%s sig=true", f.entA, f.entA)
		if len(px.calls) != 1 || px.calls[0] != want {
			t.Errorf("proxy calls = %v, want [%s]", px.calls, want)
		}
		f.flushAudit(t)
		for _, a := range []string{"retention_purge_requested", "retention_purged"} {
			if n := f.countAudit(t, f.entA, a); n != 1 {
				t.Errorf("audit_logs %s = %d, want 1", a, n)
			}
		}
		if n := f.countEntAudit(t, f.entA, "retention_purged"); n != 1 {
			t.Errorf("enterprise_audit_log retention_purged = %d, want 1", n)
		}
		var st *string
		var by *string
		_ = f.d.Pool.QueryRow(ctx, `SELECT retention_last_purge_status, retention_last_purge_by::text FROM enterprises WHERE id=$1::uuid`, f.entA).Scan(&st, &by)
		if retDeref(st) != "ok" || retDeref(by) != f.adminA {
			t.Errorf("last purge = %v by %v", retDeref(st), retDeref(by))
		}
		// GET exposes it.
		_, got := f.call(t, f.r.getEnterpriseRetention, http.MethodGet, f.adminA, "", nil)
		lp, _ := got["last_purge"].(map[string]interface{})
		if lp["status"] != "ok" || lp["at"] == nil {
			t.Errorf("GET last_purge = %v", lp)
		}
	})

	t.Run("super_admin purge names the enterprise via confirm", func(t *testing.T) {
		px := newPurgeProxy(t, http.StatusOK, `{}`)
		t.Setenv("PROXY_URL", px.URL)
		code, resp := f.call(t, f.r.purgeEnterpriseRetention, http.MethodPost, f.superAdm, "/purge",
			map[string]interface{}{"confirm": f.entB})
		if code != http.StatusOK {
			t.Fatalf("status=%d resp=%v", code, resp)
		}
		if len(px.calls) != 1 || !strings.Contains(px.calls[0], f.entB) {
			t.Errorf("proxy calls = %v", px.calls)
		}
	})

	t.Run("proxy error is reported and still audited", func(t *testing.T) {
		px := newPurgeProxy(t, http.StatusInternalServerError, `internal`)
		t.Setenv("PROXY_URL", px.URL)
		code, resp := f.call(t, f.r.purgeEnterpriseRetention, http.MethodPost, f.adminB, "/purge",
			map[string]interface{}{"confirm": f.entB})
		if code != http.StatusBadGateway || resp["success"] != false {
			t.Fatalf("status=%d resp=%v, want 502", code, resp)
		}
		f.assertFailedPurgeAudited(t, f.entB)
	})

	t.Run("unreachable proxy returns a clear error and is audited", func(t *testing.T) {
		px := newPurgeProxy(t, http.StatusOK, `{}`)
		url := px.URL
		px.Close() // now nothing listens there
		t.Setenv("PROXY_URL", url)
		code, resp := f.call(t, f.r.purgeEnterpriseRetention, http.MethodPost, f.adminA, "/purge",
			map[string]interface{}{"confirm": f.entA})
		if code != http.StatusBadGateway {
			t.Fatalf("status=%d resp=%v, want 502", code, resp)
		}
		msg, _ := json.Marshal(resp)
		if strings.Contains(string(msg), "127.0.0.1") {
			t.Errorf("response leaks the proxy address: %s", msg)
		}
		f.assertFailedPurgeAudited(t, f.entA)
	})

	t.Run("proxy not configured is 503 and audited", func(t *testing.T) {
		t.Setenv("PROXY_URL", "")
		code, _ := f.call(t, f.r.purgeEnterpriseRetention, http.MethodPost, f.adminA, "/purge",
			map[string]interface{}{"confirm": f.entA})
		if code != http.StatusServiceUnavailable {
			t.Fatalf("status=%d, want 503", code)
		}
	})
}

func (f *retentionFixture) assertFailedPurgeAudited(t *testing.T, ent string) {
	t.Helper()
	f.flushAudit(t)
	if n := f.countAudit(t, ent, "retention_purge_requested"); n < 1 {
		t.Errorf("purge attempt not audited (requested rows = %d)", n)
	}
	if n := f.countAudit(t, ent, "retention_purge_failed"); n < 1 {
		t.Errorf("purge failure not audited (failed rows = %d)", n)
	}
	var st *string
	_ = f.d.Pool.QueryRow(context.Background(),
		`SELECT retention_last_purge_status FROM enterprises WHERE id=$1::uuid`, ent).Scan(&st)
	if retDeref(st) != "failed" {
		t.Errorf("last purge status = %v, want failed", retDeref(st))
	}
}

// ---------------------------------------------------------------------------
// /api/keys/validate
// ---------------------------------------------------------------------------

// validateFixture adds a miniredis-backed cache and a fake Proxy callback
// endpoint recording key_revoked notifications.
type validateFixture struct {
	*retentionFixture
	keys     *APIKeyRoutes
	redis    *miniredis.Miniredis
	mu       sync.Mutex
	revoked  []string // key hashes announced to the Proxy
	callback *httptest.Server
}

func newValidateFixture(t *testing.T) *validateFixture {
	t.Helper()
	f := newRetentionFixture(t)
	mr := miniredis.RunT(t)
	f.d.Redis = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	vf := &validateFixture{retentionFixture: f, redis: mr}
	vf.callback = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var msg struct {
			Type    string `json:"type"`
			KeyHash string `json:"key_hash"`
		}
		_ = json.NewDecoder(r.Body).Decode(&msg)
		if msg.Type == "key_revoked" {
			vf.mu.Lock()
			vf.revoked = append(vf.revoked, msg.KeyHash)
			vf.mu.Unlock()
		}
	}))
	t.Cleanup(vf.callback.Close)
	// The DB reads PROXY_CALLBACK_URLS once per instance (sync.Once), on first publish.
	t.Setenv("PROXY_CALLBACK_URLS", vf.callback.URL)
	vf.keys = NewAPIKeyRoutes(f.d, &config.Config{}, zap.NewNop(), nil, &audit.Logger{})
	return vf
}

func (v *validateFixture) revokedHashes() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]string(nil), v.revoked...)
}

func (v *validateFixture) mkKey(t *testing.T, userID, raw, keyType, ent string) {
	t.Helper()
	createGateTestKey(t, v.d, userID, raw, keyType, ent)
}

func TestValidateKey_RetentionFields(t *testing.T) {
	v := newValidateFixture(t)
	ctx := context.Background()
	rawEnt := "sk-ret-ent-" + v.suffix
	rawPlain := "sk-ret-plain-" + v.suffix

	// Enterprise key (policy: metadata / 14d) and a personal key with no enterprise.
	_, _ = v.d.Pool.Exec(ctx, `UPDATE enterprises SET retention_mode='metadata', retention_days=14 WHERE id=$1::uuid`, v.entA)
	v.mkKey(t, v.memberA, rawEnt, "enterprise", v.entA)
	plainUser := createGateTestUser(t, v.d, "ret-"+v.suffix+"-plain@test.com", 100, "")
	v.mkKey(t, plainUser, rawPlain, "general", "")

	code, body := callValidateKey(t, v.keys, rawEnt)
	if code != http.StatusOK || body["valid"] != true {
		t.Fatalf("enterprise key: code=%d body=%v", code, body)
	}
	if body["enterprise_id"] != v.entA || body["retention_mode"] != "metadata" || body["retention_days"] != float64(14) {
		t.Errorf("enterprise key retention fields = %v / %v / %v", body["enterprise_id"], body["retention_mode"], body["retention_days"])
	}

	// Key without an enterprise: empty enterprise_id and the platform defaults
	// (no system_settings rows => standard / 30).
	code, body = callValidateKey(t, v.keys, rawPlain)
	if code != http.StatusOK || body["valid"] != true {
		t.Fatalf("plain key: code=%d body=%v", code, body)
	}
	if id, present := body["enterprise_id"]; !present || id != "" {
		t.Errorf("enterprise_id = %v (present=%v), want empty string", id, present)
	}
	if body["retention_mode"] != "standard" || body["retention_days"] != float64(defaultRetentionDays) {
		t.Errorf("platform defaults = %v / %v, want standard / default", body["retention_mode"], body["retention_days"])
	}

	// Second call is served from the Redis cache and must be identical.
	if n, _ := v.d.Redis.Exists(ctx, "validate:"+hashForTest(rawEnt)).Result(); n != 1 {
		t.Fatalf("expected the enterprise validate response to be cached")
	}
	_, cached := callValidateKey(t, v.keys, rawEnt)
	if cached["enterprise_id"] != v.entA || cached["retention_mode"] != "metadata" || cached["retention_days"] != float64(14) {
		t.Errorf("cached response lost retention fields: %v", cached)
	}

	// Platform defaults are read live even for cached enterprise-less responses.
	_, _ = v.d.Pool.Exec(ctx, `INSERT INTO system_settings (key, value) VALUES ('retention_default_mode','off'), ('retention_default_days','7')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	resetPlatformRetentionCache()
	_, cachedPlain := callValidateKey(t, v.keys, rawPlain)
	if cachedPlain["retention_mode"] != "off" || cachedPlain["retention_days"] != float64(7) {
		t.Errorf("cached enterprise-less response did not pick up new defaults: %v / %v",
			cachedPlain["retention_mode"], cachedPlain["retention_days"])
	}

	// Invalid stored defaults fall back to the built-in ones instead of leaking
	// junk to the Proxy.
	_, _ = v.d.Pool.Exec(ctx, `UPDATE system_settings SET value='bogus' WHERE key='retention_default_mode'`)
	_, _ = v.d.Pool.Exec(ctx, `UPDATE system_settings SET value='9999' WHERE key='retention_default_days'`)
	resetPlatformRetentionCache()
	_, junk := callValidateKey(t, v.keys, rawPlain)
	if junk["retention_mode"] != "standard" || junk["retention_days"] != float64(defaultRetentionDays) {
		t.Errorf("invalid defaults leaked: %v / %v", junk["retention_mode"], junk["retention_days"])
	}
}

// TestValidateKey_LegacyCachedEntryIsRefreshed: entries cached before this
// release have no retention_mode; serving them would hand enterprise keys the
// platform defaults for up to 5 minutes after deploy.
func TestValidateKey_LegacyCachedEntryIsRefreshed(t *testing.T) {
	v := newValidateFixture(t)
	ctx := context.Background()
	raw := "sk-ret-legacy-" + v.suffix
	_, _ = v.d.Pool.Exec(ctx, `UPDATE enterprises SET retention_mode='off', retention_days=3 WHERE id=$1::uuid`, v.entA)
	v.mkKey(t, v.memberA, raw, "enterprise", v.entA)

	legacy := `{"valid":true,"user_id":"` + v.memberA + `","api_key_id":"x","role":"user","access_mode":"general","allowed_models":null,"rate_limit_rpm":1,"rate_limit_tpm":1,"balance":1,"routing_track":"standard"}`
	if err := v.d.Redis.Set(ctx, "validate:"+hashForTest(raw), legacy, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	_, body := callValidateKey(t, v.keys, raw)
	if body["enterprise_id"] != v.entA || body["retention_mode"] != "off" || body["retention_days"] != float64(3) {
		t.Errorf("legacy cache entry was served as-is: %v", body)
	}
}

// TestRetentionUpdate_InvalidatesEnterpriseKeyCaches proves a policy change
// drops the cached validate response of every key of that enterprise (and only
// that enterprise) and tells the Proxy to drop its own copy.
func TestRetentionUpdate_InvalidatesEnterpriseKeyCaches(t *testing.T) {
	v := newValidateFixture(t)
	ctx := context.Background()

	raw1, raw2 := "sk-ret-inv1-"+v.suffix, "sk-ret-inv2-"+v.suffix
	rawOther := "sk-ret-invB-" + v.suffix
	v.mkKey(t, v.memberA, raw1, "enterprise", v.entA)
	v.mkKey(t, v.adminA, raw2, "enterprise", v.entA)
	otherUser := createGateTestUser(t, v.d, "ret-"+v.suffix+"-other@test.com", 100, v.entB)
	v.mkKey(t, otherUser, rawOther, "enterprise", v.entB)

	for _, raw := range []string{raw1, raw2, rawOther} {
		if code, body := callValidateKey(t, v.keys, raw); code != http.StatusOK || body["valid"] != true {
			t.Fatalf("prime %s: %d %v", raw, code, body)
		}
	}
	for _, raw := range []string{raw1, raw2, rawOther} {
		if n, _ := v.d.Redis.Exists(ctx, "validate:"+hashForTest(raw)).Result(); n != 1 {
			t.Fatalf("%s should be cached before the policy change", raw)
		}
	}

	code, resp := v.call(t, v.r.updateEnterpriseRetention, http.MethodPut, v.adminA, "",
		map[string]interface{}{"mode": "off", "days": 5})
	if code != http.StatusOK {
		t.Fatalf("update: %d %v", code, resp)
	}

	for _, raw := range []string{raw1, raw2} {
		if n, _ := v.d.Redis.Exists(ctx, "validate:"+hashForTest(raw)).Result(); n != 0 {
			t.Errorf("cached validate for %s survived the policy change", raw)
		}
	}
	if n, _ := v.d.Redis.Exists(ctx, "validate:"+hashForTest(rawOther)).Result(); n != 1 {
		t.Errorf("another enterprise's cache entry was flushed")
	}

	// Next validate shows the new policy.
	_, body := callValidateKey(t, v.keys, raw1)
	if body["retention_mode"] != "off" || body["retention_days"] != float64(5) {
		t.Errorf("validate after change = %v / %v, want off / 5", body["retention_mode"], body["retention_days"])
	}

	// The Proxy was told to drop its in-process copy of both keys (async HTTP).
	deadline := time.Now().Add(3 * time.Second)
	for {
		got := map[string]bool{}
		for _, h := range v.revokedHashes() {
			got[h] = true
		}
		if got[hashForTest(raw1)] && got[hashForTest(raw2)] {
			if got[hashForTest(rawOther)] {
				t.Error("Proxy was told to drop another enterprise's key")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Proxy not notified for both keys; got %v", v.revokedHashes())
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// ---------------------------------------------------------------------------
// Platform defaults API (super_admin)
// ---------------------------------------------------------------------------

func TestSettingsRetentionDefaults(t *testing.T) {
	f := newRetentionFixture(t)
	s := NewSettingsRoutes(f.d, nil, &mail.Sender{}, config.SMTPConfig{}, "", zap.NewNop()).WithAudit(f.logger)

	do := func(h gin.HandlerFunc, method string, body interface{}) (int, map[string]interface{}) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		raw, _ := json.Marshal(body)
		c.Request = httptest.NewRequest(method, "/api/admin/settings/retention-defaults", bytes.NewReader(raw))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set("user_id", f.superAdm)
		c.Set("email", "super@test.com")
		h(c)
		var out map[string]interface{}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}

	code, got := do(s.getRetentionDefaults, http.MethodGet, nil)
	if code != http.StatusOK || got["mode"] != "standard" || got["days"] != float64(defaultRetentionDays) {
		t.Fatalf("defaults before any write: %d %v", code, got)
	}

	for _, bad := range []map[string]interface{}{
		{"mode": "bogus", "days": 30}, {"mode": "off", "days": 0}, {"mode": "off", "days": 366}, {"days": 30},
	} {
		if code, _ := do(s.updateRetentionDefaults, http.MethodPut, bad); code != http.StatusBadRequest {
			t.Errorf("body %v: status = %d, want 400", bad, code)
		}
	}

	code, got = do(s.updateRetentionDefaults, http.MethodPut, map[string]interface{}{"mode": "metadata", "days": 60})
	if code != http.StatusOK || got["changed"] != true {
		t.Fatalf("update: %d %v", code, got)
	}
	code, got = do(s.getRetentionDefaults, http.MethodGet, nil)
	if got["mode"] != "metadata" || got["days"] != float64(60) {
		t.Errorf("defaults after write: %d %v", code, got)
	}
	// Seen by the validate path immediately (cache reset on write).
	if m, d := platformRetentionDefaults(context.Background(), f.d); m != "metadata" || d != 60 {
		t.Errorf("platformRetentionDefaults = %s/%d", m, d)
	}

	f.flushAudit(t)
	var n int
	_ = f.d.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM audit_logs WHERE action = 'retention_defaults_updated' AND user_id = $1::uuid`, f.superAdm).Scan(&n)
	if n != 1 {
		t.Errorf("retention_defaults_updated audit rows = %d, want 1", n)
	}
	_, _ = f.d.Pool.Exec(context.Background(), `DELETE FROM audit_logs WHERE user_id = $1::uuid`, f.superAdm)
}

// TestRetentionRoutesRegistered guards the wiring (a missing Register call
// would otherwise only surface as 404s in production) and the gin route table:
// registering the whole enterprise route set must not panic on a conflict
// between /enterprise/retention and the other /enterprise/... routes.
func TestRetentionRoutesRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	r := &EnterpriseRoutes{auth: &middleware.Auth{}}
	r.Register(router.Group("/api"))

	have := map[string]bool{}
	for _, ri := range router.Routes() {
		have[ri.Method+" "+ri.Path] = true
	}
	for _, want := range []string{
		"GET /api/enterprise/retention", "PUT /api/enterprise/retention", "POST /api/enterprise/retention/purge",
	} {
		if !have[want] {
			t.Errorf("route %s not registered", want)
		}
	}
}

func hashForTest(raw string) string { return crypto.HashKeySHA256(raw) }

func retDeref(p *string) string {
	if p == nil {
		return "<NULL>"
	}
	return *p
}

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

	"github.com/ai-proxy/ai-portal-go/internal/config"
	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/middleware"
	"github.com/ai-proxy/ai-portal-go/pkg/audit"
	"github.com/ai-proxy/ai-portal-go/pkg/crypto"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func newMgmtTestRoutes(d *db.DB) *ManagementTokenRoutes {
	auth := middleware.NewAuth(&config.Config{}, zap.NewNop())
	cfg := &config.Config{Auth: config.AuthConfig{EncryptionKey: "test-encryption-key-32-characters!!"}}
	return NewManagementTokenRoutes(d, cfg, auth, zap.NewNop(), &audit.Logger{})
}

// callCreateMgmtToken invokes createToken as a given user/role.
func callCreateMgmtToken(mt *ManagementTokenRoutes, userID, role string, body map[string]interface{}) (int, map[string]interface{}) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	raw, _ := json.Marshal(body)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/management-tokens", bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user_id", userID)
	c.Set("email", "admin@ent.test")
	c.Set("role", role)
	// Run the gate then the handler (mirrors the middleware chain).
	mt.requireEnterpriseAdmin()(c)
	if !c.IsAborted() {
		mt.createToken(c)
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w.Code, resp
}

// TestManagementToken_P1 covers create (admin only) + scope enforcement +
// enterprise isolation + revoke/expiry on the mgmt-token auth path.
func TestManagementToken_P1(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	mt := newMgmtTestRoutes(d)
	ctx := context.Background()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	entA := createApprovalTestEnterprise(t, d, "mgmt-a-"+suffix, "")
	entB := createApprovalTestEnterprise(t, d, "mgmt-b-"+suffix, "")
	admin := createGateTestUser(t, d, "mgmt-"+suffix+"-adm@test.com", 0, entA)
	member := createGateTestUser(t, d, "mgmt-"+suffix+"-mbr@test.com", 0, entA)
	// The token owner must be a real enterprise_admin with an active admin
	// membership so authenticate()'s owner re-validation accepts the token.
	_, _ = d.Pool.Exec(ctx, `UPDATE users SET role='enterprise_admin' WHERE id=$1::uuid`, admin)
	_, _ = d.Pool.Exec(ctx, `INSERT INTO enterprise_members (enterprise_id, user_id, role, joined_at) VALUES ($1::uuid,$2::uuid,'admin',NOW()) ON CONFLICT (enterprise_id, user_id) DO UPDATE SET role='admin', removed_at=NULL`, entA, admin)

	defer func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM management_tokens WHERE user_id = ANY($1::uuid[])`, []string{admin, member})
		_, _ = d.Pool.Exec(ctx, `DELETE FROM enterprise_members WHERE enterprise_id = ANY($1::uuid[])`, []string{entA, entB})
		cleanupGateTestData(t, d, suffix)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM enterprises WHERE id = ANY($1::uuid[])`, []string{entA, entB})
	}()

	// 1. enterprise_admin creates a token with valid scopes → 200 + one-time plaintext.
	code, resp := callCreateMgmtToken(mt, admin, "enterprise_admin", map[string]interface{}{
		"name": "ci-token", "scopes": []string{"read:usage", "read:pricing"},
	})
	if code != http.StatusOK {
		t.Fatalf("admin create: status=%d, want 200 (resp %v)", code, resp)
	}
	rawToken, _ := resp["token"].(string)
	if rawToken == "" || rawToken[:5] != "mgmt-" {
		t.Fatalf("expected mgmt- plaintext token, got %q", rawToken)
	}
	// Stored as hash, never plaintext.
	var storedHash string
	if err := d.Pool.QueryRow(ctx, `SELECT token_hash FROM management_tokens WHERE user_id=$1::uuid`, admin).Scan(&storedHash); err != nil {
		t.Fatalf("select stored token: %v", err)
	}
	if storedHash != crypto.HashKeySHA256(rawToken) {
		t.Errorf("stored hash mismatch")
	}

	// 2. invalid scope rejected.
	code, _ = callCreateMgmtToken(mt, admin, "enterprise_admin", map[string]interface{}{
		"name": "bad", "scopes": []string{"manage:everything"},
	})
	if code != http.StatusBadRequest {
		t.Errorf("invalid scope: status=%d, want 400", code)
	}

	// 3. plain member (role=user) cannot create → 403.
	code, _ = callCreateMgmtToken(mt, member, "user", map[string]interface{}{
		"name": "x", "scopes": []string{"read:usage"},
	})
	if code != http.StatusForbidden {
		t.Errorf("member create: status=%d, want 403", code)
	}

	// 4. authenticate() resolves the token to entA with its scopes.
	mc, isMgmt := mt.authenticate(ctx, "Bearer "+rawToken)
	if !isMgmt || mc == nil {
		t.Fatalf("authenticate valid token: isMgmt=%v mc=%v", isMgmt, mc)
	}
	if mc.EnterpriseID != entA {
		t.Errorf("token enterprise = %q, want %q (isolation)", mc.EnterpriseID, entA)
	}
	if !mgmtHasScope(mc.Scopes, "read:usage") || !mgmtHasScope(mc.Scopes, "read:pricing") {
		t.Errorf("scopes = %v, want read:usage+read:pricing", mc.Scopes)
	}

	// 5. RequireManagementScope enforces scope: read:usage ok, manage:keys denied.
	runScope := func(scope string) int {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/mgmt/usage", nil)
		c.Request.Header.Set("Authorization", "Bearer "+rawToken)
		mt.RequireManagementScope(scope)(c)
		return w.Code
	}
	if got := runScope("read:usage"); got != http.StatusOK && got != 0 {
		// 0 = not aborted (c.Next proceeds to nothing in test) → treated as pass
		if got == http.StatusForbidden || got == http.StatusUnauthorized {
			t.Errorf("read:usage scope wrongly denied: %d", got)
		}
	}
	if got := runScope("manage:keys"); got != http.StatusForbidden {
		t.Errorf("manage:keys should be denied (token lacks it): status=%d, want 403", got)
	}

	// 6. revoke → token no longer authenticates.
	_, _ = d.Pool.Exec(ctx, `UPDATE management_tokens SET status='revoked' WHERE token_hash=$1`, crypto.HashKeySHA256(rawToken))
	if mcR, _ := mt.authenticate(ctx, "Bearer "+rawToken); mcR != nil {
		t.Errorf("revoked token still authenticates")
	}

	// 7. expired token does not authenticate.
	_, _ = d.Pool.Exec(ctx,
		`UPDATE management_tokens SET status='active', expires_at = NOW() - INTERVAL '1 day' WHERE token_hash=$1`,
		crypto.HashKeySHA256(rawToken))
	if mcE, _ := mt.authenticate(ctx, "Bearer "+rawToken); mcE != nil {
		t.Errorf("expired token still authenticates")
	}

	// 8. a non-mgmt header is reported as not-a-mgmt-token (fall-through).
	if _, isMgmt := mt.authenticate(ctx, "Bearer sk-wgit-abc"); isMgmt {
		t.Errorf("sk- key should not be treated as a mgmt token")
	}
}

// TestManagementToken_OwnerRevalidation is the P1-3 regression guard: a token
// must stop authenticating the moment its owner is suspended, demoted, removed
// from the enterprise, or the enterprise itself is suspended — not only when the
// token row is explicitly revoked.
func TestManagementToken_OwnerRevalidation(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	mt := newMgmtTestRoutes(d)
	ctx := context.Background()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	ent := createApprovalTestEnterprise(t, d, "mgmt-rv-"+suffix, "")
	owner := createGateTestUser(t, d, "mgmt-rv-"+suffix+"@test.com", 0, ent)
	_, _ = d.Pool.Exec(ctx, `UPDATE users SET role='enterprise_admin' WHERE id=$1::uuid`, owner)
	_, _ = d.Pool.Exec(ctx, `INSERT INTO enterprise_members (enterprise_id, user_id, role, joined_at) VALUES ($1::uuid,$2::uuid,'admin',NOW()) ON CONFLICT (enterprise_id, user_id) DO UPDATE SET role='admin', removed_at=NULL`, ent, owner)
	defer func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM management_tokens WHERE user_id=$1::uuid`, owner)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM enterprise_members WHERE enterprise_id=$1::uuid`, ent)
		cleanupGateTestData(t, d, suffix)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM enterprises WHERE id=$1::uuid`, ent)
	}()

	cases := []struct {
		name   string
		break_ func()
		reset  func()
	}{
		{"owner suspended",
			func() { _, _ = d.Pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1::uuid`, owner) },
			func() { _, _ = d.Pool.Exec(ctx, `UPDATE users SET status='active' WHERE id=$1::uuid`, owner) }},
		{"owner demoted (role != enterprise_admin)",
			func() { _, _ = d.Pool.Exec(ctx, `UPDATE users SET role='user' WHERE id=$1::uuid`, owner) },
			func() { _, _ = d.Pool.Exec(ctx, `UPDATE users SET role='enterprise_admin' WHERE id=$1::uuid`, owner) }},
		{"owner removed from enterprise_members",
			func() {
				_, _ = d.Pool.Exec(ctx, `UPDATE enterprise_members SET removed_at=NOW() WHERE user_id=$1::uuid`, owner)
			},
			func() {
				_, _ = d.Pool.Exec(ctx, `UPDATE enterprise_members SET removed_at=NULL WHERE user_id=$1::uuid`, owner)
			}},
		{"enterprise suspended",
			func() { _, _ = d.Pool.Exec(ctx, `UPDATE enterprises SET status='suspended' WHERE id=$1::uuid`, ent) },
			func() { _, _ = d.Pool.Exec(ctx, `UPDATE enterprises SET status='active' WHERE id=$1::uuid`, ent) }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := seedMgmtToken(t, d, owner, ent, []string{"manage:keys"})
			// Sanity: eligible owner authenticates.
			if mc, _ := mt.authenticate(ctx, "Bearer "+raw); mc == nil {
				t.Fatalf("precondition: eligible token should authenticate")
			}
			tc.break_()
			if mc, _ := mt.authenticate(ctx, "Bearer "+raw); mc != nil {
				t.Errorf("token still authenticates after %q", tc.name)
			}
			tc.reset()
			// After reset the same token authenticates again (guard is live, not sticky).
			if mc, _ := mt.authenticate(ctx, "Bearer "+raw); mc == nil {
				t.Errorf("token should authenticate again after reset of %q", tc.name)
			}
			_, _ = d.Pool.Exec(ctx, `DELETE FROM management_tokens WHERE token_hash=$1`, crypto.HashKeySHA256(raw))
		})
	}

	// Explicit revoke helper: RevokeManagementTokensForUser kills all the owner's
	// tokens in the enterprise immediately.
	raw := seedMgmtToken(t, d, owner, ent, []string{"manage:keys"})
	if mc, _ := mt.authenticate(ctx, "Bearer "+raw); mc == nil {
		t.Fatalf("precondition: token should authenticate before explicit revoke")
	}
	RevokeManagementTokensForUser(ctx, d, nil, owner, ent)
	if mc, _ := mt.authenticate(ctx, "Bearer "+raw); mc != nil {
		t.Errorf("token still authenticates after RevokeManagementTokensForUser")
	}
}

// seedMgmtToken inserts an active management token for (userID, enterpriseID)
// with the given scopes and returns the raw token.
func seedMgmtToken(t *testing.T, d *db.DB, userID, enterpriseID string, scopes []string) string {
	t.Helper()
	raw := "mgmt-" + uuid.NewString()
	sj, _ := json.Marshal(scopes)
	_, err := d.Pool.Exec(context.Background(),
		`INSERT INTO management_tokens (token_hash, token_prefix, name, user_id, enterprise_id, scopes, expires_at)
		 VALUES ($1,$2,'t',$3::uuid,$4::uuid,$5::jsonb, NOW()+INTERVAL '365 days')`,
		crypto.HashKeySHA256(raw), raw[:12]+"...", userID, enterpriseID, string(sj))
	if err != nil {
		t.Fatalf("seed mgmt token: %v", err)
	}
	return raw
}

// mgmtReq runs a mgmt-token-authed request through RequireManagementScope + handler.
func mgmtReq(mt *ManagementTokenRoutes, method, path, scope, token string, params gin.Params, body interface{}, handler gin.HandlerFunc) (int, map[string]interface{}) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	c.Request = httptest.NewRequest(method, path, bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Authorization", "Bearer "+token)
	c.Params = params
	mt.RequireManagementScope(scope)(c)
	if !c.IsAborted() {
		handler(c)
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w.Code, resp
}

// TestManagementToken_P2P3 covers manage:keys + read:members/manage:members:
// enterprise-scoped key create/disable/delete and member list/quota, plus
// scope enforcement (a read-only token cannot manage).
func TestManagementToken_P2P3(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	mt := newMgmtTestRoutes(d)
	ctx := context.Background()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	entID := createApprovalTestEnterprise(t, d, "p23-"+suffix, "")
	admin := createGateTestUser(t, d, "p23-"+suffix+"-adm@test.com", 0, entID)
	member := createGateTestUser(t, d, "p23-"+suffix+"-mbr@test.com", 0, entID)
	// enterprise membership rows
	_, _ = d.Pool.Exec(ctx, `INSERT INTO enterprise_members (enterprise_id, user_id, role, joined_at) VALUES ($1::uuid,$2::uuid,'admin',NOW()),($1::uuid,$3::uuid,'member',NOW()) ON CONFLICT (enterprise_id, user_id) DO UPDATE SET role=EXCLUDED.role, removed_at=NULL`, entID, admin, member)
	// The token owner must be a real enterprise_admin so authenticate()'s
	// owner re-validation accepts the token.
	_, _ = d.Pool.Exec(ctx, `UPDATE users SET role='enterprise_admin' WHERE id=$1::uuid`, admin)
	// an enterprise group with a model
	var groupID string
	_ = d.Pool.QueryRow(ctx,
		`INSERT INTO user_groups (name, group_type, models, enterprise_id, visibility, status)
		 VALUES ($1,'enterprise','["claude-sonnet-4-6"]'::jsonb,$2::uuid,'public','active') RETURNING id::text`,
		"p23-grp-"+suffix, entID).Scan(&groupID)

	full := seedMgmtToken(t, d, admin, entID, []string{"manage:keys", "read:members", "manage:members"})
	readonly := seedMgmtToken(t, d, admin, entID, []string{"read:usage"})

	defer func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM management_tokens WHERE user_id=$1::uuid`, admin)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM enterprise_audit_log WHERE enterprise_id=$1::uuid`, entID)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM api_keys WHERE enterprise_id=$1::uuid`, entID)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM enterprise_members WHERE enterprise_id=$1::uuid`, entID)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM user_groups WHERE id=$1::uuid`, groupID)
		cleanupGateTestData(t, d, suffix)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM enterprises WHERE id=$1::uuid`, entID)
	}()

	// P2: create enterprise key via manage:keys.
	code, resp := mgmtReq(mt, http.MethodPost, "/api/mgmt/keys", "manage:keys", full, nil,
		map[string]interface{}{"name": "ci-key", "group_id": groupID}, mt.mgmtCreateKey)
	if code != http.StatusOK || resp["key"] == nil {
		t.Fatalf("create key via mgmt: code=%d resp=%v", code, resp)
	}
	keyID, _ := resp["id"].(string)
	// bound to the enterprise + assigned to token owner.
	var ent, assigned string
	_ = d.Pool.QueryRow(ctx, `SELECT COALESCE(enterprise_id::text,''),COALESCE(assigned_user_id::text,'') FROM api_keys WHERE id=$1::uuid`, keyID).Scan(&ent, &assigned)
	if ent != entID || assigned != admin {
		t.Errorf("created key ent=%s assigned=%s, want %s/%s", ent, assigned, entID, admin)
	}
	// mgmt create-key must also write enterprise_audit_log key_created.
	var nAudit int
	_ = d.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM enterprise_audit_log WHERE enterprise_id=$1::uuid AND category='key' AND change_type='key_created' AND target_id=$2`, entID, keyID).Scan(&nAudit)
	if nAudit != 1 {
		t.Errorf("mgmt create-key enterprise_audit_log count=%d, want 1", nAudit)
	}

	// scope enforcement: read-only token cannot create.
	code, _ = mgmtReq(mt, http.MethodPost, "/api/mgmt/keys", "manage:keys", readonly, nil,
		map[string]interface{}{"name": "x", "group_id": groupID}, mt.mgmtCreateKey)
	if code != http.StatusForbidden {
		t.Errorf("readonly token create key: code=%d, want 403", code)
	}

	// disable then delete the key.
	code, _ = mgmtReq(mt, http.MethodPost, "/api/mgmt/keys/"+keyID+"/disable", "manage:keys", full, gin.Params{{Key: "id", Value: keyID}}, nil, mt.mgmtDisableKey)
	if code != http.StatusOK {
		t.Errorf("disable key: code=%d, want 200", code)
	}
	var st string
	_ = d.Pool.QueryRow(ctx, `SELECT status FROM api_keys WHERE id=$1::uuid`, keyID).Scan(&st)
	if st != "disabled" {
		t.Errorf("key status=%s, want disabled", st)
	}
	code, _ = mgmtReq(mt, http.MethodDelete, "/api/mgmt/keys/"+keyID, "manage:keys", full, gin.Params{{Key: "id", Value: keyID}}, nil, mt.mgmtDeleteKey)
	if code != http.StatusOK {
		t.Errorf("delete key: code=%d, want 200", code)
	}

	// P3: list members (read:members). Note the full token has no read:members?
	// It does. Verify list returns both admin + member.
	fullR := seedMgmtToken(t, d, admin, entID, []string{"read:members", "manage:members"})
	code, resp = mgmtReq(mt, http.MethodGet, "/api/mgmt/members", "read:members", fullR, nil, nil, mt.mgmtListMembers)
	if code != http.StatusOK {
		t.Fatalf("list members: code=%d resp=%v", code, resp)
	}
	if arr, ok := resp["members"].([]interface{}); !ok || len(arr) < 2 {
		t.Errorf("members list = %v, want >=2", resp["members"])
	}

	// manage:members: set member quota limit.
	code, _ = mgmtReq(mt, http.MethodPut, "/api/mgmt/members/"+member+"/quota-limit", "manage:members", fullR,
		gin.Params{{Key: "userId", Value: member}}, map[string]interface{}{"limit": 5000000}, mt.mgmtSetMemberQuota)
	if code != http.StatusOK {
		t.Errorf("set member quota: code=%d, want 200", code)
	}
	var ql int64
	_ = d.Pool.QueryRow(ctx, `SELECT quota_limit FROM enterprise_members WHERE enterprise_id=$1::uuid AND user_id=$2::uuid`, entID, member).Scan(&ql)
	if ql != 5000000 {
		t.Errorf("member quota_limit=%d, want 5000000", ql)
	}

	// remove member → membership removed + keys disabled.
	code, _ = mgmtReq(mt, http.MethodDelete, "/api/mgmt/members/"+member, "manage:members", fullR,
		gin.Params{{Key: "userId", Value: member}}, nil, mt.mgmtRemoveMember)
	if code != http.StatusOK {
		t.Errorf("remove member: code=%d, want 200", code)
	}
	var removed int
	_ = d.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM enterprise_members WHERE enterprise_id=$1::uuid AND user_id=$2::uuid AND removed_at IS NULL`, entID, member).Scan(&removed)
	if removed != 0 {
		t.Errorf("member still active after removal")
	}
}

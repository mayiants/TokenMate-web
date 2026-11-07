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

	"github.com/mayiants/ai-portal-go/internal/config"
	"github.com/mayiants/ai-portal-go/pkg/audit"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// callCreateKey invokes createKey as the given user/role with a JSON body.
func callCreateKey(r *APIKeyRoutes, userID, role string, body map[string]interface{}) (int, map[string]interface{}) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	raw, _ := json.Marshal(body)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/keys", bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user_id", userID)
	c.Set("email", "selfserve@test.com")
	c.Set("role", role)
	r.createKey(c)
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w.Code, resp
}

// makeEntGroup inserts an enterprise group with explicit visibility + models.
func makeEntGroup(t *testing.T, r *APIKeyRoutes, name, enterpriseID, visibility string, models []string) string {
	t.Helper()
	modelsJSON, _ := json.Marshal(models)
	var id string
	err := r.db.Pool.QueryRow(context.Background(),
		`INSERT INTO user_groups (name, group_type, models, enterprise_id, visibility, status, rate_limit_rpm)
		 VALUES ($1, 'enterprise', $2::jsonb, $3::uuid, $4, 'active', 1000) RETURNING id::text`,
		name, string(modelsJSON), enterpriseID, visibility,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert enterprise group: %v", err)
	}
	return id
}

// TestCreateKey_EnterpriseSelfService covers scheme-3 controlled self-service:
// an enterprise member can self-create an enterprise key bound to a
// member-visible (public) group in their own enterprise, but is blocked from
// admin_only groups, cross-enterprise groups, models outside the group, and
// when they belong to no enterprise.
func TestCreateKey_EnterpriseSelfService(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	r := NewAPIKeyRoutes(d, &config.Config{Auth: config.AuthConfig{EncryptionKey: "test-encryption-key-32-characters!!"}}, zap.NewNop(), nil, &audit.Logger{})

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	entA := createApprovalTestEnterprise(t, d, "selfserve-a-"+suffix, "")
	entB := createApprovalTestEnterprise(t, d, "selfserve-b-"+suffix, "")
	member := createGateTestUser(t, d, "gate-"+suffix+"-mbr@test.com", 0, entA)

	pubGroup := makeEntGroup(t, r, "pub-"+suffix, entA, "public", []string{"claude-sonnet-4-6"})
	adminGroup := makeEntGroup(t, r, "adm-"+suffix, entA, "admin_only", []string{"claude-sonnet-4-6"})
	otherEntGroup := makeEntGroup(t, r, "other-"+suffix, entB, "public", []string{"claude-sonnet-4-6"})

	defer func() {
		ctx := context.Background()
		_, _ = d.Pool.Exec(ctx, `DELETE FROM api_keys WHERE user_id = $1::uuid`, member)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM user_groups WHERE id = ANY($1::uuid[])`,
			[]string{pubGroup, adminGroup, otherEntGroup})
		cleanupGateTestData(t, d, suffix)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM enterprises WHERE id = ANY($1::uuid[])`, []string{entA, entB})
	}()

	// 1. Member self-creates an enterprise key in a public group of their own
	//    enterprise → allowed, and the key is bound to the enterprise + self.
	code, resp := callCreateKey(r, member, "user", map[string]interface{}{
		"name": "self-ent", "key_type": "enterprise", "group_priority": []string{pubGroup},
	})
	if code != http.StatusOK {
		t.Fatalf("self-serve public group: status=%d, want 200 (resp: %v)", code, resp)
	}
	keyID, _ := resp["id"].(string)
	var entID, assigned, ktype string
	if err := d.Pool.QueryRow(context.Background(),
		`SELECT COALESCE(enterprise_id::text,''), COALESCE(assigned_user_id::text,''), key_type
		 FROM api_keys WHERE id = $1::uuid`, keyID).Scan(&entID, &assigned, &ktype); err != nil {
		t.Fatalf("select created key: %v", err)
	}
	if entID != entA {
		t.Errorf("key enterprise_id = %q, want %q (must bind caller's enterprise)", entID, entA)
	}
	if assigned != member {
		t.Errorf("key assigned_user_id = %q, want %q (self)", assigned, member)
	}
	if ktype != "enterprise" {
		t.Errorf("key_type = %q, want enterprise", ktype)
	}

	// 2. admin_only group in own enterprise → hidden from members → 403.
	code, _ = callCreateKey(r, member, "user", map[string]interface{}{
		"name": "adm", "key_type": "enterprise", "group_priority": []string{adminGroup},
	})
	if code != http.StatusForbidden {
		t.Errorf("admin_only group self-serve: status=%d, want 403", code)
	}

	// 3. Public group in a DIFFERENT enterprise → cross-enterprise → 403.
	code, _ = callCreateKey(r, member, "user", map[string]interface{}{
		"name": "cross", "key_type": "enterprise", "group_priority": []string{otherEntGroup},
	})
	if code != http.StatusForbidden {
		t.Errorf("cross-enterprise group: status=%d, want 403", code)
	}

	// 4. Enterprise key with no group → 400.
	code, _ = callCreateKey(r, member, "user", map[string]interface{}{
		"name": "nogroup", "key_type": "enterprise",
	})
	if code != http.StatusBadRequest {
		t.Errorf("enterprise key without group: status=%d, want 400", code)
	}

	// 5. Model outside the group's model list → 400 (subset enforcement).
	code, _ = callCreateKey(r, member, "user", map[string]interface{}{
		"name": "badmodel", "key_type": "enterprise", "group_priority": []string{pubGroup},
		"model_routing_mode": "round_robin", "selected_models": []string{"gpt-4o"},
	})
	if code != http.StatusBadRequest {
		t.Errorf("model outside group: status=%d, want 400", code)
	}

	// 6. A user with NO enterprise cannot create an enterprise key → 403.
	loner := createGateTestUser(t, d, "gate-"+suffix+"-loner@test.com", 0, "")
	defer func() {
		_, _ = d.Pool.Exec(context.Background(), `DELETE FROM api_keys WHERE user_id = $1::uuid`, loner)
	}()
	code, _ = callCreateKey(r, loner, "user", map[string]interface{}{
		"name": "noent", "key_type": "enterprise", "group_priority": []string{pubGroup},
	})
	if code != http.StatusForbidden {
		t.Errorf("non-enterprise user: status=%d, want 403", code)
	}
}

// TestDeleteEnterpriseKey_WritesEnterpriseAudit verifies that deleting an
// enterprise key via the user-facing deleteKey path records a 'key_deleted'
// event in enterprise_audit_log (regression: only global audit_logs was
// written, so the enterprise/audit page showed a create with no delete).
func TestDeleteEnterpriseKey_WritesEnterpriseAudit(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	r := NewAPIKeyRoutes(d, &config.Config{Auth: config.AuthConfig{EncryptionKey: "test-encryption-key-32-characters!!"}}, zap.NewNop(), nil, &audit.Logger{})
	ctx := context.Background()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	entID := createApprovalTestEnterprise(t, d, "audit-"+suffix, "")
	owner := createGateTestUser(t, d, "audit-"+suffix+"@test.com", 0, entID)
	var keyID string
	_ = d.Pool.QueryRow(ctx,
		`INSERT INTO api_keys (user_id, name, key_hash, key_prefix, key_encrypted, key_type, status, enterprise_id, assigned_user_id, access_mode)
		 VALUES ($1::uuid,'audit-key',$2,'sk-wgit-en','','enterprise','active',$3::uuid,$1::uuid,'enterprise') RETURNING id::text`,
		owner, "hash-audit-"+suffix, entID).Scan(&keyID)

	defer func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM enterprise_audit_log WHERE enterprise_id=$1::uuid`, entID)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM api_keys WHERE user_id=$1::uuid`, owner)
		cleanupGateTestData(t, d, suffix)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM enterprises WHERE id=$1::uuid`, entID)
	}()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/keys/"+keyID, nil)
	c.Params = gin.Params{{Key: "id", Value: keyID}}
	c.Set("user_id", owner)
	c.Set("email", "audit@test.com")
	r.deleteKey(c)
	if w.Code != http.StatusOK {
		t.Fatalf("deleteKey: status=%d, want 200 (body %s)", w.Code, w.Body.String())
	}

	var n int
	_ = d.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM enterprise_audit_log WHERE enterprise_id=$1::uuid AND category='key' AND change_type='key_deleted' AND target_id=$2`,
		entID, keyID).Scan(&n)
	if n != 1 {
		t.Errorf("enterprise_audit_log key_deleted count = %d, want 1 (delete must be audited to enterprise log)", n)
	}
}

// TestCreateKey_InheritedPlatformGroup covers inherit_to_enterprises: an
// enterprise member can self-create an enterprise key directly against a
// platform template group marked for inheritance; non-inherited templates and
// non-enterprise users remain blocked.
func TestCreateKey_InheritedPlatformGroup(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	r := NewAPIKeyRoutes(d, &config.Config{Auth: config.AuthConfig{EncryptionKey: "test-encryption-key-32-characters!!"}}, zap.NewNop(), nil, &audit.Logger{})

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	entA := createApprovalTestEnterprise(t, d, "inherit-a-"+suffix, "")
	member := createGateTestUser(t, d, "inherit-"+suffix+"-mbr@test.com", 0, entA)
	loner := createGateTestUser(t, d, "inherit-"+suffix+"-loner@test.com", 0, "")

	// Platform template groups: one inherited, one not.
	modelsJSON := `["claude-sonnet-4-6"]`
	var inhGroup, plainGroup string
	err := d.Pool.QueryRow(context.Background(),
		`INSERT INTO user_groups (name, group_type, models, enterprise_id, visibility, status, inherit_to_enterprises)
		 VALUES ($1, 'enterprise', $2::jsonb, NULL, 'public', 'active', TRUE) RETURNING id::text`,
		"inh-"+suffix, modelsJSON).Scan(&inhGroup)
	if err != nil {
		t.Fatalf("insert inherited platform group: %v", err)
	}
	err = d.Pool.QueryRow(context.Background(),
		`INSERT INTO user_groups (name, group_type, models, enterprise_id, visibility, status, inherit_to_enterprises)
		 VALUES ($1, 'enterprise', $2::jsonb, NULL, 'public', 'active', FALSE) RETURNING id::text`,
		"plain-"+suffix, modelsJSON).Scan(&plainGroup)
	if err != nil {
		t.Fatalf("insert plain platform group: %v", err)
	}

	defer func() {
		ctx := context.Background()
		_, _ = d.Pool.Exec(ctx, `DELETE FROM api_keys WHERE user_id = ANY($1::uuid[])`, []string{member, loner})
		_, _ = d.Pool.Exec(ctx, `DELETE FROM user_groups WHERE id = ANY($1::uuid[])`, []string{inhGroup, plainGroup})
		cleanupGateTestData(t, d, suffix)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM enterprises WHERE id = $1::uuid`, entA)
	}()

	// 1. Member uses the inherited platform template directly → allowed.
	code, resp := callCreateKey(r, member, "user", map[string]interface{}{
		"name": "inh-key", "key_type": "enterprise", "group_priority": []string{inhGroup},
	})
	if code != http.StatusOK {
		t.Fatalf("inherited platform group self-serve: status=%d, want 200 (resp: %v)", code, resp)
	}

	// 2. Non-inherited platform template stays blocked → 403.
	code, _ = callCreateKey(r, member, "user", map[string]interface{}{
		"name": "plain-key", "key_type": "enterprise", "group_priority": []string{plainGroup},
	})
	if code != http.StatusForbidden {
		t.Errorf("non-inherited platform group: status=%d, want 403", code)
	}

	// 3. Non-enterprise user cannot use the inherited group → 403.
	code, _ = callCreateKey(r, loner, "user", map[string]interface{}{
		"name": "loner-key", "key_type": "enterprise", "group_priority": []string{inhGroup},
	})
	if code != http.StatusForbidden {
		t.Errorf("non-enterprise user on inherited group: status=%d, want 403", code)
	}
}

// TestCreateKey_PackageKeyPlanBinding covers the plan-bound package key flow:
// subscription keys bind to a chosen active subscription, take the plan's
// model allowlist as their model pool, and never touch subscription groups.
func TestCreateKey_PackageKeyPlanBinding(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	r := NewAPIKeyRoutes(d, &config.Config{Auth: config.AuthConfig{EncryptionKey: "test-encryption-key-32-characters!!"}}, zap.NewNop(), nil, &audit.Logger{})

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	user := createGateTestUser(t, d, "pkg-"+suffix+"@test.com", 0, "")
	other := createGateTestUser(t, d, "pkg-other-"+suffix+"@test.com", 0, "")

	var planID, subID, otherSubID string
	err := d.Pool.QueryRow(context.Background(),
		`INSERT INTO subscription_plans (name, plan_type, price, quota, duration_days, model_permissions, managed_by)
		 VALUES ($1, 'token', 1000000, 100000000, 30, '["glm-5.3"]', 'admin') RETURNING id::text`,
		"pkg-plan-"+suffix).Scan(&planID)
	if err != nil {
		t.Fatalf("insert plan: %v", err)
	}
	err = d.Pool.QueryRow(context.Background(),
		`INSERT INTO user_subscriptions (user_id, plan_id, plan_name, plan_type, quota_total, quota_remaining, status, started_at, expires_at)
		 VALUES ($1::uuid, $2::uuid, 'pkg-plan', 'token', 100000000, 100000000, 'active', NOW(), NOW()+INTERVAL '30 days') RETURNING id::text`,
		user, planID).Scan(&subID)
	if err != nil {
		t.Fatalf("insert subscription: %v", err)
	}
	err = d.Pool.QueryRow(context.Background(),
		`INSERT INTO user_subscriptions (user_id, plan_id, plan_name, plan_type, quota_total, quota_remaining, status, started_at, expires_at)
		 VALUES ($1::uuid, $2::uuid, 'pkg-plan', 'token', 100000000, 100000000, 'active', NOW(), NOW()+INTERVAL '30 days') RETURNING id::text`,
		other, planID).Scan(&otherSubID)
	if err != nil {
		t.Fatalf("insert other subscription: %v", err)
	}

	defer func() {
		ctx := context.Background()
		_, _ = d.Pool.Exec(ctx, `DELETE FROM api_keys WHERE user_id = ANY($1::uuid[])`, []string{user, other})
		_, _ = d.Pool.Exec(ctx, `DELETE FROM user_subscriptions WHERE id = ANY($1::uuid[])`, []string{subID, otherSubID})
		_, _ = d.Pool.Exec(ctx, `DELETE FROM subscription_plans WHERE id = $1::uuid`, planID)
		cleanupGateTestData(t, d, suffix)
	}()

	// 1. Bound to own active subscription → 200; key carries subscription_id and
	//    no group.
	code, resp := callCreateKey(r, user, "user", map[string]interface{}{
		"name": "pkg-key", "key_type": "subscription", "subscription_id": subID,
		"model_routing_mode": "single", "current_model": "glm-5.3",
	})
	if code != http.StatusOK {
		t.Fatalf("bound package key: status=%d, want 200 (resp: %v)", code, resp)
	}
	keyID, _ := resp["id"].(string)
	var gotSub, gotGroup string
	if err := d.Pool.QueryRow(context.Background(),
		`SELECT COALESCE(subscription_id::text,''), COALESCE(group_id::text,'') FROM api_keys WHERE id = $1::uuid`, keyID,
	).Scan(&gotSub, &gotGroup); err != nil {
		t.Fatalf("select created key: %v", err)
	}
	if gotSub != subID {
		t.Errorf("subscription_id = %q, want %q", gotSub, subID)
	}
	if gotGroup != "" {
		t.Errorf("group_id = %q, want empty (no subscription group)", gotGroup)
	}

	// 2. No subscription_id → 400.
	code, _ = callCreateKey(r, user, "user", map[string]interface{}{
		"name": "no-sub", "key_type": "subscription", "model_routing_mode": "single", "current_model": "glm-5.3",
	})
	if code != http.StatusBadRequest {
		t.Errorf("missing subscription_id: status=%d, want 400", code)
	}

	// 3. Another user's subscription → 400.
	code, _ = callCreateKey(r, user, "user", map[string]interface{}{
		"name": "foreign-sub", "key_type": "subscription", "subscription_id": otherSubID,
		"model_routing_mode": "single", "current_model": "glm-5.3",
	})
	if code != http.StatusBadRequest {
		t.Errorf("foreign subscription: status=%d, want 400", code)
	}

	// 4. Model outside the plan allowlist → 400.
	code, _ = callCreateKey(r, user, "user", map[string]interface{}{
		"name": "bad-model", "key_type": "subscription", "subscription_id": subID,
		"model_routing_mode": "round_robin", "selected_models": []string{"claude-opus-4.6"},
	})
	if code != http.StatusBadRequest {
		t.Errorf("model outside plan: status=%d, want 400", code)
	}
}

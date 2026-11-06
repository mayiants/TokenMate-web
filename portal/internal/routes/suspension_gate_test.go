package routes

import (
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
	"github.com/ai-proxy/ai-portal-go/pkg/mail"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// --- helpers for suspension-gate tests ---

// createGateTestUser inserts a user with a balance and optional enterprise.
func createGateTestUser(t *testing.T, d *db.DB, email string, balance int64, enterpriseID string) string {
	t.Helper()
	var id string
	err := d.Pool.QueryRow(context.Background(),
		`INSERT INTO users (name, email, password_hash, role, balance, enterprise_id)
		 VALUES ($1, $2, 'test-hash', 'user', $3, NULLIF($4, '')::uuid) RETURNING id::text`,
		"Gate Test", email, balance, enterpriseID,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert user %s: %v", email, err)
	}
	return id
}

// createGateTestKey inserts an active key for rawKey and returns its ID.
func createGateTestKey(t *testing.T, d *db.DB, userID, rawKey, keyType, enterpriseID string) string {
	t.Helper()
	var id string
	err := d.Pool.QueryRow(context.Background(),
		`INSERT INTO api_keys (user_id, name, key_hash, key_prefix, key_encrypted, key_type, status, enterprise_id, access_mode)
		 VALUES ($1::uuid, 'gate-key', $2, $3, '', $4, 'active', NULLIF($5, '')::uuid, 'general') RETURNING id::text`,
		userID, crypto.HashKeySHA256(rawKey), rawKey[:7], keyType, enterpriseID,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert key: %v", err)
	}
	return id
}

// callValidateKey invokes the validateKey handler with a Bearer token.
func callValidateKey(t *testing.T, r *APIKeyRoutes, rawKey string) (int, map[string]interface{}) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/keys/validate", nil)
	c.Request.Header.Set("Authorization", "Bearer "+rawKey)
	r.validateKey(c)
	var body map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w.Code, body
}

func newGateTestAPIKeyRoutes(d *db.DB) *APIKeyRoutes {
	return NewAPIKeyRoutes(d, &config.Config{}, zap.NewNop(), nil, &audit.Logger{})
}

func newGateTestAdminRoutes(d *db.DB) *AdminRoutes {
	return NewAdminRoutes(d, &config.Config{}, zap.NewNop(), nil, &audit.Logger{}, nil, &mail.Sender{})
}

func cleanupGateTestData(t *testing.T, d *db.DB, suffix string) {
	t.Helper()
	ctx := context.Background()
	_, _ = d.Pool.Exec(ctx, `DELETE FROM api_keys WHERE name = 'gate-key' AND key_prefix LIKE 'sk-gt%'`)
	_, _ = d.Pool.Exec(ctx, `DELETE FROM enterprise_members WHERE user_id IN (SELECT id FROM users WHERE email LIKE $1)`, "gate-"+suffix+"%@test.com")
	_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE email LIKE $1`, "gate-"+suffix+"%@test.com")
	// Enterprises are created via createApprovalTestEnterprise, whose contact
	// email is prefixed with "approval-ent-".
	_, _ = d.Pool.Exec(ctx, `DELETE FROM enterprises WHERE contact_email LIKE $1`, "%gate-"+suffix+"%@test.com")
}

// --- tests ---

// TestValidateKey_RejectsSuspendedAndDeletedUsers verifies that keys of
// suspended or soft-deleted users are rejected with disabled-key semantics.
func TestValidateKey_RejectsSuspendedAndDeletedUsers(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	r := newGateTestAPIKeyRoutes(d)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	defer cleanupGateTestData(t, d, suffix)

	// Baseline: active user with balance passes.
	activeUser := createGateTestUser(t, d, "gate-"+suffix+"-active@test.com", 100, "")
	createGateTestKey(t, d, activeUser, "sk-gt-active-"+suffix, "general", "")
	code, body := callValidateKey(t, r, "sk-gt-active-"+suffix)
	if code != http.StatusOK || body["valid"] != true {
		t.Fatalf("active user key: code=%d body=%v, want valid", code, body)
	}

	// Suspended user is rejected.
	suspendedUser := createGateTestUser(t, d, "gate-"+suffix+"-susp@test.com", 100, "")
	createGateTestKey(t, d, suspendedUser, "sk-gt-susp-"+suffix, "general", "")
	if _, err := d.Pool.Exec(context.Background(),
		`UPDATE users SET status = 'suspended' WHERE id = $1::uuid`, suspendedUser); err != nil {
		t.Fatalf("suspend user: %v", err)
	}
	code, body = callValidateKey(t, r, "sk-gt-susp-"+suffix)
	if code != http.StatusOK || body["valid"] != false {
		t.Errorf("suspended user key: code=%d body=%v, want valid=false", code, body)
	}

	// Soft-deleted user is rejected (regression: JOIN previously ignored deleted_at).
	deletedUser := createGateTestUser(t, d, "gate-"+suffix+"-del@test.com", 100, "")
	createGateTestKey(t, d, deletedUser, "sk-gt-del-"+suffix, "general", "")
	if _, err := d.Pool.Exec(context.Background(),
		`UPDATE users SET deleted_at = NOW() WHERE id = $1::uuid`, deletedUser); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	code, body = callValidateKey(t, r, "sk-gt-del-"+suffix)
	if code != http.StatusOK || body["valid"] != false {
		t.Errorf("deleted user key: code=%d body=%v, want valid=false", code, body)
	}
}

// TestValidateKey_RejectsSuspendedEnterprise verifies that keys of members of
// a suspended enterprise are rejected.
func TestValidateKey_RejectsSuspendedEnterprise(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	r := newGateTestAPIKeyRoutes(d)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	defer cleanupGateTestData(t, d, suffix)

	entID := createApprovalTestEnterprise(t, d, "gate-"+suffix, "")
	userID := createGateTestUser(t, d, "gate-"+suffix+"-ent@test.com", 100, entID)
	createGateTestKey(t, d, userID, "sk-gt-ent-"+suffix, "general", "")

	// Active enterprise: key works.
	code, body := callValidateKey(t, r, "sk-gt-ent-"+suffix)
	if code != http.StatusOK || body["valid"] != true {
		t.Fatalf("active enterprise member key: code=%d body=%v, want valid", code, body)
	}

	// Suspend the enterprise: member keys stop working.
	if _, err := d.Pool.Exec(context.Background(),
		`UPDATE enterprises SET status = 'suspended' WHERE id = $1::uuid`, entID); err != nil {
		t.Fatalf("suspend enterprise: %v", err)
	}
	code, body = callValidateKey(t, r, "sk-gt-ent-"+suffix)
	if code != http.StatusOK || body["valid"] != false {
		t.Errorf("suspended enterprise member key: code=%d body=%v, want valid=false", code, body)
	}
}

// TestToggleMemberStatus_SuspensionGatesKeys verifies the enterprise member
// toggle makes validateKey reject the member's keys immediately.
func TestToggleMemberStatus_SuspensionGatesKeys(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	er := newTestEnterpriseRoutes(t, d)
	kr := newGateTestAPIKeyRoutes(d)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	defer cleanupGateTestData(t, d, suffix)

	entID := createApprovalTestEnterprise(t, d, "gate-"+suffix, "")
	userID := createGateTestUser(t, d, "gate-"+suffix+"-mem@test.com", 100, entID)
	createGateTestKey(t, d, userID, "sk-gt-mem-"+suffix, "general", "")

	if _, body := callValidateKey(t, kr, "sk-gt-mem-"+suffix); body["valid"] != true {
		t.Fatalf("precondition: member key should be valid, got %v", body)
	}

	// Enterprise admin suspends the member.
	c, w := newApprovalTestContext(http.MethodPost, "/api/enterprise/members/"+userID+"/toggle", nil)
	c.Params = gin.Params{{Key: "userId", Value: userID}}
	c.Set("enterprise_id", entID)
	c.Set("user_id", "00000000-0000-0000-0000-000000000001")
	c.Set("email", "admin@test.com")
	er.toggleMemberStatus(c)
	if w.Code != http.StatusOK {
		t.Fatalf("toggle member: status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	if _, body := callValidateKey(t, kr, "sk-gt-mem-"+suffix); body["valid"] != false {
		t.Errorf("suspended member key: body=%v, want valid=false", body)
	}
}

// TestAdminToggleUserStatus_SuspensionGatesKeys verifies the admin user
// toggle makes validateKey reject the user's keys immediately.
func TestAdminToggleUserStatus_SuspensionGatesKeys(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	ar := newGateTestAdminRoutes(d)
	kr := newGateTestAPIKeyRoutes(d)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	defer cleanupGateTestData(t, d, suffix)

	userID := createGateTestUser(t, d, "gate-"+suffix+"-adm@test.com", 100, "")
	createGateTestKey(t, d, userID, "sk-gt-adm-"+suffix, "general", "")

	if _, body := callValidateKey(t, kr, "sk-gt-adm-"+suffix); body["valid"] != true {
		t.Fatalf("precondition: user key should be valid, got %v", body)
	}

	c, w := newApprovalTestContext(http.MethodPost, "/api/admin/users/"+userID+"/toggle-status", nil)
	c.Params = gin.Params{{Key: "id", Value: userID}}
	c.Set("user_id", "00000000-0000-0000-0000-000000000001")
	c.Set("email", "admin@test.com")
	ar.toggleUserStatus(c)
	if w.Code != http.StatusOK {
		t.Fatalf("toggle user: status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	if _, body := callValidateKey(t, kr, "sk-gt-adm-"+suffix); body["valid"] != false {
		t.Errorf("suspended user key: body=%v, want valid=false", body)
	}
}

// TestDeleteUser_DisablesKeys verifies that admin user deletion disables the
// user's active keys in the same operation.
func TestDeleteUser_DisablesKeys(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	ar := newGateTestAdminRoutes(d)
	kr := newGateTestAPIKeyRoutes(d)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	defer cleanupGateTestData(t, d, suffix)

	userID := createGateTestUser(t, d, "gate-"+suffix+"-rm@test.com", 100, "")
	keyID := createGateTestKey(t, d, userID, "sk-gt-rm-"+suffix, "general", "")

	c, w := newApprovalTestContext(http.MethodDelete, "/api/admin/users/"+userID, nil)
	c.Params = gin.Params{{Key: "id", Value: userID}}
	c.Set("user_id", "00000000-0000-0000-0000-000000000001")
	ar.deleteUser(c)
	if w.Code != http.StatusOK {
		t.Fatalf("delete user: status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	var keyStatus string
	if err := d.Pool.QueryRow(context.Background(),
		`SELECT status FROM api_keys WHERE id = $1::uuid`, keyID).Scan(&keyStatus); err != nil {
		t.Fatalf("select key status: %v", err)
	}
	if keyStatus != "disabled" {
		t.Errorf("key status after user deletion = %q, want disabled", keyStatus)
	}
	if _, body := callValidateKey(t, kr, "sk-gt-rm-"+suffix); body["valid"] != false {
		t.Errorf("deleted user key: body=%v, want valid=false", body)
	}
}

// TestSuspendEnterprise_DisablesEnterpriseKeys verifies that suspending an
// enterprise disables its enterprise keys, and reactivating does not restore
// them (same semantics as group disable).
func TestSuspendEnterprise_DisablesEnterpriseKeys(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	er := newTestEnterpriseRoutes(t, d)
	kr := newGateTestAPIKeyRoutes(d)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	defer cleanupGateTestData(t, d, suffix)

	entID := createApprovalTestEnterprise(t, d, "gate-"+suffix, "")
	userID := createGateTestUser(t, d, "gate-"+suffix+"-ekey@test.com", 100, entID)
	keyID := createGateTestKey(t, d, userID, "sk-gt-ekey-"+suffix, "enterprise", entID)

	c, w := newApprovalTestContext(http.MethodPost, "/api/admin/enterprises/"+entID+"/suspend", nil)
	c.Params = gin.Params{{Key: "id", Value: entID}}
	c.Set("user_id", "00000000-0000-0000-0000-000000000001")
	er.suspendEnterprise(c)
	if w.Code != http.StatusOK {
		t.Fatalf("suspend enterprise: status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	var keyStatus string
	if err := d.Pool.QueryRow(context.Background(),
		`SELECT status FROM api_keys WHERE id = $1::uuid`, keyID).Scan(&keyStatus); err != nil {
		t.Fatalf("select key status: %v", err)
	}
	if keyStatus != "disabled" {
		t.Errorf("enterprise key status after suspend = %q, want disabled", keyStatus)
	}

	// Reactivate the enterprise: key must stay disabled (no auto-restore).
	c, w = newApprovalTestContext(http.MethodPost, "/api/admin/enterprises/"+entID+"/activate", nil)
	c.Params = gin.Params{{Key: "id", Value: entID}}
	er.activateEnterprise(c)
	if w.Code != http.StatusOK {
		t.Fatalf("activate enterprise: status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	if err := d.Pool.QueryRow(context.Background(),
		`SELECT status FROM api_keys WHERE id = $1::uuid`, keyID).Scan(&keyStatus); err != nil {
		t.Fatalf("select key status: %v", err)
	}
	if keyStatus != "disabled" {
		t.Errorf("enterprise key status after reactivate = %q, want disabled (no auto-restore)", keyStatus)
	}

	// Sanity: the enterprise itself validates again once its key is manually re-enabled.
	if _, err := d.Pool.Exec(context.Background(),
		`UPDATE api_keys SET status = 'active' WHERE id = $1::uuid`, keyID); err != nil {
		t.Fatalf("re-enable key: %v", err)
	}
	if _, body := callValidateKey(t, kr, "sk-gt-ekey-"+suffix); body["valid"] != true {
		t.Errorf("reactivated enterprise with re-enabled key: body=%v, want valid", body)
	}
}

// newGateTestDownstreamRoutes builds DownstreamRoutes with a real Auth so the
// sensitive-ops (CanPerformSensitiveOps) guard can be exercised. Default admin
// mode is "" (production semantics): only super_admin passes.
func newGateTestDownstreamRoutes(d *db.DB) *DownstreamRoutes {
	auth := middleware.NewAuth(&config.Config{}, zap.NewNop())
	return NewDownstreamRoutes(d, auth, &audit.Logger{})
}

// callAdminDeleteKey invokes deleteDownstreamKey with the given caller role.
func callAdminDeleteKey(dr *DownstreamRoutes, keyID, role string) (int, map[string]interface{}) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/admin/downstream-keys/"+keyID, nil)
	c.Params = gin.Params{{Key: "id", Value: keyID}}
	c.Set("user_id", "00000000-0000-0000-0000-000000000001")
	c.Set("email", "admin@test.com")
	c.Set("role", role)
	dr.deleteDownstreamKey(c)
	var body map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w.Code, body
}

// TestAdminDeleteDownstreamKey covers the cross-user admin key delete: the
// sensitive-ops RBAC guard, soft-deletion of an orphaned key (owner already
// soft-deleted), and not-found handling.
func TestAdminDeleteDownstreamKey(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	dr := newGateTestDownstreamRoutes(d)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	defer cleanupGateTestData(t, d, suffix)

	// An orphaned key: create a user + key, then soft-delete the owner (which
	// in production also disables the key). The key row lingers with a dead
	// owner — exactly what admin delete must be able to clean up.
	owner := createGateTestUser(t, d, "gate-"+suffix+"-orphan@test.com", 0, "")
	keyID := createGateTestKey(t, d, owner, "sk-gt-orph-"+suffix, "general", "")
	if _, err := d.Pool.Exec(context.Background(),
		`UPDATE users SET deleted_at = NOW() WHERE id = $1::uuid`, owner); err != nil {
		t.Fatalf("soft-delete owner: %v", err)
	}

	// Non-privileged admin (system_admin in production mode) is rejected and
	// the key must survive.
	code, body := callAdminDeleteKey(dr, keyID, "system_admin")
	if code != http.StatusForbidden {
		t.Fatalf("system_admin delete in production mode: code=%d body=%v, want 403", code, body)
	}
	var deletedAt interface{}
	if err := d.Pool.QueryRow(context.Background(),
		`SELECT deleted_at FROM api_keys WHERE id = $1::uuid`, keyID).Scan(&deletedAt); err != nil {
		t.Fatalf("select key after forbidden delete: %v", err)
	}
	if deletedAt != nil {
		t.Errorf("key deleted_at = %v after forbidden delete, want nil (key must survive)", deletedAt)
	}

	// super_admin succeeds: the orphaned key is soft-deleted and revoked.
	code, body = callAdminDeleteKey(dr, keyID, "super_admin")
	if code != http.StatusOK || body["success"] != true {
		t.Fatalf("super_admin delete: code=%d body=%v, want 200 success", code, body)
	}
	var status string
	if err := d.Pool.QueryRow(context.Background(),
		`SELECT status, deleted_at FROM api_keys WHERE id = $1::uuid`, keyID).Scan(&status, &deletedAt); err != nil {
		t.Fatalf("select key after delete: %v", err)
	}
	if deletedAt == nil {
		t.Errorf("key deleted_at is nil after admin delete, want set")
	}
	if status != "revoked" {
		t.Errorf("key status after admin delete = %q, want revoked", status)
	}

	// Deleting an already-deleted / unknown key returns 404.
	code, _ = callAdminDeleteKey(dr, keyID, "super_admin")
	if code != http.StatusNotFound {
		t.Errorf("re-delete of soft-deleted key: code=%d, want 404", code)
	}
}

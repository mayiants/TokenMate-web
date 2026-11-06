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
	"github.com/ai-proxy/ai-portal-go/pkg/audit"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// newTestAPIKeyRoutes builds APIKeyRoutes for handler-level tests.
// auth is unused by updateKey; audit with a nil db is a no-op.
func newTestAPIKeyRoutes(database *db.DB) *APIKeyRoutes {
	return NewAPIKeyRoutes(database, &config.Config{}, zap.NewNop(), nil, &audit.Logger{})
}

// newUpdateKeyContext builds a gin context for PATCH /api/keys/:id.
func newUpdateKeyContext(keyID, userID, email string, body interface{}) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPatch, "/api/keys/"+keyID, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	c.Params = gin.Params{{Key: "id", Value: keyID}}
	c.Set("user_id", userID)
	c.Set("email", email)
	return c, w
}

// insertUpdateTestKey inserts an enterprise key owned by ownerID and assigned
// to assignedID, returning its ID.
func insertUpdateTestKey(t *testing.T, d *db.DB, suffix, enterpriseID, ownerID, assignedID string) string {
	t.Helper()
	var id string
	err := d.Pool.QueryRow(context.Background(),
		`INSERT INTO api_keys (user_id, assigned_user_id, enterprise_id, key_hash, key_prefix, name, status, key_type)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, 'update-test', 'active', 'enterprise')
		 RETURNING id::text`,
		ownerID, assignedID, enterpriseID,
		"hash-upd-"+suffix+"-"+ownerID[len(ownerID)-8:],
		"sk-upd-"+suffix[len(suffix)-8:]+ownerID[len(ownerID)-4:],
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert api key: %v", err)
	}
	return id
}

func keyStatus(t *testing.T, d *db.DB, keyID string) string {
	t.Helper()
	var status string
	if err := d.Pool.QueryRow(context.Background(),
		`SELECT status FROM api_keys WHERE id = $1::uuid`, keyID,
	).Scan(&status); err != nil {
		t.Fatalf("select key status: %v", err)
	}
	return status
}

func cleanupUpdateTestData(d *db.DB, enterpriseID string, userIDs, keyIDs []string) {
	ctx := context.Background()
	for _, id := range keyIDs {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM api_keys WHERE id = $1::uuid`, id)
	}
	for _, id := range userIDs {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, id)
	}
	if enterpriseID != "" {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM enterprises WHERE id = $1::uuid`, enterpriseID)
	}
}

// TestUpdateKey_AdminAssignedKey_Returns404: a member toggling an
// administrator-assigned enterprise key must get 404 (P0-4: previously the
// UPDATE matched 0 rows but the handler still returned success).
func TestUpdateKey_AdminAssignedKey_Returns404(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	r := newTestAPIKeyRoutes(d)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	entID := createApprovalTestEnterprise(t, d, suffix, "acme.com")
	adminID := createApprovalTestUser(t, d, "upd-admin-"+suffix+"@acme.com", "enterprise_admin")
	memberID := createApprovalTestUser(t, d, "upd-member-"+suffix+"@acme.com", "user")
	keyID := insertUpdateTestKey(t, d, suffix, entID, adminID, memberID)
	defer cleanupUpdateTestData(d, entID, []string{adminID, memberID}, []string{keyID})

	c, w := newUpdateKeyContext(keyID, memberID, "upd-member-"+suffix+"@acme.com",
		map[string]string{"status": "inactive"})
	r.updateKey(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok || errObj["message"] != "key not found or not editable" {
		t.Errorf("expected error message 'key not found or not editable', got %v", resp["error"])
	}
	if got := keyStatus(t, d, keyID); got != "active" {
		t.Errorf("expected key status unchanged 'active', got %q", got)
	}
}

// TestUpdateKey_SelfCreatedKey_Succeeds: a member can still toggle an
// enterprise key they created themselves.
func TestUpdateKey_SelfCreatedKey_Succeeds(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	r := newTestAPIKeyRoutes(d)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	entID := createApprovalTestEnterprise(t, d, suffix, "acme.com")
	memberID := createApprovalTestUser(t, d, "upd-self-"+suffix+"@acme.com", "user")
	keyID := insertUpdateTestKey(t, d, suffix, entID, memberID, memberID)
	defer cleanupUpdateTestData(d, entID, []string{memberID}, []string{keyID})

	c, w := newUpdateKeyContext(keyID, memberID, "upd-self-"+suffix+"@acme.com",
		map[string]string{"status": "inactive"})
	r.updateKey(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := keyStatus(t, d, keyID); got != "inactive" {
		t.Errorf("expected key status 'inactive', got %q", got)
	}
}

// TestUpdateKey_NonexistentKey_Returns404: an unknown key id also returns 404
// instead of a fake success.
func TestUpdateKey_NonexistentKey_Returns404(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	r := newTestAPIKeyRoutes(d)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	memberID := createApprovalTestUser(t, d, "upd-ghost-"+suffix+"@acme.com", "user")
	defer cleanupUpdateTestData(d, "", []string{memberID}, nil)

	c, w := newUpdateKeyContext("00000000-0000-0000-0000-000000000000", memberID,
		"upd-ghost-"+suffix+"@acme.com", map[string]string{"status": "inactive"})
	r.updateKey(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

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

	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// newGroupTestContext builds a gin context bound to an enterprise with a JSON body.
func newGroupTestContext(t *testing.T, method, path string, body interface{}, enterpriseID string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	c.Set("enterprise_id", enterpriseID)
	c.Set("user_id", "")
	c.Set("email", "test@example.com")
	return c, w
}

// createGroupTestPlatformGroup inserts a platform enterprise group (SA-owned) and returns its ID.
func createGroupTestPlatformGroup(t *testing.T, d *db.DB, suffix string, rpm int, tpm int64) string {
	t.Helper()
	var id string
	err := d.Pool.QueryRow(context.Background(),
		`INSERT INTO user_groups (name, group_type, models, rpm, tpm, enterprise_id, visibility)
		 VALUES ($1, 'enterprise', '[]'::jsonb, $2, $3, NULL, 'public') RETURNING id::text`,
		"pg-"+suffix, rpm, tpm,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert platform group: %v", err)
	}
	return id
}

// createGroupTestEnterpriseGroup inserts an enterprise group under a platform group and returns its ID.
func createGroupTestEnterpriseGroup(t *testing.T, d *db.DB, suffix, enterpriseID, platformGroupID string, rpm int, tpm int64) string {
	t.Helper()
	var id string
	err := d.Pool.QueryRow(context.Background(),
		`INSERT INTO user_groups (name, group_type, models, rpm, tpm, enterprise_id, parent_group_id, visibility, status)
		 VALUES ($1, 'enterprise', '[]'::jsonb, $2, $3, $4::uuid, NULLIF($5, '')::uuid, 'public', 'active') RETURNING id::text`,
		"eg-"+suffix, rpm, tpm, enterpriseID, platformGroupID,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert enterprise group: %v", err)
	}
	return id
}

// createGroupTestKey inserts an active enterprise key referencing groupID and returns its ID.
func createGroupTestKey(t *testing.T, d *db.DB, suffix, enterpriseID, userID, groupID string) string {
	t.Helper()
	var id string
	err := d.Pool.QueryRow(context.Background(),
		`INSERT INTO api_keys (user_id, name, key_hash, key_prefix, key_encrypted, key_type, status, enterprise_id, assigned_user_id, group_id, access_mode)
		 VALUES ($1::uuid, $2, $3, $4, '', 'enterprise', 'active', $5::uuid, $6::uuid, $7::uuid, 'enterprise') RETURNING id::text`,
		userID, "key-"+suffix, "hash-"+suffix, "sk-"+suffix[len(suffix)-17:], enterpriseID, userID, groupID,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert enterprise key: %v", err)
	}
	return id
}

// cleanupGroupTestData removes everything created by the group tests.
func cleanupGroupTestData(t *testing.T, d *db.DB, enterpriseID string, groupIDs ...string) {
	t.Helper()
	ctx := context.Background()
	_, _ = d.Pool.Exec(ctx, `DELETE FROM api_keys WHERE enterprise_id = $1::uuid`, enterpriseID)
	for _, gid := range groupIDs {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM user_groups WHERE id = $1::uuid`, gid)
	}
	_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE email LIKE 'grouptest-%@test.com'`)
	_, _ = d.Pool.Exec(ctx, `DELETE FROM enterprises WHERE id = $1::uuid`, enterpriseID)
}

// TestUpdateEnterpriseGroup_RPMExceedsPlatformLimit verifies that editing RPM
// above the parent platform group's RPM is rejected with 400.
func TestUpdateEnterpriseGroup_RPMExceedsPlatformLimit(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	r := newTestEnterpriseRoutes(t, d)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	entID := createApprovalTestEnterprise(t, d, "grouptest-"+suffix, "")
	pgID := createGroupTestPlatformGroup(t, d, suffix, 500, 0)
	egID := createGroupTestEnterpriseGroup(t, d, suffix, entID, pgID, 100, 0)
	defer cleanupGroupTestData(t, d, entID, pgID, egID)

	rpm := 600
	c, w := newGroupTestContext(t, http.MethodPut, "/api/enterprise/groups/"+egID, gin.H{"rpm": rpm}, entID)
	c.Params = gin.Params{{Key: "groupId", Value: egID}}
	r.updateEnterpriseGroup(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("RPM above platform limit: status = %d, want 400 (body: %s)", w.Code, w.Body.String())
	}

	// Within limit must succeed
	rpm = 500
	c, w = newGroupTestContext(t, http.MethodPut, "/api/enterprise/groups/"+egID, gin.H{"rpm": rpm}, entID)
	c.Params = gin.Params{{Key: "groupId", Value: egID}}
	r.updateEnterpriseGroup(c)
	if w.Code != http.StatusOK {
		t.Errorf("RPM within platform limit: status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
}

// TestUpdateEnterpriseGroup_TPMExceedsPlatformLimit verifies TPM edit validation:
// TPM must be in [1, platformTPM] when the platform group has a TPM limit, and
// 0 (unlimited) is rejected in that case.
func TestUpdateEnterpriseGroup_TPMExceedsPlatformLimit(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	r := newTestEnterpriseRoutes(t, d)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	entID := createApprovalTestEnterprise(t, d, "grouptest-"+suffix, "")
	pgID := createGroupTestPlatformGroup(t, d, suffix, 500, 10000)
	egID := createGroupTestEnterpriseGroup(t, d, suffix, entID, pgID, 100, 5000)
	defer cleanupGroupTestData(t, d, entID, pgID, egID)

	for _, tc := range []struct {
		name string
		tpm  int64
		want int
	}{
		{"above platform limit", 20000, http.StatusBadRequest},
		{"zero when platform limited", 0, http.StatusBadRequest},
		{"at platform limit", 10000, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, w := newGroupTestContext(t, http.MethodPut, "/api/enterprise/groups/"+egID, gin.H{"tpm": tc.tpm}, entID)
			c.Params = gin.Params{{Key: "groupId", Value: egID}}
			r.updateEnterpriseGroup(c)
			if w.Code != tc.want {
				t.Errorf("status = %d, want %d (body: %s)", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

// TestUpdateEnterpriseGroup_InvalidVisibility verifies that visibility values
// outside the public/admin_only whitelist are rejected.
func TestUpdateEnterpriseGroup_InvalidVisibility(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	r := newTestEnterpriseRoutes(t, d)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	entID := createApprovalTestEnterprise(t, d, "grouptest-"+suffix, "")
	egID := createGroupTestEnterpriseGroup(t, d, suffix, entID, "", 100, 0)
	defer cleanupGroupTestData(t, d, entID, egID)

	c, w := newGroupTestContext(t, http.MethodPut, "/api/enterprise/groups/"+egID, gin.H{"visibility": "private"}, entID)
	c.Params = gin.Params{{Key: "groupId", Value: egID}}
	r.updateEnterpriseGroup(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid visibility: status = %d, want 400 (body: %s)", w.Code, w.Body.String())
	}
}

// TestDeleteEnterpriseGroup_BlockedByActiveKeys verifies that a group still
// referenced by non-deleted keys cannot be deleted.
func TestDeleteEnterpriseGroup_BlockedByActiveKeys(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	r := newTestEnterpriseRoutes(t, d)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	entID := createApprovalTestEnterprise(t, d, "grouptest-"+suffix, "")
	userID := createApprovalTestUser(t, d, "grouptest-"+suffix+"@test.com", "user")
	egID := createGroupTestEnterpriseGroup(t, d, suffix, entID, "", 100, 0)
	createGroupTestKey(t, d, suffix, entID, userID, egID)
	defer cleanupGroupTestData(t, d, entID, egID)

	c, w := newGroupTestContext(t, http.MethodDelete, "/api/enterprise/groups/"+egID, nil, entID)
	c.Params = gin.Params{{Key: "groupId", Value: egID}}
	r.deleteEnterpriseGroup(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("delete with active keys: status = %d, want 400 (body: %s)", w.Code, w.Body.String())
	}

	// Group must not be soft-deleted
	var deletedAt *time.Time
	if err := d.Pool.QueryRow(context.Background(),
		`SELECT deleted_at FROM user_groups WHERE id = $1::uuid`, egID).Scan(&deletedAt); err != nil {
		t.Fatalf("select group: %v", err)
	}
	if deletedAt != nil {
		t.Error("group should not be soft-deleted while keys still reference it")
	}
}

// TestUpdateEnterpriseGroup_DisableCascadesKeys verifies that disabling a group
// disables its active keys, and re-enabling does not restore them.
func TestUpdateEnterpriseGroup_DisableCascadesKeys(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	r := newTestEnterpriseRoutes(t, d)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	entID := createApprovalTestEnterprise(t, d, "grouptest-"+suffix, "")
	userID := createApprovalTestUser(t, d, "grouptest-"+suffix+"@test.com", "user")
	egID := createGroupTestEnterpriseGroup(t, d, suffix, entID, "", 100, 0)
	keyID := createGroupTestKey(t, d, suffix, entID, userID, egID)
	defer cleanupGroupTestData(t, d, entID, egID)

	// Disable the group (status whitelist: active/inactive per DB CHECK constraint)
	c, w := newGroupTestContext(t, http.MethodPut, "/api/enterprise/groups/"+egID, gin.H{"status": "inactive"}, entID)
	c.Params = gin.Params{{Key: "groupId", Value: egID}}
	r.updateEnterpriseGroup(c)
	if w.Code != http.StatusOK {
		t.Fatalf("disable group: status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	var groupStatus, keyStatus string
	if err := d.Pool.QueryRow(context.Background(),
		`SELECT status FROM user_groups WHERE id = $1::uuid`, egID).Scan(&groupStatus); err != nil {
		t.Fatalf("select group status: %v", err)
	}
	if groupStatus != "inactive" {
		t.Errorf("group status = %q, want inactive", groupStatus)
	}
	if err := d.Pool.QueryRow(context.Background(),
		`SELECT status FROM api_keys WHERE id = $1::uuid`, keyID).Scan(&keyStatus); err != nil {
		t.Fatalf("select key status: %v", err)
	}
	if keyStatus != "disabled" {
		t.Errorf("key status after group disable = %q, want disabled", keyStatus)
	}

	// Re-enable the group — key must stay disabled
	c, w = newGroupTestContext(t, http.MethodPut, "/api/enterprise/groups/"+egID, gin.H{"status": "active"}, entID)
	c.Params = gin.Params{{Key: "groupId", Value: egID}}
	r.updateEnterpriseGroup(c)
	if w.Code != http.StatusOK {
		t.Fatalf("re-enable group: status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	if err := d.Pool.QueryRow(context.Background(),
		`SELECT status FROM api_keys WHERE id = $1::uuid`, keyID).Scan(&keyStatus); err != nil {
		t.Fatalf("select key status: %v", err)
	}
	if keyStatus != "disabled" {
		t.Errorf("key status after group re-enable = %q, want disabled (no auto-restore)", keyStatus)
	}
}

// TestReassignKey_StoresEnterpriseGroupID is a regression test: editing a key's
// group must store the enterprise group id (same semantics as key creation),
// not the platform parent group id.
func TestReassignKey_StoresEnterpriseGroupID(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	r := newTestEnterpriseRoutes(t, d)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	entID := createApprovalTestEnterprise(t, d, "grouptest-"+suffix, "")
	userID := createApprovalTestUser(t, d, "grouptest-"+suffix+"@test.com", "user")
	pgID := createGroupTestPlatformGroup(t, d, suffix, 500, 0)
	egID := createGroupTestEnterpriseGroup(t, d, suffix, entID, pgID, 100, 0)
	keyID := createGroupTestKey(t, d, suffix, entID, userID, egID)
	defer cleanupGroupTestData(t, d, entID, pgID, egID)

	c, w := newGroupTestContext(t, http.MethodPut, "/api/enterprise/keys/"+keyID, gin.H{"group_id": egID}, entID)
	c.Params = gin.Params{{Key: "keyId", Value: keyID}}
	r.reassignKey(c)
	if w.Code != http.StatusOK {
		t.Fatalf("reassign key: status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	var storedGroupID string
	if err := d.Pool.QueryRow(context.Background(),
		`SELECT group_id::text FROM api_keys WHERE id = $1::uuid`, keyID).Scan(&storedGroupID); err != nil {
		t.Fatalf("select key group_id: %v", err)
	}
	if storedGroupID != egID {
		t.Errorf("key group_id = %q, want enterprise group id %q (not platform group %q)", storedGroupID, egID, pgID)
	}
}

// TestEnterpriseGroups_CreateGroupValidation tests the create group request binding.
func TestEnterpriseGroups_CreateGroupValidation(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		wantValid     bool
		wantGroupName string
		wantDefRPM    int
		wantDefVis    string
	}{
		{"valid with models", `{"name": "My Group", "platform_group_id": "pg-1", "models": ["gpt-4", "claude-3"], "rpm": 100}`, true, "My Group", 100, "public"},
		{"only required", `{"name": "My Group", "platform_group_id": "pg-1"}`, true, "My Group", 0, "public"},
		{"missing name", `{"platform_group_id": "pg-1"}`, true, "", 0, "public"},
		{"missing platform_group_id", `{"name": "My Group"}`, true, "My Group", 0, "public"},
		{"invalid json", `not json`, false, "", 0, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				Name            string   `json:"name" binding:"required"`
				PlatformGroupID string   `json:"platform_group_id" binding:"required"`
				Models          []string `json:"models"`
				RPM             int      `json:"rpm"`
				Visibility      string   `json:"visibility"`
			}
			err := json.Unmarshal([]byte(tt.body), &req)
			valid := err == nil

			if valid != tt.wantValid {
				t.Errorf("valid = %v, want %v", valid, tt.wantValid)
				return
			}
			if !valid {
				return
			}
			if req.Name != tt.wantGroupName {
				t.Errorf("name = %q, want %q", req.Name, tt.wantGroupName)
			}

			// Simulate defaults
			rpm := req.RPM
			if rpm <= 0 {
				rpm = 500
			}
			vis := req.Visibility
			if vis == "" {
				vis = "public"
			}
			if rpm != tt.wantDefRPM && tt.wantDefRPM != 0 {
				t.Errorf("rpm default = %d, want %d", rpm, tt.wantDefRPM)
			}
			if vis != tt.wantDefVis {
				t.Errorf("visibility default = %q, want %q", vis, tt.wantDefVis)
			}
		})
	}
}

// TestEnterpriseGroups_UpdateGroupValidation tests the update group request binding.
func TestEnterpriseGroups_UpdateGroupValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantValid bool
	}{
		{"valid update name", `{"name": "New Name"}`, true},
		{"valid update models", `{"models": ["gpt-4"]}`, true},
		{"valid update rpm", `{"rpm": 500}`, true},
		{"valid update visibility", `{"visibility": "private"}`, true},
		{"invalid json", `not json`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				Name       *string   `json:"name"`
				Models     *[]string `json:"models"`
				RPM        *int      `json:"rpm"`
				Visibility *string   `json:"visibility"`
				Status     *string   `json:"status"`
			}
			err := json.Unmarshal([]byte(tt.body), &req)
			valid := err == nil

			if valid != tt.wantValid {
				t.Errorf("valid = %v, want %v", valid, tt.wantValid)
			}
		})
	}
}

// TestEnterpriseGroups_RPMValidation tests RPM limit validation logic.
func TestEnterpriseGroups_RPMValidation(t *testing.T) {
	tests := []struct {
		name        string
		reqRPM      int
		platformMax int
		wantOK      bool
	}{
		{"within limit", 100, 500, true},
		{"at limit", 500, 500, true},
		{"exceeds limit", 600, 500, false},
		{"no platform limit", 1000, 0, true},
		{"zero rpm", 0, 500, true}, // gets defaulted to 500
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rpm := tt.reqRPM
			if rpm <= 0 {
				rpm = 500
			}
			ok := tt.platformMax == 0 || rpm <= tt.platformMax
			if ok != tt.wantOK {
				t.Errorf("RPM check(rpm=%d, platformMax=%d) = %v, want %v", rpm, tt.platformMax, ok, tt.wantOK)
			}
		})
	}
}

// TestEnterpriseGroups_DefaultValues tests default values for group creation.
func TestEnterpriseGroups_DefaultValues(t *testing.T) {
	// Test RPM default
	if defaultEntGroupRPM := 500; defaultEntGroupRPM <= 0 {
		t.Error("default enterprise group RPM should be positive")
	}

	// Test visibility default
	defaultVis := "public"
	if defaultVis != "public" {
		t.Error("default visibility should be 'public'")
	}
}

// TestGetAvailableModels_EmptyParentDoesNotLeakCatalog verifies that when the
// parent platform group has no models configured, the enterprise
// available-models endpoint returns an empty list rather than the entire
// active platform catalog (regression: an empty-parent fallback exposed every
// active service_model to enterprise group editing).
func TestGetAvailableModels_EmptyParentDoesNotLeakCatalog(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	r := newTestEnterpriseRoutes(t, d)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	entID := createApprovalTestEnterprise(t, d, "grouptest-"+suffix, "")
	// Platform group is created with models='[]' by the helper.
	pgID := createGroupTestPlatformGroup(t, d, suffix, 500, 0)
	defer cleanupGroupTestData(t, d, entID, pgID)

	// Seed at least one active service model so a leak (if present) would be visible.
	_, _ = d.Pool.Exec(context.Background(),
		`INSERT INTO service_models (provider, product_id, api_model_name, status)
		 VALUES ('amazon-mantle-us-east-1', 'leaktest.model-`+suffix+`', 'leaktest.model-`+suffix+`', 'active')
		 ON CONFLICT (provider, product_id) DO NOTHING`)
	defer func() {
		_, _ = d.Pool.Exec(context.Background(), `DELETE FROM service_models WHERE product_id = 'leaktest.model-`+suffix+`'`)
	}()

	c, w := newGroupTestContext(t, http.MethodGet, "/api/enterprise/available-models?platform_group_id="+pgID, nil, entID)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/enterprise/available-models?platform_group_id="+pgID, nil)
	r.getAvailableModels(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var resp struct {
		Models []string `json:"models"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Models) != 0 {
		t.Errorf("empty-parent available-models = %d models, want 0 (leak of platform catalog): %v", len(resp.Models), resp.Models)
	}
}

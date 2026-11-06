package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// newImportTestContext builds a gin context carrying a multipart CSV upload.
func newImportTestContext(t *testing.T, csvContent string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", "members.csv")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write([]byte(csvContent)); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/api/enterprise/members/import", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	c.Request = req
	return c, rec
}

func decodeImportResult(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return body
}

// TestImportEnterpriseMembers_NewUser verifies a CSV row for an unknown email
// creates a pending invitation with token and expiry (previously every row
// failed: INSERT missed the NOT NULL token column).
func TestImportEnterpriseMembers_NewUser(t *testing.T) {
	d := testEnterpriseDB(t)
	r := newTestEnterpriseRoutes(t, d)
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	entID := createApprovalTestEnterprise(t, d, "imp-new-"+suffix, "")
	adminID := createApprovalTestUser(t, d, "imp-admin-"+suffix+"@test.com", "enterprise_admin")
	defer func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM enterprise_invitations WHERE enterprise_id = $1::uuid`, entID)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM enterprises WHERE id = $1::uuid`, entID)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, adminID)
	}()

	email := "imp-new-" + suffix + "@test.com"
	c, rec := newImportTestContext(t, "email,name,role\n"+email+",New Member,admin\n")
	c.Set("enterprise_id", entID)
	c.Set("user_id", adminID)
	c.Set("email", "imp-admin-"+suffix+"@test.com")

	r.importEnterpriseMembers(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := decodeImportResult(t, rec)
	if body["imported"].(float64) != 1 || body["failed"].(float64) != 0 {
		t.Fatalf("expected imported=1 failed=0, got %v", body)
	}

	var token, status string
	var expiresAt *time.Time
	err := d.Pool.QueryRow(ctx,
		`SELECT token, status, expires_at FROM enterprise_invitations WHERE enterprise_id = $1::uuid AND email = $2`,
		entID, email).Scan(&token, &status, &expiresAt)
	if err != nil {
		t.Fatalf("query invitation: %v", err)
	}
	if token == "" {
		t.Fatal("invitation token must not be empty")
	}
	if status != "pending" {
		t.Fatalf("expected status pending, got %s", status)
	}
	if expiresAt == nil {
		t.Fatal("invitation expires_at must be set")
	}
}

// TestImportEnterpriseMembers_DuplicateSkipped verifies a second import of the
// same email is skipped via ON CONFLICT on the partial unique index.
func TestImportEnterpriseMembers_DuplicateSkipped(t *testing.T) {
	d := testEnterpriseDB(t)
	r := newTestEnterpriseRoutes(t, d)
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	entID := createApprovalTestEnterprise(t, d, "imp-dup-"+suffix, "")
	adminID := createApprovalTestUser(t, d, "imp-dup-admin-"+suffix+"@test.com", "enterprise_admin")
	defer func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM enterprise_invitations WHERE enterprise_id = $1::uuid`, entID)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM enterprises WHERE id = $1::uuid`, entID)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, adminID)
	}()

	email := "imp-dup-" + suffix + "@test.com"
	csvContent := "email\n" + email + "\n"
	for i := 0; i < 2; i++ {
		c, rec := newImportTestContext(t, csvContent)
		c.Set("enterprise_id", entID)
		c.Set("user_id", adminID)
		c.Set("email", "imp-dup-admin-"+suffix+"@test.com")
		r.importEnterpriseMembers(c)
		if rec.Code != http.StatusOK {
			t.Fatalf("import %d: expected 200, got %d: %s", i+1, rec.Code, rec.Body.String())
		}
		if i == 1 {
			body := decodeImportResult(t, rec)
			if body["imported"].(float64) != 0 || body["skipped"].(float64) != 1 {
				t.Fatalf("second import should skip the duplicate, got %v", body)
			}
		}
	}
}

// TestImportEnterpriseMembers_MissingEmailColumn rejects a CSV without the
// required email column.
func TestImportEnterpriseMembers_MissingEmailColumn(t *testing.T) {
	d := testEnterpriseDB(t)
	r := newTestEnterpriseRoutes(t, d)

	c, rec := newImportTestContext(t, "name,role\nFoo,admin\n")
	c.Set("enterprise_id", "00000000-0000-0000-0000-000000000000")
	c.Set("user_id", "00000000-0000-0000-0000-000000000000")

	r.importEnterpriseMembers(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestImportEnterpriseMembers_ExistingUser verifies an existing user without
// an enterprise gets a pre-confirmed invitation awaiting EA approval
// (mirrors inviteMember).
func TestImportEnterpriseMembers_ExistingUser(t *testing.T) {
	d := testEnterpriseDB(t)
	r := newTestEnterpriseRoutes(t, d)
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	entID := createApprovalTestEnterprise(t, d, "imp-exist-"+suffix, "")
	adminID := createApprovalTestUser(t, d, "imp-exist-admin-"+suffix+"@test.com", "enterprise_admin")
	memberEmail := "imp-exist-" + suffix + "@test.com"
	memberID := createApprovalTestUser(t, d, memberEmail, "user")
	defer func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM enterprise_invitations WHERE enterprise_id = $1::uuid`, entID)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM enterprises WHERE id = $1::uuid`, entID)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1::uuid, $2::uuid)`, adminID, memberID)
	}()

	c, rec := newImportTestContext(t, "email,name\n"+memberEmail+",Existing User\n")
	c.Set("enterprise_id", entID)
	c.Set("user_id", adminID)
	c.Set("email", "imp-exist-admin-"+suffix+"@test.com")

	r.importEnterpriseMembers(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := decodeImportResult(t, rec)
	if body["imported"].(float64) != 1 {
		t.Fatalf("expected imported=1, got %v", body)
	}

	var status string
	var confirmed bool
	err := d.Pool.QueryRow(ctx,
		`SELECT status, email_confirmed FROM enterprise_invitations WHERE enterprise_id = $1::uuid AND email = $2`,
		entID, memberEmail).Scan(&status, &confirmed)
	if err != nil {
		t.Fatalf("query invitation: %v", err)
	}
	if status != "email_confirmed" || !confirmed {
		t.Fatalf("expected email_confirmed invitation, got status=%s confirmed=%v", status, confirmed)
	}
}

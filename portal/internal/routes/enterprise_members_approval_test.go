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

	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/gin-gonic/gin"
)

// newApprovalTestContext builds a gin context with an optional JSON body.
func newApprovalTestContext(method, path string, body interface{}) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	return c, w
}

// createApprovalTestEnterprise inserts a test enterprise and returns its ID.
func createApprovalTestEnterprise(t *testing.T, d *db.DB, suffix, allowedDomains string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	err := d.Pool.QueryRow(ctx,
		`INSERT INTO enterprises (name, contact_email, status, allowed_domains)
		 VALUES ($1, $2, 'active', $3) RETURNING id::text`,
		"approval-ent-"+suffix, "approval-ent-"+suffix+"@test.com", allowedDomains,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert enterprise: %v", err)
	}
	return id
}

// createApprovalTestUser inserts a test user and returns its ID.
func createApprovalTestUser(t *testing.T, d *db.DB, email, role string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	err := d.Pool.QueryRow(ctx,
		`INSERT INTO users (name, email, password_hash, role)
		 VALUES ($1, $2, 'test-hash', $3) RETURNING id::text`,
		"Approval Test", email, role,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert user %s: %v", email, err)
	}
	return id
}

// createApprovalTestInvitation inserts a test invitation and returns its ID.
func createApprovalTestInvitation(t *testing.T, d *db.DB, enterpriseID, email, status, invitedBy string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	err := d.Pool.QueryRow(ctx,
		`INSERT INTO enterprise_invitations (enterprise_id, email, role, token, status, email_confirmed, expires_at, invited_by)
		 VALUES ($1::uuid, $2, 'member', $3, $4, TRUE, NOW() + INTERVAL '7 days', NULLIF($5, '')::uuid) RETURNING id::text`,
		enterpriseID, email, generateRandomCode("ABCDEFGHJKLMNPQRSTUVWXYZ23456789", 32), status, invitedBy,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert invitation: %v", err)
	}
	return id
}

func invitationStatus(t *testing.T, d *db.DB, inviteID string) string {
	t.Helper()
	var status string
	if err := d.Pool.QueryRow(context.Background(),
		`SELECT status FROM enterprise_invitations WHERE id = $1::uuid`, inviteID,
	).Scan(&status); err != nil {
		t.Fatalf("select invitation status: %v", err)
	}
	return status
}

func memberCount(t *testing.T, d *db.DB, enterpriseID, userID string) int {
	t.Helper()
	var n int
	if err := d.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM enterprise_members WHERE enterprise_id = $1::uuid AND user_id = $2::uuid AND removed_at IS NULL`,
		enterpriseID, userID,
	).Scan(&n); err != nil {
		t.Fatalf("count members: %v", err)
	}
	return n
}

// cleanupApprovalTestData removes all rows created by a test case.
func cleanupApprovalTestData(d *db.DB, enterpriseID string, userIDs, inviteIDs []string) {
	ctx := context.Background()
	if enterpriseID != "" {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM enterprise_members WHERE enterprise_id = $1::uuid`, enterpriseID)
	}
	for _, id := range inviteIDs {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM enterprise_invitations WHERE id = $1::uuid`, id)
	}
	if enterpriseID != "" {
		// Invitations created implicitly by handlers (e.g. addEnterpriseMember).
		_, _ = d.Pool.Exec(ctx, `DELETE FROM enterprise_invitations WHERE enterprise_id = $1::uuid`, enterpriseID)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM enterprises WHERE id = $1::uuid`, enterpriseID)
	}
	for _, id := range userIDs {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, id)
	}
}

// TestApproveInvitation_EA_DomainMatched: EA approves an email_confirmed
// invitation whose domain is whitelisted -> fully approved + member created.
func TestApproveInvitation_EA_DomainMatched(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	r := newTestEnterpriseRoutes(t, d)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	entID := createApprovalTestEnterprise(t, d, suffix, "acme.com")
	eaID := createApprovalTestUser(t, d, "ea-"+suffix+"@acme.com", "enterprise_admin")
	memberEmail := "member-" + suffix + "@acme.com"
	memberID := createApprovalTestUser(t, d, memberEmail, "user")
	invID := createApprovalTestInvitation(t, d, entID, memberEmail, "email_confirmed", eaID)
	defer cleanupApprovalTestData(d, entID, []string{eaID, memberID}, []string{invID})

	c, w := newApprovalTestContext(http.MethodPut, "/api/enterprise/invitations/"+invID+"/approve", nil)
	c.Params = gin.Params{{Key: "invId", Value: invID}}
	c.Set("user_id", eaID)
	c.Set("enterprise_id", entID)
	c.Set("email", "ea-"+suffix+"@acme.com")
	r.approveInvitation(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := invitationStatus(t, d, invID); got != "approved" {
		t.Errorf("expected invitation status 'approved', got %q", got)
	}
	if n := memberCount(t, d, entID, memberID); n != 1 {
		t.Errorf("expected 1 enterprise_members row, got %d", n)
	}
}

// TestApproveInvitation_EA_DomainNotMatched: EA approves an email_confirmed
// invitation outside the whitelist -> ea_approved, no member yet.
func TestApproveInvitation_EA_DomainNotMatched(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	r := newTestEnterpriseRoutes(t, d)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	entID := createApprovalTestEnterprise(t, d, suffix, "acme.com")
	eaID := createApprovalTestUser(t, d, "ea-"+suffix+"@acme.com", "enterprise_admin")
	memberEmail := "outsider-" + suffix + "@other.com"
	memberID := createApprovalTestUser(t, d, memberEmail, "user")
	invID := createApprovalTestInvitation(t, d, entID, memberEmail, "email_confirmed", eaID)
	defer cleanupApprovalTestData(d, entID, []string{eaID, memberID}, []string{invID})

	c, w := newApprovalTestContext(http.MethodPut, "/api/enterprise/invitations/"+invID+"/approve", nil)
	c.Params = gin.Params{{Key: "invId", Value: invID}}
	c.Set("user_id", eaID)
	c.Set("enterprise_id", entID)
	c.Set("email", "ea-"+suffix+"@acme.com")
	r.approveInvitation(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["pending_sa_approval"] != true {
		t.Errorf("expected pending_sa_approval=true, got %v", resp["pending_sa_approval"])
	}
	if got := invitationStatus(t, d, invID); got != "ea_approved" {
		t.Errorf("expected invitation status 'ea_approved', got %q", got)
	}
	if n := memberCount(t, d, entID, memberID); n != 0 {
		t.Errorf("expected no enterprise_members row, got %d", n)
	}
}

// TestApproveInvitationAdmin_SA_EaApproved: SA gives final approval to an
// ea_approved invitation -> approved + member written.
func TestApproveInvitationAdmin_SA_EaApproved(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	r := newTestEnterpriseRoutes(t, d)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	entID := createApprovalTestEnterprise(t, d, suffix, "acme.com")
	saID := createApprovalTestUser(t, d, "sa-"+suffix+"@test.com", "super_admin")
	memberEmail := "outsider-" + suffix + "@other.com"
	memberID := createApprovalTestUser(t, d, memberEmail, "user")
	invID := createApprovalTestInvitation(t, d, entID, memberEmail, "ea_approved", "")
	defer cleanupApprovalTestData(d, entID, []string{saID, memberID}, []string{invID})

	c, w := newApprovalTestContext(http.MethodPost, "/api/admin/enterprise/"+entID+"/invitations/"+invID+"/approve", nil)
	c.Params = gin.Params{{Key: "id", Value: entID}, {Key: "invId", Value: invID}}
	c.Set("user_id", saID)
	c.Set("email", "sa-"+suffix+"@test.com")
	r.approveInvitationAdmin(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := invitationStatus(t, d, invID); got != "approved" {
		t.Errorf("expected invitation status 'approved', got %q", got)
	}
	if n := memberCount(t, d, entID, memberID); n != 1 {
		t.Errorf("expected 1 enterprise_members row, got %d", n)
	}
}

// TestApproveInvitationAdmin_SA_EmailConfirmedRejected: SA cannot approve an
// email_confirmed invitation when the enterprise already has an admin — EA
// approval must come first -> 400.
func TestApproveInvitationAdmin_SA_EmailConfirmedRejected(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	r := newTestEnterpriseRoutes(t, d)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	entID := createApprovalTestEnterprise(t, d, suffix, "acme.com")
	saID := createApprovalTestUser(t, d, "sa-"+suffix+"@test.com", "super_admin")
	// Enterprise already has an admin, so EA approval is required first.
	eaID := createApprovalTestUser(t, d, "ea-"+suffix+"@test.com", "enterprise_admin")
	if _, err := d.Pool.Exec(context.Background(),
		`INSERT INTO enterprise_members (enterprise_id, user_id, role, joined_at) VALUES ($1::uuid, $2::uuid, 'admin', NOW())`,
		entID, eaID); err != nil {
		t.Fatalf("insert ea member: %v", err)
	}
	memberEmail := "outsider-" + suffix + "@other.com"
	invID := createApprovalTestInvitation(t, d, entID, memberEmail, "email_confirmed", "")
	defer cleanupApprovalTestData(d, entID, []string{saID, eaID}, []string{invID})

	c, w := newApprovalTestContext(http.MethodPost, "/api/admin/enterprise/"+entID+"/invitations/"+invID+"/approve", nil)
	c.Params = gin.Params{{Key: "id", Value: entID}, {Key: "invId", Value: invID}}
	c.Set("user_id", saID)
	c.Set("email", "sa-"+suffix+"@test.com")
	r.approveInvitationAdmin(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if got := invitationStatus(t, d, invID); got != "email_confirmed" {
		t.Errorf("expected invitation status unchanged 'email_confirmed', got %q", got)
	}
}

// TestApproveInvitationAdmin_SA_NoEABootstrap: when the enterprise has no admin
// yet, SA may approve an email_confirmed invitation directly -> approved +
// member written with enterprise_admin role.
func TestApproveInvitationAdmin_SA_NoEABootstrap(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	r := newTestEnterpriseRoutes(t, d)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	entID := createApprovalTestEnterprise(t, d, suffix, "acme.com")
	saID := createApprovalTestUser(t, d, "sa-"+suffix+"@test.com", "super_admin")
	adminEmail := "first-admin-" + suffix + "@acme.com"
	adminID := createApprovalTestUser(t, d, adminEmail, "user")
	// Invitation for the first admin (role='admin'), email already confirmed.
	var invID string
	if err := d.Pool.QueryRow(context.Background(),
		`INSERT INTO enterprise_invitations (enterprise_id, email, role, token, status, email_confirmed, expires_at)
		 VALUES ($1::uuid, $2, 'admin', $3, 'email_confirmed', TRUE, NOW() + INTERVAL '7 days') RETURNING id::text`,
		entID, adminEmail, generateRandomCode("ABCDEFGHJKLMNPQRSTUVWXYZ23456789", 32),
	).Scan(&invID); err != nil {
		t.Fatalf("insert invitation: %v", err)
	}
	defer cleanupApprovalTestData(d, entID, []string{saID, adminID}, []string{invID})

	c, w := newApprovalTestContext(http.MethodPost, "/api/admin/enterprise/"+entID+"/invitations/"+invID+"/approve", nil)
	c.Params = gin.Params{{Key: "id", Value: entID}, {Key: "invId", Value: invID}}
	c.Set("user_id", saID)
	c.Set("email", "sa-"+suffix+"@test.com")
	r.approveInvitationAdmin(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := invitationStatus(t, d, invID); got != "approved" {
		t.Errorf("expected invitation status 'approved', got %q", got)
	}
	if n := memberCount(t, d, entID, adminID); n != 1 {
		t.Errorf("expected 1 enterprise_members row, got %d", n)
	}
	var memberRole, userRole string
	if err := d.Pool.QueryRow(context.Background(),
		`SELECT role FROM enterprise_members WHERE enterprise_id = $1::uuid AND user_id = $2::uuid AND removed_at IS NULL`,
		entID, adminID).Scan(&memberRole); err != nil {
		t.Fatalf("select member role: %v", err)
	}
	if memberRole != "admin" {
		t.Errorf("member role = %q, want admin", memberRole)
	}
	if err := d.Pool.QueryRow(context.Background(),
		`SELECT role FROM users WHERE id = $1::uuid`, adminID).Scan(&userRole); err != nil {
		t.Fatalf("select user role: %v", err)
	}
	if userRole != "enterprise_admin" {
		t.Errorf("users.role = %q, want enterprise_admin", userRole)
	}
}

// TestAddEnterpriseMember_EA_OutsideDomainExistingUser: EA adds an existing
// user whose domain is outside the whitelist -> ea_approved invitation
// created, no member written.
func TestAddEnterpriseMember_EA_OutsideDomainExistingUser(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	r := newTestEnterpriseRoutes(t, d)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	entID := createApprovalTestEnterprise(t, d, suffix, "acme.com")
	eaEmail := "ea-" + suffix + "@acme.com"
	eaID := createApprovalTestUser(t, d, eaEmail, "enterprise_admin")
	memberEmail := "outsider-" + suffix + "@other.com"
	memberID := createApprovalTestUser(t, d, memberEmail, "user")
	defer cleanupApprovalTestData(d, entID, []string{eaID, memberID}, nil)

	c, w := newApprovalTestContext(http.MethodPost, "/api/enterprise/members", map[string]string{"email": memberEmail})
	c.Set("enterprise_id", entID)
	c.Set("user_id", eaID)
	c.Set("email", eaEmail)
	r.addEnterpriseMember(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["pending_sa_approval"] != true {
		t.Errorf("expected pending_sa_approval=true, got %v", resp["pending_sa_approval"])
	}

	var status string
	err := d.Pool.QueryRow(context.Background(),
		`SELECT status FROM enterprise_invitations WHERE enterprise_id = $1::uuid AND email = $2`,
		entID, memberEmail,
	).Scan(&status)
	if err != nil {
		t.Fatalf("expected an invitation to be created: %v", err)
	}
	if status != "ea_approved" {
		t.Errorf("expected invitation status 'ea_approved', got %q", status)
	}
	if n := memberCount(t, d, entID, memberID); n != 0 {
		t.Errorf("expected no enterprise_members row, got %d", n)
	}
}

// TestApproveInvitation_EA_CrossTenantRejected: an enterprise admin for
// enterprise A tries to approve an invitation that belongs to enterprise B.
// The ownership check must return 403 and leave the invitation untouched.
func TestApproveInvitation_EA_CrossTenantRejected(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	r := newTestEnterpriseRoutes(t, d)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	entA := createApprovalTestEnterprise(t, d, suffix+"a", "acme.com")
	entB := createApprovalTestEnterprise(t, d, suffix+"b", "beta.com")
	eaA := createApprovalTestUser(t, d, "ea-"+suffix+"a@acme.com", "enterprise_admin")
	memberEmail := "member-" + suffix + "@beta.com"
	memberID := createApprovalTestUser(t, d, memberEmail, "user")
	// Invitation is for enterprise B.
	invID := createApprovalTestInvitation(t, d, entB, memberEmail, "email_confirmed", "")
	defer cleanupApprovalTestData(d, entA, []string{eaA}, nil)
	defer cleanupApprovalTestData(d, entB, []string{memberID}, []string{invID})

	c, w := newApprovalTestContext(http.MethodPut, "/api/enterprise/invitations/"+invID+"/approve", nil)
	c.Params = gin.Params{{Key: "invId", Value: invID}}
	c.Set("user_id", eaA)
	c.Set("enterprise_id", entA) // caller belongs to enterprise A
	c.Set("email", "ea-"+suffix+"a@acme.com")
	r.approveInvitation(c)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-tenant approval, got %d: %s", w.Code, w.Body.String())
	}
	if got := invitationStatus(t, d, invID); got != "email_confirmed" {
		t.Errorf("expected invitation status unchanged 'email_confirmed', got %q", got)
	}
}

// TestRejectInvitation_EA_CrossTenantRejected: an enterprise admin for
// enterprise A tries to reject an invitation that belongs to enterprise B.
// The WHERE-clause enterprise filter must silently match zero rows (404)
// and leave the invitation untouched.
func TestRejectInvitation_EA_CrossTenantRejected(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	r := newTestEnterpriseRoutes(t, d)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	entA := createApprovalTestEnterprise(t, d, suffix+"a", "acme.com")
	entB := createApprovalTestEnterprise(t, d, suffix+"b", "beta.com")
	eaA := createApprovalTestUser(t, d, "ea-"+suffix+"a@acme.com", "enterprise_admin")
	memberEmail := "member-" + suffix + "@beta.com"
	memberID := createApprovalTestUser(t, d, memberEmail, "user")
	invID := createApprovalTestInvitation(t, d, entB, memberEmail, "email_confirmed", "")
	defer cleanupApprovalTestData(d, entA, []string{eaA}, nil)
	defer cleanupApprovalTestData(d, entB, []string{memberID}, []string{invID})

	c, w := newApprovalTestContext(http.MethodPut, "/api/enterprise/invitations/"+invID+"/reject", nil)
	c.Params = gin.Params{{Key: "invId", Value: invID}}
	c.Set("user_id", eaA)
	c.Set("enterprise_id", entA) // caller belongs to enterprise A
	c.Set("email", "ea-"+suffix+"a@acme.com")
	r.rejectInvitation(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for cross-tenant rejection, got %d: %s", w.Code, w.Body.String())
	}
	if got := invitationStatus(t, d, invID); got != "email_confirmed" {
		t.Errorf("expected invitation status unchanged 'email_confirmed', got %q", got)
	}
}

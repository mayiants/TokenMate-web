package routes

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/db"
)

// userEnterpriseID returns the users.enterprise_id (as text, "" when NULL) for a user.
func userEnterpriseID(t *testing.T, d *db.DB, userID string) string {
	t.Helper()
	var entID *string
	if err := d.Pool.QueryRow(context.Background(),
		`SELECT enterprise_id::text FROM users WHERE id = $1::uuid`, userID,
	).Scan(&entID); err != nil {
		t.Fatalf("select user enterprise_id: %v", err)
	}
	if entID == nil {
		return ""
	}
	return *entID
}

// reassignUserRoleValue returns the users.role for a user.
func reassignUserRoleValue(t *testing.T, d *db.DB, userID string) string {
	t.Helper()
	var role string
	if err := d.Pool.QueryRow(context.Background(),
		`SELECT role FROM users WHERE id = $1::uuid`, userID,
	).Scan(&role); err != nil {
		t.Fatalf("select user role: %v", err)
	}
	return role
}

// setUserEnterprise attaches a user to an enterprise (users row + active member row).
func setUserEnterprise(t *testing.T, d *db.DB, userID, enterpriseID, role string) {
	t.Helper()
	ctx := context.Background()
	usersRole := "user"
	if role == "admin" {
		usersRole = "enterprise_admin"
	}
	if _, err := d.Pool.Exec(ctx,
		`UPDATE users SET enterprise_id = $1::uuid, role = $2 WHERE id = $3::uuid`,
		enterpriseID, usersRole, userID); err != nil {
		t.Fatalf("attach user to enterprise: %v", err)
	}
	if _, err := d.Pool.Exec(ctx,
		`INSERT INTO enterprise_members (enterprise_id, user_id, role, joined_at)
		 VALUES ($1::uuid, $2::uuid, $3, NOW())
		 ON CONFLICT (enterprise_id, user_id) DO UPDATE SET role = $3, removed_at = NULL, removed_by = NULL`,
		enterpriseID, userID, role); err != nil {
		t.Fatalf("insert member: %v", err)
	}
}

// TestReassignUserEnterprise_MoveToTarget: a user mistakenly in the source
// enterprise is force-moved to the target enterprise by a platform admin.
// This is the core recovery path (a@abc.com stuck in bcd.com -> moved to abc.com).
func TestReassignUserEnterprise_MoveToTarget(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	r := newTestEnterpriseRoutes(t, d)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	sourceID := createApprovalTestEnterprise(t, d, "src-"+suffix, "bcd.com")
	targetID := createApprovalTestEnterprise(t, d, "dst-"+suffix, "abc.com")
	userID := createApprovalTestUser(t, d, "victim-"+suffix+"@abc.com", "user")
	adminID := createApprovalTestUser(t, d, "sa-"+suffix+"@platform.com", "super_admin")
	defer cleanupApprovalTestData(d, sourceID, []string{userID, adminID}, nil)
	defer cleanupApprovalTestData(d, targetID, nil, nil)

	// User is currently (wrongly) in the source enterprise.
	setUserEnterprise(t, d, userID, sourceID, "member")

	c, w := newApprovalTestContext(http.MethodPost, "/api/admin/enterprise/reassign-user", map[string]interface{}{
		"user_id":              userID,
		"target_enterprise_id": targetID,
		"role":                 "member",
		"reason":               "moved to correct company",
	})
	c.Set("user_id", adminID)
	c.Set("email", "sa-"+suffix+"@platform.com")

	r.reassignUserEnterprise(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := userEnterpriseID(t, d, userID); got != targetID {
		t.Fatalf("user enterprise_id = %q, want target %q", got, targetID)
	}
	// Active in target, removed from source.
	if memberCount(t, d, targetID, userID) != 1 {
		t.Fatalf("expected 1 active member row in target enterprise")
	}
	if memberCount(t, d, sourceID, userID) != 0 {
		t.Fatalf("expected 0 active member rows in source enterprise")
	}
}

// TestReassignUserEnterprise_DetachOnly: an empty target detaches the user,
// leaving them standalone (enterprise_id NULL, role reset to 'user').
func TestReassignUserEnterprise_DetachOnly(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	r := newTestEnterpriseRoutes(t, d)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	sourceID := createApprovalTestEnterprise(t, d, "src-"+suffix, "bcd.com")
	userID := createApprovalTestUser(t, d, "victim-"+suffix+"@abc.com", "user")
	adminID := createApprovalTestUser(t, d, "sa-"+suffix+"@platform.com", "super_admin")
	defer cleanupApprovalTestData(d, sourceID, []string{userID, adminID}, nil)

	setUserEnterprise(t, d, userID, sourceID, "admin")

	c, w := newApprovalTestContext(http.MethodPost, "/api/admin/enterprise/reassign-user", map[string]interface{}{
		"user_id":              userID,
		"target_enterprise_id": "",
		"reason":               "detach",
	})
	c.Set("user_id", adminID)
	c.Set("email", "sa-"+suffix+"@platform.com")

	r.reassignUserEnterprise(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := userEnterpriseID(t, d, userID); got != "" {
		t.Fatalf("user enterprise_id = %q, want empty (detached)", got)
	}
	if got := reassignUserRoleValue(t, d, userID); got != "user" {
		t.Fatalf("user role = %q, want 'user' after detach", got)
	}
	if memberCount(t, d, sourceID, userID) != 0 {
		t.Fatalf("expected 0 active member rows in source enterprise after detach")
	}
}

// TestReassignUserEnterprise_SelfBlocked: an admin cannot reassign their own
// enterprise through this endpoint.
func TestReassignUserEnterprise_SelfBlocked(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	r := newTestEnterpriseRoutes(t, d)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	targetID := createApprovalTestEnterprise(t, d, "dst-"+suffix, "abc.com")
	adminID := createApprovalTestUser(t, d, "sa-"+suffix+"@platform.com", "super_admin")
	defer cleanupApprovalTestData(d, targetID, []string{adminID}, nil)

	c, w := newApprovalTestContext(http.MethodPost, "/api/admin/enterprise/reassign-user", map[string]interface{}{
		"user_id":              adminID,
		"target_enterprise_id": targetID,
	})
	c.Set("user_id", adminID)
	c.Set("email", "sa-"+suffix+"@platform.com")

	r.reassignUserEnterprise(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for self-reassign, got %d: %s", w.Code, w.Body.String())
	}
}

// TestReassignUserEnterprise_NoOpWhenAlreadyInTarget: reassigning a user to the
// enterprise they already belong to is rejected (no-op guard).
func TestReassignUserEnterprise_NoOpWhenAlreadyInTarget(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	r := newTestEnterpriseRoutes(t, d)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	entID := createApprovalTestEnterprise(t, d, "ent-"+suffix, "abc.com")
	userID := createApprovalTestUser(t, d, "member-"+suffix+"@abc.com", "user")
	adminID := createApprovalTestUser(t, d, "sa-"+suffix+"@platform.com", "super_admin")
	defer cleanupApprovalTestData(d, entID, []string{userID, adminID}, nil)

	setUserEnterprise(t, d, userID, entID, "member")

	c, w := newApprovalTestContext(http.MethodPost, "/api/admin/enterprise/reassign-user", map[string]interface{}{
		"user_id":              userID,
		"target_enterprise_id": entID,
	})
	c.Set("user_id", adminID)
	c.Set("email", "sa-"+suffix+"@platform.com")

	r.reassignUserEnterprise(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for already-in-target, got %d: %s", w.Code, w.Body.String())
	}
}

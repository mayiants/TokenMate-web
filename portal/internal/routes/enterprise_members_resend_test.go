package routes

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// TestResendMyEnterpriseInvitation: a pending (email-link) invitation whose
// link has expired can be resent — a new token is issued and expires_at is
// pushed back into the future (~14 days).
func TestResendMyEnterpriseInvitation(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	r := newTestEnterpriseRoutes(t, d)
	ctx := context.Background()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	entID := createApprovalTestEnterprise(t, d, suffix, "acme.com")
	eaID := createApprovalTestUser(t, d, "ea-"+suffix+"@acme.com", "enterprise_admin")

	// Pending invite with an already-expired link.
	var invID, oldToken string
	if err := d.Pool.QueryRow(ctx,
		`INSERT INTO enterprise_invitations (enterprise_id, email, role, token, status, expires_at, invited_by)
		 VALUES ($1::uuid, $2, 'member', $3, 'pending', NOW() - INTERVAL '1 day', $4::uuid)
		 RETURNING id::text, token`,
		entID, "newuser-"+suffix+"@acme.com", generateRandomCode("ABCDEFGHJKLMNPQRSTUVWXYZ23456789", 32), eaID,
	).Scan(&invID, &oldToken); err != nil {
		t.Fatalf("insert invitation: %v", err)
	}
	defer cleanupApprovalTestData(d, entID, []string{eaID}, []string{invID})

	c, w := newApprovalTestContext(http.MethodPost, "/api/enterprise/invitations/"+invID+"/resend", nil)
	c.Params = gin.Params{{Key: "invId", Value: invID}}
	c.Set("user_id", eaID)
	c.Set("enterprise_id", entID)
	c.Set("email", "ea-"+suffix+"@acme.com")

	r.resendMyEnterpriseInvitation(c)

	if w.Code != http.StatusOK {
		t.Fatalf("resend returned %d, want 200 (body=%s)", w.Code, w.Body.String())
	}

	var newToken string
	var expiresAt time.Time
	var status string
	if err := d.Pool.QueryRow(ctx,
		`SELECT token, expires_at, status FROM enterprise_invitations WHERE id = $1::uuid`, invID,
	).Scan(&newToken, &expiresAt, &status); err != nil {
		t.Fatalf("reselect invitation: %v", err)
	}
	if newToken == oldToken {
		t.Fatal("resend must issue a NEW token")
	}
	if !expiresAt.After(time.Now().Add(12 * 24 * time.Hour)) {
		t.Fatalf("expires_at should be ~14 days out, got %v", expiresAt)
	}
	if status != "pending" {
		t.Fatalf("status should remain pending, got %s", status)
	}
}

// TestResendMyEnterpriseInvitation_NonPendingRejected: an email_confirmed
// invitation (existing user, in-app approval) is not resendable.
func TestResendMyEnterpriseInvitation_NonPendingRejected(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	r := newTestEnterpriseRoutes(t, d)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	entID := createApprovalTestEnterprise(t, d, suffix, "acme.com")
	eaID := createApprovalTestUser(t, d, "ea2-"+suffix+"@acme.com", "enterprise_admin")
	invID := createApprovalTestInvitation(t, d, entID, "confirmed-"+suffix+"@acme.com", "email_confirmed", eaID)
	defer cleanupApprovalTestData(d, entID, []string{eaID}, []string{invID})

	c, w := newApprovalTestContext(http.MethodPost, "/api/enterprise/invitations/"+invID+"/resend", nil)
	c.Params = gin.Params{{Key: "invId", Value: invID}}
	c.Set("user_id", eaID)
	c.Set("enterprise_id", entID)
	c.Set("email", "ea2-"+suffix+"@acme.com")

	r.resendMyEnterpriseInvitation(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("resend of non-pending invite returned %d, want 404", w.Code)
	}
}

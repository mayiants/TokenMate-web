package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mayiants/ai-portal-go/pkg/crypto"
)

// TestCopyMyEnterpriseInvitationLink: copying a pending invitation's link
// rotates the token and returns a URL whose raw token hashes to the stored
// value — i.e. the link actually passes acceptInvitationPage's hash check
// (the old page copied the stored hash, which never matched after re-hashing).
func TestCopyMyEnterpriseInvitationLink(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	r := newTestEnterpriseRoutes(t, d)
	r.cfg.App.BaseURL = "https://portal.test"
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

	c, w := newApprovalTestContext(http.MethodPost, "/api/enterprise/invitations/"+invID+"/copy-link", nil)
	c.Params = gin.Params{{Key: "invId", Value: invID}}
	c.Set("user_id", eaID)
	c.Set("enterprise_id", entID)
	c.Set("email", "ea-"+suffix+"@acme.com")

	r.copyMyEnterpriseInvitationLink(c)

	if w.Code != http.StatusOK {
		t.Fatalf("copy-link returned %d, want 200 (body=%s)", w.Code, w.Body.String())
	}

	var body struct {
		Success   bool   `json:"success"`
		AcceptURL string `json:"accept_url"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, w.Body.String())
	}
	if !body.Success || !strings.HasPrefix(body.AcceptURL, "https://portal.test/api/enterprise/accept?token=") {
		t.Fatalf("unexpected accept_url: %s", body.AcceptURL)
	}
	rawToken := strings.TrimPrefix(body.AcceptURL, "https://portal.test/api/enterprise/accept?token=")

	// The returned raw token, once hashed, must equal the stored token — this
	// is exactly what acceptInvitationPage does before matching.
	var storedToken string
	var expiresAt time.Time
	if err := d.Pool.QueryRow(ctx,
		`SELECT token, expires_at FROM enterprise_invitations WHERE id = $1::uuid`, invID,
	).Scan(&storedToken, &expiresAt); err != nil {
		t.Fatalf("reselect invitation: %v", err)
	}
	if storedToken != crypto.HashKeySHA256(rawToken) {
		t.Fatal("stored token is not the hash of the returned raw token — copied link would be dead")
	}
	if storedToken == oldToken {
		t.Fatal("copy-link must rotate the token")
	}
	if !expiresAt.After(time.Now().Add(12 * 24 * time.Hour)) {
		t.Fatalf("expires_at should be ~14 days out, got %v", expiresAt)
	}
}

// TestCopyMyEnterpriseInvitationLink_NonPendingRejected: only pending
// invitations have a shareable link; email_confirmed ones 404.
func TestCopyMyEnterpriseInvitationLink_NonPendingRejected(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	r := newTestEnterpriseRoutes(t, d)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	entID := createApprovalTestEnterprise(t, d, suffix, "acme.com")
	eaID := createApprovalTestUser(t, d, "ea2-"+suffix+"@acme.com", "enterprise_admin")
	invID := createApprovalTestInvitation(t, d, entID, "confirmed-"+suffix+"@acme.com", "email_confirmed", eaID)
	defer cleanupApprovalTestData(d, entID, []string{eaID}, []string{invID})

	c, w := newApprovalTestContext(http.MethodPost, "/api/enterprise/invitations/"+invID+"/copy-link", nil)
	c.Params = gin.Params{{Key: "invId", Value: invID}}
	c.Set("user_id", eaID)
	c.Set("enterprise_id", entID)
	c.Set("email", "ea2-"+suffix+"@acme.com")

	r.copyMyEnterpriseInvitationLink(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("copy-link of non-pending invite returned %d, want 404", w.Code)
	}
}

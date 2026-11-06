package routes

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestVerifyEnterprise_SQL verifies the exact UPDATE used by verifyEnterprise:
// it sets verified_at only when currently NULL, and returns no row when already verified.
func TestVerifyEnterprise_SQL(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	ctx := context.Background()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var enterpriseID string
	err := d.Pool.QueryRow(ctx,
		`INSERT INTO enterprises (name, contact_email, status)
		 VALUES ($1, $2, 'active') RETURNING id::text`,
		"verify-ent-"+suffix, "verify-"+suffix+"@test.com").Scan(&enterpriseID)
	if err != nil {
		t.Fatalf("insert enterprise: %v", err)
	}
	defer func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM enterprises WHERE id = $1::uuid", enterpriseID)
	}()

	const updateSQL = `UPDATE enterprises SET verified_at = NOW(), updated_at = NOW()
		 WHERE id = $1::uuid AND deleted_at IS NULL AND verified_at IS NULL
		 RETURNING verified_at`

	// First verify: should succeed and return a timestamp
	var verifiedAt time.Time
	if err := d.Pool.QueryRow(ctx, updateSQL, enterpriseID).Scan(&verifiedAt); err != nil {
		t.Fatalf("first verify should succeed: %v", err)
	}
	if verifiedAt.IsZero() {
		t.Fatal("verified_at should be set")
	}

	// Second verify: already verified → no row
	var again time.Time
	err = d.Pool.QueryRow(ctx, updateSQL, enterpriseID).Scan(&again)
	if err != pgx.ErrNoRows {
		t.Fatalf("second verify should return ErrNoRows (already verified), got: %v", err)
	}

	// verified_at unchanged after the second attempt
	var finalAt time.Time
	if err := d.Pool.QueryRow(ctx, "SELECT verified_at FROM enterprises WHERE id = $1::uuid", enterpriseID).Scan(&finalAt); err != nil {
		t.Fatalf("select verified_at: %v", err)
	}
	if !finalAt.Equal(verifiedAt) {
		t.Errorf("verified_at changed after duplicate verify: %v != %v", finalAt, verifiedAt)
	}
}

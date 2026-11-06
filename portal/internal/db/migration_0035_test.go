package db

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigration0035_UpgradesPre0035ShapeWithoutForcingMFA replays
// migrations/0035 on a database that lacks the MFA objects (inside a rolled
// back transaction) and checks the safety defaults: nobody has MFA enabled,
// no enterprise requires it and mfa_required_roles is empty, so deploying the
// migration cannot lock anyone out. It also proves idempotency.
func TestMigration0035_UpgradesPre0035ShapeWithoutForcingMFA(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/0035_totp_mfa.sql")
	require.NoError(t, err)

	d := testDB(t)
	defer d.Close()
	ctx := context.Background()

	tx, err := d.Pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	for _, stmt := range []string{
		`DROP TABLE IF EXISTS user_recovery_codes`,
		`ALTER TABLE users DROP COLUMN IF EXISTS totp_secret_enc, DROP COLUMN IF EXISTS totp_enabled,
		        DROP COLUMN IF EXISTS totp_enabled_at, DROP COLUMN IF EXISTS totp_last_step`,
		`ALTER TABLE enterprises DROP COLUMN IF EXISTS require_mfa`,
		`DELETE FROM system_settings WHERE key = 'mfa_required_roles'`,
	} {
		_, err := tx.Exec(ctx, stmt)
		require.NoError(t, err, stmt)
	}

	// An existing user and enterprise that pre-date the migration.
	var userID, entID string
	require.NoError(t, tx.QueryRow(ctx,
		`INSERT INTO enterprises (name, contact_email, status) VALUES ('m35-ent', 'm35@test.com', 'active') RETURNING id::text`).Scan(&entID))
	require.NoError(t, tx.QueryRow(ctx,
		`INSERT INTO users (name, email, password_hash, role, enterprise_id) VALUES ('m35', 'm35-user@test.com', 'x', 'enterprise_admin', $1::uuid) RETURNING id::text`,
		entID).Scan(&userID))

	for i := 0; i < 2; i++ { // second pass: idempotent
		_, err = tx.Exec(ctx, string(raw))
		require.NoError(t, err, "pass %d", i+1)
	}

	var enabled bool
	var secret *string
	var enabledAt *string
	var lastStep int64
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT totp_enabled, totp_secret_enc, totp_enabled_at::text, totp_last_step FROM users WHERE id = $1::uuid`, userID,
	).Scan(&enabled, &secret, &enabledAt, &lastStep))
	assert.False(t, enabled, "existing users must not be switched to MFA")
	assert.Nil(t, secret)
	assert.Nil(t, enabledAt)
	assert.Zero(t, lastStep)

	var entRequires bool
	require.NoError(t, tx.QueryRow(ctx, `SELECT require_mfa FROM enterprises WHERE id = $1::uuid`, entID).Scan(&entRequires))
	assert.False(t, entRequires, "existing enterprises must not be forced")

	var roles string
	var managed string
	require.NoError(t, tx.QueryRow(ctx, `SELECT value, managed_by FROM system_settings WHERE key = 'mfa_required_roles'`).Scan(&roles, &managed))
	assert.Equal(t, "", roles, "default must force nobody")

	// user_recovery_codes: one hash per user, cascade on user delete.
	_, err = tx.Exec(ctx, `INSERT INTO user_recovery_codes (user_id, code_hash) VALUES ($1::uuid, 'h1')`, userID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SAVEPOINT dup`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `INSERT INTO user_recovery_codes (user_id, code_hash) VALUES ($1::uuid, 'h1')`, userID)
	assert.Error(t, err, "the same hash twice for one user must be rejected")
	_, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT dup`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, userID)
	require.NoError(t, err)
	var left int
	require.NoError(t, tx.QueryRow(ctx, `SELECT COUNT(*) FROM user_recovery_codes WHERE user_id = $1::uuid`, userID).Scan(&left))
	assert.Zero(t, left)
}

package db

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigration0036_CreatesSSOTablesIdempotentlyWithSafeDefaults replays
// migrations/0036 (inside a rolled back transaction) on a database that lacks
// the SSO tables, twice, and checks that nothing is enabled or enforced by
// default and that the constraints SSO relies on exist.
func TestMigration0036_CreatesSSOTablesIdempotentlyWithSafeDefaults(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/0036_enterprise_sso.sql")
	require.NoError(t, err)

	d := testDB(t)
	defer d.Close()
	ctx := context.Background()

	tx, err := d.Pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	for _, stmt := range []string{`DROP TABLE IF EXISTS user_identities`, `DROP TABLE IF EXISTS enterprise_sso_configs`} {
		_, err := tx.Exec(ctx, stmt)
		require.NoError(t, err, stmt)
	}
	var entID, userID string
	require.NoError(t, tx.QueryRow(ctx,
		`INSERT INTO enterprises (name, contact_email, status) VALUES ('m36-ent', 'm36@test.com', 'active') RETURNING id::text`).Scan(&entID))
	require.NoError(t, tx.QueryRow(ctx,
		`INSERT INTO users (name, email, password_hash, role, enterprise_id) VALUES ('m36', 'm36-user@test.com', 'x', 'user', $1::uuid) RETURNING id::text`,
		entID).Scan(&userID))

	for i := 0; i < 2; i++ { // second pass: idempotent
		_, err = tx.Exec(ctx, string(raw))
		require.NoError(t, err, "pass %d", i+1)
	}

	// A bare row gets safe defaults: disabled, not enforced, member role, JIT on.
	_, err = tx.Exec(ctx, `INSERT INTO enterprise_sso_configs (enterprise_id) VALUES ($1::uuid)`, entID)
	require.NoError(t, err)
	var enabled, enforce, jit bool
	var scopes, role string
	var domains []string
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT enabled, enforce_sso, jit_provisioning, scopes, default_role, allowed_email_domains FROM enterprise_sso_configs WHERE enterprise_id = $1::uuid`,
		entID).Scan(&enabled, &enforce, &jit, &scopes, &role, &domains))
	assert.False(t, enabled)
	assert.False(t, enforce)
	assert.True(t, jit)
	assert.Equal(t, "openid email profile", scopes)
	assert.Equal(t, "member", role)
	assert.Empty(t, domains)

	// (issuer, subject) is globally unique: one IdP subject cannot map to two users.
	_, err = tx.Exec(ctx, `INSERT INTO user_identities (user_id, enterprise_id, issuer, subject) VALUES ($1::uuid, $2::uuid, 'https://idp.example', 's1')`, userID, entID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SAVEPOINT dup`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `INSERT INTO user_identities (user_id, enterprise_id, issuer, subject) VALUES ($1::uuid, $2::uuid, 'https://idp.example', 's1')`, userID, entID)
	assert.Error(t, err, "UNIQUE(issuer, subject)")
	_, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT dup`)
	require.NoError(t, err)

	// Deleting the enterprise's SSO setup with the enterprise cascades.
	_, err = tx.Exec(ctx, `DELETE FROM user_identities WHERE enterprise_id = $1::uuid`, entID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `DELETE FROM enterprise_sso_configs WHERE enterprise_id = $1::uuid`, entID)
	require.NoError(t, err)
}

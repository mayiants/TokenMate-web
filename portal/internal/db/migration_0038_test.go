package db

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigration0038_MovesOnlyOldDefaultsTo90 replays the migration in a
// rolled-back transaction: rows still at '30' become '90', an admin-chosen
// value stays, and a second run changes nothing.
func TestMigration0038_MovesOnlyOldDefaultsTo90(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/0038_retention_defaults_90d.sql")
	require.NoError(t, err)

	d := testDB(t)
	defer d.Close()
	ctx := context.Background()

	tx, err := d.Pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `INSERT INTO system_settings (key, value) VALUES ('retention_default_days','30'), ('log_retention_days','45')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	require.NoError(t, err)

	get := func(key string) string {
		var v string
		require.NoError(t, tx.QueryRow(ctx, `SELECT value FROM system_settings WHERE key = $1`, key).Scan(&v))
		return v
	}

	_, err = tx.Exec(ctx, string(raw))
	require.NoError(t, err)
	assert.Equal(t, "90", get("retention_default_days"))
	assert.Equal(t, "45", get("log_retention_days"), "an admin-chosen value must survive")

	_, err = tx.Exec(ctx, string(raw))
	require.NoError(t, err)
	assert.Equal(t, "90", get("retention_default_days"))
	assert.Equal(t, "45", get("log_retention_days"))
}

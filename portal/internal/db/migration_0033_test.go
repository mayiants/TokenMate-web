package db

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigration0033_UpgradesPre0033Shape replays migrations/0033 against a
// database that still has the pre-0033 shape (new columns/constraints dropped,
// legacy duplicate trace_ids and blank enterprise_audit_log.action present),
// all inside one transaction that is rolled back so the shared test database
// is left untouched. It proves the migration (a) succeeds despite existing
// duplicate trace_ids, (b) backfills action from change_type, (c) installs the
// retention columns/constraints/defaults, and (d) is idempotent.
func TestMigration0033_UpgradesPre0033Shape(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/0033_audit_retention.sql")
	require.NoError(t, err)
	sqlText := string(raw)

	d := testDB(t)
	defer d.Close()
	ctx := context.Background()

	tx, err := d.Pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	// Recreate the pre-0033 shape.
	for _, stmt := range []string{
		`ALTER TABLE enterprises DROP CONSTRAINT IF EXISTS chk_enterprises_retention_mode`,
		`ALTER TABLE enterprises DROP CONSTRAINT IF EXISTS chk_enterprises_retention_days`,
		`ALTER TABLE enterprises
			DROP COLUMN IF EXISTS retention_mode, DROP COLUMN IF EXISTS retention_days,
			DROP COLUMN IF EXISTS retention_updated_at, DROP COLUMN IF EXISTS retention_updated_by,
			DROP COLUMN IF EXISTS retention_last_purge_at, DROP COLUMN IF EXISTS retention_last_purge_by,
			DROP COLUMN IF EXISTS retention_last_purge_status`,
		`ALTER TABLE proxy_request_audits
			DROP COLUMN IF EXISTS user_agent, DROP COLUMN IF EXISTS http_status, DROP COLUMN IF EXISTS enterprise_id`,
		`DROP INDEX IF EXISTS idx_proxy_request_audits_trace_id`,
		`DROP INDEX IF EXISTS idx_proxy_request_audits_enterprise_created`,
		`ALTER TABLE enterprise_audit_log DROP COLUMN IF EXISTS ip_address, DROP COLUMN IF EXISTS user_agent`,
	} {
		_, err := tx.Exec(ctx, stmt)
		require.NoError(t, err, stmt)
	}

	// Legacy rows pre-date the tamper-evidence chain (row_hash IS NULL). The
	// chain triggers (migration 0034) would hash them at INSERT, so disable them
	// while seeding and re-enable before running the migration under test.
	_, err = tx.Exec(ctx, `ALTER TABLE enterprise_audit_log DISABLE TRIGGER USER`)
	require.NoError(t, err)

	// Legacy data: duplicate trace_ids and blank/NULL action rows.
	dupTrace := uuid.NewString()
	for i := 0; i < 2; i++ {
		_, err = tx.Exec(ctx, `INSERT INTO proxy_request_audits (trace_id, model) VALUES ($1::uuid, 'legacy')`, dupTrace)
		require.NoError(t, err)
	}
	var entID string
	require.NoError(t, tx.QueryRow(ctx,
		`INSERT INTO enterprises (name, contact_email) VALUES ($1, $2) RETURNING id::text`,
		"mig0033-"+dupTrace, "mig0033-"+dupTrace+"@test.com").Scan(&entID))
	for _, c := range []struct{ action, ct string }{
		{"", "member_added"}, {"", "role_changed"}, {"kept", "other"}, {"", ""},
	} {
		var act interface{} = c.action
		if c.action == "" {
			act = nil // NULL case
		}
		var ct interface{} = c.ct
		if c.ct == "" {
			ct = nil
		}
		_, err = tx.Exec(ctx,
			`INSERT INTO enterprise_audit_log (enterprise_id, category, change_type, action) VALUES ($1::uuid, 'member', $2, $3)`,
			entID, ct, act)
		require.NoError(t, err)
	}
	// An empty-string (not NULL) action must be backfilled too.
	_, err = tx.Exec(ctx,
		`INSERT INTO enterprise_audit_log (enterprise_id, category, change_type, action) VALUES ($1::uuid, 'member', 'empty_string_action', '')`, entID)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `ALTER TABLE enterprise_audit_log ENABLE TRIGGER USER`)
	require.NoError(t, err)

	// Apply twice: the second run must be a no-op, not an error.
	for i := 0; i < 2; i++ {
		_, err = tx.Exec(ctx, sqlText)
		require.NoError(t, err, "run %d", i+1)
	}

	// (a) columns + non-unique trace index; the legacy duplicates are still there.
	var n int
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM information_schema.columns
		  WHERE table_name = 'proxy_request_audits' AND column_name IN ('user_agent','http_status','enterprise_id')`).Scan(&n))
	assert.Equal(t, 3, n)
	var idxDef string
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT indexdef FROM pg_indexes WHERE indexname = 'idx_proxy_request_audits_trace_id'`).Scan(&idxDef))
	assert.NotContains(t, idxDef, "UNIQUE", "unique index would fail on legacy duplicates")
	require.NoError(t, tx.QueryRow(ctx, `SELECT COUNT(*) FROM proxy_request_audits WHERE trace_id = $1::uuid`, dupTrace).Scan(&n))
	assert.Equal(t, 2, n)

	// (b) action backfill.
	rows, err := tx.Query(ctx, `SELECT COALESCE(change_type,''), COALESCE(action,'') FROM enterprise_audit_log WHERE enterprise_id = $1::uuid`, entID)
	require.NoError(t, err)
	got := map[string]string{}
	for rows.Next() {
		var ct, act string
		require.NoError(t, rows.Scan(&ct, &act))
		got[ct] = act
	}
	rows.Close()
	assert.Equal(t, "member_added", got["member_added"])
	assert.Equal(t, "role_changed", got["role_changed"])
	assert.Equal(t, "empty_string_action", got["empty_string_action"])
	assert.Equal(t, "kept", got["other"], "an existing action must not be overwritten")
	assert.Equal(t, "", got[""], "rows with neither value stay empty")

	// (c) retention columns: defaults for existing rows + CHECK constraints.
	var mode string
	var days int
	require.NoError(t, tx.QueryRow(ctx, `SELECT retention_mode, retention_days FROM enterprises WHERE id = $1::uuid`, entID).Scan(&mode, &days))
	assert.Equal(t, "standard", mode)
	assert.Equal(t, 90, days)

	for _, stmt := range []string{
		`UPDATE enterprises SET retention_mode = 'bogus' WHERE id = $1::uuid`,
		`UPDATE enterprises SET retention_days = 0 WHERE id = $1::uuid`,
		`UPDATE enterprises SET retention_days = 366 WHERE id = $1::uuid`,
	} {
		_, err = tx.Exec(ctx, `SAVEPOINT chk`)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, stmt, entID)
		assert.Error(t, err, stmt)
		_, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT chk`)
		require.NoError(t, err)
	}

	// Platform default settings are seeded without clobbering admin edits.
	var v string
	require.NoError(t, tx.QueryRow(ctx, `SELECT value FROM system_settings WHERE key = 'retention_default_days'`).Scan(&v))
	assert.NotEmpty(t, v)
}

// TestMigration0033_LeavesChainedAuditRowsUntouched: once the audit chain
// (0034) hashes `action`, rewriting a chained row is rejected by the DB. The
// migration must skip those rows instead of aborting the whole upgrade.
func TestMigration0033_LeavesChainedAuditRowsUntouched(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/0033_audit_retention.sql")
	require.NoError(t, err)

	d := testDB(t)
	defer d.Close()
	ctx := context.Background()

	var chained bool
	require.NoError(t, d.Pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.columns
		  WHERE table_name = 'enterprise_audit_log' AND column_name = 'row_hash')`).Scan(&chained))
	if !chained {
		t.Skip("audit chain (0034) is not installed in this database")
	}

	tx, err := d.Pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	var entID string
	require.NoError(t, tx.QueryRow(ctx,
		`INSERT INTO enterprises (name, contact_email) VALUES ($1, $2) RETURNING id::text`,
		"mig0033b-"+uuid.NewString(), "mig0033b-"+uuid.NewString()+"@test.com").Scan(&entID))
	// Inserted with the chain trigger active => chained, action stays blank.
	_, err = tx.Exec(ctx,
		`INSERT INTO enterprise_audit_log (enterprise_id, category, change_type) VALUES ($1::uuid, 'member', 'chained_row')`, entID)
	require.NoError(t, err)

	var rowHash *string
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT row_hash FROM enterprise_audit_log WHERE enterprise_id = $1::uuid`, entID).Scan(&rowHash))
	if rowHash == nil {
		// The chain columns exist but the insert trigger is not installed (e.g.
		// after another suite recreated the table); nothing is chained to protect.
		t.Skip("chain trigger not installed; row was not hashed at insert")
	}

	_, err = tx.Exec(ctx, string(raw))
	require.NoError(t, err, "migration must not fail on chained rows")

	var action *string
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT action FROM enterprise_audit_log WHERE enterprise_id = $1::uuid`, entID).Scan(&action))
	assert.True(t, action == nil || *action == "", "chained row must keep its hashed value, got %q", retDerefStr(action))
}

func retDerefStr(p *string) string {
	if p == nil {
		return "<NULL>"
	}
	return *p
}

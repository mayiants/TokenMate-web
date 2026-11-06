package db

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/testutil"
	"github.com/ai-proxy/ai-portal-go/migrations"
	"github.com/ai-proxy/ai-portal-go/pkg/auditchain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// strictDB connects WITHOUT the test-suite guard bypass, so the session behaves
// exactly like a production session (delete guards active).
func strictDB(t *testing.T) *DB {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), testutil.PostgresDSNStrict())
	require.NoError(t, err)
	require.NoError(t, pool.Ping(context.Background()))
	t.Cleanup(pool.Close)
	return &DB{Pool: pool, logger: zap.NewNop()}
}

// isolated is a rolled-back transaction in which both audit tables start empty,
// so chain assertions are hermetic regardless of what other tests committed.
type isolated struct {
	t  *testing.T
	db *DB
	tx pgx.Tx
}

func newIsolated(t *testing.T) *isolated {
	t.Helper()
	db := strictDB(t)
	ctx := context.Background()
	tx, err := db.Pool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	iso := &isolated{t: t, db: db, tx: tx}
	// Setup uses the documented purge switch; tests that assert the guard flip
	// it back off with guard(false).
	iso.guard(true)
	for _, stmt := range []string{
		`ALTER TABLE audit_logs DISABLE TRIGGER USER`,
		`ALTER TABLE enterprise_audit_log DISABLE TRIGGER USER`,
		`DELETE FROM audit_logs`,
		`DELETE FROM enterprise_audit_log`,
		`DELETE FROM audit_anchors`,
		`ALTER TABLE audit_logs ENABLE TRIGGER USER`,
		`ALTER TABLE enterprise_audit_log ENABLE TRIGGER USER`,
	} {
		iso.exec(stmt)
	}
	iso.guard(false)
	return iso
}

func (i *isolated) guard(purge bool) {
	v := "off"
	if purge {
		v = "on"
	}
	i.exec(`SELECT set_config('app.audit_purge', $1, true)`, v)
}

func (i *isolated) exec(sql string, args ...any) {
	i.t.Helper()
	_, err := i.tx.Exec(context.Background(), sql, args...)
	require.NoError(i.t, err, sql)
}

// expectErr runs a statement that must fail, then rolls back to a savepoint so
// the surrounding transaction stays usable.
func (i *isolated) expectErr(wantSubstr, sql string, args ...any) {
	i.t.Helper()
	ctx := context.Background()
	_, err := i.tx.Exec(ctx, `SAVEPOINT expect_err`)
	require.NoError(i.t, err)
	_, err = i.tx.Exec(ctx, sql, args...)
	require.Error(i.t, err, sql)
	assert.Contains(i.t, err.Error(), wantSubstr, sql)
	_, rbErr := i.tx.Exec(ctx, `ROLLBACK TO SAVEPOINT expect_err`)
	require.NoError(i.t, rbErr)
}

func (i *isolated) insertLog(msg string) int64 {
	i.t.Helper()
	var id int64
	require.NoError(i.t, i.tx.QueryRow(context.Background(),
		`INSERT INTO audit_logs (user_email, category, action, level, message, details, ip_address, old_value)
		 VALUES ('chain@example.test', 'tamper-test', 'act', 'info', $1, '{"b":2,"a":[1,null]}'::jsonb, '10.1.2.3', '{"x":"é☃"}'::jsonb)
		 RETURNING id`, msg).Scan(&id))
	return id
}

func (i *isolated) verify(table string) *VerifyResult {
	i.t.Helper()
	res, err := verifyAuditChain(context.Background(), i.tx, VerifyOptions{Table: table})
	require.NoError(i.t, err)
	return res
}

// disableTriggersAsAttacker simulates a DBA/attacker bypassing the guards.
func (i *isolated) disableTriggersAsAttacker(table string) {
	i.exec(`ALTER TABLE ` + table + ` DISABLE TRIGGER USER`)
}

func TestAuditChain_InsertBuildsChainAndGoRecomputes(t *testing.T) {
	iso := newIsolated(t)
	for n := 0; n < 5; n++ {
		iso.insertLog(fmt.Sprintf("row-%d", n))
	}
	// Multi-row statement: each row must see its predecessor from the same command.
	iso.exec(`INSERT INTO audit_logs (user_email, category, action, level, message)
	          SELECT 'bulk@example.test', 'tamper-test', 'bulk', 'info', 'bulk-' || g FROM generate_series(1, 25) g`)

	res := iso.verify(auditchain.TableAuditLogs)
	assert.True(t, res.OK, res.Reason)
	assert.Equal(t, int64(30), res.Checked)
	assert.Equal(t, int64(30), res.HeadSeq)
	assert.Nil(t, res.PurgedBeforeID, "pristine chain starts at genesis")
	assert.Equal(t, int64(0), res.BackfilledRows)

	// First row links to genesis, subsequent rows link to their predecessor.
	rows, err := iso.tx.Query(context.Background(), `SELECT chain_seq, prev_hash, row_hash, chain_version FROM audit_logs ORDER BY chain_seq`)
	require.NoError(t, err)
	defer rows.Close()
	prev := auditchain.Genesis(auditchain.TableAuditLogs)
	n := int64(0)
	for rows.Next() {
		var seq int64
		var p, h string
		var v int16
		require.NoError(t, rows.Scan(&seq, &p, &h, &v))
		n++
		assert.Equal(t, n, seq, "chain_seq is gap-free")
		assert.Equal(t, prev, p)
		assert.Equal(t, int16(1), v)
		prev = h
	}
	assert.Equal(t, int64(30), n)
}

func TestAuditChain_GoMatchesSQLHashForEveryField(t *testing.T) {
	iso := newIsolated(t)
	id := iso.insertLog("héllo ☃ \"quoted\" ; V3:abc")
	spec := auditchain.AuditLogsSpec
	cols := strings.Join(spec.SelectExprs(), ", ")
	vals := make([]*string, len(spec.Fields))
	dest := []any{}
	for k := range vals {
		dest = append(dest, &vals[k])
	}
	var prev, stored string
	require.NoError(t, iso.tx.QueryRow(context.Background(),
		`SELECT prev_hash, row_hash, `+cols+` FROM audit_logs WHERE id = $1`, id).Scan(append([]any{&prev, &stored}, dest...)...))
	assert.Equal(t, stored, auditchain.RowHash(auditchain.TableAuditLogs, prev, vals))
	// NULL columns must be encoded distinctly from empty strings.
	nulls := 0
	for _, v := range vals {
		if v == nil {
			nulls++
		}
	}
	assert.Greater(t, nulls, 0, "fixture should exercise the NULL marker")
}

func TestAuditChain_UpdateRules(t *testing.T) {
	iso := newIsolated(t)
	id := iso.insertLog("original")
	iso.insertLog("second")

	// Hashed columns, prev_hash and row_hash are immutable.
	iso.expectErr("tamper-evident", `UPDATE audit_logs SET message = 'edited' WHERE id = $1`, id)
	iso.expectErr("tamper-evident", `UPDATE audit_logs SET action = 'edited' WHERE id = $1`, id)
	iso.expectErr("tamper-evident", `UPDATE audit_logs SET created_at = created_at - INTERVAL '1 day' WHERE id = $1`, id)
	iso.expectErr("tamper-evident", `UPDATE audit_logs SET details = '{"z":1}' WHERE id = $1`, id)
	iso.expectErr("tamper-evident", `UPDATE audit_logs SET row_hash = repeat('0', 64) WHERE id = $1`, id)
	iso.expectErr("tamper-evident", `UPDATE audit_logs SET prev_hash = repeat('0', 64) WHERE id = $1`, id)
	iso.expectErr("tamper-evident", `UPDATE audit_logs SET chain_seq = 99 WHERE id = $1`, id)

	// GDPR anonymisation columns stay writable.
	iso.exec(`UPDATE audit_logs SET user_email = 'anonymized', ip_address = NULL, user_agent = 'x' WHERE id = $1`, id)

	// Soft delete needs a reason (DB-level), restore clears it.
	iso.expectErr("delete_reason", `UPDATE audit_logs SET deleted_at = NOW() WHERE id = $1`, id)
	iso.expectErr("delete_reason", `UPDATE audit_logs SET deleted_at = NOW(), delete_reason = '   ' WHERE id = $1`, id)
	iso.exec(`UPDATE audit_logs SET deleted_at = NOW(), delete_reason = 'duplicate entry' WHERE id = $1`, id)
	iso.exec(`UPDATE audit_logs SET deleted_at = NULL, deleted_by = NULL WHERE id = $1`, id)

	res := iso.verify(auditchain.TableAuditLogs)
	assert.True(t, res.OK, res.Reason)
	assert.Equal(t, int64(0), res.SoftDeletedCount)

	iso.exec(`UPDATE audit_logs SET deleted_at = NOW(), delete_reason = 'dup' WHERE id = $1`, id)
	res = iso.verify(auditchain.TableAuditLogs)
	assert.True(t, res.OK, "soft-deleted rows remain part of the chain")
	assert.Equal(t, int64(1), res.SoftDeletedCount)
}

func TestAuditChain_DeleteTruncateGuards(t *testing.T) {
	iso := newIsolated(t)
	id := iso.insertLog("a")
	iso.insertLog("b")

	iso.expectErr("append-only", `DELETE FROM audit_logs WHERE id = $1`, id)
	iso.expectErr("append-only", `TRUNCATE audit_logs`)

	iso.guard(true)
	iso.exec(`DELETE FROM audit_logs WHERE id = $1`, id)
	iso.guard(false)
	iso.expectErr("append-only", `DELETE FROM audit_logs`)
	iso.guard(true)
	iso.exec(`TRUNCATE audit_logs`)
}

func TestAuditChain_AnchorTableIsInsertOnly(t *testing.T) {
	iso := newIsolated(t)
	iso.exec(`INSERT INTO audit_anchors (anchor_date, table_name, hmac) VALUES ('2026-01-01', 'audit_logs', 'x')`)
	iso.expectErr("insert-only", `UPDATE audit_anchors SET hmac = 'y'`)
	iso.expectErr("append-only", `DELETE FROM audit_anchors`)
	iso.expectErr("append-only", `TRUNCATE audit_anchors`)
	iso.guard(true)
	iso.exec(`DELETE FROM audit_anchors`)
}

func TestAuditChain_EnterpriseAuditLogChainAndGuards(t *testing.T) {
	iso := newIsolated(t)
	ctx := context.Background()
	var entID string
	require.NoError(t, iso.tx.QueryRow(ctx,
		`INSERT INTO enterprises (name, contact_email, status) VALUES ($1, 'ent@example.test', 'active') RETURNING id::text`,
		"chain-ent-"+uuid.NewString()).Scan(&entID))
	var rowID string
	for n := 0; n < 4; n++ {
		require.NoError(t, iso.tx.QueryRow(ctx,
			`INSERT INTO enterprise_audit_log (enterprise_id, category, change_type, action, amount, delta, old_value, new_value, reason, description)
			 VALUES ($1::uuid, 'billing', 'topup', 'topup', $2, $2, 0, $2, 'r', NULL) RETURNING id::text`, entID, int64(100+n)).Scan(&rowID))
	}
	res := iso.verify(auditchain.TableEnterpriseAuditLog)
	assert.True(t, res.OK, res.Reason)
	assert.Equal(t, int64(4), res.Checked)

	iso.expectErr("tamper-evident", `UPDATE enterprise_audit_log SET amount = 1 WHERE id = $1::uuid`, rowID)
	iso.expectErr("tamper-evident", `UPDATE enterprise_audit_log SET reason = 'x' WHERE id = $1::uuid`, rowID)
	iso.expectErr("append-only", `DELETE FROM enterprise_audit_log WHERE id = $1::uuid`, rowID)
	iso.expectErr("append-only", `TRUNCATE enterprise_audit_log`)

	// Tamper with the guards disabled: detected, and the broken row is named.
	iso.disableTriggersAsAttacker("enterprise_audit_log")
	iso.exec(`UPDATE enterprise_audit_log SET amount = 999999 WHERE id = $1::uuid`, rowID)
	res = iso.verify(auditchain.TableEnterpriseAuditLog)
	assert.False(t, res.OK)
	assert.Equal(t, rowID, res.FirstBrokenID)
	assert.Contains(t, res.Reason, "modified")
}

func TestAuditChain_TamperDetection(t *testing.T) {
	t.Run("edited row", func(t *testing.T) {
		iso := newIsolated(t)
		iso.insertLog("1")
		mid := iso.insertLog("2")
		iso.insertLog("3")
		iso.disableTriggersAsAttacker("audit_logs")
		iso.exec(`UPDATE audit_logs SET message = 'forged' WHERE id = $1`, mid)
		res := iso.verify(auditchain.TableAuditLogs)
		assert.False(t, res.OK)
		assert.EqualValues(t, strconv64(mid), fmt.Sprint(res.FirstBrokenID))
		assert.Contains(t, res.Reason, "modified")
		assert.Equal(t, int64(1), res.Checked, "only the rows before the break count as verified")
	})
	t.Run("deleted row", func(t *testing.T) {
		iso := newIsolated(t)
		iso.insertLog("1")
		gone := iso.insertLog("2")
		after := iso.insertLog("3")
		iso.disableTriggersAsAttacker("audit_logs")
		iso.exec(`DELETE FROM audit_logs WHERE id = $1`, gone)
		res := iso.verify(auditchain.TableAuditLogs)
		assert.False(t, res.OK)
		assert.Equal(t, strconv64(after), fmt.Sprint(res.FirstBrokenID))
		assert.Contains(t, res.Reason, "gap")
	})
	t.Run("row inserted with trigger disabled", func(t *testing.T) {
		iso := newIsolated(t)
		iso.insertLog("1")
		iso.disableTriggersAsAttacker("audit_logs")
		iso.exec(`INSERT INTO audit_logs (user_email, category, action, level, message) VALUES ('a', 'c', 'a', 'info', 'sneaky')`)
		res := iso.verify(auditchain.TableAuditLogs)
		assert.False(t, res.OK)
		assert.Equal(t, int64(1), res.UnchainedRows)
		assert.Contains(t, res.Reason, "no chain hash")
	})
	t.Run("row forged with recomputed hash breaks the next link", func(t *testing.T) {
		iso := newIsolated(t)
		iso.insertLog("1")
		mid := iso.insertLog("2")
		next := iso.insertLog("3")
		iso.disableTriggersAsAttacker("audit_logs")
		iso.rehashAfterEdit(mid, "forged-and-rehashed")
		res := iso.verify(auditchain.TableAuditLogs)
		assert.False(t, res.OK)
		assert.Equal(t, strconv64(next), fmt.Sprint(res.FirstBrokenID))
		assert.Contains(t, res.Reason, "prev_hash")
	})
	t.Run("pristine", func(t *testing.T) {
		iso := newIsolated(t)
		iso.insertLog("1")
		res := iso.verify(auditchain.TableAuditLogs)
		assert.True(t, res.OK)
		assert.Nil(t, res.FirstBrokenID)
	})
}

// rehashAfterEdit plays a clever attacker: edit the message and recompute the
// row's own row_hash in Go so that row looks internally consistent.
func (i *isolated) rehashAfterEdit(id int64, msg string) {
	i.t.Helper()
	i.exec(`UPDATE audit_logs SET message = $2 WHERE id = $1`, id, msg)
	i.recomputeRowHash(id)
}

func (i *isolated) recomputeRowHash(id int64) {
	i.t.Helper()
	spec := auditchain.AuditLogsSpec
	vals := make([]*string, len(spec.Fields))
	dest := []any{}
	var prev string
	dest = append(dest, &prev)
	for k := range vals {
		dest = append(dest, &vals[k])
	}
	require.NoError(i.t, i.tx.QueryRow(context.Background(),
		`SELECT prev_hash, `+strings.Join(spec.SelectExprs(), ", ")+` FROM audit_logs WHERE id = $1`, id).Scan(dest...))
	i.exec(`UPDATE audit_logs SET row_hash = $2 WHERE id = $1`, id, auditchain.RowHash(spec.Table, prev, vals))
}

func strconv64(v int64) string { return fmt.Sprintf("%d", v) }

func TestAuditChain_PurgedPrefixIsTolerated(t *testing.T) {
	iso := newIsolated(t)
	iso.insertLog("1")
	iso.insertLog("2")
	third := iso.insertLog("3")
	iso.insertLog("4")

	iso.guard(true)
	iso.exec(`DELETE FROM audit_logs WHERE chain_seq < 3`)
	res := iso.verify(auditchain.TableAuditLogs)
	assert.True(t, res.OK, res.Reason)
	assert.Equal(t, int64(2), res.Checked)
	assert.Equal(t, strconv64(third), fmt.Sprint(res.PurgedBeforeID))
}

func TestAuditChain_RangeVerification(t *testing.T) {
	iso := newIsolated(t)
	for n := 0; n < 3; n++ {
		iso.insertLog(fmt.Sprintf("old-%d", n))
	}
	iso.exec(`SELECT pg_sleep(0.05)`)
	var cut time.Time
	require.NoError(t, iso.tx.QueryRow(context.Background(), `SELECT clock_timestamp()::timestamp`).Scan(&cut))
	// created_at defaults to transaction start in a tx, so force distinct stamps.
	for n := 0; n < 2; n++ {
		iso.exec(`INSERT INTO audit_logs (category, action, level, message, created_at)
		          VALUES ('tamper-test', 'a', 'info', 'new', $1)`, cut.Add(time.Duration(n+1)*time.Second))
	}
	res, err := verifyAuditChain(context.Background(), iso.tx, VerifyOptions{Table: auditchain.TableAuditLogs, From: &cut})
	require.NoError(t, err)
	assert.True(t, res.OK, res.Reason)
	assert.Equal(t, int64(2), res.Checked)

	// A forged row inside the window is found; one outside it is not looked at.
	iso.disableTriggersAsAttacker("audit_logs")
	iso.exec(`UPDATE audit_logs SET message = 'forged' WHERE message = 'old-1'`)
	res, err = verifyAuditChain(context.Background(), iso.tx, VerifyOptions{Table: auditchain.TableAuditLogs, From: &cut})
	require.NoError(t, err)
	assert.True(t, res.OK, "tamper outside the requested window is out of scope")
	res, err = verifyAuditChain(context.Background(), iso.tx, VerifyOptions{Table: auditchain.TableAuditLogs})
	require.NoError(t, err)
	assert.False(t, res.OK)
}

func TestAuditChain_UnsupportedTable(t *testing.T) {
	_, err := strictDB(t).VerifyAuditChain(context.Background(), VerifyOptions{Table: "users"})
	require.Error(t, err)
}

// TestAuditChain_Backfill proves that rows that pre-date the chain are chained
// by the migration with chain_version 0 and then verify cleanly.
func TestAuditChain_Backfill(t *testing.T) {
	iso := newIsolated(t)
	ctx := context.Background()
	iso.exec(`ALTER TABLE audit_logs DISABLE TRIGGER USER`)
	for n := 0; n < 3; n++ {
		iso.exec(`INSERT INTO audit_logs (user_email, category, action, level, message) VALUES ('legacy', 'c', 'a', 'info', $1)`, fmt.Sprintf("legacy-%d", n))
	}
	iso.exec(`ALTER TABLE audit_logs ENABLE TRIGGER USER`)
	res := iso.verify(auditchain.TableAuditLogs)
	assert.False(t, res.OK, "legacy rows are unchained until the backfill runs")
	assert.Equal(t, int64(3), res.UnchainedRows)

	// Re-running the (idempotent) migration performs the backfill.
	_, err := iso.tx.Exec(ctx, auditTamperMigrationSQL(t))
	require.NoError(t, err)
	res = iso.verify(auditchain.TableAuditLogs)
	assert.True(t, res.OK, res.Reason)
	assert.Equal(t, int64(3), res.BackfilledRows)
	assert.Equal(t, int64(0), res.UnchainedRows)

	// New rows continue the chain with chain_version 1.
	iso.insertLog("post-migration")
	res = iso.verify(auditchain.TableAuditLogs)
	assert.True(t, res.OK, res.Reason)
	assert.Equal(t, int64(4), res.Checked)
	assert.Equal(t, int64(3), res.BackfilledRows)

	// And the backfilled rows are protected like any other.
	iso.expectErr("tamper-evident", `UPDATE audit_logs SET message = 'x' WHERE message = 'legacy-0'`)
}

func TestMigration0034_IsIdempotentAndMatchesEmbed(t *testing.T) {
	iso := newIsolated(t)
	iso.insertLog("x")
	for k := 0; k < 2; k++ {
		_, err := iso.tx.Exec(context.Background(), auditTamperMigrationSQL(t))
		require.NoError(t, err, "migration re-run %d", k)
	}
	assert.True(t, iso.verify(auditchain.TableAuditLogs).OK)
}

// ---------------------------------------------------------------------------
// Concurrency (committed rows; verified inside a created_at window)
// ---------------------------------------------------------------------------

func TestAuditChain_ConcurrentInsertsStayGapFree(t *testing.T) {
	db := strictDB(t)
	ctx := context.Background()
	var start time.Time
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT clock_timestamp()::timestamp - INTERVAL '1 second'`).Scan(&start))
	marker := "conc-" + uuid.NewString()

	const workers, perWorker = 20, 50
	var wg sync.WaitGroup
	var failures atomic.Int64
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			if w%2 == 0 {
				for n := 0; n < perWorker; n++ {
					if _, err := db.Pool.Exec(ctx,
						`INSERT INTO audit_logs (category, action, level, message) VALUES ($1, 'insert', 'info', $2)`,
						marker, fmt.Sprintf("w%d-%d", w, n)); err != nil {
						failures.Add(1)
					}
				}
				return
			}
			// Odd workers use COPY, like pkg/audit's batch flush.
			for batch := 0; batch < 5; batch++ {
				rows := make([][]any, 10)
				for r := range rows {
					rows[r] = []any{marker, "copy", "info", fmt.Sprintf("w%d-%d-%d", w, batch, r)}
				}
				if _, err := db.Pool.CopyFrom(ctx, pgx.Identifier{"audit_logs"},
					[]string{"category", "action", "level", "message"}, pgx.CopyFromRows(rows)); err != nil {
					failures.Add(1)
				}
			}
		}(w)
	}
	wg.Wait()
	require.Zero(t, failures.Load())

	var count, minSeq, maxSeq int64
	require.NoError(t, db.Pool.QueryRow(ctx,
		`SELECT COUNT(*), MIN(chain_seq), MAX(chain_seq) FROM audit_logs WHERE category = $1`, marker).Scan(&count, &minSeq, &maxSeq))
	assert.Equal(t, int64(workers*perWorker), count)
	assert.Equal(t, count, maxSeq-minSeq+1, "chain_seq must be gap-free under concurrency")

	res, err := db.VerifyAuditChain(ctx, VerifyOptions{Table: auditchain.TableAuditLogs, From: &start})
	require.NoError(t, err)
	assert.True(t, res.OK, res.Reason)
	assert.GreaterOrEqual(t, res.Checked, count)
}

// ---------------------------------------------------------------------------
// Retention + GDPR keep working with the triggers installed
// ---------------------------------------------------------------------------

func TestAuditChain_RetentionRulePurgesOnlyAPrefix(t *testing.T) {
	iso := newIsolated(t)
	var rule *retentionRule
	for k := range defaultRetentionRules {
		if defaultRetentionRules[k].name == "audit_logs" {
			rule = &defaultRetentionRules[k]
		}
	}
	require.NotNil(t, rule)
	sql := strings.Join(strings.Fields(fmt.Sprintf(rule.sql, 30)), " ")

	old := func(msg string) {
		iso.exec(`INSERT INTO audit_logs (category, action, level, message, created_at) VALUES ('c', 'a', 'info', $1, NOW() - INTERVAL '90 days')`, msg)
	}
	fresh := func(msg string) {
		iso.exec(`INSERT INTO audit_logs (category, action, level, message) VALUES ('c', 'a', 'info', $1)`, msg)
	}
	old("old-1")
	old("old-2")
	fresh("fresh-1")
	old("old-out-of-order") // older than the cutoff but chained AFTER a retained row
	fresh("fresh-2")

	iso.guard(true)
	tag, err := iso.tx.Exec(context.Background(), sql)
	require.NoError(t, err)
	assert.EqualValues(t, 2, tag.RowsAffected(), "only the old prefix is purged; the out-of-order old row must stay")
	iso.guard(false)

	res := iso.verify(auditchain.TableAuditLogs)
	assert.True(t, res.OK, res.Reason)
	assert.Equal(t, int64(3), res.Checked)
	assert.NotNil(t, res.PurgedBeforeID)
}

func TestAuditChain_RetentionKeepsHeadWhenEverythingIsOld(t *testing.T) {
	iso := newIsolated(t)
	for n := 0; n < 3; n++ {
		iso.exec(`INSERT INTO audit_logs (category, action, level, message, created_at) VALUES ('c', 'a', 'info', 'o', NOW() - INTERVAL '90 days')`)
	}
	sql := strings.Join(strings.Fields(fmt.Sprintf(defaultRetentionRules[0].sql, 30)), " ")
	require.Equal(t, "audit_logs", defaultRetentionRules[0].name)
	iso.guard(true)
	tag, err := iso.tx.Exec(context.Background(), sql)
	require.NoError(t, err)
	assert.EqualValues(t, 2, tag.RowsAffected(), "the newest row survives so anchors keep a head to point at")
}

// TestRunRetentionCleanup_AuditLogsThroughGuard exercises the real job on a
// STRICT session: without SET LOCAL app.audit_purge the DELETE would be rejected.
func TestRunRetentionCleanup_AuditLogsThroughGuard(t *testing.T) {
	db := strictDB(t)
	ctx := context.Background()
	t.Setenv("PORTAL_RETENTION_DAYS", "")
	// Hermetic table state (committed): empty both tables via the purge switch.
	require.NoError(t, db.WithAuditPurge(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `TRUNCATE audit_logs, audit_anchors`)
		return err
	}))
	for _, msg := range []string{"ancient-1", "ancient-2"} {
		_, err := db.Pool.Exec(ctx, `INSERT INTO audit_logs (category, action, level, message, created_at)
		  VALUES ('ret-test', 'a', 'info', $1, NOW() - INTERVAL '4000 days')`, msg)
		require.NoError(t, err)
	}
	for _, msg := range []string{"recent-1", "recent-2"} {
		_, err := db.Pool.Exec(ctx, `INSERT INTO audit_logs (category, action, level, message) VALUES ('ret-test', 'a', 'info', $1)`, msg)
		require.NoError(t, err)
	}

	require.NoError(t, db.RunRetentionCleanup(ctx, zap.NewNop()))

	var left int
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM audit_logs WHERE category = 'ret-test'`).Scan(&left))
	assert.Equal(t, 2, left, "ancient rows purged, recent kept")
	res, err := db.VerifyAuditChain(ctx, VerifyOptions{Table: auditchain.TableAuditLogs})
	require.NoError(t, err)
	assert.True(t, res.OK, res.Reason)
	assert.NotNil(t, res.PurgedBeforeID)
}

func TestAnonymizeUser_WorksWithChainTriggers(t *testing.T) {
	db := strictDB(t)
	ctx := context.Background()
	userID := uuid.NewString()
	email := "gdpr-" + userID + "@example.test"
	_, err := db.Pool.Exec(ctx, `INSERT INTO users (id, name, email, password_hash) VALUES ($1::uuid, 'GDPR Test', $2, 'x')`, userID, email)
	require.NoError(t, err)
	t.Cleanup(func() {
		b := testDB(t)
		defer b.Close()
		_, _ = b.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, userID)
	})
	var start time.Time
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT clock_timestamp()::timestamp - INTERVAL '1 second'`).Scan(&start))
	var logID int64
	require.NoError(t, db.Pool.QueryRow(ctx,
		`INSERT INTO audit_logs (user_id, user_email, category, action, level, message, ip_address, user_agent)
		 VALUES ($1::uuid, $2, 'auth', 'login', 'info', 'login ok', '203.0.113.9', 'UA/1.0') RETURNING id`, userID, email).Scan(&logID))

	require.NoError(t, db.AnonymizeUser(ctx, userID))

	var gotEmail string
	var ip *string
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT user_email, ip_address::text FROM audit_logs WHERE id = $1`, logID).Scan(&gotEmail, &ip))
	assert.Equal(t, "anonymized", gotEmail)
	assert.Nil(t, ip)
	res, err := db.VerifyAuditChain(ctx, VerifyOptions{Table: auditchain.TableAuditLogs, From: &start})
	require.NoError(t, err)
	assert.True(t, res.OK, "anonymisation must not invalidate the chain: %s", res.Reason)
}

// ---------------------------------------------------------------------------
// Anchors
// ---------------------------------------------------------------------------

const testAnchorKey = "unit-test-encryption-key-0123456789abcdef"

func resetAuditStateCommitted(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, db.WithAuditPurge(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `TRUNCATE audit_logs, enterprise_audit_log, audit_anchors`)
		return err
	}))
}

func TestAuditAnchors_CreateVerifyAndTamper(t *testing.T) {
	db := strictDB(t)
	ctx := context.Background()
	resetAuditStateCommitted(t, db)
	for n := 0; n < 3; n++ {
		_, err := db.Pool.Exec(ctx, `INSERT INTO audit_logs (category, action, level, message) VALUES ('anchor-test', 'a', 'info', $1)`, fmt.Sprintf("m%d", n))
		require.NoError(t, err)
	}

	now := time.Now().UTC()
	r1, err := db.CreateAuditAnchors(ctx, testAnchorKey, now)
	require.NoError(t, err)
	assert.Contains(t, r1.Created, now.Format("2006-01-02")+"/audit_logs")
	r2, err := db.CreateAuditAnchors(ctx, testAnchorKey, now)
	require.NoError(t, err)
	assert.Empty(t, r2.Created, "anchors are insert-only: a re-run creates nothing")

	anchors, err := loadAnchors(ctx, db.Pool, auditchain.TableAuditLogs)
	require.NoError(t, err)
	require.Len(t, anchors, 1)
	key := auditchain.DeriveAnchorKey(testAnchorKey)
	assert.True(t, anchors[0].VerifyHMAC(key))
	assert.False(t, anchors[0].VerifyHMAC(auditchain.DeriveAnchorKey("another-key-another-key-another-key")))
	assert.EqualValues(t, 3, *anchors[0].LastSeq)

	// More rows after the anchor: still verifies, and the anchor was matched.
	_, err = db.Pool.Exec(ctx, `INSERT INTO audit_logs (category, action, level, message) VALUES ('anchor-test', 'a', 'info', 'later')`)
	require.NoError(t, err)
	res, err := db.VerifyAuditChain(ctx, VerifyOptions{Table: auditchain.TableAuditLogs, AnchorKey: testAnchorKey})
	require.NoError(t, err)
	assert.True(t, res.OK, res.Reason)
	assert.Equal(t, 1, res.AnchorsChecked)
	assert.Empty(t, res.AnchorMismatches)

	// Without the key the HMAC cannot be checked: reported as a warning.
	res, err = db.VerifyAuditChain(ctx, VerifyOptions{Table: auditchain.TableAuditLogs})
	require.NoError(t, err)
	assert.NotEmpty(t, res.Warnings)

	// Wrong key => every anchor HMAC mismatches.
	res, err = db.VerifyAuditChain(ctx, VerifyOptions{Table: auditchain.TableAuditLogs, AnchorKey: "wrong-wrong-wrong-wrong-wrong-wrong-"})
	require.NoError(t, err)
	assert.False(t, res.OK)
	require.Len(t, res.AnchorMismatches, 1)
	assert.Contains(t, res.AnchorMismatches[0].Reason, "HMAC")

	// Anchor rows cannot be edited or deleted through the application role.
	_, err = db.Pool.Exec(ctx, `UPDATE audit_anchors SET head_hash = 'x'`)
	require.Error(t, err)
	_, err = db.Pool.Exec(ctx, `DELETE FROM audit_anchors`)
	require.Error(t, err)

	// Attacker (guards disabled, inside a rolled-back tx) edits the anchor.
	t.Run("edited anchor", func(t *testing.T) {
		tx, err := db.Pool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		_, err = tx.Exec(ctx, `ALTER TABLE audit_anchors DISABLE TRIGGER USER`)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `UPDATE audit_anchors SET head_hash = repeat('a', 64)`)
		require.NoError(t, err)
		res, err := verifyAuditChain(ctx, tx, VerifyOptions{Table: auditchain.TableAuditLogs, AnchorKey: testAnchorKey})
		require.NoError(t, err)
		assert.False(t, res.OK)
		require.Len(t, res.AnchorMismatches, 1)
		assert.Contains(t, res.AnchorMismatches[0].Reason, "HMAC")
	})

	// Attacker rewrites the anchored head row AND recomputes its hash: the
	// chain is internally consistent, only the anchor exposes the rewrite.
	t.Run("rewritten history under an anchor", func(t *testing.T) {
		tx, err := db.Pool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		iso := &isolated{t: t, db: db, tx: tx}
		iso.disableTriggersAsAttacker("audit_logs")
		var id int64
		require.NoError(t, tx.QueryRow(ctx, `SELECT id FROM audit_logs WHERE chain_seq = 3`).Scan(&id))
		iso.rehashAfterEdit(id, "history rewritten")
		// Also re-link the successor so the rest of the chain stays consistent.
		var nextID int64
		require.NoError(t, tx.QueryRow(ctx, `SELECT id FROM audit_logs WHERE chain_seq = 4`).Scan(&nextID))
		var newHash string
		require.NoError(t, tx.QueryRow(ctx, `SELECT row_hash FROM audit_logs WHERE id = $1`, id).Scan(&newHash))
		_, err = tx.Exec(ctx, `UPDATE audit_logs SET prev_hash = $2 WHERE id = $1`, nextID, newHash)
		require.NoError(t, err)
		iso.recomputeRowHash(nextID)
		res, err := verifyAuditChain(ctx, tx, VerifyOptions{Table: auditchain.TableAuditLogs, AnchorKey: testAnchorKey})
		require.NoError(t, err)
		assert.False(t, res.OK)
		assert.Nil(t, res.FirstBrokenID, "the chain itself looks fine")
		require.Len(t, res.AnchorMismatches, 1)
		assert.Contains(t, res.AnchorMismatches[0].Reason, "rewritten")
	})

	// Truncating the tail below an anchored seq is caught too.
	t.Run("truncated tail", func(t *testing.T) {
		tx, err := db.Pool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		_, err = tx.Exec(ctx, `ALTER TABLE audit_logs DISABLE TRIGGER USER`)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `DELETE FROM audit_logs WHERE chain_seq >= 3`)
		require.NoError(t, err)
		res, err := verifyAuditChain(ctx, tx, VerifyOptions{Table: auditchain.TableAuditLogs, AnchorKey: testAnchorKey})
		require.NoError(t, err)
		assert.False(t, res.OK)
		require.NotEmpty(t, res.AnchorMismatches)
		assert.Contains(t, res.AnchorMismatches[0].Reason, "truncated")
	})
	resetAuditStateCommitted(t, db)
}

func TestAuditAnchors_RequireEncryptionKey(t *testing.T) {
	_, err := strictDB(t).CreateAuditAnchors(context.Background(), "", time.Now())
	require.Error(t, err)
}

func TestAuditAnchors_UsageDigestAndWebhook(t *testing.T) {
	db := strictDB(t)
	ctx := context.Background()
	resetAuditStateCommitted(t, db)

	var received atomic.Int64
	var gotBody atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		gotBody.Store(string(buf[:n]))
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t.Setenv("AUDIT_ANCHOR_WEBHOOK_URL", srv.URL)

	// Two usage rows two UTC days ago (sealed day).
	day := time.Now().UTC().AddDate(0, 0, -2)
	stamp := time.Date(day.Year(), day.Month(), day.Day(), 10, 0, 0, 0, time.UTC)
	traces := []string{uuid.NewString(), uuid.NewString()}
	bypass := testDB(t)
	t.Cleanup(bypass.Close)
	t.Cleanup(func() {
		_ = bypass.WithLedgerDelete(ctx, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `DELETE FROM usage_records WHERE trace_id = ANY($1::uuid[])`, traces)
			return err
		})
		resetAuditStateCommitted(t, db)
	})
	// Start from an empty day so the sealed figures only cover this test's rows.
	dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	require.NoError(t, bypass.WithLedgerDelete(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM usage_records WHERE created_at >= $1 AND created_at < $2`, dayStart, dayStart.AddDate(0, 0, 1))
		return err
	}))
	for n, tr := range traces {
		_, err := db.Pool.Exec(ctx, `INSERT INTO usage_records (trace_id, model, total_cost, created_at) VALUES ($1::uuid, 'm', $2, $3)`,
			tr, int64(100*(n+1)), stamp.Add(time.Duration(n)*time.Minute))
		require.NoError(t, err)
	}
	_, err := db.Pool.Exec(ctx, `INSERT INTO audit_logs (category, action, level, message) VALUES ('anchor-test', 'a', 'info', 'x')`)
	require.NoError(t, err)

	res, err := db.CreateAuditAnchors(ctx, testAnchorKey, time.Now().UTC())
	require.NoError(t, err)
	assert.Contains(t, res.Created, day.Format("2006-01-02")+"/usage_records")
	assert.GreaterOrEqual(t, received.Load(), int64(2), "every new anchor is published")
	assert.Contains(t, gotBody.Load().(string), "hmac")

	anchors, err := loadAnchors(ctx, db.Pool, auditchain.TableUsageRecords)
	require.NoError(t, err)
	var usage *auditchain.Anchor
	for k := range anchors {
		if anchors[k].AnchorDate == day.Format("2006-01-02") {
			usage = &anchors[k]
		}
	}
	require.NotNil(t, usage)
	assert.EqualValues(t, 2, *usage.UsageRows)
	assert.EqualValues(t, 300, *usage.UsageCostSum)
	assert.True(t, usage.VerifyHMAC(auditchain.DeriveAnchorKey(testAnchorKey)))

	v, err := db.VerifyAuditChain(ctx, VerifyOptions{Table: auditchain.TableAuditLogs, AnchorKey: testAnchorKey, IncludeUsage: true})
	require.NoError(t, err)
	assert.True(t, v.OK)
	assert.Empty(t, v.UsageAnchorMismatches)
	assert.GreaterOrEqual(t, v.UsageAnchorsChecked, 1)

	// Remove a usage row (ledger guard lifted, like an operator's ad-hoc SQL).
	require.NoError(t, bypass.WithLedgerDelete(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM usage_records WHERE trace_id = $1::uuid`, traces[0])
		return err
	}))
	v, err = db.VerifyAuditChain(ctx, VerifyOptions{Table: auditchain.TableAuditLogs, AnchorKey: testAnchorKey, IncludeUsage: true})
	require.NoError(t, err)
	require.NotEmpty(t, v.UsageAnchorMismatches)
	assert.Contains(t, v.UsageAnchorMismatches[0].Reason, "usage day differs")
}

func TestAuditAnchors_WebhookFailureNeverFailsTheJob(t *testing.T) {
	db := strictDB(t)
	ctx := context.Background()
	resetAuditStateCommitted(t, db)
	t.Cleanup(func() { resetAuditStateCommitted(t, db) })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	url := srv.URL
	srv.Close() // connection refused from now on
	t.Setenv("AUDIT_ANCHOR_WEBHOOK_URL", url)
	_, err := db.Pool.Exec(ctx, `INSERT INTO audit_logs (category, action, level, message) VALUES ('anchor-test', 'a', 'info', 'x')`)
	require.NoError(t, err)
	res, err := db.CreateAuditAnchors(ctx, testAnchorKey, time.Now().UTC())
	require.NoError(t, err)
	assert.NotEmpty(t, res.Created)
}

// ---------------------------------------------------------------------------
// Ledger guards + API key tombstones
// ---------------------------------------------------------------------------

func TestLedgerDeleteGuard(t *testing.T) {
	db := strictDB(t)
	ctx := context.Background()
	userID := uuid.NewString()
	_, err := db.Pool.Exec(ctx, `INSERT INTO users (id, name, email, password_hash) VALUES ($1::uuid, 'ledger', $2, 'x')`, userID, "ledger-"+userID+"@example.test")
	require.NoError(t, err)
	bypass := testDB(t)
	t.Cleanup(bypass.Close)
	t.Cleanup(func() {
		_ = bypass.WithLedgerDelete(ctx, func(tx pgx.Tx) error {
			for _, q := range []string{
				`DELETE FROM usage_records WHERE model = 'ledger-guard-test'`,
				`DELETE FROM balance_transactions WHERE user_id = $1::uuid`,
				`DELETE FROM orders WHERE user_id = $1::uuid`,
			} {
				args := []any{}
				if strings.Contains(q, "$1") {
					args = append(args, userID)
				}
				if _, err := tx.Exec(ctx, q, args...); err != nil {
					return err
				}
			}
			return nil
		})
		_, _ = bypass.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, userID)
	})

	// usage_records: one row in a monthly partition, one in the default partition.
	for _, ts := range []time.Time{time.Now().UTC(), time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)} {
		_, err := db.Pool.Exec(ctx, `INSERT INTO usage_records (trace_id, model, created_at) VALUES (gen_random_uuid(), 'ledger-guard-test', $1)`, ts)
		require.NoError(t, err)
	}
	_, err = db.Pool.Exec(ctx, `DELETE FROM usage_records WHERE model = 'ledger-guard-test'`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "financial ledger")

	_, err = db.Pool.Exec(ctx, `INSERT INTO balance_transactions (user_id, transaction_type, amount) VALUES ($1::uuid, 'topup', 5)`, userID)
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, `DELETE FROM balance_transactions WHERE user_id = $1::uuid`, userID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "financial ledger")

	_, err = db.Pool.Exec(ctx, `INSERT INTO orders (user_id, order_id, amount) VALUES ($1::uuid, $2, 5)`, userID, "ord-"+userID[:8])
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, `DELETE FROM orders WHERE user_id = $1::uuid`, userID)
	require.Error(t, err)

	// Updates are not blocked (GDPR nulls usage_records.user_id, orders soft-delete).
	_, err = db.Pool.Exec(ctx, `UPDATE usage_records SET user_id = NULL WHERE model = 'ledger-guard-test'`)
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, `UPDATE orders SET deleted_at = NOW() WHERE user_id = $1::uuid`, userID)
	require.NoError(t, err)

	// The sanctioned path works for all three tables.
	require.NoError(t, db.WithLedgerDelete(ctx, func(tx pgx.Tx) error {
		for _, q := range []string{
			`DELETE FROM usage_records WHERE model = 'ledger-guard-test'`,
			`DELETE FROM balance_transactions WHERE user_id = $1::uuid`,
			`DELETE FROM orders WHERE user_id = $1::uuid`,
		} {
			args := []any{}
			if strings.Contains(q, "$1") {
				args = append(args, userID)
			}
			if _, err := tx.Exec(ctx, q, args...); err != nil {
				return err
			}
		}
		return nil
	}))
	var left int
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM usage_records WHERE model = 'ledger-guard-test'`).Scan(&left))
	assert.Zero(t, left)
}

func TestLedgerGuardIsTransactionLocal(t *testing.T) {
	db := strictDB(t)
	ctx := context.Background()
	require.NoError(t, db.WithLedgerDelete(ctx, func(tx pgx.Tx) error { return nil }))
	// The pooled connection that just ran WithLedgerDelete must be guarded again.
	for k := 0; k < 8; k++ {
		var v string
		require.NoError(t, db.Pool.QueryRow(ctx, `SELECT COALESCE(current_setting('app.allow_ledger_delete', true), '')`).Scan(&v))
		assert.NotEqual(t, "on", v)
	}
}

func TestAPIKeyTombstone(t *testing.T) {
	db := strictDB(t)
	ctx := context.Background()
	userID := uuid.NewString()
	keyID := uuid.NewString()
	_, err := db.Pool.Exec(ctx, `INSERT INTO users (id, name, email, password_hash) VALUES ($1::uuid, 'tomb', $2, 'x')`, userID, "tomb-"+userID+"@example.test")
	require.NoError(t, err)
	bypass := testDB(t)
	t.Cleanup(bypass.Close)
	t.Cleanup(func() {
		_ = bypass.WithAuditPurge(ctx, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `DELETE FROM api_key_tombstones WHERE key_id = $1::uuid`, keyID)
			return err
		})
		_, _ = bypass.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, userID)
	})
	_, err = db.Pool.Exec(ctx,
		`INSERT INTO api_keys (id, user_id, key_hash, key_prefix, name, status) VALUES ($1::uuid, $2::uuid, 'abcdef0123456789fullhash', 'sk-tomb', 'my key', 'active')`,
		keyID, userID)
	require.NoError(t, err)

	tx, err := db.Pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `SELECT set_config('app.deleted_by', 'ops@example.test', true), set_config('app.delete_reason', 'cleanup after incident', true)`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `DELETE FROM api_keys WHERE id = $1::uuid`, keyID)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	var gotUser, name, prefix, hashPrefix, by, reason string
	require.NoError(t, db.Pool.QueryRow(ctx,
		`SELECT user_id::text, name, key_prefix, key_hash_prefix, deleted_by, reason FROM api_key_tombstones WHERE key_id = $1::uuid`, keyID).
		Scan(&gotUser, &name, &prefix, &hashPrefix, &by, &reason))
	assert.Equal(t, userID, gotUser)
	assert.Equal(t, "my key", name)
	assert.Equal(t, "sk-tomb", prefix)
	assert.Equal(t, "abcdef01", hashPrefix, "only the first 8 chars of the hash are kept")
	assert.Equal(t, "ops@example.test", by)
	assert.Equal(t, "cleanup after incident", reason)

	// Tombstones are insert-only.
	_, err = db.Pool.Exec(ctx, `UPDATE api_key_tombstones SET reason = 'x' WHERE key_id = $1::uuid`, keyID)
	require.Error(t, err)
	_, err = db.Pool.Exec(ctx, `DELETE FROM api_key_tombstones WHERE key_id = $1::uuid`, keyID)
	require.Error(t, err)

	// Without GUCs the DB session user is recorded so ad-hoc deletes stay attributable.
	key2 := uuid.NewString()
	_, err = db.Pool.Exec(ctx, `INSERT INTO api_keys (id, user_id, key_hash, key_prefix, name, status) VALUES ($1::uuid, $2::uuid, 'h2', 'sk-t2', 'k2', 'active')`, key2, userID)
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, `DELETE FROM api_keys WHERE id = $1::uuid`, key2)
	require.NoError(t, err)
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT deleted_by FROM api_key_tombstones WHERE key_id = $1::uuid`, key2).Scan(&by))
	assert.NotEmpty(t, by)
	t.Cleanup(func() {
		_ = bypass.WithAuditPurge(ctx, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `DELETE FROM api_key_tombstones WHERE key_id = $1::uuid`, key2)
			return err
		})
	})
}

// auditTamperMigrationSQL returns migration 0034 exactly as db.Migrate would
// load it from disk, after checking it is identical to the embedded copy that
// InitSchema executes (so fresh and migrated databases cannot diverge).
func auditTamperMigrationSQL(t *testing.T) string {
	t.Helper()
	ms, err := loadMigrations("../../migrations")
	require.NoError(t, err)
	for _, m := range ms {
		if m.Version == "0034" {
			require.Equal(t, migrations.AuditTamperEvidenceSQL, m.SQL, "embedded copy must equal migrations/0034 on disk")
			return m.SQL
		}
	}
	t.Fatal("migration 0034 not found on disk")
	return ""
}

// TestLedgerGuard_AppliesToNewPartitionsAndDropIsDDL proves the row trigger
// defined on the partitioned parent is cloned onto partitions created later
// (EnsureUsagePartitions keeps adding months) and that dropping a partition,
// which is DDL, is not affected by the DELETE guard.
func TestLedgerGuard_AppliesToNewPartitionsAndDropIsDDL(t *testing.T) {
	db := strictDB(t)
	ctx := context.Background()
	const part = "usage_records_p209001_guardtest"
	_, _ = db.Pool.Exec(ctx, `DROP TABLE IF EXISTS `+part)
	_, err := db.Pool.Exec(ctx, `CREATE TABLE `+part+` PARTITION OF usage_records FOR VALUES FROM ('2090-01-01') TO ('2090-02-01')`)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = db.Pool.Exec(ctx, `DROP TABLE IF EXISTS `+part) })

	_, err = db.Pool.Exec(ctx, `INSERT INTO usage_records (trace_id, model, created_at) VALUES (gen_random_uuid(), 'part-guard', '2090-01-15')`)
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, `DELETE FROM usage_records WHERE model = 'part-guard'`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "financial ledger")
	// Deleting straight from the partition is guarded too.
	_, err = db.Pool.Exec(ctx, `DELETE FROM `+part)
	require.Error(t, err)

	_, err = db.Pool.Exec(ctx, `DROP TABLE `+part)
	require.NoError(t, err, "retiring a whole partition is DDL and must keep working")
}

// TestMigration0034_BackfillRuntime measures the one-off backfill on a table the
// size of production's audit_logs (~1.1k rows) and on a 20k-row table to show
// how it scales. Both run inside a rolled-back transaction.
func TestMigration0034_BackfillRuntime(t *testing.T) {
	for _, n := range []int{1100, 20000} {
		t.Run(fmt.Sprintf("%d_rows", n), func(t *testing.T) {
			iso := newIsolated(t)
			iso.exec(`ALTER TABLE audit_logs DISABLE TRIGGER USER`)
			iso.exec(`INSERT INTO audit_logs (user_email, category, action, level, message, details, ip_address, created_at)
			          SELECT 'legacy@example.test', 'c', 'a', 'info', 'legacy ' || g, jsonb_build_object('n', g), '10.0.0.1',
			                 NOW() - (g || ' minutes')::interval
			            FROM generate_series(1, $1) g`, n)
			iso.exec(`ALTER TABLE audit_logs ENABLE TRIGGER USER`)

			sql := auditTamperMigrationSQL(t)
			started := time.Now()
			_, err := iso.tx.Exec(context.Background(), sql)
			elapsed := time.Since(started)
			require.NoError(t, err)
			t.Logf("migration 0034 backfill of %d audit_logs rows took %s", n, elapsed)
			assert.Less(t, elapsed, 60*time.Second)

			res := iso.verify(auditchain.TableAuditLogs)
			assert.True(t, res.OK, res.Reason)
			assert.EqualValues(t, n, res.BackfilledRows)

			// Second run is a cheap no-op (this is what every restart costs).
			started = time.Now()
			_, err = iso.tx.Exec(context.Background(), sql)
			require.NoError(t, err)
			t.Logf("idempotent re-run with %d chained rows took %s", n, time.Since(started))
		})
	}
}

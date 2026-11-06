package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Migration 0034 installs triggers that reject DELETE/TRUNCATE on the audit
// tables unless the transaction sets app.audit_purge = 'on', and DELETE on the
// money ledgers unless it sets app.allow_ledger_delete = 'on'. These helpers
// are the ONLY sanctioned way for application code to do that: the setting is
// transaction-local (SET LOCAL semantics via set_config(..., true)), so it can
// never leak to another request on a pooled connection.
const (
	// GUCAuditPurge gates DELETE/TRUNCATE on audit_logs, enterprise_audit_log,
	// audit_anchors and api_key_tombstones.
	GUCAuditPurge = "app.audit_purge"
	// GUCAllowLedgerDelete gates DELETE on usage_records, balance_transactions
	// and orders.
	GUCAllowLedgerDelete = "app.allow_ledger_delete"
)

// auditPurgeTables are the retention rule names whose DELETE must run with the
// purge guard lifted.
var auditPurgeTables = map[string]bool{
	"audit_logs":           true,
	"enterprise_audit_log": true,
}

// withGUC runs fn in a transaction with the named boolean GUC set to 'on' for
// that transaction only.
func (db *DB) withGUC(ctx context.Context, guc string, fn func(tx pgx.Tx) error) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	if _, err := tx.Exec(ctx, `SELECT set_config($1, 'on', true)`, guc); err != nil {
		return fmt.Errorf("set %s: %w", guc, err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// WithAuditPurge runs fn in a transaction in which DELETE/TRUNCATE on the
// append-only audit tables is permitted. Use it for retention/maintenance
// only; every call site should be reviewable by grep.
func (db *DB) WithAuditPurge(ctx context.Context, fn func(tx pgx.Tx) error) error {
	return db.withGUC(ctx, GUCAuditPurge, fn)
}

// WithLedgerDelete runs fn in a transaction in which DELETE on the ledger
// tables (usage_records, balance_transactions, orders) is permitted.
func (db *DB) WithLedgerDelete(ctx context.Context, fn func(tx pgx.Tx) error) error {
	return db.withGUC(ctx, GUCAllowLedgerDelete, fn)
}

// execRetentionSQL executes one retention statement. Rules for the audit
// tables get the purge guard lifted for exactly that statement's transaction;
// everything else is a plain Exec.
func (db *DB) execRetentionSQL(ctx context.Context, ruleName, sql string) (pgconn.CommandTag, error) {
	if !auditPurgeTables[ruleName] {
		return db.Pool.Exec(ctx, sql)
	}
	var tag pgconn.CommandTag
	err := db.WithAuditPurge(ctx, func(tx pgx.Tx) error {
		var execErr error
		tag, execErr = tx.Exec(ctx, sql)
		return execErr
	})
	return tag, err
}

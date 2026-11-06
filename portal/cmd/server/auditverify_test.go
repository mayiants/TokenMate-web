package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/testutil"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const cliTestKey = "cli-test-encryption-key-0123456789abcdef"

func cliTestDB(t *testing.T) *db.DB {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, db.InitTestSchema(ctx, testutil.PostgresDSN(), zap.NewNop()))
	pool, err := pgxpool.New(ctx, testutil.PostgresDSNStrict())
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	d := &db.DB{Pool: pool}
	reset := func() {
		require.NoError(t, d.WithAuditPurge(ctx, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `TRUNCATE audit_logs, enterprise_audit_log, audit_anchors`)
			return err
		}))
	}
	reset()
	t.Cleanup(reset)
	return d
}

func TestVerifyAuditCommand_ExitCodes(t *testing.T) {
	d := cliTestDB(t)
	ctx := context.Background()
	for n := 0; n < 3; n++ {
		_, err := d.Pool.Exec(ctx, `INSERT INTO audit_logs (category, action, level, message) VALUES ('cli-test', 'a', 'info', 'm')`)
		require.NoError(t, err)
	}

	var out, errBuf bytes.Buffer
	assert.Equal(t, 0, verifyAuditCommand(ctx, d, cliTestKey, nil, &out, &errBuf), errBuf.String())
	dec := json.NewDecoder(&out)
	var first map[string]any
	require.NoError(t, dec.Decode(&first))
	assert.Equal(t, true, first["ok"])
	assert.Equal(t, "audit_logs", first["table"])
	var second map[string]any
	require.NoError(t, dec.Decode(&second))
	assert.Equal(t, "enterprise_audit_log", second["table"], "default is all chained tables")

	// Bad usage and bad input.
	out.Reset()
	errBuf.Reset()
	assert.Equal(t, 2, verifyAuditCommand(ctx, d, cliTestKey, []string{"--nope"}, &out, &errBuf))
	assert.Equal(t, 1, verifyAuditCommand(ctx, d, cliTestKey, []string{"--table", "users"}, &out, &errBuf))
	assert.Equal(t, 1, verifyAuditCommand(ctx, d, cliTestKey, []string{"--table", "audit_logs", "--from", "yesterday-ish"}, &out, &errBuf))

	// An attacker with the guards disabled edits a row: exit code must be 1.
	tx, err := d.Pool.Begin(ctx)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `ALTER TABLE audit_logs DISABLE TRIGGER USER`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `UPDATE audit_logs SET message = 'forged' WHERE chain_seq = 2`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `ALTER TABLE audit_logs ENABLE TRIGGER USER`)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	out.Reset()
	errBuf.Reset()
	assert.Equal(t, 1, verifyAuditCommand(ctx, d, cliTestKey, []string{"--table", "audit_logs"}, &out, &errBuf))
	assert.Contains(t, errBuf.String(), "FAILED")
	var res map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &res))
	assert.Equal(t, false, res["ok"])
	assert.NotNil(t, res["first_broken_id"])
}

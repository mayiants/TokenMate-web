package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

const schemaAdvisoryLockID int64 = 4242424242

// acquireSchemaLockAndRun connects to the test database, acquires a PostgreSQL advisory
// lock on a dedicated connection, and runs fn. The lock is held for the entire duration
// of fn so that schema-mutating tests (e.g. destructive full-flow tests) can serialize
// against schema-initialization in other test packages.
func acquireSchemaLockAndRun(ctx context.Context, dsn string, fn func(*pgxpool.Pool) error) error {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect to test db: %w", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping test db: %w", err)
	}

	// Serialize schema operations across concurrent test packages.
	// The lock MUST be held on a single dedicated connection for the entire duration.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection for schema lock: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", schemaAdvisoryLockID); err != nil {
		return fmt.Errorf("acquire schema lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", schemaAdvisoryLockID)
	}()

	return fn(pool)
}

// InitTestSchema connects to the test database and ensures the schema is initialized.
// It uses a PostgreSQL advisory lock on a dedicated connection so that multiple test
// packages can safely call this function concurrently without racing on DDL statements.
func InitTestSchema(ctx context.Context, dsn string, logger *zap.Logger) error {
	return acquireSchemaLockAndRun(ctx, dsn, func(pool *pgxpool.Pool) error {
		database := &DB{Pool: pool}
		if err := database.InitSchema(ctx, logger); err != nil {
			return fmt.Errorf("init schema: %w", err)
		}
		return nil
	})
}

// WithSchemaLock runs fn while holding the shared schema advisory lock. Use this to wrap
// tests that mutate the database schema (e.g. drop/recreate tables) so they do not race
// with schema initialization in other test packages.
func WithSchemaLock(ctx context.Context, dsn string, fn func(*pgxpool.Pool) error) error {
	return acquireSchemaLockAndRun(ctx, dsn, fn)
}

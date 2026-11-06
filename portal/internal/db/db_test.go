package db

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/testutil"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// TestMain initializes the database schema once before running any db package tests.
// It snapshots the test database first so destructive suites can be rolled back,
// and prunes snapshots older than 7 days.
func TestMain(m *testing.M) {
	testutil.LoadEnv() // load .env from repo root so `go test` reads it automatically
	ctx := context.Background()
	dsn := testutil.PostgresDSN()
	if err := testutil.EnsureTestDatabase(dsn); err != nil {
		fmt.Fprintf(os.Stderr, "ensure test database in db TestMain: %v\n", err)
		os.Exit(1)
	}
	if err := InitTestSchema(ctx, dsn, zap.NewNop()); err != nil {
		fmt.Fprintf(os.Stderr, "init schema in db TestMain: %v\n", err)
		os.Exit(1)
	}
	if _, err := testutil.SnapshotDatabase(dsn); err != nil {
		fmt.Fprintf(os.Stderr, "pre-test snapshot in db TestMain: %v\n", err)
		os.Exit(1)
	}
	testutil.CleanupTestSnapshots(7 * 24 * time.Hour)

	os.Exit(m.Run())
}

// testDB returns a DB connected to the running test PostgreSQL instance.
func testDB(t *testing.T) *DB {
	t.Helper()

	pool, err := pgxpool.New(context.Background(), testutil.PostgresDSN())
	if err != nil {
		t.Fatalf("connect to test db: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("ping test db: %v", err)
	}

	logger := zap.NewNop()
	return &DB{Pool: pool, Redis: nil, logger: logger}
}

// testDBWithObservedLogger returns a DB with an observable zap logger.
func testDBWithObservedLogger(t *testing.T) (*DB, *observer.ObservedLogs) {
	t.Helper()

	pool, err := pgxpool.New(context.Background(), testutil.PostgresDSN())
	if err != nil {
		t.Fatalf("connect to test db: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("ping test db: %v", err)
	}

	core, observed := observer.New(zapcore.ErrorLevel)
	logger := zap.New(core)
	return &DB{Pool: pool, Redis: nil, logger: logger}, observed
}

// cleanupTestProvider removes a test provider by id.
func cleanupTestProvider(t *testing.T, db *DB, id string) {
	t.Helper()
	ctx := context.Background()
	_, _ = db.Pool.Exec(ctx, "DELETE FROM providers WHERE id = $1", id)
}

// TestMustExec_InsertSuccess verifies MustExec successfully inserts a row.
func TestMustExec_InsertSuccess(t *testing.T) {
	db := testDB(t)
	defer db.Close()

	ctx := context.Background()
	providerID := fmt.Sprintf("test-provider-%d", time.Now().UnixNano())
	defer cleanupTestProvider(t, db, providerID)

	// Use MustExec to insert
	db.MustExec(ctx,
		"INSERT INTO providers (id, name, type, base_url, sync_source, enabled) VALUES ($1, $2, $3, $4, $5, $6)",
		providerID, "Test Provider", "international", "https://test.example.com/v1", "manual", true,
	)

	// Verify row exists
	var name string
	err := db.Pool.QueryRow(ctx, "SELECT name FROM providers WHERE id = $1", providerID).Scan(&name)
	if err != nil {
		t.Fatalf("expected inserted row to exist, got error: %v", err)
	}
	if name != "Test Provider" {
		t.Errorf("expected name 'Test Provider', got %q", name)
	}
}

// TestMustExec_UpdateSuccess verifies MustExec successfully updates a row.
func TestMustExec_UpdateSuccess(t *testing.T) {
	db := testDB(t)
	defer db.Close()

	ctx := context.Background()
	providerID := fmt.Sprintf("test-provider-%d", time.Now().UnixNano())
	defer cleanupTestProvider(t, db, providerID)

	// Insert test row
	_, err := db.Pool.Exec(ctx,
		"INSERT INTO providers (id, name, type, base_url, sync_source, enabled) VALUES ($1, $2, $3, $4, $5, $6)",
		providerID, "Original Name", "international", "https://test.example.com/v1", "manual", true,
	)
	if err != nil {
		t.Fatalf("setup insert failed: %v", err)
	}

	// Use MustExec to update
	db.MustExec(ctx,
		"UPDATE providers SET name = $1, updated_at = NOW() WHERE id = $2",
		"Updated Name", providerID,
	)

	// Verify update
	var name string
	err = db.Pool.QueryRow(ctx, "SELECT name FROM providers WHERE id = $1", providerID).Scan(&name)
	if err != nil {
		t.Fatalf("expected row to exist after update, got error: %v", err)
	}
	if name != "Updated Name" {
		t.Errorf("expected name 'Updated Name', got %q", name)
	}
}

// TestMustExec_DeleteSuccess verifies MustExec successfully deletes a row.
func TestMustExec_DeleteSuccess(t *testing.T) {
	db := testDB(t)
	defer db.Close()

	ctx := context.Background()
	providerID := fmt.Sprintf("test-provider-%d", time.Now().UnixNano())
	defer cleanupTestProvider(t, db, providerID)

	// Insert test row
	_, err := db.Pool.Exec(ctx,
		"INSERT INTO providers (id, name, type, base_url, sync_source, enabled) VALUES ($1, $2, $3, $4, $5, $6)",
		providerID, "Delete Me", "international", "https://test.example.com/v1", "manual", true,
	)
	if err != nil {
		t.Fatalf("setup insert failed: %v", err)
	}

	// Use MustExec to delete
	db.MustExec(ctx, "DELETE FROM providers WHERE id = $1", providerID)

	// Verify deletion
	var count int
	err = db.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM providers WHERE id = $1", providerID).Scan(&count)
	if err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if count != 0 {
		t.Errorf("expected row to be deleted, count = %d", count)
	}
}

// TestMustExec_LogsErrorOnBadSQL verifies MustExec logs an error when SQL is invalid.
func TestMustExec_LogsErrorOnBadSQL(t *testing.T) {
	db, observed := testDBWithObservedLogger(t)
	defer db.Close()

	ctx := context.Background()

	// Execute invalid SQL via MustExec — should NOT panic
	db.MustExec(ctx, "INSERT INTO nonexistent_table (id) VALUES ($1)", "x")

	// Verify error was logged
	logs := observed.All()
	if len(logs) == 0 {
		t.Fatal("expected error to be logged, got none")
	}
	entry := logs[0]
	if entry.Level != zapcore.ErrorLevel {
		t.Errorf("expected error level, got %v", entry.Level)
	}
	if !strings.Contains(entry.Message, "Database exec failed") {
		t.Errorf("expected log message to contain 'Database exec failed', got %q", entry.Message)
	}
}

// TestExec_ReturnsError verifies the original Exec method still returns errors.
func TestExec_ReturnsError(t *testing.T) {
	db := testDB(t)
	defer db.Close()

	ctx := context.Background()
	err := db.Exec(ctx, "INSERT INTO nonexistent_table (id) VALUES ($1)", "x")
	if err == nil {
		t.Fatal("expected Exec to return error for invalid SQL, got nil")
	}
	if !strings.Contains(err.Error(), "nonexistent_table") {
		t.Errorf("expected error to mention table name, got: %v", err)
	}
}

// TestExec_ReturnsErrorOnConstraintViolation verifies Exec returns errors on constraint violations.
func TestExec_ReturnsErrorOnConstraintViolation(t *testing.T) {
	db := testDB(t)
	defer db.Close()

	ctx := context.Background()
	providerID := fmt.Sprintf("test-provider-%d", time.Now().UnixNano())
	defer cleanupTestProvider(t, db, providerID)

	// Insert first row
	err := db.Exec(ctx,
		"INSERT INTO providers (id, name, type, base_url, sync_source, enabled) VALUES ($1, $2, $3, $4, $5, $6)",
		providerID, "First", "international", "https://test.example.com/v1", "manual", true,
	)
	if err != nil {
		t.Fatalf("first insert failed: %v", err)
	}

	// Insert duplicate primary key — should error
	err = db.Exec(ctx,
		"INSERT INTO providers (id, name, type, base_url, sync_source, enabled) VALUES ($1, $2, $3, $4, $5, $6)",
		providerID, "Second", "international", "https://test.example.com/v1", "manual", true,
	)
	if err == nil {
		t.Fatal("expected Exec to return error for duplicate PK, got nil")
	}
}

// TestMustExecTx_LogsError verifies MustExecTx logs errors within a transaction.
func TestMustExecTx_LogsError(t *testing.T) {
	core, observed := observer.New(zapcore.ErrorLevel)
	logger := zap.New(core)

	db := testDB(t)
	defer db.Close()

	ctx := context.Background()
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Execute invalid SQL via MustExecTx — should NOT panic
	MustExecTx(ctx, logger, tx, "INSERT INTO nonexistent_table (id) VALUES ($1)", "x")

	// Verify error was logged
	logs := observed.All()
	if len(logs) == 0 {
		t.Fatal("expected error to be logged by MustExecTx, got none")
	}
	entry := logs[0]
	if entry.Level != zapcore.ErrorLevel {
		t.Errorf("expected error level, got %v", entry.Level)
	}
	if !strings.Contains(entry.Message, "Tx exec failed") {
		t.Errorf("expected log message to contain 'Tx exec failed', got %q", entry.Message)
	}
}

// TestWithTx_RollbackOnError verifies WithTx rolls back when fn returns an error.
func TestWithTx_RollbackOnError(t *testing.T) {
	db := testDB(t)
	defer db.Close()

	ctx := context.Background()
	providerID := fmt.Sprintf("test-provider-%d", time.Now().UnixNano())
	defer cleanupTestProvider(t, db, providerID)

	// Attempt an insert inside a transaction that deliberately fails afterward
	err := db.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			"INSERT INTO providers (id, name, type, base_url, sync_source, enabled) VALUES ($1, $2, $3, $4, $5, $6)",
			providerID, "Tx Test", "international", "https://tx.example.com/v1", "manual", true,
		)
		if err != nil {
			return err
		}
		return fmt.Errorf("deliberate failure")
	})
	if err == nil {
		t.Fatal("expected WithTx to return the deliberate error")
	}

	// Verify row was NOT committed
	var count int
	err = db.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM providers WHERE id = $1", providerID).Scan(&count)
	if err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if count != 0 {
		t.Errorf("expected transaction to be rolled back, count = %d", count)
	}
}

// TestWithTx_CommitOnSuccess verifies WithTx commits when fn succeeds.
func TestWithTx_CommitOnSuccess(t *testing.T) {
	db := testDB(t)
	defer db.Close()

	ctx := context.Background()
	providerID := fmt.Sprintf("test-provider-%d", time.Now().UnixNano())
	defer cleanupTestProvider(t, db, providerID)

	err := db.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			"INSERT INTO providers (id, name, type, base_url, sync_source, enabled) VALUES ($1, $2, $3, $4, $5, $6)",
			providerID, "Tx Commit Test", "international", "https://tx.example.com/v1", "manual", true,
		)
		return err
	})
	if err != nil {
		t.Fatalf("expected WithTx to succeed: %v", err)
	}

	// Verify row was committed
	var count int
	err = db.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM providers WHERE id = $1", providerID).Scan(&count)
	if err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected transaction to be committed, count = %d", count)
	}
}

// TestMustExec_MultipleCalls verifies multiple MustExec calls in sequence work correctly.
func TestMustExec_MultipleCalls(t *testing.T) {
	db := testDB(t)
	defer db.Close()

	ctx := context.Background()
	baseID := fmt.Sprintf("test-multi-%d", time.Now().UnixNano())
	id1 := baseID + "-1"
	id2 := baseID + "-2"
	defer func() {
		cleanupTestProvider(t, db, id1)
		cleanupTestProvider(t, db, id2)
	}()

	// Insert two rows in sequence
	db.MustExec(ctx,
		"INSERT INTO providers (id, name, type, base_url, sync_source, enabled) VALUES ($1, $2, $3, $4, $5, $6)",
		id1, "Multi 1", "international", "https://multi.example.com/v1", "manual", true,
	)
	db.MustExec(ctx,
		"INSERT INTO providers (id, name, type, base_url, sync_source, enabled) VALUES ($1, $2, $3, $4, $5, $6)",
		id2, "Multi 2", "international", "https://multi.example.com/v2", "manual", true,
	)

	// Verify both exist
	var count int
	err := db.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM providers WHERE id IN ($1, $2)", id1, id2).Scan(&count)
	if err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 rows, got %d", count)
	}
}

// TestMustExec_DoesNotPanicOnNilLogger verifies MustExec works even with nil logger.
func TestMustExec_DoesNotPanicOnNilLogger(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), testutil.PostgresDSN())
	if err != nil {
		t.Fatalf("connect to test db: %v", err)
	}
	defer pool.Close()

	// Create DB with nil logger
	db := &DB{Pool: pool, Redis: nil, logger: nil}

	ctx := context.Background()
	providerID := fmt.Sprintf("test-nil-logger-%d", time.Now().UnixNano())
	defer cleanupTestProvider(t, db, providerID)

	// This should NOT panic even with nil logger
	db.MustExec(ctx,
		"INSERT INTO providers (id, name, type, base_url, sync_source, enabled) VALUES ($1, $2, $3, $4, $5, $6)",
		providerID, "Nil Logger Test", "international", "https://nil.example.com/v1", "manual", true,
	)

	// Verify
	var count int
	err = db.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM providers WHERE id = $1", providerID).Scan(&count)
	if err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row, got %d", count)
	}
}

// TestMustExec_LogsErrorOnConstraintViolation verifies MustExec logs constraint violation errors.
func TestMustExec_LogsErrorOnConstraintViolation(t *testing.T) {
	db, observed := testDBWithObservedLogger(t)
	defer db.Close()

	ctx := context.Background()
	providerID := fmt.Sprintf("test-constraint-%d", time.Now().UnixNano())
	defer cleanupTestProvider(t, db, providerID)

	// Insert first row via normal exec
	_, err := db.Pool.Exec(ctx,
		"INSERT INTO providers (id, name, type, base_url, sync_source, enabled) VALUES ($1, $2, $3, $4, $5, $6)",
		providerID, "First", "international", "https://test.example.com/v1", "manual", true,
	)
	if err != nil {
		t.Fatalf("setup insert failed: %v", err)
	}

	observed.TakeAll() // clear previous logs

	// Insert duplicate via MustExec — should log error but NOT panic
	db.MustExec(ctx,
		"INSERT INTO providers (id, name, type, base_url, sync_source, enabled) VALUES ($1, $2, $3, $4, $5, $6)",
		providerID, "Second", "international", "https://test.example.com/v1", "manual", true,
	)

	// Verify error was logged
	logs := observed.All()
	if len(logs) == 0 {
		t.Fatal("expected constraint violation to be logged, got none")
	}
	entry := logs[0]
	if entry.Level != zapcore.ErrorLevel {
		t.Errorf("expected error level, got %v", entry.Level)
	}
}

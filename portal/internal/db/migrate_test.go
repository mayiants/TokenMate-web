package db

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestLoadMigrations_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	migrations, err := loadMigrations(dir)
	require.NoError(t, err)
	assert.Empty(t, migrations)
}

func TestLoadMigrations_ParsesFiles(t *testing.T) {
	dir := t.TempDir()
	writeMigrationFile(t, dir, "001_create_users.sql", "CREATE TABLE users (id SERIAL PRIMARY KEY);")
	writeMigrationFile(t, dir, "002_add_email.sql", "ALTER TABLE users ADD COLUMN email TEXT;")

	migrations, err := loadMigrations(dir)
	require.NoError(t, err)
	assert.Len(t, migrations, 2)

	assert.Equal(t, "001", migrations[0].Version)
	assert.Equal(t, "create_users", migrations[0].Name)
	assert.Contains(t, migrations[0].SQL, "CREATE TABLE users")

	assert.Equal(t, "002", migrations[1].Version)
	assert.Equal(t, "add_email", migrations[1].Name)
	assert.Contains(t, migrations[1].SQL, "ALTER TABLE users")
}

func TestLoadMigrations_SortedByVersion(t *testing.T) {
	dir := t.TempDir()
	writeMigrationFile(t, dir, "010_third.sql", "SELECT 3;")
	writeMigrationFile(t, dir, "001_first.sql", "SELECT 1;")
	writeMigrationFile(t, dir, "005_second.sql", "SELECT 2;")

	migrations, err := loadMigrations(dir)
	require.NoError(t, err)
	assert.Len(t, migrations, 3)

	assert.Equal(t, "001", migrations[0].Version)
	assert.Equal(t, "005", migrations[1].Version)
	assert.Equal(t, "010", migrations[2].Version)
}

func TestLoadMigrations_VersionCollision(t *testing.T) {
	dir := t.TempDir()
	writeMigrationFile(t, dir, "001_create_users.sql", "SELECT 1;")
	writeMigrationFile(t, dir, "001_add_email.sql", "SELECT 2;")

	_, err := loadMigrations(dir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "version collision")
	assert.Contains(t, err.Error(), "001")
}

func TestLoadMigrations_SkipsNonSQLFiles(t *testing.T) {
	dir := t.TempDir()
	writeMigrationFile(t, dir, "001_create_users.sql", "SELECT 1;")
	writeMigrationFile(t, dir, "README.md", "readme content")
	writeMigrationFile(t, dir, "002_add_email.sql", "SELECT 2;")

	migrations, err := loadMigrations(dir)
	require.NoError(t, err)
	assert.Len(t, migrations, 2)
}

func TestLoadMigrations_SkipsDirectories(t *testing.T) {
	dir := t.TempDir()
	writeMigrationFile(t, dir, "001_create_users.sql", "SELECT 1;")
	require.NoError(t, os.Mkdir(filepath.Join(dir, "subdir"), 0755))

	migrations, err := loadMigrations(dir)
	require.NoError(t, err)
	assert.Len(t, migrations, 1)
}

func TestLoadMigrations_NonexistentDir(t *testing.T) {
	_, err := loadMigrations("/nonexistent/directory/path")
	assert.Error(t, err)
}

func TestLoadMigrations_ParsesVersionCorrectly(t *testing.T) {
	dir := t.TempDir()
	// Version is everything before first underscore
	writeMigrationFile(t, dir, "001_initial_schema.sql", "SELECT 1;")

	migrations, err := loadMigrations(dir)
	require.NoError(t, err)
	assert.Len(t, migrations, 1)
	assert.Equal(t, "001", migrations[0].Version)
	assert.Equal(t, "initial_schema", migrations[0].Name)
}

func TestMigrationStruct(t *testing.T) {
	m := Migration{
		Version: "001",
		Name:    "create_users",
		SQL:     "CREATE TABLE users (id INT);",
	}
	assert.Equal(t, "001", m.Version)
	assert.Equal(t, "create_users", m.Name)
	assert.Equal(t, "CREATE TABLE users (id INT);", m.SQL)
}

// === Integration tests (require test DB) ===

func TestMigrate_CreatesTableAndApplies(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := testDB(t)
	defer db.Close()

	ctx := context.Background()
	logger := zap.NewNop()

	// Create a temp dir with a test migration
	dir := t.TempDir()
	writeMigrationFile(t, dir, "001_test_migration.sql",
		`CREATE TABLE IF NOT EXISTS _test_migrate_1 (id SERIAL PRIMARY KEY, name TEXT);`)

	err := db.Migrate(ctx, logger, dir)
	require.NoError(t, err)

	// Verify migration was tracked
	var count int
	err = db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version = '001'`,
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "migration should be recorded")

	// Clean up
	defer func() { _, _ = db.Pool.Exec(ctx, "DROP TABLE IF EXISTS _test_migrate_1 CASCADE") }()
	defer func() { _, _ = db.Pool.Exec(ctx, "DELETE FROM schema_migrations WHERE version = '001'") }()
}

func TestMigrate_AlreadyApplied_Skips(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := testDB(t)
	defer db.Close()

	ctx := context.Background()
	logger := zap.NewNop()

	// Create two migrations
	dir := t.TempDir()
	writeMigrationFile(t, dir, "001_test_a.sql",
		`CREATE TABLE IF NOT EXISTS _test_migrate_a (id SERIAL);`)
	writeMigrationFile(t, dir, "002_test_b.sql",
		`CREATE TABLE IF NOT EXISTS _test_migrate_b (id SERIAL);`)

	// Apply first migration
	err := db.Migrate(ctx, logger, dir)
	require.NoError(t, err)

	// Apply again — 001 should be skipped, 002 already applied, should report none
	err = db.Migrate(ctx, logger, dir)
	require.NoError(t, err)

	// Both should be recorded
	var count int
	err = db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version IN ('001', '002')`,
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	// Clean up
	defer func() { _, _ = db.Pool.Exec(ctx, "DROP TABLE IF EXISTS _test_migrate_a CASCADE") }()
	defer func() { _, _ = db.Pool.Exec(ctx, "DROP TABLE IF EXISTS _test_migrate_b CASCADE") }()
	defer func() { _, _ = db.Pool.Exec(ctx, "DELETE FROM schema_migrations WHERE version IN ('001', '002')") }()
}

func TestMigrate_RollbackOnSQLFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := testDB(t)
	defer db.Close()

	ctx := context.Background()
	logger := zap.NewNop()

	dir := t.TempDir()
	writeMigrationFile(t, dir, "001_bad_migration.sql", `INVALID SQL STATEMENT THAT FAILS;`)

	err := db.Migrate(ctx, logger, dir)
	assert.Error(t, err)

	// Migration should NOT be tracked
	var count int
	err = db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version = '001'`,
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "failed migration should not be recorded")
}

func TestMigrateSQL_NewMigration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := testDB(t)
	defer db.Close()

	ctx := context.Background()

	err := db.MigrateSQL(ctx, "099",
		`CREATE TABLE IF NOT EXISTS _test_migrate_sql (id SERIAL PRIMARY KEY);`)
	require.NoError(t, err)

	// Verify tracked
	var count int
	err = db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version = '099'`,
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	defer func() { _, _ = db.Pool.Exec(ctx, "DROP TABLE IF EXISTS _test_migrate_sql CASCADE") }()
	defer func() { _, _ = db.Pool.Exec(ctx, "DELETE FROM schema_migrations WHERE version = '099'") }()
}

func TestMigrateSQL_AlreadyApplied_Skips(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := testDB(t)
	defer db.Close()

	ctx := context.Background()

	// Apply once
	err := db.MigrateSQL(ctx, "098", `SELECT 1;`)
	require.NoError(t, err)
	defer func() { _, _ = db.Pool.Exec(ctx, "DELETE FROM schema_migrations WHERE version = '098'") }()

	// Apply again — should skip silently
	err = db.MigrateSQL(ctx, "098", `SELECT 2;`)
	require.NoError(t, err)

	// Only one entry should exist
	var count int
	err = db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version = '098'`,
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

// Helper: write a migration file in a temp directory
func writeMigrationFile(t *testing.T, dir, filename, content string) {
	t.Helper()
	err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0644)
	require.NoError(t, err)
}

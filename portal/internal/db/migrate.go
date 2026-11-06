package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// Migration represents a single database migration
type Migration struct {
	Version string
	Name    string
	SQL     string
}

// Migrate runs all pending migrations.
// If migrationsDir does not exist or contains no .sql files, Migrate is a
// no-op (it still ensures schema_migrations exists). This keeps the call safe
// to enable before any migration files are shipped.
func (db *DB) Migrate(ctx context.Context, logger *zap.Logger, migrationsDir string) error {
	// Acquire a session-level advisory lock so concurrent instances don't race
	// on DDL + schema_migrations INSERT on first deploy.
	const migrationLockKey int64 = 7277950
	if _, err := db.Pool.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockKey); err != nil {
		return fmt.Errorf("acquire migration advisory lock: %w", err)
	}
	defer func() {
		_, _ = db.Pool.Exec(ctx, "SELECT pg_advisory_unlock($1)", migrationLockKey)
	}()

	// Create migrations tracking table
	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version VARCHAR(50) PRIMARY KEY,
			applied_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}

	// No migrations directory yet: nothing to do.
	if info, err := os.Stat(migrationsDir); err != nil || !info.IsDir() {
		logger.Info("No migrations directory present, skipping", zap.String("dir", migrationsDir))
		return nil
	}

	// Load migrations from directory
	migrations, err := loadMigrations(migrationsDir)
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}
	if len(migrations) == 0 {
		logger.Info("No migrations to apply", zap.String("dir", migrationsDir))
		return nil
	}

	// Get applied migrations
	applied := make(map[string]bool)
	rows, err := db.Pool.Query(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return fmt.Errorf("query applied migrations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err == nil {
			applied[v] = true
		}
	}

	// Apply pending migrations in order
	for _, m := range migrations {
		if applied[m.Version] {
			logger.Info("Migration already applied", zap.String("version", m.Version))
			continue
		}

		logger.Info("Applying migration", zap.String("version", m.Version), zap.String("name", m.Name))

		tx, err := db.Pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin transaction for %s: %w", m.Version, err)
		}

		if _, err := tx.Exec(ctx, m.SQL); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply migration %s: %w", m.Version, err)
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (version) VALUES ($1)`,
			m.Version); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record migration %s: %w", m.Version, err)
		}

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %s: %w", m.Version, err)
		}

		logger.Info("Migration applied successfully", zap.String("version", m.Version))
	}

	return nil
}

func loadMigrations(dir string) ([]Migration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var migrations []Migration
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		content, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", entry.Name(), err)
		}

		// Parse version from filename: 001_name.sql -> version=001
		name := strings.TrimSuffix(entry.Name(), ".sql")
		parts := strings.SplitN(name, "_", 2)
		version := parts[0]
		displayName := name
		if len(parts) > 1 {
			displayName = parts[1]
		}

		migrations = append(migrations, Migration{
			Version: version,
			Name:    displayName,
			SQL:     string(content),
		})
	}

	// Sort by version
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})

	// Detect version collisions — same version for different files is a silent skip bug
	seen := make(map[string]string)
	for _, m := range migrations {
		if prevFile, ok := seen[m.Version]; ok {
			return nil, fmt.Errorf("migration version collision: version %q used by both %q and %q — rename one to have a unique prefix", m.Version, prevFile, m.Name)
		}
		seen[m.Version] = m.Name
	}

	return migrations, nil
}

// MigrateSQL runs a single SQL migration directly (for embedded migrations)
func (db *DB) MigrateSQL(ctx context.Context, version, sql string) error {
	// Check if already applied
	var count int
	err := db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version = $1`, version,
	).Scan(&count)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, sql); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// QueryRowContext is a convenience wrapper for QueryRow with context
func (db *DB) QueryRowContext(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	return db.Pool.QueryRow(ctx, sql, args...)
}

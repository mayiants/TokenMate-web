package testutil

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const testSnapshotPrefix = "snapshot-test-"

// backupDir returns the repository-level backups/ directory (two levels up
// from internal/testutil).
func backupDir() string {
	return filepath.Join("..", "..", "backups")
}

// dbNameFromDSN extracts the database name from a postgres:// DSN.
func dbNameFromDSN(dsn string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parse dsn: %w", err)
	}
	name := strings.TrimPrefix(u.Path, "/")
	if name == "" {
		return "", fmt.Errorf("dsn has no database name")
	}
	return name, nil
}

// EnsureTestDatabase creates the test database if it does not exist yet.
// It connects to the maintenance "postgres" database with the same
// credentials. Safe to call before every test run.
func EnsureTestDatabase(dsn string) error {
	dbname, err := dbNameFromDSN(dsn)
	if err != nil {
		return err
	}
	maintDSN := strings.Replace(dsn, "/"+dbname+"?", "/postgres?", 1)
	pool, err := pgxpool.New(context.Background(), maintDSN)
	if err != nil {
		return fmt.Errorf("connect maintenance db: %w", err)
	}
	defer pool.Close()

	ctx := context.Background()
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`, dbname).Scan(&exists); err != nil {
		return fmt.Errorf("check database existence: %w", err)
	}
	if exists {
		return nil
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(`CREATE DATABASE "%s"`, dbname)); err != nil {
		return fmt.Errorf("create test database %s: %w", dbname, err)
	}
	return nil
}

// SnapshotDatabase dumps the database referenced by dsn (via the local
// postgres docker container) into backups/snapshot-test-<db>-<ts>.dump and
// returns the file path. It fails loudly if the container, pg_dump or the
// resulting file is missing/empty — a silent backup failure must never be
// mistaken for a real backup.
func SnapshotDatabase(dsn string) (string, error) {
	// In CI environments (GitHub Actions, etc.) the postgres service is not a
	// named docker container we can exec into. The CI database is ephemeral and
	// does not need snapshot protection, so skip gracefully.
	if os.Getenv("CI") != "" {
		return "", nil
	}

	dbname, err := dbNameFromDSN(dsn)
	if err != nil {
		return "", err
	}
	container := getEnv("DB_CONTAINER", "ai-portal-postgres")
	user := getEnv("DB_USER", "postgres")

	dir := backupDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create backup dir: %w", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("%s%s-%s.dump", testSnapshotPrefix, dbname, time.Now().UTC().Format("20060102T150405Z")))

	out, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create snapshot file: %w", err)
	}
	defer func() { _ = out.Close() }()

	cmd := exec.Command("docker", "exec", container, "pg_dump", "-U", user, "-Fc", dbname)
	cmd.Stdout = out
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if runErr := cmd.Run(); runErr != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("pg_dump via container %s failed: %v: %s", container, runErr, stderr.String())
	}

	info, err := os.Stat(path)
	if err != nil || info.Size() < 1024 {
		_ = os.Remove(path)
		return "", fmt.Errorf("snapshot file %s missing or suspiciously small", path)
	}
	return path, nil
}

// RestoreDatabase restores a snapshot file into the database referenced by
// dsn, replacing existing objects (--clean --if-exists).
func RestoreDatabase(dsn, snapshotPath string) error {
	if os.Getenv("CI") != "" {
		return nil
	}
	dbname, err := dbNameFromDSN(dsn)
	if err != nil {
		return err
	}
	container := getEnv("DB_CONTAINER", "ai-portal-postgres")
	user := getEnv("DB_USER", "postgres")

	abs, err := filepath.Abs(snapshotPath)
	if err != nil {
		return err
	}
	containerPath := "/tmp/" + filepath.Base(snapshotPath)
	if out, cpErr := exec.Command("docker", "cp", abs, container+":"+containerPath).CombinedOutput(); cpErr != nil {
		return fmt.Errorf("docker cp snapshot: %v: %s", cpErr, out)
	}
	defer func() {
		_ = exec.Command("docker", "exec", container, "rm", "-f", containerPath).Run()
	}()

	out, err := exec.Command("docker", "exec", container, "pg_restore",
		"-U", user, "-d", dbname, "--clean", "--if-exists", "--no-owner", "--no-privileges", containerPath,
	).CombinedOutput()
	if err != nil {
		// pg_restore --clean emits non-fatal errors on partitioned tables
		// (inherited constraints cannot be dropped individually; they go away
		// with the parent table's DROP CASCADE). Treat only those known-benign
		// lines as warnings; any other error line fails the restore.
		var unexpected []string
		for _, line := range strings.Split(string(out), "\n") {
			if !strings.Contains(line, "error") && !strings.Contains(line, "ERROR") {
				continue
			}
			if strings.Contains(line, "inherited constraint") ||
				strings.Contains(line, "does not exist, skipping") ||
				strings.Contains(line, "errors ignored on restore") ||
				strings.Contains(line, "cannot drop") ||
				strings.Contains(line, "already exists") ||
				strings.Contains(line, "multiple primary keys") {
				continue
			}
			unexpected = append(unexpected, line)
		}
		if len(unexpected) > 0 {
			return fmt.Errorf("pg_restore failed: %v: %s", err, strings.Join(unexpected, "\n"))
		}
	}
	return nil
}

// DatabaseRowCounts returns row counts for every public table, for
// before/after integrity comparison around destructive tests.
func DatabaseRowCounts(ctx context.Context, pool *pgxpool.Pool) (map[string]int64, error) {
	rows, err := pool.Query(ctx,
		`SELECT table_name FROM information_schema.tables
		 WHERE table_schema = 'public' AND table_type = 'BASE TABLE' ORDER BY table_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := map[string]int64{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		var n int64
		if err := pool.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM "%s"`, name)).Scan(&n); err != nil {
			// Table may have been dropped concurrently; record as -1.
			n = -1
		}
		counts[name] = n
	}
	return counts, rows.Err()
}

// CleanupTestSnapshots deletes test snapshots older than maxAge so the
// backups directory does not grow without bound.
func CleanupTestSnapshots(maxAge time.Duration) {
	entries, err := os.ReadDir(backupDir())
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), testSnapshotPrefix) {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(backupDir(), e.Name()))
	}
}

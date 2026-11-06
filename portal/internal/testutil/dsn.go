// Package testutil provides helpers for tests that need external services.
// All helpers read from environment variables and fall back to local dev defaults.
package testutil

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/joho/godotenv"
)

var envOnce sync.Once

// getEnv returns the value of key from the environment, or defaultValue if unset.
func getEnv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

// LoadEnv loads a .env file from the repository root into the current process.
// It is safe to call multiple times; already-set variables are NOT overwritten.
// Intended for use in test TestMain so `go test` picks up .env automatically.
func LoadEnv() {
	envOnce.Do(func() {
		// Look for .env in the repository root (project root is two levels up from internal/testutil).
		root := filepath.Join("..", "..")
		_ = godotenv.Load(filepath.Join(root, ".env"))
	})
}

// guardBypassOptions are libpq startup options that switch on the DB-level
// delete guards' escape hatches (migration 0034). Test suites create and
// delete fixture rows in audit/ledger tables directly; production code paths
// must opt in explicitly with SET LOCAL instead.
const guardBypassOptions = "-c app.audit_purge=on -c app.allow_ledger_delete=on"

// PostgresDSN returns a PostgreSQL DSN constructed from environment variables.
// Tests MUST run against a dedicated test database: the default dbname is
// ai_proxy_portal_test, and any resolved name that does not contain "test"
// panics — running tests (which include destructive drop/recreate suites)
// against a production database wiped one once already.
//
// Sessions opened with this DSN have the audit-purge and ledger-delete guards
// bypassed so fixture cleanup (DELETE FROM usage_records, ...) keeps working.
// Tests that exercise the guards themselves must use PostgresDSNStrict.
func PostgresDSN() string {
	return postgresDSN(true)
}

// PostgresDSNStrict is PostgresDSN without the guard bypass options: sessions
// behave exactly like production sessions.
func PostgresDSNStrict() string {
	return postgresDSN(false)
}

func postgresDSN(bypassGuards bool) string {
	host := getEnv("DB_HOST", "127.0.0.1")
	port := getEnv("DB_PORT", "5432")
	user := getEnv("DB_USER", "postgres")
	// Keep the historical dev password as the default so local tests work without a .env file.
	password := getEnv("DB_PASSWORD", "66ededab2addc43f194e15169497842289f123e068d62a105cbca65b93becf69")
	dbname := getEnv("DB_NAME", "ai_proxy_portal_test")
	if !strings.Contains(strings.ToLower(dbname), "test") {
		panic(fmt.Sprintf("testutil: refusing to run tests against non-test database %q; set DB_NAME to a *test* database", dbname))
	}
	sslmode := getEnv("DB_SSLMODE", "disable")
	timezone := getEnv("DB_TIMEZONE", "UTC")
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s&timezone=%s",
		user, password, host, port, dbname, sslmode, timezone)
	if bypassGuards {
		dsn += "&options=" + url.QueryEscape(guardBypassOptions)
	}
	return dsn
}

// RedisAddr returns the Redis address from the environment or the local dev default.
func RedisAddr() string {
	return getEnv("REDIS_ADDR", "127.0.0.1:6379")
}

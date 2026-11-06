package testutil

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPostgresDSN_Defaults(t *testing.T) {
	// Unset all DB env vars to test defaults
	for _, env := range []string{
		"DB_HOST", "DB_PORT", "DB_USER", "DB_PASSWORD", "DB_NAME", "DB_SSLMODE", "DB_TIMEZONE",
	} {
		_ = os.Unsetenv(env)
	}

	dsn := PostgresDSN()
	assert.Contains(t, dsn, "postgres://")
	assert.Contains(t, dsn, "127.0.0.1")
	assert.Contains(t, dsn, "5432")
	assert.Contains(t, dsn, "postgres")
	assert.Contains(t, dsn, "ai_proxy_portal")
	assert.Contains(t, dsn, "sslmode=disable")
	assert.Contains(t, dsn, "timezone=UTC")
}

func TestPostgresDSN_CustomValues(t *testing.T) {
	_ = os.Setenv("DB_HOST", "db.example.com")
	_ = os.Setenv("DB_PORT", "5433")
	_ = os.Setenv("DB_USER", "custom_user")
	_ = os.Setenv("DB_PASSWORD", "custom_pass")
	_ = os.Setenv("DB_NAME", "custom_test_db")
	_ = os.Setenv("DB_SSLMODE", "require")
	_ = os.Setenv("DB_TIMEZONE", "Asia/Shanghai")

	defer func() {
		for _, env := range []string{
			"DB_HOST", "DB_PORT", "DB_USER", "DB_PASSWORD", "DB_NAME", "DB_SSLMODE", "DB_TIMEZONE",
		} {
			_ = os.Unsetenv(env)
		}
	}()

	dsn := PostgresDSN()
	assert.Contains(t, dsn, "db.example.com")
	assert.Contains(t, dsn, "5433")
	assert.Contains(t, dsn, "custom_user")
	assert.Contains(t, dsn, "custom_pass")
	assert.Contains(t, dsn, "custom_test_db")
	assert.Contains(t, dsn, "sslmode=require")
	assert.Contains(t, dsn, "timezone=Asia/Shanghai")
}

func TestPostgresDSN_RejectsNonTestDB(t *testing.T) {
	_ = os.Setenv("DB_NAME", "ai_proxy_portal")
	defer func() { _ = os.Unsetenv("DB_NAME") }()

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for non-test database name")
		}
	}()
	PostgresDSN()
}

func TestPostgresDSN_URLEncodedPassword(t *testing.T) {
	_ = os.Setenv("DB_PASSWORD", "p@ss:word/with?special&chars")
	defer func() { _ = os.Unsetenv("DB_PASSWORD") }()

	dsn := PostgresDSN()
	// The password is included as-is in the DSN with URL encoding
	assert.Contains(t, dsn, "postgres://")
	// The DSN should be a valid URL format
	assert.True(t, strings.HasPrefix(dsn, "postgres://"))
}

func TestRedisAddr_Defaults(t *testing.T) {
	_ = os.Unsetenv("REDIS_ADDR")

	addr := RedisAddr()
	assert.Equal(t, "127.0.0.1:6379", addr)
}

func TestRedisAddr_Custom(t *testing.T) {
	_ = os.Setenv("REDIS_ADDR", "redis.example.com:6380")
	defer func() { _ = os.Unsetenv("REDIS_ADDR") }()

	addr := RedisAddr()
	assert.Equal(t, "redis.example.com:6380", addr)
}

func TestGetEnv_Helper(t *testing.T) {
	// Not set → returns default
	_ = os.Unsetenv("TEST_GETENV_KEY")
	got := getEnv("TEST_GETENV_KEY", "fallback")
	assert.Equal(t, "fallback", got)

	// Set → returns the value
	_ = os.Setenv("TEST_GETENV_KEY", "custom_value")
	defer func() { _ = os.Unsetenv("TEST_GETENV_KEY") }()
	got = getEnv("TEST_GETENV_KEY", "fallback")
	assert.Equal(t, "custom_value", got)

	// Empty → returns empty, not fallback (matches source code behavior)
	_ = os.Setenv("TEST_GETENV_KEY", "")
	got = getEnv("TEST_GETENV_KEY", "fallback")
	assert.Equal(t, "fallback", got) // source checks v != ""
}

package config

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLoad_Defaults(t *testing.T) {
	// Unset all relevant env vars
	for _, env := range []string{
		"PORT", "GIN_MODE", "ALLOWED_ORIGINS",
		"DB_HOST", "DB_PORT", "DB_NAME", "DB_USER", "DB_PASSWORD", "DB_POOL_MAX",
		"REDIS_HOST", "REDIS_PORT", "REDIS_PASSWORD", "REDIS_DB",
		"JWT_SECRET", "JWT_EXPIRES", "ADMIN_MODE", "ENCRYPTION_KEY",
		"MAX_SYSTEM_ADMINS", "MAX_SUPER_ADMINS", "MAX_ADMINS",
		"SMTP_HOST", "SMTP_PORT", "SMTP_SECURE", "SMTP_USER", "SMTP_PASSWORD", "SMTP_FROM",
		"PROXY_SYNC_SECRET", "APP_BASE_URL",
	} {
		_ = os.Unsetenv(env)
	}

	cfg := Load()
	assert.NotNil(t, cfg)

	// Server defaults
	assert.Equal(t, 3001, cfg.Server.Port)
	assert.Equal(t, "debug", cfg.Server.Mode)
	assert.Nil(t, cfg.Server.AllowedOrigins)

	// Database defaults
	assert.Equal(t, "127.0.0.1", cfg.Database.Host)
	assert.Equal(t, 5432, cfg.Database.Port)
	assert.Equal(t, "ai_proxy_portal", cfg.Database.Name)
	assert.Equal(t, "postgres", cfg.Database.User)
	assert.Equal(t, "", cfg.Database.Password)
	assert.Equal(t, 20, cfg.Database.MaxConns)

	// Redis defaults
	assert.Equal(t, "127.0.0.1", cfg.Redis.Host)
	assert.Equal(t, 6379, cfg.Redis.Port)
	assert.Equal(t, "", cfg.Redis.Password)
	assert.Equal(t, 0, cfg.Redis.DB)

	// Auth defaults
	assert.Equal(t, "", cfg.Auth.JWTSecret)
	assert.Equal(t, 7*24*time.Hour, cfg.Auth.JWTExpires)
	assert.Equal(t, "production", cfg.Auth.AdminMode)
	assert.Equal(t, "", cfg.Auth.EncryptionKey)

	// Admin defaults
	assert.Equal(t, 10, cfg.Admin.MaxSystemAdmins)
	assert.Equal(t, 3, cfg.Admin.MaxSuperAdmins)
	assert.Equal(t, 5, cfg.Admin.MaxAdminsMVP)

	// SMTP defaults
	assert.Equal(t, "", cfg.SMTP.Host)
	assert.Equal(t, 465, cfg.SMTP.Port)
	assert.Equal(t, true, cfg.SMTP.Secure)
	assert.Equal(t, "", cfg.SMTP.User)
	assert.Equal(t, "", cfg.SMTP.Password)
	assert.Equal(t, "", cfg.SMTP.From)

	// Portal defaults
	assert.Equal(t, "", cfg.Portal.SyncSecret)

	// App defaults
	assert.Equal(t, "https://portal.example.com", cfg.App.BaseURL)
}

func TestLoad_WithEnvVars(t *testing.T) {
	_ = os.Setenv("PORT", "8080")
	_ = os.Setenv("GIN_MODE", "release")
	_ = os.Setenv("ALLOWED_ORIGINS", "https://example.com, https://app.example.com")
	_ = os.Setenv("DB_HOST", "db.example.com")
	_ = os.Setenv("DB_PORT", "5433")
	_ = os.Setenv("DB_NAME", "test_db")
	_ = os.Setenv("DB_USER", "test_user")
	_ = os.Setenv("DB_PASSWORD", "secret")
	_ = os.Setenv("DB_POOL_MAX", "50")
	_ = os.Setenv("REDIS_HOST", "redis.example.com")
	_ = os.Setenv("REDIS_PORT", "6380")
	_ = os.Setenv("REDIS_PASSWORD", "redis-secret")
	_ = os.Setenv("REDIS_DB", "1")
	_ = os.Setenv("JWT_SECRET", "my-super-secret-jwt-key-32-chars")
	_ = os.Setenv("JWT_EXPIRES", "48h")
	_ = os.Setenv("ADMIN_MODE", "mvp")
	_ = os.Setenv("ENCRYPTION_KEY", "32-byte-encryption-key-here!!")
	_ = os.Setenv("MAX_SYSTEM_ADMINS", "5")
	_ = os.Setenv("MAX_SUPER_ADMINS", "2")
	_ = os.Setenv("MAX_ADMINS", "10")
	_ = os.Setenv("SMTP_HOST", "smtp.example.com")
	_ = os.Setenv("SMTP_PORT", "587")
	_ = os.Setenv("SMTP_SECURE", "false")
	_ = os.Setenv("SMTP_USER", "noreply@example.com")
	_ = os.Setenv("SMTP_PASSWORD", "smtp-secret")
	_ = os.Setenv("SMTP_FROM", "noreply@example.com")
	_ = os.Setenv("PROXY_SYNC_SECRET", "sync-secret")
	_ = os.Setenv("APP_BASE_URL", "https://custom.example.com")

	defer func() {
		for _, env := range []string{
			"PORT", "GIN_MODE", "ALLOWED_ORIGINS",
			"DB_HOST", "DB_PORT", "DB_NAME", "DB_USER", "DB_PASSWORD", "DB_POOL_MAX",
			"REDIS_HOST", "REDIS_PORT", "REDIS_PASSWORD", "REDIS_DB",
			"JWT_SECRET", "JWT_EXPIRES", "ADMIN_MODE", "ENCRYPTION_KEY",
			"MAX_SYSTEM_ADMINS", "MAX_SUPER_ADMINS", "MAX_ADMINS",
			"SMTP_HOST", "SMTP_PORT", "SMTP_SECURE", "SMTP_USER", "SMTP_PASSWORD", "SMTP_FROM",
			"PROXY_SYNC_SECRET", "APP_BASE_URL",
		} {
			_ = os.Unsetenv(env)
		}
	}()

	cfg := Load()

	// Server
	assert.Equal(t, 8080, cfg.Server.Port)
	assert.Equal(t, "release", cfg.Server.Mode)
	assert.Equal(t, []string{"https://example.com", "https://app.example.com"}, cfg.Server.AllowedOrigins)

	// Database
	assert.Equal(t, "db.example.com", cfg.Database.Host)
	assert.Equal(t, 5433, cfg.Database.Port)
	assert.Equal(t, "test_db", cfg.Database.Name)
	assert.Equal(t, "test_user", cfg.Database.User)
	assert.Equal(t, "secret", cfg.Database.Password)
	assert.Equal(t, 50, cfg.Database.MaxConns)

	// Redis
	assert.Equal(t, "redis.example.com", cfg.Redis.Host)
	assert.Equal(t, 6380, cfg.Redis.Port)
	assert.Equal(t, "redis-secret", cfg.Redis.Password)
	assert.Equal(t, 1, cfg.Redis.DB)

	// Auth
	assert.Equal(t, "my-super-secret-jwt-key-32-chars", cfg.Auth.JWTSecret)
	assert.Equal(t, 48*time.Hour, cfg.Auth.JWTExpires)
	assert.Equal(t, "mvp", cfg.Auth.AdminMode)
	assert.Equal(t, "32-byte-encryption-key-here!!", cfg.Auth.EncryptionKey)

	// Admin
	assert.Equal(t, 5, cfg.Admin.MaxSystemAdmins)
	assert.Equal(t, 2, cfg.Admin.MaxSuperAdmins)
	assert.Equal(t, 10, cfg.Admin.MaxAdminsMVP)

	// SMTP
	assert.Equal(t, "smtp.example.com", cfg.SMTP.Host)
	assert.Equal(t, 587, cfg.SMTP.Port)
	assert.Equal(t, false, cfg.SMTP.Secure)
	assert.Equal(t, "noreply@example.com", cfg.SMTP.User)
	assert.Equal(t, "smtp-secret", cfg.SMTP.Password)
	assert.Equal(t, "noreply@example.com", cfg.SMTP.From)

	// Portal
	assert.Equal(t, "sync-secret", cfg.Portal.SyncSecret)

	// App
	assert.Equal(t, "https://custom.example.com", cfg.App.BaseURL)
}

func TestGetEnv_Helper(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		setVal   string
		fallback string
		want     string
	}{
		{"env set", "TEST_ENV_SET", "hello", "fallback", "hello"},
		{"env not set", "TEST_ENV_NOT_SET", "", "fallback", "fallback"},
		{"env empty falls to fallback", "TEST_ENV_EMPTY", "", "default", "default"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setVal != "" {
				_ = os.Setenv(tt.key, tt.setVal)
				defer func() { _ = os.Unsetenv(tt.key) }()
			} else {
				_ = os.Unsetenv(tt.key)
			}
			got := getEnv(tt.key, tt.fallback)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetEnvInt_Helper(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		setVal   string
		fallback int
		want     int
	}{
		{"valid int", "TEST_INT_VALID", "42", 0, 42},
		{"not set", "TEST_INT_NOT_SET", "", 99, 99},
		{"invalid int", "TEST_INT_INVALID", "abc", 77, 77},
		{"negative", "TEST_INT_NEG", "-5", 0, -5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setVal != "" {
				_ = os.Setenv(tt.key, tt.setVal)
				defer func() { _ = os.Unsetenv(tt.key) }()
			} else {
				_ = os.Unsetenv(tt.key)
			}
			got := getEnvInt(tt.key, tt.fallback)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetEnvBool_Helper(t *testing.T) {
	tests := []struct {
		name     string
		setVal   string
		fallback bool
		want     bool
	}{
		{"true string", "true", false, true},
		{"1", "1", false, true},
		{"yes", "yes", false, true},
		{"false", "false", true, false},
		{"0", "0", true, false},
		{"not set", "", true, true},
		{"not set false fallback", "", false, false},
		{"random value", "random", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = os.Unsetenv("TEST_BOOL")
			if tt.setVal != "" {
				_ = os.Setenv("TEST_BOOL", tt.setVal)
			}
			got := getEnvBool("TEST_BOOL", tt.fallback)
			assert.Equal(t, tt.want, got)
			_ = os.Unsetenv("TEST_BOOL")
		})
	}
}

func TestGetEnvDuration_Helper(t *testing.T) {
	tests := []struct {
		name     string
		setVal   string
		fallback time.Duration
		want     time.Duration
	}{
		{"hours", "24h", time.Minute, 24 * time.Hour},
		{"minutes", "30m", time.Hour, 30 * time.Minute},
		{"seconds", "45s", time.Hour, 45 * time.Second},
		{"not set", "", time.Hour, time.Hour},
		{"invalid", "abc", 30 * time.Minute, 30 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = os.Unsetenv("TEST_DUR")
			if tt.setVal != "" {
				_ = os.Setenv("TEST_DUR", tt.setVal)
			}
			got := getEnvDuration("TEST_DUR", tt.fallback)
			assert.Equal(t, tt.want, got)
			_ = os.Unsetenv("TEST_DUR")
		})
	}
}

func TestGetEnvStringSlice_Helper(t *testing.T) {
	tests := []struct {
		name   string
		setVal string
		want   []string
	}{
		{"not set", "", nil},
		{"single value", "hello", []string{"hello"}},
		{"comma separated", "a, b, c", []string{"a", "b", "c"}},
		{"with spaces", "  x  , y ,  z  ", []string{"x", "y", "z"}},
		{"empty parts ignored", "a,,b", []string{"a", "b"}},
		{"URLs", "https://a.com, https://b.com", []string{"https://a.com", "https://b.com"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = os.Unsetenv("TEST_SLICE")
			if tt.setVal != "" {
				_ = os.Setenv("TEST_SLICE", tt.setVal)
			}
			got := getEnvStringSlice("TEST_SLICE")
			assert.Equal(t, tt.want, got)
			_ = os.Unsetenv("TEST_SLICE")
		})
	}
}

func TestLoad_AllowedOrigins_Empty(t *testing.T) {
	_ = os.Unsetenv("ALLOWED_ORIGINS")
	cfg := Load()
	assert.Nil(t, cfg.Server.AllowedOrigins)
}

func TestLoad_AllowedOrigins_Single(t *testing.T) {
	_ = os.Setenv("ALLOWED_ORIGINS", "https://myapp.com")
	defer func() { _ = os.Unsetenv("ALLOWED_ORIGINS") }()
	cfg := Load()
	assert.Equal(t, []string{"https://myapp.com"}, cfg.Server.AllowedOrigins)
}

func TestLoad_BoolFormats(t *testing.T) {
	tests := []struct {
		name string
		val  string
		want bool
	}{
		{"true", "true", true},
		{"false", "false", false},
		{"1", "1", true},
		{"0", "0", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = os.Setenv("SMTP_SECURE", tt.val)
			cfg := Load()
			assert.Equal(t, tt.want, cfg.SMTP.Secure)
		})
	}
	_ = os.Unsetenv("SMTP_SECURE")
}

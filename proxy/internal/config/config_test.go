package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidate(t *testing.T) {
	// W1 fix: Validate() requires Admin.Secret to be set (the insecure
	// "admin-secret" default was removed). Set a valid secret on every
	// config so these cases isolate the Portal URL validation logic.
	const testSecret = "test-admin-secret-32-characters-long"

	t.Run("valid portal URL with https", func(t *testing.T) {
		cfg := &Config{
			Admin: AdminConfig{Secret: testSecret},
			Portal: PortalConfig{
				URL: "https://portal.example.com",
			},
		}
		err := cfg.Validate()
		assert.NoError(t, err)
	})

	t.Run("valid empty portal URL", func(t *testing.T) {
		cfg := &Config{
			Admin: AdminConfig{Secret: testSecret},
			Portal: PortalConfig{
				URL: "",
			},
		}
		err := cfg.Validate()
		assert.NoError(t, err)
	})

	t.Run("valid portal URL with dev mode", func(t *testing.T) {
		cfg := &Config{
			Admin: AdminConfig{Secret: testSecret},
			Portal: PortalConfig{
				URL:     "http://localhost:3001",
				DevMode: true,
			},
		}
		err := cfg.Validate()
		assert.NoError(t, err)
	})

	t.Run("valid portal URL without schema (not parsed)", func(t *testing.T) {
		cfg := &Config{
			Admin: AdminConfig{Secret: testSecret},
			Portal: PortalConfig{
				URL:     "localhost:3001",
				DevMode: true,
			},
		}
		err := cfg.Validate()
		assert.NoError(t, err)
	})

	t.Run("invalid http URL in production mode", func(t *testing.T) {
		cfg := &Config{
			Admin: AdminConfig{Secret: testSecret},
			Portal: PortalConfig{
				URL:     "http://portal.example.com",
				DevMode: false,
			},
		}
		err := cfg.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "https://")
	})

	t.Run("invalid URL with bad format", func(t *testing.T) {
		cfg := &Config{
			Admin: AdminConfig{Secret: testSecret},
			Portal: PortalConfig{
				URL:     "://bad-url",
				DevMode: false,
			},
		}
		err := cfg.Validate()
		assert.Error(t, err)
	})
}

func TestIsLoopbackHost(t *testing.T) {
	tests := []struct {
		host     string
		expected bool
	}{
		{"localhost", true},
		{"LOCALHOST", true},
		{"127.0.0.1", true},
		{"::1", true},
		{"example.com", false},
		{"192.168.1.1", false},
		{"10.0.0.1", false},
	}

	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			result := isLoopbackHost(tt.host)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSetDefaults(t *testing.T) {
	v := viper.New()
	setDefaults(v)

	t.Run("server defaults", func(t *testing.T) {
		assert.Equal(t, 8080, v.GetInt("server.port"))
		assert.Equal(t, "release", v.GetString("server.mode"))
	})

	t.Run("database defaults", func(t *testing.T) {
		assert.Equal(t, "127.0.0.1", v.GetString("database.host"))
		assert.Equal(t, 5432, v.GetInt("database.port"))
		assert.Equal(t, "ai_proxy", v.GetString("database.name"))
		assert.Equal(t, "postgres", v.GetString("database.user"))
		assert.Equal(t, 20, v.GetInt("database.max_connections"))
	})

	t.Run("redis defaults", func(t *testing.T) {
		assert.Equal(t, "127.0.0.1", v.GetString("redis.host"))
		assert.Equal(t, 6379, v.GetInt("redis.port"))
		assert.Equal(t, 0, v.GetInt("redis.db"))
	})

	t.Run("portal defaults", func(t *testing.T) {
		assert.Equal(t, "http://127.0.0.1:3001", v.GetString("portal.url"))
		assert.Equal(t, "60s", v.GetString("portal.sync_interval"))
	})

	t.Run("rate limit defaults", func(t *testing.T) {
		assert.Equal(t, true, v.GetBool("rate_limit.enabled"))
		assert.Equal(t, 300, v.GetInt("rate_limit.default_rpm"))
		assert.Equal(t, 100000, v.GetInt("rate_limit.default_tpm"))
		assert.Equal(t, "ratelimit:", v.GetString("rate_limit.redis_prefix"))
	})

	t.Run("cache defaults", func(t *testing.T) {
		assert.Equal(t, "1h", v.GetString("cache.affinity_ttl"))
		assert.Equal(t, true, v.GetBool("cache.affinity.enabled"))
		assert.Equal(t, 100000, v.GetInt("cache.affinity.max_entries"))
		assert.Equal(t, "1h", v.GetString("cache.affinity.default_ttl"))
		assert.Equal(t, true, v.GetBool("cache.affinity.redis_enabled"))
	})

	t.Run("audit defaults", func(t *testing.T) {
		assert.Equal(t, true, v.GetBool("audit.enabled"))
		assert.Equal(t, 100, v.GetInt("audit.batch_size"))
		assert.Equal(t, "1s", v.GetString("audit.flush_interval"))
		assert.Equal(t, 7, v.GetInt("audit.retention_years"))
	})

	t.Run("billing defaults", func(t *testing.T) {
		assert.Equal(t, true, v.GetBool("billing.pre_consume_enabled"))
		assert.Equal(t, 0.1, v.GetFloat64("billing.cache_read_ratio"))
		assert.Equal(t, 0.25, v.GetFloat64("billing.cache_write_ratio"))
	})

	t.Run("admin defaults", func(t *testing.T) {
		// W1 fix: the insecure "admin-secret" default was removed. setDefaults
		// no longer seeds admin.secret; it must be provided via env/config and
		// Validate() rejects both empty and the well-known "admin-secret".
		assert.Equal(t, "", v.GetString("admin.secret"))
	})

	t.Run("simulate defaults", func(t *testing.T) {
		assert.Equal(t, false, v.GetBool("simulate.enabled"))
		assert.Equal(t, 100, v.GetInt("simulate.latency_ms"))
	})
}

func TestOverrideFromEnv(t *testing.T) {
	t.Run("DB environment variables", func(t *testing.T) {
		v := viper.New()

		// Set env vars
		os.Setenv("DB_HOST", "db.example.com")
		os.Setenv("DB_PORT", "5433")
		os.Setenv("DB_NAME", "testdb")
		os.Setenv("DB_USER", "admin")
		os.Setenv("DB_PASSWORD", "secret")
		defer func() {
			os.Unsetenv("DB_HOST")
			os.Unsetenv("DB_PORT")
			os.Unsetenv("DB_NAME")
			os.Unsetenv("DB_USER")
			os.Unsetenv("DB_PASSWORD")
		}()

		overrideFromEnv(v)

		assert.Equal(t, "db.example.com", v.GetString("database.host"))
		assert.Equal(t, 5433, v.GetInt("database.port"))
		assert.Equal(t, "testdb", v.GetString("database.name"))
		assert.Equal(t, "admin", v.GetString("database.user"))
		assert.Equal(t, "secret", v.GetString("database.password"))
	})

	t.Run("Redis environment variables", func(t *testing.T) {
		v := viper.New()

		os.Setenv("REDIS_HOST", "redis.example.com")
		os.Setenv("REDIS_PORT", "6380")
		os.Setenv("REDIS_PASSWORD", "redis-secret")
		defer func() {
			os.Unsetenv("REDIS_HOST")
			os.Unsetenv("REDIS_PORT")
			os.Unsetenv("REDIS_PASSWORD")
		}()

		overrideFromEnv(v)

		assert.Equal(t, "redis.example.com", v.GetString("redis.host"))
		assert.Equal(t, 6380, v.GetInt("redis.port"))
		assert.Equal(t, "redis-secret", v.GetString("redis.password"))
	})

	t.Run("async charge on dispatch providers", func(t *testing.T) {
		v := viper.New()

		os.Setenv("ASYNC_CHARGE_ON_DISPATCH_PROVIDERS", "tokenrouter, aws-bedrock")
		defer func() {
			os.Unsetenv("ASYNC_CHARGE_ON_DISPATCH_PROVIDERS")
		}()

		overrideFromEnv(v)

		assert.Equal(t, []string{"tokenrouter", "aws-bedrock"},
			v.GetStringSlice("billing.async_charge_on_dispatch_providers"))
	})

	t.Run("Portal environment variables", func(t *testing.T) {
		v := viper.New()

		os.Setenv("PORTAL_URL", "https://portal.example.com")
		os.Setenv("PORTAL_SYNC_SECRET", "sync-key")
		os.Setenv("PORTAL_DEV_MODE", "true")
		defer func() {
			os.Unsetenv("PORTAL_URL")
			os.Unsetenv("PORTAL_SYNC_SECRET")
			os.Unsetenv("PORTAL_DEV_MODE")
		}()

		overrideFromEnv(v)

		assert.Equal(t, "https://portal.example.com", v.GetString("portal.url"))
		assert.Equal(t, "sync-key", v.GetString("portal.sync_secret"))
		assert.True(t, v.GetBool("portal.dev_mode"))
	})

	t.Run("Auth environment variables", func(t *testing.T) {
		v := viper.New()

		os.Setenv("JWT_SECRET", "my-jwt-secret")
		os.Setenv("ENCRYPTION_KEY", "my-enc-key")
		defer func() {
			os.Unsetenv("JWT_SECRET")
			os.Unsetenv("ENCRYPTION_KEY")
		}()

		overrideFromEnv(v)

		assert.Equal(t, "my-jwt-secret", v.GetString("auth.jwt_secret"))
		assert.Equal(t, "my-enc-key", v.GetString("auth.encryption_key"))
	})

	t.Run("Server environment variables", func(t *testing.T) {
		v := viper.New()

		os.Setenv("PORT", "9090")
		os.Setenv("GIN_MODE", "debug")
		os.Setenv("ALLOWED_ORIGINS", "https://a.com, https://b.com")
		defer func() {
			os.Unsetenv("PORT")
			os.Unsetenv("GIN_MODE")
			os.Unsetenv("ALLOWED_ORIGINS")
		}()

		overrideFromEnv(v)

		assert.Equal(t, 9090, v.GetInt("server.port"))
		assert.Equal(t, "debug", v.GetString("server.mode"))
		origins := v.GetStringSlice("server.allowed_origins")
		assert.Len(t, origins, 2)
		assert.Contains(t, origins, "https://a.com")
		assert.Contains(t, origins, "https://b.com")
	})

	t.Run("Rate limit environment variables", func(t *testing.T) {
		v := viper.New()

		os.Setenv("RATE_LIMIT_ENABLED", "false")
		os.Setenv("RATE_LIMIT_DEFAULT_RPM", "120")
		os.Setenv("RATE_LIMIT_DEFAULT_TPM", "200000")
		defer func() {
			os.Unsetenv("RATE_LIMIT_ENABLED")
			os.Unsetenv("RATE_LIMIT_DEFAULT_RPM")
			os.Unsetenv("RATE_LIMIT_DEFAULT_TPM")
		}()

		overrideFromEnv(v)

		assert.False(t, v.GetBool("rate_limit.enabled"))
		assert.Equal(t, 120, v.GetInt("rate_limit.default_rpm"))
		assert.Equal(t, 200000, v.GetInt("rate_limit.default_tpm"))
	})

	t.Run("Audit environment variables", func(t *testing.T) {
		v := viper.New()

		os.Setenv("AUDIT_ENABLED", "true")
		os.Setenv("AUDIT_BATCH_SIZE", "500")
		defer func() {
			os.Unsetenv("AUDIT_ENABLED")
			os.Unsetenv("AUDIT_BATCH_SIZE")
		}()

		overrideFromEnv(v)

		assert.True(t, v.GetBool("audit.enabled"))
		assert.Equal(t, 500, v.GetInt("audit.batch_size"))
	})

	t.Run("Simulate environment variables", func(t *testing.T) {
		v := viper.New()

		os.Setenv("SIMULATE_ENABLED", "true")
		os.Setenv("SIMULATE_LATENCY_MS", "250")
		defer func() {
			os.Unsetenv("SIMULATE_ENABLED")
			os.Unsetenv("SIMULATE_LATENCY_MS")
		}()

		overrideFromEnv(v)

		assert.True(t, v.GetBool("simulate.enabled"))
		assert.Equal(t, 250, v.GetInt("simulate.latency_ms"))
	})

	t.Run("Admin environment variable", func(t *testing.T) {
		v := viper.New()

		os.Setenv("ADMIN_SECRET", "my-admin")
		defer func() { os.Unsetenv("ADMIN_SECRET") }()

		overrideFromEnv(v)

		assert.Equal(t, "my-admin", v.GetString("admin.secret"))
	})

	t.Run("invalid numeric env values are ignored", func(t *testing.T) {
		v := viper.New()
		setDefaults(v)

		os.Setenv("DB_PORT", "not-a-number")
		defer func() { os.Unsetenv("DB_PORT") }()

		overrideFromEnv(v)

		// Should remain default since "not-a-number" is not a valid int
		assert.Equal(t, 5432, v.GetInt("database.port"))
	})
}

func TestLoad_FromFile(t *testing.T) {
	// Create a temporary config file
	tmpDir := t.TempDir()
	configYAML := `
server:
  port: 9999
  mode: debug
  id: test-server
database:
  host: test-db.example.com
  port: 5433
  name: testdb
  user: testuser
  password: testpass
redis:
  host: test-redis.example.com
  port: 6380
  password: redispass
  db: 1
portal:
  url: "https://portal.example.com"
  dev_mode: false
rate_limit:
  enabled: true
  default_rpm: 120
cache:
  affinity_ttl: "30m"
audit:
  enabled: false
  batch_size: 200
billing:
  pre_consume_enabled: false
  cache_read_ratio: 0.5
admin:
  secret: admin123
simulate:
  enabled: true
  latency_ms: 500
`

	err := os.WriteFile(filepath.Join(tmpDir, "config.yaml"), []byte(configYAML), 0644)
	require.NoError(t, err)

	// Read config from temp dir
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(tmpDir)
	err = v.ReadInConfig()
	require.NoError(t, err)

	var cfg Config
	err = v.Unmarshal(&cfg)
	require.NoError(t, err)

	assert.Equal(t, 9999, cfg.Server.Port)
	assert.Equal(t, "debug", cfg.Server.Mode)
	assert.Equal(t, "test-server", cfg.Server.ID)
	assert.Equal(t, "test-db.example.com", cfg.Database.Host)
	assert.Equal(t, 5433, cfg.Database.Port)
	assert.Equal(t, "testdb", cfg.Database.Name)
	assert.Equal(t, "testuser", cfg.Database.User)
	assert.Equal(t, "testpass", cfg.Database.Password)
	assert.Equal(t, "test-redis.example.com", cfg.Redis.Host)
	assert.Equal(t, 6380, cfg.Redis.Port)
	assert.Equal(t, "redispass", cfg.Redis.Password)
	assert.Equal(t, 1, cfg.Redis.DB)
	assert.Equal(t, "https://portal.example.com", cfg.Portal.URL)
	assert.False(t, cfg.Portal.DevMode)
	assert.True(t, cfg.RateLimit.Enabled)
	assert.Equal(t, 120, cfg.RateLimit.DefaultRPM)
	assert.False(t, cfg.Audit.Enabled)
	assert.Equal(t, 200, cfg.Audit.BatchSize)
	assert.False(t, cfg.Billing.PreConsumeEnabled)
	assert.Equal(t, 0.5, cfg.Billing.CacheReadRatio)
	assert.Equal(t, "admin123", cfg.Admin.Secret)
	assert.True(t, cfg.Simulate.Enabled)
	assert.Equal(t, 500, cfg.Simulate.LatencyMs)
}

func TestConfigStructs(t *testing.T) {
	t.Run("DatabaseConfig", func(t *testing.T) {
		cfg := DatabaseConfig{
			Host:           "db.example.com",
			Port:           5432,
			Name:           "mydb",
			User:           "dbuser",
			Password:       "dbpass",
			MaxConnections: 50,
		}

		assert.Equal(t, "db.example.com", cfg.Host)
		assert.Equal(t, 5432, cfg.Port)
		assert.Equal(t, "mydb", cfg.Name)
		assert.Equal(t, "dbuser", cfg.User)
		assert.Equal(t, "dbpass", cfg.Password)
		assert.Equal(t, 50, cfg.MaxConnections)
	})

	t.Run("ArchiveConfig", func(t *testing.T) {
		cfg := ArchiveConfig{
			Enabled:  true,
			S3Bucket: "my-bucket",
			S3Prefix: "audit/",
		}

		assert.True(t, cfg.Enabled)
		assert.Equal(t, "my-bucket", cfg.S3Bucket)
		assert.Equal(t, "audit/", cfg.S3Prefix)
	})

	t.Run("AuthConfig", func(t *testing.T) {
		cfg := AuthConfig{
			JWTSecret:     "jwt-secret",
			JWTIssuer:     "ai-proxy",
			EncryptionKey: "enc-key",
		}

		assert.Equal(t, "jwt-secret", cfg.JWTSecret)
		assert.Equal(t, "ai-proxy", cfg.JWTIssuer)
		assert.Equal(t, "enc-key", cfg.EncryptionKey)
	})

	t.Run("ServerConfig with allowed origins", func(t *testing.T) {
		cfg := ServerConfig{
			Port:           8080,
			Mode:           "release",
			ID:             "server-1",
			AllowedOrigins: []string{"https://app.example.com", "https://admin.example.com"},
		}

		assert.Equal(t, 8080, cfg.Port)
		assert.Equal(t, "release", cfg.Mode)
		assert.Equal(t, "server-1", cfg.ID)
		assert.Len(t, cfg.AllowedOrigins, 2)
	})
}

func TestValidate_Portal(t *testing.T) {
	// W1 fix: Validate() now requires Admin.Secret to be set explicitly (the
	// insecure "admin-secret" default was removed). Set a valid secret on
	// every test config so these cases isolate the Portal URL validation
	// logic rather than tripping the admin-secret gate.
	const testSecret = "test-admin-secret-32-characters-long"

	t.Run("empty portal URL is valid (no validation needed)", func(t *testing.T) {
		cfg := &Config{Admin: AdminConfig{Secret: testSecret}, Portal: PortalConfig{URL: ""}}
		assert.NoError(t, cfg.Validate())
	})

	t.Run("localhost is valid in production with http", func(t *testing.T) {
		cfg := &Config{Admin: AdminConfig{Secret: testSecret}, Portal: PortalConfig{URL: "http://localhost:3001"}}
		assert.NoError(t, cfg.Validate())
	})

	t.Run("127.0.0.1 is valid in production with http", func(t *testing.T) {
		cfg := &Config{Admin: AdminConfig{Secret: testSecret}, Portal: PortalConfig{URL: "http://127.0.0.1:3001"}}
		assert.NoError(t, cfg.Validate())
	})

	t.Run("non-loopback http in production is invalid", func(t *testing.T) {
		cfg := &Config{Admin: AdminConfig{Secret: testSecret}, Portal: PortalConfig{URL: "http://192.168.1.100:3001"}}
		assert.Error(t, cfg.Validate())
	})

	t.Run("any URL with dev_mode true is valid", func(t *testing.T) {
		cfg := &Config{Admin: AdminConfig{Secret: testSecret}, Portal: PortalConfig{
			URL:     "http://my-insecure-portal.local:3001",
			DevMode: true,
		}}
		assert.NoError(t, cfg.Validate())
	})
}

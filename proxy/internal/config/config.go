package config

import (
	"crypto/tls"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/spf13/viper"
)

type Config struct {
	Server      ServerConfig      `mapstructure:"server"`
	Database    DatabaseConfig    `mapstructure:"database"`
	Redis       RedisConfig       `mapstructure:"redis"`
	Portal      PortalConfig      `mapstructure:"portal"`
	Auth        AuthConfig        `mapstructure:"auth"`
	RateLimit   RateLimitConfig   `mapstructure:"rate_limit"`
	Cache       CacheConfig       `mapstructure:"cache"`
	PromptCache PromptCacheConfig `mapstructure:"prompt_cache"`
	Audit       AuditConfig       `mapstructure:"audit"`
	Billing     BillingConfig     `mapstructure:"billing"`
	Admin       AdminConfig       `mapstructure:"admin"`
	Simulate    SimulateConfig    `mapstructure:"simulate"`
	Queue       QueueConfig       `mapstructure:"queue"`
	Retention   RetentionConfig   `mapstructure:"retention"`
	Plugin      PluginConfig      `mapstructure:"plugin"`
	Failover    FailoverConfig    `mapstructure:"failover"`
}

// FailoverConfig controls in-request upstream retry & failover. When an upstream
// returns a retryable error (429/5xx/524/transport EOF) before any response byte
// has been sent, the proxy re-dispatches to the next candidate key/provider.
// See docs/upstream-retry-failover-design-202610251430.md.
type FailoverConfig struct {
	Enabled               bool  `mapstructure:"enabled"`                  // master switch
	MaxAttempts           int   `mapstructure:"max_attempts"`             // total attempts incl. the first (default 3)
	RetryStatuses         []int `mapstructure:"retry_statuses"`           // HTTP statuses that trigger retry
	RetryOnTransportError bool  `mapstructure:"retry_on_transport_error"` // retry on EOF/reset/RemoteDisconnected
	PerAttemptJitterMs    int   `mapstructure:"per_attempt_jitter_ms"`    // small jitter before same-provider retry
	SlowUpstreamStatus    int   `mapstructure:"slow_upstream_status"`     // status treated as slow-upstream (default 524)
	SlowUpstreamMaxSwitch int   `mapstructure:"slow_upstream_max_switch"` // max cross-provider switches for slow-upstream (524), default 2
	// DisabledModels lists logical models for which failover is OFF even when
	// Enabled is true (per-model control). Empty = failover on for all models.
	DisabledModels []string `mapstructure:"disabled_models"`
}

type QueueConfig struct {
	Enabled            bool `mapstructure:"enabled"`              // Enable per-provider worker queues for non-streaming upstream dispatch
	WorkersPerProvider int  `mapstructure:"workers_per_provider"` // Number of worker goroutines per provider queue
	SizePerProvider    int  `mapstructure:"size_per_provider"`    // Maximum number of queued jobs per provider
	SyncFallback       bool `mapstructure:"sync_fallback"`        // Dispatch synchronously when the queue is full instead of rejecting
}

type PluginConfig struct {
	Enabled                   bool `mapstructure:"enabled"`
	StreamOptionsInjector     bool `mapstructure:"stream_options_injector"`
	ClaudeCodeCCHNormalizer   bool `mapstructure:"claude_code_cch_normalizer"`
	ClaudeCodeTrackingRemover bool `mapstructure:"claude_code_tracking_remover"`
	ResponsesPromptCacheKey   bool `mapstructure:"responses_prompt_cache_key"`
}

type RetentionConfig struct {
	Enabled           bool          `mapstructure:"enabled"`
	Directory         string        `mapstructure:"directory"`
	MaxAge            time.Duration `mapstructure:"max_age"`
	BatchSize         int           `mapstructure:"batch_size"`
	FlushEvery        time.Duration `mapstructure:"flush_every"`
	CaptureAllHeaders bool          `mapstructure:"capture_all_headers"`
	// MaxDiskPercent: retention writes are skipped while the retention
	// filesystem is fuller than this percentage (default 90; >=100 disables).
	MaxDiskPercent int `mapstructure:"max_disk_percent"`
}

type SimulateConfig struct {
	Enabled   bool `mapstructure:"enabled"`
	LatencyMs int  `mapstructure:"latency_ms"`
}

type ServerConfig struct {
	Port           int      `mapstructure:"port"`
	Mode           string   `mapstructure:"mode"`            // debug, release
	ID             string   `mapstructure:"id"`              // Server instance ID for identification
	Region         string   `mapstructure:"region"`          // Server region for cross-region routing
	AllowedOrigins []string `mapstructure:"allowed_origins"` // CORS allowed origins; empty = allow all
}

type DatabaseConfig struct {
	Host           string `mapstructure:"host"`
	Port           int    `mapstructure:"port"`
	Name           string `mapstructure:"name"`
	User           string `mapstructure:"user"`
	Password       string `mapstructure:"password"`
	MaxConnections int    `mapstructure:"max_connections"`
	SSLMode        string `mapstructure:"ssl_mode"`
	SSLRootCert    string `mapstructure:"ssl_root_cert"`
}

type RedisConfig struct {
	Host          string `mapstructure:"host"`
	Port          int    `mapstructure:"port"`
	Password      string `mapstructure:"password"`
	DB            int    `mapstructure:"db"`
	TLSEnabled    bool   `mapstructure:"tls_enabled"`
	TLSSkipVerify bool   `mapstructure:"tls_skip_verify"`
}

// Options builds a go-redis Options value from this config.
func (c RedisConfig) Options() *redis.Options {
	opts := &redis.Options{
		Addr:     fmt.Sprintf("%s:%d", c.Host, c.Port),
		Password: c.Password,
		DB:       c.DB,
	}
	if c.TLSEnabled {
		opts.TLSConfig = &tls.Config{
			InsecureSkipVerify: c.TLSSkipVerify,
		}
	}
	return opts
}

type PortalConfig struct {
	URL                 string        `mapstructure:"url"`
	SyncSecret          string        `mapstructure:"sync_secret"`
	SyncInterval        time.Duration `mapstructure:"sync_interval"`
	HealthCheckInterval time.Duration `mapstructure:"health_check_interval"`
	DevMode             bool          `mapstructure:"dev_mode"` // Allow placeholder claims when URL is empty (development only)
}

type AuthConfig struct {
	JWTSecret     string `mapstructure:"jwt_secret"`
	JWTIssuer     string `mapstructure:"jwt_issuer"`
	EncryptionKey string `mapstructure:"encryption_key"`
}

type RateLimitConfig struct {
	Enabled     bool   `mapstructure:"enabled"`
	DefaultRPM  int    `mapstructure:"default_rpm"`
	DefaultTPM  int    `mapstructure:"default_tpm"`
	RedisPrefix string `mapstructure:"redis_prefix"`
}

type CacheConfig struct {
	AffinityTTL time.Duration       `mapstructure:"affinity_ttl"`
	Affinity    AffinityCacheConfig `mapstructure:"affinity"`
}

type AffinityCacheConfig struct {
	Enabled      bool          `mapstructure:"enabled"`
	MaxEntries   int           `mapstructure:"max_entries"`
	DefaultTTL   time.Duration `mapstructure:"default_ttl"`
	RedisEnabled bool          `mapstructure:"redis_enabled"`
}

// PromptCacheConfig controls proxy-side prompt-cache marker injection.
type PromptCacheConfig struct {
	// OpenAIInjectEnabled injects Anthropic-style cache_control markers into
	// OpenAI-format requests relayed to aggregators (OpenRouter/TokenRouter)
	// fronting Claude models. Default true; the Bedrock channel always
	// injects its own cachePoints regardless of this flag.
	OpenAIInjectEnabled bool `mapstructure:"openai_inject_enabled"`
}

type AuditConfig struct {
	Enabled        bool          `mapstructure:"enabled"`
	BatchSize      int           `mapstructure:"batch_size"`
	FlushInterval  time.Duration `mapstructure:"flush_interval"`
	RetentionYears int           `mapstructure:"retention_years"`
	HotDuration    time.Duration `mapstructure:"hot_duration"`
	WarmDuration   time.Duration `mapstructure:"warm_duration"`
	Archive        ArchiveConfig `mapstructure:"archive"`
}

type ArchiveConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	S3Bucket string `mapstructure:"s3_bucket"`
	S3Prefix string `mapstructure:"s3_prefix"`
}

type BillingConfig struct {
	PreConsumeEnabled        bool          `mapstructure:"pre_consume_enabled"`
	PreConsumeMargin         float64       `mapstructure:"pre_consume_margin"` // Multiplier for estimated input cost (e.g. 1.2 = 20% safety margin)
	DailyBudgetUSD           float64       `mapstructure:"daily_budget_usd"`   // Daily cost budget in USD (0 = disabled)
	CacheReadRatio           float64       `mapstructure:"cache_read_ratio"`
	CacheWriteRatio          float64       `mapstructure:"cache_write_ratio"`
	LocalBalanceCacheEnabled bool          `mapstructure:"local_balance_cache_enabled"` // Use Redis-backed local balance cache instead of portal pre-consume/settle
	LocalBalanceCacheTTL     time.Duration `mapstructure:"local_balance_cache_ttl"`     // TTL for cached balance entries

	// AsyncChargeOnDispatchProviders lists upstream providers that bill media
	// tasks at dispatch time regardless of the final task outcome (e.g.
	// TokenRouter records the charge when the task is created). For these
	// providers the proxy bills at dispatch and emits an automatic refund
	// when the poll sees a terminal failure, matching upstream cost
	// semantics. Providers not listed keep deferred bill-on-terminal-success.
	AsyncChargeOnDispatchProviders []string `mapstructure:"async_charge_on_dispatch_providers"`
}

type AdminConfig struct {
	Secret string `mapstructure:"secret"`
}

// Load reads configuration from file and environment variables
func Load() (*Config, error) {
	v := viper.New()

	// Set defaults
	setDefaults(v)

	// Read config file
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	v.AddConfigPath("/app")
	v.AddConfigPath("/etc/ai-proxy")

	// Read config file. A missing file is acceptable (env vars may provide
	// everything), but a parse error must surface early.
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("reading config file: %w", err)
		}
	}

	// Override with environment variables directly
	// This is more reliable than viper's AutomaticEnv for nested configs
	overrideFromEnv(v)

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// Validate checks the configuration for unsafe or inconsistent settings.
func (cfg *Config) Validate() error {
	if cfg.Portal.URL != "" && !cfg.Portal.DevMode {
		u, err := url.Parse(cfg.Portal.URL)
		if err != nil {
			return fmt.Errorf("invalid portal.url: %w", err)
		}
		if u.Scheme != "https" && !isLoopbackHost(u.Hostname()) {
			return fmt.Errorf("portal.url must use https:// in production (got %q). Set portal.dev_mode=true for local development", cfg.Portal.URL)
		}
	}

	// Admin secret must be set explicitly and must not be the well-known default.
	if cfg.Admin.Secret == "" {
		return fmt.Errorf("admin.secret must be set (env ADMIN_SECRET or config file); no insecure default is provided")
	}
	if cfg.Admin.Secret == "admin-secret" {
		return fmt.Errorf("admin.secret must not be the well-known default \"admin-secret\"; set a strong unique value via ADMIN_SECRET")
	}
	return nil
}

// isLoopbackHost reports whether host is a loopback address or localhost name.
func isLoopbackHost(host string) bool {
	host = strings.ToLower(host)
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	return false
}

// overrideFromEnv reads environment variables and overrides viper values
func overrideFromEnv(v *viper.Viper) {
	// Database
	if val := os.Getenv("DB_HOST"); val != "" {
		v.Set("database.host", val)
	}
	if val := os.Getenv("DB_PORT"); val != "" {
		if port, err := strconv.Atoi(val); err == nil {
			v.Set("database.port", port)
		}
	}
	if val := os.Getenv("DB_NAME"); val != "" {
		v.Set("database.name", val)
	}
	if val := os.Getenv("DB_USER"); val != "" {
		v.Set("database.user", val)
	}
	if val := os.Getenv("DB_PASSWORD"); val != "" {
		v.Set("database.password", val)
	}
	if val := os.Getenv("DB_SSLMODE"); val != "" {
		v.Set("database.ssl_mode", val)
	}
	if val := os.Getenv("DB_SSL_ROOT_CERT"); val != "" {
		v.Set("database.ssl_root_cert", val)
	}

	// Redis
	if val := os.Getenv("REDIS_HOST"); val != "" {
		v.Set("redis.host", val)
	}
	if val := os.Getenv("REDIS_PORT"); val != "" {
		if port, err := strconv.Atoi(val); err == nil {
			v.Set("redis.port", port)
		}
	}
	if val := os.Getenv("REDIS_PASSWORD"); val != "" {
		v.Set("redis.password", val)
	}
	if val := os.Getenv("REDIS_TLS"); val != "" {
		if enabled, err := strconv.ParseBool(val); err == nil {
			v.Set("redis.tls_enabled", enabled)
		}
	}
	if val := os.Getenv("REDIS_TLS_SKIP_VERIFY"); val != "" {
		if skip, err := strconv.ParseBool(val); err == nil {
			v.Set("redis.tls_skip_verify", skip)
		}
	}

	// Portal
	if val := os.Getenv("PORTAL_URL"); val != "" {
		v.Set("portal.url", val)
	}
	if val := os.Getenv("PORTAL_SYNC_SECRET"); val != "" {
		v.Set("portal.sync_secret", val)
	}
	if val := os.Getenv("PORTAL_DEV_MODE"); val != "" {
		if b, err := strconv.ParseBool(val); err == nil {
			v.Set("portal.dev_mode", b)
		}
	}
	if val := os.Getenv("PORTAL_SYNC_INTERVAL"); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			v.Set("portal.sync_interval", d)
		}
	}
	if val := os.Getenv("KEYPOOL_HEALTH_CHECK_INTERVAL"); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			v.Set("portal.health_check_interval", d)
		}
	}

	// Auth
	if val := os.Getenv("JWT_SECRET"); val != "" {
		v.Set("auth.jwt_secret", val)
	}
	if val := os.Getenv("ENCRYPTION_KEY"); val != "" {
		v.Set("auth.encryption_key", val)
	}

	// Admin
	if val := os.Getenv("ADMIN_SECRET"); val != "" {
		v.Set("admin.secret", val)
	}

	// Server
	if val := os.Getenv("PORT"); val != "" {
		if port, err := strconv.Atoi(val); err == nil {
			v.Set("server.port", port)
		}
	}
	if val := os.Getenv("GIN_MODE"); val != "" {
		v.Set("server.mode", val)
	}
	if val := os.Getenv("SERVER_ID"); val != "" {
		v.Set("server.id", val)
	}
	if val := os.Getenv("SERVER_REGION"); val != "" {
		v.Set("server.region", val)
	}
	if val := os.Getenv("ALLOWED_ORIGINS"); val != "" {
		origins := []string{}
		for _, o := range strings.Split(val, ",") {
			o = strings.TrimSpace(o)
			if o != "" {
				origins = append(origins, o)
			}
		}
		v.Set("server.allowed_origins", origins)
	}

	// Rate limit
	if val := os.Getenv("RATE_LIMIT_ENABLED"); val != "" {
		if enabled, err := strconv.ParseBool(val); err == nil {
			v.Set("rate_limit.enabled", enabled)
		}
	}
	if val := os.Getenv("RATE_LIMIT_DEFAULT_RPM"); val != "" {
		if rpm, err := strconv.Atoi(val); err == nil {
			v.Set("rate_limit.default_rpm", rpm)
		}
	}
	if val := os.Getenv("RATE_LIMIT_DEFAULT_TPM"); val != "" {
		if tpm, err := strconv.Atoi(val); err == nil {
			v.Set("rate_limit.default_tpm", tpm)
		}
	}

	// Cache
	if val := os.Getenv("CACHE_AFFINITY_TTL"); val != "" {
		if ttl, err := time.ParseDuration(val); err == nil {
			v.Set("cache.affinity_ttl", ttl)
		}
	}

	// Audit
	if val := os.Getenv("AUDIT_ENABLED"); val != "" {
		if enabled, err := strconv.ParseBool(val); err == nil {
			v.Set("audit.enabled", enabled)
		}
	}
	if val := os.Getenv("AUDIT_BATCH_SIZE"); val != "" {
		if size, err := strconv.Atoi(val); err == nil {
			v.Set("audit.batch_size", size)
		}
	}

	// Simulate
	if val := os.Getenv("SIMULATE_ENABLED"); val != "" {
		if enabled, err := strconv.ParseBool(val); err == nil {
			v.Set("simulate.enabled", enabled)
		}
	}
	if val := os.Getenv("SIMULATE_LATENCY_MS"); val != "" {
		if latency, err := strconv.Atoi(val); err == nil {
			v.Set("simulate.latency_ms", latency)
		}
	}

	// Queue
	if val := os.Getenv("QUEUE_ENABLED"); val != "" {
		if enabled, err := strconv.ParseBool(val); err == nil {
			v.Set("queue.enabled", enabled)
		}
	}
	if val := os.Getenv("QUEUE_WORKERS_PER_PROVIDER"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			v.Set("queue.workers_per_provider", n)
		}
	}
	if val := os.Getenv("QUEUE_SIZE_PER_PROVIDER"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			v.Set("queue.size_per_provider", n)
		}
	}
	if val := os.Getenv("QUEUE_SYNC_FALLBACK"); val != "" {
		if enabled, err := strconv.ParseBool(val); err == nil {
			v.Set("queue.sync_fallback", enabled)
		}
	}

	// Retention (local disk)
	if val := os.Getenv("RETENTION_ENABLED"); val != "" {
		if enabled, err := strconv.ParseBool(val); err == nil {
			v.Set("retention.enabled", enabled)
		}
	}
	if val := os.Getenv("RETENTION_DIRECTORY"); val != "" {
		v.Set("retention.directory", val)
	}
	if val := os.Getenv("RETENTION_MAX_DISK_PERCENT"); val != "" {
		if n, err := strconv.Atoi(val); err == nil && n >= 0 {
			v.Set("retention.max_disk_percent", n)
		}
	}
	if val := os.Getenv("RETENTION_MAX_AGE"); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			v.Set("retention.max_age", d)
		}
	}
	if val := os.Getenv("RETENTION_BATCH_SIZE"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			v.Set("retention.batch_size", n)
		}
	}
	if val := os.Getenv("RETENTION_FLUSH_EVERY"); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			v.Set("retention.flush_every", d)
		}
	}
	if val := os.Getenv("RETENTION_CAPTURE_ALL_HEADERS"); val != "" {
		if enabled, err := strconv.ParseBool(val); err == nil {
			v.Set("retention.capture_all_headers", enabled)
		}
	}

	// Plugin
	if val := os.Getenv("PLUGIN_ENABLED"); val != "" {
		if enabled, err := strconv.ParseBool(val); err == nil {
			v.Set("plugin.enabled", enabled)
		}
	}
	if val := os.Getenv("PLUGIN_STREAM_OPTIONS_INJECTOR"); val != "" {
		if enabled, err := strconv.ParseBool(val); err == nil {
			v.Set("plugin.stream_options_injector", enabled)
		}
	}
	if val := os.Getenv("PLUGIN_CLAUDE_CODE_CCH_NORMALIZER"); val != "" {
		if enabled, err := strconv.ParseBool(val); err == nil {
			v.Set("plugin.claude_code_cch_normalizer", enabled)
		}
	}
	if val := os.Getenv("PLUGIN_CLAUDE_CODE_TRACKING_REMOVER"); val != "" {
		if enabled, err := strconv.ParseBool(val); err == nil {
			v.Set("plugin.claude_code_tracking_remover", enabled)
		}
	}
	if val := os.Getenv("PLUGIN_RESPONSES_PROMPT_CACHE_KEY"); val != "" {
		if enabled, err := strconv.ParseBool(val); err == nil {
			v.Set("plugin.responses_prompt_cache_key", enabled)
		}
	}

	// Billing / local balance cache
	if val := os.Getenv("LOCAL_BALANCE_CACHE_ENABLED"); val != "" {
		if enabled, err := strconv.ParseBool(val); err == nil {
			v.Set("billing.local_balance_cache_enabled", enabled)
		}
	}
	if val := os.Getenv("LOCAL_BALANCE_CACHE_TTL"); val != "" {
		if ttl, err := time.ParseDuration(val); err == nil {
			v.Set("billing.local_balance_cache_ttl", ttl)
		}
	}
	// Comma-separated provider list, e.g. "tokenrouter". The production
	// container runs without a mounted config.yaml, so this list must be
	// settable from the environment.
	if val := os.Getenv("ASYNC_CHARGE_ON_DISPATCH_PROVIDERS"); val != "" {
		providers := strings.Split(val, ",")
		for i := range providers {
			providers[i] = strings.TrimSpace(providers[i])
		}
		v.Set("billing.async_charge_on_dispatch_providers", providers)
	}
}

func setDefaults(v *viper.Viper) {
	// Server
	v.SetDefault("server.port", 8080)
	v.SetDefault("server.mode", "release")
	v.SetDefault("server.id", "")

	// Database
	v.SetDefault("database.host", "127.0.0.1")
	v.SetDefault("database.port", 5432)
	v.SetDefault("database.name", "ai_proxy")
	v.SetDefault("database.user", "postgres")
	v.SetDefault("database.max_connections", 20)

	// Redis
	v.SetDefault("redis.host", "127.0.0.1")
	v.SetDefault("redis.port", 6379)
	v.SetDefault("redis.db", 0)

	// Portal
	v.SetDefault("portal.url", "http://127.0.0.1:3001")
	v.SetDefault("portal.sync_interval", "60s")
	v.SetDefault("portal.health_check_interval", "5m")

	// Rate Limit
	v.SetDefault("rate_limit.enabled", true)
	v.SetDefault("rate_limit.default_rpm", 300)
	v.SetDefault("rate_limit.default_tpm", 100000)
	v.SetDefault("rate_limit.redis_prefix", "ratelimit:")

	// Cache
	v.SetDefault("cache.affinity_ttl", "1h")
	v.SetDefault("cache.affinity.enabled", true)
	v.SetDefault("cache.affinity.max_entries", 100000)
	v.SetDefault("cache.affinity.default_ttl", "1h")
	v.SetDefault("cache.affinity.redis_enabled", true)

	v.SetDefault("prompt_cache.openai_inject_enabled", true)

	// Audit
	v.SetDefault("audit.enabled", true)
	v.SetDefault("audit.batch_size", 100)
	v.SetDefault("audit.flush_interval", "1s")
	v.SetDefault("audit.retention_years", 7)
	v.SetDefault("audit.hot_duration", "2160h")  // 3 months
	v.SetDefault("audit.warm_duration", "8760h") // 12 months

	// Billing
	v.SetDefault("billing.pre_consume_enabled", true)
	v.SetDefault("billing.pre_consume_margin", 1.0)
	v.SetDefault("billing.cache_read_ratio", 0.1)
	v.SetDefault("billing.cache_write_ratio", 0.25)
	v.SetDefault("billing.local_balance_cache_enabled", false)
	v.SetDefault("billing.local_balance_cache_ttl", "10s")

	// Admin — no default secret; Validate() enforces a non-empty, non-default value.

	// Simulate
	v.SetDefault("simulate.enabled", false)
	v.SetDefault("simulate.latency_ms", 100)

	// Queue
	v.SetDefault("queue.enabled", false)
	v.SetDefault("queue.workers_per_provider", 4)
	v.SetDefault("queue.size_per_provider", 64)
	v.SetDefault("queue.sync_fallback", true)

	// In-request upstream retry & failover (enabled by default; see
	// docs/upstream-retry-failover-design-202610251430.md).
	v.SetDefault("failover.enabled", true)
	v.SetDefault("failover.max_attempts", 3)
	v.SetDefault("failover.retry_statuses", []int{429, 500, 502, 503, 504, 524})
	v.SetDefault("failover.retry_on_transport_error", true)
	v.SetDefault("failover.per_attempt_jitter_ms", 200)
	v.SetDefault("failover.slow_upstream_status", 524)
	v.SetDefault("failover.slow_upstream_max_switch", 2)
	v.SetDefault("failover.disabled_models", []string{})

	// Retention (local disk, disabled by default)
	v.SetDefault("retention.enabled", false)
	v.SetDefault("retention.directory", "./retention")
	v.SetDefault("retention.max_age", "720h") // 30 days
	v.SetDefault("retention.batch_size", 1)
	v.SetDefault("retention.flush_every", "3s")
	v.SetDefault("retention.capture_all_headers", false)
	v.SetDefault("retention.max_disk_percent", 90)

	// Plugin
	v.SetDefault("plugin.enabled", true)
	v.SetDefault("plugin.stream_options_injector", true)
	v.SetDefault("plugin.claude_code_cch_normalizer", true)
	v.SetDefault("plugin.claude_code_tracking_remover", true)
	v.SetDefault("plugin.responses_prompt_cache_key", true)
}

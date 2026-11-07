package db

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mayiants/ai-portal-go/internal/config"
	"github.com/mayiants/ai-portal-go/pkg/crypto"
	"github.com/mayiants/ai-portal-go/pkg/metrics"
	"github.com/mayiants/ai-portal-go/pkg/telemetry"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const (
	// RedisSyncChannel is the Pub/Sub channel for Proxy sync notifications
	RedisSyncChannel = "ai-proxy:sync"
	// RedisBalanceChannel is the Pub/Sub channel for balance update notifications
	RedisBalanceChannel = "ai-proxy:balance"
)

// ModelLimit is a per-model RPM/TPM budget for one upstream key. A map entry
// keyed by "*" applies to every model without a more specific entry.
type ModelLimit struct {
	RPM int   `json:"rpm"`
	TPM int64 `json:"tpm"`
}

// SyncKeyResponse is the key-pool entry returned to Proxies. It lives in the
// db package so that PublishSyncEvent can compute the same deterministic
// config_version used by the /key-pool/sync and /key-pool/version endpoints.
type SyncKeyResponse struct {
	ID             string                `json:"id"`
	Provider       string                `json:"provider"`
	Key            string                `json:"key"`
	KeyPreview     string                `json:"key_preview"`
	Tier           string                `json:"tier"`
	Priority       string                `json:"priority"`
	BaseURL        string                `json:"baseUrl,omitempty"`
	Models         []string              `json:"models,omitempty"`
	Enabled        bool                  `json:"enabled"`
	AssignmentMode string                `json:"assignmentMode"`
	IsShared       bool                  `json:"isShared"`
	RPMLimit       int                   `json:"rpm_limit,omitempty"`
	TPMLimit       int                   `json:"tpm_limit,omitempty"`
	ModelLimits    map[string]ModelLimit `json:"model_limits,omitempty"`
	QuotaLimit     int64                 `json:"quota_limit,omitempty"`
	QuotaUsed      int64                 `json:"quota_used,omitempty"`
}

// ParseModelLimits normalizes a model_limits JSONB value (as returned by the
// pgx driver: []byte, string or map) into a ModelLimit map. Returns nil when
// the column is NULL or malformed.
func ParseModelLimits(raw interface{}) map[string]ModelLimit {
	if raw == nil {
		return nil
	}
	var data []byte
	switch v := raw.(type) {
	case []byte:
		data = v
	case string:
		data = []byte(v)
	case map[string]interface{}:
		b, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		data = b
	default:
		return nil
	}
	out := map[string]ModelLimit{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// SyncMessage is the message published to notify Proxies of config changes
type SyncMessage struct {
	Type          string `json:"type"`
	Source        string `json:"source"`
	Timestamp     string `json:"timestamp"`
	ConfigVersion string `json:"config_version,omitempty"`
}

// BalanceUpdateMessage is the message published when a user's balance drops below threshold
type BalanceUpdateMessage struct {
	Type      string `json:"type"`      // "balance_update"
	UserID    string `json:"user_id"`   // user whose balance changed
	Balance   int64  `json:"balance"`   // current total balance in micro-USD (gift + recharge + balance)
	Threshold int64  `json:"threshold"` // threshold that triggered this notification
	Timestamp string `json:"timestamp"`
}

// RevocationMessage is published to invalidate a Proxy's auth caches immediately
// when access is revoked, so a disabled/deleted key or suspended user/enterprise
// stops spending without waiting for the Proxy's ~5 min validation cache TTL.
// It is delivered both over Redis pub/sub (RedisBalanceChannel) and the HTTPS
// callback (/api/webhook/balance-update), matching the Proxy's two listeners.
type RevocationMessage struct {
	Type      string `json:"type"`               // "key_revoked" | "user_banned"
	KeyHash   string `json:"key_hash,omitempty"` // SHA-256 hash the Proxy caches by (key_revoked)
	UserID    string `json:"user_id,omitempty"`  // user whose caches to drop (user_banned)
	Timestamp string `json:"timestamp"`
}

type DB struct {
	Pool              *pgxpool.Pool
	Redis             *redis.Client
	logger            *zap.Logger
	cfg               *config.Config
	proxyCallbackURLs []string
	proxySyncSecret   string
	callbackClient    *http.Client
	callbackOnce      sync.Once
}

func New(cfg *config.Config, logger *zap.Logger) (*DB, error) {
	// PostgreSQL
	sslMode := cfg.Database.SSLMode
	if sslMode == "" {
		sslMode = "disable"
	}
	userPass := url.UserPassword(cfg.Database.User, cfg.Database.Password)
	dsn := fmt.Sprintf("postgres://%s@%s:%d/%s?sslmode=%s&timezone=UTC",
		userPass.String(),
		cfg.Database.Host, cfg.Database.Port, cfg.Database.Name, sslMode)
	if cfg.Database.SSLRootCert != "" {
		dsn += "&sslrootcert=" + cfg.Database.SSLRootCert
	}

	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse db config: %w", err)
	}
	poolCfg.MaxConns = int32(cfg.Database.MaxConns)
	poolCfg.MaxConnLifetime = 30 * time.Minute
	poolCfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(context.Background(), poolCfg)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}

	if err := pool.Ping(context.Background()); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}
	logger.Info("PostgreSQL connected", zap.String("sslmode", sslMode))

	// Redis
	opts := &redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Redis.Host, cfg.Redis.Port),
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	}
	if cfg.Redis.TLSEnabled {
		opts.TLSConfig = &tls.Config{
			InsecureSkipVerify: cfg.Redis.TLSSkipVerify,
		}
	}
	rdb := redis.NewClient(opts)

	if err := rdb.Ping(context.Background()).Err(); err != nil {
		logger.Warn("Redis connection failed", zap.Error(err))
	} else {
		logger.Info("Redis connected", zap.Bool("tls", cfg.Redis.TLSEnabled))
	}

	return &DB{Pool: pool, Redis: rdb, logger: logger, cfg: cfg}, nil
}

func (db *DB) Close() {
	if db.Pool != nil {
		db.Pool.Close()
	}
	if db.Redis != nil {
		_ = db.Redis.Close()
	}
}

// metricsPipeline wraps a redis.Pipeliner and counts each Exec as one Redis
// operation for metrics purposes.
type metricsPipeline struct {
	redis.Pipeliner
}

func (p *metricsPipeline) Exec(ctx context.Context) ([]redis.Cmder, error) {
	metrics.GetMetrics().RedisOpsCount.Add(1)
	return p.Pipeliner.Exec(ctx)
}

// RedisGet wraps Redis Get and counts the operation.
func (db *DB) RedisGet(ctx context.Context, key string) *redis.StringCmd {
	metrics.GetMetrics().RedisOpsCount.Add(1)
	return db.Redis.Get(ctx, key)
}

// RedisSet wraps Redis Set and counts the operation.
func (db *DB) RedisSet(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd {
	metrics.GetMetrics().RedisOpsCount.Add(1)
	return db.Redis.Set(ctx, key, value, expiration)
}

// RedisPublish wraps Redis Publish and counts the operation.
func (db *DB) RedisPublish(ctx context.Context, channel string, message interface{}) *redis.IntCmd {
	metrics.GetMetrics().RedisOpsCount.Add(1)
	return db.Redis.Publish(ctx, channel, message)
}

// RedisRPush wraps Redis RPush and counts the operation.
func (db *DB) RedisRPush(ctx context.Context, key string, values ...interface{}) *redis.IntCmd {
	metrics.GetMetrics().RedisOpsCount.Add(1)
	return db.Redis.RPush(ctx, key, values...)
}

// RedisRPop wraps Redis RPop and counts the operation.
func (db *DB) RedisRPop(ctx context.Context, key string) *redis.StringCmd {
	metrics.GetMetrics().RedisOpsCount.Add(1)
	return db.Redis.RPop(ctx, key)
}

// RedisPipeline wraps Redis Pipeline and counts each Exec as one operation.
func (db *DB) RedisPipeline() redis.Pipeliner {
	return &metricsPipeline{Pipeliner: db.Redis.Pipeline()}
}

// Logger returns the configured logger, or nil if none was provided.
func (db *DB) Logger() *zap.Logger {
	return db.logger
}

// Query executes a query and returns rows
func (db *DB) Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error) {
	start := time.Now()
	rows, err := db.Pool.Query(ctx, sql, args...)
	metrics.GetMetrics().DBQueryCount.Add(1)
	metrics.GetMetrics().DBQueryLatencyMs.Add(time.Since(start).Milliseconds())
	return rows, err
}

// QueryRow executes a query that returns a single row
func (db *DB) QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	start := time.Now()
	row := db.Pool.QueryRow(ctx, sql, args...)
	metrics.GetMetrics().DBQueryCount.Add(1)
	metrics.GetMetrics().DBQueryLatencyMs.Add(time.Since(start).Milliseconds())
	return row
}

// Exec executes a query without returning rows
func (db *DB) Exec(ctx context.Context, sql string, args ...interface{}) error {
	start := time.Now()
	_, err := db.Pool.Exec(ctx, sql, args...)
	metrics.GetMetrics().DBQueryCount.Add(1)
	metrics.GetMetrics().DBQueryLatencyMs.Add(time.Since(start).Milliseconds())
	return err
}

// MustExec executes SQL and logs any error. Use when the caller does not handle the error.
func (db *DB) MustExec(ctx context.Context, sql string, args ...interface{}) {
	if err := db.Exec(ctx, sql, args...); err != nil && db.logger != nil {
		db.logger.Error("Database exec failed", zap.Error(err), zap.String("sql", sql))
	}
}

// MustExecTx executes SQL within a transaction and logs any error.
func MustExecTx(ctx context.Context, logger *zap.Logger, tx pgx.Tx, sql string, args ...interface{}) {
	if _, err := tx.Exec(ctx, sql, args...); err != nil && logger != nil {
		logger.Error("Tx exec failed", zap.Error(err), zap.String("sql", sql))
	}
}

// WithTx executes fn within a database transaction. Commit on success, rollback on error.
func (db *DB) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ComputeKeyPoolConfigVersion returns a deterministic hash of the stable
// key-pool configuration (keys, providers, routing, model routing and smart
// routing). It mirrors the data used by routes.syncKeyPool to compute the
// config_version field, so the version endpoint, sync endpoint and sync
// notifications all agree on the same version string.
func (db *DB) ComputeKeyPoolConfigVersion(ctx context.Context, proxyID, region string) (string, error) {
	rows, err := db.Query(ctx, `
		SELECT id::text, provider, key_encrypted, key_preview, tier, base_url, models,
		       enabled, assignment_mode, assigned_proxy, rpm_limit, tpm_limit, model_limits, quota_limit, quota_used
		FROM upstream_keys
		WHERE enabled = true AND validation_status IN ('valid', 'pending')
		ORDER BY provider, created_at
	`)
	if err != nil {
		return "", fmt.Errorf("query upstream keys: %w", err)
	}
	defer rows.Close()

	keys := []SyncKeyResponse{}
	exclusiveCount := 0
	sharedCount := 0

	for rows.Next() {
		var id, provider, keyEncrypted, keyPreview, tier string
		var baseURL *string
		var models interface{}
		var enabled bool
		var assignmentMode string
		var assignedProxy *string
		var rpmLimit, tpmLimit *int
		var modelLimitsRaw interface{}
		var quotaLimit, quotaUsed *int64

		if err := rows.Scan(&id, &provider, &keyEncrypted, &keyPreview, &tier, &baseURL, &models,
			&enabled, &assignmentMode, &assignedProxy, &rpmLimit, &tpmLimit, &modelLimitsRaw, &quotaLimit, &quotaUsed); err != nil {
			if db.logger != nil {
				db.logger.Warn("Failed to scan upstream key for version", zap.Error(err))
			}
			continue
		}

		var modelList []string
		if models != nil {
			switch v := models.(type) {
			case []byte:
				_ = json.Unmarshal(v, &modelList)
			case string:
				_ = json.Unmarshal([]byte(v), &modelList)
			case []interface{}:
				for _, item := range v {
					if s, ok := item.(string); ok {
						modelList = append(modelList, s)
					}
				}
			}
		}

		rpmVal := 0
		if rpmLimit != nil {
			rpmVal = *rpmLimit
		}
		tpmVal := 0
		if tpmLimit != nil {
			tpmVal = *tpmLimit
		}
		modelLimits := ParseModelLimits(modelLimitsRaw)
		quotaVal := int64(0)
		if quotaLimit != nil {
			quotaVal = *quotaLimit
		}
		quotaUsedVal := int64(0)
		if quotaUsed != nil {
			quotaUsedVal = *quotaUsed
		}

		if assignmentMode == "exclusive" && assignedProxy != nil {
			if proxyID != "" && *assignedProxy != proxyID {
				exclusiveCount++
				continue
			}
			exclusiveCount++
		} else {
			sharedCount++
		}

		priority := "normal"
		switch tier {
		case "premium", "enterprise":
			priority = "high"
		case "economy":
			priority = "economy"
		}

		baseURLStr := ""
		if baseURL != nil {
			baseURLStr = *baseURL
		}

		var actualKey string
		if db.cfg != nil && db.cfg.Auth.EncryptionKey != "" {
			decrypted, err := crypto.Decrypt(keyEncrypted, db.cfg.Auth.EncryptionKey)
			if err != nil {
				if db.logger != nil {
					db.logger.Warn("Failed to decrypt upstream key for version, skipping",
						zap.String("key_id", id),
						zap.Error(err))
				}
				continue
			}
			actualKey = decrypted
		} else {
			if db.cfg != nil && db.cfg.Auth.AdminMode == "production" {
				return "", fmt.Errorf("upstream key encryption key (AUTH_ENCRYPTION_KEY) is not configured; refusing to sync plaintext keys in production mode")
			}
			if db.logger != nil {
				db.logger.Error("Auth.EncryptionKey is empty; using key_encrypted as plaintext — configure AUTH_ENCRYPTION_KEY",
					zap.String("key_id", id))
			}
			actualKey = keyEncrypted
		}

		keys = append(keys, SyncKeyResponse{
			ID:             id,
			Provider:       provider,
			Key:            actualKey,
			KeyPreview:     keyPreview,
			Tier:           tier,
			Priority:       priority,
			BaseURL:        baseURLStr,
			Models:         modelList,
			Enabled:        enabled,
			AssignmentMode: assignmentMode,
			IsShared:       assignmentMode != "exclusive",
			RPMLimit:       rpmVal,
			TPMLimit:       tpmVal,
			ModelLimits:    modelLimits,
			QuotaLimit:     quotaVal,
			QuotaUsed:      quotaUsedVal,
		})
	}

	providerRows, err := db.Query(ctx, `
		SELECT id, name, base_url, enabled
		FROM providers
		WHERE enabled = true
		ORDER BY sort_order, name
	`)
	providers := []map[string]interface{}{}
	if err == nil {
		defer providerRows.Close()
		for providerRows.Next() {
			var id, name string
			var baseURL *string
			var enabled bool
			if err := providerRows.Scan(&id, &name, &baseURL, &enabled); err != nil {
				continue
			}
			baseURLStr := ""
			if baseURL != nil {
				baseURLStr = *baseURL
			}
			providers = append(providers, map[string]interface{}{
				"id":      id,
				"name":    name,
				"baseUrl": baseURLStr,
				"enabled": enabled,
			})
		}
	}

	smartRouting := map[string]bool{
		"health": true, "tier": true, "rpm": true, "latency": true, "quota": true,
	}
	settingRows, err := db.Query(ctx,
		`SELECT key, value FROM system_settings WHERE key LIKE 'routing_smart_%'`)
	if err == nil {
		defer settingRows.Close()
		for settingRows.Next() {
			var key, value string
			if settingRows.Scan(&key, &value) == nil {
				if strings.HasPrefix(key, "routing_smart_") {
					ruleName := strings.TrimPrefix(key, "routing_smart_")
					smartRouting[ruleName] = value == "true"
				}
			}
		}
	}

	routingRows, err := db.Query(ctx, `
		SELECT id::text, model_name, provider, priority, enabled
		FROM model_provider_routing
		WHERE enabled = true
		ORDER BY model_name, priority, provider
	`)
	modelRouting := []map[string]interface{}{}
	if err == nil {
		defer routingRows.Close()
		for routingRows.Next() {
			var id, modelName, provider string
			var priority int
			var enabled bool
			if err := routingRows.Scan(&id, &modelName, &provider, &priority, &enabled); err == nil {
				modelRouting = append(modelRouting, map[string]interface{}{
					"id": id, "model_name": modelName, "provider": provider,
					"priority": priority, "enabled": enabled,
				})
			}
		}
	}

	modelNameMap := db.buildModelNameMapForVersion(ctx)

	versionResp := map[string]interface{}{
		"keys":           keys,
		"providers":      providers,
		"exclusiveCount": exclusiveCount,
		"sharedCount":    sharedCount,
		"proxyId":        proxyID,
		"region":         region,
		"model_routing":  modelRouting,
		"model_name_map": modelNameMap,
		"smart_routing":  smartRouting,
	}
	versionInput, _ := json.Marshal(versionResp)
	hash := md5.Sum(versionInput)
	return hex.EncodeToString(hash[:]), nil
}

// buildModelNameMapForVersion returns the (provider, api_model_name, product_id)
// tuples used in the config version computation. Servable statuses follow the
// model lifecycle: 'active' + 'eol_pending' + 'eol' (see active_models_cache.go).
func (db *DB) buildModelNameMapForVersion(ctx context.Context) []map[string]interface{} {
	rows, err := db.Query(ctx, `
		SELECT provider, product_id, api_model_name, capabilities
		FROM service_models
		WHERE status IN ('active', 'eol_pending', 'eol') AND api_model_name IS NOT NULL AND api_model_name != ''
		ORDER BY provider, api_model_name
	`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var result []map[string]interface{}
	for rows.Next() {
		var provider, productID, apiModelName string
		var capsRaw []byte
		if rows.Scan(&provider, &productID, &apiModelName, &capsRaw) == nil {
			entry := map[string]interface{}{
				"provider":       provider,
				"product_id":     productID,
				"api_model_name": apiModelName,
			}
			if len(capsRaw) > 0 && string(capsRaw) != "{}" && string(capsRaw) != "null" {
				var caps map[string]bool
				if json.Unmarshal(capsRaw, &caps) == nil {
					entry["capabilities"] = caps
				}
			}
			result = append(result, entry)
		}
	}
	return result
}

// PublishSyncEvent publishes a sync notification to Redis Pub/Sub channel.
// Silently fails if Redis is not available to avoid blocking user operations.
func (db *DB) PublishSyncEvent(ctx context.Context) {
	db.initProxyCallbacks()
	if db.Redis == nil && len(db.proxyCallbackURLs) == 0 {
		return
	}

	configVersion := ""
	if cv, err := db.ComputeKeyPoolConfigVersion(ctx, "", ""); err == nil {
		configVersion = cv
	} else if db.logger != nil {
		db.logger.Warn("Failed to compute key-pool config version for sync event", zap.Error(err))
	}

	msg := SyncMessage{
		Type:          "sync_needed",
		Source:        "portal",
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
		ConfigVersion: configVersion,
	}
	payload, _ := json.Marshal(msg)
	// Use a short timeout to avoid blocking the request
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	if db.Redis != nil {
		_ = db.RedisPublish(ctx, RedisSyncChannel, payload).Err()
	}

	// Push via HTTPS callbacks as well, so Proxies without shared Redis still sync.
	db.notifyProxies(ctx, "/api/webhook/sync", payload)
}

// PublishBalanceUpdate publishes a balance update notification to Redis Pub/Sub.
// Called when a user's balance drops below the threshold after billing deduction.
// Proxies listening on this channel will invalidate their local balance cache immediately.
func (db *DB) PublishBalanceUpdate(ctx context.Context, userID string, balance int64, threshold int64) {
	if db.Redis == nil {
		return
	}
	msg := BalanceUpdateMessage{
		Type:      "balance_update",
		UserID:    userID,
		Balance:   balance,
		Threshold: threshold,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	payload, _ := json.Marshal(msg)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = db.RedisPublish(ctx, RedisBalanceChannel, payload).Err()

	// Also push via HTTPS callbacks so Proxies across VPCs / without shared Redis receive it.
	db.notifyProxies(ctx, "/api/webhook/balance-update", payload)
}

// PublishKeyRevoked notifies Proxies to invalidate the cached validation for a
// single key (by its SHA-256 hash) the moment the key is disabled or deleted,
// so it cannot keep spending until the Proxy's ~5 min validation cache expires.
// Delivered over both Redis pub/sub and the HTTPS callback (Portal and Proxy run
// separate Redis instances across EC2s, so the HTTPS callback is the reliable
// cross-host path).
func (db *DB) PublishKeyRevoked(ctx context.Context, keyHash string) {
	if keyHash == "" {
		return
	}
	msg := RevocationMessage{
		Type:      "key_revoked",
		KeyHash:   keyHash,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	payload, _ := json.Marshal(msg)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if db.Redis != nil {
		_ = db.RedisPublish(ctx, RedisBalanceChannel, payload).Err()
	}
	db.notifyProxies(ctx, "/api/webhook/balance-update", payload)
}

// PublishUserBanned notifies Proxies to drop ALL cached state (balance + every
// cached key) for a user the moment the user or their enterprise is suspended or
// deleted. Same dual delivery as PublishKeyRevoked.
func (db *DB) PublishUserBanned(ctx context.Context, userID string) {
	if userID == "" {
		return
	}
	msg := RevocationMessage{
		Type:      "user_banned",
		UserID:    userID,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	payload, _ := json.Marshal(msg)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if db.Redis != nil {
		_ = db.RedisPublish(ctx, RedisBalanceChannel, payload).Err()
	}
	db.notifyProxies(ctx, "/api/webhook/balance-update", payload)
}
func (db *DB) initProxyCallbacks() {
	db.callbackOnce.Do(func() {
		if urls := os.Getenv("PROXY_CALLBACK_URLS"); urls != "" {
			for _, u := range strings.Split(urls, ",") {
				u = strings.TrimSpace(u)
				if u != "" {
					db.proxyCallbackURLs = append(db.proxyCallbackURLs, strings.TrimSuffix(u, "/"))
				}
			}
		}
		db.proxySyncSecret = os.Getenv("PROXY_SYNC_SECRET")
		db.callbackClient = &http.Client{
			Timeout: 5 * time.Second,
		}
	})
}

// notifyProxies sends an HTTPS POST to each configured Proxy callback URL.
// It runs asynchronously and logs failures without blocking the caller.
func (db *DB) notifyProxies(ctx context.Context, path string, payload []byte) {
	db.initProxyCallbacks()
	if len(db.proxyCallbackURLs) == 0 {
		return
	}

	go func(parentCtx context.Context) {
		// Use a detached context with its own timeout. The caller's request
		// context may be canceled as soon as the HTTP handler returns, but the
		// callback must still be attempted in the background.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		for _, base := range db.proxyCallbackURLs {
			url := base + path
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
			if err != nil {
				db.logger.Warn("Failed to build proxy callback request", zap.String("url", url), zap.Error(err))
				continue
			}
			req.Header.Set("Content-Type", "application/json")
			if db.proxySyncSecret != "" {
				req.Header.Set("X-Sync-Secret", db.proxySyncSecret)
			}
			// Propagate any active OpenTelemetry trace from the parent request.
			telemetry.InjectTraceparent(parentCtx, req)

			resp, err := db.callbackClient.Do(req)
			if err != nil {
				db.logger.Warn("Proxy callback failed", zap.String("url", url), zap.Error(err))
				continue
			}
			_ = resp.Body.Close()
			if resp.StatusCode >= 300 {
				db.logger.Warn("Proxy callback returned non-2xx",
					zap.String("url", url),
					zap.Int("status", resp.StatusCode))
			}
		}
	}(ctx)
}

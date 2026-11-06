package routes

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/db"
	"go.uber.org/zap"
)

// Data-retention modes understood by the Proxy. They mirror the CHECK
// constraint on enterprises.retention_mode.
const (
	retentionModeOff      = "off"      // store nothing
	retentionModeMetadata = "metadata" // store request metadata only, no payloads
	retentionModeStandard = "standard" // store full request/response payloads

	retentionDaysMin = 1
	retentionDaysMax = 365

	// Platform defaults for keys that belong to no enterprise, used when the
	// system_settings rows are missing or hold an invalid value.
	defaultRetentionMode = retentionModeStandard
	defaultRetentionDays = 30

	settingRetentionDefaultMode = "retention_default_mode"
	settingRetentionDefaultDays = "retention_default_days"

	// platformRetentionCacheTTL keeps /api/keys/validate (hot path, called per
	// uncached key) from querying system_settings on every request.
	platformRetentionCacheTTL = 30 * time.Second
)

func validRetentionMode(mode string) bool {
	switch mode {
	case retentionModeOff, retentionModeMetadata, retentionModeStandard:
		return true
	}
	return false
}

func validRetentionDays(days int) bool {
	return days >= retentionDaysMin && days <= retentionDaysMax
}

var platformRetentionCache struct {
	mu       sync.Mutex
	mode     string
	days     int
	loadedAt time.Time
}

// resetPlatformRetentionCache drops the cached defaults after they are edited
// so the next validate call observes the change immediately on this instance.
func resetPlatformRetentionCache() {
	platformRetentionCache.mu.Lock()
	platformRetentionCache.loadedAt = time.Time{}
	platformRetentionCache.mu.Unlock()
}

// loadPlatformRetentionDefaults reads the platform defaults straight from
// system_settings, falling back per key when missing/invalid.
func loadPlatformRetentionDefaults(ctx context.Context, database *db.DB) (string, int) {
	mode, days := defaultRetentionMode, defaultRetentionDays
	if database == nil || database.Pool == nil {
		return mode, days
	}
	rows, err := database.Pool.Query(ctx,
		`SELECT key, value FROM system_settings WHERE key IN ($1, $2)`,
		settingRetentionDefaultMode, settingRetentionDefaultDays)
	if err != nil {
		return mode, days
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) != nil {
			continue
		}
		switch k {
		case settingRetentionDefaultMode:
			if validRetentionMode(v) {
				mode = v
			}
		case settingRetentionDefaultDays:
			if n, convErr := strconv.Atoi(v); convErr == nil && validRetentionDays(n) {
				days = n
			}
		}
	}
	return mode, days
}

// platformRetentionDefaults returns the (cached) platform-wide retention mode
// and days applied to keys without an enterprise.
func platformRetentionDefaults(ctx context.Context, database *db.DB) (string, int) {
	platformRetentionCache.mu.Lock()
	if !platformRetentionCache.loadedAt.IsZero() && time.Since(platformRetentionCache.loadedAt) < platformRetentionCacheTTL {
		mode, days := platformRetentionCache.mode, platformRetentionCache.days
		platformRetentionCache.mu.Unlock()
		return mode, days
	}
	platformRetentionCache.mu.Unlock()

	mode, days := loadPlatformRetentionDefaults(ctx, database)

	platformRetentionCache.mu.Lock()
	platformRetentionCache.mode, platformRetentionCache.days = mode, days
	platformRetentionCache.loadedAt = time.Now()
	platformRetentionCache.mu.Unlock()
	return mode, days
}

// invalidateEnterpriseValidateCaches drops the cached /api/keys/validate
// response (Portal Redis + Proxy in-process cache) of every non-deleted key of
// an enterprise. Call it after anything that changes the enterprise-level part
// of the validate response, such as the retention policy. There is no generic
// enterprise-wide helper elsewhere (suspendEnterprise iterates its own keys), so
// this reuses the per-key invalidateValidateCache that also notifies Proxies.
func invalidateEnterpriseValidateCaches(ctx context.Context, database *db.DB, logger *zap.Logger, enterpriseID string) {
	if database == nil || database.Pool == nil || enterpriseID == "" {
		return
	}
	rows, err := database.Pool.Query(ctx,
		`SELECT key_hash FROM api_keys WHERE enterprise_id = $1::uuid AND deleted_at IS NULL`, enterpriseID)
	if err != nil {
		if logger != nil {
			logger.Warn("Failed to list enterprise key hashes for cache invalidation",
				zap.String("enterprise_id", enterpriseID), zap.Error(err))
		}
		return
	}
	var hashes []string
	for rows.Next() {
		var h string
		if rows.Scan(&h) == nil {
			hashes = append(hashes, h)
		}
	}
	rows.Close()
	for _, h := range hashes {
		invalidateValidateCache(ctx, database, logger, h)
	}
}

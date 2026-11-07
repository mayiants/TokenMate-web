package routes

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/mayiants/ai-portal-go/internal/db"
)

const defaultSystemSettingsCacheTTL = 1 * time.Minute

// systemSettingsCache holds frequently-read global settings in memory to avoid
// hitting the database on every validate/key-creation request.
var systemSettingsCache struct {
	mu                       sync.RWMutex
	loadedAt                 time.Time
	ttl                      time.Duration
	DefaultUserRPM           int
	DefaultUserTPM           int
	DefaultUserDailyRequests int
}

// LoadSystemSettings loads default rate-limit settings from the database once
// at startup. Missing rows fall back to the hard-coded defaults.
func LoadSystemSettings(database *db.DB) error {
	systemSettingsCache.mu.Lock()
	defer systemSettingsCache.mu.Unlock()

	systemSettingsCache.DefaultUserRPM = loadSettingInt(database, "default_user_rpm", defaultAPIKeyRateLimitRPM)
	systemSettingsCache.DefaultUserTPM = loadSettingInt(database, "default_user_tpm", defaultAPIKeyRateLimitTPM)
	systemSettingsCache.DefaultUserDailyRequests = loadSettingInt(database, "default_user_daily_requests", defaultAPIKeyDailyRequests)
	systemSettingsCache.loadedAt = time.Now()
	if systemSettingsCache.ttl <= 0 {
		systemSettingsCache.ttl = defaultSystemSettingsCacheTTL
	}
	return nil
}

// ReloadSystemSettings refreshes the in-memory cache after an admin updates
// system settings.
func ReloadSystemSettings(database *db.DB) error {
	return LoadSystemSettings(database)
}

func loadSettingInt(database *db.DB, key string, fallback int) int {
	var value string
	err := database.QueryRow(context.Background(),
		`SELECT value FROM system_settings WHERE key = $1`, key).Scan(&value)
	if err != nil {
		return fallback
	}
	v, err := strconv.Atoi(value)
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

// maybeRefresh reloads settings if the cache has expired. It is called lazily
// from the getters so out-of-band DB changes converge without per-request DB
// queries.
func maybeRefresh(database *db.DB) {
	systemSettingsCache.mu.RLock()
	expired := time.Since(systemSettingsCache.loadedAt) > systemSettingsCache.ttl
	systemSettingsCache.mu.RUnlock()
	if !expired {
		return
	}
	_ = LoadSystemSettings(database)
}

// DefaultUserRPM returns the cached global default RPM.
func DefaultUserRPM(database *db.DB) int {
	maybeRefresh(database)
	systemSettingsCache.mu.RLock()
	defer systemSettingsCache.mu.RUnlock()
	return systemSettingsCache.DefaultUserRPM
}

// DefaultUserTPM returns the cached global default TPM.
func DefaultUserTPM(database *db.DB) int {
	maybeRefresh(database)
	systemSettingsCache.mu.RLock()
	defer systemSettingsCache.mu.RUnlock()
	return systemSettingsCache.DefaultUserTPM
}

// DefaultUserDailyRequests returns the cached global default daily request limit.
func DefaultUserDailyRequests(database *db.DB) int {
	maybeRefresh(database)
	systemSettingsCache.mu.RLock()
	defer systemSettingsCache.mu.RUnlock()
	return systemSettingsCache.DefaultUserDailyRequests
}

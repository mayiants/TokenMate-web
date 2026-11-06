package routes

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const (
	// activeServiceModelsCacheKey stores the set of currently active model names
	// that may be exposed through /api/keys/validate. It is a global cache shared
	// across all keys because the active model list is not key-specific.
	activeServiceModelsCacheKey = "active_service_models"

	// activeServiceModelsCacheTTL controls how long the active model set is cached.
	// Model-mutation handlers invalidate this key so updates are reflected quickly.
	activeServiceModelsCacheTTL = 1 * time.Minute
)

// loadActiveServiceModels returns the set of model names (api_model_name falling
// back to product_id) that are currently active. It first checks Redis, then
// falls back to the database and populates the cache.
func loadActiveServiceModels(ctx context.Context, database *db.DB, logger *zap.Logger) (map[string]bool, error) {
	if database == nil || database.Pool == nil {
		return nil, nil
	}

	// Try Redis first.
	if database.Redis != nil {
		cached, err := database.RedisGet(ctx, activeServiceModelsCacheKey).Result()
		if err == nil && cached != "" {
			var models map[string]bool
			if err := json.Unmarshal([]byte(cached), &models); err == nil {
				return models, nil
			}
			logger.Warn("Failed to unmarshal active service models cache", zap.Error(err))
		} else if err != nil && err != redis.Nil {
			logger.Warn("Failed to read active service models cache", zap.Error(err))
		}
	}

	// Cache miss or Redis unavailable: query the database.
	rows, err := database.Query(ctx,
		`SELECT COALESCE(api_model_name, product_id) FROM service_models
		 WHERE status IN ('active', 'eol_pending', 'eol')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	models := make(map[string]bool)
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err == nil && m != "" {
			models[m] = true
		}
	}

	// Populate Redis cache.
	if database.Redis != nil && len(models) > 0 {
		if data, err := json.Marshal(models); err == nil {
			if err := database.RedisSet(ctx, activeServiceModelsCacheKey, string(data), activeServiceModelsCacheTTL).Err(); err != nil {
				logger.Warn("Failed to write active service models cache", zap.Error(err))
			}
		}
	}

	return models, nil
}

// invalidateActiveServiceModelsCache removes the cached active model set. It
// should be called after any operation that changes service_models status or
// model names.
func invalidateActiveServiceModelsCache(ctx context.Context, database *db.DB, logger *zap.Logger) {
	if database == nil || database.Redis == nil {
		return
	}
	if err := database.Redis.Del(ctx, activeServiceModelsCacheKey).Err(); err != nil {
		logger.Warn("Failed to invalidate active service models cache", zap.Error(err))
	}
}

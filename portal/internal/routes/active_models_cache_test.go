package routes

import (
	"context"
	"testing"

	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/internal/testutil"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

func testRoutesDB(t *testing.T) *db.DB {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), testutil.PostgresDSN())
	if err != nil {
		t.Fatalf("connect to test db: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("ping test db: %v", err)
	}

	// Use a local Redis if one is available.
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		_ = rdb.Close()
		rdb = nil
	}

	return &db.DB{Pool: pool, Redis: rdb}
}

func TestLoadActiveServiceModels(t *testing.T) {
	database := testRoutesDB(t)
	defer database.Close()

	ctx := context.Background()
	logger := zap.NewNop()

	// Clean up any stale cache and models.
	if database.Redis != nil {
		_ = database.Redis.Del(ctx, activeServiceModelsCacheKey).Err()
	}
	_, _ = database.Pool.Exec(ctx, `DELETE FROM service_models WHERE provider = 'test-provider-load'`)

	// Seed active and inactive models.
	_, err := database.Pool.Exec(ctx,
		`INSERT INTO service_models (provider, product_id, api_model_name, status)
		 VALUES ('test-provider-load', 'prod-active', 'active-model', 'active'),
		        ('test-provider-load', 'prod-paused', 'paused-model', 'paused')`,
	)
	if err != nil {
		t.Fatalf("seed service models: %v", err)
	}
	defer func() {
		_, _ = database.Pool.Exec(context.Background(), `DELETE FROM service_models WHERE provider = 'test-provider-load'`)
	}()

	models, err := loadActiveServiceModels(ctx, database, logger)
	if err != nil {
		t.Fatalf("load active service models: %v", err)
	}
	if !models["active-model"] {
		t.Errorf("expected active-model to be active, got %v", models)
	}
	if models["paused-model"] {
		t.Errorf("expected paused-model to be inactive, got %v", models)
	}

	// Verify cache was written.
	if database.Redis != nil {
		cached, err := database.Redis.Get(ctx, activeServiceModelsCacheKey).Result()
		if err != nil {
			t.Fatalf("read cache: %v", err)
		}
		if cached == "" {
			t.Error("expected active service models cache to be set")
		}

		// Second load should hit cache.
		models2, err := loadActiveServiceModels(ctx, database, logger)
		if err != nil {
			t.Fatalf("load active service models from cache: %v", err)
		}
		if !models2["active-model"] {
			t.Errorf("expected active-model from cache, got %v", models2)
		}
	}
}

func TestInvalidateActiveServiceModelsCache(t *testing.T) {
	database := testRoutesDB(t)
	defer database.Close()

	if database.Redis == nil {
		t.Skip("Redis not available")
	}

	ctx := context.Background()
	logger := zap.NewNop()

	_ = database.RedisSet(ctx, activeServiceModelsCacheKey, `{"model":true}`, activeServiceModelsCacheTTL).Err()
	invalidateActiveServiceModelsCache(ctx, database, logger)

	_, err := database.Redis.Get(ctx, activeServiceModelsCacheKey).Result()
	if err == nil {
		t.Error("expected active service models cache to be deleted")
	}
}

func TestSyncServiceModelsToKeysInvalidatesCache(t *testing.T) {
	database := testRoutesDB(t)
	defer database.Close()

	if database.Redis == nil {
		t.Skip("Redis not available")
	}

	ctx := context.Background()

	_ = database.RedisSet(ctx, activeServiceModelsCacheKey, `{"model":true}`, activeServiceModelsCacheTTL).Err()

	// syncServiceModelsToKeys should invalidate the active models cache.
	_, err := syncServiceModelsToKeys(database, ctx, "")
	if err != nil {
		t.Fatalf("sync service models to keys: %v", err)
	}

	_, err = database.Redis.Get(ctx, activeServiceModelsCacheKey).Result()
	if err == nil {
		t.Error("expected active service models cache to be invalidated after sync")
	}
}

// Helper to avoid uuid import warning in case it's not used elsewhere.
var _ = uuid.New

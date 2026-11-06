package query

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openTestDB returns a *db.DB connected to the test Postgres instance, or skips
// the test if no database is available. It is modelled on the integration tests
// in internal/db/schema_test.go.
func openTestDB(t *testing.T) *db.DB {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, testutil.PostgresDSN())
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := pool.Ping(ctx); err != nil {
		t.Skipf("postgres ping failed: %v", err)
	}

	return &db.DB{Pool: pool}
}

// TestUsageQueries_Compile is a compile-time guard for the usage query helpers.
func TestUsageQueries_Compile(t *testing.T) {
	t.Skip("requires DB - compile guard only")
}

// TestUsageTypesExist validates the new usage query types compile.
func TestUsageTypesExist(t *testing.T) {
	_ = UsageRecordFilter{}
	_ = DailyUsage{}
	_ = TotalSummary{}
	_ = DistributionRow{}
	_ = DashboardStats{}
}

// TestGetUsageSummary_Integration verifies daily aggregation and totals.
func TestGetUsageSummary_Integration(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	email := fmt.Sprintf("usage_summary_%d@test.local", time.Now().UnixNano())
	var userID string
	require.NoError(t, database.QueryRow(ctx,
		`INSERT INTO users (name, email, password_hash, role, status, email_verified)
		 VALUES ('Test User', $1, 'hash', 'user', 'active', true)
		 RETURNING id::text`, email).Scan(&userID))

	t.Cleanup(func() {
		_, _ = database.Pool.Exec(ctx, "DELETE FROM usage_records WHERE user_id = $1", userID)
		_, _ = database.Pool.Exec(ctx, "DELETE FROM users WHERE id = $1", userID)
	})

	_, err := database.Pool.Exec(ctx,
		`INSERT INTO usage_records (trace_id, user_id, model, provider, input_tokens, output_tokens, total_cost, created_at)
		 VALUES (gen_random_uuid(), $1, 'gpt-4', 'openai', 10, 20, 1000, NOW())`, userID)
	require.NoError(t, err)

	daily, totals, err := GetUsageSummary(ctx, database, userID, 7)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(daily), 1, "expected at least one daily row")
	assert.GreaterOrEqual(t, totals.Requests, 1)
	assert.GreaterOrEqual(t, totals.Tokens, int64(30))
	assert.GreaterOrEqual(t, totals.Cost, int64(1000))
}

// TestGetUsageRecords_Integration verifies filtered usage record retrieval.
func TestGetUsageRecords_Integration(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	email := fmt.Sprintf("usage_records_%d@test.local", time.Now().UnixNano())
	var userID string
	require.NoError(t, database.QueryRow(ctx,
		`INSERT INTO users (name, email, password_hash, role, status, email_verified)
		 VALUES ('Test User', $1, 'hash', 'user', 'active', true)
		 RETURNING id::text`, email).Scan(&userID))

	t.Cleanup(func() {
		_, _ = database.Pool.Exec(ctx, "DELETE FROM usage_records WHERE user_id = $1", userID)
		_, _ = database.Pool.Exec(ctx, "DELETE FROM users WHERE id = $1", userID)
	})

	_, err := database.Pool.Exec(ctx,
		`INSERT INTO usage_records (trace_id, user_id, model, provider, input_tokens, output_tokens, cached_input_tokens, total_cost, latency_ms, created_at)
		 VALUES (gen_random_uuid(), $1, 'gpt-4', 'openai', 10, 20, 5, 1000, 100, NOW())`, userID)
	require.NoError(t, err)

	records, total, err := GetUsageRecords(ctx, database, userID, UsageRecordFilter{PageSize: 10})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, total, 1)
	assert.GreaterOrEqual(t, len(records), 1)
	assert.Equal(t, "gpt-4", records[0].Model)
	assert.Equal(t, "openai", records[0].Provider)
}

// TestGetUsageDistribution_Integration verifies model/provider aggregation.
func TestGetUsageDistribution_Integration(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	email := fmt.Sprintf("usage_distribution_%d@test.local", time.Now().UnixNano())
	var userID string
	require.NoError(t, database.QueryRow(ctx,
		`INSERT INTO users (name, email, password_hash, role, status, email_verified)
		 VALUES ('Test User', $1, 'hash', 'user', 'active', true)
		 RETURNING id::text`, email).Scan(&userID))

	t.Cleanup(func() {
		_, _ = database.Pool.Exec(ctx, "DELETE FROM usage_records WHERE user_id = $1", userID)
		_, _ = database.Pool.Exec(ctx, "DELETE FROM users WHERE id = $1", userID)
	})

	_, err := database.Pool.Exec(ctx,
		`INSERT INTO usage_records (trace_id, user_id, model, provider, input_tokens, output_tokens, total_cost, created_at)
		 VALUES (gen_random_uuid(), $1, 'claude-sonnet', 'anthropic', 5, 5, 500, NOW())`, userID)
	require.NoError(t, err)

	modelRows, err := GetUsageDistribution(ctx, database, userID, 7, "model")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(modelRows), 1)
	assert.Equal(t, "claude-sonnet", modelRows[0].Name)

	providerRows, err := GetUsageDistribution(ctx, database, userID, 7, "provider")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(providerRows), 1)
	assert.Equal(t, "anthropic", providerRows[0].Name)
}

// TestGetDashboardStats_Integration verifies high-level dashboard stats.
func TestGetDashboardStats_Integration(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	email := fmt.Sprintf("dashboard_stats_%d@test.local", time.Now().UnixNano())
	var userID string
	require.NoError(t, database.QueryRow(ctx,
		`INSERT INTO users (name, email, password_hash, role, status, email_verified)
		 VALUES ('Test User', $1, 'hash', 'user', 'active', true)
		 RETURNING id::text`, email).Scan(&userID))

	t.Cleanup(func() {
		_, _ = database.Pool.Exec(ctx, "DELETE FROM usage_records WHERE user_id = $1", userID)
		_, _ = database.Pool.Exec(ctx, "DELETE FROM users WHERE id = $1", userID)
	})

	_, err := database.Pool.Exec(ctx,
		`INSERT INTO usage_records (trace_id, user_id, model, provider, input_tokens, output_tokens, total_cost, created_at)
		 VALUES (gen_random_uuid(), $1, 'gpt-4', 'openai', 10, 10, 1000, NOW())`, userID)
	require.NoError(t, err)

	stats, err := GetDashboardStats(ctx, database, 7)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, stats.TotalUsers, 1)
	assert.GreaterOrEqual(t, stats.TotalRequests, 1)
	assert.GreaterOrEqual(t, stats.TotalTokens, int64(20))
	assert.GreaterOrEqual(t, stats.TotalCost, float64(1000))
	assert.GreaterOrEqual(t, stats.TodayRequests, 1)
}

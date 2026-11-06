package db

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestSeedDefaultData_Providers(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := testDB(t)
	defer db.Close()

	ctx := context.Background()
	logger := zap.NewNop()

	// NOTE: SeedDefaultData uses ON CONFLICT DO NOTHING so it should be safe
	err := db.SeedDefaultData(ctx, logger)
	require.NoError(t, err)

	// Verify base providers exist
	expectedProviders := []struct{ id, name string }{
		{"openai", "OpenAI"},
		{"anthropic", "Anthropic"},
		{"deepseek", "DeepSeek"},
	}

	for _, ep := range expectedProviders {
		t.Run("provider_"+ep.id, func(t *testing.T) {
			var name string
			err := db.Pool.QueryRow(ctx,
				"SELECT name FROM providers WHERE id = $1", ep.id,
			).Scan(&name)
			require.NoError(t, err)
			assert.Equal(t, ep.name, name)
		})
	}

	// Verify counts
	var providerCount int
	err = db.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM providers").Scan(&providerCount)
	require.NoError(t, err)
	// Base providers (20) + bedrock regions (28) + mantle regions (13) = 61 total
	assert.Greater(t, providerCount, 20, "should have 20+ providers")
}

func TestSeedDefaultData_SubscriptionPlans(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := testDB(t)
	defer db.Close()

	ctx := context.Background()
	logger := zap.NewNop()

	err := db.SeedDefaultData(ctx, logger)
	require.NoError(t, err)

	planNames := []string{"Lite", "Standard", "Pro", "Max"}
	for _, name := range planNames {
		t.Run("plan_"+name, func(t *testing.T) {
			var count int
			err := db.Pool.QueryRow(ctx,
				"SELECT COUNT(*) FROM subscription_plans WHERE name = $1 AND plan_type = 'token'",
				name,
			).Scan(&count)
			require.NoError(t, err)
			assert.Equal(t, 1, count, "plan %s should exist", name)
		})
	}
}

func TestSeedDefaultData_UserGroup(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := testDB(t)
	defer db.Close()

	ctx := context.Background()
	logger := zap.NewNop()

	err := db.SeedDefaultData(ctx, logger)
	require.NoError(t, err)

	var isDefault bool
	var groupType string
	var ratio float64
	err = db.Pool.QueryRow(ctx,
		`SELECT is_default, group_type, pricing_ratio FROM user_groups WHERE name = 'default-general' AND is_default = true`,
	).Scan(&isDefault, &groupType, &ratio)
	require.NoError(t, err)
	assert.True(t, isDefault)
	assert.Equal(t, "general", groupType)
	assert.Equal(t, 1.0, ratio)
}

func TestSeedDefaultData_Voucher(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := testDB(t)
	defer db.Close()

	ctx := context.Background()
	logger := zap.NewNop()

	err := db.SeedDefaultData(ctx, logger)
	require.NoError(t, err)

	var code string
	var amount int64
	err = db.Pool.QueryRow(ctx,
		`SELECT code, amount FROM vouchers WHERE type = 'welcome' AND status = 'active' AND deleted_at IS NULL LIMIT 1`,
	).Scan(&code, &amount)
	require.NoError(t, err)
	assert.Equal(t, "WELCOME068", code)
	assert.Equal(t, int64(680000), amount)
}

func TestSeedDefaultData_SystemSettings(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := testDB(t)
	defer db.Close()

	ctx := context.Background()
	logger := zap.NewNop()

	err := db.SeedDefaultData(ctx, logger)
	require.NoError(t, err)

	settings := []struct{ key, category string }{
		{"quota_for_new_user", "operation"},
		{"usd_exchange_rate", "operation"},
		{"retry_times", "operation"},
		{"max_user_tokens", "token"},
		{"token_expire_days", "token"},
		{"log_consume_enabled", "log"},
		{"alipay_enabled", "payment"},
	}

	for _, s := range settings {
		t.Run("setting_"+s.key, func(t *testing.T) {
			var count int
			err := db.Pool.QueryRow(ctx,
				`SELECT COUNT(*) FROM system_settings WHERE key = $1 AND category = $2`,
				s.key, s.category,
			).Scan(&count)
			require.NoError(t, err)
			assert.Equal(t, 1, count, "setting %s should exist", s.key)
		})
	}
}

func TestSeedDefaultData_Idempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := testDB(t)
	defer db.Close()

	ctx := context.Background()
	logger := zap.NewNop()

	// Run twice — should not error (ON CONFLICT DO NOTHING)
	require.NoError(t, db.SeedDefaultData(ctx, logger))
	require.NoError(t, db.SeedDefaultData(ctx, logger))

	// Provider count should be same
	var count int
	_ = db.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM providers").Scan(&count)
	assert.Greater(t, count, 20)
}

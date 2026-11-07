package auth

import (
	"context"
	"testing"
	"time"

	"github.com/mayiants/ai-proxy-go/internal/config"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func testBalanceCacheConfig() *config.Config {
	return &config.Config{
		Billing: config.BillingConfig{
			LocalBalanceCacheEnabled: true,
			LocalBalanceCacheTTL:     10 * time.Second,
		},
	}
}

func TestBalanceCache_MemorySetGetInvalidate(t *testing.T) {
	m := NewMiddleware(testBalanceCacheConfig(), zap.NewNop())

	m.setBalance("user-1", 1_230_000) // 1.23 USD in micro
	assert.Equal(t, int64(1_230_000), m.getBalance("user-1"))

	m.InvalidateBalance("user-1")
	assert.Equal(t, int64(-1), m.getBalance("user-1"))
}

func TestPreConsumeLocal_Memory(t *testing.T) {
	m := NewMiddleware(testBalanceCacheConfig(), zap.NewNop())

	// No cached balance: fail-open, not handled locally.
	remaining, ok, handled := m.PreConsumeLocal("user-2", 100_000)
	assert.Equal(t, int64(0), remaining)
	assert.True(t, ok)
	assert.False(t, handled)

	// Seed balance.
	m.setBalance("user-2", 500_000) // 0.5 USD

	remaining, ok, handled = m.PreConsumeLocal("user-2", 100_000)
	assert.True(t, ok)
	assert.True(t, handled)
	assert.Equal(t, int64(400_000), remaining)
	assert.Equal(t, int64(400_000), m.getBalance("user-2"))

	remaining, ok, handled = m.PreConsumeLocal("user-2", 500_000)
	assert.False(t, ok)
	assert.True(t, handled)
	assert.Equal(t, int64(400_000), remaining)
}

// TestPreConsumeLocal_MemoryRemainingZeroDistinguishable pins the fix for the
// double-charge bug: a post-deduction balance of exactly 0 must be reported as
// handled (not confused with a missing cache entry).
func TestPreConsumeLocal_MemoryRemainingZeroDistinguishable(t *testing.T) {
	m := NewMiddleware(testBalanceCacheConfig(), zap.NewNop())
	m.setBalance("user-zero", 500_000)

	remaining, ok, handled := m.PreConsumeLocal("user-zero", 500_000)
	assert.True(t, ok)
	assert.True(t, handled)
	assert.Equal(t, int64(0), remaining)
	assert.Equal(t, int64(0), m.getBalance("user-zero"))
}

func TestSettleLocal_MemoryRefundAndCharge(t *testing.T) {
	m := NewMiddleware(testBalanceCacheConfig(), zap.NewNop())
	m.setBalance("user-3", 1_000_000) // 1.0 USD

	// Pre-consume estimate of 0.8 USD.
	remaining, ok, _ := m.PreConsumeLocal("user-3", 800_000)
	require.True(t, ok)
	assert.Equal(t, int64(200_000), remaining)

	// Actual cost 0.5 USD -> refund 0.3 USD.
	m.SettleLocal("user-3", 800_000, 500_000)
	assert.Equal(t, int64(500_000), m.getBalance("user-3"))

	// Pre-consume estimate of 0.4 USD and actual cost 0.5 USD -> charge extra 0.1 USD.
	remaining, ok, _ = m.PreConsumeLocal("user-3", 400_000)
	require.True(t, ok)
	assert.Equal(t, int64(100_000), remaining)
	m.SettleLocal("user-3", 400_000, 500_000)
	assert.Equal(t, int64(0), m.getBalance("user-3"))
}

func TestChargeLocal_MemoryKeepsDeduction(t *testing.T) {
	m := NewMiddleware(testBalanceCacheConfig(), zap.NewNop())
	m.setBalance("user-4", 1_000_000)

	remaining, ok, _ := m.PreConsumeLocal("user-4", 600_000)
	require.True(t, ok)
	assert.Equal(t, int64(400_000), remaining)

	m.ChargeLocal("user-4", 600_000)
	assert.Equal(t, int64(400_000), m.getBalance("user-4"))
}

// redisClientForTest returns a Redis client if one is available on the local
// stack (proxy Redis on 6380, otherwise standard 6379). It skips the test if
// neither is reachable.
func redisClientForTest(t *testing.T) *redis.Client {
	t.Helper()
	addrs := []string{"127.0.0.1:6380", "127.0.0.1:6379"}
	for _, addr := range addrs {
		rdb := redis.NewClient(&redis.Options{Addr: addr})
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		err := rdb.Ping(ctx).Err()
		cancel()
		if err == nil {
			return rdb
		}
		rdb.Close()
	}
	t.Skip("No reachable Redis for balance cache test")
	return nil
}

func TestBalanceCache_RedisSharedAcrossInstances(t *testing.T) {
	rdb := redisClientForTest(t)
	defer rdb.Close()

	cfg := testBalanceCacheConfig()
	m1 := NewMiddleware(cfg, zap.NewNop())
	m1.SetRedisClient(rdb)
	m2 := NewMiddleware(cfg, zap.NewNop())
	m2.SetRedisClient(rdb)

	userID := "shared-user"
	_ = rdb.Del(context.Background(), balanceRedisKey(userID)).Err()
	defer rdb.Del(context.Background(), balanceRedisKey(userID)).Err()

	m1.setBalance(userID, 2_000_000) // 2.0 USD
	// m2 should see the Redis-backed value.
	assert.Equal(t, int64(2_000_000), m2.getBalance(userID))

	remaining, ok, handled := m2.PreConsumeLocal(userID, 500_000)
	require.True(t, ok)
	require.True(t, handled)
	assert.Equal(t, int64(1_500_000), remaining)

	// m1 should observe the deduction.
	assert.Equal(t, int64(1_500_000), m1.getBalance(userID))

	m1.SettleLocal(userID, 500_000, 200_000)
	// delta = preConsumed - actual = +300_000 refund on the 1_500_000 remainder.
	assert.Equal(t, int64(1_800_000), m2.getBalance(userID))
}

func TestPreConsumeLocal_RedisInsufficientBalance(t *testing.T) {
	rdb := redisClientForTest(t)
	defer rdb.Close()

	cfg := testBalanceCacheConfig()
	m := NewMiddleware(cfg, zap.NewNop())
	m.SetRedisClient(rdb)

	userID := "poor-user"
	_ = rdb.Del(context.Background(), balanceRedisKey(userID)).Err()
	defer rdb.Del(context.Background(), balanceRedisKey(userID)).Err()

	m.setBalance(userID, 100_000) // 0.1 USD
	remaining, ok, handled := m.PreConsumeLocal(userID, 500_000)
	assert.False(t, ok)
	assert.True(t, handled)
	assert.Equal(t, int64(100_000), remaining)

	// Balance should be unchanged after a rejected pre-consume.
	assert.Equal(t, int64(100_000), m.getBalance(userID))
}

// TestPreConsumeLocal_RedisMissingKeyFailOpen pins the fix for the wrongful-402
// bug: a missing Redis key must be reported as not-handled (fail-open to the
// portal path) and must not pollute the in-memory balance with 0.
func TestPreConsumeLocal_RedisMissingKeyFailOpen(t *testing.T) {
	rdb := redisClientForTest(t)
	defer rdb.Close()

	cfg := testBalanceCacheConfig()
	m := NewMiddleware(cfg, zap.NewNop())
	m.SetRedisClient(rdb)

	userID := "ghost-user"
	_ = rdb.Del(context.Background(), balanceRedisKey(userID)).Err()

	remaining, ok, handled := m.PreConsumeLocal(userID, 500_000)
	assert.True(t, ok)
	assert.False(t, handled)
	assert.Equal(t, int64(0), remaining)

	// The in-memory cache must not have been polluted with a zero balance.
	assert.Equal(t, int64(-1), m.getBalance(userID))
}

// TestSettleLocal_RedisNoPhantomBalance pins the fix for the phantom-balance
// bug: settling on an expired/missing Redis key must not create a new key
// holding only the delta.
func TestSettleLocal_RedisNoPhantomBalance(t *testing.T) {
	rdb := redisClientForTest(t)
	defer rdb.Close()

	cfg := testBalanceCacheConfig()
	m := NewMiddleware(cfg, zap.NewNop())
	m.SetRedisClient(rdb)

	userID := "phantom-user"
	_ = rdb.Del(context.Background(), balanceRedisKey(userID)).Err()
	defer rdb.Del(context.Background(), balanceRedisKey(userID)).Err()

	// Refund delta on a non-existent key: must not create the key.
	m.SettleLocal(userID, 300_000, 0)
	exists, err := rdb.Exists(context.Background(), balanceRedisKey(userID)).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), exists)
	assert.Equal(t, int64(-1), m.getBalance(userID))
}

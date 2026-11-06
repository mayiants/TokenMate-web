package services

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
	"go.uber.org/zap"
)

func rewardTestDB(t *testing.T) *db.DB {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), testutil.PostgresDSN())
	if err != nil {
		t.Fatalf("connect to test db: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("ping test db: %v", err)
	}
	return &db.DB{Pool: pool, Redis: nil}
}

func TestNewRewardService(t *testing.T) {
	d := rewardTestDB(t)
	defer d.Close()

	svc := NewRewardService(d, zap.NewNop(), nil)
	require.NotNil(t, svc)
	require.NotNil(t, svc.db)
	require.NotNil(t, svc.logger)
}

func createTestUser(t *testing.T, d *db.DB, userID, email string) {
	t.Helper()
	ctx := context.Background()
	if err := d.Exec(ctx, `INSERT INTO users (id, name, email, password_hash, role, status, email_verified, gift_balance, balance)
			 VALUES ($1::uuid, 'Test User', $2, 'hash', 'user', 'active', true, 0, 1000000)
			 ON CONFLICT (id) DO UPDATE SET gift_balance = 0, balance = 1000000`, userID, email); err != nil {
		t.Fatalf("database operation failed: %v", err)
	}
}

func ensureWelcomeVoucher(t *testing.T, d *db.DB) {
	t.Helper()
	ctx := context.Background()
	if err := d.Exec(ctx, `INSERT INTO vouchers (code, name, type, owner_type, amount, status, max_uses, reward_type, description)
			 VALUES ('WELCOME068', 'Welcome Test', 'welcome', 'system', 680000, 'active', 999999, 'gift_balance', 'Test welcome voucher')
			 ON CONFLICT (code) DO NOTHING`); err != nil {
		t.Fatalf("database operation failed: %v", err)
	}
	if err := d.Exec(ctx, `UPDATE vouchers SET status = 'active', deleted_at = NULL, amount = 680000 WHERE code = 'WELCOME068'`); err != nil {
		t.Fatalf("database operation failed: %v", err)
	}
}

func ensureReferralVoucher(t *testing.T, d *db.DB, code, inviterID string, amount int64, rewardJSON string) {
	t.Helper()
	ctx := context.Background()
	if err := d.Exec(ctx, `INSERT INTO vouchers (code, name, type, owner_type, amount, reward_config, max_uses, used_count, status, user_id)
			 VALUES ($1, 'Test Referral', 'referral', 'user', $2, $3, 100, 0, 'active', $4::uuid)
			 ON CONFLICT (code) DO UPDATE SET user_id = $4::uuid, status = 'active', used_count = 0, amount = $2, reward_config = $3`, code, amount, rewardJSON, inviterID); err != nil {
		t.Fatalf("database operation failed: %v", err)
	}
}

func TestApplyWelcomeBonus(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	d := rewardTestDB(t)
	defer d.Close()

	ctx := context.Background()
	ensureWelcomeVoucher(t, d)

	userID := fmt.Sprintf("a0000000-0000-0000-0000-%012d", time.Now().UnixNano()%1000000000000)
	email := fmt.Sprintf("welcome_%d@test.local", time.Now().UnixNano())
	createTestUser(t, d, userID, email)

	defer func() {
		if err := d.Exec(ctx, "DELETE FROM voucher_uses WHERE user_id = $1", userID); err != nil {
			t.Fatalf("database operation failed: %v", err)
		}
		if err := d.Exec(ctx, "DELETE FROM users WHERE id = $1", userID); err != nil {
			t.Fatalf("database operation failed: %v", err)
		}
	}()

	svc := NewRewardService(d, zap.NewNop(), nil)
	svc.ApplyWelcomeBonus(ctx, userID, email)

	// Verify gift_balance was updated
	var giftBalance int64
	err := d.Pool.QueryRow(ctx,
		"SELECT COALESCE(gift_balance, 0) FROM users WHERE id = $1", userID,
	).Scan(&giftBalance)
	require.NoError(t, err)
	// Should have received 680000 (the welcome voucher amount)
	assert.Greater(t, giftBalance, int64(0), "gift_balance should be increased")

	// Verify voucher_uses recorded
	var useCount int
	err = d.Pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM voucher_uses WHERE user_id = $1", userID,
	).Scan(&useCount)
	require.NoError(t, err)
	assert.Equal(t, 1, useCount)
}

func TestApplyWelcomeBonus_NoVoucher(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	d := rewardTestDB(t)
	defer d.Close()

	ctx := context.Background()

	// Delete welcome voucher
	if err := d.Exec(ctx, "UPDATE vouchers SET status = 'inactive' WHERE type = 'welcome'"); err != nil {
		t.Fatalf("database operation failed: %v", err)
	}
	defer ensureWelcomeVoucher(t, d)

	userID := fmt.Sprintf("a0000000-0000-0000-0000-%012d", time.Now().UnixNano()%1000000000000)
	email := fmt.Sprintf("nowelcome_%d@test.local", time.Now().UnixNano())
	createTestUser(t, d, userID, email)

	defer func() {
		if err := d.Exec(ctx, "DELETE FROM users WHERE id = $1", userID); err != nil {
			t.Fatalf("database operation failed: %v", err)
		}
	}()

	svc := NewRewardService(d, zap.NewNop(), nil)
	// Should not panic when no welcome voucher exists
	svc.ApplyWelcomeBonus(ctx, userID, email)

	var giftBalance int64
	_ = d.Pool.QueryRow(ctx,
		"SELECT COALESCE(gift_balance, 0) FROM users WHERE id = $1", userID,
	).Scan(&giftBalance)
	assert.Equal(t, int64(0), giftBalance, "balance should not change without voucher")
}

func TestProcessInvitationCode_EmptyCode(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	d := rewardTestDB(t)
	defer d.Close()

	ctx := context.Background()
	svc := NewRewardService(d, zap.NewNop(), nil)

	// Should not panic with empty code
	svc.ProcessInvitationCode(ctx, "user-123", "")
}

func TestProcessInvitationCode_InvalidCode(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	d := rewardTestDB(t)
	defer d.Close()

	ctx := context.Background()
	svc := NewRewardService(d, zap.NewNop(), nil)

	// Should not panic with invalid code
	svc.ProcessInvitationCode(ctx, "user-123", "INVALID-CODE-NONEXISTENT")
}

func TestProcessInvitationCode_FullFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	d := rewardTestDB(t)
	defer d.Close()

	ctx := context.Background()

	// Create inviter
	inviterID := fmt.Sprintf("a0000000-0000-0000-0000-%012d", time.Now().UnixNano()%1000000000000)
	createTestUser(t, d, inviterID, fmt.Sprintf("inviter_%d@test.local", time.Now().UnixNano()))

	// Create invitee
	inviteeID := fmt.Sprintf("a0000000-0000-0000-0000-%012d", (time.Now().UnixNano()+1)%1000000000000)
	createTestUser(t, d, inviteeID, fmt.Sprintf("invitee_%d@test.local", time.Now().UnixNano()))

	defer func() {
		if err := d.Exec(ctx, "DELETE FROM voucher_uses WHERE user_id IN ($1, $2)", inviteeID, inviterID); err != nil {
			t.Fatalf("database operation failed: %v", err)
		}
		if err := d.Exec(ctx, "DELETE FROM vouchers WHERE code = 'TEST-REF-CODE-001'"); err != nil {
			t.Fatalf("database operation failed: %v", err)
		}
		if err := d.Exec(ctx, "DELETE FROM users WHERE id IN ($1, $2)", inviteeID, inviterID); err != nil {
			t.Fatalf("database operation failed: %v", err)
		}
	}()

	// Create a referral voucher with reward_config
	rewardConfig := `{"inviter":{"amount":50000,"destination":"gift_balance"},"invitee":{"amount":30000,"destination":"gift_balance"}}`
	ensureReferralVoucher(t, d, "TEST-REF-CODE-001", inviterID, 50000, rewardConfig)

	svc := NewRewardService(d, zap.NewNop(), nil)
	svc.ProcessInvitationCode(ctx, inviteeID, "TEST-REF-CODE-001")

	// Verify invitee got reward
	var inviteeGift int64
	err := d.Pool.QueryRow(ctx,
		"SELECT COALESCE(gift_balance, 0) FROM users WHERE id = $1", inviteeID,
	).Scan(&inviteeGift)
	require.NoError(t, err)
	assert.Equal(t, int64(30000), inviteeGift, "invitee should receive reward")

	// Verify inviter got reward
	var inviterGift int64
	err = d.Pool.QueryRow(ctx,
		"SELECT COALESCE(gift_balance, 0) FROM users WHERE id = $1", inviterID,
	).Scan(&inviterGift)
	require.NoError(t, err)
	assert.Equal(t, int64(50000), inviterGift, "inviter should receive reward")

	// Verify inviter_id was set on invitee
	var setInviterID string
	err = d.Pool.QueryRow(ctx,
		"SELECT COALESCE(inviter_id::text, '') FROM users WHERE id = $1", inviteeID,
	).Scan(&setInviterID)
	require.NoError(t, err)
	assert.Equal(t, inviterID, setInviterID)
}

func TestProcessInvitationCode_SelfReferral(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	d := rewardTestDB(t)
	defer d.Close()

	ctx := context.Background()

	userID := fmt.Sprintf("a0000000-0000-0000-0000-%012d", time.Now().UnixNano()%1000000000000)
	createTestUser(t, d, userID, fmt.Sprintf("selfref_%d@test.local", time.Now().UnixNano()))

	defer func() {
		if err := d.Exec(ctx, "DELETE FROM vouchers WHERE code = 'TEST-SELF-REF'"); err != nil {
			t.Fatalf("database operation failed: %v", err)
		}
		if err := d.Exec(ctx, "DELETE FROM users WHERE id = $1", userID); err != nil {
			t.Fatalf("database operation failed: %v", err)
		}
	}()

	rewardConfig := `{"inviter":{"amount":50000,"destination":"gift_balance"},"invitee":{"amount":30000,"destination":"gift_balance"}}`
	ensureReferralVoucher(t, d, "TEST-SELF-REF", userID, 50000, rewardConfig)

	svc := NewRewardService(d, zap.NewNop(), nil)
	// User cannot use own referral code — should not panic
	svc.ProcessInvitationCode(ctx, userID, "TEST-SELF-REF")

	// Gift balance should NOT change
	var giftBalance int64
	_ = d.Pool.QueryRow(ctx,
		"SELECT COALESCE(gift_balance, 0) FROM users WHERE id = $1", userID,
	).Scan(&giftBalance)
	assert.Equal(t, int64(0), giftBalance, "self-referral should not grant reward")
}

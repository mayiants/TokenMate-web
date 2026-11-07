package routes

import (
	"context"
	"testing"
	"time"

	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// TestRenewal_PendingOrderGrantsQuotaAndExtendsExpiry is the regression guard
// for the renewal metadata gap. A manually-paid (or insufficient-balance
// auto-renew) renewal order only carries {plan_id, subscription_id, is_renewal}
// in its metadata — it does NOT carry quota / duration_days. When such an order
// is paid, activateSubscriptionTx must still grant the PLAN's quota and extend
// the expiry by the plan's duration. Before the fix it read quota=0/duration=0
// from metadata, adding zero quota and NULLing expires_at (the user paid but got
// nothing).
func TestRenewal_PendingOrderGrantsQuotaAndExtendsExpiry(t *testing.T) {
	_, pool := newSubscriptionTestRoutes(t)
	ctx := context.Background()
	database := &db.DB{Pool: pool}
	pr := &PaymentRoutes{db: database, logger: zap.NewNop()}

	userID := uuid.NewString()
	planID := uuid.NewString()
	subID := uuid.NewString()
	orderUUID := uuid.NewString()

	cleanup := func() {
		_, _ = pool.Exec(ctx, "DELETE FROM subscription_purchases WHERE user_id = $1::uuid", userID)
		_, _ = pool.Exec(ctx, "DELETE FROM orders WHERE id = $1::uuid", orderUUID)
		_, _ = pool.Exec(ctx, "DELETE FROM user_subscriptions WHERE id = $1::uuid", subID)
		_, _ = pool.Exec(ctx, "DELETE FROM subscription_plans WHERE id = $1::uuid", planID)
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1::uuid", userID)
	}
	cleanup()
	t.Cleanup(cleanup)

	mustExec := func(sql string, args ...interface{}) {
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("setup exec failed: %v\nSQL: %s", err, sql)
		}
	}
	mustExec(`INSERT INTO users (id, name, email, password_hash, role, balance, created_at, updated_at)
		 VALUES ($1::uuid, 'Renew User', $2, 'h', 'user', 0, NOW(), NOW())`, userID, "renew-"+userID[:8]+"@example.com")
	// Plan: 1,000,000 quota, 30-day duration.
	mustExec(`INSERT INTO subscription_plans (id, name, plan_type, price, quota, duration_days, status, created_at, updated_at)
		 VALUES ($1::uuid, 'Renew Plan', 'token', 1000, 1000000, 30, 'active', NOW(), NOW())`, planID)
	// Existing subscription with some remaining quota and an expiry ~5 days out.
	startExpiry := time.Now().AddDate(0, 0, 5)
	mustExec(`INSERT INTO user_subscriptions (id, user_id, plan_id, plan_name, plan_type,
		 quota_total, quota_used, quota_remaining, status, started_at, expires_at, created_at, updated_at)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, 'Renew Plan', 'token', 1000000, 400000, 600000, 'active', NOW(), $4, NOW(), NOW())`,
		subID, userID, planID, startExpiry)
	// Pending renewal order as created by renewSubscription / ProcessAutoRenewals:
	// metadata only has plan_id/subscription_id/is_renewal.
	mustExec(`INSERT INTO orders (id, user_id, order_id, amount, currency, exchange_rate, status, product_type, product_name, metadata)
		 VALUES ($1::uuid, $2::uuid, $3, 1000, 'USD', 7.2, 'completed', 'subscription_renewal', 'Renew', $4)`,
		orderUUID, userID, "REN-TESTORDER",
		map[string]interface{}{"plan_id": planID, "subscription_id": subID, "is_renewal": true})

	// Act: activate the renewal (as completeOrder does after payment).
	if err := database.WithTx(ctx, func(tx pgx.Tx) error {
		var metadata map[string]interface{}
		if err := tx.QueryRow(ctx, `SELECT metadata FROM orders WHERE id = $1::uuid`, orderUUID).Scan(&metadata); err != nil {
			return err
		}
		return pr.activateSubscriptionTx(ctx, tx, userID, orderUUID, metadata, true /*isRenewal*/)
	}); err != nil {
		t.Fatalf("activateSubscriptionTx: %v", err)
	}

	// Assert: quota grew by the plan's quota, and expiry extended by ~30 days.
	var quotaTotal, quotaRemaining int64
	var expiresAt *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT quota_total, quota_remaining, expires_at FROM user_subscriptions WHERE id = $1::uuid`, subID,
	).Scan(&quotaTotal, &quotaRemaining, &expiresAt); err != nil {
		t.Fatalf("read subscription: %v", err)
	}
	if quotaTotal != 2000000 {
		t.Errorf("quota_total = %d, want 2000000 (1,000,000 existing + 1,000,000 renewed)", quotaTotal)
	}
	if quotaRemaining != 1600000 {
		t.Errorf("quota_remaining = %d, want 1600000 (600,000 existing + 1,000,000 renewed)", quotaRemaining)
	}
	if expiresAt == nil {
		t.Fatalf("expires_at is NULL after renewal — the renewal wiped the expiry")
	}
	// Expect the old expiry extended by ~30 days (allow a small clock delta).
	wantMin := startExpiry.AddDate(0, 0, 29)
	wantMax := startExpiry.AddDate(0, 0, 31)
	if expiresAt.Before(wantMin) || expiresAt.After(wantMax) {
		t.Errorf("expires_at = %v, want ~%v (old expiry + 30 days)", expiresAt, startExpiry.AddDate(0, 0, 30))
	}
}

// TestRenewal_ExpiredSubscriptionRevivesFromNow guards the already-expired case
// (e.g. an auto-renew that only fired after the sub lapsed). The new period must
// be anchored at "now", not at the stale past expiry — otherwise renewal would
// leave the subscription still expired.
func TestRenewal_ExpiredSubscriptionRevivesFromNow(t *testing.T) {
	_, pool := newSubscriptionTestRoutes(t)
	ctx := context.Background()
	database := &db.DB{Pool: pool}
	pr := &PaymentRoutes{db: database, logger: zap.NewNop()}

	userID := uuid.NewString()
	planID := uuid.NewString()
	subID := uuid.NewString()
	orderUUID := uuid.NewString()

	cleanup := func() {
		_, _ = pool.Exec(ctx, "DELETE FROM subscription_purchases WHERE user_id = $1::uuid", userID)
		_, _ = pool.Exec(ctx, "DELETE FROM orders WHERE id = $1::uuid", orderUUID)
		_, _ = pool.Exec(ctx, "DELETE FROM user_subscriptions WHERE id = $1::uuid", subID)
		_, _ = pool.Exec(ctx, "DELETE FROM subscription_plans WHERE id = $1::uuid", planID)
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1::uuid", userID)
	}
	cleanup()
	t.Cleanup(cleanup)

	mustExec := func(sql string, args ...interface{}) {
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("setup exec failed: %v\nSQL: %s", err, sql)
		}
	}
	mustExec(`INSERT INTO users (id, name, email, password_hash, role, balance, created_at, updated_at)
		 VALUES ($1::uuid, 'Renew User', $2, 'h', 'user', 0, NOW(), NOW())`, userID, "renewexp-"+userID[:8]+"@example.com")
	mustExec(`INSERT INTO subscription_plans (id, name, plan_type, price, quota, duration_days, status, created_at, updated_at)
		 VALUES ($1::uuid, 'Renew Plan', 'token', 1000, 1000000, 30, 'active', NOW(), NOW())`, planID)
	// Subscription expired 10 days ago.
	pastExpiry := time.Now().AddDate(0, 0, -10)
	mustExec(`INSERT INTO user_subscriptions (id, user_id, plan_id, plan_name, plan_type,
		 quota_total, quota_used, quota_remaining, status, started_at, expires_at, created_at, updated_at)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, 'Renew Plan', 'token', 1000000, 1000000, 0, 'active', NOW(), $4, NOW(), NOW())`,
		subID, userID, planID, pastExpiry)
	mustExec(`INSERT INTO orders (id, user_id, order_id, amount, currency, exchange_rate, status, product_type, product_name, metadata)
		 VALUES ($1::uuid, $2::uuid, $3, 1000, 'USD', 7.2, 'completed', 'subscription_renewal', 'Renew', $4)`,
		orderUUID, userID, "REN-EXPORDER",
		map[string]interface{}{"plan_id": planID, "subscription_id": subID, "is_renewal": true})

	if err := database.WithTx(ctx, func(tx pgx.Tx) error {
		var metadata map[string]interface{}
		if err := tx.QueryRow(ctx, `SELECT metadata FROM orders WHERE id = $1::uuid`, orderUUID).Scan(&metadata); err != nil {
			return err
		}
		return pr.activateSubscriptionTx(ctx, tx, userID, orderUUID, metadata, true)
	}); err != nil {
		t.Fatalf("activateSubscriptionTx: %v", err)
	}

	var expiresAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT expires_at FROM user_subscriptions WHERE id = $1::uuid`, subID).Scan(&expiresAt); err != nil {
		t.Fatalf("read subscription: %v", err)
	}
	if expiresAt == nil {
		t.Fatalf("expires_at is NULL after renewing an expired subscription")
	}
	// Must be ~30 days from NOW (not 20 days ago).
	if expiresAt.Before(time.Now().AddDate(0, 0, 28)) {
		t.Errorf("expires_at = %v, want ~30 days from now (expired sub must revive from today)", expiresAt)
	}
}

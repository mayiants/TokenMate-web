package routes

import (
	"context"
	"testing"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// TestExpireLapsedSubscriptions marks active-but-past-expiry subscriptions as
// expired, and leaves still-valid / already-cancelled ones untouched.
func TestExpireLapsedSubscriptions(t *testing.T) {
	_, pool := newSubscriptionTestRoutes(t)
	ctx := context.Background()
	database := &db.DB{Pool: pool}

	userID := uuid.NewString()
	planID := uuid.NewString()
	lapsedID := uuid.NewString() // active + expired -> should become 'expired'
	validID := uuid.NewString()  // active + future   -> stays 'active'
	noExpID := uuid.NewString()  // active + NULL exp  -> stays 'active'
	cancelID := uuid.NewString() // cancelled + expired -> stays 'cancelled'

	cleanup := func() {
		_, _ = pool.Exec(ctx, "DELETE FROM user_subscriptions WHERE user_id = $1::uuid", userID)
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
	mustExec(`INSERT INTO users (id, name, email, password_hash, role, created_at, updated_at)
		 VALUES ($1::uuid, 'Exp User', $2, 'h', 'user', NOW(), NOW())`, userID, "exp-"+userID[:8]+"@example.com")
	mustExec(`INSERT INTO subscription_plans (id, name, plan_type, price, quota, duration_days, status, created_at, updated_at)
		 VALUES ($1::uuid, 'Exp Plan', 'token', 1000, 1000000, 30, 'active', NOW(), NOW())`, planID)

	ins := func(id, status string, expires interface{}) {
		mustExec(`INSERT INTO user_subscriptions (id, user_id, plan_id, plan_name, plan_type,
			 quota_total, quota_used, quota_remaining, status, started_at, expires_at, created_at, updated_at)
			 VALUES ($1::uuid, $2::uuid, $3::uuid, 'Exp Plan', 'token', 1000000, 0, 1000000, $4, NOW(), $5, NOW(), NOW())`,
			id, userID, planID, status, expires)
	}
	ins(lapsedID, "active", time.Now().AddDate(0, 0, -3))
	ins(validID, "active", time.Now().AddDate(0, 0, 10))
	ins(noExpID, "active", nil)
	ins(cancelID, "cancelled", time.Now().AddDate(0, 0, -3))

	n, err := ExpireLapsedSubscriptions(ctx, database, zap.NewNop())
	if err != nil {
		t.Fatalf("ExpireLapsedSubscriptions: %v", err)
	}
	if n != 1 {
		t.Errorf("expired count = %d, want 1 (only the lapsed active one)", n)
	}

	statusOf := func(id string) string {
		var s string
		if err := pool.QueryRow(ctx, `SELECT status FROM user_subscriptions WHERE id = $1::uuid`, id).Scan(&s); err != nil {
			t.Fatalf("read status: %v", err)
		}
		return s
	}
	if got := statusOf(lapsedID); got != "expired" {
		t.Errorf("lapsed sub status = %q, want expired", got)
	}
	if got := statusOf(validID); got != "active" {
		t.Errorf("valid sub status = %q, want active (future expiry untouched)", got)
	}
	if got := statusOf(noExpID); got != "active" {
		t.Errorf("no-expiry sub status = %q, want active (NULL expiry untouched)", got)
	}
	if got := statusOf(cancelID); got != "cancelled" {
		t.Errorf("cancelled sub status = %q, want cancelled (untouched)", got)
	}
}

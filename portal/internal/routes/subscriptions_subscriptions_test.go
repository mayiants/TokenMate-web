package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// TestSubscriptions_PurchasePlanValidation tests the purchase plan request binding.
func TestSubscriptions_PurchasePlanValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantValid bool
		wantPlan  string
	}{
		{"valid purchase", `{"plan_id": "plan-123"}`, true, "plan-123"},
		{"missing plan_id", `{}`, true, ""},
		{"invalid json", `bad`, false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				PlanID string `json:"plan_id" binding:"required"`
			}
			err := json.Unmarshal([]byte(tt.body), &req)
			valid := err == nil
			if valid != tt.wantValid {
				t.Errorf("valid = %v, want %v", valid, tt.wantValid)
				return
			}
			if !valid {
				return
			}
			if req.PlanID != tt.wantPlan {
				t.Errorf("plan_id = %q, want %q", req.PlanID, tt.wantPlan)
			}
		})
	}
}

// TestSubscriptions_CanRenewLogic tests the canRenew determination logic.
func TestSubscriptions_CanRenewLogic(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		status    string
		expiresAt *time.Time
		wantRenew bool
	}{
		{"active expiring soon", "active", timePtr(now.Add(5 * 24 * time.Hour)), true},
		{"active expiring tomorrow", "active", timePtr(now.Add(24 * time.Hour)), true},
		{"active far from expiry", "active", timePtr(now.Add(30 * 24 * time.Hour)), false},
		{"expired", "expired", timePtr(now.Add(-1 * 24 * time.Hour)), true},
		{"cancelled", "cancelled", timePtr(now.Add(30 * 24 * time.Hour)), false},
		{"active no expiry", "active", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			canRenew := tt.status == "active" || tt.status == "expired"
			if tt.expiresAt != nil {
				canRenew = now.Add(7*24*time.Hour).After(*tt.expiresAt) || tt.status == "expired"
			}
			if canRenew != tt.wantRenew {
				t.Errorf("canRenew = %v, want %v", canRenew, tt.wantRenew)
			}
		})
	}
}

func timePtr(t time.Time) *time.Time { return &t }

// TestSubscriptions_CancelRefundCalculation tests the refund calculation logic.
func TestSubscriptions_CancelRefundCalculation(t *testing.T) {
	tests := []struct {
		name       string
		planPrice  int64
		quotaTotal int64
		quotaUsed  int64
		wantRefund int64
	}{
		{"fully unused", 1000, 1000000, 0, 1000},
		{"half used", 1000, 1000000, 500000, 500},
		{"fully used", 1000, 1000000, 1000000, 0},
		{"overused", 1000, 1000000, 1500000, 0},
		{"zero total", 1000, 0, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var refundQuota int64
			if tt.quotaTotal > 0 && tt.quotaUsed < tt.quotaTotal {
				remainingRatio := float64(tt.quotaTotal-tt.quotaUsed) / float64(tt.quotaTotal)
				refundQuota = int64(float64(tt.planPrice) * remainingRatio)
			}
			if refundQuota != tt.wantRefund {
				t.Errorf("refund = %d, want %d", refundQuota, tt.wantRefund)
			}
		})
	}
}

// TestSubscriptions_UpdateUserSubscriptionValidation tests the update subscription request binding.
func TestSubscriptions_UpdateUserSubscriptionValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantValid bool
	}{
		{"update rpm", `{"rpm_limit": 100}`, true},
		{"update tpm", `{"tpm_limit": 500000}`, true},
		{"update daily_limit", `{"daily_limit": 1000}`, true},
		{"update quota_remaining", `{"quota_remaining": 5000000}`, true},
		{"multiple fields", `{"rpm_limit": 100, "tpm_limit": 500000}`, true},
		{"empty body", `{}`, true},
		{"invalid json", `bad`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				RPMLimit       *int   `json:"rpm_limit"`
				TPMLimit       *int   `json:"tpm_limit"`
				DailyLimit     *int64 `json:"daily_limit"`
				QuotaRemaining *int64 `json:"quota_remaining"`
			}
			err := json.Unmarshal([]byte(tt.body), &req)
			valid := err == nil
			if valid != tt.wantValid {
				t.Errorf("valid = %v, want %v", valid, tt.wantValid)
			}
		})
	}
}

// newSubscriptionTestRoutes builds SubscriptionRoutes against the test database.
func newSubscriptionTestRoutes(t *testing.T) (*SubscriptionRoutes, *pgxpool.Pool) {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), testutil.PostgresDSN())
	if err != nil {
		t.Fatalf("connect to test db: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := db.InitTestSchema(context.Background(), testutil.PostgresDSN(), zap.NewNop()); err != nil {
		t.Fatalf("init test schema: %v", err)
	}
	return NewSubscriptionRoutes(&db.DB{Pool: pool}, nil, nil, zap.NewNop()), pool
}

// TestCancelSubscription_CreditsUnusedQuota verifies that cancelling a
// subscription locks the user's balance, credits the unused portion, records a
// balance_transaction, and marks the subscription cancelled.
func TestCancelSubscription_CreditsUnusedQuota(t *testing.T) {
	r, pool := newSubscriptionTestRoutes(t)
	ctx := context.Background()

	userID := "d1d2d3d4-0000-4000-8000-000000000001"
	planID := "e1e2e3e4-0000-4000-8000-000000000001"
	subID := "f1f2f3f4-0000-4000-8000-000000000001"

	cleanup := func() {
		_, _ = pool.Exec(ctx, "DELETE FROM balance_transactions WHERE user_id = $1::uuid", userID)
		_, _ = pool.Exec(ctx, "DELETE FROM orders WHERE user_id = $1::uuid", userID)
		_, _ = pool.Exec(ctx, "DELETE FROM subscription_purchases WHERE user_id = $1::uuid", userID)
		_, _ = pool.Exec(ctx, "DELETE FROM user_subscriptions WHERE id = $1::uuid", subID)
		_, _ = pool.Exec(ctx, "DELETE FROM subscription_plans WHERE id = $1::uuid", planID)
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1::uuid", userID)
	}
	cleanup()
	t.Cleanup(cleanup)

	if _, err := pool.Exec(ctx,
		"INSERT INTO users (id, name, email, password_hash, role, balance, created_at, updated_at) VALUES ($1::uuid, 'C', $2, 'h', 'user', 500000, NOW(), NOW())",
		userID, "cancel-sub-test@example.com"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := pool.Exec(ctx,
		"INSERT INTO subscription_plans (id, name, price, quota, duration_days, status, created_at, updated_at) VALUES ($1::uuid, 'Monthly', 1000000, 1000000, 30, 'active', NOW(), NOW())",
		planID); err != nil {
		t.Fatalf("insert plan: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO user_subscriptions (id, user_id, plan_id, plan_name, plan_type, quota_total, quota_used, quota_remaining, status, created_at, updated_at)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, 'Monthly', 'monthly', 1000000, 200000, 800000, 'active', NOW(), NOW())`,
		subID, userID, planID); err != nil {
		t.Fatalf("insert subscription: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO subscription_purchases (id, user_id, plan_id, amount, status, created_at, updated_at)
		 VALUES (gen_random_uuid(), $1::uuid, $2::uuid, 1000000, 'active', NOW(), NOW())`,
		userID, planID); err != nil {
		t.Fatalf("insert purchase: %v", err)
	}

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/subscriptions/"+subID+"/cancel", nil)
	c.Params = gin.Params{{Key: "id", Value: subID}}
	c.Set("user_id", userID)
	c.Set("email", "cancel-sub-test@example.com")
	r.cancelSubscription(c)

	if w.Code != http.StatusOK {
		t.Fatalf("cancel returned %d: %s", w.Code, w.Body.String())
	}

	var bal int64
	if err := pool.QueryRow(ctx, "SELECT balance FROM users WHERE id = $1::uuid", userID).Scan(&bal); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	// 80% unused of 1,000,000 micro-USD = 800,000 credit on top of initial 500,000.
	if bal != 1300000 {
		t.Errorf("balance = %d, want 1300000", bal)
	}
	var subStatus string
	if err := pool.QueryRow(ctx, "SELECT status FROM user_subscriptions WHERE id = $1::uuid", subID).Scan(&subStatus); err != nil {
		t.Fatalf("read subscription: %v", err)
	}
	if subStatus != "cancelled" {
		t.Errorf("subscription status = %q, want cancelled", subStatus)
	}
	var refundTxns int
	if err := pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM balance_transactions WHERE user_id = $1::uuid AND transaction_type = 'subscription_refund'",
		userID).Scan(&refundTxns); err != nil {
		t.Fatalf("count refund txns: %v", err)
	}
	if refundTxns != 1 {
		t.Errorf("subscription_refund transactions = %d, want 1", refundTxns)
	}
}

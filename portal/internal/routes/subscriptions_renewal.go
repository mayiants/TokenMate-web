package routes

import (
	"context"
	"fmt"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/config"
	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ProcessAutoRenewals creates renewal orders / extends subscriptions for plans
// with auto_renew enabled that are within 7 days of expiry.
//
// DORMANT (not scheduled): the payment gateway is currently disabled, so
// auto-renewal cannot complete a charge. The background scheduler therefore
// does NOT invoke this job — a lapsed subscription simply expires (see
// ExpireLapsedSubscriptions) and the user must subscribe again. This function
// is intentionally retained so it can be re-enabled once the payment gateway is
// back; before re-wiring it, re-verify the renewal activation path
// (activateSubscriptionTx) still grants quota/extends expiry correctly.
func ProcessAutoRenewals(database *db.DB, cfg *config.Config, logger *zap.Logger) {
	ctx := context.Background()

	// Find subscriptions expiring in next 7 days with auto_renew enabled.
	// The duplicate-order guard (NOT EXISTS) is intentionally NOT applied
	// here; it is re-checked inside the per-subscription transaction below so
	// a concurrent run cannot insert a second renewal order between the check
	// and the insert.
	rows, err := database.Query(ctx,
		`SELECT us.id::text, us.user_id::text, us.plan_id::text, sp.name, sp.price,
		        sp.duration_days, us.expires_at, u.email, u.balance
		 FROM user_subscriptions us
	JOIN subscription_plans sp ON us.plan_id = sp.id AND sp.deleted_at IS NULL
	JOIN users u ON us.user_id = u.id
	WHERE us.status = 'active'
	  AND us.auto_renew = true
		   AND us.expires_at BETWEEN NOW() AND NOW() + INTERVAL '7 days'`)
	if err != nil {
		logger.Error("Failed to query expiring subscriptions", zap.Error(err))
		return
	}
	defer rows.Close()

	processed := 0
	for rows.Next() {
		var subID, userID, planID, planName string
		var price int64
		var durationDays int
		var expiresAt time.Time
		var email string
		var balance int64

		if err := rows.Scan(&subID, &userID, &planID, &planName, &price,
			&durationDays, &expiresAt, &email, &balance); err != nil {
			logger.Warn("Failed to scan subscription", zap.Error(err))
			continue
		}

		// Renew inside a transaction so the balance check, the duplicate-order
		// guard, and the renewal extension are atomic.
		tx, err := database.Pool.Begin(ctx)
		if err != nil {
			logger.Warn("Failed to begin transaction", zap.Error(err))
			continue
		}

		// Guard against a duplicate pending/paid renewal order for this
		// subscription. Checked inside the tx so a concurrent run cannot slip
		// a second order in between the check and the insert below.
		var hasPendingRenewal bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (
			   SELECT 1 FROM orders o
			   WHERE o.user_id = $1::uuid
			     AND o.product_type = 'subscription_renewal'
			     AND o.status IN ('pending', 'paid')
			     AND o.metadata->>'subscription_id' = $2
			 )`, userID, subID).Scan(&hasPendingRenewal); err != nil {
			_ = tx.Rollback(ctx)
			logger.Warn("Failed to check pending renewal", zap.Error(err), zap.String("sub_id", subID))
			continue
		}
		if hasPendingRenewal {
			_ = tx.Rollback(ctx)
			continue
		}

		// Atomically deduct the price only when the user still has enough
		// balance. RowsAffected == 0 means a concurrent spend drained the
		// balance between our read and this update, so the renewal fails and
		// we fall back to a pending order for manual payment. Using
		// `balance = balance - $1` (instead of GREATEST(balance - $1, 0))
		// together with `WHERE balance >= $1` guarantees we never clamp an
		// insufficient balance to 0 and hand out a free renewal.
		ct, err := tx.Exec(ctx,
			`UPDATE users SET balance = balance - $1, updated_at = NOW() WHERE id = $2::uuid AND balance >= $1`,
			price, userID)
		if err != nil {
			_ = tx.Rollback(ctx)
			logger.Warn("Failed to deduct balance", zap.Error(err), zap.String("user_id", userID))
			continue
		}
		if ct.RowsAffected() == 0 {
			_ = tx.Rollback(ctx)

			// Insufficient balance - create a pending order for manual payment.
			orderID := "REN-" + uuid.New().String()[:8]
			_, err := database.Pool.Exec(ctx,
				`INSERT INTO orders (user_id, order_id, amount, status, product_type, product_name, metadata)
				 VALUES ($1::uuid, $2, $3, 'pending', 'subscription_renewal', $4, $5)`,
				userID, orderID, price, fmt.Sprintf(routeI18n.T("info.renew_description", "zh"), planName),
				map[string]interface{}{
					"plan_id":         planID,
					"subscription_id": subID,
					"is_renewal":      true,
				})
			if err != nil {
				logger.Warn("Failed to create pending renewal order", zap.Error(err))
				continue
			}

			logger.Info("Created pending renewal order (insufficient balance)",
				zap.String("user_id", userID),
				zap.String("subscription_id", subID),
				zap.Int64("price", price),
				zap.Int64("balance", balance))
			processed++
			continue
		}

		// Extend the subscription by the plan's duration_days instead of a
		// hardcoded 30-day interval.
		_, err = tx.Exec(ctx,
			`UPDATE user_subscriptions
			 SET expires_at = expires_at + make_interval(days => $1::int),
			     quota_remaining = quota_remaining + quota_total,
			     updated_at = NOW()
			 WHERE id = $2::uuid`,
			durationDays, subID)
		if err != nil {
			_ = tx.Rollback(ctx)
			logger.Warn("Failed to extend subscription", zap.Error(err), zap.String("sub_id", subID))
			continue
		}

		// Create renewal order record
		orderID := "REN-AUTO-" + uuid.New().String()[:8]
		_, err = tx.Exec(ctx,
			`INSERT INTO orders (user_id, order_id, amount, status, product_type, product_name, metadata)
			 VALUES ($1::uuid, $2, $3, 'paid', 'subscription_renewal', $4, $5)`,
			userID, orderID, price, fmt.Sprintf(routeI18n.T("info.auto_renew_description", "zh"), planName),
			map[string]interface{}{
				"plan_id":         planID,
				"subscription_id": subID,
				"is_renewal":      true,
				"auto_renewed":    true,
			})
		if err != nil {
			_ = tx.Rollback(ctx)
			logger.Warn("Failed to create renewal order", zap.Error(err))
			continue
		}

		if err := tx.Commit(ctx); err != nil {
			logger.Warn("Failed to commit renewal", zap.Error(err))
			continue
		}

		logger.Info("Auto-renewal completed",
			zap.String("user_id", userID),
			zap.String("subscription_id", subID),
			zap.Int64("price", price))
		processed++
	}

	if processed > 0 {
		logger.Info("Auto-renewal processing completed", zap.Int("processed", processed))
	}
}

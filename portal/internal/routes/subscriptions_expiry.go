package routes

import (
	"context"

	"github.com/ai-proxy/ai-portal-go/internal/db"
	"go.uber.org/zap"
)

// ExpireLapsedSubscriptions marks any still-'active' subscription whose
// expires_at has passed as 'expired'. This is the authoritative lifecycle
// transition: the payment platform is disabled and auto-renewal does not run,
// so a subscription that reaches its expiry simply lapses. Users who want to
// keep using the service must subscribe again (there is no silent auto-renew).
//
// The service layer already denies access to expired subscriptions via the
// `expires_at > NOW()` predicate in key validation; this sweeper keeps the
// stored status in sync so the UI, the `status='expired'` stats, and any
// status-based queries reflect reality.
//
// Rows with a NULL expires_at (non-expiring grants) and non-active rows
// (cancelled/expired) are left untouched. Returns the number of rows updated.
func ExpireLapsedSubscriptions(ctx context.Context, database *db.DB, logger *zap.Logger) (int64, error) {
	tag, err := database.Pool.Exec(ctx,
		`UPDATE user_subscriptions
		 SET status = 'expired', updated_at = NOW()
		 WHERE status = 'active'
		   AND expires_at IS NOT NULL
		   AND expires_at < NOW()`)
	if err != nil {
		logger.Error("Failed to expire lapsed subscriptions", zap.Error(err))
		return 0, err
	}
	n := tag.RowsAffected()
	if n > 0 {
		logger.Info("Expired lapsed subscriptions", zap.Int64("count", n))
	}
	return n, nil
}

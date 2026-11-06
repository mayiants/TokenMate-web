package routes

import (
	"context"
	"fmt"

	"github.com/ai-proxy/ai-portal-go/internal/db"
	"go.uber.org/zap"
)

// AutoCloseStaleTickets closes non-closed tickets whose last activity — the
// most recent reply, or creation when the ticket has no replies — is older
// than the given number of days. Closing mirrors the manual close semantics
// (status -> 'closed'); a later user reply still reopens the ticket through
// the normal flow. Returns the number of tickets closed.
func AutoCloseStaleTickets(ctx context.Context, database *db.DB, logger *zap.Logger, days int) (int, error) {
	if days <= 0 {
		days = 30
	}
	rows, err := database.Query(ctx,
		`UPDATE support_tickets
		    SET status = 'closed', updated_at = NOW()
		  WHERE deleted_at IS NULL
		    AND status != 'closed'
		    AND COALESCE(last_reply_at, created_at) < NOW() - ($1 * INTERVAL '1 day')
		  RETURNING id`,
		days)
	if err != nil {
		return 0, fmt.Errorf("auto-close stale tickets: %w", err)
	}
	defer rows.Close()

	closed := 0
	for rows.Next() {
		closed++
	}
	if closed > 0 && logger != nil {
		logger.Info("auto-closed stale support tickets",
			zap.Int("closed", closed),
			zap.Int("inactivity_days", days))
	}
	return closed, nil
}

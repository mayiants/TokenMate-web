package audit

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/pkg/auditchain"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestLoggerBatchInsertsFormAValidHashChain proves the COPY-based batch flush
// and the single-row sync fallback both fire the BEFORE INSERT chain trigger,
// even with several producers racing, and that the resulting chain verifies.
func TestLoggerBatchInsertsFormAValidHashChain(t *testing.T) {
	database := testAuditDB(t)
	defer database.Close()
	ctx := context.Background()

	var start time.Time
	require.NoError(t, database.Pool.QueryRow(ctx, `SELECT clock_timestamp()::timestamp - INTERVAL '1 second'`).Scan(&start))

	logger := NewLogger(database, zap.NewNop())
	marker := "chain-" + uuid.NewString()
	userID := uuid.NewString()

	const producers, perProducer = 10, 25
	var wg sync.WaitGroup
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for n := 0; n < perProducer; n++ {
				switch n % 3 {
				case 0:
					logger.Log(ctx, userID, "u@example.test", "test", marker, "info", fmt.Sprintf("p%d-%d", p, n))
				case 1:
					logger.LogWithDetails(ctx, userID, "u@example.test", "test", marker, "info", "details", map[string]any{"p": p, "n": n, "é": "☃"})
				default:
					logger.LogStructured(ctx, userID, "u@example.test", "test", marker, "info", "structured", "thing", "42",
						map[string]any{"old": n}, map[string]any{"new": n + 1})
				}
			}
		}(p)
	}
	wg.Wait()

	// One entry through the synchronous fallback path used when the buffer is full.
	require.NoError(t, logger.syncFlush(ctx, entry{UserID: userID, UserEmail: "u@example.test", Category: "test",
		Action: marker, Level: "info", Message: "sync fallback", IPAddress: "198.51.100.7"}))

	stopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	logger.Stop(stopCtx)

	var count int
	require.NoError(t, database.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM audit_logs WHERE action = $1`, marker).Scan(&count))
	assert.Equal(t, producers*perProducer+1, count, "no entry may be lost")

	var unchained int
	require.NoError(t, database.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_logs WHERE action = $1 AND (row_hash IS NULL OR chain_seq IS NULL)`, marker).Scan(&unchained))
	assert.Zero(t, unchained, "every logger insert must be chained by the trigger")

	res, err := database.VerifyAuditChain(ctx, db.VerifyOptions{Table: auditchain.TableAuditLogs, From: &start})
	require.NoError(t, err)
	assert.True(t, res.OK, res.Reason)
	assert.GreaterOrEqual(t, res.Checked, int64(count))
}

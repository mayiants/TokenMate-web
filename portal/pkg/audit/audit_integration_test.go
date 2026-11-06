package audit

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/testutil"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// TestMain ensures the test schema is initialized once for this package.
// If no test database is available, the integration tests are skipped.
func TestMain(m *testing.M) {
	testutil.LoadEnv()
	ctx := context.Background()
	if err := db.InitTestSchema(ctx, testutil.PostgresDSN(), zap.NewNop()); err != nil {
		if strings.Contains(err.Error(), "connection refused") || strings.Contains(err.Error(), "dial error") {
			fmt.Fprintf(os.Stderr, "skipping audit integration tests: no test database available\n")
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "init schema in audit TestMain: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

func testAuditDB(t *testing.T) *db.DB {
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

func TestAsyncBatchFlush(t *testing.T) {
	database := testAuditDB(t)
	defer database.Close()

	ctx := context.Background()
	observedZap, observed := observer.New(zap.InfoLevel)
	logger := NewLogger(database, zap.New(observedZap))
	defer logger.Stop(ctx)

	userID := uuid.New().String()
	action := fmt.Sprintf("batch_action_%s", uuid.New().String())
	for i := 0; i < 5; i++ {
		logger.Log(ctx, userID, fmt.Sprintf("test-%d@example.com", i), "test", action, "info", "batch message")
	}

	// Force flush by stopping the logger.
	stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	logger.Stop(stopCtx)

	for _, log := range observed.All() {
		fields := make([]string, 0, len(log.Context))
		for _, f := range log.Context {
			fields = append(fields, fmt.Sprintf("%s=%v", f.Key, f.Interface))
		}
		t.Logf("audit log: %s - %s (%s)", log.Level.String(), log.Message, strings.Join(fields, ", "))
	}

	var count int
	err := database.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_logs WHERE category = 'test' AND action = $1`,
		action,
	).Scan(&count)
	if err != nil {
		t.Fatalf("count audit logs: %v", err)
	}
	if count != 5 {
		t.Errorf("expected 5 audit logs, got %d", count)
	}
}

func TestStopDrainsBuffer(t *testing.T) {
	database := testAuditDB(t)
	defer database.Close()

	ctx := context.Background()
	observedZap, observed := observer.New(zap.InfoLevel)
	logger := NewLogger(database, zap.New(observedZap))

	userID := uuid.New().String()
	email := fmt.Sprintf("drain-%s@example.com", uuid.New().String())
	// Enqueue a single entry and stop immediately without waiting for the interval.
	logger.Log(ctx, userID, email, "test", "drain_action", "info", "drain message")

	stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	logger.Stop(stopCtx)

	for _, log := range observed.All() {
		fields := make([]string, 0, len(log.Context))
		for _, f := range log.Context {
			fields = append(fields, fmt.Sprintf("%s=%v", f.Key, f.Interface))
		}
		t.Logf("audit log: %s - %s (%s)", log.Level.String(), log.Message, strings.Join(fields, ", "))
	}

	var count int
	err := database.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_logs WHERE user_email = $1`,
		email,
	).Scan(&count)
	if err != nil {
		t.Fatalf("count audit logs: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 drained audit log, got %d", count)
	}
}

package query

import (
	"context"
	"testing"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/testutil"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestUpstreamReconciliationDetailNullKeyID verifies that detail rows with a
// NULL upstream_key_id (possible when usage_records was joined without an
// upstream key) do not cause a scan error.
func TestUpstreamReconciliationDetailNullKeyID(t *testing.T) {
	testutil.LoadEnv()
	dsn := testutil.PostgresDSN()
	if err := testutil.EnsureTestDatabase(dsn); err != nil {
		t.Skipf("test database unavailable: %v", err)
	}

	ctx := context.Background()
	if err := db.InitTestSchema(ctx, dsn, zap.NewNop()); err != nil {
		t.Fatalf("init test schema: %v", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()
	database := &db.DB{Pool: pool}

	// Insert a usage record with no upstream_key_id to exercise the FULL OUTER
	// JOIN path that produces a NULL key_id.
	userID := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO users (id, name, email, password_hash, role, status, group_id, preferred_currency)
		VALUES ($1::uuid, 'recon-test', $2, 'hash', 'user', 'active', NULL, 'USD')`,
		userID.String(), "recon-"+uuid.New().String()+"@example.com")
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO usage_records (
			trace_id, user_id, provider, model, request_count,
			input_tokens, output_tokens, total_cost, created_at
		) VALUES ($1::uuid, $2::uuid, 'openai', 'gpt-4', 1, 10, 20, 100, NOW())`,
		uuid.New().String(), userID.String())
	require.NoError(t, err)

	rng := ReconciliationRange{
		From: time.Now().UTC().Add(-24 * time.Hour),
		To:   time.Now().UTC(),
	}
	filters := ReconciliationFilters{Page: 1, Limit: 20}
	rows, total, err := UpstreamReconciliationDetail(ctx, database, nil, rng, filters)
	require.NoError(t, err, "detail query should not fail on NULL upstream_key_id")
	require.GreaterOrEqual(t, total, 1)
	require.GreaterOrEqual(t, len(rows), 1)
}

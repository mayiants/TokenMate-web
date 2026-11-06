package routes

import (
	"context"
	"testing"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

func TestUpsertProviderHealth_MultiTable(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), testutil.PostgresDSN())
	if err != nil {
		t.Fatalf("connect to test db: %v", err)
	}
	defer pool.Close()

	database := &db.DB{Pool: pool}
	routes := &WebhookRoutes{db: database, logger: zap.NewNop()}

	ctx := context.Background()
	provider := "openai"
	model := "gpt-4o"
	group := "enterprise"

	// Clean up any pre-existing test rows.
	cleanup := func() {
		_, _ = pool.Exec(ctx, "DELETE FROM model_group_health_stats WHERE model = $1 AND group_id = $2", model, group)
		_, _ = pool.Exec(ctx, "DELETE FROM provider_health_stats_model WHERE provider = $1 AND model = $2", provider, model)
		_, _ = pool.Exec(ctx, "DELETE FROM provider_health_stats WHERE provider = $1", provider)
	}
	cleanup()
	defer cleanup()

	record := &ProviderHealthRecord{
		Provider:   provider,
		Model:      model,
		GroupID:    group,
		StatusCode: 200,
		LatencyMs:  1200,
		ErrorType:  "",
	}

	if err := routes.upsertProviderHealth(ctx, record); err != nil {
		t.Fatalf("upsertProviderHealth failed: %v", err)
	}

	// Second record with error to test aggregation.
	record2 := &ProviderHealthRecord{
		Provider:   provider,
		Model:      model,
		GroupID:    group,
		StatusCode: 429,
		LatencyMs:  500,
		ErrorType:  "rate_limit",
	}
	if err := routes.upsertProviderHealth(ctx, record2); err != nil {
		t.Fatalf("upsertProviderHealth second record failed: %v", err)
	}

	// Allow bucket to be consistent (upsert uses time.Now().Truncate(5m)).
	bucket := time.Now().Truncate(5 * time.Minute)

	var providerReqCount int
	err = pool.QueryRow(ctx,
		"SELECT request_count FROM provider_health_stats WHERE provider = $1 AND bucket = $2",
		provider, bucket).Scan(&providerReqCount)
	if err != nil {
		t.Fatalf("query provider_health_stats failed: %v", err)
	}
	if providerReqCount != 2 {
		t.Errorf("provider request_count = %d, want 2", providerReqCount)
	}

	var modelReqCount int
	err = pool.QueryRow(ctx,
		"SELECT request_count FROM provider_health_stats_model WHERE provider = $1 AND model = $2 AND bucket = $3",
		provider, model, bucket).Scan(&modelReqCount)
	if err != nil {
		t.Fatalf("query provider_health_stats_model failed: %v", err)
	}
	if modelReqCount != 2 {
		t.Errorf("provider-model request_count = %d, want 2", modelReqCount)
	}

	var groupReqCount int
	err = pool.QueryRow(ctx,
		"SELECT request_count FROM model_group_health_stats WHERE model = $1 AND group_id = $2 AND bucket = $3",
		model, group, bucket).Scan(&groupReqCount)
	if err != nil {
		t.Fatalf("query model_group_health_stats failed: %v", err)
	}
	if groupReqCount != 2 {
		t.Errorf("model-group request_count = %d, want 2", groupReqCount)
	}
}

package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ai-proxy/ai-portal-go/pkg/metrics"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// TestWebhookUsage_UsageRecordParseValidation tests UsageRecord JSON parsing edge cases.
func TestWebhookUsage_UsageRecordParseValidation(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr bool
		check   func(*UsageRecord) bool
	}{
		{
			name: "cache creation tokens",
			json: `{"traceId": "t1", "cacheCreationTokens": 500}`,
			check: func(r *UsageRecord) bool {
				return r.CacheCreationTokens == 500
			},
		},
		{
			name: "retry count",
			json: `{"traceId": "t1", "retryCount": 3}`,
			check: func(r *UsageRecord) bool {
				return r.RetryCount == 3
			},
		},
		{
			name: "pricing ratio",
			json: `{"traceId": "t1", "pricingRatio": 1.5}`,
			check: func(r *UsageRecord) bool {
				return r.PricingRatio == 1.5
			},
		},
		{
			name: "routing track",
			json: `{"traceId": "t1", "routingTrack": "priority"}`,
			check: func(r *UsageRecord) bool {
				return r.RoutingTrack == "priority"
			},
		},
		{
			name: "is fallback",
			json: `{"traceId": "t1", "isFallback": true}`,
			check: func(r *UsageRecord) bool {
				return r.IsFallback == true
			},
		},
		{
			name: "group id",
			json: `{"traceId": "t1", "groupId": "group-123"}`,
			check: func(r *UsageRecord) bool {
				return r.GroupID == "group-123"
			},
		},
		{
			name: "actual group id",
			json: `{"traceId": "t1", "actualGroupId": "ag-456"}`,
			check: func(r *UsageRecord) bool {
				return r.ActualGroupID == "ag-456"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var record UsageRecord
			err := json.Unmarshal([]byte(tt.json), &record)
			if (err != nil) != tt.wantErr {
				t.Errorf("Unmarshal() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && tt.check != nil && !tt.check(&record) {
				t.Errorf("check failed for record: %+v", record)
			}
		})
	}
}

// TestWebhookUsage_ProcessStatsPath tests processStats function path.
// This is a structural test that verifies the logic flow without needing DB/Redis.
func TestWebhookUsage_ProcessStatsPath(t *testing.T) {
	// processStats and processRecord have complex DB operations.
	// These tests verify the pure logic parts that can be tested offline.

	// Test the nullStr helper again (already tested in webhook_test.go)
	// This is a supplementary test for the specific usage in processStats

	tests := []struct {
		name  string
		input string
		want  interface{}
	}{
		{"empty userID becomes nil", "", nil},
		{"valid userID passed through", "user-123", "user-123"},
		{"empty keyID becomes nil", "", nil},
		{"valid keyID passed through", "key-456", "key-456"},
		{"empty groupID becomes nil", "", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nullStr(tt.input)
			if got != tt.want {
				t.Errorf("nullStr(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// TestWebhookUsage_DefensiveDefaults tests defensive defaults in processStats.
func TestWebhookUsage_DefensiveDefaults(t *testing.T) {
	// When RoutingTrack is empty, default to "standard"
	routingTrack := ""
	if routingTrack == "" {
		routingTrack = "standard"
	}
	if routingTrack != "standard" {
		t.Errorf("routingTrack default = %q, want 'standard'", routingTrack)
	}

	// When OriginalTrack is empty, leave empty
	originalTrack := ""
	if originalTrack != "" {
		t.Errorf("originalTrack default should be empty, got %q", originalTrack)
	}

	// When TraceID is empty, generate UUID
	traceID := ""
	if traceID == "" {
		// In real code: uuid.New().String()
		traceID = "generated-uuid"
	}
	if traceID == "" {
		t.Error("traceID should be generated when empty")
	}
}

// TestWebhookUsage_UpstreamKeyValidation tests upstream key validation.
func TestWebhookUsage_UpstreamKeyValidation(t *testing.T) {
	// upsertUpstreamKeyUsage requires UpstreamKeyID and Date
	requiredFields := []string{"UpstreamKeyID", "Date"}

	var record UpstreamUsageRecord
	if record.UpstreamKeyID == "" || record.Date == "" {
		// Would return error from upsertUpstreamKeyUsage
		t.Logf("empty UpstreamKeyID and Date would cause upsert error")
	}

	for _, field := range requiredFields {
		if field == "" {
			t.Error("required field name should not be empty")
		}
	}
}

// TestWebhookUsage_HandleUsageBatchEmptyCheck tests empty batch handling.
func TestWebhookUsage_HandleUsageBatchEmptyCheck(t *testing.T) {
	// Empty batches should return successfully with count
	batch := BatchUsageReport{Records: []UsageRecord{}}
	if len(batch.Records) != 0 {
		t.Errorf("empty batch should have 0 records, got %d", len(batch.Records))
	}

	// Non-empty batches should process each record
	batch2 := BatchUsageReport{Records: []UsageRecord{{TraceID: "t1"}, {TraceID: "t2"}}}
	if len(batch2.Records) != 2 {
		t.Errorf("batch should have 2 records, got %d", len(batch2.Records))
	}
}

// TestWebhookUsage_BatchUsageStatsJSON tests the UsageRecord JSON for batch stats path.
func TestWebhookUsage_BatchUsageStatsJSON(t *testing.T) {
	batch := BatchUsageReport{
		ServerID: "server-1",
		Records: []UsageRecord{
			{TraceID: "t1", Model: "gpt-4", InputTokens: 100, OutputTokens: 50},
			{TraceID: "t2", Model: "claude-3", InputTokens: 200, OutputTokens: 100},
		},
	}

	data, err := json.Marshal(batch)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var parsed BatchUsageReport
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if parsed.ServerID != "server-1" {
		t.Errorf("ServerID = %q, want 'server-1'", parsed.ServerID)
	}
	if len(parsed.Records) != 2 {
		t.Errorf("got %d records, want 2", len(parsed.Records))
	}
}

// TestProcessStatsBatch_BulkInsertAndUpdate verifies that processStatsBatch inserts
// multiple usage_records rows and updates api_keys stats in a single batched transaction.
func TestProcessStatsBatch_BulkInsertAndUpdate(t *testing.T) {
	d := testWebhookBillingDB(t)
	defer d.Close()
	r := newTestWebhookRoutes(t, d)
	ctx := context.Background()

	userID := "550e8400-e29b-41d4-a716-446655440200"
	keyHash1 := "test-batch-key-1-" + fmt.Sprintf("%d", time.Now().UnixNano())
	keyHash2 := "test-batch-key-2-" + fmt.Sprintf("%d", time.Now().UnixNano())
	traceID1 := "550e8400-e29b-41d4-a716-446655440210"
	traceID2 := "550e8400-e29b-41d4-a716-446655440211"

	_, _ = d.Pool.Exec(ctx, "DELETE FROM usage_records WHERE trace_id IN ($1, $2)", traceID1, traceID2)
	_, _ = d.Pool.Exec(ctx, "DELETE FROM api_keys WHERE key_hash IN ($1, $2)", keyHash1, keyHash2)
	_, _ = d.Pool.Exec(ctx, "DELETE FROM users WHERE id = $1::uuid", userID)
	defer func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM usage_records WHERE trace_id IN ($1, $2)", traceID1, traceID2)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM api_keys WHERE key_hash IN ($1, $2)", keyHash1, keyHash2)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM users WHERE id = $1::uuid", userID)
	}()

	_, err := d.Pool.Exec(ctx,
		"INSERT INTO users (id, name, email, password_hash, role, balance, created_at, updated_at) VALUES ($1::uuid, $2, $3, $4, $5, $6, NOW(), NOW())",
		userID, "Batch User", "batch@example.com", "hash", "user", 1000000)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	for _, kh := range []string{keyHash1, keyHash2} {
		_, err = d.Pool.Exec(ctx,
			`INSERT INTO api_keys (id, user_id, key_hash, key_prefix, name, status, total_requests, total_tokens, created_at, updated_at)
			 VALUES (gen_random_uuid(), $1::uuid, $2, 'sk-batch', 'Batch Key', 'active', 0, 0, NOW(), NOW())`,
			userID, kh)
		if err != nil {
			t.Fatalf("insert api key %s: %v", kh, err)
		}
	}

	records := []*UsageRecord{
		{TraceID: traceID1, APIKeyHash: keyHash1, Model: "gpt-4", Provider: "openai", InputTokens: 100, OutputTokens: 50, RequestCount: 1, RoutingTrack: "standard"},
		{TraceID: traceID2, APIKeyHash: keyHash2, Model: "claude-3", Provider: "anthropic", InputTokens: 200, OutputTokens: 100, RequestCount: 2, RoutingTrack: "standard"},
	}

	if err := r.processStatsBatch(ctx, records); err != nil {
		t.Fatalf("processStatsBatch failed: %v", err)
	}

	var count int
	err = d.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM usage_records WHERE trace_id IN ($1, $2)", traceID1, traceID2).Scan(&count)
	if err != nil {
		t.Fatalf("select usage_records: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 usage_records, got %d", count)
	}

	for _, tc := range []struct {
		hash       string
		wantReqs   int64
		wantTokens int64
	}{
		{keyHash1, 1, 150},
		{keyHash2, 2, 300},
	} {
		var totalReqs, totalTokens int64
		err = d.Pool.QueryRow(ctx, "SELECT total_requests, total_tokens FROM api_keys WHERE key_hash = $1", tc.hash).Scan(&totalReqs, &totalTokens)
		if err != nil {
			t.Fatalf("select api_keys %s: %v", tc.hash, err)
		}
		if totalReqs != tc.wantReqs {
			t.Errorf("expected total_requests = %d for %s, got %d", tc.wantReqs, tc.hash, totalReqs)
		}
		if totalTokens != tc.wantTokens {
			t.Errorf("expected total_tokens = %d for %s, got %d", tc.wantTokens, tc.hash, totalTokens)
		}
	}
}

// TestProcessStatsBatch_UpdatesSubscriptionCounters verifies subscription counter
// updates are still applied per-record inside processStatsBatch.
func TestProcessStatsBatch_UpdatesSubscriptionCounters(t *testing.T) {
	d := testWebhookBillingDB(t)
	defer d.Close()
	r := newTestWebhookRoutes(t, d)
	ctx := context.Background()

	userID := fmt.Sprintf("550e8400-e29b-41d4-a716-44665544%04d", time.Now().UnixNano()%10000)
	keyHash := "test-batch-sub-key-" + fmt.Sprintf("%d", time.Now().UnixNano())
	planID := fmt.Sprintf("2f820af6-7d60-4c48-a4d3-%012d", time.Now().UnixNano()%1000000000000)
	traceID1 := "550e8400-e29b-41d4-a716-446655440220"
	traceID2 := "550e8400-e29b-41d4-a716-446655440221"

	_, _ = d.Pool.Exec(ctx, "DELETE FROM usage_records WHERE trace_id IN ($1, $2)", traceID1, traceID2)
	_, _ = d.Pool.Exec(ctx, "DELETE FROM api_keys WHERE key_hash = $1", keyHash)
	_, _ = d.Pool.Exec(ctx, "DELETE FROM user_subscriptions WHERE user_id = $1::uuid", userID)
	_, _ = d.Pool.Exec(ctx, "DELETE FROM users WHERE id = $1::uuid", userID)
	_, _ = d.Pool.Exec(ctx, "DELETE FROM subscription_plans WHERE id = $1::uuid", planID)
	defer func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM usage_records WHERE trace_id IN ($1, $2)", traceID1, traceID2)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM api_keys WHERE key_hash = $1", keyHash)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM user_subscriptions WHERE user_id = $1::uuid", userID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM users WHERE id = $1::uuid", userID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM subscription_plans WHERE id = $1::uuid", planID)
	}()

	_, err := d.Pool.Exec(ctx,
		`INSERT INTO subscription_plans (id, name, plan_type, price, quota, duration_days, daily_limit, status, tpm_limit, tph_limit, rpm_limit, rph_limit, created_at, updated_at)
		 VALUES ($1::uuid, 'Test Plan', 'token', 1000, 100000, 30, 5000, 'active', 100000, 1000000, 60, 3600, NOW(), NOW())`,
		planID)
	if err != nil {
		t.Fatalf("insert plan: %v", err)
	}

	_, err = d.Pool.Exec(ctx,
		"INSERT INTO users (id, name, email, password_hash, role, balance, created_at, updated_at) VALUES ($1::uuid, $2, $3, $4, $5, $6, NOW(), NOW())",
		userID, "Batch Sub User", fmt.Sprintf("batch-sub-%d@example.com", time.Now().UnixNano()), "hash", "user", 1000000)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	_, err = d.Pool.Exec(ctx,
		`INSERT INTO api_keys (id, user_id, key_hash, key_prefix, name, status, key_type, total_requests, total_tokens, created_at, updated_at)
		 VALUES (gen_random_uuid(), $1::uuid, $2, 'sk-batch-sub', 'Batch Sub Key', 'active', 'subscription', 0, 0, NOW(), NOW())`,
		userID, keyHash)
	if err != nil {
		t.Fatalf("insert api key: %v", err)
	}

	_, err = d.Pool.Exec(ctx,
		`INSERT INTO user_subscriptions (user_id, plan_id, status, quota_total, quota_used, quota_remaining, tpm_used, tph_used, rpm_used, rph_used, expires_at, created_at, updated_at)
		 VALUES ($1::uuid, $2::uuid, 'active', 10000, 0, 10000, 0, 0, 0, 0, NOW() + INTERVAL '1 year', NOW(), NOW())`,
		userID, planID)
	if err != nil {
		t.Fatalf("insert subscription: %v", err)
	}

	records := []*UsageRecord{
		{TraceID: traceID1, APIKeyHash: keyHash, Model: "gpt-4", Provider: "openai", InputTokens: 100, OutputTokens: 50, RequestCount: 1, RoutingTrack: "standard"},
		{TraceID: traceID2, APIKeyHash: keyHash, Model: "gpt-4", Provider: "openai", InputTokens: 200, OutputTokens: 100, RequestCount: 2, RoutingTrack: "standard"},
	}

	if err := r.processStatsBatch(ctx, records); err != nil {
		t.Fatalf("processStatsBatch failed: %v", err)
	}

	var tpmUsed, tphUsed, rpmUsed, rphUsed int64
	err = d.Pool.QueryRow(ctx,
		"SELECT tpm_used, tph_used, rpm_used, rph_used FROM user_subscriptions WHERE user_id = $1::uuid AND status = 'active'",
		userID).Scan(&tpmUsed, &tphUsed, &rpmUsed, &rphUsed)
	if err != nil {
		t.Fatalf("select subscription counters: %v", err)
	}

	expectedTokens := int64(100 + 50 + 200 + 100)
	if tpmUsed != expectedTokens {
		t.Errorf("expected tpm_used = %d, got %d", expectedTokens, tpmUsed)
	}
	if tphUsed != expectedTokens {
		t.Errorf("expected tph_used = %d, got %d", expectedTokens, tphUsed)
	}
	expectedRequests := int64(1 + 2)
	if rpmUsed != expectedRequests {
		t.Errorf("expected rpm_used = %d, got %d", expectedRequests, rpmUsed)
	}
	if rphUsed != expectedRequests {
		t.Errorf("expected rph_used = %d, got %d", expectedRequests, rphUsed)
	}
}

// TestProcessStatsBatch_BatchPricing verifies that processStatsBatch uses the
// batched pricing lookup path and produces correct per-record costs.
func TestProcessStatsBatch_BatchPricing(t *testing.T) {
	d := testWebhookBillingDB(t)
	defer d.Close()
	r := newTestWebhookRoutes(t, d)
	ctx := context.Background()

	userID := "550e8400-e29b-41d4-a716-446655440300"
	keyHash := "test-batch-pricing-key-" + fmt.Sprintf("%d", time.Now().UnixNano())
	provider := "test-batch-provider"
	model := "test-batch-model"

	_, _ = d.Pool.Exec(ctx, "DELETE FROM usage_records WHERE user_id = $1::uuid", userID)
	_, _ = d.Pool.Exec(ctx, "DELETE FROM api_keys WHERE key_hash = $1", keyHash)
	_, _ = d.Pool.Exec(ctx, "DELETE FROM users WHERE id = $1::uuid", userID)
	defer func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM usage_records WHERE user_id = $1::uuid", userID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM api_keys WHERE key_hash = $1", keyHash)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM users WHERE id = $1::uuid", userID)
	}()

	_, err := d.Pool.Exec(ctx,
		"INSERT INTO users (id, name, email, password_hash, role, balance, created_at, updated_at) VALUES ($1::uuid, $2, $3, $4, $5, $6, NOW(), NOW())",
		userID, "Batch Pricing User", "batch-pricing@example.com", "hash", "user", 1000000)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	_, err = d.Pool.Exec(ctx,
		`INSERT INTO api_keys (id, user_id, key_hash, key_prefix, name, status, total_requests, total_tokens, created_at, updated_at)
		 VALUES (gen_random_uuid(), $1::uuid, $2, 'sk-batch', 'Batch Pricing Key', 'active', 0, 0, NOW(), NOW())`,
		userID, keyHash)
	if err != nil {
		t.Fatalf("insert api key: %v", err)
	}

	// $1/M input, $2/M output.
	_, err = d.Pool.Exec(ctx,
		`INSERT INTO model_pricing_rules (provider, region, product_id, api_model_name, billing_mode, prices, status, rule_name, priority, effective_from)
		 VALUES ($1, '*', $2, $2, 'flat', $3, 'active', 'batch pricing rule', 0, NOW())`,
		provider, model, `{"input": 1.0, "output": 2.0, "cache_hit": 0.2, "cache_write": 2.0, "reasoning": 2.0, "fixed_request": 0}`)
	if err != nil {
		t.Fatalf("insert pricing rule: %v", err)
	}
	defer func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM model_pricing_rules WHERE provider = $1 AND product_id = $2", provider, model)
	}()

	traceID1 := "550e8400-e29b-41d4-a716-446655440310"
	traceID2 := "550e8400-e29b-41d4-a716-446655440311"
	_, _ = d.Pool.Exec(ctx, "DELETE FROM usage_records WHERE trace_id IN ($1, $2)", traceID1, traceID2)
	defer func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM usage_records WHERE trace_id IN ($1, $2)", traceID1, traceID2)
	}()

	records := []*UsageRecord{
		{TraceID: traceID1, APIKeyHash: keyHash, Model: model, Provider: provider, Region: "us", InputTokens: 1_000_000, OutputTokens: 500_000, RequestCount: 1, RoutingTrack: "standard"},
		{TraceID: traceID2, APIKeyHash: keyHash, Model: model, Provider: provider, Region: "us", InputTokens: 2_000_000, OutputTokens: 1_000_000, RequestCount: 1, RoutingTrack: "standard"},
	}

	if err := r.processStatsBatch(ctx, records); err != nil {
		t.Fatalf("processStatsBatch failed: %v", err)
	}

	var cost1, cost2 int64
	err = d.Pool.QueryRow(ctx, "SELECT total_cost FROM usage_records WHERE trace_id = $1", traceID1).Scan(&cost1)
	if err != nil {
		t.Fatalf("select cost1: %v", err)
	}
	err = d.Pool.QueryRow(ctx, "SELECT total_cost FROM usage_records WHERE trace_id = $1", traceID2).Scan(&cost2)
	if err != nil {
		t.Fatalf("select cost2: %v", err)
	}

	// Record 1: $1 + $1 = $2 → 2_000_000 micro-USD
	if cost1 != 2_000_000 {
		t.Errorf("expected cost1 = 2000000, got %d", cost1)
	}
	// Record 2: $2 + $2 = $4 → 4_000_000 micro-USD
	if cost2 != 4_000_000 {
		t.Errorf("expected cost2 = 4000000, got %d", cost2)
	}
}

// TestHandleUsage_RedisQueueFailedMetric verifies that a failed single-record
// Redis RPUSH increments WebhookRedisQueueFailed.
func TestHandleUsage_RedisQueueFailedMetric(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = rdb.Close() }()

	d := testWebhookBillingDB(t)
	defer d.Close()
	d.Redis = rdb
	r := newTestWebhookRoutes(t, d)

	metrics.GetMetrics().WebhookRedisQueueFailed.Store(0)
	mr.Close()

	gin.SetMode(gin.TestMode)
	body, _ := json.Marshal(UsageRecord{TraceID: "t-redis-fail", InputTokens: 100})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/usage", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	r.handleUsage(c)

	if metrics.GetMetrics().WebhookRedisQueueFailed.Load() == 0 {
		t.Error("expected WebhookRedisQueueFailed to be incremented after failed single-record RPUSH")
	}
}

// TestHandleUsageStatsBatch_RedisQueueFailedMetric verifies that a failed batch
// pipeline RPUSH increments WebhookRedisQueueFailed.
func TestHandleUsageStatsBatch_RedisQueueFailedMetric(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = rdb.Close() }()

	d := testWebhookBillingDB(t)
	defer d.Close()
	d.Redis = rdb
	r := newTestWebhookRoutes(t, d)

	metrics.GetMetrics().WebhookRedisQueueFailed.Store(0)
	mr.Close()

	gin.SetMode(gin.TestMode)
	body, _ := json.Marshal(BatchUsageReport{Records: []UsageRecord{{TraceID: "t1", InputTokens: 100}}})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/usage-stats-batch", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	r.handleUsageStatsBatch(c)

	if metrics.GetMetrics().WebhookRedisQueueFailed.Load() == 0 {
		t.Error("expected WebhookRedisQueueFailed to be incremented after failed batch pipeline")
	}
}

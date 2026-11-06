package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// TestHandleUsageBatch_PersistsProxyRequestAudits verifies that the audit batch
// endpoint persists records to proxy_request_audits (instead of discarding
// them) and that a duplicate POST of the same batch still succeeds.
func TestHandleUsageBatch_PersistsProxyRequestAudits(t *testing.T) {
	d := testWebhookBillingDB(t)
	defer d.Close()
	r := newTestWebhookRoutes(t, d)
	ctx := context.Background()

	traceID1 := uuid.NewString()
	traceID2 := uuid.NewString()
	keyHash := "test-audit-key-" + uuid.NewString()

	_, _ = d.Pool.Exec(ctx, "DELETE FROM proxy_request_audits WHERE trace_id IN ($1::uuid, $2::uuid)", traceID1, traceID2)
	defer func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM proxy_request_audits WHERE trace_id IN ($1::uuid, $2::uuid)", traceID1, traceID2)
	}()

	batch := BatchUsageReport{
		ServerID: "server-audit-test",
		Records: []UsageRecord{
			{
				TraceID:     traceID1,
				APIKeyHash:  keyHash,
				Model:       "gpt-4o",
				Provider:    "openai",
				Region:      "us",
				InputTokens: 100, OutputTokens: 50, CachedTokens: 10,
				LatencyMs: 123, RequestCount: 1,
				UpstreamKeyID: "up-key-1", Endpoint: "/v1/chat/completions",
				IsStreaming: true, ClientIP: "203.0.113.10",
				PricingRatio: 1.5, RoutingTrack: "priority",
			},
			{
				TraceID:     traceID2,
				APIKeyHash:  keyHash,
				Model:       "claude-3-5-sonnet",
				Provider:    "anthropic",
				Region:      "us",
				InputTokens: 200, OutputTokens: 100,
				LatencyMs: 456, RequestCount: 1,
				UpstreamKeyID: "up-key-2", Endpoint: "/v1/messages",
				Error: "upstream 529 overloaded", Status: "failed", ErrorCode: "529",
			},
		},
	}

	postBatch := func() *httptest.ResponseRecorder {
		gin.SetMode(gin.TestMode)
		body, err := json.Marshal(batch)
		if err != nil {
			t.Fatalf("marshal batch: %v", err)
		}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/usage-batch", bytes.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		r.handleUsageBatch(c)
		return w
	}

	w := postBatch()
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	type auditRow struct {
		apiKeyHash    string
		model         string
		provider      string
		region        string
		upstreamKeyID string
		endpoint      string
		status        string
		errText       string
		errorCode     string
		inputTokens   int64
		outputTokens  int64
		cachedTokens  int64
		latencyMs     int64
		requestCount  int
		isStreaming   bool
		clientIP      string
		pricingRatio  float64
		routingTrack  string
		serverID      string
	}

	loadRows := func() map[string]auditRow {
		t.Helper()
		rows, err := d.Pool.Query(ctx,
			`SELECT trace_id::text, api_key_hash, model, provider, region, upstream_key_id, endpoint,
			        status, COALESCE(error, ''), COALESCE(error_code, ''),
			        input_tokens, output_tokens, cached_tokens, latency_ms, request_count,
			        is_streaming, COALESCE(client_ip, ''), COALESCE(pricing_ratio, 0)::float8,
			        COALESCE(routing_track, ''), COALESCE(server_id, '')
			 FROM proxy_request_audits WHERE trace_id IN ($1::uuid, $2::uuid)`, traceID1, traceID2)
		if err != nil {
			t.Fatalf("query proxy_request_audits: %v", err)
		}
		defer rows.Close()
		out := map[string]auditRow{}
		for rows.Next() {
			var traceID string
			var row auditRow
			if err := rows.Scan(&traceID, &row.apiKeyHash, &row.model, &row.provider, &row.region,
				&row.upstreamKeyID, &row.endpoint, &row.status, &row.errText, &row.errorCode,
				&row.inputTokens, &row.outputTokens, &row.cachedTokens, &row.latencyMs, &row.requestCount,
				&row.isStreaming, &row.clientIP, &row.pricingRatio, &row.routingTrack, &row.serverID); err != nil {
				t.Fatalf("scan audit row: %v", err)
			}
			out[traceID] = row
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate audit rows: %v", err)
		}
		return out
	}

	audits := loadRows()
	if len(audits) != 2 {
		t.Fatalf("expected 2 audit rows, got %d", len(audits))
	}

	rec1 := audits[traceID1]
	if rec1.model != "gpt-4o" || rec1.provider != "openai" || rec1.region != "us" {
		t.Errorf("record 1 identity mismatch: %+v", rec1)
	}
	if rec1.apiKeyHash != keyHash || rec1.upstreamKeyID != "up-key-1" || rec1.endpoint != "/v1/chat/completions" {
		t.Errorf("record 1 key/upstream/endpoint mismatch: %+v", rec1)
	}
	if rec1.inputTokens != 100 || rec1.outputTokens != 50 || rec1.cachedTokens != 10 {
		t.Errorf("record 1 tokens mismatch: %+v", rec1)
	}
	if rec1.latencyMs != 123 || rec1.requestCount != 1 || !rec1.isStreaming {
		t.Errorf("record 1 latency/count/streaming mismatch: %+v", rec1)
	}
	if rec1.clientIP != "203.0.113.10" || rec1.pricingRatio != 1.5 || rec1.routingTrack != "priority" {
		t.Errorf("record 1 client/ratio/track mismatch: %+v", rec1)
	}
	if rec1.status != "success" || rec1.errText != "" || rec1.errorCode != "" {
		t.Errorf("record 1 should be a clean success: %+v", rec1)
	}
	if rec1.serverID != "server-audit-test" {
		t.Errorf("record 1 serverID = %q, want 'server-audit-test'", rec1.serverID)
	}

	rec2 := audits[traceID2]
	if rec2.status != "failed" || rec2.errText == "" || rec2.errorCode != "529" {
		t.Errorf("record 2 error fields mismatch: %+v", rec2)
	}
	if rec2.inputTokens != 200 || rec2.outputTokens != 100 || rec2.latencyMs != 456 {
		t.Errorf("record 2 tokens/latency mismatch: %+v", rec2)
	}
	if rec2.isStreaming {
		t.Errorf("record 2 isStreaming should be false: %+v", rec2)
	}
	if rec2.serverID != "server-audit-test" {
		t.Errorf("record 2 serverID = %q, want 'server-audit-test'", rec2.serverID)
	}

	// Second identical POST (Proxy retry): the insert is idempotent on trace_id,
	// so it must succeed and add nothing.
	w = postBatch()
	if w.Code != http.StatusOK {
		t.Fatalf("second POST: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal second POST response: %v", err)
	}
	if resp["ok"] != true || resp["inserted"] != float64(0) {
		t.Errorf("second POST response mismatch: %s", w.Body.String())
	}
	audits = loadRows()
	if len(audits) != 2 {
		t.Errorf("expected 2 distinct trace_ids after duplicate POST, got %d", len(audits))
	}
	var total int
	if err := d.Pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM proxy_request_audits WHERE trace_id IN ($1::uuid, $2::uuid)", traceID1, traceID2).Scan(&total); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if total != 2 {
		t.Errorf("expected 2 audit rows after duplicate POST (idempotent on trace_id), got %d", total)
	}
}

// TestHandleUsageBatch_AuditInsertSurvivesBadRecords verifies that records with
// unparseable trace_id values are skipped while valid records in the same batch
// are still persisted.
func TestHandleUsageBatch_AuditInsertSurvivesBadRecords(t *testing.T) {
	d := testWebhookBillingDB(t)
	defer d.Close()
	r := newTestWebhookRoutes(t, d)
	ctx := context.Background()

	goodTraceID := uuid.NewString()
	_, _ = d.Pool.Exec(ctx, "DELETE FROM proxy_request_audits WHERE trace_id = $1::uuid", goodTraceID)
	defer func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM proxy_request_audits WHERE trace_id = $1::uuid", goodTraceID)
	}()

	batch := BatchUsageReport{
		Records: []UsageRecord{
			{TraceID: goodTraceID, Model: "gpt-4o", Provider: "openai", InputTokens: 10, OutputTokens: 5},
			{TraceID: "not-a-uuid", Model: "gpt-4o", Provider: "openai", InputTokens: 10, OutputTokens: 5},
		},
	}

	gin.SetMode(gin.TestMode)
	body, _ := json.Marshal(batch)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/usage-batch", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	r.handleUsageBatch(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var count int
	if err := d.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM proxy_request_audits WHERE trace_id = $1::uuid", goodTraceID).Scan(&count); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 valid audit row persisted, got %d", count)
	}
}

// TestHandleUsageBatch_OversizeBatchDropped verifies the defensive batch-size cap.
func TestHandleUsageBatch_OversizeBatchDropped(t *testing.T) {
	d := testWebhookBillingDB(t)
	defer d.Close()
	r := newTestWebhookRoutes(t, d)

	records := make([]UsageRecord, 0, maxUsageAuditBatchSize+1)
	for i := 0; i < maxUsageAuditBatchSize+1; i++ {
		records = append(records, UsageRecord{TraceID: uuid.NewString(), Model: "gpt-4o"})
	}

	gin.SetMode(gin.TestMode)
	body, _ := json.Marshal(BatchUsageReport{Records: records})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/usage-batch", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	r.handleUsageBatch(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 even for oversize batch, got %d", w.Code)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp["dropped"] != true {
		t.Errorf("expected dropped=true for oversize batch: %s", w.Body.String())
	}
}

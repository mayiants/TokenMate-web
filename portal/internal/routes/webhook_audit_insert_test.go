package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mayiants/ai-portal-go/pkg/crypto"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type auditRow struct {
	userAgent, status, errorCode, clientIP *string
	httpStatus                             *int
	enterpriseID                           *string
	rows                                   int
}

func readAuditRow(t *testing.T, r *WebhookRoutes, traceID string) auditRow {
	t.Helper()
	var a auditRow
	if err := r.db.Pool.QueryRow(context.Background(),
		`SELECT user_agent, status, error_code, client_ip, http_status, enterprise_id::text,
		        (SELECT COUNT(*) FROM proxy_request_audits WHERE trace_id = $1::uuid)
		   FROM proxy_request_audits WHERE trace_id = $1::uuid ORDER BY id LIMIT 1`, traceID,
	).Scan(&a.userAgent, &a.status, &a.errorCode, &a.clientIP, &a.httpStatus, &a.enterpriseID, &a.rows); err != nil {
		t.Fatalf("read audit row %s: %v", traceID, err)
	}
	return a
}

func TestInsertProxyRequestAudits_NewColumnsAndEnterpriseResolution(t *testing.T) {
	d := testWebhookBillingDB(t)
	t.Cleanup(d.Close) // registered first => runs last, after the data cleanups below
	r := newTestWebhookRoutes(t, d)
	ctx := context.Background()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	entID := createApprovalTestEnterprise(t, d, "audit-"+suffix, "")
	userID := createGateTestUser(t, d, "gate-"+suffix+"-aud@test.com", 100, entID)
	rawKey := "sk-gt-aud-" + suffix
	createGateTestKey(t, d, userID, rawKey, "enterprise", entID)
	keyHash := crypto.HashKeySHA256(rawKey)

	okTrace, failTrace, unknownTrace := uuid.NewString(), uuid.NewString(), uuid.NewString()
	t.Cleanup(func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM proxy_request_audits WHERE trace_id = ANY($1::uuid[])`, []string{okTrace, failTrace, unknownTrace})
		cleanupGateTestData(t, d, suffix)
	})

	batch := &BatchUsageReport{ServerID: "srv-1", Records: []UsageRecord{
		{TraceID: okTrace, APIKeyHash: keyHash, Model: "m", Provider: "p", ClientIP: "203.0.113.9",
			UserAgent: "OpenAI/Python 1.2", StatusCode: 200, InputTokens: 10, OutputTokens: 5, RequestCount: 1},
		// Failed request: no error text and no status string, only the code.
		{TraceID: failTrace, APIKeyHash: keyHash, Model: "m", Provider: "p", StatusCode: 502},
		// Unknown key hash (e.g. key purged): must still be stored, enterprise NULL.
		{TraceID: unknownTrace, APIKeyHash: "no-such-hash-" + suffix, Model: "m", Provider: "p"},
	}}
	n, err := r.insertProxyRequestAudits(ctx, batch)
	if err != nil || n != 3 {
		t.Fatalf("insert = %d, %v; want 3, nil", n, err)
	}

	ok := readAuditRow(t, r, okTrace)
	if ok.userAgent == nil || *ok.userAgent != "OpenAI/Python 1.2" {
		t.Errorf("user_agent = %v", retDeref(ok.userAgent))
	}
	if ok.httpStatus == nil || *ok.httpStatus != 200 {
		t.Errorf("http_status = %v, want 200", ok.httpStatus)
	}
	if ok.clientIP == nil || *ok.clientIP != "203.0.113.9" {
		t.Errorf("client_ip = %v", retDeref(ok.clientIP))
	}
	if ok.enterpriseID == nil || *ok.enterpriseID != entID {
		t.Errorf("enterprise_id = %v, want %s (resolved from api_keys)", retDeref(ok.enterpriseID), entID)
	}
	if retDeref(ok.status) != "success" {
		t.Errorf("status = %v, want success", retDeref(ok.status))
	}

	fail := readAuditRow(t, r, failTrace)
	if retDeref(fail.status) != "error" || retDeref(fail.errorCode) != "502" {
		t.Errorf("failed request status/error_code = %v / %v, want error / 502", retDeref(fail.status), retDeref(fail.errorCode))
	}
	if fail.httpStatus == nil || *fail.httpStatus != 502 {
		t.Errorf("http_status = %v, want 502", fail.httpStatus)
	}

	unk := readAuditRow(t, r, unknownTrace)
	if unk.enterpriseID != nil {
		t.Errorf("enterprise_id = %v for an unknown key, want NULL", *unk.enterpriseID)
	}
	if unk.httpStatus != nil {
		t.Errorf("http_status = %v, want NULL when the Proxy sent none", *unk.httpStatus)
	}
}

func TestInsertProxyRequestAudits_IdempotentOnTraceID(t *testing.T) {
	d := testWebhookBillingDB(t)
	t.Cleanup(d.Close) // registered first => runs last, after the data cleanups below
	r := newTestWebhookRoutes(t, d)
	ctx := context.Background()

	trace := uuid.NewString()
	other := uuid.NewString()
	t.Cleanup(func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM proxy_request_audits WHERE trace_id = ANY($1::uuid[])`, []string{trace, other})
	})

	rec := func(id string) UsageRecord {
		return UsageRecord{TraceID: id, Model: "m", Provider: "p", APIKeyHash: "h"}
	}

	// Duplicates inside one batch (including a differently-cased copy).
	n, err := r.insertProxyRequestAudits(ctx, &BatchUsageReport{Records: []UsageRecord{
		rec(trace), rec(trace), rec(strings.ToUpper(trace)), rec(other)}})
	if err != nil || n != 2 {
		t.Fatalf("first insert = %d, %v; want 2 (in-batch duplicates dropped)", n, err)
	}
	// Replaying the same batch (Proxy retry after a timeout) inserts nothing.
	n, err = r.insertProxyRequestAudits(ctx, &BatchUsageReport{Records: []UsageRecord{rec(trace), rec(other)}})
	if err != nil || n != 0 {
		t.Fatalf("replay = %d, %v; want 0", n, err)
	}
	if a := readAuditRow(t, r, trace); a.rows != 1 {
		t.Errorf("rows for trace = %d, want 1", a.rows)
	}

	// Legacy data may already contain duplicates; the insert path must keep
	// working (this is why trace_id has a non-unique index).
	legacy := uuid.NewString()
	t.Cleanup(func() { _, _ = d.Pool.Exec(ctx, `DELETE FROM proxy_request_audits WHERE trace_id = $1::uuid`, legacy) })
	for i := 0; i < 2; i++ {
		if _, err := d.Pool.Exec(ctx, `INSERT INTO proxy_request_audits (trace_id, model) VALUES ($1::uuid, 'legacy')`, legacy); err != nil {
			t.Fatalf("legacy duplicate insert must be possible: %v", err)
		}
	}
	if n, err := r.insertProxyRequestAudits(ctx, &BatchUsageReport{Records: []UsageRecord{rec(legacy)}}); err != nil || n != 0 {
		t.Errorf("insert over legacy duplicates = %d, %v; want 0, nil", n, err)
	}

	// Records without a trace id get a generated one and are always stored;
	// an invalid trace id is skipped without failing the batch.
	n, err = r.insertProxyRequestAudits(ctx, &BatchUsageReport{Records: []UsageRecord{
		{Model: "gen-" + trace}, {TraceID: "not-a-uuid", Model: "bad"}}})
	if err != nil || n != 1 {
		t.Errorf("generated/invalid ids = %d, %v; want 1, nil", n, err)
	}
	_, _ = d.Pool.Exec(ctx, `DELETE FROM proxy_request_audits WHERE model = $1`, "gen-"+trace)
}

// TestInsertProxyRequestAudits_ChunksLargeBatches: a single statement with
// 20+ columns per row hits Postgres' 65535 bind-parameter limit above ~2900
// rows, while the handler accepts up to 5000.
func TestInsertProxyRequestAudits_ChunksLargeBatches(t *testing.T) {
	d := testWebhookBillingDB(t)
	t.Cleanup(d.Close) // registered first => runs last, after the data cleanups below
	r := newTestWebhookRoutes(t, d)
	ctx := context.Background()

	if proxyAuditColumnsPerRow*maxUsageAuditBatchSize <= 65535 {
		t.Skip("batch fits in one statement; chunking not exercised")
	}
	marker := "chunk-" + uuid.NewString()
	t.Cleanup(func() { _, _ = d.Pool.Exec(ctx, `DELETE FROM proxy_request_audits WHERE server_id = $1`, marker) })

	recs := make([]UsageRecord, maxUsageAuditBatchSize)
	for i := range recs {
		recs[i] = UsageRecord{TraceID: uuid.NewString(), Model: "m", ServerID: marker, UserAgent: "ua"}
	}
	n, err := r.insertProxyRequestAudits(ctx, &BatchUsageReport{Records: recs})
	if err != nil || n != maxUsageAuditBatchSize {
		t.Fatalf("insert = %d, %v; want %d, nil", n, err, maxUsageAuditBatchSize)
	}
}

// TestHandleUsageBatch_PersistsProxyFields drives the real HTTP handler with
// the JSON the Proxy sends (traceId, clientIp, userAgent, statusCode).
func TestHandleUsageBatch_PersistsProxyFields(t *testing.T) {
	d := testWebhookBillingDB(t)
	t.Cleanup(d.Close) // registered first => runs last, after the data cleanups below
	r := newTestWebhookRoutes(t, d)
	ctx := context.Background()

	trace := uuid.NewString()
	t.Cleanup(func() { _, _ = d.Pool.Exec(ctx, `DELETE FROM proxy_request_audits WHERE trace_id = $1::uuid`, trace) })

	body, _ := json.Marshal(map[string]interface{}{
		"serverId": "srv-9",
		"records": []map[string]interface{}{{
			"traceId": trace, "apiKeyHash": "h", "model": "m", "provider": "p",
			"clientIp": "2001:db8::7", "userAgent": "curl/8", "statusCode": 429, "error": "rate limited",
		}},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/webhook/usage/batch", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	r.handleUsageBatch(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["inserted"] != float64(1) {
		t.Errorf("response = %v, want inserted=1", resp)
	}
	a := readAuditRow(t, r, trace)
	if retDeref(a.userAgent) != "curl/8" || retDeref(a.clientIP) != "2001:db8::7" ||
		a.httpStatus == nil || *a.httpStatus != 429 || retDeref(a.status) != "error" || retDeref(a.errorCode) != "429" {
		t.Errorf("stored row = ua %v ip %v status %v http %v code %v", retDeref(a.userAgent), retDeref(a.clientIP),
			retDeref(a.status), a.httpStatus, retDeref(a.errorCode))
	}
}

func TestUsageRecordStatus_StatusCode(t *testing.T) {
	cases := []struct {
		rec  UsageRecord
		want string
	}{
		{UsageRecord{}, "success"},
		{UsageRecord{StatusCode: 200}, "success"},
		{UsageRecord{StatusCode: 399}, "success"},
		{UsageRecord{StatusCode: 400}, "error"},
		{UsageRecord{StatusCode: 503}, "error"},
		{UsageRecord{Error: "boom"}, "error"},
		{UsageRecord{Status: "refund", StatusCode: 500}, "refund"}, // explicit status wins
	}
	for _, c := range cases {
		if got := usageRecordStatus(c.rec); got != c.want {
			t.Errorf("usageRecordStatus(%+v) = %q, want %q", c.rec, got, c.want)
		}
	}
	if got := proxyAuditErrorCode("error", &UsageRecord{StatusCode: 502}); got != "502" {
		t.Errorf("proxyAuditErrorCode = %q, want 502", got)
	}
	if got := proxyAuditErrorCode("error", &UsageRecord{StatusCode: 502, ErrorCode: "UPSTREAM"}); got != "UPSTREAM" {
		t.Errorf("explicit error code must win, got %q", got)
	}
	if got := proxyAuditErrorCode("success", &UsageRecord{StatusCode: 200}); got != "" {
		t.Errorf("success must have no error code, got %q", got)
	}
}

package routes

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// proxyAuditColumn describes one proxy_request_audits column written by the
// batch insert together with the SQL type its bind parameter is cast to.
// Explicit casts are required because the rows are fed through a VALUES list
// (INSERT ... SELECT), where Postgres cannot infer parameter types from the
// target columns.
type proxyAuditColumn struct {
	name string
	typ  string
}

// proxyAuditColumns is the single source of truth for the batch insert layout.
// columnsPerRow is derived from it so the placeholder generator and the
// argument builder cannot drift apart when a column is added.
var proxyAuditColumns = []proxyAuditColumn{
	{"trace_id", "uuid"}, {"api_key_hash", "text"}, {"model", "text"}, {"provider", "text"},
	{"region", "text"}, {"upstream_key_id", "text"}, {"endpoint", "text"}, {"status", "text"},
	{"error", "text"}, {"error_code", "text"}, {"input_tokens", "bigint"}, {"output_tokens", "bigint"},
	{"cached_tokens", "bigint"}, {"latency_ms", "bigint"}, {"request_count", "integer"},
	{"is_streaming", "boolean"}, {"client_ip", "text"}, {"pricing_ratio", "numeric"},
	{"routing_track", "text"}, {"server_id", "text"}, {"user_agent", "text"}, {"http_status", "integer"},
}

// proxyAuditColumnsPerRow is the number of bind parameters consumed per row.
var proxyAuditColumnsPerRow = len(proxyAuditColumns)

// proxyAuditInsertChunk bounds the rows per statement so a statement never
// exceeds Postgres' 65535 bind-parameter limit (2000 rows * 22 columns = 44000).
const proxyAuditInsertChunk = 2000

// proxyAuditErrorCode returns the error code stored for an audit row. Beyond
// the shared deriveErrorCode logic it falls back to the HTTP status code so a
// failure the Proxy reported only as "statusCode: 502" is still classifiable.
func proxyAuditErrorCode(status string, rec *UsageRecord) string {
	code := deriveErrorCode(status, rec.Error, rec.ErrorCode)
	if code == "" && rec.StatusCode >= 400 {
		return strconv.Itoa(rec.StatusCode)
	}
	return code
}

// insertProxyRequestAudits bulk-inserts an audit batch into proxy_request_audits
// inside one transaction (chunked to respect the bind-parameter limit).
//
// Idempotency: the table has no unique index on trace_id (it is large and may
// already hold duplicates, and creating a unique index on it at startup could
// block boot). Instead, rows whose trace_id already exists are skipped, and a
// transaction-scoped advisory lock serialises concurrent audit inserts so two
// replays of the same batch cannot both pass the NOT EXISTS check.
//
// enterprise_id is resolved in the same statement from api_keys.key_hash; an
// unknown hash simply yields NULL (the key may have been purged).
func (r *WebhookRoutes) insertProxyRequestAudits(ctx context.Context, batch *BatchUsageReport) (int, error) {
	columnsPerRow := proxyAuditColumnsPerRow

	args := make([]interface{}, 0, len(batch.Records)*columnsPerRow)
	seen := make(map[string]struct{}, len(batch.Records))
	rows := 0
	for i := range batch.Records {
		rec := &batch.Records[i]
		if rec.TraceID == "" {
			rec.TraceID = uuid.New().String()
		}
		parsed, err := uuid.Parse(rec.TraceID)
		if err != nil {
			r.logger.Warn("Skipping audit record with invalid trace_id",
				zap.String("trace_id", rec.TraceID))
			continue
		}
		// Normalise so "ABC-.." and "abc-.." are recognised as the same trace.
		traceID := parsed.String()
		if _, dup := seen[traceID]; dup {
			continue
		}
		seen[traceID] = struct{}{}

		serverID := rec.ServerID
		if serverID == "" {
			serverID = batch.ServerID
		}
		status := usageRecordStatus(*rec)
		var pricingRatio interface{}
		if rec.PricingRatio > 0 {
			pricingRatio = rec.PricingRatio
		}
		// NULL (not 0) for "unknown" so reports can tell the two apart.
		var httpStatus interface{}
		if rec.StatusCode > 0 {
			httpStatus = rec.StatusCode
		}
		args = append(args,
			traceID, rec.APIKeyHash, rec.Model, rec.Provider, rec.Region,
			rec.UpstreamKeyID, rec.Endpoint, status, rec.Error,
			proxyAuditErrorCode(status, rec),
			rec.InputTokens, rec.OutputTokens, rec.CachedTokens, rec.LatencyMs,
			rec.RequestCount, rec.IsStreaming, rec.ClientIP, pricingRatio,
			rec.RoutingTrack, serverID, rec.UserAgent, httpStatus,
		)
		rows++
	}
	if rows == 0 {
		return 0, nil
	}

	names := make([]string, len(proxyAuditColumns))
	for i, col := range proxyAuditColumns {
		names[i] = col.name
	}
	colList := strings.Join(names, ", ")

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('proxy_request_audits_insert'))`); err != nil {
		return 0, fmt.Errorf("lock proxy request audits insert: %w", err)
	}

	inserted := 0
	for start := 0; start < rows; start += proxyAuditInsertChunk {
		end := start + proxyAuditInsertChunk
		if end > rows {
			end = rows
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "INSERT INTO proxy_request_audits (%s, enterprise_id)\nSELECT v.*, (SELECT k.enterprise_id FROM api_keys k WHERE k.key_hash = v.api_key_hash LIMIT 1)\nFROM (VALUES ", colList)
		argIdx := 1
		for i := start; i < end; i++ {
			if i > start {
				sb.WriteString(",")
			}
			sb.WriteString("(")
			for j, col := range proxyAuditColumns {
				if j > 0 {
					sb.WriteString(",")
				}
				fmt.Fprintf(&sb, "$%d::%s", argIdx, col.typ)
				argIdx++
			}
			sb.WriteString(")")
		}
		fmt.Fprintf(&sb, ") AS v(%s)\nWHERE NOT EXISTS (SELECT 1 FROM proxy_request_audits p WHERE p.trace_id = v.trace_id)", colList)

		cmdTag, err := tx.Exec(ctx, sb.String(), args[start*columnsPerRow:end*columnsPerRow]...)
		if err != nil {
			return 0, fmt.Errorf("bulk insert proxy request audits: %w", err)
		}
		inserted += int(cmdTag.RowsAffected())
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit proxy request audits: %w", err)
	}
	return inserted, nil
}

package query

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mayiants/ai-portal-go/internal/db"
)

// UpstreamUsageRecordItem holds a single upstream usage record.
type UpstreamUsageRecordItem struct {
	ID                  int64
	UpstreamKeyID       string
	Provider            string
	Model               string
	Date                string
	RequestCount        int
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
	ErrorCount          int
	AvgLatencyMs        int
	KeyPreview          string
}

// AdminUpstreamUsageData holds upstream usage page data.
type AdminUpstreamUsageData struct {
	Records    []UpstreamUsageRecordItem
	Total      int
	Page       int
	PageSize   int
	TotalPages int
	Providers  []string
	Summary    struct {
		TotalRequests int
		TotalInput    int64
		TotalOutput   int64
		TotalErrors   int
	}
}

// GetAdminUpstreamUsageData fetches upstream usage data for admin page rendering.
func GetAdminUpstreamUsageData(ctx context.Context, database *db.DB, page, pageSize int) (*AdminUpstreamUsageData, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 50
	}
	offset := (page - 1) * pageSize

	data := &AdminUpstreamUsageData{
		Records:   []UpstreamUsageRecordItem{},
		Page:      page,
		PageSize:  pageSize,
		Providers: []string{},
	}

	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM upstream_key_usage`).Scan(&data.Total)
	data.TotalPages = (data.Total + pageSize - 1) / pageSize

	rows, err := database.Query(ctx, `
		SELECT u.id, u.upstream_key_id, u.provider, u.model, u.date,
			u.request_count, u.input_tokens, u.output_tokens,
			u.cache_read_tokens, u.cache_creation_tokens, u.error_count,
			u.avg_latency_ms, k.key_preview
		FROM upstream_key_usage u
		LEFT JOIN upstream_keys k ON k.id = u.upstream_key_id
		ORDER BY u.date DESC, u.provider, u.model
		LIMIT $1 OFFSET $2
	`, pageSize, offset)
	if err != nil {
		return data, err
	}
	defer rows.Close()

	providerSet := make(map[string]bool)
	for rows.Next() {
		var item UpstreamUsageRecordItem
		var dt time.Time
		var keyPreview *string
		if err := rows.Scan(&item.ID, &item.UpstreamKeyID, &item.Provider, &item.Model, &dt,
			&item.RequestCount, &item.InputTokens, &item.OutputTokens,
			&item.CacheReadTokens, &item.CacheCreationTokens, &item.ErrorCount,
			&item.AvgLatencyMs, &keyPreview); err != nil {
			continue
		}
		item.Date = dt.Format("2006-01-02")
		if keyPreview != nil {
			item.KeyPreview = *keyPreview
		}
		data.Records = append(data.Records, item)
		data.Summary.TotalRequests += item.RequestCount
		data.Summary.TotalInput += item.InputTokens
		data.Summary.TotalOutput += item.OutputTokens
		data.Summary.TotalErrors += item.ErrorCount
		providerSet[item.Provider] = true
	}

	for p := range providerSet {
		data.Providers = append(data.Providers, p)
	}

	return data, nil
}

// ── User-facing usage_records helpers ─────────────────────────────────────────

// UsageRecordFilter holds optional filters for usage record queries.
type UsageRecordFilter struct {
	StartDate    string
	EndDate      string
	APIKeyID     string
	Model        string
	GroupID      string
	IsStreaming  *bool
	Status       string
	IsAdmin      bool
	UserID       string // Admin-only: filter by a specific user.
	EnterpriseID string // Admin-only: filter by a specific enterprise (via api_keys).
	Page         int
	PageSize     int
	Limit        int // Used when pagination is not required (e.g. recent details).
}

// DailyUsage holds per-day aggregated usage.
type DailyUsage struct {
	Date         time.Time
	Requests     int
	Tokens       int64
	Cost         int64
	InputTokens  int64
	OutputTokens int64
	CachedTokens int64
}

// TotalSummary holds aggregate usage over a period.
type TotalSummary struct {
	Requests int
	Tokens   int64
	Cost     int64
}

// GetUsageSummary returns daily usage for the last N calendar days plus period totals.
// The range is from (CURRENT_DATE - days + 1) to CURRENT_DATE inclusive. CURRENT_DATE
// is the DB session timezone (expected to be UTC); daily-trend endpoints elsewhere
// compute their own UTC boundaries.
// Passing an empty userID returns data for all users (used by admin dashboards).
func GetUsageSummary(ctx context.Context, database *db.DB, userID string, days int) ([]DailyUsage, TotalSummary, error) {
	if days < 1 {
		days = 7
	}

	args := []interface{}{days}
	argIdx := 2
	userFilter := ""
	if userID != "" {
		userFilter = " AND ur.user_id = $" + itoa(argIdx) + "::uuid"
		args = append(args, userID)
	}

	rows, err := database.Query(ctx,
		`WITH days AS (
			SELECT generate_series(CURRENT_DATE - INTERVAL '1 day' * ($1 - 1), CURRENT_DATE, INTERVAL '1 day')::date AS day
		)
		SELECT d.day,
		        COALESCE(COUNT(ur.id), 0)::int as requests,
		        COALESCE(SUM(ur.input_tokens + ur.output_tokens), 0)::bigint as tokens,
		        COALESCE(SUM(ur.total_cost), 0)::bigint as cost,
		        COALESCE(SUM(ur.input_tokens), 0)::bigint as input_tokens,
		        COALESCE(SUM(ur.output_tokens), 0)::bigint as output_tokens,
		        COALESCE(SUM(ur.cached_input_tokens), 0)::bigint as cached_tokens
		 FROM days d
		 LEFT JOIN usage_records ur ON DATE(ur.created_at) = d.day`+userFilter+`
		 GROUP BY d.day
		 ORDER BY d.day ASC`,
		args...,
	)
	if err != nil {
		return nil, TotalSummary{}, err
	}
	defer rows.Close()

	var daily []DailyUsage
	var summary TotalSummary
	for rows.Next() {
		var d DailyUsage
		if err := rows.Scan(&d.Date, &d.Requests, &d.Tokens, &d.Cost, &d.InputTokens, &d.OutputTokens, &d.CachedTokens); err == nil {
			daily = append(daily, d)
			summary.Requests += d.Requests
			summary.Tokens += d.Tokens
			summary.Cost += d.Cost
		}
	}

	return daily, summary, nil
}

// GetUsageRecords returns filtered usage records with a total count.
func GetUsageRecords(ctx context.Context, database *db.DB, userID string, filters UsageRecordFilter) ([]UsageRecord, int, error) {
	where := []string{}
	args := []interface{}{}
	argIdx := 1

	if !filters.IsAdmin {
		where = append(where, "ur.user_id = $"+itoa(argIdx)+"::uuid")
		args = append(args, userID)
		argIdx++
	}
	if filters.StartDate != "" {
		where = append(where, "ur.created_at >= $"+itoa(argIdx))
		args = append(args, filters.StartDate)
		argIdx++
	}
	if filters.EndDate != "" {
		where = append(where, "ur.created_at < ($"+itoa(argIdx)+"::date + INTERVAL '1 day')")
		args = append(args, filters.EndDate)
		argIdx++
	}
	if filters.APIKeyID != "" {
		where = append(where, "ur.user_api_key_id = $"+itoa(argIdx)+"::uuid")
		args = append(args, filters.APIKeyID)
		argIdx++
	}
	if filters.Model != "" {
		where = append(where, "ur.model = $"+itoa(argIdx))
		args = append(args, filters.Model)
		argIdx++
	}
	if filters.GroupID != "" {
		where = append(where, "ur.group_id = $"+itoa(argIdx)+"::uuid")
		args = append(args, filters.GroupID)
		argIdx++
	}
	if filters.IsStreaming != nil {
		where = append(where, "ur.is_streaming = $"+itoa(argIdx))
		args = append(args, *filters.IsStreaming)
		argIdx++
	}
	if filters.Status != "" {
		where = append(where, "ur.status = $"+itoa(argIdx))
		args = append(args, filters.Status)
		argIdx++
	}
	if filters.UserID != "" {
		where = append(where, "ur.user_id = $"+itoa(argIdx)+"::uuid")
		args = append(args, filters.UserID)
		argIdx++
	}
	if filters.EnterpriseID != "" {
		where = append(where, "ur.user_api_key_id IN (SELECT id FROM api_keys WHERE enterprise_id = $"+itoa(argIdx)+"::uuid AND key_type = 'enterprise')")
		args = append(args, filters.EnterpriseID)
		argIdx++
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	_ = database.QueryRow(ctx, "SELECT COUNT(*) FROM usage_records ur"+whereClause, args...).Scan(&total)

	page := filters.Page
	pageSize := filters.PageSize
	limit := filters.Limit
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if limit < 1 && filters.PageSize < 1 && filters.Limit > 0 {
		limit = filters.Limit
	}

	query := `SELECT ur.trace_id, ur.model, ur.provider, ur.input_tokens, ur.output_tokens, ur.cached_input_tokens,
	                 ur.request_count, ur.total_cost, ur.latency_ms, ur.created_at,
	                 COALESCE(ur.endpoint, ''),
	                 COALESCE(ur.is_streaming, false),
	                 COALESCE(ur.client_ip, ''),
	                 COALESCE(ur.user_agent, ''),
	                 COALESCE(ur.agent_tools, ''),
	                 COALESCE(ur.called_tools, ''),
	                 COALESCE(ur.status, 'success'),
	                 COALESCE(ur.group_id::text, ''),
	                 COALESCE(g.name, ''),
	                 COALESCE(NULLIF(ur.api_key_name, ''), k.name, ''),
	                 COALESCE(NULLIF(ur.key_prefix, ''), k.key_prefix,
	                          CASE WHEN ur.user_api_key_id IS NULL THEN '(pre-charge)'
	                               ELSE '(deleted key)' END),
	                 COALESCE(ur.error_code, '')
	          FROM usage_records ur
	          LEFT JOIN user_groups g ON g.id = ur.group_id
	          LEFT JOIN api_keys k ON k.id = ur.user_api_key_id` + whereClause + ` ORDER BY ur.created_at DESC`
	queryArgs := args
	if limit > 0 {
		query += " LIMIT $" + itoa(argIdx)
		queryArgs = append(queryArgs, limit)
	} else if pageSize > 0 {
		offset := (page - 1) * pageSize
		query += " LIMIT $" + itoa(argIdx) + " OFFSET $" + itoa(argIdx+1)
		queryArgs = append(queryArgs, pageSize, offset)
	}

	rows, err := database.Query(ctx, query, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var records []UsageRecord
	for rows.Next() {
		var r UsageRecord
		if err := rows.Scan(&r.TraceID, &r.Model, &r.Provider, &r.InputTokens, &r.OutputTokens, &r.CachedTokens,
			&r.Requests, &r.Cost, &r.LatencyMs, &r.CreatedAt,
			&r.Endpoint, &r.IsStreaming, &r.ClientIP, &r.UserAgent, &r.AgentTools, &r.CalledTools, &r.Status,
			&r.GroupID, &r.GroupName, &r.APIKeyName, &r.KeyPrefix, &r.ErrorCode); err == nil {
			records = append(records, r)
		}
	}

	return records, total, nil
}

// DistributionRow holds aggregated usage grouped by a dimension.
type DistributionRow struct {
	Name         string  `json:"name"`
	Requests     int     `json:"requests"`
	Tokens       int64   `json:"tokens"`
	Cost         int64   `json:"cost"`
	AvgLatencyMs int     `json:"avg_latency_ms"`
	AvgTps       float64 `json:"avg_tps"`
	KeyID        string  `json:"key_id"`
	KeyPrefix    string  `json:"key_prefix"`
	KeyName      string  `json:"key_name"`
}

// UserUsageTrendPoint holds one bucket of user usage for the dashboard trend chart.
type UserUsageTrendPoint struct {
	Date         string `json:"date"`
	Time         string `json:"time"`
	Requests     int    `json:"requests"`
	Tokens       int64  `json:"tokens"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	CachedTokens int64  `json:"cached_tokens"`
	Cost         int64  `json:"cost"`
}

// GetUserUsageTrend returns per-bucket usage for a user over a dynamic range.
// Buckets are generated so the x-axis always has the expected number of segments.
func GetUserUsageTrend(ctx context.Context, database *db.DB, userID string, rangeKey string) ([]UserUsageTrendPoint, error) {
	tr, ok := timeRanges[rangeKey]
	if !ok {
		tr = timeRanges[DefaultRange]
		rangeKey = DefaultRange
	}

	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	var start, end time.Time
	switch rangeKey {
	case "1d":
		start = today
		end = start.Add(time.Duration(tr.Buckets-1) * 4 * time.Hour)
	case "quarter":
		end = time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
		start = end.AddDate(0, -(tr.Buckets - 1), 0)
	default:
		end = today
		start = today.AddDate(0, 0, -(tr.Buckets - 1))
	}

	sql := fmt.Sprintf(
		`WITH buckets AS (
			SELECT generate_series($1::timestamp, $2::timestamp, $3::interval) AS bucket
		)
		SELECT TO_CHAR(b.bucket, '%s') as date,
		       CASE WHEN $4::bool THEN TO_CHAR(b.bucket, 'HH24:00') ELSE '' END as time,
		       COALESCE(COUNT(ur.id), 0),
		       COALESCE(SUM(ur.input_tokens + ur.output_tokens), 0),
		       COALESCE(SUM(ur.input_tokens), 0),
		       COALESCE(SUM(ur.output_tokens), 0),
		       COALESCE(SUM(ur.cached_input_tokens), 0),
		       COALESCE(SUM(ur.total_cost), 0)
		FROM buckets b
		LEFT JOIN usage_records ur
		       ON ur.created_at >= b.bucket
		      AND ur.created_at < b.bucket + $3::interval
		      AND ur.created_at >= $1::timestamp
		      AND ur.created_at < $2::timestamp + $3::interval
		      AND ur.user_id = $5::uuid
		GROUP BY b.bucket
		ORDER BY b.bucket`,
		tr.Format)

	rows, err := database.Query(ctx, sql, start, end, tr.Bucket, rangeKey == "1d", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var points []UserUsageTrendPoint
	for rows.Next() {
		var p UserUsageTrendPoint
		if err := rows.Scan(&p.Date, &p.Time, &p.Requests, &p.Tokens, &p.InputTokens, &p.OutputTokens, &p.CachedTokens, &p.Cost); err == nil {
			points = append(points, p)
		}
	}
	return points, nil
}

// GetEnterpriseUsageTrend returns per-bucket usage for an enterprise over a dynamic range.
// It aggregates usage_records whose user_api_key_id belongs to an enterprise key
// (key_type = 'enterprise') of the enterprise.
func GetEnterpriseUsageTrend(ctx context.Context, database *db.DB, enterpriseID string, rangeKey string) ([]UserUsageTrendPoint, error) {
	tr, ok := timeRanges[rangeKey]
	if !ok {
		tr = timeRanges[DefaultRange]
		rangeKey = DefaultRange
	}

	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	var start, end time.Time
	switch rangeKey {
	case "1d":
		start = today
		end = start.Add(time.Duration(tr.Buckets-1) * 4 * time.Hour)
	case "quarter":
		end = time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
		start = end.AddDate(0, -(tr.Buckets - 1), 0)
	default:
		end = today
		start = today.AddDate(0, 0, -(tr.Buckets - 1))
	}

	sql := fmt.Sprintf(
		`WITH buckets AS (
			SELECT generate_series($1::timestamp, $2::timestamp, $3::interval) AS bucket
		)
		SELECT TO_CHAR(b.bucket, '%s') as date,
		       CASE WHEN $4::bool THEN TO_CHAR(b.bucket, 'HH24:00') ELSE '' END as time,
		       COALESCE(COUNT(ur.id), 0),
		       COALESCE(SUM(ur.input_tokens + ur.output_tokens), 0),
		       COALESCE(SUM(ur.input_tokens), 0),
		       COALESCE(SUM(ur.output_tokens), 0),
		       COALESCE(SUM(ur.cached_input_tokens), 0),
		       COALESCE(SUM(ur.total_cost), 0)
		FROM buckets b
		LEFT JOIN usage_records ur
		       ON ur.created_at >= b.bucket
		      AND ur.created_at < b.bucket + $3::interval
		      AND ur.created_at >= $1::timestamp
		      AND ur.created_at < $2::timestamp + $3::interval
		      AND ur.user_api_key_id IN (SELECT id FROM api_keys WHERE enterprise_id = $5::uuid AND key_type = 'enterprise')
		GROUP BY b.bucket
		ORDER BY b.bucket`,
		tr.Format)

	rows, err := database.Query(ctx, sql, start, end, tr.Bucket, rangeKey == "1d", enterpriseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var points []UserUsageTrendPoint
	for rows.Next() {
		var p UserUsageTrendPoint
		if err := rows.Scan(&p.Date, &p.Time, &p.Requests, &p.Tokens, &p.InputTokens, &p.OutputTokens, &p.CachedTokens, &p.Cost); err == nil {
			points = append(points, p)
		}
	}
	return points, nil
}

// GetAdminUsageTrend returns per-bucket usage for the whole platform over a dynamic range.
// It reuses UserUsageTrendPoint but leaves user_id unrestricted.
func GetAdminUsageTrend(ctx context.Context, database *db.DB, rangeKey string) ([]UserUsageTrendPoint, error) {
	tr, ok := timeRanges[rangeKey]
	if !ok {
		tr = timeRanges[DefaultRange]
		rangeKey = DefaultRange
	}

	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	var start, end time.Time
	switch rangeKey {
	case "1d":
		start = today
		end = start.Add(time.Duration(tr.Buckets-1) * 4 * time.Hour)
	case "quarter":
		end = time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
		start = end.AddDate(0, -(tr.Buckets - 1), 0)
	default:
		end = today
		start = today.AddDate(0, 0, -(tr.Buckets - 1))
	}

	sql := fmt.Sprintf(
		`WITH buckets AS (
			SELECT generate_series($1::timestamp, $2::timestamp, $3::interval) AS bucket
		)
		SELECT TO_CHAR(b.bucket, '%s') as date,
		       CASE WHEN $4::bool THEN TO_CHAR(b.bucket, 'HH24:00') ELSE '' END as time,
		       COALESCE(COUNT(ur.id), 0),
		       COALESCE(SUM(ur.input_tokens + ur.output_tokens), 0),
		       COALESCE(SUM(ur.input_tokens), 0),
		       COALESCE(SUM(ur.output_tokens), 0),
		       COALESCE(SUM(ur.cached_input_tokens), 0),
		       COALESCE(SUM(ur.total_cost), 0)
		FROM buckets b
		LEFT JOIN usage_records ur
		       ON ur.created_at >= b.bucket
		      AND ur.created_at < b.bucket + $3::interval
		      AND ur.created_at >= $1::timestamp
		      AND ur.created_at < $2::timestamp + $3::interval
		GROUP BY b.bucket
		ORDER BY b.bucket`,
		tr.Format)

	rows, err := database.Query(ctx, sql, start, end, tr.Bucket, rangeKey == "1d")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var points []UserUsageTrendPoint
	for rows.Next() {
		var p UserUsageTrendPoint
		if err := rows.Scan(&p.Date, &p.Time, &p.Requests, &p.Tokens, &p.InputTokens, &p.OutputTokens, &p.CachedTokens, &p.Cost); err == nil {
			points = append(points, p)
		}
	}
	return points, nil
}

// GetUsageDistribution returns usage grouped by model or provider for the last N days.
// Passing a non-empty userID restricts results to that user.
// GetUsageDistributionRange returns usage grouped by model, provider or api_key for a dynamic range.
func GetUsageDistributionRange(ctx context.Context, database *db.DB, userID string, rangeKey, groupBy string) ([]DistributionRow, error) {
	tr, ok := timeRanges[rangeKey]
	if !ok {
		tr = timeRanges[DefaultRange]
	}

	baseWhere := "created_at >= NOW() - $1::interval"
	baseArgs := []interface{}{tr.Interval}
	argIdx := 2
	if userID != "" {
		baseWhere += " AND user_id = $" + itoa(argIdx) + "::uuid"
		baseArgs = append(baseArgs, userID)
	}

	switch groupBy {
	case "model":
		rows, err := database.Query(ctx,
			`SELECT model,
			        COUNT(*) as requests,
			        COALESCE(SUM(input_tokens + output_tokens), 0) as tokens,
			        COALESCE(SUM(total_cost), 0) as cost,
			        COALESCE(AVG(latency_ms), 0)::int as avg_latency_ms,
			        CASE WHEN COALESCE(SUM(latency_ms), 0) > 0
			             THEN COALESCE(SUM(input_tokens + output_tokens), 0)::numeric * 1000.0 / SUM(latency_ms)
			             ELSE 0
			        END as avg_tps
			 FROM usage_records
			 WHERE `+baseWhere+`
			 GROUP BY model
			 ORDER BY cost DESC`,
			baseArgs...,
		)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		var results []DistributionRow
		for rows.Next() {
			var r DistributionRow
			if err := rows.Scan(&r.Name, &r.Requests, &r.Tokens, &r.Cost, &r.AvgLatencyMs, &r.AvgTps); err == nil {
				results = append(results, r)
			}
		}
		return results, nil
	case "provider":
		rows, err := database.Query(ctx,
			`SELECT provider,
			        COUNT(*) as requests,
			        COALESCE(SUM(total_cost), 0) as cost,
			        COALESCE(AVG(latency_ms), 0)::int as avg_latency
			 FROM usage_records
			 WHERE `+baseWhere+`
			 GROUP BY provider
			 ORDER BY requests DESC`,
			baseArgs...,
		)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		var results []DistributionRow
		for rows.Next() {
			var r DistributionRow
			if err := rows.Scan(&r.Name, &r.Requests, &r.Cost, &r.AvgLatencyMs); err == nil {
				results = append(results, r)
			}
		}
		return results, nil
	case "api_key":
		keyWhere := "u.created_at >= NOW() - $1::interval"
		keyArgs := []interface{}{tr.Interval}
		keyIdx := 2
		if userID != "" {
			keyWhere += " AND u.user_id = $" + itoa(keyIdx) + "::uuid"
			keyArgs = append(keyArgs, userID)
		}
		rows, err := database.Query(ctx,
			`SELECT COALESCE(u.user_api_key_id::text, '') as api_key_id,
			        COALESCE(NULLIF(u.key_prefix, ''), k.key_prefix, '') as key_prefix,
			        COALESCE(NULLIF(u.api_key_name, ''), k.name, k.key_prefix,
			                 CASE WHEN u.user_api_key_id IS NULL THEN '(pre-charge)'
			                      ELSE '(deleted key)' END) as key_name,
			        COUNT(*)::int as requests,
			        COALESCE(SUM(u.input_tokens + u.output_tokens), 0)::bigint as tokens,
			        COALESCE(SUM(u.total_cost), 0)::bigint as cost,
			        COALESCE(AVG(u.latency_ms), 0)::int as avg_latency_ms,
			        CASE WHEN COALESCE(SUM(u.latency_ms), 0) > 0
			             THEN COALESCE(SUM(u.input_tokens + u.output_tokens), 0)::numeric * 1000.0 / SUM(u.latency_ms)
			             ELSE 0
			        END as avg_tps
			 FROM usage_records u
			 LEFT JOIN api_keys k ON k.id = u.user_api_key_id
			 WHERE `+keyWhere+`
			 GROUP BY u.user_api_key_id, u.key_prefix, u.api_key_name, k.key_prefix, k.name
			 ORDER BY cost DESC`,
			keyArgs...,
		)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		var results []DistributionRow
		for rows.Next() {
			var r DistributionRow
			if err := rows.Scan(&r.KeyID, &r.KeyPrefix, &r.KeyName, &r.Requests, &r.Tokens, &r.Cost, &r.AvgLatencyMs, &r.AvgTps); err == nil {
				results = append(results, r)
			}
		}
		return results, nil
	default:
		return nil, fmt.Errorf("unsupported group_by: %s", groupBy)
	}
}

// GetEnterpriseUsageDistributionRange returns usage grouped by model or api_key for an enterprise over a dynamic range.
func GetEnterpriseUsageDistributionRange(ctx context.Context, database *db.DB, enterpriseID string, rangeKey, groupBy string) ([]DistributionRow, error) {
	tr, ok := timeRanges[rangeKey]
	if !ok {
		tr = timeRanges[DefaultRange]
	}

	baseWhere := "created_at >= NOW() - $1::interval AND user_api_key_id IN (SELECT id FROM api_keys WHERE enterprise_id = $2::uuid AND key_type = 'enterprise')"
	baseArgs := []interface{}{tr.Interval, enterpriseID}

	switch groupBy {
	case "model":
		rows, err := database.Query(ctx,
			`SELECT model,
			        COUNT(*) as requests,
			        COALESCE(SUM(input_tokens + output_tokens), 0) as tokens,
			        COALESCE(SUM(total_cost), 0) as cost,
			        COALESCE(AVG(latency_ms), 0)::int as avg_latency_ms,
			        CASE WHEN COALESCE(SUM(latency_ms), 0) > 0
			             THEN COALESCE(SUM(input_tokens + output_tokens), 0)::numeric * 1000.0 / SUM(latency_ms)
			             ELSE 0
			        END as avg_tps
			 FROM usage_records
			 WHERE `+baseWhere+`
			 GROUP BY model
			 ORDER BY cost DESC`,
			baseArgs...,
		)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		var results []DistributionRow
		for rows.Next() {
			var r DistributionRow
			if err := rows.Scan(&r.Name, &r.Requests, &r.Tokens, &r.Cost, &r.AvgLatencyMs, &r.AvgTps); err == nil {
				results = append(results, r)
			}
		}
		return results, nil
	case "api_key":
		keyWhere := "u.created_at >= NOW() - $1::interval AND u.user_api_key_id IN (SELECT id FROM api_keys WHERE enterprise_id = $2::uuid AND key_type = 'enterprise')"
		keyArgs := []interface{}{tr.Interval, enterpriseID}
		rows, err := database.Query(ctx,
			`SELECT COALESCE(u.user_api_key_id::text, '') as api_key_id,
			        COALESCE(NULLIF(u.key_prefix, ''), k.key_prefix, '') as key_prefix,
			        COALESCE(NULLIF(u.api_key_name, ''), k.name, k.key_prefix,
			                 CASE WHEN u.user_api_key_id IS NULL THEN '(pre-charge)'
			                      ELSE '(deleted key)' END) as key_name,
			        COUNT(*)::int as requests,
			        COALESCE(SUM(u.input_tokens + u.output_tokens), 0)::bigint as tokens,
			        COALESCE(SUM(u.total_cost), 0)::bigint as cost,
			        COALESCE(AVG(u.latency_ms), 0)::int as avg_latency_ms,
			        CASE WHEN COALESCE(SUM(u.latency_ms), 0) > 0
			             THEN COALESCE(SUM(u.input_tokens + u.output_tokens), 0)::numeric * 1000.0 / SUM(u.latency_ms)
			             ELSE 0
			        END as avg_tps
			 FROM usage_records u
			 LEFT JOIN api_keys k ON k.id = u.user_api_key_id
			 WHERE `+keyWhere+`
			 GROUP BY u.user_api_key_id, u.key_prefix, u.api_key_name, k.key_prefix, k.name
			 ORDER BY cost DESC`,
			keyArgs...,
		)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		var results []DistributionRow
		for rows.Next() {
			var r DistributionRow
			if err := rows.Scan(&r.KeyID, &r.KeyPrefix, &r.KeyName, &r.Requests, &r.Tokens, &r.Cost, &r.AvgLatencyMs, &r.AvgTps); err == nil {
				results = append(results, r)
			}
		}
		return results, nil
	default:
		return nil, fmt.Errorf("unsupported group_by: %s", groupBy)
	}
}

// GetUsageDistribution returns usage grouped by model or provider for the last N days.
// Passing a non-empty userID restricts results to that user.
func GetUsageDistribution(ctx context.Context, database *db.DB, userID string, days int, groupBy string) ([]DistributionRow, error) {
	if days < 1 {
		days = 7
	}

	baseWhere := "created_at > NOW() - INTERVAL '1 day' * $1"
	baseArgs := []interface{}{days}
	argIdx := 2
	if userID != "" {
		baseWhere += " AND user_id = $" + itoa(argIdx) + "::uuid"
		baseArgs = append(baseArgs, userID)
	}

	switch groupBy {
	case "model":
		rows, err := database.Query(ctx,
			`SELECT model,
			        COUNT(*) as requests,
			        COALESCE(SUM(input_tokens + output_tokens), 0) as tokens,
			        COALESCE(SUM(total_cost), 0) as cost,
			        COALESCE(AVG(latency_ms), 0)::int as avg_latency_ms,
			        CASE WHEN COALESCE(SUM(latency_ms), 0) > 0
			             THEN COALESCE(SUM(input_tokens + output_tokens), 0)::numeric * 1000.0 / SUM(latency_ms)
			             ELSE 0
			        END as avg_tps
			 FROM usage_records
			 WHERE `+baseWhere+`
			 GROUP BY model
			 ORDER BY cost DESC`,
			baseArgs...,
		)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		var results []DistributionRow
		for rows.Next() {
			var r DistributionRow
			if err := rows.Scan(&r.Name, &r.Requests, &r.Tokens, &r.Cost, &r.AvgLatencyMs, &r.AvgTps); err == nil {
				results = append(results, r)
			}
		}
		return results, nil
	case "provider":
		rows, err := database.Query(ctx,
			`SELECT provider,
			        COUNT(*) as requests,
			        COALESCE(SUM(total_cost), 0) as cost,
			        COALESCE(AVG(latency_ms), 0)::int as avg_latency
			 FROM usage_records
			 WHERE `+baseWhere+`
			 GROUP BY provider
			 ORDER BY requests DESC`,
			baseArgs...,
		)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		var results []DistributionRow
		for rows.Next() {
			var r DistributionRow
			if err := rows.Scan(&r.Name, &r.Requests, &r.Cost, &r.AvgLatencyMs); err == nil {
				results = append(results, r)
			}
		}
		return results, nil
	case "api_key":
		keyWhere := "u.created_at > NOW() - INTERVAL '1 day' * $1 - INTERVAL '30 days'"
		keyArgs := []interface{}{days}
		keyIdx := 2
		if userID != "" {
			keyWhere += " AND u.user_id = $" + itoa(keyIdx) + "::uuid"
			keyArgs = append(keyArgs, userID)
		}
		rows, err := database.Query(ctx,
			`SELECT COALESCE(u.user_api_key_id::text, '') as api_key_id,
			        COALESCE(NULLIF(u.key_prefix, ''), k.key_prefix, '') as key_prefix,
			        COALESCE(NULLIF(u.api_key_name, ''), k.name, k.key_prefix,
			                 CASE WHEN u.user_api_key_id IS NULL THEN '(pre-charge)'
			                      ELSE '(deleted key)' END) as key_name,
			        COUNT(*) as requests,
			        COALESCE(SUM(u.input_tokens + u.output_tokens), 0) as tokens,
			        COALESCE(SUM(u.total_cost), 0) as cost
			 FROM usage_records u
			 LEFT JOIN api_keys k ON k.id = u.user_api_key_id
			 WHERE `+keyWhere+`
			 GROUP BY u.user_api_key_id, u.key_prefix, u.api_key_name, k.key_prefix, k.name
			 ORDER BY cost DESC`,
			keyArgs...,
		)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		var results []DistributionRow
		for rows.Next() {
			var r DistributionRow
			if err := rows.Scan(&r.KeyID, &r.KeyPrefix, &r.KeyName, &r.Requests, &r.Tokens, &r.Cost); err == nil {
				results = append(results, r)
			}
		}
		return results, nil
	default:
		return nil, fmt.Errorf("unsupported group_by: %s", groupBy)
	}
}

// DashboardStats holds aggregated admin dashboard statistics.
type DashboardStats struct {
	TotalUsers    int
	TotalKeys     int
	ActiveKeys    int
	TotalRequests int
	TotalTokens   int64
	TotalCost     float64
	TodayRequests int
	TodayTokens   int64
	TodayCost     float64
}

// GetDashboardStats returns high-level dashboard stats.
// A days value of 0 disables the date filter for the main aggregate.
func GetDashboardStats(ctx context.Context, database *db.DB, days int) (DashboardStats, error) {
	var s DashboardStats

	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE deleted_at IS NULL`).Scan(&s.TotalUsers)
	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM api_keys WHERE deleted_at IS NULL`).Scan(&s.TotalKeys)
	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM api_keys WHERE status = 'active' AND deleted_at IS NULL`).Scan(&s.ActiveKeys)

	aggregateWhere := ""
	aggregateArgs := []interface{}{}
	if days > 0 {
		aggregateWhere = " WHERE created_at > NOW() - INTERVAL '1 day' * $1"
		aggregateArgs = append(aggregateArgs, days)
	}
	_ = database.QueryRow(ctx,
		`SELECT COUNT(*), COALESCE(SUM(input_tokens + output_tokens), 0), COALESCE(SUM(total_cost), 0)
		 FROM usage_records`+aggregateWhere,
		aggregateArgs...,
	).Scan(&s.TotalRequests, &s.TotalTokens, &s.TotalCost)

	_ = database.QueryRow(ctx,
		`SELECT COUNT(*), COALESCE(SUM(input_tokens + output_tokens), 0), COALESCE(SUM(total_cost), 0)
		 FROM usage_records WHERE created_at >= CURRENT_DATE`,
	).Scan(&s.TodayRequests, &s.TodayTokens, &s.TodayCost)

	return s, nil
}

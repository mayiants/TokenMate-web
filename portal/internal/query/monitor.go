package query

import (
	"context"
	"math"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/db"
)

// UpstreamKeyStatus holds upstream key monitoring data.
type UpstreamKeyStatus struct {
	ID               string
	Provider         string
	KeyPreview       string
	Enabled          bool
	ValidationStatus string
	LastValidatedAt  *time.Time
	RPMLimit         *int
	TPMLimit         *int
	CurrentRPM       int
	CurrentTPM       int
	CreatedAt        time.Time
}

// UpstreamKeyMonitorData holds the full upstream monitor page data.
type UpstreamKeyMonitorData struct {
	Keys        []UpstreamKeyStatus
	TotalKeys   int
	ActiveKeys  int
	FailedKeys  int
	PendingKeys int
	Providers   []string
	Total       int
	Page        int
	PageSize    int
	TotalPages  int
}

// GetUpstreamKeyMonitorData fetches upstream key monitoring data.
func GetUpstreamKeyMonitorData(ctx context.Context, database *db.DB, page, pageSize int) (*UpstreamKeyMonitorData, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 50 {
		pageSize = 50
	}

	data := &UpstreamKeyMonitorData{
		Keys:      []UpstreamKeyStatus{},
		Page:      page,
		PageSize:  pageSize,
		Providers: []string{},
	}
	providerSet := make(map[string]bool)

	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM upstream_keys WHERE deleted_at IS NULL`).Scan(&data.Total)
	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM upstream_keys WHERE deleted_at IS NULL AND enabled = true`).Scan(&data.ActiveKeys)
	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM upstream_keys WHERE deleted_at IS NULL AND COALESCE(validation_status, 'pending') IN ('failed', 'invalid')`).Scan(&data.FailedKeys)
	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM upstream_keys WHERE deleted_at IS NULL AND COALESCE(validation_status, 'pending') = 'pending'`).Scan(&data.PendingKeys)
	data.TotalKeys = data.Total

	totalPages := (data.Total + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}
	data.TotalPages = totalPages

	offset := (page - 1) * pageSize
	rows, err := database.Query(ctx,
		`SELECT id::text, provider, key_preview, enabled,
		        COALESCE(validation_status, 'pending'),
		        validation_at,
		        rpm_limit, tpm_limit, created_at
		 FROM upstream_keys WHERE deleted_at IS NULL ORDER BY provider, created_at DESC LIMIT $1 OFFSET $2`,
		pageSize, offset)
	if err != nil {
		return data, nil
	}
	defer rows.Close()

	for rows.Next() {
		var k UpstreamKeyStatus
		if err := rows.Scan(&k.ID, &k.Provider, &k.KeyPreview, &k.Enabled,
			&k.ValidationStatus, &k.LastValidatedAt,
			&k.RPMLimit, &k.TPMLimit, &k.CreatedAt); err == nil {
			data.Keys = append(data.Keys, k)
			providerSet[k.Provider] = true
		}
	}

	for p := range providerSet {
		data.Providers = append(data.Providers, p)
	}

	return data, nil
}

// DownstreamKeyStatus holds downstream (user API) key monitoring data over a period.
type DownstreamKeyStatus struct {
	ID             string
	Name           string
	KeyPrefix      string
	UserName       string
	UserEmail      string
	Status         string
	KeyType        string
	GroupName      string
	CurrentModel   string
	RequestCount   int64
	TokenCount     int64
	ErrorCount     int64
	ErrorRate      float64
	AvgLatencyMs   int
	LastUsedAt     *time.Time
	RateLimitRPM   int
	RateLimitTPM   int
	RPMUtilization float64
	TPMUtilization float64
	Utilization    float64
	CreatedAt      time.Time
}

// DownstreamKeyMonitorData holds the full downstream monitor page data.
type DownstreamKeyMonitorData struct {
	Keys         []DownstreamKeyStatus
	TotalKeys    int
	ActiveKeys   int
	DisabledKeys int
	RequestCount int64
	TokenCount   int64
	Total        int
	Page         int
	PageSize     int
	TotalPages   int
}

// GetDownstreamKeyMonitorData fetches downstream key monitoring data over the requested period.
func GetDownstreamKeyMonitorData(ctx context.Context, database *db.DB, period, search, status, keyType, sort string, page, pageSize int) (*DownstreamKeyMonitorData, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 50
	}
	// Cap UI pages at 50; allow larger exports.
	const maxPageSize = 5000
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}

	// Normalize unified UI values (1d/15d/quarter) to the canonical set
	// (24h/7d/30d/90d) before resolving the window.
	period = NormalizePeriod(period)
	windowDays := 7
	switch period {
	case "24h":
		windowDays = 1
	case "7d":
		windowDays = 7
	case "15d":
		windowDays = 15
	case "30d":
		windowDays = 30
	case "90d":
		windowDays = 90
	default:
	}
	windowMinutes := windowDays * 1440

	data := &DownstreamKeyMonitorData{
		Keys:     []DownstreamKeyStatus{},
		Page:     page,
		PageSize: pageSize,
	}

	filterArgs := []interface{}{status, keyType, search, windowMinutes}

	// Summary aggregates over the filtered set and selected period.
	_ = database.QueryRow(ctx, `
		WITH filtered AS (
			SELECT ak.id, ak.status
			FROM api_keys ak
			JOIN users u ON ak.user_id = u.id
			WHERE ak.deleted_at IS NULL
			  AND ($1 = '' OR ak.status = $1)
			  AND ($2 = '' OR ak.key_type = $2)
			  AND ($3 = '' OR ak.key_prefix ILIKE '%' || $3 || '%' OR u.name ILIKE '%' || $3 || '%' OR u.email ILIKE '%' || $3 || '%')
		),
		stats AS (
			SELECT f.id, f.status,
			       COALESCE(SUM(ur.request_count), 0)::bigint as req,
			       COALESCE(SUM(ur.input_tokens + ur.output_tokens), 0)::bigint as tok
			FROM filtered f
			LEFT JOIN usage_records ur ON ur.user_api_key_id = f.id
			  AND ur.created_at >= NOW() - ($4::int || ' minutes')::interval
			GROUP BY f.id, f.status
		)
		SELECT COUNT(*) as total_keys,
		       COUNT(*) FILTER (WHERE status = 'active') as active_keys,
		       COUNT(*) FILTER (WHERE status != 'active') as disabled_keys,
		       COALESCE(SUM(req), 0) as total_requests,
		       COALESCE(SUM(tok), 0) as total_tokens
		FROM stats
	`, filterArgs...).Scan(&data.Total, &data.ActiveKeys, &data.DisabledKeys, &data.RequestCount, &data.TokenCount)
	data.TotalKeys = data.Total

	totalPages := (data.Total + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}
	data.TotalPages = totalPages

	offset := (page - 1) * pageSize

	orderClause := "s.last_used_at DESC NULLS LAST, ak.created_at DESC"
	switch sort {
	case "requests_desc":
		orderClause = "COALESCE(s.request_count, 0) DESC, s.last_used_at DESC NULLS LAST"
	case "tokens_desc":
		orderClause = "COALESCE(s.token_count, 0) DESC"
	case "latency_desc":
		orderClause = "COALESCE(s.avg_latency_ms, 0) DESC"
	case "last_used_desc":
		orderClause = "s.last_used_at DESC NULLS LAST, ak.created_at DESC"
	}

	query := `
		SELECT ak.id::text, COALESCE(ak.name, ''), ak.key_prefix,
		       COALESCE(u.name, u.email) as user_name, u.email,
		       ak.status, COALESCE(ak.key_type, 'general'),
		       COALESCE(g.name, '') as group_name,
		       COALESCE(s.top_model, ak.current_model, '') as current_model,
		       ak.created_at,
		       COALESCE(s.request_count, 0)::bigint,
		       COALESCE(s.token_count, 0)::bigint,
		       COALESCE(s.error_count, 0)::bigint,
		       COALESCE(s.avg_latency_ms, 0)::int,
		       s.last_used_at,
		       COALESCE(ak.rate_limit_rpm, 0),
		       COALESCE(ak.rate_limit_tpm, 0)
		FROM api_keys ak
		JOIN users u ON ak.user_id = u.id
		LEFT JOIN user_groups g ON ak.group_id = g.id
		LEFT JOIN LATERAL (
			SELECT COALESCE(SUM(request_count), 0)::bigint as request_count,
			       COALESCE(SUM(input_tokens + output_tokens), 0)::bigint as token_count,
			       COALESCE(SUM(CASE WHEN error IS NOT NULL AND error <> '' THEN request_count ELSE 0 END), 0)::bigint as error_count,
			       COALESCE(AVG(CASE WHEN latency_ms > 0 THEN latency_ms END), 0)::int as avg_latency_ms,
			       MAX(created_at) as last_used_at,
			       (SELECT model FROM usage_records
			        WHERE user_api_key_id = ak.id
			          AND created_at >= NOW() - ($6::int || ' minutes')::interval
			        GROUP BY model
			        ORDER BY SUM(request_count) DESC
			        LIMIT 1) as top_model
			FROM usage_records
			WHERE user_api_key_id = ak.id
			  AND created_at >= NOW() - ($6::int || ' minutes')::interval
		) s ON true
		WHERE ak.deleted_at IS NULL
		  AND ($1 = '' OR ak.status = $1)
		  AND ($2 = '' OR ak.key_type = $2)
		  AND ($3 = '' OR ak.key_prefix ILIKE '%' || $3 || '%' OR u.name ILIKE '%' || $3 || '%' OR u.email ILIKE '%' || $3 || '%')
		ORDER BY ` + orderClause + `
		LIMIT $4 OFFSET $5`

	rows, err := database.Query(ctx, query, status, keyType, search, pageSize, offset, windowMinutes)
	if err != nil {
		logQueryError(ctx, "GetDownstreamKeyMonitorData", err)
		return data, nil
	}
	defer rows.Close()

	for rows.Next() {
		var k DownstreamKeyStatus
		if err := rows.Scan(&k.ID, &k.Name, &k.KeyPrefix,
			&k.UserName, &k.UserEmail,
			&k.Status, &k.KeyType,
			&k.GroupName, &k.CurrentModel,
			&k.CreatedAt,
			&k.RequestCount, &k.TokenCount, &k.ErrorCount,
			&k.AvgLatencyMs, &k.LastUsedAt,
			&k.RateLimitRPM, &k.RateLimitTPM); err == nil {
			if k.RequestCount > 0 {
				k.ErrorRate = float64(k.ErrorCount) / float64(k.RequestCount) * 100.0
			}
			if k.RateLimitRPM > 0 {
				k.RPMUtilization = float64(k.RequestCount) / (float64(k.RateLimitRPM) * float64(windowDays) * 1440.0) * 100.0
			}
			if k.RateLimitTPM > 0 {
				k.TPMUtilization = float64(k.TokenCount) / (float64(k.RateLimitTPM) * float64(windowDays) * 1440.0) * 100.0
			}
			k.Utilization = math.Max(k.RPMUtilization, k.TPMUtilization)
			data.Keys = append(data.Keys, k)
		}
	}

	return data, nil
}

// DownstreamUserStatus holds per-user aggregated downstream monitoring data.
type DownstreamUserStatus struct {
	UserID       string
	UserName     string
	UserEmail    string
	TotalKeys    int
	ActiveKeys   int
	RequestCount int64
	TokenCount   int64
	ErrorCount   int64
	ErrorRate    float64
	AvgLatencyMs int
	LastUsedAt   *time.Time
}

// DownstreamUserMonitorData holds the user-grouped downstream monitor page data.
type DownstreamUserMonitorData struct {
	Users        []DownstreamUserStatus
	TotalKeys    int
	ActiveKeys   int
	DisabledKeys int
	RequestCount int64
	TokenCount   int64
	Total        int
	Page         int
	PageSize     int
	TotalPages   int
}

// DownstreamEnterpriseStatus holds per-enterprise aggregated downstream monitoring data.
type DownstreamEnterpriseStatus struct {
	EnterpriseID   string
	EnterpriseName string
	TotalKeys      int
	ActiveKeys     int
	MemberCount    int
	RequestCount   int64
	TokenCount     int64
	ErrorCount     int64
	ErrorRate      float64
	AvgLatencyMs   int
	LastUsedAt     *time.Time
}

// DownstreamEnterpriseMonitorData holds the enterprise-grouped downstream monitor page data.
type DownstreamEnterpriseMonitorData struct {
	Enterprises  []DownstreamEnterpriseStatus
	TotalKeys    int
	ActiveKeys   int
	DisabledKeys int
	RequestCount int64
	TokenCount   int64
	Total        int
	Page         int
	PageSize     int
	TotalPages   int
}

// GetDownstreamUserMonitorData aggregates downstream usage by user over the requested period.
func GetDownstreamUserMonitorData(ctx context.Context, database *db.DB, period, search, status, keyType, sort string, page, pageSize int) (*DownstreamUserMonitorData, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 50
	}
	const maxPageSize = 5000
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}

	// Normalize unified UI values (1d/15d/quarter) to the canonical set
	// (24h/7d/30d/90d) before resolving the window.
	period = NormalizePeriod(period)
	windowDays := 7
	switch period {
	case "24h":
		windowDays = 1
	case "7d":
		windowDays = 7
	case "15d":
		windowDays = 15
	case "30d":
		windowDays = 30
	case "90d":
		windowDays = 90
	}
	windowMinutes := windowDays * 1440

	data := &DownstreamUserMonitorData{
		Users:    []DownstreamUserStatus{},
		Page:     page,
		PageSize: pageSize,
	}

	filterArgs := []interface{}{status, keyType, search, windowMinutes}
	var totalUsers int

	_ = database.QueryRow(ctx, `
		WITH filtered_keys AS (
			SELECT ak.id, ak.status, ak.user_id
			FROM api_keys ak
			JOIN users u ON ak.user_id = u.id
			WHERE ak.deleted_at IS NULL
			  AND ($1 = '' OR ak.status = $1)
			  AND ($2 = '' OR ak.key_type = $2)
			  AND ($3 = '' OR ak.key_prefix ILIKE '%' || $3 || '%' OR u.name ILIKE '%' || $3 || '%' OR u.email ILIKE '%' || $3 || '%')
		),
		stats AS (
			SELECT fk.user_id,
			       COUNT(*) as total_keys,
			       COUNT(*) FILTER (WHERE fk.status = 'active') as active_keys,
			       COALESCE(SUM(ur.request_count), 0)::bigint as req,
			       COALESCE(SUM(ur.input_tokens + ur.output_tokens), 0)::bigint as tok,
			       COALESCE(SUM(CASE WHEN ur.error IS NOT NULL AND ur.error <> '' THEN ur.request_count ELSE 0 END), 0)::bigint as err
			FROM filtered_keys fk
			LEFT JOIN usage_records ur ON ur.user_api_key_id = fk.id
			  AND ur.created_at >= NOW() - ($4::int || ' minutes')::interval
			GROUP BY fk.user_id
		)
		SELECT (SELECT COUNT(*) FROM stats) as total_users,
		       COALESCE(SUM(total_keys), 0) as total_keys,
		       COALESCE(SUM(active_keys), 0) as active_keys,
		       COALESCE(SUM(total_keys - active_keys), 0) as disabled_keys,
		       COALESCE(SUM(req), 0) as total_requests,
		       COALESCE(SUM(tok), 0) as total_tokens
		FROM stats
	`, filterArgs...).Scan(&totalUsers, &data.TotalKeys, &data.ActiveKeys, &data.DisabledKeys, &data.RequestCount, &data.TokenCount)
	data.Total = totalUsers

	totalPages := (data.Total + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}
	data.TotalPages = totalPages
	offset := (page - 1) * pageSize

	orderClause := "last_used_at DESC NULLS LAST, u.created_at DESC"
	switch sort {
	case "requests_desc":
		orderClause = "COALESCE(requests, 0) DESC, last_used_at DESC NULLS LAST"
	case "tokens_desc":
		orderClause = "COALESCE(tokens, 0) DESC"
	case "latency_desc":
		orderClause = "COALESCE(avg_latency_ms, 0) DESC"
	case "last_used_desc":
		orderClause = "last_used_at DESC NULLS LAST, u.created_at DESC"
	}

	query := `
		WITH filtered_keys AS (
			SELECT ak.id, ak.status, ak.user_id
			FROM api_keys ak
			JOIN users u ON ak.user_id = u.id
			WHERE ak.deleted_at IS NULL
			  AND ($1 = '' OR ak.status = $1)
			  AND ($2 = '' OR ak.key_type = $2)
			  AND ($3 = '' OR ak.key_prefix ILIKE '%' || $3 || '%' OR u.name ILIKE '%' || $3 || '%' OR u.email ILIKE '%' || $3 || '%')
		),
		stats AS (
			SELECT fk.user_id,
			       COUNT(*) as total_keys,
			       COUNT(*) FILTER (WHERE fk.status = 'active') as active_keys,
			       COALESCE(SUM(ur.request_count), 0)::bigint as requests,
			       COALESCE(SUM(ur.input_tokens + ur.output_tokens), 0)::bigint as tokens,
			       COALESCE(SUM(CASE WHEN ur.error IS NOT NULL AND ur.error <> '' THEN ur.request_count ELSE 0 END), 0)::bigint as errors,
			       COALESCE(AVG(CASE WHEN ur.latency_ms > 0 THEN ur.latency_ms END), 0)::int as avg_latency_ms,
			       MAX(ur.created_at) as last_used_at
			FROM filtered_keys fk
			LEFT JOIN usage_records ur ON ur.user_api_key_id = fk.id
			  AND ur.created_at >= NOW() - ($6::int || ' minutes')::interval
			GROUP BY fk.user_id
		)
		SELECT u.id::text, COALESCE(u.name, u.email) as user_name, u.email,
		       s.total_keys, s.active_keys,
		       COALESCE(s.requests, 0)::bigint,
		       COALESCE(s.tokens, 0)::bigint,
		       COALESCE(s.errors, 0)::bigint,
		       COALESCE(s.avg_latency_ms, 0)::int,
		       s.last_used_at
		FROM stats s
		JOIN users u ON u.id = s.user_id
		ORDER BY ` + orderClause + `
		LIMIT $4 OFFSET $5`

	rows, err := database.Query(ctx, query, status, keyType, search, pageSize, offset, windowMinutes)
	if err != nil {
		logQueryError(ctx, "GetDownstreamKeyMonitorData", err)
		return data, nil
	}
	defer rows.Close()

	for rows.Next() {
		var u DownstreamUserStatus
		if err := rows.Scan(&u.UserID, &u.UserName, &u.UserEmail,
			&u.TotalKeys, &u.ActiveKeys,
			&u.RequestCount, &u.TokenCount, &u.ErrorCount,
			&u.AvgLatencyMs, &u.LastUsedAt); err == nil {
			if u.RequestCount > 0 {
				u.ErrorRate = float64(u.ErrorCount) / float64(u.RequestCount) * 100.0
			}
			data.Users = append(data.Users, u)
		}
	}

	return data, nil
}

// GetDownstreamEnterpriseMonitorData aggregates downstream usage by enterprise over the requested period.
func GetDownstreamEnterpriseMonitorData(ctx context.Context, database *db.DB, period, search, status, keyType, sort string, page, pageSize int) (*DownstreamEnterpriseMonitorData, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 50
	}
	const maxPageSize = 5000
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}

	// Normalize unified UI values (1d/15d/quarter) to the canonical set
	// (24h/7d/30d/90d) before resolving the window.
	period = NormalizePeriod(period)
	windowDays := 7
	switch period {
	case "24h":
		windowDays = 1
	case "7d":
		windowDays = 7
	case "15d":
		windowDays = 15
	case "30d":
		windowDays = 30
	case "90d":
		windowDays = 90
	}
	windowMinutes := windowDays * 1440

	data := &DownstreamEnterpriseMonitorData{
		Enterprises: []DownstreamEnterpriseStatus{},
		Page:        page,
		PageSize:    pageSize,
	}

	filterArgs := []interface{}{status, keyType, search, windowMinutes}
	var totalEnterprises int

	_ = database.QueryRow(ctx, `
		WITH filtered_keys AS (
			SELECT ak.id, ak.status, ak.enterprise_id
			FROM api_keys ak
			JOIN enterprises e ON ak.enterprise_id = e.id
			WHERE ak.deleted_at IS NULL
			  AND ak.enterprise_id IS NOT NULL
			  AND ($1 = '' OR ak.status = $1)
			  AND ($2 = '' OR ak.key_type = $2)
			  AND ($3 = '' OR e.name ILIKE '%' || $3 || '%' OR COALESCE(e.contact_email, '') ILIKE '%' || $3 || '%')
		),
		stats AS (
			SELECT fk.enterprise_id,
			       COUNT(*) as total_keys,
			       COUNT(*) FILTER (WHERE fk.status = 'active') as active_keys,
			       COALESCE(SUM(ur.request_count), 0)::bigint as req,
			       COALESCE(SUM(ur.input_tokens + ur.output_tokens), 0)::bigint as tok,
			       COALESCE(SUM(CASE WHEN ur.error IS NOT NULL AND ur.error <> '' THEN ur.request_count ELSE 0 END), 0)::bigint as err
			FROM filtered_keys fk
			LEFT JOIN usage_records ur ON ur.user_api_key_id = fk.id
			  AND ur.created_at >= NOW() - ($4::int || ' minutes')::interval
			GROUP BY fk.enterprise_id
		)
		SELECT (SELECT COUNT(*) FROM stats) as total_enterprises,
		       COALESCE(SUM(total_keys), 0) as total_keys,
		       COALESCE(SUM(active_keys), 0) as active_keys,
		       COALESCE(SUM(total_keys - active_keys), 0) as disabled_keys,
		       COALESCE(SUM(req), 0) as total_requests,
		       COALESCE(SUM(tok), 0) as total_tokens
		FROM stats
	`, filterArgs...).Scan(&totalEnterprises, &data.TotalKeys, &data.ActiveKeys, &data.DisabledKeys, &data.RequestCount, &data.TokenCount)
	data.Total = totalEnterprises

	totalPages := (data.Total + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}
	data.TotalPages = totalPages
	offset := (page - 1) * pageSize

	orderClause := "last_used_at DESC NULLS LAST, e.created_at DESC"
	switch sort {
	case "requests_desc":
		orderClause = "COALESCE(requests, 0) DESC, last_used_at DESC NULLS LAST"
	case "tokens_desc":
		orderClause = "COALESCE(tokens, 0) DESC"
	case "latency_desc":
		orderClause = "COALESCE(avg_latency_ms, 0) DESC"
	case "last_used_desc":
		orderClause = "last_used_at DESC NULLS LAST, e.created_at DESC"
	}

	query := `
		WITH filtered_keys AS (
			SELECT ak.id, ak.status, ak.enterprise_id
			FROM api_keys ak
			JOIN enterprises e ON ak.enterprise_id = e.id
			WHERE ak.deleted_at IS NULL
			  AND ak.enterprise_id IS NOT NULL
			  AND ($1 = '' OR ak.status = $1)
			  AND ($2 = '' OR ak.key_type = $2)
			  AND ($3 = '' OR e.name ILIKE '%' || $3 || '%' OR COALESCE(e.contact_email, '') ILIKE '%' || $3 || '%')
		),
		stats AS (
			SELECT fk.enterprise_id,
			       COUNT(*) as total_keys,
			       COUNT(*) FILTER (WHERE fk.status = 'active') as active_keys,
			       COALESCE(SUM(ur.request_count), 0)::bigint as requests,
			       COALESCE(SUM(ur.input_tokens + ur.output_tokens), 0)::bigint as tokens,
			       COALESCE(SUM(CASE WHEN ur.error IS NOT NULL AND ur.error <> '' THEN ur.request_count ELSE 0 END), 0)::bigint as errors,
			       COALESCE(AVG(CASE WHEN ur.latency_ms > 0 THEN ur.latency_ms END), 0)::int as avg_latency_ms,
			       MAX(ur.created_at) as last_used_at
			FROM filtered_keys fk
			LEFT JOIN usage_records ur ON ur.user_api_key_id = fk.id
			  AND ur.created_at >= NOW() - ($6::int || ' minutes')::interval
			GROUP BY fk.enterprise_id
		)
		SELECT e.id::text, e.name,
		       s.total_keys, s.active_keys,
		       (SELECT COUNT(*) FROM users WHERE enterprise_id = e.id AND deleted_at IS NULL) as member_count,
		       COALESCE(s.requests, 0)::bigint,
		       COALESCE(s.tokens, 0)::bigint,
		       COALESCE(s.errors, 0)::bigint,
		       COALESCE(s.avg_latency_ms, 0)::int,
		       s.last_used_at
		FROM stats s
		JOIN enterprises e ON e.id = s.enterprise_id
		ORDER BY ` + orderClause + `
		LIMIT $4 OFFSET $5`

	rows, err := database.Query(ctx, query, status, keyType, search, pageSize, offset, windowMinutes)
	if err != nil {
		logQueryError(ctx, "GetDownstreamKeyMonitorData", err)
		return data, nil
	}
	defer rows.Close()

	for rows.Next() {
		var e DownstreamEnterpriseStatus
		if err := rows.Scan(&e.EnterpriseID, &e.EnterpriseName,
			&e.TotalKeys, &e.ActiveKeys, &e.MemberCount,
			&e.RequestCount, &e.TokenCount, &e.ErrorCount,
			&e.AvgLatencyMs, &e.LastUsedAt); err == nil {
			if e.RequestCount > 0 {
				e.ErrorRate = float64(e.ErrorCount) / float64(e.RequestCount) * 100.0
			}
			data.Enterprises = append(data.Enterprises, e)
		}
	}

	return data, nil
}

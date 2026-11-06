package query

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/db"
)

// APIKeyItem holds a unified API key record.
// Fields are a superset of both the web page render and JSON API needs.
type APIKeyItem struct {
	ID               string
	Name             string
	KeyPrefix        string
	Status           string
	AccessMode       string
	AllowedModels    interface{}
	RateLimitRPM     int
	RateLimitTPM     int
	TotalRequests    int
	TotalTokens      int64
	CreatedAt        time.Time
	GroupID          *string
	GroupName        string
	KeyType          *string
	BillingMode      *string
	CurrentModel     *string
	ModelRoutingMode *string
	SelectedModels   interface{}
	GroupPriority    interface{}
	MonthRequests    int
	MonthTokens      int64
	TodayRequests    int
	TodayTokens      int64
	ExpiresAt        *time.Time
}

// APIKeysOptions controls the behavior of GetAPIKeysData.
type APIKeysOptions struct {
	Page              int  // 1-based; <=0 disables pagination
	PageSize          int  // default 50, max 50
	Limit             int  // hard row limit; 0 = no limit (overrides Page/PageSize when >0)
	ExcludeEnterprise bool // true = filter out keys where key_type='enterprise' AND enterprise_id IS NOT NULL
	IncludeStats      bool // true = add LATERAL JOINs for month/today usage stats
}

// APIKeysData holds paginated API key results.
type APIKeysData struct {
	Items      []APIKeyItem
	Total      int
	Page       int
	PageSize   int
	TotalPages int
}

// GetAPIKeysData fetches API keys for a user with flexible options.
//
// Used by:
//   - routes/apikeys.go:listKeys   (JSON API, IncludeStats=true, Limit from query param)
//   - web/data.go:GetAPIKeys       (page render, ExcludeEnterprise=true, paginated)
func GetAPIKeysData(ctx context.Context, database *db.DB, userID string, opts APIKeysOptions) (*APIKeysData, error) {
	if opts.Page < 1 {
		opts.Page = 1
	}
	if opts.PageSize < 1 || opts.PageSize > 50 {
		opts.PageSize = 50
	}

	// Build WHERE clause
	where := "ak.user_id = $1::uuid AND ak.deleted_at IS NULL"
	if opts.ExcludeEnterprise {
		where += " AND NOT (ak.key_type = 'enterprise' AND ak.enterprise_id IS NOT NULL)"
	}

	// Count total
	var total int
	countSQL := "SELECT COUNT(*) FROM api_keys ak WHERE " + where
	_ = database.QueryRow(ctx, countSQL, userID).Scan(&total)

	// Build main query
	selectCols := `ak.id::text, ak.name, ak.key_prefix, ak.status, ak.access_mode, ak.allowed_models,
		ak.rate_limit_rpm, ak.rate_limit_tpm, ak.total_requests, ak.total_tokens, ak.created_at,
		ak.group_id::text, ak.key_type, ak.billing_mode, ak.current_model,
		ak.model_routing_mode, ak.selected_models, ak.group_priority,
		COALESCE(g.name, '') as group_name, ak.expires_at`

	joins := `FROM api_keys ak
		LEFT JOIN user_groups g ON ak.group_id = g.id`

	if opts.IncludeStats {
		selectCols += `,
		COALESCE(month_stats.month_requests, 0) as month_requests,
		COALESCE(month_stats.month_tokens, 0) as month_tokens,
		COALESCE(today_stats.today_requests, 0) as today_requests,
		COALESCE(today_stats.today_tokens, 0) as today_tokens`
		// "today"/"this month" stats use the DB CURRENT_DATE (DB session
		// timezone, expected to be UTC). Kept consistent with the dashboard
		// and usage-summary endpoints.
		joins += `
		LEFT JOIN LATERAL (
			SELECT COUNT(*) as month_requests,
			       COALESCE(SUM(input_tokens + output_tokens), 0) as month_tokens
			FROM usage_records
			WHERE user_api_key_id = ak.id
			  AND created_at >= DATE_TRUNC('month', CURRENT_DATE)
		) month_stats ON true
		LEFT JOIN LATERAL (
			SELECT COUNT(*) as today_requests,
			       COALESCE(SUM(input_tokens + output_tokens), 0) as today_tokens
			FROM usage_records
			WHERE user_api_key_id = ak.id
			  AND created_at >= CURRENT_DATE
		) today_stats ON true`
	}

	querySQL := "SELECT " + selectCols + " " + joins + " WHERE " + where + " ORDER BY ak.created_at DESC"
	args := []interface{}{userID}
	argIdx := 2

	// Apply limit / pagination
	if opts.Limit > 0 {
		querySQL += " LIMIT $" + itoa(argIdx)
		args = append(args, opts.Limit)
	} else if opts.Page > 0 && opts.PageSize > 0 {
		offset := (opts.Page - 1) * opts.PageSize
		querySQL += " LIMIT $" + itoa(argIdx) + " OFFSET $" + itoa(argIdx+1)
		args = append(args, opts.PageSize, offset)
	}

	rows, err := database.Query(ctx, querySQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []APIKeyItem
	for rows.Next() {
		var k APIKeyItem
		var createdAt sql.NullTime
		var selectedModels, allowedModels, groupPriority interface{}

		scanArgs := []interface{}{
			&k.ID, &k.Name, &k.KeyPrefix, &k.Status, &k.AccessMode, &allowedModels,
			&k.RateLimitRPM, &k.RateLimitTPM, &k.TotalRequests, &k.TotalTokens, &createdAt,
			&k.GroupID, &k.KeyType, &k.BillingMode, &k.CurrentModel,
			&k.ModelRoutingMode, &selectedModels, &groupPriority, &k.GroupName, &k.ExpiresAt,
		}
		if opts.IncludeStats {
			scanArgs = append(scanArgs, &k.MonthRequests, &k.MonthTokens, &k.TodayRequests, &k.TodayTokens)
		}
		if err := rows.Scan(scanArgs...); err != nil {
			continue
		}
		if createdAt.Valid {
			k.CreatedAt = createdAt.Time
		}
		k.AllowedModels = allowedModels
		k.SelectedModels = selectedModels
		k.GroupPriority = groupPriority
		keys = append(keys, k)
	}

	totalPages := 1
	if opts.PageSize > 0 && opts.Limit == 0 {
		totalPages = (total + opts.PageSize - 1) / opts.PageSize
		if totalPages < 1 {
			totalPages = 1
		}
	}

	return &APIKeysData{
		Items: keys, Total: total, Page: opts.Page,
		PageSize: opts.PageSize, TotalPages: totalPages,
	}, nil
}

// itoa is a tiny helper to avoid strconv import just for query building.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	sign := n
	if sign < 0 {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte(n%10) + '0'
		n /= 10
	}
	if sign < 0 {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// AssignedEnterpriseKeysOptions controls pagination for GetAssignedEnterpriseKeysData.
type AssignedEnterpriseKeysOptions struct {
	Page     int
	PageSize int
}

// AssignedEnterpriseKeysData holds paginated enterprise key results.
type AssignedEnterpriseKeysData struct {
	Items      []AssignedEnterpriseKey
	Total      int
	Page       int
	PageSize   int
	TotalPages int
}

// GetAssignedEnterpriseKeysData fetches enterprise keys assigned to a user with pagination.
func GetAssignedEnterpriseKeysData(ctx context.Context, database *db.DB, userID string, opts AssignedEnterpriseKeysOptions) (*AssignedEnterpriseKeysData, error) {
	if opts.Page < 1 {
		opts.Page = 1
	}
	if opts.PageSize < 1 || opts.PageSize > 50 {
		opts.PageSize = 50
	}

	where := `(k.assigned_user_id = $1::uuid OR (k.user_id = $1::uuid AND k.assigned_user_id IS NULL))
		   AND k.key_type = 'enterprise' AND k.enterprise_id IS NOT NULL AND k.deleted_at IS NULL`

	var total int
	countSQL := "SELECT COUNT(*) FROM api_keys k WHERE " + where
	_ = database.QueryRow(ctx, countSQL, userID).Scan(&total)

	offset := (opts.Page - 1) * opts.PageSize
	rows, err := database.Query(ctx,
		`SELECT k.id::text, COALESCE(k.name, ''), k.key_prefix, k.status,
		 COALESCE(e.name, '') as enterprise_name,
		 (COALESCE(k.user_id = k.assigned_user_id, false)) as is_self_created,
		 k.created_at,
		 CASE WHEN k.selected_models::text IS NOT NULL AND k.selected_models::text NOT IN ('[]', 'null', '')
		      THEN k.selected_models::text
		      WHEN k.allowed_models::text IS NOT NULL AND k.allowed_models::text NOT IN ('[]', 'null', '')
		      THEN k.allowed_models::text
		      WHEN g.models::text IS NOT NULL AND g.models::text NOT IN ('[]', 'null', '')
		      THEN g.models::text
		      ELSE (SELECT COALESCE(jsonb_agg(DISTINCT m)::text, '[]')
		            FROM (SELECT jsonb_array_elements_text(eg.models) AS m
		                  FROM user_groups eg
		                  WHERE eg.enterprise_id = k.enterprise_id AND eg.deleted_at IS NULL) sub)
		 END,
		 (SELECT COUNT(DISTINCT m)
		  FROM (SELECT jsonb_array_elements_text(eg.models) AS m
		        FROM user_groups eg
		        WHERE eg.enterprise_id = k.enterprise_id AND eg.deleted_at IS NULL) sub),
		 COALESCE(month_stats.month_requests, 0),
		 COALESCE(month_stats.month_tokens, 0),
		 COALESCE(k.rate_limit_rpm, 0),
		 k.expires_at,
		 COALESCE(k.model_routing_mode, 'single')
		 FROM api_keys k
		 LEFT JOIN enterprises e ON k.enterprise_id = e.id
		 LEFT JOIN user_groups g ON k.group_id = g.id
		 LEFT JOIN LATERAL (
			 SELECT COUNT(*) as month_requests,
			        COALESCE(SUM(input_tokens + output_tokens), 0) as month_tokens
			 FROM usage_records
			 WHERE user_api_key_id = k.id
			   AND created_at >= DATE_TRUNC('month', CURRENT_DATE)
		 ) month_stats ON true
		 WHERE `+where+`
		 ORDER BY k.created_at DESC
		 LIMIT $2 OFFSET $3`,
		userID, opts.PageSize, offset,
	)
	if err != nil {
		logQueryError(ctx, "GetAssignedEnterpriseKeysData", err)
		return &AssignedEnterpriseKeysData{Items: []AssignedEnterpriseKey{}, Page: opts.Page, PageSize: opts.PageSize, TotalPages: 1}, nil
	}
	defer rows.Close()

	var keys []AssignedEnterpriseKey
	for rows.Next() {
		var k AssignedEnterpriseKey
		var models *string
		if err := rows.Scan(&k.ID, &k.Name, &k.KeyPrefix, &k.Status, &k.EnterpriseName, &k.IsSelfCreated, &k.CreatedAt, &models, &k.TotalModels, &k.MonthRequests, &k.MonthTokens, &k.RateLimitRPM, &k.ExpiresAt, &k.ModelRoutingMode); err == nil {
			if models != nil && *models != "" {
				_ = json.Unmarshal([]byte(*models), &k.ModelList)
			}
			keys = append(keys, k)
		}
	}

	totalPages := 1
	if opts.PageSize > 0 {
		totalPages = (total + opts.PageSize - 1) / opts.PageSize
		if totalPages < 1 {
			totalPages = 1
		}
	}

	return &AssignedEnterpriseKeysData{
		Items: keys, Total: total, Page: opts.Page,
		PageSize: opts.PageSize, TotalPages: totalPages,
	}, nil
}

// AssignedEnterpriseKey represents an enterprise key assigned to the user.
type AssignedEnterpriseKey struct {
	ID               string
	Name             string
	KeyPrefix        string
	Status           string
	EnterpriseName   string
	IsSelfCreated    bool
	CreatedAt        time.Time
	Models           *string
	ModelList        []string
	TotalModels      int
	MonthRequests    int
	MonthTokens      int64
	RateLimitRPM     int
	ExpiresAt        *time.Time
	ModelRoutingMode string
}

// RecentKey holds a single recent API key record (raw data, no formatting).
type RecentKey struct {
	Name      string
	KeyPrefix string
	Status    string
	LastUsed  time.Time
}

// GetRecentKeysData fetches the 5 most recently used API keys for a user.
func GetRecentKeysData(ctx context.Context, database *db.DB, userID string) ([]RecentKey, error) {
	rows, err := database.Query(ctx,
		`SELECT ak.name, ak.key_prefix, ak.status, ak.updated_at
		 FROM api_keys ak
		 WHERE ak.user_id = $1::uuid AND ak.deleted_at IS NULL
		 ORDER BY ak.updated_at DESC LIMIT 5`,
		userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []RecentKey
	for rows.Next() {
		var k RecentKey
		var lastUsed sql.NullTime
		if rows.Scan(&k.Name, &k.KeyPrefix, &k.Status, &lastUsed) == nil {
			if lastUsed.Valid {
				k.LastUsed = lastUsed.Time
			}
			keys = append(keys, k)
		}
	}
	return keys, nil
}

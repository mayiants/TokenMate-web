package query

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/db"
)

// ModelGroupHealthRow is an aggregated health row for a model + user group.
type ModelGroupHealthRow struct {
	Model           string
	ModelType       string
	GroupID         string
	GroupName       string
	RequestCount    int
	SuccessCount    int
	FailureCount    int
	AuthErrorCount  int
	InvalidReqCount int
	Error4xxCount   int
	AvgLatencyMs    int
	Availability    float64 // -1 means no data
	Status          string  // healthy/degraded/unhealthy/unknown
	LastBucket      time.Time
}

// ProviderModelHealthRow is an aggregated health row for a provider + model.
type ProviderModelHealthRow struct {
	Provider        string
	Model           string
	ModelType       string
	RequestCount    int
	SuccessCount    int
	FailureCount    int
	AuthErrorCount  int
	InvalidReqCount int
	Error4xxCount   int
	AvgLatencyMs    int
	Availability    float64
	Status          string
	LastBucket      time.Time
}

// UpstreamKeyMetric holds per-key operational metrics for the key ops console.
type UpstreamKeyMetric struct {
	ID               string
	Provider         string
	KeyPreview       string
	Enabled          bool
	ValidationStatus string
	LastValidatedAt  *time.Time
	RPMLimit         int
	TPMLimit         int
	QuotaLimit       int64
	QuotaUsed        int64
	RequestCount     int
	ErrorCount       int
	TokenCount       int64
	AvgLatencyMs     int
	LatencyTrendJSON []byte
	ErrorDistJSON    []byte
}

// LatencyPoint represents a single day's latency for a key.
type LatencyPoint struct {
	Date         string `json:"date"`
	AvgLatencyMs int    `json:"avg_latency_ms"`
	RequestCount int    `json:"request_count"`
}

// ErrorBucket represents an error category count for a key.
type ErrorBucket struct {
	Error string `json:"error"`
	Count int    `json:"count"`
}

// NormalizePeriod maps unified UI period names (1day/7days/30days/quarter) to the
// canonical backend values (24h/7d/30d/90d). Unknown values are returned as-is.
func NormalizePeriod(period string) string {
	switch period {
	case "1d", "1day":
		return "24h"
	case "7d", "7days":
		return "7d"
	case "15d", "15days":
		return "15d"
	case "30d", "30days":
		return "30d"
	case "90d", "quarter":
		return "90d"
	default:
		return period
	}
}

// ParsePeriod converts a period string such as 24h/7d or 1day/7days into a duration.
func ParsePeriod(period string) (time.Duration, error) {
	switch NormalizePeriod(period) {
	case "90m":
		return 90 * time.Minute, nil
	case "24h":
		return 24 * time.Hour, nil
	case "7d":
		return 7 * 24 * time.Hour, nil
	case "15d":
		return 15 * 24 * time.Hour, nil
	case "30d":
		return 30 * 24 * time.Hour, nil
	case "90d":
		return 90 * 24 * time.Hour, nil
	default:
		return 24 * time.Hour, nil
	}
}

// UserModelGroupPair represents a model + group that a user may access.
type UserModelGroupPair struct {
	Model   string
	GroupID string
}

// GetUserAccessibleModelGroups returns the distinct (model, group_id) pairs a
// non-admin user can actually use through their active API keys. Admin users
// (super_admin/system_admin) receive nil, which callers interpret as
// unrestricted access.
func GetUserAccessibleModelGroups(ctx context.Context, database *db.DB, userID, role string) ([]UserModelGroupPair, error) {
	if role == "super_admin" || role == "system_admin" {
		return nil, nil
	}

	rows, err := database.Query(ctx, `
		SELECT ak.group_id::text, g.models, ak.allowed_models
		FROM api_keys ak
		JOIN user_groups g ON g.id = ak.group_id
		WHERE (ak.user_id = $1::uuid OR ak.assigned_user_id = $1::uuid)
		  AND ak.deleted_at IS NULL
		  AND ak.status = 'active'
		  AND g.deleted_at IS NULL
		  AND g.status = 'active'
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	pairSet := make(map[UserModelGroupPair]struct{})
	for rows.Next() {
		var groupID string
		var groupModelsJSON, allowedModelsJSON []byte
		if err := rows.Scan(&groupID, &groupModelsJSON, &allowedModelsJSON); err != nil {
			continue
		}

		groupModels := parseJSONStringArray(groupModelsJSON)
		allowedModels := parseJSONStringArray(allowedModelsJSON)

		effective := allowedModels
		if len(allowedModels) == 0 {
			effective = groupModels
		} else if len(groupModels) > 0 {
			groupSet := make(map[string]struct{}, len(groupModels))
			for _, m := range groupModels {
				groupSet[m] = struct{}{}
			}
			filtered := make([]string, 0, len(allowedModels))
			for _, m := range allowedModels {
				if _, ok := groupSet[m]; ok {
					filtered = append(filtered, m)
				}
			}
			effective = filtered
		}

		for _, model := range effective {
			if model == "" {
				continue
			}
			pairSet[UserModelGroupPair{Model: model, GroupID: groupID}] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	pairs := make([]UserModelGroupPair, 0, len(pairSet))
	for p := range pairSet {
		pairs = append(pairs, p)
	}
	return pairs, nil
}

func parseJSONStringArray(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	var arr []string
	if err := json.Unmarshal(b, &arr); err != nil {
		return nil
	}
	return arr
}

// ListModelGroupHealth aggregates model_group_health_stats over the requested period.
// A nil allowedPairs means unrestricted (admin). A non-nil slice restricts results
// to exactly those (model, group_id) combinations — an empty slice means no access,
// so keyless users must not receive aggregated stats of other tenants.
func ListModelGroupHealth(ctx context.Context, database *db.DB, period, modelFilter, groupFilter string, allowedPairs []UserModelGroupPair) ([]ModelGroupHealthRow, error) {
	window, err := ParsePeriod(period)
	if err != nil {
		return nil, err
	}

	allowedModels := make([]string, 0, len(allowedPairs))
	allowedGroups := make([]string, 0, len(allowedPairs))
	for _, p := range allowedPairs {
		allowedModels = append(allowedModels, p.Model)
		allowedGroups = append(allowedGroups, p.GroupID)
	}
	restricted := allowedPairs != nil

	rows, err := database.Query(ctx, `
		WITH allowed AS (
			SELECT unnest($4::text[]) AS model, unnest($5::text[]) AS group_id
		)
		SELECT m.model, m.group_id, COALESCE(g.name, m.group_id),
			COALESCE(
				MAX(CASE WHEN COALESCE(sm.capabilities->>'supported_types', '') ILIKE ANY(ARRAY['%image%', '%video%', '%audio%', '%multimodal%'])
						 THEN 'multimodal' END),
				MAX(NULLIF(sm.aimodels_data->>'type', '')),
				MAX(NULLIF(sm.capabilities->>'type', '')),
				'text'
			) as model_type,
			COALESCE(SUM(m.request_count), 0),
			COALESCE(SUM(m.success_count), 0),
			COALESCE(SUM(m.failure_count), 0),
			COALESCE(SUM(m.auth_error_count), 0),
			COALESCE(SUM(m.invalid_request_count), 0),
			COALESCE(SUM(m.error_4xx_count), 0),
			COALESCE(SUM(m.total_latency_ms), 0),
			COALESCE(MAX(m.bucket), 'epoch'),
			CASE WHEN COALESCE(SUM(m.request_count), 0) > 0
				 THEN ROUND(SUM(m.success_count)::numeric * 100.0 / SUM(m.request_count), 2)
				 ELSE -1
			END
		FROM model_group_health_stats m
		LEFT JOIN user_groups g ON g.id::text = m.group_id
		LEFT JOIN service_models sm ON sm.status = 'active'
		  AND (sm.product_id = m.model OR sm.api_model_name = m.model)
		WHERE m.bucket >= NOW() - $1::interval
		  AND ($2 = '' OR m.model = $2)
		  AND ($3 = '' OR m.group_id = $3)
		  AND ($6 = false OR EXISTS (SELECT 1 FROM allowed a WHERE a.model = m.model AND a.group_id = m.group_id))
		GROUP BY m.model, m.group_id, g.name
		ORDER BY m.model, m.group_id
	`, fmt.Sprintf("%f seconds", window.Seconds()), modelFilter, groupFilter, allowedModels, allowedGroups, restricted)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []ModelGroupHealthRow
	for rows.Next() {
		var r ModelGroupHealthRow
		var totalLatency int64
		if err := rows.Scan(&r.Model, &r.GroupID, &r.GroupName, &r.ModelType, &r.RequestCount, &r.SuccessCount, &r.FailureCount, &r.AuthErrorCount, &r.InvalidReqCount, &r.Error4xxCount, &totalLatency, &r.LastBucket, &r.Availability); err != nil {
			continue
		}
		if r.RequestCount > 0 {
			r.AvgLatencyMs = int(totalLatency / int64(r.RequestCount))
		}
		r.Status = DeriveInternalStatus(r.ModelType, r.RequestCount, r.FailureCount, r.AuthErrorCount+r.InvalidReqCount+r.Error4xxCount, r.AvgLatencyMs)
		results = append(results, r)
	}
	return results, rows.Err()
}

// ListProviderModelHealth aggregates provider_health_stats_model over the requested period.
func ListProviderModelHealth(ctx context.Context, database *db.DB, period, providerFilter, modelFilter string) ([]ProviderModelHealthRow, error) {
	window, err := ParsePeriod(period)
	if err != nil {
		return nil, err
	}

	rows, err := database.Query(ctx, `
		SELECT ph.provider, ph.model,
			COALESCE(sm.model_type, 'text') as model_type,
			COALESCE(SUM(ph.request_count), 0),
			COALESCE(SUM(ph.success_count), 0),
			COALESCE(SUM(ph.failure_count), 0),
			COALESCE(SUM(ph.auth_error_count), 0),
			COALESCE(SUM(ph.invalid_request_count), 0),
			COALESCE(SUM(ph.error_4xx_count), 0),
			COALESCE(SUM(ph.total_latency_ms), 0),
			COALESCE(MAX(ph.bucket), 'epoch'),
			CASE WHEN COALESCE(SUM(ph.request_count), 0) > 0
				 THEN ROUND(SUM(ph.success_count)::numeric * 100.0 / SUM(ph.request_count), 2)
				 ELSE -1
			END
		FROM provider_health_stats_model ph
		LEFT JOIN LATERAL (
			SELECT
				CASE
					WHEN COALESCE(capabilities->>'supported_types', '') ILIKE ANY(ARRAY['%image%', '%video%', '%audio%', '%multimodal%'])
						THEN 'multimodal'
					ELSE COALESCE(NULLIF(aimodels_data->>'type', ''), capabilities->>'type', 'text')
				END as model_type
			FROM service_models
			WHERE status = 'active'
			  AND (product_id = ph.model OR api_model_name = ph.model)
			ORDER BY (provider = ph.provider) DESC
			LIMIT 1
		) sm ON true
		WHERE ph.bucket >= NOW() - $1::interval
		  AND ($2 = '' OR ph.provider = $2)
		  AND ($3 = '' OR ph.model = $3)
		GROUP BY ph.provider, ph.model, sm.model_type
		ORDER BY ph.provider, ph.model
	`, fmt.Sprintf("%f seconds", window.Seconds()), providerFilter, modelFilter)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []ProviderModelHealthRow
	for rows.Next() {
		var r ProviderModelHealthRow
		var totalLatency int64
		if err := rows.Scan(&r.Provider, &r.Model, &r.ModelType, &r.RequestCount, &r.SuccessCount, &r.FailureCount, &r.AuthErrorCount, &r.InvalidReqCount, &r.Error4xxCount, &totalLatency, &r.LastBucket, &r.Availability); err != nil {
			continue
		}
		if r.RequestCount > 0 {
			r.AvgLatencyMs = int(totalLatency / int64(r.RequestCount))
		}
		r.Status = DeriveInternalStatus(r.ModelType, r.RequestCount, r.FailureCount, r.AuthErrorCount+r.InvalidReqCount+r.Error4xxCount, r.AvgLatencyMs)
		results = append(results, r)
	}
	return results, rows.Err()
}

// ListUpstreamKeyMetrics augments upstream key list with usage/error heat over the chosen period.
func ListUpstreamKeyMetrics(ctx context.Context, database *db.DB, period string, page, pageSize int) ([]UpstreamKeyMetric, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 50
	}
	// Allow large page sizes so the UI can fetch all keys for client-side filtering.
	const maxPageSize = 5000
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	offset := (page - 1) * pageSize

	windowDays := 7
	switch period {
	case "24h":
		windowDays = 1
	case "7d":
		windowDays = 7
	case "30d":
		windowDays = 30
	case "90d":
		windowDays = 90
	default:
	}
	intervalDays := windowDays - 1
	if intervalDays < 0 {
		intervalDays = 0
	}

	rows, err := database.Query(ctx, `
		SELECT k.id::text, k.provider, k.key_preview, k.enabled,
		       COALESCE(k.validation_status, 'pending'),
		       k.validation_at,
		       COALESCE(k.rpm_limit, 0), COALESCE(k.tpm_limit, 0), COALESCE(k.quota_limit, 0), COALESCE(k.quota_used, 0),
		       COALESCE(u.request_count, 0),
		       COALESCE(u.error_count, 0),
		       COALESCE(u.token_count, 0),
		       COALESCE(u.avg_latency_ms, 0),
		       COALESCE(lt.latency_trend, '[]'::jsonb),
		       COALESCE(ed.error_dist, '[]'::jsonb)
		FROM upstream_keys k
		LEFT JOIN LATERAL (
			SELECT COALESCE(SUM(request_count), 0) as request_count,
			       COALESCE(SUM(error_count), 0) as error_count,
			       COALESCE(SUM(input_tokens + output_tokens + cache_read_tokens), 0) as token_count,
			       CASE WHEN COALESCE(SUM(request_count), 0) > 0
			            THEN (SUM(total_latency_ms) / SUM(request_count))::int
			            ELSE 0
			       END as avg_latency_ms
			FROM upstream_key_usage
			WHERE upstream_key_id = k.id AND date >= CURRENT_DATE - $3::int
		) u ON true
		LEFT JOIN LATERAL (
			SELECT jsonb_agg(jsonb_build_object('date', date, 'avg_latency_ms', avg_latency_ms, 'request_count', request_count) ORDER BY date) as latency_trend
			FROM upstream_key_usage
			WHERE upstream_key_id = k.id AND date >= CURRENT_DATE - $3::int
		) lt ON true
		LEFT JOIN LATERAL (
			SELECT jsonb_agg(jsonb_build_object('error', error, 'count', cnt) ORDER BY cnt DESC) as error_dist
			FROM (
				SELECT COALESCE(NULLIF(error, ''), 'unknown') as error, COUNT(*) as cnt
				FROM usage_records
				WHERE upstream_key_id = k.id::text
				  AND created_at >= NOW() - ($4::int || ' days')::interval
				  AND error IS NOT NULL AND error <> ''
				GROUP BY error
			) t
		) ed ON true
		WHERE k.deleted_at IS NULL
		ORDER BY k.provider, k.created_at DESC
		LIMIT $1 OFFSET $2
	`, pageSize, offset, intervalDays, windowDays)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []UpstreamKeyMetric
	for rows.Next() {
		var m UpstreamKeyMetric
		if err := rows.Scan(&m.ID, &m.Provider, &m.KeyPreview, &m.Enabled,
			&m.ValidationStatus, &m.LastValidatedAt,
			&m.RPMLimit, &m.TPMLimit, &m.QuotaLimit, &m.QuotaUsed,
			&m.RequestCount, &m.ErrorCount, &m.TokenCount,
			&m.AvgLatencyMs, &m.LatencyTrendJSON, &m.ErrorDistJSON); err != nil {
			continue
		}
		results = append(results, m)
	}
	return results, rows.Err()
}

// MinHealthSampleSize is the minimum number of effective (model-attributable)
// requests in the window before the error rate / latency is trusted enough to
// judge health. Below it the status is "unknown" so a handful of requests —
// especially ones that only failed for client/credential reasons — cannot
// paint a model red. Kept in sync with the bucket writer's floor.
const MinHealthSampleSize = 10

// DeriveInternalStatus classifies a model/provider's health over a window from
// aggregated counters. It applies the same thresholds the webhook bucket writer
// uses. modelType relaxes latency thresholds for generation-heavy modalities
// (image/video/audio) that naturally take longer than text models.
//
// excludedCount is the number of requests whose failure is NOT the model's
// fault and must be removed from both the numerator and the denominator:
//   - auth_error (401/403): the provider key was denied/expired — a credential
//     problem, not a model outage;
//   - invalid_request (400) and error_4xx: the CALLER sent a bad request.
//
// Counting these as "model down" produced large swaths of false red on the
// Status/health pages (e.g. a model with 2 requests that were both malformed
// client payloads showing "unavailable"). They remain recorded in their own
// *_count columns and are viewable on the health detail / usage pages.
//
// A window with fewer than MinHealthSampleSize effective requests returns
// "unknown" rather than a red/degraded verdict.
func DeriveInternalStatus(modelType string, requestCount, failureCount, excludedCount, avgLatencyMs int) string {
	if requestCount == 0 {
		return "unknown"
	}
	// Remove client/credential failures from both numerator and denominator.
	realFailures := failureCount - excludedCount
	if realFailures < 0 {
		realFailures = 0
	}
	effectiveRequests := requestCount - excludedCount
	if effectiveRequests < MinHealthSampleSize {
		// Not enough model-attributable traffic to judge — don't flag red.
		return "unknown"
	}
	errRate := float64(realFailures) / float64(effectiveRequests)

	// Latency thresholds: relaxed for image/video/audio generation models,
	// which routinely take tens of seconds.
	healthyLatency := 15000
	switch modelType {
	case "image", "video", "audio", "multimodal":
		healthyLatency = 90000
	}

	// Error rate is the primary signal and the ONLY thing that can mark a model
	// "unhealthy": if real (model-attributable) errors exceed the threshold the
	// model is failing requests.
	if errRate >= 0.20 {
		return "unhealthy"
	}
	// Error rate is acceptable (< 20%). Latency can only DOWNGRADE to
	// "degraded" — a slow but succeeding model is working, not down. It must
	// never be reported "unhealthy"/"unavailable" on latency alone.
	if errRate >= 0.05 {
		return "degraded"
	}
	// Healthy error rate (< 5%): latency decides healthy vs degraded.
	if avgLatencyMs < healthyLatency {
		return "healthy"
	}
	return "degraded"
}

// modelTypeCache memoizes model-name -> type so trend rendering can apply the
// right latency thresholds (media models are legitimately slow) without a
// per-row DB hit. Populated lazily; cheap and process-lifetime.
var (
	modelTypeCacheMu sync.RWMutex
	modelTypeCache   = map[string]string{}
)

// lookupModelType returns the capability type (text/image/video/audio) for a
// model name, caching the result. Unknown models default to "text".
func lookupModelType(ctx context.Context, database *db.DB, model string) string {
	modelTypeCacheMu.RLock()
	if t, ok := modelTypeCache[model]; ok {
		modelTypeCacheMu.RUnlock()
		return t
	}
	modelTypeCacheMu.RUnlock()

	var t string
	_ = database.QueryRow(ctx,
		`SELECT COALESCE(MAX(NULLIF(aimodels_data->>'type','')), MAX(NULLIF(capabilities->>'type','')), 'text')
		 FROM service_models WHERE (api_model_name=$1 OR product_id=$1) AND deleted_at IS NULL`, model).Scan(&t)
	if t == "" {
		t = "text"
	}
	modelTypeCacheMu.Lock()
	modelTypeCache[model] = t
	modelTypeCacheMu.Unlock()
	return t
}

// HealthHistoryPoint is a single aggregated bucket for trend/heatmap rendering.
type HealthHistoryPoint struct {
	Timestamp            time.Time `json:"timestamp"`
	Availability         float64   `json:"availability"`
	RequestCount         int       `json:"request_count"`
	SuccessCount         int       `json:"success_count"`
	FailureCount         int       `json:"failure_count"`
	TimeoutCount         int       `json:"timeout_count"`
	RateLimitCount       int       `json:"rate_limit_count"`
	Error5xxCount        int       `json:"error_5xx_count"`
	Error4xxCount        int       `json:"error_4xx_count"`
	AuthErrorCount       int       `json:"auth_error_count"`
	NetworkErrorCount    int       `json:"network_error_count"`
	ResponseTimeoutCount int       `json:"response_timeout_count"`
	ContentMismatchCount int       `json:"content_mismatch_count"`
	InvalidRequestCount  int       `json:"invalid_request_count"`
	AvgLatencyMs         int       `json:"avg_latency_ms"`
	Status               string    `json:"status"`
}

// ModelGroupHealthHistory returns per-bucket history for each model+group combo.
// For 24h it uses hourly buckets; for longer periods it uses daily buckets.
// A nil allowedPairs means unrestricted (admin); a non-nil slice (even empty)
// restricts results to exactly those (model, group_id) combinations.
func ModelGroupHealthHistory(ctx context.Context, database *db.DB, period, modelFilter, groupFilter string, allowedPairs []UserModelGroupPair) (map[string][]HealthHistoryPoint, error) {
	window, err := ParsePeriod(period)
	if err != nil {
		return nil, err
	}

	trunc := "date_trunc('day', bucket)"
	if period == "24h" || period == "90m" {
		trunc = "date_trunc('hour', bucket)"
	}

	allowedModels := make([]string, 0, len(allowedPairs))
	allowedGroups := make([]string, 0, len(allowedPairs))
	for _, p := range allowedPairs {
		allowedModels = append(allowedModels, p.Model)
		allowedGroups = append(allowedGroups, p.GroupID)
	}
	restricted := allowedPairs != nil

	rows, err := database.Query(ctx, `
		WITH allowed AS (
			SELECT unnest($4::text[]) AS model, unnest($5::text[]) AS group_id
		)
		SELECT model, group_id, `+trunc+` AS ts,
			COALESCE(SUM(request_count), 0),
			COALESCE(SUM(success_count), 0),
			COALESCE(SUM(failure_count), 0),
			COALESCE(SUM(timeout_count), 0),
			COALESCE(SUM(rate_limit_count), 0),
			COALESCE(SUM(error_5xx_count), 0),
			COALESCE(SUM(error_4xx_count), 0),
			COALESCE(SUM(auth_error_count), 0),
			COALESCE(SUM(network_error_count), 0),
			COALESCE(SUM(response_timeout_count), 0),
			COALESCE(SUM(content_mismatch_count), 0),
			COALESCE(SUM(invalid_request_count), 0),
			COALESCE(SUM(total_latency_ms), 0)
		FROM model_group_health_stats m
		WHERE m.bucket >= NOW() - $1::interval
		  AND ($2 = '' OR m.model = $2)
		  AND ($3 = '' OR m.group_id = $3)
		  AND ($6 = false OR EXISTS (SELECT 1 FROM allowed a WHERE a.model = m.model AND a.group_id = m.group_id))
		GROUP BY model, group_id, ts
		ORDER BY model, group_id, ts
	`, fmt.Sprintf("%f seconds", window.Seconds()), modelFilter, groupFilter, allowedModels, allowedGroups, restricted)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	raw := make(map[string]map[time.Time]HealthHistoryPoint)
	for rows.Next() {
		var p HealthHistoryPoint
		var model, group string
		var totalLatency int64
		if err := rows.Scan(&model, &group, &p.Timestamp,
			&p.RequestCount, &p.SuccessCount, &p.FailureCount,
			&p.TimeoutCount, &p.RateLimitCount, &p.Error5xxCount, &p.Error4xxCount,
			&p.AuthErrorCount, &p.NetworkErrorCount, &p.ResponseTimeoutCount,
			&p.ContentMismatchCount, &p.InvalidRequestCount, &totalLatency); err != nil {
			continue
		}
		if p.RequestCount > 0 {
			p.Availability = float64(p.SuccessCount) * 100.0 / float64(p.RequestCount)
			p.AvgLatencyMs = int(totalLatency / int64(p.RequestCount))
		} else {
			p.Availability = -1
		}
		p.Status = DeriveInternalStatus(lookupModelType(ctx, database, model), p.RequestCount, p.FailureCount, p.AuthErrorCount, p.AvgLatencyMs)
		key := model + "|" + group
		if raw[key] == nil {
			raw[key] = make(map[time.Time]HealthHistoryPoint)
		}
		raw[key][p.Timestamp.UTC()] = p
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Fill missing buckets so the heatmap/sparkline always has a complete series.
	unit := 24 * time.Hour
	if period == "24h" || period == "90m" {
		unit = time.Hour
	}
	now := time.Now().UTC()
	end := now.Truncate(unit)
	start := end.Add(-window + unit)
	if start.After(end) {
		start = end
	}

	result := make(map[string][]HealthHistoryPoint)
	for key, pointMap := range raw {
		var series []HealthHistoryPoint
		for t := start; !t.After(end); t = t.Add(unit) {
			if p, ok := pointMap[t]; ok {
				series = append(series, p)
				continue
			}
			series = append(series, HealthHistoryPoint{
				Timestamp:    t,
				Availability: -1,
				Status:       "unknown",
			})
		}
		result[key] = series
	}
	return result, nil
}

// ProviderModelHealthHistory returns per-bucket history for each provider+model combo.
func ProviderModelHealthHistory(ctx context.Context, database *db.DB, period, providerFilter, modelFilter string) (map[string][]HealthHistoryPoint, error) {
	window, err := ParsePeriod(period)
	if err != nil {
		return nil, err
	}

	trunc := "date_trunc('day', bucket)"
	if period == "24h" || period == "90m" {
		trunc = "date_trunc('hour', bucket)"
	}

	rows, err := database.Query(ctx, `
		SELECT provider, model, `+trunc+` AS ts,
			COALESCE(SUM(request_count), 0),
			COALESCE(SUM(success_count), 0),
			COALESCE(SUM(failure_count), 0),
			COALESCE(SUM(timeout_count), 0),
			COALESCE(SUM(rate_limit_count), 0),
			COALESCE(SUM(error_5xx_count), 0),
			COALESCE(SUM(error_4xx_count), 0),
			COALESCE(SUM(auth_error_count), 0),
			COALESCE(SUM(network_error_count), 0),
			COALESCE(SUM(response_timeout_count), 0),
			COALESCE(SUM(content_mismatch_count), 0),
			COALESCE(SUM(invalid_request_count), 0),
			COALESCE(SUM(total_latency_ms), 0)
		FROM provider_health_stats_model
		WHERE bucket >= NOW() - $1::interval
		  AND ($2 = '' OR provider = $2)
		  AND ($3 = '' OR model = $3)
		GROUP BY provider, model, ts
		ORDER BY provider, model, ts
	`, fmt.Sprintf("%f seconds", window.Seconds()), providerFilter, modelFilter)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	raw := make(map[string]map[time.Time]HealthHistoryPoint)
	for rows.Next() {
		var p HealthHistoryPoint
		var provider, model string
		var totalLatency int64
		if err := rows.Scan(&provider, &model, &p.Timestamp,
			&p.RequestCount, &p.SuccessCount, &p.FailureCount,
			&p.TimeoutCount, &p.RateLimitCount, &p.Error5xxCount, &p.Error4xxCount,
			&p.AuthErrorCount, &p.NetworkErrorCount, &p.ResponseTimeoutCount,
			&p.ContentMismatchCount, &p.InvalidRequestCount, &totalLatency); err != nil {
			continue
		}
		if p.RequestCount > 0 {
			p.Availability = float64(p.SuccessCount) * 100.0 / float64(p.RequestCount)
			p.AvgLatencyMs = int(totalLatency / int64(p.RequestCount))
		} else {
			p.Availability = -1
		}
		p.Status = DeriveInternalStatus(lookupModelType(ctx, database, model), p.RequestCount, p.FailureCount, p.AuthErrorCount, p.AvgLatencyMs)
		key := provider + "|" + model
		if raw[key] == nil {
			raw[key] = make(map[time.Time]HealthHistoryPoint)
		}
		raw[key][p.Timestamp.UTC()] = p
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	unit := 24 * time.Hour
	if period == "24h" || period == "90m" {
		unit = time.Hour
	}
	now := time.Now().UTC()
	end := now.Truncate(unit)
	start := end.Add(-window + unit)
	if start.After(end) {
		start = end
	}

	result := make(map[string][]HealthHistoryPoint)
	for key, pointMap := range raw {
		var series []HealthHistoryPoint
		for t := start; !t.After(end); t = t.Add(unit) {
			if p, ok := pointMap[t]; ok {
				series = append(series, p)
				continue
			}
			series = append(series, HealthHistoryPoint{
				Timestamp:    t,
				Availability: -1,
				Status:       "unknown",
			})
		}
		result[key] = series
	}
	return result, nil
}

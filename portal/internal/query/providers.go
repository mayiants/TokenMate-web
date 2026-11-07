package query

import (
	"context"

	"github.com/mayiants/ai-portal-go/internal/db"
)

// ProviderHealthItem holds provider health data for admin display.
type ProviderHealthItem struct {
	Provider     string
	Name         string
	Enabled      bool
	RequestCount int
	SuccessCount int
	FailureCount int
	AvgLatencyMs int
	ErrorRate    float64
	SuccessRate  float64
	Status       string
}

// AdminProviderHealthData holds provider health page data.
type AdminProviderHealthData struct {
	Providers []ProviderHealthItem
	Summary   struct {
		Total     int
		Healthy   int
		Degraded  int
		Unhealthy int
		Unknown   int
	}
}

// GetAdminProviderHealthData fetches provider health data for admin page rendering.
func GetAdminProviderHealthData(ctx context.Context, database *db.DB) (*AdminProviderHealthData, error) {
	data := &AdminProviderHealthData{
		Providers: []ProviderHealthItem{},
	}

	rows, err := database.Query(ctx, `
		SELECT p.id, p.name, p.enabled,
			COALESCE(SUM(h.request_count), 0) as req_count,
			COALESCE(SUM(h.success_count), 0) as success_count,
			COALESCE(SUM(h.failure_count), 0) as failure_count,
			COALESCE(SUM(h.total_latency_ms), 0) as total_latency,
			COALESCE(MAX(h.error_rate), 0) as error_rate,
			CASE
				WHEN MAX(CASE h.status WHEN 'unhealthy' THEN 3 WHEN 'degraded' THEN 2 WHEN 'healthy' THEN 1 ELSE 0 END) = 3 THEN 'unhealthy'
				WHEN MAX(CASE h.status WHEN 'unhealthy' THEN 3 WHEN 'degraded' THEN 2 WHEN 'healthy' THEN 1 ELSE 0 END) = 2 THEN 'degraded'
				WHEN MAX(CASE h.status WHEN 'unhealthy' THEN 3 WHEN 'degraded' THEN 2 WHEN 'healthy' THEN 1 ELSE 0 END) = 1 THEN 'healthy'
				WHEN COALESCE(k.valid_keys, 0) > 0 THEN 'healthy'
				WHEN COALESCE(k.invalid_keys, 0) > 0 THEN 'unhealthy'
				ELSE 'unknown'
			END as status
		FROM providers p
		LEFT JOIN provider_health_stats h ON h.provider = p.id
			AND h.bucket >= NOW() - INTERVAL '15 minutes'
		LEFT JOIN (
			SELECT provider,
				COUNT(*) FILTER (WHERE enabled AND validation_status = 'valid') as valid_keys,
				COUNT(*) FILTER (WHERE enabled AND validation_status IN ('invalid', 'failed')) as invalid_keys
			FROM upstream_keys WHERE deleted_at IS NULL GROUP BY provider
		) k ON k.provider = p.id
		GROUP BY p.id, p.name, p.enabled, k.valid_keys, k.invalid_keys
		ORDER BY p.sort_order, p.name
	`)
	if err != nil {
		return data, err
	}
	defer rows.Close()

	for rows.Next() {
		var item ProviderHealthItem
		var totalLatency int64
		if err := rows.Scan(&item.Provider, &item.Name, &item.Enabled,
			&item.RequestCount, &item.SuccessCount, &item.FailureCount,
			&totalLatency, &item.ErrorRate, &item.Status); err != nil {
			continue
		}
		if item.RequestCount > 0 {
			item.AvgLatencyMs = int(totalLatency / int64(item.RequestCount))
			item.SuccessRate = float64(item.SuccessCount) / float64(item.RequestCount) * 100
		}
		switch item.Status {
		case "healthy":
			data.Summary.Healthy++
		case "degraded":
			data.Summary.Degraded++
		case "unhealthy":
			data.Summary.Unhealthy++
		default:
			data.Summary.Unknown++
		}
		data.Providers = append(data.Providers, item)
	}

	data.Summary.Total = len(data.Providers)

	return data, nil
}

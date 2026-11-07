package query

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/internal/services"
)

// ReconciliationPeriod represents a supported shortcut or custom range.
type ReconciliationPeriod string

const (
	Period24h    ReconciliationPeriod = "24h"
	Period7d     ReconciliationPeriod = "7d"
	Period30d    ReconciliationPeriod = "30d"
	Period90d    ReconciliationPeriod = "90d"
	PeriodCustom ReconciliationPeriod = "custom"
)

// ReconciliationRange holds the resolved date/time range for queries.
type ReconciliationRange struct {
	From time.Time
	To   time.Time
}

// ParseReconciliationPeriod resolves a period shortcut or custom dates.
func ParseReconciliationPeriod(period, dateFrom, dateTo string) (ReconciliationRange, error) {
	now := time.Now().UTC()
	r := ReconciliationRange{}

	switch ReconciliationPeriod(period) {
	case Period24h, "1d":
		// Align to the UTC day boundary like the multi-day periods: the
		// upstream side filters on the day-granular upstream_key_usage.date
		// while the billed side filters on created_at timestamps, so a
		// rolling 24h start would silently pull in the previous whole day
		// upstream (e.g. 1-day showing 2 days of upstream cost).
		r.From = now.Add(-24 * time.Hour).Truncate(24 * time.Hour)
		r.To = now
	case Period7d:
		r.From = now.Add(-7 * 24 * time.Hour).Truncate(24 * time.Hour)
		r.To = now
	case "15d":
		r.From = now.Add(-15 * 24 * time.Hour).Truncate(24 * time.Hour)
		r.To = now
	case Period30d:
		r.From = now.Add(-30 * 24 * time.Hour).Truncate(24 * time.Hour)
		r.To = now
	case Period90d, "quarter":
		r.From = now.Add(-90 * 24 * time.Hour).Truncate(24 * time.Hour)
		r.To = now
	default:
		from, err := time.Parse("2006-01-02", dateFrom)
		if err != nil {
			return r, fmt.Errorf("invalid date_from: %w", err)
		}
		to, err := time.Parse("2006-01-02", dateTo)
		if err != nil {
			return r, fmt.Errorf("invalid date_to: %w", err)
		}
		if to.Before(from) {
			return r, fmt.Errorf("date_to must be after date_from")
		}
		r.From = from.UTC()
		r.To = to.Add(24*time.Hour - time.Nanosecond).UTC()
	}

	return r, nil
}

// ReconciliationSummary holds the global upstream vs billed snapshot.
type ReconciliationSummary struct {
	Period                    string  `json:"period"`
	DateFrom                  string  `json:"date_from"`
	DateTo                    string  `json:"date_to"`
	UpstreamRequests          int64   `json:"upstream_requests"`
	UpstreamInputTokens       int64   `json:"upstream_input_tokens"`
	UpstreamOutputTokens      int64   `json:"upstream_output_tokens"`
	UpstreamCacheReadTokens   int64   `json:"upstream_cache_read_tokens"`
	UpstreamCacheWriteTokens  int64   `json:"upstream_cache_write_tokens"`
	UpstreamErrors            int64   `json:"upstream_errors"`
	BilledRequests            int64   `json:"billed_requests"`
	BilledInputTokens         int64   `json:"billed_input_tokens"`
	BilledOutputTokens        int64   `json:"billed_output_tokens"`
	BilledCachedInputTokens   int64   `json:"billed_cached_input_tokens"`
	BilledReasoningTokens     int64   `json:"billed_reasoning_tokens"`
	BilledCostQuota           int64   `json:"billed_cost_quota"`
	BilledCostUSD             float64 `json:"billed_cost_usd"`
	BilledInputCostUSD        float64 `json:"billed_input_cost_usd"`
	BilledOutputCostUSD       float64 `json:"billed_output_cost_usd"`
	BilledCacheCostUSD        float64 `json:"billed_cache_cost_usd"`
	BilledReasoningCostUSD    float64 `json:"billed_reasoning_cost_usd"`
	BilledFixedCostUSD        float64 `json:"billed_fixed_cost_usd"`
	EstimatedUpstreamCostUSD  float64 `json:"estimated_upstream_cost_usd"`
	UpstreamInputCostUSD      float64 `json:"upstream_input_cost_usd"`
	UpstreamOutputCostUSD     float64 `json:"upstream_output_cost_usd"`
	UpstreamCacheCostUSD      float64 `json:"upstream_cache_cost_usd"`
	UpstreamCacheWriteCostUSD float64 `json:"upstream_cache_write_cost_usd"`
	UpstreamReasoningCostUSD  float64 `json:"upstream_reasoning_cost_usd"`
	UpstreamFixedCostUSD      float64 `json:"upstream_fixed_cost_usd"`
	CostDiffUSD               float64 `json:"cost_diff_usd"`
	GrossMarginUSD            float64 `json:"gross_margin_usd"`
	GrossMarginPct            float64 `json:"gross_margin_pct"`
	ErrorRate                 float64 `json:"error_rate"`
}

// ReconciliationTrend holds aligned time series for a single metric.
type ReconciliationTrend struct {
	Metric   string    `json:"metric"`
	Labels   []string  `json:"labels"`
	Upstream []float64 `json:"upstream"`
	Billed   []float64 `json:"billed"`
}

// ReconciliationDetailRow is a single provider/model/key reconciliation line.
type ReconciliationDetailRow struct {
	Provider                  string  `json:"provider"`
	Model                     string  `json:"model"`
	UpstreamModel             string  `json:"upstream_model"`
	UpstreamKeyID             string  `json:"upstream_key_id"`
	KeyPreview                string  `json:"key_preview"`
	UpstreamRequests          int64   `json:"upstream_requests"`
	BilledRequests            int64   `json:"billed_requests"`
	RequestDiff               int64   `json:"request_diff"`
	UpstreamInputTokens       int64   `json:"upstream_input_tokens"`
	BilledInputTokens         int64   `json:"billed_input_tokens"`
	InputTokenDiff            int64   `json:"input_token_diff"`
	UpstreamOutputTokens      int64   `json:"upstream_output_tokens"`
	BilledOutputTokens        int64   `json:"billed_output_tokens"`
	OutputTokenDiff           int64   `json:"output_token_diff"`
	UpstreamCacheReadTokens   int64   `json:"upstream_cache_read_tokens"`
	UpstreamCacheWriteTokens  int64   `json:"upstream_cache_write_tokens"`
	BilledCachedInputTokens   int64   `json:"billed_cached_input_tokens"`
	BilledReasoningTokens     int64   `json:"billed_reasoning_tokens"`
	EstimatedUpstreamCostUSD  float64 `json:"upstream_cost_usd"`
	UpstreamInputCostUSD      float64 `json:"upstream_input_cost_usd"`
	UpstreamOutputCostUSD     float64 `json:"upstream_output_cost_usd"`
	UpstreamCacheCostUSD      float64 `json:"upstream_cache_cost_usd"`
	UpstreamCacheWriteCostUSD float64 `json:"upstream_cache_write_cost_usd"`
	UpstreamReasoningCostUSD  float64 `json:"upstream_reasoning_cost_usd"`
	UpstreamFixedCostUSD      float64 `json:"upstream_fixed_cost_usd"`
	BilledCostUSD             float64 `json:"billed_cost_usd"`
	BilledInputCostUSD        float64 `json:"billed_input_cost_usd"`
	BilledOutputCostUSD       float64 `json:"billed_output_cost_usd"`
	BilledCacheCostUSD        float64 `json:"billed_cache_cost_usd"`
	BilledReasoningCostUSD    float64 `json:"billed_reasoning_cost_usd"`
	BilledFixedCostUSD        float64 `json:"billed_fixed_cost_usd"`
	CostDiffUSD               float64 `json:"cost_diff_usd"`
	GrossMarginUSD            float64 `json:"gross_margin_usd"`
	GrossMarginPct            float64 `json:"gross_margin_pct"`
	ErrorCount                int64   `json:"error_count"`
	ErrorRate                 float64 `json:"error_rate"`
}

// ReconciliationFilters controls detail/trend queries.
type ReconciliationFilters struct {
	Provider      string
	Model         string
	KeySearch     string
	Search        string // combined search across model + key_preview
	DiffThreshold float64
	Page          int
	Limit         int
}

// modelPrice holds a cached pricing row for upstream cost estimation.
type modelPrice struct {
	InputPrice      float64
	OutputPrice     float64
	CacheReadPrice  float64
	CacheWritePrice float64
	ReasoningPrice  float64
	FixedPrice      float64
	Currency        string
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// round4 keeps sub-cent precision for cost figures (daily costs are often
// fractions of a cent; round2 zeroes them out).
func round4(v float64) float64 {
	return math.Round(v*10000) / 10000
}

// loadModelPrices loads service_models upstream pricing into a lookup map keyed by
// provider:model. Both the client-facing api_model_name and the upstream
// product_id (e.g. Bedrock "anthropic.claude-...-v1:0") are registered so
// callers holding either name can resolve a price.
func loadModelPrices(ctx context.Context, database *db.DB) (map[string]*modelPrice, error) {
	rows, err := database.Query(ctx, `
		SELECT provider,
		       COALESCE(api_model_name, product_id) AS model,
		       COALESCE(product_id, '') AS product_id,
		       COALESCE(input_price_cache_miss, input_price, 0) AS input_price,
		       COALESCE(output_price, 0) AS output_price,
		       COALESCE(cache_read_price, 0) AS cache_read_price,
		       COALESCE(cache_write_price, 0) AS cache_write_price,
		       COALESCE(reasoning_price, 0) AS reasoning_price,
		       COALESCE(fixed_price, 0) AS fixed_price,
		       COALESCE(currency, 'USD') AS currency
		FROM service_models
		WHERE COALESCE(api_model_name, product_id) <> ''
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	prices := make(map[string]*modelPrice)
	for rows.Next() {
		var provider, model, productID, currency string
		var input, output, cacheRead, cacheWrite, reasoning, fixed float64
		if err := rows.Scan(&provider, &model, &productID, &input, &output, &cacheRead, &cacheWrite, &reasoning, &fixed, &currency); err != nil {
			continue
		}
		p := &modelPrice{
			InputPrice: input, OutputPrice: output, CacheReadPrice: cacheRead,
			CacheWritePrice: cacheWrite, ReasoningPrice: reasoning, FixedPrice: fixed,
			Currency: currency,
		}
		prices[provider+":"+model] = p
		prices[model] = p // fallback by model name only
		if productID != "" {
			prices[provider+":"+productID] = p
			if _, exists := prices[productID]; !exists {
				prices[productID] = p // don't overwrite across providers
			}
		}
	}
	return prices, rows.Err()
}

// lookupPrice finds a price entry for a provider/model pair. Inference
// profile prefixes (us./global./eu./apac.) are stripped as a fallback so
// Bedrock profile model IDs resolve to the base model's price.
func lookupPrice(prices map[string]*modelPrice, provider, model string) *modelPrice {
	if p, ok := prices[provider+":"+model]; ok {
		return p
	}
	if p, ok := prices[model]; ok {
		return p
	}
	// Aggregator raw IDs carry a vendor prefix ("anthropic/claude-opus-5",
	// "qwen/qwen3.8-max"); service_models stores bare names.
	if idx := strings.LastIndex(model, "/"); idx >= 0 && idx < len(model)-1 {
		if p, ok := prices[provider+":"+model[idx+1:]]; ok {
			return p
		}
		if p, ok := prices[model[idx+1:]]; ok {
			return p
		}
	}
	for _, prefix := range []string{"us.", "global.", "eu.", "apac."} {
		if stripped, found := strings.CutPrefix(model, prefix); found {
			if p, ok := prices[provider+":"+stripped]; ok {
				return p
			}
			if p, ok := prices[stripped]; ok {
				return p
			}
		}
	}
	return nil
}

// estimateUpstreamCostUSD computes estimated upstream cost from token/request counts and pricing.
// It returns both the total cost and the component breakdown.
func estimateUpstreamCostUSD(req, input, output, cacheRead, cacheWrite, reasoning int64, p *modelPrice) (total float64, cc services.ComponentCosts) {
	if p == nil {
		return 0, cc
	}
	// Upstream input_tokens includes the cache-read portion (Bedrock reports
	// total prompt tokens), so billing miss-price on the full input would
	// double-charge cached tokens that are already priced at CacheReadPrice.
	missInput := input - cacheRead
	if missInput < 0 {
		missInput = 0
	}
	cc.Input = float64(missInput) / 1_000_000.0 * p.InputPrice
	cc.Output = float64(output) / 1_000_000.0 * p.OutputPrice
	cc.CacheHit = float64(cacheRead) / 1_000_000.0 * p.CacheReadPrice
	cc.CacheWrite = float64(cacheWrite) / 1_000_000.0 * p.CacheWritePrice
	cc.Reasoning = float64(reasoning) / 1_000_000.0 * p.ReasoningPrice
	cc.Fixed = p.FixedPrice * float64(req)
	return cc.Total(), cc
}

// UpstreamReconciliationSummary returns a global snapshot for the requested range.
func UpstreamReconciliationSummary(ctx context.Context, database *db.DB, pricingSvc *services.PricingService, r ReconciliationRange, period string) (*ReconciliationSummary, error) {
	prices, err := loadModelPrices(ctx, database)
	if err != nil {
		return nil, err
	}

	var upReq, upIn, upOut, upCacheRead, upCacheWrite, upErr int64
	_ = database.QueryRow(ctx, `
		SELECT COALESCE(SUM(request_count), 0),
		       COALESCE(SUM(input_tokens), 0),
		       COALESCE(SUM(output_tokens), 0),
		       COALESCE(SUM(cache_read_tokens), 0),
		       COALESCE(SUM(cache_creation_tokens), 0),
		       COALESCE(SUM(error_count), 0)
		FROM upstream_key_usage
		WHERE date >= $1 AND date <= $2
	`, r.From.Format("2006-01-02"), r.To.Format("2006-01-02")).Scan(
		&upReq, &upIn, &upOut, &upCacheRead, &upCacheWrite, &upErr)

	var billedReq, billedIn, billedOut, billedCachedIn, billedReasoning, billedCost int64
	_ = database.QueryRow(ctx, `
		SELECT COALESCE(SUM(request_count), 0),
		       COALESCE(SUM(input_tokens), 0),
		       COALESCE(SUM(output_tokens), 0),
		       COALESCE(SUM(cached_input_tokens), 0),
		       COALESCE(SUM(reasoning_tokens), 0),
		       COALESCE(SUM(total_cost), 0)
		FROM usage_records
		WHERE created_at >= $1 AND created_at <= $2
	`, r.From, r.To).Scan(&billedReq, &billedIn, &billedOut, &billedCachedIn, &billedReasoning, &billedCost)

	// Estimate upstream cost from aggregate token counts using a weighted price lookup.
	// Since we don't have per-provider/model token split in the single-row summary,
	// we query the split and sum weighted costs.
	var upstreamCostUSD, upstreamInputCost, upstreamOutputCost, upstreamCacheCost, upstreamCacheWriteCost, upstreamReasoningCost, upstreamFixedCost float64
	rows, err := database.Query(ctx, `
		SELECT provider, model,
		       COALESCE(SUM(request_count), 0),
		       COALESCE(SUM(input_tokens), 0),
		       COALESCE(SUM(output_tokens), 0),
		       COALESCE(SUM(cache_read_tokens), 0),
		       COALESCE(SUM(cache_creation_tokens), 0)
		FROM upstream_key_usage
		WHERE date >= $1 AND date <= $2
		GROUP BY provider, model
	`, r.From.Format("2006-01-02"), r.To.Format("2006-01-02"))
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var provider, model string
			var req, inTokens, outTokens, cacheRead, cacheWrite int64
			if err := rows.Scan(&provider, &model, &req, &inTokens, &outTokens, &cacheRead, &cacheWrite); err != nil {
				continue
			}
			p := lookupPrice(prices, provider, model)
			total, cc := estimateUpstreamCostUSD(req, inTokens, outTokens, cacheRead, cacheWrite, 0, p)
			upstreamCostUSD += total
			upstreamInputCost += cc.Input
			upstreamOutputCost += cc.Output
			upstreamCacheCost += cc.CacheHit
			upstreamCacheWriteCost += cc.CacheWrite
			upstreamReasoningCost += cc.Reasoning
			upstreamFixedCost += cc.Fixed
		}
		upstreamCostUSD = round4(upstreamCostUSD)
		upstreamInputCost = round4(upstreamInputCost)
		upstreamOutputCost = round4(upstreamOutputCost)
		upstreamCacheCost = round4(upstreamCacheCost)
		upstreamCacheWriteCost = round4(upstreamCacheWriteCost)
		upstreamReasoningCost = round4(upstreamReasoningCost)
		upstreamFixedCost = round4(upstreamFixedCost)
	}

	// Estimate billed cost components from per-provider/model token totals using
	// the current downstream pricing rules. This is approximate (uses flat base
	// prices, not tiered/time windows) but lets admins verify cache/reasoning
	// composition.
	var billedInputCost, billedOutputCost, billedCacheCost, billedReasoningCost, billedFixedCost float64
	if pricingSvc != nil {
		billRows, err := database.Query(ctx, `
			SELECT provider, model,
			       COALESCE(SUM(request_count), 0),
			       COALESCE(SUM(input_tokens), 0),
			       COALESCE(SUM(output_tokens), 0),
			       COALESCE(SUM(cached_input_tokens), 0),
			       COALESCE(SUM(reasoning_tokens), 0),
			       COALESCE(SUM(effective_input_tokens), 0)
			FROM usage_records
			WHERE created_at >= $1 AND created_at <= $2
			GROUP BY provider, model
		`, r.From, r.To)
		if err == nil {
			defer billRows.Close()
			for billRows.Next() {
				var provider, model string
				var req, inTok, outTok, cached, reasoning, effective int64
				if err := billRows.Scan(&provider, &model, &req, &inTok, &outTok, &cached, &reasoning, &effective); err != nil {
					continue
				}
				// Region is not available in the aggregate; pricing rules usually use '*'.
				cc, _ := pricingSvc.EstimateComponentCostForAggregate(ctx, provider, "", model, r.To, inTok, outTok, cached, 0, reasoning, effective, req)
				if cc != nil {
					billedInputCost += cc.Input
					billedOutputCost += cc.Output
					billedCacheCost += cc.CacheHit
					billedReasoningCost += cc.Reasoning
					billedFixedCost += cc.Fixed
				}
			}
		}
	}

	billedCostUSD := services.QuotaToUSDFloat(billedCost)
	errorRate := float64(0)
	if upReq > 0 {
		errorRate = round2(float64(upErr) / float64(upReq) * 100)
	}

	marginUSD := round4(billedCostUSD - upstreamCostUSD)
	marginPct := float64(0)
	if billedCostUSD > 0 {
		marginPct = round2((billedCostUSD - upstreamCostUSD) / billedCostUSD * 100)
	}

	return &ReconciliationSummary{
		Period:                    period,
		DateFrom:                  r.From.Format("2006-01-02"),
		DateTo:                    r.To.Format("2006-01-02"),
		UpstreamRequests:          upReq,
		UpstreamInputTokens:       upIn, // upstream input_tokens already includes cache_read_tokens
		UpstreamOutputTokens:      upOut,
		UpstreamCacheReadTokens:   upCacheRead,
		UpstreamCacheWriteTokens:  upCacheWrite,
		UpstreamErrors:            upErr,
		BilledRequests:            billedReq,
		BilledInputTokens:         billedIn,
		BilledOutputTokens:        billedOut,
		BilledCachedInputTokens:   billedCachedIn,
		BilledReasoningTokens:     billedReasoning,
		BilledCostQuota:           billedCost,
		BilledCostUSD:             round4(billedCostUSD),
		BilledInputCostUSD:        round4(billedInputCost),
		BilledOutputCostUSD:       round4(billedOutputCost),
		BilledCacheCostUSD:        round4(billedCacheCost),
		BilledReasoningCostUSD:    round4(billedReasoningCost),
		BilledFixedCostUSD:        round4(billedFixedCost),
		EstimatedUpstreamCostUSD:  upstreamCostUSD,
		UpstreamInputCostUSD:      upstreamInputCost,
		UpstreamOutputCostUSD:     upstreamOutputCost,
		UpstreamCacheCostUSD:      upstreamCacheCost,
		UpstreamCacheWriteCostUSD: upstreamCacheWriteCost,
		UpstreamReasoningCostUSD:  upstreamReasoningCost,
		UpstreamFixedCostUSD:      upstreamFixedCost,
		CostDiffUSD:               round4(upstreamCostUSD - billedCostUSD),
		GrossMarginUSD:            marginUSD,
		GrossMarginPct:            marginPct,
		ErrorRate:                 errorRate,
	}, nil
}

// UpstreamReconciliationTrend returns daily upstream vs billed values for a metric.
func UpstreamReconciliationTrend(ctx context.Context, database *db.DB, r ReconciliationRange, metric string, filters ReconciliationFilters) (*ReconciliationTrend, error) {
	prices, err := loadModelPrices(ctx, database)
	if err != nil {
		return nil, err
	}

	providerFilter := filters.Provider
	modelFilter := filters.Model

	// Upstream daily series.
	upRows, err := database.Query(ctx, `
		WITH up_normalized AS (
		    SELECT u.date, u.provider,
		           COALESCE(sm.api_model_name, u.model) AS model,
		           u.request_count, u.input_tokens, u.output_tokens,
		           u.cache_read_tokens, u.cache_creation_tokens, u.error_count
		    FROM upstream_key_usage u
		    LEFT JOIN LATERAL (
		        SELECT sm.api_model_name
		        FROM service_models sm
		        WHERE sm.provider = u.provider
		          AND sm.deleted_at IS NULL
		          AND (
		              u.model = sm.api_model_name
		              OR u.model = sm.product_id
		              OR regexp_replace(u.model, '^(us|global|eu|apac)\.', '') = sm.product_id
		              OR (position('/' in u.model) > 0 AND substring(u.model from position('/' in u.model) + 1) = sm.api_model_name)
		              OR (position('/' in u.model) > 0 AND substring(u.model from position('/' in u.model) + 1) = sm.product_id)
		          )
		        ORDER BY
		          CASE WHEN u.model = sm.api_model_name THEN 0 ELSE 1 END,
		          CASE WHEN position('/' in u.model) > 0 AND substring(u.model from position('/' in u.model) + 1) = sm.api_model_name THEN 0 ELSE 1 END,
		          CASE WHEN u.model = sm.product_id AND sm.api_model_name <> sm.product_id THEN 0 ELSE 1 END,
		          CASE WHEN regexp_replace(u.model, '^(us|global|eu|apac)\.', '') = sm.product_id AND sm.api_model_name <> sm.product_id THEN 0 ELSE 1 END,
		          CASE WHEN u.model = sm.product_id THEN 0 ELSE 1 END,
		          CASE WHEN regexp_replace(u.model, '^(us|global|eu|apac)\.', '') = sm.product_id THEN 0 ELSE 1 END
		        LIMIT 1
		    ) sm ON true
		    WHERE u.date >= $1 AND u.date <= $2
		)
		SELECT date, provider, model,
		       COALESCE(SUM(request_count), 0),
		       COALESCE(SUM(input_tokens), 0),
		       COALESCE(SUM(output_tokens), 0),
		       COALESCE(SUM(cache_read_tokens), 0),
		       COALESCE(SUM(cache_creation_tokens), 0),
		       COALESCE(SUM(error_count), 0)
		FROM up_normalized
		WHERE ($3 = '' OR provider = $3)
		  AND ($4 = '' OR model = $4)
		GROUP BY date, provider, model
		ORDER BY date
	`, r.From.Format("2006-01-02"), r.To.Format("2006-01-02"), providerFilter, modelFilter)
	if err != nil {
		return nil, err
	}
	defer upRows.Close()

	// Billed daily series.
	billedRows, err := database.Query(ctx, `
		SELECT created_at::date AS d, provider, model,
		       COALESCE(SUM(request_count), 0),
		       COALESCE(SUM(input_tokens), 0),
		       COALESCE(SUM(output_tokens), 0),
		       COALESCE(SUM(total_cost), 0)
		FROM usage_records
		WHERE created_at >= $1 AND created_at <= $2
		  AND ($3 = '' OR provider = $3)
		  AND ($4 = '' OR model = $4)
		GROUP BY created_at::date, provider, model
		ORDER BY d
	`, r.From, r.To, providerFilter, modelFilter)
	if err != nil {
		return nil, err
	}
	defer billedRows.Close()

	// Build daily maps.
	type dayVals struct {
		upstream float64
		billed   float64
	}
	days := make(map[string]*dayVals)

	seedDays := func() {
		for d := r.From; !d.After(r.To); d = d.Add(24 * time.Hour) {
			days[d.Format("2006-01-02")] = &dayVals{}
		}
	}
	seedDays()

	for upRows.Next() {
		var date time.Time
		var provider, model string
		var req, inTokens, outTokens, cacheRead, cacheWrite, errCount int64
		if err := upRows.Scan(&date, &provider, &model, &req, &inTokens, &outTokens, &cacheRead, &cacheWrite, &errCount); err != nil {
			continue
		}
		ds := date.Format("2006-01-02")
		v := days[ds]
		if v == nil {
			continue
		}
		p := lookupPrice(prices, provider, model)
		switch metric {
		case "requests":
			v.upstream += float64(req)
		case "tokens":
			// Token volume only: input_tokens already includes cache_read_tokens.
			v.upstream += float64(inTokens + outTokens)
		case "error_rate":
			if req > 0 {
				v.upstream += float64(errCount) / float64(req) * 100
			}
		case "cost":
			total, _ := estimateUpstreamCostUSD(req, inTokens, outTokens, cacheRead, cacheWrite, 0, p)
			v.upstream += total
		default:
			v.upstream += float64(req)
		}
	}

	for billedRows.Next() {
		var date time.Time
		var provider, model string
		var req, inTokens, outTokens, cost int64
		if err := billedRows.Scan(&date, &provider, &model, &req, &inTokens, &outTokens, &cost); err != nil {
			continue
		}
		ds := date.Format("2006-01-02")
		v := days[ds]
		if v == nil {
			continue
		}
		switch metric {
		case "requests":
			v.billed += float64(req)
		case "tokens":
			v.billed += float64(inTokens + outTokens)
		case "error_rate":
			v.billed = v.upstream // show upstream error rate on both lines for comparison
		case "cost":
			v.billed += services.QuotaToUSDFloat(cost)
		default:
			v.billed += float64(req)
		}
	}

	// Sort labels and arrays.
	trend := &ReconciliationTrend{Metric: metric}
	for d := r.From; !d.After(r.To); d = d.Add(24 * time.Hour) {
		ds := d.Format("2006-01-02")
		v := days[ds]
		trend.Labels = append(trend.Labels, ds[5:]) // MM-DD
		if metric == "cost" {
			trend.Upstream = append(trend.Upstream, round4(v.upstream))
			trend.Billed = append(trend.Billed, round4(v.billed))
		} else {
			trend.Upstream = append(trend.Upstream, round2(v.upstream))
			trend.Billed = append(trend.Billed, round2(v.billed))
		}
	}

	return trend, nil
}

// UpstreamReconciliationDetail returns paginated provider/model/key reconciliation rows.
func UpstreamReconciliationDetail(ctx context.Context, database *db.DB, pricingSvc *services.PricingService, r ReconciliationRange, filters ReconciliationFilters) ([]ReconciliationDetailRow, int, error) {
	prices, err := loadModelPrices(ctx, database)
	if err != nil {
		return nil, 0, err
	}

	if filters.Page < 1 {
		filters.Page = 1
	}
	if filters.Limit < 1 || filters.Limit > 500 {
		filters.Limit = 20
	}
	offset := (filters.Page - 1) * filters.Limit

	keySearch := filters.KeySearch
	if keySearch != "" {
		keySearch = "%" + strings.ToLower(keySearch) + "%"
	}

	searchPattern := filters.Search
	if searchPattern != "" {
		searchPattern = "%" + strings.ToLower(searchPattern) + "%"
	}

	var total int
	countQuery := `
		WITH up AS (
			-- Normalize the upstream provider model ID (e.g. Bedrock
			-- "us.anthropic.claude-...-v1:0") to the client-facing logical
			-- model name so it matches usage_records on the join.
			SELECT u.provider,
			       COALESCE(sm.api_model_name, u.model) AS model,
			       lower(replace(replace(regexp_replace(CASE WHEN position('/' in COALESCE(sm.api_model_name, u.model))>0 THEN substring(COALESCE(sm.api_model_name, u.model) from position('/' in COALESCE(sm.api_model_name, u.model))+1) ELSE COALESCE(sm.api_model_name, u.model) END,'^(us|global|eu|apac)\.(anthropic\.)?|^anthropic\.','','g'),'.',''),'-','')) AS norm_model,
			       u.upstream_key_id::text AS key_id,
			       COALESCE(SUM(u.request_count), 0) AS req,
			       COALESCE(SUM(u.input_tokens), 0) AS in_tok,
			       COALESCE(SUM(u.output_tokens), 0) AS out_tok,
			       COALESCE(SUM(u.cache_read_tokens), 0) AS cache_read,
			       COALESCE(SUM(u.cache_creation_tokens), 0) AS cache_write,
			       COALESCE(SUM(u.error_count), 0) AS err
			FROM upstream_key_usage u
			LEFT JOIN LATERAL (
			    SELECT sm.api_model_name
			    FROM service_models sm
			    WHERE sm.provider = u.provider
			      AND sm.deleted_at IS NULL
			      AND (
			          u.model = sm.api_model_name
			          OR u.model = sm.product_id
			          OR regexp_replace(u.model, '^(us|global|eu|apac)\.', '') = sm.product_id
			          OR (position('/' in u.model) > 0 AND substring(u.model from position('/' in u.model) + 1) = sm.api_model_name)
			          OR (position('/' in u.model) > 0 AND substring(u.model from position('/' in u.model) + 1) = sm.product_id)
			      )
			    ORDER BY
			      CASE WHEN u.model = sm.api_model_name THEN 0 ELSE 1 END,
			      CASE WHEN position('/' in u.model) > 0 AND substring(u.model from position('/' in u.model) + 1) = sm.api_model_name THEN 0 ELSE 1 END,
			      CASE WHEN u.model = sm.product_id AND sm.api_model_name <> sm.product_id THEN 0 ELSE 1 END,
			      CASE WHEN regexp_replace(u.model, '^(us|global|eu|apac)\.', '') = sm.product_id AND sm.api_model_name <> sm.product_id THEN 0 ELSE 1 END,
			      CASE WHEN u.model = sm.product_id THEN 0 ELSE 1 END,
			      CASE WHEN regexp_replace(u.model, '^(us|global|eu|apac)\.', '') = sm.product_id THEN 0 ELSE 1 END
			    LIMIT 1
			) sm ON true
			WHERE u.date >= $1 AND u.date <= $2
			GROUP BY u.provider, COALESCE(sm.api_model_name, u.model), u.upstream_key_id
		),
		billed AS (
			SELECT provider, model,
			       lower(replace(replace(regexp_replace(CASE WHEN position('/' in model)>0 THEN substring(model from position('/' in model)+1) ELSE model END,'^(us|global|eu|apac)\.(anthropic\.)?|^anthropic\.','','g'),'.',''),'-','')) AS norm_model,
			       upstream_key_id::text AS key_id,
			       COALESCE(SUM(request_count), 0) AS req,
			       COALESCE(SUM(input_tokens), 0) AS in_tok,
			       COALESCE(SUM(output_tokens), 0) AS out_tok,
			       COALESCE(SUM(cached_input_tokens), 0) AS cached_in,
			       COALESCE(SUM(reasoning_tokens), 0) AS reasoning,
			       COALESCE(SUM(effective_input_tokens), 0) AS effective_in,
			       COALESCE(SUM(total_cost), 0) AS cost
			FROM usage_records
			WHERE created_at >= $3 AND created_at <= $4
			GROUP BY provider, model, upstream_key_id
		),
		combined AS (
			SELECT COALESCE(up.provider, billed.provider) AS provider,
			       COALESCE(up.model, billed.model) AS model,
			       COALESCE(up.key_id, billed.key_id, '') AS key_id,
			       COALESCE(up.req, 0) AS up_req,
			       COALESCE(billed.req, 0) AS billed_req,
			       COALESCE(up.in_tok, 0) AS up_in,
			       COALESCE(billed.in_tok, 0) AS billed_in,
			       COALESCE(up.out_tok, 0) AS up_out,
			       COALESCE(billed.out_tok, 0) AS billed_out,
			       COALESCE(up.cache_read, 0) AS up_cache_read,
			       COALESCE(up.cache_write, 0) AS up_cache_write,
			       COALESCE(billed.cached_in, 0) AS billed_cached_in,
			       COALESCE(billed.reasoning, 0) AS billed_reasoning,
			       COALESCE(billed.effective_in, 0) AS billed_effective_in,
			       COALESCE(up.err, 0) AS err,
			       COALESCE(billed.cost, 0) AS billed_cost
			FROM up
			FULL OUTER JOIN billed USING (provider, norm_model, key_id)
		)
		SELECT COUNT(*)
		FROM combined
		LEFT JOIN upstream_keys k ON k.id = NULLIF(combined.key_id, '')::uuid
		WHERE ($5 = '' OR combined.provider = $5)
		  AND ($6 = '' OR combined.model = $6)
		  AND ($7 = '' OR LOWER(k.key_preview) LIKE $7)
		  AND ($8 = '' OR LOWER(combined.model) LIKE $8 OR LOWER(k.key_preview) LIKE $8)
	`
	_ = database.QueryRow(ctx, countQuery,
		r.From.Format("2006-01-02"), r.To.Format("2006-01-02"), r.From, r.To,
		filters.Provider, filters.Model, keySearch, searchPattern).Scan(&total)

	rows, err := database.Query(ctx, `
		WITH up AS (
			-- Normalize the upstream provider model ID (e.g. Bedrock
			-- "us.anthropic.claude-...-v1:0") to the client-facing logical
			-- model name so it matches usage_records on the join.
			SELECT u.provider,
			       COALESCE(sm.api_model_name, u.model) AS model,
			       lower(replace(replace(regexp_replace(CASE WHEN position('/' in COALESCE(sm.api_model_name, u.model))>0 THEN substring(COALESCE(sm.api_model_name, u.model) from position('/' in COALESCE(sm.api_model_name, u.model))+1) ELSE COALESCE(sm.api_model_name, u.model) END,'^(us|global|eu|apac)\.(anthropic\.)?|^anthropic\.','','g'),'.',''),'-','')) AS norm_model,
			       -- The same logical model may arrive under several raw
			       -- upstream IDs (inference-profile prefixes like
			       -- us./global./eu.). Keep them out of GROUP BY so one
			       -- logical row aligns with one billed row; show the raw
			       -- variants as a comma-separated list instead.
			       string_agg(DISTINCT u.model, ', ' ORDER BY u.model) AS raw_model,
			       u.upstream_key_id::text AS key_id,
			       COALESCE(SUM(u.request_count), 0) AS req,
			       COALESCE(SUM(u.input_tokens), 0) AS in_tok,
			       COALESCE(SUM(u.output_tokens), 0) AS out_tok,
			       COALESCE(SUM(u.cache_read_tokens), 0) AS cache_read,
			       COALESCE(SUM(u.cache_creation_tokens), 0) AS cache_write,
			       COALESCE(SUM(u.error_count), 0) AS err
			FROM upstream_key_usage u
			LEFT JOIN LATERAL (
			    SELECT sm.api_model_name
			    FROM service_models sm
			    WHERE sm.provider = u.provider
			      AND sm.deleted_at IS NULL
			      AND (
			          u.model = sm.api_model_name
			          OR u.model = sm.product_id
			          OR regexp_replace(u.model, '^(us|global|eu|apac)\.', '') = sm.product_id
			          OR (position('/' in u.model) > 0 AND substring(u.model from position('/' in u.model) + 1) = sm.api_model_name)
			          OR (position('/' in u.model) > 0 AND substring(u.model from position('/' in u.model) + 1) = sm.product_id)
			      )
			    ORDER BY
			      CASE WHEN u.model = sm.api_model_name THEN 0 ELSE 1 END,
			      CASE WHEN position('/' in u.model) > 0 AND substring(u.model from position('/' in u.model) + 1) = sm.api_model_name THEN 0 ELSE 1 END,
			      CASE WHEN u.model = sm.product_id AND sm.api_model_name <> sm.product_id THEN 0 ELSE 1 END,
			      CASE WHEN regexp_replace(u.model, '^(us|global|eu|apac)\.', '') = sm.product_id AND sm.api_model_name <> sm.product_id THEN 0 ELSE 1 END,
			      CASE WHEN u.model = sm.product_id THEN 0 ELSE 1 END,
			      CASE WHEN regexp_replace(u.model, '^(us|global|eu|apac)\.', '') = sm.product_id THEN 0 ELSE 1 END
			    LIMIT 1
			) sm ON true
			WHERE u.date >= $1 AND u.date <= $2
			GROUP BY u.provider, COALESCE(sm.api_model_name, u.model), u.upstream_key_id
		),
		billed AS (
			SELECT provider, model,
			       lower(replace(replace(regexp_replace(CASE WHEN position('/' in model)>0 THEN substring(model from position('/' in model)+1) ELSE model END,'^(us|global|eu|apac)\.(anthropic\.)?|^anthropic\.','','g'),'.',''),'-','')) AS norm_model,
			       upstream_key_id::text AS key_id,
			       COALESCE(SUM(request_count), 0) AS req,
			       COALESCE(SUM(input_tokens), 0) AS in_tok,
			       COALESCE(SUM(output_tokens), 0) AS out_tok,
			       COALESCE(SUM(cached_input_tokens), 0) AS cached_in,
			       COALESCE(SUM(reasoning_tokens), 0) AS reasoning,
			       COALESCE(SUM(effective_input_tokens), 0) AS effective_in,
			       COALESCE(SUM(total_cost), 0) AS cost
			FROM usage_records
			WHERE created_at >= $3 AND created_at <= $4
			GROUP BY provider, model, upstream_key_id
		),
		combined AS (
			SELECT COALESCE(up.provider, billed.provider) AS provider,
			       COALESCE(up.model, billed.model) AS model,
			       up.raw_model AS upstream_model,
			       COALESCE(up.key_id, billed.key_id, '') AS key_id,
			       COALESCE(up.req, 0) AS up_req,
			       COALESCE(billed.req, 0) AS billed_req,
			       COALESCE(up.in_tok, 0) AS up_in,
			       COALESCE(billed.in_tok, 0) AS billed_in,
			       COALESCE(up.out_tok, 0) AS up_out,
			       COALESCE(billed.out_tok, 0) AS billed_out,
			       COALESCE(up.cache_read, 0) AS up_cache_read,
			       COALESCE(up.cache_write, 0) AS up_cache_write,
			       COALESCE(billed.cached_in, 0) AS billed_cached_in,
			       COALESCE(billed.reasoning, 0) AS billed_reasoning,
			       COALESCE(billed.effective_in, 0) AS billed_effective_in,
			       COALESCE(up.err, 0) AS err,
			       COALESCE(billed.cost, 0) AS billed_cost
			FROM up
			FULL OUTER JOIN billed USING (provider, norm_model, key_id)
		)
		SELECT combined.provider, combined.model, COALESCE(combined.upstream_model, ''), combined.key_id,
		       COALESCE(k.key_preview, ''),
		       combined.up_req, combined.billed_req,
		       combined.up_in, combined.billed_in,
		       combined.up_out, combined.billed_out,
		       combined.up_cache_read, combined.up_cache_write,
		       combined.billed_cached_in, combined.billed_reasoning, combined.billed_effective_in,
		       combined.err, combined.billed_cost
		FROM combined
		LEFT JOIN upstream_keys k ON k.id = NULLIF(combined.key_id, '')::uuid
		WHERE ($5 = '' OR combined.provider = $5)
		  AND ($6 = '' OR combined.model = $6)
		  AND ($7 = '' OR LOWER(k.key_preview) LIKE $7)
		  AND ($8 = '' OR LOWER(combined.model) LIKE $8 OR LOWER(k.key_preview) LIKE $8)
		ORDER BY ABS(combined.up_req - combined.billed_req) DESC, combined.provider, combined.model, combined.key_id
		LIMIT $9 OFFSET $10
	`, r.From.Format("2006-01-02"), r.To.Format("2006-01-02"), r.From, r.To,
		filters.Provider, filters.Model, keySearch, searchPattern, filters.Limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var results []ReconciliationDetailRow
	for rows.Next() {
		var row ReconciliationDetailRow
		var billedCostQuota int64
		var billedEffectiveIn int64
		if err := rows.Scan(
			&row.Provider, &row.Model, &row.UpstreamModel, &row.UpstreamKeyID, &row.KeyPreview,
			&row.UpstreamRequests, &row.BilledRequests,
			&row.UpstreamInputTokens, &row.BilledInputTokens,
			&row.UpstreamOutputTokens, &row.BilledOutputTokens,
			&row.UpstreamCacheReadTokens, &row.UpstreamCacheWriteTokens,
			&row.BilledCachedInputTokens, &row.BilledReasoningTokens, &billedEffectiveIn,
			&row.ErrorCount, &billedCostQuota); err != nil {
			continue
		}
		row.RequestDiff = row.UpstreamRequests - row.BilledRequests
		row.InputTokenDiff = row.UpstreamInputTokens - row.BilledInputTokens
		row.OutputTokenDiff = row.UpstreamOutputTokens - row.BilledOutputTokens
		row.BilledCostUSD = round4(services.QuotaToUSDFloat(billedCostQuota))
		p := lookupPrice(prices, row.Provider, row.Model)
		total, ucc := estimateUpstreamCostUSD(row.UpstreamRequests, row.UpstreamInputTokens, row.UpstreamOutputTokens, row.UpstreamCacheReadTokens, row.UpstreamCacheWriteTokens, 0, p)
		row.EstimatedUpstreamCostUSD = round4(total)
		row.UpstreamInputCostUSD = round4(ucc.Input)
		row.UpstreamOutputCostUSD = round4(ucc.Output)
		row.UpstreamCacheCostUSD = round4(ucc.CacheHit)
		row.UpstreamCacheWriteCostUSD = round4(ucc.CacheWrite)
		row.UpstreamReasoningCostUSD = round4(ucc.Reasoning)
		row.UpstreamFixedCostUSD = round4(ucc.Fixed)
		row.CostDiffUSD = round4(row.EstimatedUpstreamCostUSD - row.BilledCostUSD)
		row.GrossMarginUSD = round4(row.BilledCostUSD - row.EstimatedUpstreamCostUSD)
		if row.BilledCostUSD > 0 {
			row.GrossMarginPct = round2((row.BilledCostUSD - row.EstimatedUpstreamCostUSD) / row.BilledCostUSD * 100)
		}
		if row.UpstreamRequests > 0 {
			row.ErrorRate = round2(float64(row.ErrorCount) / float64(row.UpstreamRequests) * 100)
		}
		if pricingSvc != nil {
			cc, _ := pricingSvc.EstimateComponentCostForAggregate(ctx, row.Provider, "", row.Model, r.To,
				row.BilledInputTokens, row.BilledOutputTokens, row.BilledCachedInputTokens, 0,
				row.BilledReasoningTokens, billedEffectiveIn, row.BilledRequests)
			if cc != nil {
				row.BilledInputCostUSD = round4(cc.Input)
				row.BilledOutputCostUSD = round4(cc.Output)
				row.BilledCacheCostUSD = round4(cc.CacheHit)
				row.BilledReasoningCostUSD = round4(cc.Reasoning)
				row.BilledFixedCostUSD = round4(cc.Fixed)
			}
		}
		results = append(results, row)
	}

	// Apply optional diff threshold filter client-side for cost or request diff.
	if filters.DiffThreshold > 0 {
		filtered := make([]ReconciliationDetailRow, 0, len(results))
		for _, row := range results {
			if math.Abs(float64(row.RequestDiff)) >= filters.DiffThreshold ||
				math.Abs(row.CostDiffUSD) >= filters.DiffThreshold {
				filtered = append(filtered, row)
			}
		}
		results = filtered
	}

	return results, total, rows.Err()
}

// FloatFromString parses a float query parameter safely.
func FloatFromString(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return v
}

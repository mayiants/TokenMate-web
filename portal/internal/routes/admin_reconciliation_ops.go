package routes

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/mayiants/ai-portal-go/internal/query"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (r *AdminRoutes) parseReconciliationRange(c *gin.Context) (query.ReconciliationRange, string, error) {
	period := c.DefaultQuery("period", "7d")
	dateFrom := c.Query("date_from")
	dateTo := c.Query("date_to")

	// If no custom dates are provided for a non-custom period, treat as shortcut.
	if period != string(query.PeriodCustom) || dateFrom == "" || dateTo == "" {
		rng, err := query.ParseReconciliationPeriod(period, "", "")
		return rng, period, err
	}

	rng, err := query.ParseReconciliationPeriod("custom", dateFrom, dateTo)
	return rng, "custom", err
}

func (r *AdminRoutes) getUpstreamReconciliationSummary(c *gin.Context) {
	rng, period, err := r.parseReconciliationRange(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}

	summary, err := query.UpstreamReconciliationSummary(c.Request.Context(), r.db, r.pricingSvc, rng, period)
	if err != nil {
		r.logger.Warn("Failed to fetch upstream reconciliation summary", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to fetch reconciliation summary"}})
		return
	}

	c.JSON(http.StatusOK, summary)
}

func (r *AdminRoutes) getUpstreamReconciliationTrend(c *gin.Context) {
	rng, _, err := r.parseReconciliationRange(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}

	metric := c.DefaultQuery("metric", "requests")
	if metric != "requests" && metric != "tokens" && metric != "cost" && metric != "error_rate" {
		metric = "requests"
	}

	filters := query.ReconciliationFilters{
		Provider: c.Query("provider"),
		Model:    c.Query("model"),
	}

	trend, err := query.UpstreamReconciliationTrend(c.Request.Context(), r.db, rng, metric, filters)
	if err != nil {
		r.logger.Warn("Failed to fetch upstream reconciliation trend", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to fetch reconciliation trend"}})
		return
	}

	c.JSON(http.StatusOK, trend)
}

func (r *AdminRoutes) getUpstreamReconciliationDetail(c *gin.Context) {
	rng, _, err := r.parseReconciliationRange(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 500 {
		limit = 20
	}

	filters := query.ReconciliationFilters{
		Provider:      c.Query("provider"),
		Model:         c.Query("model"),
		KeySearch:     c.Query("key_search"),
		Search:        c.Query("search"),
		DiffThreshold: query.FloatFromString(c.Query("diff_threshold")),
		Page:          page,
		Limit:         limit,
	}

	rows, total, err := query.UpstreamReconciliationDetail(c.Request.Context(), r.db, r.pricingSvc, rng, filters)
	if err != nil {
		r.logger.Warn("Failed to fetch upstream reconciliation detail", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to fetch reconciliation detail"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"rows":        rows,
		"total":       total,
		"page":        page,
		"limit":       limit,
		"total_pages": (total + limit - 1) / limit,
	})
}

func (r *AdminRoutes) exportUpstreamReconciliation(c *gin.Context) {
	rng, _, err := r.parseReconciliationRange(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}

	filters := query.ReconciliationFilters{
		Provider:      c.Query("provider"),
		Model:         c.Query("model"),
		KeySearch:     c.Query("key_search"),
		Search:        c.Query("search"),
		DiffThreshold: query.FloatFromString(c.Query("diff_threshold")),
		Page:          1,
		Limit:         10000,
	}

	rows, _, err := query.UpstreamReconciliationDetail(c.Request.Context(), r.db, r.pricingSvc, rng, filters)
	if err != nil {
		r.logger.Warn("Failed to export upstream reconciliation", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to export reconciliation data"}})
		return
	}

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=upstream-reconciliation-"+time.Now().UTC().Format("200601021504")+".csv")
	_, _ = c.Writer.Write([]byte{0xEF, 0xBB, 0xBF})

	w := csv.NewWriter(c.Writer)
	defer w.Flush()

	_ = w.Write([]string{
		"provider", "model", "key_id", "key_preview",
		"upstream_requests", "billed_requests", "request_diff",
		"upstream_input_tokens", "billed_input_tokens", "input_token_diff",
		"upstream_output_tokens", "billed_output_tokens", "output_token_diff",
		"upstream_cache_read_tokens", "upstream_cache_write_tokens",
		"billed_cached_input_tokens", "billed_reasoning_tokens",
		"upstream_cost_usd", "upstream_input_cost_usd", "upstream_output_cost_usd",
		"upstream_cache_cost_usd", "upstream_cache_write_cost_usd", "upstream_fixed_cost_usd",
		"billed_cost_usd", "billed_input_cost_usd", "billed_output_cost_usd",
		"billed_cache_cost_usd", "billed_reasoning_cost_usd", "billed_fixed_cost_usd",
		"cost_diff_usd", "gross_margin_usd", "gross_margin_pct",
		"error_count", "error_rate",
	})

	for _, row := range rows {
		_ = w.Write([]string{
			row.Provider,
			row.Model,
			row.UpstreamKeyID,
			row.KeyPreview,
			strconv.FormatInt(row.UpstreamRequests, 10),
			strconv.FormatInt(row.BilledRequests, 10),
			strconv.FormatInt(row.RequestDiff, 10),
			strconv.FormatInt(row.UpstreamInputTokens, 10),
			strconv.FormatInt(row.BilledInputTokens, 10),
			strconv.FormatInt(row.InputTokenDiff, 10),
			strconv.FormatInt(row.UpstreamOutputTokens, 10),
			strconv.FormatInt(row.BilledOutputTokens, 10),
			strconv.FormatInt(row.OutputTokenDiff, 10),
			strconv.FormatInt(row.UpstreamCacheReadTokens, 10),
			strconv.FormatInt(row.UpstreamCacheWriteTokens, 10),
			strconv.FormatInt(row.BilledCachedInputTokens, 10),
			strconv.FormatInt(row.BilledReasoningTokens, 10),
			fmt.Sprintf("%.4f", row.EstimatedUpstreamCostUSD),
			fmt.Sprintf("%.4f", row.UpstreamInputCostUSD),
			fmt.Sprintf("%.4f", row.UpstreamOutputCostUSD),
			fmt.Sprintf("%.4f", row.UpstreamCacheCostUSD),
			fmt.Sprintf("%.4f", row.UpstreamCacheWriteCostUSD),
			fmt.Sprintf("%.4f", row.UpstreamFixedCostUSD),
			fmt.Sprintf("%.4f", row.BilledCostUSD),
			fmt.Sprintf("%.4f", row.BilledInputCostUSD),
			fmt.Sprintf("%.4f", row.BilledOutputCostUSD),
			fmt.Sprintf("%.4f", row.BilledCacheCostUSD),
			fmt.Sprintf("%.4f", row.BilledReasoningCostUSD),
			fmt.Sprintf("%.4f", row.BilledFixedCostUSD),
			fmt.Sprintf("%.4f", row.CostDiffUSD),
			fmt.Sprintf("%.4f", row.GrossMarginUSD),
			fmt.Sprintf("%.2f", row.GrossMarginPct),
			strconv.FormatInt(row.ErrorCount, 10),
			fmt.Sprintf("%.2f", row.ErrorRate),
		})
	}
}

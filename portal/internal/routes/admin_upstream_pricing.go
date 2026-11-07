package routes

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mayiants/ai-portal-go/internal/services"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// upstreamPricingModel is the service-model row exposed for upstream cost config.
type upstreamPricingSummary struct {
	Total      int `json:"total"`
	Priced     int `json:"priced"`
	Inactive   int `json:"inactive"`
	Incomplete int `json:"incomplete"`
}

type upstreamPricingModel struct {
	ID                     string                 `json:"id"`
	Provider               string                 `json:"provider"`
	ProductID              string                 `json:"product_id"`
	APIModelName           *string                `json:"api_model_name"`
	Status                 string                 `json:"status"`
	Currency               string                 `json:"currency"`
	BillingType            string                 `json:"billing_type"`
	Capabilities           map[string]interface{} `json:"capabilities"`
	InputPriceCacheMiss    *float64               `json:"input_price_cache_miss"`
	InputPriceCacheHit     *float64               `json:"input_price_cache_hit"`
	OutputPrice            *float64               `json:"output_price"`
	CacheReadPrice         *float64               `json:"cache_read_price"`
	CacheWritePrice        *float64               `json:"cache_write_price"`
	ReasoningPrice         *float64               `json:"reasoning_price"`
	FixedPrice             *float64               `json:"fixed_price"`
	DownstreamInputPrice   *float64               `json:"downstream_input_price"`
	DownstreamOutputPrice  *float64               `json:"downstream_output_price"`
	DownstreamFixedRequest *float64               `json:"downstream_fixed_request"`
	MarginPercent          *float64               `json:"margin_percent"`
}

func (r *AdminRoutes) listUpstreamPricing(c *gin.Context) {
	provider := c.Query("provider")
	search := c.Query("search")
	// status filter: active | inactive (non-servable statuses) | all (default).
	statusFilter := c.Query("status")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 500 {
		limit = 100
	}
	offset := (page - 1) * limit

	where := "WHERE deleted_at IS NULL"
	args := []interface{}{}
	idx := 1
	switch statusFilter {
	case "active":
		where += " AND status = 'active'"
	case "inactive":
		where += " AND status IN ('paused','eol_pending','eol','discontinued','inactive','disabled','deprecated','ignored','pending')"
	}
	if provider != "" && provider != "all" {
		where += fmt.Sprintf(" AND provider = $%d", idx)
		args = append(args, provider)
		idx++
	}
	if search != "" {
		where += fmt.Sprintf(" AND (product_id ILIKE $%d OR COALESCE(api_model_name,'') ILIKE $%d OR provider ILIKE $%d)", idx, idx, idx)
		args = append(args, "%"+search+"%")
		idx++
	}

	countQuery := "SELECT COUNT(*) FROM service_models " + where
	var total int
	_ = r.db.QueryRow(c.Request.Context(), countQuery, args...).Scan(&total)

	summaryQuery := fmt.Sprintf(`
		SELECT
			COUNT(*) FILTER (%s)::int,
			COUNT(*) FILTER (%s AND COALESCE(input_price_cache_miss,0) > 0 AND COALESCE(input_price_cache_hit,0) > 0 AND COALESCE(output_price,0) > 0)::int,
			COUNT(*) FILTER (%s AND status IN ('paused','eol_pending','eol','discontinued','inactive','disabled','deprecated','ignored','pending'))::int,
			COUNT(*) FILTER (%s AND (COALESCE(input_price_cache_miss,0) = 0 OR COALESCE(input_price_cache_hit,0) = 0 OR COALESCE(output_price,0) = 0))::int
		FROM service_models`, where, where, where, where)
	var summary upstreamPricingSummary
	_ = r.db.QueryRow(c.Request.Context(), summaryQuery, args...).Scan(
		&summary.Total, &summary.Priced, &summary.Inactive, &summary.Incomplete)

	query := `SELECT id::text, provider, product_id, api_model_name, status, COALESCE(currency,'USD'),
			 COALESCE(billing_type,'per_token') as billing_type,
			 COALESCE(capabilities, '{}') as capabilities,
			 input_price_cache_miss, input_price_cache_hit, output_price, cache_read_price,
			 cache_write_price, reasoning_price, fixed_price
		  FROM service_models ` + where + ` ORDER BY provider, product_id LIMIT $` + strconv.Itoa(idx) + ` OFFSET $` + strconv.Itoa(idx+1)
	args = append(args, limit, offset)

	rows, err := r.db.Query(c.Request.Context(), query, args...)
	if err != nil {
		r.logger.Error("failed to list upstream pricing", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list upstream pricing"}})
		return
	}
	defer rows.Close()

	models := []upstreamPricingModel{}
	for rows.Next() {
		var m upstreamPricingModel
		var capsRaw []byte
		if err := rows.Scan(&m.ID, &m.Provider, &m.ProductID, &m.APIModelName, &m.Status, &m.Currency,
			&m.BillingType, &capsRaw,
			&m.InputPriceCacheMiss, &m.InputPriceCacheHit, &m.OutputPrice, &m.CacheReadPrice,
			&m.CacheWritePrice, &m.ReasoningPrice, &m.FixedPrice); err != nil {
			continue
		}
		if len(capsRaw) > 0 && string(capsRaw) != "{}" && string(capsRaw) != "null" {
			_ = json.Unmarshal(capsRaw, &m.Capabilities)
		}
		if m.Capabilities == nil {
			m.Capabilities = map[string]interface{}{}
		}
		models = append(models, m)
	}

	// Enrich with the effective downstream customer price and gross margin.
	ctx := c.Request.Context()
	now := time.Now()
	for i := range models {
		m := &models[i]
		modelName := m.ProductID
		if m.APIModelName != nil && *m.APIModelName != "" {
			modelName = *m.APIModelName
		}
		rule, err := r.pricingSvc.MatchRule(ctx, m.Provider, "*", modelName, now)
		if err != nil {
			r.logger.Warn("failed to match downstream rule for upstream pricing",
				zap.String("provider", m.Provider), zap.String("model", modelName), zap.Error(err))
		}
		if rule != nil {
			m.DownstreamInputPrice = &rule.Prices.Input
			m.DownstreamOutputPrice = &rule.Prices.Output
			m.DownstreamFixedRequest = &rule.Prices.FixedRequest
			m.MarginPercent = computeMarginPercent(m.InputPriceCacheMiss, m.OutputPrice, rule.Prices.Input, rule.Prices.Output)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"models":      models,
		"total":       total,
		"summary":     summary,
		"page":        page,
		"limit":       limit,
		"total_pages": (total + limit - 1) / limit,
	})
}

// computeMarginPercent returns a simple blended gross margin based on input and
// output prices. It prefers the input-price margin when both upstream and
// downstream input prices are present; otherwise it falls back to output price.
func computeMarginPercent(upInput, upOutput *float64, downInput, downOutput float64) *float64 {
	var up, down float64
	var useInput bool
	if upInput != nil && *upInput > 0 && downInput > 0 {
		up = *upInput
		down = downInput
		useInput = true
	}
	if !useInput {
		if upOutput == nil || *upOutput <= 0 || downOutput <= 0 {
			return nil
		}
		up = *upOutput
		down = downOutput
	}
	margin := (down - up) / up * 100
	return &margin
}

func (r *AdminRoutes) updateUpstreamPricing(c *gin.Context) {
	modelID := c.Param("id")
	var req struct {
		Currency            string                  `json:"currency"`
		InputPriceCacheMiss *float64                `json:"input_price_cache_miss"`
		InputPriceCacheHit  *float64                `json:"input_price_cache_hit"`
		OutputPrice         *float64                `json:"output_price"`
		CacheReadPrice      *float64                `json:"cache_read_price"`
		CacheWritePrice     *float64                `json:"cache_write_price"`
		ReasoningPrice      *float64                `json:"reasoning_price"`
		FixedPrice          *float64                `json:"fixed_price"`
		BillingType         string                  `json:"billing_type"`
		Variants            []services.PriceVariant `json:"variants"`
		BlockSeconds        int                     `json:"block_seconds"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	ctx := c.Request.Context()
	setParts := []string{"updated_at = NOW()"}
	args := []interface{}{}
	idx := 1

	add := func(name string, v interface{}) {
		setParts = append(setParts, fmt.Sprintf("%s = $%d", name, idx))
		args = append(args, v)
		idx++
	}

	if req.Currency != "" {
		add("currency", req.Currency)
	}
	if req.InputPriceCacheMiss != nil {
		add("input_price_cache_miss", *req.InputPriceCacheMiss)
	}
	if req.InputPriceCacheHit != nil {
		add("input_price_cache_hit", *req.InputPriceCacheHit)
	}
	if req.OutputPrice != nil {
		add("output_price", *req.OutputPrice)
	}
	if req.CacheReadPrice != nil {
		add("cache_read_price", *req.CacheReadPrice)
	}
	if req.CacheWritePrice != nil {
		add("cache_write_price", *req.CacheWritePrice)
	}
	if req.ReasoningPrice != nil {
		add("reasoning_price", *req.ReasoningPrice)
	}
	if req.FixedPrice != nil {
		add("fixed_price", *req.FixedPrice)
	}
	if req.BillingType != "" {
		add("billing_type", req.BillingType)
	}

	query := fmt.Sprintf("UPDATE service_models SET %s WHERE id = $%d", strings.Join(setParts, ", "), idx)
	args = append(args, modelID)
	if err := r.db.Exec(ctx, query, args...); err != nil {
		r.logger.Error("failed to update upstream pricing", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update upstream pricing"}})
		return
	}

	// If variants were provided, update capabilities.pricing and mirror to the
	// active pricing rule so media-mode billing takes effect immediately.
	if req.Variants != nil {
		if err := r.updateUpstreamVariants(ctx, modelID, req.Variants, req.BlockSeconds); err != nil {
			r.logger.Error("failed to update upstream variants", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update variants"}})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *AdminRoutes) updateUpstreamVariants(ctx context.Context, modelID string, variants []services.PriceVariant, blockSeconds int) error {
	if len(variants) == 0 {
		return nil
	}
	for i := range variants {
		if strings.TrimSpace(variants[i].Label) == "" {
			return fmt.Errorf("variant label is required")
		}
		if variants[i].Price < 0 {
			return fmt.Errorf("variant price cannot be negative")
		}
		if variants[i].Unit == "" {
			variants[i].Unit = "request"
		}
		switch variants[i].Unit {
		case "image", "second", "block", "request":
		default:
			return fmt.Errorf("invalid variant unit: %s", variants[i].Unit)
		}
	}
	if blockSeconds < 1 {
		blockSeconds = 1
	}

	var provider, productID, apiModelName string
	err := r.db.QueryRow(ctx,
		`SELECT provider, product_id, COALESCE(api_model_name, product_id)
		 FROM service_models WHERE id = $1::uuid AND deleted_at IS NULL`,
		modelID,
	).Scan(&provider, &productID, &apiModelName)
	if err != nil {
		return err
	}

	capabilities := map[string]interface{}{
		"pricing": map[string]interface{}{
			"variants":      variants,
			"block_seconds": blockSeconds,
		},
	}
	capsJSON, err := json.Marshal(capabilities)
	if err != nil {
		return err
	}

	if err := r.db.Exec(ctx,
		`UPDATE service_models
		 SET capabilities = COALESCE(capabilities, '{}'::jsonb) || $1::jsonb,
		     billing_type = 'per_request',
		     updated_at = NOW()
		 WHERE id = $2::uuid`,
		capsJSON, modelID,
	); err != nil {
		return err
	}

	prices := services.ComponentPrices{Variants: variants}
	rule := &services.PricingRule{
		Provider:      provider,
		Region:        "*",
		ProductID:     productID,
		APIModelName:  apiModelName,
		BillingMode:   "media",
		Prices:        prices,
		Currency:      "USD",
		Timezone:      "UTC",
		Priority:      0,
		Status:        "active",
		EffectiveFrom: time.Now(),
		RuleName:      fmt.Sprintf("Auto media: %s", apiModelName),
		Note:          "generated from upstream variant pricing",
		BlockSeconds:  blockSeconds,
	}

	var existingID string
	_ = r.db.QueryRow(ctx,
		`SELECT id::text FROM model_pricing_rules
		 WHERE provider = $1 AND product_id = $2 AND status = 'active'
		 LIMIT 1`,
		provider, productID,
	).Scan(&existingID)

	if existingID != "" {
		rule.ID = existingID
		return r.pricingSvc.UpdateRule(ctx, rule)
	}
	_, err = r.pricingSvc.CreateRule(ctx, rule)
	return err
}

// generateDownstreamPricing creates or updates a downstream pricing rule from
// the upstream cost basis plus an optional markup. The generated prices are
// guaranteed to be at or above the upstream costs by the inversion validator.
// For media models (image/video/audio with variants) it generates a media-mode
// rule with per-variant prices and a small per-request fixed fee.
func (r *AdminRoutes) generateDownstreamPricing(c *gin.Context) {
	modelID := c.Param("id")
	var req struct {
		MarkupPercent float64 `json:"markup_percent"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	ctx := c.Request.Context()
	var provider, productID, apiModelName, billingType string
	var capsRaw []byte
	var upInputMiss, upInputHit, upOutput, upCacheWrite, upReasoning, upFixed sql.NullFloat64
	err := r.db.QueryRow(ctx,
		`SELECT provider, product_id, COALESCE(api_model_name, product_id),
		        billing_type, COALESCE(capabilities, '{}'),
		        input_price_cache_miss, input_price_cache_hit, output_price,
		        cache_write_price, reasoning_price, fixed_price
		 FROM service_models
		 WHERE id = $1::uuid AND deleted_at IS NULL`,
		modelID,
	).Scan(&provider, &productID, &apiModelName,
		&billingType, &capsRaw,
		&upInputMiss, &upInputHit, &upOutput,
		&upCacheWrite, &upReasoning, &upFixed)
	if err != nil {
		r.logger.Error("failed to load upstream model for downstream generation", zap.Error(err))
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Model not found"}})
		return
	}

	markup := 1 + req.MarkupPercent/100
	price := func(v sql.NullFloat64) float64 {
		if !v.Valid {
			return 0
		}
		return v.Float64 * markup
	}
	fixedRequest := 0.001
	if upFixed.Valid {
		fixedRequest = upFixed.Float64 + 0.001
	}

	// Detect upstream media variants. If present, build a media-mode rule.
	var variants []services.PriceVariant
	var blockSeconds int
	var caps map[string]interface{}
	_ = json.Unmarshal(capsRaw, &caps)
	if pricingBlock, ok := caps["pricing"].(map[string]interface{}); ok {
		if rawVariants, ok := pricingBlock["variants"].([]interface{}); ok && len(rawVariants) > 0 {
			for _, rv := range rawVariants {
				vm, ok := rv.(map[string]interface{})
				if !ok {
					continue
				}
				label, _ := vm["label"].(string)
				unit, _ := vm["unit"].(string)
				if unit == "" {
					unit = "request"
				}
				var p float64
				switch v := vm["price"].(type) {
				case float64:
					p = v
				case json.Number:
					p, _ = v.Float64()
				}
				if label != "" && p >= 0 {
					variants = append(variants, services.PriceVariant{
						Label: label,
						Price: p * markup,
						Unit:  unit,
					})
				}
			}
		}
		if bs, ok := pricingBlock["block_seconds"].(float64); ok {
			blockSeconds = int(bs)
		}
	}

	// When the upstream model carries a per-request fixed cost or media variants,
	// ensure the service model is tagged as per_request so galleries and status
	// checks treat it consistently.
	if (upFixed.Valid && upFixed.Float64 > 0) || len(variants) > 0 {
		_ = r.db.Exec(ctx,
			`UPDATE service_models SET billing_type = 'per_request', updated_at = NOW()
			 WHERE id = $1::uuid AND COALESCE(billing_type,'per_token') = 'per_token'`,
			modelID)
	}

	rule := &services.PricingRule{
		Provider:      provider,
		Region:        "*",
		ProductID:     productID,
		APIModelName:  apiModelName,
		Currency:      "USD",
		Timezone:      "UTC",
		Priority:      0,
		Status:        "active",
		EffectiveFrom: time.Now(),
		RuleName:      fmt.Sprintf("Auto: %s", apiModelName),
		Note:          fmt.Sprintf("generated from upstream pricing with %.2f%% markup", req.MarkupPercent),
	}
	if len(variants) > 0 {
		rule.BillingMode = "media"
		rule.Prices = services.ComponentPrices{
			FixedRequest: fixedRequest,
			Variants:     variants,
		}
		rule.BlockSeconds = blockSeconds
	} else {
		rule.BillingMode = "flat"
		rule.Prices = services.ComponentPrices{
			Input:        price(upInputMiss),
			Output:       price(upOutput),
			CacheHit:     price(upInputHit),
			CacheWrite:   price(upCacheWrite),
			Reasoning:    price(upReasoning),
			FixedRequest: fixedRequest,
		}
	}

	var existingID string
	_ = r.db.QueryRow(ctx,
		`SELECT id::text FROM model_pricing_rules
		 WHERE provider = $1 AND product_id = $2 AND status = 'active'
		 LIMIT 1`,
		provider, productID,
	).Scan(&existingID)

	var ruleID string
	if existingID != "" {
		rule.ID = existingID
		if err := r.pricingSvc.UpdateRule(ctx, rule); err != nil {
			r.logger.Error("failed to update generated downstream rule", zap.Error(err))
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error()}})
			return
		}
		ruleID = existingID
	} else {
		id, err := r.pricingSvc.CreateRule(ctx, rule)
		if err != nil {
			r.logger.Error("failed to create generated downstream rule", zap.Error(err))
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error()}})
			return
		}
		ruleID = id
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "id": ruleID})
}

func (r *AdminRoutes) exportUpstreamPricingCSV(c *gin.Context) {
	where := "deleted_at IS NULL"
	statusFilter := c.Query("status")
	switch statusFilter {
	case "active":
		where += " AND status = 'active'"
	case "inactive":
		where += " AND status IN ('paused','eol_pending','eol','discontinued','inactive','disabled','deprecated','ignored','pending')"
	}
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT provider, product_id, COALESCE(api_model_name,''), status, COALESCE(currency,'USD'),
			 input_price_cache_miss, input_price_cache_hit, output_price, cache_read_price,
			 cache_write_price, reasoning_price, fixed_price
		 FROM service_models
		 WHERE `+where+`
		 ORDER BY provider, product_id`)
	if err != nil {
		r.logger.Error("failed to export upstream pricing", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to export upstream pricing"}})
		return
	}
	defer rows.Close()

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=upstream-pricing-"+time.Now().UTC().Format("200601021504")+".csv")
	_, _ = c.Writer.Write([]byte{0xEF, 0xBB, 0xBF})

	w := csv.NewWriter(c.Writer)
	defer w.Flush()
	_ = w.Write([]string{
		"provider", "product_id", "api_model_name", "status", "currency",
		"input_price_cache_miss", "input_price_cache_hit", "output_price", "cache_read_price",
		"cache_write_price", "reasoning_price", "fixed_price",
	})

	for rows.Next() {
		var provider, productID, apiModelName, status, currency string
		var inputMiss, inputHit, output, cacheRead, cacheWrite, reasoning, fixed *float64
		if err := rows.Scan(&provider, &productID, &apiModelName, &status, &currency,
			&inputMiss, &inputHit, &output, &cacheRead, &cacheWrite, &reasoning, &fixed); err != nil {
			continue
		}
		_ = w.Write([]string{
			provider, productID, apiModelName, status, currency,
			fmtPrice(inputMiss), fmtPrice(inputHit), fmtPrice(output), fmtPrice(cacheRead),
			fmtPrice(cacheWrite), fmtPrice(reasoning), fmtPrice(fixed),
		})
	}
}

func fmtPrice(p *float64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatFloat(*p, 'f', 6, 64)
}

func parsePrice(s string) *float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &v
}

func (r *AdminRoutes) importUpstreamPricingCSV(c *gin.Context) {
	file, _, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Missing CSV file"}})
		return
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(file)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Failed to read CSV file"}})
		return
	}

	reader := csv.NewReader(strings.NewReader(string(data)))
	records, err := reader.ReadAll()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Failed to parse CSV"}})
		return
	}
	if len(records) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Empty CSV"}})
		return
	}

	header := records[0]
	colIdx := map[string]int{}
	for i, h := range header {
		colIdx[strings.TrimSpace(h)] = i
	}
	get := func(row []string, name string) string {
		idx, ok := colIdx[name]
		if !ok || idx >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[idx])
	}

	updated := 0
	skipped := 0
	ctx := c.Request.Context()
	for _, row := range records[1:] {
		provider := get(row, "provider")
		productID := get(row, "product_id")
		if provider == "" || productID == "" {
			skipped++
			continue
		}

		var id string
		err := r.db.QueryRow(ctx,
			`SELECT id::text FROM service_models WHERE provider = $1 AND product_id = $2 AND deleted_at IS NULL`,
			provider, productID).Scan(&id)
		if err != nil {
			skipped++
			continue
		}

		setParts := []string{"updated_at = NOW()"}
		args := []interface{}{}
		idx := 1
		add := func(name string, v interface{}) {
			setParts = append(setParts, fmt.Sprintf("%s = $%d", name, idx))
			args = append(args, v)
			idx++
		}

		currency := get(row, "currency")
		if currency != "" {
			add("currency", currency)
		}
		if v := parsePrice(get(row, "input_price_cache_miss")); v != nil {
			add("input_price_cache_miss", *v)
		}
		if v := parsePrice(get(row, "input_price_cache_hit")); v != nil {
			add("input_price_cache_hit", *v)
		}
		if v := parsePrice(get(row, "output_price")); v != nil {
			add("output_price", *v)
		}
		if v := parsePrice(get(row, "cache_read_price")); v != nil {
			add("cache_read_price", *v)
		}
		if v := parsePrice(get(row, "cache_write_price")); v != nil {
			add("cache_write_price", *v)
		}
		if v := parsePrice(get(row, "reasoning_price")); v != nil {
			add("reasoning_price", *v)
		}
		if v := parsePrice(get(row, "fixed_price")); v != nil {
			add("fixed_price", *v)
		}

		if len(setParts) == 1 { // only updated_at
			skipped++
			continue
		}
		query := fmt.Sprintf("UPDATE service_models SET %s WHERE id = $%d", strings.Join(setParts, ", "), idx)
		args = append(args, id)
		if err := r.db.Exec(ctx, query, args...); err != nil {
			r.logger.Warn("failed to import upstream pricing row", zap.Error(err))
			skipped++
			continue
		}
		updated++
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"updated": updated,
		"skipped": skipped,
		"total":   len(records) - 1,
	})
}

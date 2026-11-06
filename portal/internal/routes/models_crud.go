package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/services"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// modelManufacturer derives the original model manufacturer (lowercase) from
// the model name, used as fallback when service_models.vendor is NULL.
// The mapping mirrors the family logic in resolveReferencePricing.
func modelManufacturer(apiModelName, productID string) string {
	name := strings.ToLower(apiModelName + " " + productID)
	switch {
	case strings.Contains(name, "claude") || strings.Contains(name, "opus") ||
		strings.Contains(name, "sonnet") || strings.Contains(name, "haiku") ||
		strings.Contains(name, "fable"):
		return "anthropic"
	case strings.Contains(name, "gpt") || strings.Contains(name, "o3") ||
		strings.Contains(name, "o4") || strings.Contains(name, "dall-e"):
		return "openai"
	case strings.Contains(name, "gemini"):
		return "google"
	case strings.Contains(name, "nova"):
		return "amazon"
	case strings.Contains(name, "llama"):
		return "meta"
	case strings.Contains(name, "mistral") || strings.Contains(name, "mixtral") ||
		strings.Contains(name, "magistral") || strings.Contains(name, "ministral") ||
		strings.Contains(name, "devstral"):
		return "mistral ai"
	case strings.Contains(name, "deepseek"):
		return "deepseek"
	case strings.Contains(name, "qwen"):
		return "alibaba"
	case strings.Contains(name, "minimax"):
		return "minimax"
	case strings.Contains(name, "glm"):
		return "zhipu ai"
	case strings.Contains(name, "kimi") || strings.Contains(name, "moonshot"):
		return "moonshot ai"
	case strings.Contains(name, "kling") || strings.Contains(name, "kuaishou"):
		return "kuaishou"
	case strings.Contains(name, "seedream") || strings.Contains(name, "seed") || strings.Contains(name, "bytedance"):
		return "bytedance"
	case strings.Contains(name, "hailuo") || strings.Contains(name, "minimax"):
		return "minimax"
	case strings.Contains(name, "baichuan"):
		return "baichuan"
	case strings.Contains(name, "step") || strings.Contains(name, "stepfun"):
		return "stepfun"
	case strings.Contains(name, "yi-") || strings.Contains(name, "yi_"):
		return "01.ai"
	case strings.Contains(name, "nvidia") || strings.Contains(name, "nemotron"):
		return "nvidia"
	case strings.Contains(name, "xiaomi") || strings.Contains(name, "mimo"):
		return "xiaomi"
	case strings.Contains(name, "microsoft") || strings.Contains(name, "phi"):
		return "microsoft"
	case strings.Contains(name, "grok") || strings.Contains(name, "xai") || strings.Contains(name, "x-ai"):
		return "x-ai"
	case strings.Contains(name, "sonar"):
		return "perplexity"
	case strings.Contains(name, "gemma"):
		return "google"
	case strings.Contains(name, "command"):
		return "cohere"
	case strings.Contains(name, "hunyuan") || strings.Contains(name, "tencent"):
		return "tencent"
	case strings.Contains(name, "fugu"):
		return "sakana"
	case strings.Contains(name, "inkling"):
		return "thinkingmachines"
	case strings.Contains(name, "laguna"):
		return "poolside"
	case strings.Contains(name, "hermes"):
		return "nousresearch"
	case strings.Contains(name, "solar"):
		return "upstage"
	case strings.Contains(name, "palmyra"):
		return "writer"
	case strings.Contains(name, "granite"):
		return "ibm"
	default:
		return "other"
	}
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// listPublicModels returns the public model gallery. Prices are read from
// model_pricing_rules (downstream customer prices) rather than service_models
// (upstream costs). When the same api_model_name is served by multiple
// providers, the representative provider is chosen using the admin-configured
// model_provider_routing table, falling back to the cheapest upstream input
// price to match the previous behaviour.
func (r *ModelRoutes) listPublicModels(c *gin.Context) {
	ctx := c.Request.Context()
	now := time.Now()

	where := "WHERE status IN ('active', 'eol_pending') AND show_in_store = true AND COALESCE(hidden, false) = false"
	args := []interface{}{}
	argIdx := 1

	if provider := c.Query("provider"); provider != "" {
		where += fmt.Sprintf(" AND provider = $%d", argIdx)
		args = append(args, provider)
		argIdx++
	}
	if modelType := c.Query("type"); modelType != "" {
		// Filter by the model's declared primary type; supported_types is used
		// only for badges/health thresholds, not for type counting.
		where += fmt.Sprintf(` AND COALESCE(NULLIF(aimodels_data->>'type', ''), capabilities->>'type', 'text') = $%d`, argIdx)
		args = append(args, modelType)
		argIdx++
	}
	if q := c.Query("q"); q != "" {
		where += fmt.Sprintf(" AND (api_model_name ILIKE $%d OR aimodels_data->>'name' ILIKE $%d OR product_id ILIKE $%d)", argIdx, argIdx, argIdx)
		args = append(args, "%"+q+"%")
	}

	query := `SELECT provider, product_id, api_model_name, vendor,
	       input_price_cache_miss, output_price, input_price_cache_hit,
	       aimodels_data->>'name' as name,
	       aimodels_data->>'description' as description,
	       aimodels_data->>'features' as features,
	       aimodels_data->>'context_window' as context_window,
	       COALESCE(NULLIF(aimodels_data->>'type', ''), capabilities->>'type', 'text') as model_type,
	       COALESCE(NULLIF(aimodels_data->>'billing_type', ''), 'pay_as_you_go') as billing_type,
	       COALESCE(context_window, 0) as db_context_window,
	       COALESCE(max_output_tokens, 0) as db_max_output_tokens,
	       COALESCE(capabilities, '{}') as capabilities,
	       status, eol_service_at
	 FROM service_models ` + where + ` ORDER BY provider, product_id`

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		r.logger.Error("failed to list public models", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list models"}})
		return
	}
	defer rows.Close()

	type candidate struct {
		provider, productID, status, modelType, billingType  string
		vendor                                               *string
		apiModelName                                         *string
		inputPriceCacheMiss, outputPrice, inputPriceCacheHit *float64
		name, description, features, contextWindow           *string
		dbContextWindow, dbMaxOutputTokens                   int
		capabilitiesRaw                                      []byte
		eolServiceAt                                         *time.Time
	}

	var candidates []candidate
	apiNames := make(map[string]struct{})
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.provider, &c.productID, &c.apiModelName, &c.vendor,
			&c.inputPriceCacheMiss, &c.outputPrice, &c.inputPriceCacheHit,
			&c.name, &c.description, &c.features, &c.contextWindow,
			&c.modelType, &c.billingType,
			&c.dbContextWindow, &c.dbMaxOutputTokens, &c.capabilitiesRaw,
			&c.status, &c.eolServiceAt); err != nil {
			r.logger.Warn("failed to scan public model candidate", zap.Error(err))
			continue
		}
		candidates = append(candidates, c)
		if c.apiModelName != nil && *c.apiModelName != "" {
			apiNames[*c.apiModelName] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		r.logger.Error("failed to read public model candidates", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to read models"}})
		return
	}

	// Load routing preferences for candidate model names.
	routing := make(map[string][]struct {
		provider string
		tier     string
		priority int
	})
	if len(apiNames) > 0 {
		names := make([]string, 0, len(apiNames))
		for n := range apiNames {
			names = append(names, n)
		}
		placeholders := make([]string, len(names))
		nameArgs := make([]interface{}, len(names))
		for i, n := range names {
			placeholders[i] = fmt.Sprintf("$%d", i+1)
			nameArgs[i] = n
		}
		rq := `SELECT model_name, provider, tier, priority
			   FROM model_provider_routing
			   WHERE enabled = true AND model_name IN (` + strings.Join(placeholders, ",") + `)
			   ORDER BY model_name, priority`
		rRows, err := r.db.Query(ctx, rq, nameArgs...)
		if err != nil {
			r.logger.Warn("failed to load model provider routing", zap.Error(err))
		} else {
			defer rRows.Close()
			for rRows.Next() {
				var modelName, provider, tier string
				var priority int
				if err := rRows.Scan(&modelName, &provider, &tier, &priority); err != nil {
					continue
				}
				routing[modelName] = append(routing[modelName], struct {
					provider string
					tier     string
					priority int
				}{provider, tier, priority})
			}
		}
	}

	// Group candidates by api_model_name (falling back to product_id).
	groupKey := func(c *candidate) string {
		if c.apiModelName != nil && *c.apiModelName != "" {
			return *c.apiModelName
		}
		return c.productID
	}
	groups := make(map[string][]int)
	var order []string
	for i := range candidates {
		c := &candidates[i]
		key := groupKey(c)
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], i)
	}

	// Pick one provider per api_model_name.
	selected := make([]*candidate, 0, len(order))
	for _, key := range order {
		idxs := groups[key]
		chosenIdx := idxs[0]
		prefs := routing[key]
		if len(prefs) > 0 {
			providers := make(map[string]int, len(idxs))
			for _, idx := range idxs {
				providers[candidates[idx].provider] = idx
			}
			for _, p := range prefs {
				if idx, ok := providers[p.provider]; ok {
					chosenIdx = idx
					break
				}
			}
		} else {
			// Fallback: cheapest upstream input price.
			for _, idx := range idxs[1:] {
				cand := candidates[idx]
				best := candidates[chosenIdx]
				if best.inputPriceCacheMiss == nil || (cand.inputPriceCacheMiss != nil && *cand.inputPriceCacheMiss < *best.inputPriceCacheMiss) {
					chosenIdx = idx
				}
			}
		}
		selected = append(selected, &candidates[chosenIdx])
	}

	// Resolve downstream pricing rules for selected models.
	ruleCache := make(map[string]*services.PricingRule)
	for _, c := range selected {
		modelName := c.productID
		if c.apiModelName != nil && *c.apiModelName != "" {
			modelName = *c.apiModelName
		}
		rule, err := r.pricingSvc.MatchRule(ctx, c.provider, "*", modelName, now)
		if err != nil {
			r.logger.Warn("failed to match pricing rule for public model",
				zap.String("provider", c.provider), zap.String("model", modelName), zap.Error(err))
		}
		ruleCache[modelName] = rule
	}

	// Apply user-facing sort.
	sortMode := c.Query("sort")
	switch sortMode {
	case "price-low", "price-high":
		less := func(i, j int) bool {
			a := selected[i]
			b := selected[j]
			modelA := a.productID
			if a.apiModelName != nil && *a.apiModelName != "" {
				modelA = *a.apiModelName
			}
			modelB := b.productID
			if b.apiModelName != nil && *b.apiModelName != "" {
				modelB = *b.apiModelName
			}
			ra := ruleCache[modelA]
			rb := ruleCache[modelB]
			var pa, pb float64
			if ra != nil {
				pa = ra.Prices.Input
			}
			if rb != nil {
				pb = rb.Prices.Input
			}
			if sortMode == "price-low" {
				return pa < pb
			}
			return pa > pb
		}
		sort.SliceStable(selected, less)
	case "name":
		sort.SliceStable(selected, func(i, j int) bool {
			a := selected[i]
			b := selected[j]
			na := a.name
			if na == nil || *na == "" {
				na = a.apiModelName
			}
			nb := b.name
			if nb == nil || *nb == "" {
				nb = b.apiModelName
			}
			return derefStr(na) < derefStr(nb)
		})
	default:
		// Default: vendor/provider, then api_model_name.
		sort.SliceStable(selected, func(i, j int) bool {
			a, b := selected[i], selected[j]
			va := derefStr(a.vendor)
			if va == "" {
				va = a.provider
			}
			vb := derefStr(b.vendor)
			if vb == "" {
				vb = b.provider
			}
			if va != vb {
				return va < vb
			}
			return derefStr(a.apiModelName) < derefStr(b.apiModelName)
		})
	}

	if len(selected) > 500 {
		selected = selected[:500]
	}

	// Capability master data (manual/probe/upstream overrides) takes
	// precedence over the per-provider capabilities row.
	var capOverrides map[string][]string
	{
		names := make([]string, 0, len(selected))
		for _, c := range selected {
			names = append(names, derefStr(c.apiModelName))
		}
		capOverrides = loadCapabilityOverrides(ctx, r.db, names)
	}

	models := []gin.H{}
	for _, c := range selected {
		manufacturer := modelManufacturer(derefStr(c.apiModelName), c.productID)
		if manufacturer == "other" && c.vendor != nil && *c.vendor != "" {
			manufacturer = *c.vendor
		}

		capabilities := make(map[string]interface{})
		if len(c.capabilitiesRaw) > 0 && string(c.capabilitiesRaw) != "{}" && string(c.capabilitiesRaw) != "null" {
			_ = json.Unmarshal(c.capabilitiesRaw, &capabilities)
		}
		supportedTypes := []string{}
		if st, ok := capabilities["supported_types"].([]interface{}); ok {
			for _, t := range st {
				if s, ok := t.(string); ok && s != "" {
					supportedTypes = append(supportedTypes, s)
				}
			}
		}
		if len(supportedTypes) == 0 {
			supportedTypes = []string{c.modelType}
		}
		if ov, ok := capOverrides[derefStr(c.apiModelName)]; ok {
			supportedTypes = ov
		}
		contextWindow := c.contextWindow
		if c.dbContextWindow > 0 {
			cw := strconv.Itoa(c.dbContextWindow)
			contextWindow = &cw
		}

		modelName := c.productID
		if c.apiModelName != nil && *c.apiModelName != "" {
			modelName = *c.apiModelName
		}
		rule := ruleCache[modelName]

		entry := gin.H{
			"provider": c.provider, "product_id": c.productID, "api_model_name": c.apiModelName,
			"manufacturer": manufacturer,
			"name":         c.name, "description": c.description,
			"features": c.features, "context_window": contextWindow,
			"max_output_tokens": c.dbMaxOutputTokens,
			"model_type":        c.modelType,
			"supported_types":   supportedTypes,
			"billing_type":      c.billingType,
			"capabilities":      capabilities,
			"status":            c.status,
		}
		if rule != nil {
			entry["input_price"] = rule.Prices.Input
			entry["output_price"] = rule.Prices.Output
			entry["input_price_cache_hit"] = rule.Prices.CacheHit
			entry["input_price_cache_miss"] = rule.Prices.Input
			entry["reasoning_price"] = rule.Prices.Reasoning
			entry["fixed_price"] = rule.Prices.FixedRequest
		} else {
			entry["pricing_missing"] = true
		}
		if c.eolServiceAt != nil {
			entry["eol_service_at"] = c.eolServiceAt.Format("2006-01-02")
			daysRemaining := int(time.Until(*c.eolServiceAt).Hours() / 24)
			if daysRemaining > 0 {
				entry["eol_days_remaining"] = daysRemaining
			}
		}
		models = append(models, entry)
	}
	c.JSON(http.StatusOK, gin.H{"models": models})
}

// listServiceModelsPublic is the legacy public catalog endpoint used by the
// key model selector and landing page. It returns downstream customer prices
// from model_pricing_rules and falls back to capabilities when aimodels_data
// does not include the model type.
func (r *ModelRoutes) listServiceModelsPublic(c *gin.Context) {
	ctx := c.Request.Context()
	now := time.Now()

	rows, err := r.db.Query(ctx, `
		SELECT provider, product_id, api_model_name, vendor,
		       input_price_cache_miss, output_price, input_price_cache_hit,
		       cache_write_price, reasoning_price, fixed_price,
		       aimodels_data->>'name' as name,
		       aimodels_data->>'description' as description,
		       COALESCE(NULLIF(aimodels_data->>'type', ''), capabilities->>'type', 'text') as model_type,
		       COALESCE(NULLIF(aimodels_data->>'billing_type', ''), 'pay_as_you_go') as billing_type,
		       COALESCE(context_window, 0) as context_window,
		       COALESCE(max_output_tokens, 0) as max_output_tokens,
		       COALESCE(capabilities, '{}') as capabilities,
		       COALESCE(is_hot, false) as is_hot,
		       status, eol_service_at
		 FROM service_models
		 WHERE status IN ('active', 'eol_pending') AND show_in_store = true
		   AND COALESCE(hidden, false) = false
		 ORDER BY sort_order, provider, product_id`)
	if err != nil {
		r.logger.Error("failed to list service models public", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list models"}})
		return
	}
	defer rows.Close()

	models := []gin.H{}
	for rows.Next() {
		var provider, productID, status, modelType, billingType string
		var vendor *string
		var apiModelName *string
		var upInputMiss, upOutput, upInputHit, upCacheWrite, upReasoning, upFixed *float64
		var name, description *string
		var contextWindow, maxOutputTokens int
		var capabilitiesRaw []byte
		var isHot bool
		var eolServiceAt *time.Time
		if err := rows.Scan(&provider, &productID, &apiModelName, &vendor,
			&upInputMiss, &upOutput, &upInputHit, &upCacheWrite, &upReasoning, &upFixed,
			&name, &description, &modelType, &billingType,
			&contextWindow, &maxOutputTokens, &capabilitiesRaw,
			&isHot,
			&status, &eolServiceAt); err != nil {
			r.logger.Warn("failed to scan service model public", zap.Error(err))
			continue
		}

		manufacturer := modelManufacturer(derefStr(apiModelName), productID)
		if manufacturer == "other" && vendor != nil && *vendor != "" {
			manufacturer = *vendor
		}

		capabilities := make(map[string]interface{})
		if len(capabilitiesRaw) > 0 && string(capabilitiesRaw) != "{}" && string(capabilitiesRaw) != "null" {
			_ = json.Unmarshal(capabilitiesRaw, &capabilities)
		}
		supportedTypes := []string{}
		if st, ok := capabilities["supported_types"].([]interface{}); ok {
			for _, t := range st {
				if s, ok := t.(string); ok && s != "" {
					supportedTypes = append(supportedTypes, s)
				}
			}
		}
		if len(supportedTypes) == 0 {
			supportedTypes = []string{modelType}
		}

		modelName := productID
		if apiModelName != nil && *apiModelName != "" {
			modelName = *apiModelName
		}
		rule, err := r.pricingSvc.MatchRule(ctx, provider, "*", modelName, now)
		if err != nil {
			r.logger.Warn("failed to match pricing rule for service model public",
				zap.String("provider", provider), zap.String("model", modelName), zap.Error(err))
		}

		entry := gin.H{
			"provider":          provider,
			"product_id":        productID,
			"api_model_name":    apiModelName,
			"manufacturer":      manufacturer,
			"name":              name,
			"description":       description,
			"model_type":        modelType,
			"supported_types":   supportedTypes,
			"billing_type":      billingType,
			"context_window":    contextWindow,
			"max_output_tokens": maxOutputTokens,
			"capabilities":      capabilities,
			"is_hot":            isHot,
			"status":            status,
		}
		if rule != nil {
			entry["input_price"] = rule.Prices.Input
			entry["output_price"] = rule.Prices.Output
			entry["input_price_cache_hit"] = rule.Prices.CacheHit
			entry["input_price_cache_miss"] = rule.Prices.Input
			entry["cache_write_price"] = rule.Prices.CacheWrite
			entry["reasoning_price"] = rule.Prices.Reasoning
			entry["fixed_price"] = rule.Prices.FixedRequest
		} else {
			entry["input_price"] = upInputMiss
			entry["output_price"] = upOutput
			entry["input_price_cache_hit"] = upInputHit
			entry["input_price_cache_miss"] = upInputMiss
			entry["cache_write_price"] = upCacheWrite
			entry["reasoning_price"] = upReasoning
			entry["fixed_price"] = upFixed
		}
		if eolServiceAt != nil {
			entry["eol_service_at"] = eolServiceAt.Format("2006-01-02")
			daysRemaining := int(time.Until(*eolServiceAt).Hours() / 24)
			if daysRemaining > 0 {
				entry["eol_days_remaining"] = daysRemaining
			}
		}
		models = append(models, entry)
	}
	if err := rows.Err(); err != nil {
		r.logger.Error("failed to iterate service models public", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to read models"}})
		return
	}

	// Deduplicate by api_model_name: when the same model exists across multiple
	// providers, keep the entry with the lowest effective price ("from $X").
	deduped := deduplicatePublicModels(models)

	c.JSON(http.StatusOK, gin.H{"models": deduped})
}

// deduplicatePublicModels merges duplicate api_model_name entries, keeping the
// one with the lowest effective price. This handles the same model being served
// by multiple providers at different cost points — users see "from $X" (the minimum).
func deduplicatePublicModels(models []gin.H) []gin.H {
	type entry struct {
		index int
		price float64
	}
	seen := map[string]entry{} // api_model_name → best entry

	for i, m := range models {
		name := ""
		if v, ok := m["api_model_name"].(*string); ok && v != nil {
			name = *v
		} else if v, ok := m["api_model_name"].(string); ok {
			name = v
		}
		if name == "" {
			if v, ok := m["product_id"].(string); ok {
				name = v
			}
		}

		// Compute effective price for comparison
		price := effectiveModelPrice(m)

		prev, exists := seen[name]
		if !exists || price < prev.price {
			seen[name] = entry{index: i, price: price}
		}
	}

	result := make([]gin.H, 0, len(seen))
	for _, e := range seen {
		result = append(result, models[e.index])
	}
	return result
}

// effectiveModelPrice returns the primary price for sorting/comparison.
func effectiveModelPrice(m gin.H) float64 {
	// For per-request models, use fixed_price
	if bt, _ := m["billing_type"].(string); bt == "per_request" {
		if v := toFloat(m["fixed_price"]); v > 0 {
			return v
		}
	}
	// For token models, use input_price as primary indicator
	if v := toFloat(m["input_price"]); v > 0 {
		return v
	}
	if v := toFloat(m["output_price"]); v > 0 {
		return v
	}
	return 0
}

func toFloat(v interface{}) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case *float64:
		if x != nil {
			return *x
		}
	case float32:
		return float64(x)
	case int:
		return float64(x)
	}
	return 0
}

func (r *ModelRoutes) createServiceModel(c *gin.Context) {
	var req struct {
		Provider            string   `json:"provider" binding:"required"`
		ProductID           string   `json:"product_id" binding:"required"`
		APIModelName        string   `json:"api_model_name"`
		Vendor              string   `json:"vendor"`
		InputPriceCacheHit  *float64 `json:"input_price_cache_hit"`
		InputPriceCacheMiss *float64 `json:"input_price_cache_miss"`
		OutputPrice         *float64 `json:"output_price"`
		ReasoningPrice      *float64 `json:"reasoning_price"`
		ShowInStore         bool     `json:"show_in_store"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Provider and product_id required"}})
		return
	}
	if len(req.Vendor) > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Vendor name must be 100 characters or fewer"}})
		return
	}

	var id string
	err := r.db.QueryRow(c.Request.Context(),
		`INSERT INTO service_models (provider, product_id, api_model_name, vendor,
		 input_price_cache_hit, input_price_cache_miss, output_price, reasoning_price,
		 show_in_store, status, confirmed_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'active', NOW()) RETURNING id`,
		req.Provider, req.ProductID, req.APIModelName, strings.ToLower(req.Vendor),
		req.InputPriceCacheHit, req.InputPriceCacheMiss, req.OutputPrice, req.ReasoningPrice,
		req.ShowInStore,
	).Scan(&id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "An internal error occurred"}})
		return
	}

	// Auto-create pricing rule for the new model
	apiName := req.APIModelName
	if apiName == "" {
		apiName = req.ProductID
	}
	r.ensurePricingRule(c.Request.Context(), req.Provider, req.ProductID, apiName)

	c.JSON(http.StatusOK, gin.H{"success": true, "model": gin.H{"id": id}})
}

func (r *ModelRoutes) updateServiceModel(c *gin.Context) {
	modelID := c.Param("id")
	var req struct {
		APIModelName        string          `json:"api_model_name"`
		ProductID           string          `json:"product_id"`
		Vendor              *string         `json:"vendor"`
		InputPrice          *float64        `json:"input_price"`
		InputPriceCacheHit  *float64        `json:"input_price_cache_hit"`
		InputPriceCacheMiss *float64        `json:"input_price_cache_miss"`
		OutputPrice         *float64        `json:"output_price"`
		ReasoningPrice      *float64        `json:"reasoning_price"`
		BillingType         string          `json:"billing_type"`
		FixedPrice          *float64        `json:"fixed_price"`
		TieredPricing       json.RawMessage `json:"tiered_pricing"`
		ShowInStore         *bool           `json:"show_in_store"`
		Status              string          `json:"status"`
	}
	_ = c.ShouldBindJSON(&req)

	if req.APIModelName != "" {
		if err := r.db.Exec(c.Request.Context(), `UPDATE service_models SET api_model_name = $1, updated_at = NOW() WHERE id = $2::uuid`, req.APIModelName, modelID); err != nil {
			r.logger.Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}
	if req.ProductID != "" {
		if err := r.db.Exec(c.Request.Context(), `UPDATE service_models SET product_id = $1, updated_at = NOW() WHERE id = $2::uuid`, req.ProductID, modelID); err != nil {
			r.logger.Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}
	if req.InputPrice != nil {
		if err := r.db.Exec(c.Request.Context(), `UPDATE service_models SET input_price = $1, input_price_cache_miss = $1, updated_at = NOW() WHERE id = $2::uuid`, *req.InputPrice, modelID); err != nil {
			r.logger.Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}
	if req.InputPriceCacheHit != nil {
		if err := r.db.Exec(c.Request.Context(), `UPDATE service_models SET input_price_cache_hit = $1, updated_at = NOW() WHERE id = $2::uuid`, *req.InputPriceCacheHit, modelID); err != nil {
			r.logger.Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}
	if req.InputPriceCacheMiss != nil {
		if err := r.db.Exec(c.Request.Context(), `UPDATE service_models SET input_price_cache_miss = $1, updated_at = NOW() WHERE id = $2::uuid`, *req.InputPriceCacheMiss, modelID); err != nil {
			r.logger.Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}
	if req.OutputPrice != nil {
		if err := r.db.Exec(c.Request.Context(), `UPDATE service_models SET output_price = $1, updated_at = NOW() WHERE id = $2::uuid`, *req.OutputPrice, modelID); err != nil {
			r.logger.Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}
	if req.ReasoningPrice != nil {
		if err := r.db.Exec(c.Request.Context(), `UPDATE service_models SET reasoning_price = $1, updated_at = NOW() WHERE id = $2::uuid`, *req.ReasoningPrice, modelID); err != nil {
			r.logger.Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}
	if req.BillingType != "" {
		if err := r.db.Exec(c.Request.Context(), `UPDATE service_models SET billing_type = $1, updated_at = NOW() WHERE id = $2::uuid`, req.BillingType, modelID); err != nil {
			r.logger.Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}
	if req.FixedPrice != nil {
		if err := r.db.Exec(c.Request.Context(), `UPDATE service_models SET fixed_price = $1, updated_at = NOW() WHERE id = $2::uuid`, *req.FixedPrice, modelID); err != nil {
			r.logger.Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}
	if req.TieredPricing != nil {
		if err := r.db.Exec(c.Request.Context(), `UPDATE service_models SET tiered_pricing = $1::jsonb, updated_at = NOW() WHERE id = $2::uuid`, []byte(req.TieredPricing), modelID); err != nil {
			r.logger.Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}
	if req.ShowInStore != nil {
		if err := r.db.Exec(c.Request.Context(), `UPDATE service_models SET show_in_store = $1, updated_at = NOW() WHERE id = $2::uuid`, *req.ShowInStore, modelID); err != nil {
			r.logger.Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}
	if req.Vendor != nil {
		if len(*req.Vendor) > 100 {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Vendor name must be 100 characters or fewer"}})
			return
		}
		if err := r.db.Exec(c.Request.Context(), `UPDATE service_models SET vendor = $1, updated_at = NOW() WHERE id = $2::uuid`, strings.ToLower(*req.Vendor), modelID); err != nil {
			r.logger.Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}
	if req.Status != "" {
		// Read the previous status so confirm semantics only fire on an actual
		// transition to active — repeated PATCH status=active on an already-active
		// model must not re-trigger key sync, pricing ensure, and audit noise.
		var oldStatus string
		if err := r.db.QueryRow(c.Request.Context(),
			`SELECT status FROM service_models WHERE id = $1::uuid`, modelID,
		).Scan(&oldStatus); err != nil {
			r.logger.Warn("failed to read previous model status", zap.String("model_id", modelID), zap.Error(err))
		}
		if err := r.db.Exec(c.Request.Context(), `UPDATE service_models SET status = $1, updated_at = NOW() WHERE id = $2::uuid`, req.Status, modelID); err != nil {
			r.logger.Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
		// The AWS models page confirms models via PATCH status=active rather than
		// POST /:id/confirm. Give it the same confirm semantics: stamp confirmed_at,
		// sync keys, and auto-create a pricing rule.
		if req.Status == "active" && oldStatus != "active" {
			if err := r.db.Exec(c.Request.Context(),
				`UPDATE service_models SET confirmed_at = COALESCE(confirmed_at, NOW()), updated_at = NOW() WHERE id = $1::uuid`, modelID); err != nil {
				r.logger.Warn("failed to stamp confirmed_at after PATCH-confirm", zap.String("model_id", modelID), zap.Error(err))
			}
			var provider, productID, apiModelName string
			if err := r.db.QueryRow(c.Request.Context(),
				`SELECT provider, product_id, COALESCE(api_model_name, '') FROM service_models WHERE id = $1::uuid`, modelID,
			).Scan(&provider, &productID, &apiModelName); err != nil {
				r.logger.Warn("failed to load model for PATCH-confirm sync", zap.String("model_id", modelID), zap.Error(err))
			}
			if apiModelName != "" && provider != "" {
				r.ensurePricingRule(c.Request.Context(), provider, productID, apiModelName)
			}
			if provider != "" {
				go func(p string) {
					if n, err := syncServiceModelsToKeys(r.db, context.Background(), p); err != nil {
						r.logger.Warn("Auto-sync after PATCH-confirm failed", zap.String("provider", p), zap.Error(err))
					} else {
						r.logger.Info("Auto-sync after PATCH-confirm", zap.String("provider", p), zap.Int64("keys_updated", n))
					}
					r.db.PublishSyncEvent(context.Background())
				}(provider)
			}
			if r.audit != nil {
				r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "admin", "model_confirmed", "info", fmt.Sprintf("Model confirmed via PATCH: %s", modelID))
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *ModelRoutes) toggleHidden(c *gin.Context) {
	modelID := c.Param("id")
	var req struct {
		Hidden bool `json:"hidden"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if err := r.db.Exec(c.Request.Context(), `UPDATE service_models SET hidden = $1, updated_at = NOW() WHERE id = $2::uuid`, req.Hidden, modelID); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
		return
	}
	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "admin", "model_toggled", "info", fmt.Sprintf("Model %s status toggled", modelID))
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "hidden": req.Hidden})
}

func (r *ModelRoutes) pauseModel(c *gin.Context) {
	modelID := c.Param("id")
	var provider, apiModelName string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT provider, COALESCE(api_model_name,'') FROM service_models WHERE id = $1::uuid`, modelID,
	).Scan(&provider, &apiModelName)
	if err := r.db.Exec(c.Request.Context(), `UPDATE service_models SET status = 'paused', paused_at = NOW(), updated_at = NOW() WHERE id = $1::uuid`, modelID); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
		return
	}
	// Deactivate pricing rules and record exactly which rows were flipped, so
	// resume can restore them without resurrecting rules an admin had already
	// disabled manually.
	var pricingRuleIDs []string
	if apiModelName != "" && provider != "" {
		pricingRuleIDs = r.deactivatePricingRule(c.Request.Context(), provider, apiModelName)
	}
	// Disable smart routing entries while the model is paused
	routingIDs := r.disableModelProviderRouting(c.Request.Context(), modelID)
	var marker pauseCascadeMarker
	marker.Cascade.PricingRuleIDs = pricingRuleIDs
	marker.Cascade.RoutingIDs = routingIDs
	markerJSON, _ := json.Marshal(marker)
	if err := r.db.Exec(c.Request.Context(),
		`UPDATE service_models SET paused_reason = $2 WHERE id = $1::uuid`, modelID, string(markerJSON)); err != nil {
		r.logger.Warn("Failed to record pause cascade marker", zap.String("model_id", modelID), zap.Error(err))
	}
	// Auto-sync
	if provider != "" {
		go func(p string) {
			if n, err := syncServiceModelsToKeys(r.db, context.Background(), p); err != nil {
				r.logger.Warn("Auto-sync after pause failed", zap.String("provider", p), zap.Error(err))
			} else {
				r.logger.Info("Auto-sync after pause", zap.String("provider", p), zap.Int64("keys_updated", n))
			}
			r.db.PublishSyncEvent(context.Background())
		}(provider)
	}
	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "admin", "model_paused", "warning", fmt.Sprintf("Model paused: %s", modelID))
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *ModelRoutes) resumeModel(c *gin.Context) {
	modelID := c.Param("id")
	var provider, apiModelName, pausedReason string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT provider, COALESCE(api_model_name,''), COALESCE(paused_reason,'') FROM service_models WHERE id = $1::uuid`, modelID,
	).Scan(&provider, &apiModelName, &pausedReason)
	if err := r.db.Exec(c.Request.Context(), `UPDATE service_models SET status = 'active', paused_at = NULL, paused_reason = NULL, updated_at = NOW() WHERE id = $1::uuid`, modelID); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
		return
	}
	// Restore exactly the pricing rules / routing rows this pause disabled
	// (recorded in the pause marker). Models paused before the marker existed
	// fall back to the legacy name-based restore; admin-manually-disabled
	// rules for such legacy pauses cannot be distinguished, so log a hint.
	var marker pauseCascadeMarker
	if err := json.Unmarshal([]byte(pausedReason), &marker); err == nil && (len(marker.Cascade.PricingRuleIDs) > 0 || len(marker.Cascade.RoutingIDs) > 0) {
		r.reactivatePricingRule(c.Request.Context(), marker.Cascade.PricingRuleIDs)
		r.enableModelProviderRouting(c.Request.Context(), marker.Cascade.RoutingIDs)
	} else {
		r.logger.Warn("Resume without pause cascade marker: restoring by name (legacy pause); verify pricing rules manually",
			zap.String("model_id", modelID))
		if apiModelName != "" && provider != "" {
			r.reactivatePricingRuleLegacy(c.Request.Context(), provider, apiModelName)
		}
		r.enableModelProviderRoutingLegacy(c.Request.Context(), modelID)
	}
	// Auto-sync
	if provider != "" {
		go func(p string) {
			if n, err := syncServiceModelsToKeys(r.db, context.Background(), p); err != nil {
				r.logger.Warn("Auto-sync after resume failed", zap.String("provider", p), zap.Error(err))
			} else {
				r.logger.Info("Auto-sync after resume", zap.String("provider", p), zap.Int64("keys_updated", n))
			}
			r.db.PublishSyncEvent(context.Background())
		}(provider)
	}
	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "admin", "model_resumed", "info", fmt.Sprintf("Model resumed: %s", modelID))
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *ModelRoutes) deleteModel(c *gin.Context) {
	modelID := c.Param("id")
	var provider, apiModelName string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT provider, COALESCE(api_model_name,'') FROM service_models WHERE id = $1::uuid`, modelID,
	).Scan(&provider, &apiModelName)
	if err := r.db.Exec(c.Request.Context(), `UPDATE service_models SET deleted_at = NOW(), updated_at = NOW() WHERE id = $1::uuid AND deleted_at IS NULL`, modelID); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
		return
	}
	// Deactivate auto-created pricing rule
	if apiModelName != "" && provider != "" {
		r.deactivatePricingRule(c.Request.Context(), provider, apiModelName)
	}
	// Auto-sync
	if provider != "" {
		go func(p string) {
			if n, err := syncServiceModelsToKeys(r.db, context.Background(), p); err != nil {
				r.logger.Warn("Auto-sync after delete failed", zap.String("provider", p), zap.Error(err))
			} else {
				r.logger.Info("Auto-sync after delete", zap.String("provider", p), zap.Int64("keys_updated", n))
			}
			r.db.PublishSyncEvent(context.Background())
		}(provider)
	}
	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "admin", "model_deleted", "warning", fmt.Sprintf("Model deleted: %s", modelID))
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *ModelRoutes) confirmModel(c *gin.Context) {
	modelID := c.Param("id")
	var req struct {
		APIModelName        string   `json:"api_model_name"`
		InputPriceCacheHit  *float64 `json:"input_price_cache_hit"`
		InputPriceCacheMiss *float64 `json:"input_price_cache_miss"`
		OutputPrice         *float64 `json:"output_price"`
		ReasoningPrice      *float64 `json:"reasoning_price"`
	}
	_ = c.ShouldBindJSON(&req)

	// Get provider + product_id before updating
	var provider, productID string
	if err := r.db.QueryRow(c.Request.Context(),
		`SELECT provider, product_id FROM service_models WHERE id = $1::uuid`, modelID,
	).Scan(&provider, &productID); err != nil {
		r.logger.Warn("failed to load model for confirm", zap.String("model_id", modelID), zap.Error(err))
	}

	// Auto-generate api_model_name from product_id if not provided
	apiModelName := req.APIModelName
	if apiModelName == "" && productID != "" {
		apiModelName = generateBedrockAPIModelName(productID)
	}

	if err := r.db.Exec(c.Request.Context(), `UPDATE service_models SET
			 status = 'active',
			 api_model_name = $1,
			 input_price_cache_hit = COALESCE($2, input_price_cache_hit),
			 input_price_cache_miss = COALESCE($3, input_price_cache_miss),
			 output_price = COALESCE($4, output_price),
			 reasoning_price = COALESCE($5, reasoning_price),
			 confirmed_at = NOW(),
			 updated_at = NOW()
			 WHERE id = $6::uuid`, apiModelName, req.InputPriceCacheHit, req.InputPriceCacheMiss, req.OutputPrice, req.ReasoningPrice, modelID); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
		return
	}

	// Auto-sync active service_models to keys for this provider
	if provider != "" {
		go func(p string) {
			if n, err := syncServiceModelsToKeys(r.db, context.Background(), p); err != nil {
				r.logger.Warn("Auto-sync after confirm failed", zap.String("provider", p), zap.Error(err))
			} else {
				r.logger.Info("Auto-sync after confirm", zap.String("provider", p), zap.Int64("keys_updated", n))
			}
			r.db.PublishSyncEvent(context.Background())
		}(provider)
	}

	// Auto-create pricing rule if none exists for this model.
	// Uses reference pricing based on model family; admin can adjust later in /admin/pricing.
	if apiModelName != "" && provider != "" {
		r.ensurePricingRule(c.Request.Context(), provider, productID, apiModelName)
	}

	// Auto-generate model_provider_routing rules for this model.
	// All providers with this model confirmed get a routing rule sorted by
	// upstream key tier (premium/enterprise > normal > economy) then confirmed_at.
	// Admin can reorder via drag-and-drop in the Model Routing Config UI.
	if apiModelName != "" {
		r.autoGenerateRouting(c.Request.Context(), apiModelName)
	}

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "admin", "model_confirmed", "info", fmt.Sprintf("Model confirmed: %s", modelID))
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// autoGenerateRouting generates model_provider_routing rules for a model.
// Each provider that has this model confirmed gets a rule. Priority is assigned
// by upstream key tier (premium/enterprise → 1, normal/default → 2, economy → 3),
// then by confirmed_at within the same tier. Having at least two providers is
// required to create rules; a single-provider model needs no routing.
func (r *ModelRoutes) autoGenerateRouting(ctx context.Context, apiModelName string) {
	if apiModelName == "" {
		return
	}

	type candidate struct {
		provider    string
		confirmedAt time.Time
		tierRank    int // 1=high, 2=normal, 3=economy
	}

	rows, err := r.db.Query(ctx, `
		SELECT sm.provider, sm.confirmed_at,
		       COALESCE((
		         SELECT uk.tier FROM upstream_keys uk
		         WHERE uk.provider = sm.provider
		           AND uk.deleted_at IS NULL
		           AND uk.enabled = true
		         ORDER BY CASE uk.tier
		           WHEN 'premium'   THEN 1
		           WHEN 'enterprise' THEN 1
		           WHEN 'economy'    THEN 3
		           ELSE 2
		         END
		         LIMIT 1
		       ), 'default') AS best_tier
		FROM service_models sm
		WHERE sm.api_model_name = $1 AND sm.status = 'active'
	`, apiModelName)
	if err != nil {
		r.logger.Warn("autoGenerateRouting: query failed",
			zap.String("model", apiModelName), zap.Error(err))
		return
	}
	defer rows.Close()

	var candidates []candidate
	for rows.Next() {
		var prov string
		var ct time.Time
		var tier string
		if err := rows.Scan(&prov, &ct, &tier); err != nil {
			continue
		}
		rank := 2
		switch tier {
		case "premium", "enterprise":
			rank = 1
		case "economy":
			rank = 3
		}
		candidates = append(candidates, candidate{prov, ct, rank})
	}
	if len(candidates) < 2 {
		return
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].tierRank != candidates[j].tierRank {
			return candidates[i].tierRank < candidates[j].tierRank
		}
		return candidates[i].confirmedAt.Before(candidates[j].confirmedAt)
	})

	// Map tier rank to display name
	tierName := func(rank int) string {
		switch rank {
		case 1:
			return "high"
		case 3:
			return "economy"
		default:
			return "normal"
		}
	}

	// Assign priority within each tier, reset per tier
	type tierGroup struct {
		items []candidate
	}
	groups := make(map[int]*tierGroup)
	var tierOrder []int
	for _, c := range candidates {
		if groups[c.tierRank] == nil {
			groups[c.tierRank] = &tierGroup{}
			tierOrder = append(tierOrder, c.tierRank)
		}
		groups[c.tierRank].items = append(groups[c.tierRank].items, c)
	}

	_ = r.db.Exec(ctx, `DELETE FROM model_provider_routing WHERE model_name = $1`, apiModelName)
	for _, rank := range tierOrder {
		group := groups[rank]
		tier := tierName(rank)
		for i, c := range group.items {
			_ = r.db.Exec(ctx,
				`INSERT INTO model_provider_routing (model_name, provider, tier, priority)
				 VALUES ($1, $2, $3, $4)
				 ON CONFLICT (model_name, provider)
				 DO UPDATE SET tier = $3, priority = $4, updated_at = NOW()`,
				apiModelName, c.provider, tier, i+1)
		}
	}
}

func (r *ModelRoutes) ignoreModel(c *gin.Context) {
	modelID := c.Param("id")
	var provider string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT provider FROM service_models WHERE id = $1::uuid`, modelID,
	).Scan(&provider)
	if err := r.db.Exec(c.Request.Context(), `UPDATE service_models SET status = 'ignored', updated_at = NOW() WHERE id = $1::uuid`, modelID); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
		return
	}
	// Trigger cleanup: remove this model from all groups — but ONLY if no other
	// active provider still serves the same model name. Otherwise ignoring one
	// provider's row (e.g. a stale huawei/TR deepseek-v4-flash) would strip the
	// name from every group even though deepseek/laozhang still serve it active,
	// silently dropping it from users' groups. Mirrors the discontinue path.
	if r.modelAvailableElsewhere(c.Request.Context(), modelID) {
		r.logger.Info("Skipping group cleanup: model still served by another provider",
			zap.String("model_id", modelID))
	} else {
		r.cleanupSingleModelFromGroups(c.Request.Context(), modelID, true)
	}
	// Disable smart routing entries for this model so the proxy does not try to
	// route requests to an ignored provider/model.
	r.disableModelProviderRouting(c.Request.Context(), modelID)
	// Auto-sync
	if provider != "" {
		go func(p string) {
			if n, err := syncServiceModelsToKeys(r.db, context.Background(), p); err != nil {
				r.logger.Warn("Auto-sync after ignore failed", zap.String("provider", p), zap.Error(err))
			} else {
				r.logger.Info("Auto-sync after ignore", zap.String("provider", p), zap.Int64("keys_updated", n))
			}
			r.db.PublishSyncEvent(context.Background())
		}(provider)
	}
	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "admin", "model_ignored", "warning", fmt.Sprintf("Model ignored: %s", modelID))
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *ModelRoutes) acceptChanges(c *gin.Context) {
	modelID := c.Param("id")
	// Copy aimodels prices to input/output price
	if err := r.db.Exec(c.Request.Context(), `UPDATE service_models
			 SET input_price = (aimodels_data->>'input_price')::decimal,
			     output_price = (aimodels_data->>'output_price')::decimal,
			     updated_at = NOW()
			 WHERE id = $1::uuid`, modelID); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
		return
	}
	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "admin", "model_changes_accepted", "info", fmt.Sprintf("Model changes accepted: %s", modelID))
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// getServicemodel gets a single service model by ID
func (r *ModelRoutes) getServiceModel(c *gin.Context) {
	modelID := c.Param("id")

	var id, provider, productID, status string
	var apiModelName *string
	var vendor *string
	var inputPrice, inputPriceCacheHit, inputPriceCacheMiss, outputPrice, reasoningPrice, fixedPrice *float64
	var billingType string
	var tieredPricing interface{}
	var showInStore bool
	var createdAt, updatedAt interface{}
	var eolAnnouncedAt, eolServiceAt, eolPurgeAt *time.Time
	var eolNotes *string
	var contextWindow, maxOutputTokens int
	var capabilitiesRaw []byte

	err := r.db.QueryRow(c.Request.Context(),
		`SELECT id::text, provider, product_id, api_model_name, vendor, status,
		 input_price, input_price_cache_hit, input_price_cache_miss, output_price, reasoning_price,
		 billing_type, fixed_price, tiered_pricing,
		 show_in_store, created_at, updated_at,
		 eol_announced_at, eol_service_at, eol_purge_at, eol_notes,
		 COALESCE(context_window, 0), COALESCE(max_output_tokens, 0), COALESCE(capabilities, '{}')
		 FROM service_models WHERE id = $1::uuid`, modelID,
	).Scan(&id, &provider, &productID, &apiModelName, &vendor, &status,
		&inputPrice, &inputPriceCacheHit, &inputPriceCacheMiss, &outputPrice, &reasoningPrice,
		&billingType, &fixedPrice, &tieredPricing,
		&showInStore, &createdAt, &updatedAt,
		&eolAnnouncedAt, &eolServiceAt, &eolPurgeAt, &eolNotes,
		&contextWindow, &maxOutputTokens, &capabilitiesRaw)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Model not found"}})
		return
	}

	capabilities := make(map[string]interface{})
	if len(capabilitiesRaw) > 0 && string(capabilitiesRaw) != "{}" && string(capabilitiesRaw) != "null" {
		_ = json.Unmarshal(capabilitiesRaw, &capabilities)
	}

	c.JSON(http.StatusOK, gin.H{
		"model": gin.H{
			"id": id, "provider": provider, "product_id": productID,
			"api_model_name": apiModelName, "vendor": vendor, "status": status,
			"input_price":            inputPrice,
			"input_price_cache_hit":  inputPriceCacheHit,
			"input_price_cache_miss": inputPriceCacheMiss,
			"output_price":           outputPrice,
			"reasoning_price":        reasoningPrice,
			"billing_type":           billingType,
			"fixed_price":            fixedPrice,
			"tiered_pricing":         tieredPricing,
			"show_in_store":          showInStore,
			"created_at":             createdAt, "updated_at": updatedAt,
			"eol_announced_at":  eolAnnouncedAt,
			"eol_service_at":    eolServiceAt,
			"eol_purge_at":      eolPurgeAt,
			"eol_notes":         eolNotes,
			"context_window":    contextWindow,
			"max_output_tokens": maxOutputTokens,
			"capabilities":      capabilities,
		},
	})
}

// --- Model Approvals ---

func (r *ModelRoutes) getPendingApprovals(c *gin.Context) {
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT a.id::text, a.model_id::text, a.status, a.submitted_by::text, a.submitted_at,
		        a.reviewed_by::text, a.reviewed_at, a.notes,
		        sm.provider, sm.product_id, sm.api_model_name, sm.status as model_status
		 FROM service_model_approvals a
		 JOIN service_models sm ON a.model_id = sm.id
		 WHERE a.status = 'pending'
		 ORDER BY a.submitted_at DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list approvals"}})
		return
	}
	defer rows.Close()

	approvals := []gin.H{}
	for rows.Next() {
		var id, modelID, status, submittedBy string
		var submittedAt interface{}
		var reviewedBy, reviewedAt, notes *string
		var provider, productID string
		var apiModelName, modelStatus *string
		_ = rows.Scan(&id, &modelID, &status, &submittedBy, &submittedAt,
			&reviewedBy, &reviewedAt, &notes,
			&provider, &productID, &apiModelName, &modelStatus)
		approvals = append(approvals, gin.H{
			"id": id, "model_id": modelID, "status": status,
			"submitted_by": submittedBy, "submitted_at": submittedAt,
			"reviewed_by": reviewedBy, "reviewed_at": reviewedAt,
			"notes":    notes,
			"provider": provider, "product_id": productID,
			"api_model_name": apiModelName, "model_status": modelStatus,
		})
	}
	c.JSON(http.StatusOK, gin.H{"approvals": approvals})
}

func (r *ModelRoutes) reviewApproval(c *gin.Context) {
	approvalID := c.Param("id")
	var req struct {
		Action string `json:"action" binding:"required"` // "approve" or "reject"
		Notes  string `json:"notes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Action required"}})
		return
	}
	if req.Action != "approve" && req.Action != "reject" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Action must be 'approve' or 'reject'"}})
		return
	}

	reviewerID := c.GetString("user_id")
	newStatus := "approved"
	if req.Action == "reject" {
		newStatus = "rejected"
	}

	_, err := r.db.Pool.Exec(c.Request.Context(),
		`UPDATE service_model_approvals
		 SET status = $1, reviewed_by = $2::uuid, reviewed_at = NOW(), notes = $3, updated_at = NOW()
		 WHERE id = $4::uuid`,
		newStatus, reviewerID, req.Notes, approvalID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "An internal error occurred"}})
		return
	}

	if req.Action == "approve" && r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "admin", "model_approved", "info", fmt.Sprintf("Model approved: %s", approvalID))
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "status": newStatus})
}

// --- Model Aliases ---

func (r *ModelRoutes) getAliases(c *gin.Context) {
	modelID := c.Param("id")

	var aliases interface{}
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT provider_aliases FROM service_models WHERE id = $1::uuid`, modelID,
	).Scan(&aliases)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Model not found"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"aliases": aliases})
}

func (r *ModelRoutes) addAlias(c *gin.Context) {
	modelID := c.Param("id")
	var req struct {
		Alias string `json:"alias" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Alias == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Alias required"}})
		return
	}

	// Get current aliases
	var aliases interface{}
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT provider_aliases FROM service_models WHERE id = $1::uuid`, modelID,
	).Scan(&aliases)

	// Append alias to JSONB array
	_, err := r.db.Pool.Exec(c.Request.Context(),
		`UPDATE service_models
		 SET provider_aliases = COALESCE(provider_aliases, '[]'::jsonb) || to_jsonb($1::text),
		     updated_at = NOW()
		 WHERE id = $2::uuid`,
		req.Alias, modelID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to add alias"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "alias": req.Alias})
}

func (r *ModelRoutes) removeAlias(c *gin.Context) {
	modelID := c.Param("id")
	alias := c.Param("alias")

	_, err := r.db.Pool.Exec(c.Request.Context(),
		`UPDATE service_models
		 SET provider_aliases = (
		   SELECT COALESCE(jsonb_agg(elem), '[]'::jsonb)
		   FROM jsonb_array_elements_text(COALESCE(provider_aliases, '[]'::jsonb)) as elem
		   WHERE elem != $1
		 ),
		 updated_at = NOW()
		 WHERE id = $2::uuid`,
		alias, modelID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to remove alias"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// --- Model Provider Routing ---

func (r *ModelRoutes) listModelRouting(c *gin.Context) {
	modelName := c.Query("model")

	query := `SELECT id::text, model_name, provider, tier, priority, enabled, created_at, updated_at
	          FROM model_provider_routing WHERE 1=1`
	args := []interface{}{}
	idx := 1

	if modelName != "" {
		query += " AND model_name = $" + strconv.Itoa(idx)
		args = append(args, modelName)
	}
	query += ` ORDER BY model_name,
	  CASE tier WHEN 'high' THEN 1 WHEN 'normal' THEN 2 WHEN 'economy' THEN 3 ELSE 4 END,
	  priority, provider`

	rows, err := r.db.Query(c.Request.Context(), query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list routing rules"}})
		return
	}
	defer rows.Close()

	rules := []gin.H{}
	for rows.Next() {
		var id, modelName, provider string
		var tier *string
		var priority int
		var enabled bool
		var createdAt, updatedAt interface{}
		if err := rows.Scan(&id, &modelName, &provider, &tier, &priority, &enabled, &createdAt, &updatedAt); err != nil {
			r.logger.Warn("listModelRouting: scan failed", zap.Error(err))
			continue
		}
		rules = append(rules, gin.H{
			"id": id, "model_name": modelName, "provider": provider, "tier": tier,
			"priority": priority, "enabled": enabled,
			"created_at": createdAt, "updated_at": updatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"rules": rules})
}

func (r *ModelRoutes) createModelRouting(c *gin.Context) {
	var req struct {
		ModelName string  `json:"model_name" binding:"required"`
		Provider  string  `json:"provider" binding:"required"`
		Tier      *string `json:"tier"`
		Priority  int     `json:"priority"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "model_name and provider required"}})
		return
	}

	var id string
	tier := ""
	if req.Tier != nil {
		tier = *req.Tier
	}

	var exists bool
	if err := r.db.QueryRow(c.Request.Context(),
		`SELECT EXISTS(SELECT 1 FROM service_models WHERE provider = $1 AND (api_model_name = $2 OR product_id = $2) AND status = 'active')`,
		req.Provider, req.ModelName,
	).Scan(&exists); err != nil || !exists {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Provider does not serve this model"}})
		return
	}

	err := r.db.QueryRow(c.Request.Context(),
		`INSERT INTO model_provider_routing (model_name, provider, tier, priority)
		 VALUES ($1, $2, $3, $4) RETURNING id::text`,
		req.ModelName, req.Provider, tier, req.Priority,
	).Scan(&id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "An internal error occurred"}})
		return
	}

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "admin", "model_routing_created", "info",
			fmt.Sprintf("Model routing created: %s -> %s", req.ModelName, req.Provider))
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "rule": gin.H{"id": id}})
}

func (r *ModelRoutes) updateModelRouting(c *gin.Context) {
	ruleID := c.Param("id")
	var req struct {
		Priority *int    `json:"priority"`
		Enabled  *bool   `json:"enabled"`
		Tier     *string `json:"tier"`
	}
	_ = c.ShouldBindJSON(&req)

	fields := []string{}
	args := []interface{}{}
	idx := 1

	if req.Priority != nil {
		fields = append(fields, "priority = $"+strconv.Itoa(idx))
		args = append(args, *req.Priority)
		idx++
	}
	if req.Enabled != nil {
		fields = append(fields, "enabled = $"+strconv.Itoa(idx))
		args = append(args, *req.Enabled)
		idx++
	}
	if req.Tier != nil {
		val := *req.Tier
		fields = append(fields, "tier = $"+strconv.Itoa(idx))
		args = append(args, val)
		idx++
	}

	if len(fields) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": true})
		return
	}

	fields = append(fields, "updated_at = NOW()")
	args = append(args, ruleID)
	q := `UPDATE model_provider_routing SET ` + strings.Join(fields, ", ") + ` WHERE id = $` + strconv.Itoa(idx) + `::uuid`

	if err := r.db.Exec(c.Request.Context(), q, args...); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
		return
	}

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "admin", "model_routing_updated", "info",
			fmt.Sprintf("Model routing updated: %s", ruleID))
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *ModelRoutes) deleteModelRouting(c *gin.Context) {
	ruleID := c.Param("id")
	if err := r.db.Exec(c.Request.Context(), `DELETE FROM model_provider_routing WHERE id = $1::uuid`, ruleID); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
		return
	}
	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "admin", "model_routing_deleted", "warning",
			fmt.Sprintf("Model routing deleted: %s", ruleID))
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *ModelRoutes) reorderModelRouting(c *gin.Context) {
	var req struct {
		ModelName string `json:"model_name" binding:"required"`
		Items     []struct {
			ID       string `json:"id"`
			Priority int    `json:"priority"`
		} `json:"items" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "model_name and items required"}})
		return
	}

	for _, item := range req.Items {
		if err := r.db.Exec(c.Request.Context(), `UPDATE model_provider_routing SET priority = $1, updated_at = NOW()
					 WHERE id = $2::uuid AND model_name = $3`, item.Priority, item.ID, req.ModelName); err != nil {
			r.logger.Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}
	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "admin", "model_routing_reordered", "info",
			fmt.Sprintf("Model routing reordered for %s (%d items)", req.ModelName, len(req.Items)))
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ensurePricingRule creates a default pricing rule for a model if none exists.
// It uses a built-in reference pricing table based on model family patterns.
// Admin can later adjust in /admin/pricing. Rules created here are tagged
// with note='auto-created on model confirm' for easy identification.
func (r *ModelRoutes) ensurePricingRule(ctx context.Context, provider, productID, apiModelName string) {
	// Check if an active rule already exists for this model
	var exists bool
	if err := r.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM model_pricing_rules
		 WHERE status = 'active'
		   AND (api_model_name = $1 OR product_id = $2)
		   AND (provider = $3 OR provider = '*'))`,
		apiModelName, productID, provider,
	).Scan(&exists); err != nil {
		r.logger.Warn("failed to check existing pricing rule", zap.String("model", apiModelName), zap.Error(err))
		return
	}
	if exists {
		return
	}

	// Resolve reference pricing from model name patterns
	prices := resolveReferencePricing(apiModelName, productID)

	// Create the rule
	if err := r.db.Exec(ctx,
		`INSERT INTO model_pricing_rules
		 (provider, region, api_model_name, product_id, billing_mode,
		  prices, priority, status, rule_name, note)
		 VALUES ($1, '*', $2, $3, 'flat', $4, 0, 'active', $5, 'auto-created on model confirm')`,
		provider, apiModelName, productID,
		prices,
		fmt.Sprintf("Auto: %s", apiModelName),
	); err != nil {
		r.logger.Warn("failed to auto-create pricing rule", zap.String("model", apiModelName), zap.String("provider", provider), zap.Error(err))
		return
	}
	r.logger.Info("Auto-created pricing rule for confirmed model",
		zap.String("model", apiModelName),
		zap.String("provider", provider),
	)
}

// resolveReferencePricing returns a JSON pricing object based on model name patterns.
// Prices are in $/1M tokens (industry standard unit).
// These are reasonable defaults — admin should adjust for actual cost + margin.
func resolveReferencePricing(apiModelName, productID string) string {
	name := strings.ToLower(apiModelName + " " + productID)

	type ref struct {
		pattern string
		input   float64
		output  float64
		cache   float64
	}

	// Reference pricing table ($/1M tokens) — conservative defaults with margin
	refs := []ref{
		// Claude family
		{"opus", 15.0, 75.0, 1.5},
		{"sonnet-4", 3.0, 15.0, 0.3},
		{"sonnet-5", 3.0, 15.0, 0.3},
		{"haiku", 0.8, 4.0, 0.08},
		{"fable", 5.0, 25.0, 0.5},
		// GPT family
		{"gpt-4o", 2.5, 10.0, 0.25},
		{"gpt-4.1-nano", 0.1, 0.4, 0.01},
		{"gpt-4.1-mini", 0.4, 1.6, 0.04},
		{"gpt-4.1", 2.0, 8.0, 0.2},
		{"o4-mini", 1.1, 4.4, 0.11},
		{"o3-mini", 1.1, 4.4, 0.11},
		// Amazon Nova
		{"nova-premier", 2.5, 10.0, 0.25},
		{"nova-pro", 0.8, 3.2, 0.08},
		{"nova-lite", 0.06, 0.24, 0.006},
		{"nova-micro", 0.035, 0.14, 0.004},
		// Meta Llama
		{"llama4-maverick", 0.2, 0.6, 0.02},
		{"llama4-scout", 0.15, 0.4, 0.015},
		{"llama3-3-70b", 0.6, 1.8, 0.06},
		{"llama3-1-70b", 0.6, 1.8, 0.06},
		{"llama3-1-8b", 0.15, 0.4, 0.015},
		// Mistral
		{"mistral-large", 2.0, 6.0, 0.2},
		{"devstral", 1.0, 3.0, 0.1},
		{"magistral", 0.5, 1.5, 0.05},
		{"ministral", 0.1, 0.3, 0.01},
		{"mixtral", 0.3, 0.9, 0.03},
		// DeepSeek
		{"deepseek-v4-pro", 2.0, 8.0, 0.2},
		{"deepseek-v4-flash", 0.5, 2.0, 0.05},
		{"deepseek-v3", 0.5, 2.0, 0.05},
		{"deepseek-r1", 1.0, 4.0, 0.1},
		// MiniMax
		{"minimax-m3", 0.3, 1.2, 0.03},
		{"minimax-m2", 0.2, 0.8, 0.02},
		// Zhipu
		{"glm-5", 0.5, 2.0, 0.05},
		{"glm-4", 0.3, 1.0, 0.03},
	}

	for _, r := range refs {
		if strings.Contains(name, r.pattern) {
			return fmt.Sprintf(
				`{"input": %.3f, "output": %.3f, "cache_hit": %.4f, "cache_write": %.3f, "reasoning": %.3f, "fixed_request": 0}`,
				r.input, r.output, r.cache, r.input, r.input*1.5,
			)
		}
	}

	// Fallback: generic pricing for unknown models
	return `{"input": 1.0, "output": 3.0, "cache_hit": 0.1, "cache_write": 1.0, "reasoning": 1.0, "fixed_request": 0}`
}

// pauseCascadeMarker is stored in service_models.paused_reason when a model is
// paused. It records exactly which pricing rules and provider-routing rows the
// pause cascade disabled, so resume can restore precisely those rows instead
// of blanket-reactivating every inactive rule (which would resurrect rules an
// admin had manually disabled before the pause).
type pauseCascadeMarker struct {
	Cascade struct {
		PricingRuleIDs []string `json:"pricing_rules"`
		RoutingIDs     []string `json:"routing"`
	} `json:"cascade"`
}

// deactivatePricingRule marks pricing rules as inactive when a model is paused or
// deleted. It applies to all rules for the matching provider+api_model_name so
// that admin/models status changes stay in sync with /admin/pricing, regardless
// of whether the rule was auto-created, auto-imported, or manually edited.
// Scoped by provider+api_model_name to avoid cross-provider interference
// (e.g. pausing a model on zimaocloud must not deactivate amazon-us-east-1's rule
// for the same api_model_name).
//
// It returns the IDs of the rules it flipped so callers (pause) can record them
// in the pause marker; the delete path ignores the return value.
func (r *ModelRoutes) deactivatePricingRule(ctx context.Context, provider, apiModelName string) []string {
	rows, err := r.db.Pool.Query(ctx,
		`UPDATE model_pricing_rules SET status = 'inactive', updated_at = NOW()
		 WHERE provider = $1 AND api_model_name = $2 AND status = 'active'
		 RETURNING id::text`,
		provider, apiModelName,
	)
	if err != nil {
		r.logger.Warn("Failed to deactivate pricing rule", zap.String("provider", provider), zap.String("api_model_name", apiModelName), zap.Error(err))
		return nil
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// reactivatePricingRule restores the pricing rules recorded in the pause marker.
// Rules an admin disabled manually stay inactive because only pause-flipped IDs
// are passed in.
func (r *ModelRoutes) reactivatePricingRule(ctx context.Context, pricingRuleIDs []string) {
	if len(pricingRuleIDs) == 0 {
		return
	}
	if _, err := r.db.Pool.Exec(ctx,
		`UPDATE model_pricing_rules SET status = 'active', updated_at = NOW()
		 WHERE id = ANY($1::uuid[]) AND status = 'inactive'`,
		pricingRuleIDs,
	); err != nil {
		r.logger.Warn("Failed to reactivate pricing rules", zap.Strings("rule_ids", pricingRuleIDs), zap.Error(err))
	}
}

// reactivatePricingRuleLegacy is the pre-marker fallback for models paused
// before the cascade marker existed: restore by provider+api_model_name.
func (r *ModelRoutes) reactivatePricingRuleLegacy(ctx context.Context, provider, apiModelName string) {
	_ = r.db.Exec(ctx,
		`UPDATE model_pricing_rules SET status = 'active', updated_at = NOW()
		 WHERE provider = $1 AND api_model_name = $2 AND status = 'inactive'`,
		provider, apiModelName,
	)
}

package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mayiants/ai-portal-go/internal/aws"
	"github.com/mayiants/ai-portal-go/pkg/crypto"
	"github.com/mayiants/ai-portal-go/pkg/validation"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// openRouterProviderMapping maps OpenRouter provider prefixes to our standard
// provider names stored in openrouter_models.provider.
var openRouterProviderMapping = map[string]string{
	"openai": "openai", "anthropic": "anthropic", "google": "google",
	"meta-llama": "meta", "mistralai": "mistral", "cohere": "cohere",
	"x-ai": "x-ai", "deepseek": "deepseek", "perplexity": "perplexity",
	"amazon": "amazon", "microsoft": "microsoft", "qwen": "alibaba",
	"alibaba": "alibaba", "baidu": "baidu", "bytedance": "bytedance",
	"moonshot": "moonshot", "moonshotai": "moonshot", "minimax": "minimax",
	"zhipu": "zhipu", "z-ai": "zhipu", "tencent": "tencent",
}

// resolveOpenRouterProvider translates a service_models provider (or a provider
// prefix embedded in a product_id) to the standard provider name used in
// openrouter_models. Returns empty string if it cannot be resolved.
func resolveOpenRouterProvider(provider, productID string) string {
	// Already a standard name.
	for _, std := range openRouterProviderMapping {
		if std == provider {
			return provider
		}
	}
	// Known OpenRouter prefix.
	if std, ok := openRouterProviderMapping[provider]; ok {
		return std
	}
	// Provider buried in product_id (e.g. tokenrouter models).
	if idx := strings.Index(productID, "/"); idx > 0 {
		prefix := productID[:idx]
		if std, ok := openRouterProviderMapping[prefix]; ok {
			return std
		}
		for _, std := range openRouterProviderMapping {
			if std == prefix {
				return prefix
			}
		}
	}
	return ""
}

func (r *AdminRoutes) listServiceModels(c *gin.Context) {
	status := c.Query("status")
	query := `SELECT id, provider, product_id, api_model_name, vendor, status, validation_status, validated_at,
		        input_price, output_price,
		        input_price_cache_hit, input_price_cache_miss, reasoning_price,
		        billing_type, fixed_price, tiered_pricing,
		        aimodels_data, show_in_store, COALESCE(is_hot, false) as is_hot, created_at,
		        COALESCE(context_window, 0) as context_window,
		        COALESCE(max_output_tokens, 0) as max_output_tokens,
		        COALESCE(capabilities, '{}') as capabilities,
		        COALESCE(hidden, false) as hidden,
		        COALESCE(NULLIF(aimodels_data->>'type', ''), capabilities->>'type', 'text') as model_type
		 FROM service_models WHERE 1=1`
	args := []interface{}{}
	idx := 1
	if status != "" && status != "all" {
		query += " AND status = $" + strconv.Itoa(idx)
		args = append(args, status)
	}
	query += " ORDER BY COALESCE(vendor, provider), api_model_name"
	rows, err := r.db.Query(c.Request.Context(), query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list models"}})
		return
	}
	defer rows.Close()

	models := []gin.H{}
	for rows.Next() {
		var id, provider, productID, status, validationStatus, modelType string
		var apiModelName *string
		var vendor *string
		var validatedAt *time.Time
		var inputPrice, outputPrice, cacheHitPrice, cacheMissPrice, reasoningPrice, fixedPrice *float64
		var billingType string
		var tieredPricing, aimodelsData interface{}
		var contextWindow, maxOutputTokens int
		var capabilitiesRaw []byte
		var showInStore bool
		var isHot bool
		var createdAt time.Time
		var hidden bool
		_ = rows.Scan(&id, &provider, &productID, &apiModelName, &vendor, &status, &validationStatus, &validatedAt,
			&inputPrice, &outputPrice,
			&cacheHitPrice, &cacheMissPrice, &reasoningPrice,
			&billingType, &fixedPrice, &tieredPricing,
			&aimodelsData, &showInStore, &isHot, &createdAt,
			&contextWindow, &maxOutputTokens, &capabilitiesRaw,
			&hidden, &modelType)
		capabilities := make(map[string]interface{})
		if len(capabilitiesRaw) > 0 && string(capabilitiesRaw) != "{}" && string(capabilitiesRaw) != "null" {
			_ = json.Unmarshal(capabilitiesRaw, &capabilities)
		}
		models = append(models, gin.H{
			"id": id, "provider": provider, "product_id": productID,
			"api_model_name": apiModelName, "vendor": vendor, "status": status,
			"validation_status": validationStatus,
			"validated_at":      validatedAt,
			"input_price":       inputPrice, "output_price": outputPrice,
			"input_price_cache_hit":  cacheHitPrice,
			"input_price_cache_miss": cacheMissPrice,
			"reasoning_price":        reasoningPrice,
			"billing_type":           billingType,
			"fixed_price":            fixedPrice,
			"tiered_pricing":         tieredPricing,
			"aimodels_data":          aimodelsData, "show_in_store": showInStore,
			"is_hot":            isHot,
			"created_at":        createdAt,
			"context_window":    contextWindow,
			"max_output_tokens": maxOutputTokens,
			"capabilities":      capabilities,
			"hidden":            hidden,
			"model_type":        modelType,
		})
	}

	c.JSON(http.StatusOK, gin.H{"models": models})
}
func (r *AdminRoutes) updateServiceModel(c *gin.Context) {
	modelID := c.Param("id")

	var req struct {
		Status          string                 `json:"status"`
		APIModelName    string                 `json:"api_model_name"`
		InputPrice      *float64               `json:"input_price"`
		OutputPrice     *float64               `json:"output_price"`
		ShowInStore     *bool                  `json:"show_in_store"`
		IsHot           *bool                  `json:"is_hot"`
		ContextWindow   *int                   `json:"context_window"`
		MaxOutputTokens *int                   `json:"max_output_tokens"`
		Capabilities    map[string]interface{} `json:"capabilities"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	setParts := []string{}
	args := []interface{}{}
	argIdx := 1

	if req.Status != "" {
		setParts = append(setParts, fmt.Sprintf("status = $%d", argIdx))
		args = append(args, req.Status)
		argIdx++
	}
	if req.APIModelName != "" {
		setParts = append(setParts, fmt.Sprintf("api_model_name = $%d", argIdx))
		args = append(args, req.APIModelName)
		argIdx++
	}
	if req.InputPrice != nil {
		setParts = append(setParts, fmt.Sprintf("input_price = $%d", argIdx))
		args = append(args, *req.InputPrice)
		argIdx++
	}
	if req.OutputPrice != nil {
		setParts = append(setParts, fmt.Sprintf("output_price = $%d", argIdx))
		args = append(args, *req.OutputPrice)
		argIdx++
	}
	if req.ShowInStore != nil {
		setParts = append(setParts, fmt.Sprintf("show_in_store = $%d", argIdx))
		args = append(args, *req.ShowInStore)
		argIdx++
	}
	if req.IsHot != nil {
		setParts = append(setParts, fmt.Sprintf("is_hot = $%d", argIdx))
		args = append(args, *req.IsHot)
		argIdx++
	}

	// Merge explicit context_window / max_output_tokens columns into capabilities
	// so the JSONB object remains the single source of truth for downstream.
	mergedCaps := make(map[string]interface{})
	if req.Capabilities != nil {
		for k, v := range req.Capabilities {
			mergedCaps[k] = v
		}
	}
	if req.ContextWindow != nil && *req.ContextWindow > 0 {
		mergedCaps["context_window"] = *req.ContextWindow
	}
	if req.MaxOutputTokens != nil && *req.MaxOutputTokens > 0 {
		mergedCaps["max_output_tokens"] = *req.MaxOutputTokens
	}
	if len(mergedCaps) > 0 {
		capsJSON, err := json.Marshal(mergedCaps)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid capabilities"}})
			return
		}
		setParts = append(setParts, fmt.Sprintf("capabilities = $%d", argIdx))
		args = append(args, capsJSON)
		argIdx++
	}
	if req.ContextWindow != nil {
		setParts = append(setParts, fmt.Sprintf("context_window = $%d", argIdx))
		args = append(args, *req.ContextWindow)
		argIdx++
	}
	if req.MaxOutputTokens != nil {
		setParts = append(setParts, fmt.Sprintf("max_output_tokens = $%d", argIdx))
		args = append(args, *req.MaxOutputTokens)
		argIdx++
	}

	if len(setParts) > 0 {
		setParts = append(setParts, "updated_at = NOW()")
		query := fmt.Sprintf("UPDATE service_models SET %s WHERE id = $%d", strings.Join(setParts, ", "), argIdx)
		args = append(args, modelID)
		if err := r.db.Exec(c.Request.Context(), query, args...); err != nil {
			r.logger.Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}
func (r *AdminRoutes) batchValidateModels(c *gin.Context) {
	var req struct {
		Provider       string   `json:"provider"`
		ProviderPrefix string   `json:"provider_prefix"`
		Status         string   `json:"status"`
		ModelIDs       []string `json:"model_ids"`
		IDs            []string `json:"ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	// Support both "model_ids" and "ids" field names
	ids := req.IDs
	if len(ids) == 0 {
		ids = req.ModelIDs
	}

	// Build query to fetch models to validate
	query := `SELECT id::text, provider, product_id, api_model_name,
	          COALESCE(NULLIF(aimodels_data->>'type', ''), capabilities->>'type', 'text') as model_type,
	          COALESCE(capabilities->>'supports_thinking', 'false') = 'true' as supports_thinking
	          FROM service_models WHERE deleted_at IS NULL`
	args := []interface{}{}
	idx := 1

	if len(ids) > 0 {
		query += " AND id = ANY($" + strconv.Itoa(idx) + "::uuid[])"
		args = append(args, ids)
	} else {
		if req.Provider != "" {
			query += " AND provider = $" + strconv.Itoa(idx)
			args = append(args, req.Provider)
			idx++
		} else if req.ProviderPrefix != "" {
			query += " AND provider LIKE $" + strconv.Itoa(idx)
			args = append(args, req.ProviderPrefix+"%")
			idx++
		}
		if req.Status != "" {
			query += " AND status = $" + strconv.Itoa(idx)
			args = append(args, req.Status)
		}
	}
	query += " LIMIT 100"

	rows, err := r.db.Query(c.Request.Context(), query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to query models"}})
		return
	}
	defer rows.Close()

	type modelEntry struct {
		ID               string
		Provider         string
		ProductID        string
		APIModelName     string
		ModelType        string
		SupportsThinking bool
	}
	var models []modelEntry

	for rows.Next() {
		var id, provider, productID, modelType string
		var apiModelName *string
		var supportsThinking bool
		if err := rows.Scan(&id, &provider, &productID, &apiModelName, &modelType, &supportsThinking); err != nil {
			continue
		}
		apiName := productID
		if apiModelName != nil && *apiModelName != "" {
			apiName = *apiModelName
		}
		models = append(models, modelEntry{ID: id, Provider: provider, ProductID: productID, APIModelName: apiName, ModelType: modelType, SupportsThinking: supportsThinking})
	}

	if len(models) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": true, "validated": 0, "valid": 0, "invalid": 0, "results": []gin.H{}})
		return
	}

	// Convert to validator input format
	validatorModels := make([]struct {
		Provider         string
		ProductID        string
		APIModelName     string
		ModelType        string
		SupportsThinking bool
	}, len(models))
	for i, m := range models {
		validatorModels[i] = struct {
			Provider         string
			ProductID        string
			APIModelName     string
			ModelType        string
			SupportsThinking bool
		}{m.Provider, m.ProductID, m.APIModelName, m.ModelType, m.SupportsThinking}
	}

	// Run batch validation
	results, err := r.validator.BatchValidate(c.Request.Context(), validatorModels)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Validation failed: " + err.Error()}})
		return
	}

	// Update validation statuses in DB
	valid := 0
	invalid := 0
	for i, result := range results {
		if i >= len(models) {
			break
		}
		if err := r.validator.UpdateModelValidation(c.Request.Context(), models[i].ID, &result); err != nil {
			r.logger.Warn("Failed to update model validation", zap.String("model_id", models[i].ID), zap.Error(err))
		}
		if result.Available {
			valid++
		} else {
			invalid++
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"validated": len(results),
		"valid":     valid,
		"invalid":   invalid,
	})
}

// listModelsGrouped returns service models grouped by provider (for model selector)
func (r *AdminRoutes) listModelsGrouped(c *gin.Context) {
	r.listModelsGroupedInternal(c, false)
}

// listPublicModelsGrouped returns active service models grouped by provider,
// filtering out hidden models for regular users.
func (r *AdminRoutes) listPublicModelsGrouped(c *gin.Context) {
	r.listModelsGroupedInternal(c, true)
}

func (r *AdminRoutes) listModelsGroupedInternal(c *gin.Context, filterHidden bool) {
	// Optional track filter mirrors the proxy's dual-track routing:
	// bedrock = AWS Bedrock Runtime providers, mantle = AWS Mantle providers,
	// standard = everything else. Empty track returns all active models.
	where := "status = 'active'"
	if filterHidden {
		where += " AND COALESCE(hidden, false) = false"
	}
	switch c.Query("track") {
	case "bedrock":
		where += " AND " + validation.BedrockRuntimeProviderSQLCondition("provider")
	case "mantle":
		where += " AND provider LIKE 'amazon-mantle-%'"
	case "standard":
		where += " AND provider NOT LIKE 'amazon-%'"
	}
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT id::text, provider, product_id, api_model_name, status,
		        input_price, output_price, show_in_store, sort_order, validation_status,
		        COALESCE(NULLIF(aimodels_data->>'type', ''), capabilities->>'type', 'text') AS model_type,
		        capabilities->'supported_types' AS supported_types
		 FROM service_models
		 WHERE `+where+`
		 ORDER BY provider, sort_order, product_id`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list models"}})
		return
	}
	defer rows.Close()

	// Group by provider
	providers := make(map[string][]gin.H)
	providerNames := []string{}
	// Names as clients see them, for the capability override lookup. Collected
	// during the scan and resolved in one batch query afterwards.
	overrideNames := []string{}
	nameSeen := map[string]bool{}
	for rows.Next() {
		var id, provider, productID, status string
		var apiModelName *string
		var inputPrice, outputPrice *float64
		var showInStore bool
		var sortOrder int
		var validationStatus *string
		var modelType string
		var supportedTypesRaw []byte
		_ = rows.Scan(&id, &provider, &productID, &apiModelName, &status,
			&inputPrice, &outputPrice, &showInStore, &sortOrder, &validationStatus, &modelType, &supportedTypesRaw)

		name := productID
		if apiModelName != nil && *apiModelName != "" {
			name = *apiModelName
		}
		if !nameSeen[name] {
			nameSeen[name] = true
			overrideNames = append(overrideNames, name)
		}

		var supportedTypes []string
		if len(supportedTypesRaw) > 0 {
			_ = json.Unmarshal(supportedTypesRaw, &supportedTypes)
		}

		if _, ok := providers[provider]; !ok {
			providerNames = append(providerNames, provider)
		}
		entry := gin.H{
			"id": id, "product_id": productID,
			"api_model_name": apiModelName,
			"status":         status, "input_price": inputPrice,
			"output_price":      outputPrice,
			"show_in_store":     showInStore,
			"sort_order":        sortOrder,
			"validation_status": validationStatus,
			// model_type (text/image/video/audio/embedding) lets admin UIs bucket
			// long model lists. Computed in SQL as
			// COALESCE(aimodels_data.type, capabilities.type, 'text') so an
			// unclassified model defaults to 'text' instead of falling to 'other'.
			"model_type": modelType,
			// supported_types lists every capability (e.g. ["text","image"]) so
			// admin UIs can show a model under each of its type buckets, not
			// just its primary one.
			"supported_types": supportedTypes,
		}
		providers[provider] = append(providers[provider], entry)
	}

	result := []gin.H{}
	// Capability overrides (manual/probe/upstream master data) take
	// precedence over the per-provider capabilities row.
	overrides := loadCapabilityOverrides(c.Request.Context(), r.db, overrideNames)
	for _, models := range providers {
		for _, m := range models {
			apiName, _ := m["api_model_name"].(*string)
			ovName := m["product_id"].(string)
			if apiName != nil && *apiName != "" {
				ovName = *apiName
			}
			if ov, ok := overrides[ovName]; ok {
				m["supported_types"] = ov
			}
		}
	}
	for _, pname := range providerNames {
		result = append(result, gin.H{
			"name":   pname,
			"models": providers[pname],
		})
	}

	c.JSON(http.StatusOK, gin.H{"providers": result})
}
func (r *AdminRoutes) listModelsAdmin(c *gin.Context) {
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT id, provider, product_id, api_model_name, status,
		        input_price, output_price, show_in_store, sort_order
		 FROM service_models ORDER BY provider, sort_order`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list models"}})
		return
	}
	defer rows.Close()

	models := []gin.H{}
	enabledModels := []string{}
	for rows.Next() {
		var id, provider, productID, status string
		var apiModelName *string
		var inputPrice, outputPrice *float64
		var showInStore bool
		var sortOrder int
		_ = rows.Scan(&id, &provider, &productID, &apiModelName, &status, &inputPrice, &outputPrice, &showInStore, &sortOrder)
		models = append(models, gin.H{
			"id": id, "provider": provider, "product_id": productID,
			"api_model_name": apiModelName, "status": status,
			"input_price": inputPrice, "output_price": outputPrice,
			"show_in_store": showInStore, "sort_order": sortOrder,
		})
		if status == "active" {
			enabledModels = append(enabledModels, provider+":"+productID)
		}
	}
	c.JSON(http.StatusOK, gin.H{"models": models, "enabledModels": enabledModels})
}
func (r *AdminRoutes) getModelsStatus(c *gin.Context) {
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT id, provider, product_id, api_model_name, status, updated_at
		 FROM service_models WHERE status IN ('active', 'paused', 'eol') ORDER BY provider, product_id`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to get model status"}})
		return
	}
	defer rows.Close()

	models := []gin.H{}
	for rows.Next() {
		var id, provider, productID, status string
		var apiModelName *string
		var updatedAt interface{}
		_ = rows.Scan(&id, &provider, &productID, &apiModelName, &status, &updatedAt)
		models = append(models, gin.H{
			"id": id, "provider": provider, "product_id": productID,
			"api_model_name": apiModelName, "status": status, "updated_at": updatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"models": models})
}
func (r *AdminRoutes) getSyncStatus(c *gin.Context) {
	var lastSync interface{}
	var totalSynced int
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT MAX(aimodels_synced_at), COUNT(*) FROM service_models WHERE aimodels_synced_at IS NOT NULL`,
	).Scan(&lastSync, &totalSynced)

	var status = "idle"
	if lastSync != nil {
		status = "completed"
	}

	c.JSON(http.StatusOK, gin.H{
		"status":      status,
		"lastSync":    lastSync,
		"progress":    100,
		"message":     "Sync service ready",
		"totalSynced": totalSynced,
	})
}
func (r *AdminRoutes) syncOpenRouter(c *gin.Context) {
	stats, err := syncAIModels(r.db, c.Request.Context())
	if err != nil {
		r.logger.Error("Sync failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "An internal error occurred",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Sync completed!",
		"stats":   stats,
		"log":     "Synced from aiModels.json",
	})
}

// openRouterModel mirrors the full OpenRouter /api/v1/models response for a single model.
type openRouterModel struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ContextLen int    `json:"context_length"`
	Pricing    struct {
		Prompt          string `json:"prompt"`
		Completion      string `json:"completion"`
		InputCacheRead  string `json:"input_cache_read"`
		InputCacheWrite string `json:"input_cache_write"`
		OutputCacheRead string `json:"output_cache_read"`
	} `json:"pricing"`
	TopProvider struct {
		ContextLength       int  `json:"context_length"`
		MaxCompletionTokens int  `json:"max_completion_tokens"`
		IsModerated         bool `json:"is_moderated"`
	} `json:"top_provider"`
	Architecture struct {
		Modality         string   `json:"modality"`
		InputModalities  []string `json:"input_modalities"`
		OutputModalities []string `json:"output_modalities"`
		Tokenizer        string   `json:"tokenizer"`
		InstructType     *string  `json:"instruct_type"`
	} `json:"architecture"`
	SupportedParameters []string `json:"supported_parameters"`
	Reasoning           *struct {
		Mandatory      bool `json:"mandatory"`
		DefaultEnabled bool `json:"default_enabled"`
	} `json:"reasoning"`
}

// openRouterCapabilities derives our normalized capabilities from an OpenRouter model record.
func openRouterCapabilities(m openRouterModel) map[string]interface{} {
	caps := make(map[string]interface{})

	contextWindow := m.ContextLen
	if contextWindow == 0 {
		contextWindow = m.TopProvider.ContextLength
	}
	if contextWindow > 0 {
		caps["context_window"] = contextWindow
	}

	maxOutput := m.TopProvider.MaxCompletionTokens
	if maxOutput == 0 && contextWindow > 0 {
		maxOutput = contextWindow / 8
	}
	if maxOutput > 0 {
		caps["max_output_tokens"] = maxOutput
	}

	// Vision / audio from modalities.
	for _, mod := range m.Architecture.InputModalities {
		switch strings.ToLower(mod) {
		case "image":
			caps["supports_vision"] = true
		case "audio":
			caps["supports_audio"] = true
		}
	}
	for _, mod := range m.Architecture.OutputModalities {
		if strings.ToLower(mod) == "audio" {
			caps["supports_audio"] = true
		}
	}

	// Tool calls / thinking / json mode from supported_parameters.
	hasToolChoice := false
	hasTools := false
	hasReasoning := false
	for _, p := range m.SupportedParameters {
		switch p {
		case "tools":
			hasTools = true
		case "tool_choice":
			hasToolChoice = true
		case "reasoning", "include_reasoning":
			hasReasoning = true
		case "json_mode", "response_format":
			caps["supports_json_mode"] = true
		}
	}
	if hasTools || hasToolChoice {
		caps["supports_tool_calls"] = true
	}
	if hasTools && hasToolChoice {
		caps["supports_parallel_tool_calls"] = true
	}
	if hasReasoning || (m.Reasoning != nil && (m.Reasoning.Mandatory || m.Reasoning.DefaultEnabled)) {
		caps["supports_thinking"] = true
		caps["reasoning_efforts"] = []string{"low", "medium", "high"}
	}

	// Streaming is the default for text models; explicit negative signals are rare.
	if m.Architecture.Modality == "" || strings.HasPrefix(strings.ToLower(m.Architecture.Modality), "text") {
		caps["supports_streaming"] = true
	}

	return caps
}

// SyncOpenRouterModels fetches the latest model list from OpenRouter and
// upserts it into the openrouter_models candidate table. It is safe to call
// from both HTTP handlers and background jobs.
func (r *AdminRoutes) SyncOpenRouterModels(ctx context.Context) (total, synced, skipped int, err error) {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get("https://openrouter.ai/api/v1/models")
	if err != nil {
		r.logger.Error("Failed to fetch from OpenRouter", zap.Error(err))
		return 0, 0, 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		Data []openRouterModel `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		r.logger.Error("Failed to parse OpenRouter response", zap.Error(err))
		return 0, 0, 0, err
	}

	total = len(result.Data)
	for _, model := range result.Data {
		parts := strings.Split(model.ID, "/")
		if len(parts) < 2 {
			skipped++
			continue
		}

		provider, ok := openRouterProviderMapping[parts[0]]
		if !ok {
			skipped++
			continue
		}

		var inputPrice, outputPrice float64
		_, _ = fmt.Sscanf(model.Pricing.Prompt, "%f", &inputPrice)
		_, _ = fmt.Sscanf(model.Pricing.Completion, "%f", &outputPrice)

		contextLen := model.ContextLen
		if contextLen == 0 {
			contextLen = model.TopProvider.ContextLength
		}
		maxOutputTokens := model.TopProvider.MaxCompletionTokens
		if maxOutputTokens == 0 && contextLen > 0 {
			maxOutputTokens = contextLen / 8
		}

		caps := openRouterCapabilities(model)
		capsJSON, _ := json.Marshal(caps)
		rawData, _ := json.Marshal(model)

		_, err := r.db.Pool.Exec(ctx,
			`INSERT INTO openrouter_models (openrouter_id, provider, name, input_price, output_price, context_window, max_output_tokens, capabilities, raw_data, synced_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
			 ON CONFLICT (openrouter_id) DO UPDATE SET
			 provider = EXCLUDED.provider,
			 name = EXCLUDED.name,
			 input_price = EXCLUDED.input_price,
			 output_price = EXCLUDED.output_price,
			 context_window = EXCLUDED.context_window,
			 max_output_tokens = EXCLUDED.max_output_tokens,
			 capabilities = EXCLUDED.capabilities,
			 raw_data = EXCLUDED.raw_data,
			 synced_at = NOW()`,
			model.ID, provider, model.Name, inputPrice, outputPrice, contextLen, maxOutputTokens, capsJSON, rawData)
		if err != nil {
			r.logger.Warn("Failed to sync openrouter model",
				zap.String("openrouter_id", model.ID),
				zap.Error(err))
		} else {
			synced++
		}
	}

	return total, synced, skipped, nil
}

func (r *AdminRoutes) syncOpenRouterLive(c *gin.Context) {
	total, synced, skipped, err := r.SyncOpenRouterModels(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "An internal error occurred"})
		return
	}

	if synced == 0 && total > 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Sync completed but 0 models imported. Check logs for details.",
			"total":   total,
			"synced":  synced,
			"skipped": skipped,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"total":   total,
		"synced":  synced,
		"skipped": skipped,
	})
}

func (r *AdminRoutes) validateOpenRouter(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Validation queued",
	})
}
func (r *AdminRoutes) getOpenRouterModels(c *gin.Context) {
	provider := c.Query("provider")
	search := c.Query("search")

	query := `SELECT id::text, openrouter_id, provider, name, input_price, output_price,
	          context_window, max_output_tokens, capabilities, raw_data, synced_at
	          FROM openrouter_models WHERE 1=1`
	args := []interface{}{}
	idx := 1

	if provider != "" && provider != "all" {
		query += " AND provider = $" + strconv.Itoa(idx)
		args = append(args, provider)
		idx++
	}
	if search != "" {
		query += " AND (openrouter_id ILIKE $" + strconv.Itoa(idx) + " OR name ILIKE $" + strconv.Itoa(idx) + ")"
		args = append(args, "%"+search+"%")
	}
	query += " ORDER BY provider, name LIMIT 500"

	rows, err := r.db.Query(c.Request.Context(), query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list models"}})
		return
	}
	defer rows.Close()

	models := []gin.H{}
	for rows.Next() {
		var id, openrouterID, provider, name string
		var inputPrice, outputPrice *float64
		var contextWindow, maxOutputTokens *int
		var capabilities interface{}
		var rawData interface{}
		var syncedAt interface{}
		_ = rows.Scan(&id, &openrouterID, &provider, &name, &inputPrice, &outputPrice,
			&contextWindow, &maxOutputTokens, &capabilities, &rawData, &syncedAt)
		models = append(models, gin.H{
			"id": id, "openrouter_id": openrouterID, "provider": provider, "name": name,
			"input_price": inputPrice, "output_price": outputPrice,
			"context_window": contextWindow, "max_output_tokens": maxOutputTokens,
			"capabilities": capabilities, "raw_data": rawData, "synced_at": syncedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"models": models})
}
func (r *AdminRoutes) syncFromAIModelsStub(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "Sync queued", "added": 0, "hasChanges": 0, "skipped": 0})
}

// syncBedrockModels syncs AWS models into service_models.
// For Bedrock Runtime keys: calls ListFoundationModels + ListInferenceProfiles via Portal's SigV4.
// For Mantle keys: reads from upstream_keys.models (already populated by /v1/models verify).
// Preview mode (?preview=true) returns the model list without writing to DB.
func (r *AdminRoutes) syncBedrockModels(c *gin.Context) {
	requestedProvider := c.Query("provider")
	upstreamKeyID := c.Query("upstream_key_id")

	// Determine if this is a Mantle provider request
	isMantle := strings.HasPrefix(requestedProvider, "amazon-mantle-")

	var runtimeModels []aws.BedrockModel
	var mantleModels []string
	var provider string

	if isMantle {
		// Mantle: read models directly from upstream_keys.models (already fetched during verify)
		var modelsJSON []byte
		var query string
		var args []interface{}
		if upstreamKeyID != "" {
			query = `SELECT models, provider FROM upstream_keys
					 WHERE id = $1
					 AND provider LIKE 'amazon-mantle-%'
					 AND validation_status = 'valid'
					 AND enabled = true AND deleted_at IS NULL
					 AND models IS NOT NULL AND models != '[]'::jsonb`
			args = []interface{}{upstreamKeyID}
		} else {
			query = `SELECT models, provider FROM upstream_keys
					 WHERE provider = $1
					 AND validation_status = 'valid'
					 AND enabled = true AND deleted_at IS NULL
					 AND models IS NOT NULL AND models != '[]'::jsonb
					 ORDER BY created_at DESC LIMIT 1`
			args = []interface{}{requestedProvider}
		}
		err := r.db.QueryRow(c.Request.Context(), query, args...).Scan(&modelsJSON, &provider)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"error":   fmt.Sprintf("No valid %s key with models found. Please validate the key first.", requestedProvider),
			})
			return
		}
		if err := json.Unmarshal(modelsJSON, &mantleModels); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Failed to parse models"})
			return
		}
	} else {
		// Bedrock Runtime: validate via Portal's own SigV4 (also refreshes model list)
		var keyID, keyEncrypted string
		var query string
		var args []interface{}
		if upstreamKeyID != "" {
			query = `SELECT id::text, key_encrypted, provider FROM upstream_keys
					 WHERE id = $1
					 AND ` + validation.BedrockRuntimeProviderSQLCondition("provider") + `
					 AND validation_status = 'valid'
					 AND enabled = true AND deleted_at IS NULL`
			args = []interface{}{upstreamKeyID}
		} else {
			query = `SELECT id::text, key_encrypted, provider FROM upstream_keys
					 WHERE ` + validation.BedrockRuntimeProviderSQLCondition("provider") + `
					 AND validation_status = 'valid'
					 AND enabled = true AND deleted_at IS NULL
					 ORDER BY created_at DESC LIMIT 1`
			args = []interface{}{}
		}
		err := r.db.QueryRow(c.Request.Context(), query, args...).Scan(&keyID, &keyEncrypted, &provider)

		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"error":   T("error.bedrock_key_missing", c),
			})
			return
		}

		// Decrypt key
		var apiKey string
		if r.cfg.Auth.EncryptionKey != "" {
			decrypted, decErr := crypto.Decrypt(keyEncrypted, r.cfg.Auth.EncryptionKey)
			if decErr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Key decryption failed"})
				return
			}
			apiKey = decrypted
		} else {
			apiKey = keyEncrypted
		}

		// Validate via Portal's own SigV4 (gets real model list from AWS Bedrock, auto-filtered)
		status, models, valErr := aws.ValidateBedrockKeyWithCapabilities(provider, apiKey)
		if valErr != nil || status != "valid" {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"error":   fmt.Sprintf("AWS key validation failed: %v", valErr),
			})
			return
		}
		runtimeModels = models
	}

	// Optional selection: a POST body {"models": [...]} limits the sync to the
	// admin's checkbox selection. An absent/empty body keeps full-sync behavior.
	if c.Request.Method == http.MethodPost && c.Request.ContentLength != 0 {
		var req struct {
			Models []string `json:"models"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Invalid request body"})
			return
		}
		if isMantle {
			mantleModels = filterSelectedModels(mantleModels, req.Models)
		} else {
			want := make(map[string]bool, len(req.Models))
			for _, m := range req.Models {
				want[m] = true
			}
			filtered := make([]aws.BedrockModel, 0, len(req.Models))
			for _, m := range runtimeModels {
				if want[m.ModelID] {
					filtered = append(filtered, m)
				}
			}
			runtimeModels = filtered
		}
	}

	// Preview mode: return model list without writing to DB
	if c.Query("preview") == "true" {
		type previewModel struct {
			ProductID    string `json:"product_id"`
			APIModelName string `json:"api_model_name"`
			Name         string `json:"name"`
		}
		var previews []previewModel
		if isMantle {
			for _, m := range mantleModels {
				apiName := generateBedrockAPIModelName(m)
				previews = append(previews, previewModel{
					ProductID:    m,
					APIModelName: apiName,
					Name:         m,
				})
			}
		} else {
			for _, m := range runtimeModels {
				apiName := generateBedrockAPIModelName(m.ModelID)
				name := m.ModelName
				if name == "" {
					name = m.ModelID
				}
				previews = append(previews, previewModel{
					ProductID:    m.ModelID,
					APIModelName: apiName,
					Name:         name,
				})
			}
		}
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"preview": true,
			"models":  previews,
			"total":   len(previews),
		})
		return
	}

	// Sync to DB
	added := 0
	updated := 0
	if isMantle {
		for _, m := range mantleModels {
			apiName := generateBedrockAPIModelName(m)
			result, dbErr := r.db.Pool.Exec(c.Request.Context(),
				`INSERT INTO service_models (provider, product_id, api_model_name, status,
				  aimodels_data, created_at)
				 VALUES ($1, $2, $3, 'pending', $4, NOW())
				 ON CONFLICT (provider, product_id) DO UPDATE SET
				  api_model_name = EXCLUDED.api_model_name,
				  updated_at = NOW()`,
				provider, m, apiName, json.RawMessage("{}"))

			if dbErr != nil {
				r.logger.Warn("Failed to upsert mantle model", zap.String("model", m), zap.String("error", dbErr.Error()))
				continue
			}
			if result.RowsAffected() > 0 {
				added++
			} else {
				updated++
			}
		}
		r.logger.Info("AWS Mantle models synced from upstream key",
			zap.Int("added", added),
			zap.Int("updated", updated),
			zap.Int("total", len(mantleModels)),
		)
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"added":   added,
			"updated": updated,
			"total":   len(mantleModels),
		})
		return
	}

	for _, m := range runtimeModels {
		apiName := generateBedrockAPIModelName(m.ModelID)
		caps := bedrockCapabilities(m)
		capsJSON, _ := json.Marshal(caps)
		result, dbErr := r.db.Pool.Exec(c.Request.Context(),
			`INSERT INTO service_models (provider, product_id, api_model_name, status,
			  capabilities, aimodels_data, created_at)
			 VALUES ($1, $2, $3, 'pending', $4, $5, NOW())
			 ON CONFLICT (provider, product_id) DO UPDATE SET
			  api_model_name = EXCLUDED.api_model_name,
			  capabilities = COALESCE(NULLIF(service_models.capabilities, '{}'::jsonb), EXCLUDED.capabilities),
			  updated_at = NOW()`,
			provider, m.ModelID, apiName, capsJSON, json.RawMessage("{}"))

		if dbErr != nil {
			r.logger.Warn("Failed to upsert bedrock model", zap.String("model", m.ModelID), zap.String("error", dbErr.Error()))
			continue
		}
		if result.RowsAffected() > 0 {
			added++
		} else {
			updated++
		}
	}

	r.logger.Info("AWS Bedrock models synced from upstream key",
		zap.Int("added", added),
		zap.Int("updated", updated),
		zap.Int("total", len(runtimeModels)),
	)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"added":   added,
		"updated": updated,
		"total":   len(runtimeModels),
	})
}

// bedrockCapabilities derives our normalized capabilities from an AWS Bedrock
// model record. Bedrock does not expose context window via ListFoundationModels,
// so those fields are left for manual / later enrichment.
func bedrockCapabilities(m aws.BedrockModel) map[string]interface{} {
	caps := make(map[string]interface{})

	has := func(list []string, want string) bool {
		for _, v := range list {
			if strings.EqualFold(v, want) {
				return true
			}
		}
		return false
	}

	if has(m.InputModalities, "IMAGE") || has(m.OutputModalities, "IMAGE") {
		caps["supports_vision"] = true
	}
	if has(m.InputModalities, "AUDIO") || has(m.OutputModalities, "AUDIO") {
		caps["supports_audio"] = true
	}
	if m.StreamingSupported {
		caps["supports_streaming"] = true
	}
	if has(m.OutputModalities, "TEXT") {
		caps["supports_text"] = true
	}

	return caps
}

// syncMantleModels syncs AWS Bedrock Mantle models from the first valid Mantle upstream key.
// Mantle keys use Bearer token auth — Portal can validate them directly via /v1/models.
func (r *AdminRoutes) syncMantleModels(c *gin.Context) {
	// Find the first valid AWS Mantle upstream key
	var keyID, keyEncrypted, provider, keyBaseURL string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT id::text, key_encrypted, provider, COALESCE(base_url, '') FROM upstream_keys
		 WHERE provider LIKE 'amazon-mantle-%'
		 AND validation_status = 'valid'
		 AND enabled = true
		 ORDER BY created_at DESC LIMIT 1`,
	).Scan(&keyID, &keyEncrypted, &provider, &keyBaseURL)

	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   T("error.mantle_key_missing", c),
		})
		return
	}

	// Decrypt key
	var apiKey string
	if r.cfg.Auth.EncryptionKey != "" {
		decrypted, decErr := crypto.Decrypt(keyEncrypted, r.cfg.Auth.EncryptionKey)
		if decErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Key decryption failed"})
			return
		}
		apiKey = decrypted
	} else {
		apiKey = keyEncrypted
	}

	// Mantle uses Bearer token — Portal can validate directly
	status, fetchedModels, valErr := validateUpstreamKey(provider, apiKey, keyBaseURL)
	if valErr != nil || (status != "valid" && status != "pending") {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   fmt.Sprintf("Mantle key validation failed: %v", valErr),
		})
		return
	}

	// Preview mode
	if c.Query("preview") == "true" {
		type previewModel struct {
			ProductID    string `json:"product_id"`
			APIModelName string `json:"api_model_name"`
			Name         string `json:"name"`
		}
		var previews []previewModel
		for _, m := range fetchedModels {
			apiName := generateBedrockAPIModelName(m)
			previews = append(previews, previewModel{
				ProductID:    m,
				APIModelName: apiName,
				Name:         m,
			})
		}
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"preview": true,
			"models":  previews,
			"total":   len(previews),
		})
		return
	}

	// Sync to DB
	added := 0
	updated := 0
	for _, m := range fetchedModels {
		apiName := generateBedrockAPIModelName(m)
		result, dbErr := r.db.Pool.Exec(c.Request.Context(),
			`INSERT INTO service_models (provider, product_id, api_model_name, status,
			  aimodels_data, created_at)
			 VALUES ($1, $2, $3, 'pending', $4, NOW())
			 ON CONFLICT (provider, product_id) DO UPDATE SET
			  api_model_name = EXCLUDED.api_model_name,
			  updated_at = NOW()`,
			provider, m, apiName, json.RawMessage("{}"))

		if dbErr != nil {
			r.logger.Warn("Failed to upsert mantle model", zap.String("model", m), zap.String("error", dbErr.Error()))
			continue
		}
		if result.RowsAffected() > 0 {
			added++
		} else {
			updated++
		}
	}

	r.logger.Info("AWS Mantle models synced from upstream key",
		zap.Int("added", added),
		zap.Int("updated", updated),
		zap.Int("total", len(fetchedModels)),
	)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"added":   added,
		"updated": updated,
		"total":   len(fetchedModels),
	})
}

// syncFromUpstreamKey is the unified endpoint for syncing models from any upstream key.
// POST /api/admin/service-models/sync-from-key  body: {"provider":"amazon"}
// Routes to Proxy (AK/SK) or Portal direct (Bearer) based on provider type.
func (r *AdminRoutes) syncFromUpstreamKey(c *gin.Context) {
	var req struct {
		Provider       string `json:"provider"`
		ProviderFilter string `json:"provider_filter"`
		Models         []struct {
			ProductID     string `json:"product_id"`
			APIModelName  string `json:"api_model_name"`
			Name          string `json:"name"`
			InputPrice    string `json:"input_price"`
			CacheHitPrice string `json:"cache_hit_price"`
			OutputPrice   string `json:"output_price"`
		} `json:"models"`
		Activate bool `json:"activate"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Provider == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": T("error.provider_param_required", c)})
		return
	}

	var keyID, keyEncrypted, keyBaseURL string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT id::text, key_encrypted, COALESCE(base_url, '') FROM upstream_keys
		 WHERE provider = $1 AND validation_status IN ('valid', 'pending')
		 AND enabled = true
		 ORDER BY created_at DESC LIMIT 1`,
		req.Provider,
	).Scan(&keyID, &keyEncrypted, &keyBaseURL)

	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   fmt.Sprintf(T("error.upstream_key_not_found_fmt", c), req.Provider),
		})
		return
	}

	var apiKey string
	if r.cfg.Auth.EncryptionKey != "" {
		decrypted, decErr := crypto.Decrypt(keyEncrypted, r.cfg.Auth.EncryptionKey)
		if decErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Key decryption failed"})
			return
		}
		apiKey = decrypted
	} else {
		apiKey = keyEncrypted
	}

	var status string
	var fetchedModels []string
	var valErr error

	isAWSRuntime := req.Provider == "amazon" || (strings.HasPrefix(req.Provider, "amazon-") && !strings.HasPrefix(req.Provider, "amazon-mantle-"))

	// If models are provided in the request, sync those directly (batch confirm flow)
	if len(req.Models) > 0 {
		added := 0
		updated := 0
		for _, m := range req.Models {
			apiName := m.APIModelName
			if apiName == "" {
				apiName = generateBedrockAPIModelName(m.ProductID)
			}
			inputPrice, _ := strconv.ParseFloat(m.InputPrice, 64)
			cacheHitPrice, _ := strconv.ParseFloat(m.CacheHitPrice, 64)
			outputPrice, _ := strconv.ParseFloat(m.OutputPrice, 64)
			status := "pending"
			if req.Activate {
				status = "active"
			}
			result, dbErr := r.db.Pool.Exec(c.Request.Context(),
				`INSERT INTO service_models (provider, product_id, api_model_name, status,
				  input_price, input_price_cache_hit, output_price, aimodels_data, created_at)
				 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
				 ON CONFLICT (provider, product_id) DO UPDATE SET
				  api_model_name = EXCLUDED.api_model_name,
				  status = EXCLUDED.status,
				  input_price = EXCLUDED.input_price,
				  input_price_cache_hit = EXCLUDED.input_price_cache_hit,
				  output_price = EXCLUDED.output_price,
				  updated_at = NOW()`,
				req.Provider, m.ProductID, apiName, status, inputPrice, cacheHitPrice, outputPrice, json.RawMessage("{}"))
			if dbErr != nil {
				r.logger.Warn("Failed to upsert model from batch", zap.String("model", m.ProductID), zap.String("error", dbErr.Error()))
				continue
			}
			if result.RowsAffected() > 0 {
				added++
			} else {
				updated++
			}
		}
		r.logger.Info("Models batch-synced from key", zap.String("provider", req.Provider), zap.Int("added", added), zap.Int("updated", updated))
		c.JSON(http.StatusOK, gin.H{"success": true, "added": added, "updated": updated, "total": len(req.Models)})
		return
	}

	// No models provided — fetch from the upstream key
	if isAWSRuntime {
		status, fetchedModels, valErr = validateViaProxyFilter(req.Provider, apiKey, req.ProviderFilter)
	} else {
		status, fetchedModels, valErr = validateUpstreamKey(req.Provider, apiKey, keyBaseURL)
		// Apply provider filter for Mantle/Bearer keys (models have provider prefix like "deepseek.v3.1")
		if req.ProviderFilter != "" && len(fetchedModels) > 0 {
			filters := strings.Split(strings.ToLower(req.ProviderFilter), ",")
			var filtered []string
			for _, m := range fetchedModels {
				ml := strings.ToLower(m)
				for _, f := range filters {
					f = strings.TrimSpace(f)
					if f != "" && strings.HasPrefix(ml, f+".") {
						filtered = append(filtered, m)
						break
					}
				}
			}
			if len(filtered) > 0 {
				fetchedModels = filtered
			}
		}
	}

	if valErr != nil || (status != "valid" && status != "pending") {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   fmt.Sprintf("Key validation failed: %v", valErr),
		})
		return
	}

	if c.Query("preview") == "true" {
		type previewModel struct {
			ProductID    string `json:"product_id"`
			APIModelName string `json:"api_model_name"`
			Name         string `json:"name"`
		}
		var previews []previewModel
		for _, m := range fetchedModels {
			apiName := generateBedrockAPIModelName(m)
			previews = append(previews, previewModel{
				ProductID:    m,
				APIModelName: apiName,
				Name:         m,
			})
		}
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"preview": true,
			"models":  previews,
			"total":   len(previews),
		})
		return
	}

	added := 0
	updated := 0
	for _, m := range fetchedModels {
		apiName := generateBedrockAPIModelName(m)
		result, dbErr := r.db.Pool.Exec(c.Request.Context(),
			`INSERT INTO service_models (provider, product_id, api_model_name, status,
			  aimodels_data, created_at)
			 VALUES ($1, $2, $3, 'pending', $4, NOW())
			 ON CONFLICT (provider, product_id) DO UPDATE SET
			  api_model_name = EXCLUDED.api_model_name,
			  updated_at = NOW()`,
			req.Provider, m, apiName, json.RawMessage("{}"))

		if dbErr != nil {
			r.logger.Warn("Failed to upsert model from key", zap.String("provider", req.Provider), zap.String("model", m), zap.String("error", dbErr.Error()))
			continue
		}
		if result.RowsAffected() > 0 {
			added++
		} else {
			updated++
		}
	}

	r.logger.Info("Models synced from upstream key",
		zap.String("provider", req.Provider),
		zap.Int("added", added),
		zap.Int("updated", updated),
		zap.Int("total", len(fetchedModels)),
	)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"added":   added,
		"updated": updated,
		"total":   len(fetchedModels),
	})
}

// BackfillBedrockCapabilities iterates over existing AWS service_models with empty
// capabilities and tries to pre-fill them from the upstream Bedrock key.
// It is intended to run once at startup (idempotent).
func (r *AdminRoutes) BackfillBedrockCapabilities(ctx context.Context) (updated int, err error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, provider, product_id, COALESCE(api_model_name, '')
		 FROM service_models
		 WHERE deleted_at IS NULL
		   AND provider LIKE 'amazon-%'
		   AND provider NOT LIKE 'amazon-mantle-%'
		   AND (capabilities IS NULL OR capabilities = '{}'::jsonb OR capabilities = 'null'::jsonb)`)
	if err != nil {
		return 0, fmt.Errorf("query service_models for bedrock backfill: %w", err)
	}
	defer rows.Close()

	type modelRef struct {
		id           string
		provider     string
		productID    string
		apiModelName string
	}
	var refs []modelRef
	for rows.Next() {
		var id, provider, productID, apiModelName string
		if err := rows.Scan(&id, &provider, &productID, &apiModelName); err != nil {
			continue
		}
		refs = append(refs, modelRef{id: id, provider: provider, productID: productID, apiModelName: apiModelName})
	}

	// Group by provider so we only call ValidateBedrockKey once per provider.
	byProvider := make(map[string][]modelRef)
	for _, m := range refs {
		byProvider[m.provider] = append(byProvider[m.provider], m)
	}

	for provider, models := range byProvider {
		var keyEncrypted string
		err := r.db.QueryRow(ctx,
			`SELECT key_encrypted FROM upstream_keys
			 WHERE provider = $1
			 AND validation_status = 'valid'
			 AND enabled = true AND deleted_at IS NULL
			 ORDER BY created_at DESC LIMIT 1`,
			provider,
		).Scan(&keyEncrypted)
		if err != nil {
			r.logger.Warn("No valid Bedrock key for backfill", zap.String("provider", provider), zap.Error(err))
			continue
		}

		var apiKey string
		if r.cfg.Auth.EncryptionKey != "" {
			decrypted, decErr := crypto.Decrypt(keyEncrypted, r.cfg.Auth.EncryptionKey)
			if decErr != nil {
				r.logger.Warn("Failed to decrypt Bedrock key", zap.String("provider", provider), zap.Error(decErr))
				continue
			}
			apiKey = decrypted
		} else {
			apiKey = keyEncrypted
		}

		status, bedrockModels, valErr := aws.ValidateBedrockKeyWithCapabilities(provider, apiKey)
		if valErr != nil || status != "valid" {
			r.logger.Warn("Bedrock validation failed during backfill", zap.String("provider", provider), zap.Error(valErr))
			continue
		}

		capsByModelID := make(map[string]map[string]interface{}, len(bedrockModels))
		for _, m := range bedrockModels {
			capsByModelID[m.ModelID] = bedrockCapabilities(m)
		}

		for _, m := range models {
			caps, ok := capsByModelID[m.productID]
			if !ok && m.apiModelName != "" {
				caps, ok = capsByModelID[m.apiModelName]
			}
			if !ok || len(caps) == 0 {
				continue
			}
			capsJSON, _ := json.Marshal(caps)
			_, err := r.db.Pool.Exec(ctx,
				`UPDATE service_models
				 SET capabilities = $1,
				     updated_at = NOW()
				 WHERE id = $2`,
				capsJSON, m.id)
			if err != nil {
				r.logger.Warn("Failed to backfill Bedrock capabilities",
					zap.String("model_id", m.id),
					zap.Error(err))
				continue
			}
			updated++
		}
	}

	return updated, nil
}

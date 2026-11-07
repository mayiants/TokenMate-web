package models

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mayiants/ai-proxy-go/internal/config"
	"github.com/mayiants/ai-proxy-go/pkg/relay"
	"go.uber.org/zap"
)

// ValidationResult contains model validation details
type ValidationResult struct {
	Provider         string    `json:"provider"`
	ProductID        string    `json:"product_id"`
	SuggestedAPIName string    `json:"suggested_api_name"`
	Available        bool      `json:"available"`
	LatencyMs        int       `json:"latency_ms"`
	ErrorType        string    `json:"error_type,omitempty"`
	ErrorMsg         string    `json:"error_msg,omitempty"`
	OpenRouterName   string    `json:"openrouter_name,omitempty"`
	CandidatesTested []string  `json:"candidates_tested"`
	CheckedAt        time.Time `json:"checked_at"`
}

// ModelValidator validates model availability and suggests correct API names
type ModelValidator struct {
	cfg    *config.Config
	logger *zap.Logger
	client *http.Client

	// API keys for testing (from key pool or config)
	testKeys   map[string]string
	testKeysMu sync.RWMutex

	// OpenRouter model list cache
	openRouterModels map[string]string // product_id -> openrouter_name
	openRouterMu     sync.RWMutex
}

// NewModelValidator creates a new model validator
func NewModelValidator(cfg *config.Config, logger *zap.Logger) *ModelValidator {
	return &ModelValidator{
		cfg:              cfg,
		logger:           logger,
		client:           &http.Client{Timeout: 30 * time.Second},
		testKeys:         make(map[string]string),
		openRouterModels: make(map[string]string),
	}
}

// SetTestKey sets an API key for testing a specific provider
func (v *ModelValidator) SetTestKey(provider, apiKey string) {
	v.testKeysMu.Lock()
	v.testKeys[provider] = apiKey
	v.testKeysMu.Unlock()
}

// GetTestKey gets the test API key for a provider
func (v *ModelValidator) GetTestKey(provider string) string {
	v.testKeysMu.RLock()
	key := v.testKeys[provider]
	v.testKeysMu.RUnlock()
	return key
}

// ValidateModel validates a single model and returns suggested API name
func (v *ModelValidator) ValidateModel(ctx context.Context, provider, productID string) *ValidationResult {
	result := &ValidationResult{
		Provider:         provider,
		ProductID:        productID,
		CheckedAt:        time.Now(),
		CandidatesTested: generateAPINameCandidates(productID),
	}

	// Get test API key
	apiKey := v.GetTestKey(provider)
	if apiKey == "" {
		result.ErrorType = "no_test_key"
		result.ErrorMsg = "No API key configured for testing"
		return result
	}

	// Get provider config
	providerCfg, ok := relay.GetProviderConfig(provider)
	if !ok {
		result.ErrorType = "unknown_provider"
		result.ErrorMsg = fmt.Sprintf("Provider %s not configured", provider)
		return result
	}

	// Try each candidate API name
	for _, candidate := range result.CandidatesTested {
		testResult := v.testModelCall(ctx, provider, providerCfg.BaseURL, candidate, apiKey)

		if testResult.Available {
			result.SuggestedAPIName = candidate
			result.Available = true
			result.LatencyMs = testResult.LatencyMs
			v.logger.Info("Model validated",
				zap.String("provider", provider),
				zap.String("product_id", productID),
				zap.String("api_name", candidate),
				zap.Int("latency_ms", testResult.LatencyMs),
			)
			return result
		}
	}

	// All candidates failed
	result.Available = false
	result.ErrorType = "model_not_found"
	result.ErrorMsg = "All API name candidates failed validation"

	v.logger.Warn("Model validation failed",
		zap.String("provider", provider),
		zap.String("product_id", productID),
		zap.Strings("candidates", result.CandidatesTested),
	)

	return result
}

// testModelCall tests if a model is available by making a minimal API call
func (v *ModelValidator) testModelCall(ctx context.Context, provider, baseURL, modelName, apiKey string) *ValidationResult {
	result := &ValidationResult{
		Available: false,
	}

	start := time.Now()

	// Create minimal test request
	testReq := map[string]interface{}{
		"model": modelName,
		"messages": []map[string]string{
			{"role": "user", "content": "test"},
		},
		"max_tokens": 1,
	}

	reqBody, err := json.Marshal(testReq)
	if err != nil {
		result.ErrorType = "request_error"
		result.ErrorMsg = err.Error()
		return result
	}

	// Build URL
	url := baseURL + "/chat/completions"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(reqBody)))
	if err != nil {
		result.ErrorType = "request_error"
		result.ErrorMsg = err.Error()
		return result
	}

	// Set headers based on provider auth type
	providerCfg, _ := relay.GetProviderConfig(provider)
	switch providerCfg.AuthType {
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+apiKey)
	case "x-api-key":
		req.Header.Set("x-api-key", apiKey)
	case "query":
		// SECURITY: API key exposed in URL query parameter — will appear in server access logs.
		// Only use for providers that do not support header-based auth (e.g., some Google endpoints).
		// Prefer migrating such providers to bearer or x-api-key auth if the provider supports it.
		req.URL.RawQuery = "key=" + apiKey
	default:
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := v.client.Do(req)
	if err != nil {
		result.ErrorType = "network_error"
		result.ErrorMsg = err.Error()
		return result
	}
	defer func() { _ = resp.Body.Close() }()

	result.LatencyMs = int(time.Since(start).Milliseconds())

	// Check response
	if resp.StatusCode == 200 {
		result.Available = true
		return result
	}

	// Parse error response
	var errResp struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&errResp); err == nil {
		result.ErrorType = errResp.Error.Type
		result.ErrorMsg = errResp.Error.Message

		// Check for specific error types
		if strings.Contains(errResp.Error.Code, "model_not_found") ||
			strings.Contains(errResp.Error.Message, "does not exist") ||
			strings.Contains(errResp.Error.Message, "not found") {
			result.ErrorType = "model_not_found"
		} else if strings.Contains(errResp.Error.Code, "invalid_api_key") ||
			strings.Contains(errResp.Error.Message, "invalid") {
			result.ErrorType = "auth_error"
		}
	}

	return result
}

// generateAPINameCandidates generates possible API names from product_id
func generateAPINameCandidates(productID string) []string {
	candidates := []string{productID}

	// Replace last - with . (e.g., kimi-k2-5 -> kimi-k2.5)
	if idx := strings.LastIndex(productID, "-"); idx > 0 {
		dotVersion := productID[:idx] + "." + productID[idx+1:]
		candidates = append(candidates, dotVersion)
	}

	// Handle version patterns like claude-sonnet-4-5 -> claude-sonnet-4.5
	if strings.Contains(productID, "-4-") || strings.Contains(productID, "-5-") {
		// Try with dot version for the version part
		parts := strings.Split(productID, "-")
		if len(parts) >= 3 {
			// Join last two parts with dot
			lastIdx := len(parts) - 1
			dotVersion := strings.Join(parts[:lastIdx-1], "-") + "-" + parts[lastIdx-1] + "." + parts[lastIdx]
			candidates = append(candidates, dotVersion)
		}
	}

	// Add common suffixes
	candidates = append(candidates, productID+"-latest")

	// Add date suffix (common for Anthropic)
	candidates = append(candidates, productID+"-20250101")

	return candidates
}

// FetchOpenRouterModels fetches model list from OpenRouter and builds mapping
func (v *ModelValidator) FetchOpenRouterModels(ctx context.Context) error {
	url := "https://openrouter.ai/api/v1/models"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch models: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return fmt.Errorf("openrouter returned status %d", resp.StatusCode)
	}

	var result struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	// Build mapping
	v.openRouterMu.Lock()
	v.openRouterModels = make(map[string]string)
	for _, model := range result.Data {
		// Extract product_id from OpenRouter ID (e.g., "moonshot/kimi-k2-5" -> "kimi-k2-5")
		parts := strings.Split(model.ID, "/")
		if len(parts) == 2 {
			productID := parts[1]
			v.openRouterModels[productID] = model.ID
		}
	}
	v.openRouterMu.Unlock()

	v.logger.Info("Fetched OpenRouter models", zap.Int("count", len(v.openRouterModels)))
	return nil
}

// GetOpenRouterName returns the OpenRouter model name for a product_id
func (v *ModelValidator) GetOpenRouterName(productID string) string {
	v.openRouterMu.RLock()
	name := v.openRouterModels[productID]
	v.openRouterMu.RUnlock()
	return name
}

// BatchValidate validates multiple models in parallel
func (v *ModelValidator) BatchValidate(ctx context.Context, models []ServiceModel) []*ValidationResult {
	results := make([]*ValidationResult, len(models))

	var wg sync.WaitGroup
	for i, model := range models {
		wg.Add(1)
		go func(idx int, m ServiceModel) {
			defer wg.Done()
			results[idx] = v.ValidateModel(ctx, m.Provider, m.ProductID)
		}(i, model)
	}
	wg.Wait()

	return results
}

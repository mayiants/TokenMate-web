package validation

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	awspkg "github.com/ai-proxy/ai-portal-go/internal/aws"
	"github.com/ai-proxy/ai-portal-go/internal/config"
	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/pkg/crypto"
	"go.uber.org/zap"
)

// ModelValidator validates model API names by testing against providers
type ModelValidator struct {
	db     *db.DB
	cfg    *config.Config
	logger *zap.Logger
	client *http.Client
}

// ValidationResult contains the result of a model validation
type ValidationResult struct {
	Provider         string    `json:"provider"`
	ProductID        string    `json:"productId"`
	APIModelName     string    `json:"apiModelName"`
	SuggestedName    string    `json:"suggestedName"`
	Available        bool      `json:"available"`
	Skipped          bool      `json:"skipped"`
	LatencyMs        int       `json:"latencyMs"`
	ErrorType        string    `json:"errorType"`
	ErrorMessage     string    `json:"errorMessage"`
	CandidatesTested []string  `json:"candidatesTested"`
	CheckedAt        time.Time `json:"checkedAt"`
}

// NewModelValidator creates a new model validator
func NewModelValidator(db *db.DB, cfg *config.Config, logger *zap.Logger) *ModelValidator {
	return &ModelValidator{
		db:     db,
		cfg:    cfg,
		logger: logger,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// GenerateAPINameCandidates generates possible API names from a product ID
func GenerateAPINameCandidates(productID string) []string {
	candidates := []string{productID}

	// Replace hyphens with dots: kimi-k2-5 -> kimi-k2.5
	if strings.Contains(productID, "-") {
		dotVersion := strings.ReplaceAll(productID, "-", ".")
		candidates = append(candidates, dotVersion)
	}

	// Add -latest suffix for versioned models
	if !strings.HasSuffix(productID, "-latest") {
		candidates = append(candidates, productID+"-latest")
	}

	// Add common version patterns
	parts := strings.Split(productID, "-")
	if len(parts) >= 2 {
		// Try with just the base name: gpt-4-turbo -> gpt-4
		baseName := strings.Join(parts[:2], "-")
		if baseName != productID {
			candidates = append(candidates, baseName)
		}
	}

	return candidates
}

// isNonTextModel checks if a model is likely a non-text model (video, image,
// audio, embedding) that cannot be validated via chat/completions.
// It inspects the model type from aimodels_data and the model name itself.
func isNonTextModel(modelType, productID, apiModelName string) bool {
	// Check explicit model type from aimodels_data
	t := strings.ToLower(modelType)
	if t == "video" || t == "image" || t == "audio" || t == "embedding" || t == "tts" || t == "stt" {
		return true
	}

	// Check model name patterns for known non-text models
	name := strings.ToLower(productID + " " + apiModelName)
	nonTextPatterns := []string{
		// Video generation
		"kling", "sora", "runway", "pika", "veo", "minimax-hailuo",
		// Image generation
		"dall-e", "midjourney", "stable-diffusion", "sdxl", "flux", "imagen",
		// Audio
		"tts", "whisper", "speech", "audio",
		// Embeddings
		"embedding", "embed",
	}
	for _, p := range nonTextPatterns {
		if strings.Contains(name, p) {
			return true
		}
	}
	return false
}

// ValidateModel validates a single model's API name
func (v *ModelValidator) ValidateModel(ctx context.Context, provider, productID, currentAPIName, modelType string, supportsThinking bool) (*ValidationResult, error) {
	result := &ValidationResult{
		Provider:     provider,
		ProductID:    productID,
		APIModelName: currentAPIName,
		CheckedAt:    time.Now(),
	}

	// Skip non-text models (video, image, audio, embedding) — the validator
	// only tests chat/completions, so these would be falsely marked invalid.
	if isNonTextModel(modelType, productID, currentAPIName) {
		result.Skipped = true
		result.ErrorType = "skipped"
		result.ErrorMessage = "Non-text model (video/image/audio/embedding) — validation skipped"
		return result, nil
	}

	// Generate candidates
	candidates := GenerateAPINameCandidates(productID)
	if currentAPIName != "" && currentAPIName != productID {
		// Include current name as first candidate
		candidates = append([]string{currentAPIName}, candidates...)
	}
	result.CandidatesTested = candidates

	// Get an upstream key for testing
	var keyID, key string
	var baseURL *string
	err := v.db.Pool.QueryRow(ctx, `
		SELECT id::text, key_encrypted, base_url
		FROM upstream_keys
		WHERE provider = $1 AND enabled = true AND validation_status = 'valid'
		LIMIT 1
	`, provider).Scan(&keyID, &key, &baseURL)
	if err != nil {
		result.ErrorType = "no_key"
		result.ErrorMessage = "No valid upstream key available for validation"
		return result, nil
	}

	// Decrypt key
	if v.cfg.Auth.EncryptionKey != "" {
		decrypted, decErr := crypto.Decrypt(key, v.cfg.Auth.EncryptionKey)
		if decErr != nil {
			result.ErrorType = "decrypt_error"
			result.ErrorMessage = "Failed to decrypt upstream key"
			return result, nil
		}
		key = decrypted
	}

	// Test each candidate
	for _, candidate := range candidates {
		start := time.Now()
		available, errType, errMsg := v.testModelRequest(ctx, provider, key, baseURL, candidate, supportsThinking)
		latency := time.Since(start).Milliseconds()

		result.LatencyMs = int(latency)

		if available {
			result.Available = true
			result.SuggestedName = candidate
			if candidate != currentAPIName {
				v.logger.Info("Model validation found better API name",
					zap.String("provider", provider),
					zap.String("productId", productID),
					zap.String("current", currentAPIName),
					zap.String("suggested", candidate))
			}
			return result, nil
		}

		// If we got a specific error (not 404), record it
		if errType != "not_found" {
			result.ErrorType = errType
			result.ErrorMessage = errMsg
		}
	}

	// If no candidate produced a specific error, fall back to a generic one.
	// Keep a recorded error (e.g. access_denied) so admins see the real cause.
	if result.ErrorType == "" {
		result.ErrorType = "all_candidates_failed"
		result.ErrorMessage = fmt.Sprintf("None of the %d candidates were valid", len(candidates))
	}
	return result, nil
}

// IsBedrockRuntimeProvider reports whether the provider uses the Bedrock
// Runtime Converse API with SigV4 signing (Mantle is OpenAI-compatible and excluded).
func IsBedrockRuntimeProvider(provider string) bool {
	return provider == "amazon" || (strings.HasPrefix(provider, "amazon-") && !strings.HasPrefix(provider, "amazon-mantle-"))
}

// BedrockRuntimeProviderSQLCondition returns the SQL WHERE fragment matching
// Bedrock Runtime providers — the SQL twin of IsBedrockRuntimeProvider.
// column is the provider column name (already qualified if needed).
func BedrockRuntimeProviderSQLCondition(column string) string {
	return "(" + column + " = 'amazon' OR (" + column + " LIKE 'amazon-%' AND " + column + " NOT LIKE 'amazon-mantle-%'))"
}

// testModelRequest makes a test request to check if a model name is valid
func (v *ModelValidator) testModelRequest(ctx context.Context, provider, key string, baseURL *string, model string, supportsThinking bool) (bool, string, string) {
	// Bedrock Runtime providers need SigV4 + Converse API format
	if IsBedrockRuntimeProvider(provider) {
		return v.testBedrockRuntimeModel(ctx, provider, key, model)
	}

	// All other providers: OpenAI-compatible chat/completions
	url := v.buildTestURL(provider, baseURL, model, key)
	if url == "" {
		return false, "unsupported_provider", "Provider not supported for validation"
	}

	// Build a minimal valid chat request body
	var body string
	switch provider {
	case "anthropic":
		if supportsThinking {
			body = fmt.Sprintf(`{"model":"%s","messages":[{"role":"user","content":"hi"}],"max_tokens":2000,"thinking":{"type":"enabled","budget_tokens":1024}}`, model)
		} else {
			body = fmt.Sprintf(`{"model":"%s","messages":[{"role":"user","content":"hi"}],"max_tokens":1}`, model)
		}
	default:
		// OpenAI-compatible format (works for Mantle, accessapi, deepseek, etc.)
		body = fmt.Sprintf(`{"model":"%s","messages":[{"role":"user","content":"hi"}],"max_tokens":1}`, model)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return false, "request_error", err.Error()
	}

	// Set headers based on provider
	v.setHeaders(req, provider, key)

	resp, err := v.client.Do(req)
	if err != nil {
		return false, "request_error", err.Error()
	}
	defer func() { _ = resp.Body.Close() }()

	// Read and parse response
	respBytes, _ := io.ReadAll(resp.Body)
	var respBody map[string]interface{}
	_ = json.Unmarshal(respBytes, &respBody)

	switch resp.StatusCode {
	case 200, 201:
		return true, "", ""
	case 400:
		// Check if it's a model not found error vs a format error
		errMsg := extractErrorMessage(respBody)
		lower := strings.ToLower(errMsg)
		if strings.Contains(lower, "does not exist") ||
			strings.Contains(lower, "not found") ||
			strings.Contains(lower, "unknown model") ||
			strings.Contains(lower, "not available") ||
			strings.Contains(lower, "model identifier is invalid") {
			return false, "not_found", errMsg
		}
		// 400 but not model-related = model probably exists but request format issue
		// Treat as valid (model exists, just bad request format)
		if strings.Contains(lower, "message") || strings.Contains(lower, "token") {
			return true, "", ""
		}
		return false, "bad_request", errMsg
	case 401, 403:
		errMsg := extractErrorMessage(respBody)
		lower := strings.ToLower(errMsg)
		// "not available for this account" = model exists but no access
		if strings.Contains(lower, "not available") || strings.Contains(lower, "access_denied") {
			return false, "access_denied", errMsg
		}
		return false, "auth_error", "Authentication failed"
	case 404:
		return false, "not_found", "Model not found"
	case 429:
		// Rate limited = model exists and works, just throttled
		return true, "", ""
	default:
		return false, "error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, extractErrorMessage(respBody))
	}
}

// testBedrockRuntimeModel validates a model via Bedrock Converse API with SigV4
func (v *ModelValidator) testBedrockRuntimeModel(ctx context.Context, provider, credentials, model string) (bool, string, string) {
	accessKey, secretKey, sessionToken, err := parseBedrockCredentials(credentials)
	if err != nil {
		return false, "decrypt_error", err.Error()
	}

	region := extractRegionFromProvider(provider)
	if region == "" {
		return false, "config_error", "Cannot extract region from provider"
	}

	// Build Converse API request
	endpoint := fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com/model/%s/converse", region, model)
	body := `{"messages":[{"role":"user","content":[{"text":"hi"}]}],"inferenceConfig":{"maxTokens":1}}`

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		return false, "request_error", err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	awspkg.SignRequest(req, []byte(body), accessKey, secretKey, sessionToken, region, "bedrock")

	resp, err := v.client.Do(req)
	if err != nil {
		return false, "request_error", err.Error()
	}
	defer func() { _ = resp.Body.Close() }()

	respBytes, _ := io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case 200:
		return true, "", ""
	case 400:
		msg := string(respBytes)
		lower := strings.ToLower(msg)
		if strings.Contains(lower, "model identifier is invalid") ||
			strings.Contains(lower, "not found") {
			return false, "not_found", msg
		}
		// Other 400 = model exists but request issue
		return true, "", ""
	case 403:
		msg := string(respBytes)
		if strings.Contains(strings.ToLower(msg), "not available") {
			return false, "access_denied", msg
		}
		return false, "auth_error", msg
	case 404:
		return false, "not_found", "Model not found"
	case 429:
		return true, "", ""
	default:
		return false, "error", fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
}

// extractErrorMessage extracts error message from various response formats
func extractErrorMessage(respBody map[string]interface{}) string {
	// OpenAI format: {"error": {"message": "..."}}
	if errObj, ok := respBody["error"].(map[string]interface{}); ok {
		if msg, ok := errObj["message"].(string); ok {
			return msg
		}
	}
	// Direct message: {"message": "..."}
	if msg, ok := respBody["message"].(string); ok {
		return msg
	}
	return fmt.Sprintf("%v", respBody)
}

// parseBedrockCredentials parses "AKID:SECRET" or "AKID:SECRET:SESSION"
func parseBedrockCredentials(credentials string) (accessKey, secretKey, sessionToken string, err error) {
	parts := strings.SplitN(credentials, ":", 3)
	if len(parts) < 2 {
		return "", "", "", fmt.Errorf("invalid credentials format")
	}
	accessKey = strings.TrimSpace(parts[0])
	secretKey = strings.TrimSpace(parts[1])
	if len(parts) == 3 {
		sessionToken = strings.TrimSpace(parts[2])
	}
	return
}

// extractRegionFromProvider extracts region from provider name
func extractRegionFromProvider(provider string) string {
	if strings.HasPrefix(provider, "amazon-mantle-") {
		return strings.TrimPrefix(provider, "amazon-mantle-")
	}
	if strings.HasPrefix(provider, "amazon-") {
		return strings.TrimPrefix(provider, "amazon-")
	}
	return "us-east-1"
}

// buildTestURL builds the test URL for a provider
func (v *ModelValidator) buildTestURL(provider string, baseURL *string, model, key string) string {
	base := "https://api.openai.com/v1"
	if baseURL != nil && *baseURL != "" {
		base = *baseURL
	}
	base = strings.TrimSuffix(base, "/")

	switch provider {
	case "openai", "openrouter":
		return fmt.Sprintf("%s/chat/completions", base)
	case "anthropic":
		return fmt.Sprintf("%s/v1/messages", base)
	case "google":
		// Google Gemini requires the API key as a query parameter.
		return fmt.Sprintf("%s/models/%s:generateContent?key=%s", base, model, url.QueryEscape(key))
	case "deepseek":
		return fmt.Sprintf("%s/chat/completions", base)
	case "moonshot":
		return fmt.Sprintf("%s/v1/chat/completions", base)
	case "zhipu":
		return fmt.Sprintf("%s/v4/chat/completions", base)
	default:
		// Generic OpenAI-compatible endpoint
		// If base already ends with /v1, /v2 etc, just append /chat/completions
		if strings.HasSuffix(base, "/v1") || strings.HasSuffix(base, "/v2") {
			return fmt.Sprintf("%s/chat/completions", base)
		}
		return fmt.Sprintf("%s/v1/chat/completions", base)
	}
}

// setHeaders sets the appropriate headers for a provider
func (v *ModelValidator) setHeaders(req *http.Request, provider, key string) {
	switch provider {
	case "anthropic":
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2025-01-01")
		req.Header.Set("Content-Type", "application/json")
	case "google":
		req.Header.Set("Content-Type", "application/json")
		// Google uses key in URL, not header
	default:
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
	}
}

// BatchValidate validates multiple models in parallel
func (v *ModelValidator) BatchValidate(ctx context.Context, models []struct {
	Provider         string
	ProductID        string
	APIModelName     string
	ModelType        string
	SupportsThinking bool
}) ([]ValidationResult, error) {
	results := make([]ValidationResult, len(models))
	var wg sync.WaitGroup
	var mu sync.Mutex

	for i, m := range models {
		wg.Add(1)
		go func(idx int, provider, productID, apiModelName, modelType string, supportsThinking bool) {
			defer wg.Done()
			result, err := v.ValidateModel(ctx, provider, productID, apiModelName, modelType, supportsThinking)
			if err != nil {
				result = &ValidationResult{
					Provider:     provider,
					ProductID:    productID,
					APIModelName: apiModelName,
					ErrorType:    "validation_error",
					ErrorMessage: err.Error(),
					CheckedAt:    time.Now(),
				}
			}
			mu.Lock()
			results[idx] = *result
			mu.Unlock()
		}(i, m.Provider, m.ProductID, m.APIModelName, m.ModelType, m.SupportsThinking)
	}

	wg.Wait()
	return results, nil
}

// UpdateModelValidation updates the model's validation status in the database
func (v *ModelValidator) UpdateModelValidation(ctx context.Context, modelID string, result *ValidationResult) error {
	resultJSON, _ := json.Marshal(result)

	_, err := v.db.Pool.Exec(ctx, `
		UPDATE service_models
		SET validation_status = $1,
		    validation_result = $2,
		    validated_at = $3,
		    suggested_api_name = $4,
		    avg_latency_ms = $5,
		    updated_at = NOW()
		WHERE id = $6::uuid
	`, mapResultToStatus(result), resultJSON, result.CheckedAt, result.SuggestedName, result.LatencyMs, modelID)

	return err
}

// mapResultToStatus maps validation result to database status
func mapResultToStatus(result *ValidationResult) string {
	if result.Skipped {
		return "skipped"
	}
	if !result.Available {
		return "invalid"
	}
	// Bedrock routes upstream by product_id, so when the product_id itself
	// tested successfully the model is fully usable even if the friendly
	// API name differs — no admin review needed.
	if IsBedrockRuntimeProvider(result.Provider) && result.SuggestedName == result.ProductID {
		return "valid"
	}
	if result.SuggestedName != "" && result.SuggestedName != result.APIModelName {
		return "needs_review" // Found a better name
	}
	return "valid"
}

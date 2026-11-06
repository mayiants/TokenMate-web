package aws

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// BedrockModel represents a discovered model from AWS Bedrock APIs.
type BedrockModel struct {
	ModelID            string   `json:"model_id"`
	ModelName          string   `json:"model_name"`
	ProviderName       string   `json:"provider_name"`
	IsInferenceProfile bool     `json:"is_inference_profile"`
	InputModalities    []string `json:"input_modalities"`
	OutputModalities   []string `json:"output_modalities"`
	StreamingSupported bool     `json:"streaming_supported"`
}

// BedrockRuntimeVendorPrefixes are vendors whose models can be invoked
// directly via Bedrock Runtime (Converse API). These are first-party or
// deeply integrated providers on the Bedrock platform.
var BedrockRuntimeVendorPrefixes = []string{
	"anthropic",
	"amazon",
	"meta",
	"mistral",
	"cohere",
	"stability",
	"ai21",
}

// BedrockRuntimeRegionPrefixes are inference profile region prefixes
// for Bedrock Runtime models.
var BedrockRuntimeRegionPrefixes = []string{
	"us.",
	"eu.",
	"ap.",
	"global.",
}

// ValidateBedrockKey validates an AWS Bedrock key by calling ListFoundationModels.
// Returns ("valid", modelIDs, nil) on success.
// The region is extracted from the provider name (e.g. "amazon-us-east-1" → "us-east-1").
func ValidateBedrockKey(provider, credentials string) (status string, models []string, err error) {
	status, detailed, err := ValidateBedrockKeyWithCapabilities(provider, credentials)
	if err != nil {
		return status, nil, err
	}
	modelIDs := make([]string, 0, len(detailed))
	for _, m := range detailed {
		modelIDs = append(modelIDs, m.ModelID)
	}
	return status, modelIDs, nil
}

// ValidateBedrockKeyWithCapabilities validates an AWS Bedrock key and returns
// detailed model records including modalities discovered from
// ListFoundationModels / ListInferenceProfiles.
func ValidateBedrockKeyWithCapabilities(provider, credentials string) (status string, models []BedrockModel, err error) {
	// Parse credentials: "AKID:SECRET" or "AKID:SECRET:SESSION"
	accessKey, secretKey, sessionToken, err := parseAWSCredentials(credentials)
	if err != nil {
		return "invalid", nil, fmt.Errorf("invalid credentials format: %w", err)
	}

	region := ExtractRegionFromProvider(provider)
	if region == "" {
		return "error", nil, fmt.Errorf("cannot extract region from provider: %s", provider)
	}

	// 1. List Foundation Models
	fmModels, err := ListFoundationModels(accessKey, secretKey, sessionToken, region)
	if err != nil {
		return "invalid", nil, fmt.Errorf("ListFoundationModels failed: %w", err)
	}

	// Build lookup by model ID so inference profiles can inherit modalities.
	fmMap := make(map[string]BedrockModel, len(fmModels))
	for _, m := range fmModels {
		fmMap[m.ModelID] = m
	}

	// 2. List Inference Profiles
	ipModels, err := ListInferenceProfiles(accessKey, secretKey, sessionToken, region, fmMap)
	if err != nil {
		// Non-fatal: some accounts may not have ListInferenceProfiles permission
		ipModels = nil
	}

	// 3. Merge and deduplicate
	seen := make(map[string]bool)
	var allModels []BedrockModel

	for _, m := range fmModels {
		if !seen[m.ModelID] {
			seen[m.ModelID] = true
			allModels = append(allModels, m)
		}
	}
	for _, m := range ipModels {
		if !seen[m.ModelID] {
			seen[m.ModelID] = true
			allModels = append(allModels, m)
		}
	}

	// 4. Filter: only keep Bedrock Runtime native models
	filtered := FilterBedrockRuntimeModels(allModels)

	return "valid", filtered, nil
}

// ListFoundationModels calls the Bedrock ListFoundationModels API.
func ListFoundationModels(accessKey, secretKey, sessionToken, region string) ([]BedrockModel, error) {
	endpoint := fmt.Sprintf("https://bedrock.%s.amazonaws.com/foundation-models", region)

	req, err := http.NewRequest("GET", endpoint, bytes.NewReader([]byte{}))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	SignRequest(req, []byte{}, accessKey, secretKey, sessionToken, region, "bedrock")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		bodyStr := string(body)
		if len(bodyStr) > 300 {
			bodyStr = bodyStr[:300]
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, bodyStr)
	}

	var result struct {
		ModelSummaries []struct {
			ModelID                    string   `json:"modelId"`
			ModelName                  string   `json:"modelName"`
			ProviderName               string   `json:"providerName"`
			InputModalities            []string `json:"inputModalities"`
			OutputModalities           []string `json:"outputModalities"`
			ResponseStreamingSupported bool     `json:"responseStreamingSupported"`
		} `json:"modelSummaries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	var models []BedrockModel
	for _, m := range result.ModelSummaries {
		if m.ModelID != "" {
			models = append(models, BedrockModel{
				ModelID:            m.ModelID,
				ModelName:          m.ModelName,
				ProviderName:       m.ProviderName,
				InputModalities:    m.InputModalities,
				OutputModalities:   m.OutputModalities,
				StreamingSupported: m.ResponseStreamingSupported,
			})
		}
	}
	return models, nil
}

// ListInferenceProfiles calls the Bedrock ListInferenceProfiles API.
// fmMap is the foundation-model lookup used to inherit modalities for inference profiles.
func ListInferenceProfiles(accessKey, secretKey, sessionToken, region string, fmMap map[string]BedrockModel) ([]BedrockModel, error) {
	endpoint := fmt.Sprintf("https://bedrock.%s.amazonaws.com/inference-profiles", region)

	req, err := http.NewRequest("GET", endpoint, bytes.NewReader([]byte{}))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	SignRequest(req, []byte{}, accessKey, secretKey, sessionToken, region, "bedrock")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		bodyStr := string(body)
		if len(bodyStr) > 300 {
			bodyStr = bodyStr[:300]
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, bodyStr)
	}

	var result struct {
		InferenceProfileSummaries []struct {
			InferenceProfileID   string   `json:"inferenceProfileId"`
			InferenceProfileName string   `json:"inferenceProfileName"`
			Status               string   `json:"status"`
			Models               []string `json:"models"`
		} `json:"inferenceProfileSummaries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	var models []BedrockModel
	for _, p := range result.InferenceProfileSummaries {
		if p.InferenceProfileID != "" && p.Status == "ACTIVE" {
			providerName := extractProviderFromProfileID(p.InferenceProfileID)
			m := BedrockModel{
				ModelID:            p.InferenceProfileID,
				ModelName:          p.InferenceProfileName,
				ProviderName:       providerName,
				IsInferenceProfile: true,
			}
			// Inherit modalities from the underlying foundation model(s).
			for _, modelRef := range p.Models {
				baseID := extractBedrockModelIDFromARN(modelRef)
				if baseID == "" {
					baseID = modelRef
				}
				if fm, ok := fmMap[baseID]; ok {
					if len(m.InputModalities) == 0 && len(fm.InputModalities) > 0 {
						m.InputModalities = fm.InputModalities
					}
					if len(m.OutputModalities) == 0 && len(fm.OutputModalities) > 0 {
						m.OutputModalities = fm.OutputModalities
					}
					if !m.StreamingSupported && fm.StreamingSupported {
						m.StreamingSupported = fm.StreamingSupported
					}
				}
			}
			models = append(models, m)
		}
	}
	return models, nil
}

// FilterBedrockRuntimeModels filters a model list to only include models
// that belong to Bedrock Runtime (can be invoked via Converse API).
// Mantle/marketplace models (e.g. openai.*, deepseek.*, qwen.*) are excluded.
func FilterBedrockRuntimeModels(models []BedrockModel) []BedrockModel {
	var result []BedrockModel
	for _, m := range models {
		if IsBedrockRuntimeModel(m.ModelID) {
			result = append(result, m)
		}
	}
	return result
}

// IsBedrockRuntimeModel returns true if the model ID belongs to Bedrock Runtime
// (can be invoked via the Converse API) rather than Bedrock Mantle.
func IsBedrockRuntimeModel(modelID string) bool {
	lower := strings.ToLower(modelID)

	// Check region prefixes first (inference profiles like us.anthropic.*, global.anthropic.*)
	for _, prefix := range BedrockRuntimeRegionPrefixes {
		if strings.HasPrefix(lower, prefix) {
			// Extract the vendor after the region prefix
			rest := lower[len(prefix):]
			for _, vendor := range BedrockRuntimeVendorPrefixes {
				if strings.HasPrefix(rest, vendor+".") {
					return true
				}
			}
			// Has a region prefix but unknown vendor → not Bedrock Runtime
			return false
		}
	}

	// Check direct vendor prefixes (e.g. anthropic.claude-*, amazon.nova-*)
	for _, vendor := range BedrockRuntimeVendorPrefixes {
		if strings.HasPrefix(lower, vendor+".") {
			return true
		}
	}

	// No recognized prefix → not a Bedrock Runtime model
	return false
}

// ─── Helpers ───────────────────────────────────────────────────────────────────

// ExtractRegionFromProvider extracts AWS region from provider name.
// e.g. "amazon-us-east-1" → "us-east-1", "amazon-mantle-us-east-1" → "us-east-1"
func ExtractRegionFromProvider(provider string) string {
	if strings.HasPrefix(provider, "amazon-mantle-") {
		return strings.TrimPrefix(provider, "amazon-mantle-")
	}
	if strings.HasPrefix(provider, "amazon-") {
		return strings.TrimPrefix(provider, "amazon-")
	}
	if provider == "amazon" {
		return "us-east-1" // default
	}
	return ""
}

// extractBedrockModelIDFromARN extracts the foundation model ID from an ARN-like
// reference returned by ListInferenceProfiles. Examples:
//
//	"arn:aws:bedrock:us-east-1::foundation-model/anthropic.claude-opus-4-6-v1:0"
//	"arn:aws:bedrock:us-east-1:123456789012:application-profile/abc..."
//
// Returns empty string if the input does not look like a foundation-model ARN.
func extractBedrockModelIDFromARN(ref string) string {
	ref = strings.TrimSpace(ref)
	if idx := strings.Index(ref, ":foundation-model/"); idx >= 0 {
		return ref[idx+len(":foundation-model/"):]
	}
	if idx := strings.Index(ref, "/foundation-model/"); idx >= 0 {
		return ref[idx+len("/foundation-model/"):]
	}
	return ""
}

// parseAWSCredentials parses "AKID:SECRET" or "AKID:SECRET:SESSION" format.
func parseAWSCredentials(credentials string) (accessKey, secretKey, sessionToken string, err error) {
	parts := strings.SplitN(credentials, ":", 3)
	if len(parts) < 2 {
		return "", "", "", fmt.Errorf("expected 'ACCESS_KEY:SECRET_KEY[:SESSION_TOKEN]'")
	}
	accessKey = strings.TrimSpace(parts[0])
	secretKey = strings.TrimSpace(parts[1])
	if len(parts) == 3 {
		sessionToken = strings.TrimSpace(parts[2])
	}
	if accessKey == "" || secretKey == "" {
		return "", "", "", fmt.Errorf("access key and secret key cannot be empty")
	}
	return accessKey, secretKey, sessionToken, nil
}

// extractProviderFromProfileID derives the vendor name from an inference profile ID.
// e.g. "us.anthropic.claude-sonnet-4-6" → "anthropic"
func extractProviderFromProfileID(profileID string) string {
	parts := strings.Split(profileID, ".")
	if len(parts) >= 2 {
		// Profile IDs: "{region}.{provider}.{model}" or "{provider}.{model}"
		candidate := parts[0]
		// If first part is a region prefix, use second part
		for _, prefix := range BedrockRuntimeRegionPrefixes {
			if strings.TrimSuffix(prefix, ".") == candidate {
				if len(parts) >= 3 {
					return parts[1]
				}
				return ""
			}
		}
		return candidate
	}
	return ""
}

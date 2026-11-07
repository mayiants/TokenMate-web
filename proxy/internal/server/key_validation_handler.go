package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mayiants/ai-proxy-go/pkg/relay"
	"github.com/mayiants/ai-proxy-go/pkg/relay/channel/aws"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ============================================================================
// Model Discovery Cache
// ============================================================================

type discoveryCacheEntry struct {
	models    []bedrockModelInfo
	expiresAt time.Time
}

type bedrockModelInfo struct {
	ModelID            string   `json:"model_id"`
	ModelName          string   `json:"model_name"`
	ProviderName       string   `json:"provider_name"`
	InputModalities    []string `json:"input_modalities"`
	OutputModalities   []string `json:"output_modalities"`
	StreamingSupported bool     `json:"streaming_supported"`
	IsInferenceProfile bool     `json:"is_inference_profile"`
}

var (
	discoveryCache   = make(map[string]*discoveryCacheEntry) // key: "region:providerFilter"
	discoveryCacheMu sync.RWMutex
	discoveryTTL     = 1 * time.Hour
)

// SetDiscoveryTTL allows overriding the cache TTL (0 = disable cache).
func SetDiscoveryTTL(ttl time.Duration) { discoveryTTL = ttl }

// ============================================================================
// Handler
// ============================================================================

// handleValidateUpstreamKey validates an upstream key and returns the list of supported models.
// POST /api/internal/validate-upstream-key
//
//	Body: {
//	  "provider": "amazon",
//	  "key": "AKID:SECRET",
//	  "provider_filter": "anthropic,amazon"   // optional: comma-separated provider names
//	  "force_refresh": false                   // optional: bypass cache
//	}
func (s *HandlerService) handleValidateUpstreamKey(c *gin.Context) {
	var req struct {
		Provider       string `json:"provider" binding:"required"`
		Key            string `json:"key" binding:"required"`
		ProviderFilter string `json:"provider_filter"`
		ForceRefresh   bool   `json:"force_refresh"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status": "error",
			"error":  "Invalid request: " + err.Error(),
		})
		return
	}

	provider := req.Provider
	apiKey := req.Key

	if req.ForceRefresh {
		invalidateDiscoveryCache(provider, "")
	}

	var status string
	var models []bedrockModelInfo
	var modelIDs []string
	var err error

	switch {
	case provider == "amazon" || (strings.HasPrefix(provider, "amazon-") && !strings.HasPrefix(provider, "amazon-mantle-")):
		status, models, err = s.validateAWSBedrockKey(provider, apiKey, req.ProviderFilter)
	case strings.HasPrefix(provider, "amazon-mantle-"):
		status, modelIDs, err = validateMantleKey(provider, apiKey)
		models = make([]bedrockModelInfo, len(modelIDs))
		for i, id := range modelIDs {
			models[i] = bedrockModelInfo{ModelID: id, ModelName: id, ProviderName: provider}
		}
	default:
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "Provider type not supported by Proxy"})
		return
	}

	if err != nil {
		s.handlerDeps.Logger.Warn("Upstream key validation failed via Proxy",
			zap.String("provider", provider),
			zap.String("error", err.Error()),
		)
		c.JSON(http.StatusOK, gin.H{"status": status, "models": models, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": status, "models": models})
}

// ============================================================================
// AWS Bedrock Key Validation
// ============================================================================

func (s *HandlerService) validateAWSBedrockKey(provider, apiKey, providerFilter string) (string, []bedrockModelInfo, error) {
	cfg, ok := relay.GetProviderConfig(provider)
	if !ok {
		cfg, ok = relay.GetProviderConfig("amazon")
		if !ok {
			return "error", nil, fmt.Errorf("provider config not found: %s", provider)
		}
	}

	region := extractRegionFromBaseURL(cfg.BaseURL)
	if region == "" {
		region = "us-east-1"
	}

	// Bedrock API Key (ABSK...): a single opaque bearer token, not "AK:SK".
	// Validate it against the runtime control plane with a Bearer header — the
	// same key can then serve the runtime track (amazon-{region}) via
	// CallWithBearerToken as well as the Mantle track.
	if aws.IsBedrockBearerKey(apiKey) {
		models, err := listAWSFoundationModelsBearer(apiKey, region)
		if err != nil {
			return "invalid", nil, err
		}
		filter := parseProviderFilter(providerFilter)
		if len(filter) > 0 {
			filtered := models[:0]
			for _, m := range models {
				if passesFilter(m.ProviderName, filter) {
					filtered = append(filtered, m)
				}
			}
			models = filtered
		}
		return "valid", models, nil
	}

	parts := strings.SplitN(apiKey, ":", 2)
	if len(parts) != 2 {
		return "invalid", nil, fmt.Errorf("AWS key format: use ACCESS_KEY_ID:SECRET_ACCESS_KEY or a Bedrock API key (ABSK...)")
	}
	accessKey, secretKey := parts[0], parts[1]

	// Parse provider filter
	filter := parseProviderFilter(providerFilter)

	models, err := s.discoverAWSModels(accessKey, secretKey, region, filter)
	if err != nil {
		return "invalid", nil, err
	}

	return "valid", models, nil
}

func validateMantleKey(provider, apiKey string) (string, []string, error) {
	cfg, ok := relay.GetProviderConfig(provider)
	if !ok {
		return "error", nil, fmt.Errorf("provider config not found: %s", provider)
	}

	baseURL := strings.TrimSuffix(cfg.BaseURL, "/")
	modelsURL := baseURL + "/v1/models"

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", modelsURL, nil)
	if err != nil {
		return "error", nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return "error", nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "invalid", nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "error", nil, err
	}

	var models []string
	for _, m := range result.Data {
		if m.ID != "" {
			models = append(models, m.ID)
		}
	}
	return "valid", models, nil
}

// ============================================================================
// Model Discovery (ListFoundationModels + ListInferenceProfiles + Caching)
// ============================================================================

// discoverAWSModels returns merged & deduplicated Bedrock models, with caching.
func (s *HandlerService) discoverAWSModels(accessKey, secretKey, region string, providerFilter map[string]bool) ([]bedrockModelInfo, error) {
	filterKey := ""
	if len(providerFilter) > 0 {
		keys := make([]string, 0, len(providerFilter))
		for k := range providerFilter {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		filterKey = strings.Join(keys, ",")
	}
	// Include a hash of the accessKey so different credentials never share a
	// cached model-discovery entry (different keys may see different models).
	akHash := sha256.Sum256([]byte(accessKey))
	cacheKey := region + ":" + filterKey + ":" + hex.EncodeToString(akHash[:])[:16]

	// Check cache
	if discoveryTTL > 0 {
		discoveryCacheMu.RLock()
		entry, ok := discoveryCache[cacheKey]
		discoveryCacheMu.RUnlock()
		if ok && time.Now().Before(entry.expiresAt) {
			s.handlerDeps.Logger.Debug("Discovery cache hit", zap.String("region", region), zap.Int("models", len(entry.models)))
			return entry.models, nil
		}
	}

	s.handlerDeps.Logger.Info("Discovering AWS Bedrock models", zap.String("region", region))

	var allModels []bedrockModelInfo
	seen := make(map[string]bool)

	// 1. ListFoundationModels
	fmModels, err := listAWSFoundationModels(accessKey, secretKey, region)
	if err != nil {
		s.handlerDeps.Logger.Warn("ListFoundationModels failed", zap.Error(err))
	} else {
		for _, m := range fmModels {
			if !passesFilter(m.ProviderName, providerFilter) {
				continue
			}
			if seen[m.ModelID] {
				continue
			}
			seen[m.ModelID] = true
			allModels = append(allModels, m)
		}
	}

	// 2. ListInferenceProfiles
	ipModels, err := listAWSInferenceProfiles(accessKey, secretKey, region)
	if err != nil {
		s.handlerDeps.Logger.Warn("ListInferenceProfiles failed (may lack permission)", zap.Error(err))
	} else {
		for _, m := range ipModels {
			if !passesFilter(m.ProviderName, providerFilter) {
				continue
			}
			if seen[m.ModelID] {
				continue
			}
			seen[m.ModelID] = true
			m.IsInferenceProfile = true
			allModels = append(allModels, m)
		}
	}

	// Sort: foundation models first, then inference profiles
	sort.Slice(allModels, func(i, j int) bool {
		if allModels[i].IsInferenceProfile != allModels[j].IsInferenceProfile {
			return !allModels[i].IsInferenceProfile
		}
		return allModels[i].ModelID < allModels[j].ModelID
	})

	// Store in cache
	if discoveryTTL > 0 {
		discoveryCacheMu.Lock()
		discoveryCache[cacheKey] = &discoveryCacheEntry{
			models:    allModels,
			expiresAt: time.Now().Add(discoveryTTL),
		}
		discoveryCacheMu.Unlock()
	}

	s.handlerDeps.Logger.Info("AWS Bedrock discovery complete",
		zap.Int("foundation_models", len(fmModels)),
		zap.Int("inference_profiles", len(ipModels)),
		zap.Int("total_unique", len(allModels)),
	)

	return allModels, nil
}

func invalidateDiscoveryCache(provider, region string) {
	discoveryCacheMu.Lock()
	defer discoveryCacheMu.Unlock()
	if region != "" {
		delete(discoveryCache, region+":")
		delete(discoveryCache, region+":*")
	}
	// Clear all
	discoveryCache = make(map[string]*discoveryCacheEntry)
}

// ============================================================================
// AWS API Calls
// ============================================================================

// listAWSFoundationModels calls bedrock:ListFoundationModels.
func listAWSFoundationModels(accessKey, secretKey, region string) ([]bedrockModelInfo, error) {
	endpoint := fmt.Sprintf("https://bedrock.%s.amazonaws.com/foundation-models", region)

	req, err := http.NewRequest("GET", endpoint, bytes.NewReader([]byte{}))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	aws.SignRequest(req, []byte{}, accessKey, secretKey, "", region, "bedrock")

	return doListFoundationModels(req)
}

// listAWSFoundationModelsBearer lists foundation models using a Bedrock API
// Key (Bearer) instead of SigV4, so a bearer key registered under the runtime
// provider (amazon-{region}) can be validated. AWS accepts the bearer token on
// the bedrock control-plane endpoint via CallWithBearerToken.
func listAWSFoundationModelsBearer(bearerToken, region string) ([]bedrockModelInfo, error) {
	endpoint := fmt.Sprintf("https://bedrock.%s.amazonaws.com/foundation-models", region)

	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearerToken)

	return doListFoundationModels(req)
}

// doListFoundationModels executes a prepared ListFoundationModels request and
// parses the model summaries. Auth (SigV4 or Bearer) is set by the caller.
func doListFoundationModels(req *http.Request) ([]bedrockModelInfo, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status: %d, body: %s", resp.StatusCode, string(body[:min(len(body), 300)]))
	}

	var result struct {
		ModelSummaries []struct {
			ModelID            string   `json:"modelId"`
			ModelName          string   `json:"modelName"`
			ProviderName       string   `json:"providerName"`
			InputModalities    []string `json:"inputModalities"`
			OutputModalities   []string `json:"outputModalities"`
			StreamingSupported bool     `json:"responseStreamingSupported"`
		} `json:"modelSummaries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	var models []bedrockModelInfo
	for _, m := range result.ModelSummaries {
		if m.ModelID != "" {
			models = append(models, bedrockModelInfo{
				ModelID:            m.ModelID,
				ModelName:          m.ModelName,
				ProviderName:       m.ProviderName,
				InputModalities:    m.InputModalities,
				OutputModalities:   m.OutputModalities,
				StreamingSupported: m.StreamingSupported,
			})
		}
	}
	return models, nil
}

// listAWSInferenceProfiles calls bedrock:ListInferenceProfiles.
func listAWSInferenceProfiles(accessKey, secretKey, region string) ([]bedrockModelInfo, error) {
	endpoint := fmt.Sprintf("https://bedrock.%s.amazonaws.com/inference-profiles", region)

	req, err := http.NewRequest("GET", endpoint, bytes.NewReader([]byte{}))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	aws.SignRequest(req, []byte{}, accessKey, secretKey, "", region, "bedrock")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status: %d, body: %s", resp.StatusCode, string(body[:min(len(body), 300)]))
	}

	var result struct {
		InferenceProfileSummaries []struct {
			InferenceProfileID   string `json:"inferenceProfileId"`
			InferenceProfileName string `json:"inferenceProfileName"`
			ModelSource          struct {
				CopyFrom string `json:"copyFrom"`
			} `json:"modelSource"`
			Status string `json:"status"`
		} `json:"inferenceProfileSummaries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	var models []bedrockModelInfo
	for _, p := range result.InferenceProfileSummaries {
		if p.InferenceProfileID != "" && p.Status == "ACTIVE" {
			// Derive provider name from the profile ID (e.g. "us.anthropic.claude-sonnet-4-6-v1:0" → "anthropic")
			providerName := extractProviderFromProfileID(p.InferenceProfileID)
			models = append(models, bedrockModelInfo{
				ModelID:            p.InferenceProfileID,
				ModelName:          p.InferenceProfileName,
				ProviderName:       providerName,
				IsInferenceProfile: true,
				// Inference profiles inherit capabilities from the base model
			})
		}
	}
	return models, nil
}

// ============================================================================
// Helpers
// ============================================================================

// parseProviderFilter parses a comma-separated provider filter string.
// Returns nil if no filter (pass all), or a set of allowed provider names.
func parseProviderFilter(filter string) map[string]bool {
	if filter == "" {
		return nil
	}
	parts := strings.Split(filter, ",")
	result := make(map[string]bool, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(strings.ToLower(p))
		if p != "" {
			result[p] = true
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// providerAliases maps common provider names to their AWS Bedrock provider names.
var providerAliases = map[string]string{
	"openai":    "openai",
	"alibaba":   "qwen", // Alibaba models are under Qwen on Bedrock
	"qwen":      "qwen",
	"ali":       "qwen",
	"stability": "stability", // matches both "Stability AI" and "stability"
}

// normalizeProviderName normalizes a provider name for filtering.
func normalizeProviderName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	// Map well-known aliases
	if mapped, ok := providerAliases[n]; ok {
		return mapped
	}
	return n
}

// passesFilter returns true if the model's provider passes the filter.
// Uses case-insensitive substring matching for robustness against AWS naming inconsistencies.
func passesFilter(providerName string, filter map[string]bool) bool {
	if len(filter) == 0 {
		return true
	}
	normalized := normalizeProviderName(providerName)
	for k := range filter {
		kn := normalizeProviderName(k)
		// Match if the normalized provider name contains the filter key or vice versa
		if strings.Contains(normalized, kn) || strings.Contains(kn, normalized) {
			return true
		}
	}
	return false
}

// extractProviderFromProfileID derives the Bedrock provider name from an inference profile ID.
// e.g. "us.anthropic.claude-sonnet-4-6-v1:0" → "anthropic"
//
//	"eu.amazon.nova-pro-v1:0" → "amazon"
func extractProviderFromProfileID(profileID string) string {
	parts := strings.Split(profileID, ".")
	if len(parts) >= 2 {
		// Profile IDs are like "{region}.{provider}.{model-name}"
		return parts[1]
	}
	return ""
}

// extractRegionFromBaseURL extracts the AWS region from a Bedrock base URL.
func extractRegionFromBaseURL(baseURL string) string {
	s := strings.TrimPrefix(baseURL, "https://")
	s = strings.TrimPrefix(s, "http://")
	if idx := strings.Index(s, "bedrock-runtime."); idx >= 0 {
		start := idx + len("bedrock-runtime.")
		if end := strings.Index(s[start:], "."); end > 0 {
			return s[start : start+end]
		}
	}
	if idx := strings.Index(s, "bedrock."); idx >= 0 {
		start := idx + len("bedrock.")
		if end := strings.Index(s[start:], "."); end > 0 {
			return s[start : start+end]
		}
	}
	return ""
}

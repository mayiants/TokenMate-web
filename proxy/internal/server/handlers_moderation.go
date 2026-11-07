package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/mayiants/ai-proxy-go/dto"
	"github.com/mayiants/ai-proxy-go/pkg/keypool"
	"github.com/mayiants/ai-proxy-go/pkg/relay"
	"github.com/mayiants/ai-proxy-go/pkg/relay/channel/aws"
	"github.com/mayiants/ai-proxy-go/pkg/token"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// handleModerations - Handle content moderation requests (OpenAI /v1/moderations
// compatible). Request/response shapes mirror the OpenAI Moderation API: the
// body carries {model, input} and the upstream returns {results: [...], usage}.
// Upstream models that omit usage in the response fall back to a local token
// estimate of the input so billing still has a token count.
func (s *HandlerService) handleModerations(c *gin.Context) {
	startTime := time.Now()

	// Read and preserve request body for retention and affinity cache fingerprinting
	const maxBodySize = 10 * 1024 * 1024 // 10 MB
	bodyBytes, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBodySize))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "Failed to read request body",
			"type":    "invalid_request_error",
		}})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	c.Set("request_body", bodyBytes)

	var req struct {
		Model string      `json:"model"`
		Input interface{} `json:"input"`
		User  string      `json:"user,omitempty"`
	}

	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "Invalid request: " + err.Error(),
			"type":    "invalid_request_error",
		}})
		return
	}

	keyHash := c.GetString("key_hash")

	// Enforce allowed_models restriction
	if !checkAllowedModels(c, req.Model) {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{
			"message": fmt.Sprintf("Model '%s' is not in your allowed models list.", req.Model),
			"type":    "model_not_allowed",
			"code":    "MODEL_NOT_ALLOWED",
		}})
		return
	}

	// Dual-track routing (same as embeddings — bedrock/mantle first, standard fallback)
	track := resolveTrack(c)
	c.Set("routing_track", track)

	var provider string
	var upstreamKey *keypool.KeyInternal
	var upstreamModelName string
	var isFallback bool
	var modErr error

	switch track {
	case "bedrock":
		provider, upstreamKey, upstreamModelName, isFallback, modErr = s.resolveBedrockProviderAndKey(c.Request.Context(), req.Model, "")
		if modErr != nil {
			provider, upstreamKey, upstreamModelName, isFallback, modErr = s.resolveProviderAndKey(c.Request.Context(), req.Model, "normal", "")
			c.Set("original_track", "bedrock")
			c.Set("routing_track", "standard")
		}
	case "mantle":
		provider, upstreamKey, upstreamModelName, isFallback, modErr = s.resolveMantleProviderAndKey(c.Request.Context(), req.Model, "")
		if modErr != nil {
			provider, upstreamKey, upstreamModelName, isFallback, modErr = s.resolveProviderAndKey(c.Request.Context(), req.Model, "normal", "")
			c.Set("original_track", "mantle")
			c.Set("routing_track", "standard")
		}
	default:
		provider, upstreamKey, upstreamModelName, isFallback, modErr = s.resolveProviderAndKey(c.Request.Context(), req.Model, "normal", "")
	}
	if modErr != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
			"message": modErr.Error(),
			"type":    "service_error",
		}})
		return
	}
	c.Set("is_fallback", isFallback)
	if isFallback {
		c.Set("actual_group_id", c.GetString("group_id"))
	}

	// Preserve logical model for reporting before switching to provider-specific name.
	logicalModel := req.Model

	// Set context values for metrics middleware
	c.Set("provider", provider)
	c.Set("model", logicalModel)
	c.Set("is_stream", false)

	// Replace model name with provider-specific name for upstream request
	req.Model = upstreamModelName

	upstreamBaseURL := s.resolveBaseURL(upstreamKey, provider)

	// Moderation is an OpenAI-compatible endpoint; forward as-is with Bearer
	// auth (same dispatch path as non-Bedrock embeddings).
	var body []byte
	statusCode := http.StatusOK
	if _, isAWS := relay.GetAdaptor(provider, &relay.AdaptorConfig{
		BaseURL: upstreamBaseURL,
		APIKey:  upstreamKey.ActualKey,
	}).(*aws.AWSAdaptor); isAWS {
		// Bedrock has no OpenAI-compatible /moderations route — reject
		// client-side instead of attempting an unsupported conversion.
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "Moderation is not supported on Bedrock providers",
			"type":    "invalid_request_error",
		}})
		return
	}

	var httpReq *http.Request
	httpReq, err = http.NewRequest("POST", upstreamBaseURL+"/moderations", c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": "Failed to create upstream request",
			"type":    "upstream_error",
		}})
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+upstreamKey.ActualKey)
	s.applyForwardedHeaders(httpReq, c)

	var resp *http.Response
	resp, err = sharedHandlerClient.Do(httpReq)
	if err == nil {
		defer func() { _ = resp.Body.Close() }()
		statusCode = resp.StatusCode
		body, err = io.ReadAll(resp.Body)
	}
	if err != nil {
		// Record upstream usage (error)
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, req.Model, nil, time.Since(startTime).Milliseconds(), true)
		// Record provider health (transport error)
		s.recordProviderHealthAsync(c, provider, logicalModel, statusCode, time.Since(startTime).Milliseconds(), err, err.Error())

		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": "Upstream request failed",
			"type":    "upstream_error",
		}})
		return
	}

	// Parse response for usage (success responses only — upstream error
	// bodies are handled by the error branch below).
	var modResp struct {
		Usage dto.Usage `json:"usage"`
	}
	if statusCode < 400 {
		if err := json.Unmarshal(body, &modResp); err != nil {
			s.handlerDeps.Logger.Warn("Failed to parse moderation usage", zap.Error(err), zap.String("body", string(body[:min(500, len(body))])))
			providerBreakers.RecordFailure(provider)
			s.recordProviderHealthAsync(c, provider, logicalModel, statusCode, time.Since(startTime).Milliseconds(), nil, "parse_error")
			c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
				"message": "Failed to parse upstream response",
				"type":    "upstream_error",
			}})
			return
		}
		// Some moderation endpoints omit usage — estimate from the input so
		// per-token billing still receives a token count.
		if modResp.Usage.PromptTokens == 0 {
			modResp.Usage.PromptTokens = estimateModerationInputTokens(req.Input, logicalModel)
			modResp.Usage.TotalTokens = modResp.Usage.PromptTokens
		}
	}

	// Check for upstream errors (4xx/5xx) before recording success metrics or billing.
	if statusCode >= 400 {
		providerBreakers.RecordFailure(provider)
		if s.keyPoolManager != nil {
			errorMsg := string(body[:min(500, len(body))])
			s.keyPoolManager.ReportKeyFailure(provider, upstreamKey.ID, req.Model, statusCode, "upstream_error", "upstream_error", errorMsg)
			if (statusCode == 401 || statusCode == 403) ||
				(statusCode == 400 && !keypool.IsModelLevelError(errorMsg)) {
				s.keyPoolManager.ReportModelFailure(upstreamKey.ID, req.Model, errorMsg)
			}
		}
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, req.Model, nil, time.Since(startTime).Milliseconds(), true)
		s.recordProviderHealthAsync(c, provider, logicalModel, statusCode, time.Since(startTime).Milliseconds(), nil, string(body[:min(200, len(body))]))

		upErr := ClassifyUpstreamError(statusCode)
		c.Data(upErr.ClientStatus, "application/json", upErr.JSON())
		return
	}

	// Successful upstream dispatch.
	if s.keyPoolManager != nil {
		s.keyPoolManager.RecordUpstreamRequest(provider, upstreamKey.ID, req.Model)
	}

	// Record upstream usage for provider reconciliation
	s.recordUpstreamUsageAsync(upstreamKey.ID, provider, req.Model, &modResp.Usage, time.Since(startTime).Milliseconds(), false)
	// Record provider health (success)
	s.recordProviderHealthAsync(c, provider, logicalModel, statusCode, time.Since(startTime).Milliseconds(), nil, "")
	providerBreakers.RecordSuccess(provider)

	// Set tokens_consumed for TPD post-check and metrics middleware
	totalTokens := int64(modResp.Usage.PromptTokens)
	c.Set("tokens_consumed", totalTokens)
	if s.keyPoolManager != nil && totalTokens > 0 {
		s.keyPoolManager.RecordUpstreamTokens(provider, upstreamKey.ID, req.Model, totalTokens, 0)
	}
	if upstreamKey.QuotaLimit > 0 && totalTokens > 0 {
		upstreamKey.AddQuotaUsed(totalTokens)
	}

	// Record token usage for billing with group pricing. Use the logical
	// (client-facing) model name — req.Model holds the upstream model ID.
	s.recordUsageAsyncFromContext(c, keyHash, upstreamKey.ID, logicalModel, provider, &modResp.Usage, time.Since(startTime).Milliseconds(), "")

	// Capture retention for moderation request
	var requestBody []byte
	if v, ok := c.Get("request_body"); ok {
		if b, ok := v.([]byte); ok {
			requestBody = b
		}
	}
	setRetentionContext(c, "openai", "openai", req.Model, false)
	s.captureRetention(c, requestBody, body, provider, logicalModel, upstreamKey.ID, &modResp.Usage, time.Since(startTime).Milliseconds(), statusCode, false, "moderations")

	c.Data(statusCode, "application/json", body)
}

// estimateModerationInputTokens counts tokens of the moderation input when the
// upstream response omits usage. Input may be a single string or an array of
// strings (OpenAI accepts both).
func estimateModerationInputTokens(input interface{}, model string) int {
	switch v := input.(type) {
	case string:
		return token.CountTextToken(v, model)
	case []interface{}:
		total := 0
		for _, item := range v {
			if s, ok := item.(string); ok {
				total += token.CountTextToken(s, model)
			}
		}
		return total
	case []string:
		total := 0
		for _, s := range v {
			total += token.CountTextToken(s, model)
		}
		return total
	default:
		return 0
	}
}

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
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// handleEmbeddings - Handle embeddings requests
func (s *HandlerService) handleEmbeddings(c *gin.Context) {
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

	_ = c.GetString(string(keyIDKey))
	_ = c.GetString(string(userIDKey))
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

	// Dual-track routing for embeddings
	track := resolveTrack(c)
	c.Set("routing_track", track)

	var provider string
	var upstreamKey *keypool.KeyInternal
	var upstreamModelName string
	var isFallback bool
	var embErr error

	switch track {
	case "bedrock":
		provider, upstreamKey, upstreamModelName, isFallback, embErr = s.resolveBedrockProviderAndKey(c.Request.Context(), req.Model, "")
		if embErr != nil {
			// Bedrock embeddings not available for this model — fallback to standard
			provider, upstreamKey, upstreamModelName, isFallback, embErr = s.resolveProviderAndKey(c.Request.Context(), req.Model, "normal", "")
			c.Set("original_track", "bedrock")
			c.Set("routing_track", "standard")
		}
	case "mantle":
		provider, upstreamKey, upstreamModelName, isFallback, embErr = s.resolveMantleProviderAndKey(c.Request.Context(), req.Model, "")
		if embErr != nil {
			provider, upstreamKey, upstreamModelName, isFallback, embErr = s.resolveProviderAndKey(c.Request.Context(), req.Model, "normal", "")
			c.Set("original_track", "mantle")
			c.Set("routing_track", "standard")
		}
	default:
		provider, upstreamKey, upstreamModelName, isFallback, embErr = s.resolveProviderAndKey(c.Request.Context(), req.Model, "normal", "")
	}
	if embErr != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
			"message": embErr.Error(),
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

	// Dispatch to the upstream. Bedrock providers (amazon / amazon-*) use
	// SigV4-signed InvokeModel with format conversion instead of the
	// OpenAI-compatible /embeddings endpoint; everything else (including
	// amazon-mantle-*) is forwarded as-is with Bearer auth.
	var body []byte
	statusCode := http.StatusOK
	if awsAdaptor, ok := relay.GetAdaptor(provider, &relay.AdaptorConfig{
		BaseURL: upstreamBaseURL,
		APIKey:  upstreamKey.ActualKey,
	}).(*aws.AWSAdaptor); ok {
		body, statusCode, err = awsAdaptor.InvokeEmbeddings(c.Request.Context(), upstreamModelName, logicalModel, bodyBytes)
		if err != nil && statusCode == 0 {
			// Request-level problem (unsupported model, bad input) — the
			// client can fix this, so it is a 4xx, not an upstream failure.
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
				"message": err.Error(),
				"type":    "invalid_request_error",
			}})
			return
		}
	} else {
		// Forward request to provider with upstream key
		var httpReq *http.Request
		httpReq, err = http.NewRequest("POST", upstreamBaseURL+"/embeddings", c.Request.Body)
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
	var embedResp struct {
		Usage dto.Usage `json:"usage"`
	}
	if statusCode < 400 {
		if err := json.Unmarshal(body, &embedResp); err != nil {
			s.handlerDeps.Logger.Warn("Failed to parse embeddings usage", zap.Error(err), zap.String("body", string(body[:min(500, len(body))])))
			// Parse failure means we cannot safely extract usage or forward the
			// body to the client; return 502 (mirrors the images handler).
			providerBreakers.RecordFailure(provider)
			s.recordProviderHealthAsync(c, provider, logicalModel, statusCode, time.Since(startTime).Milliseconds(), nil, "parse_error")
			c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
				"message": "Failed to parse upstream response",
				"type":    "upstream_error",
			}})
			return
		}
	}

	// Check for upstream errors (4xx/5xx) before recording success metrics or billing.
	if statusCode >= 400 {
		providerBreakers.RecordFailure(provider)
		if s.keyPoolManager != nil {
			errorMsg := string(body[:min(500, len(body))])
			s.keyPoolManager.ReportKeyFailure(provider, upstreamKey.ID, req.Model, statusCode, "upstream_error", "upstream_error", errorMsg)
			// Model-level failure only for key-specific access issues; general model
			// errors mean the model doesn't exist on the provider (see handlers_chat.go).
			if (statusCode == 401 || statusCode == 403) ||
				(statusCode == 400 && !keypool.IsModelLevelError(errorMsg)) {
				s.keyPoolManager.ReportModelFailure(upstreamKey.ID, req.Model, errorMsg)
			}
		}
		// Record upstream usage (error)
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, req.Model, nil, time.Since(startTime).Milliseconds(), true)
		// Record provider health (upstream error)
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
	s.recordUpstreamUsageAsync(upstreamKey.ID, provider, req.Model, &embedResp.Usage, time.Since(startTime).Milliseconds(), false)
	// Record provider health (success)
	s.recordProviderHealthAsync(c, provider, logicalModel, statusCode, time.Since(startTime).Milliseconds(), nil, "")
	providerBreakers.RecordSuccess(provider)

	// Set tokens_consumed for TPD post-check and metrics middleware
	totalTokens := int64(embedResp.Usage.PromptTokens)
	c.Set("tokens_consumed", totalTokens)
	if s.keyPoolManager != nil && totalTokens > 0 {
		s.keyPoolManager.RecordUpstreamTokens(provider, upstreamKey.ID, req.Model, totalTokens, 0)
	}
	if upstreamKey.QuotaLimit > 0 && totalTokens > 0 {
		upstreamKey.AddQuotaUsed(totalTokens)
	}

	// Record token usage for billing with group pricing. Use the logical
	// (client-facing) model name — req.Model holds the upstream model ID.
	s.recordUsageAsyncFromContext(c, keyHash, upstreamKey.ID, logicalModel, provider, &embedResp.Usage, time.Since(startTime).Milliseconds(), "")

	// Capture retention for embeddings request
	var requestBody []byte
	if v, ok := c.Get("request_body"); ok {
		if b, ok := v.([]byte); ok {
			requestBody = b
		}
	}
	setRetentionContext(c, "openai", "openai", req.Model, false)
	s.captureRetention(c, requestBody, body, provider, logicalModel, upstreamKey.ID, &embedResp.Usage, time.Since(startTime).Milliseconds(), statusCode, false, "embeddings")

	c.Data(statusCode, "application/json", body)
}

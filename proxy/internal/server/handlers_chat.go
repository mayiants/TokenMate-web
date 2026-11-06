package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ai-proxy/ai-proxy-go/dto"
	"github.com/ai-proxy/ai-proxy-go/internal/audit"
	"github.com/ai-proxy/ai-proxy-go/pkg/billing"
	"github.com/ai-proxy/ai-proxy-go/pkg/convert"
	"github.com/ai-proxy/ai-proxy-go/pkg/keypool"
	"github.com/ai-proxy/ai-proxy-go/pkg/metrics"
	"github.com/ai-proxy/ai-proxy-go/pkg/plugin"
	"github.com/ai-proxy/ai-proxy-go/pkg/pool"
	"github.com/ai-proxy/ai-proxy-go/pkg/relay"
	"github.com/ai-proxy/ai-proxy-go/pkg/routing"
	"github.com/ai-proxy/ai-proxy-go/pkg/sse"
	"github.com/ai-proxy/ai-proxy-go/types"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// anthropicNativeProviders lists upstream providers that expose the Anthropic
// Messages API (/v1/messages) natively. For these, an Anthropic-format client
// request is passed through unchanged instead of being converted to the OpenAI
// chat format — this preserves Anthropic-only features, most importantly prompt
// caching (cache_control), which the OpenAI format cannot express.
//
// "anthropic" is the first-party Anthropic API. "openrouter" and "tokenrouter"
// are aggregators that front anthropic/claude-* models via /v1/messages
// (verified to return cache_creation_input_tokens / cache_read_input_tokens).
var anthropicNativeProviders = map[string]bool{
	"anthropic":   true,
	"openrouter":  true,
	"tokenrouter": true,
}

// isAnthropicNativeProvider reports whether the provider speaks the Anthropic
// Messages API natively and should receive Anthropic-format passthrough.
func isAnthropicNativeProvider(provider string) bool {
	return anthropicNativeProviders[provider]
}

// anthropicAuthUsesAPIKey reports whether the provider authenticates with the
// native Anthropic x-api-key header. Aggregators use a standard Bearer token.
func anthropicAuthUsesAPIKey(provider string) bool {
	return provider == "anthropic"
}

// handleChatCompletions - Handle chat completions requests
func (s *HandlerService) handleChatCompletions(c *gin.Context) {
	startTime := time.Now()

	// Read and store request body for affinity cache fingerprinting
	const maxBodySize = 10 * 1024 * 1024 // 10 MB
	bodyBuf := pool.GetBuffer()
	defer pool.PutBuffer(bodyBuf)
	if _, err := io.Copy(bodyBuf, io.LimitReader(c.Request.Body, maxBodySize)); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "Failed to read request body",
			"type":    "invalid_request_error",
		}})
		return
	}
	bodyBytes := bodyBuf.Bytes()
	// bodyBytes aliases the pooled buffer's internal array. Anything retained
	// beyond the handler (captureRetention, c.Set("request_body"), affinity
	// cache) may be read asynchronously after pool.PutBuffer returns the
	// buffer to the pool. Copy now so outgoing references are safe.
	bodyBytes = append([]byte(nil), bodyBytes...)
	c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	// Set request body in context for affinity cache (store as []byte to avoid string copy)
	c.Set("request_body", bodyBytes)

	// Parse request
	req := pool.GetGeneralOpenAIRequest()
	defer pool.PutGeneralOpenAIRequest(req)
	if err := json.Unmarshal(bodyBytes, req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "Invalid request: " + err.Error(),
			"type":    "invalid_request_error",
		}})
		return
	}
	if req.ReasoningEffort != "" {
		c.Set("reasoning_intensity", req.ReasoningEffort)
	}
	if len(req.Tools) > 0 {
		toolNames := make([]string, 0, len(req.Tools))
		for _, t := range req.Tools {
			if t.Function.Name != "" {
				toolNames = append(toolNames, t.Function.Name)
			} else if t.Type != "" {
				toolNames = append(toolNames, t.Type)
			}
		}
		if s := joinAgentToolNames(toolNames); s != "" {
			c.Set("agent_tools", s)
		}
	}

	// Get key info from context (set by auth middleware)
	keyID := c.GetString(string(keyIDKey))
	userID := c.GetString(string(userIDKey))
	keyHash := c.GetString("key_hash")
	accessMode := c.GetString("access_mode")

	// Apply pre-conversion plugins to the OpenAI-format client request body.
	// The affinity cache and retention use request_body, so update it after
	// plugins have cleaned the request.
	bodyBytes = s.pluginChain.Apply(bodyBytes, plugin.Context{
		ClientFormat: "openai",
		HostKey:      keyID,
		ClientName:   userID,
	})
	c.Set("request_body", bodyBytes)

	// Check simulate mode
	if s.handlerDeps.Simulate.Enabled {
		s.handleSimulate(c, *req, keyID, userID, keyHash, startTime)
		return
	}

	// Phase 2: Resolve model based on routing mode
	modelRoutingMode := c.GetString("model_routing_mode")
	if modelRoutingMode == "" {
		modelRoutingMode = "single" // fallback for legacy keys
	}
	var selectedModels []string
	if v, ok := c.Get("selected_models"); ok {
		selectedModels, ok = v.([]string)
		if !ok {
			s.handlerDeps.Logger.Warn("selected_models has unexpected type",
				zap.String("expected", "[]string"),
				zap.Any("actual", v),
			)
			selectedModels = nil
		}
	}
	currentModel := c.GetString("current_model")

	var allowedModels []string
	if v, ok := c.Get("allowed_models"); ok {
		allowedModels, _ = v.([]string)
	}

	resolvedModel, err := s.resolveModel(req.Model, modelRoutingMode, currentModel, selectedModels, allowedModels, keyHash)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": err.Error(),
			"type":    "invalid_request_error",
			"code":    "MODEL_RESOLUTION_ERROR",
		}})
		return
	}
	req.Model = resolvedModel
	normalizeReasoningFields(req)

	// Phase 2: Scene-based auto model selection (when model=auto and mapping configured)
	if req.Model == "auto" && s.sceneMapping != nil && s.sceneMapping.IsConfigured() {
		isAvailable := func(model string) bool {
			if !checkAllowedModelsList(allowedModels, model) {
				return false // must be in group whitelist
			}
			return s.keyPoolManager != nil && s.keyPoolManager.HasActiveKeysForModel(model)
		}
		autoModel, scene := routing.ResolveAutoModel(req.Messages, s.sceneMapping, isAvailable)
		if autoModel == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
				"message": "model=auto: no available model found for your request. Configure scene_routing in group settings.",
				"type":    "invalid_request_error",
				"code":    "AUTO_MODEL_UNAVAILABLE",
			}})
			return
		}
		req.Model = autoModel
		c.Set("auto_scene", string(scene))
		c.Set("auto_resolved_model", autoModel)
	} else if req.Model == "auto" {
		// auto requested but no scene mapping configured
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "model=auto is not available. Your group does not have scene routing configured.",
			"type":    "invalid_request_error",
			"code":    "AUTO_NOT_CONFIGURED",
		}})
		return
	}

	// Phase 4: Budget-based model downgrade
	if s.budgetController != nil && s.budgetController.IsEnabled() {
		if downgraded := s.budgetController.GetDowngradeModel(req.Model); downgraded != req.Model {
			// Verify downgrade target is available
			if s.keyPoolManager != nil && s.keyPoolManager.HasActiveKeysForModel(downgraded) {
				c.Set("budget_downgraded_from", req.Model)
				req.Model = downgraded
			}
			// If downgrade target not available, keep original model
		}
	}

	// Enforce allowed_models restriction (after auto-resolution and budget downgrade)
	if !checkAllowedModels(c, req.Model) {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{
			"message": fmt.Sprintf("Model '%s' is not in your allowed models list. Contact your administrator to add this model.", req.Model),
			"type":    "model_not_allowed",
			"code":    "MODEL_NOT_ALLOWED",
		}})
		return
	}

	// Get user priority from access_mode
	userPriority := getUserPriority(accessMode)

	// Get affinity-preferred key ID
	var preferredKeyID string
	if s.handlerDeps.AffinityCache != nil {
		cachePriority := getUserPriority(accessMode)
		preferredKeyID = s.handlerDeps.AffinityCache.GetPreferredKeyID(c, req.Model, "default", cachePriority)
	}

	// Dual-track routing: Bedrock (VIP) vs Standard
	track := resolveTrack(c)
	c.Set("routing_track", track)

	var provider string
	var upstreamKey *keypool.KeyInternal
	var upstreamModelName string
	var isFallback bool

	switch track {
	case "bedrock":
		provider, upstreamKey, upstreamModelName, _, err = s.resolveBedrockProviderAndKey(c.Request.Context(), req.Model, preferredKeyID)
		if err != nil {
			// Bedrock unavailable for this model — graceful fallback to standard routing
			s.handlerDeps.Logger.Warn("Bedrock routing failed, falling back to standard",
				zap.String("model", req.Model), zap.String("track", track), zap.Error(err))
			provider, upstreamKey, upstreamModelName, _, err = s.resolveProviderAndKey(c.Request.Context(), req.Model, userPriority, preferredKeyID)
			if err != nil {
				// Both Bedrock and standard fallback failed
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
					"message": err.Error(),
					"type":    "service_error",
				}})
				return
			}
			c.Set("is_fallback", true)
			c.Set("original_track", "bedrock")
			c.Set("routing_track", "standard")
			c.Set("actual_group_id", c.GetString("group_id"))
		} else {
			c.Set("is_fallback", false)
		}
	case "mantle":
		// Mantle track: route through amazon-mantle-* providers (Bearer token, OpenAI-compatible API)
		provider, upstreamKey, upstreamModelName, _, err = s.resolveMantleProviderAndKey(c.Request.Context(), req.Model, preferredKeyID)
		if err != nil {
			s.handlerDeps.Logger.Warn("Mantle routing failed, falling back to standard",
				zap.String("model", req.Model), zap.String("track", track), zap.Error(err))
			provider, upstreamKey, upstreamModelName, _, err = s.resolveProviderAndKey(c.Request.Context(), req.Model, userPriority, preferredKeyID)
			if err != nil {
				// Both Mantle and standard fallback failed
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
					"message": err.Error(),
					"type":    "service_error",
				}})
				return
			}
			c.Set("is_fallback", true)
			c.Set("original_track", "mantle")
			c.Set("routing_track", "standard")
			c.Set("actual_group_id", c.GetString("group_id"))
		} else {
			c.Set("is_fallback", false)
		}
	default:
		provider, upstreamKey, upstreamModelName, isFallback, err = s.resolveProviderAndKey(c.Request.Context(), req.Model, userPriority, preferredKeyID)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
				"message": err.Error(),
				"type":    "service_error",
			}})
			return
		}
		c.Set("is_fallback", isFallback)
		if isFallback {
			c.Set("actual_group_id", c.GetString("group_id"))
		}
	}

	// Preserve logical model for reporting before switching to provider-specific name.
	logicalModel := req.Model
	c.Set("logical_model", logicalModel)

	// Media models (video/image/audio generations) hit dedicated endpoints.
	// Clients like Tencent WorkBuddy send image/video tasks to the chat
	// endpoint without switching paths, so instead of rejecting them we
	// translate the request and dispatch it to the matching media handler.
	// (Previously this 400'd with MEDIA_MODEL_WRONG_ENDPOINT.)
	if s.keyPoolManager != nil {
		if mt := s.keyPoolManager.GetModelType(provider, logicalModel); mt == "image" || mt == "video" || mt == "audio" {
			s.rerouteMediaFromChat(c, bodyBytes, logicalModel, mt)
			return
		}
	}
	if s.handlerDeps.Billing != nil {
		if pricing := s.handlerDeps.Billing.GetPricing(logicalModel, provider); pricing != nil && pricing.BillingMode == "media" {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
				"message": fmt.Sprintf("model '%s' is a media generation model; use /v1/video/generations or /v1/images/generations", logicalModel),
				"type":    "invalid_request_error",
				"code":    "MEDIA_MODEL_WRONG_ENDPOINT",
			}})
			return
		}
	}

	// Replace model name with provider-specific name for upstream request
	req.Model = upstreamModelName
	// Use upstream key's base URL if available
	upstreamBaseURL := s.resolveBaseURL(upstreamKey, provider)

	// Debug logging
	s.handlerDeps.Logger.Info("Selected upstream key",
		zap.String("provider", provider),
		zap.String("key_id", upstreamKey.ID),
		zap.String("key_preview", upstreamKey.KeyPreview),
		zap.String("resolved_base_url", upstreamBaseURL),
	)

	// Create adapter with upstream key (not user key)
	adaptorCfg := &relay.AdaptorConfig{
		BaseURL:      upstreamBaseURL,
		APIKey:       upstreamKey.ActualKey, // Use upstream key from pool
		ExtraHeaders: s.filterClientHeaders(c),
	}
	adaptor := relay.GetAdaptor(provider, adaptorCfg)

	// Record format/conversion metadata for retention and observability.
	c.Set("client_format", "openai")
	c.Set("upstream_format", string(adaptor.GetRelayFormat()))
	c.Set("vendor_model_name", req.Model)
	c.Set("is_converted", adaptor.GetRelayFormat() != types.RelayFormatOpenAI)

	// Prompt caching for aggregators fronting Claude models: the Bedrock
	// channel injects cache points itself, but OpenAI-format relays to
	// OpenRouter/TokenRouter need explicit cache_control markers or nothing
	// is ever cached (clients like WorkBuddy don't send them).
	if s.cfg.PromptCache.OpenAIInjectEnabled &&
		maybeInjectAggregatorCacheControl(req, provider, upstreamModelName, adaptor.GetRelayFormat()) {
		c.Set("cache_control_injected", true)
	}

	// Transform request for provider
	providerReq := adaptor.ToProvider(*req)

	// Apply post-conversion plugins to the upstream request body.
	upstreamBody, err := json.Marshal(providerReq)
	if err != nil {
		s.handlerDeps.Logger.Error("Failed to marshal upstream request", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"message": "Failed to marshal upstream request",
			"type":    "internal_error",
		}})
		return
	}
	upstreamBody = s.pluginChain.Apply(upstreamBody, plugin.Context{
		ClientFormat:   "openai",
		UpstreamFormat: string(adaptor.GetRelayFormat()),
		HostKey:        upstreamKey.ID,
		ClientName:     userID,
	})

	// Debug: log request details. Request/response bodies are logged at Debug
	// only because they may contain user prompts, PII, or upstream provider
	// details that must not appear in production default logs.
	s.handlerDeps.Logger.Debug("Upstream request details",
		zap.String("provider", provider),
		zap.String("model", req.Model),
		zap.String("request_url", adaptor.GetRequestURL(req.Model, false)),
		zap.ByteString("request_body", upstreamBody),
	)

	// Estimate input tokens
	var inputTokens int
	if s.handlerDeps.TokenCounter != nil {
		inputTokens = s.handlerDeps.TokenCounter.EstimateMessages(req.Messages, req.Model)
	}

	// Check if streaming
	isStream := req.Stream != nil && *req.Stream

	// Set context values for metrics middleware
	c.Set("provider", provider)
	c.Set("model", logicalModel)
	c.Set("is_stream", isStream)

	if isStream {
		s.handleStreamingResponse(c, adaptor, upstreamBody, *req, logicalModel, provider, keyID, userID, keyHash, upstreamKey, inputTokens, startTime)
		return
	}

	// Pre-consume: deduct estimated input cost before making upstream request.
	// This ensures timeout/error requests are still billed (upstream charges us).
	var preConsumedAmount int64
	var localPreConsumedAmount int64
	localPreConsumedAmount, used, rejected := s.preConsumeLocal(c, userID, c.GetString("key_type"), logicalModel, provider, inputTokens)
	if rejected {
		return
	}
	if !used {
		if s.cfg.Billing.PreConsumeEnabled && s.handlerDeps.Billing != nil && inputTokens > 0 && c.GetString("key_type") != "subscription" {
			traceID := c.GetString("request_id")
			if traceID == "" {
				traceID = fmt.Sprintf("pc-%d", time.Now().UnixNano())
			}
			c.Set("trace_id", traceID) // Store for later settle calls

			pcCtx, pcCancel := billing.NewPreConsumeContext(c.Request.Context())
			pcResult := s.handlerDeps.Billing.PreConsume(pcCtx, billing.PreConsumeRequest{
				UserID:      userID,
				KeyHash:     keyHash,
				Model:       logicalModel,
				Provider:    provider,
				InputTokens: inputTokens,
				RequestID:   traceID,
				Margin:      s.cfg.Billing.PreConsumeMargin,
			})
			pcCancel()

			if pcResult.Rejected {
				metrics.GetMetrics().IncrementPreconsumeRejected()
				c.JSON(http.StatusPaymentRequired, gin.H{"error": gin.H{
					"message": "Insufficient balance for this request",
					"type":    "billing_error",
					"code":    "INSUFFICIENT_BALANCE",
				}})
				return
			}
			preConsumedAmount = pcResult.Amount
		}
	}

	upstreamStart := time.Now()

	// Make request to upstream (queued for non-streaming when enabled).
	statusCode, body, err := s.dispatchUpstream(c, provider, adaptor, upstreamBody, false)

	// In-request failover (non-streaming is fully buffered, so retryable until
	// we write the client response). On a retryable transport error or
	// retryable status, re-dispatch to the next candidate key/provider.
	{
		fo := newFailoverState(s.cfg.Failover, logicalModel)
		foUserPriority := getUserPriority(c.GetString("access_mode"))
		var foPreferredKeyID string
		if s.handlerDeps.AffinityCache != nil {
			foPreferredKeyID = s.handlerDeps.AffinityCache.GetPreferredKeyID(c, logicalModel, "default", foUserPriority)
		}
		for {
			retryStatus := 0
			transportErr := false
			if err != nil {
				transportErr = true
			} else if statusCode >= 400 && statusCode != http.StatusOK {
				retryStatus = statusCode
			} else {
				break // success
			}
			fo.recordAttempt(provider, upstreamKey.ID)
			if !fo.shouldRetry(retryStatus, transportErr, false) {
				break
			}
			if s.keyPoolManager != nil {
				if retryStatus == http.StatusTooManyRequests {
					s.keyPoolManager.ReportKeyRateLimited(provider, upstreamKey.ID, req.Model, 0)
				} else {
					s.keyPoolManager.ReportKeyFailure(provider, upstreamKey.ID, req.Model, retryStatus, "upstream_error", "upstream_error", "failover_retry")
				}
			}
			s.recordUpstreamUsageAsync(upstreamKey.ID, provider, req.Model, nil, time.Since(startTime).Milliseconds(), true)
			np, nk, num, _, rerr := s.resolveProviderAndKeyExcluding(c.Request.Context(), logicalModel, foUserPriority, foPreferredKeyID, fo.excluded)
			if rerr != nil || nk == nil {
				break
			}
			provider, upstreamKey = np, nk
			req.Model = num
			s.handlerDeps.Logger.Warn("Failover retry (non-stream)",
				zap.String("model", logicalModel),
				zap.String("to_provider", provider),
				zap.Int("attempt", fo.attempt),
				zap.Int("last_status", retryStatus),
			)
			c.Set("is_fallback", true)
			adaptor = relay.GetAdaptor(provider, &relay.AdaptorConfig{
				BaseURL:      s.resolveBaseURL(upstreamKey, provider),
				APIKey:       upstreamKey.ActualKey,
				ExtraHeaders: s.filterClientHeaders(c),
			})
			nb, mErr := json.Marshal(adaptor.ToProvider(*req))
			if mErr != nil {
				break
			}
			upstreamBody = s.pluginChain.Apply(nb, plugin.Context{
				ClientFormat: "openai", UpstreamFormat: string(adaptor.GetRelayFormat()),
				HostKey: upstreamKey.ID, ClientName: userID,
			})
			statusCode, body, err = s.dispatchUpstream(c, provider, adaptor, upstreamBody, false)
		}
	}

	if err == nil && s.keyPoolManager != nil {
		s.keyPoolManager.RecordUpstreamRequest(provider, upstreamKey.ID, req.Model)
	}
	if err != nil {
		s.handlerDeps.Logger.Error("Upstream request failed", zap.Error(err))

		// Report failure to key pool manager
		if s.keyPoolManager != nil {
			// Extract error info from response if available
			httpStatus := 500
			errorCode := "upstream_error"
			errorType := "upstream_error"
			errorMsg := err.Error()

			s.keyPoolManager.ReportKeyFailure(provider, upstreamKey.ID, req.Model, httpStatus, errorCode, errorType, errorMsg)
			s.keyPoolManager.ReportModelFailure(upstreamKey.ID, req.Model, errorMsg)
		}

		// Record upstream usage (error)
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, req.Model, nil, time.Since(startTime).Milliseconds(), true)

		s.handlerDeps.Logger.Info("request timing",
			zap.Int64("preprocess_ms", upstreamStart.Sub(startTime).Milliseconds()),
			zap.Int64("upstream_io_ms", time.Since(upstreamStart).Milliseconds()),
			zap.String("phase", "do_request_error"),
		)

		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": "Upstream request failed",
			"type":    "upstream_error",
		}})
		// Settle pre-consumed: timeout/connection error — keep charge (upstream may have consumed input)
		if localPreConsumedAmount > 0 {
			s.settleLocal(userID, localPreConsumedAmount, nil, logicalModel, provider, false, 0, classifyTransportError(err))
		} else if preConsumedAmount > 0 && s.handlerDeps.Billing != nil {
			metrics.GetMetrics().IncrementSettleCharged(provider, logicalModel, false)
			s.Settle(context.Background(), billing.SettleRequest{
				RequestID:   c.GetString("trace_id"),
				UserID:      userID,
				KeyHash:     keyHash,
				Model:       logicalModel,
				Provider:    provider,
				PreConsumed: preConsumedAmount,
				Success:     false,
				StatusCode:  0,
				ErrorType:   classifyTransportError(err),
				LatencyMs:   time.Since(startTime).Milliseconds(),
			})
		}
		return
	}

	// Check for upstream errors (4xx, 5xx)
	// Per proxy-return-code-spec-202606201359.md:
	//   - upstream 4xx → pass through to client with original status code
	//   - upstream 5xx → map to 502 Bad Gateway
	if statusCode >= 400 {
		providerBreakers.RecordFailure(provider)
		s.handlerDeps.Logger.Warn("Upstream error response",
			zap.Int("status_code", statusCode),
			zap.String("provider", provider),
			zap.String("model", logicalModel),
			zap.String("phase", "upstream_4xx_5xx"),
			zap.String("body_prefix", string(body[:min(500, len(body))])),
		)

		// Report failure to key pool manager
		if s.keyPoolManager != nil {
			errorCode := "upstream_error"
			errorType := "upstream_error"
			errorMsg := string(body[:min(500, len(body))])

			s.keyPoolManager.ReportKeyFailure(provider, upstreamKey.ID, req.Model, statusCode, errorCode, errorType, errorMsg)
			// Record model-level failure only for key-specific access issues (e.g., "not have access"),
			// NOT for general model invalidity ("model identifier is invalid", "does not support").
			// General model errors indicate the model simply doesn't exist on the provider —
			// marking model_health would incorrectly prevent all keys from being tried.
			if (statusCode == 401 || statusCode == 403) ||
				(statusCode == 400 && !keypool.IsModelLevelError(errorMsg)) {
				s.keyPoolManager.ReportModelFailure(upstreamKey.ID, req.Model, errorMsg)
			}
		}
		// Record provider health (upstream 4xx/5xx)
		s.recordProviderHealthAsync(c, provider, logicalModel, statusCode, time.Since(startTime).Milliseconds(), nil, string(body[:min(200, len(body))]))

		s.handlerDeps.Logger.Info("request timing",
			zap.Int64("preprocess_ms", upstreamStart.Sub(startTime).Milliseconds()),
			zap.Int64("upstream_io_ms", time.Since(upstreamStart).Milliseconds()),
			zap.Int("http_status", statusCode),
			zap.String("phase", "upstream_4xx_5xx"),
		)

		// Return a sanitized, classified error — never forward the raw upstream
		// body, which may leak provider internals.
		upErr := ClassifyUpstreamError(statusCode)
		c.Data(upErr.ClientStatus, "application/json", upErr.JSON())
		// Settle pre-consumed: 4xx/5xx → Portal determines action based on outcome
		if localPreConsumedAmount > 0 {
			s.settleLocal(userID, localPreConsumedAmount, nil, logicalModel, provider, false, statusCode, "")
		} else if preConsumedAmount > 0 && s.handlerDeps.Billing != nil {
			metrics.GetMetrics().IncrementSettleCharged(provider, logicalModel, false)
			errType := "4xx"
			if statusCode >= 500 {
				errType = "5xx"
			}
			s.Settle(context.Background(), billing.SettleRequest{
				RequestID:   c.GetString("trace_id"),
				UserID:      userID,
				KeyHash:     keyHash,
				Model:       logicalModel,
				Provider:    provider,
				PreConsumed: preConsumedAmount,
				Success:     false,
				StatusCode:  statusCode,
				ErrorType:   errType,
				LatencyMs:   time.Since(startTime).Milliseconds(),
			})
		}
		return
	}

	// Some upstreams (e.g. MiniMax via TokenRouter) report failures with HTTP
	// 200, embedding the error in a base_resp envelope instead of the status
	// line. Treat a non-zero base_resp.status_code as an upstream failure so
	// the request is neither billed nor recorded as a model success.
	if baseStatus := embeddedBaseRespStatus(body); baseStatus != 0 {
		s.handlerDeps.Logger.Warn("Upstream returned embedded error envelope",
			zap.Int("base_resp_status", baseStatus),
			zap.Int("http_status", statusCode),
			zap.String("model", req.Model),
		)
		providerBreakers.RecordFailure(provider)
		if s.keyPoolManager != nil {
			s.keyPoolManager.ReportKeyFailure(provider, upstreamKey.ID, req.Model, statusCode, "embedded_error", "embedded_error", fmt.Sprintf("base_resp.status_code=%d", baseStatus))
		}
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, req.Model, nil, time.Since(startTime).Milliseconds(), true)
		s.recordProviderHealthAsync(c, provider, logicalModel, statusCode, time.Since(startTime).Milliseconds(), nil, fmt.Sprintf("base_resp.status_code=%d", baseStatus))

		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": "The upstream provider rejected the request. Check the model name and request parameters.",
			"type":    "upstream_error",
			"code":    "UPSTREAM_BAD_REQUEST",
		}})
		// Settle pre-consumed: embedded error → refund the estimate.
		if localPreConsumedAmount > 0 {
			s.settleLocal(userID, localPreConsumedAmount, nil, logicalModel, provider, false, statusCode, "embedded_error")
		} else if preConsumedAmount > 0 && s.handlerDeps.Billing != nil {
			metrics.GetMetrics().IncrementSettleCharged(provider, logicalModel, false)
			s.Settle(context.Background(), billing.SettleRequest{
				RequestID:   c.GetString("trace_id"),
				UserID:      userID,
				KeyHash:     keyHash,
				Model:       logicalModel,
				Provider:    provider,
				PreConsumed: preConsumedAmount,
				Success:     false,
				StatusCode:  statusCode,
				ErrorType:   "embedded_error",
				LatencyMs:   time.Since(startTime).Milliseconds(),
			})
		}
		return
	}

	// Record model success only after confirming statusCode < 400. Recording
	// before the status check (as done previously) double-counted 4xx/5xx as
	// both success and failure, polluting model health stats.
	if s.keyPoolManager != nil {
		s.keyPoolManager.RecordModelSuccess(upstreamKey.ID, req.Model)
	}

	// Parse upstream response once and reuse for transformation and usage extraction
	parsedResp, parseErr := adaptor.ParseResponse(body)
	if parseErr != nil {
		providerBreakers.RecordFailure(provider)
		s.handlerDeps.Logger.Error("Failed to parse upstream response",
			zap.Error(parseErr),
			zap.Int("status_code", statusCode),
			zap.Int("body_len", len(body)),
		)

		if s.keyPoolManager != nil {
			s.keyPoolManager.ReportKeyFailure(provider, upstreamKey.ID, req.Model, statusCode, "parse_error", "parse_error", parseErr.Error())
		}
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, req.Model, nil, time.Since(startTime).Milliseconds(), true)
		s.recordProviderHealthAsync(c, provider, logicalModel, statusCode, time.Since(startTime).Milliseconds(), nil, parseErr.Error())

		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": "Failed to parse upstream response",
			"type":    "upstream_error",
		}})
		// Settle pre-consumed: parse failure → refund the estimate.
		if localPreConsumedAmount > 0 {
			s.settleLocal(userID, localPreConsumedAmount, nil, logicalModel, provider, false, statusCode, "parse_error")
		} else if preConsumedAmount > 0 && s.handlerDeps.Billing != nil {
			metrics.GetMetrics().IncrementSettleCharged(provider, logicalModel, false)
			s.Settle(context.Background(), billing.SettleRequest{
				RequestID:   c.GetString("trace_id"),
				UserID:      userID,
				KeyHash:     keyHash,
				Model:       logicalModel,
				Provider:    provider,
				PreConsumed: preConsumedAmount,
				Success:     false,
				StatusCode:  statusCode,
				ErrorType:   "parse_error",
				LatencyMs:   time.Since(startTime).Milliseconds(),
			})
		}
		return
	}

	// Debug: log response body. Response content is logged at Debug only
	// because it may contain user prompts, generated text, or PII.
	s.handlerDeps.Logger.Debug("Upstream response",
		zap.Int("status_code", statusCode),
		zap.Int("body_len", len(body)),
		zap.String("body_prefix", string(body[:min(200, len(body))])),
		zap.String("body_type", fmt.Sprintf("%T", body)),
	)

	// Transform response to OpenAI format
	openaiResp := adaptor.FromProvider(parsedResp, *req)
	if openaiResp == nil {
		s.handlerDeps.Logger.Error("Failed to transform response",
			zap.String("body_type", fmt.Sprintf("%T", body)),
			zap.Int("body_len", len(body)),
		)
		s.handlerDeps.Logger.Info("request timing",
			zap.Int64("preprocess_ms", upstreamStart.Sub(startTime).Milliseconds()),
			zap.Int64("upstream_io_ms", time.Since(upstreamStart).Milliseconds()),
			zap.String("phase", "transform_error"),
		)

		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"message": "Failed to transform response",
			"type":    "internal_error",
		}})
		// Settle pre-consumed: transform failure → refund the estimate.
		if localPreConsumedAmount > 0 {
			s.settleLocal(userID, localPreConsumedAmount, nil, logicalModel, provider, false, statusCode, "transform_error")
		} else if preConsumedAmount > 0 && s.handlerDeps.Billing != nil {
			metrics.GetMetrics().IncrementSettleCharged(provider, logicalModel, false)
			s.Settle(context.Background(), billing.SettleRequest{
				RequestID:   c.GetString("trace_id"),
				UserID:      userID,
				KeyHash:     keyHash,
				Model:       logicalModel,
				Provider:    provider,
				PreConsumed: preConsumedAmount,
				Success:     false,
				StatusCode:  statusCode,
				ErrorType:   "transform_error",
				LatencyMs:   time.Since(startTime).Milliseconds(),
			})
		}
		return
	}

	// Adaptors whose upstream does not return a model name (e.g. AWS Bedrock
	// Converse) leave Model empty; echo the client-facing logical model.
	if openaiResp.Model == "" {
		if lm := c.GetString("logical_model"); lm != "" {
			openaiResp.Model = lm
		}
	}

	// Track actually-called tools (response side) for the usage log.
	for i := range openaiResp.Choices {
		trackOpenAIMessageToolCalls(c, openaiResp.Choices[i].Message)
	}

	// Extract usage
	usage := adaptor.ExtractUsage(parsedResp)
	if usage == nil {
		usage = &dto.Usage{}
	}

	// Record upstream token usage and quota
	var totalTokens int64
	if s.keyPoolManager != nil {
		totalTokens = int64(usage.PromptTokens + usage.CompletionTokens)
		if totalTokens > 0 {
			s.keyPoolManager.RecordUpstreamTokens(provider, upstreamKey.ID, req.Model, totalTokens, int64(usage.PromptTokensDetails.CachedTokens))
		}
		// Increment local quota used (preserved across syncs via KeyRuntime)
		if upstreamKey.QuotaLimit > 0 {
			upstreamKey.AddQuotaUsed(totalTokens)
		}
		// Set tokens_consumed for TPD post-check and metrics middleware
		c.Set("tokens_consumed", totalTokens)
	}

	// Record upstream usage for provider reconciliation
	s.recordUpstreamUsageAsync(upstreamKey.ID, provider, req.Model, usage, time.Since(startTime).Milliseconds(), false)
	// Record provider health (success)
	s.recordProviderHealthAsync(c, provider, logicalModel, statusCode, time.Since(startTime).Milliseconds(), nil, "")

	// Record channel affinity for cache optimization
	if s.handlerDeps.AffinityCache != nil {
		cachePriority := getUserPriority(accessMode)
		s.handlerDeps.AffinityCache.RecordPreferredKeyID(c, req.Model, "default", upstreamKey.ID, cachePriority)
		s.handlerDeps.AffinityCache.ObserveUsageCache(c, usage, adaptor.GetRelayFormat())
	}

	// Record user TPM usage with group pricing. Use the logical (client-facing)
	// model name — req.Model has been replaced with the provider-specific
	// upstream model ID, which must not leak into usage records or clients.
	s.recordUsageAsyncFromContext(c, keyHash, upstreamKey.ID, logicalModel, provider, usage, time.Since(startTime).Milliseconds(), "")

	// Record cost to budget controller for daily spend tracking
	if s.budgetController != nil && usage != nil {
		cost := s.estimateActualCost(logicalModel, provider, usage)
		s.budgetController.RecordCost(cost)
	}

	// Capture request/response payload for trust-and-safety retention
	setRetentionContext(c, "openai", string(adaptor.GetRelayFormat()), req.Model, adaptor.GetRelayFormat() != types.RelayFormatOpenAI)
	s.captureRetention(c, bodyBytes, body, provider, logicalModel, upstreamKey.ID, usage, time.Since(startTime).Milliseconds(), statusCode, false, "chat_completions")

	// Record success to circuit breaker
	if providerBreakers != nil {
		providerBreakers.RecordSuccess(provider)
	}

	{
		totalMs := time.Since(startTime).Milliseconds()
		preprocessMs := upstreamStart.Sub(startTime).Milliseconds()
		upstreamMs := time.Since(upstreamStart).Milliseconds()
		postprocessMs := totalMs - preprocessMs - upstreamMs
		s.handlerDeps.Logger.Info("request timing",
			zap.Int64("preprocess_ms", preprocessMs),
			zap.Int64("upstream_ms", upstreamMs),
			zap.Int64("postprocess_ms", postprocessMs),
			zap.Int64("total_ms", totalMs),
			zap.String("phase", "success"),
			zap.String("provider", provider),
			zap.String("model", req.Model),
		)
	}

	// Settle: adjust pre-consumed amount based on actual usage
	if localPreConsumedAmount > 0 {
		s.settleLocal(userID, localPreConsumedAmount, usage, logicalModel, provider, true, statusCode, "")
	} else {
		s.settleSuccess(c, preConsumedAmount, userID, keyHash, logicalModel, provider, usage, statusCode, time.Since(startTime).Milliseconds())
	}

	// Return response
	c.JSON(http.StatusOK, openaiResp)
}

// settleSuccess settles a portal pre-consumed amount after a successful
// upstream response. When the upstream reports no token usage, the normal
// success path (unfreeze + zero-token usage record) would make the request
// completely free, so the pre-consumed estimate is charged instead.
func (s *HandlerService) settleSuccess(c *gin.Context, preConsumedAmount int64, userID, keyHash, logicalModel, provider string, usage *dto.Usage, statusCode int, latencyMs int64) {
	if preConsumedAmount <= 0 || s.handlerDeps.Billing == nil {
		return
	}
	if usage == nil || usage.PromptTokens+usage.CompletionTokens <= 0 {
		s.handlerDeps.Logger.Warn("Upstream success without usage data; charging pre-consumed estimate",
			zap.String("user_id", userID),
			zap.String("model", logicalModel),
			zap.String("provider", provider),
			zap.Int("status_code", statusCode),
		)
		metrics.GetMetrics().IncrementSettleCharged(provider, logicalModel, false)
		s.Settle(context.Background(), billing.SettleRequest{
			RequestID:   c.GetString("trace_id"),
			UserID:      userID,
			KeyHash:     keyHash,
			Model:       logicalModel,
			Provider:    provider,
			PreConsumed: preConsumedAmount,
			Success:     false,
			StatusCode:  statusCode,
			ErrorType:   "usage_missing",
			LatencyMs:   latencyMs,
		})
		return
	}
	metrics.GetMetrics().IncrementSettleCharged(provider, logicalModel, true)
	s.Settle(context.Background(), billing.SettleRequest{
		RequestID:   c.GetString("trace_id"),
		UserID:      userID,
		KeyHash:     keyHash,
		Model:       logicalModel,
		Provider:    provider,
		PreConsumed: preConsumedAmount,
		ActualUsage: usage,
		Success:     true,
		StatusCode:  statusCode,
		LatencyMs:   latencyMs,
	})
}

// preConsumeState bundles the amounts pre-consumed from the local balance
// cache and the portal freeze (the two are mutually exclusive) so outcome
// paths can settle whichever one exists.
type preConsumeState struct {
	local  int64
	portal int64
}

// settlePreConsume settles whichever pre-consume exists for one request
// outcome: the local balance-cache deduction or the portal freeze.
func (s *HandlerService) settlePreConsume(c *gin.Context, pcs *preConsumeState, usage *dto.Usage, model, provider string, success bool, statusCode int, errorType string, latencyMs int64) {
	if pcs == nil {
		return
	}
	userID := c.GetString(string(userIDKey))
	keyHash := c.GetString("key_hash")
	if pcs.local > 0 {
		s.settleLocal(userID, pcs.local, usage, model, provider, success, statusCode, errorType)
		return
	}
	if pcs.portal <= 0 || s.handlerDeps.Billing == nil {
		return
	}
	if success {
		s.settleSuccess(c, pcs.portal, userID, keyHash, model, provider, usage, statusCode, latencyMs)
		return
	}
	metrics.GetMetrics().IncrementSettleCharged(provider, model, false)
	s.Settle(context.Background(), billing.SettleRequest{
		RequestID:   c.GetString("trace_id"),
		UserID:      userID,
		KeyHash:     keyHash,
		Model:       model,
		Provider:    provider,
		PreConsumed: pcs.portal,
		Success:     false,
		StatusCode:  statusCode,
		ErrorType:   errorType,
		LatencyMs:   latencyMs,
	})
}

// handleStreamingResponse - Handle streaming responses
func (s *HandlerService) handleStreamingResponse(c *gin.Context, adaptor relay.Adaptor, upstreamBody []byte, req dto.GeneralOpenAIRequest, logicalModel, provider, keyID, userID, keyHash string, upstreamKey *keypool.KeyInternal, inputTokens int, startTime time.Time) {
	upstreamStart := time.Now()

	// Pre-consume: reserve estimated input cost before streaming.
	var preConsumedAmount int64
	var localPreConsumedAmount int64
	localPreConsumedAmount, used, rejected := s.preConsumeLocal(c, userID, c.GetString("key_type"), logicalModel, provider, inputTokens)
	if rejected {
		return
	}
	if !used {
		if s.cfg.Billing.PreConsumeEnabled && s.handlerDeps.Billing != nil && inputTokens > 0 && c.GetString("key_type") != "subscription" {
			traceID := c.GetString("request_id")
			if traceID == "" {
				traceID = fmt.Sprintf("pc-%d", time.Now().UnixNano())
			}
			c.Set("trace_id", traceID)
			pcCtx, pcCancel := billing.NewPreConsumeContext(c.Request.Context())
			pcResult := s.handlerDeps.Billing.PreConsume(pcCtx, billing.PreConsumeRequest{
				UserID:      userID,
				KeyHash:     keyHash,
				Model:       logicalModel,
				Provider:    provider,
				InputTokens: inputTokens,
				RequestID:   traceID,
				Margin:      s.cfg.Billing.PreConsumeMargin,
			})
			pcCancel()
			if pcResult.Rejected {
				metrics.GetMetrics().IncrementPreconsumeRejected()
				c.JSON(http.StatusPaymentRequired, gin.H{"error": gin.H{
					"message": "Insufficient balance for this request",
					"type":    "billing_error",
					"code":    "INSUFFICIENT_BALANCE",
				}})
				return
			}
			preConsumedAmount = pcResult.Amount
		}
	}

	// In-request failover: on a retryable upstream error BEFORE the first SSE
	// byte is flushed, re-dispatch to the next candidate key/provider. Once any
	// byte is written to the client the request is non-retryable. See
	// docs/upstream-retry-failover-design-202610251430.md.
	fo := newFailoverState(s.cfg.Failover, logicalModel)
	userPriority := getUserPriority(c.GetString("access_mode"))
	var preferredKeyID string
	if s.handlerDeps.AffinityCache != nil {
		preferredKeyID = s.handlerDeps.AffinityCache.GetPreferredKeyID(c, logicalModel, "default", userPriority)
	}

	// Make streaming request
	resp, err := adaptor.DoRequestBytesWithContext(c.Request.Context(), upstreamBody, true)

	// Failover loop (pre-first-byte only). On a retryable transport error or
	// non-200, record the failure, pick the next candidate, rebuild the adaptor
	// and body, and retry — up to the configured attempt/switch caps.
	for {
		retryStatus := 0
		transportErr := false
		if err != nil {
			if errors.Is(err, context.Canceled) {
				break // client gone — handled by the err!=nil block below
			}
			transportErr = true
		} else if resp != nil && resp.StatusCode != http.StatusOK {
			retryStatus = resp.StatusCode
		} else {
			break // success
		}
		fo.recordAttempt(provider, upstreamKey.ID)
		if !fo.shouldRetry(retryStatus, transportErr, false) {
			break
		}
		// Report the failure for key health before switching.
		if s.keyPoolManager != nil {
			if retryStatus != 0 {
				s.reportUpstreamKeyError(resp, provider, upstreamKey.ID, req.Model, "failover_retry")
			} else {
				s.keyPoolManager.ReportKeyFailure(provider, upstreamKey.ID, req.Model, 0, "transport_error", "transport_error", "failover_retry")
			}
		}
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, req.Model, nil, time.Since(startTime).Milliseconds(), true)
		if resp != nil {
			_ = resp.Body.Close()
		}
		// Pick the next candidate, excluding everything tried so far.
		np, nk, num, _, rerr := s.resolveProviderAndKeyExcluding(c.Request.Context(), logicalModel, userPriority, preferredKeyID, fo.excluded)
		if rerr != nil || nk == nil {
			break // no more candidates — fall through to final-error handling
		}
		provider, upstreamKey = np, nk
		req.Model = num
		s.handlerDeps.Logger.Warn("Failover retry (streaming)",
			zap.String("model", logicalModel),
			zap.String("to_provider", provider),
			zap.Int("attempt", fo.attempt),
			zap.Int("last_status", retryStatus),
		)
		c.Set("is_fallback", true)
		adaptor = relay.GetAdaptor(provider, &relay.AdaptorConfig{
			BaseURL:      s.resolveBaseURL(upstreamKey, provider),
			APIKey:       upstreamKey.ActualKey,
			ExtraHeaders: s.filterClientHeaders(c),
		})
		nb, mErr := json.Marshal(adaptor.ToProvider(req))
		if mErr != nil {
			break
		}
		upstreamBody = s.pluginChain.Apply(nb, plugin.Context{
			ClientFormat: "openai", UpstreamFormat: string(adaptor.GetRelayFormat()),
			HostKey: upstreamKey.ID, ClientName: userID,
		})
		resp, err = adaptor.DoRequestBytesWithContext(c.Request.Context(), upstreamBody, true)
	}

	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.handlerDeps.Logger.Warn("Client disconnected before upstream streaming request completed", zap.Error(err))
			if localPreConsumedAmount > 0 {
				s.settleLocal(userID, localPreConsumedAmount, nil, logicalModel, provider, false, 499, "client_disconnect")
			} else if preConsumedAmount > 0 && s.handlerDeps.Billing != nil {
				metrics.GetMetrics().IncrementSettleCharged(provider, logicalModel, false)
				s.Settle(context.Background(), billing.SettleRequest{
					RequestID:   c.GetString("trace_id"),
					UserID:      userID,
					KeyHash:     keyHash,
					Model:       logicalModel,
					Provider:    provider,
					PreConsumed: preConsumedAmount,
					Success:     false,
					StatusCode:  499,
					ErrorType:   "client_disconnect",
					LatencyMs:   time.Since(startTime).Milliseconds(),
				})
			}
			return
		}
		s.handlerDeps.Logger.Error("Upstream streaming request failed", zap.Error(err))

		// Record upstream usage (error)
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, req.Model, nil, time.Since(startTime).Milliseconds(), true)
		// Record provider health (transport error in streaming)
		s.recordProviderHealthAsync(c, provider, logicalModel, 0, time.Since(startTime).Milliseconds(), err, err.Error())

		// Sanitized transport-level error; the underlying error is logged above.
		upErr := UpstreamUnreachableError()
		errorBody := upErr.JSON()
		s.handlerDeps.Logger.Info("request timing",
			zap.Int64("preprocess_ms", upstreamStart.Sub(startTime).Milliseconds()),
			zap.String("phase", "stream_do_request_error"),
		)
		if localPreConsumedAmount > 0 {
			s.settleLocal(userID, localPreConsumedAmount, nil, logicalModel, provider, false, 0, classifyTransportError(err))
		} else if preConsumedAmount > 0 && s.handlerDeps.Billing != nil {
			metrics.GetMetrics().IncrementSettleCharged(provider, logicalModel, false)
			s.Settle(context.Background(), billing.SettleRequest{
				RequestID:   c.GetString("trace_id"),
				UserID:      userID,
				KeyHash:     keyHash,
				Model:       logicalModel,
				Provider:    provider,
				PreConsumed: preConsumedAmount,
				Success:     false,
				StatusCode:  0,
				ErrorType:   classifyTransportError(err),
				LatencyMs:   time.Since(startTime).Milliseconds(),
			})
		}
		c.Data(http.StatusBadGateway, "application/json", errorBody)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	// If upstream returned non-200 status, forward error body and return
	if resp.StatusCode != http.StatusOK {
		errorBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		s.handlerDeps.Logger.Warn("Upstream error response",
			zap.Int("status_code", resp.StatusCode),
			zap.String("provider", provider),
			zap.String("model", logicalModel),
			zap.String("phase", "stream_non_200"),
			zap.String("body_prefix", string(errorBody[:min(500, len(errorBody))])),
		)
		// Sanitized, classified error for the client; raw body is only used
		// server-side (key-pool failure reports, provider health).
		upErr := ClassifyUpstreamError(resp.StatusCode)
		c.Data(upErr.ClientStatus, "application/json", upErr.JSON())

		// Record failure
		if s.keyPoolManager != nil {
			s.reportUpstreamKeyError(resp, provider, upstreamKey.ID, req.Model, string(errorBody[:min(500, len(errorBody))]))
		}
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, req.Model, nil, time.Since(startTime).Milliseconds(), true)
		s.recordProviderHealthAsync(c, provider, logicalModel, resp.StatusCode, time.Since(startTime).Milliseconds(), nil, string(errorBody[:min(200, len(errorBody))]))

		if localPreConsumedAmount > 0 {
			s.settleLocal(userID, localPreConsumedAmount, nil, logicalModel, provider, false, resp.StatusCode, "")
		} else if preConsumedAmount > 0 && s.handlerDeps.Billing != nil {
			metrics.GetMetrics().IncrementSettleCharged(provider, logicalModel, false)
			errType := "4xx"
			if resp.StatusCode >= 500 {
				errType = "5xx"
			}
			s.Settle(context.Background(), billing.SettleRequest{
				RequestID:   c.GetString("trace_id"),
				UserID:      userID,
				KeyHash:     keyHash,
				Model:       logicalModel,
				Provider:    provider,
				PreConsumed: preConsumedAmount,
				Success:     false,
				StatusCode:  resp.StatusCode,
				ErrorType:   errType,
				LatencyMs:   time.Since(startTime).Milliseconds(),
			})
		}

		s.handlerDeps.Logger.Info("request timing",
			zap.Int64("preprocess_ms", upstreamStart.Sub(startTime).Milliseconds()),
			zap.Int64("upstream_io_ms", time.Since(upstreamStart).Milliseconds()),
			zap.Int("http_status", resp.StatusCode),
			zap.String("phase", "stream_non_200"),
		)
		return
	}

	upstreamConnected := time.Now()

	// Record successful upstream dispatch for key-pool rate/quota tracking.
	if s.keyPoolManager != nil {
		s.keyPoolManager.RecordUpstreamRequest(provider, upstreamKey.ID, req.Model)
	}

	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Transfer-Encoding", "chunked")

	// Stream response
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		if localPreConsumedAmount > 0 {
			s.settleLocal(userID, localPreConsumedAmount, nil, logicalModel, provider, false, http.StatusInternalServerError, "internal_error")
		} else if preConsumedAmount > 0 && s.handlerDeps.Billing != nil {
			metrics.GetMetrics().IncrementSettleCharged(provider, logicalModel, false)
			s.Settle(context.Background(), billing.SettleRequest{
				RequestID:   c.GetString("trace_id"),
				UserID:      userID,
				KeyHash:     keyHash,
				Model:       logicalModel,
				Provider:    provider,
				PreConsumed: preConsumedAmount,
				Success:     false,
				StatusCode:  http.StatusInternalServerError,
				ErrorType:   "internal_error",
				LatencyMs:   time.Since(startTime).Milliseconds(),
			})
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Streaming not supported"})
		return
	}

	reader := bufio.NewReader(resp.Body)
	parser := sse.NewParser(sseFormatForAdaptor(adaptor))
	var totalUsage dto.Usage
	// Assembles fragmented tool-call names across chunks (response-side tracking).
	toolFrags := map[int]*strings.Builder{}
	chunkIndex := 0
	var firstTokenLatency int64
	// embeddedErrStatus is set when an upstream reports a failure inside an
	// HTTP-200 SSE envelope (e.g. MiniMax base_resp.status_code != 0). It is
	// only inspected on the first chunk, before any content is streamed, so
	// the request can be settled as a failure instead of billed as a success.
	var embeddedErrStatus int

	// Set a maximum stream duration to prevent unbounded loops
	streamCtx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Minute)
	defer cancel()

	// If the client disconnects or the stream timeout fires, force the upstream
	// response body closed so the background reader goroutine exits promptly
	// instead of blocking until the upstream closes the connection.
	stopReader := context.AfterFunc(streamCtx, func() {
		_ = resp.Body.Close()
	})
	defer stopReader()

	// Stream chunk reader in background goroutine so we can interleave heartbeat ticks.
	type streamChunk struct {
		data []byte
		err  error
	}
	chunkCh := make(chan streamChunk, 1)
	go func() {
		defer close(chunkCh)
		buf := pool.GetBytes(4096)
		buf = buf[:4096]
		defer pool.PutBytes(buf)
		for {
			n, err := reader.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				select {
				case chunkCh <- streamChunk{data: chunk}:
				case <-streamCtx.Done():
					return
				}
			}
			if err != nil {
				select {
				case chunkCh <- streamChunk{err: err}:
				case <-streamCtx.Done():
				}
				return
			}
		}
	}()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-streamCtx.Done():
			s.handlerDeps.Logger.Warn("Streaming timeout or client disconnected", zap.Error(streamCtx.Err()))
			// Bill the partial usage already streamed to the client; otherwise
			// a client disconnecting mid-stream gets all output tokens for free
			// and leaves no usage/audit trail.
			hasPartialUsage := totalUsage.PromptTokens+totalUsage.CompletionTokens > 0
			if hasPartialUsage {
				c.Set("tokens_consumed", int64(totalUsage.PromptTokens+totalUsage.CompletionTokens))
				s.recordUsageAsyncFromContext(c, keyHash, upstreamKey.ID, logicalModel, provider, &totalUsage, time.Since(startTime).Milliseconds(), "")
				s.recordUpstreamUsageAsync(upstreamKey.ID, provider, req.Model, &totalUsage, time.Since(startTime).Milliseconds(), true)
				if s.handlerDeps.AuditLogger != nil {
					var partialCost float64
					if s.handlerDeps.Billing != nil {
						partialCost = s.handlerDeps.Billing.CalculateCost(&totalUsage, req.Model, provider)
					}
					s.logAudit(c, audit.AuditEntry{
						Timestamp:      startTime,
						KeyID:          keyID,
						UpstreamKeyID:  upstreamKey.ID,
						APIKeyHash:     keyHash,
						UserID:         userID,
						Provider:       provider,
						ChannelID:      0,
						Model:          req.Model,
						InputTokens:    totalUsage.PromptTokens,
						OutputTokens:   totalUsage.CompletionTokens,
						CacheHitTokens: totalUsage.PromptCacheHitTokens,
						Cost:           partialCost,
						LatencyMs:      time.Since(startTime).Milliseconds(),
						StatusCode:     resp.StatusCode,
						IsStream:       true,
						ActualGroupID:  c.GetString("group_id"),
					})
				}
			}
			if localPreConsumedAmount > 0 {
				if hasPartialUsage {
					s.settleLocal(userID, localPreConsumedAmount, &totalUsage, logicalModel, provider, true, resp.StatusCode, "")
				} else {
					s.settleLocal(userID, localPreConsumedAmount, nil, logicalModel, provider, false, 0, "timeout")
				}
			} else if preConsumedAmount > 0 && s.handlerDeps.Billing != nil {
				if hasPartialUsage {
					s.settleSuccess(c, preConsumedAmount, userID, keyHash, logicalModel, provider, &totalUsage, resp.StatusCode, time.Since(startTime).Milliseconds())
				} else {
					metrics.GetMetrics().IncrementSettleCharged(provider, logicalModel, false)
					s.Settle(context.Background(), billing.SettleRequest{
						RequestID:   c.GetString("trace_id"),
						UserID:      userID,
						KeyHash:     keyHash,
						Model:       logicalModel,
						Provider:    provider,
						PreConsumed: preConsumedAmount,
						Success:     false,
						StatusCode:  0,
						ErrorType:   "timeout",
						LatencyMs:   time.Since(startTime).Milliseconds(),
					})
				}
			}
			return
		case <-heartbeat.C:
			_, _ = c.Writer.WriteString(": heartbeat\n\n")
			flusher.Flush()
			continue
		case sc, ok := <-chunkCh:
			if !ok {
				// Channel closed — reader goroutine exited
				goto streamDone
			}
			if sc.err != nil {
				if sc.err == io.EOF {
					goto streamDone
				}
				s.handlerDeps.Logger.Error("Error reading stream", zap.Error(sc.err))
				goto streamDone
			}

			events, err := parser.Feed(sc.data)
			if err != nil {
				s.handlerDeps.Logger.Error("Error parsing SSE stream", zap.Error(err))
				goto streamDone
			}

			for _, ev := range events {
				if parser.IsStreamCompleted(ev) {
					c.SSEvent("message", "[DONE]")
					flusher.Flush()
					goto streamDone
				}

				if parser.IsStreamError(ev) {
					s.handlerDeps.Logger.Warn("Upstream stream error event", zap.String("data", ev.Data))
					// Forward the error event to the client so it can decide what to do.
					c.SSEvent("message", ev.Data)
					flusher.Flush()
					goto streamDone
				}

				// Some upstreams (e.g. MiniMax via TokenRouter) report a
				// failure inside an HTTP-200 SSE envelope: the data line carries
				// base_resp.status_code != 0 rather than an error status line.
				// Mirror the non-stream path — treat it as an upstream failure so
				// the request is settled as a failure (refunding any pre-consume)
				// and recorded as a model-health failure instead of being billed
				// as an empty success. Only meaningful on the first chunk, before
				// any content has been streamed to the client.
				if chunkIndex == 0 {
					if baseStatus := embeddedBaseRespStatus([]byte(ev.Data)); baseStatus != 0 {
						embeddedErrStatus = baseStatus
						s.handlerDeps.Logger.Warn("Upstream returned embedded error envelope on stream",
							zap.Int("base_resp_status", baseStatus),
							zap.String("provider", provider),
							zap.String("model", req.Model),
						)
						// The SSE headers are already sent, so surface the failure
						// as a stream error event followed by [DONE].
						c.SSEvent("message", `{"error":{"message":"The upstream provider rejected the request. Check the model name and request parameters.","type":"upstream_error","code":"UPSTREAM_BAD_REQUEST"}}`)
						c.SSEvent("message", "[DONE]")
						flusher.Flush()
						goto streamDone
					}
				}

				// Capture first-token latency on the first non-empty content-bearing event.
				if firstTokenLatency == 0 {
					firstTokenLatency = time.Since(upstreamConnected).Milliseconds()
				}

				// Reconstruct the raw SSE data line for adapters that still expect it.
				adaptorInput := []byte("data: " + ev.Data)

				// Transform chunk if needed
				var chunkData []byte
				if adaptor.GetRelayFormat() != types.RelayFormatOpenAI {
					// Transform to OpenAI format
					transformed := adaptor.TransformStreamChunk(adaptorInput, chunkIndex)
					if transformed != nil {
						if streamResp, ok := transformed.(*dto.ChatCompletionsStreamResponse); ok {
							chunkData, _ = json.Marshal(streamResp)
						} else {
							// Type assertion failed, use original data
							chunkData = []byte(ev.Data)
						}
					} else {
						// Transform returned nil, skip this chunk
						chunkIndex++
						continue
					}
				} else {
					chunkData = []byte(ev.Data)
				}

				// Extract usage from chunk
				streamResp := pool.GetChatCompletionsStreamResponse()
				if err := json.Unmarshal(chunkData, streamResp); err == nil {
					// Track fragmented tool-call names for the usage log.
					trackOpenAIStreamToolCalls(toolFrags, streamResp)
					if streamResp.Usage != nil {
						// Take the last non-empty usage: some providers emit
						// usage in multiple chunks (cumulative), so assign
						// (not accumulate) to avoid double-counting.
						if streamResp.Usage.PromptTokens != 0 {
							totalUsage.PromptTokens = streamResp.Usage.PromptTokens
						}
						if streamResp.Usage.CompletionTokens != 0 {
							totalUsage.CompletionTokens = streamResp.Usage.CompletionTokens
						}
						totalUsage.PromptCacheHitTokens = streamResp.Usage.PromptCacheHitTokens
						// PromptTokensDetails carries cached_tokens for providers (GLM,
						// Claude) that report cache hits via
						// prompt_tokens_details.cached_tokens instead of
						// prompt_cache_hit_tokens; propagate it too.
						totalUsage.PromptTokensDetails = streamResp.Usage.PromptTokensDetails
						// Propagate reasoning tokens (completion_tokens_details)
						// so streaming requests persist them like non-streaming.
						totalUsage.CompletionTokenDetails = streamResp.Usage.CompletionTokenDetails
					}
					// Echo the client-facing logical model — upstream chunks may
					// carry provider-specific model IDs (e.g. Bedrock
					// "anthropic.claude-...-v1:0") that must not leak to clients.
					if streamResp.Model != logicalModel {
						streamResp.Model = logicalModel
						if reencoded, mErr := json.Marshal(streamResp); mErr == nil {
							chunkData = reencoded
						}
					}
				}
				pool.PutChatCompletionsStreamResponse(streamResp)

				// Send chunk
				c.SSEvent("message", string(chunkData))
				flusher.Flush()
				chunkIndex++
			}
		}
	}

streamDone:

	// Drain any trailing events that were not terminated by a blank line.
	trailingEvents, _ := parser.Flush()
	for _, ev := range trailingEvents {
		if parser.IsStreamCompleted(ev) || parser.IsStreamError(ev) {
			continue
		}
		adaptorInput := []byte("data: " + ev.Data)
		var chunkData []byte
		if adaptor.GetRelayFormat() != types.RelayFormatOpenAI {
			transformed := adaptor.TransformStreamChunk(adaptorInput, chunkIndex)
			if transformed != nil {
				if streamResp, ok := transformed.(*dto.ChatCompletionsStreamResponse); ok {
					chunkData, _ = json.Marshal(streamResp)
				} else {
					chunkData = []byte(ev.Data)
				}
			} else {
				continue
			}
		} else {
			chunkData = []byte(ev.Data)
		}

		streamResp := pool.GetChatCompletionsStreamResponse()
		if err := json.Unmarshal(chunkData, streamResp); err == nil {
			// Track fragmented tool-call names for the usage log.
			trackOpenAIStreamToolCalls(toolFrags, streamResp)
			if streamResp.Usage != nil {
				// Take the last non-empty usage (see main loop above).
				if streamResp.Usage.PromptTokens != 0 {
					totalUsage.PromptTokens = streamResp.Usage.PromptTokens
				}
				if streamResp.Usage.CompletionTokens != 0 {
					totalUsage.CompletionTokens = streamResp.Usage.CompletionTokens
				}
				totalUsage.PromptCacheHitTokens = streamResp.Usage.PromptCacheHitTokens
				totalUsage.PromptTokensDetails = streamResp.Usage.PromptTokensDetails
				totalUsage.CompletionTokenDetails = streamResp.Usage.CompletionTokenDetails
			}
			// Echo the client-facing logical model (see main loop above).
			if streamResp.Model != logicalModel {
				streamResp.Model = logicalModel
				if reencoded, mErr := json.Marshal(streamResp); mErr == nil {
					chunkData = reencoded
				}
			}
		}
		pool.PutChatCompletionsStreamResponse(streamResp)

		c.SSEvent("message", string(chunkData))
		flusher.Flush()
		chunkIndex++
	}

	// Upstream reported an embedded failure (HTTP-200 envelope) on the first
	// chunk. Nothing billable was produced: settle any pre-consume as a
	// failure (refunds the estimate), record the failure for key-pool and
	// provider health, and return before the success billing path.
	if embeddedErrStatus != 0 {
		providerBreakers.RecordFailure(provider)
		if s.keyPoolManager != nil {
			s.keyPoolManager.ReportKeyFailure(provider, upstreamKey.ID, req.Model, resp.StatusCode, "embedded_error", "embedded_error", fmt.Sprintf("base_resp.status_code=%d", embeddedErrStatus))
		}
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, req.Model, nil, time.Since(startTime).Milliseconds(), true)
		s.recordProviderHealthAsync(c, provider, logicalModel, resp.StatusCode, time.Since(startTime).Milliseconds(), nil, fmt.Sprintf("base_resp.status_code=%d", embeddedErrStatus))
		if localPreConsumedAmount > 0 {
			s.settleLocal(userID, localPreConsumedAmount, nil, logicalModel, provider, false, resp.StatusCode, "embedded_error")
		} else if preConsumedAmount > 0 && s.handlerDeps.Billing != nil {
			metrics.GetMetrics().IncrementSettleCharged(provider, logicalModel, false)
			s.Settle(context.Background(), billing.SettleRequest{
				RequestID:   c.GetString("trace_id"),
				UserID:      userID,
				KeyHash:     keyHash,
				Model:       logicalModel,
				Provider:    provider,
				PreConsumed: preConsumedAmount,
				Success:     false,
				StatusCode:  resp.StatusCode,
				ErrorType:   "embedded_error",
				LatencyMs:   time.Since(startTime).Milliseconds(),
			})
		}
		return
	}

	// Calculate cost
	var cost float64
	if s.handlerDeps.Billing != nil {
		cost = s.handlerDeps.Billing.CalculateCost(&totalUsage, req.Model, provider)
	}

	// Log audit only when recordUsageAsync will skip the request (zero
	// tokens): it early-returns on empty usage, and it already emits the
	// logical-model audit with ratio/endpoint for non-empty streams. Logging
	// here unconditionally would double every streaming audit row.
	if s.handlerDeps.AuditLogger != nil && totalUsage.PromptTokens+totalUsage.CompletionTokens == 0 {
		s.logAudit(c, audit.AuditEntry{
			Timestamp:      startTime,
			KeyID:          keyID,
			UpstreamKeyID:  upstreamKey.ID,
			APIKeyHash:     keyHash,
			UserID:         userID,
			Provider:       provider,
			ChannelID:      0,
			Model:          req.Model,
			InputTokens:    totalUsage.PromptTokens,
			OutputTokens:   totalUsage.CompletionTokens,
			CacheHitTokens: totalUsage.PromptCacheHitTokens,
			Cost:           cost,
			LatencyMs:      time.Since(startTime).Milliseconds(),
			StatusCode:     resp.StatusCode,
			IsStream:       true,
			ActualGroupID:  c.GetString("group_id"),
			Endpoint:       c.Request.URL.Path,
		})
	}

	// Record channel affinity for cache optimization
	if s.handlerDeps.AffinityCache != nil {
		cachePriority := getUserPriority(c.GetString("access_mode"))
		s.handlerDeps.AffinityCache.RecordPreferredKeyID(c, req.Model, "default", upstreamKey.ID, cachePriority)
		s.handlerDeps.AffinityCache.ObserveUsageCache(c, &totalUsage, adaptor.GetRelayFormat())
	}

	// Record upstream usage for provider reconciliation
	// Note the tools actually called during the stream (response side).
	flushStreamToolCalls(c, toolFrags)
	s.recordUpstreamUsageAsync(upstreamKey.ID, provider, req.Model, &totalUsage, time.Since(startTime).Milliseconds(), false)
	// Record provider health (streaming success)
	s.recordProviderHealthAsync(c, provider, logicalModel, resp.StatusCode, time.Since(startTime).Milliseconds(), nil, "")
	providerBreakers.RecordSuccess(provider)

	// Record upstream token usage and quota.
	totalTokens := int64(totalUsage.PromptTokens + totalUsage.CompletionTokens)
	if s.keyPoolManager != nil && totalTokens > 0 {
		s.keyPoolManager.RecordUpstreamTokens(provider, upstreamKey.ID, req.Model, totalTokens, int64(totalUsage.PromptTokensDetails.CachedTokens))
	}
	// Increment local quota used (preserved across syncs via KeyRuntime)
	if upstreamKey.QuotaLimit > 0 && totalTokens > 0 {
		upstreamKey.AddQuotaUsed(totalTokens)
	}
	// Set tokens_consumed for TPD post-check and metrics middleware. Always
	// set (outside the QuotaLimit branch) so keys without a quota limit still
	// get the TPD post-check and metrics can observe consumption.
	c.Set("tokens_consumed", int64(totalUsage.PromptTokens+totalUsage.CompletionTokens))

	// Set first-token latency in context before usage recording so that
	// recordUsageAsyncFromContext can capture it for usage_records.
	c.Set("first_token_latency_ms", firstTokenLatency)

	// Record token usage for billing with group pricing. Use the logical
	// (client-facing) model name — req.Model holds the provider-specific
	// upstream model ID at this point.
	s.recordUsageAsyncFromContext(c, keyHash, upstreamKey.ID, logicalModel, provider, &totalUsage, time.Since(startTime).Milliseconds(), "")

	// Settle the locally pre-consumed estimate against actual streaming usage.
	if localPreConsumedAmount > 0 {
		s.settleLocal(userID, localPreConsumedAmount, &totalUsage, logicalModel, provider, true, resp.StatusCode, "")
	} else {
		s.settleSuccess(c, preConsumedAmount, userID, keyHash, logicalModel, provider, &totalUsage, resp.StatusCode, time.Since(startTime).Milliseconds())
	}

	// Capture retention metadata for streaming request (response body is not accumulated in minimal version)
	var requestBody []byte
	if v, ok := c.Get("request_body"); ok {
		if b, ok := v.([]byte); ok {
			requestBody = b
		}
	}
	setRetentionContext(c, "openai", string(adaptor.GetRelayFormat()), req.Model, adaptor.GetRelayFormat() != types.RelayFormatOpenAI)
	s.captureRetention(c, requestBody, nil, provider, logicalModel, upstreamKey.ID, &totalUsage, time.Since(startTime).Milliseconds(), resp.StatusCode, true, "chat_completions_stream")

	totalMs := time.Since(startTime).Milliseconds()
	preprocessMs := upstreamStart.Sub(startTime).Milliseconds()
	upstreamInitialMs := upstreamConnected.Sub(upstreamStart).Milliseconds()
	streamingDurationMs := time.Since(upstreamConnected).Milliseconds()
	s.handlerDeps.Logger.Info("request timing",
		zap.Int64("preprocess_ms", preprocessMs),
		zap.Int64("upstream_initial_ms", upstreamInitialMs),
		zap.Int64("streaming_duration_ms", streamingDurationMs),
		zap.Int64("total_ms", totalMs),
		zap.String("phase", "stream_success"),
		zap.String("provider", provider),
		zap.String("model", req.Model),
	)
}

// handleCompletions - Handle legacy completions (deprecated)
func (s *HandlerService) handleCompletions(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"error": gin.H{
			"message": "Completions API is deprecated. Please use chat/completions instead.",
			"type":    "deprecated_error",
		},
	})
}

// handleClaudeMessages - Handle Claude native messages API
// Phase 1: Supports Anthropic client -> Anthropic upstream passthrough with billing header stripping
// Phase 2: Will support Anthropic client -> OpenAI/Other upstream via full conversion
func (s *HandlerService) handleClaudeMessages(c *gin.Context) {
	startTime := time.Now()

	// Read raw request body
	const maxBodySize = 10 * 1024 * 1024 // 10 MB
	bodyBytes, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBodySize))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "Failed to read request body",
			"type":    "invalid_request_error",
		}})
		return
	}

	// Get key info from context (set by auth middleware)
	keyID := c.GetString(string(keyIDKey))
	userID := c.GetString(string(userIDKey))
	keyHash := c.GetString("key_hash")
	accessMode := c.GetString("access_mode")

	// Store original request body for retention, then apply pre-conversion
	// plugins for Anthropic-format requests.
	c.Set("request_body", bodyBytes)
	bodyBytes = s.pluginChain.Apply(bodyBytes, plugin.Context{
		ClientFormat: "anthropic",
		HostKey:      keyID,
		ClientName:   userID,
	})
	c.Set("request_body", bodyBytes)

	// Parse as Anthropic request
	var anthropicReq convert.AnthropicRequest
	if err := json.Unmarshal(bodyBytes, &anthropicReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "Invalid Anthropic request: " + err.Error(),
			"type":    "invalid_request_error",
		}})
		return
	}

	// Record streaming intent in context so usage/audit reporting marks the
	// request as streaming. Without this, every /v1/messages call (passthrough
	// and converted alike) is reported as non-streaming downstream.
	isStream := anthropicReq.Stream != nil && *anthropicReq.Stream
	c.Set("is_stream", isStream)
	if len(anthropicReq.Tools) > 0 {
		toolNames := make([]string, 0, len(anthropicReq.Tools))
		for _, t := range anthropicReq.Tools {
			if t.Name != "" {
				toolNames = append(toolNames, t.Name)
			}
		}
		if s := joinAgentToolNames(toolNames); s != "" {
			c.Set("agent_tools", s)
		}
	}

	// Convert to OpenAI Hub for routing/validation logic
	hubReq, err := convert.AnthropicToOpenAI(&anthropicReq)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "Failed to convert request: " + err.Error(),
			"type":    "invalid_request_error",
		}})
		return
	}
	if hubReq.ReasoningEffort != "" {
		c.Set("reasoning_intensity", hubReq.ReasoningEffort)
	}

	// Resolve model and routing
	modelRoutingMode := c.GetString("model_routing_mode")
	if modelRoutingMode == "" {
		modelRoutingMode = "single"
	}
	var selectedModels []string
	if v, ok := c.Get("selected_models"); ok {
		selectedModels, ok = v.([]string)
		if !ok {
			s.handlerDeps.Logger.Warn("selected_models has unexpected type",
				zap.String("expected", "[]string"),
				zap.Any("actual", v),
			)
			selectedModels = nil
		}
	}
	currentModel := c.GetString("current_model")

	var allowedModels []string
	if v, ok := c.Get("allowed_models"); ok {
		allowedModels, _ = v.([]string)
	}

	resolvedModel, err := s.resolveModel(hubReq.Model, modelRoutingMode, currentModel, selectedModels, allowedModels, keyHash)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": err.Error(),
			"type":    "invalid_request_error",
			"code":    "MODEL_RESOLUTION_ERROR",
		}})
		return
	}
	hubReq.Model = resolvedModel
	normalizeReasoningFields(hubReq)

	// Enforce allowed_models restriction (after model resolution)
	if !checkAllowedModels(c, hubReq.Model) {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{
			"message": fmt.Sprintf("Model '%s' is not in your allowed models list.", hubReq.Model),
			"type":    "model_not_allowed",
			"code":    "MODEL_NOT_ALLOWED",
		}})
		return
	}

	// Get user priority
	userPriority := getUserPriority(accessMode)

	// Get affinity-preferred key ID
	var preferredKeyID string
	if s.handlerDeps.AffinityCache != nil {
		cachePriority := getUserPriority(accessMode)
		preferredKeyID = s.handlerDeps.AffinityCache.GetPreferredKeyID(c, hubReq.Model, "default", cachePriority)
	}

	// Resolve provider and upstream key
	provider, upstreamKey, upstreamModelName, isFallback, err := s.resolveProviderAndKey(c.Request.Context(), hubReq.Model, userPriority, preferredKeyID)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
			"message": err.Error(),
			"type":    "service_error",
		}})
		return
	}
	c.Set("is_fallback", isFallback)

	// Phase 1: If the upstream speaks the Anthropic Messages API natively,
	// pass the request through unchanged. This preserves Anthropic-only
	// features that the OpenAI chat format cannot express — most importantly
	// prompt caching (cache_control markers). Aggregators like OpenRouter and
	// TokenRouter expose /v1/messages for anthropic/claude-* models, so an
	// Anthropic client -> Anthropic upstream passthrough keeps cache hits
	// (previously the Anthropic->OpenAI conversion dropped cache_control,
	// forcing 0% cache hit rate and much higher cost).
	if isAnthropicNativeProvider(provider) {
		s.handleAnthropicPassthrough(c, bodyBytes, &anthropicReq, hubReq, provider, upstreamKey, upstreamModelName, keyID, userID, keyHash, startTime)
		return
	}

	// Phase 2: Anthropic client -> OpenAI/Other upstream via full conversion
	s.handleAnthropicToOpenAI(c, &anthropicReq, hubReq, provider, upstreamKey, upstreamModelName, keyID, userID, keyHash, startTime)
}

// rewriteAnthropicBodyModel returns body with its top-level "model" set to
// upstreamModelName (no-op when equal/empty). Used by passthrough failover to
// re-point the body at the next provider's model name.
func rewriteAnthropicBodyModel(body []byte, logicalModel, upstreamModelName string) []byte {
	if upstreamModelName == "" || upstreamModelName == logicalModel {
		return body
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) != nil {
		return body
	}
	mb, err := json.Marshal(upstreamModelName)
	if err != nil {
		return body
	}
	m["model"] = mb
	if nb, err := json.Marshal(m); err == nil {
		return nb
	}
	return body
}

// buildAnthropicPassthroughReq builds a raw POST to {base}/messages with the
// provider's auth scheme (native Anthropic x-api-key vs aggregator Bearer).
func (s *HandlerService) buildAnthropicPassthroughReq(c *gin.Context, provider string, upstreamKey *keypool.KeyInternal, body []byte, stream bool) (*http.Request, error) {
	base := s.resolveBaseURL(upstreamKey, provider)
	req, err := http.NewRequest("POST", base+"/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if anthropicAuthUsesAPIKey(provider) {
		req.Header.Set("x-api-key", upstreamKey.ActualKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("anthropic-dangerous-direct-browser-access", "true")
	} else {
		req.Header.Set("Authorization", "Bearer "+upstreamKey.ActualKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	s.applyForwardedHeaders(req, c)
	return req, nil
}

// handleAnthropicPassthrough handles Anthropic client -> Anthropic upstream with billing header stripping
func (s *HandlerService) handleAnthropicPassthrough(c *gin.Context, bodyBytes []byte, anthropicReq *convert.AnthropicRequest, hubReq *dto.GeneralOpenAIRequest, provider string, upstreamKey *keypool.KeyInternal, upstreamModelName, keyID, userID, keyHash string, startTime time.Time) {
	// Strip billing header from system prompt to improve prefix cache hit rate
	if anthropicReq.System != nil {
		var systemStr string
		if err := json.Unmarshal(anthropicReq.System, &systemStr); err == nil {
			stripped := convert.StripLeadingAnthropicBillingHeader(systemStr)
			if stripped != systemStr {
				anthropicReq.System = json.RawMessage(`"` + stripped + `"`)
				newBody, err := json.Marshal(anthropicReq)
				if err == nil {
					bodyBytes = newBody
				}
			}
		}
	}

	// Rewrite the model name in the UPSTREAM body to the provider-specific name
	// (e.g. client "claude-opus-5" -> aggregator "anthropic/claude-opus-5").
	// Done last, on the final body bytes, so the billing-header strip above
	// cannot overwrite it. anthropicReq.Model stays the logical name for
	// billing/usage attribution (mirrors handleAnthropicToOpenAI). Native
	// Anthropic keeps the client name (upstreamModelName == client), a no-op.
	baseBody := bodyBytes // logical-model body, reused if failover re-points the model
	bodyBytes = rewriteAnthropicBodyModel(bodyBytes, anthropicReq.Model, upstreamModelName)

	// Estimate input tokens and pre-consume before upstream request.
	var inputTokens int
	if s.handlerDeps.TokenCounter != nil {
		inputTokens = s.handlerDeps.TokenCounter.EstimateMessages(hubReq.Messages, anthropicReq.Model)
	}
	var preConsumedAmount int64
	var localPreConsumedAmount int64
	localPreConsumedAmount, used, rejected := s.preConsumeLocal(c, userID, c.GetString("key_type"), anthropicReq.Model, provider, inputTokens)
	if rejected {
		return
	}
	if !used {
		if s.cfg.Billing.PreConsumeEnabled && s.handlerDeps.Billing != nil && inputTokens > 0 && c.GetString("key_type") != "subscription" {
			traceID := c.GetString("request_id")
			if traceID == "" {
				traceID = fmt.Sprintf("pc-%d", time.Now().UnixNano())
			}
			c.Set("trace_id", traceID)
			pcCtx, pcCancel := billing.NewPreConsumeContext(c.Request.Context())
			pcResult := s.handlerDeps.Billing.PreConsume(pcCtx, billing.PreConsumeRequest{
				UserID:      userID,
				KeyHash:     keyHash,
				Model:       anthropicReq.Model,
				Provider:    provider,
				InputTokens: inputTokens,
				RequestID:   traceID,
				Margin:      s.cfg.Billing.PreConsumeMargin,
			})
			pcCancel()
			if pcResult.Rejected {
				metrics.GetMetrics().IncrementPreconsumeRejected()
				c.JSON(http.StatusPaymentRequired, gin.H{"error": gin.H{
					"message": "Insufficient balance for this request",
					"type":    "billing_error",
					"code":    "INSUFFICIENT_BALANCE",
				}})
				return
			}
			preConsumedAmount = pcResult.Amount
		}
	}

	isStream := anthropicReq.Stream != nil && *anthropicReq.Stream

	// Build the raw upstream request (base/messages + provider auth). Rebuilt by
	// the failover loops below when switching to a native-compatible provider.
	req, err := s.buildAnthropicPassthroughReq(c, provider, upstreamKey, bodyBytes, isStream)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"message": "Failed to create upstream request",
			"type":    "internal_error",
		}})
		return
	}

	// Failover is restricted to Anthropic-native providers so the unchanged
	// passthrough body is never sent to a convert-only upstream.
	foUserPriority := getUserPriority(c.GetString("access_mode"))
	foPassthrough := newFailoverState(s.cfg.Failover, anthropicReq.Model)
	nativeOK := func(p string) bool { return isAnthropicNativeProvider(p) }
	if isStream {
		// Streaming passthrough
		resp, err := sharedStreamingClient.Do(req)

		// Failover (native-only, pre-first-byte): on retryable transport/non-200,
		// switch to another Anthropic-native provider and resend the raw body.
		for {
			retryStatus := 0
			transportErr := false
			if err != nil {
				transportErr = true
			} else if resp != nil && resp.StatusCode != http.StatusOK {
				retryStatus = resp.StatusCode
			} else {
				break
			}
			foPassthrough.recordAttempt(provider, upstreamKey.ID)
			if !foPassthrough.shouldRetry(retryStatus, transportErr, false) {
				break
			}
			if s.keyPoolManager != nil {
				if retryStatus == http.StatusTooManyRequests {
					s.keyPoolManager.ReportKeyRateLimited(provider, upstreamKey.ID, anthropicReq.Model, 0)
				} else {
					s.keyPoolManager.ReportKeyFailure(provider, upstreamKey.ID, anthropicReq.Model, retryStatus, "upstream_error", "upstream_error", "failover_retry")
				}
			}
			s.recordUpstreamUsageAsync(upstreamKey.ID, provider, anthropicReq.Model, nil, time.Since(startTime).Milliseconds(), true)
			if resp != nil {
				_ = resp.Body.Close()
			}
			np, nk, num, _, rerr := s.resolveProviderAndKeyFiltered(c.Request.Context(), anthropicReq.Model, foUserPriority, "", foPassthrough.excluded, nativeOK)
			if rerr != nil || nk == nil {
				break
			}
			provider, upstreamKey = np, nk
			c.Set("is_fallback", true)
			s.handlerDeps.Logger.Warn("Failover retry (anthropic passthrough stream)",
				zap.String("model", anthropicReq.Model), zap.String("to_provider", provider),
				zap.Int("attempt", foPassthrough.attempt), zap.Int("last_status", retryStatus))
			nbody := rewriteAnthropicBodyModel(baseBody, anthropicReq.Model, num)
			nreq, berr := s.buildAnthropicPassthroughReq(c, provider, upstreamKey, nbody, true)
			if berr != nil {
				break
			}
			req = nreq
			resp, err = sharedStreamingClient.Do(req)
		}

		if err != nil {
			if localPreConsumedAmount > 0 {
				s.settleLocal(userID, localPreConsumedAmount, nil, anthropicReq.Model, provider, false, 0, classifyTransportError(err))
			} else if preConsumedAmount > 0 && s.handlerDeps.Billing != nil {
				metrics.GetMetrics().IncrementSettleCharged(provider, anthropicReq.Model, false)
				s.Settle(context.Background(), billing.SettleRequest{
					RequestID:   c.GetString("trace_id"),
					UserID:      userID,
					KeyHash:     keyHash,
					Model:       anthropicReq.Model,
					Provider:    provider,
					PreConsumed: preConsumedAmount,
					Success:     false,
					StatusCode:  0,
					ErrorType:   classifyTransportError(err),
					LatencyMs:   time.Since(startTime).Milliseconds(),
				})
			}
			s.recordUpstreamUsageAsync(upstreamKey.ID, provider, anthropicReq.Model, nil, time.Since(startTime).Milliseconds(), true)
			s.recordProviderHealthAsync(c, provider, anthropicReq.Model, 0, time.Since(startTime).Milliseconds(), err, err.Error())
			if s.keyPoolManager != nil {
				s.keyPoolManager.ReportKeyFailure(provider, upstreamKey.ID, anthropicReq.Model, 0, "transport_error", "transport_error", err.Error())
			}
			// Sanitized transport-level error; the underlying error is logged above.
			upErr := UpstreamUnreachableError()
			c.Data(upErr.ClientStatus, "application/json", upErr.JSON())
			return
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			errorBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			s.handlerDeps.Logger.Warn("Upstream error response",
				zap.Int("status_code", resp.StatusCode),
				zap.String("provider", provider),
				zap.String("model", anthropicReq.Model),
				zap.String("phase", "stream_non_200"),
				zap.String("body_prefix", string(errorBody[:min(500, len(errorBody))])),
			)
			// Sanitized, classified error for the client; raw body is only used
			// server-side (key-pool failure reports, provider health).
			upErr := ClassifyUpstreamError(resp.StatusCode)
			c.Data(upErr.ClientStatus, "application/json", upErr.JSON())

			if s.keyPoolManager != nil {
				s.reportUpstreamKeyError(resp, provider, upstreamKey.ID, anthropicReq.Model, string(errorBody[:min(500, len(errorBody))]))
			}
			s.recordUpstreamUsageAsync(upstreamKey.ID, provider, anthropicReq.Model, nil, time.Since(startTime).Milliseconds(), true)
			s.recordProviderHealthAsync(c, provider, anthropicReq.Model, resp.StatusCode, time.Since(startTime).Milliseconds(), nil, string(errorBody[:min(200, len(errorBody))]))

			if localPreConsumedAmount > 0 {
				s.settleLocal(userID, localPreConsumedAmount, nil, anthropicReq.Model, provider, false, resp.StatusCode, "")
			} else if preConsumedAmount > 0 && s.handlerDeps.Billing != nil {
				metrics.GetMetrics().IncrementSettleCharged(provider, anthropicReq.Model, false)
				errType := "4xx"
				if resp.StatusCode >= 500 {
					errType = "5xx"
				}
				s.Settle(context.Background(), billing.SettleRequest{
					RequestID:   c.GetString("trace_id"),
					UserID:      userID,
					KeyHash:     keyHash,
					Model:       anthropicReq.Model,
					Provider:    provider,
					PreConsumed: preConsumedAmount,
					Success:     false,
					StatusCode:  resp.StatusCode,
					ErrorType:   errType,
					LatencyMs:   time.Since(startTime).Milliseconds(),
				})
			}
			return
		}

		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("Transfer-Encoding", "chunked")

		flusher, ok := c.Writer.(http.Flusher)
		if !ok {
			if localPreConsumedAmount > 0 {
				s.settleLocal(userID, localPreConsumedAmount, nil, anthropicReq.Model, provider, false, http.StatusInternalServerError, "internal_error")
			} else if preConsumedAmount > 0 && s.handlerDeps.Billing != nil {
				metrics.GetMetrics().IncrementSettleCharged(provider, anthropicReq.Model, false)
				s.Settle(context.Background(), billing.SettleRequest{
					RequestID:   c.GetString("trace_id"),
					UserID:      userID,
					KeyHash:     keyHash,
					Model:       anthropicReq.Model,
					Provider:    provider,
					PreConsumed: preConsumedAmount,
					Success:     false,
					StatusCode:  http.StatusInternalServerError,
					ErrorType:   "internal_error",
					LatencyMs:   time.Since(startTime).Milliseconds(),
				})
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Streaming not supported"})
			return
		}

		streamCtx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Minute)
		defer cancel()
		stopReader := context.AfterFunc(streamCtx, func() {
			_ = resp.Body.Close()
		})
		defer stopReader()

		reader := bufio.NewReader(resp.Body)
		var totalUsage dto.Usage
		var streamSuccess bool

		for {
			line, err := reader.ReadString('\n')
			if line != "" {
				_, _ = c.Writer.Write([]byte(line))
				flusher.Flush()
			}
			if err != nil {
				if err == io.EOF {
					streamSuccess = true
				}
				break
			}

			// Parse Anthropic SSE events to extract usage for billing.
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "data: ") {
				data := strings.TrimPrefix(trimmed, "data: ")
				var event convert.AnthropicStreamEvent
				if json.Unmarshal([]byte(data), &event) == nil {
					switch event.Type {
					case "message_start":
						if event.Message != nil {
							// Anthropic's input_tokens excludes cache_read; add it
							// back to align with OpenAI semantics (see UsageFromClaude).
							cacheRead := event.Message.Usage.CacheReadInputTokens
							totalUsage.PromptTokens = event.Message.Usage.InputTokens + cacheRead
							totalUsage.InputTokens = event.Message.Usage.InputTokens
							totalUsage.PromptCacheHitTokens = cacheRead
							totalUsage.PromptTokensDetails.CachedTokens = cacheRead
							totalUsage.PromptTokensDetails.CachedCreationTokens = event.Message.Usage.CacheCreationInputTokens
						}
					case "message_delta":
						if event.Usage != nil {
							totalUsage.CompletionTokens = event.Usage.OutputTokens
							totalUsage.OutputTokens = event.Usage.OutputTokens
						}
					case "content_block_start":
						// Track actually-called tools (response side): a tool_use
						// block announces one tool invocation by name.
						if event.ContentBlock != nil && event.ContentBlock.Type == "tool_use" {
							noteCalledTool(c, event.ContentBlock.Name)
						}
					}
				}
			}
		}

		if streamCtx.Err() != nil {
			streamSuccess = false
		}

		// Record provider health and upstream usage after stream
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, anthropicReq.Model, &totalUsage, time.Since(startTime).Milliseconds(), !streamSuccess)
		s.recordProviderHealthAsync(c, provider, anthropicReq.Model, resp.StatusCode, time.Since(startTime).Milliseconds(), nil, "")
		if streamSuccess {
			providerBreakers.RecordSuccess(provider)
		}

		var cost float64
		if streamSuccess && s.handlerDeps.Billing != nil {
			cost = s.handlerDeps.Billing.CalculateCost(&totalUsage, anthropicReq.Model, provider)
		}
		if s.handlerDeps != nil && s.handlerDeps.AuditLogger != nil {
			s.logAudit(c, audit.AuditEntry{
				Timestamp:      startTime,
				KeyID:          keyID,
				UpstreamKeyID:  upstreamKey.ID,
				APIKeyHash:     keyHash,
				UserID:         userID,
				Provider:       provider,
				Model:          anthropicReq.Model,
				InputTokens:    totalUsage.PromptTokens,
				OutputTokens:   totalUsage.CompletionTokens,
				CacheHitTokens: totalUsage.PromptCacheHitTokens,
				Cost:           cost,
				LatencyMs:      time.Since(startTime).Milliseconds(),
				StatusCode:     http.StatusOK,
				RequestType:    "claude_native_stream",
				IsStream:       true,
			})
		}

		// Record channel affinity
		if s.handlerDeps.AffinityCache != nil {
			cachePriority := getUserPriority(c.GetString("access_mode"))
			s.handlerDeps.AffinityCache.RecordPreferredKeyID(c, anthropicReq.Model, "default", upstreamKey.ID, cachePriority)
			s.handlerDeps.AffinityCache.ObserveUsageCache(c, &totalUsage, types.RelayFormatClaude)
		}

		c.Set("tokens_consumed", int64(totalUsage.PromptTokens+totalUsage.CompletionTokens))
		s.recordUsageAsyncFromContext(c, keyHash, upstreamKey.ID, anthropicReq.Model, provider, &totalUsage, time.Since(startTime).Milliseconds(), "")

		// Settle the pre-consumed estimate against actual streaming usage.
		if localPreConsumedAmount > 0 {
			s.settleLocal(userID, localPreConsumedAmount, &totalUsage, anthropicReq.Model, provider, streamSuccess, resp.StatusCode, "")
		} else if preConsumedAmount > 0 && s.handlerDeps.Billing != nil {
			metrics.GetMetrics().IncrementSettleCharged(provider, anthropicReq.Model, streamSuccess)
			s.Settle(context.Background(), billing.SettleRequest{
				RequestID:   c.GetString("trace_id"),
				UserID:      userID,
				KeyHash:     keyHash,
				Model:       anthropicReq.Model,
				Provider:    provider,
				PreConsumed: preConsumedAmount,
				ActualUsage: &totalUsage,
				Success:     streamSuccess,
				StatusCode:  resp.StatusCode,
				LatencyMs:   time.Since(startTime).Milliseconds(),
			})
		}

		// Capture retention metadata for streaming Anthropic native passthrough
		var requestBody []byte
		if v, ok := c.Get("request_body"); ok {
			if b, ok := v.([]byte); ok {
				requestBody = b
			}
		}
		setRetentionContext(c, "anthropic", "anthropic", anthropicReq.Model, true)
		s.captureRetention(c, requestBody, nil, provider, anthropicReq.Model, upstreamKey.ID, &totalUsage, time.Since(startTime).Milliseconds(), resp.StatusCode, true, "claude_native_stream")
		return
	}

	// Non-streaming passthrough
	resp, err := sharedHandlerClient.Do(req)

	// Failover (native-only): non-stream is buffered → retryable pre-response.
	for {
		retryStatus := 0
		transportErr := false
		if err != nil {
			transportErr = true
		} else if resp != nil && resp.StatusCode != http.StatusOK {
			retryStatus = resp.StatusCode
		} else {
			break
		}
		foPassthrough.recordAttempt(provider, upstreamKey.ID)
		if !foPassthrough.shouldRetry(retryStatus, transportErr, false) {
			break
		}
		if s.keyPoolManager != nil {
			if retryStatus == http.StatusTooManyRequests {
				s.keyPoolManager.ReportKeyRateLimited(provider, upstreamKey.ID, anthropicReq.Model, 0)
			} else {
				s.keyPoolManager.ReportKeyFailure(provider, upstreamKey.ID, anthropicReq.Model, retryStatus, "upstream_error", "upstream_error", "failover_retry")
			}
		}
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, anthropicReq.Model, nil, time.Since(startTime).Milliseconds(), true)
		if resp != nil {
			_ = resp.Body.Close()
		}
		np, nk, num, _, rerr := s.resolveProviderAndKeyFiltered(c.Request.Context(), anthropicReq.Model, foUserPriority, "", foPassthrough.excluded, nativeOK)
		if rerr != nil || nk == nil {
			break
		}
		provider, upstreamKey = np, nk
		c.Set("is_fallback", true)
		s.handlerDeps.Logger.Warn("Failover retry (anthropic passthrough)",
			zap.String("model", anthropicReq.Model), zap.String("to_provider", provider),
			zap.Int("attempt", foPassthrough.attempt), zap.Int("last_status", retryStatus))
		nbody := rewriteAnthropicBodyModel(baseBody, anthropicReq.Model, num)
		nreq, berr := s.buildAnthropicPassthroughReq(c, provider, upstreamKey, nbody, false)
		if berr != nil {
			break
		}
		req = nreq
		resp, err = sharedHandlerClient.Do(req)
	}

	if err != nil {
		if localPreConsumedAmount > 0 {
			s.settleLocal(userID, localPreConsumedAmount, nil, anthropicReq.Model, provider, false, 0, classifyTransportError(err))
		} else if preConsumedAmount > 0 && s.handlerDeps.Billing != nil {
			metrics.GetMetrics().IncrementSettleCharged(provider, anthropicReq.Model, false)
			s.Settle(context.Background(), billing.SettleRequest{
				RequestID:   c.GetString("trace_id"),
				UserID:      userID,
				KeyHash:     keyHash,
				Model:       anthropicReq.Model,
				Provider:    provider,
				PreConsumed: preConsumedAmount,
				Success:     false,
				StatusCode:  0,
				ErrorType:   classifyTransportError(err),
				LatencyMs:   time.Since(startTime).Milliseconds(),
			})
		}
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, anthropicReq.Model, nil, time.Since(startTime).Milliseconds(), true)
		s.recordProviderHealthAsync(c, provider, anthropicReq.Model, 0, time.Since(startTime).Milliseconds(), err, err.Error())
		if s.keyPoolManager != nil {
			s.keyPoolManager.ReportKeyFailure(provider, upstreamKey.ID, anthropicReq.Model, 0, "transport_error", "transport_error", err.Error())
		}
		// Sanitized transport-level error; the underlying error is logged above.
		s.handlerDeps.Logger.Error("Upstream request failed", zap.Error(err))
		upErr := UpstreamUnreachableError()
		c.Data(upErr.ClientStatus, "application/json", upErr.JSON())
		return
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if localPreConsumedAmount > 0 {
			s.settleLocal(userID, localPreConsumedAmount, nil, anthropicReq.Model, provider, false, 0, "read_error")
		} else if preConsumedAmount > 0 && s.handlerDeps.Billing != nil {
			metrics.GetMetrics().IncrementSettleCharged(provider, anthropicReq.Model, false)
			s.Settle(context.Background(), billing.SettleRequest{
				RequestID:   c.GetString("trace_id"),
				UserID:      userID,
				KeyHash:     keyHash,
				Model:       anthropicReq.Model,
				Provider:    provider,
				PreConsumed: preConsumedAmount,
				Success:     false,
				StatusCode:  0,
				ErrorType:   "read_error",
				LatencyMs:   time.Since(startTime).Milliseconds(),
			})
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": "Failed to read upstream response",
			"type":    "upstream_error",
		}})
		return
	}

	// Extract usage for audit/billing (parsed before recording so we can pass it downstream)
	var usage *dto.Usage
	if resp.StatusCode == http.StatusOK {
		var anthropicResp convert.AnthropicResponse
		if err := json.Unmarshal(body, &anthropicResp); err == nil {
			// Track actually-called tools (response side) for the usage log.
			for _, blk := range anthropicResp.Content {
				if blk.Type == "tool_use" {
					noteCalledTool(c, blk.Name)
				}
			}
			// Anthropic's input_tokens excludes cache_read; add it back to
			// align with OpenAI semantics (see UsageFromClaude).
			cacheRead := anthropicResp.Usage.CacheReadInputTokens
			usage = &dto.Usage{
				PromptTokens:     anthropicResp.Usage.InputTokens + cacheRead,
				CompletionTokens: anthropicResp.Usage.OutputTokens,
				TotalTokens:      anthropicResp.Usage.InputTokens + cacheRead + anthropicResp.Usage.OutputTokens,
				InputTokens:      anthropicResp.Usage.InputTokens,
				OutputTokens:     anthropicResp.Usage.OutputTokens,
			}
			if cacheRead > 0 {
				usage.PromptTokensDetails.CachedTokens = cacheRead
				usage.PromptCacheHitTokens = cacheRead
			}
			if anthropicResp.Usage.CacheCreationInputTokens > 0 {
				usage.PromptTokensDetails.CachedCreationTokens = anthropicResp.Usage.CacheCreationInputTokens
			}
		}
	}
	if usage == nil {
		usage = &dto.Usage{}
	}

	// Return Anthropic response directly (success) or sanitized error (non-200).
	if resp.StatusCode == http.StatusOK {
		c.Data(resp.StatusCode, "application/json", body)
	} else {
		upErr := ClassifyUpstreamError(resp.StatusCode)
		c.Data(upErr.ClientStatus, "application/json", upErr.JSON())
	}

	// Settle pre-consumed estimate for non-2xx responses (refund) before further recording.
	if resp.StatusCode != http.StatusOK {
		if localPreConsumedAmount > 0 {
			s.settleLocal(userID, localPreConsumedAmount, nil, anthropicReq.Model, provider, false, resp.StatusCode, "")
		} else if preConsumedAmount > 0 && s.handlerDeps.Billing != nil {
			metrics.GetMetrics().IncrementSettleCharged(provider, anthropicReq.Model, false)
			errType := "4xx"
			if resp.StatusCode >= 500 {
				errType = "5xx"
			}
			s.Settle(context.Background(), billing.SettleRequest{
				RequestID:   c.GetString("trace_id"),
				UserID:      userID,
				KeyHash:     keyHash,
				Model:       anthropicReq.Model,
				Provider:    provider,
				PreConsumed: preConsumedAmount,
				Success:     false,
				StatusCode:  resp.StatusCode,
				ErrorType:   errType,
				LatencyMs:   time.Since(startTime).Milliseconds(),
			})
		}
	}

	// Record provider health and upstream usage
	if resp.StatusCode == http.StatusOK {
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, anthropicReq.Model, usage, time.Since(startTime).Milliseconds(), false)
		s.recordProviderHealthAsync(c, provider, anthropicReq.Model, resp.StatusCode, time.Since(startTime).Milliseconds(), nil, "")
		providerBreakers.RecordSuccess(provider)
	} else {
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, anthropicReq.Model, nil, time.Since(startTime).Milliseconds(), true)
		s.recordProviderHealthAsync(c, provider, anthropicReq.Model, resp.StatusCode, time.Since(startTime).Milliseconds(), nil, resp.Status)
		providerBreakers.RecordFailure(provider)
	}

	if s.handlerDeps.AuditLogger != nil {
		var cost float64
		if s.handlerDeps.Billing != nil {
			cost = s.handlerDeps.Billing.CalculateCost(usage, anthropicReq.Model, provider)
		}
		s.logAudit(c, audit.AuditEntry{
			Timestamp:      startTime,
			KeyID:          keyID,
			UpstreamKeyID:  upstreamKey.ID,
			APIKeyHash:     keyHash,
			UserID:         userID,
			Provider:       provider,
			Model:          anthropicReq.Model,
			InputTokens:    usage.PromptTokens,
			OutputTokens:   usage.CompletionTokens,
			CacheHitTokens: usage.PromptCacheHitTokens,
			Cost:           cost,
			LatencyMs:      time.Since(startTime).Milliseconds(),
			StatusCode:     resp.StatusCode,
			RequestType:    "claude_native",
		})
	}

	// Record channel affinity
	if s.handlerDeps.AffinityCache != nil {
		cachePriority := getUserPriority(c.GetString("access_mode"))
		s.handlerDeps.AffinityCache.RecordPreferredKeyID(c, anthropicReq.Model, "default", upstreamKey.ID, cachePriority)
		s.handlerDeps.AffinityCache.ObserveUsageCache(c, usage, types.RelayFormatClaude)
	}

	c.Set("tokens_consumed", int64(usage.PromptTokens+usage.CompletionTokens))
	s.recordUsageAsyncFromContext(c, keyHash, upstreamKey.ID, anthropicReq.Model, provider, usage, time.Since(startTime).Milliseconds(), "")

	// Settle the pre-consumed estimate against actual usage on success.
	if resp.StatusCode == http.StatusOK {
		if localPreConsumedAmount > 0 {
			s.settleLocal(userID, localPreConsumedAmount, usage, anthropicReq.Model, provider, true, resp.StatusCode, "")
		} else if preConsumedAmount > 0 && s.handlerDeps.Billing != nil {
			metrics.GetMetrics().IncrementSettleCharged(provider, anthropicReq.Model, true)
			s.Settle(context.Background(), billing.SettleRequest{
				RequestID:   c.GetString("trace_id"),
				UserID:      userID,
				KeyHash:     keyHash,
				Model:       anthropicReq.Model,
				Provider:    provider,
				PreConsumed: preConsumedAmount,
				ActualUsage: usage,
				Success:     true,
				StatusCode:  resp.StatusCode,
				LatencyMs:   time.Since(startTime).Milliseconds(),
			})
		}
	}

	// Capture retention for Claude native passthrough
	setRetentionContext(c, "anthropic", "anthropic", anthropicReq.Model, false)
	s.captureRetention(c, bodyBytes, body, provider, anthropicReq.Model, upstreamKey.ID, usage, time.Since(startTime).Milliseconds(), resp.StatusCode, false, "claude_native")
}

// handleAnthropicToOpenAI handles Anthropic client -> OpenAI/Other upstream via full conversion
func (s *HandlerService) handleAnthropicToOpenAI(c *gin.Context, anthropicReq *convert.AnthropicRequest, hubReq *dto.GeneralOpenAIRequest, provider string, upstreamKey *keypool.KeyInternal, upstreamModelName, keyID, userID, keyHash string, startTime time.Time) {
	// Replace model name with provider-specific name
	hubReq.Model = upstreamModelName
	upstreamBaseURL := s.resolveBaseURL(upstreamKey, provider)

	adaptorCfg := &relay.AdaptorConfig{
		BaseURL:      upstreamBaseURL,
		APIKey:       upstreamKey.ActualKey,
		ExtraHeaders: s.filterClientHeaders(c),
	}
	adaptor := relay.GetAdaptor(provider, adaptorCfg)

	// Transform request for provider
	providerReq := adaptor.ToProvider(*hubReq)

	// Apply post-conversion plugins to the upstream request body.
	upstreamBody, err := json.Marshal(providerReq)
	if err != nil {
		s.handlerDeps.Logger.Error("Failed to marshal upstream request", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"message": "Failed to marshal upstream request",
			"type":    "internal_error",
		}})
		return
	}
	upstreamBody = s.pluginChain.Apply(upstreamBody, plugin.Context{
		ClientFormat:   "anthropic",
		UpstreamFormat: string(adaptor.GetRelayFormat()),
		HostKey:        upstreamKey.ID,
		ClientName:     userID,
	})

	isStream := hubReq.Stream != nil && *hubReq.Stream

	if isStream {
		s.handleAnthropicStreamingResponse(c, adaptor, upstreamBody, *hubReq, provider, keyID, userID, keyHash, upstreamKey, anthropicReq.Model, startTime)
		return
	}

	// Non-streaming: pre-consume estimated input cost before upstream request.
	var inputTokens int
	if s.handlerDeps.TokenCounter != nil {
		inputTokens = s.handlerDeps.TokenCounter.EstimateMessages(hubReq.Messages, anthropicReq.Model)
	}
	var preConsumedAmount int64
	var localPreConsumedAmount int64
	localPreConsumedAmount, used, rejected := s.preConsumeLocal(c, userID, c.GetString("key_type"), anthropicReq.Model, provider, inputTokens)
	if rejected {
		return
	}
	if !used {
		if s.cfg.Billing.PreConsumeEnabled && s.handlerDeps.Billing != nil && inputTokens > 0 && c.GetString("key_type") != "subscription" {
			traceID := c.GetString("request_id")
			if traceID == "" {
				traceID = fmt.Sprintf("pc-%d", time.Now().UnixNano())
			}
			c.Set("trace_id", traceID)
			pcCtx, pcCancel := billing.NewPreConsumeContext(c.Request.Context())
			pcResult := s.handlerDeps.Billing.PreConsume(pcCtx, billing.PreConsumeRequest{
				UserID:      userID,
				KeyHash:     keyHash,
				Model:       anthropicReq.Model,
				Provider:    provider,
				InputTokens: inputTokens,
				RequestID:   traceID,
				Margin:      s.cfg.Billing.PreConsumeMargin,
			})
			pcCancel()
			if pcResult.Rejected {
				metrics.GetMetrics().IncrementPreconsumeRejected()
				c.JSON(http.StatusPaymentRequired, gin.H{"error": gin.H{
					"message": "Insufficient balance for this request",
					"type":    "billing_error",
					"code":    "INSUFFICIENT_BALANCE",
				}})
				return
			}
			preConsumedAmount = pcResult.Amount
		}
	}

	// settleOnError is a helper to refund or zero-settle pre-consumed amounts on failure.
	settleOnError := func(statusCode int, errType string) {
		if localPreConsumedAmount > 0 {
			s.settleLocal(userID, localPreConsumedAmount, nil, anthropicReq.Model, provider, false, statusCode, errType)
		} else if preConsumedAmount > 0 && s.handlerDeps.Billing != nil {
			metrics.GetMetrics().IncrementSettleCharged(provider, anthropicReq.Model, false)
			s.Settle(context.Background(), billing.SettleRequest{
				RequestID:   c.GetString("trace_id"),
				UserID:      userID,
				KeyHash:     keyHash,
				Model:       anthropicReq.Model,
				Provider:    provider,
				PreConsumed: preConsumedAmount,
				Success:     false,
				StatusCode:  statusCode,
				ErrorType:   errType,
				LatencyMs:   time.Since(startTime).Milliseconds(),
			})
		}
	}

	// Non-streaming request
	resp, err := adaptor.DoRequestBytes(upstreamBody, false)

	// In-request failover (anthropic→openai non-stream; fully buffered → retryable
	// until the client response is written). See failover.go.
	{
		fo := newFailoverState(s.cfg.Failover, anthropicReq.Model)
		foUserPriority := getUserPriority(c.GetString("access_mode"))
		for {
			retryStatus := 0
			transportErr := false
			if err != nil {
				transportErr = true
			} else if resp != nil && resp.StatusCode >= 400 && resp.StatusCode != http.StatusOK {
				retryStatus = resp.StatusCode
			} else {
				break
			}
			fo.recordAttempt(provider, upstreamKey.ID)
			if !fo.shouldRetry(retryStatus, transportErr, false) {
				break
			}
			if s.keyPoolManager != nil {
				if retryStatus == http.StatusTooManyRequests {
					s.keyPoolManager.ReportKeyRateLimited(provider, upstreamKey.ID, anthropicReq.Model, 0)
				} else {
					s.keyPoolManager.ReportKeyFailure(provider, upstreamKey.ID, anthropicReq.Model, retryStatus, "upstream_error", "upstream_error", "failover_retry")
				}
			}
			s.recordUpstreamUsageAsync(upstreamKey.ID, provider, hubReq.Model, nil, time.Since(startTime).Milliseconds(), true)
			if resp != nil {
				_ = resp.Body.Close()
			}
			np, nk, num, _, rerr := s.resolveProviderAndKeyExcluding(c.Request.Context(), anthropicReq.Model, foUserPriority, "", fo.excluded)
			if rerr != nil || nk == nil {
				break
			}
			provider, upstreamKey = np, nk
			hubReq.Model = num
			c.Set("is_fallback", true)
			s.handlerDeps.Logger.Warn("Failover retry (anthropic->openai)",
				zap.String("model", anthropicReq.Model), zap.String("to_provider", provider),
				zap.Int("attempt", fo.attempt), zap.Int("last_status", retryStatus))
			adaptor = relay.GetAdaptor(provider, &relay.AdaptorConfig{
				BaseURL:      s.resolveBaseURL(upstreamKey, provider),
				APIKey:       upstreamKey.ActualKey,
				ExtraHeaders: s.filterClientHeaders(c),
			})
			nb, mErr := json.Marshal(adaptor.ToProvider(*hubReq))
			if mErr != nil {
				break
			}
			upstreamBody = s.pluginChain.Apply(nb, plugin.Context{
				ClientFormat: "anthropic", UpstreamFormat: string(adaptor.GetRelayFormat()),
				HostKey: upstreamKey.ID, ClientName: userID,
			})
			resp, err = adaptor.DoRequestBytes(upstreamBody, false)
		}
	}

	if err != nil {
		settleOnError(0, classifyTransportError(err))
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, hubReq.Model, nil, time.Since(startTime).Milliseconds(), true)
		upErr := UpstreamUnreachableError()
		c.Data(upErr.ClientStatus, "application/json", upErr.JSON())
		return
	}

	body, err := adaptor.ReadResponse(resp)
	if err != nil {
		errType := "4xx"
		if resp.StatusCode >= 500 {
			errType = "5xx"
		}
		settleOnError(resp.StatusCode, errType)
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, hubReq.Model, nil, time.Since(startTime).Milliseconds(), true)
		if s.keyPoolManager != nil {
			s.reportUpstreamKeyError(resp, provider, upstreamKey.ID, anthropicReq.Model, "")
		}
		s.recordProviderHealthAsync(c, provider, anthropicReq.Model, resp.StatusCode, time.Since(startTime).Milliseconds(), nil, "")
		upErr := ClassifyUpstreamError(resp.StatusCode)
		c.Data(upErr.ClientStatus, "application/json", upErr.JSON())
		return
	}

	// Convert OpenAI response to Anthropic format
	openaiResp := adaptor.FromProvider(body, *hubReq)
	if openaiResp == nil {
		settleOnError(http.StatusInternalServerError, "parse_error")
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"message": "Failed to parse upstream response",
			"type":    "internal_error",
		}})
		return
	}
	// Bedrock responses carry no model name; echo the client-facing logical
	// model rather than leaving it empty (or leaking the upstream ID).
	openaiResp.Model = anthropicReq.Model

	// Track actually-called tools (response side) for the usage log.
	for i := range openaiResp.Choices {
		trackOpenAIMessageToolCalls(c, openaiResp.Choices[i].Message)
	}

	anthropicResp, err := convert.OpenAIToAnthropicResponse(openaiResp)
	if err != nil {
		settleOnError(http.StatusInternalServerError, "conversion_error")
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"message": "Failed to convert response: " + err.Error(),
			"type":    "internal_error",
		}})
		return
	}

	anthropicBody, _ := json.Marshal(anthropicResp)
	c.Data(http.StatusOK, "application/json", anthropicBody)

	// Extract usage for audit
	usage := &dto.Usage{}
	if openaiResp != nil {
		usage.PromptTokens = openaiResp.Usage.PromptTokens
		usage.CompletionTokens = openaiResp.Usage.CompletionTokens
		usage.TotalTokens = openaiResp.Usage.TotalTokens
		usage.PromptTokensDetails = openaiResp.Usage.PromptTokensDetails
		usage.PromptCacheHitTokens = openaiResp.Usage.PromptCacheHitTokens
	}

	// Settle pre-consumed amount against actual usage.
	if localPreConsumedAmount > 0 {
		s.settleLocal(userID, localPreConsumedAmount, usage, anthropicReq.Model, provider, true, http.StatusOK, "")
	} else if preConsumedAmount > 0 && s.handlerDeps.Billing != nil {
		metrics.GetMetrics().IncrementSettleCharged(provider, anthropicReq.Model, true)
		s.Settle(context.Background(), billing.SettleRequest{
			RequestID:   c.GetString("trace_id"),
			UserID:      userID,
			KeyHash:     keyHash,
			Model:       anthropicReq.Model,
			Provider:    provider,
			ActualUsage: usage,
			PreConsumed: preConsumedAmount,
			Success:     true,
			StatusCode:  http.StatusOK,
			LatencyMs:   time.Since(startTime).Milliseconds(),
		})
	}

	// Record provider health success
	s.recordUpstreamUsageAsync(upstreamKey.ID, provider, hubReq.Model, usage, time.Since(startTime).Milliseconds(), false)
	s.recordProviderHealthAsync(c, provider, anthropicReq.Model, http.StatusOK, time.Since(startTime).Milliseconds(), nil, "")
	providerBreakers.RecordSuccess(provider)

	// Record audit
	if s.handlerDeps.AuditLogger != nil {
		var cost float64
		if s.handlerDeps.Billing != nil {
			cost = s.handlerDeps.Billing.CalculateCost(usage, anthropicReq.Model, provider)
		}
		s.logAudit(c, audit.AuditEntry{
			Timestamp:      startTime,
			KeyID:          keyID,
			UpstreamKeyID:  upstreamKey.ID,
			APIKeyHash:     keyHash,
			UserID:         userID,
			Provider:       provider,
			Model:          anthropicReq.Model,
			InputTokens:    usage.PromptTokens,
			OutputTokens:   usage.CompletionTokens,
			CacheHitTokens: usage.PromptCacheHitTokens,
			Cost:           cost,
			LatencyMs:      time.Since(startTime).Milliseconds(),
			StatusCode:     http.StatusOK,
			RequestType:    "claude_to_openai",
		})
	}

	// Record channel affinity
	if s.handlerDeps.AffinityCache != nil {
		cachePriority := getUserPriority(c.GetString("access_mode"))
		s.handlerDeps.AffinityCache.RecordPreferredKeyID(c, anthropicReq.Model, "default", upstreamKey.ID, cachePriority)
		s.handlerDeps.AffinityCache.ObserveUsageCache(c, usage, adaptor.GetRelayFormat())
	}

	c.Set("tokens_consumed", int64(usage.PromptTokens+usage.CompletionTokens))
	s.recordUsageAsyncFromContext(c, keyHash, upstreamKey.ID, anthropicReq.Model, provider, usage, time.Since(startTime).Milliseconds(), "")

	// Capture retention for Anthropic client -> OpenAI conversion
	var anthropicRequestBody []byte
	if v, ok := c.Get("request_body"); ok {
		if b, ok := v.([]byte); ok {
			anthropicRequestBody = b
		}
	}
	setRetentionContext(c, "anthropic", string(adaptor.GetRelayFormat()), hubReq.Model, true)
	s.captureRetention(c, anthropicRequestBody, body, provider, anthropicReq.Model, upstreamKey.ID, usage, time.Since(startTime).Milliseconds(), http.StatusOK, false, "claude_to_openai")
}

// handleAnthropicStreamingResponse handles streaming Anthropic client -> OpenAI upstream conversion
func (s *HandlerService) handleAnthropicStreamingResponse(c *gin.Context, adaptor relay.Adaptor, upstreamBody []byte, req dto.GeneralOpenAIRequest, provider, keyID, userID, keyHash string, upstreamKey *keypool.KeyInternal, originalModel string, startTime time.Time) {
	var inputTokens int
	if s.handlerDeps.TokenCounter != nil {
		inputTokens = s.handlerDeps.TokenCounter.EstimateMessages(req.Messages, originalModel)
	}

	// Pre-consume: reserve estimated input cost before streaming.
	var preConsumedAmount int64
	var localPreConsumedAmount int64
	localPreConsumedAmount, used, rejected := s.preConsumeLocal(c, userID, c.GetString("key_type"), originalModel, provider, inputTokens)
	if rejected {
		return
	}
	if !used {
		if s.cfg.Billing.PreConsumeEnabled && s.handlerDeps.Billing != nil && inputTokens > 0 && c.GetString("key_type") != "subscription" {
			traceID := c.GetString("request_id")
			if traceID == "" {
				traceID = fmt.Sprintf("pc-%d", time.Now().UnixNano())
			}
			c.Set("trace_id", traceID)
			pcCtx, pcCancel := billing.NewPreConsumeContext(c.Request.Context())
			pcResult := s.handlerDeps.Billing.PreConsume(pcCtx, billing.PreConsumeRequest{
				UserID:      userID,
				KeyHash:     keyHash,
				Model:       originalModel,
				Provider:    provider,
				InputTokens: inputTokens,
				RequestID:   traceID,
				Margin:      s.cfg.Billing.PreConsumeMargin,
			})
			pcCancel()
			if pcResult.Rejected {
				metrics.GetMetrics().IncrementPreconsumeRejected()
				c.JSON(http.StatusPaymentRequired, gin.H{"error": gin.H{
					"message": "Insufficient balance for this request",
					"type":    "billing_error",
					"code":    "INSUFFICIENT_BALANCE",
				}})
				return
			}
			preConsumedAmount = pcResult.Amount
		}
	}

	resp, err := adaptor.DoRequestBytesWithContext(c.Request.Context(), upstreamBody, true)

	// In-request failover (anthropic→openai convert streaming; pre-first-byte).
	{
		fo := newFailoverState(s.cfg.Failover, originalModel)
		foUserPriority := getUserPriority(c.GetString("access_mode"))
		for {
			retryStatus := 0
			transportErr := false
			if err != nil {
				if errors.Is(err, context.Canceled) {
					break
				}
				transportErr = true
			} else if resp != nil && resp.StatusCode != http.StatusOK {
				retryStatus = resp.StatusCode
			} else {
				break
			}
			fo.recordAttempt(provider, upstreamKey.ID)
			if !fo.shouldRetry(retryStatus, transportErr, false) {
				break
			}
			if s.keyPoolManager != nil {
				if retryStatus == http.StatusTooManyRequests {
					s.keyPoolManager.ReportKeyRateLimited(provider, upstreamKey.ID, originalModel, 0)
				} else {
					s.keyPoolManager.ReportKeyFailure(provider, upstreamKey.ID, originalModel, retryStatus, "upstream_error", "upstream_error", "failover_retry")
				}
			}
			s.recordUpstreamUsageAsync(upstreamKey.ID, provider, originalModel, nil, time.Since(startTime).Milliseconds(), true)
			if resp != nil {
				_ = resp.Body.Close()
			}
			np, nk, num, _, rerr := s.resolveProviderAndKeyExcluding(c.Request.Context(), originalModel, foUserPriority, "", fo.excluded)
			if rerr != nil || nk == nil {
				break
			}
			provider, upstreamKey = np, nk
			req.Model = num
			c.Set("is_fallback", true)
			s.handlerDeps.Logger.Warn("Failover retry (anthropic stream)",
				zap.String("model", originalModel), zap.String("to_provider", provider),
				zap.Int("attempt", fo.attempt), zap.Int("last_status", retryStatus))
			adaptor = relay.GetAdaptor(provider, &relay.AdaptorConfig{
				BaseURL:      s.resolveBaseURL(upstreamKey, provider),
				APIKey:       upstreamKey.ActualKey,
				ExtraHeaders: s.filterClientHeaders(c),
			})
			nb, mErr := json.Marshal(adaptor.ToProvider(req))
			if mErr != nil {
				break
			}
			upstreamBody = s.pluginChain.Apply(nb, plugin.Context{
				ClientFormat: "anthropic", UpstreamFormat: string(adaptor.GetRelayFormat()),
				HostKey: upstreamKey.ID, ClientName: userID,
			})
			resp, err = adaptor.DoRequestBytesWithContext(c.Request.Context(), upstreamBody, true)
		}
	}

	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.handlerDeps.Logger.Warn("Client disconnected before Anthropic streaming request completed", zap.Error(err))
			if localPreConsumedAmount > 0 {
				s.settleLocal(userID, localPreConsumedAmount, nil, originalModel, provider, false, 499, "client_disconnect")
			} else if preConsumedAmount > 0 && s.handlerDeps.Billing != nil {
				metrics.GetMetrics().IncrementSettleCharged(provider, originalModel, false)
				s.Settle(context.Background(), billing.SettleRequest{
					RequestID:   c.GetString("trace_id"),
					UserID:      userID,
					KeyHash:     keyHash,
					Model:       originalModel,
					Provider:    provider,
					PreConsumed: preConsumedAmount,
					Success:     false,
					StatusCode:  499,
					ErrorType:   "client_disconnect",
					LatencyMs:   time.Since(startTime).Milliseconds(),
				})
			}
			return
		}
		if localPreConsumedAmount > 0 {
			s.settleLocal(userID, localPreConsumedAmount, nil, originalModel, provider, false, 0, classifyTransportError(err))
		} else if preConsumedAmount > 0 && s.handlerDeps.Billing != nil {
			metrics.GetMetrics().IncrementSettleCharged(provider, originalModel, false)
			s.Settle(context.Background(), billing.SettleRequest{
				RequestID:   c.GetString("trace_id"),
				UserID:      userID,
				KeyHash:     keyHash,
				Model:       originalModel,
				Provider:    provider,
				PreConsumed: preConsumedAmount,
				Success:     false,
				StatusCode:  0,
				ErrorType:   classifyTransportError(err),
				LatencyMs:   time.Since(startTime).Milliseconds(),
			})
		}
		errorBody := UpstreamUnreachableError().JSON()
		c.Data(http.StatusBadGateway, "application/json", errorBody)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	// Forward upstream non-200 errors
	if resp.StatusCode != http.StatusOK {
		errorBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		if len(errorBody) == 0 {
			errorBody = []byte(fmt.Sprintf(`{"error":{"message":"upstream error (status %d)","type":"upstream_error"}}`, resp.StatusCode))
		}
		if localPreConsumedAmount > 0 {
			s.settleLocal(userID, localPreConsumedAmount, nil, originalModel, provider, false, resp.StatusCode, "")
		} else if preConsumedAmount > 0 && s.handlerDeps.Billing != nil {
			metrics.GetMetrics().IncrementSettleCharged(provider, originalModel, false)
			errType := "4xx"
			if resp.StatusCode >= 500 {
				errType = "5xx"
			}
			s.Settle(context.Background(), billing.SettleRequest{
				RequestID:   c.GetString("trace_id"),
				UserID:      userID,
				KeyHash:     keyHash,
				Model:       originalModel,
				Provider:    provider,
				PreConsumed: preConsumedAmount,
				Success:     false,
				StatusCode:  resp.StatusCode,
				ErrorType:   errType,
				LatencyMs:   time.Since(startTime).Milliseconds(),
			})
		}
		upErr := ClassifyUpstreamError(resp.StatusCode)
		c.Data(upErr.ClientStatus, "application/json", upErr.JSON())
		if s.keyPoolManager != nil {
			s.reportUpstreamKeyError(resp, provider, upstreamKey.ID, req.Model, string(errorBody[:min(500, len(errorBody))]))
		}
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, req.Model, nil, time.Since(startTime).Milliseconds(), true)
		return
	}

	upstreamConnected := time.Now()

	// Set SSE headers for Anthropic format
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Transfer-Encoding", "chunked")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		if localPreConsumedAmount > 0 {
			s.settleLocal(userID, localPreConsumedAmount, nil, originalModel, provider, false, http.StatusInternalServerError, "internal_error")
		} else if preConsumedAmount > 0 && s.handlerDeps.Billing != nil {
			metrics.GetMetrics().IncrementSettleCharged(provider, originalModel, false)
			s.Settle(context.Background(), billing.SettleRequest{
				RequestID:   c.GetString("trace_id"),
				UserID:      userID,
				KeyHash:     keyHash,
				Model:       originalModel,
				Provider:    provider,
				PreConsumed: preConsumedAmount,
				Success:     false,
				StatusCode:  http.StatusInternalServerError,
				ErrorType:   "internal_error",
				LatencyMs:   time.Since(startTime).Milliseconds(),
			})
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Streaming not supported"})
		return
	}

	reader := bufio.NewReader(resp.Body)
	state := &convert.AnthropicStreamState{Model: originalModel}
	var totalUsage dto.Usage
	// Assembles fragmented tool-call names across chunks (response-side tracking).
	toolFrags := map[int]*strings.Builder{}
	var firstTokenLatency int64

	streamCtx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Minute)
	defer cancel()

	// If the client disconnects or the stream timeout fires, force the upstream
	// response body closed so the background reader goroutine exits promptly
	// instead of blocking until the upstream closes the connection.
	stopReader := context.AfterFunc(streamCtx, func() {
		_ = resp.Body.Close()
	})
	defer stopReader()

	// Stream line reader in background goroutine so we can interleave heartbeat ticks.
	type streamLine struct {
		line string
		err  error
	}
	chunkCh := make(chan streamLine, 1)
	go func() {
		defer close(chunkCh)
		for {
			line, err := reader.ReadString('\n')
			if line != "" {
				select {
				case chunkCh <- streamLine{line: line}:
				case <-streamCtx.Done():
					return
				}
			}
			if err != nil {
				select {
				case chunkCh <- streamLine{err: err}:
				case <-streamCtx.Done():
				}
				return
			}
		}
	}()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-streamCtx.Done():
			s.handlerDeps.Logger.Warn("Anthropic streaming timeout or client disconnected", zap.Error(streamCtx.Err()))
			// Bill the partial usage already streamed to the client; otherwise
			// a client disconnecting mid-stream gets all output tokens for free
			// and leaves no usage/audit trail.
			// Note tools called so far (idempotent flush) before the partial record.
			flushStreamToolCalls(c, toolFrags)
			hasPartialUsage := totalUsage.PromptTokens+totalUsage.CompletionTokens > 0
			if hasPartialUsage {
				c.Set("tokens_consumed", int64(totalUsage.PromptTokens+totalUsage.CompletionTokens))
				s.recordUsageAsyncFromContext(c, keyHash, upstreamKey.ID, originalModel, provider, &totalUsage, time.Since(startTime).Milliseconds(), "")
				s.recordUpstreamUsageAsync(upstreamKey.ID, provider, req.Model, &totalUsage, time.Since(startTime).Milliseconds(), true)
				if s.handlerDeps.AuditLogger != nil {
					var partialCost float64
					if s.handlerDeps.Billing != nil {
						partialCost = s.handlerDeps.Billing.CalculateCost(&totalUsage, req.Model, provider)
					}
					s.logAudit(c, audit.AuditEntry{
						Timestamp:      startTime,
						KeyID:          keyID,
						UpstreamKeyID:  upstreamKey.ID,
						APIKeyHash:     keyHash,
						UserID:         userID,
						Provider:       provider,
						ChannelID:      0,
						Model:          req.Model,
						InputTokens:    totalUsage.PromptTokens,
						OutputTokens:   totalUsage.CompletionTokens,
						CacheHitTokens: totalUsage.PromptCacheHitTokens,
						Cost:           partialCost,
						LatencyMs:      time.Since(startTime).Milliseconds(),
						StatusCode:     resp.StatusCode,
						IsStream:       true,
						ActualGroupID:  c.GetString("group_id"),
					})
				}
			}
			if localPreConsumedAmount > 0 {
				if hasPartialUsage {
					s.settleLocal(userID, localPreConsumedAmount, &totalUsage, originalModel, provider, true, resp.StatusCode, "")
				} else {
					s.settleLocal(userID, localPreConsumedAmount, nil, originalModel, provider, false, 0, "timeout")
				}
			} else if preConsumedAmount > 0 && s.handlerDeps.Billing != nil {
				if hasPartialUsage {
					s.settleSuccess(c, preConsumedAmount, userID, keyHash, originalModel, provider, &totalUsage, resp.StatusCode, time.Since(startTime).Milliseconds())
				} else {
					metrics.GetMetrics().IncrementSettleCharged(provider, originalModel, false)
					s.Settle(context.Background(), billing.SettleRequest{
						RequestID:   c.GetString("trace_id"),
						UserID:      userID,
						KeyHash:     keyHash,
						Model:       originalModel,
						Provider:    provider,
						PreConsumed: preConsumedAmount,
						Success:     false,
						StatusCode:  0,
						ErrorType:   "timeout",
						LatencyMs:   time.Since(startTime).Milliseconds(),
					})
				}
			}
			return
		case <-heartbeat.C:
			_, _ = c.Writer.WriteString(": heartbeat\n\n")
			flusher.Flush()
			continue
		case sl, ok := <-chunkCh:
			if !ok {
				// Channel closed — reader goroutine exited
				goto streamDone
			}
			if sl.err != nil {
				if sl.err == io.EOF {
					goto streamDone
				}
				s.handlerDeps.Logger.Error("Error reading Anthropic stream", zap.Error(sl.err))
				goto streamDone
			}

			line := strings.TrimSpace(sl.line)

			if strings.HasPrefix(line, "data: ") {
				data := strings.TrimPrefix(line, "data: ")

				if firstTokenLatency == 0 {
					firstTokenLatency = time.Since(upstreamConnected).Milliseconds()
				}

				if data == "[DONE]" {
					// OpenAIChunkToAnthropicEvent already emits message_stop on
					// finish_reason; only send the done marker when the upstream
					// stream ended without one (truncated stream).
					if !state.HasStopped {
						doneEvent := convert.AnthropicStreamDone()
						_, _ = c.Writer.Write([]byte(doneEvent + "\n"))
						flusher.Flush()
					}
					goto streamDone
				}

				// Convert OpenAI SSE chunk to Anthropic SSE events
				events, err := convert.OpenAIChunkToAnthropicEvent([]byte(data), state)
				if err != nil {
					continue
				}

				for _, event := range events {
					_, _ = c.Writer.Write([]byte(event + "\n"))
					flusher.Flush()
				}

				// Extract usage
				streamResp := pool.GetChatCompletionsStreamResponse()
				if err := json.Unmarshal([]byte(data), streamResp); err == nil {
					// Track fragmented tool-call names for the usage log.
					trackOpenAIStreamToolCalls(toolFrags, streamResp)
					if streamResp.Usage != nil {
						// Take the last non-empty usage (see main streaming loop).
						if streamResp.Usage.PromptTokens != 0 {
							totalUsage.PromptTokens = streamResp.Usage.PromptTokens
						}
						if streamResp.Usage.CompletionTokens != 0 {
							totalUsage.CompletionTokens = streamResp.Usage.CompletionTokens
						}
						totalUsage.PromptCacheHitTokens = streamResp.Usage.PromptCacheHitTokens
						// Propagate cache details (cached_tokens /
						// cached_creation_tokens) and reasoning tokens like the
						// main streaming loop does, so portal billing can apply
						// cache read/write rates instead of full input price.
						totalUsage.PromptTokensDetails = streamResp.Usage.PromptTokensDetails
						totalUsage.CompletionTokenDetails = streamResp.Usage.CompletionTokenDetails
					}
				}
				pool.PutChatCompletionsStreamResponse(streamResp)
			}
		}
	}

streamDone:
	// Note the tools actually called during the stream (response side).
	flushStreamToolCalls(c, toolFrags)

	// Record affinity
	if s.handlerDeps.AffinityCache != nil {
		cachePriority := getUserPriority(c.GetString("access_mode"))
		s.handlerDeps.AffinityCache.RecordPreferredKeyID(c, originalModel, "default", upstreamKey.ID, cachePriority)
		s.handlerDeps.AffinityCache.ObserveUsageCache(c, &totalUsage, adaptor.GetRelayFormat())
	}

	s.recordUpstreamUsageAsync(upstreamKey.ID, provider, req.Model, &totalUsage, time.Since(startTime).Milliseconds(), false)
	s.recordProviderHealthAsync(c, provider, originalModel, resp.StatusCode, time.Since(startTime).Milliseconds(), nil, "")
	providerBreakers.RecordSuccess(provider)
	c.Set("tokens_consumed", int64(totalUsage.PromptTokens+totalUsage.CompletionTokens))

	// Set first-token latency in context before usage recording.
	c.Set("first_token_latency_ms", firstTokenLatency)

	s.recordUsageAsyncFromContext(c, keyHash, upstreamKey.ID, originalModel, provider, &totalUsage, time.Since(startTime).Milliseconds(), "")

	// Settle the locally pre-consumed estimate against actual streaming usage.
	if localPreConsumedAmount > 0 {
		s.settleLocal(userID, localPreConsumedAmount, &totalUsage, originalModel, provider, true, resp.StatusCode, "")
	} else if preConsumedAmount > 0 && s.handlerDeps.Billing != nil {
		metrics.GetMetrics().IncrementSettleCharged(provider, originalModel, true)
		s.Settle(context.Background(), billing.SettleRequest{
			RequestID:   c.GetString("trace_id"),
			UserID:      userID,
			KeyHash:     keyHash,
			Model:       originalModel,
			Provider:    provider,
			PreConsumed: preConsumedAmount,
			ActualUsage: &totalUsage,
			Success:     true,
			StatusCode:  resp.StatusCode,
			LatencyMs:   time.Since(startTime).Milliseconds(),
		})
	}

	// Capture retention metadata for streaming Anthropic->OpenAI conversion
	var requestBody []byte
	if v, ok := c.Get("request_body"); ok {
		if b, ok := v.([]byte); ok {
			requestBody = b
		}
	}
	setRetentionContext(c, "anthropic", string(adaptor.GetRelayFormat()), req.Model, true)
	s.captureRetention(c, requestBody, nil, provider, originalModel, upstreamKey.ID, &totalUsage, time.Since(startTime).Milliseconds(), resp.StatusCode, true, "claude_to_openai_stream")
}

// rerouteMediaFromChat translates a chat/completions request that targets a
// media generation model into the matching media endpoint request and
// dispatches it internally. Tencent WorkBuddy and similar clients send image
// (and video) tasks to the chat endpoint without switching paths; rejecting
// them broke the client, so we extract the prompt from either the images-style
// "prompt" field or the last user chat message and delegate to the dedicated
// handler, which owns pre-consume, dispatch, and billing.
func (s *HandlerService) rerouteMediaFromChat(c *gin.Context, bodyBytes []byte, model, mediaType string) {
	var probe struct {
		Prompt   string `json:"prompt"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		N       int    `json:"n"`
		Size    string `json:"size"`
		Quality string `json:"quality"`
	}
	_ = json.Unmarshal(bodyBytes, &probe)

	// extractPromptText handles both plain-string content and multimodal
	// content-parts arrays ([{"type":"text","text":"..."}, ...]) — clients
	// like WorkBuddy send the latter even for pure text prompts.
	extractPromptText := func(raw json.RawMessage) string {
		if len(raw) == 0 {
			return ""
		}
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		var parts []struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Content string `json:"content"`
		}
		if json.Unmarshal(raw, &parts) == nil {
			var b strings.Builder
			for _, p := range parts {
				t := p.Text
				if t == "" {
					t = p.Content
				}
				// Match "text", "input_text", "output_text", etc.
				if t != "" && (p.Type == "" || strings.Contains(p.Type, "text")) {
					if b.Len() > 0 {
						b.WriteString("\n")
					}
					b.WriteString(t)
				}
			}
			return b.String()
		}
		return ""
	}

	prompt := strings.TrimSpace(probe.Prompt)
	if prompt == "" {
		for i := len(probe.Messages) - 1; i >= 0; i-- {
			if probe.Messages[i].Role == "user" {
				if p := strings.TrimSpace(extractPromptText(probe.Messages[i].Content)); p != "" {
					prompt = p
					break
				}
			}
		}
	}
	if prompt == "" {
		// Diagnostic: record the payload shape so we can extend extraction
		// without guessing (bodies can be 100k+ tokens, so log structure only).
		var diag struct {
			Keys     []string `json:"-"`
			MsgRoles []string `json:"-"`
			MsgTypes []string `json:"-"`
		}
		var top map[string]json.RawMessage
		if json.Unmarshal(bodyBytes, &top) == nil {
			for k := range top {
				diag.Keys = append(diag.Keys, k)
			}
		}
		for _, m := range probe.Messages {
			diag.MsgRoles = append(diag.MsgRoles, m.Role+":"+contentKind(m.Content))
		}
		s.handlerDeps.Logger.Warn("MEDIA_PROMPT_MISSING payload shape",
			zap.String("model", model),
			zap.Strings("body_keys", diag.Keys),
			zap.Strings("messages", diag.MsgRoles))
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": fmt.Sprintf("model '%s' is a %s generation model but no prompt could be extracted from the request (expected 'prompt' or a user message)", model, mediaType),
			"type":    "invalid_request_error",
			"code":    "MEDIA_PROMPT_MISSING",
		}})
		return
	}

	out := map[string]interface{}{"model": model, "prompt": prompt}
	if probe.N > 0 {
		out["n"] = probe.N
	}
	if probe.Size != "" {
		out["size"] = probe.Size
	}
	if probe.Quality != "" {
		out["quality"] = probe.Quality
	}
	payload, err := json.Marshal(out)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"message": "failed to build media request",
			"type":    "internal_error",
		}})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(payload))
	s.handlerDeps.Logger.Info("Rerouting chat request to media endpoint",
		zap.String("model", model),
		zap.String("media_type", mediaType),
		zap.String("path", c.Request.URL.Path))

	switch mediaType {
	case "image":
		s.handleImageGenerations(c)
	case "video":
		s.handleVideoGenerations(c)
	case "audio":
		s.handleAudioGenerations(c)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": fmt.Sprintf("unsupported media type '%s' for model '%s'", mediaType, model),
			"type":    "invalid_request_error",
		}})
	}
}

// contentKind classifies a raw message content field for diagnostics:
// "string", "parts", "object", or "empty".
func contentKind(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "empty"
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return "string"
	}
	var arr []json.RawMessage
	if json.Unmarshal(raw, &arr) == nil {
		return "parts"
	}
	return "object"
}

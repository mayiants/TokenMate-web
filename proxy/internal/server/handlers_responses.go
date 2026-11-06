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
	"sync"
	"time"

	"github.com/ai-proxy/ai-proxy-go/dto"
	"github.com/ai-proxy/ai-proxy-go/internal/audit"
	"github.com/ai-proxy/ai-proxy-go/pkg/billing"
	"github.com/ai-proxy/ai-proxy-go/pkg/convert"
	"github.com/ai-proxy/ai-proxy-go/pkg/keypool"
	"github.com/ai-proxy/ai-proxy-go/pkg/metrics"
	"github.com/ai-proxy/ai-proxy-go/pkg/plugin"
	"github.com/ai-proxy/ai-proxy-go/pkg/relay"
	"github.com/ai-proxy/ai-proxy-go/pkg/token"
	"github.com/ai-proxy/ai-proxy-go/types"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type responsesRouteCache struct {
	mu      sync.RWMutex
	entries map[string]responsesRouteEntry
}

type responsesRouteEntry struct {
	passthrough bool
	expiresAt   time.Time
}

var responsesRouteCacheInstance = &responsesRouteCache{
	entries: make(map[string]responsesRouteEntry),
}

func (c *responsesRouteCache) Get(provider, model string) (bool, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[provider+":"+model]
	if !ok || time.Now().After(e.expiresAt) {
		return false, false
	}
	return e.passthrough, true
}

func (c *responsesRouteCache) Set(provider, model string, passthrough bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[provider+":"+model] = responsesRouteEntry{
		passthrough: passthrough,
		expiresAt:   time.Now().Add(time.Hour),
	}
}

func (c *responsesRouteCache) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for k, v := range c.entries {
			if now.After(v.expiresAt) {
				delete(c.entries, k)
			}
		}
		c.mu.Unlock()
	}
}

func init() {
	go responsesRouteCacheInstance.cleanupLoop()
}

// estimateResponsesInputTokens estimates the input tokens for a Responses API
// request by converting it to the OpenAI Chat Completions format and reusing
// the existing token counter. This avoids duplicating tokenization logic and
// keeps the estimate consistent with chat completions billing.
func estimateResponsesInputTokens(counter *token.Counter, req *convert.ResponsesRequest) int {
	if counter == nil || req == nil {
		return 0
	}
	hubReq, err := convert.ResponsesToOpenAI(req)
	if err != nil {
		return 0
	}
	return counter.EstimateMessages(hubReq.Messages, req.Model)
}

// handleResponses - Handle OpenAI Responses API (new format)
// Routes to either passthrough mode (for Responses-only models) or conversion mode
// (for models that support Chat Completions).
func (s *HandlerService) handleResponses(c *gin.Context) {
	startTime := time.Now()

	// Read request body
	const maxBodySize = 10 * 1024 * 1024 // 10 MB
	bodyBytes, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBodySize))
	if err != nil {
		c.JSON(http.StatusBadRequest, convert.NewResponsesError(
			"Failed to read request body", "invalid_request_error", "bad_request",
		))
		return
	}

	// FIX: Set request_body for affinity cache fingerprinting
	c.Set("request_body", bodyBytes)

	// Get key info from context (set by auth middleware)
	keyID := c.GetString(string(keyIDKey))
	userID := c.GetString(string(userIDKey))
	keyHash := c.GetString("key_hash")
	accessMode := c.GetString("access_mode")

	// Apply pre-conversion plugins to the Responses-format client request body.
	bodyBytes = s.pluginChain.Apply(bodyBytes, plugin.Context{
		ClientFormat: "openai_responses",
		HostKey:      keyID,
		ClientName:   userID,
	})
	c.Set("request_body", bodyBytes)

	// Parse as Responses request
	var responsesReq convert.ResponsesRequest
	if err := json.Unmarshal(bodyBytes, &responsesReq); err != nil {
		c.JSON(http.StatusBadRequest, convert.NewResponsesError(
			"Invalid Responses request: "+err.Error(), "invalid_request_error", "bad_request",
		))
		return
	}
	if responsesReq.Reasoning != nil && responsesReq.Reasoning.Effort != "" {
		c.Set("reasoning_intensity", responsesReq.Reasoning.Effort)
	}
	if len(responsesReq.Tools) > 0 {
		toolNames := make([]string, 0, len(responsesReq.Tools))
		for _, t := range responsesReq.Tools {
			if t.Name != "" {
				toolNames = append(toolNames, t.Name)
			} else if t.Type != "" {
				toolNames = append(toolNames, t.Type)
			}
		}
		if s := joinAgentToolNames(toolNames); s != "" {
			c.Set("agent_tools", s)
		}
	}

	// Resolve model (use original Responses model name)
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

	resolvedModel, err := s.resolveModel(responsesReq.Model, modelRoutingMode, currentModel, selectedModels, allowedModels, keyHash)
	if err != nil {
		c.JSON(http.StatusBadRequest, convert.NewResponsesError(
			err.Error(), "invalid_request_error", "model_resolution_error",
		))
		return
	}

	// Enforce allowed_models restriction
	if !checkAllowedModels(c, resolvedModel) {
		c.JSON(http.StatusForbidden, convert.NewResponsesError(
			fmt.Sprintf("Model '%s' is not in your allowed models list.", resolvedModel),
			"model_not_allowed", "MODEL_NOT_ALLOWED",
		))
		return
	}

	// Get user priority
	userPriority := getUserPriority(accessMode)

	// Get affinity-preferred key ID
	var preferredKeyID string
	if s.handlerDeps.AffinityCache != nil {
		cachePriority := getUserPriority(accessMode)
		preferredKeyID = s.handlerDeps.AffinityCache.GetPreferredKeyID(c, resolvedModel, "default", cachePriority)
	}

	// Dual-track routing
	track := resolveTrack(c)
	c.Set("routing_track", track)

	var provider string
	var upstreamKey *keypool.KeyInternal
	var upstreamModelName string
	var isFallback bool

	switch track {
	case "bedrock":
		provider, upstreamKey, upstreamModelName, _, err = s.resolveBedrockProviderAndKey(c.Request.Context(), resolvedModel, preferredKeyID)
		if err != nil {
			s.handlerDeps.Logger.Warn("Bedrock routing failed, falling back to standard",
				zap.String("model", resolvedModel), zap.String("track", track), zap.Error(err))
			provider, upstreamKey, upstreamModelName, _, err = s.resolveProviderAndKey(c.Request.Context(), resolvedModel, userPriority, preferredKeyID)
			if err != nil {
				// Both Bedrock and standard fallback failed
				c.JSON(http.StatusServiceUnavailable, convert.NewResponsesError(
					err.Error(), "service_error", "service_unavailable",
				))
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
		provider, upstreamKey, upstreamModelName, _, err = s.resolveMantleProviderAndKey(c.Request.Context(), resolvedModel, preferredKeyID)
		if err != nil {
			s.handlerDeps.Logger.Warn("Mantle routing failed, falling back to standard",
				zap.String("model", resolvedModel), zap.String("track", track), zap.Error(err))
			provider, upstreamKey, upstreamModelName, _, err = s.resolveProviderAndKey(c.Request.Context(), resolvedModel, userPriority, preferredKeyID)
			if err != nil {
				// Both Mantle and standard fallback failed
				c.JSON(http.StatusServiceUnavailable, convert.NewResponsesError(
					err.Error(), "service_error", "service_unavailable",
				))
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
		provider, upstreamKey, upstreamModelName, isFallback, err = s.resolveProviderAndKey(c.Request.Context(), resolvedModel, userPriority, preferredKeyID)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, convert.NewResponsesError(
				err.Error(), "service_error", "service_unavailable",
			))
			return
		}
		c.Set("is_fallback", isFallback)
		if isFallback {
			c.Set("actual_group_id", c.GetString("group_id"))
		}
	}

	// Set context values for metrics middleware
	c.Set("provider", provider)
	c.Set("model", resolvedModel)

	// Estimate input tokens for pre-consume. Convert to Chat Completions format
	// so we can reuse the existing token counter.
	var inputTokens int
	if s.handlerDeps.TokenCounter != nil {
		inputTokens = estimateResponsesInputTokens(s.handlerDeps.TokenCounter, &responsesReq)
	}

	// Pre-consume: reserve estimated input cost before making upstream request.
	pcs := &preConsumeState{}
	if !s.handlerDeps.Simulate.Enabled {
		var used, rejected bool
		pcs.local, used, rejected = s.preConsumeLocal(c, userID, c.GetString("key_type"), resolvedModel, provider, inputTokens)
		if rejected {
			return
		}
		if !used {
			// Local balance cache did not handle this request — fall back to
			// the portal freeze, mirroring the chat completions path. Without
			// this the responses endpoint had no balance gate at all when the
			// local cache was disabled or cold.
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
					Model:       resolvedModel,
					Provider:    provider,
					InputTokens: inputTokens,
					RequestID:   traceID,
					Margin:      s.cfg.Billing.PreConsumeMargin,
				})
				pcCancel()

				if pcResult.Rejected {
					metrics.GetMetrics().IncrementPreconsumeRejected()
					c.JSON(http.StatusPaymentRequired, convert.NewResponsesError(
						"Insufficient balance for this request", "billing_error", "insufficient_balance",
					))
					return
				}
				pcs.portal = pcResult.Amount
			}
		}
	}

	// Check simulate mode
	if s.handlerDeps.Simulate.Enabled {
		hubReq, _ := convert.ResponsesToOpenAI(&responsesReq)
		hubReq.Model = upstreamModelName
		s.handleSimulate(c, *hubReq, keyID, userID, keyHash, startTime)
		return
	}

	// Determine route: passthrough vs convert
	usePassthrough := s.shouldUsePassthrough(provider, upstreamModelName, bodyBytes)

	isStream := responsesReq.Stream != nil && *responsesReq.Stream
	c.Set("is_stream", isStream)

	if usePassthrough {
		if isStream {
			s.handleResponsesPassthroughStreaming(c, bodyBytes, provider, upstreamKey, responsesReq.Model, startTime, pcs)
		} else {
			s.handleResponsesPassthrough(c, bodyBytes, provider, upstreamKey, responsesReq.Model, startTime, pcs)
		}
		return
	}

	// ===== Convert mode (existing logic) =====
	hubReq, err := convert.ResponsesToOpenAI(&responsesReq)
	if err != nil {
		// Refund the pre-consumed estimate because we are not calling upstream.
		s.settlePreConsume(c, pcs, nil, resolvedModel, provider, false, http.StatusBadRequest, "invalid_request", time.Since(startTime).Milliseconds())
		c.JSON(http.StatusBadRequest, convert.NewResponsesError(
			"Failed to convert request: "+err.Error(), "invalid_request_error", "bad_request",
		))
		return
	}
	hubReq.Model = upstreamModelName
	upstreamBaseURL := s.resolveBaseURL(upstreamKey, provider)

	adaptorCfg := &relay.AdaptorConfig{
		BaseURL:      upstreamBaseURL,
		APIKey:       upstreamKey.ActualKey,
		ExtraHeaders: s.filterClientHeaders(c),
	}
	adaptor := relay.GetAdaptor(provider, adaptorCfg)
	providerReq := adaptor.ToProvider(*hubReq)

	// Apply post-conversion plugins to the upstream request body.
	upstreamBody, err := json.Marshal(providerReq)
	if err != nil {
		s.handlerDeps.Logger.Error("Failed to marshal upstream request", zap.Error(err))
		// Refund the pre-consumed estimate because we are not calling upstream.
		s.settlePreConsume(c, pcs, nil, resolvedModel, provider, false, http.StatusInternalServerError, "internal_error", time.Since(startTime).Milliseconds())
		c.JSON(http.StatusInternalServerError, convert.NewResponsesError(
			"Failed to marshal upstream request", "internal_error", "internal_error",
		))
		return
	}
	upstreamBody = s.pluginChain.Apply(upstreamBody, plugin.Context{
		ClientFormat:   "openai_responses",
		UpstreamFormat: string(adaptor.GetRelayFormat()),
		HostKey:        upstreamKey.ID,
		ClientName:     userID,
	})

	if isStream {
		s.handleResponsesStreaming(c, adaptor, upstreamBody, *hubReq, provider, keyID, userID, keyHash, upstreamKey, responsesReq.Model, startTime, pcs, resolvedModel)
		return
	}

	// Non-streaming request
	statusCode, body, err := s.dispatchUpstream(c, provider, adaptor, upstreamBody, false)

	// In-request failover (responses non-stream; buffered → retryable pre-response).
	{
		fo := newFailoverState(s.cfg.Failover, resolvedModel)
		foUserPriority := getUserPriority(c.GetString("access_mode"))
		for {
			retryStatus := 0
			transportErr := false
			if err != nil {
				transportErr = true
			} else if statusCode >= 400 {
				retryStatus = statusCode
			} else {
				break
			}
			fo.recordAttempt(provider, upstreamKey.ID)
			if !fo.shouldRetry(retryStatus, transportErr, false) {
				break
			}
			if s.keyPoolManager != nil {
				if retryStatus == http.StatusTooManyRequests {
					s.keyPoolManager.ReportKeyRateLimited(provider, upstreamKey.ID, hubReq.Model, 0)
				} else {
					s.keyPoolManager.ReportKeyFailure(provider, upstreamKey.ID, hubReq.Model, retryStatus, "upstream_error", "upstream_error", "failover_retry")
				}
			}
			s.recordUpstreamUsageAsync(upstreamKey.ID, provider, hubReq.Model, nil, time.Since(startTime).Milliseconds(), true)
			np, nk, num, _, rerr := s.resolveProviderAndKeyExcluding(c.Request.Context(), resolvedModel, foUserPriority, "", fo.excluded)
			if rerr != nil || nk == nil {
				break
			}
			provider, upstreamKey = np, nk
			hubReq.Model = num
			c.Set("is_fallback", true)
			s.handlerDeps.Logger.Warn("Failover retry (responses)",
				zap.String("model", resolvedModel), zap.String("to_provider", provider),
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
				ClientFormat: "openai", UpstreamFormat: string(adaptor.GetRelayFormat()),
				HostKey: upstreamKey.ID, ClientName: userID,
			})
			statusCode, body, err = s.dispatchUpstream(c, provider, adaptor, upstreamBody, false)
		}
	}

	if err != nil {
		s.settlePreConsume(c, pcs, nil, resolvedModel, provider, false, 0, classifyTransportError(err), time.Since(startTime).Milliseconds())
		upErr := UpstreamUnreachableError()
		c.JSON(upErr.ClientStatus, convert.NewResponsesError(upErr.Message, upErr.Type, upErr.Code))
		return
	}

	// Check for upstream errors
	if statusCode >= 400 {
		providerBreakers.RecordFailure(provider)
		upErr := ClassifyUpstreamError(statusCode)
		c.Data(upErr.ClientStatus, "application/json", convert.ResponsesErrorJSON(upErr.Message, upErr.Type, upErr.Code))
		if s.keyPoolManager != nil {
			s.keyPoolManager.ReportKeyFailure(provider, upstreamKey.ID, hubReq.Model, statusCode, "upstream_error", "upstream_error", string(body[:min(500, len(body))]))
		}
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, hubReq.Model, nil, time.Since(startTime).Milliseconds(), true)
		s.recordProviderHealthAsync(c, provider, resolvedModel, statusCode, time.Since(startTime).Milliseconds(), nil, string(body[:min(200, len(body))]))
		// 4xx/5xx errors refund the pre-consumed estimate.
		s.settlePreConsume(c, pcs, nil, resolvedModel, provider, false, statusCode, "upstream_error", time.Since(startTime).Milliseconds())
		return
	}

	// Successful upstream dispatch.
	if s.keyPoolManager != nil {
		s.keyPoolManager.RecordUpstreamRequest(provider, upstreamKey.ID, hubReq.Model)
	}

	// Parse OpenAI response
	openaiResp := adaptor.FromProvider(body, *hubReq)
	if openaiResp == nil {
		s.settlePreConsume(c, pcs, nil, resolvedModel, provider, false, http.StatusInternalServerError, "parse_error", time.Since(startTime).Milliseconds())
		c.JSON(http.StatusInternalServerError, convert.NewResponsesError(
			"Failed to parse upstream response", "internal_error", "internal_error",
		))
		return
	}

	// Convert to Responses format
	responsesResp, err := convert.OpenAIToResponses(openaiResp, responsesReq.Model)
	if err != nil {
		s.settlePreConsume(c, pcs, nil, resolvedModel, provider, false, http.StatusInternalServerError, "convert_error", time.Since(startTime).Milliseconds())
		c.JSON(http.StatusInternalServerError, convert.NewResponsesError(
			"Failed to convert response: "+err.Error(), "internal_error", "internal_error",
		))
		return
	}

	// Track actually-called tools (response side) for the usage log.
	for i := range openaiResp.Choices {
		trackOpenAIMessageToolCalls(c, openaiResp.Choices[i].Message)
	}

	responsesBody, _ := json.Marshal(responsesResp)
	c.Data(http.StatusOK, "application/json", responsesBody)

	// Extract usage for audit
	usage := &dto.Usage{}
	if openaiResp != nil {
		usage.PromptTokens = openaiResp.Usage.PromptTokens
		usage.CompletionTokens = openaiResp.Usage.CompletionTokens
		usage.TotalTokens = openaiResp.Usage.TotalTokens
		usage.PromptTokensDetails = openaiResp.Usage.PromptTokensDetails
		usage.PromptCacheHitTokens = openaiResp.Usage.PromptCacheHitTokens
	}

	// Settle actual usage against the pre-consumed estimate.
	s.settlePreConsume(c, pcs, usage, resolvedModel, provider, true, http.StatusOK, "", time.Since(startTime).Milliseconds())

	// Record audit
	if s.handlerDeps.AuditLogger != nil {
		s.logAudit(c, audit.AuditEntry{
			Timestamp:      startTime,
			KeyID:          keyID,
			UpstreamKeyID:  upstreamKey.ID,
			APIKeyHash:     keyHash,
			UserID:         userID,
			Provider:       provider,
			Model:          responsesReq.Model,
			InputTokens:    usage.PromptTokens,
			OutputTokens:   usage.CompletionTokens,
			CacheHitTokens: usage.PromptCacheHitTokens,
			Cost:           billingCost(s.handlerDeps.Billing, usage, responsesReq.Model, provider),
			LatencyMs:      time.Since(startTime).Milliseconds(),
			StatusCode:     http.StatusOK,
			RequestType:    "responses_api",
		})
	}

	// Record channel affinity
	if s.handlerDeps.AffinityCache != nil {
		cachePriority := getUserPriority(accessMode)
		s.handlerDeps.AffinityCache.RecordPreferredKeyID(c, responsesReq.Model, "default", upstreamKey.ID, cachePriority)
		s.handlerDeps.AffinityCache.ObserveUsageCache(c, usage, adaptor.GetRelayFormat())
	}

	totalTokens := int64(usage.PromptTokens + usage.CompletionTokens)
	c.Set("tokens_consumed", totalTokens)
	if s.keyPoolManager != nil && totalTokens > 0 {
		s.keyPoolManager.RecordUpstreamTokens(provider, upstreamKey.ID, responsesReq.Model, totalTokens, int64(usage.PromptTokensDetails.CachedTokens))
	}
	if upstreamKey.QuotaLimit > 0 && totalTokens > 0 {
		upstreamKey.AddQuotaUsed(totalTokens)
	}
	s.recordUsageAsyncFromContext(c, keyHash, upstreamKey.ID, responsesReq.Model, provider, usage, time.Since(startTime).Milliseconds(), "")

	// Capture retention for Responses API convert mode
	setRetentionContext(c, "responses", string(adaptor.GetRelayFormat()), hubReq.Model, adaptor.GetRelayFormat() != types.RelayFormatOpenAIResponses)
	s.captureRetention(c, bodyBytes, body, provider, responsesReq.Model, upstreamKey.ID, usage, time.Since(startTime).Milliseconds(), http.StatusOK, false, "responses_api")
}

// handleResponsesStreaming handles streaming Responses API → OpenAI upstream conversion
func (s *HandlerService) handleResponsesStreaming(c *gin.Context, adaptor relay.Adaptor, upstreamBody []byte, req dto.GeneralOpenAIRequest, provider, keyID, userID, keyHash string, upstreamKey *keypool.KeyInternal, originalModel string, startTime time.Time, pcs *preConsumeState, resolvedModel string) {
	resp, err := adaptor.DoRequestBytesWithContext(c.Request.Context(), upstreamBody, true)

	// In-request failover (responses convert streaming; pre-first-byte).
	{
		fo := newFailoverState(s.cfg.Failover, resolvedModel)
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
					s.keyPoolManager.ReportKeyRateLimited(provider, upstreamKey.ID, req.Model, 0)
				} else {
					s.keyPoolManager.ReportKeyFailure(provider, upstreamKey.ID, req.Model, retryStatus, "upstream_error", "upstream_error", "failover_retry")
				}
			}
			s.recordUpstreamUsageAsync(upstreamKey.ID, provider, req.Model, nil, time.Since(startTime).Milliseconds(), true)
			if resp != nil {
				_ = resp.Body.Close()
			}
			np, nk, num, _, rerr := s.resolveProviderAndKeyExcluding(c.Request.Context(), resolvedModel, foUserPriority, "", fo.excluded)
			if rerr != nil || nk == nil {
				break
			}
			provider, upstreamKey = np, nk
			req.Model = num
			c.Set("is_fallback", true)
			s.handlerDeps.Logger.Warn("Failover retry (responses stream)",
				zap.String("model", resolvedModel), zap.String("to_provider", provider),
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
				ClientFormat: "openai_responses", UpstreamFormat: string(adaptor.GetRelayFormat()),
				HostKey: upstreamKey.ID, ClientName: userID,
			})
			resp, err = adaptor.DoRequestBytesWithContext(c.Request.Context(), upstreamBody, true)
		}
	}

	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.handlerDeps.Logger.Warn("Client disconnected before Responses streaming request completed", zap.Error(err))
			s.settlePreConsume(c, pcs, nil, resolvedModel, provider, false, 499, "client_disconnect", time.Since(startTime).Milliseconds())
			return
		}
		// Upstream request failed before receiving a status, refund pre-consume.
		s.settlePreConsume(c, pcs, nil, resolvedModel, provider, false, 0, classifyTransportError(err), time.Since(startTime).Milliseconds())
		upErr := UpstreamUnreachableError()
		c.Data(upErr.ClientStatus, "application/json", convert.ResponsesErrorJSON(upErr.Message, upErr.Type, upErr.Code))
		return
	}
	defer func() { _ = resp.Body.Close() }()

	// Forward upstream non-200 errors
	if resp.StatusCode != http.StatusOK {
		errorBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		providerBreakers.RecordFailure(provider)
		upErr := ClassifyUpstreamError(resp.StatusCode)
		c.Data(upErr.ClientStatus, "application/json", convert.ResponsesErrorJSON(upErr.Message, upErr.Type, upErr.Code))
		if s.keyPoolManager != nil {
			s.keyPoolManager.ReportKeyFailure(provider, upstreamKey.ID, req.Model, resp.StatusCode, "upstream_error", "upstream_error", string(errorBody[:min(500, len(errorBody))]))
		}
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, req.Model, nil, time.Since(startTime).Milliseconds(), true)
		s.recordProviderHealthAsync(c, provider, originalModel, resp.StatusCode, time.Since(startTime).Milliseconds(), nil, string(errorBody[:min(200, len(errorBody))]))
		// 4xx/5xx errors refund the pre-consumed estimate.
		s.settlePreConsume(c, pcs, nil, resolvedModel, provider, false, resp.StatusCode, "upstream_error", time.Since(startTime).Milliseconds())
		return
	}

	// Successful upstream dispatch.
	if s.keyPoolManager != nil {
		s.keyPoolManager.RecordUpstreamRequest(provider, upstreamKey.ID, req.Model)
	}

	upstreamConnected := time.Now()

	// Set SSE headers for Responses format
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Transfer-Encoding", "chunked")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, convert.NewResponsesError(
			"Streaming not supported", "internal_error", "streaming_not_supported",
		))
		return
	}

	reader := bufio.NewReader(resp.Body)
	// Pre-seed the logical (client-facing) model so upstream model IDs
	// (e.g. Bedrock "anthropic.claude-...-v1:0") never reach the client.
	state := &convert.ResponsesStreamState{Model: resolvedModel, CreatedAt: time.Now().Unix()}
	var totalUsage dto.Usage
	// Assembles fragmented tool-call names across chunks (response-side tracking).
	toolFrags := map[int]*strings.Builder{}
	var firstTokenLatency int64
	// Set once the converter emits response.completed, so the [DONE] handler
	// doesn't append a duplicate malformed completed event.
	var stateCompleted bool

	streamCtx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Minute)
	defer cancel()

	// If the client disconnects or the stream timeout fires, force the upstream
	// response body closed so the reader exits promptly instead of blocking until
	// the upstream closes the connection.
	stopReader := context.AfterFunc(streamCtx, func() {
		_ = resp.Body.Close()
	})
	defer stopReader()

	for {
		select {
		case <-streamCtx.Done():
			// Client disconnected or stream timeout: keep the pre-consumed estimate
			// as a conservative charge for input tokens already consumed upstream.
			s.settlePreConsume(c, pcs, nil, resolvedModel, provider, false, 0, "timeout", time.Since(startTime).Milliseconds())
			s.handlerDeps.Logger.Warn("Responses streaming timeout or client disconnected", zap.Error(streamCtx.Err()))
			return
		default:
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			s.handlerDeps.Logger.Error("Error reading Responses stream", zap.Error(err))
			break
		}

		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")

			if firstTokenLatency == 0 {
				firstTokenLatency = time.Since(upstreamConnected).Milliseconds()
			}

			if data == "[DONE]" {
				// The converter already emitted a full response.completed event
				// at the finish_reason chunk. Emitting the minimal fallback here
				// too produces a DUPLICATE response.completed with an invalid
				// (field-less) Response object, which strict clients (codex CLI)
				// reject as a stream error and reconnect-loop (2026-10-24).
				// Clients following the OpenAI SSE spec expect the [DONE]
				// terminator after response.completed; a bare connection close
				// reads as "stream disconnected before completion".
				if !stateCompleted {
					doneEvent := convert.ResponsesStreamDone()
					_, _ = c.Writer.Write([]byte(doneEvent + "\n"))
					flusher.Flush()
				}
				_, _ = c.Writer.Write([]byte("data: [DONE]\n\n"))
				flusher.Flush()
				break
			}

			// Transform OpenAI SSE chunk to Responses SSE events
			events, err := convert.OpenAIChunkToResponsesEvent([]byte(data), state)
			if err != nil {
				continue
			}

			for _, event := range events {
				if strings.Contains(event, `"type":"response.completed"`) || strings.HasPrefix(event, "event: response.completed\n") {
					stateCompleted = true
				}
				// Each event string is "event: <type>\ndata: <json>". SSE
				// requires a blank line after each event to dispatch it; without
				// it a strict parser (codex CLI) merges the whole stream into
				// one event and fails JSON parsing with "trailing characters
				// at line 2 column 1" (2026-10-24).
				_, _ = c.Writer.Write([]byte(event + "\n\n"))
				flusher.Flush()
			}

			// Extract usage
			var streamResp dto.ChatCompletionsStreamResponse
			if err := json.Unmarshal([]byte(data), &streamResp); err == nil {
				// Track fragmented tool-call names for the usage log.
				trackOpenAIStreamToolCalls(toolFrags, &streamResp)
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
				}
			}
		}
	}

	// Record affinity
	if s.handlerDeps.AffinityCache != nil {
		cachePriority := getUserPriority(c.GetString("access_mode"))
		s.handlerDeps.AffinityCache.RecordPreferredKeyID(c, originalModel, "default", upstreamKey.ID, cachePriority)
		s.handlerDeps.AffinityCache.ObserveUsageCache(c, &totalUsage, adaptor.GetRelayFormat())
	}

	s.recordUpstreamUsageAsync(upstreamKey.ID, provider, req.Model, &totalUsage, time.Since(startTime).Milliseconds(), false)
	s.recordProviderHealthAsync(c, provider, originalModel, resp.StatusCode, time.Since(startTime).Milliseconds(), nil, "")
	providerBreakers.RecordSuccess(provider)
	totalTokens := int64(totalUsage.PromptTokens + totalUsage.CompletionTokens)
	c.Set("tokens_consumed", totalTokens)
	if s.keyPoolManager != nil && totalTokens > 0 {
		s.keyPoolManager.RecordUpstreamTokens(provider, upstreamKey.ID, req.Model, totalTokens, int64(totalUsage.PromptTokensDetails.CachedTokens))
	}
	if upstreamKey.QuotaLimit > 0 && totalTokens > 0 {
		upstreamKey.AddQuotaUsed(totalTokens)
	}

	// Settle actual usage against the pre-consumed estimate.
	s.settlePreConsume(c, pcs, &totalUsage, resolvedModel, provider, true, http.StatusOK, "", time.Since(startTime).Milliseconds())

	// Set first-token latency in context before usage recording.
	c.Set("first_token_latency_ms", firstTokenLatency)

	// Note the tools actually called during the stream (response side).
	flushStreamToolCalls(c, toolFrags)
	s.recordUsageAsyncFromContext(c, keyHash, upstreamKey.ID, originalModel, provider, &totalUsage, time.Since(startTime).Milliseconds(), "")

	// Capture retention metadata for streaming Responses API
	var responsesRequestBody []byte
	if v, ok := c.Get("request_body"); ok {
		if b, ok := v.([]byte); ok {
			responsesRequestBody = b
		}
	}
	setRetentionContext(c, "responses", string(adaptor.GetRelayFormat()), req.Model, adaptor.GetRelayFormat() != types.RelayFormatOpenAIResponses)
	s.captureRetention(c, responsesRequestBody, nil, provider, originalModel, upstreamKey.ID, &totalUsage, time.Since(startTime).Milliseconds(), resp.StatusCode, true, "responses_api_stream")
}

// Decision order:
//  1. Check Portal-synced model capabilities (responses=true, chat_completions=false → passthrough)
//  2. Check route cache (previous dynamic fallback decision)
//  3. Default to convert mode (will attempt Chat Completions, with fallback on 400)
func (s *HandlerService) shouldUsePassthrough(provider, upstreamModelName string, bodyBytes []byte) bool {
	// 1. Portal-synced capabilities (primary)
	if s.keyPoolManager != nil {
		caps := s.keyPoolManager.GetModelCapability(provider, upstreamModelName)
		if caps != nil {
			if caps["responses"] && !caps["chat_completions"] {
				return true
			}
			return false
		}
	}

	// 2. Route cache (dynamic fallback result)
	if passthrough, ok := responsesRouteCacheInstance.Get(provider, upstreamModelName); ok {
		return passthrough
	}

	// 3. Default: try convert mode first
	return false
}

// buildResponsesPassthroughReq builds a raw POST to {base}/responses (Bearer auth).
func (s *HandlerService) buildResponsesPassthroughReq(c *gin.Context, provider string, upstreamKey *keypool.KeyInternal, body []byte, stream bool) (*http.Request, error) {
	base := s.resolveBaseURL(upstreamKey, provider)
	req, err := http.NewRequest("POST", strings.TrimRight(base, "/")+"/responses", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+upstreamKey.ActualKey)
	s.applyForwardedHeaders(req, c)
	return req, nil
}

// responsesPassthroughFailover attempts in-request failover for a responses
// passthrough send. Given the current (resp, err), it returns the final
// (resp, err, provider, upstreamKey) after retrying on retryable errors to the
// next responses-native candidate (a provider whose model is responses-only, so
// the unchanged passthrough body is valid). Pre-first-byte / pre-response only.
func (s *HandlerService) responsesPassthroughFailover(c *gin.Context, resp *http.Response, err error, provider string, upstreamKey *keypool.KeyInternal, logicalModel string, bodyBytes []byte, stream bool, startTime time.Time) (*http.Response, string, *keypool.KeyInternal, error) {
	fo := newFailoverState(s.cfg.Failover, logicalModel)
	foUserPriority := getUserPriority(c.GetString("access_mode"))
	for {
		retryStatus := 0
		transportErr := false
		if err != nil {
			transportErr = true
		} else if resp != nil && resp.StatusCode >= 400 {
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
				s.keyPoolManager.ReportKeyRateLimited(provider, upstreamKey.ID, logicalModel, 0)
			} else {
				s.keyPoolManager.ReportKeyFailure(provider, upstreamKey.ID, logicalModel, retryStatus, "upstream_error", "upstream_error", "failover_retry")
			}
		}
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, logicalModel, nil, time.Since(startTime).Milliseconds(), true)
		if resp != nil {
			_ = resp.Body.Close()
		}
		np, nk, num, _, rerr := s.resolveProviderAndKeyExcluding(c.Request.Context(), logicalModel, foUserPriority, "", fo.excluded)
		if rerr != nil || nk == nil {
			break
		}
		// Only fail over to a responses-native target (unchanged body is valid).
		if !s.shouldUsePassthrough(np, num, bodyBytes) {
			fo.excluded[np+":"+nk.ID] = true
			continue
		}
		provider, upstreamKey = np, nk
		c.Set("is_fallback", true)
		s.handlerDeps.Logger.Warn("Failover retry (responses passthrough)",
			zap.String("model", logicalModel), zap.String("to_provider", provider),
			zap.Int("attempt", fo.attempt), zap.Int("last_status", retryStatus))
		nreq, berr := s.buildResponsesPassthroughReq(c, provider, upstreamKey, bodyBytes, stream)
		if berr != nil {
			break
		}
		if stream {
			resp, err = sharedStreamingClient.Do(nreq)
		} else {
			resp, err = sharedHandlerClient.Do(nreq)
		}
	}
	return resp, provider, upstreamKey, err
}

// handleResponsesPassthrough forwards the raw Responses API request directly to
// the upstream provider without conversion. Used for Responses-only models.
func (s *HandlerService) handleResponsesPassthrough(c *gin.Context, bodyBytes []byte,
	provider string, upstreamKey *keypool.KeyInternal, originalModel string, startTime time.Time, pcs *preConsumeState) {

	keyID := c.GetString(string(keyIDKey))
	userID := c.GetString(string(userIDKey))
	keyHash := c.GetString("key_hash")
	accessMode := c.GetString("access_mode")

	upstreamBaseURL := s.resolveBaseURL(upstreamKey, provider)
	reqURL := strings.TrimRight(upstreamBaseURL, "/") + "/responses"

	req, err := http.NewRequest("POST", reqURL, bytes.NewReader(bodyBytes))
	if err != nil {
		s.settlePreConsume(c, pcs, nil, originalModel, provider, false, http.StatusInternalServerError, "internal_error", time.Since(startTime).Milliseconds())
		c.JSON(http.StatusInternalServerError, convert.NewResponsesError(
			"Failed to build upstream request: "+err.Error(), "internal_error", "internal_error",
		))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+upstreamKey.ActualKey)
	s.applyForwardedHeaders(req, c)

	resp, err := sharedHandlerClient.Do(req)
	resp, provider, upstreamKey, err = s.responsesPassthroughFailover(c, resp, err, provider, upstreamKey, originalModel, bodyBytes, false, startTime)
	if err != nil {
		s.settlePreConsume(c, pcs, nil, originalModel, provider, false, 0, classifyTransportError(err), time.Since(startTime).Milliseconds())
		upErr := UpstreamUnreachableError()
		c.JSON(upErr.ClientStatus, convert.NewResponsesError(upErr.Message, upErr.Type, upErr.Code))
		return
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		s.settlePreConsume(c, pcs, nil, originalModel, provider, false, http.StatusBadGateway, "upstream_error", time.Since(startTime).Milliseconds())
		upErr := UpstreamUnreachableError()
		c.Data(upErr.ClientStatus, "application/json", convert.ResponsesErrorJSON(upErr.Message, upErr.Type, upErr.Code))
		return
	}

	// Check for upstream 400 indicating "does not support Chat Completions"
	// This should not happen in passthrough mode, but handle gracefully
	if resp.StatusCode >= 400 {
		providerBreakers.RecordFailure(provider)
		upErr := ClassifyUpstreamError(resp.StatusCode)
		c.Data(upErr.ClientStatus, "application/json", convert.ResponsesErrorJSON(upErr.Message, upErr.Type, upErr.Code))
		if s.keyPoolManager != nil {
			s.keyPoolManager.ReportKeyFailure(provider, upstreamKey.ID, originalModel, resp.StatusCode, "upstream_error", "upstream_error", string(respBody))
		}
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, originalModel, nil, time.Since(startTime).Milliseconds(), true)
		s.recordProviderHealthAsync(c, provider, originalModel, resp.StatusCode, time.Since(startTime).Milliseconds(), nil, string(respBody[:min(200, len(respBody))]))
		// 4xx/5xx errors refund the pre-consumed estimate.
		s.settlePreConsume(c, pcs, nil, originalModel, provider, false, resp.StatusCode, "upstream_error", time.Since(startTime).Milliseconds())
		return
	}

	// Successful upstream dispatch.
	if s.keyPoolManager != nil {
		s.keyPoolManager.RecordUpstreamRequest(provider, upstreamKey.ID, originalModel)
	}

	// Direct return (no conversion)
	c.Data(resp.StatusCode, "application/json", respBody)

	// Extract usage from Responses format
	usage := convert.ExtractResponsesUsage(respBody)
	// Track actually-called tools (response side) for the usage log.
	for _, name := range convert.ExtractResponsesCalledTools(respBody) {
		noteCalledTool(c, name)
	}
	if usage == nil {
		usage = &dto.Usage{}
	}

	totalTokens := int64(usage.PromptTokens + usage.CompletionTokens)
	c.Set("tokens_consumed", totalTokens)
	if s.keyPoolManager != nil && totalTokens > 0 {
		s.keyPoolManager.RecordUpstreamTokens(provider, upstreamKey.ID, originalModel, totalTokens, int64(usage.PromptTokensDetails.CachedTokens))
	}
	if upstreamKey.QuotaLimit > 0 && totalTokens > 0 {
		upstreamKey.AddQuotaUsed(totalTokens)
	}

	// Settle actual usage against the pre-consumed estimate.
	s.settlePreConsume(c, pcs, usage, originalModel, provider, true, resp.StatusCode, "", time.Since(startTime).Milliseconds())

	// Record audit
	if s.handlerDeps.AuditLogger != nil {
		s.logAudit(c, audit.AuditEntry{
			Timestamp:      startTime,
			KeyID:          keyID,
			UpstreamKeyID:  upstreamKey.ID,
			APIKeyHash:     keyHash,
			UserID:         userID,
			Provider:       provider,
			Model:          originalModel,
			InputTokens:    usage.PromptTokens,
			OutputTokens:   usage.CompletionTokens,
			CacheHitTokens: usage.PromptCacheHitTokens,
			Cost:           billingCost(s.handlerDeps.Billing, usage, originalModel, provider),
			LatencyMs:      time.Since(startTime).Milliseconds(),
			StatusCode:     resp.StatusCode,
			RequestType:    "responses_api_passthrough",
		})
	}

	// Record affinity (use OpenAI format since passthrough doesn't have a specific format)
	if s.handlerDeps.AffinityCache != nil {
		cachePriority := getUserPriority(accessMode)
		s.handlerDeps.AffinityCache.RecordPreferredKeyID(c, originalModel, "default", upstreamKey.ID, cachePriority)
		s.handlerDeps.AffinityCache.ObserveUsageCache(c, usage, types.RelayFormatOpenAI)
	}

	s.recordUpstreamUsageAsync(upstreamKey.ID, provider, originalModel, usage, time.Since(startTime).Milliseconds(), false)
	s.recordProviderHealthAsync(c, provider, originalModel, resp.StatusCode, time.Since(startTime).Milliseconds(), nil, "")
	providerBreakers.RecordSuccess(provider)

	// Capture retention for Responses API passthrough
	setRetentionContext(c, "responses", "responses", originalModel, false)
	s.captureRetention(c, bodyBytes, respBody, provider, originalModel, upstreamKey.ID, usage, time.Since(startTime).Milliseconds(), resp.StatusCode, false, "responses_api_passthrough")
}

// handleResponsesPassthroughStreaming forwards streaming Responses API requests
// directly to the upstream without conversion.
func (s *HandlerService) handleResponsesPassthroughStreaming(c *gin.Context, bodyBytes []byte,
	provider string, upstreamKey *keypool.KeyInternal, originalModel string, startTime time.Time, pcs *preConsumeState) {

	keyID := c.GetString(string(keyIDKey))
	userID := c.GetString(string(userIDKey))
	keyHash := c.GetString("key_hash")
	accessMode := c.GetString("access_mode")

	upstreamBaseURL := s.resolveBaseURL(upstreamKey, provider)
	reqURL := strings.TrimRight(upstreamBaseURL, "/") + "/responses"

	// Bound stream lifetime and propagate cancellation to the upstream request.
	streamCtx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(streamCtx, "POST", reqURL, bytes.NewReader(bodyBytes))
	if err != nil {
		s.settlePreConsume(c, pcs, nil, originalModel, provider, false, http.StatusInternalServerError, "internal_error", time.Since(startTime).Milliseconds())
		c.JSON(http.StatusInternalServerError, convert.NewResponsesError(
			"Failed to build upstream request: "+err.Error(), "internal_error", "internal_error",
		))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+upstreamKey.ActualKey)
	s.applyForwardedHeaders(req, c)

	resp, err := sharedStreamingClient.Do(req)
	resp, provider, upstreamKey, err = s.responsesPassthroughFailover(c, resp, err, provider, upstreamKey, originalModel, bodyBytes, true, startTime)
	if err != nil {
		s.settlePreConsume(c, pcs, nil, originalModel, provider, false, 0, classifyTransportError(err), time.Since(startTime).Milliseconds())
		upErr := UpstreamUnreachableError()
		c.Data(upErr.ClientStatus, "application/json", convert.ResponsesErrorJSON(upErr.Message, upErr.Type, upErr.Code))
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		errorBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		providerBreakers.RecordFailure(provider)
		upErr := ClassifyUpstreamError(resp.StatusCode)
		c.Data(upErr.ClientStatus, "application/json", convert.ResponsesErrorJSON(upErr.Message, upErr.Type, upErr.Code))
		if s.keyPoolManager != nil {
			s.keyPoolManager.ReportKeyFailure(provider, upstreamKey.ID, originalModel, resp.StatusCode, "upstream_error", "upstream_error", string(errorBody[:min(500, len(errorBody))]))
		}
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, originalModel, nil, time.Since(startTime).Milliseconds(), true)
		s.recordProviderHealthAsync(c, provider, originalModel, resp.StatusCode, time.Since(startTime).Milliseconds(), nil, string(errorBody[:min(200, len(errorBody))]))
		// 4xx/5xx errors refund the pre-consumed estimate.
		s.settlePreConsume(c, pcs, nil, originalModel, provider, false, resp.StatusCode, "upstream_error", time.Since(startTime).Milliseconds())
		return
	}

	// Successful upstream dispatch.
	if s.keyPoolManager != nil {
		s.keyPoolManager.RecordUpstreamRequest(provider, upstreamKey.ID, originalModel)
	}

	upstreamConnected := time.Now()

	// Set SSE headers and stream directly
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Transfer-Encoding", "chunked")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, convert.NewResponsesError(
			"Streaming not supported", "internal_error", "streaming_not_supported",
		))
		return
	}

	reader := bufio.NewReader(resp.Body)
	var totalUsage dto.Usage
	var firstTokenLatency int64

	// Force the upstream body closed when the client disconnects or the stream
	// timeout fires, so the blocking ReadString below returns promptly.
	stopReader := context.AfterFunc(streamCtx, func() {
		_ = resp.Body.Close()
	})
	defer stopReader()

	for {
		select {
		case <-streamCtx.Done():
			// Client disconnected or stream timeout: keep the pre-consumed estimate
			// as a conservative charge for input tokens already consumed upstream.
			s.settlePreConsume(c, pcs, nil, originalModel, provider, false, 0, "timeout", time.Since(startTime).Milliseconds())
			s.handlerDeps.Logger.Warn("Responses passthrough streaming timeout or client disconnected", zap.Error(streamCtx.Err()))
			return
		default:
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			s.handlerDeps.Logger.Error("Error reading Responses passthrough stream", zap.Error(err))
			break
		}

		_, _ = c.Writer.Write([]byte(line))
		flusher.Flush()

		if firstTokenLatency == 0 {
			firstTokenLatency = time.Since(upstreamConnected).Milliseconds()
		}

		// Attempt to extract usage from the last line
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data != "[DONE]" {
				if u := convert.ExtractResponsesUsage([]byte(data)); u != nil {
					totalUsage = *u
				}
				// Track actually-called tools (response side): each
				// response.output_item.done event announces one completed item.
				if strings.Contains(data, `"function_call"`) {
					var evt struct {
						Type string `json:"type"`
						Item struct {
							Type string `json:"type"`
							Name string `json:"name"`
						} `json:"item"`
					}
					if json.Unmarshal([]byte(data), &evt) == nil && evt.Item.Type == "function_call" {
						noteCalledTool(c, evt.Item.Name)
					}
				}
			}
		}
	}

	// Audit log
	if s.handlerDeps.AuditLogger != nil {
		s.logAudit(c, audit.AuditEntry{
			Timestamp:      startTime,
			KeyID:          keyID,
			UpstreamKeyID:  upstreamKey.ID,
			APIKeyHash:     keyHash,
			UserID:         userID,
			Provider:       provider,
			Model:          originalModel,
			InputTokens:    totalUsage.PromptTokens,
			OutputTokens:   totalUsage.CompletionTokens,
			CacheHitTokens: totalUsage.PromptCacheHitTokens,
			Cost:           billingCost(s.handlerDeps.Billing, &totalUsage, originalModel, provider),
			LatencyMs:      time.Since(startTime).Milliseconds(),
			StatusCode:     http.StatusOK,
			RequestType:    "responses_api_passthrough_stream",
			IsStream:       true,
		})
	}

	// Record affinity
	if s.handlerDeps.AffinityCache != nil {
		cachePriority := getUserPriority(accessMode)
		s.handlerDeps.AffinityCache.RecordPreferredKeyID(c, originalModel, "default", upstreamKey.ID, cachePriority)
		s.handlerDeps.AffinityCache.ObserveUsageCache(c, &totalUsage, types.RelayFormatOpenAI)
	}

	s.recordUpstreamUsageAsync(upstreamKey.ID, provider, originalModel, &totalUsage, time.Since(startTime).Milliseconds(), false)
	s.recordProviderHealthAsync(c, provider, originalModel, resp.StatusCode, time.Since(startTime).Milliseconds(), nil, "")
	providerBreakers.RecordSuccess(provider)
	totalTokens := int64(totalUsage.PromptTokens + totalUsage.CompletionTokens)
	c.Set("tokens_consumed", totalTokens)
	if s.keyPoolManager != nil && totalTokens > 0 {
		s.keyPoolManager.RecordUpstreamTokens(provider, upstreamKey.ID, originalModel, totalTokens, int64(totalUsage.PromptTokensDetails.CachedTokens))
	}
	if upstreamKey.QuotaLimit > 0 && totalTokens > 0 {
		upstreamKey.AddQuotaUsed(totalTokens)
	}

	// Settle actual usage against the pre-consumed estimate.
	s.settlePreConsume(c, pcs, &totalUsage, originalModel, provider, true, resp.StatusCode, "", time.Since(startTime).Milliseconds())

	// Set first-token latency in context before usage recording.
	c.Set("first_token_latency_ms", firstTokenLatency)

	// Record token usage for billing.
	s.recordUsageAsyncFromContext(c, keyHash, upstreamKey.ID, originalModel, provider, &totalUsage, time.Since(startTime).Milliseconds(), "")

	// Capture retention metadata for streaming Responses API passthrough
	setRetentionContext(c, "responses", "responses", originalModel, false)
	s.captureRetention(c, bodyBytes, nil, provider, originalModel, upstreamKey.ID, &totalUsage, time.Since(startTime).Milliseconds(), resp.StatusCode, true, "responses_api_passthrough_stream")
}

// billingCost is a nil-safe wrapper around Billing.CalculateCost.
func billingCost(b interface {
	CalculateCost(*dto.Usage, string, string) float64
}, usage *dto.Usage, model, provider string) float64 {
	if b == nil {
		return 0
	}
	return b.CalculateCost(usage, model, provider)
}

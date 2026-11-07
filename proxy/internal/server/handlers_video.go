package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/mayiants/ai-proxy-go/internal/audit"
	"github.com/mayiants/ai-proxy-go/pkg/keypool"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// videoGenerationRequest mirrors the TokenRouter video generation payload.
// Most optional fields are accepted for upstream compatibility but are not
// inspected by the proxy.
type videoGenerationRequest struct {
	Model           string          `json:"model"`
	Prompt          string          `json:"prompt"`
	Image           json.RawMessage `json:"image,omitempty"`
	Images          json.RawMessage `json:"images,omitempty"`
	Audios          json.RawMessage `json:"audios,omitempty"`
	Videos          json.RawMessage `json:"videos,omitempty"`
	FirstFrameImage json.RawMessage `json:"first_frame_image,omitempty"`
	InputReference  json.RawMessage `json:"input_reference,omitempty"`
	Size            string          `json:"size,omitempty"`
	Duration        int             `json:"duration,omitempty"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
}

// asyncTaskResponse detects the two async shapes returned by TokenRouter:
//
//	{ "data": { "task_id": "task_xxx" } }
//	{ "task_id": "task_xxx" }
type asyncTaskResponse struct {
	Data   asyncTaskData `json:"data"`
	TaskID string        `json:"task_id"`
}

type asyncTaskData struct {
	TaskID string `json:"task_id"`
}

// handleVideoGenerations creates an async video generation task upstream.
// On success it bills once per request and returns a consistent async envelope
// when the upstream responds with a task_id.
func (s *HandlerService) handleVideoGenerations(c *gin.Context) {
	startTime := time.Now()

	// Read and preserve request body for retention.
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

	var req videoGenerationRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "Invalid request: " + err.Error(),
			"type":    "invalid_request_error",
		}})
		return
	}

	if req.Model == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "Missing required field: model",
			"type":    "invalid_request_error",
		}})
		return
	}

	// Bound the client-controlled duration before it reaches billing
	// (Portal multiplies the per-second/block variant price by it).
	// duration <= 0 means "provider default" and bills as one unit.
	if req.Duration < 0 || req.Duration > maxVideoDurationSeconds {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": fmt.Sprintf("duration must be between 1 and %d seconds", maxVideoDurationSeconds),
			"type":    "invalid_request_error",
		}})
		return
	}

	keyID := c.GetString(string(keyIDKey))
	userID := c.GetString(string(userIDKey))
	keyHash := c.GetString("key_hash")

	// Enforce allowed_models restriction.
	if !checkAllowedModels(c, req.Model) {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{
			"message": fmt.Sprintf("Model '%s' is not in your allowed models list.", req.Model),
			"type":    "model_not_allowed",
			"code":    "MODEL_NOT_ALLOWED",
		}})
		return
	}

	logicalModel := req.Model

	// Media metadata for Portal media-mode billing.
	c.Set("duration", req.Duration)
	c.Set("image_size", req.Size)

	// Resolve upstream provider and key.
	provider, upstreamKey, upstreamModelName, isFallback, resolveErr := s.resolveProviderAndKey(c.Request.Context(), req.Model, "normal", "")
	if resolveErr != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
			"message": resolveErr.Error(),
			"type":    "service_error",
		}})
		return
	}
	c.Set("is_fallback", isFallback)
	if isFallback {
		c.Set("actual_group_id", c.GetString("group_id"))
	}

	// Reject image/audio model types routed here by mistake (portal-synced
	// model_type), before any pre-consume or dispatch.
	if s.keyPoolManager != nil {
		if mt := s.keyPoolManager.GetModelType(provider, logicalModel); mt == "image" || mt == "audio" {
			ep := mediaEndpointForType(mt)
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
				"message": fmt.Sprintf("model '%s' (type: %s) must use %s", logicalModel, mt, ep),
				"type":    "invalid_request_error",
				"code":    "MEDIA_MODEL_WRONG_ENDPOINT",
			}})
			return
		}
	}

	// Set context values for metrics middleware.
	c.Set("provider", provider)
	c.Set("model", logicalModel)
	c.Set("is_stream", false)

	// Pre-conume: reserve the estimated media cost before dispatch so a
	// zero-balance key cannot push generation cost onto the platform. The
	// worst-case estimate is driven by the declared duration (capped above):
	// per-second billing makes a fixed one-unit estimate under-freeze long
	// videos.
	estimatedUnits := req.Duration
	if estimatedUnits < 1 {
		estimatedUnits = 1
	}
	var estimatedMicro int64
	if s.handlerDeps.Billing != nil {
		var margin float64
		if s.cfg != nil {
			margin = s.cfg.Billing.PreConsumeMargin
		}
		estimatedMicro = s.handlerDeps.Billing.EstimateMediaCostMicroUSD(logicalModel, provider, estimatedUnits, margin)
	}
	pcs, rejected := s.preConsumeMedia(c, userID, c.GetString("key_type"), logicalModel, provider, estimatedMicro)
	if rejected {
		return
	}

	// TokenRouter's video endpoint is case-sensitive for some model IDs
	// (e.g. "MiniMax-Hailuo-2.3" works while "minimax-hailuo-2.3" returns 503).
	// Rewrite the upstream payload to the provider's canonical model name while
	// keeping the original body for retention/auditing.
	upstreamBody := bodyBytes
	if upstreamModelName != "" && upstreamModelName != logicalModel {
		req.Model = upstreamModelName
		if rewritten, err := json.Marshal(req); err == nil {
			upstreamBody = rewritten
		}
	}

	upstreamBaseURL := s.resolveBaseURL(upstreamKey, provider)

	httpReq, err := http.NewRequest("POST", upstreamBaseURL+"/video/generations", bytes.NewReader(upstreamBody))
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

	resp, err := sharedMediaClient.Do(httpReq)
	if err != nil {
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, req.Model, nil, time.Since(startTime).Milliseconds(), true)
		s.recordProviderHealthAsync(c, provider, logicalModel, 0, time.Since(startTime).Milliseconds(), err, err.Error())
		s.settleMediaPreConsume(c, pcs, logicalModel, provider, 0, classifyTransportError(err), time.Since(startTime).Milliseconds())

		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": "Upstream request failed",
			"type":    "upstream_error",
		}})
		return
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxMediaResponseBody+1))
	if err != nil {
		s.handlerDeps.Logger.Error("Failed to read upstream response body", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": "Failed to read upstream response",
			"type":    "upstream_error",
		}})
		return
	}
	if int64(len(body)) > maxMediaResponseBody {
		s.handlerDeps.Logger.Warn("Upstream media response too large",
			zap.Int64("limit", maxMediaResponseBody), zap.Int("received", len(body)))
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": "Upstream media response too large",
			"type":    "upstream_error",
		}})
		return
	}

	// Log audit.
	var videoCost float64
	if s.handlerDeps.Billing != nil {
		videoCost = s.handlerDeps.Billing.CalculateImageCost(logicalModel, req.Size, "", 1)
	}
	if s.handlerDeps.AuditLogger != nil {
		s.logAudit(c, audit.AuditEntry{
			Timestamp:     startTime,
			KeyID:         keyID,
			UpstreamKeyID: upstreamKey.ID,
			APIKeyHash:    keyHash,
			UserID:        userID,
			Provider:      provider,
			Model:         logicalModel,
			Cost:          videoCost,
			LatencyMs:     time.Since(startTime).Milliseconds(),
			StatusCode:    resp.StatusCode,
			RequestType:   "video_generation",
		})
	}

	// Check for upstream errors (4xx/5xx) before recording success metrics.
	if resp.StatusCode >= 400 {
		providerBreakers.RecordFailure(provider)
		if s.keyPoolManager != nil {
			errorMsg := string(body[:min(500, len(body))])
			s.keyPoolManager.ReportKeyFailure(provider, upstreamKey.ID, req.Model, resp.StatusCode, "upstream_error", "upstream_error", errorMsg)
			if (resp.StatusCode == 401 || resp.StatusCode == 403) ||
				(resp.StatusCode == 400 && !keypool.IsModelLevelError(errorMsg)) {
				s.keyPoolManager.ReportModelFailure(upstreamKey.ID, req.Model, errorMsg)
			}
		}
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, req.Model, nil, time.Since(startTime).Milliseconds(), true)
		s.recordProviderHealthAsync(c, provider, logicalModel, resp.StatusCode, time.Since(startTime).Milliseconds(), nil, string(body[:min(200, len(body))]))

		upErr := ClassifyUpstreamError(resp.StatusCode)
		s.settleMediaPreConsume(c, pcs, logicalModel, provider, resp.StatusCode, "upstream_error", time.Since(startTime).Milliseconds())
		c.Data(upErr.ClientStatus, "application/json", upErr.JSON())
		return
	}

	// Successful upstream dispatch.
	if s.keyPoolManager != nil {
		s.keyPoolManager.RecordUpstreamRequest(provider, upstreamKey.ID, req.Model)
	}
	s.recordUpstreamUsageAsync(upstreamKey.ID, provider, req.Model, nil, time.Since(startTime).Milliseconds(), false)
	s.recordProviderHealthAsync(c, provider, logicalModel, resp.StatusCode, time.Since(startTime).Milliseconds(), nil, "")
	providerBreakers.RecordSuccess(provider)

	// Forward token counts from upstream when reported.
	var upstreamVideoResp struct {
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			InputTokens      int `json:"input_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			OutputTokens     int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &upstreamVideoResp); err == nil {
		inputTokens := upstreamVideoResp.Usage.PromptTokens
		if inputTokens == 0 {
			inputTokens = upstreamVideoResp.Usage.InputTokens
		}
		outputTokens := upstreamVideoResp.Usage.CompletionTokens
		if outputTokens == 0 {
			outputTokens = upstreamVideoResp.Usage.OutputTokens
		}
		if inputTokens > 0 || outputTokens > 0 {
			c.Set("input_tokens", inputTokens)
			c.Set("output_tokens", outputTokens)
		}
	}

	// Detect async task creation before billing. Async tasks are billed when
	// the poll endpoint sees a terminal success state (see
	// handleGetVideoGeneration), so a task that fails upstream is never
	// charged — except for charge-on-dispatch providers (e.g. TokenRouter),
	// which bill at task creation regardless of outcome; those are billed
	// now and refunded by the poll endpoint on terminal failure. Without
	// Redis the dispatch-time billing path is kept.
	var async asyncTaskResponse
	isAsyncTask := false
	var taskID string
	if err := json.Unmarshal(body, &async); err == nil && (async.Data.TaskID != "" || async.TaskID != "") {
		isAsyncTask = true
		taskID = async.Data.TaskID
		if taskID == "" {
			taskID = async.TaskID
		}
	}

	c.Set("upstream_status_code", resp.StatusCode)
	chargeOnDispatch := s.asyncChargeOnDispatch(provider)
	if isAsyncTask && s.mediaAsyncBillingEnabled() {
		s.storeMediaTaskBilling(c, taskID, upstreamKey.ID, logicalModel, provider, videoCost, chargeOnDispatch)
		if chargeOnDispatch {
			// Upstream bills at task creation regardless of outcome: bill
			// now, refund on terminal failure at the poll endpoint.
			s.recordUsageFromContextByRequest(c, keyHash, upstreamKey.ID, logicalModel, provider, videoCost, time.Since(startTime).Milliseconds(), "")
		}
	} else {
		s.recordUsageFromContextByRequest(c, keyHash, upstreamKey.ID, logicalModel, provider, videoCost, time.Since(startTime).Milliseconds(), "")
	}
	s.settleMediaPreConsume(c, pcs, logicalModel, provider, resp.StatusCode, "", time.Since(startTime).Milliseconds())

	// Capture retention for video generation request.
	setRetentionContext(c, "openai", "openai", logicalModel, false)
	s.captureRetention(c, bodyBytes, body, provider, logicalModel, upstreamKey.ID, nil, time.Since(startTime).Milliseconds(), resp.StatusCode, false, "video_generation")

	if isAsyncTask {
		c.Data(resp.StatusCode, "application/json", wrapAsyncGenerationResponse(taskID, "video.generation", time.Now().Unix()))
		return
	}

	c.Data(resp.StatusCode, "application/json", body)
}

// handleGetVideoGeneration polls the upstream status of an async video task.
// It is read-only and does not bill again.
func (s *HandlerService) handleGetVideoGeneration(c *gin.Context) {
	startTime := time.Now()

	taskID := c.Param("task_id")
	if taskID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "Missing task_id",
			"type":    "invalid_request_error",
		}})
		return
	}
	// task_id is concatenated into the upstream URL (a request that carries
	// provider credentials); reject anything outside the safe alphabet.
	if !validTaskIDRE.MatchString(taskID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "Invalid task_id",
			"type":    "invalid_request_error",
		}})
		return
	}

	model := c.Query("model")
	if model == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "Missing required query parameter: model",
			"type":    "invalid_request_error",
			"code":    "MISSING_MODEL",
		}})
		return
	}

	// Enforce allowed_models restriction.
	if !checkAllowedModels(c, model) {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{
			"message": fmt.Sprintf("Model '%s' is not in your allowed models list.", model),
			"type":    "model_not_allowed",
			"code":    "MODEL_NOT_ALLOWED",
		}})
		return
	}

	provider, upstreamKey, upstreamModelName, isFallback, resolveErr := s.resolveProviderAndKey(c.Request.Context(), model, "normal", "")
	if resolveErr != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
			"message": resolveErr.Error(),
			"type":    "service_error",
		}})
		return
	}
	c.Set("is_fallback", isFallback)
	if isFallback {
		c.Set("actual_group_id", c.GetString("group_id"))
	}

	c.Set("provider", provider)
	c.Set("model", model)
	c.Set("is_stream", false)
	_ = upstreamModelName

	upstreamBaseURL := s.resolveBaseURL(upstreamKey, provider)

	httpReq, err := http.NewRequest("GET", upstreamBaseURL+"/video/generations/"+taskID, nil)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": "Failed to create upstream request",
			"type":    "upstream_error",
		}})
		return
	}
	httpReq.Header.Set("Authorization", "Bearer "+upstreamKey.ActualKey)
	s.applyForwardedHeaders(httpReq, c)

	resp, err := sharedHandlerClient.Do(httpReq)
	if err != nil {
		s.recordProviderHealthAsync(c, provider, model, 0, time.Since(startTime).Milliseconds(), err, err.Error())
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": "Upstream request failed",
			"type":    "upstream_error",
		}})
		return
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxMediaResponseBody+1))
	if err != nil {
		s.handlerDeps.Logger.Error("Failed to read upstream response body", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": "Failed to read upstream response",
			"type":    "upstream_error",
		}})
		return
	}
	if int64(len(body)) > maxMediaResponseBody {
		s.handlerDeps.Logger.Warn("Upstream media response too large",
			zap.Int64("limit", maxMediaResponseBody), zap.Int("received", len(body)))
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": "Upstream media response too large",
			"type":    "upstream_error",
		}})
		return
	}

	if resp.StatusCode >= 400 {
		providerBreakers.RecordFailure(provider)
		if s.keyPoolManager != nil {
			errorMsg := string(body[:min(500, len(body))])
			s.keyPoolManager.ReportKeyFailure(provider, upstreamKey.ID, model, resp.StatusCode, "upstream_error", "upstream_error", errorMsg)
		}
		s.recordProviderHealthAsync(c, provider, model, resp.StatusCode, time.Since(startTime).Milliseconds(), nil, string(body[:min(200, len(body))]))

		upErr := ClassifyUpstreamError(resp.StatusCode)
		c.Data(upErr.ClientStatus, "application/json", upErr.JSON())
		return
	}

	providerBreakers.RecordSuccess(provider)
	s.recordProviderHealthAsync(c, provider, model, resp.StatusCode, time.Since(startTime).Milliseconds(), nil, "")

	// Settle async tasks at a terminal state (exactly once, see
	// billAsyncMediaTask). Deferred-billing tasks are billed on terminal
	// success; charge-on-dispatch tasks were billed at dispatch and are
	// refunded here on terminal failure.
	if status, terminal := asyncTaskTerminalStatus(body); terminal {
		s.billAsyncMediaTask(c, taskID, status, time.Since(startTime).Milliseconds())
	}

	c.Data(resp.StatusCode, "application/json", body)
}

// wrapAsyncGenerationResponse returns a small OpenAI-ish envelope that exposes
// the upstream task_id as the response id. This lets clients poll the
// corresponding GET endpoint uniformly.
func wrapAsyncGenerationResponse(taskID, object string, created int64) []byte {
	payload, _ := json.Marshal(map[string]interface{}{
		"id":      taskID,
		"object":  object,
		"created": created,
		"data": []map[string]string{
			{"url": ""},
		},
	})
	return payload
}

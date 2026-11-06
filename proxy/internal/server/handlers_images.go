package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ai-proxy/ai-proxy-go/internal/audit"
	"github.com/ai-proxy/ai-proxy-go/pkg/keypool"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

var (
	errImageUpstreamCreate = errors.New("failed to create upstream request")
	errImageBodyRead       = errors.New("failed to read upstream response body")
	errImageBodyTooLarge   = errors.New("upstream media response too large")
)

// dispatchImageGeneration performs one image-generation upstream call and
// returns the response with its body fully read. forceClose bypasses the
// keep-alive pool so a retry lands on a fresh connection to the upstream LB
// (see the "No available channel" retry in handleImageGenerations).
//
// OpenRouter does not expose /images/generations: its image models generate via
// /chat/completions and return the image as a message part. When the upstream
// is OpenRouter we translate the OpenAI images request into a chat request and
// translate the chat response back into the OpenAI images shape
// ({data:[{b64_json}]}), so the rest of handleImageGenerations (billing,
// retention, response) is unchanged and clients keep using the standard
// /v1/images/generations endpoint.
func (s *HandlerService) dispatchImageGeneration(baseURL string, upstreamKey *keypool.KeyInternal, payload []byte, c *gin.Context, forceClose bool) (*http.Response, []byte, error) {
	endpointPath := "/images/generations"
	sendPayload := payload
	openRouter := isOpenRouterBaseURL(baseURL)
	if openRouter {
		chatBody, err := imagesRequestToOpenRouterChat(payload)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %v", errImageUpstreamCreate, err)
		}
		sendPayload = chatBody
		endpointPath = "/chat/completions"
	}

	httpReq, err := http.NewRequest("POST", baseURL+endpointPath, bytes.NewReader(sendPayload))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", errImageUpstreamCreate, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+upstreamKey.ActualKey)
	httpReq.Close = forceClose
	s.applyForwardedHeaders(httpReq, c)

	resp, err := sharedMediaClient.Do(httpReq)
	if err != nil {
		if resp != nil {
			_ = resp.Body.Close()
		}
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxMediaResponseBody+1))
	if err != nil {
		return nil, nil, errImageBodyRead
	}
	if int64(len(body)) > maxMediaResponseBody {
		return nil, nil, errImageBodyTooLarge
	}
	// Translate OpenRouter's chat response back into the OpenAI images shape so
	// the caller sees a normal {data:[{b64_json}]} body. Only rewrite on a 2xx
	// with a parseable image part; upstream error bodies pass through untouched
	// so existing error handling/classification still works.
	if openRouter && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if translated, ok := openRouterChatToImagesResponse(body); ok {
			body = translated
		}
	}
	return resp, body, nil
}

// isOpenRouterBaseURL reports whether an upstream base URL points at OpenRouter.
func isOpenRouterBaseURL(baseURL string) bool {
	return strings.Contains(strings.ToLower(baseURL), "openrouter.ai")
}

// imagesRequestToOpenRouterChat converts an OpenAI images-generation request
// into an OpenRouter chat request that asks for an image modality.
func imagesRequestToOpenRouterChat(payload []byte) ([]byte, error) {
	var in struct {
		Model  string `json:"model"`
		Prompt string `json:"prompt"`
		N      int    `json:"n,omitempty"`
	}
	if err := json.Unmarshal(payload, &in); err != nil {
		return nil, err
	}
	if in.Prompt == "" {
		return nil, fmt.Errorf("empty prompt")
	}
	chat := map[string]interface{}{
		"model":      in.Model, // already the upstream model name at dispatch time
		"modalities": []string{"image", "text"},
		"messages": []map[string]interface{}{
			{"role": "user", "content": in.Prompt},
		},
	}
	return json.Marshal(chat)
}

// openRouterChatToImagesResponse extracts image parts from an OpenRouter chat
// completion and reshapes them into an OpenAI images response. OpenRouter puts
// generated images in choices[].message.images[].image_url.url as a data URI
// (data:image/png;base64,....). Returns (imagesJSON, true) on success.
func openRouterChatToImagesResponse(chatBody []byte) ([]byte, bool) {
	var resp struct {
		Created int64 `json:"created"`
		Choices []struct {
			Message struct {
				Images []struct {
					ImageURL struct {
						URL string `json:"url"`
					} `json:"image_url"`
				} `json:"images"`
			} `json:"message"`
		} `json:"choices"`
		Usage map[string]interface{} `json:"usage"`
	}
	if err := json.Unmarshal(chatBody, &resp); err != nil {
		return nil, false
	}
	type imgData struct {
		B64JSON string `json:"b64_json,omitempty"`
		URL     string `json:"url,omitempty"`
	}
	var data []imgData
	for _, ch := range resp.Choices {
		for _, im := range ch.Message.Images {
			u := im.ImageURL.URL
			if u == "" {
				continue
			}
			if b64, ok := dataURIToBase64(u); ok {
				data = append(data, imgData{B64JSON: b64})
			} else {
				data = append(data, imgData{URL: u})
			}
		}
	}
	if len(data) == 0 {
		return nil, false // no image part — let the raw body/error path handle it
	}
	created := resp.Created
	if created == 0 {
		created = time.Now().Unix()
	}
	out := map[string]interface{}{"created": created, "data": data}
	if resp.Usage != nil {
		out["usage"] = resp.Usage
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, false
	}
	return b, true
}

// dataURIToBase64 returns the base64 payload of a data: URI (e.g.
// "data:image/png;base64,AAAA" -> "AAAA"). Returns (_, false) for non-data URIs
// (e.g. hosted https URLs), which are passed through as a url field instead.
func dataURIToBase64(u string) (string, bool) {
	if !strings.HasPrefix(u, "data:") {
		return "", false
	}
	i := strings.Index(u, ";base64,")
	if i < 0 {
		return "", false
	}
	return u[i+len(";base64,"):], true
}

// handleImageGenerations - Handle image generation requests
func (s *HandlerService) handleImageGenerations(c *gin.Context) {
	startTime := time.Now()

	// Read and preserve request body for retention
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
		Model          string `json:"model"`
		Prompt         string `json:"prompt"`
		N              int    `json:"n,omitempty"`
		Size           string `json:"size,omitempty"`
		Quality        string `json:"quality,omitempty"`
		ResponseFormat string `json:"response_format,omitempty"`
	}

	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "Invalid request: " + err.Error(),
			"type":    "invalid_request_error",
		}})
		return
	}

	// Bound the client-controlled image count before it reaches billing
	// (Portal multiplies the variant price by n); n <= 0 means "provider
	// default" and is normalized to 1 below.
	if req.N < 0 || req.N > maxImagesPerRequest {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": fmt.Sprintf("n must be between 1 and %d", maxImagesPerRequest),
			"type":    "invalid_request_error",
		}})
		return
	}

	keyID := c.GetString(string(keyIDKey))
	userID := c.GetString(string(userIDKey))
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

	// Dual-track routing for images
	track := resolveTrack(c)
	c.Set("routing_track", track)

	var provider string
	var upstreamKey *keypool.KeyInternal
	var upstreamModelName string
	var isFallback bool
	var imgErr error

	switch track {
	case "bedrock":
		provider, upstreamKey, upstreamModelName, isFallback, imgErr = s.resolveBedrockProviderAndKey(c.Request.Context(), req.Model, "")
		if imgErr != nil {
			// Bedrock images not available for this model — fallback to standard
			provider, upstreamKey, upstreamModelName, isFallback, imgErr = s.resolveProviderAndKey(c.Request.Context(), req.Model, "normal", "")
			c.Set("original_track", "bedrock")
			c.Set("routing_track", "standard")
		}
	case "mantle":
		provider, upstreamKey, upstreamModelName, isFallback, imgErr = s.resolveMantleProviderAndKey(c.Request.Context(), req.Model, "")
		if imgErr != nil {
			provider, upstreamKey, upstreamModelName, isFallback, imgErr = s.resolveProviderAndKey(c.Request.Context(), req.Model, "normal", "")
			c.Set("original_track", "mantle")
			c.Set("routing_track", "standard")
		}
	default:
		provider, upstreamKey, upstreamModelName, isFallback, imgErr = s.resolveProviderAndKey(c.Request.Context(), req.Model, "normal", "")
	}
	if imgErr != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
			"message": imgErr.Error(),
			"type":    "service_error",
		}})
		return
	}
	// Mirror handlers_chat.go: knowing WHICH upstream key served an image
	// request is essential for diagnosing fast upstream failures.
	if upstreamKey != nil {
		s.handlerDeps.Logger.Info("Selected upstream key (image)",
			zap.String("provider", provider),
			zap.String("key_id", upstreamKey.ID),
			zap.String("key_preview", upstreamKey.KeyPreview),
			zap.String("model", req.Model),
			zap.String("upstream_model", upstreamModelName),
			zap.Bool("is_fallback", isFallback))
	}
	c.Set("is_fallback", isFallback)
	if isFallback {
		c.Set("actual_group_id", c.GetString("group_id"))
	}

	// Preserve logical model for reporting before switching to provider-specific name.
	logicalModel := req.Model

	// Reject video/audio model types routed here by mistake (portal-synced
	// model_type), before any pre-consume or dispatch.
	if s.keyPoolManager != nil {
		if mt := s.keyPoolManager.GetModelType(provider, logicalModel); mt == "video" || mt == "audio" {
			ep := mediaEndpointForType(mt)
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
				"message": fmt.Sprintf("model '%s' (type: %s) must use %s", logicalModel, mt, ep),
				"type":    "invalid_request_error",
				"code":    "MEDIA_MODEL_WRONG_ENDPOINT",
			}})
			return
		}
	}

	// Media metadata for Portal media-mode billing.
	imageCount := req.N
	if imageCount < 1 {
		imageCount = 1
	}
	c.Set("image_size", req.Size)
	c.Set("image_quality", req.Quality)
	c.Set("image_count", imageCount)

	// Set context values for metrics middleware
	c.Set("provider", provider)
	c.Set("model", logicalModel)
	c.Set("is_stream", false)

	// Replace model name with provider-specific name for upstream request
	req.Model = upstreamModelName

	upstreamBaseURL := s.resolveBaseURL(upstreamKey, provider)

	// Pre-consume: reserve the estimated media cost before dispatch so a
	// zero-balance key cannot push generation cost onto the platform. The
	// estimate uses the declared image count as the billed unit.
	estimatedUnits := imageCount
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

	// Forward request with upstream key. TokenRouter answers "No available
	// channel for model ... under group default" fast when its channel pool
	// for the model is momentarily depleted — availability fluctuates on a
	// minute timescale, so retry a few times with a short backoff on fresh
	// connections. Nothing is generated for these errors, so retries are free
	// for the client.
	const maxNoChannelRetries = 3
	// Transport stalls on a poisoned keep-alive connection are retried once
	// on a fresh connection: providers like LZ under throttle have been
	// observed silently blackholing an established connection, and Go will
	// not re-dispatch a POST on a stale conn. Nothing reached the upstream
	// application, so the retry cannot double-bill.
	const maxTransportRetries = 1
	var resp *http.Response
	var body []byte
	var dispatchErr error
	noChannelRetries := 0
	transportRetries := 0
	for attempt := 0; attempt < 1+maxNoChannelRetries+maxTransportRetries; attempt++ {
		resp, body, dispatchErr = s.dispatchImageGeneration(upstreamBaseURL, upstreamKey, bodyBytes, c, attempt > 0)
		if dispatchErr != nil {
			// Retry plain transport errors (timeout, reset, EOF) once on a
			// fresh connection. Body/read/size errors are not transport
			// stalls and fail immediately.
			if transportRetries < maxTransportRetries &&
				!errors.Is(dispatchErr, errImageBodyRead) &&
				!errors.Is(dispatchErr, errImageBodyTooLarge) &&
				!errors.Is(dispatchErr, errImageUpstreamCreate) {
				transportRetries++
				s.handlerDeps.Logger.Warn("Upstream transport stalled; retrying image request on fresh connection",
					zap.String("provider", provider),
					zap.String("model", logicalModel),
					zap.String("key_id", upstreamKey.ID),
					zap.Error(dispatchErr))
				continue
			}
			break
		}
		if resp.StatusCode == http.StatusServiceUnavailable && noChannelRetries < maxNoChannelRetries &&
			bytes.Contains(body, []byte("No available channel")) {
			noChannelRetries++
			s.handlerDeps.Logger.Warn("Upstream had no available channel; backing off and retrying",
				zap.String("provider", provider),
				zap.String("model", logicalModel),
				zap.String("key_id", upstreamKey.ID),
				zap.Int("attempt", noChannelRetries))
			// Fresh connection per retry; brief backoff lets TR's channel pool recover.
			time.Sleep(3 * time.Second)
			continue
		}
		break
	}
	switch {
	case errors.Is(dispatchErr, errImageBodyRead):
		s.handlerDeps.Logger.Error("Failed to read upstream response body", zap.Error(dispatchErr))
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": "Failed to read upstream response",
			"type":    "upstream_error",
		}})
		return
	case errors.Is(dispatchErr, errImageBodyTooLarge):
		s.handlerDeps.Logger.Warn("Upstream media response too large",
			zap.Int64("limit", maxMediaResponseBody), zap.Int("received", len(body)))
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": "Upstream media response too large",
			"type":    "upstream_error",
		}})
		return
	case dispatchErr != nil:
		// Record upstream usage (error)
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, req.Model, nil, time.Since(startTime).Milliseconds(), true)
		// Record provider health (transport error)
		s.recordProviderHealthAsync(c, provider, logicalModel, 0, time.Since(startTime).Milliseconds(), dispatchErr, dispatchErr.Error())
		s.settleMediaPreConsume(c, pcs, logicalModel, provider, 0, classifyTransportError(dispatchErr), time.Since(startTime).Milliseconds())

		msg := "Upstream request failed"
		if errors.Is(dispatchErr, errImageUpstreamCreate) {
			msg = "Failed to create upstream request"
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": msg,
			"type":    "upstream_error",
		}})
		return
	}
	defer func() { _ = resp.Body.Close() }()

	// Log audit
	if s.handlerDeps.AuditLogger != nil {
		var auditCost float64
		if s.handlerDeps.Billing != nil {
			auditCost = s.handlerDeps.Billing.CalculateImageCost(logicalModel, req.Size, req.Quality, req.N)
		}
		s.logAudit(c, audit.AuditEntry{
			Timestamp:     startTime,
			KeyID:         keyID,
			UpstreamKeyID: upstreamKey.ID,
			APIKeyHash:    keyHash,
			UserID:        userID,
			Provider:      provider,
			Model:         logicalModel,
			Cost:          auditCost,
			LatencyMs:     time.Since(startTime).Milliseconds(),
			StatusCode:    resp.StatusCode,
			RequestType:   "image_generation",
		})
	}

	// Check for upstream errors (4xx/5xx) before recording success metrics.
	if resp.StatusCode >= 400 {
		errorMsg := string(body[:min(500, len(body))])
		// "No available channel" is a transient capacity signal for one model,
		// not a provider outage; counting it toward the global provider breaker
		// would knock out every other model on that provider.
		noChannel := bytes.Contains(body, []byte("No available channel"))
		if !noChannel {
			providerBreakers.RecordFailure(provider)
		}
		// Log the upstream body: image upstreams fail fast and opaquely (e.g.
		// TokenRouter returns bare 5xx), so the body is essential for diagnosis.
		s.handlerDeps.Logger.Warn("Upstream image error",
			zap.String("provider", provider),
			zap.String("model", logicalModel),
			zap.String("upstream_model", upstreamModelName),
			zap.String("key_id", upstreamKey.ID),
			zap.Int("status", resp.StatusCode),
			zap.String("body", errorMsg))
		if s.keyPoolManager != nil {
			s.keyPoolManager.ReportKeyFailure(provider, upstreamKey.ID, req.Model, resp.StatusCode, "upstream_error", "upstream_error", errorMsg)
			// Model-level failure only for key-specific access issues; general model
			// errors mean the model doesn't exist on the provider (see handlers_chat.go).
			if (resp.StatusCode == 401 || resp.StatusCode == 403) ||
				(resp.StatusCode == 400 && !keypool.IsModelLevelError(errorMsg)) {
				s.keyPoolManager.ReportModelFailure(upstreamKey.ID, req.Model, errorMsg)
			}
		}
		// Record upstream usage (error)
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, req.Model, nil, time.Since(startTime).Milliseconds(), true)
		// Record provider health (upstream error)
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

	// Record upstream usage for provider reconciliation (image generation uses request count only)
	s.recordUpstreamUsageAsync(upstreamKey.ID, provider, req.Model, nil, time.Since(startTime).Milliseconds(), false)
	// Record provider health (success)
	s.recordProviderHealthAsync(c, provider, logicalModel, resp.StatusCode, time.Since(startTime).Milliseconds(), nil, "")
	providerBreakers.RecordSuccess(provider)

	// Use the upstream-returned image size and token counts when available.
	// Some providers (e.g. TokenRouter/ByteDance) ignore or reject the
	// requested size and return the actual generated dimensions; billing must
	// reflect those dimensions to match the configured variant prices.
	var upstreamImageResp struct {
		Data []struct {
			Size string `json:"size"`
		} `json:"data"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			InputTokens      int `json:"input_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			OutputTokens     int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &upstreamImageResp); err == nil {
		if len(upstreamImageResp.Data) > 0 && upstreamImageResp.Data[0].Size != "" {
			c.Set("image_size", upstreamImageResp.Data[0].Size)
		}
		inputTokens := upstreamImageResp.Usage.PromptTokens
		if inputTokens == 0 {
			inputTokens = upstreamImageResp.Usage.InputTokens
		}
		outputTokens := upstreamImageResp.Usage.CompletionTokens
		if outputTokens == 0 {
			outputTokens = upstreamImageResp.Usage.OutputTokens
		}
		if inputTokens > 0 || outputTokens > 0 {
			c.Set("input_tokens", inputTokens)
			c.Set("output_tokens", outputTokens)
		}
	}

	// Detect async task creation before billing. Async tasks are billed when
	// the poll endpoint sees a terminal success state (see
	// handleGetImageGeneration), so a task that fails upstream is never
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

	// Record user-side usage (images are billed per request, no token counts).
	var imgCost float64
	if s.handlerDeps.Billing != nil {
		imgCost = s.handlerDeps.Billing.CalculateImageCost(logicalModel, req.Size, req.Quality, req.N)
	}
	c.Set("upstream_status_code", resp.StatusCode)
	chargeOnDispatch := s.asyncChargeOnDispatch(provider)
	if isAsyncTask && s.mediaAsyncBillingEnabled() {
		s.storeMediaTaskBilling(c, taskID, upstreamKey.ID, logicalModel, provider, imgCost, chargeOnDispatch)
		if chargeOnDispatch {
			// Upstream bills at task creation regardless of outcome: bill
			// now, refund on terminal failure at the poll endpoint.
			s.recordUsageFromContextByRequest(c, keyHash, upstreamKey.ID, logicalModel, provider, imgCost, time.Since(startTime).Milliseconds(), "")
		}
	} else {
		s.recordUsageFromContextByRequest(c, keyHash, upstreamKey.ID, logicalModel, provider, imgCost, time.Since(startTime).Milliseconds(), "")
	}
	s.settleMediaPreConsume(c, pcs, logicalModel, provider, resp.StatusCode, "", time.Since(startTime).Milliseconds())

	// Capture retention for image generation request
	setRetentionContext(c, "openai", "openai", logicalModel, false)
	s.captureRetention(c, bodyBytes, body, provider, logicalModel, upstreamKey.ID, nil, time.Since(startTime).Milliseconds(), resp.StatusCode, false, "image_generation")

	if isAsyncTask {
		c.Data(resp.StatusCode, "application/json", wrapAsyncGenerationResponse(taskID, "image.generation", time.Now().Unix()))
		return
	}

	c.Data(resp.StatusCode, "application/json", body)
}

// handleGetImageGeneration polls the upstream status of an async image task.
// It is read-only and does not bill again.
func (s *HandlerService) handleGetImageGeneration(c *gin.Context) {
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

	httpReq, err := http.NewRequest("GET", upstreamBaseURL+"/images/generations/"+taskID, nil)
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

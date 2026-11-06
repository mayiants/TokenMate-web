package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/ai-proxy/ai-proxy-go/internal/audit"
	"github.com/ai-proxy/ai-proxy-go/pkg/keypool"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// maxImageEditBodySize bounds the multipart edit request (input image(s) +
// fields). Larger than the JSON handlers because edits carry an image file.
const maxImageEditBodySize = 32 * 1024 * 1024 // 32 MB

// handleImageEdits - Handle image edit requests (OpenAI /v1/images/edits
// compatible). The client sends multipart/form-data with one or more image
// files plus text fields (model, prompt, size, quality, n, ...). The handler
// resolves the upstream key from the model field, rebuilds an equivalent
// multipart body with the upstream model name, and forwards it. Response,
// billing (per request), pre-consume, and async-task handling mirror
// handleImageGenerations.
func (s *HandlerService) handleImageEdits(c *gin.Context) {
	startTime := time.Now()

	// Read and preserve the raw multipart body for retention
	bodyBytes, err := io.ReadAll(io.LimitReader(c.Request.Body, maxImageEditBodySize))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "Failed to read request body",
			"type":    "invalid_request_error",
		}})
		return
	}
	if int64(len(bodyBytes)) >= maxImageEditBodySize {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "Request body too large (max 32 MB)",
			"type":    "invalid_request_error",
		}})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	// Parse the client multipart body
	boundary := ""
	if ct := c.Request.Header.Get("Content-Type"); ct != "" {
		if _, params, err := mime.ParseMediaType(ct); err == nil {
			boundary = params["boundary"]
		}
	}
	if boundary == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "Content-Type must be multipart/form-data with a boundary",
			"type":    "invalid_request_error",
		}})
		return
	}
	mr := multipart.NewReader(bytes.NewReader(bodyBytes), boundary)
	form, err := mr.ReadForm(maxImageEditBodySize)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "Failed to parse multipart form: " + err.Error(),
			"type":    "invalid_request_error",
		}})
		return
	}
	defer func() { _ = form.RemoveAll() }()

	fieldValue := func(name string) string {
		if vs := form.Value[name]; len(vs) > 0 {
			return vs[0]
		}
		return ""
	}

	model := fieldValue("model")
	prompt := fieldValue("prompt")
	if model == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "Missing required form field: model",
			"type":    "invalid_request_error",
			"code":    "MISSING_MODEL",
		}})
		return
	}
	if prompt == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "Missing required form field: prompt",
			"type":    "invalid_request_error",
		}})
		return
	}

	var n int
	if _, err := fmt.Sscan(fieldValue("n"), &n); err != nil {
		n = 0 // provider default; normalized to 1 below
	}
	if n < 0 || n > maxImagesPerRequest {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": fmt.Sprintf("n must be between 1 and %d", maxImagesPerRequest),
			"type":    "invalid_request_error",
		}})
		return
	}
	size := fieldValue("size")
	quality := fieldValue("quality")

	// At least one image file is required (OpenAI accepts "image" or "image[]")
	imageFiles := form.File["image"]
	imageFiles = append(imageFiles, form.File["image[]"]...)
	if len(imageFiles) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "Missing required form file: image",
			"type":    "invalid_request_error",
		}})
		return
	}

	keyID := c.GetString(string(keyIDKey))
	userID := c.GetString(string(userIDKey))
	keyHash := c.GetString("key_hash")

	// Enforce allowed_models restriction
	if !checkAllowedModels(c, model) {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{
			"message": fmt.Sprintf("Model '%s' is not in your allowed models list.", model),
			"type":    "model_not_allowed",
			"code":    "MODEL_NOT_ALLOWED",
		}})
		return
	}

	// Dual-track routing (same as generations)
	track := resolveTrack(c)
	c.Set("routing_track", track)

	var provider string
	var upstreamKey *keypool.KeyInternal
	var upstreamModelName string
	var isFallback bool
	var imgErr error

	switch track {
	case "bedrock":
		provider, upstreamKey, upstreamModelName, isFallback, imgErr = s.resolveBedrockProviderAndKey(c.Request.Context(), model, "")
		if imgErr != nil {
			provider, upstreamKey, upstreamModelName, isFallback, imgErr = s.resolveProviderAndKey(c.Request.Context(), model, "normal", "")
			c.Set("original_track", "bedrock")
			c.Set("routing_track", "standard")
		}
	case "mantle":
		provider, upstreamKey, upstreamModelName, isFallback, imgErr = s.resolveMantleProviderAndKey(c.Request.Context(), model, "")
		if imgErr != nil {
			provider, upstreamKey, upstreamModelName, isFallback, imgErr = s.resolveProviderAndKey(c.Request.Context(), model, "normal", "")
			c.Set("original_track", "mantle")
			c.Set("routing_track", "standard")
		}
	default:
		provider, upstreamKey, upstreamModelName, isFallback, imgErr = s.resolveProviderAndKey(c.Request.Context(), model, "normal", "")
	}
	if imgErr != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
			"message": imgErr.Error(),
			"type":    "service_error",
		}})
		return
	}
	c.Set("is_fallback", isFallback)
	if isFallback {
		c.Set("actual_group_id", c.GetString("group_id"))
	}

	logicalModel := model

	// Reject video/audio model types routed here by mistake
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

	imageCount := n
	if imageCount < 1 {
		imageCount = 1
	}
	c.Set("image_size", size)
	c.Set("image_quality", quality)
	c.Set("image_count", imageCount)

	c.Set("provider", provider)
	c.Set("model", logicalModel)
	c.Set("is_stream", false)

	upstreamBaseURL := s.resolveBaseURL(upstreamKey, provider)

	// Pre-consume: reserve the estimated media cost before dispatch
	estimatedUnits := imageCount
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

	// Rebuild the multipart body for the upstream request: copy every text
	// field (with the model swapped for the upstream model name) and re-attach
	// every file part under its original field name and filename.
	var upstreamBuf bytes.Buffer
	w := multipart.NewWriter(&upstreamBuf)
	for name, values := range form.Value {
		for _, v := range values {
			fieldName := name
			fieldVal := v
			if name == "model" {
				fieldVal = upstreamModelName
			}
			if err := w.WriteField(fieldName, fieldVal); err != nil {
				c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
					"message": "Failed to rebuild multipart form",
					"type":    "upstream_error",
				}})
				return
			}
		}
	}
	writeFiles := func(field string, files []*multipart.FileHeader) error {
		for _, fh := range files {
			part, err := w.CreateFormFile(field, fh.Filename)
			if err != nil {
				return err
			}
			f, err := fh.Open()
			if err != nil {
				return err
			}
			if _, err := io.Copy(part, f); err != nil {
				_ = f.Close()
				return err
			}
			_ = f.Close()
		}
		return nil
	}
	if err := writeFiles("image", form.File["image"]); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": "Failed to rebuild multipart form",
			"type":    "upstream_error",
		}})
		return
	}
	if err := writeFiles("image[]", form.File["image[]"]); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": "Failed to rebuild multipart form",
			"type":    "upstream_error",
		}})
		return
	}
	if err := w.Close(); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": "Failed to rebuild multipart form",
			"type":    "upstream_error",
		}})
		return
	}

	httpReq, err := http.NewRequest("POST", upstreamBaseURL+"/images/edits", &upstreamBuf)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": "Failed to create upstream request",
			"type":    "upstream_error",
		}})
		return
	}
	httpReq.Header.Set("Content-Type", w.FormDataContentType())
	httpReq.Header.Set("Authorization", "Bearer "+upstreamKey.ActualKey)
	s.applyForwardedHeaders(httpReq, c)

	resp, err := sharedMediaClient.Do(httpReq)
	if err != nil {
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, upstreamModelName, nil, time.Since(startTime).Milliseconds(), true)
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

	// Log audit
	if s.handlerDeps.AuditLogger != nil {
		var auditCost float64
		if s.handlerDeps.Billing != nil {
			auditCost = s.handlerDeps.Billing.CalculateImageCost(logicalModel, size, quality, n)
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
			RequestType:   "image_edit",
		})
	}

	if resp.StatusCode >= 400 {
		providerBreakers.RecordFailure(provider)
		if s.keyPoolManager != nil {
			errorMsg := string(body[:min(500, len(body))])
			s.keyPoolManager.ReportKeyFailure(provider, upstreamKey.ID, upstreamModelName, resp.StatusCode, "upstream_error", "upstream_error", errorMsg)
			if (resp.StatusCode == 401 || resp.StatusCode == 403) ||
				(resp.StatusCode == 400 && !keypool.IsModelLevelError(errorMsg)) {
				s.keyPoolManager.ReportModelFailure(upstreamKey.ID, upstreamModelName, errorMsg)
			}
		}
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, upstreamModelName, nil, time.Since(startTime).Milliseconds(), true)
		s.recordProviderHealthAsync(c, provider, logicalModel, resp.StatusCode, time.Since(startTime).Milliseconds(), nil, string(body[:min(200, len(body))]))

		upErr := ClassifyUpstreamError(resp.StatusCode)
		s.settleMediaPreConsume(c, pcs, logicalModel, provider, resp.StatusCode, "upstream_error", time.Since(startTime).Milliseconds())
		c.Data(upErr.ClientStatus, "application/json", upErr.JSON())
		return
	}

	if s.keyPoolManager != nil {
		s.keyPoolManager.RecordUpstreamRequest(provider, upstreamKey.ID, upstreamModelName)
	}
	s.recordUpstreamUsageAsync(upstreamKey.ID, provider, upstreamModelName, nil, time.Since(startTime).Milliseconds(), false)
	s.recordProviderHealthAsync(c, provider, logicalModel, resp.StatusCode, time.Since(startTime).Milliseconds(), nil, "")
	providerBreakers.RecordSuccess(provider)

	// Use the upstream-returned image size and token counts when available
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

	// Detect async task creation before billing (same as generations)
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

	var imgCost float64
	if s.handlerDeps.Billing != nil {
		imgCost = s.handlerDeps.Billing.CalculateImageCost(logicalModel, size, quality, n)
	}
	c.Set("upstream_status_code", resp.StatusCode)
	chargeOnDispatch := s.asyncChargeOnDispatch(provider)
	if isAsyncTask && s.mediaAsyncBillingEnabled() {
		s.storeMediaTaskBilling(c, taskID, upstreamKey.ID, logicalModel, provider, imgCost, chargeOnDispatch)
		if chargeOnDispatch {
			s.recordUsageFromContextByRequest(c, keyHash, upstreamKey.ID, logicalModel, provider, imgCost, time.Since(startTime).Milliseconds(), "")
		}
	} else {
		s.recordUsageFromContextByRequest(c, keyHash, upstreamKey.ID, logicalModel, provider, imgCost, time.Since(startTime).Milliseconds(), "")
	}
	s.settleMediaPreConsume(c, pcs, logicalModel, provider, resp.StatusCode, "", time.Since(startTime).Milliseconds())

	// Capture retention for image edit request (raw multipart body)
	c.Set("request_body", bodyBytes)
	setRetentionContext(c, "openai", "openai", logicalModel, false)
	s.captureRetention(c, bodyBytes, body, provider, logicalModel, upstreamKey.ID, nil, time.Since(startTime).Milliseconds(), resp.StatusCode, false, "image_edit")

	if isAsyncTask {
		c.Data(resp.StatusCode, "application/json", wrapAsyncGenerationResponse(taskID, "image.generation", time.Now().Unix()))
		return
	}

	c.Data(resp.StatusCode, "application/json", body)
}

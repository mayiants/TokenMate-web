package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ai-proxy/ai-proxy-go/internal/audit"
	"github.com/ai-proxy/ai-proxy-go/pkg/keypool"
	"github.com/ai-proxy/ai-proxy-go/pkg/relay"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// handleAudioTranscriptions - Handle audio transcription requests
func (s *HandlerService) handleAudioTranscriptions(c *gin.Context) {
	startTime := time.Now()

	keyID := c.GetString(string(keyIDKey))
	userID := c.GetString(string(userIDKey))
	keyHash := c.GetString("key_hash")

	// Audio requests are multipart/form-data, forward as-is
	provider := "openai"
	_, _ = relay.GetProviderConfig(provider)

	// Set context values for metrics middleware
	c.Set("provider", provider)
	c.Set("model", "whisper-1")
	c.Set("is_stream", false)

	// Enforce allowed_models restriction for audio (fixed whisper model)
	if !checkAllowedModels(c, "whisper-1") {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{
			"message": "Audio transcription model 'whisper-1' is not in your allowed models list.",
			"type":    "model_not_allowed",
			"code":    "MODEL_NOT_ALLOWED",
		}})
		return
	}

	// Get upstream key from pool
	upstreamKey, err := s.getUpstreamKey(provider, "normal", "", "")
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
			"message": err.Error(),
			"type":    "service_error",
		}})
		return
	}

	upstreamBaseURL := s.resolveBaseURL(upstreamKey, provider)

	httpReq, err := http.NewRequest("POST", upstreamBaseURL+"/audio/transcriptions", c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": "Failed to create upstream request",
			"type":    "upstream_error",
		}})
		return
	}
	httpReq.Header.Set("Authorization", "Bearer "+upstreamKey.ActualKey)
	// Copy content type for multipart
	httpReq.Header.Set("Content-Type", c.GetHeader("Content-Type"))
	s.applyForwardedHeaders(httpReq, c)

	resp, err := sharedHandlerClient.Do(httpReq)
	if err != nil {
		// Record upstream usage (error)
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, "whisper-1", nil, time.Since(startTime).Milliseconds(), true)
		// Record provider health (transport error)
		s.recordProviderHealthAsync(c, provider, "whisper-1", 0, time.Since(startTime).Milliseconds(), err, err.Error())

		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": "Upstream request failed",
			"type":    "upstream_error",
		}})
		return
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.handlerDeps.Logger.Error("Failed to read upstream response body", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": "Failed to read upstream response",
			"type":    "upstream_error",
		}})
		return
	}

	// Log audit
	if s.handlerDeps.AuditLogger != nil {
		s.logAudit(c, audit.AuditEntry{
			Timestamp:     startTime,
			KeyID:         keyID,
			UpstreamKeyID: upstreamKey.ID,
			APIKeyHash:    keyHash,
			UserID:        userID,
			Provider:      provider,
			LatencyMs:     time.Since(startTime).Milliseconds(),
			StatusCode:    resp.StatusCode,
			RequestType:   "audio_transcription",
		})
	}

	// Check for upstream errors (4xx/5xx) before recording success metrics.
	if resp.StatusCode >= 400 {
		providerBreakers.RecordFailure(provider)
		if s.keyPoolManager != nil {
			errorMsg := string(body[:min(500, len(body))])
			s.keyPoolManager.ReportKeyFailure(provider, upstreamKey.ID, "whisper-1", resp.StatusCode, "upstream_error", "upstream_error", errorMsg)
			// Model-level failure only for key-specific access issues; general model
			// errors mean the model doesn't exist on the provider (see handlers_chat.go).
			if (resp.StatusCode == 401 || resp.StatusCode == 403) ||
				(resp.StatusCode == 400 && !keypool.IsModelLevelError(errorMsg)) {
				s.keyPoolManager.ReportModelFailure(upstreamKey.ID, "whisper-1", errorMsg)
			}
		}
		// Record upstream usage (error)
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, "whisper-1", nil, time.Since(startTime).Milliseconds(), true)
		// Record provider health (upstream error)
		s.recordProviderHealthAsync(c, provider, "whisper-1", resp.StatusCode, time.Since(startTime).Milliseconds(), nil, string(body[:min(200, len(body))]))

		upErr := ClassifyUpstreamError(resp.StatusCode)
		c.Data(upErr.ClientStatus, "application/json", upErr.JSON())
		return
	}

	// Successful upstream dispatch.
	if s.keyPoolManager != nil {
		s.keyPoolManager.RecordUpstreamRequest(provider, upstreamKey.ID, "whisper-1")
	}

	// Record upstream usage for provider reconciliation
	s.recordUpstreamUsageAsync(upstreamKey.ID, provider, "whisper-1", nil, time.Since(startTime).Milliseconds(), false)
	// Record provider health (success)
	s.recordProviderHealthAsync(c, provider, "whisper-1", resp.StatusCode, time.Since(startTime).Milliseconds(), nil, "")
	providerBreakers.RecordSuccess(provider)

	// Record user-side usage (audio is billed per request, no token counts).
	var audioCost float64
	if s.handlerDeps.Billing != nil {
		audioCost = s.handlerDeps.Billing.CalculateImageCost("whisper-1", "", "", 1)
	}
	c.Set("upstream_status_code", resp.StatusCode)
	s.recordUsageFromContextByRequest(c, keyHash, upstreamKey.ID, "whisper-1", provider, audioCost, time.Since(startTime).Milliseconds(), "")

	// Audio lacks precise token usage; use an empirical token estimate for
	// upstream key-pool quota tracking.
	if s.keyPoolManager != nil {
		s.keyPoolManager.RecordUpstreamTokens(provider, upstreamKey.ID, "whisper-1", 256, 0)
	}
	if upstreamKey.QuotaLimit > 0 {
		upstreamKey.AddQuotaUsed(256)
	}

	// Capture retention metadata for audio transcription (binary body intentionally omitted)
	setRetentionContext(c, "openai", "openai", "whisper-1", false)
	s.captureRetention(c, nil, body, provider, "whisper-1", upstreamKey.ID, nil, time.Since(startTime).Milliseconds(), resp.StatusCode, false, "audio_transcription")

	c.Data(resp.StatusCode, "application/json", body)
}

// handleAudioTranslations - Handle audio translation requests
func (s *HandlerService) handleAudioTranslations(c *gin.Context) {
	startTime := time.Now()

	keyID := c.GetString(string(keyIDKey))
	userID := c.GetString(string(userIDKey))
	keyHash := c.GetString("key_hash")

	provider := "openai"
	_, _ = relay.GetProviderConfig(provider)

	// Set context values for metrics middleware
	c.Set("provider", provider)
	c.Set("model", "whisper-1")
	c.Set("is_stream", false)

	// Enforce allowed_models restriction for audio translation (fixed whisper model)
	if !checkAllowedModels(c, "whisper-1") {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{
			"message": "Audio translation model 'whisper-1' is not in your allowed models list.",
			"type":    "model_not_allowed",
			"code":    "MODEL_NOT_ALLOWED",
		}})
		return
	}

	// Get upstream key from pool
	upstreamKey, err := s.getUpstreamKey(provider, "normal", "", "")
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
			"message": err.Error(),
			"type":    "service_error",
		}})
		return
	}

	upstreamBaseURL := s.resolveBaseURL(upstreamKey, provider)

	httpReq, err := http.NewRequest("POST", upstreamBaseURL+"/audio/translations", c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": "Failed to create upstream request",
			"type":    "upstream_error",
		}})
		return
	}
	httpReq.Header.Set("Authorization", "Bearer "+upstreamKey.ActualKey)
	httpReq.Header.Set("Content-Type", c.GetHeader("Content-Type"))
	s.applyForwardedHeaders(httpReq, c)

	resp, err := sharedHandlerClient.Do(httpReq)
	if err != nil {
		// Record upstream usage (error)
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, "whisper-1", nil, time.Since(startTime).Milliseconds(), true)
		// Record provider health (transport error)
		s.recordProviderHealthAsync(c, provider, "whisper-1", 0, time.Since(startTime).Milliseconds(), err, err.Error())
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": "Upstream request failed",
			"type":    "upstream_error",
		}})
		return
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.handlerDeps.Logger.Error("Failed to read upstream response body", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": "Failed to read upstream response",
			"type":    "upstream_error",
		}})
		return
	}

	if s.handlerDeps.AuditLogger != nil {
		s.logAudit(c, audit.AuditEntry{
			Timestamp:     startTime,
			KeyID:         keyID,
			UpstreamKeyID: upstreamKey.ID,
			APIKeyHash:    keyHash,
			UserID:        userID,
			Provider:      provider,
			LatencyMs:     time.Since(startTime).Milliseconds(),
			StatusCode:    resp.StatusCode,
			RequestType:   "audio_translation",
		})
	}
	// Check for upstream errors (4xx/5xx) before recording success metrics.
	if resp.StatusCode >= 400 {
		providerBreakers.RecordFailure(provider)
		if s.keyPoolManager != nil {
			errorMsg := string(body[:min(500, len(body))])
			s.keyPoolManager.ReportKeyFailure(provider, upstreamKey.ID, "whisper-1", resp.StatusCode, "upstream_error", "upstream_error", errorMsg)
			if (resp.StatusCode == 401 || resp.StatusCode == 403) ||
				(resp.StatusCode == 400 && !keypool.IsModelLevelError(errorMsg)) {
				s.keyPoolManager.ReportModelFailure(upstreamKey.ID, "whisper-1", errorMsg)
			}
		}
		// Record upstream usage (error)
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, "whisper-1", nil, time.Since(startTime).Milliseconds(), true)
		// Record provider health (upstream error)
		s.recordProviderHealthAsync(c, provider, "whisper-1", resp.StatusCode, time.Since(startTime).Milliseconds(), nil, string(body[:min(200, len(body))]))

		upErr := ClassifyUpstreamError(resp.StatusCode)
		c.Data(upErr.ClientStatus, "application/json", upErr.JSON())
		return
	}

	// Successful upstream dispatch.
	if s.keyPoolManager != nil {
		s.keyPoolManager.RecordUpstreamRequest(provider, upstreamKey.ID, "whisper-1")
	}

	// Record upstream usage for provider reconciliation
	s.recordUpstreamUsageAsync(upstreamKey.ID, provider, "whisper-1", nil, time.Since(startTime).Milliseconds(), false)
	// Record provider health (success)
	s.recordProviderHealthAsync(c, provider, "whisper-1", resp.StatusCode, time.Since(startTime).Milliseconds(), nil, "")
	providerBreakers.RecordSuccess(provider)

	// Record user-side usage (audio is billed per request, no token counts).
	var audioCost float64
	if s.handlerDeps.Billing != nil {
		audioCost = s.handlerDeps.Billing.CalculateImageCost("whisper-1", "", "", 1)
	}
	c.Set("upstream_status_code", resp.StatusCode)
	s.recordUsageFromContextByRequest(c, keyHash, upstreamKey.ID, "whisper-1", provider, audioCost, time.Since(startTime).Milliseconds(), "")

	// Audio lacks precise token usage; use an empirical token estimate for
	// upstream key-pool quota tracking.
	if s.keyPoolManager != nil {
		s.keyPoolManager.RecordUpstreamTokens(provider, upstreamKey.ID, "whisper-1", 256, 0)
	}
	if upstreamKey.QuotaLimit > 0 {
		upstreamKey.AddQuotaUsed(256)
	}

	// Capture retention metadata for audio translation (binary body intentionally omitted)
	setRetentionContext(c, "openai", "openai", "whisper-1", false)
	s.captureRetention(c, nil, body, provider, "whisper-1", upstreamKey.ID, nil, time.Since(startTime).Milliseconds(), resp.StatusCode, false, "audio_translation")

	c.Data(resp.StatusCode, "application/json", body)
}

// audioSpeechRequest mirrors the OpenAI text-to-speech payload.
type audioSpeechRequest struct {
	Model          string  `json:"model"`
	Input          string  `json:"input"`
	Voice          string  `json:"voice,omitempty"`
	ResponseFormat string  `json:"response_format,omitempty"`
	Speed          float64 `json:"speed,omitempty"`
}

// handleAudioSpeech proxies OpenAI-compatible /v1/audio/speech requests.
// Audio output is returned verbatim; billing is per-request because the proxy
// cannot determine the exact duration without parsing the binary response.
func (s *HandlerService) handleAudioSpeech(c *gin.Context) {
	startTime := time.Now()

	const maxBodySize = 10 * 1024 * 1024
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

	var req audioSpeechRequest
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

	keyID := c.GetString(string(keyIDKey))
	userID := c.GetString(string(userIDKey))
	keyHash := c.GetString("key_hash")

	if !checkAllowedModels(c, req.Model) {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{
			"message": fmt.Sprintf("Model '%s' is not in your allowed models list.", req.Model),
			"type":    "model_not_allowed",
			"code":    "MODEL_NOT_ALLOWED",
		}})
		return
	}

	logicalModel := req.Model

	// Best-effort duration estimate for media-mode billing: ~15 chars per second.
	estimatedDuration := len(req.Input) / 15
	if estimatedDuration < 1 {
		estimatedDuration = 1
	}
	c.Set("duration", estimatedDuration)

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

	c.Set("provider", provider)
	c.Set("model", logicalModel)
	c.Set("is_stream", false)
	_ = upstreamModelName

	upstreamBaseURL := s.resolveBaseURL(upstreamKey, provider)

	httpReq, err := http.NewRequest("POST", upstreamBaseURL+"/audio/speech", c.Request.Body)
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

	resp, err := sharedHandlerClient.Do(httpReq)
	if err != nil {
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, req.Model, nil, time.Since(startTime).Milliseconds(), true)
		s.recordProviderHealthAsync(c, provider, logicalModel, 0, time.Since(startTime).Milliseconds(), err, err.Error())
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": "Upstream request failed",
			"type":    "upstream_error",
		}})
		return
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.handlerDeps.Logger.Error("Failed to read upstream response body", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": "Failed to read upstream response",
			"type":    "upstream_error",
		}})
		return
	}

	var audioCost float64
	if s.handlerDeps.Billing != nil {
		audioCost = s.handlerDeps.Billing.CalculateImageCost(logicalModel, "", "", 1)
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
			Cost:          audioCost,
			LatencyMs:     time.Since(startTime).Milliseconds(),
			StatusCode:    resp.StatusCode,
			RequestType:   "audio_speech",
		})
	}

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
		c.Data(upErr.ClientStatus, "application/json", upErr.JSON())
		return
	}

	if s.keyPoolManager != nil {
		s.keyPoolManager.RecordUpstreamRequest(provider, upstreamKey.ID, req.Model)
	}
	s.recordUpstreamUsageAsync(upstreamKey.ID, provider, req.Model, nil, time.Since(startTime).Milliseconds(), false)
	s.recordProviderHealthAsync(c, provider, logicalModel, resp.StatusCode, time.Since(startTime).Milliseconds(), nil, "")
	providerBreakers.RecordSuccess(provider)

	c.Set("upstream_status_code", resp.StatusCode)
	s.recordUsageFromContextByRequest(c, keyHash, upstreamKey.ID, logicalModel, provider, audioCost, time.Since(startTime).Milliseconds(), "")

	setRetentionContext(c, "openai", "openai", logicalModel, false)
	s.captureRetention(c, bodyBytes, body, provider, logicalModel, upstreamKey.ID, nil, time.Since(startTime).Milliseconds(), resp.StatusCode, false, "audio_speech")

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "audio/mpeg"
	}
	c.Data(resp.StatusCode, contentType, body)
}

// audioGenerationRequest mirrors a generic audio generation payload.
type audioGenerationRequest struct {
	Model    string          `json:"model"`
	Prompt   string          `json:"prompt"`
	Duration int             `json:"duration,omitempty"`
	Voice    string          `json:"voice,omitempty"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

// handleAudioGenerations creates an async audio generation task upstream.
func (s *HandlerService) handleAudioGenerations(c *gin.Context) {
	startTime := time.Now()

	const maxBodySize = 10 * 1024 * 1024
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

	var req audioGenerationRequest
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

	keyID := c.GetString(string(keyIDKey))
	userID := c.GetString(string(userIDKey))
	keyHash := c.GetString("key_hash")

	if !checkAllowedModels(c, req.Model) {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{
			"message": fmt.Sprintf("Model '%s' is not in your allowed models list.", req.Model),
			"type":    "model_not_allowed",
			"code":    "MODEL_NOT_ALLOWED",
		}})
		return
	}

	logicalModel := req.Model
	c.Set("duration", req.Duration)

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

	c.Set("provider", provider)
	c.Set("model", logicalModel)
	c.Set("is_stream", false)
	_ = upstreamModelName

	upstreamBaseURL := s.resolveBaseURL(upstreamKey, provider)

	httpReq, err := http.NewRequest("POST", upstreamBaseURL+"/audio/generations", c.Request.Body)
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

	resp, err := sharedHandlerClient.Do(httpReq)
	if err != nil {
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, req.Model, nil, time.Since(startTime).Milliseconds(), true)
		s.recordProviderHealthAsync(c, provider, logicalModel, 0, time.Since(startTime).Milliseconds(), err, err.Error())
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": "Upstream request failed",
			"type":    "upstream_error",
		}})
		return
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.handlerDeps.Logger.Error("Failed to read upstream response body", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{
			"message": "Failed to read upstream response",
			"type":    "upstream_error",
		}})
		return
	}

	var audioCost float64
	if s.handlerDeps.Billing != nil {
		audioCost = s.handlerDeps.Billing.CalculateImageCost(logicalModel, "", "", 1)
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
			Cost:          audioCost,
			LatencyMs:     time.Since(startTime).Milliseconds(),
			StatusCode:    resp.StatusCode,
			RequestType:   "audio_generation",
		})
	}

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
		c.Data(upErr.ClientStatus, "application/json", upErr.JSON())
		return
	}

	if s.keyPoolManager != nil {
		s.keyPoolManager.RecordUpstreamRequest(provider, upstreamKey.ID, req.Model)
	}
	s.recordUpstreamUsageAsync(upstreamKey.ID, provider, req.Model, nil, time.Since(startTime).Milliseconds(), false)
	s.recordProviderHealthAsync(c, provider, logicalModel, resp.StatusCode, time.Since(startTime).Milliseconds(), nil, "")
	providerBreakers.RecordSuccess(provider)

	// Detect async task creation before billing. Async tasks are billed when
	// the poll endpoint sees a terminal success state (see
	// handleGetAudioGeneration), so a task that fails upstream is never
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
		s.storeMediaTaskBilling(c, taskID, upstreamKey.ID, logicalModel, provider, audioCost, chargeOnDispatch)
		if chargeOnDispatch {
			// Upstream bills at task creation regardless of outcome: bill
			// now, refund on terminal failure at the poll endpoint.
			s.recordUsageFromContextByRequest(c, keyHash, upstreamKey.ID, logicalModel, provider, audioCost, time.Since(startTime).Milliseconds(), "")
		}
	} else {
		s.recordUsageFromContextByRequest(c, keyHash, upstreamKey.ID, logicalModel, provider, audioCost, time.Since(startTime).Milliseconds(), "")
	}

	setRetentionContext(c, "openai", "openai", logicalModel, false)
	s.captureRetention(c, bodyBytes, body, provider, logicalModel, upstreamKey.ID, nil, time.Since(startTime).Milliseconds(), resp.StatusCode, false, "audio_generation")

	if isAsyncTask {
		c.Data(resp.StatusCode, "application/json", wrapAsyncGenerationResponse(taskID, "audio.generation", time.Now().Unix()))
		return
	}

	c.Data(resp.StatusCode, "application/json", body)
}

// handleGetAudioGeneration polls the upstream status of an async audio task.
// It is read-only and does not bill again.
func (s *HandlerService) handleGetAudioGeneration(c *gin.Context) {
	startTime := time.Now()

	taskID := c.Param("task_id")
	if taskID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message": "Missing task id",
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

	httpReq, err := http.NewRequest("GET", upstreamBaseURL+"/audio/generations/"+taskID, nil)
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

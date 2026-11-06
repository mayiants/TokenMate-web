package server

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ai-proxy/ai-proxy-go/pkg/keypool"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// musicRequest is the client-facing text-to-music request. It mirrors the
// OpenAI audio/speech shape (model + a text field) so SDKs feel familiar.
// Both "prompt" and "input" are accepted for the text.
type musicRequest struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt,omitempty"`
	Input          string `json:"input,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
}

func (r musicRequest) text() string {
	if r.Prompt != "" {
		return r.Prompt
	}
	return r.Input
}

// handleAudioMusic handles POST /v1/audio/music — text-to-music generation.
// Upstreams (e.g. OpenRouter lyria-3) generate music via /chat/completions and
// return the audio as a message part; we translate to a stable response shape
// {created, data:[{b64_json, format}]}. Media-billed per request, like speech.
func (s *HandlerService) handleAudioMusic(c *gin.Context) {
	startTime := time.Now()

	const maxBodySize = 2 * 1024 * 1024
	bodyBytes, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBodySize))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Failed to read request body", "type": "invalid_request_error"}})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	c.Set("request_body", bodyBytes)

	var req musicRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request: " + err.Error(), "type": "invalid_request_error"}})
		return
	}
	if req.Model == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Missing required field: model", "type": "invalid_request_error"}})
		return
	}
	if req.text() == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Missing required field: prompt (or input)", "type": "invalid_request_error"}})
		return
	}

	keyID := c.GetString(string(keyIDKey))
	userID := c.GetString(string(userIDKey))
	keyHash := c.GetString("key_hash")

	if !checkAllowedModels(c, req.Model) {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{
			"message": fmt.Sprintf("Model '%s' is not in your allowed models list.", req.Model),
			"type":    "model_not_allowed", "code": "MODEL_NOT_ALLOWED",
		}})
		return
	}

	logicalModel := req.Model
	provider, upstreamKey, upstreamModelName, isFallback, resolveErr := s.resolveProviderAndKey(c.Request.Context(), req.Model, "normal", "")
	if resolveErr != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": resolveErr.Error(), "type": "service_error"}})
		return
	}
	c.Set("is_fallback", isFallback)
	if isFallback {
		c.Set("actual_group_id", c.GetString("group_id"))
	}
	c.Set("provider", provider)
	c.Set("model", logicalModel)
	c.Set("is_stream", false)
	c.Set("image_count", 1) // media billing: one generation == one unit

	if upstreamKey != nil {
		s.handlerDeps.Logger.Info("Selected upstream key (music)",
			zap.String("provider", provider), zap.String("key_id", upstreamKey.ID),
			zap.String("model", req.Model), zap.String("upstream_model", upstreamModelName),
			zap.Bool("is_fallback", isFallback))
	}

	upstreamBaseURL := s.resolveBaseURL(upstreamKey, provider)

	// Pre-consume one media unit before dispatch (zero-balance guard).
	var estimatedMicro int64
	if s.handlerDeps.Billing != nil {
		var margin float64
		if s.cfg != nil {
			margin = s.cfg.Billing.PreConsumeMargin
		}
		estimatedMicro = s.handlerDeps.Billing.EstimateMediaCostMicroUSD(logicalModel, provider, 1, margin)
	}
	pcs, rejected := s.preConsumeMedia(c, userID, c.GetString("key_type"), logicalModel, provider, estimatedMicro)
	if rejected {
		return
	}

	resp, audioB64, format, rawBody, dispatchErr := s.dispatchMusic(upstreamBaseURL, provider, upstreamModelName, req.text(), upstreamKey, c)
	if dispatchErr != nil {
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, upstreamModelName, nil, time.Since(startTime).Milliseconds(), true)
		s.recordProviderHealthAsync(c, provider, logicalModel, 0, time.Since(startTime).Milliseconds(), dispatchErr, dispatchErr.Error())
		s.settleMediaPreConsume(c, pcs, logicalModel, provider, 0, classifyTransportError(dispatchErr), time.Since(startTime).Milliseconds())
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": "Upstream request failed", "type": "upstream_error"}})
		return
	}
	c.Set("upstream_status_code", resp.StatusCode)

	// Non-2xx or no audio produced: surface the upstream error body and settle as failure.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || audioB64 == "" {
		s.recordUpstreamUsageAsync(upstreamKey.ID, provider, upstreamModelName, nil, time.Since(startTime).Milliseconds(), true)
		s.recordProviderHealthAsync(c, provider, logicalModel, resp.StatusCode, time.Since(startTime).Milliseconds(), nil, "")
		s.settleMediaPreConsume(c, pcs, logicalModel, provider, resp.StatusCode, "upstream_error", time.Since(startTime).Milliseconds())
		if len(rawBody) > 0 {
			c.Data(resp.StatusCode, "application/json", rawBody)
		} else {
			c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": "Upstream returned no audio", "type": "upstream_error"}})
		}
		return
	}

	// Success: bill one media unit, settle pre-consume, return a stable shape.
	var musicCost float64
	if s.handlerDeps.Billing != nil {
		musicCost = s.handlerDeps.Billing.CalculateImageCost(logicalModel, "", "", 1)
	}
	s.recordUsageFromContextByRequest(c, keyHash, upstreamKey.ID, logicalModel, provider, musicCost, time.Since(startTime).Milliseconds(), "")
	s.settleMediaPreConsume(c, pcs, logicalModel, provider, resp.StatusCode, "", time.Since(startTime).Milliseconds())
	s.recordProviderHealthAsync(c, provider, logicalModel, resp.StatusCode, time.Since(startTime).Milliseconds(), nil, "")

	if format == "" {
		format = "mp3"
	}
	out := map[string]interface{}{
		"created": time.Now().Unix(),
		"model":   logicalModel,
		"data":    []map[string]string{{"b64_json": audioB64, "format": format}},
	}
	outBytes, _ := json.Marshal(out)
	c.Data(http.StatusOK, "application/json", outBytes)
	_ = keyID
}

// dispatchMusic sends a text-to-music request upstream and returns the HTTP
// response plus the extracted base64 audio and format. For OpenRouter it uses
// the chat endpoint with an audio modality; OpenRouter streams audio output
// (it rejects non-streaming audio with 400), so we request stream:true and
// accumulate the base64 audio from the SSE deltas. Other providers fall back to
// an OpenAI audio/speech-style passthrough.
func (s *HandlerService) dispatchMusic(baseURL, provider, upstreamModel, prompt string, upstreamKey *keypool.KeyInternal, c *gin.Context) (resp *http.Response, audioB64 string, format string, rawBody []byte, err error) {
	if isOpenRouterBaseURL(baseURL) {
		chat := map[string]interface{}{
			"model":      upstreamModel,
			"modalities": []string{"audio", "text"},
			"stream":     true,
			"messages":   []map[string]interface{}{{"role": "user", "content": prompt}},
		}
		payload, _ := json.Marshal(chat)
		httpReq, cerr := http.NewRequest("POST", baseURL+"/chat/completions", bytes.NewReader(payload))
		if cerr != nil {
			return nil, "", "", nil, cerr
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "text/event-stream")
		httpReq.Header.Set("Authorization", "Bearer "+upstreamKey.ActualKey)
		s.applyForwardedHeaders(httpReq, c)

		r, derr := sharedMediaClient.Do(httpReq)
		if derr != nil {
			if r != nil {
				_ = r.Body.Close()
			}
			return nil, "", "", nil, derr
		}
		defer func() { _ = r.Body.Close() }()
		if r.StatusCode < 200 || r.StatusCode >= 300 {
			b, _ := io.ReadAll(io.LimitReader(r.Body, maxMediaResponseBody+1))
			return r, "", "", b, nil
		}
		b64, fmtStr := accumulateOpenRouterAudioSSE(r.Body)
		return r, b64, fmtStr, nil, nil
	}

	// Default: OpenAI audio/speech-style passthrough (raw audio bytes).
	sp := map[string]interface{}{"model": upstreamModel, "input": prompt}
	payload, _ := json.Marshal(sp)
	httpReq, cerr := http.NewRequest("POST", baseURL+"/audio/speech", bytes.NewReader(payload))
	if cerr != nil {
		return nil, "", "", nil, cerr
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+upstreamKey.ActualKey)
	s.applyForwardedHeaders(httpReq, c)
	r, derr := sharedMediaClient.Do(httpReq)
	if derr != nil {
		if r != nil {
			_ = r.Body.Close()
		}
		return nil, "", "", nil, derr
	}
	defer func() { _ = r.Body.Close() }()
	b, rerr := io.ReadAll(io.LimitReader(r.Body, maxMediaResponseBody+1))
	if rerr != nil {
		return nil, "", "", nil, rerr
	}
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return r, "", "", b, nil
	}
	// Passthrough providers return raw audio bytes → base64-encode for the
	// uniform {b64_json} response shape.
	return r, base64.StdEncoding.EncodeToString(b), "mp3", nil, nil
}

// accumulateOpenRouterAudioSSE reads an OpenRouter SSE stream and concatenates
// the base64 audio carried in choices[].delta.audio.data across chunks.
func accumulateOpenRouterAudioSSE(body io.Reader) (string, string) {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024) // audio deltas can be large
	var b64 strings.Builder
	var format string
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Audio struct {
						Data   string `json:"data"`
						Format string `json:"format"`
					} `json:"audio"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		for _, ch := range chunk.Choices {
			if d := ch.Delta.Audio.Data; d != "" {
				if raw, ok := dataURIToBase64(d); ok {
					b64.WriteString(raw)
				} else if !strings.HasPrefix(d, "data:") {
					b64.WriteString(d)
				}
			}
			if ch.Delta.Audio.Format != "" {
				format = ch.Delta.Audio.Format
			}
		}
	}
	return b64.String(), format
}

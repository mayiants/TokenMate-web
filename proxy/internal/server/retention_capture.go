package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/mayiants/ai-proxy-go/dto"
	"github.com/mayiants/ai-proxy-go/internal/retention"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// retentionRecord builds a retention record from the request/response bodies
// already available in the handler. It is intentionally non-blocking: if the
// store is nil or disabled, it returns immediately.
func (s *HandlerService) retentionRecord(
	c *gin.Context,
	requestBody []byte,
	responseBody []byte,
	provider, model, upstreamKeyID string,
	usage *dto.Usage,
	latencyMs int64,
	statusCode int,
	isStream bool,
	requestType string,
) *retention.Record {
	if s.retentionStore == nil {
		return nil
	}

	// Policy comes from the Portal via the cached key validation. Missing
	// values (older Portal, non-authenticated paths) mean legacy behaviour.
	mode := c.GetString("retention_mode")
	if mode == "" {
		mode = "standard"
	}
	if mode == "off" {
		return nil
	}
	if mode == "metadata" {
		// Metadata-only: the record proves the call happened without keeping
		// any customer content.
		requestBody, responseBody = nil, nil
	}
	// Streaming handlers pass no response body (it is never buffered), so
	// "captured" simply reflects whether a body is actually being stored.
	responseCaptured := mode == "standard" && len(responseBody) > 0
	var reqHeaders map[string]string
	if mode == "standard" {
		reqHeaders = s.requestHeadersForRetention(c)
	}

	traceID := c.GetString("trace_id")
	if traceID == "" {
		traceID = uuid.New().String()
	}

	routingTrack := c.GetString("routing_track")
	if routingTrack == "" {
		routingTrack = "standard"
	}

	inputTokens, outputTokens, cachedTokens := 0, 0, 0
	if usage != nil {
		inputTokens = usage.PromptTokens
		outputTokens = usage.CompletionTokens
		cachedTokens = usage.PromptCacheHitTokens
		if cachedTokens == 0 {
			cachedTokens = usage.PromptTokensDetails.CachedTokens
		}
	}

	clientFormat := c.GetString("client_format")
	if clientFormat == "" {
		clientFormat = "openai" // default façade
	}
	upstreamFormat := c.GetString("upstream_format")
	if upstreamFormat == "" {
		upstreamFormat = clientFormat
	}

	return &retention.Record{
		TraceID:    traceID,
		ReceivedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Metadata: retention.Metadata{
			UserID:            c.GetString(string(userIDKey)),
			APIKeyHash:        c.GetString("key_hash"),
			ClientIP:          c.ClientIP(),
			UpstreamKeyID:     upstreamKeyID,
			Provider:          provider,
			Model:             model,
			VendorModelName:   c.GetString("vendor_model_name"),
			ClientFormat:      clientFormat,
			UpstreamFormat:    upstreamFormat,
			Region:            c.GetString("region"),
			StatusCode:        statusCode,
			LatencyMs:         latencyMs,
			FirstTokenLatency: c.GetInt64("first_token_latency_ms"),
			InputTokens:       inputTokens,
			OutputTokens:      outputTokens,
			CachedTokens:      cachedTokens,
			IsStream:          isStream,
			IsConverted:       c.GetBool("is_converted"),
			RoutingTrack:      routingTrack,
			IsFallback:        c.GetBool("is_fallback"),
			RequestType:       requestType,
			EnterpriseID:      c.GetString("enterprise_id"),
			RetentionMode:     mode,
			RetentionDays:     c.GetInt("retention_days"),
			ResponseCaptured:  responseCaptured,
			Truncated:         mode == "metadata" || (isStream && !responseCaptured),
		},
		Request: retention.PayloadPart{
			Method:  c.Request.Method,
			Path:    c.Request.URL.Path,
			Headers: reqHeaders,
			Body:    cloneBytes(requestBody),
		},
		Response: retention.PayloadPart{
			Headers: nil, // not available without the upstream http.Response
			Body:    cloneBytes(responseBody),
		},
	}
}

// captureRetention writes a retention record asynchronously. Call this after the
// response body has been read and usage has been extracted.
func (s *HandlerService) captureRetention(
	c *gin.Context,
	requestBody []byte,
	responseBody []byte,
	provider, model, upstreamKeyID string,
	usage *dto.Usage,
	latencyMs int64,
	statusCode int,
	isStream bool,
	requestType string,
) {
	rec := s.retentionRecord(c, requestBody, responseBody, provider, model, upstreamKeyID, usage, latencyMs, statusCode, isStream, requestType)
	if rec == nil {
		return
	}
	s.retentionStore.Record(rec)
}

// setRetentionContext records format and model metadata needed by retentionRecord.
func setRetentionContext(c *gin.Context, clientFormat, upstreamFormat, vendorModelName string, isConverted bool) {
	c.Set("client_format", clientFormat)
	c.Set("upstream_format", upstreamFormat)
	c.Set("vendor_model_name", vendorModelName)
	c.Set("is_converted", isConverted)
}

func (s *HandlerService) requestHeadersForRetention(c *gin.Context) map[string]string {
	if s.cfg != nil && s.cfg.Retention.CaptureAllHeaders {
		return allHeadersRedacted(c.Request.Header)
	}
	return selectedHeaders(c.Request.Header)
}

// cloneBytes returns a fresh copy of b, or nil if b is nil.
func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	return append([]byte(nil), b...)
}

func selectedHeaders(h http.Header) map[string]string {
	out := make(map[string]string)
	for _, key := range []string{"Content-Type", "User-Agent", "Accept", "X-Request-ID"} {
		if v := h.Get(key); v != "" {
			out[key] = v
		}
	}
	return out
}

// redactedHeaderValues are header names whose values must never be retained,
// even when CaptureAllHeaders is enabled.
var redactedHeaderValues = []string{
	"authorization",
	"x-api-key",
	"x-admin-secret",
	"cookie",
	"set-cookie",
	"proxy-authorization",
}

func allHeadersRedacted(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for key, values := range h {
		if shouldRedactHeader(key) {
			out[key] = "***"
			continue
		}
		out[key] = strings.Join(values, ", ")
	}
	return out
}

func shouldRedactHeader(key string) bool {
	lower := strings.ToLower(key)
	for _, h := range redactedHeaderValues {
		if lower == h {
			return true
		}
	}
	return false
}

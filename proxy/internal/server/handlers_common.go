package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ai-proxy/ai-proxy-go/dto"
	"github.com/ai-proxy/ai-proxy-go/internal/audit"
	"github.com/ai-proxy/ai-proxy-go/pkg/billing"
	"github.com/ai-proxy/ai-proxy-go/pkg/cache"
	"github.com/ai-proxy/ai-proxy-go/pkg/circuitbreaker"
	"github.com/ai-proxy/ai-proxy-go/pkg/geoip"
	"github.com/ai-proxy/ai-proxy-go/pkg/keypool"
	"github.com/ai-proxy/ai-proxy-go/pkg/models"
	"github.com/ai-proxy/ai-proxy-go/pkg/relay"
	"github.com/ai-proxy/ai-proxy-go/pkg/relay/channel/aws"
	"github.com/ai-proxy/ai-proxy-go/pkg/token"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// sharedHandlerClient is the shared HTTP client for non-streaming upstream
// requests. It keeps a total Timeout so that hung upstream connections do
// not hold a request goroutine forever.
var sharedHandlerClient = &http.Client{
	Timeout: 120 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
	},
}

// sharedMediaClient is used for image/video/audio generation upstream requests.
// These endpoints can take significantly longer than chat completions
// (TokenRouter image/video generation often exceeds 120s).
var sharedMediaClient = &http.Client{
	Timeout: 300 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
	},
}

// sharedStreamingClient is the HTTP client for SSE/streaming upstream requests.
// It has no total Timeout: http.Client.Timeout covers the entire response body
// read period, which would hard-cut long SSE streams at 120s. Each streaming
// caller wraps its request context in a context.WithTimeout (typically 10m) to
// bound the overall stream duration.
var sharedStreamingClient = &http.Client{
	Timeout: 0,
	Transport: &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
	},
}

type contextKey string

const (
	keyIDKey     contextKey = "api_key_id"
	userIDKey    contextKey = "user_id"
	providerKey  contextKey = "provider"
	channelIDKey contextKey = "channel_id"
)

type HandlerDependencies struct {
	Logger        *zap.Logger
	AuditLogger   *audit.Logger
	AffinityCache *cache.AffinityCache
	TokenCounter  *token.Counter
	Billing       *billing.Billing
	ModelRegistry *models.ModelRegistry
	Simulate      SimulateConfig
}

type SimulateConfig struct {
	Enabled   bool
	LatencyMs int
}

var providerBreakers = circuitbreaker.NewProviderBreakers(circuitbreaker.Config{
	Threshold:    5,
	ResetTimeout: 30 * time.Second,
	HalfOpenMax:  1,
})

// WireBreakerObservability installs the breaker transition logger. Called
// once during server construction with the configured logger (the global
// zap logger is intentionally not used — it is never replaced in main).
func WireBreakerObservability(logger *zap.Logger) {
	providerBreakers.SetTransitionHook(func(provider string, from, to circuitbreaker.State) {
		switch to {
		case circuitbreaker.Open:
			probeFailures := providerBreakers.Get(provider).ProbeFailures()
			msg := "provider circuit breaker OPEN — provider excluded from routing until a half-open probe succeeds"
			fields := []zap.Field{
				zap.String("provider", provider),
				zap.String("from_state", from.String()),
				zap.String("to_state", to.String()),
				zap.Int("consecutive_probe_failures", probeFailures),
			}
			if probeFailures >= 3 {
				// Repeated probe failures with no billing trail are the signature
				// of a transport-level wedge (dead keep-alive pool / silent LB
				// blackhole): the provider may be perfectly healthy while every
				// probe dies before the upstream can respond. Surface loudly.
				logger.Warn(msg+"; probes keep failing — possible transport wedge, investigate or reset the breaker (POST /admin/circuit-breakers/:provider/reset)", fields...)
			} else {
				logger.Warn(msg, fields...)
			}
		case circuitbreaker.Closed:
			logger.Info("provider circuit breaker closed — provider recovered",
				zap.String("provider", provider),
				zap.String("from_state", from.String()))
		}
	})
}

// bedrockProviderList is the ordered list of Bedrock provider names for multi-region failover.
// Can be overridden at runtime (e.g. from config or Portal sync). Defaults are used when nil/empty.
var (
	bedrockProviderList   []string
	bedrockProviderListMu sync.RWMutex

	mantleProviderList   []string
	mantleProviderListMu sync.RWMutex
)

// SetBedrockProviders sets the ordered Bedrock provider list. Thread-safe.
func SetBedrockProviders(providers []string) {
	bedrockProviderListMu.Lock()
	defer bedrockProviderListMu.Unlock()
	bedrockProviderList = providers
}

// getBedrockProviders returns the current Bedrock provider list, or defaults if not set.
func getBedrockProviders() []string {
	bedrockProviderListMu.RLock()
	defer bedrockProviderListMu.RUnlock()
	if len(bedrockProviderList) > 0 {
		return bedrockProviderList
	}
	return []string{"amazon", "amazon-us-west-2", "amazon-eu-west-1"}
}

// SetMantleProviders sets the ordered Mantle provider list. Thread-safe.
func SetMantleProviders(providers []string) {
	mantleProviderListMu.Lock()
	defer mantleProviderListMu.Unlock()
	mantleProviderList = providers
}

// getMantleProviders returns the current Mantle provider list, or defaults if not set.
func getMantleProviders() []string {
	mantleProviderListMu.RLock()
	defer mantleProviderListMu.RUnlock()
	if len(mantleProviderList) > 0 {
		return mantleProviderList
	}
	// Default: mantle regions that support Chat Completions / Responses API
	return relay.GetMantleProviderNames()
}

// UsageRecordContext - Context for recording usage
type UsageRecordContext struct {
	KeyHash              string
	UpstreamKeyID        string // UUID of the upstream key actually used
	Model                string
	Provider             string
	Region               string
	TraceID              string
	ServerID             string
	PricingRatio         float64 // Group pricing ratio for cost calculation
	GroupID              string  // Group ID for usage attribution
	ActualGroupID        string  // Actual group that served the request (same as GroupID unless fallback occurred)
	IsFallback           bool    // Whether this request was served via cross-provider fallback
	RoutingTrack         string  // "bedrock" or "standard"
	OriginalTrack        string  // Original track before fallback (e.g. "bedrock" when fallback to standard)
	StatusCode           int     // HTTP status code from upstream
	Status               string  // Optional status override (e.g. "refund"); empty = derive from errMsg/status code
	Cost                 float64 // Pre-calculated cost
	IsStream             bool    // Whether this was a streaming request
	CacheCreation        int     // Cache write tokens
	ReasoningTokens      int     // Reasoning tokens
	CacheHitTokens       int     // Cache read tokens
	EffectiveInputTokens int     // For direct pass-through when available
	RetryCount           int     // Number of retries
	Endpoint             string  // Request path (e.g. /v1/chat/completions)
	ClientIP             string  // Client IP address
	ClientGeo            string  // Client geolocation (ISO country code)
	UserAgent            string  // Raw client User-Agent (for client-app identification)
	AgentTools           string  // Tool names offered in an agent request (deduped, comma-separated)
	CalledTools          string  // Tool names actually called in the response (name:count pairs)
	TTFTMs               int     // Time to first token (ms), streaming only
	ReasoningIntensity   string  // Request reasoning effort (e.g. low/medium/high)

	// Media generation metadata forwarded to Portal for media-mode billing.
	ImageSize    string
	ImageQuality string
	ImageCount   int
	Duration     int // video/audio seconds

	// Token counts forwarded from upstream when available (image/video models
	// often report output tokens even though they are billed per request/image).
	InputTokens  int
	OutputTokens int
}

// maxAgentToolNames caps the tool list stored on a usage record. Agent
// clients can send large tool schemas; the log only needs the names.
const maxAgentToolNames = 20

// joinAgentToolNames dedupes tool names (order-preserving), caps the list and
// joins with commas for the usage record. Returns "" when no usable name.
func joinAgentToolNames(names []string) string {
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))
	for _, n := range names {
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
		if len(out) >= maxAgentToolNames {
			break
		}
	}
	return strings.Join(out, ",")
}

// --- Response-side called-tool tracking --------------------------------
// noteCalledTool increments the per-request counter of tools the upstream
// response actually invoked. Called from every response-parse path.
func noteCalledTool(c *gin.Context, name string) {
	if name == "" {
		return
	}
	if v, ok := c.Get("called_tools_counter"); ok {
		if m, ok := v.(map[string]int); ok {
			m[name]++
			return
		}
	}
	c.Set("called_tools_counter", map[string]int{name: 1})
}

// maxCalledToolEntries caps distinct tool names stored on a usage record.
const maxCalledToolEntries = 20

// calledToolsFromContext serializes the counter as "name:count" pairs sorted
// by count desc then name asc (deterministic). Returns "" when none.
func calledToolsFromContext(c *gin.Context) string {
	v, ok := c.Get("called_tools_counter")
	if !ok {
		return ""
	}
	m, ok := v.(map[string]int)
	if !ok || len(m) == 0 {
		return ""
	}
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if m[names[i]] != m[names[j]] {
			return m[names[i]] > m[names[j]]
		}
		return names[i] < names[j]
	})
	if len(names) > maxCalledToolEntries {
		names = names[:maxCalledToolEntries]
	}
	parts := make([]string, 0, len(names))
	for _, n := range names {
		parts = append(parts, fmt.Sprintf("%s:%d", n, m[n]))
	}
	return strings.Join(parts, ",")
}

// trackOpenAIMessageToolCalls notes tool calls from a non-stream OpenAI-format
// response message.
func trackOpenAIMessageToolCalls(c *gin.Context, msg dto.Message) {
	for _, tc := range msg.ToolCalls {
		noteCalledTool(c, tc.Function.Name)
	}
}

// trackOpenAIStreamToolCalls accumulates fragmented tool-call names from a
// streamed OpenAI-format chunk. frags maps the stream index to the assembled
// name; pass the same map for every chunk of one request.
func trackOpenAIStreamToolCalls(frags map[int]*strings.Builder, chunk *dto.ChatCompletionsStreamResponse) {
	if chunk == nil {
		return
	}
	for _, ch := range chunk.Choices {
		for _, tc := range ch.Delta.ToolCalls {
			if tc.Function.Name == "" {
				continue
			}
			b, ok := frags[tc.Index]
			if !ok {
				b = &strings.Builder{}
				frags[tc.Index] = b
			}
			b.WriteString(tc.Function.Name)
		}
	}
}

// flushStreamToolCalls notes the completed tool names. Safe to call multiple
// times (mid-stream abort + final): entries are cleared as they are noted.
func flushStreamToolCalls(c *gin.Context, frags map[int]*strings.Builder) {
	for i, b := range frags {
		noteCalledTool(c, b.String())
		delete(frags, i)
	}
}

// Does NOT enforce TPM limits - RPM is sufficient for rate limiting.
// Token stats are used for billing, not real-time blocking.
func (s *HandlerService) recordTokenUsageAsync(keyHash string, usage *dto.Usage) {
	s.recordUsageAsync(UsageRecordContext{KeyHash: keyHash}, usage, 0, "")
}

// recordUsageAsyncFromContext - Record usage with group pricing from gin context
func (s *HandlerService) recordUsageAsyncFromContext(c *gin.Context, keyHash, upstreamKeyID, model, provider string, usage *dto.Usage, latencyMs int64, errMsg string) {
	pricingRatio, _ := c.Get("pricing_ratio")
	groupID := c.GetString("group_id")
	ratio := 1.0
	if r, ok := pricingRatio.(float64); ok && r > 0 {
		ratio = r
	}
	routingTrack := c.GetString("routing_track")
	if routingTrack == "" {
		routingTrack = "standard"
	}
	traceID := c.GetString("trace_id")
	if traceID == "" {
		traceID = uuid.New().String()
	}
	statusCode, _ := c.Get("upstream_status_code")
	sc := 0
	if s, ok := statusCode.(int); ok {
		sc = s
	}
	cacheCreation := 0
	reasoningTokens := 0
	cacheHitTokens := 0
	if usage != nil {
		cacheCreation = usage.PromptTokensDetails.CachedCreationTokens
		reasoningTokens = usage.CompletionTokenDetails.ReasoningTokens
		cacheHitTokens = usage.PromptTokensDetails.CachedTokens
		if cacheHitTokens == 0 {
			cacheHitTokens = usage.PromptCacheHitTokens
		}
	}
	clientIP := c.ClientIP()
	clientGeo := ""
	if clientIP != "" {
		clientGeo = geoip.Lookup(clientIP)
	}
	var reasoningIntensity string
	if ri, ok := c.Get("reasoning_intensity"); ok {
		if s, ok := ri.(string); ok {
			reasoningIntensity = s
		}
	}
	var ttftMs int
	if v, ok := c.Get("first_token_latency_ms"); ok {
		if n, ok := v.(int64); ok {
			ttftMs = int(n)
		}
	}
	s.recordUsageAsync(UsageRecordContext{
		TraceID:            traceID,
		KeyHash:            keyHash,
		UpstreamKeyID:      upstreamKeyID,
		Model:              model,
		Provider:           provider,
		Region:             c.GetString("region"),
		PricingRatio:       ratio,
		GroupID:            groupID,
		ActualGroupID:      c.GetString("actual_group_id"),
		IsFallback:         c.GetBool("is_fallback"),
		RoutingTrack:       routingTrack,
		OriginalTrack:      c.GetString("original_track"),
		StatusCode:         sc,
		Cost:               0, // deprecated: portal recomputes cost from token counts
		IsStream:           c.GetBool("is_stream"),
		CacheCreation:      cacheCreation,
		ReasoningTokens:    reasoningTokens,
		CacheHitTokens:     cacheHitTokens,
		Endpoint:           c.Request.URL.Path,
		ClientIP:           clientIP,
		ClientGeo:          clientGeo,
		UserAgent:          c.Request.UserAgent(),
		AgentTools:         c.GetString("agent_tools"),
		CalledTools:        calledToolsFromContext(c),
		TTFTMs:             ttftMs,
		ReasoningIntensity: reasoningIntensity,
	}, usage, int(latencyMs), errMsg)
}

// recordUsageFromContextByRequest records per-request billed usage (audio/image
// endpoints that carry no token counts). Unlike recordUsageAsyncFromContext it
// does not short-circuit on zero tokens, so the per-request cost still lands in
// the usage buffer and audit log.
func (s *HandlerService) recordUsageFromContextByRequest(c *gin.Context, keyHash, upstreamKeyID, model, provider string, cost float64, latencyMs int64, errMsg string) {
	pricingRatio, _ := c.Get("pricing_ratio")
	groupID := c.GetString("group_id")
	ratio := 1.0
	if r, ok := pricingRatio.(float64); ok && r > 0 {
		ratio = r
	}
	routingTrack := c.GetString("routing_track")
	if routingTrack == "" {
		routingTrack = "standard"
	}
	traceID := c.GetString("trace_id")
	if traceID == "" {
		traceID = uuid.New().String()
	}
	statusCode, _ := c.Get("upstream_status_code")
	sc := 0
	if s, ok := statusCode.(int); ok {
		sc = s
	}
	clientIP := c.ClientIP()
	clientGeo := ""
	if clientIP != "" {
		clientGeo = geoip.Lookup(clientIP)
	}

	imageCount := 0
	if v, ok := c.Get("image_count"); ok {
		if n, ok := v.(int); ok {
			imageCount = n
		}
	}
	duration := 0
	if v, ok := c.Get("duration"); ok {
		if n, ok := v.(int); ok {
			duration = n
		}
	}
	inputTokens := 0
	if v, ok := c.Get("input_tokens"); ok {
		if n, ok := v.(int); ok {
			inputTokens = n
		}
	}
	outputTokens := 0
	if v, ok := c.Get("output_tokens"); ok {
		if n, ok := v.(int); ok {
			outputTokens = n
		}
	}

	ctx := UsageRecordContext{
		TraceID:       traceID,
		KeyHash:       keyHash,
		UpstreamKeyID: upstreamKeyID,
		Model:         model,
		Provider:      provider,
		Region:        c.GetString("region"),
		PricingRatio:  ratio,
		GroupID:       groupID,
		ActualGroupID: c.GetString("actual_group_id"),
		IsFallback:    c.GetBool("is_fallback"),
		RoutingTrack:  routingTrack,
		OriginalTrack: c.GetString("original_track"),
		StatusCode:    sc,
		Cost:          cost,
		IsStream:      c.GetBool("is_stream"),
		Endpoint:      c.Request.URL.Path,
		ClientIP:      clientIP,
		ClientGeo:     clientGeo,
		UserAgent:     c.Request.UserAgent(),
		AgentTools:    c.GetString("agent_tools"),
		CalledTools:   calledToolsFromContext(c),
		ImageSize:     c.GetString("image_size"),
		ImageQuality:  c.GetString("image_quality"),
		ImageCount:    imageCount,
		Duration:      duration,
		InputTokens:   inputTokens,
		OutputTokens:  outputTokens,
	}

	s.recordMediaUsage(ctx, latencyMs, errMsg)
}

// recordMediaUsage flushes a per-request usage record (usage buffer + audit
// log). It is shared by the media dispatch handlers and the async task poll
// billing path.
func (s *HandlerService) recordMediaUsage(ctx UsageRecordContext, latencyMs int64, errMsg string) {
	status := "success"
	if errMsg != "" || ctx.StatusCode >= 400 {
		status = "error"
	}
	if ctx.Status != "" {
		status = ctx.Status
	}

	if s.usageBuffer != nil {
		s.usageBuffer.Add(&billing.UsageRecord{
			TraceID:       ctx.TraceID,
			APIKeyHash:    ctx.KeyHash,
			Model:         ctx.Model,
			Provider:      ctx.Provider,
			Region:        ctx.Region,
			InputTokens:   ctx.InputTokens,
			OutputTokens:  ctx.OutputTokens,
			Cost:          ctx.Cost,
			LatencyMs:     int(latencyMs),
			RequestCount:  1,
			ServerID:      s.serverID,
			UpstreamKeyID: ctx.UpstreamKeyID,
			Timestamp:     time.Now().UTC().Format(time.RFC3339),
			Error:         errMsg,
			GroupID:       ctx.GroupID,
			ActualGroupID: ctx.ActualGroupID,
			IsFallback:    ctx.IsFallback,
			PricingRatio:  ctx.PricingRatio,
			RoutingTrack:  ctx.RoutingTrack,
			OriginalTrack: ctx.OriginalTrack,
			Endpoint:      ctx.Endpoint,
			IsStreaming:   ctx.IsStream,
			ClientIP:      ctx.ClientIP,
			ClientGeo:     ctx.ClientGeo,
			UserAgent:     ctx.UserAgent,
			AgentTools:    ctx.AgentTools,
			CalledTools:   ctx.CalledTools,
			Status:        status,
			ImageSize:     ctx.ImageSize,
			ImageQuality:  ctx.ImageQuality,
			ImageCount:    ctx.ImageCount,
			Duration:      ctx.Duration,
		})
	}

	if s.handlerDeps != nil && s.handlerDeps.AuditLogger != nil {
		go s.handlerDeps.AuditLogger.Log(audit.AuditEntry{
			TraceID:       ctx.TraceID,
			ClientIP:      ctx.ClientIP,
			UserAgent:     ctx.UserAgent,
			Timestamp:     time.Now(),
			APIKeyHash:    ctx.KeyHash,
			UpstreamKeyID: ctx.UpstreamKeyID,
			Model:         ctx.Model,
			Provider:      ctx.Provider,
			Region:        ctx.Region,
			Cost:          ctx.Cost,
			LatencyMs:     int64(latencyMs),
			StatusCode:    ctx.StatusCode,
			IsStream:      ctx.IsStream,
			ActualGroupID: ctx.ActualGroupID,
			GroupID:       ctx.GroupID,
			IsFallback:    ctx.IsFallback,
			PricingRatio:  ctx.PricingRatio,
			RoutingTrack:  ctx.RoutingTrack,
			OriginalTrack: ctx.OriginalTrack,
			Error:         errMsg,
			Endpoint:      ctx.Endpoint,
		})
	}
}

// recordUsageAsync - Full usage recording with context. Sends via audit logger.
func (s *HandlerService) recordUsageAsync(ctx UsageRecordContext, usage *dto.Usage, latencyMs int, errMsg string) {
	if usage == nil {
		return
	}
	totalTokens := int64(usage.PromptTokens + usage.CompletionTokens)
	if totalTokens <= 0 {
		return
	}

	// Defensive defaults
	if ctx.TraceID == "" {
		ctx.TraceID = uuid.New().String()
	}
	if ctx.RoutingTrack == "" {
		ctx.RoutingTrack = "standard"
	}

	// Stats pipeline — synchronous, feeds UsageBuffer for batch reporting to Portal
	if s.usageBuffer != nil {
		effectiveInput := usage.PromptTokens - ctx.CacheHitTokens
		if effectiveInput < 0 {
			effectiveInput = 0
		}
		cacheHit := ctx.CacheHitTokens > 0
		status := "success"
		if errMsg != "" || ctx.StatusCode >= 400 {
			status = "error"
		}
		s.usageBuffer.Add(&billing.UsageRecord{
			TraceID:              ctx.TraceID,
			APIKeyHash:           ctx.KeyHash,
			Model:                ctx.Model,
			Provider:             ctx.Provider,
			Region:               ctx.Region,
			InputTokens:          usage.PromptTokens,
			OutputTokens:         usage.CompletionTokens,
			CachedTokens:         ctx.CacheHitTokens,
			CacheCreationTokens:  ctx.CacheCreation,
			EffectiveInputTokens: effectiveInput,
			ReasoningTokens:      ctx.ReasoningTokens,
			Cost:                 ctx.Cost,
			LatencyMs:            int(latencyMs),
			RequestCount:         1,
			ServerID:             s.serverID,
			UpstreamKeyID:        ctx.UpstreamKeyID,
			Timestamp:            time.Now().UTC().Format(time.RFC3339),
			CacheHit:             cacheHit,
			Error:                errMsg,
			GroupID:              ctx.GroupID,
			ActualGroupID:        ctx.ActualGroupID,
			IsFallback:           ctx.IsFallback,
			PricingRatio:         ctx.PricingRatio,
			RoutingTrack:         ctx.RoutingTrack,
			OriginalTrack:        ctx.OriginalTrack,
			Endpoint:             ctx.Endpoint,
			IsStreaming:          ctx.IsStream,
			ClientIP:             ctx.ClientIP,
			ClientGeo:            ctx.ClientGeo,
			UserAgent:            ctx.UserAgent,
			AgentTools:           ctx.AgentTools,
			CalledTools:          ctx.CalledTools,
			TTFTMs:               ctx.TTFTMs,
			ReasoningIntensity:   ctx.ReasoningIntensity,
			Status:               status,
		})
	}

	// Audit logging — fire-and-forget, lossy, for debugging / historical queries only
	go func() {
		if s.handlerDeps == nil || s.handlerDeps.AuditLogger == nil {
			return
		}

		cacheHit := ctx.CacheHitTokens > 0
		s.handlerDeps.AuditLogger.Log(audit.AuditEntry{
			TraceID:        ctx.TraceID,
			ClientIP:       ctx.ClientIP,
			UserAgent:      ctx.UserAgent,
			Timestamp:      time.Now(),
			APIKeyHash:     ctx.KeyHash,
			UpstreamKeyID:  ctx.UpstreamKeyID,
			Model:          ctx.Model,
			Provider:       ctx.Provider,
			Region:         ctx.Region,
			InputTokens:    usage.PromptTokens,
			OutputTokens:   usage.CompletionTokens,
			CacheHitTokens: ctx.CacheHitTokens,
			CacheCreation:  ctx.CacheCreation,
			Reasoning:      ctx.ReasoningTokens,
			Cost:           ctx.Cost,
			LatencyMs:      int64(latencyMs),
			StatusCode:     ctx.StatusCode,
			IsStream:       ctx.IsStream,
			ActualGroupID:  ctx.ActualGroupID,
			GroupID:        ctx.GroupID,
			IsFallback:     ctx.IsFallback,
			PricingRatio:   ctx.PricingRatio,
			RoutingTrack:   ctx.RoutingTrack,
			OriginalTrack:  ctx.OriginalTrack,
			Error:          errMsg,
			CacheHit:       cacheHit,
			Endpoint:       ctx.Endpoint,
		})
	}()
}

// recordUpstreamUsageAsync records upstream key usage for provider reconciliation.
// Called after each upstream request (success or error) to accumulate daily stats.
func (s *HandlerService) recordUpstreamUsageAsync(upstreamKeyID, provider, model string, usage *dto.Usage, latencyMs int64, isError bool) {
	if s.upstreamUsageBuffer == nil || upstreamKeyID == "" {
		return
	}
	date := time.Now().Format("2006-01-02")
	record := &billing.UpstreamUsageRecord{
		UpstreamKeyID: upstreamKeyID,
		Provider:      provider,
		Model:         model,
		Date:          date,
		RequestCount:  1,
		LatencyMs:     int(latencyMs),
	}
	if isError {
		record.ErrorCount = 1
	} else if usage != nil {
		record.InputTokens = int64(usage.PromptTokens)
		record.OutputTokens = int64(usage.CompletionTokens)
		cacheReadTokens := usage.PromptTokensDetails.CachedTokens
		if cacheReadTokens == 0 {
			// Fallback: see billing.CalculateCost.
			cacheReadTokens = usage.PromptCacheHitTokens
		}
		record.CacheReadTokens = int64(cacheReadTokens)
		record.CacheCreationTokens = int64(usage.PromptTokensDetails.CachedCreationTokens)
	}
	s.upstreamUsageBuffer.Add(record)

	// Reset the key-level failure counter on a successful upstream call so
	// sparse intermittent errors never accumulate to the cooldown threshold.
	if !isError && s.keyPoolManager != nil {
		s.keyPoolManager.ReportKeySuccess(provider, upstreamKeyID, model)
	}
}

// parseRetryAfter extracts a backoff hint (seconds) from an upstream 429/503
// response. Supports both forms of the Retry-After header: delta-seconds
// ("120") and an HTTP-date. Returns 0 when absent or unparseable.
func parseRetryAfter(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	v := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		return secs
	}
	if t, err := http.ParseTime(v); err == nil {
		d := time.Until(t)
		if d <= 0 {
			return 0
		}
		return int(d.Seconds())
	}
	return 0
}

// reportUpstreamKeyError routes an upstream error to the right key-health path.
// A 429 is a rate limit (not a key malfunction): cool only this (key, model),
// honoring the upstream's Retry-After, without touching the key failure counter.
// Everything else goes through ReportKeyFailure's normal classification.
func (s *HandlerService) reportUpstreamKeyError(resp *http.Response, provider, keyID, model, errorMsg string) {
	if s.keyPoolManager == nil {
		return
	}
	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	if status == http.StatusTooManyRequests {
		s.keyPoolManager.ReportKeyRateLimited(provider, keyID, model, parseRetryAfter(resp))
		return
	}
	s.keyPoolManager.ReportKeyFailure(provider, keyID, model, status, "upstream_error", "upstream_error", errorMsg)
}

// recordProviderHealthAsync records a provider health event from the proxy.
// transportErr should be non-nil when the request failed before receiving an HTTP response.
func (s *HandlerService) recordProviderHealthAsync(c *gin.Context, provider, model string, statusCode int, latencyMs int64, transportErr error, errMsg string) {
	if s.providerHealthBuffer == nil || provider == "" {
		return
	}

	groupID := c.GetString("group_id")

	record := &billing.ProviderHealthRecord{
		Provider:     provider,
		Model:        model,
		GroupID:      groupID,
		TraceID:      "",
		StatusCode:   statusCode,
		LatencyMs:    int(latencyMs),
		ErrorType:    string(billing.ClassifyProviderError(statusCode, transportErr, errMsg)),
		ErrorMessage: errMsg,
		ServerID:     "",
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
	}
	s.providerHealthBuffer.Add(record)
}

// checkAllowedModels verifies the requested model is in the user's allowed list.
// If allowedModels is empty, all models are permitted (no restriction).
func checkAllowedModels(c *gin.Context, model string) bool {
	allowedModels, exists := c.Get("allowed_models")
	if !exists {
		return true // No restriction set
	}
	models, ok := allowedModels.([]string)
	if !ok || len(models) == 0 {
		return true // Empty or malformed = no restriction
	}
	for _, m := range models {
		if m == model {
			return true
		}
	}
	return false
}

// getUserPriority - Get user priority from access_mode (aligned with portal group types)
func getUserPriority(accessMode string) string {
	switch accessMode {
	case "enterprise", "subscription", "token_plan", "single_model":
		return "high"
	case "economy":
		return "economy"
	case "general":
		return "normal"
	default:
		return "normal"
	}
}

// getUpstreamKey - Get upstream key from pool for a provider
func (s *HandlerService) getUpstreamKey(provider string, priority string, model string, preferredKeyID string) (*keypool.KeyInternal, error) {
	if s.keyPoolManager == nil {
		return nil, fmt.Errorf("key pool not initialized")
	}
	key := s.keyPoolManager.GetKeyByPriority(context.Background(), provider, priority, model, preferredKeyID)
	if key == nil {
		return nil, fmt.Errorf("no available upstream keys for provider: %s", provider)
	}
	return key, nil
}

// bedrockContainsMatch checks if a model name matches any item in the slice (case-insensitive).
// Returns the matched item for upstream model name mapping.
func bedrockContainsMatch(slice []string, item string) (bool, string) {
	lowerItem := strings.ToLower(item)
	for _, s := range slice {
		if strings.ToLower(s) == lowerItem {
			return true, s
		}
	}
	return false, ""
}

// resolveTrack determines which routing track the request should use.
// Reads "routing_track" from gin context (set by auth middleware from Portal validateKey).
// Returns "bedrock" or "standard".
func resolveTrack(c *gin.Context) string {
	track := c.GetString("routing_track")
	if track == "bedrock" {
		return "bedrock"
	}
	if track == "mantle" {
		return "mantle"
	}
	return "standard"
}

// resolveBedrockProviderAndKey finds an available Bedrock key for the requested model.
// This is a completely independent routing path for the Bedrock dual-track system.
// It queries Bedrock providers in priority order (multi-region failover) and applies
// a simplified filter chain: HEALTH → LRU (no tier/latency filtering).
// RPM/TPM checks are already done by GetKeysForProvider → checkUpstreamAvailable.
func (s *HandlerService) resolveBedrockProviderAndKey(ctx context.Context, model, preferredKeyID string) (string, *keypool.KeyInternal, string, bool, error) {
	if s.keyPoolManager == nil {
		return "", nil, "", false, fmt.Errorf("key pool not initialized")
	}

	// 1. Try Bedrock providers in priority order (multi-region failover)
	var allCandidates []*keypool.CandidateKey

	for _, provider := range getBedrockProviders() {
		high, normal, economy := s.keyPoolManager.GetKeysForProvider(provider)
		allCandidates = append(allCandidates, high...)
		allCandidates = append(allCandidates, normal...)
		allCandidates = append(allCandidates, economy...)
	}

	if len(allCandidates) == 0 {
		return "", nil, "", false, fmt.Errorf("no available Bedrock keys for model: %s", model)
	}

	// 2. Map model name to Bedrock model ID
	bedrockModelID := aws.MapModelToBedrock(model)

	// 3. Filter by model support (check if key's models list includes this model)
	var filtered []*keypool.CandidateKey
	for _, ck := range allCandidates {
		if len(ck.Key.Models) > 0 {
			matched, _ := bedrockContainsMatch(ck.Key.Models, model)
			if !matched {
				continue
			}
		}
		// Update upstream model name for Bedrock
		ck.UpstreamModelName = bedrockModelID
		filtered = append(filtered, ck)
	}
	if len(filtered) == 0 {
		return "", nil, "", false, fmt.Errorf("no Bedrock keys support model: %s", model)
	}
	candidates := filtered

	// 4. HEALTH filter — exclude unhealthy keys
	keyHealth := s.keyPoolManager.GetKeyHealth()
	if len(keyHealth) > 0 {
		healthFiltered := make([]*keypool.CandidateKey, 0, len(candidates))
		for _, ck := range candidates {
			k := ck.Provider + ":" + ck.Key.ID
			if h, ok := keyHealth[k]; ok {
				if h.ErrorRate > 0.80 || h.Status == "unhealthy" {
					continue
				}
			}
			healthFiltered = append(healthFiltered, ck)
		}
		candidates = healthFiltered
	}

	// Guard: if all keys were filtered out, fail
	if len(candidates) == 0 {
		return "", nil, "", false, fmt.Errorf("no healthy Bedrock keys available for model: %s", model)
	}

	// 5. Affinity (same-provider only)
	if preferredKeyID != "" {
		for _, ck := range candidates {
			if ck.Key.ID == preferredKeyID {
				ck.Key.SetLastUsed(time.Now())
				return ck.Provider, ck.Key, bedrockModelID, false, nil
			}
		}
	}

	// 6. LRU — least recently used wins
	if len(candidates) > 1 {
		sort.SliceStable(candidates, func(i, j int) bool {
			return candidates[i].Key.GetLastUsed().Before(candidates[j].Key.GetLastUsed())
		})
	}

	selected := candidates[0]
	selected.Key.SetLastUsed(time.Now())

	return selected.Provider, selected.Key, bedrockModelID, false, nil
}

// resolveMantleProviderAndKey finds an available Mantle key for the requested model.
// Mantle is AWS Bedrock's newer Bearer-token / OpenAI-compatible API layer.
// Unlike Bedrock Runtime (SigV4 + proprietary URL format), Mantle uses standard
// /v1/chat/completions and /v1/responses endpoints with Bearer auth.
//
// Resolution: iterate Mantle providers in priority order, collect keys that
// support the model, apply tier→LRU selection. No model ID mapping needed
// (Mantle uses the same model names as the upstream key's models list).
func (s *HandlerService) resolveMantleProviderAndKey(ctx context.Context, model, preferredKeyID string) (string, *keypool.KeyInternal, string, bool, error) {
	if s.keyPoolManager == nil {
		return "", nil, "", false, fmt.Errorf("key pool not initialized")
	}

	// 1. Collect keys from all Mantle providers
	var allCandidates []*keypool.CandidateKey
	for _, provider := range getMantleProviders() {
		high, normal, economy := s.keyPoolManager.GetKeysForProvider(provider)
		allCandidates = append(allCandidates, high...)
		allCandidates = append(allCandidates, normal...)
		allCandidates = append(allCandidates, economy...)
	}

	if len(allCandidates) == 0 {
		return "", nil, "", false, fmt.Errorf("no available Mantle keys for model: %s", model)
	}

	// 2. Filter by model support (check if key's models list includes this model)
	var filtered []*keypool.CandidateKey
	for _, ck := range allCandidates {
		if len(ck.Key.Models) > 0 {
			for _, km := range ck.Key.Models {
				if km == model {
					filtered = append(filtered, ck)
					break
				}
			}
		} else {
			// Empty models list = supports all models
			filtered = append(filtered, ck)
		}
	}

	if len(filtered) == 0 {
		return "", nil, "", false, fmt.Errorf("no Mantle key supports model: %s", model)
	}

	// 3. Filter by health (circuit breaker)
	var healthy []*keypool.CandidateKey
	for _, ck := range filtered {
		if providerBreakers.Allow(ck.Provider) {
			healthy = append(healthy, ck)
		}
	}
	if len(healthy) == 0 {
		return "", nil, "", false, fmt.Errorf("all Mantle providers for model %s are circuit-broken", model)
	}

	// 4. Prefer preferred key (affinity)
	if preferredKeyID != "" {
		for _, ck := range healthy {
			if ck.Key.ID == preferredKeyID {
				ck.Key.SetLastUsed(time.Now())
				return ck.Provider, ck.Key, model, false, nil
			}
		}
	}

	// 5. LRU — least recently used wins (same as bedrock resolver)
	if len(healthy) > 1 {
		sort.SliceStable(healthy, func(i, j int) bool {
			return healthy[i].Key.GetLastUsed().Before(healthy[j].Key.GetLastUsed())
		})
	}

	selected := healthy[0]
	selected.Key.SetLastUsed(time.Now())

	return selected.Provider, selected.Key, model, false, nil
}

// resolveProviderAndKey finds an available upstream key for the requested model
// across all providers. It collects candidate keys from every provider that
// supports the model (excluding circuit-broken providers), then applies unified
// priority-based selection — same logic as GetKeyByPriority but cross-provider.
// Primary provider keys are preferred when priorities are equal.
// Uses a Priority-Tier retry loop: all tiers → "high" only → all tiers (last resort).
func (s *HandlerService) resolveProviderAndKey(ctx context.Context, model, userPriority, preferredKeyID string) (string, *keypool.KeyInternal, string, bool, error) {
	return s.resolveProviderAndKeyExcluding(ctx, model, userPriority, preferredKeyID, nil)
}

// resolveProviderAndKeyExcluding is resolveProviderAndKey with a per-request
// exclusion set of already-failed candidates, keyed "provider:keyID". Used by
// in-request failover to skip the key/provider that just failed and move to the
// next candidate in the same admin-priority order. When excluded is non-empty
// (i.e. this is a retry) the affinity short-circuit is skipped so a
// sticky-but-failing key is not reselected.
func (s *HandlerService) resolveProviderAndKeyExcluding(ctx context.Context, model, userPriority, preferredKeyID string, excluded map[string]bool) (string, *keypool.KeyInternal, string, bool, error) {
	return s.resolveProviderAndKeyFiltered(ctx, model, userPriority, preferredKeyID, excluded, nil)
}

// resolveProviderAndKeyFiltered adds an optional provider predicate on top of
// the exclusion set. When providerOK is non-nil, only candidates whose provider
// satisfies it are considered. Used by raw-passthrough failover to restrict the
// failover target to format-compatible (e.g. Anthropic-native) providers, so an
// unchanged passthrough body is never sent to a convert-only upstream.
func (s *HandlerService) resolveProviderAndKeyFiltered(ctx context.Context, model, userPriority, preferredKeyID string, excluded map[string]bool, providerOK func(string) bool) (string, *keypool.KeyInternal, string, bool, error) {
	if s.keyPoolManager == nil {
		return "", nil, "", false, fmt.Errorf("key pool not initialized")
	}
	isRetry := len(excluded) > 0

	// The "primary" provider is the one we consider the non-fallback default for
	// this model. Prefer the admin-configured routing order (model_provider_routing
	// priority 0) — that is the provider we actually intend to serve from. Only
	// fall back to the model-name heuristic (GetProviderFromModel) when no routing
	// is configured. Using the heuristic unconditionally mislabeled every request
	// as is_fallback=true (e.g. glm-5.3 guessed as "zhipu" but served by "approach",
	// with no zhipu key), which polluted the is_fallback signal that real
	// in-request failover now relies on.
	primaryProvider := relay.GetProviderFromModel(model)
	if order := s.keyPoolManager.GetRoutingOrder(model); len(order) > 0 {
		primaryProvider = order[0]
	}

	// Build set of circuit-broken providers once (doesn't change between retries)
	excludedProviders := make(map[string]bool)
	for _, p := range s.keyPoolManager.GetProvidersForModel(model) {
		if !providerBreakers.Allow(p) {
			excludedProviders[p] = true
		}
	}
	if !providerBreakers.Allow(primaryProvider) {
		excludedProviders[primaryProvider] = true
	}

	// Tier progression for retry — each step narrows or falls back the
	// upstream key priority bucket. Users only ever access their own tier
	// and at most one adjacent tier (down for normal/high, up for economy).
	//
	//  high    → high → normal       (no economy fallback)
	//  normal  → normal → economy    (no high escalation)
	//  economy → economy → normal    (up one tier, then back)
	var tierProgression []string
	switch userPriority {
	case "high":
		tierProgression = []string{"high", "normal", "normal"}
	case "economy":
		tierProgression = []string{"economy", "normal", "economy"}
	default:
		tierProgression = []string{"normal", "normal", "economy"}
	}
	maxRetries := len(tierProgression)

	for retry := 0; retry < maxRetries; retry++ {
		tierFilter := tierProgression[retry]

		// Collect candidate keys across all non-excluded providers with tier filter.
		// When tierFilter is set, GetAllKeysForModel only returns keys whose
		// Priority matches that value; the other two slices are empty.
		high, normal, economy := s.keyPoolManager.GetAllKeysForModel(model, excludedProviders, tierFilter)

		// Reassemble: high/normal/economy are mutually exclusive per tierFilter,
		// so it is safe to merge all three unconditionally.
		candidates := high
		candidates = append(candidates, normal...)
		candidates = append(candidates, economy...)

		// Drop candidates already tried-and-failed this request (failover), and
		// any whose provider fails the optional predicate (passthrough targets).
		if len(excluded) > 0 || providerOK != nil {
			filtered := candidates[:0:0]
			for _, ck := range candidates {
				if excluded[ck.Provider+":"+ck.Key.ID] {
					continue
				}
				if providerOK != nil && !providerOK(ck.Provider) {
					continue
				}
				filtered = append(filtered, ck)
			}
			candidates = filtered
		}

		// If no candidates at this tier, try next retry tier
		if len(candidates) == 0 {
			continue
		}
		if os.Getenv("PROXY_DEBUG_ROUTING") != "" {
			byProvider := map[string]int{}
			for _, ck := range candidates {
				byProvider[ck.Provider]++
			}
			s.handlerDeps.Logger.Info("DEBUG routing candidates",
				zap.String("model", model),
				zap.String("tier_filter", tierFilter),
				zap.Int("retry", retry),
				zap.Any("by_provider", byProvider))
		}

		// Check for admin-configured manual routing
		providerOrder := s.keyPoolManager.GetRoutingOrder(model)
		if providerOrder != nil {
			// Manual routing: admin-configured provider order takes priority
			providerRank := make(map[string]int)
			for i, p := range providerOrder {
				providerRank[p] = i
			}
			defaultRank := len(providerRank) + 1

			if preferredKeyID != "" && !isRetry {
				// Affinity pins must never override admin-configured routing
				// priority: only honor the pin when it lands on the current
				// primary provider. A stale pin to a fallback provider would
				// otherwise self-perpetuate (every served request renews the
				// pin) and silently defeat the routing table — e.g. a transient
				// failover to amazon months ago pinning the model there forever.
				for _, ck := range candidates {
					if ck.Key.ID == preferredKeyID {
						if ck.Provider != primaryProvider {
							break
						}
						ck.Key.SetLastUsed(time.Now())
						upstreamModelName := ck.UpstreamModelName
						if mapped := s.keyPoolManager.ResolveUpstreamModelName(ck.Provider, model); mapped != "" {
							upstreamModelName = mapped
						}
						return ck.Provider, ck.Key, upstreamModelName, ck.Provider != primaryProvider, nil
					}
				}
			}

			sort.SliceStable(candidates, func(i, j int) bool {
				iRank, iOk := providerRank[candidates[i].Provider]
				if !iOk {
					iRank = defaultRank
				}
				jRank, jOk := providerRank[candidates[j].Provider]
				if !jOk {
					jRank = defaultRank
				}
				if iRank != jRank {
					return iRank < jRank
				}
				return lessByHealthThenSuccess(candidates[i].Key, candidates[j].Key)
			})
		} else {
			affinityKey := preferredKeyID
			if isRetry {
				affinityKey = ""
			}
			candidates = s.smartRoute(candidates, model, primaryProvider, affinityKey)
		}

		selected := candidates[0]
		selected.Key.SetLastUsed(time.Now())

		upstreamModelName := selected.UpstreamModelName
		if mapped := s.keyPoolManager.ResolveUpstreamModelName(selected.Provider, model); mapped != "" {
			upstreamModelName = mapped
		}

		// For Bedrock providers, ensure the upstream model name is a full Bedrock model ID.
		// Only map through MapModelToBedrock if the name doesn't already look like a
		// Bedrock model ID (i.e., contains no vendor prefix like "anthropic." or "amazon.").
		// If it already has a vendor prefix, it's from the key's models list or model_name_map
		// and should be used as-is (the key's models list reflects actually available models).
		if strings.HasPrefix(selected.Provider, "amazon") && upstreamModelName != "" {
			if !strings.Contains(upstreamModelName, ".") {
				// Standard model name (e.g. "claude-sonnet-4-6") → map to Bedrock ID
				bedrockID := aws.MapModelToBedrock(upstreamModelName)
				if bedrockID != upstreamModelName {
					upstreamModelName = bedrockID
				}
			}
			// If it already has a vendor prefix (e.g. "anthropic.claude-sonnet-4-6"), use as-is
		}

		if selected.Provider != primaryProvider {
			s.handlerDeps.Logger.Info("Fallback routing",
				zap.String("primary_provider", primaryProvider),
				zap.String("fallback_provider", selected.Provider),
				zap.String("model", model),
				zap.String("upstream_model_name", upstreamModelName),
			)
		}

		isFallback := selected.Provider != primaryProvider
		return selected.Provider, selected.Key, upstreamModelName, isFallback, nil
	}

	return "", nil, "", false, fmt.Errorf("no available upstream keys for model: %s after %d retries", model, maxRetries)
}

// effectiveLRUTime returns the timestamp used for round-robin LRU: the more
// recent of lastSuccess and lastUsed. Using lastUsed as a floor means a brand
// new key (never succeeded, lastSuccess==0) is NOT pushed to the back forever —
// it participates fairly by its pool-entry/last-touch time and gets a chance to
// serve. Perpetually-failing keys are handled by the health bucket, not here.
func effectiveLRUTime(k *keypool.KeyInternal) time.Time {
	ls, lu := k.GetLastSuccess(), k.GetLastUsed()
	if ls.After(lu) {
		return ls
	}
	return lu
}

// lessBySuccessThenLRU orders two keys by least-recently-active first
// (round-robin), where "active" = max(lastSuccess, lastUsed). This spreads load
// across keys and avoids both (a) the old bug where plain oldest-LastUsed LRU
// preferred a never-successful key, and (b) starving a brand new key by ranking
// it strictly behind every key that has ever succeeded.
func lessBySuccessThenLRU(a, b *keypool.KeyInternal) bool {
	return effectiveLRUTime(a).Before(effectiveLRUTime(b))
}

// liveHealthTier delegates to keypool.LiveHealthTier, the single
// implementation of the proxy-side real-time per-key health signal (kept
// exported there so the admin selection-state dump reports the same tiers the
// router uses).
func liveHealthTier(k *keypool.KeyInternal) int {
	return keypool.LiveHealthTier(k)
}

// lessByHealthThenSuccess first orders by live health tier (healthy < suspect <
// failing), then by round-robin LRU. Used as the manual-routing tie-break (admin
// provider priority is applied before this) and inside smartRoute.
func lessByHealthThenSuccess(a, b *keypool.KeyInternal) bool {
	if ta, tb := liveHealthTier(a), liveHealthTier(b); ta != tb {
		return ta < tb
	}
	return lessBySuccessThenLRU(a, b)
}

// smartRoute applies the smart routing chain when no manual config exists:
// HEALTH (filter) → AFFINITY (conversation stickiness) → TIER → LATENCY →
// QUOTA → LRU. Ordering criteria are applied in a single composite sort so
// earlier criteria dominate and LRU is only the final tie-breaker.
// RPM/TPM filtering is already performed by GetAllKeysForModel via
// checkUpstreamAvailable, so it is not repeated here.
// All metrics are per-upstream-key, not per-provider.
func (s *HandlerService) smartRoute(candidates []*keypool.CandidateKey, model, primaryProvider, preferredKeyID string) []*keypool.CandidateKey {
	sm := s.keyPoolManager.GetSmartRouting()
	keyHealth := s.keyPoolManager.GetKeyHealth()
	if sm == nil {
		sm = map[string]bool{"health": true, "tier": true, "latency": true, "quota": true}
	}

	// Step 1: HEALTH — exclude unhealthy keys (error_rate > 80% or status=unhealthy)
	// Per-key health from Portal sync; falls back to per-provider health if no per-key data.
	if sm["health"] {
		filtered := make([]*keypool.CandidateKey, 0, len(candidates))
		for _, ck := range candidates {
			k := ck.Provider + ":" + ck.Key.ID
			if h, ok := keyHealth[k]; ok {
				if h.ErrorRate > 0.80 || h.Status == "unhealthy" {
					continue
				}
			}
			filtered = append(filtered, ck)
		}
		if len(filtered) > 0 {
			candidates = filtered
		}
	}

	if len(candidates) <= 1 {
		return candidates
	}

	// Step 2: AFFINITY — the key that successfully served this conversation
	// fingerprint wins outright, preserving upstream prompt-cache locality
	// (cached input tokens are priced ~10x lower than uncached on DeepSeek).
	if preferredKeyID != "" {
		for i, ck := range candidates {
			if ck.Key.ID == preferredKeyID {
				if i != 0 {
					candidates[0], candidates[i] = candidates[i], candidates[0]
				}
				return candidates
			}
		}
	}

	// Step 3: composite ordering — TIER → LATENCY bucket → remaining QUOTA → LRU.
	tierRank := func(tier string) int {
		switch tier {
		case "premium":
			return 0
		case "enterprise":
			return 1
		case "economy":
			return 3
		default: // "" and "default"
			return 2
		}
	}
	latencyBucketOf := func(ck *keypool.CandidateKey) int {
		ih, ok := keyHealth[ck.Provider+":"+ck.Key.ID]
		if !ok {
			return 0
		}
		switch avg := ih.AvgLatencyMs; {
		case avg <= 0:
			return 0
		case avg < 800:
			return 1
		case avg < 2000:
			return 2
		case avg < 5000:
			return 3
		default:
			return 4
		}
	}
	// quotaScore: limited keys first, then more remaining quota first.
	quotaScore := func(k *keypool.KeyInternal) int64 {
		if k.QuotaLimit <= 0 {
			return math.MinInt64
		}
		return k.QuotaLimit - k.GetQuotaUsed()
	}
	// liveHealthTier (shared, 3-level: healthy<suspect<failing) deprioritizes
	// keys with a live failure streak using the in-proxy signal that
	// Portal-synced keyHealth cannot see yet. Defined as a package function.

	sort.SliceStable(candidates, func(i, j int) bool {
		ki, kj := candidates[i], candidates[j]
		if sm["tier"] {
			if r1, r2 := tierRank(ki.Key.Tier), tierRank(kj.Key.Tier); r1 != r2 {
				return r1 < r2
			}
		}
		if sm["latency"] {
			if b1, b2 := latencyBucketOf(ki), latencyBucketOf(kj); b1 != b2 {
				return b1 < b2
			}
		}
		if sm["quota"] {
			if q1, q2 := quotaScore(ki.Key), quotaScore(kj.Key); q1 != q2 {
				return q1 > q2
			}
		}
		// Plan c: deprioritize keys with a live failure streak (3-tier).
		if b1, b2 := liveHealthTier(ki.Key), liveHealthTier(kj.Key); b1 != b2 {
			return b1 < b2
		}
		// Plan b: success-aware round-robin LRU (max(lastSuccess,lastUsed)).
		return lessBySuccessThenLRU(ki.Key, kj.Key)
	})

	return candidates
}

// resolveBaseURL returns the upstream base URL to use, in priority order:
// 1. upstream key's custom base_url
// 2. Portal sync provider config
// 3. Proxy DefaultProviderConfigs fallback
func (s *HandlerService) resolveBaseURL(upstreamKey *keypool.KeyInternal, provider string) string {
	if upstreamKey.BaseURL != "" {
		return upstreamKey.BaseURL
	}
	if s.keyPoolManager != nil {
		if syncedURL, ok := s.keyPoolManager.GetProviderConfig(provider); ok {
			return syncedURL
		}
	}
	if cfg, ok := relay.GetProviderConfig(provider); ok {
		return cfg.BaseURL
	}
	return ""
}

// handleSimulate - Handle simulate mode requests
func (s *HandlerService) handleSimulate(c *gin.Context, req dto.GeneralOpenAIRequest, keyID, userID, keyHash string, startTime time.Time) {
	isStream := req.Stream != nil && *req.Stream
	model := req.Model
	if model == "" {
		model = "gpt-3.5-turbo"
	}

	// Simulate latency (0 means no artificial delay)
	latency := time.Duration(s.handlerDeps.Simulate.LatencyMs) * time.Millisecond
	if latency > 0 {
		time.Sleep(latency)
	}

	// Generate simulated response
	responseContent := fmt.Sprintf("Simulated response for model %s", model)

	if isStream {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")

		// Send streaming chunks
		for _, char := range responseContent {
			chunk := gin.H{
				"id":      "chatcmpl-sim",
				"object":  "chat.completion.chunk",
				"created": time.Now().Unix(),
				"model":   model,
				"choices": []gin.H{{
					"index":         0,
					"delta":         gin.H{"content": string(char)},
					"finish_reason": nil,
				}},
			}
			c.SSEvent("message", chunk)
			c.Writer.Flush()
			time.Sleep(10 * time.Millisecond)
		}

		// Send done
		c.SSEvent("message", "[DONE]")
		c.Writer.Flush()
	} else {
		c.JSON(http.StatusOK, gin.H{
			"id":      "chatcmpl-sim",
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   model,
			"choices": []gin.H{{
				"index": 0,
				"message": gin.H{
					"role":    "assistant",
					"content": responseContent,
				},
				"finish_reason": "stop",
			}},
			"usage": gin.H{
				"prompt_tokens":     10,
				"completion_tokens": 20,
				"total_tokens":      30,
			},
		})
	}

	// Log audit
	if s.handlerDeps.AuditLogger != nil {
		s.logAudit(c, audit.AuditEntry{
			Timestamp:     startTime,
			KeyID:         keyID,
			UpstreamKeyID: "", // simulate mode has no upstream key
			APIKeyHash:    keyHash,
			UserID:        userID,
			Provider:      "simulate",
			Model:         model,
			InputTokens:   10,
			OutputTokens:  20,
			Cost:          0,
			LatencyMs:     time.Since(startTime).Milliseconds(),
			StatusCode:    200,
			RequestType:   "simulate",
		})
	}

	// Record token usage for billing (async, non-blocking)
	// Simulate mode uses fixed 30 tokens per request
	usage := &dto.Usage{
		PromptTokens:     10,
		CompletionTokens: 20,
	}
	s.recordTokenUsageAsync(keyHash, usage)

	// Capture retention for simulate mode
	var requestBody []byte
	if v, ok := c.Get("request_body"); ok {
		if b, ok := v.([]byte); ok {
			requestBody = b
		}
	}
	var responseBody []byte
	if !isStream {
		responseBody = []byte(fmt.Sprintf(`{"choices":[{"message":{"content":%q}}]}`, responseContent))
	}
	setRetentionContext(c, "openai", "simulate", model, false)
	s.captureRetention(c, requestBody, responseBody, "simulate", model, "", usage, time.Since(startTime).Milliseconds(), http.StatusOK, isStream, "simulate")
}

// roundRobinCounters stores in-memory rotation counters for round_robin mode
// Note: This is per-Proxy-instance. For distributed consistency, use Redis.
var roundRobinCounters sync.Map

// resolveModel resolves the requested model based on routing mode
func (s *HandlerService) resolveModel(reqModel, routingMode, currentModel string, selectedModels, allowedModels []string, keyHash string) (string, error) {
	// 1. wgit-maas-latest alias resolution
	if reqModel == "wgit-maas-latest" {
		// This alias is only resolved on text endpoints (chat/responses).
		// Media models (image/video/audio) can never serve a text request,
		// so drop them from the candidate lists instead of resolving to a
		// model that immediately 400s with MEDIA_MODEL_WRONG_ENDPOINT.
		// candidates that can actually serve this request: media models are
		// wrong for a text endpoint, and models outside allowedModels (when
		// the key has an allowlist) would 403 immediately after resolution.
		textOnly := func(models []string) []string {
			out := make([]string, 0, len(models))
			for _, m := range models {
				if s.keyPoolManager != nil && keypool.IsMediaModelType(s.keyPoolManager.GetModelTypeAny(m)) {
					continue
				}
				if len(allowedModels) > 0 && !containsString(allowedModels, m) {
					continue
				}
				out = append(out, m)
			}
			return out
		}
		switch routingMode {
		case "single":
			if currentModel == "" {
				return "", fmt.Errorf("wgit-maas-latest requires a current_model to be configured. Please set your model in the dashboard")
			}
			if s.keyPoolManager != nil && keypool.IsMediaModelType(s.keyPoolManager.GetModelTypeAny(currentModel)) {
				return "", fmt.Errorf("wgit-maas-latest resolved to '%s', which is a media generation model and cannot serve chat completions. Set a text model in the dashboard", currentModel)
			}
			return currentModel, nil
		case "round_robin":
			models := textOnly(selectedModels)
			if len(models) == 0 {
				return "", fmt.Errorf("wgit-maas-latest requires text-capable selected_models for round_robin mode (media-only lists cannot use chat completions)")
			}
			return s.selectRoundRobin(keyHash, models), nil
		case "priority":
			models := textOnly(selectedModels)
			if len(models) == 0 {
				return "", fmt.Errorf("wgit-maas-latest requires text-capable selected_models for priority mode (media-only lists cannot use chat completions)")
			}
			return models[0], nil
		default:
			return currentModel, nil
		}
	}

	// 2. Real model name validation — check against allowed_models (group permission boundary)
	if len(allowedModels) > 0 && !containsString(allowedModels, reqModel) {
		return "", fmt.Errorf("model '%s' is not available in your group. Available models: %v. Use 'wgit-maas-latest' or request a model within your group's allowed list", reqModel, allowedModels)
	}

	return reqModel, nil
}

// normalizeReasoningFields maps the non-standard `reasoning` field some
// clients (e.g. WorkBuddy) send on assistant history messages to the
// DeepSeek-standard `reasoning_content`, which DeepSeek thinking mode
// requires to be passed back. The non-standard field is always stripped so
// it is never forwarded upstream (preserving the previous drop behavior).
func normalizeReasoningFields(req *dto.GeneralOpenAIRequest) {
	isDeepSeek := strings.Contains(strings.ToLower(req.Model), "deepseek")
	for i := range req.Messages {
		m := &req.Messages[i]
		if len(m.Reasoning) == 0 {
			continue
		}
		var text string
		if err := json.Unmarshal(m.Reasoning, &text); err == nil &&
			isDeepSeek && m.ReasoningContent == nil && text != "" {
			m.ReasoningContent = &text
		}
		m.Reasoning = nil
	}
}

// selectRoundRobin selects the next model in rotation.
// Uses Redis via keypool manager for distributed consistency, with in-memory fallback.
func (s *HandlerService) selectRoundRobin(keyHash string, models []string) string {
	if len(models) == 0 {
		return ""
	}
	if len(models) == 1 {
		return models[0]
	}

	// Use keypool manager's Redis-based round robin if available
	if s.keyPoolManager != nil {
		return s.keyPoolManager.SelectRoundRobinModel(keyHash, models)
	}

	// Fallback to in-memory counter
	actual, _ := roundRobinCounters.LoadOrStore(keyHash, new(int64))
	counter := actual.(*int64)
	count := atomic.AddInt64(counter, 1)

	index := int(count-1) % len(models)
	return models[index]
}

// containsString checks if a string slice contains a value
func containsString(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// estimateActualCost calculates the cost in micro-USD for budget tracking.
func (s *HandlerService) estimateActualCost(model, provider string, usage *dto.Usage) int64 {
	if usage == nil || s.handlerDeps == nil || s.handlerDeps.Billing == nil {
		return 0
	}
	costUSD := s.handlerDeps.Billing.CalculateCost(usage, model, provider)
	return int64(costUSD * 1_000_000)
}

// dispatchUpstream executes the upstream HTTP call. When the provider queue is
// enabled and the request is non-streaming, the call is handed to a worker pool.
// The caller remains responsible for parsing, usage recording, settle, and the
// client response.
func (s *HandlerService) dispatchUpstream(c *gin.Context, provider string, adaptor relay.Adaptor, upstreamBody []byte, isStream bool) (statusCode int, body []byte, err error) {
	ctx := c.Request.Context()

	if !isStream && s.keyPoolManager != nil && s.keyPoolManager.QueueEnabled() {
		res, qErr := s.keyPoolManager.Dispatch(ctx, provider, keypool.ProviderJob{
			Ctx:          ctx,
			Adaptor:      adaptor,
			UpstreamBody: upstreamBody,
			Provider:     provider,
			IsStream:     false,
		})
		if qErr == nil {
			return res.StatusCode, res.Body, res.Err
		}
		s.handlerDeps.Logger.Warn("upstream queue dispatch failed, falling back to inline",
			zap.String("provider", provider),
			zap.Error(qErr),
		)
	}

	resp, err := adaptor.DoRequestBytesWithContext(ctx, upstreamBody, isStream)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	const maxResponseBody = 50 << 20 // 50 MiB
	body, err = io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	return resp.StatusCode, body, err
}

// localBalanceCacheEnabled reports whether the local Redis-backed balance cache
// can be used for this request.
func (s *HandlerService) localBalanceCacheEnabled(userID string) bool {
	return s.cfg != nil && s.cfg.Billing.LocalBalanceCacheEnabled &&
		s.authMiddleware != nil && userID != ""
}

// preConsumeLocal tries to reserve the estimated input cost in the local
// balance cache. It returns the reserved micro-USD amount, a flag indicating
// whether the local cache handled the request, and a flag indicating whether
// the caller should abort with an insufficient-balance response.
//
// Known limitation: the cache is per-proxy and uses optimistic Redis decr.
// In a multi-proxy deployment, concurrent decrs from different instances can
// briefly overdraw a user until Portal settlement reconciles the balance.
// For high-risk users (balance near zero), consider disabling the local cache
// so that all requests use the Portal synchronous pre-consume path.
func (s *HandlerService) preConsumeLocal(c *gin.Context, userID, keyType, model, provider string, inputTokens int) (amountMicro int64, used bool, rejected bool) {
	if !s.localBalanceCacheEnabled(userID) || keyType == "enterprise" || keyType == "subscription" || inputTokens <= 0 ||
		s.handlerDeps == nil || s.handlerDeps.Billing == nil {
		return 0, false, false
	}

	estimated := s.handlerDeps.Billing.EstimateInputCost(model, provider, inputTokens, s.cfg.Billing.PreConsumeMargin)
	if estimated <= 0 {
		return 0, false, false
	}

	_, ok, handled := s.authMiddleware.PreConsumeLocal(userID, estimated)
	if !handled {
		// No cached balance present; fail-open and let portal pre-consume handle it.
		return 0, false, false
	}
	if !ok {
		c.JSON(http.StatusPaymentRequired, gin.H{"error": gin.H{
			"message": "Insufficient balance for this request",
			"type":    "billing_error",
			"code":    "INSUFFICIENT_BALANCE",
		}})
		return estimated, true, true
	}
	return estimated, true, false
}

// classifyTransportError maps an upstream dispatch error to the errorType used
// for billing settlement. Only genuine timeouts (client-side deadline while
// the upstream may already be processing) justify charging the pre-consumed
// estimate; other transport errors (DNS failure, connection refused, TLS
// handshake failure, ...) mean the upstream never received the request, so
// the estimate must be refunded.
func classifyTransportError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}
	return "transport_error"
}

// settleLocal adjusts the local balance cache after a request completes.
// It mirrors the portal settle semantics: success charges actual usage,
// timeout keeps the pre-consumed estimate, and 4xx/5xx refunds the estimate.
func (s *HandlerService) settleLocal(userID string, preConsumedMicro int64, usage *dto.Usage, model, provider string, success bool, statusCode int, errorType string) {
	if preConsumedMicro <= 0 || !s.localBalanceCacheEnabled(userID) {
		return
	}

	var actualMicro int64
	if success && usage != nil && s.handlerDeps != nil && s.handlerDeps.Billing != nil {
		actualMicro = s.estimateActualCost(model, provider, usage)
	}

	switch {
	case success && usage != nil && usage.PromptTokens+usage.CompletionTokens > 0:
		s.authMiddleware.SettleLocal(userID, preConsumedMicro, actualMicro)
	case success:
		// Upstream succeeded but reported no token usage; refunding would make
		// the request completely free. Keep the pre-consumed estimate instead.
		s.authMiddleware.ChargeLocal(userID, preConsumedMicro)
	case errorType == "timeout":
		// Keep the pre-consumed estimate (upstream likely consumed input tokens).
		s.authMiddleware.ChargeLocal(userID, preConsumedMicro)
	default:
		// 4xx/5xx or transport errors (upstream never saw the request): refund.
		s.authMiddleware.SettleLocal(userID, preConsumedMicro, 0)
	}
}

// checkAllowedModelsList checks if a model is in the allowed list (without gin context).
func checkAllowedModelsList(allowedModels []string, model string) bool {
	if len(allowedModels) == 0 {
		return true // no restriction
	}
	for _, m := range allowedModels {
		if m == model {
			return true
		}
	}
	return false
}

// mediaEndpointForType maps a media model type to the endpoint that serves it.
// Used in MEDIA_MODEL_WRONG_ENDPOINT errors to point callers at the right route.
func mediaEndpointForType(modelType string) string {
	switch modelType {
	case "image":
		return "/v1/images/generations"
	case "video":
		return "/v1/video/generations"
	case "audio":
		return "/v1/audio/speech"
	default:
		return "its dedicated endpoint"
	}
}

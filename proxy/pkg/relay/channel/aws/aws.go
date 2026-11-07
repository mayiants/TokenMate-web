package aws

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mayiants/ai-proxy-go/dto"
	"github.com/mayiants/ai-proxy-go/pkg/relay/base"
	"github.com/mayiants/ai-proxy-go/types"
	"github.com/google/uuid"
)

// AWSAdaptor - AWS Bedrock adapter for the bedrock-runtime endpoint.
// Expects cfg.BaseURL like https://bedrock-runtime.{region}.amazonaws.com
//
// Two credential forms are supported on cfg.APIKey:
//   - AK/SK: "access_key_id:secret_access_key" or
//     "access_key_id:secret_access_key:session_token" → requests are SigV4-signed.
//   - Bedrock API Key (ABSK...): a single opaque token with no ':' separator →
//     sent as "Authorization: Bearer <token>" with no SigV4 signing. AWS accepts
//     these on bedrock-runtime InvokeModel/Converse via CallWithBearerToken, so
//     the same key can serve both the runtime track (here) and the Mantle track.
//
// The `model` field is captured in ToProvider for use by DoRequest. It is
// effectively per-request: a new adaptor is created per request, so the field
// is not shared across concurrent requests. If the adaptor is ever reused
// across requests, this must be passed through the call chain instead.
type AWSAdaptor struct {
	*base.BaseAdaptor
	region       string
	accessKey    string
	secretKey    string
	sessionToken string
	bearerToken  string // Bedrock API Key (ABSK...) for Bearer auth; mutually exclusive with accessKey/secretKey
	model        string // captured in ToProvider for use by DoRequest
}

// NewAWSAdaptor - Create AWS Bedrock adapter
func NewAWSAdaptor(cfg *base.AdaptorConfig) *AWSAdaptor {
	baseURL := ""
	region := "us-east-1"
	accessKey := ""
	secretKey := ""
	sessionToken := ""
	bearerToken := ""

	if cfg != nil {
		baseURL = cfg.BaseURL
		if cfg.APIKey != "" {
			// A Bedrock API Key (ABSK...) is a single opaque token with no ':'
			// separator; AK/SK is "access_key:secret_key[:session_token]".
			// Use the ':' presence as the discriminator, with an explicit
			// prefix check as a guard for well-known Bearer key shapes.
			if IsBedrockBearerKey(cfg.APIKey) {
				bearerToken = cfg.APIKey
			} else {
				parts := strings.SplitN(cfg.APIKey, ":", 3)
				if len(parts) >= 2 {
					accessKey = parts[0]
					secretKey = parts[1]
					if len(parts) == 3 {
						sessionToken = parts[2]
					}
				} else {
					// Single token without ':' and not a recognized Bearer key:
					// treat as a Bearer token (safer than an unsigned SigV4
					// request that would fail with 401 anyway).
					bearerToken = cfg.APIKey
				}
			}
		}
	}

	if baseURL == "" {
		baseURL = "https://bedrock-runtime.us-east-1.amazonaws.com"
	}

	// Extract region from BaseURL
	// Format: https://bedrock-runtime.us-east-1.amazonaws.com
	if strings.Contains(baseURL, "bedrock-runtime.") {
		start := strings.Index(baseURL, "bedrock-runtime.") + len("bedrock-runtime.")
		end := strings.Index(baseURL[start:], ".")
		if end > 0 {
			region = baseURL[start : start+end]
		}
	}

	a := &AWSAdaptor{
		region:       region,
		accessKey:    accessKey,
		secretKey:    secretKey,
		sessionToken: sessionToken,
		bearerToken:  bearerToken,
	}
	a.BaseAdaptor = base.NewBaseAdaptor("amazon", &base.AdaptorConfig{
		BaseURL:  baseURL,
		APIKey:   cfg.APIKey,
		Models:   cfg.Models,
		AuthType: "aws",
	})
	a.SetEndpointResolver(a.GetEndpoint)
	return a
}

// IsBedrockBearerKey reports whether an APIKey value is a Bedrock API Key
// (Bearer token) rather than an "access_key:secret_key" pair. Bedrock API
// keys are a single opaque token (commonly prefixed "ABSK" or
// "bedrock-api-key-") and never contain the ':' that separates AK/SK fields.
func IsBedrockBearerKey(apiKey string) bool {
	if strings.Contains(apiKey, ":") {
		return false
	}
	if strings.HasPrefix(apiKey, "ABSK") || strings.HasPrefix(apiKey, "bedrock-api-key-") {
		return true
	}
	// Any single token without ':' is treated as a Bearer token; a bare
	// access key alone cannot produce a valid SigV4 signature anyway.
	return apiKey != ""
}

// GetRelayFormat - Get relay format
func (a *AWSAdaptor) GetRelayFormat() types.RelayFormat {
	return types.RelayFormatOpenAI
}

// GetHeaders - AWS SigV4 headers are added during signing, not here
func (a *AWSAdaptor) GetHeaders() map[string]string {
	return map[string]string{
		"Content-Type": "application/json",
	}
}

// dynamicModelMapping holds runtime-configurable model mappings (e.g. synced from Portal).
// Protected by modelMapMu.
var (
	modelMapMu      sync.RWMutex
	dynamicModelMap map[string]string
)

// SetModelMapping sets (or replaces) the dynamic Bedrock model mapping table.
// The hard-coded fallback table is used when a model is not found in this map.
func SetModelMapping(m map[string]string) {
	modelMapMu.Lock()
	defer modelMapMu.Unlock()
	dynamicModelMap = m
}

// MapModelToBedrock - Map OpenAI model name to Bedrock model ID.
// Exported for use by the dual-track routing layer.
// First checks the dynamic mapping table (e.g. synced from Portal), then falls back
// to the hard-coded table. This allows new models to be added without re-deploying.
//
// Mapping priority:
//  1. Dynamic mapping table (synced from Portal service_models)
//  2. Already-qualified IDs (global.*, us.*, etc.) → pass through
//  3. Hard-coded fallback table → returns global.* (highest quota) inference profile ID
//
// Bedrock inference profile priority strategy:
//   - global.* → cross-region, highest quota pool (preferred)
//   - us.*     → single-region US fallback
//   - ap.*     → single-region Asia-Pacific fallback
//   - eu.*     → single-region Europe fallback
//   - anthropic.* (bare) → legacy, may not work for on-demand
func MapModelToBedrock(model string) string {
	m := strings.ToLower(model)

	// 1. Check dynamic mapping table first
	modelMapMu.RLock()
	if dynamicModelMap != nil {
		if mapped, ok := dynamicModelMap[m]; ok {
			modelMapMu.RUnlock()
			return mapped
		}
	}
	modelMapMu.RUnlock()

	// 2. If the model already has an inference profile prefix, return as-is.
	if strings.HasPrefix(m, "global.") || strings.HasPrefix(m, "us.") ||
		strings.HasPrefix(m, "eu.") || strings.HasPrefix(m, "ap.") {
		return model
	}

	// 3. Hard-coded fallback table
	// Strategy: prefer global.* (cross-region, highest quota), with comments
	// showing the us.* fallback for reference.
	switch {
	// ─── Claude 4.6+ series (latest) ───
	case strings.Contains(m, "claude-opus-4-8"):
		return "global.anthropic.claude-opus-4-8"
	case strings.Contains(m, "claude-opus-4-7"):
		return "global.anthropic.claude-opus-4-7"
	case strings.Contains(m, "claude-opus-4-6"):
		return "global.anthropic.claude-opus-4-6-v1"
	case strings.Contains(m, "claude-sonnet-4-6"):
		return "global.anthropic.claude-sonnet-4-6"
	case strings.Contains(m, "claude-sonnet-5"):
		return "global.anthropic.claude-sonnet-5"
	case strings.Contains(m, "claude-fable-5"):
		return "global.anthropic.claude-fable-5"

	// ─── Claude 4.5 series ───
	case strings.Contains(m, "claude-opus-4-5"):
		return "global.anthropic.claude-opus-4-5-20251101-v1:0"
	case strings.Contains(m, "claude-sonnet-4-5"):
		return "global.anthropic.claude-sonnet-4-5-20250929-v1:0"
	case strings.Contains(m, "claude-haiku-4-5"):
		return "global.anthropic.claude-haiku-4-5-20251001-v1:0"

	// ─── Claude 4.0 series ───
	case strings.Contains(m, "claude-sonnet-4"):
		return "global.anthropic.claude-sonnet-4-20250514-v1:0"

	// ─── Claude 3.5 series (legacy) ───
	case strings.Contains(m, "claude-3-5-sonnet"):
		return "us.anthropic.claude-3-5-sonnet-20240620-v1:0"

	// ─── Claude 3 series (legacy, us. only — no global available) ───
	case strings.Contains(m, "claude-3-opus"):
		return "us.anthropic.claude-3-opus-20240229-v1:0"
	case strings.Contains(m, "claude-3-sonnet"):
		return "us.anthropic.claude-3-sonnet-20240229-v1:0"
	case strings.Contains(m, "claude-3-haiku"):
		return "us.anthropic.claude-3-haiku-20240307-v1:0"

	// Catch-all Claude → latest Sonnet (global)
	case strings.Contains(m, "claude"):
		return "global.anthropic.claude-sonnet-4-6"

	// ─── OpenAI GPT-OSS (on Bedrock) ───
	case strings.Contains(m, "gpt-oss-120b"):
		return "openai.gpt-oss-120b-1:0"
	case strings.Contains(m, "gpt-oss-20b"):
		return "openai.gpt-oss-20b-1:0"
	case strings.Contains(m, "gpt-oss"):
		return "openai.gpt-oss-20b-1:0"

	// ─── Meta Llama ───
	case strings.Contains(m, "llama-3") && strings.Contains(m, "70b"):
		return "meta.llama3-70b-instruct-v1:0"
	case strings.Contains(m, "llama-3"):
		return "meta.llama3-8b-instruct-v1:0"

	// ─── Mistral ───
	case strings.Contains(m, "mistral-large"):
		return "mistral.mistral-large-2402-v1:0"
	case strings.Contains(m, "mistral"):
		return "mistral.mistral-7b-instruct-v0:2"

	// ─── Amazon Nova ───
	case strings.Contains(m, "nova-pro"):
		return "amazon.nova-pro-v1:0"
	case strings.Contains(m, "nova-lite"):
		return "amazon.nova-lite-v1:0"
	case strings.Contains(m, "nova-micro"):
		return "amazon.nova-micro-v1:0"
	case strings.Contains(m, "amazon"):
		return "amazon.titan-text-express-v1"

	default:
		// Default to Claude Sonnet 4.6 (global) for unknown models
		return "global.anthropic.claude-sonnet-4-6"
	}
}

// bedrockRegionPrefixes defines recognized inference profile region prefixes.
// Order matters: global (highest quota) > regional geo prefixes > bare vendor.
var bedrockRegionPrefixes = []string{"global.", "us.", "eu.", "ap."}

// bedrockVendorPrefixes are bare vendor prefixes that indicate an already-qualified
// Bedrock model ID, but without an inference profile wrapper.
var bedrockVendorPrefixes = []string{
	// NOTE: "openai." is intentionally absent — OpenAI models on Bedrock
	// (gpt-oss-*) are on-demand IDs without cross-region inference profiles,
	// and promoting them to global.* yields "model identifier is invalid".
	"anthropic.", "amazon.", "meta.", "mistral.",
	"ai21.", "cohere.", "stability.", "deepseek.", "google.",
	"minimax.", "moonshot.", "moonshotai.", "nvidia.", "qwen.",
	"writer.", "zai.", "twelvelabs.",
}

// GetEndpoint - Bedrock Converse endpoint
func (a *AWSAdaptor) GetEndpoint(model string, stream bool) string {
	modelID := resolveBedrockModelID(model)
	if stream {
		return "/model/" + modelID + "/converse-stream"
	}
	return "/model/" + modelID + "/converse"
}

// resolveBedrockModelID determines the final model ID to use in the Bedrock URL.
// Priority:
//  1. If already has a region prefix (global.*, us.*, eu.*, ap.*) → use as-is
//  2. If has a bare vendor prefix (anthropic.*) → promote to global.* if available, else us.*
//  3. Otherwise → run through MapModelToBedrock mapping
func resolveBedrockModelID(model string) string {
	m := strings.ToLower(model)

	// Already has region prefix → use as-is
	for _, prefix := range bedrockRegionPrefixes {
		if strings.HasPrefix(m, prefix) {
			return model
		}
	}

	// Has bare vendor prefix (e.g. "anthropic.claude-sonnet-4-6") → promote to global.*
	// Bedrock requires inference profiles for on-demand throughput.
	for _, prefix := range bedrockVendorPrefixes {
		if strings.HasPrefix(m, prefix) {
			// Prefer global (cross-region, highest quota), fallback to us.
			return "global." + model
		}
	}

	// Not a Bedrock ID yet → map through the lookup table
	return MapModelToBedrock(model)
}

// GetRequestURL - Build full Bedrock URL
func (a *AWSAdaptor) GetRequestURL(model string, stream bool) string {
	endpoint := a.GetEndpoint(model, stream)
	return strings.TrimSuffix(a.BaseURL, "/") + endpoint
}

// bedrockMessage represents a message in Bedrock Converse format
type bedrockMessage struct {
	Role    string           `json:"role"`
	Content []bedrockContent `json:"content"`
}

type bedrockContent struct {
	Text       string               `json:"text,omitempty"`
	ToolUse    *bedrockToolUse      `json:"toolUse,omitempty"`
	ToolResult *bedrockToolResult   `json:"toolResult,omitempty"`
	Image      *bedrockImageContent `json:"image,omitempty"`
	CachePoint *bedrockCachePoint   `json:"cachePoint,omitempty"`
}

// bedrockCachePoint marks a prompt-caching breakpoint: Bedrock caches
// everything before it and serves matching prefixes from cache on later
// requests (charged at cache-read/write rates instead of full input price).
type bedrockCachePoint struct {
	Type string `json:"type"`
}

type bedrockToolUse struct {
	ToolUseID string          `json:"toolUseId"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
}

type bedrockToolResult struct {
	ToolUseID string           `json:"toolUseId"`
	Content   []bedrockContent `json:"content"`
	Status    string           `json:"status,omitempty"`
}

type bedrockImageContent struct {
	Format string             `json:"format"`
	Source bedrockImageSource `json:"source"`
}

type bedrockImageSource struct {
	Bytes string `json:"bytes"`
}

// bedrockRequest represents a Bedrock Converse request
type bedrockRequest struct {
	Messages        []bedrockMessage       `json:"messages"`
	System          []bedrockContent       `json:"system,omitempty"`
	InferenceConfig bedrockInferenceConfig `json:"inferenceConfig,omitempty"`
	ToolConfig      *bedrockToolConfig     `json:"toolConfig,omitempty"`
}

type bedrockInferenceConfig struct {
	MaxTokens   int     `json:"maxTokens,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
	TopP        float64 `json:"topP,omitempty"`
}

type bedrockToolConfig struct {
	Tools      []bedrockTool      `json:"tools"`
	ToolChoice *bedrockToolChoice `json:"toolChoice,omitempty"`
}

type bedrockTool struct {
	ToolSpec *bedrockToolSpec `json:"toolSpec,omitempty"`
}

type bedrockToolSpec struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema bedrockToolInputSchema `json:"inputSchema"`
}

// bedrockToolInputSchema wraps the JSON Schema in the Bedrock UNION format.
// Bedrock Converse API requires: {"json": <actual_schema>}
type bedrockToolInputSchema struct {
	JSON json.RawMessage `json:"json"`
}

type bedrockToolChoice struct {
	Auto *struct{}        `json:"auto,omitempty"`
	Any  *struct{}        `json:"any,omitempty"`
	Tool *bedrockToolName `json:"tool,omitempty"`
}

type bedrockToolName struct {
	Name string `json:"name"`
}

// ToProvider - Convert OpenAI request to Bedrock Converse format
func (a *AWSAdaptor) ToProvider(req dto.GeneralOpenAIRequest) interface{} {
	// Remember the model so DoRequest can build the correct URL.
	a.model = req.Model

	br := bedrockRequest{
		InferenceConfig: bedrockInferenceConfig{},
	}

	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		br.InferenceConfig.MaxTokens = *req.MaxTokens
	}
	if req.Temperature != nil && *req.Temperature > 0 {
		br.InferenceConfig.Temperature = *req.Temperature
	}
	if req.TopP != nil && *req.TopP > 0 {
		br.InferenceConfig.TopP = *req.TopP
	}

	// Convert tools → Bedrock toolConfig
	if len(req.Tools) > 0 {
		br.ToolConfig = &bedrockToolConfig{
			Tools: make([]bedrockTool, 0, len(req.Tools)),
		}
		for _, t := range req.Tools {
			if t.Type != "function" {
				continue
			}
			schemaJSON, _ := json.Marshal(t.Function.Parameters)
			// Bedrock requires inputSchema to be non-empty. If parameters is
			// nil/empty, default to a minimal valid object schema.
			if len(schemaJSON) == 0 || string(schemaJSON) == "null" || string(schemaJSON) == "{}" {
				schemaJSON = []byte(`{"type":"object","properties":{}}`)
			}
			br.ToolConfig.Tools = append(br.ToolConfig.Tools, bedrockTool{
				ToolSpec: &bedrockToolSpec{
					Name:        t.Function.Name,
					Description: t.Function.Description,
					InputSchema: bedrockToolInputSchema{JSON: schemaJSON},
				},
			})
		}
		// Convert tool_choice
		br.ToolConfig.ToolChoice = convertToolChoice(req.ToolChoice)
	}

	// Convert messages
	// Bedrock Converse requires strict alternating user/assistant turns.
	// Multiple consecutive "tool" messages must be merged into a single "user"
	// message with multiple toolResult content blocks.
	for i := 0; i < len(req.Messages); i++ {
		msg := req.Messages[i]
		role := msg.Role

		// Handle tool result messages (role="tool" in OpenAI → role="user" in Bedrock with toolResult content)
		// Collect all consecutive tool messages into one user message.
		if role == "tool" {
			toolContents := make([]bedrockContent, 0)
			for ; i < len(req.Messages) && req.Messages[i].Role == "tool"; i++ {
				tmsg := req.Messages[i]
				contentText := ""
				switch v := tmsg.Content.(type) {
				case string:
					contentText = v
				default:
					// Non-string content (e.g. a structured tool_result block
					// array serialized to JSON by the Anthropic→OpenAI
					// converter). Bedrock requires a non-blank text block, so
					// JSON-encode anything that isn't already a string.
					if v != nil {
						if b, err := json.Marshal(v); err == nil {
							contentText = string(b)
						}
					}
				}
				toolContents = append(toolContents, bedrockContent{
					ToolResult: &bedrockToolResult{
						ToolUseID: tmsg.ToolCallID,
						Content:   []bedrockContent{{Text: contentText}},
						Status:    "success",
					},
				})
			}
			i-- // outer loop will increment
			br.Messages = append(br.Messages, bedrockMessage{
				Role:    "user",
				Content: toolContents,
			})
			continue
		}

		// Handle assistant messages with tool_calls
		if role == "assistant" && len(msg.ToolCalls) > 0 {
			contents := make([]bedrockContent, 0)
			// Add text content if present
			if text, ok := msg.Content.(string); ok && text != "" {
				contents = append(contents, bedrockContent{Text: text})
			}
			// Add toolUse blocks
			for _, tc := range msg.ToolCalls {
				if tc.Type != "function" {
					continue
				}
				// tc.Function.Arguments is already a JSON string (e.g. "{\"city\":\"Tokyo\"}")
				// Bedrock expects input as raw JSON (json.RawMessage), not a re-encoded string.
				inputRaw := json.RawMessage(tc.Function.Arguments)
				if len(inputRaw) == 0 || string(inputRaw) == "" {
					inputRaw = json.RawMessage(`{}`)
				}
				contents = append(contents, bedrockContent{
					ToolUse: &bedrockToolUse{
						ToolUseID: tc.ID,
						Name:      tc.Function.Name,
						Input:     inputRaw,
					},
				})
			}
			if len(contents) == 0 {
				// No usable content (e.g. only non-function tool calls):
				// Bedrock requires at least one content block per message.
				continue
			}
			br.Messages = append(br.Messages, bedrockMessage{
				Role:    "assistant",
				Content: contents,
			})
			continue
		}

		contentText := ""
		switch v := msg.Content.(type) {
		case string:
			contentText = v
		case []interface{}:
			// Multi-modal: extract text parts
			for _, part := range v {
				if p, ok := part.(map[string]interface{}); ok {
					if t, ok := p["text"].(string); ok {
						contentText += t
					}
				}
			}
		}

		if role == "system" {
			// Bedrock uses top-level system field
			if contentText != "" {
				br.System = append(br.System, bedrockContent{Text: contentText})
			}
			continue
		}
		if role == "assistant" {
			role = "assistant"
		} else {
			role = "user"
		}
		// Bedrock rejects blank text blocks ("The text field in the
		// ContentBlock object ... is blank"), so drop messages whose
		// content is empty (e.g. OpenAI clients that send
		// {"role":"assistant","content":""} in history).
		if contentText == "" {
			continue
		}
		br.Messages = append(br.Messages, bedrockMessage{
			Role:    role,
			Content: []bedrockContent{{Text: contentText}},
		})
	}

	// Prompt caching: Bedrock caches everything before a cachePoint block
	// and serves matching prefixes from cache on later requests at
	// cache-read prices (~10% of input price). Coding agents resend a
	// growing conversation every turn, so caching the system prompt and
	// the full current prefix turns the next turn's history into cache
	// reads. Bedrock rejects cachePoints whose prefix is below the
	// model's minimum (~1024 tokens), so only inject above a conservative
	// size heuristic (~4 chars/token).
	injectCachePoints(&br)

	return br
}

// minCacheableChars approximates Bedrock's minimum cacheable prefix
// (1024 tokens for Claude models) using ~4 chars per token.
const minCacheableChars = 4096

// injectCachePoints adds prompt-cache breakpoints to the system prompt and
// the end of the conversation when they are large enough to cache. At most
// two points are used (Bedrock allows four).
func injectCachePoints(br *bedrockRequest) {
	sysLen := 0
	for _, c := range br.System {
		sysLen += len(c.Text)
	}
	if sysLen >= minCacheableChars {
		br.System = append(br.System, bedrockContent{CachePoint: &bedrockCachePoint{Type: "default"}})
	}

	msgLen := 0
	for _, m := range br.Messages {
		for _, c := range m.Content {
			msgLen += len(c.Text)
		}
	}
	if msgLen >= minCacheableChars && len(br.Messages) > 0 {
		last := &br.Messages[len(br.Messages)-1]
		last.Content = append(last.Content, bedrockContent{CachePoint: &bedrockCachePoint{Type: "default"}})
	}
}

// convertToolChoice converts OpenAI tool_choice to Bedrock toolChoice format.
func convertToolChoice(tc interface{}) *bedrockToolChoice {
	if tc == nil {
		return nil
	}
	switch v := tc.(type) {
	case string:
		switch v {
		case "auto":
			return &bedrockToolChoice{Auto: &struct{}{}} //nolint:govet
		case "required", "any":
			return &bedrockToolChoice{Any: &struct{}{}} //nolint:govet
		case "none":
			return nil
		}
	case map[string]interface{}:
		if t, ok := v["type"].(string); ok && t == "function" {
			if name, ok := v["function"].(map[string]interface{}); ok {
				if n, ok := name["name"].(string); ok {
					return &bedrockToolChoice{Tool: &bedrockToolName{Name: n}}
				}
			}
		}
	}
	return nil
}

// bedrockResponse represents a Bedrock Converse response
type bedrockResponse struct {
	Output     bedrockOutput  `json:"output"`
	Usage      bedrockUsage   `json:"usage"`
	StopReason string         `json:"stopReason"`
	Metrics    bedrockMetrics `json:"metrics"`
}

type bedrockOutput struct {
	Message bedrockMessage `json:"message"`
}

type bedrockUsage struct {
	InputTokens           int `json:"inputTokens"`
	OutputTokens          int `json:"outputTokens"`
	TotalTokens           int `json:"totalTokens"`
	CacheReadInputTokens  int `json:"cacheReadInputTokens"`
	CacheWriteInputTokens int `json:"cacheWriteInputTokens"`
}

// promptTokensForBilling returns the input token count to report downstream.
// Bedrock reports cache-read tokens separately from inputTokens, but OpenAI
// semantics (which our billing follows) count them inside prompt_tokens and
// break the cached portion out via prompt_tokens_details.cached_tokens.
// Cache-write tokens stay in inputTokens (billed at input price).
func promptTokensForBilling(u bedrockUsage) int {
	return u.InputTokens + u.CacheReadInputTokens
}

type bedrockMetrics struct {
	LatencyMs int `json:"latencyMs"`
}

// ParseResponse - Parse upstream response body into native struct
func (a *AWSAdaptor) ParseResponse(body []byte) (interface{}, error) {
	var br bedrockResponse
	if err := json.Unmarshal(body, &br); err != nil {
		return nil, err
	}
	return &br, nil
}

// FromProvider - Convert Bedrock response to OpenAI format
func (a *AWSAdaptor) FromProvider(response interface{}, req dto.GeneralOpenAIRequest) *dto.OpenAITextResponse {
	var br *bedrockResponse
	switch v := response.(type) {
	case *bedrockResponse:
		br = v
	case bedrockResponse:
		br = &v
	case []byte:
		br = &bedrockResponse{}
		if err := json.Unmarshal(v, br); err != nil {
			return nil
		}
	default:
		return nil
	}

	// Extract text content and tool_use blocks
	var content strings.Builder
	var toolCalls []dto.ToolCall
	toolIndex := 0
	for _, c := range br.Output.Message.Content {
		if c.Text != "" {
			content.WriteString(c.Text)
		}
		if c.ToolUse != nil {
			args := string(c.ToolUse.Input)
			if args == "" {
				args = "{}"
			}
			toolCalls = append(toolCalls, dto.ToolCall{
				Index: toolIndex,
				ID:    c.ToolUse.ToolUseID,
				Type:  "function",
				Function: dto.FunctionCall{
					Name:      c.ToolUse.Name,
					Arguments: args,
				},
			})
			toolIndex++
		}
	}

	// Map stop reason
	finishReason := "stop"
	switch br.StopReason {
	case "max_tokens":
		finishReason = "length"
	case "stop_sequence", "end_turn":
		finishReason = "stop"
	case "tool_use":
		finishReason = "tool_calls"
	}

	msg := dto.Message{
		Role:    "assistant",
		Content: content.String(),
	}
	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
	}

	return &dto.OpenAITextResponse{
		// Bedrock Converse responses carry no request ID or model name, so
		// generate the ID here; Model is filled by the chat handler from the
		// client-facing logical model.
		ID:      "chatcmpl-" + uuid.NewString(),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Choices: []dto.OpenAITextResponseChoice{
			{
				Index:        0,
				Message:      msg,
				FinishReason: finishReason,
			},
		},
		Usage: dto.Usage{
			PromptTokens:     promptTokensForBilling(br.Usage),
			CompletionTokens: br.Usage.OutputTokens,
			TotalTokens:      promptTokensForBilling(br.Usage) + br.Usage.OutputTokens,
			PromptTokensDetails: dto.InputTokenDetails{
				CachedTokens:         br.Usage.CacheReadInputTokens,
				CachedCreationTokens: br.Usage.CacheWriteInputTokens,
			},
			PromptCacheHitTokens:        br.Usage.CacheReadInputTokens,
			ClaudeCacheCreation5mTokens: br.Usage.CacheWriteInputTokens,
		},
	}
}

// ExtractUsage - Extract usage from Bedrock response
func (a *AWSAdaptor) ExtractUsage(response interface{}) *dto.Usage {
	var br *bedrockResponse
	switch v := response.(type) {
	case *bedrockResponse:
		br = v
	case bedrockResponse:
		br = &v
	case []byte:
		br = &bedrockResponse{}
		if err := json.Unmarshal(v, br); err != nil {
			return &dto.Usage{}
		}
	default:
		return &dto.Usage{}
	}
	return &dto.Usage{
		PromptTokens:     promptTokensForBilling(br.Usage),
		CompletionTokens: br.Usage.OutputTokens,
		TotalTokens:      promptTokensForBilling(br.Usage) + br.Usage.OutputTokens,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         br.Usage.CacheReadInputTokens,
			CachedCreationTokens: br.Usage.CacheWriteInputTokens,
		},
		PromptCacheHitTokens:        br.Usage.CacheReadInputTokens,
		ClaudeCacheCreation5mTokens: br.Usage.CacheWriteInputTokens,
	}
}

// TransformStreamChunk - Transform Bedrock ConverseStream event to OpenAI format.
// Bedrock ConverseStream returns Amazon EventStream chunks. Each chunk contains
// a JSON payload with event types like contentBlockDelta, messageStart, messageStop.
func (a *AWSAdaptor) TransformStreamChunk(chunk interface{}, index int) interface{} {
	data, ok := chunk.([]byte)
	if !ok {
		return nil
	}

	// Try to extract JSON payload from the event stream chunk.
	// Amazon EventStream format: [length][headers-length][headers][crc][payload][crc]
	// We scan for JSON objects starting with '{'.
	payload := extractJSONPayload(data)
	if payload == nil {
		return nil
	}

	var event bedrockStreamEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		// Try raw payload without event wrapper
		var raw map[string]interface{}
		if err := json.Unmarshal(payload, &raw); err != nil {
			return nil
		}
		// Check for contentBlockDelta directly
		if delta, ok := raw["contentBlockDelta"].(map[string]interface{}); ok {
			if d, ok := delta["delta"].(map[string]interface{}); ok {
				if text, ok := d["text"].(string); ok {
					return buildStreamResponse(text, "", nil)
				}
			}
		}
		// Check for messageStop with stop reason
		if stop, ok := raw["messageStop"].(map[string]interface{}); ok {
			if reason, ok := stop["stopReason"].(string); ok {
				fr := mapBedrockStopReason(reason)
				return buildStreamResponse("", "", &fr)
			}
		}
		return nil
	}

	// Handle contentBlockDelta
	if event.ContentBlockDelta != nil {
		if event.ContentBlockDelta.Delta.Text != "" {
			return buildStreamResponse(event.ContentBlockDelta.Delta.Text, "", nil)
		}
	}

	// Handle messageStop
	if event.MessageStop != nil && event.MessageStop.StopReason != "" {
		fr := mapBedrockStopReason(event.MessageStop.StopReason)
		return buildStreamResponse("", "", &fr)
	}

	// Handle messageStart for model ID
	if event.MessageStart != nil && event.MessageStart.Role != "" {
		return buildStreamResponse("", "", nil)
	}

	return nil
}

// bedrockStreamEvent represents a Bedrock ConverseStream event
type bedrockStreamEvent struct {
	MessageStart      *bedrockMessageStart      `json:"messageStart"`
	ContentBlockDelta *bedrockContentBlockDelta `json:"contentBlockDelta"`
	MessageStop       *bedrockMessageStop       `json:"messageStop"`
	Metadata          *bedrockMetadata          `json:"metadata"`
}

type bedrockMessageStart struct {
	Role string `json:"role"`
}

type bedrockContentBlockDelta struct {
	Delta             bedrockDelta `json:"delta"`
	ContentBlockIndex int          `json:"contentBlockIndex"`
}

type bedrockDelta struct {
	Text    string               `json:"text"`
	ToolUse *bedrockDeltaToolUse `json:"toolUse,omitempty"`
}

type bedrockDeltaToolUse struct {
	Input string `json:"input"`
}

// bedrockContentBlockStart is the payload for contentBlockStart events.
type bedrockContentBlockStart struct {
	ContentBlockIndex int                             `json:"contentBlockIndex"`
	Start             *bedrockContentBlockStartDetail `json:"start,omitempty"`
}

type bedrockContentBlockStartDetail struct {
	ToolUse *bedrockToolUseStart `json:"toolUse,omitempty"`
}

type bedrockToolUseStart struct {
	ToolUseID string `json:"toolUseId"`
	Name      string `json:"name"`
}

type bedrockMessageStop struct {
	StopReason string `json:"stopReason"`
}

type bedrockMetadata struct {
	Usage bedrockUsage `json:"usage"`
}

// extractJSONPayload extracts the JSON payload from an Amazon EventStream message.
// EventStream format (per AWS spec):
//
//	[4 bytes: total length (big-endian)][4 bytes: headers length (big-endian)]
//	[4 bytes: prelude CRC32][headers bytes][payload bytes][4 bytes: message CRC32]
func extractJSONPayload(data []byte) []byte {
	// Minimum size: 4+4+4+0+0+4 = 16 bytes
	if len(data) < 16 {
		return nil
	}

	totalLen := binary.BigEndian.Uint32(data[0:4])
	headersLen := binary.BigEndian.Uint32(data[4:8])

	// Sanity checks
	if int(totalLen) > len(data) || headersLen+16 > totalLen {
		// Not a valid EventStream frame — fall back to legacy brace scan
		return extractJSONPayloadLegacy(data)
	}

	payloadLen := int(totalLen) - int(headersLen) - 16
	payloadStart := 12 + int(headersLen)
	if payloadStart < 0 || payloadStart+payloadLen > len(data) {
		return extractJSONPayloadLegacy(data)
	}

	return data[payloadStart : payloadStart+payloadLen]
}

// extractJSONPayloadLegacy is the old brute-force scanner used as a fallback
// when data does not conform to EventStream framing.
func extractJSONPayloadLegacy(data []byte) []byte {
	start := -1
	depth := 0
	for i, b := range data {
		switch b {
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			depth--
			if depth == 0 && start >= 0 {
				return data[start : i+1]
			}
		}
	}
	return nil
}

// mapBedrockStopReason maps Bedrock stop reasons to OpenAI finish reasons
func mapBedrockStopReason(reason string) string {
	switch reason {
	case "max_tokens":
		return "length"
	case "stop_sequence", "end_turn":
		return "stop"
	case "tool_use":
		return "tool_calls"
	case "content_filtered":
		return "content_filter"
	default:
		return "stop"
	}
}

// buildStreamResponse builds an OpenAI-compatible stream response
func buildStreamResponse(content, model string, finishReason *string) *dto.ChatCompletionsStreamResponse {
	resp := &dto.ChatCompletionsStreamResponse{
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: make([]dto.ChatCompletionsStreamResponseChoice, 0),
	}

	choice := dto.ChatCompletionsStreamResponseChoice{
		Index: 0,
		Delta: dto.ChatCompletionsStreamResponseChoiceDelta{},
	}

	if content != "" {
		choice.Delta.Content = &content
	}
	if finishReason != nil {
		choice.FinishReason = finishReason
	}

	resp.Choices = append(resp.Choices, choice)
	return resp
}

// DoRequest - Make HTTP request with AWS SigV4 signing
func (a *AWSAdaptor) DoRequest(body interface{}, stream bool) (*http.Response, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	return a.DoRequestBytes(jsonBody, stream)
}

// DoRequestBytes - Make HTTP request with AWS SigV4 signing using pre-serialized body.
func (a *AWSAdaptor) DoRequestBytes(body []byte, stream bool) (*http.Response, error) {
	return a.DoRequestBytesWithContext(context.Background(), body, stream)
}

// DoRequestBytesWithContext - Make HTTP request with AWS SigV4 signing using
// pre-serialized body and a caller-supplied context.
func (a *AWSAdaptor) DoRequestBytesWithContext(ctx context.Context, body []byte, stream bool) (*http.Response, error) {
	url := a.GetRequestURL(a.model, stream)

	resp, err := a.sendSigned(ctx, url, body)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	// Bedrock rejects maxTokens above the model's output limit with a 400
	// whose message names the limit ("...exceeds the model limit of N").
	// Clamp to the advertised limit and retry once instead of failing the
	// request — clients (e.g. coding agents) routinely ask for more than a
	// given model can emit.
	if resp.StatusCode == http.StatusBadRequest {
		if clamped, limit, ok := clampMaxTokensBody(body, resp); ok {
			log.Printf("[WARN] aws: maxTokens exceeds model limit, clamping to %d and retrying once (model=%s)", limit, a.model)
			resp, err = a.sendSigned(ctx, url, clamped)
			if err != nil {
				return nil, fmt.Errorf("request failed: %w", err)
			}
		}
	}

	// ConverseStream returns binary Amazon EventStream frames, but callers
	// downstream expect an OpenAI-style text/event-stream body. Convert the
	// wire format here so the rest of the pipeline can treat Bedrock like any
	// other OpenAI-compatible upstream.
	if stream && resp.StatusCode == http.StatusOK {
		log.Printf("[DEBUG] aws stream resp: status=%d ct=%q url=%s", resp.StatusCode, resp.Header.Get("Content-Type"), url)
		resp.Body = newConverseStreamConverter(resp.Body, a.model)
		resp.Header.Set("Content-Type", "text/event-stream")
		resp.Header.Del("Content-Length")
	}

	return resp, nil
}

// sendSigned builds a POST to url with the given JSON body and executes it
// with the shared HTTP client. When the adaptor holds a Bedrock API Key it
// authenticates with an "Authorization: Bearer" header (no SigV4); otherwise
// it applies SigV4 signing with the AK/SK credentials.
func (a *AWSAdaptor) sendSigned(ctx context.Context, url string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set content-type header
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Host", req.URL.Host)

	switch {
	case a.bearerToken != "":
		// Bedrock API Key: bearer-token auth, no SigV4. AWS accepts this on
		// bedrock-runtime InvokeModel/Converse (CallWithBearerToken).
		req.Header.Set("Authorization", "Bearer "+a.bearerToken)
	case a.accessKey != "" && a.secretKey != "":
		SignRequest(req, body, a.accessKey, a.secretKey, a.sessionToken, a.region, "bedrock")
	default:
		log.Printf("[WARN] AWS Bedrock request has no usable credentials (no bearer token and incomplete AK/SK); it will likely fail with 401")
	}

	return base.SharedHTTPClient().Do(req)
}

// bedrockMaxTokensRe matches Bedrock's 400 message when maxTokens exceeds
// the model's output limit, capturing the advertised limit.
var bedrockMaxTokensRe = regexp.MustCompile(`model limit of (\d+)`)

// clampMaxTokensBody inspects a 400 response for Bedrock's maxTokens-limit
// error and, if found, returns a copy of the request body with maxTokens
// (Converse: inferenceConfig.maxTokens; anthropic-style: max_tokens) capped
// at the advertised limit. The response body is always restored so callers
// can read it when no clamp applies.
func clampMaxTokensBody(reqBody []byte, resp *http.Response) ([]byte, int, bool) {
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(respBody))

	m := bedrockMaxTokensRe.FindSubmatch(respBody)
	if m == nil {
		return nil, 0, false
	}
	limit, err := strconv.Atoi(string(m[1]))
	if err != nil || limit <= 0 {
		return nil, 0, false
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(reqBody, &payload); err != nil {
		return nil, 0, false
	}
	changed := false
	if ic, ok := payload["inferenceConfig"].(map[string]interface{}); ok {
		if mt, ok := ic["maxTokens"].(float64); ok && int(mt) > limit {
			ic["maxTokens"] = limit
			changed = true
		}
	}
	if mt, ok := payload["max_tokens"].(float64); ok && int(mt) > limit {
		payload["max_tokens"] = limit
		changed = true
	}
	if !changed {
		return nil, 0, false
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, false
	}
	return out, limit, true
}

// converseStreamConverter reads Bedrock ConverseStream (binary Amazon
// EventStream) frames from src and exposes them as an OpenAI-compatible
// text/event-stream body. Frames are decoded one at a time with io.ReadFull,
// so TCP chunking and frame boundaries never matter.
type converseStreamConverter struct {
	src   io.ReadCloser
	model string
	id    string
	buf   bytes.Buffer
	done  bool
	// Tool call streaming state
	toolCalls []streamingToolCall
}

type streamingToolCall struct {
	index int
	id    string
	name  string
}

// oaiStreamToolCall is a stream-specific tool_call delta shape. Unlike
// dto.ToolCall it always serializes "index" (0 is meaningful and clients
// group deltas by it) and omits empty id/type/name fields, matching the
// OpenAI SSE wire format.
type oaiStreamToolCall struct {
	Index    int            `json:"index"`
	ID       string         `json:"id,omitempty"`
	Type     string         `json:"type,omitempty"`
	Function *oaiStreamFunc `json:"function,omitempty"`
}

type oaiStreamFunc struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

func newConverseStreamConverter(src io.ReadCloser, model string) *converseStreamConverter {
	return &converseStreamConverter{
		src:   src,
		model: model,
		id:    "chatcmpl-" + uuid.NewString(),
	}
}

func (c *converseStreamConverter) Read(p []byte) (int, error) {
	for c.buf.Len() == 0 && !c.done {
		if err := c.convertNextFrame(); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				c.buf.WriteString("data: [DONE]\n\n")
				c.done = true
				break
			}
			_ = c.src.Close()
			return 0, err
		}
	}
	if c.buf.Len() == 0 && c.done {
		return 0, io.EOF
	}
	return c.buf.Read(p)
}

func (c *converseStreamConverter) Close() error {
	return c.src.Close()
}

// convertNextFrame decodes one EventStream frame and appends its SSE
// representation to c.buf. Some frames (e.g. messageStart) produce no output.
// Frame layout: [4 totalLen][4 headersLen][4 preludeCRC][headers][payload][4 msgCRC].
//
// On the wire, the event type is carried in the `:event-type` header and the
// payload is the bare event body (e.g. {"delta":{"text":"..."}} for a
// contentBlockDelta frame) — NOT wrapped in a {"contentBlockDelta":{...}}
// envelope. Payloads may also contain a "p" padding field.
func (c *converseStreamConverter) convertNextFrame() error {
	prelude := make([]byte, 8)
	if _, err := io.ReadFull(c.src, prelude); err != nil {
		return err
	}
	totalLen := binary.BigEndian.Uint32(prelude[0:4])
	headersLen := binary.BigEndian.Uint32(prelude[4:8])
	if totalLen < 16 || headersLen+16 > totalLen || totalLen > 16*1024*1024 {
		return fmt.Errorf("invalid eventstream frame header: totalLen=%d headersLen=%d", totalLen, headersLen)
	}
	rest := make([]byte, totalLen-8)
	if _, err := io.ReadFull(c.src, rest); err != nil {
		return err
	}
	// rest = [4 preludeCRC][headers][payload][4 msgCRC]
	payloadEnd := len(rest) - 4
	payloadStart := 4 + int(headersLen)
	if payloadStart > payloadEnd {
		return fmt.Errorf("invalid eventstream frame: headers overrun")
	}
	headers := rest[4:payloadStart]
	payload := rest[payloadStart:payloadEnd]

	eventType := parseEventStreamEventType(headers)

	switch eventType {
	case "messageStart":
		// No content; the response id/model are already set by the converter.
		return nil
	case "contentBlockStart":
		// Parse to check for tool_use block start
		var ev bedrockContentBlockStart
		if err := json.Unmarshal(payload, &ev); err == nil && ev.Start != nil && ev.Start.ToolUse != nil {
			tc := streamingToolCall{
				// OpenAI tool_calls index is the 0-based ordinal among tool
				// calls, NOT the Bedrock contentBlockIndex (which also counts
				// text blocks).
				index: len(c.toolCalls),
				id:    ev.Start.ToolUse.ToolUseID,
				name:  ev.Start.ToolUse.Name,
			}
			c.toolCalls = append(c.toolCalls, tc)
			// Emit initial tool_calls chunk with id and function name
			c.emitToolCallStart(tc)
		}
		return nil
	case "contentBlockStop":
		return nil
	case "contentBlockDelta":
		var ev bedrockContentBlockDelta
		if err := json.Unmarshal(payload, &ev); err != nil {
			log.Printf("[WARN] aws: undecodable contentBlockDelta frame (%d bytes)", len(payload))
			return nil
		}
		if ev.Delta.Text != "" {
			c.emitChunk(buildStreamResponse(ev.Delta.Text, c.model, nil))
		}
		if ev.Delta.ToolUse != nil && ev.Delta.ToolUse.Input != "" {
			// Emit tool call arguments delta
			toolIndex := len(c.toolCalls) - 1
			if toolIndex < 0 {
				toolIndex = 0
			}
			c.emitToolCallDelta(toolIndex, ev.Delta.ToolUse.Input)
		}
		return nil
	case "messageStop":
		var ev bedrockMessageStop
		if err := json.Unmarshal(payload, &ev); err != nil {
			log.Printf("[WARN] aws: undecodable messageStop frame (%d bytes)", len(payload))
			return nil
		}
		fr := mapBedrockStopReason(ev.StopReason)
		c.emitChunk(buildStreamResponse("", c.model, &fr))
		return nil
	case "metadata":
		var ev bedrockMetadata
		if err := json.Unmarshal(payload, &ev); err != nil {
			log.Printf("[WARN] aws: undecodable metadata frame (%d bytes)", len(payload))
			return nil
		}
		c.emitUsage(&ev.Usage)
		return nil
	}

	// Unknown frame type — likely an exception frame. Surface its message as
	// an SSE error event so the client can react instead of hanging.
	var errPayload struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(payload, &errPayload) == nil && errPayload.Message != "" {
		errJSON, _ := json.Marshal(gin_error(errPayload.Message))
		c.buf.WriteString("data: " + string(errJSON) + "\n\n")
		return nil
	}
	log.Printf("[WARN] aws: skipping eventstream frame type=%q (%d bytes)", eventType, len(payload))
	return nil
}

// parseEventStreamEventType extracts the `:event-type` header value from an
// EventStream frame's headers block. Header encoding per AWS spec:
// [1B nameLen][name][1B valueType][value], where string values (type 7) are
// [2B big-endian len][bytes].
func parseEventStreamEventType(headers []byte) string {
	i := 0
	for i < len(headers) {
		nameLen := int(headers[i])
		i++
		if i+nameLen+1 > len(headers) {
			return ""
		}
		name := string(headers[i : i+nameLen])
		i += nameLen
		valueType := headers[i]
		i++
		var valueLen int
		switch valueType {
		case 0, 1: // bool true/false
			valueLen = 0
		case 2: // byte
			valueLen = 1
		case 3: // short
			valueLen = 2
		case 4: // int
			valueLen = 4
		case 5: // long
			valueLen = 8
		case 6: // bytes
			if i+2 > len(headers) {
				return ""
			}
			valueLen = 2 + int(binary.BigEndian.Uint16(headers[i:i+2]))
		case 7: // string
			if i+2 > len(headers) {
				return ""
			}
			n := int(binary.BigEndian.Uint16(headers[i : i+2]))
			if name == ":event-type" {
				if i+2+n > len(headers) {
					return ""
				}
				return string(headers[i+2 : i+2+n])
			}
			valueLen = 2 + n
		case 8: // timestamp
			valueLen = 8
		case 9: // uuid
			valueLen = 16
		default:
			return ""
		}
		i += valueLen
	}
	return ""
}

func (c *converseStreamConverter) emitChunk(resp *dto.ChatCompletionsStreamResponse) {
	resp.ID = c.id
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	c.buf.WriteString("data: " + string(data) + "\n\n")
}

func (c *converseStreamConverter) emitUsage(u *bedrockUsage) {
	resp := &dto.ChatCompletionsStreamResponse{
		ID:      c.id,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   c.model,
		Choices: []dto.ChatCompletionsStreamResponseChoice{},
		Usage: &dto.Usage{
			PromptTokens:     promptTokensForBilling(*u),
			CompletionTokens: u.OutputTokens,
			TotalTokens:      promptTokensForBilling(*u) + u.OutputTokens,
			PromptTokensDetails: dto.InputTokenDetails{
				CachedTokens:         u.CacheReadInputTokens,
				CachedCreationTokens: u.CacheWriteInputTokens,
			},
			PromptCacheHitTokens:        u.CacheReadInputTokens,
			ClaudeCacheCreation5mTokens: u.CacheWriteInputTokens,
		},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	c.buf.WriteString("data: " + string(data) + "\n\n")
}

func (c *converseStreamConverter) emitToolCallStart(tc streamingToolCall) {
	c.emitToolCallChunk(oaiStreamToolCall{
		Index: tc.index,
		ID:    tc.id,
		Type:  "function",
		Function: &oaiStreamFunc{
			Name: tc.name,
		},
	})
}

func (c *converseStreamConverter) emitToolCallDelta(toolIndex int, args string) {
	c.emitToolCallChunk(oaiStreamToolCall{
		Index: toolIndex,
		Function: &oaiStreamFunc{
			Arguments: args,
		},
	})
}

func (c *converseStreamConverter) emitToolCallChunk(tc oaiStreamToolCall) {
	chunk := map[string]interface{}{
		"id": c.id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": c.model,
		"choices": []map[string]interface{}{
			{"index": 0, "delta": map[string]interface{}{"tool_calls": []oaiStreamToolCall{tc}}, "finish_reason": nil},
		},
	}
	data, err := json.Marshal(chunk)
	if err != nil {
		return
	}
	c.buf.WriteString("data: " + string(data) + "\n\n")
}

// gin_error builds an OpenAI-style error envelope for mid-stream failures.
func gin_error(message string) map[string]interface{} {
	return map[string]interface{}{
		"error": map[string]interface{}{
			"message": message,
			"type":    "upstream_error",
		},
	}
}

// ReadResponse - Read response body
func (a *AWSAdaptor) ReadResponse(resp *http.Response) ([]byte, error) {
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}
	return body, nil
}

// ---------------------------------------------------------------------------
// AWS SigV4 signing (lightweight implementation)
// ---------------------------------------------------------------------------

func SignRequest(req *http.Request, body []byte, accessKey, secretKey, sessionToken, region, service string) {
	now := time.Now().UTC()
	dateStamp := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")

	// 1. Create canonical request
	payloadHash := sha256Hex(body)
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	if sessionToken != "" {
		req.Header.Set("x-amz-security-token", sessionToken)
	}

	canonicalHeaders, signedHeaders := buildCanonicalHeaders(req)
	// AWS SigV4 canonicalisation requires ':' in the path to be
	// percent-encoded as %3A, even though ':' is a valid URL path
	// character per RFC 3986.  We encode it here *only* for the
	// signature computation; the wire path keeps the raw ':'.
	canonicalPath := strings.ReplaceAll(req.URL.EscapedPath(), ":", "%3A")
	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalPath,
		req.URL.RawQuery,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	// 2. Create string to sign
	credentialScope := dateStamp + "/" + region + "/" + service + "/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		credentialScope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	// 3. Calculate signature
	signingKey := getSignatureKey(secretKey, dateStamp, region, service)
	signature := hmacHex(signingKey, []byte(stringToSign))

	// 4. Build Authorization header
	authHeader := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKey, credentialScope, signedHeaders, signature)
	req.Header.Set("Authorization", authHeader)
}

func buildCanonicalHeaders(req *http.Request) (canonical, signed string) {
	// Required headers for Bedrock
	headers := []string{"host", "x-amz-content-sha256", "x-amz-date"}
	if req.Header.Get("Content-Type") != "" {
		headers = append([]string{"content-type"}, headers...)
	}
	if req.Header.Get("x-amz-security-token") != "" {
		headers = append(headers, "x-amz-security-token")
	}

	var cb strings.Builder
	for _, h := range headers {
		v := strings.TrimSpace(req.Header.Get(h))
		if v == "" && h == "host" {
			v = req.URL.Host
		}
		cb.WriteString(strings.ToLower(h))
		cb.WriteString(":")
		cb.WriteString(v)
		cb.WriteString("\n")
	}

	return cb.String(), strings.Join(headers, ";")
}

func getSignatureKey(secretKey, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secretKey), []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))
	return kSigning
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func hmacHex(key, data []byte) string {
	return hex.EncodeToString(hmacSHA256(key, data))
}

func sha256Hex(data []byte) string {
	h := sha256.New()
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

package convert

import (
	"encoding/json"

	"github.com/mayiants/ai-proxy-go/dto"
)

// ============================================================================
// Responses API DTOs
// ============================================================================

// ResponsesRequest represents OpenAI Responses API request format.
type ResponsesRequest struct {
	Model              string              `json:"model"`
	Input              interface{}         `json:"input"` // string or []ResponsesInputItem
	Instructions       string              `json:"instructions,omitempty"`
	PreviousResponseID string              `json:"previous_response_id,omitempty"`
	Store              *bool               `json:"store,omitempty"`
	Stream             *bool               `json:"stream,omitempty"`
	MaxOutputTokens    *int                `json:"max_output_tokens,omitempty"`
	Temperature        *float64            `json:"temperature,omitempty"`
	TopP               *float64            `json:"top_p,omitempty"`
	Tools              []ResponsesTool     `json:"tools,omitempty"`
	ToolChoice         interface{}         `json:"tool_choice,omitempty"`
	Reasoning          *ResponsesReasoning `json:"reasoning,omitempty"`
	ParallelToolCalls  *bool               `json:"parallel_tool_calls,omitempty"`
	User               string              `json:"user,omitempty"`
	Truncation         string              `json:"truncation,omitempty"`
}

// ResponsesInputItem represents a single item in the input array.
type ResponsesInputItem struct {
	Type      string      `json:"type"`
	Role      string      `json:"role,omitempty"`
	Content   interface{} `json:"content,omitempty"` // string or []ResponsesContentPart
	Name      string      `json:"name,omitempty"`
	Arguments string      `json:"arguments,omitempty"`
	CallID    string      `json:"call_id,omitempty"`
	ID        string      `json:"id,omitempty"`
	Status    string      `json:"status,omitempty"`
	Output    string      `json:"output,omitempty"`
}

// ResponsesContentPart represents a content part within an input item.
type ResponsesContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// ResponsesTool represents a tool definition in Responses format.
type ResponsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// ResponsesReasoning represents reasoning configuration.
type ResponsesReasoning struct {
	Effort string `json:"effort,omitempty"`
}

// ResponsesResponse represents OpenAI Responses API response format.
type ResponsesResponse struct {
	ID                 string                      `json:"id"`
	Object             string                      `json:"object"`
	CreatedAt          int64                       `json:"created_at"`
	Model              string                      `json:"model"`
	Status             string                      `json:"status"`
	Output             []ResponsesOutputItem       `json:"output"`
	Usage              ResponsesUsage              `json:"usage"`
	Error              *ResponsesError             `json:"error,omitempty"`
	IncompleteDetails  *ResponsesIncompleteDetails `json:"incomplete_details,omitempty"`
	PreviousResponseID string                      `json:"previous_response_id,omitempty"`
}

// ResponsesOutputItem represents an output item in Responses format.
type ResponsesOutputItem struct {
	Type      string                   `json:"type"`
	ID        string                   `json:"id,omitempty"`
	Status    string                   `json:"status,omitempty"`
	Role      string                   `json:"role,omitempty"`
	Content   []ResponsesOutputContent `json:"content,omitempty"`
	Name      string                   `json:"name,omitempty"`
	Arguments string                   `json:"arguments,omitempty"`
	CallID    string                   `json:"call_id,omitempty"`
}

// ResponsesOutputContent represents content within an output item.
type ResponsesOutputContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// ResponsesUsage represents usage in Responses format.
type ResponsesUsage struct {
	InputTokens        int                        `json:"input_tokens"`
	OutputTokens       int                        `json:"output_tokens"`
	TotalTokens        int                        `json:"total_tokens"`
	InputTokensDetails ResponsesInputTokenDetails `json:"input_tokens_details,omitempty"`
}

// ResponsesInputTokenDetails represents detailed input token usage.
type ResponsesInputTokenDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
}

// ResponsesError represents an error in Responses format.
type ResponsesError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
}

// ResponsesIncompleteDetails represents incomplete response details.
type ResponsesIncompleteDetails struct {
	Reason string `json:"reason"`
}

// ============================================================================
// Responses → OpenAI Chat Conversion
// ============================================================================

// ResponsesToOpenAI converts a Responses API request to OpenAI Chat Completions format.
func ResponsesToOpenAI(req *ResponsesRequest) (*dto.GeneralOpenAIRequest, error) {
	hubReq := &dto.GeneralOpenAIRequest{
		Model: req.Model,
	}

	// Map basic parameters
	if req.MaxOutputTokens != nil {
		hubReq.MaxTokens = req.MaxOutputTokens
	}
	if req.Temperature != nil {
		hubReq.Temperature = req.Temperature
	}
	if req.TopP != nil {
		hubReq.TopP = req.TopP
	}
	if req.Stream != nil {
		hubReq.Stream = req.Stream
	}
	if req.User != "" {
		hubReq.User = req.User
	}

	// Convert reasoning.effort → reasoning_effort
	if req.Reasoning != nil && req.Reasoning.Effort != "" {
		hubReq.ReasoningEffort = req.Reasoning.Effort
	}

	// Convert instructions → system message
	if req.Instructions != "" {
		hubReq.Messages = append(hubReq.Messages, dto.Message{
			Role:    "system",
			Content: req.Instructions,
		})
	}

	// Convert input → messages
	switch v := req.Input.(type) {
	case string:
		if v != "" {
			hubReq.Messages = append(hubReq.Messages, dto.Message{
				Role:    "user",
				Content: v,
			})
		}
	case []interface{}:
		for _, item := range v {
			itemBytes, _ := json.Marshal(item)
			var inputItem ResponsesInputItem
			if err := json.Unmarshal(itemBytes, &inputItem); err != nil {
				continue
			}
			msg := responsesInputItemToMessage(inputItem)
			if msg != nil {
				hubReq.Messages = append(hubReq.Messages, *msg)
			}
		}
	}

	// Chat-completions upstreams require assistant tool_calls to be followed
	// by tool messages. Responses histories (codex CLI) order a turn's items
	// as function_call → message(text) → function_call_output, which converts
	// to assistant(tool_calls), assistant(text), tool(...) — the intervening
	// text message makes the sequence invalid and upstreams reject the whole
	// request. Fold a trailing content-only assistant message into the
	// preceding assistant message that carries tool_calls.
	for i := 0; i+1 < len(hubReq.Messages); i++ {
		cur, next := &hubReq.Messages[i], &hubReq.Messages[i+1]
		if cur.Role == "assistant" && len(cur.ToolCalls) > 0 &&
			next.Role == "assistant" && len(next.ToolCalls) == 0 {
			if next.Content != nil {
				cur.Content = next.Content
			}
			hubReq.Messages = append(hubReq.Messages[:i+1], hubReq.Messages[i+2:]...)
			i-- // re-check the pair at this index
		}
	}

	// Convert tools. Responses allows non-function tool types (web_search,
	// custom, local_shell, ...) which chat-completions upstreams reject
	// ("the type of the tool can only be function"). Only function-type
	// tools with a name survive the conversion — codex CLI sends a
	// web_search tool that previously became an empty-name function and
	// got the whole request rejected upstream (2026-10-24).
	for _, tool := range req.Tools {
		if tool.Type != "" && tool.Type != "function" {
			continue
		}
		if tool.Name == "" {
			continue
		}
		hubReq.Tools = append(hubReq.Tools, dto.ToolCallRequest{
			Type: "function",
			Function: dto.FunctionRequest{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  map[string]interface{}{},
			},
		})
		if tool.Parameters != nil {
			var params map[string]interface{}
			if err := json.Unmarshal(tool.Parameters, &params); err == nil {
				// Strip unsupported format fields from schema
				StripFormatFromSchema(params)
				hubReq.Tools[len(hubReq.Tools)-1].Function.Parameters = params
			}
		}
	}

	// Convert tool_choice
	if req.ToolChoice != nil {
		hubReq.ToolChoice = req.ToolChoice
	}

	return hubReq, nil
}

// responsesInputItemToMessage converts a Responses input item to an OpenAI message.
func responsesInputItemToMessage(item ResponsesInputItem) *dto.Message {
	switch item.Type {
	case "message":
		msg := &dto.Message{Role: item.Role}
		switch content := item.Content.(type) {
		case string:
			msg.Content = content
		case []interface{}:
			parts := make([]dto.MediaContent, 0)
			for _, p := range content {
				pBytes, _ := json.Marshal(p)
				var part ResponsesContentPart
				if err := json.Unmarshal(pBytes, &part); err != nil {
					continue
				}
				if part.Type == "text" && part.Text != "" {
					parts = append(parts, dto.MediaContent{Type: "text", Text: part.Text})
				}
				if (part.Type == "input_text" || part.Type == "output_text") && part.Text != "" {
					parts = append(parts, dto.MediaContent{Type: "text", Text: part.Text})
				}
				// image_url handling: input_image
				if part.Type == "input_image" { //nolint:staticcheck // SA9003: placeholder for future image part conversion
					// Responses image parts may have a URL or base64 data
					// For now, skip unsupported image parts
				}
			}
			if len(parts) > 0 {
				msg.Content = parts
			}
		}
		return msg

	case "function_call":
		// This represents a previous assistant tool_use turn
		msg := &dto.Message{
			Role: "assistant",
		}
		if item.Name != "" && item.CallID != "" {
			msg.ToolCalls = []dto.ToolCall{
				{
					ID:   item.CallID,
					Type: "function",
					Function: dto.FunctionCall{
						Name:      item.Name,
						Arguments: item.Arguments,
					},
				},
			}
		}
		return msg

	case "function_call_output":
		// This represents a tool result
		msg := &dto.Message{
			Role:       "tool",
			ToolCallID: item.CallID,
			Content:    item.Output,
		}
		return msg

	default:
		return nil
	}
}

// ============================================================================
// OpenAI Chat → Responses Conversion
// ============================================================================

// OpenAIToResponses converts an OpenAI Chat Completions response to Responses API format.
func OpenAIToResponses(openaiResp *dto.OpenAITextResponse, model string) (*ResponsesResponse, error) {
	if len(openaiResp.Choices) == 0 {
		return &ResponsesResponse{
			ID:        openaiResp.ID,
			Object:    "response",
			CreatedAt: openaiResp.Created,
			Model:     model,
			Status:    "completed",
			Output:    []ResponsesOutputItem{},
			Usage:     mapUsageToResponses(openaiResp.Usage),
		}, nil
	}

	choice := openaiResp.Choices[0]
	msg := choice.Message

	resp := &ResponsesResponse{
		ID:        openaiResp.ID,
		Object:    "response",
		CreatedAt: openaiResp.Created,
		Model:     model,
		Status:    "completed",
		Output:    []ResponsesOutputItem{},
		Usage:     mapUsageToResponses(openaiResp.Usage),
	}

	// Map finish_reason to status
	resp.Status, resp.IncompleteDetails = mapFinishReasonToResponsesStatus(choice.FinishReason)

	// Convert content → output_text
	var textContent string
	switch content := msg.Content.(type) {
	case string:
		textContent = content
	case []dto.MediaContent:
		for _, m := range content {
			if m.Type == "text" {
				textContent += m.Text
			}
		}
	}

	if textContent != "" {
		outputItem := ResponsesOutputItem{
			Type: "message",
			Role: "assistant",
			Content: []ResponsesOutputContent{
				{Type: "output_text", Text: textContent},
			},
		}
		resp.Output = append(resp.Output, outputItem)
	}

	// Convert tool_calls → function_call output items
	for _, tc := range msg.ToolCalls {
		resp.Output = append(resp.Output, ResponsesOutputItem{
			Type:      "function_call",
			ID:        tc.ID,
			CallID:    tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}

	return resp, nil
}

// mapUsageToResponses converts OpenAI Usage to Responses Usage format.
func mapUsageToResponses(usage dto.Usage) ResponsesUsage {
	return ResponsesUsage{
		InputTokens:  usage.PromptTokens,
		OutputTokens: usage.CompletionTokens,
		TotalTokens:  usage.TotalTokens,
		InputTokensDetails: ResponsesInputTokenDetails{
			CachedTokens: usage.PromptTokensDetails.CachedTokens,
		},
	}
}

// mapFinishReasonToResponsesStatus maps OpenAI finish_reason to Responses status.
func mapFinishReasonToResponsesStatus(finishReason string) (string, *ResponsesIncompleteDetails) {
	switch finishReason {
	case "stop":
		return "completed", nil
	case "length":
		return "incomplete", &ResponsesIncompleteDetails{Reason: "max_output_tokens"}
	case "content_filter":
		return "incomplete", &ResponsesIncompleteDetails{Reason: "content_filter"}
	case "tool_calls":
		return "completed", nil
	default:
		return "completed", nil
	}
}

// ============================================================================
// Error Helpers
// ============================================================================

// NewResponsesError creates a Responses-format error response.
func NewResponsesError(message, errType, code string) *ResponsesResponse {
	return &ResponsesResponse{
		Object: "response",
		Status: "failed",
		Error: &ResponsesError{
			Message: message,
			Type:    errType,
			Code:    code,
		},
		Output: []ResponsesOutputItem{},
		Usage:  ResponsesUsage{},
	}
}

// ResponsesErrorJSON returns a JSON error body for Responses API.
func ResponsesErrorJSON(message, errType, code string) []byte {
	resp := NewResponsesError(message, errType, code)
	b, _ := json.Marshal(resp)
	return b
}

// ============================================================================
// Responses Usage Extraction (for passthrough mode)
// ============================================================================

// ExtractResponsesUsage extracts token usage from a raw Responses API response body.
// Returns nil if parsing fails or usage is not present.
func ExtractResponsesUsage(body []byte) *dto.Usage {
	var resp struct {
		Usage struct {
			InputTokens        int `json:"input_tokens"`
			OutputTokens       int `json:"output_tokens"`
			TotalTokens        int `json:"total_tokens"`
			InputTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details,omitempty"`
			OutputTokensDetails struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"output_tokens_details,omitempty"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil
	}
	if resp.Usage.TotalTokens == 0 && resp.Usage.InputTokens == 0 && resp.Usage.OutputTokens == 0 {
		return nil
	}
	return &dto.Usage{
		PromptTokens:           resp.Usage.InputTokens,
		CompletionTokens:       resp.Usage.OutputTokens,
		TotalTokens:            resp.Usage.TotalTokens,
		PromptCacheHitTokens:   resp.Usage.InputTokensDetails.CachedTokens,
		CompletionTokenDetails: dto.OutputTokenDetails{ReasoningTokens: resp.Usage.OutputTokensDetails.ReasoningTokens},
	}
}

// ExtractResponsesCalledTools returns the names of function_call output items
// in a Responses-format response body (one entry per call, in order).
func ExtractResponsesCalledTools(body []byte) []string {
	var resp struct {
		Output []struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil
	}
	var names []string
	for _, item := range resp.Output {
		if item.Type == "function_call" && item.Name != "" {
			names = append(names, item.Name)
		}
	}
	return names
}

// ============================================================================
// Schema cleanup: recursively remove "format" fields from JSON schema
// ============================================================================

// StripFormatFromSchema recursively removes "format" keys from a JSON schema object.
// Some providers (e.g., older Claude versions) reject schemas containing "format".
func StripFormatFromSchema(schema map[string]interface{}) {
	delete(schema, "format")
	for k, v := range schema {
		switch val := v.(type) {
		case map[string]interface{}:
			StripFormatFromSchema(val)
		case []interface{}:
			for _, item := range val {
				if m, ok := item.(map[string]interface{}); ok {
					StripFormatFromSchema(m)
				}
			}
			// Also strip "format" from array items that are maps
			// This handles the case where schema.properties is an array
		case string:
			if k == "format" {
				delete(schema, k)
			}
		}
	}
}

// StripFormatFromRawSchema strips "format" from a JSON-raw schema and returns cleaned bytes.
func StripFormatFromRawSchema(raw json.RawMessage) json.RawMessage {
	var schema map[string]interface{}
	if err := json.Unmarshal(raw, &schema); err != nil {
		return raw
	}
	StripFormatFromSchema(schema)
	cleaned, _ := json.Marshal(schema)
	return cleaned
}

// ============================================================================
// needs_transform helper
// ============================================================================

// NeedsTransform returns true if a client request format differs from the upstream provider format.
// This allows short-circuiting conversion when client and upstream use the same native format.
func NeedsTransform(clientFormat ClientFormat, upstreamProvider string) bool {
	switch clientFormat {
	case ClientFormatOpenAI:
		// OpenAI client → any upstream needs transform (upstream adaptors handle it)
		// Actually OpenAI → OpenAI is identity, but upstream may still need model name mapping
		return upstreamProvider != "openai"
	case ClientFormatAnthropic:
		// Anthropic client → Anthropic upstream: no transform needed (passthrough)
		return upstreamProvider != "anthropic"
	case ClientFormatResponses:
		// Responses client → OpenAI upstream: needs transform (Responses → Chat)
		// Responses client → other upstream: also needs transform
		return true
	default:
		return true
	}
}

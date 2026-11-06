package convert

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ai-proxy/ai-proxy-go/dto"
)

// AnthropicRequest represents Anthropic Messages API request format
type AnthropicRequest struct {
	Model         string             `json:"model"`
	Messages      []AnthropicMessage `json:"messages"`
	System        json.RawMessage    `json:"system,omitempty"`
	MaxTokens     *int               `json:"max_tokens,omitempty"`
	Temperature   *float64           `json:"temperature,omitempty"`
	TopP          *float64           `json:"top_p,omitempty"`
	TopK          *int               `json:"top_k,omitempty"`
	Stream        *bool              `json:"stream,omitempty"`
	StopSequences []string           `json:"stop_sequences,omitempty"`
	Tools         []AnthropicTool    `json:"tools,omitempty"`
	ToolChoice    json.RawMessage    `json:"tool_choice,omitempty"`
	Thinking      *AnthropicThinking `json:"thinking,omitempty"`
	Metadata      *AnthropicMetadata `json:"metadata,omitempty"`
}

// AnthropicMessage represents a message in Anthropic format
type AnthropicMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // string or []AnthropicContentBlock
}

// AnthropicContentBlock represents a content block in Anthropic format
type AnthropicContentBlock struct {
	Type         string           `json:"type"`
	Text         string           `json:"text,omitempty"`
	ID           string           `json:"id,omitempty"`
	Name         string           `json:"name,omitempty"`
	Input        json.RawMessage  `json:"input,omitempty"`
	ToolUseID    string           `json:"tool_use_id,omitempty"`
	Content      interface{}      `json:"content,omitempty"`
	Source       *AnthropicSource `json:"source,omitempty"`
	CacheControl *CacheControl    `json:"cache_control,omitempty"`
}

// AnthropicSource represents media source in Anthropic format
type AnthropicSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

// CacheControl represents cache control for prompt caching
type CacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

// AnthropicTool represents a tool definition in Anthropic format
type AnthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// AnthropicThinking represents thinking configuration
type AnthropicThinking struct {
	Type         string `json:"type"`
	BudgetTokens *int   `json:"budget_tokens,omitempty"`
}

// AnthropicMetadata represents request metadata
type AnthropicMetadata struct {
	UserID string `json:"user_id,omitempty"`
}

// AnthropicResponse represents Anthropic Messages API response format
type AnthropicResponse struct {
	ID           string                  `json:"id"`
	Type         string                  `json:"type"`
	Role         string                  `json:"role"`
	Model        string                  `json:"model"`
	Content      []AnthropicContentBlock `json:"content"`
	StopReason   string                  `json:"stop_reason,omitempty"`
	StopSequence string                  `json:"stop_sequence,omitempty"`
	Usage        AnthropicUsage          `json:"usage"`
}

// AnthropicUsage represents usage in Anthropic format
type AnthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

// AnthropicStreamEvent represents Anthropic SSE streaming event
type AnthropicStreamEvent struct {
	Type         string                 `json:"type"`
	Index        *int                   `json:"index,omitempty"`
	Delta        *AnthropicDelta        `json:"delta,omitempty"`
	ContentBlock *AnthropicContentBlock `json:"content_block,omitempty"`
	Message      *AnthropicResponse     `json:"message,omitempty"`
	Usage        *AnthropicUsage        `json:"usage,omitempty"`
}

// AnthropicDelta represents delta in stream event
type AnthropicDelta struct {
	Type         string `json:"type,omitempty"`
	Text         string `json:"text,omitempty"`
	PartialJSON  string `json:"partial_json,omitempty"`
	StopReason   string `json:"stop_reason,omitempty"`
	StopSequence string `json:"stop_sequence,omitempty"`
}

// AnthropicToOpenAI converts Anthropic request to OpenAI GeneralOpenAIRequest
func AnthropicToOpenAI(anthropicReq *AnthropicRequest) (*dto.GeneralOpenAIRequest, error) {
	req := &dto.GeneralOpenAIRequest{
		Model:    anthropicReq.Model,
		Messages: make([]dto.Message, 0),
	}

	// Copy basic parameters
	req.MaxTokens = anthropicReq.MaxTokens
	req.Temperature = anthropicReq.Temperature
	req.TopP = anthropicReq.TopP
	req.Stream = anthropicReq.Stream
	req.Stop = anthropicReq.StopSequences

	// Convert system prompt
	if anthropicReq.System != nil {
		var systemStr string
		if err := json.Unmarshal(anthropicReq.System, &systemStr); err == nil {
			// Single system string
			systemStr = StripLeadingAnthropicBillingHeader(systemStr)
			if systemStr != "" {
				req.Messages = append(req.Messages, dto.Message{
					Role:    "system",
					Content: systemStr,
				})
			}
		} else {
			// Array of system blocks
			var systemBlocks []AnthropicContentBlock
			if err := json.Unmarshal(anthropicReq.System, &systemBlocks); err == nil {
				var parts []string
				for _, block := range systemBlocks {
					if block.Type == "text" {
						text := StripLeadingAnthropicBillingHeader(block.Text)
						if text != "" {
							parts = append(parts, text)
						}
					}
				}
				if len(parts) > 0 {
					req.Messages = append(req.Messages, dto.Message{
						Role:    "system",
						Content: strings.Join(parts, "\n"),
					})
				}
			}
		}
	}

	// Convert messages.
	// Anthropic allows multiple tool_result blocks inside a single user
	// message; OpenAI (and Bedrock Converse downstream) needs one "tool"
	// message per result, so each tool_result block becomes its own message.
	for _, msg := range anthropicReq.Messages {
		openaiMsg := dto.Message{
			Role: msg.Role,
		}
		var toolResultMsgs []dto.Message

		switch content := msg.Content.(type) {
		case string:
			openaiMsg.Content = content
		case []interface{}:
			blocks := make([]dto.MediaContent, 0)
			var toolCalls []dto.ToolCall
			for _, item := range content {
				itemBytes, _ := json.Marshal(item)
				var block AnthropicContentBlock
				if err := json.Unmarshal(itemBytes, &block); err != nil {
					continue
				}
				switch block.Type {
				case "text":
					blocks = append(blocks, dto.MediaContent{
						Type: "text",
						Text: block.Text,
					})
				case "image":
					if block.Source != nil && block.Source.Type == "base64" {
						blocks = append(blocks, dto.MediaContent{
							Type: "image_url",
							ImageURL: &dto.MessageImageURL{
								URL: fmt.Sprintf("data:%s;base64,%s", block.Source.MediaType, block.Source.Data),
							},
						})
					} else if block.Source != nil && block.Source.URL != "" {
						blocks = append(blocks, dto.MediaContent{
							Type: "image_url",
							ImageURL: &dto.MessageImageURL{
								URL: block.Source.URL,
							},
						})
					}
				case "tool_use":
					inputStr := "{}"
					if block.Input != nil {
						inputStr = string(block.Input)
					}
					toolCalls = append(toolCalls, dto.ToolCall{
						ID:   block.ID,
						Type: "function",
						Function: dto.FunctionCall{
							Name:      block.Name,
							Arguments: inputStr,
						},
					})
				case "tool_result":
					// block.Content may be a string or a structured block
					// array. The OpenAI tool message content (and downstream
					// Bedrock adapter) requires a string, so serialize
					// non-string content to a JSON string.
					content := block.Content
					if _, ok := content.(string); !ok && content != nil {
						if b, err := json.Marshal(content); err == nil {
							content = string(b)
						}
					}
					toolResultMsgs = append(toolResultMsgs, dto.Message{
						Role:       "tool",
						ToolCallID: block.ToolUseID,
						Content:    content,
					})
				}
			}
			if len(toolCalls) > 0 {
				openaiMsg.ToolCalls = toolCalls
				openaiMsg.Content = nil
			} else if len(blocks) > 0 {
				openaiMsg.Content = blocks
			}
		default:
			// Fallback: try to marshal/unmarshal
			if contentBytes, err := json.Marshal(content); err == nil {
				var text string
				if err := json.Unmarshal(contentBytes, &text); err == nil {
					openaiMsg.Content = text
				}
			}
		}

		// Tool result messages must immediately follow the assistant message
		// carrying the tool calls; any remaining text becomes a user message
		// after them (OpenAI cannot mix the two in one message).
		req.Messages = append(req.Messages, toolResultMsgs...)
		if openaiMsg.Content != nil || len(openaiMsg.ToolCalls) > 0 || len(toolResultMsgs) == 0 {
			req.Messages = append(req.Messages, openaiMsg)
		}
	}

	// Convert tools (with schema format stripping for compatibility)
	for _, tool := range anthropicReq.Tools {
		req.Tools = append(req.Tools, dto.ToolCallRequest{
			Type: "function",
			Function: dto.FunctionRequest{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  map[string]interface{}{},
			},
		})
		if tool.InputSchema != nil {
			var params map[string]interface{}
			if err := json.Unmarshal(tool.InputSchema, &params); err == nil {
				StripFormatFromSchema(params)
				req.Tools[len(req.Tools)-1].Function.Parameters = params
			}
		}
	}

	// Convert tool_choice
	if anthropicReq.ToolChoice != nil {
		req.ToolChoice = anthropicToolChoiceToOpenAI(anthropicReq.ToolChoice)
	}

	// Convert thinking to reasoning_effort
	if anthropicReq.Thinking != nil {
		req = applyAnthropicThinkingToOpenAI(req, anthropicReq.Thinking)
	}

	return req, nil
}

// anthropicToolChoiceToOpenAI converts Anthropic tool_choice to OpenAI format
func anthropicToolChoiceToOpenAI(raw json.RawMessage) interface{} {
	var strChoice string
	if err := json.Unmarshal(raw, &strChoice); err == nil {
		switch strChoice {
		case "auto":
			return "auto"
		case "any":
			return "required"
		case "none":
			return "none"
		default:
			return strChoice
		}
	}

	var objChoice map[string]interface{}
	if err := json.Unmarshal(raw, &objChoice); err == nil {
		choiceType, _ := objChoice["type"].(string)
		switch choiceType {
		case "auto":
			return "auto"
		case "any":
			return "required"
		case "none":
			return "none"
		case "tool":
			name, _ := objChoice["name"].(string)
			return map[string]interface{}{
				"type": "function",
				"function": map[string]interface{}{
					"name": name,
				},
			}
		}
	}

	return raw
}

// applyAnthropicThinkingToOpenAI converts Anthropic thinking to OpenAI reasoning_effort
func applyAnthropicThinkingToOpenAI(req *dto.GeneralOpenAIRequest, thinking *AnthropicThinking) *dto.GeneralOpenAIRequest {
	switch thinking.Type {
	case "adaptive":
		// adaptive = maximum reasoning effort
		// OpenAI doesn't have "xhigh" in standard API, use "high"
		req = setReasoningEffort(req, "high")
	case "enabled":
		if thinking.BudgetTokens != nil {
			budget := *thinking.BudgetTokens
			switch {
			case budget < 4000:
				req = setReasoningEffort(req, "low")
			case budget < 16000:
				req = setReasoningEffort(req, "medium")
			default:
				req = setReasoningEffort(req, "high")
			}
		} else {
			req = setReasoningEffort(req, "high")
		}
	}
	return req
}

func setReasoningEffort(req *dto.GeneralOpenAIRequest, effort string) *dto.GeneralOpenAIRequest {
	// Store reasoning_effort in the request so downstream adaptors (e.g. Claude)
	// can map it to their native thinking configuration.
	req.ReasoningEffort = effort
	return req
}

// OpenAIToAnthropicResponse converts OpenAI response to Anthropic response format
func OpenAIToAnthropicResponse(openaiResp *dto.OpenAITextResponse) (*AnthropicResponse, error) {
	anthropicResp := &AnthropicResponse{
		ID:      openaiResp.ID,
		Type:    "message",
		Role:    "assistant",
		Model:   openaiResp.Model,
		Content: make([]AnthropicContentBlock, 0),
	}

	if len(openaiResp.Choices) == 0 {
		return anthropicResp, nil
	}

	choice := openaiResp.Choices[0]
	msg := choice.Message

	// Convert content
	switch content := msg.Content.(type) {
	case string:
		if content != "" {
			anthropicResp.Content = append(anthropicResp.Content, AnthropicContentBlock{
				Type: "text",
				Text: content,
			})
		}
	case []dto.MediaContent:
		for _, m := range content {
			if m.Type == "text" && m.Text != "" {
				anthropicResp.Content = append(anthropicResp.Content, AnthropicContentBlock{
					Type: "text",
					Text: m.Text,
				})
			}
		}
	}

	// Convert tool_calls to tool_use
	for _, tc := range msg.ToolCalls {
		var input map[string]interface{}
		_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
		inputBytes, _ := json.Marshal(input)

		anthropicResp.Content = append(anthropicResp.Content, AnthropicContentBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: inputBytes,
		})
	}

	// Convert finish_reason to stop_reason
	anthropicResp.StopReason = openAIStopReasonToAnthropic(choice.FinishReason)

	// Convert usage
	anthropicResp.Usage = AnthropicUsage{
		InputTokens:          openaiResp.Usage.PromptTokens,
		OutputTokens:         openaiResp.Usage.CompletionTokens,
		CacheReadInputTokens: openaiResp.Usage.PromptTokensDetails.CachedTokens,
	}

	return anthropicResp, nil
}

// openAIStopReasonToAnthropic maps OpenAI finish_reason to Anthropic stop_reason
func openAIStopReasonToAnthropic(reason string) string {
	switch reason {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	case "content_filter":
		return "stop_sequence"
	default:
		return reason
	}
}

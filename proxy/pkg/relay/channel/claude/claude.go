package claude

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/mayiants/ai-proxy-go/dto"
	"github.com/mayiants/ai-proxy-go/pkg/relay/base"
	"github.com/mayiants/ai-proxy-go/types"
)

// ClaudeAdaptor - Anthropic Claude API adapter
//
// streamState is stored on the adaptor instance but is effectively per-request:
// a new ClaudeAdaptor is created for each request, so there is no sharing
// across concurrent requests. If the adaptor is ever reused across requests,
// streamState must be moved into a per-request struct to avoid data races.
type ClaudeAdaptor struct {
	*base.BaseAdaptor
	apiVersion  string
	streamState *streamState
}

// NewClaudeAdaptor - Create Claude adapter
func NewClaudeAdaptor(cfg *base.AdaptorConfig) *ClaudeAdaptor {
	baseURL := ""
	if cfg != nil {
		baseURL = cfg.BaseURL
	}
	if baseURL == "" {
		baseURL = "https://api.anthropic.com/v1"
	}

	apiVersion := ""
	if cfg != nil {
		apiVersion = cfg.APIVersion
	}
	if apiVersion == "" {
		apiVersion = "2023-06-01"
	}

	a := &ClaudeAdaptor{apiVersion: apiVersion}
	a.BaseAdaptor = base.NewBaseAdaptor("anthropic", &base.AdaptorConfig{
		BaseURL: baseURL,
		APIKey:  cfg.APIKey,
		Models:  cfg.Models,
	})
	a.SetEndpointResolver(a.GetEndpoint)
	return a
}

// GetRelayFormat - Get relay format
func (a *ClaudeAdaptor) GetRelayFormat() types.RelayFormat {
	return types.RelayFormatClaude
}

// GetHeaders - Get Claude-specific headers
func (a *ClaudeAdaptor) GetHeaders() map[string]string {
	apiKey := ""
	if a.Config != nil {
		apiKey = a.Config.APIKey
	}
	return map[string]string{
		"Content-Type":      "application/json",
		"x-api-key":         apiKey,
		"anthropic-version": a.apiVersion,
		"anthropic-dangerous-direct-browser-access": "true",
	}
}

// GetEndpoint - Get Claude endpoint
func (a *ClaudeAdaptor) GetEndpoint(model string, stream bool) string {
	return "/messages"
}

// ToProvider - Transform OpenAI request to Claude format
func (a *ClaudeAdaptor) ToProvider(req dto.GeneralOpenAIRequest) interface{} {
	claudeReq := dto.ClaudeRequest{
		Model:    req.Model,
		Messages: make([]dto.ClaudeMessage, 0),
	}

	// Set max_tokens (required for Claude)
	if req.MaxTokens != nil {
		maxTokens := *req.MaxTokens
		claudeReq.MaxTokens = &maxTokens
	} else {
		// Default max_tokens for Claude
		defaultMax := 4096
		claudeReq.MaxTokens = &defaultMax
	}

	// Set optional parameters
	if req.Temperature != nil {
		claudeReq.Temperature = req.Temperature
	}
	if req.TopP != nil {
		claudeReq.TopP = req.TopP
	}
	if req.TopK != nil {
		claudeReq.TopK = req.TopK
	}
	if req.Stream != nil {
		claudeReq.Stream = req.Stream
	}

	// Convert stop sequences
	if req.Stop != nil {
		switch v := req.Stop.(type) {
		case string:
			claudeReq.StopSequences = []string{v}
		case []interface{}:
			for _, s := range v {
				if str, ok := s.(string); ok {
					claudeReq.StopSequences = append(claudeReq.StopSequences, str)
				}
			}
		}
	}

	// Convert messages
	systemMessages := make([]string, 0)
	for _, msg := range req.Messages {
		if msg.Role == "system" {
			// Claude uses separate system field
			content := extractTextContent(msg.Content)
			systemMessages = append(systemMessages, content)
		} else {
			claudeMsg := convertToClaudeMessage(msg)
			claudeReq.Messages = append(claudeReq.Messages, claudeMsg)
		}
	}

	// Set system prompt
	if len(systemMessages) > 0 {
		claudeReq.System = strings.Join(systemMessages, "\n\n")
	}

	// Convert tools
	if len(req.Tools) > 0 {
		claudeReq.Tools = make([]interface{}, 0)
		for _, tool := range req.Tools {
			claudeTool := map[string]interface{}{
				"name": tool.Function.Name,
			}
			if tool.Function.Description != "" {
				claudeTool["description"] = tool.Function.Description
			}
			if tool.Function.Parameters != nil {
				claudeTool["input_schema"] = tool.Function.Parameters
			}
			claudeReq.Tools = append(claudeReq.Tools, claudeTool)
		}
	}

	// Convert tool_choice: OpenAI → Claude
	if req.ToolChoice != nil {
		claudeReq.ToolChoice = mapOpenAIToolChoiceToClaude(req.ToolChoice)
	}

	// Convert reasoning_effort → thinking
	if req.ReasoningEffort != "" {
		maxTokens := 4096
		if claudeReq.MaxTokens != nil && *claudeReq.MaxTokens > 0 {
			maxTokens = *claudeReq.MaxTokens
		}
		claudeReq.Thinking = mapReasoningEffortToThinking(req.ReasoningEffort, maxTokens)
	}

	return claudeReq
}

// ParseResponse - Parse upstream response body into native struct
func (a *ClaudeAdaptor) ParseResponse(body []byte) (interface{}, error) {
	var claudeResp dto.ClaudeResponse
	if err := json.Unmarshal(body, &claudeResp); err != nil {
		return nil, err
	}
	return &claudeResp, nil
}

// FromProvider - Transform Claude response to OpenAI format
func (a *ClaudeAdaptor) FromProvider(response interface{}, req dto.GeneralOpenAIRequest) *dto.OpenAITextResponse {
	var claudeResp *dto.ClaudeResponse
	switch v := response.(type) {
	case *dto.ClaudeResponse:
		claudeResp = v
	case dto.ClaudeResponse:
		claudeResp = &v
	case []byte:
		claudeResp = &dto.ClaudeResponse{}
		if err := json.Unmarshal(v, claudeResp); err != nil {
			return nil
		}
	default:
		return nil
	}

	// Convert to OpenAI format
	openaiResp := &dto.OpenAITextResponse{
		ID:      claudeResp.ID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   claudeResp.Model,
		Choices: make([]dto.OpenAITextResponseChoice, 0),
		Usage:   convertClaudeUsage(claudeResp.Usage),
	}

	// Convert content
	for _, content := range claudeResp.Content {
		if content.Type == "text" && content.Text != nil {
			openaiResp.Choices = append(openaiResp.Choices, dto.OpenAITextResponseChoice{
				Index: 0,
				Message: dto.Message{
					Role:    "assistant",
					Content: *content.Text,
				},
				FinishReason: mapClaudeStopReason(claudeResp.StopReason),
			})
		}
	}

	return openaiResp
}

// ExtractUsage - Extract usage from Claude response
func (a *ClaudeAdaptor) ExtractUsage(response interface{}) *dto.Usage {
	var claudeResp *dto.ClaudeResponse
	switch v := response.(type) {
	case *dto.ClaudeResponse:
		claudeResp = v
	case dto.ClaudeResponse:
		claudeResp = &v
	case []byte:
		claudeResp = &dto.ClaudeResponse{}
		if err := json.Unmarshal(v, claudeResp); err != nil {
			return &dto.Usage{}
		}
	default:
		return &dto.Usage{}
	}

	usage := convertClaudeUsage(claudeResp.Usage)
	return &usage
}

// convertClaudeUsage - Convert Claude usage to OpenAI format.
// Same cache_read compensation as UsageFromClaude: Anthropic's input_tokens
// excludes cache_read, but OpenAI semantics (and our billing) expect
// prompt_tokens to include cached tokens. See UsageFromClaude for details.
func convertClaudeUsage(claudeUsage dto.ClaudeUsage) dto.Usage {
	cacheRead := claudeUsage.CacheReadInputTokens
	usage := dto.Usage{
		InputTokens:      claudeUsage.InputTokens,
		OutputTokens:     claudeUsage.OutputTokens,
		PromptTokens:     claudeUsage.InputTokens + cacheRead,
		CompletionTokens: claudeUsage.OutputTokens,
		TotalTokens:      claudeUsage.InputTokens + cacheRead + claudeUsage.OutputTokens,
	}

	// Cache tokens
	if cacheRead > 0 {
		usage.PromptTokensDetails.CachedTokens = cacheRead
		usage.PromptCacheHitTokens = cacheRead
	}

	if claudeUsage.CacheCreationInputTokens > 0 {
		usage.PromptTokensDetails.CachedCreationTokens = claudeUsage.CacheCreationInputTokens
	}

	// Split cache creation tokens
	if claudeUsage.CacheCreation != nil {
		usage.ClaudeCacheCreation5mTokens = claudeUsage.CacheCreation.Ephemeral5mInputTokens
		usage.ClaudeCacheCreation1hTokens = claudeUsage.CacheCreation.Ephemeral1hInputTokens
	}

	return usage
}

// mapClaudeStopReason - Map Claude stop reason to OpenAI format
func mapClaudeStopReason(reason string) string {
	switch reason {
	case "end_turn":
		return "stop"
	case "max_tokens":
		return "length"
	case "stop_sequence":
		return "stop"
	case "tool_use":
		return "tool_calls"
	default:
		return reason
	}
}

// convertToClaudeMessage - Convert OpenAI message to Claude format
func convertToClaudeMessage(msg dto.Message) dto.ClaudeMessage {
	claudeMsg := dto.ClaudeMessage{
		Role: msg.Role,
	}

	// Convert content
	switch v := msg.Content.(type) {
	case string:
		claudeMsg.Content = []dto.ClaudeContent{
			{Type: "text", Text: &v},
		}
	case []dto.MediaContent:
		contents := make([]dto.ClaudeContent, 0)
		for _, m := range v {
			if m.Type == "text" {
				contents = append(contents, dto.ClaudeContent{
					Type:         "text",
					Text:         &m.Text,
					CacheControl: m.CacheControl,
				})
			} else if m.Type == "image_url" && m.ImageURL != nil {
				// Convert image to Claude format — detect data URI vs HTTP URL
				url := m.ImageURL.URL
				if strings.HasPrefix(url, "data:") {
					// Parse data URI: data:<mediatype>;base64,<data>
					mediaType := "image/jpeg"
					data := url
					if idx := strings.Index(url, ";base64,"); idx > 0 {
						mediaType = url[5:idx]
						data = url[idx+8:]
					}
					contents = append(contents, dto.ClaudeContent{
						Type: "image",
						Source: &dto.ClaudeSource{
							Type:      "base64",
							MediaType: mediaType,
							Data:      data,
						},
					})
				} else {
					// HTTP/HTTPS URL — use Claude's URL source type
					contents = append(contents, dto.ClaudeContent{
						Type: "image",
						Source: &dto.ClaudeSource{
							Type: "url",
							URL:  url,
						},
					})
				}
			}
		}
		claudeMsg.Content = contents
	}

	// Handle tool calls
	if len(msg.ToolCalls) > 0 {
		contents := make([]dto.ClaudeContent, 0)
		for _, tc := range msg.ToolCalls {
			contents = append(contents, dto.ClaudeContent{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: json.RawMessage(tc.Function.Arguments),
			})
		}
		claudeMsg.Content = contents
	}

	// Handle tool results
	if msg.ToolCallID != "" {
		claudeMsg.Role = "user"
		claudeMsg.Content = []dto.ClaudeContent{
			{
				Type:      "tool_result",
				ToolUseID: msg.ToolCallID,
				Content:   msg.Content,
			},
		}
	}

	return claudeMsg
}

// extractTextContent - Extract text from message content
func extractTextContent(content interface{}) string {
	switch v := content.(type) {
	case string:
		return v
	case []dto.MediaContent:
		for _, m := range v {
			if m.Type == "text" {
				return m.Text
			}
		}
	}
	return ""
}

// mapOpenAIToolChoiceToClaude converts OpenAI tool_choice to Claude format
func mapOpenAIToolChoiceToClaude(toolChoice interface{}) interface{} {
	switch v := toolChoice.(type) {
	case string:
		switch v {
		case "required":
			return "any"
		case "auto":
			return "auto"
		case "none":
			return "none"
		default:
			return v
		}
	case map[string]interface{}:
		choiceType, _ := v["type"].(string)
		switch choiceType {
		case "function":
			funcObj, _ := v["function"].(map[string]interface{})
			name, _ := funcObj["name"].(string)
			return map[string]interface{}{
				"type": "tool",
				"name": name,
			}
		default:
			return v
		}
	}
	return toolChoice
}

// mapReasoningEffortToThinking converts OpenAI reasoning_effort to Claude thinking.
// maxTokens is the request's max_tokens (or the 4096 default), used to clamp
// budget_tokens: Anthropic requires budget_tokens < max_tokens and >= 1024.
// When the client doesn't set max_tokens, ToProvider defaults it to 4096, which
// would otherwise be below the high-effort budget (20000) and trigger a 400.
func mapReasoningEffortToThinking(effort string, maxTokens int) *dto.Thinking {
	thinking := &dto.Thinking{Type: "enabled"}
	var budget int
	switch effort {
	case "low":
		budget = 2000
	case "medium":
		budget = 8000
	case "high", "xhigh":
		budget = 20000
	default:
		budget = 8000
	}
	// Anthropic requires budget_tokens < max_tokens. If the budget meets or
	// exceeds max_tokens, clamp it down leaving headroom for the response.
	if maxTokens > 0 && budget >= maxTokens {
		budget = maxTokens - 1024
	}
	// Anthropic requires budget_tokens >= 1024.
	if budget < 1024 {
		budget = 1024
	}
	thinking.BudgetTokens = &budget
	return thinking
}

// TransformStreamChunk - Transform Claude streaming chunk
func (a *ClaudeAdaptor) TransformStreamChunk(chunk interface{}, index int) interface{} {
	data, ok := chunk.([]byte)
	if !ok {
		return nil
	}

	// Parse SSE event
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "data: ") {
			dataStr := strings.TrimPrefix(line, "data: ")
			if dataStr == "" {
				continue
			}

			var event dto.ClaudeStreamEvent
			if err := json.Unmarshal([]byte(dataStr), &event); err != nil {
				continue
			}

			// Convert to OpenAI format
			return a.convertClaudeStreamEvent(event)
		}
	}

	return nil
}

// streamState tracks incremental tool call state across stream chunks
type streamState struct {
	toolIndex   int
	toolID      string
	toolName    string
	pendingArgs strings.Builder
	inToolCall  bool
}

// convertClaudeStreamEvent - Convert Claude stream event to OpenAI format.
// streamState is per-request: a new adaptor is created per request, so the
// state is not shared across requests. If the adaptor is ever reused, this
// must be moved into a per-request struct.
func (a *ClaudeAdaptor) convertClaudeStreamEvent(event dto.ClaudeStreamEvent) *dto.ChatCompletionsStreamResponse {
	resp := &dto.ChatCompletionsStreamResponse{
		ID:      "",
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   "",
		Choices: make([]dto.ChatCompletionsStreamResponseChoice, 0),
	}

	if event.Message != nil {
		resp.ID = event.Message.ID
		resp.Model = event.Message.Model
	}

	switch event.Type {
	case "content_block_start":
		if event.ContentBlock != nil {
			switch event.ContentBlock.Type {
			case "text":
				// Text block start, no delta yet
			case "tool_use":
				// Tool use block start: emit tool_calls delta with id and name
				if a.streamState == nil {
					a.streamState = &streamState{}
				}
				a.streamState.toolID = event.ContentBlock.ID
				a.streamState.toolName = event.ContentBlock.Name
				a.streamState.inToolCall = true
				a.streamState.pendingArgs.Reset()

				toolCalls := []dto.ToolCall{
					{
						Index: a.streamState.toolIndex,
						ID:    event.ContentBlock.ID,
						Type:  "function",
						Function: dto.FunctionCall{
							Name: event.ContentBlock.Name,
						},
					},
				}
				resp.Choices = append(resp.Choices, dto.ChatCompletionsStreamResponseChoice{
					Index: 0,
					Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
						ToolCalls: toolCalls,
					},
				})
			case "thinking":
				// Thinking block start
			}
		}
	case "content_block_delta":
		if event.Delta != nil {
			switch event.Delta.Type {
			case "text_delta", "":
				if event.Delta.Text != "" {
					content := event.Delta.Text
					resp.Choices = append(resp.Choices, dto.ChatCompletionsStreamResponseChoice{
						Index: 0,
						Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
							Content: &content,
						},
						FinishReason: nil,
					})
				}
			case "input_json_delta":
				// Tool call arguments delta
				if a.streamState != nil && a.streamState.inToolCall {
					a.streamState.pendingArgs.WriteString(event.Delta.PartialJSON)
					args := event.Delta.PartialJSON
					toolCalls := []dto.ToolCall{
						{
							Index: a.streamState.toolIndex,
							ID:    a.streamState.toolID,
							Type:  "function",
							Function: dto.FunctionCall{
								Arguments: args,
							},
						},
					}
					resp.Choices = append(resp.Choices, dto.ChatCompletionsStreamResponseChoice{
						Index: 0,
						Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
							ToolCalls: toolCalls,
						},
					})
				}
			case "thinking_delta":
				// Thinking/reasoning content delta
				if event.Delta.Text != "" {
					reasoning := event.Delta.Text
					resp.Choices = append(resp.Choices, dto.ChatCompletionsStreamResponseChoice{
						Index: 0,
						Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
							ReasoningContent: &reasoning,
						},
					})
				}
			}
		}
	case "content_block_stop":
		if a.streamState != nil && a.streamState.inToolCall {
			a.streamState.inToolCall = false
			a.streamState.toolIndex++
		}
	case "message_delta":
		if event.Delta != nil && event.Delta.StopReason != "" {
			finishReason := mapClaudeStopReason(event.Delta.StopReason)
			resp.Choices = append(resp.Choices, dto.ChatCompletionsStreamResponseChoice{
				Index:        0,
				Delta:        dto.ChatCompletionsStreamResponseChoiceDelta{},
				FinishReason: &finishReason,
			})
		}
		if event.Usage != nil {
			usage := convertClaudeUsage(*event.Usage)
			resp.Usage = &usage
		}
	case "message_start":
		if event.Message != nil {
			resp.ID = event.Message.ID
			resp.Model = event.Message.Model
		}
	case "message_stop":
		// Message complete, reset stream state
		a.streamState = nil
	}

	return resp
}

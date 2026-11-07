package convert

import (
	"encoding/json"
	"strings"

	"github.com/mayiants/ai-proxy-go/dto"
)

// RequestOpenAI2Claude converts an OpenAI-format request to Claude format.
// This is the primary entry point for Claude adapter request transformation.
func RequestOpenAI2Claude(req dto.GeneralOpenAIRequest) (*dto.ClaudeRequest, error) {
	claudeReq := &dto.ClaudeRequest{
		Model:       req.Model,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		TopK:        req.TopK,
		Stream:      req.Stream,
		MaxTokens:   req.MaxTokens,
	}

	// Convert stop sequences
	if req.Stop != nil {
		switch v := req.Stop.(type) {
		case string:
			if v != "" {
				claudeReq.StopSequences = []string{v}
			}
		case []string:
			claudeReq.StopSequences = v
		case []interface{}:
			stops := make([]string, 0, len(v))
			for _, s := range v {
				if str, ok := s.(string); ok {
					stops = append(stops, str)
				}
			}
			claudeReq.StopSequences = stops
		}
	}

	// Convert tools
	if len(req.Tools) > 0 {
		tools := make([]interface{}, 0, len(req.Tools))
		for _, tool := range req.Tools {
			tools = append(tools, map[string]interface{}{
				"name":         tool.Function.Name,
				"description":  tool.Function.Description,
				"input_schema": tool.Function.Parameters,
			})
		}
		claudeReq.Tools = tools
	}

	// Convert tool_choice
	if req.ToolChoice != nil {
		claudeReq.ToolChoice = convertToolChoiceToClaude(req.ToolChoice)
	}

	// Separate system messages from conversation messages
	var systemContent interface{}
	claudeMessages := make([]dto.ClaudeMessage, 0, len(req.Messages))

	for _, msg := range req.Messages {
		if msg.Role == "system" {
			// Accumulate system content
			if systemContent == nil {
				systemContent = msg.Content
			} else {
				// Append to existing system content
				existing := ExtractStringContent(systemContent)
				newContent := ExtractStringContent(msg.Content)
				systemContent = existing + "\n" + newContent
			}
			continue
		}

		claudeMsg := dto.ClaudeMessage{
			Role: ToClaudeRole(msg.Role),
		}

		// Convert content based on type
		switch content := msg.Content.(type) {
		case string:
			claudeMsg.Content = content
		case []dto.MediaContent:
			blocks := make([]dto.ClaudeContent, 0, len(content))
			for _, mc := range content {
				blocks = append(blocks, mediaContentToClaude(mc))
			}
			claudeMsg.Content = blocks
		default:
			claudeMsg.Content = ExtractStringContent(msg.Content)
		}

		claudeMessages = append(claudeMessages, claudeMsg)
	}

	if systemContent != nil {
		claudeReq.System = systemContent
	}
	claudeReq.Messages = claudeMessages

	return claudeReq, nil
}

// mediaContentToClaude converts a MediaContent to Claude content block.
func mediaContentToClaude(mc dto.MediaContent) dto.ClaudeContent {
	switch mc.Type {
	case "text":
		return dto.ClaudeContent{Type: "text", Text: &mc.Text}
	case "image_url":
		if mc.ImageURL != nil {
			url := mc.ImageURL.URL
			// Detect data URI (base64 encoded) vs HTTP URL
			if strings.HasPrefix(url, "data:") {
				// Parse data URI: data:<mediatype>;base64,<data>
				mediaType := "image/jpeg"
				data := url
				if idx := strings.Index(url, ";base64,"); idx > 0 {
					mediaType = url[5:idx] // extract media type after "data:"
					data = url[idx+8:]     // extract base64 data after ";base64,"
				}
				return dto.ClaudeContent{
					Type: "image",
					Source: &dto.ClaudeSource{
						Type:      "base64",
						MediaType: mediaType,
						Data:      data,
					},
				}
			}
			// HTTP/HTTPS URL — use Claude's URL source type
			return dto.ClaudeContent{
				Type: "image",
				Source: &dto.ClaudeSource{
					Type:      "url",
					MediaType: "image/jpeg",
					URL:       url,
				},
			}
		}
	}
	return dto.ClaudeContent{Type: "text", Text: &mc.Text}
}

// convertToolChoiceToClaude converts OpenAI tool_choice to Claude format.
func convertToolChoiceToClaude(toolChoice interface{}) interface{} {
	switch v := toolChoice.(type) {
	case string:
		switch v {
		case "auto":
			return map[string]string{"type": "auto"}
		case "none":
			return map[string]string{"type": "none"}
		case "required":
			return map[string]string{"type": "any"}
		}
	case map[string]interface{}:
		if fn, ok := v["function"].(map[string]interface{}); ok {
			if name, ok := fn["name"].(string); ok {
				return map[string]interface{}{
					"type": "tool",
					"name": name,
				}
			}
		}
	}
	return nil
}

// ResponseClaude2OpenAI converts a Claude response to OpenAI format.
func ResponseClaude2OpenAI(claudeResp *dto.ClaudeResponse, model string) *dto.OpenAITextResponse {
	if claudeResp == nil {
		return nil
	}

	// Extract text content
	var content string
	var toolCalls []dto.ToolCall
	for _, block := range claudeResp.Content {
		switch block.Type {
		case "text":
			if block.Text != nil {
				content += *block.Text
			}
		case "tool_use":
			toolCalls = append(toolCalls, dto.ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: dto.FunctionCall{
					Name:      block.Name,
					Arguments: claudeInputToArgs(block.Input),
				},
			})
		}
	}

	choice := dto.OpenAITextResponseChoice{
		Index: 0,
		Message: dto.Message{
			Role:      "assistant",
			Content:   content,
			ToolCalls: toolCalls,
		},
		FinishReason: StopReasonToOpenAI(claudeResp.StopReason),
	}

	usage := UsageFromClaude(claudeResp.Usage)

	return &dto.OpenAITextResponse{
		ID:      claudeResp.ID,
		Model:   model,
		Object:  "chat.completion",
		Choices: []dto.OpenAITextResponseChoice{choice},
		Usage:   usage,
	}
}

// claudeInputToArgs serializes a tool_use block's Input into the JSON string
// expected by OpenAI's tool_call.arguments. Input is interface{}: after JSON
// unmarshal it is typically a map[string]interface{}, but callers may also
// populate it with json.RawMessage / []byte (already-valid JSON) or a string.
// fmt.Sprintf("%v", ...) was used previously and corrupted parameters (byte
// arrays like "[123 34 ...]" or Go-native map repr like "map[city:Beijing]").
func claudeInputToArgs(input interface{}) string {
	switch v := input.(type) {
	case nil:
		return "{}"
	case string:
		if v == "" {
			return "{}"
		}
		return v
	case json.RawMessage:
		if len(v) == 0 {
			return "{}"
		}
		return string(v)
	case []byte:
		if len(v) == 0 {
			return "{}"
		}
		return string(v)
	default:
		b, err := json.Marshal(v)
		if err != nil || len(b) == 0 || string(b) == "null" {
			return "{}"
		}
		return string(b)
	}
}

// StreamResponseClaude2OpenAI converts a Claude stream chunk to OpenAI stream format.
func StreamResponseClaude2OpenAI(event *dto.ClaudeStreamEvent) *dto.ChatCompletionsStreamResponse {
	if event == nil {
		return nil
	}

	switch event.Type {
	case "content_block_delta":
		if event.Delta != nil && event.Delta.Text != "" {
			return &dto.ChatCompletionsStreamResponse{
				ID:     "chatcmpl-claude",
				Object: "chat.completion.chunk",
				Choices: []dto.ChatCompletionsStreamResponseChoice{
					{
						Index: 0,
						Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
							Content: &event.Delta.Text,
						},
					},
				},
			}
		}
		if event.Delta != nil && event.Delta.StopReason != "" {
			finishReason := StopReasonToOpenAI(event.Delta.StopReason)
			return &dto.ChatCompletionsStreamResponse{
				ID:     "chatcmpl-claude",
				Object: "chat.completion.chunk",
				Choices: []dto.ChatCompletionsStreamResponseChoice{
					{
						Index:        0,
						Delta:        dto.ChatCompletionsStreamResponseChoiceDelta{},
						FinishReason: &finishReason,
					},
				},
			}
		}
	case "message_stop":
		finishReason := "stop"
		return &dto.ChatCompletionsStreamResponse{
			ID:     "chatcmpl-claude",
			Object: "chat.completion.chunk",
			Choices: []dto.ChatCompletionsStreamResponseChoice{
				{
					Index:        0,
					Delta:        dto.ChatCompletionsStreamResponseChoiceDelta{},
					FinishReason: &finishReason,
				},
			},
		}
	case "message":
		if event.Message != nil {
			usage := UsageFromClaude(event.Message.Usage)
			return &dto.ChatCompletionsStreamResponse{
				ID:     event.Message.ID,
				Object: "chat.completion.chunk",
				Usage:  &usage,
				Choices: []dto.ChatCompletionsStreamResponseChoice{
					{Index: 0, Delta: dto.ChatCompletionsStreamResponseChoiceDelta{}},
				},
			}
		}
	}

	return nil
}

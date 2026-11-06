package convert

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/ai-proxy/ai-proxy-go/dto"
	"github.com/stretchr/testify/assert"
)

// === Responses → OpenAI Conversion Tests ===

func TestResponsesToOpenAI_StringInput(t *testing.T) {
	req := &ResponsesRequest{
		Model:           "gpt-4o",
		Input:           "Hello, world",
		Instructions:    "Be helpful",
		MaxOutputTokens: Ptr(100),
		Temperature:     Ptr(0.7),
	}

	hubReq, err := ResponsesToOpenAI(req)
	assert.NoError(t, err)
	assert.Equal(t, "gpt-4o", hubReq.Model)
	assert.Equal(t, 100, *hubReq.MaxTokens)
	assert.Equal(t, 0.7, *hubReq.Temperature)
	assert.Len(t, hubReq.Messages, 2)
	assert.Equal(t, "system", hubReq.Messages[0].Role)
	assert.Equal(t, "Be helpful", hubReq.Messages[0].Content)
	assert.Equal(t, "user", hubReq.Messages[1].Role)
	assert.Equal(t, "Hello, world", hubReq.Messages[1].Content)
}

func TestResponsesToOpenAI_ArrayInput(t *testing.T) {
	input := []interface{}{
		map[string]interface{}{"type": "message", "role": "user", "content": "Hello"},
		map[string]interface{}{"type": "message", "role": "assistant", "content": "Hi there"},
		map[string]interface{}{"type": "function_call", "call_id": "call_1", "name": "get_weather", "arguments": `{"city":"Tokyo"}`},
		map[string]interface{}{"type": "function_call_output", "call_id": "call_1", "output": "Sunny, 25C"},
	}
	req := &ResponsesRequest{
		Model: "gpt-4o",
		Input: input,
	}

	hubReq, err := ResponsesToOpenAI(req)
	assert.NoError(t, err)
	assert.Len(t, hubReq.Messages, 4)
	assert.Equal(t, "user", hubReq.Messages[0].Role)
	assert.Equal(t, "assistant", hubReq.Messages[1].Role)
	assert.Equal(t, "assistant", hubReq.Messages[2].Role)
	assert.Len(t, hubReq.Messages[2].ToolCalls, 1)
	assert.Equal(t, "get_weather", hubReq.Messages[2].ToolCalls[0].Function.Name)
	assert.Equal(t, "tool", hubReq.Messages[3].Role)
	assert.Equal(t, "call_1", hubReq.Messages[3].ToolCallID)
}

func TestResponsesToOpenAI_WithTools(t *testing.T) {
	req := &ResponsesRequest{
		Model: "gpt-4o",
		Input: "What's the weather?",
		Tools: []ResponsesTool{
			{
				Type:        "function",
				Name:        "get_weather",
				Description: "Get weather info",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"city":{"type":"string","format":"uri"}}}`),
			},
		},
		ToolChoice: "auto",
	}

	hubReq, err := ResponsesToOpenAI(req)
	assert.NoError(t, err)
	assert.Len(t, hubReq.Tools, 1)
	assert.Equal(t, "get_weather", hubReq.Tools[0].Function.Name)
	assert.Equal(t, "auto", hubReq.ToolChoice)

	// Verify format field was stripped from schema
	params := hubReq.Tools[0].Function.Parameters
	cityProps := params["properties"].(map[string]interface{})["city"].(map[string]interface{})
	assert.Equal(t, "string", cityProps["type"])
	_, hasFormat := cityProps["format"]
	assert.False(t, hasFormat, "format field should be stripped from schema")
}

func TestResponsesToOpenAI_WithReasoning(t *testing.T) {
	req := &ResponsesRequest{
		Model:     "o3-mini",
		Input:     "Solve this",
		Reasoning: &ResponsesReasoning{Effort: "high"},
	}

	hubReq, err := ResponsesToOpenAI(req)
	assert.NoError(t, err)
	assert.Equal(t, "high", hubReq.ReasoningEffort)
}

// === OpenAI → Responses Conversion Tests ===

func TestOpenAIToResponses_TextOnly(t *testing.T) {
	openaiResp := &dto.OpenAITextResponse{
		ID:      "chatcmpl-123",
		Object:  "chat.completion",
		Created: 1234567890,
		Model:   "gpt-4o",
		Choices: []dto.OpenAITextResponseChoice{
			{
				Index: 0,
				Message: dto.Message{
					Role:    "assistant",
					Content: "Hello! How can I help?",
				},
				FinishReason: "stop",
			},
		},
		Usage: dto.Usage{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
		},
	}

	resp, err := OpenAIToResponses(openaiResp, "gpt-4o")
	assert.NoError(t, err)
	assert.Equal(t, "chatcmpl-123", resp.ID)
	assert.Equal(t, "completed", resp.Status)
	assert.Len(t, resp.Output, 1)
	assert.Equal(t, "message", resp.Output[0].Type)
	assert.Equal(t, "assistant", resp.Output[0].Role)
	assert.Len(t, resp.Output[0].Content, 1)
	assert.Equal(t, "output_text", resp.Output[0].Content[0].Type)
	assert.Equal(t, "Hello! How can I help?", resp.Output[0].Content[0].Text)
	assert.Equal(t, 10, resp.Usage.InputTokens)
	assert.Equal(t, 5, resp.Usage.OutputTokens)
}

func TestOpenAIToResponses_WithToolCalls(t *testing.T) {
	openaiResp := &dto.OpenAITextResponse{
		ID:      "chatcmpl-456",
		Object:  "chat.completion",
		Created: 1234567890,
		Model:   "gpt-4o",
		Choices: []dto.OpenAITextResponseChoice{
			{
				Index: 0,
				Message: dto.Message{
					Role: "assistant",
					ToolCalls: []dto.ToolCall{
						{
							ID:   "call_1",
							Type: "function",
							Function: dto.FunctionCall{
								Name:      "get_weather",
								Arguments: `{"city":"Tokyo"}`,
							},
						},
					},
				},
				FinishReason: "tool_calls",
			},
		},
		Usage: dto.Usage{PromptTokens: 20, CompletionTokens: 15, TotalTokens: 35},
	}

	resp, err := OpenAIToResponses(openaiResp, "gpt-4o")
	assert.NoError(t, err)
	assert.Equal(t, "completed", resp.Status)
	assert.Len(t, resp.Output, 1)
	assert.Equal(t, "function_call", resp.Output[0].Type)
	assert.Equal(t, "call_1", resp.Output[0].ID)
	assert.Equal(t, "get_weather", resp.Output[0].Name)
	assert.Equal(t, `{"city":"Tokyo"}`, resp.Output[0].Arguments)
}

func TestOpenAIToResponses_LengthFinish(t *testing.T) {
	openaiResp := &dto.OpenAITextResponse{
		ID:      "chatcmpl-789",
		Object:  "chat.completion",
		Created: 1234567890,
		Model:   "gpt-4o",
		Choices: []dto.OpenAITextResponseChoice{
			{
				Index: 0,
				Message: dto.Message{
					Role:    "assistant",
					Content: "This is truncated",
				},
				FinishReason: "length",
			},
		},
		Usage: dto.Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150},
	}

	resp, err := OpenAIToResponses(openaiResp, "gpt-4o")
	assert.NoError(t, err)
	assert.Equal(t, "incomplete", resp.Status)
	assert.NotNil(t, resp.IncompleteDetails)
	assert.Equal(t, "max_output_tokens", resp.IncompleteDetails.Reason)
}

func TestOpenAIToResponses_EmptyChoices(t *testing.T) {
	openaiResp := &dto.OpenAITextResponse{
		ID:      "chatcmpl-empty",
		Object:  "chat.completion",
		Created: 1234567890,
		Model:   "gpt-4o",
		Choices: []dto.OpenAITextResponseChoice{},
		Usage:   dto.Usage{PromptTokens: 5, CompletionTokens: 0, TotalTokens: 5},
	}

	resp, err := OpenAIToResponses(openaiResp, "gpt-4o")
	assert.NoError(t, err)
	assert.Equal(t, "completed", resp.Status)
	assert.Len(t, resp.Output, 0)
}

// === Responses Streaming Tests ===

func TestOpenAIChunkToResponsesEvent_Text(t *testing.T) {
	state := &ResponsesStreamState{}

	chunk := []byte(`{"id":"chatcmpl-1","object":"chat.completion.chunk","created":1,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"Hello"}}]}`)
	events, err := OpenAIChunkToResponsesEvent(chunk, state)
	assert.NoError(t, err)
	assert.True(t, state.HasStarted)
	assert.Len(t, events, 4) // response.created, output_item.added, content_part.added, output_text.delta
	assert.Contains(t, events[0], "response.created")
	assert.Contains(t, events[1], "response.output_item.added")
	assert.Contains(t, events[2], "content_part.added")
	assert.Contains(t, events[3], "output_text.delta")
}

func TestOpenAIChunkToResponsesEvent_Finish(t *testing.T) {
	state := &ResponsesStreamState{HasStarted: true, ResponseID: "resp_1", Model: "gpt-4o"}

	chunk := []byte(`{"id":"chatcmpl-1","object":"chat.completion.chunk","created":1,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
	events, err := OpenAIChunkToResponsesEvent(chunk, state)
	assert.NoError(t, err)
	assert.True(t, len(events) > 0)
	// Should contain response.completed
	foundCompleted := false
	for _, e := range events {
		if contains(e, "response.completed") {
			foundCompleted = true
			break
		}
	}
	assert.True(t, foundCompleted)
}

func TestOpenAIChunkToResponsesEvent_ToolCall(t *testing.T) {
	state := &ResponsesStreamState{HasStarted: true, ResponseID: "resp_1", Model: "gpt-4o"}

	chunk := []byte(`{"id":"chatcmpl-1","object":"chat.completion.chunk","created":1,"model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"get_weather"}}]}}]}`)
	events, err := OpenAIChunkToResponsesEvent(chunk, state)
	assert.NoError(t, err)
	assert.True(t, state.InToolCall)
	assert.Equal(t, "call_1", state.ToolID)
	assert.Equal(t, "get_weather", state.ToolName)
	assert.True(t, len(events) > 0)
	assert.Contains(t, events[0], "response.output_item.added")
	assert.Contains(t, events[0], "function_call")
}

func TestOpenAIChunkToResponsesEvent_ToolCallArgs(t *testing.T) {
	state := &ResponsesStreamState{HasStarted: true, ResponseID: "resp_1", Model: "gpt-4o", InToolCall: true, ToolID: "call_1", ToolName: "get_weather"}

	chunk := []byte(`{"id":"chatcmpl-1","object":"chat.completion.chunk","created":1,"model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":\"Tokyo\"}"}}]}}]}`)
	events, err := OpenAIChunkToResponsesEvent(chunk, state)
	assert.NoError(t, err)
	assert.True(t, len(events) > 0)
	assert.Contains(t, events[0], "response.function_call_arguments.delta")
}

// === Schema Cleanup Tests ===

func TestStripFormatFromSchema(t *testing.T) {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{
				"type":   "string",
				"format": "uri",
			},
			"age": map[string]interface{}{
				"type":   "integer",
				"format": "int64",
			},
		},
		"required": []interface{}{"name"},
	}

	StripFormatFromSchema(schema)

	nameProps := schema["properties"].(map[string]interface{})["name"].(map[string]interface{})
	_, hasFormat := nameProps["format"]
	assert.False(t, hasFormat)
	assert.Equal(t, "string", nameProps["type"])

	ageProps := schema["properties"].(map[string]interface{})["age"].(map[string]interface{})
	_, hasFormat = ageProps["format"]
	assert.False(t, hasFormat)
	assert.Equal(t, "integer", ageProps["type"])
}

func TestStripFormatFromSchema_Nested(t *testing.T) {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"address": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"url": map[string]interface{}{
						"type":   "string",
						"format": "uri",
					},
				},
			},
		},
	}

	StripFormatFromSchema(schema)

	urlProps := schema["properties"].(map[string]interface{})["address"].(map[string]interface{})["properties"].(map[string]interface{})["url"].(map[string]interface{})
	_, hasFormat := urlProps["format"]
	assert.False(t, hasFormat)
}

func TestStripFormatFromRawSchema(t *testing.T) {
	raw := json.RawMessage(`{"type":"object","properties":{"email":{"type":"string","format":"email"}}}`)
	cleaned := StripFormatFromRawSchema(raw)

	var schema map[string]interface{}
	err := json.Unmarshal(cleaned, &schema)
	assert.NoError(t, err)

	emailProps := schema["properties"].(map[string]interface{})["email"].(map[string]interface{})
	_, hasFormat := emailProps["format"]
	assert.False(t, hasFormat)
}

// === NeedsTransform Tests ===

func TestNeedsTransform(t *testing.T) {
	// OpenAI client → OpenAI upstream: no transform needed
	assert.False(t, NeedsTransform(ClientFormatOpenAI, "openai"))
	// OpenAI client → Claude upstream: needs transform
	assert.True(t, NeedsTransform(ClientFormatOpenAI, "claude"))

	// Anthropic client → Anthropic upstream: no transform needed
	assert.False(t, NeedsTransform(ClientFormatAnthropic, "anthropic"))
	// Anthropic client → OpenAI upstream: needs transform
	assert.True(t, NeedsTransform(ClientFormatAnthropic, "openai"))
	// Anthropic client → Gemini upstream: needs transform
	assert.True(t, NeedsTransform(ClientFormatAnthropic, "gemini"))

	// Responses client → any upstream: needs transform
	assert.True(t, NeedsTransform(ClientFormatResponses, "openai"))
	assert.True(t, NeedsTransform(ClientFormatResponses, "claude"))
	assert.True(t, NeedsTransform(ClientFormatResponses, "gemini"))

	// Unknown format → any upstream: needs transform
	assert.True(t, NeedsTransform(ClientFormatUnknown, "openai"))
}

// === Responses Error Helper Tests ===

func TestNewResponsesError(t *testing.T) {
	resp := NewResponsesError("test message", "test_type", "test_code")
	assert.Equal(t, "failed", resp.Status)
	assert.NotNil(t, resp.Error)
	assert.Equal(t, "test message", resp.Error.Message)
	assert.Equal(t, "test_type", resp.Error.Type)
	assert.Equal(t, "test_code", resp.Error.Code)
}

func TestResponsesErrorJSON(t *testing.T) {
	b := ResponsesErrorJSON("test", "type", "code")
	var resp ResponsesResponse
	err := json.Unmarshal(b, &resp)
	assert.NoError(t, err)
	assert.Equal(t, "failed", resp.Status)
	assert.Equal(t, "test", resp.Error.Message)
}

// Helper
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsAt(s, substr))
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestResponsesToOpenAI_DropsNonFunctionTools(t *testing.T) {
	// Codex CLI sends a web_search-typed tool (no name); converted chat
	// upstreams reject any non-function tool type, and an empty-name function
	// tool is equally invalid. Only named function tools may survive.
	req := &ResponsesRequest{
		Model: "kimi-k3",
		Input: "hi",
		Tools: []ResponsesTool{
			{Type: "function", Name: "exec_command", Description: "run", Parameters: json.RawMessage(`{"type":"object"}`)},
			{Type: "web_search"},
			{Type: "custom", Name: "local_shell"},
			{Type: "function", Name: ""},
		},
	}

	hubReq, err := ResponsesToOpenAI(req)
	assert.NoError(t, err)
	assert.Len(t, hubReq.Tools, 1)
	assert.Equal(t, "exec_command", hubReq.Tools[0].Function.Name)
}

func TestResponsesToOpenAI_MergesAssistantTextIntoToolCallTurn(t *testing.T) {
	// Codex CLI orders a turn's input items as function_call → message(text)
	// → function_call_output. Converted naively that yields
	// assistant(tool_calls), assistant(text), tool(...) which chat upstreams
	// reject (tool_calls must be followed by tool messages). The text message
	// must fold into the tool-carrying assistant message. Also covers
	// output_text parts (assistant history content) being kept as text.
	input := []interface{}{
		map[string]interface{}{"type": "message", "role": "user", "content": []interface{}{
			map[string]interface{}{"type": "input_text", "text": "run ls"},
		}},
		map[string]interface{}{"type": "function_call", "name": "exec_command", "call_id": "c1", "arguments": `{"cmd":"ls"}`},
		map[string]interface{}{"type": "message", "role": "assistant", "content": []interface{}{
			map[string]interface{}{"type": "output_text", "text": "I'll run it."},
		}},
		map[string]interface{}{"type": "function_call_output", "call_id": "c1", "output": "file.txt"},
	}
	req := &ResponsesRequest{Model: "kimi-k3", Input: input}

	hubReq, err := ResponsesToOpenAI(req)
	assert.NoError(t, err)

	var roles []string
	for _, m := range hubReq.Messages {
		roles = append(roles, m.Role)
	}
	assert.Equal(t, []string{"user", "assistant", "tool"}, roles)
	assert.Len(t, hubReq.Messages[1].ToolCalls, 1)
	assert.Contains(t, fmt.Sprint(hubReq.Messages[1].Content), "I'll run it.")
	assert.Equal(t, "c1", hubReq.Messages[2].ToolCallID)
}

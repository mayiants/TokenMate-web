package dto

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUsage_MarshalUnmarshal(t *testing.T) {
	usage := Usage{
		PromptTokens:     1000,
		CompletionTokens: 500,
		TotalTokens:      1500,
		InputTokens:      1000,
		OutputTokens:     500,
		PromptTokensDetails: InputTokenDetails{
			CachedTokens:         200,
			CachedCreationTokens: 50,
			TextTokens:           700,
			AudioTokens:          100,
			ImageTokens:          0,
		},
		CompletionTokenDetails: OutputTokenDetails{
			TextTokens:      400,
			AudioTokens:     50,
			ReasoningTokens: 50,
		},
		ClaudeCacheCreation5mTokens: 30,
		ClaudeCacheCreation1hTokens: 20,
	}

	data, err := json.Marshal(usage)
	require.NoError(t, err)

	var decoded Usage
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, 1000, decoded.PromptTokens)
	assert.Equal(t, 500, decoded.CompletionTokens)
	assert.Equal(t, 1500, decoded.TotalTokens)
	assert.Equal(t, 200, decoded.PromptTokensDetails.CachedTokens)
	assert.Equal(t, 50, decoded.PromptTokensDetails.CachedCreationTokens)
	assert.Equal(t, 30, decoded.ClaudeCacheCreation5mTokens)
	assert.Equal(t, 20, decoded.ClaudeCacheCreation1hTokens)
}

func TestInputTokenDetails_MarshalUnmarshal(t *testing.T) {
	details := InputTokenDetails{
		CachedTokens:         300,
		CachedCreationTokens: 100,
		TextTokens:           500,
		AudioTokens:          50,
		ImageTokens:          25,
	}

	data, err := json.Marshal(details)
	require.NoError(t, err)

	var decoded InputTokenDetails
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, 300, decoded.CachedTokens)
	assert.Equal(t, 100, decoded.CachedCreationTokens)
	assert.Equal(t, 500, decoded.TextTokens)
	assert.Equal(t, 50, decoded.AudioTokens)
	assert.Equal(t, 25, decoded.ImageTokens)
}

func TestOutputTokenDetails_MarshalUnmarshal(t *testing.T) {
	details := OutputTokenDetails{
		TextTokens:      400,
		AudioTokens:     50,
		ImageTokens:     10,
		ReasoningTokens: 100,
	}

	data, err := json.Marshal(details)
	require.NoError(t, err)

	var decoded OutputTokenDetails
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, 400, decoded.TextTokens)
	assert.Equal(t, 50, decoded.AudioTokens)
	assert.Equal(t, 10, decoded.ImageTokens)
	assert.Equal(t, 100, decoded.ReasoningTokens)
}

func TestGeneralOpenAIRequest_MarshalUnmarshal(t *testing.T) {
	tests := []struct {
		name string
		req  GeneralOpenAIRequest
	}{
		{
			name: "basic chat request",
			req: GeneralOpenAIRequest{
				Model:       "gpt-4",
				Temperature: floatPtr(0.7),
				MaxTokens:   intPtr(1024),
				Stream:      boolPtr(true),
				Messages: []Message{
					{Role: "system", Content: "You are helpful."},
					{Role: "user", Content: "Hello!"},
				},
			},
		},
		{
			name: "with top_k and top_p",
			req: GeneralOpenAIRequest{
				Model:  "gpt-4",
				TopP:   floatPtr(0.9),
				TopK:   intPtr(40),
				Stream: boolPtr(false),
				Messages: []Message{
					{Role: "user", Content: "Explain Go concurrency."},
				},
			},
		},
		{
			name: "with tools",
			req: GeneralOpenAIRequest{
				Model: "gpt-4",
				Messages: []Message{
					{Role: "user", Content: "What is the weather?"},
				},
				Tools: []ToolCallRequest{
					{
						Type: "function",
						Function: FunctionRequest{
							Name:        "get_weather",
							Description: "Get current weather",
							Parameters: map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"location": map[string]interface{}{"type": "string"},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "with stream options",
			req: GeneralOpenAIRequest{
				Model:  "gpt-4",
				Stream: boolPtr(true),
				StreamOptions: &StreamOptions{
					IncludeUsage: true,
				},
				Messages: []Message{
					{Role: "user", Content: "Hello"},
				},
			},
		},
		{
			name: "with reasoning effort",
			req: GeneralOpenAIRequest{
				Model:           "o1-preview",
				ReasoningEffort: "high",
				Messages: []Message{
					{Role: "user", Content: "Solve this complex problem."},
				},
			},
		},
		{
			name: "with frequency and presence penalty",
			req: GeneralOpenAIRequest{
				Model:            "gpt-4",
				FrequencyPenalty: floatPtr(0.5),
				PresencePenalty:  floatPtr(0.3),
				Messages: []Message{
					{Role: "user", Content: "Be creative."},
				},
			},
		},
		{
			name: "with seed and user",
			req: GeneralOpenAIRequest{
				Model: "gpt-4",
				Seed:  intPtr(42),
				User:  "user-123",
				Messages: []Message{
					{Role: "user", Content: "Hello"},
				},
			},
		},
		{
			name: "with response_format",
			req: GeneralOpenAIRequest{
				Model: "gpt-4",
				ResponseFormat: map[string]interface{}{
					"type": "json_object",
				},
				Messages: []Message{
					{Role: "user", Content: "Give me JSON."},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.req)
			require.NoError(t, err)

			var decoded GeneralOpenAIRequest
			err = json.Unmarshal(data, &decoded)
			require.NoError(t, err)

			assert.Equal(t, tt.req.Model, decoded.Model)
			assert.Len(t, decoded.Messages, len(tt.req.Messages))
			if len(tt.req.Messages) > 0 {
				assert.Equal(t, tt.req.Messages[0].Role, decoded.Messages[0].Role)
			}
			if tt.req.Stream != nil {
				require.NotNil(t, decoded.Stream)
				assert.Equal(t, *tt.req.Stream, *decoded.Stream)
			}
			if tt.req.StreamOptions != nil {
				require.NotNil(t, decoded.StreamOptions)
				assert.Equal(t, tt.req.StreamOptions.IncludeUsage, decoded.StreamOptions.IncludeUsage)
			}
			if tt.req.ReasoningEffort != "" {
				assert.Equal(t, tt.req.ReasoningEffort, decoded.ReasoningEffort)
			}
		})
	}
}

func TestMessage_MarshalUnmarshal(t *testing.T) {
	tests := []struct {
		name string
		msg  Message
	}{
		{
			name: "simple user message",
			msg:  Message{Role: "user", Content: "Hello, world!"},
		},
		{
			name: "system message",
			msg:  Message{Role: "system", Content: "You are a helpful assistant."},
		},
		{
			name: "assistant with reasoning content",
			msg: Message{
				Role:             "assistant",
				Content:          "The answer is 42.",
				ReasoningContent: strPtr("Let me think about this... The meaning of life... 42."),
			},
		},
		{
			name: "with tool calls",
			msg: Message{
				Role:    "assistant",
				Content: nil,
				ToolCalls: []ToolCall{
					{
						Index: 0,
						ID:    "call_abc123",
						Type:  "function",
						Function: FunctionCall{
							Name:      "get_weather",
							Arguments: `{"location":"NYC"}`,
						},
					},
				},
			},
		},
		{
			name: "tool response",
			msg: Message{
				Role:       "tool",
				Content:    "Sunny, 72°F",
				ToolCallID: "call_abc123",
			},
		},
		{
			name: "with name",
			msg: Message{
				Role:    "user",
				Name:    strPtr("Alice"),
				Content: "Hello from Alice!",
			},
		},
		{
			name: "multi-modal content array",
			msg: Message{
				Role: "user",
				Content: []MediaContent{
					{Type: "text", Text: "What is in this image?"},
					{Type: "image_url", ImageURL: &MessageImageURL{
						URL:    "https://example.com/image.jpg",
						Detail: "high",
					}},
				},
			},
		},
		{
			name: "content with cache control",
			msg: Message{
				Role: "user",
				Content: []MediaContent{
					{
						Type: "text",
						Text: "Cached prompt content",
						CacheControl: &CacheControl{
							Type: "ephemeral",
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.msg)
			require.NoError(t, err)

			var decoded Message
			err = json.Unmarshal(data, &decoded)
			require.NoError(t, err)

			assert.Equal(t, tt.msg.Role, decoded.Role)
			if tt.msg.ToolCallID != "" {
				assert.Equal(t, tt.msg.ToolCallID, decoded.ToolCallID)
			}
			if len(tt.msg.ToolCalls) > 0 {
				assert.Len(t, decoded.ToolCalls, len(tt.msg.ToolCalls))
				assert.Equal(t, tt.msg.ToolCalls[0].ID, decoded.ToolCalls[0].ID)
			}
			if tt.msg.ReasoningContent != nil {
				require.NotNil(t, decoded.ReasoningContent)
				assert.Equal(t, *tt.msg.ReasoningContent, *decoded.ReasoningContent)
			}
			if tt.msg.Name != nil {
				require.NotNil(t, decoded.Name)
				assert.Equal(t, *tt.msg.Name, *decoded.Name)
			}
		})
	}
}

func TestMediaContent_MarshalUnmarshal(t *testing.T) {
	tests := []struct {
		name    string
		content MediaContent
	}{
		{
			name: "text content",
			content: MediaContent{
				Type: "text",
				Text: "Describe this image:",
			},
		},
		{
			name: "image url content",
			content: MediaContent{
				Type: "image_url",
				ImageURL: &MessageImageURL{
					URL:    "https://example.com/photo.jpg",
					Detail: "auto",
				},
			},
		},
		{
			name: "with cache control",
			content: MediaContent{
				Type: "text",
				Text: "Important context here",
				CacheControl: &CacheControl{
					Type: "ephemeral",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.content)
			require.NoError(t, err)

			var decoded MediaContent
			err = json.Unmarshal(data, &decoded)
			require.NoError(t, err)

			assert.Equal(t, tt.content.Type, decoded.Type)
			if tt.content.Text != "" {
				assert.Equal(t, tt.content.Text, decoded.Text)
			}
			if tt.content.ImageURL != nil {
				require.NotNil(t, decoded.ImageURL)
				assert.Equal(t, tt.content.ImageURL.URL, decoded.ImageURL.URL)
				assert.Equal(t, tt.content.ImageURL.Detail, decoded.ImageURL.Detail)
			}
			if tt.content.CacheControl != nil {
				require.NotNil(t, decoded.CacheControl)
				assert.Equal(t, tt.content.CacheControl.Type, decoded.CacheControl.Type)
			}
		})
	}
}

func TestToolCallRequest_MarshalUnmarshal(t *testing.T) {
	req := ToolCallRequest{
		Type: "function",
		Function: FunctionRequest{
			Name:        "calculate",
			Description: "Performs a calculation",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"expression": map[string]interface{}{"type": "string"},
				},
				"required": []interface{}{"expression"},
			},
		},
	}

	data, err := json.Marshal(req)
	require.NoError(t, err)

	var decoded ToolCallRequest
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, "function", decoded.Type)
	assert.Equal(t, "calculate", decoded.Function.Name)
	assert.Equal(t, "Performs a calculation", decoded.Function.Description)
}

func TestToolCall_MarshalUnmarshal(t *testing.T) {
	call := ToolCall{
		Index: 0,
		ID:    "call_abc123",
		Type:  "function",
		Function: FunctionCall{
			Name:      "get_weather",
			Arguments: `{"location":"San Francisco","unit":"fahrenheit"}`,
		},
	}

	data, err := json.Marshal(call)
	require.NoError(t, err)

	var decoded ToolCall
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, 0, decoded.Index)
	assert.Equal(t, "call_abc123", decoded.ID)
	assert.Equal(t, "function", decoded.Type)
	assert.Equal(t, "get_weather", decoded.Function.Name)
	assert.Equal(t, `{"location":"San Francisco","unit":"fahrenheit"}`, decoded.Function.Arguments)
}

func TestOpenAITextResponse_MarshalUnmarshal(t *testing.T) {
	jsonStr := `{
		"id": "chatcmpl-abc123",
		"object": "chat.completion",
		"created": 1700000000,
		"model": "gpt-4",
		"choices": [
			{
				"index": 0,
				"message": {
					"role": "assistant",
					"content": "Hello! How can I help you today?"
				},
				"finish_reason": "stop"
			}
		],
		"usage": {
			"prompt_tokens": 20,
			"completion_tokens": 8,
			"total_tokens": 28
		}
	}`

	var resp OpenAITextResponse
	err := json.Unmarshal([]byte(jsonStr), &resp)
	require.NoError(t, err)

	assert.Equal(t, "chatcmpl-abc123", resp.ID)
	assert.Equal(t, "chat.completion", resp.Object)
	assert.Equal(t, int64(1700000000), resp.Created)
	assert.Equal(t, "gpt-4", resp.Model)
	assert.Len(t, resp.Choices, 1)
	assert.Equal(t, "assistant", resp.Choices[0].Message.Role)
	assert.Equal(t, "Hello! How can I help you today?", resp.Choices[0].Message.Content)
	assert.Equal(t, "stop", resp.Choices[0].FinishReason)
	assert.Equal(t, 20, resp.Usage.PromptTokens)
	assert.Equal(t, 8, resp.Usage.CompletionTokens)
	assert.Equal(t, 28, resp.Usage.TotalTokens)
}

func TestOpenAITextResponse_WithError(t *testing.T) {
	jsonStr := `{
		"error": {
			"message": "Rate limit exceeded",
			"type": "rate_limit_error",
			"code": "rate_limit_exceeded"
		}
	}`

	var resp OpenAITextResponse
	err := json.Unmarshal([]byte(jsonStr), &resp)
	require.NoError(t, err)

	assert.NotNil(t, resp.Error)
}

func TestChatCompletionsStreamResponse_MarshalUnmarshal(t *testing.T) {
	jsonStr := `{
		"id": "chatcmpl-abc123",
		"object": "chat.completion.chunk",
		"created": 1700000000,
		"model": "gpt-4",
		"choices": [
			{
				"index": 0,
				"delta": {
					"content": "Hello",
					"role": "assistant"
				},
				"finish_reason": null
			}
		]
	}`

	var chunk ChatCompletionsStreamResponse
	err := json.Unmarshal([]byte(jsonStr), &chunk)
	require.NoError(t, err)

	assert.Equal(t, "chatcmpl-abc123", chunk.ID)
	assert.Equal(t, "chat.completion.chunk", chunk.Object)
	assert.Len(t, chunk.Choices, 1)
	assert.Equal(t, 0, chunk.Choices[0].Index)
	require.NotNil(t, chunk.Choices[0].Delta.Content)
	assert.Equal(t, "Hello", *chunk.Choices[0].Delta.Content)
	assert.Equal(t, "assistant", chunk.Choices[0].Delta.Role)
	assert.Nil(t, chunk.Choices[0].FinishReason)
}

func TestChatCompletionsStreamResponse_WithFinishReason(t *testing.T) {
	jsonStr := `{
		"id": "chatcmpl-def456",
		"object": "chat.completion.chunk",
		"created": 1700000001,
		"model": "gpt-4",
		"choices": [
			{
				"index": 0,
				"delta": {},
				"finish_reason": "stop"
			}
		],
		"usage": {
			"prompt_tokens": 10,
			"completion_tokens": 5,
			"total_tokens": 15
		}
	}`

	var chunk ChatCompletionsStreamResponse
	err := json.Unmarshal([]byte(jsonStr), &chunk)
	require.NoError(t, err)

	require.NotNil(t, chunk.Choices[0].FinishReason)
	assert.Equal(t, "stop", *chunk.Choices[0].FinishReason)
	require.NotNil(t, chunk.Usage)
	assert.Equal(t, 10, chunk.Usage.PromptTokens)
	assert.Equal(t, 5, chunk.Usage.CompletionTokens)
}

func TestChatCompletionsStreamResponse_WithReasoningContent(t *testing.T) {
	jsonStr := `{
		"id": "chatcmpl-ghi789",
		"object": "chat.completion.chunk",
		"created": 1700000002,
		"model": "gpt-4",
		"choices": [
			{
				"index": 0,
				"delta": {
					"reasoning_content": "Let me think step by step..."
				}
			}
		]
	}`

	var chunk ChatCompletionsStreamResponse
	err := json.Unmarshal([]byte(jsonStr), &chunk)
	require.NoError(t, err)

	require.NotNil(t, chunk.Choices[0].Delta.ReasoningContent)
	assert.Equal(t, "Let me think step by step...", *chunk.Choices[0].Delta.ReasoningContent)
}

func TestStreamOptions_MarshalUnmarshal(t *testing.T) {
	opts := StreamOptions{IncludeUsage: true}

	data, err := json.Marshal(opts)
	require.NoError(t, err)

	var decoded StreamOptions
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.True(t, decoded.IncludeUsage)
}

func TestMessageImageURL_MarshalUnmarshal(t *testing.T) {
	img := MessageImageURL{
		URL:    "https://example.com/photo.png",
		Detail: "high",
	}

	data, err := json.Marshal(img)
	require.NoError(t, err)

	var decoded MessageImageURL
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, "https://example.com/photo.png", decoded.URL)
	assert.Equal(t, "high", decoded.Detail)
}

func TestOpenAIRequestRoundtrip(t *testing.T) {
	original := GeneralOpenAIRequest{
		Model:            "gpt-4",
		Temperature:      floatPtr(0.7),
		TopP:             floatPtr(0.95),
		MaxTokens:        intPtr(2048),
		Stream:           boolPtr(true),
		FrequencyPenalty: floatPtr(0.2),
		PresencePenalty:  floatPtr(0.1),
		Seed:             intPtr(12345),
		User:             "user-abc",
		ReasoningEffort:  "medium",
		ResponseFormat: map[string]interface{}{
			"type": "json_object",
		},
		Messages: []Message{
			{
				Role: "system",
				Content: []MediaContent{
					{
						Type:         "text",
						Text:         "You are a JSON assistant.",
						CacheControl: &CacheControl{Type: "ephemeral"},
					},
				},
			},
			{
				Role:    "user",
				Content: "Tell me about Go.",
			},
		},
		Tools: []ToolCallRequest{
			{
				Type: "function",
				Function: FunctionRequest{
					Name:        "search",
					Description: "Search the web",
					Parameters: map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{},
					},
				},
			},
		},
		StreamOptions: &StreamOptions{IncludeUsage: true},
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var restored GeneralOpenAIRequest
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	assert.Equal(t, original.Model, restored.Model)
	require.NotNil(t, restored.Temperature)
	assert.Equal(t, *original.Temperature, *restored.Temperature)
	require.NotNil(t, restored.TopP)
	assert.Equal(t, *original.TopP, *restored.TopP)
	require.NotNil(t, restored.MaxTokens)
	assert.Equal(t, *original.MaxTokens, *restored.MaxTokens)
	require.NotNil(t, restored.Stream)
	assert.Equal(t, *original.Stream, *restored.Stream)
	assert.Equal(t, original.ReasoningEffort, restored.ReasoningEffort)
	assert.Len(t, restored.Messages, 2)
	require.NotNil(t, restored.StreamOptions)
	assert.True(t, restored.StreamOptions.IncludeUsage)
}

func TestUsage_UnmarshalAnthropicCacheFields(t *testing.T) {
	// OpenRouter/TokenRouter fronting Claude models return Anthropic-style
	// top-level cache fields; they must land in PromptTokensDetails.
	raw := `{
		"prompt_tokens": 5000,
		"completion_tokens": 120,
		"total_tokens": 5120,
		"cache_read_input_tokens": 4800,
		"cache_creation_input_tokens": 150
	}`
	var u Usage
	require.NoError(t, json.Unmarshal([]byte(raw), &u))
	assert.Equal(t, 4800, u.PromptTokensDetails.CachedTokens)
	assert.Equal(t, 150, u.PromptTokensDetails.CachedCreationTokens)

	// OpenAI-native prompt_tokens_details win when both shapes are present.
	raw2 := `{
		"prompt_tokens": 5000,
		"completion_tokens": 120,
		"total_tokens": 5120,
		"prompt_tokens_details": {"cached_tokens": 3000, "cached_creation_tokens": 90},
		"cache_read_input_tokens": 4800,
		"cache_creation_input_tokens": 150
	}`
	var u2 Usage
	require.NoError(t, json.Unmarshal([]byte(raw2), &u2))
	assert.Equal(t, 3000, u2.PromptTokensDetails.CachedTokens)
	assert.Equal(t, 90, u2.PromptTokensDetails.CachedCreationTokens)
}

func TestUsage_UnmarshalOpenRouterCacheWriteTokens(t *testing.T) {
	// OpenRouter's documented cache-write field inside prompt_tokens_details.
	raw := `{
		"prompt_tokens": 10339,
		"completion_tokens": 60,
		"total_tokens": 10399,
		"prompt_tokens_details": {"cached_tokens": 10318, "cache_write_tokens": 0}
	}`
	var u Usage
	require.NoError(t, json.Unmarshal([]byte(raw), &u))
	assert.Equal(t, 10318, u.PromptTokensDetails.CachedTokens)

	raw2 := `{
		"prompt_tokens": 5000,
		"completion_tokens": 10,
		"total_tokens": 5010,
		"prompt_tokens_details": {"cached_tokens": 0, "cache_write_tokens": 4900}
	}`
	var u2 Usage
	require.NoError(t, json.Unmarshal([]byte(raw2), &u2))
	assert.Equal(t, 4900, u2.PromptTokensDetails.CachedCreationTokens,
		"cache_write_tokens must normalize into CachedCreationTokens")
}

package dto

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func intPtr(i int) *int           { return &i }
func floatPtr(f float64) *float64 { return &f }
func boolPtr(b bool) *bool        { return &b }
func strPtr(s string) *string     { return &s }

func TestClaudeRequest_MarshalUnmarshal(t *testing.T) {
	tests := []struct {
		name string
		req  ClaudeRequest
	}{
		{
			name: "basic request",
			req: ClaudeRequest{
				Model:       "claude-3-opus-20240229",
				MaxTokens:   intPtr(1024),
				Temperature: floatPtr(0.7),
				Stream:      boolPtr(true),
				Messages: []ClaudeMessage{
					{Role: "user", Content: "Hello, Claude!"},
				},
			},
		},
		{
			name: "system prompt as string",
			req: ClaudeRequest{
				Model:     "claude-3-sonnet-20240229",
				MaxTokens: intPtr(2048),
				System:    "You are a helpful assistant.",
				Messages: []ClaudeMessage{
					{Role: "user", Content: "What is Go?"},
				},
			},
		},
		{
			name: "system prompt as array",
			req: ClaudeRequest{
				Model:     "claude-3-haiku-20240307",
				MaxTokens: intPtr(512),
				System: []interface{}{
					map[string]interface{}{"type": "text", "text": "You are a helpful assistant."},
				},
				Messages: []ClaudeMessage{
					{Role: "user", Content: "Hi"},
				},
			},
		},
		{
			name: "with stop sequences",
			req: ClaudeRequest{
				Model:         "claude-3-opus-20240229",
				MaxTokens:     intPtr(1024),
				StopSequences: []string{"\n\nHuman:", "\n\nAssistant:"},
				Messages: []ClaudeMessage{
					{Role: "user", Content: "Hello"},
				},
			},
		},
		{
			name: "with top_k and top_p",
			req: ClaudeRequest{
				Model:     "claude-3-sonnet-20240229",
				MaxTokens: intPtr(1024),
				TopK:      intPtr(10),
				TopP:      floatPtr(0.9),
				Messages: []ClaudeMessage{
					{Role: "user", Content: "Test"},
				},
			},
		},
		{
			name: "null/zero values omitted",
			req: ClaudeRequest{
				Model: "claude-3-haiku-20240307",
				Messages: []ClaudeMessage{
					{Role: "user", Content: "Hello"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.req)
			require.NoError(t, err)

			var decoded ClaudeRequest
			err = json.Unmarshal(data, &decoded)
			require.NoError(t, err)

			assert.Equal(t, tt.req.Model, decoded.Model)
			assert.Equal(t, tt.req.System, decoded.System)
			assert.Len(t, decoded.Messages, len(tt.req.Messages))
			if len(tt.req.Messages) > 0 {
				assert.Equal(t, tt.req.Messages[0].Role, decoded.Messages[0].Role)
			}
			if tt.req.Stream != nil {
				require.NotNil(t, decoded.Stream)
				assert.Equal(t, *tt.req.Stream, *decoded.Stream)
			}
			if tt.req.MaxTokens != nil {
				require.NotNil(t, decoded.MaxTokens)
				assert.Equal(t, *tt.req.MaxTokens, *decoded.MaxTokens)
			}
			if tt.req.Temperature != nil {
				require.NotNil(t, decoded.Temperature)
				assert.Equal(t, *tt.req.Temperature, *decoded.Temperature)
			}
		})
	}
}

func TestClaudeMessage_MarshalUnmarshal(t *testing.T) {
	tests := []struct {
		name string
		msg  ClaudeMessage
	}{
		{
			name: "text content",
			msg:  ClaudeMessage{Role: "user", Content: "Hello"},
		},
		{
			name: "array content",
			msg: ClaudeMessage{
				Role: "user",
				Content: []ClaudeContent{
					{Type: "text", Text: strPtr("Hello")},
				},
			},
		},
		{
			name: "assistant role",
			msg:  ClaudeMessage{Role: "assistant", Content: "Sure, I can help with that."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.msg)
			require.NoError(t, err)

			var decoded ClaudeMessage
			err = json.Unmarshal(data, &decoded)
			require.NoError(t, err)

			assert.Equal(t, tt.msg.Role, decoded.Role)
		})
	}
}

func TestClaudeContent_MarshalUnmarshal(t *testing.T) {
	tests := []struct {
		name    string
		content ClaudeContent
	}{
		{
			name: "text block",
			content: ClaudeContent{
				Type: "text",
				Text: strPtr("Hello, world!"),
			},
		},
		{
			name: "image block with base64",
			content: ClaudeContent{
				Type: "image",
				Source: &ClaudeSource{
					Type:      "base64",
					MediaType: "image/jpeg",
					Data:      "base64encodeddata",
				},
			},
		},
		{
			name: "image block with URL",
			content: ClaudeContent{
				Type: "image",
				Source: &ClaudeSource{
					Type:      "url",
					MediaType: "image/png",
					URL:       "https://example.com/image.png",
				},
			},
		},
		{
			name: "tool use block",
			content: ClaudeContent{
				Type:  "tool_use",
				ID:    "toolu_01A09qMFt1r",
				Name:  "get_weather",
				Input: map[string]interface{}{"location": "San Francisco"},
			},
		},
		{
			name: "tool result block",
			content: ClaudeContent{
				Type:      "tool_result",
				ToolUseID: "toolu_01A09qMFt1r",
				Content:   "Sunny, 72°F",
			},
		},
		{
			name: "with cache control",
			content: ClaudeContent{
				Type: "text",
				Text: strPtr("cached prompt"),
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

			var decoded ClaudeContent
			err = json.Unmarshal(data, &decoded)
			require.NoError(t, err)

			assert.Equal(t, tt.content.Type, decoded.Type)
			if tt.content.Text != nil {
				require.NotNil(t, decoded.Text)
				assert.Equal(t, *tt.content.Text, *decoded.Text)
			}
			if tt.content.Source != nil {
				require.NotNil(t, decoded.Source)
				assert.Equal(t, tt.content.Source.Type, decoded.Source.Type)
				assert.Equal(t, tt.content.Source.MediaType, decoded.Source.MediaType)
				assert.Equal(t, tt.content.Source.Data, decoded.Source.Data)
			}
			if tt.content.ToolUseID != "" {
				assert.Equal(t, tt.content.ToolUseID, decoded.ToolUseID)
			}
			if tt.content.CacheControl != nil {
				require.NotNil(t, decoded.CacheControl)
				assert.Equal(t, tt.content.CacheControl.Type, decoded.CacheControl.Type)
			}
		})
	}
}

func TestThinking_MarshalUnmarshal(t *testing.T) {
	thinking := Thinking{
		Type:         "enabled",
		BudgetTokens: intPtr(2000),
	}

	data, err := json.Marshal(thinking)
	require.NoError(t, err)

	var decoded Thinking
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, thinking.Type, decoded.Type)
	require.NotNil(t, decoded.BudgetTokens)
	assert.Equal(t, *thinking.BudgetTokens, *decoded.BudgetTokens)
}

func TestClaudeResponse_MarshalUnmarshal(t *testing.T) {
	jsonStr := `{
		"id": "msg_01AbCdEfGhIj",
		"type": "message",
		"role": "assistant",
		"content": [
			{"type": "text", "text": "Hello! How can I help you?"},
			{"type": "tool_use", "id": "toolu_01", "name": "get_weather", "input": {"location": "NYC"}}
		],
		"model": "claude-3-opus-20240229",
		"stop_reason": "end_turn",
		"usage": {
			"input_tokens": 100,
			"output_tokens": 50,
			"cache_creation_input_tokens": 20,
			"cache_read_input_tokens": 10
		}
	}`

	var resp ClaudeResponse
	err := json.Unmarshal([]byte(jsonStr), &resp)
	require.NoError(t, err)

	assert.Equal(t, "msg_01AbCdEfGhIj", resp.ID)
	assert.Equal(t, "message", resp.Type)
	assert.Equal(t, "assistant", resp.Role)
	assert.Equal(t, "claude-3-opus-20240229", resp.Model)
	assert.Equal(t, "end_turn", resp.StopReason)
	assert.Len(t, resp.Content, 2)
	assert.Equal(t, "text", resp.Content[0].Type)
	assert.Equal(t, "tool_use", resp.Content[1].Type)
	assert.Equal(t, 100, resp.Usage.InputTokens)
	assert.Equal(t, 50, resp.Usage.OutputTokens)
	assert.Equal(t, 20, resp.Usage.CacheCreationInputTokens)
	assert.Equal(t, 10, resp.Usage.CacheReadInputTokens)
}

func TestClaudeUsage_MarshalUnmarshal(t *testing.T) {
	usage := ClaudeUsage{
		InputTokens:              150,
		OutputTokens:             75,
		CacheCreationInputTokens: 30,
		CacheReadInputTokens:     15,
		CacheCreation: &ClaudeCacheCreation{
			Ephemeral5mInputTokens: 20,
			Ephemeral1hInputTokens: 10,
		},
	}

	data, err := json.Marshal(usage)
	require.NoError(t, err)

	var decoded ClaudeUsage
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, usage.InputTokens, decoded.InputTokens)
	assert.Equal(t, usage.OutputTokens, decoded.OutputTokens)
	assert.Equal(t, usage.CacheCreationInputTokens, decoded.CacheCreationInputTokens)
	require.NotNil(t, decoded.CacheCreation)
	assert.Equal(t, 20, decoded.CacheCreation.Ephemeral5mInputTokens)
	assert.Equal(t, 10, decoded.CacheCreation.Ephemeral1hInputTokens)
}

func TestClaudeStreamEvent_MarshalUnmarshal(t *testing.T) {
	tests := []struct {
		name    string
		jsonStr string
	}{
		{
			name: "content block start",
			jsonStr: `{
				"type": "content_block_start",
				"index": 0,
				"content_block": {"type": "text", "text": ""}
			}`,
		},
		{
			name: "content block delta",
			jsonStr: `{
				"type": "content_block_delta",
				"index": 0,
				"delta": {"type": "text_delta", "text": "Hello"}
			}`,
		},
		{
			name: "message delta with stop reason",
			jsonStr: `{
				"type": "message_delta",
				"delta": {"stop_reason": "end_turn"},
				"usage": {"input_tokens": 50, "output_tokens": 25}
			}`,
		},
		{
			name: "message start",
			jsonStr: `{
				"type": "message_start",
				"message": {
					"id": "msg_01", "type": "message", "role": "assistant",
					"model": "claude-3-opus-20240229",
					"content": [],
					"usage": {"input_tokens": 0, "output_tokens": 0}
				}
			}`,
		},
		{
			name:    "ping event",
			jsonStr: `{"type": "ping"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var event ClaudeStreamEvent
			err := json.Unmarshal([]byte(tt.jsonStr), &event)
			require.NoError(t, err)
			assert.NotEmpty(t, event.Type)
		})
	}
}

func TestClaudeDelta_MarshalUnmarshal(t *testing.T) {
	delta := ClaudeDelta{
		Type:         "text_delta",
		Text:         "Hello, world!",
		StopReason:   "end_turn",
		StopSequence: "\n\nHuman:",
	}

	data, err := json.Marshal(delta)
	require.NoError(t, err)

	var decoded ClaudeDelta
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, delta.Type, decoded.Type)
	assert.Equal(t, delta.Text, decoded.Text)
	assert.Equal(t, delta.StopReason, decoded.StopReason)
	assert.Equal(t, delta.StopSequence, decoded.StopSequence)
}

func TestClaudeWebSearchTool_MarshalUnmarshal(t *testing.T) {
	tool := ClaudeWebSearchTool{
		Type:    "web_search",
		Name:    "web_search",
		MaxUses: 5,
		UserLocation: &ClaudeWebSearchUserLocation{
			Type:     "approximate",
			Timezone: "America/New_York",
			Country:  "US",
		},
	}

	data, err := json.Marshal(tool)
	require.NoError(t, err)

	var decoded ClaudeWebSearchTool
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, "web_search", decoded.Type)
	assert.Equal(t, "web_search", decoded.Name)
	assert.Equal(t, 5, decoded.MaxUses)
	require.NotNil(t, decoded.UserLocation)
	assert.Equal(t, "approximate", decoded.UserLocation.Type)
	assert.Equal(t, "US", decoded.UserLocation.Country)
}

func TestClaudeSource_MarshalUnmarshal(t *testing.T) {
	tests := []struct {
		name   string
		source ClaudeSource
	}{
		{
			name: "base64 image",
			source: ClaudeSource{
				Type:      "base64",
				MediaType: "image/jpeg",
				Data:      "/9j/4AAQSkZJRg==",
			},
		},
		{
			name: "url image",
			source: ClaudeSource{
				Type:      "url",
				MediaType: "image/png",
				URL:       "https://example.com/photo.png",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.source)
			require.NoError(t, err)

			var decoded ClaudeSource
			err = json.Unmarshal(data, &decoded)
			require.NoError(t, err)

			assert.Equal(t, tt.source.Type, decoded.Type)
			assert.Equal(t, tt.source.MediaType, decoded.MediaType)
			if tt.source.Type == "base64" {
				assert.Equal(t, tt.source.Data, decoded.Data)
			} else {
				assert.Equal(t, tt.source.URL, decoded.URL)
			}
		})
	}
}

func TestClaudeRequestRoundtrip(t *testing.T) {
	original := ClaudeRequest{
		Model:         "claude-3-opus-20240229",
		MaxTokens:     intPtr(4096),
		Temperature:   floatPtr(0.3),
		TopP:          floatPtr(0.95),
		TopK:          intPtr(5),
		Stream:        boolPtr(false),
		StopSequences: []string{"\n\nHuman:"},
		System:        "You are a world-class software engineer.",
		Thinking: &Thinking{
			Type:         "enabled",
			BudgetTokens: intPtr(4000),
		},
		Messages: []ClaudeMessage{
			{
				Role: "user",
				Content: []ClaudeContent{
					{Type: "text", Text: strPtr("Analyze this code:")},
				},
			},
		},
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var restored ClaudeRequest
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	assert.Equal(t, original.Model, restored.Model)
	require.NotNil(t, restored.MaxTokens)
	assert.Equal(t, *original.MaxTokens, *restored.MaxTokens)
	require.NotNil(t, restored.Temperature)
	assert.Equal(t, *original.Temperature, *restored.Temperature)
	require.NotNil(t, restored.TopP)
	assert.Equal(t, *original.TopP, *restored.TopP)
	require.NotNil(t, restored.TopK)
	assert.Equal(t, *original.TopK, *restored.TopK)
	require.NotNil(t, restored.Stream)
	assert.Equal(t, *original.Stream, *restored.Stream)
	assert.Equal(t, original.StopSequences, restored.StopSequences)
	assert.Equal(t, original.System, restored.System)
	require.NotNil(t, restored.Thinking)
	assert.Equal(t, original.Thinking.Type, restored.Thinking.Type)
	require.NotNil(t, restored.Thinking.BudgetTokens)
	assert.Equal(t, *original.Thinking.BudgetTokens, *restored.Thinking.BudgetTokens)
}

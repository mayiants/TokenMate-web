package convert

import (
	"testing"

	"github.com/mayiants/ai-proxy-go/dto"
	"github.com/stretchr/testify/assert"
)

// === Common / Role Tests ===

func TestToClaudeRole(t *testing.T) {
	assert.Equal(t, "user", ToClaudeRole("system"))
	assert.Equal(t, "user", ToClaudeRole("user"))
	assert.Equal(t, "user", ToClaudeRole("tool"))
	assert.Equal(t, "assistant", ToClaudeRole("assistant"))
	assert.Equal(t, "user", ToClaudeRole("unknown"))
}

func TestToOpenAIRole(t *testing.T) {
	assert.Equal(t, "assistant", ToOpenAIRole("model"))
	assert.Equal(t, "user", ToOpenAIRole("user"))
	assert.Equal(t, "assistant", ToOpenAIRole("assistant"))
	assert.Equal(t, "system", ToOpenAIRole("system"))
}

func TestToGeminiRole(t *testing.T) {
	assert.Equal(t, "model", ToGeminiRole("assistant"))
	assert.Equal(t, "model", ToGeminiRole("system"))
	assert.Equal(t, "user", ToGeminiRole("user"))
	assert.Equal(t, "user", ToGeminiRole("tool"))
}

func TestStopReasonToOpenAI(t *testing.T) {
	assert.Equal(t, "stop", StopReasonToOpenAI("end_turn"))
	assert.Equal(t, "stop", StopReasonToOpenAI("endTurn"))
	assert.Equal(t, "length", StopReasonToOpenAI("max_tokens"))
	assert.Equal(t, "length", StopReasonToOpenAI("maxTokens"))
	assert.Equal(t, "stop", StopReasonToOpenAI("stop_sequence"))
	assert.Equal(t, "content_filter", StopReasonToOpenAI("content_filter"))
	assert.Equal(t, "tool_calls", StopReasonToOpenAI("tool_use"))
	assert.Equal(t, "stop", StopReasonToOpenAI(""))
	assert.Equal(t, "stop", StopReasonToOpenAI("unknown"))
}

func TestStopReasonFromOpenAI(t *testing.T) {
	assert.Equal(t, "end_turn", StopReasonFromOpenAI("stop", "claude"))
	assert.Equal(t, "max_tokens", StopReasonFromOpenAI("length", "claude"))
	assert.Equal(t, "tool_use", StopReasonFromOpenAI("tool_calls", "claude"))
	assert.Equal(t, "content_filter", StopReasonFromOpenAI("content_filter", "claude"))
	assert.Equal(t, "stop", StopReasonFromOpenAI("stop", "openai"))
}

func TestExtractStringContent(t *testing.T) {
	assert.Equal(t, "hello", ExtractStringContent("hello"))
	assert.Equal(t, "", ExtractStringContent(nil))
	assert.Equal(t, "", ExtractStringContent(123))

	mediaList := []dto.MediaContent{
		{Type: "text", Text: "part1"},
		{Type: "image_url", ImageURL: &dto.MessageImageURL{URL: "http://example.com/img.jpg"}},
		{Type: "text", Text: "part2"},
	}
	assert.Equal(t, "part1part2", ExtractStringContent(mediaList))
}

func TestBuildMediaContentList(t *testing.T) {
	assert.Nil(t, BuildMediaContentList(nil))
	assert.Nil(t, BuildMediaContentList(""))

	result := BuildMediaContentList("hello")
	assert.Len(t, result, 1)
	assert.Equal(t, "text", result[0].Type)
	assert.Equal(t, "hello", result[0].Text)

	input := []dto.MediaContent{{Type: "text", Text: "world"}}
	result = BuildMediaContentList(input)
	assert.Equal(t, input, result)
}

func TestPtrAndSafeDeref(t *testing.T) {
	p := Ptr(42)
	assert.Equal(t, 42, *p)
	assert.Equal(t, 42, SafeDeref(p))
	assert.Equal(t, 0, SafeDeref((*int)(nil)))
}

func TestTruncateString(t *testing.T) {
	assert.Equal(t, "hello", TruncateString("hello", 10))
	assert.Equal(t, "hel...", TruncateString("hello world", 3))
}

func TestFormatErrorMessage(t *testing.T) {
	err := FormatErrorMessage("test error", "test_type", "test_code")
	errMap, ok := err["error"].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, "test error", errMap["message"])
	assert.Equal(t, "test_type", errMap["type"])
	assert.Equal(t, "test_code", errMap["code"])
}

func TestMergeMessages(t *testing.T) {
	msgs := []dto.Message{
		{Role: "user", Content: "hello"},
		{Role: "user", Content: "world"},
		{Role: "assistant", Content: "hi"},
	}
	merged := MergeMessages(msgs)
	assert.Len(t, merged, 2)
	assert.Equal(t, "hello\nworld", merged[0].Content)
	assert.Equal(t, "assistant", merged[1].Role)
	assert.Equal(t, "hi", merged[1].Content)
}

func TestValidateMessages(t *testing.T) {
	// Empty messages
	assert.Error(t, ValidateMessages([]dto.Message{}, "claude"))

	// Claude: first non-system must be user
	assert.NoError(t, ValidateMessages([]dto.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello"},
	}, "claude"))
	assert.Error(t, ValidateMessages([]dto.Message{
		{Role: "system", Content: "sys"},
		{Role: "assistant", Content: "hello"},
	}, "claude"))

	// Other providers: less strict
	assert.NoError(t, ValidateMessages([]dto.Message{
		{Role: "assistant", Content: "hello"},
	}, "openai"))
}

// === Usage Conversion Tests ===

func TestUsageFromClaude(t *testing.T) {
	cu := dto.ClaudeUsage{
		InputTokens:              100,
		OutputTokens:             50,
		CacheReadInputTokens:     20,
		CacheCreationInputTokens: 10,
	}
	// W1 fix: cache_read is folded into PromptTokens to align with OpenAI's
	// semantics (effectiveInput = input + cache_read). Anthropic's
	// input_tokens excludes cache_read, so we add it back during conversion.
	// Total = prompt(incl. cache_read) + completion.
	usage := UsageFromClaude(cu)
	assert.Equal(t, 120, usage.PromptTokens) // 100 input + 20 cache_read
	assert.Equal(t, 50, usage.CompletionTokens)
	assert.Equal(t, 170, usage.TotalTokens) // 120 + 50
	assert.Equal(t, 20, usage.PromptTokensDetails.CachedTokens)
	assert.Equal(t, 10, usage.PromptTokensDetails.CachedCreationTokens)

	// With cache creation struct
	cu2 := dto.ClaudeUsage{
		InputTokens:  200,
		OutputTokens: 100,
		CacheCreation: &dto.ClaudeCacheCreation{
			Ephemeral5mInputTokens: 30,
			Ephemeral1hInputTokens: 15,
		},
	}
	usage2 := UsageFromClaude(cu2)
	assert.Equal(t, 45, usage2.PromptTokensDetails.CachedCreationTokens)
	assert.Equal(t, 30, usage2.ClaudeCacheCreation5mTokens)
	assert.Equal(t, 15, usage2.ClaudeCacheCreation1hTokens)
}

func TestUsageToClaude(t *testing.T) {
	usage := dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 50,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         20,
			CachedCreationTokens: 10,
		},
	}
	cu := UsageToClaude(usage)
	assert.Equal(t, 100, cu.InputTokens)
	assert.Equal(t, 50, cu.OutputTokens)
	assert.Equal(t, 20, cu.CacheReadInputTokens)
	assert.NotNil(t, cu.CacheCreation)
}

func TestUsageFromGemini(t *testing.T) {
	gu := GeminiUsage{PromptTokenCount: 80, CandidatesTokenCount: 40, TotalTokenCount: 120}
	usage := UsageFromGemini(gu)
	assert.Equal(t, 80, usage.PromptTokens)
	assert.Equal(t, 40, usage.CompletionTokens)
	assert.Equal(t, 120, usage.TotalTokens)
}

// === Claude Conversion Tests ===

func TestRequestOpenAI2Claude(t *testing.T) {
	maxTokens := 100
	temp := 0.7
	req := dto.GeneralOpenAIRequest{
		Model:       "claude-3-opus",
		MaxTokens:   &maxTokens,
		Temperature: &temp,
		Messages: []dto.Message{
			{Role: "system", Content: "You are helpful"},
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there"},
		},
		Stop: []string{"END"},
	}

	claudeReq, err := RequestOpenAI2Claude(req)
	assert.NoError(t, err)
	assert.Equal(t, "claude-3-opus", claudeReq.Model)
	assert.Equal(t, maxTokens, *claudeReq.MaxTokens)
	assert.Equal(t, temp, *claudeReq.Temperature)
	assert.Equal(t, []string{"END"}, claudeReq.StopSequences)
	assert.NotNil(t, claudeReq.System)
	assert.Len(t, claudeReq.Messages, 2)
	assert.Equal(t, "user", claudeReq.Messages[0].Role)
	assert.Equal(t, "assistant", claudeReq.Messages[1].Role)
}

func TestRequestOpenAI2Claude_MediaContent(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Model: "claude-3-opus",
		Messages: []dto.Message{
			{Role: "user", Content: []dto.MediaContent{
				{Type: "text", Text: "Look at this"},
				{Type: "image_url", ImageURL: &dto.MessageImageURL{URL: "data:image/jpeg;base64,abc123"}},
			}},
		},
	}

	claudeReq, err := RequestOpenAI2Claude(req)
	assert.NoError(t, err)
	assert.Len(t, claudeReq.Messages, 1)

	contentBlocks, ok := claudeReq.Messages[0].Content.([]dto.ClaudeContent)
	assert.True(t, ok)
	assert.Len(t, contentBlocks, 2)
	assert.Equal(t, "text", contentBlocks[0].Type)
	assert.Equal(t, "image", contentBlocks[1].Type)
}

func TestResponseClaude2OpenAI(t *testing.T) {
	text := "Hello world"
	claudeResp := &dto.ClaudeResponse{
		ID:         "msg_123",
		Model:      "claude-3-opus",
		StopReason: "end_turn",
		Content: []dto.ClaudeContent{
			{Type: "text", Text: &text},
		},
		Usage: dto.ClaudeUsage{InputTokens: 10, OutputTokens: 5},
	}

	openAIResp := ResponseClaude2OpenAI(claudeResp, "claude-3-opus")
	assert.NotNil(t, openAIResp)
	assert.Equal(t, "msg_123", openAIResp.ID)
	assert.Equal(t, "stop", openAIResp.Choices[0].FinishReason)
	assert.Equal(t, "Hello world", openAIResp.Choices[0].Message.Content)
	assert.Equal(t, 10, openAIResp.Usage.PromptTokens)
	assert.Equal(t, 5, openAIResp.Usage.CompletionTokens)
}

func TestResponseClaude2OpenAI_ToolUse(t *testing.T) {
	claudeResp := &dto.ClaudeResponse{
		ID:         "msg_456",
		StopReason: "tool_use",
		Content: []dto.ClaudeContent{
			{Type: "tool_use", ID: "tool_1", Name: "get_weather", Input: map[string]interface{}{"city": "Beijing"}},
		},
		Usage: dto.ClaudeUsage{InputTokens: 20, OutputTokens: 15},
	}

	openAIResp := ResponseClaude2OpenAI(claudeResp, "claude-3-opus")
	assert.NotNil(t, openAIResp)
	assert.Equal(t, "tool_calls", openAIResp.Choices[0].FinishReason)
	assert.Len(t, openAIResp.Choices[0].Message.ToolCalls, 1)
	assert.Equal(t, "get_weather", openAIResp.Choices[0].Message.ToolCalls[0].Function.Name)
}

func TestStreamResponseClaude2OpenAI(t *testing.T) {
	// Text delta
	event := &dto.ClaudeStreamEvent{
		Type:  "content_block_delta",
		Delta: &dto.ClaudeDelta{Text: "Hello"},
	}
	resp := StreamResponseClaude2OpenAI(event)
	assert.NotNil(t, resp)
	assert.Equal(t, "Hello", *resp.Choices[0].Delta.Content)

	// Stop reason delta
	event = &dto.ClaudeStreamEvent{
		Type:  "content_block_delta",
		Delta: &dto.ClaudeDelta{StopReason: "end_turn"},
	}
	resp = StreamResponseClaude2OpenAI(event)
	assert.NotNil(t, resp)
	assert.Equal(t, "stop", *resp.Choices[0].FinishReason)

	// Message stop
	event = &dto.ClaudeStreamEvent{Type: "message_stop"}
	resp = StreamResponseClaude2OpenAI(event)
	assert.NotNil(t, resp)
	assert.Equal(t, "stop", *resp.Choices[0].FinishReason)

	// Message with usage
	event = &dto.ClaudeStreamEvent{
		Type: "message",
		Message: &dto.ClaudeResponse{
			ID:    "msg_789",
			Usage: dto.ClaudeUsage{InputTokens: 5, OutputTokens: 3},
		},
	}
	resp = StreamResponseClaude2OpenAI(event)
	assert.NotNil(t, resp)
	assert.Equal(t, "msg_789", resp.ID)
	assert.NotNil(t, resp.Usage)
	assert.Equal(t, 5, resp.Usage.PromptTokens)
}

// === Gemini Conversion Tests ===

func TestRequestOpenAI2Gemini(t *testing.T) {
	maxTokens := 256
	temp := 0.5
	req := dto.GeneralOpenAIRequest{
		Model:       "gemini-pro",
		MaxTokens:   &maxTokens,
		Temperature: &temp,
		Messages: []dto.Message{
			{Role: "system", Content: "Be concise"},
			{Role: "user", Content: "Hello"},
		},
	}

	geminiReq, err := RequestOpenAI2Gemini(req)
	assert.NoError(t, err)
	assert.NotNil(t, geminiReq.SystemInstruction)
	assert.Len(t, geminiReq.Contents, 1)
	assert.Equal(t, "user", geminiReq.Contents[0].Role)
	assert.Equal(t, "Be concise", geminiReq.SystemInstruction.Parts[0].Text)
	assert.Equal(t, 0.5, geminiReq.GenerationConfig["temperature"])
	assert.Equal(t, 256, geminiReq.GenerationConfig["maxOutputTokens"])
}

func TestRequestOpenAI2Gemini_Media(t *testing.T) {
	req := dto.GeneralOpenAIRequest{
		Model: "gemini-pro-vision",
		Messages: []dto.Message{
			{Role: "user", Content: []dto.MediaContent{
				{Type: "text", Text: "What is this?"},
				{Type: "image_url", ImageURL: &dto.MessageImageURL{URL: "base64data"}},
			}},
		},
	}

	geminiReq, err := RequestOpenAI2Gemini(req)
	assert.NoError(t, err)
	assert.Len(t, geminiReq.Contents, 1)
	assert.Len(t, geminiReq.Contents[0].Parts, 2)
	assert.Equal(t, "What is this?", geminiReq.Contents[0].Parts[0].Text)
	assert.NotNil(t, geminiReq.Contents[0].Parts[1].InlineData)
}

func TestResponseGemini2OpenAI(t *testing.T) {
	geminiResp := &GeminiResponse{
		Candidates: []GeminiCandidate{
			{
				Content: GeminiContent{
					Parts: []GeminiPart{{Text: "Hello from Gemini"}},
				},
				FinishReason: "STOP",
			},
		},
		UsageMetadata: GeminiUsage{PromptTokenCount: 10, CandidatesTokenCount: 5, TotalTokenCount: 15},
	}

	openAIResp := ResponseGemini2OpenAI(geminiResp, "gemini-pro")
	assert.NotNil(t, openAIResp)
	assert.Equal(t, "Hello from Gemini", openAIResp.Choices[0].Message.Content)
	assert.Equal(t, "stop", openAIResp.Choices[0].FinishReason)
	assert.Equal(t, 10, openAIResp.Usage.PromptTokens)
}

func TestStreamResponseGemini2OpenAI(t *testing.T) {
	// Text chunk
	geminiResp := &GeminiResponse{
		Candidates: []GeminiCandidate{
			{Content: GeminiContent{Parts: []GeminiPart{{Text: "Hi"}}}},
		},
	}
	resp := StreamResponseGemini2OpenAI(geminiResp)
	assert.NotNil(t, resp)
	assert.Equal(t, "Hi", *resp.Choices[0].Delta.Content)

	// Empty chunk with finish reason
	geminiResp = &GeminiResponse{
		Candidates: []GeminiCandidate{
			{Content: GeminiContent{}, FinishReason: "STOP"},
		},
	}
	resp = StreamResponseGemini2OpenAI(geminiResp)
	assert.NotNil(t, resp)
	assert.Equal(t, "stop", *resp.Choices[0].FinishReason)

	// Nil response
	resp = StreamResponseGemini2OpenAI(nil)
	assert.Nil(t, resp)
}

// === Anthropic -> OpenAI request conversion ===

// TestAnthropicToOpenAI_MultiToolResult verifies that a user message carrying
// multiple tool_result blocks (Anthropic format) becomes one OpenAI "tool"
// message per result — previously only the last block survived, and Bedrock
// rejected the request with "Expected toolResult blocks for the following Ids".
func TestAnthropicToOpenAI_MultiToolResult(t *testing.T) {
	req := &AnthropicRequest{
		Model: "claude-test",
		Messages: []AnthropicMessage{
			{Role: "user", Content: "weather in Tokyo and Paris?"},
			{Role: "assistant", Content: []interface{}{
				map[string]interface{}{"type": "tool_use", "id": "tooluse_A", "name": "get_weather", "input": map[string]interface{}{"city": "Tokyo"}},
				map[string]interface{}{"type": "tool_use", "id": "tooluse_B", "name": "get_weather", "input": map[string]interface{}{"city": "Paris"}},
			}},
			{Role: "user", Content: []interface{}{
				map[string]interface{}{"type": "tool_result", "tool_use_id": "tooluse_A", "content": "Tokyo: sunny"},
				map[string]interface{}{"type": "tool_result", "tool_use_id": "tooluse_B", "content": "Paris: rainy"},
			}},
		},
	}

	hub, err := AnthropicToOpenAI(req)
	assert.NoError(t, err)
	assert.Len(t, hub.Messages, 4)

	assistant := hub.Messages[1]
	assert.Equal(t, "assistant", assistant.Role)
	assert.Len(t, assistant.ToolCalls, 2)

	toolA := hub.Messages[2]
	assert.Equal(t, "tool", toolA.Role)
	assert.Equal(t, "tooluse_A", toolA.ToolCallID)
	assert.Equal(t, "Tokyo: sunny", toolA.Content)

	toolB := hub.Messages[3]
	assert.Equal(t, "tool", toolB.Role)
	assert.Equal(t, "tooluse_B", toolB.ToolCallID)
	assert.Equal(t, "Paris: rainy", toolB.Content)
}

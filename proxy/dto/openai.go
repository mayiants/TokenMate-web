package dto

import "encoding/json"

// Usage - Token usage structure (consistent with new-api)
// Copied from new-api/dto/openai_response.go
type Usage struct {
	PromptTokens         int `json:"prompt_tokens"`
	CompletionTokens     int `json:"completion_tokens"`
	TotalTokens          int `json:"total_tokens"`
	PromptCacheHitTokens int `json:"prompt_cache_hit_tokens,omitempty"` // OpenAI style

	PromptTokensDetails    InputTokenDetails  `json:"prompt_tokens_details"`
	CompletionTokenDetails OutputTokenDetails `json:"completion_tokens_details"`
	InputTokens            int                `json:"input_tokens"`
	OutputTokens           int                `json:"output_tokens"`
	InputTokensDetails     *InputTokenDetails `json:"input_tokens_details"`

	// Claude cache tokens
	ClaudeCacheCreation5mTokens int `json:"claude_cache_creation_5_m_tokens"`
	ClaudeCacheCreation1hTokens int `json:"claude_cache_creation_1_h_tokens"`

	// OpenRouter Params
	Cost interface{} `json:"cost,omitempty"`
}

// InputTokenDetails - Detailed input token breakdown
type InputTokenDetails struct {
	CachedTokens         int `json:"cached_tokens"`          // Cache read tokens
	CachedCreationTokens int `json:"cached_creation_tokens"` // Cache write tokens
	// CacheWriteTokens is OpenRouter's spelling of cache writes inside
	// prompt_tokens_details; normalized into CachedCreationTokens by
	// Usage.UnmarshalJSON. omitempty keeps it out of client responses.
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
	TextTokens       int `json:"text_tokens"`
	AudioTokens      int `json:"audio_tokens"`
	ImageTokens      int `json:"image_tokens"`
}

// OutputTokenDetails - Detailed output token breakdown
type OutputTokenDetails struct {
	TextTokens      int `json:"text_tokens"`
	AudioTokens     int `json:"audio_tokens"`
	ImageTokens     int `json:"image_tokens"`
	ReasoningTokens int `json:"reasoning_tokens"`
}

// UnmarshalJSON normalizes Anthropic-style top-level cache fields
// (cache_read_input_tokens / cache_creation_input_tokens — returned by
// OpenRouter/TokenRouter when fronting Claude models) into
// PromptTokensDetails, so every downstream consumer reads a single place
// regardless of which shape the upstream used.
func (u *Usage) UnmarshalJSON(data []byte) error {
	type usageAlias Usage
	aux := struct {
		*usageAlias
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	}{usageAlias: (*usageAlias)(u)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if u.PromptTokensDetails.CachedTokens == 0 && aux.CacheReadInputTokens > 0 {
		u.PromptTokensDetails.CachedTokens = aux.CacheReadInputTokens
	}
	if u.PromptTokensDetails.CachedCreationTokens == 0 {
		if aux.CacheCreationInputTokens > 0 {
			u.PromptTokensDetails.CachedCreationTokens = aux.CacheCreationInputTokens
		} else if u.PromptTokensDetails.CacheWriteTokens > 0 {
			u.PromptTokensDetails.CachedCreationTokens = u.PromptTokensDetails.CacheWriteTokens
		}
	}
	return nil
}

// GeneralOpenAIRequest - OpenAI-compatible request format
type GeneralOpenAIRequest struct {
	Model            string            `json:"model"`
	Messages         []Message         `json:"messages"`
	Temperature      *float64          `json:"temperature,omitempty"`
	TopP             *float64          `json:"top_p,omitempty"`
	TopK             *int              `json:"top_k,omitempty"`
	MaxTokens        *int              `json:"max_tokens,omitempty"`
	Stream           *bool             `json:"stream,omitempty"`
	Stop             interface{}       `json:"stop,omitempty"`
	FrequencyPenalty *float64          `json:"frequency_penalty,omitempty"`
	PresencePenalty  *float64          `json:"presence_penalty,omitempty"`
	Seed             *int              `json:"seed,omitempty"`
	User             string            `json:"user,omitempty"`
	Tools            []ToolCallRequest `json:"tools,omitempty"`
	ToolChoice       interface{}       `json:"tool_choice,omitempty"`
	ResponseFormat   interface{}       `json:"response_format,omitempty"`
	StreamOptions    *StreamOptions    `json:"stream_options,omitempty"`
	ReasoningEffort  string            `json:"reasoning_effort,omitempty"`
}

// Message - Chat message
type Message struct {
	Role             string      `json:"role"`
	Content          interface{} `json:"content"`
	ReasoningContent *string     `json:"reasoning_content,omitempty"`
	// Reasoning is a non-standard field some clients (e.g. WorkBuddy) use to
	// carry reasoning text on assistant history messages. It is normalized
	// into ReasoningContent for DeepSeek upstreams and always stripped
	// before relaying (see normalizeReasoningFields).
	Reasoning  json.RawMessage `json:"reasoning,omitempty"`
	Name       *string         `json:"name,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
}

// MediaContent - Multi-modal content
type MediaContent struct {
	Type         string           `json:"type"`
	Text         string           `json:"text,omitempty"`
	ImageURL     *MessageImageURL `json:"image_url,omitempty"`
	CacheControl *CacheControl    `json:"cache_control,omitempty"`
}

// MessageImageURL - Image URL
type MessageImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// CacheControl - Cache control for prompt caching
type CacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

// ToolCallRequest - Tool definition
type ToolCallRequest struct {
	Type     string          `json:"type"`
	Function FunctionRequest `json:"function"`
}

// FunctionRequest - Function definition
type FunctionRequest struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

// ToolCall - Tool call in response
type ToolCall struct {
	// Index must always serialize: streaming clients group deltas by it, and
	// 0 is a valid index (stream re-encoding in the chat handler would
	// otherwise drop it).
	Index    int          `json:"index"`
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type,omitempty"`
	Function FunctionCall `json:"function"`
}

// FunctionCall - Function call details
type FunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// StreamOptions - Streaming options
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

// OpenAITextResponse - OpenAI response format
type OpenAITextResponse struct {
	ID      string                     `json:"id"`
	Object  string                     `json:"object"`
	Created int64                      `json:"created"`
	Model   string                     `json:"model"`
	Choices []OpenAITextResponseChoice `json:"choices"`
	Usage   Usage                      `json:"usage"`
	Error   interface{}                `json:"error,omitempty"`
}

// OpenAITextResponseChoice - Response choice
type OpenAITextResponseChoice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// ChatCompletionsStreamResponse - Streaming response chunk
type ChatCompletionsStreamResponse struct {
	ID                string                                `json:"id"`
	Object            string                                `json:"object"`
	Created           int64                                 `json:"created"`
	Model             string                                `json:"model"`
	SystemFingerprint *string                               `json:"system_fingerprint,omitempty"`
	Choices           []ChatCompletionsStreamResponseChoice `json:"choices"`
	Usage             *Usage                                `json:"usage,omitempty"`
}

// ChatCompletionsStreamResponseChoice - Streaming choice
type ChatCompletionsStreamResponseChoice struct {
	Index        int                                      `json:"index"`
	Delta        ChatCompletionsStreamResponseChoiceDelta `json:"delta"`
	FinishReason *string                                  `json:"finish_reason"`
}

// ChatCompletionsStreamResponseChoiceDelta - Streaming delta
type ChatCompletionsStreamResponseChoiceDelta struct {
	Content          *string    `json:"content,omitempty"`
	ReasoningContent *string    `json:"reasoning_content,omitempty"`
	Role             string     `json:"role,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
}

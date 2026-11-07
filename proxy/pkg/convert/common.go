package convert

import (
	"fmt"

	"github.com/mayiants/ai-proxy-go/dto"
)

// RoleMapping defines bidirectional role conversions between formats.
// OpenAI roles: system, user, assistant, tool
// Claude roles: user, assistant
// Gemini roles: user, model

// ToClaudeRole converts an OpenAI role to Claude role.
// Claude only supports "user" and "assistant". System messages are handled separately.
func ToClaudeRole(openAIRole string) string {
	switch openAIRole {
	case "system":
		// Claude handles system via the System field, not messages
		return "user"
	case "user", "tool":
		return "user"
	case "assistant":
		return "assistant"
	default:
		return "user"
	}
}

// ToOpenAIRole converts a provider-specific role to OpenAI role.
func ToOpenAIRole(providerRole string) string {
	switch providerRole {
	case "model": // Gemini uses "model" for assistant
		return "assistant"
	case "user", "assistant", "system", "tool":
		return providerRole
	default:
		return "user"
	}
}

// ToGeminiRole converts an OpenAI role to Gemini role.
// Gemini uses "user" and "model" (not "assistant").
func ToGeminiRole(openAIRole string) string {
	switch openAIRole {
	case "assistant", "system":
		return "model"
	case "user", "tool":
		return "user"
	default:
		return "user"
	}
}

// StopReasonToOpenAI converts provider stop reasons to OpenAI format.
func StopReasonToOpenAI(providerStopReason string) string {
	switch providerStopReason {
	case "end_turn", "endTurn":
		return "stop"
	case "max_tokens", "maxTokens":
		return "length"
	case "stop_sequence":
		return "stop"
	case "content_filter":
		return "content_filter"
	case "tool_use":
		return "tool_calls"
	default:
		return "stop"
	}
}

// StopReasonFromOpenAI converts OpenAI stop reason to provider format.
func StopReasonFromOpenAI(openAIStopReason string, provider string) string {
	switch provider {
	case "claude", "anthropic":
		switch openAIStopReason {
		case "stop":
			return "end_turn"
		case "length":
			return "max_tokens"
		case "content_filter":
			return "content_filter"
		case "tool_calls":
			return "tool_use"
		default:
			return "end_turn"
		}
	default:
		return openAIStopReason
	}
}

// ExtractStringContent extracts plain text from a message content field.
// Content can be string, []MediaContent, or nil.
func ExtractStringContent(content interface{}) string {
	if content == nil {
		return ""
	}
	if s, ok := content.(string); ok {
		return s
	}
	// Try []MediaContent
	if mediaList, ok := content.([]dto.MediaContent); ok {
		var text string
		for _, m := range mediaList {
			if m.Type == "text" {
				text += m.Text
			}
		}
		return text
	}
	// Try []interface{} (generic JSON array)
	if arr, ok := content.([]interface{}); ok {
		var text string
		for _, item := range arr {
			if m, ok := item.(map[string]interface{}); ok {
				if t, ok := m["text"].(string); ok && m["type"] == "text" {
					text += t
				}
			}
		}
		return text
	}
	return ""
}

// BuildMediaContentList converts a message content to a []MediaContent slice.
// If content is already a string, wraps it in a single text block.
// If content is already []MediaContent, returns as-is.
func BuildMediaContentList(content interface{}) []dto.MediaContent {
	if content == nil {
		return nil
	}
	if s, ok := content.(string); ok {
		if s == "" {
			return nil
		}
		return []dto.MediaContent{{Type: "text", Text: s}}
	}
	if mediaList, ok := content.([]dto.MediaContent); ok {
		return mediaList
	}
	return nil
}

// Ptr returns a pointer to the given value.
func Ptr[T any](v T) *T {
	return &v
}

// SafeDeref safely dereferences a pointer, returning the zero value if nil.
func SafeDeref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

// UsageFromClaude converts Claude usage to OpenAI-compatible Usage.
//
// Anthropic reports input_tokens EXCLUDING cache_read tokens, but OpenAI
// semantics (which our billing layer follows) count cached tokens inside
// prompt_tokens and break them out via prompt_tokens_details.cached_tokens.
// We therefore add cache_read back into PromptTokens so that the billing
// formula effectiveInput = PromptTokens - CachedTokens yields the correct
// non-cached input count. (AWS adapter does the same in promptTokensForBilling.)
func UsageFromClaude(claudeUsage dto.ClaudeUsage) dto.Usage {
	cacheRead := claudeUsage.CacheReadInputTokens
	usage := dto.Usage{
		PromptTokens:     claudeUsage.InputTokens + cacheRead,
		CompletionTokens: claudeUsage.OutputTokens,
		TotalTokens:      claudeUsage.InputTokens + cacheRead + claudeUsage.OutputTokens,
		InputTokens:      claudeUsage.InputTokens,
		OutputTokens:     claudeUsage.OutputTokens,
	}

	// Handle cache tokens
	if claudeUsage.CacheCreation != nil {
		usage.PromptTokensDetails.CachedCreationTokens =
			claudeUsage.CacheCreation.Ephemeral5mInputTokens +
				claudeUsage.CacheCreation.Ephemeral1hInputTokens
		usage.ClaudeCacheCreation5mTokens = claudeUsage.CacheCreation.Ephemeral5mInputTokens
		usage.ClaudeCacheCreation1hTokens = claudeUsage.CacheCreation.Ephemeral1hInputTokens
	} else {
		usage.PromptTokensDetails.CachedCreationTokens = claudeUsage.CacheCreationInputTokens
	}
	usage.PromptTokensDetails.CachedTokens = cacheRead
	usage.PromptCacheHitTokens = cacheRead

	return usage
}

// UsageToClaude converts OpenAI Usage to Claude-style usage map.
func UsageToClaude(usage dto.Usage) dto.ClaudeUsage {
	claudeUsage := dto.ClaudeUsage{
		InputTokens:  usage.PromptTokens,
		OutputTokens: usage.CompletionTokens,
	}

	if usage.PromptTokensDetails.CachedTokens > 0 {
		claudeUsage.CacheReadInputTokens = usage.PromptTokensDetails.CachedTokens
	}
	if usage.PromptTokensDetails.CachedCreationTokens > 0 {
		claudeUsage.CacheCreation = &dto.ClaudeCacheCreation{
			Ephemeral5mInputTokens: usage.ClaudeCacheCreation5mTokens,
			Ephemeral1hInputTokens: usage.ClaudeCacheCreation1hTokens,
		}
		if claudeUsage.CacheCreation.Ephemeral5mInputTokens == 0 && claudeUsage.CacheCreation.Ephemeral1hInputTokens == 0 {
			// Fallback: put all creation tokens into 5m bucket
			claudeUsage.CacheCreation.Ephemeral5mInputTokens = usage.PromptTokensDetails.CachedCreationTokens
		}
	}

	return claudeUsage
}

// TruncateString truncates a string to maxLen with ellipsis.
func TruncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// FormatErrorMessage formats an upstream error into a consistent error structure.
func FormatErrorMessage(message, errType, code string) map[string]interface{} {
	return map[string]interface{}{
		"error": map[string]interface{}{
			"message": message,
			"type":    errType,
			"code":    code,
		},
	}
}

// MergeMessages merges consecutive messages with the same role into a single message.
// This is useful for providers that don't support alternating roles strictly.
func MergeMessages(messages []dto.Message) []dto.Message {
	if len(messages) == 0 {
		return messages
	}

	merged := make([]dto.Message, 0, len(messages))
	merged = append(merged, messages[0])

	for i := 1; i < len(messages); i++ {
		last := &merged[len(merged)-1]
		if last.Role == messages[i].Role {
			// Merge content
			lastText := ExtractStringContent(last.Content)
			currText := ExtractStringContent(messages[i].Content)
			if lastText != "" || currText != "" {
				last.Content = lastText + "\n" + currText
			}
		} else {
			merged = append(merged, messages[i])
		}
	}

	return merged
}

// ValidateMessages ensures messages alternate between user and assistant roles.
// Some providers require strict alternation. Returns an error if invalid.
func ValidateMessages(messages []dto.Message, provider string) error {
	if len(messages) == 0 {
		return fmt.Errorf("messages cannot be empty")
	}

	// Claude requires first non-system message to be from user
	if provider == "claude" || provider == "anthropic" {
		firstNonSystemIdx := -1
		for i, m := range messages {
			if m.Role != "system" {
				firstNonSystemIdx = i
				break
			}
		}
		if firstNonSystemIdx >= 0 && messages[firstNonSystemIdx].Role != "user" {
			return fmt.Errorf("claude requires the first non-system message to be from the user")
		}
	}

	return nil
}

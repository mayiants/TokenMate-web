package convert

import (
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"
)

// ClientFormat represents the detected client request format
type ClientFormat string

const (
	// ClientFormatOpenAI is OpenAI Chat Completions API format
	ClientFormatOpenAI ClientFormat = "openai_chat"
	// ClientFormatAnthropic is Anthropic Messages API format
	ClientFormatAnthropic ClientFormat = "anthropic"
	// ClientFormatResponses is OpenAI Responses API format
	ClientFormatResponses ClientFormat = "openai_responses"
	// ClientFormatUnknown falls back to OpenAI Chat
	ClientFormatUnknown ClientFormat = "unknown"
)

// IsAnthropicFormat checks if the request body looks like Anthropic Messages API.
// Detection must use Anthropic-specific markers only — NOT "max_tokens"+"messages",
// which appear in virtually every OpenAI chat request and would mis-route OpenAI
// traffic to the Anthropic path.
func IsAnthropicFormat(body []byte) bool {
	// "anthropic_version" is an Anthropic-only request field.
	if strings.Contains(string(body), `"anthropic_version"`) {
		return true
	}
	// A top-level "system" field (string or array of blocks) is Anthropic-specific;
	// OpenAI carries system prompts inside the messages array, not as a top-level
	// field, so its presence is a strong Anthropic signal.
	return JSONFieldExists(body, "system")
}

// IsResponsesFormat checks if the request body looks like OpenAI Responses API
func IsResponsesFormat(body []byte) bool {
	bodyStr := string(body)
	return strings.Contains(bodyStr, `"input"`) &&
		(strings.Contains(bodyStr, `"store"`) || strings.Contains(bodyStr, `"instructions"`))
}

// DetectClientFormat detects the client API format from request context and body
func DetectClientFormat(c *gin.Context, body []byte) ClientFormat {
	// Priority 1: URL path detection
	path := c.Request.URL.Path
	if strings.Contains(path, "/messages") {
		return ClientFormatAnthropic
	}
	if strings.Contains(path, "/responses") {
		return ClientFormatResponses
	}

	// Priority 2: Header detection
	if c.GetHeader("anthropic-version") != "" {
		return ClientFormatAnthropic
	}

	// Priority 3: Body structure detection
	if len(body) > 0 {
		if IsAnthropicFormat(body) {
			return ClientFormatAnthropic
		}
		if IsResponsesFormat(body) {
			return ClientFormatResponses
		}
	}

	// Default: OpenAI Chat Completions
	return ClientFormatOpenAI
}

// StripLeadingAnthropicBillingHeader strips the dynamic billing header from
// Claude Code's system prompt to improve prefix cache hit rate.
// Only strips a leading line starting with "x-anthropic-billing-header:".
func StripLeadingAnthropicBillingHeader(text string) string {
	const prefix = "x-anthropic-billing-header:"
	if !strings.HasPrefix(text, prefix) {
		return text
	}

	lineEnd := strings.IndexAny(text, "\n\r")
	if lineEnd == -1 {
		return ""
	}

	restStart := lineEnd + 1
	if text[lineEnd] == '\r' && len(text) > lineEnd+1 && text[lineEnd+1] == '\n' {
		restStart = lineEnd + 2
	}

	rest := text[restStart:]
	rest = strings.TrimLeft(rest, "\n\r")
	return rest
}

// JSONFieldExists checks if a top-level field exists in JSON body
func JSONFieldExists(body []byte, field string) bool {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return false
	}
	_, ok := m[field]
	return ok
}

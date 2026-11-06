package plugin

import (
	"encoding/json"
	"strings"
)

// ResponsesPromptCacheKeyRewriter injects a stable prompt_cache_key into
// OpenAI Responses API requests when the client has not provided one. This
// helps compatible upstreams route identical prompts to the same cache slot.
type ResponsesPromptCacheKeyRewriter struct{}

func (ResponsesPromptCacheKeyRewriter) Name() string { return "responses_prompt_cache_key" }

func (ResponsesPromptCacheKeyRewriter) Transform(body []byte, ctx Context) ([]byte, error) {
	if ctx.ClientFormat != "openai_responses" {
		return body, nil
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return body, err
	}

	if v, ok := raw["prompt_cache_key"]; ok {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			return body, nil
		}
	}

	raw["prompt_cache_key"] = buildResponsesPromptCacheKey(ctx.HostKey, ctx.ClientName)
	return json.Marshal(raw)
}

func buildResponsesPromptCacheKey(hostKey, clientName string) string {
	safeHostKey := normalizePromptCacheKeyPart(hostKey, "local")
	safeClientName := normalizePromptCacheKeyPart(clientName, "unknown")
	return safeHostKey + ":" + safeClientName
}

func normalizePromptCacheKeyPart(value, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}

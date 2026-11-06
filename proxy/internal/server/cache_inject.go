package server

import (
	"strings"

	"github.com/ai-proxy/ai-proxy-go/dto"
	"github.com/ai-proxy/ai-proxy-go/types"
)

// cacheControlAggregators are providers that front Anthropic claude-* models
// and translate Anthropic-style cache_control markers on OpenAI-format content
// parts into provider-side prompt caching (OpenRouter/TokenRouter pass them
// through and report cache_creation_input_tokens / cache_read_input_tokens).
var cacheControlAggregators = map[string]bool{
	"openrouter":  true,
	"tokenrouter": true,
}

// minCacheInjectChars approximates Anthropic's minimum cacheable prefix
// (1024 tokens for Claude) at ~4 chars per token. Mirrors the Bedrock
// channel's injectCachePoints heuristic (aws.go).
const minCacheInjectChars = 4096

// maybeInjectAggregatorCacheControl adds cache_control breakpoints to an
// OpenAI-format request headed to an aggregator fronting a Claude model.
//
// The Bedrock channel injects cache points itself (aws.go injectCachePoints),
// so Bedrock calls cache-hit regardless of the client. OpenAI-format relays to
// OpenRouter/TokenRouter had no such injection, so clients that never set
// cache_control (most OpenAI-format coding agents) got 0% cache hits at full
// input price. Markers go on the system prompt and the last text-bearing
// message, matching the Bedrock strategy. Client-supplied markers win: when
// any message already carries cache_control the request is left untouched.
// Returns true when at least one marker was added.
func maybeInjectAggregatorCacheControl(req *dto.GeneralOpenAIRequest, provider, upstreamModel string, format types.RelayFormat) bool {
	if format != types.RelayFormatOpenAI || !cacheControlAggregators[provider] {
		return false
	}
	if !strings.Contains(strings.ToLower(upstreamModel), "claude") {
		return false
	}
	if requestHasCacheControl(req) {
		return false
	}

	injected := false

	// System prefix: cache when large enough to matter.
	systemChars := 0
	lastSystem := -1
	for i := range req.Messages {
		if req.Messages[i].Role != "system" {
			continue
		}
		systemChars += messageTextLen(req.Messages[i].Content)
		lastSystem = i
	}
	if lastSystem >= 0 && systemChars >= minCacheInjectChars {
		injected = markLastTextPart(&req.Messages[lastSystem]) || injected
	}

	// Conversation prefix: mark the last text-bearing message so the whole
	// history up to the new turn is served from cache on the next turn.
	msgChars := 0
	lastText := -1
	for i := range req.Messages {
		n := messageTextLen(req.Messages[i].Content)
		msgChars += n
		if n > 0 {
			lastText = i
		}
	}
	if lastText >= 0 && msgChars >= minCacheInjectChars {
		injected = markLastTextPart(&req.Messages[lastText]) || injected
	}

	return injected
}

// messageTextLen returns the total text length of message content, whether it
// is a plain string or a decoded content-part array.
func messageTextLen(content interface{}) int {
	switch v := content.(type) {
	case string:
		return len(v)
	case []interface{}:
		n := 0
		for _, part := range v {
			if m, ok := part.(map[string]interface{}); ok {
				if s, ok := m["text"].(string); ok {
					n += len(s)
				}
			}
		}
		return n
	}
	return 0
}

// markLastTextPart attaches an ephemeral cache_control marker to the last text
// part of a message, converting plain string content into a content-part array
// when needed. Returns false when the message has no text part to mark.
func markLastTextPart(m *dto.Message) bool {
	switch v := m.Content.(type) {
	case string:
		if v == "" {
			return false
		}
		m.Content = []dto.MediaContent{{
			Type:         "text",
			Text:         v,
			CacheControl: &dto.CacheControl{Type: "ephemeral"},
		}}
		return true
	case []interface{}:
		for i := len(v) - 1; i >= 0; i-- {
			part, ok := v[i].(map[string]interface{})
			if !ok {
				continue
			}
			if t, _ := part["type"].(string); t != "" && t != "text" {
				continue
			}
			part["cache_control"] = map[string]interface{}{"type": "ephemeral"}
			return true
		}
	}
	return false
}

// requestHasCacheControl reports whether any message already carries a
// cache_control marker, in which case the client's placement is respected.
func requestHasCacheControl(req *dto.GeneralOpenAIRequest) bool {
	for i := range req.Messages {
		parts, ok := req.Messages[i].Content.([]interface{})
		if !ok {
			continue
		}
		for _, part := range parts {
			if m, ok := part.(map[string]interface{}); ok {
				if _, ok := m["cache_control"]; ok {
					return true
				}
			}
		}
	}
	return false
}

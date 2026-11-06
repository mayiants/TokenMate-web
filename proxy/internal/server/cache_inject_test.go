package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ai-proxy/ai-proxy-go/dto"
	"github.com/ai-proxy/ai-proxy-go/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func chatReq(t *testing.T, body string) *dto.GeneralOpenAIRequest {
	t.Helper()
	var req dto.GeneralOpenAIRequest
	require.NoError(t, json.Unmarshal([]byte(body), &req))
	return &req
}

func contentParts(t *testing.T, m dto.Message) []interface{} {
	t.Helper()
	raw, err := json.Marshal(m.Content)
	require.NoError(t, err)
	var parts []interface{}
	require.NoError(t, json.Unmarshal(raw, &parts))
	return parts
}

func hasMarker(t *testing.T, m dto.Message) bool {
	t.Helper()
	raw, err := json.Marshal(m.Content)
	require.NoError(t, err)
	var parts []interface{}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return false // plain string content cannot carry a marker
	}
	for _, p := range parts {
		if mp, ok := p.(map[string]interface{}); ok {
			if _, ok := mp["cache_control"]; ok {
				return true
			}
		}
	}
	return false
}

func TestInjectAggregatorCacheControl_SystemAndLastMessage(t *testing.T) {
	big := strings.Repeat("x", 5000)
	req := chatReq(t, `{
		"model": "claude-opus-4.6",
		"messages": [
			{"role": "system", "content": "`+big+`"},
			{"role": "user", "content": "turn 1 `+big+`"},
			{"role": "assistant", "content": "reply"},
			{"role": "user", "content": "turn 2"}
		]
	}`)

	injected := maybeInjectAggregatorCacheControl(req, "openrouter", "anthropic/claude-opus-4.6", types.RelayFormatOpenAI)
	assert.True(t, injected)
	assert.True(t, hasMarker(t, req.Messages[0]), "system message should carry a marker")
	assert.True(t, hasMarker(t, req.Messages[3]), "last text-bearing message should carry a marker")
	assert.False(t, hasMarker(t, req.Messages[1]), "middle messages stay unmarked")

	// String content must be converted to a content-part array carrying the marker.
	parts := contentParts(t, req.Messages[0])
	require.Len(t, parts, 1)
	part := parts[0].(map[string]interface{})
	assert.Equal(t, "text", part["type"])
	assert.Equal(t, big, part["text"])
	assert.Equal(t, map[string]interface{}{"type": "ephemeral"}, part["cache_control"])
}

func TestInjectAggregatorCacheControl_MarksLastTextPartOfArrayContent(t *testing.T) {
	big := strings.Repeat("y", 5000)
	req := chatReq(t, `{
		"model": "claude-opus-4.6",
		"messages": [
			{"role": "user", "content": [
				{"type": "text", "text": "first part"},
				{"type": "text", "text": "`+big+`"}
			]}
		]
	}`)

	injected := maybeInjectAggregatorCacheControl(req, "openrouter", "anthropic/claude-opus-4.6", types.RelayFormatOpenAI)
	assert.True(t, injected)

	parts := contentParts(t, req.Messages[0])
	require.Len(t, parts, 2)
	_, firstMarked := parts[0].(map[string]interface{})["cache_control"]
	assert.False(t, firstMarked, "only the last text part gets the marker")
	cc, ok := parts[1].(map[string]interface{})["cache_control"]
	require.True(t, ok)
	assert.Equal(t, map[string]interface{}{"type": "ephemeral"}, cc)
}

func TestInjectAggregatorCacheControl_Skips(t *testing.T) {
	big := strings.Repeat("z", 5000)
	cases := []struct {
		name     string
		provider string
		model    string
		format   types.RelayFormat
	}{
		{"non-aggregator provider", "openai", "anthropic/claude-opus-4.6", types.RelayFormatOpenAI},
		{"non-claude model", "openrouter", "openai/gpt-5.6-terra", types.RelayFormatOpenAI},
		{"non-openai relay format", "openrouter", "anthropic/claude-opus-4.6", types.RelayFormatClaude},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := chatReq(t, `{"model":"m","messages":[{"role":"user","content":"`+big+`"}]}`)
			assert.False(t, maybeInjectAggregatorCacheControl(req, tc.provider, tc.model, tc.format))
			assert.False(t, hasMarker(t, req.Messages[0]))
		})
	}
}

func TestInjectAggregatorCacheControl_RespectsClientMarkers(t *testing.T) {
	big := strings.Repeat("w", 5000)
	req := chatReq(t, `{
		"model": "claude-opus-4.6",
		"messages": [
			{"role": "user", "content": [
				{"type": "text", "text": "`+big+`", "cache_control": {"type": "ephemeral"}}
			]},
			{"role": "user", "content": "`+big+`"}
		]
	}`)

	injected := maybeInjectAggregatorCacheControl(req, "openrouter", "anthropic/claude-opus-4.6", types.RelayFormatOpenAI)
	assert.False(t, injected, "client-provided markers must win over injection")
	assert.False(t, hasMarker(t, req.Messages[1]), "no extra marker added to later messages")
}

func TestInjectAggregatorCacheControl_BelowThreshold(t *testing.T) {
	req := chatReq(t, `{"model":"claude-opus-4.6","messages":[{"role":"user","content":"hi"}]}`)
	assert.False(t, maybeInjectAggregatorCacheControl(req, "openrouter", "anthropic/claude-opus-4.6", types.RelayFormatOpenAI))
	assert.False(t, hasMarker(t, req.Messages[0]))
}

func TestInjectAggregatorCacheControl_ToolCallOnlyLastMessage(t *testing.T) {
	big := strings.Repeat("v", 5000)
	req := chatReq(t, `{
		"model": "claude-opus-4.6",
		"messages": [
			{"role": "system", "content": "`+big+`"},
			{"role": "assistant", "content": null, "tool_calls": [{"index":0,"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]}
		]
	}`)

	// System still gets marked; the tool-call-only message must be skipped
	// without panicking.
	injected := maybeInjectAggregatorCacheControl(req, "tokenrouter", "anthropic/claude-opus-4.6", types.RelayFormatOpenAI)
	assert.True(t, injected)
	assert.True(t, hasMarker(t, req.Messages[0]))
	assert.Nil(t, req.Messages[1].Content)
}

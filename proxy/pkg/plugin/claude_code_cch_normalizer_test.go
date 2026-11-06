package plugin

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeCodeCCHNormalizer_StringSystem(t *testing.T) {
	p := ClaudeCodeCCHNormalizer{}
	body := []byte(`{"model":"claude-sonnet-4","system":"x-anthropic-billing-header: cch=random-id-123; other=value"}`)

	out, err := p.Transform(body, Context{ClientFormat: "anthropic"})
	require.NoError(t, err)

	var req struct {
		System string `json:"system"`
	}
	require.NoError(t, json.Unmarshal(out, &req))
	assert.Equal(t, "x-anthropic-billing-header: cch=A1234; other=value", req.System)
}

func TestClaudeCodeCCHNormalizer_ArraySystem(t *testing.T) {
	p := ClaudeCodeCCHNormalizer{}
	body := []byte(`{"model":"claude-sonnet-4","system":[{"type":"text","text":"x-anthropic-billing-header: cch=abc123;"}]}`)

	out, err := p.Transform(body, Context{ClientFormat: "anthropic"})
	require.NoError(t, err)

	var req struct {
		System []struct {
			Text string `json:"text"`
		} `json:"system"`
	}
	require.NoError(t, json.Unmarshal(out, &req))
	require.Len(t, req.System, 1)
	assert.Equal(t, "x-anthropic-billing-header: cch=A1234;", req.System[0].Text)
}

func TestClaudeCodeCCHNormalizer_NoMatch(t *testing.T) {
	p := ClaudeCodeCCHNormalizer{}
	body := []byte(`{"model":"claude-sonnet-4","system":"You are a helpful assistant."}`)

	out, err := p.Transform(body, Context{ClientFormat: "anthropic"})
	require.NoError(t, err)
	assert.Equal(t, string(body), string(out))
}

func TestClaudeCodeCCHNormalizer_SkipsNonAnthropic(t *testing.T) {
	p := ClaudeCodeCCHNormalizer{}
	body := []byte(`{"model":"gpt-4o","system":"x-anthropic-billing-header: cch=abc;"}`)

	out, err := p.Transform(body, Context{ClientFormat: "openai"})
	require.NoError(t, err)
	assert.Equal(t, string(body), string(out))
}

func TestClaudeCodeCCHNormalizer_RewritesOnClaudeUpstream(t *testing.T) {
	p := ClaudeCodeCCHNormalizer{}
	body := []byte(`{"model":"claude-sonnet-4","system":"x-anthropic-billing-header: cch=random-id-123; other=value"}`)

	out, err := p.Transform(body, Context{ClientFormat: "openai", UpstreamFormat: "claude"})
	require.NoError(t, err)

	var req struct {
		System string `json:"system"`
	}
	require.NoError(t, json.Unmarshal(out, &req))
	assert.Equal(t, "x-anthropic-billing-header: cch=A1234; other=value", req.System)
}

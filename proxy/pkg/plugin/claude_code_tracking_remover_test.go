package plugin

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeCodeTrackingRemover_StringSystem(t *testing.T) {
	p := ClaudeCodeTrackingRemover{}
	body := []byte(`{"model":"claude-sonnet-4","system":"# currentDate\nToday's date is 2026/06/28.\nYou are helpful."}`)

	out, err := p.Transform(body, Context{ClientFormat: "anthropic"})
	require.NoError(t, err)

	var req struct {
		System string `json:"system"`
	}
	require.NoError(t, json.Unmarshal(out, &req))
	assert.Equal(t, "# currentDate\nToday's date is 2026-06-28.\nYou are helpful.", req.System)
}

func TestClaudeCodeTrackingRemover_ArraySystem(t *testing.T) {
	p := ClaudeCodeTrackingRemover{}
	body := []byte(`{"model":"claude-sonnet-4","system":[{"type":"text","text":"# currentDate\nToday's date is 2026-06-28.\n"},{"type":"text","text":"You are helpful."}]}`)

	out, err := p.Transform(body, Context{ClientFormat: "anthropic"})
	require.NoError(t, err)

	var req struct {
		System []struct {
			Text string `json:"text"`
		} `json:"system"`
	}
	require.NoError(t, json.Unmarshal(out, &req))
	require.Len(t, req.System, 2)
	assert.Equal(t, "# currentDate\nToday's date is 2026-06-28.\n", req.System[0].Text)
	assert.Equal(t, "You are helpful.", req.System[1].Text)
}

func TestClaudeCodeTrackingRemover_NoMatch(t *testing.T) {
	p := ClaudeCodeTrackingRemover{}
	body := []byte(`{"model":"claude-sonnet-4","system":"You are a helpful assistant."}`)

	out, err := p.Transform(body, Context{ClientFormat: "anthropic"})
	require.NoError(t, err)
	assert.Equal(t, string(body), string(out))
}

func TestClaudeCodeTrackingRemover_SkipsNonAnthropic(t *testing.T) {
	p := ClaudeCodeTrackingRemover{}
	body := []byte(`{"model":"gpt-4o","system":"# currentDate\nToday's date is 2026/06/28."}`)

	out, err := p.Transform(body, Context{ClientFormat: "openai"})
	require.NoError(t, err)
	assert.Equal(t, string(body), string(out))
}

func TestClaudeCodeTrackingRemover_RewritesOnClaudeUpstream(t *testing.T) {
	p := ClaudeCodeTrackingRemover{}
	body := []byte(`{"model":"claude-sonnet-4","system":"# currentDate\nToday's date is 2026/06/28.\nYou are helpful."}`)

	out, err := p.Transform(body, Context{ClientFormat: "openai", UpstreamFormat: "claude"})
	require.NoError(t, err)

	var req struct {
		System string `json:"system"`
	}
	require.NoError(t, json.Unmarshal(out, &req))
	assert.Equal(t, "# currentDate\nToday's date is 2026-06-28.\nYou are helpful.", req.System)
}

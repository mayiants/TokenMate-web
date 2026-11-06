package plugin

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStreamOptionsInjector_InjectsWhenStreamingAndMissing(t *testing.T) {
	p := StreamOptionsInjector{}
	body := []byte(`{"model":"gpt-4o","stream":true}`)
	out, err := p.Transform(body, Context{UpstreamFormat: "openai"})
	require.NoError(t, err)

	var req struct {
		StreamOptions struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	require.NoError(t, json.Unmarshal(out, &req))
	assert.True(t, req.StreamOptions.IncludeUsage)
}

func TestStreamOptionsInjector_RespectsExistingValue(t *testing.T) {
	p := StreamOptionsInjector{}
	body := []byte(`{"model":"gpt-4o","stream":true,"stream_options":{"include_usage":false}}`)
	out, err := p.Transform(body, Context{UpstreamFormat: "openai"})
	require.NoError(t, err)
	assert.Equal(t, string(body), string(out))
}

func TestStreamOptionsInjector_SkipsNonStreaming(t *testing.T) {
	p := StreamOptionsInjector{}
	body := []byte(`{"model":"gpt-4o","stream":false}`)
	out, err := p.Transform(body, Context{UpstreamFormat: "openai"})
	require.NoError(t, err)
	assert.Equal(t, string(body), string(out))
}

func TestStreamOptionsInjector_SkipsNonOpenAIUpstream(t *testing.T) {
	p := StreamOptionsInjector{}
	body := []byte(`{"model":"claude","stream":true}`)
	out, err := p.Transform(body, Context{UpstreamFormat: "anthropic"})
	require.NoError(t, err)
	assert.Equal(t, string(body), string(out))
}

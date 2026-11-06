package plugin

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponsesPromptCacheKeyRewriter_InjectsWhenMissing(t *testing.T) {
	p := ResponsesPromptCacheKeyRewriter{}
	body := []byte(`{"model":"o1","messages":[{"role":"user","content":"hi"}]}`)

	out, err := p.Transform(body, Context{ClientFormat: "openai_responses", HostKey: "key-1", ClientName: "user-a"})
	require.NoError(t, err)

	var req struct {
		PromptCacheKey string `json:"prompt_cache_key"`
	}
	require.NoError(t, json.Unmarshal(out, &req))
	assert.Equal(t, "key-1:user-a", req.PromptCacheKey)
}

func TestResponsesPromptCacheKeyRewriter_RespectsExistingValue(t *testing.T) {
	p := ResponsesPromptCacheKeyRewriter{}
	body := []byte(`{"model":"o1","prompt_cache_key":"existing-key"}`)

	out, err := p.Transform(body, Context{ClientFormat: "openai_responses"})
	require.NoError(t, err)
	assert.Equal(t, string(body), string(out))
}

func TestResponsesPromptCacheKeyRewriter_FallsBackForEmptyContext(t *testing.T) {
	p := ResponsesPromptCacheKeyRewriter{}
	body := []byte(`{"model":"o1"}`)

	out, err := p.Transform(body, Context{ClientFormat: "openai_responses"})
	require.NoError(t, err)

	var req struct {
		PromptCacheKey string `json:"prompt_cache_key"`
	}
	require.NoError(t, json.Unmarshal(out, &req))
	assert.Equal(t, "local:unknown", req.PromptCacheKey)
}

func TestResponsesPromptCacheKeyRewriter_SkipsNonResponses(t *testing.T) {
	p := ResponsesPromptCacheKeyRewriter{}
	body := []byte(`{"model":"gpt-4o"}`)

	out, err := p.Transform(body, Context{ClientFormat: "openai"})
	require.NoError(t, err)
	assert.Equal(t, string(body), string(out))
}

package server

import (
	"encoding/json"
	"testing"
)

func TestRewriteAnthropicBodyModel(t *testing.T) {
	body := []byte(`{"model":"claude-opus-5","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`)

	// no-op when equal
	if got := rewriteAnthropicBodyModel(body, "claude-opus-5", "claude-opus-5"); string(got) != string(body) {
		t.Fatal("equal model should be a no-op")
	}
	// no-op when upstream empty
	if got := rewriteAnthropicBodyModel(body, "claude-opus-5", ""); string(got) != string(body) {
		t.Fatal("empty upstream should be a no-op")
	}
	// rewrite to vendor name, preserving other fields
	out := rewriteAnthropicBodyModel(body, "claude-opus-5", "anthropic/claude-opus-5")
	var m map[string]json.RawMessage
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("output not valid json: %v", err)
	}
	var model string
	_ = json.Unmarshal(m["model"], &model)
	if model != "anthropic/claude-opus-5" {
		t.Fatalf("model = %q, want anthropic/claude-opus-5", model)
	}
	if _, ok := m["max_tokens"]; !ok {
		t.Fatal("max_tokens must be preserved")
	}
	if _, ok := m["messages"]; !ok {
		t.Fatal("messages must be preserved")
	}
	// invalid json returns input unchanged
	bad := []byte(`not json`)
	if got := rewriteAnthropicBodyModel(bad, "a", "b"); string(got) != string(bad) {
		t.Fatal("invalid json should return input unchanged")
	}
}

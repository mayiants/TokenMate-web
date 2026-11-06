package plugin

import (
	"encoding/json"
	"regexp"
)

// ClaudeCodeCCHNormalizer rewrites the random cch= value inside Claude Code's
// x-anthropic-billing-header system prompt to a fixed constant. This keeps
// Anthropic prompt caching stable across requests that would otherwise have
// different billing/cache IDs.
type ClaudeCodeCCHNormalizer struct{}

func (ClaudeCodeCCHNormalizer) Name() string { return "claude_code_cch_normalizer" }

var cchRewriteRegex = regexp.MustCompile(`^(\s*x-anthropic-billing-header:[\s\S]*?cch=)[^;]+(;)`)

func (ClaudeCodeCCHNormalizer) Transform(body []byte, ctx Context) ([]byte, error) {
	if ctx.ClientFormat != "anthropic" && ctx.UpstreamFormat != "claude" {
		return body, nil
	}

	var req struct {
		System json.RawMessage `json:"system"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return body, err
	}
	if len(req.System) == 0 {
		return body, nil
	}

	modified := false

	// Case 1: system is a plain string.
	var systemStr string
	if err := json.Unmarshal(req.System, &systemStr); err == nil {
		if newSystem, ok := rewriteCCH(systemStr); ok {
			newSystemBytes, err := json.Marshal(newSystem)
			if err != nil {
				return body, err
			}
			req.System = newSystemBytes
			modified = true
		}
	} else {
		// Case 2: system is an array of content blocks.
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(req.System, &blocks); err == nil && len(blocks) > 0 {
			if newText, ok := rewriteCCH(blocks[0].Text); ok {
				blocks[0].Text = newText
				newBlocks, err := json.Marshal(blocks)
				if err != nil {
					return body, err
				}
				req.System = newBlocks
				modified = true
			}
		}
	}

	if !modified {
		return body, nil
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return body, err
	}
	raw["system"] = json.RawMessage(req.System)
	return json.Marshal(raw)
}

func rewriteCCH(text string) (string, bool) {
	trimmed := trimSpaceAndNewlines(text)
	if !startsWithBillingHeader(trimmed) {
		return text, false
	}
	if !cchRewriteRegex.MatchString(text) {
		return text, false
	}
	return cchRewriteRegex.ReplaceAllString(text, "${1}A1234${2}"), true
}

func trimSpaceAndNewlines(s string) string {
	return regexp.MustCompile(`^\s+`).ReplaceAllString(s, "")
}

func startsWithBillingHeader(s string) bool {
	const prefix = "x-anthropic-billing-header:"
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

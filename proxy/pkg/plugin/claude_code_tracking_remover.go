package plugin

import (
	"encoding/json"
	"regexp"
)

// ClaudeCodeTrackingRemover normalizes the dynamic date marker that Claude Code
// injects into the system prompt. The marker includes the current date in a
// locale-sensitive format, which breaks Anthropic prompt caching. Replacing it
// with a normalized ISO-like date keeps the prompt stable.
type ClaudeCodeTrackingRemover struct{}

func (ClaudeCodeTrackingRemover) Name() string { return "claude_code_tracking_remover" }

// Match "# currentDate\nToday(')s date is YYYY/MM/DD.\n" or similar separators.
var trackingRegex = regexp.MustCompile("(?m)(# currentDate\\r?\\n)Today(?:'|\u2019|\u02BC|\u02B9)s date is (\\d{4})[/-](\\d{2})[/-](\\d{2})\\.(\\r?\\n)")

func (ClaudeCodeTrackingRemover) Transform(body []byte, ctx Context) ([]byte, error) {
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
		newSystem := trackingRegex.ReplaceAllString(systemStr, "${1}Today's date is $2-$3-$4.$5")
		if newSystem != systemStr {
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
		if err := json.Unmarshal(req.System, &blocks); err == nil {
			for i := range blocks {
				if blocks[i].Type == "text" {
					newText := trackingRegex.ReplaceAllString(blocks[i].Text, "${1}Today's date is $2-$3-$4.$5")
					if newText != blocks[i].Text {
						blocks[i].Text = newText
						modified = true
					}
				}
			}
			if modified {
				newBlocks, err := json.Marshal(blocks)
				if err != nil {
					return body, err
				}
				req.System = newBlocks
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

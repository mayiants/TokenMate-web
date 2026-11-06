// Package sse provides a Server-Sent Events (SSE) parser suitable for AI gateway
// streaming responses. It handles byte-level chunking, multi-line data fields,
// and format-specific completion/error detection for OpenAI, Anthropic, and
// OpenAI Responses APIs.
package sse

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Format identifies the upstream/client SSE dialect.
type Format string

const (
	FormatOpenAI    Format = "openai"
	FormatAnthropic Format = "anthropic"
	FormatResponses Format = "responses"
	FormatUnknown   Format = "unknown"
)

// Event represents a single parsed SSE event.
type Event struct {
	// Data is the concatenation of all "data:" lines in the event.
	Data string `json:"data"`
	// Event is the value of the optional "event:" field.
	Event string `json:"event,omitempty"`
	// ID is the value of the optional "id:" field.
	ID string `json:"id,omitempty"`
}

// IsEmpty reports whether the event carries no data and no event type.
func (e Event) IsEmpty() bool {
	return e.Data == "" && e.Event == "" && e.ID == ""
}

// Parser parses a stream of SSE events from arbitrary byte chunks.
// It is not safe for concurrent use; each streaming response should have
// its own Parser instance.
type Parser struct {
	format Format
	buffer bytes.Buffer
}

// NewParser creates a new SSE parser. The format is used only for
// completion/error helpers (IsStreamCompleted, IsStreamError); parsing itself
// is format-agnostic.
func NewParser(format Format) *Parser {
	if format == "" {
		format = FormatUnknown
	}
	return &Parser{format: format}
}

// Format returns the parser's configured format.
func (p *Parser) Format() Format {
	return p.format
}

// Feed appends a chunk of bytes to the parser and returns any complete events.
// Incomplete events are buffered until the next Feed call provides the rest.
func (p *Parser) Feed(chunk []byte) ([]Event, error) {
	if len(chunk) == 0 {
		return nil, nil
	}
	p.buffer.Write(chunk)

	events, remaining := splitEvents(p.buffer.String())
	p.buffer.Reset()
	p.buffer.WriteString(remaining)

	result := make([]Event, 0, len(events))
	for _, raw := range events {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		ev, err := parseEvent(raw)
		if err != nil {
			return nil, fmt.Errorf("parse sse event: %w", err)
		}
		if !ev.IsEmpty() {
			result = append(result, ev)
		}
	}
	return result, nil
}

// Flush drains the internal buffer and returns any final event, even if it
// was not terminated by a blank line. Call this when the upstream stream ends.
func (p *Parser) Flush() ([]Event, error) {
	if p.buffer.Len() == 0 {
		return nil, nil
	}
	raw := strings.TrimSpace(p.buffer.String())
	p.buffer.Reset()
	if raw == "" {
		return nil, nil
	}
	ev, err := parseEvent(raw)
	if err != nil {
		return nil, fmt.Errorf("flush sse event: %w", err)
	}
	if ev.IsEmpty() {
		return nil, nil
	}
	return []Event{ev}, nil
}

// splitEvents splits a buffer into complete SSE events and a remaining
// incomplete tail. Events are separated by two consecutive line terminators
// (\n\n). It also tolerates \r\n\r\n and mixed line endings.
func splitEvents(buffer string) ([]string, string) {
	// Normalize double CRLF to double LF for easier splitting while preserving
	// single CR inside data lines.
	normalized := strings.ReplaceAll(buffer, "\r\n", "\n")
	parts := strings.Split(normalized, "\n\n")
	if len(parts) == 0 {
		return nil, ""
	}
	// The last part may be incomplete; keep it in the buffer.
	remaining := parts[len(parts)-1]
	return parts[:len(parts)-1], remaining
}

// parseEvent parses a single raw SSE event into an Event struct.
func parseEvent(raw string) (Event, error) {
	var ev Event
	var dataLines []string

	lines := strings.Split(raw, "\n")
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data:"))
			continue
		}
		if strings.HasPrefix(line, "event:") {
			ev.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "id:") {
			ev.ID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
			continue
		}
		if strings.HasPrefix(line, ":") { //nolint:staticcheck // SA4017 false positive: return value IS used in if condition
			// Comment line; ignore.
			continue
		}
		// Unknown field; ignore per SSE spec.
	}

	// Per SSE spec, trim a single leading space from each data line.
	for i, line := range dataLines {
		if len(line) > 0 && line[0] == ' ' {
			dataLines[i] = line[1:]
		}
	}
	ev.Data = strings.Join(dataLines, "\n")

	return ev, nil
}

// IsStreamCompleted reports whether the event signals the end of the stream
// for the parser's configured format.
func (p *Parser) IsStreamCompleted(ev Event) bool {
	return IsStreamCompleted(p.format, ev)
}

// IsStreamError reports whether the event represents a terminal stream error.
func (p *Parser) IsStreamError(ev Event) bool {
	return IsStreamError(p.format, ev)
}

// IsStreamCompleted reports whether the event signals the end of the stream
// for the given format.
func IsStreamCompleted(format Format, ev Event) bool {
	switch format {
	case FormatOpenAI:
		return ev.Data == "[DONE]"
	case FormatAnthropic:
		if ev.Event == "message_stop" {
			return true
		}
		return jsonEventType(ev.Data) == "message_stop"
	case FormatResponses:
		return jsonEventType(ev.Data) == "response.completed"
	default:
		return false
	}
}

// IsStreamError reports whether the event represents a terminal stream error.
func IsStreamError(format Format, ev Event) bool {
	if ev.Event == "error" {
		return true
	}
	// Some providers embed error type inside the JSON data.
	typ := jsonEventType(ev.Data)
	if typ == "error" {
		return true
	}
	var payload struct {
		Error interface{} `json:"error"`
	}
	if json.Unmarshal([]byte(ev.Data), &payload) == nil && payload.Error != nil {
		return true
	}
	return false
}

// jsonEventType extracts the "type" field from JSON data if present.
func jsonEventType(data string) string {
	if data == "" {
		return ""
	}
	var payload struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return ""
	}
	return payload.Type
}

package aws

import (
	"bytes"
	"encoding/binary"
	"io"
	"strings"
	"testing"
)

// buildEventStreamFrame constructs a real Amazon EventStream frame as
// Bedrock ConverseStream emits it on the wire: the event type rides in the
// `:event-type` header and the payload is the bare event body (no outer
// wrapper key). CRC fields are zeroed — the converter does not verify them.
func buildEventStreamFrame(eventType, payload string) []byte {
	// headers: [1B nameLen][name][1B valueType=7(string)][2B valueLen][value]
	var headers bytes.Buffer
	headers.WriteByte(byte(len(":event-type")))
	headers.WriteString(":event-type")
	headers.WriteByte(7)
	var vl [2]byte
	binary.BigEndian.PutUint16(vl[:], uint16(len(eventType)))
	headers.Write(vl[:])
	headers.WriteString(eventType)

	totalLen := 8 + 4 + headers.Len() + len(payload) + 4
	frame := make([]byte, 0, totalLen)
	var pre [8]byte
	binary.BigEndian.PutUint32(pre[0:4], uint32(totalLen))
	binary.BigEndian.PutUint32(pre[4:8], uint32(headers.Len()))
	frame = append(frame, pre[:]...)
	frame = append(frame, 0, 0, 0, 0) // prelude CRC (unchecked)
	frame = append(frame, headers.Bytes()...)
	frame = append(frame, payload...)
	frame = append(frame, 0, 0, 0, 0) // message CRC (unchecked)
	return frame
}

// TestConverseStreamConverter_RealWireFormat replays a realistic Bedrock
// ConverseStream session — messageStart, content deltas (with the "p"
// padding field Bedrock includes), messageStop, metadata — and asserts the
// converter emits proper OpenAI SSE chunks.
func TestConverseStreamConverter_RealWireFormat(t *testing.T) {
	var src bytes.Buffer
	src.Write(buildEventStreamFrame("messageStart", `{"p":"abcdefghij","role":"assistant"}`))
	src.Write(buildEventStreamFrame("contentBlockStart", `{"p":"aaaaaaaa","start":{},"contentBlockIndex":0}`))
	src.Write(buildEventStreamFrame("contentBlockDelta", `{"delta":{"text":"Hello"},"contentBlockIndex":0,"p":"bbbbbbbb"}`))
	src.Write(buildEventStreamFrame("contentBlockDelta", `{"delta":{"text":" world"},"contentBlockIndex":0,"p":"cccccccc"}`))
	src.Write(buildEventStreamFrame("contentBlockStop", `{"p":"dddddddd","contentBlockIndex":0}`))
	src.Write(buildEventStreamFrame("messageStop", `{"p":"eeeeeeee","stopReason":"end_turn"}`))
	src.Write(buildEventStreamFrame("metadata", `{"usage":{"inputTokens":12,"outputTokens":5,"totalTokens":17},"metrics":{"latencyMs":340},"p":"ffffffff"}`))

	conv := newConverseStreamConverter(io.NopCloser(&src), "claude-test")
	out, err := io.ReadAll(conv)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	body := string(out)

	for _, want := range []string{
		`"content":"Hello"`,
		`"content":" world"`,
		`"finish_reason":"stop"`,
		`"usage":{`,
		`"prompt_tokens":12`,
		`"completion_tokens":5`,
		`"total_tokens":17`,
		"data: [DONE]",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("output missing %q\n---\n%s", want, body)
		}
	}
	if strings.Contains(body, `"content":"abcdef"`) {
		t.Error("padding field leaked into output")
	}
}

// TestConverseStreamConverter_ExceptionFrame verifies that an upstream
// exception frame surfaces as an SSE error event instead of a silent hang.
func TestConverseStreamConverter_ExceptionFrame(t *testing.T) {
	var src bytes.Buffer
	src.Write(buildEventStreamFrame("throttlingException", `{"message":"Rate exceeded"}`))

	conv := newConverseStreamConverter(io.NopCloser(&src), "claude-test")
	out, err := io.ReadAll(conv)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	body := string(out)
	if !strings.Contains(body, "Rate exceeded") || !strings.Contains(body, "upstream_error") {
		t.Errorf("expected SSE error event, got:\n%s", body)
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Errorf("expected stream termination, got:\n%s", body)
	}
}

// TestConverseStreamConverter_ToolUse replays a Bedrock tool_use streaming
// session and asserts the emitted chunks follow the OpenAI SSE spec:
// index always present (including 0), id/type/name only on the first delta,
// arguments-only follow-up deltas, and finish_reason "tool_calls".
func TestConverseStreamConverter_ToolUse(t *testing.T) {
	var src bytes.Buffer
	src.Write(buildEventStreamFrame("messageStart", `{"role":"assistant","p":"aaaaaaaa"}`))
	src.Write(buildEventStreamFrame("contentBlockStart", `{"start":{"toolUse":{"toolUseId":"tooluse_123","name":"get_weather"}},"contentBlockIndex":0,"p":"bbbbbbbb"}`))
	src.Write(buildEventStreamFrame("contentBlockDelta", `{"delta":{"toolUse":{"input":"{\"city\":"}},"contentBlockIndex":0,"p":"cccccccc"}`))
	src.Write(buildEventStreamFrame("contentBlockDelta", `{"delta":{"toolUse":{"input":"\"Tokyo\"}"}},"contentBlockIndex":0,"p":"dddddddd"}`))
	src.Write(buildEventStreamFrame("contentBlockStop", `{"contentBlockIndex":0,"p":"eeeeeeee"}`))
	src.Write(buildEventStreamFrame("messageStop", `{"stopReason":"tool_use","p":"ffffffff"}`))
	src.Write(buildEventStreamFrame("metadata", `{"usage":{"inputTokens":10,"outputTokens":7,"totalTokens":17},"p":"gggggggg"}`))

	conv := newConverseStreamConverter(io.NopCloser(&src), "claude-test")
	out, err := io.ReadAll(conv)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	body := string(out)

	for _, want := range []string{
		`"index":0`,
		`"id":"tooluse_123"`,
		`"type":"function"`,
		`"name":"get_weather"`,
		`"arguments":"{\"city\":"`,
		`"arguments":"\"Tokyo\"}"`,
		`"finish_reason":"tool_calls"`,
		"data: [DONE]",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("output missing %q\n---\n%s", want, body)
		}
	}
	// Follow-up argument deltas must not carry empty id/type/name fields,
	// or naive clients merge them over the accumulated tool call.
	if strings.Contains(body, `"id":""`) || strings.Contains(body, `"type":""`) || strings.Contains(body, `"name":""`) {
		t.Errorf("empty id/type/name leaked into argument deltas\n---\n%s", body)
	}
}

func TestParseEventStreamEventType(t *testing.T) {
	frame := buildEventStreamFrame("contentBlockDelta", `{}`)
	headersLen := binary.BigEndian.Uint32(frame[4:8])
	headers := frame[12 : 12+headersLen]
	if got := parseEventStreamEventType(headers); got != "contentBlockDelta" {
		t.Errorf("got %q, want contentBlockDelta", got)
	}
	// Truncated / empty headers must not panic.
	if got := parseEventStreamEventType(nil); got != "" {
		t.Errorf("empty headers: got %q", got)
	}
	if got := parseEventStreamEventType(headers[:len(headers)/2]); got != "" {
		t.Errorf("truncated headers: got %q", got)
	}
}

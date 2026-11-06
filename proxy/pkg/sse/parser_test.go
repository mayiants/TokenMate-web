package sse

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParser_SingleEvent(t *testing.T) {
	p := NewParser(FormatOpenAI)
	events, err := p.Feed([]byte("data: hello\n\n"))
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "hello", events[0].Data)
}

func TestParser_MultipleEventsInOneChunk(t *testing.T) {
	p := NewParser(FormatOpenAI)
	events, err := p.Feed([]byte("data: first\n\ndata: second\n\n"))
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, "first", events[0].Data)
	assert.Equal(t, "second", events[1].Data)
}

func TestParser_CrossChunkBoundary(t *testing.T) {
	p := NewParser(FormatOpenAI)
	events, err := p.Feed([]byte("data: hel"))
	require.NoError(t, err)
	assert.Empty(t, events)

	events, err = p.Feed([]byte("lo\n\n"))
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "hello", events[0].Data)
}

func TestParser_MultiLineData(t *testing.T) {
	p := NewParser(FormatOpenAI)
	events, err := p.Feed([]byte("data: line1\ndata: line2\n\n"))
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "line1\nline2", events[0].Data)
}

func TestParser_EventAndID(t *testing.T) {
	p := NewParser(FormatAnthropic)
	events, err := p.Feed([]byte("event: message_start\nid: 123\ndata: {}\n\n"))
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "message_start", events[0].Event)
	assert.Equal(t, "123", events[0].ID)
	assert.Equal(t, "{}", events[0].Data)
}

func TestParser_CRLFLineEndings(t *testing.T) {
	p := NewParser(FormatOpenAI)
	events, err := p.Feed([]byte("data: hello\r\n\r\ndata: world\r\n\r\n"))
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, "hello", events[0].Data)
	assert.Equal(t, "world", events[1].Data)
}

func TestParser_IgnoresComments(t *testing.T) {
	p := NewParser(FormatOpenAI)
	events, err := p.Feed([]byte(": heartbeat\n\ndata: hello\n\n"))
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "hello", events[0].Data)
}

func TestParser_FlushTrailingData(t *testing.T) {
	p := NewParser(FormatOpenAI)
	_, err := p.Feed([]byte("data: trailing"))
	require.NoError(t, err)

	events, err := p.Flush()
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "trailing", events[0].Data)
}

func TestIsStreamCompleted_OpenAI(t *testing.T) {
	p := NewParser(FormatOpenAI)
	assert.True(t, p.IsStreamCompleted(Event{Data: "[DONE]"}))
	assert.False(t, p.IsStreamCompleted(Event{Data: `{"choices":[]}`}))
}

func TestIsStreamCompleted_Anthropic(t *testing.T) {
	p := NewParser(FormatAnthropic)
	assert.True(t, p.IsStreamCompleted(Event{Event: "message_stop"}))
	assert.True(t, p.IsStreamCompleted(Event{Data: `{"type":"message_stop"}`}))
	assert.False(t, p.IsStreamCompleted(Event{Data: `{"type":"content_block_delta"}`}))
}

func TestIsStreamCompleted_Responses(t *testing.T) {
	p := NewParser(FormatResponses)
	assert.True(t, p.IsStreamCompleted(Event{Data: `{"type":"response.completed"}`}))
	assert.False(t, p.IsStreamCompleted(Event{Data: `{"type":"response.output_text.delta"}`}))
}

func TestIsStreamError(t *testing.T) {
	p := NewParser(FormatOpenAI)
	assert.True(t, p.IsStreamError(Event{Event: "error"}))
	assert.True(t, p.IsStreamError(Event{Data: `{"type":"error"}`}))
	assert.True(t, p.IsStreamError(Event{Data: `{"error":{"message":"boom"}}`}))
	assert.False(t, p.IsStreamError(Event{Data: `{"choices":[]}`}))
}

func TestParser_EmptyFeed(t *testing.T) {
	p := NewParser(FormatOpenAI)
	events, err := p.Feed(nil)
	require.NoError(t, err)
	assert.Empty(t, events)
}

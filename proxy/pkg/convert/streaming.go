package convert

import (
	cryptorand "crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/mayiants/ai-proxy-go/dto"
)

// AnthropicStreamState tracks incremental state for OpenAI SSE → Anthropic SSE conversion
type AnthropicStreamState struct {
	MessageID   string
	Model       string
	BlockIndex  int
	InToolCall  bool
	ToolIndex   int
	ToolID      string
	ToolName    string
	PendingArgs strings.Builder
	HasStarted  bool
	HasStopped  bool
}

// OpenAIChunkToAnthropicEvent converts a single OpenAI SSE chunk to Anthropic SSE event(s)
// Returns a slice of SSE lines (each line is a complete event: "event: X\ndata: {...}")
func OpenAIChunkToAnthropicEvent(chunk []byte, state *AnthropicStreamState) ([]string, error) {
	var openaiResp dto.ChatCompletionsStreamResponse
	if err := json.Unmarshal(chunk, &openaiResp); err != nil {
		return nil, err
	}

	var events []string

	// Handle message_start on first chunk
	if !state.HasStarted && len(openaiResp.Choices) > 0 {
		state.HasStarted = true
		state.MessageID = openaiResp.ID
		// Caller may preset Model (client-facing logical model); only adopt
		// the chunk's model otherwise — upstream chunks can carry provider
		// IDs (e.g. Bedrock "anthropic.claude-...-v1") that must not leak.
		if state.Model == "" {
			state.Model = openaiResp.Model
		}
		if state.MessageID == "" {
			state.MessageID = "msg_" + generateShortID()
		}

		// Emit message_start
		startEvent := map[string]interface{}{
			"type": "message_start",
			"message": map[string]interface{}{
				"id":          state.MessageID,
				"type":        "message",
				"role":        "assistant",
				"model":       state.Model,
				"content":     []interface{}{},
				"stop_reason": nil,
			},
		}
		startJSON, _ := json.Marshal(startEvent)
		events = append(events, fmt.Sprintf("event: message_start\ndata: %s", startJSON))

		// Emit content_block_start for first text block
		blockStart := map[string]interface{}{
			"type":          "content_block_start",
			"index":         state.BlockIndex,
			"content_block": map[string]interface{}{"type": "text", "text": ""},
		}
		blockJSON, _ := json.Marshal(blockStart)
		events = append(events, fmt.Sprintf("event: content_block_start\ndata: %s", blockJSON))
	}

	for _, choice := range openaiResp.Choices {
		delta := choice.Delta

		// Handle text content
		if delta.Content != nil && *delta.Content != "" {
			textDelta := map[string]interface{}{
				"type":  "content_block_delta",
				"index": state.BlockIndex,
				"delta": map[string]interface{}{
					"type": "text_delta",
					"text": *delta.Content,
				},
			}
			deltaJSON, _ := json.Marshal(textDelta)
			events = append(events, fmt.Sprintf("event: content_block_delta\ndata: %s", deltaJSON))
		}

		// Handle reasoning content (thinking)
		if delta.ReasoningContent != nil && *delta.ReasoningContent != "" {
			thinkingDelta := map[string]interface{}{
				"type":  "content_block_delta",
				"index": state.BlockIndex,
				"delta": map[string]interface{}{
					"type":     "thinking_delta",
					"thinking": *delta.ReasoningContent,
				},
			}
			deltaJSON, _ := json.Marshal(thinkingDelta)
			events = append(events, fmt.Sprintf("event: content_block_delta\ndata: %s", deltaJSON))
		}

		// Handle tool_calls
		for _, tc := range delta.ToolCalls {
			if tc.ID != "" && tc.Function.Name != "" {
				// New tool call start
				if state.InToolCall {
					// Close previous tool call
					closeEvents := closeCurrentToolCall(state)
					events = append(events, closeEvents...)
				}
				state.InToolCall = true
				state.ToolID = tc.ID
				state.ToolName = tc.Function.Name
				state.PendingArgs.Reset()
				state.ToolIndex = tc.Index

				// Emit content_block_stop for previous text block
				blockStop := map[string]interface{}{
					"type":  "content_block_stop",
					"index": state.BlockIndex,
				}
				stopJSON, _ := json.Marshal(blockStop)
				events = append(events, fmt.Sprintf("event: content_block_stop\ndata: %s", stopJSON))
				state.BlockIndex++

				// Emit content_block_start for tool_use
				toolStart := map[string]interface{}{
					"type":  "content_block_start",
					"index": state.BlockIndex,
					"content_block": map[string]interface{}{
						"type":  "tool_use",
						"id":    tc.ID,
						"name":  tc.Function.Name,
						"input": map[string]interface{}{},
					},
				}
				toolJSON, _ := json.Marshal(toolStart)
				events = append(events, fmt.Sprintf("event: content_block_start\ndata: %s", toolJSON))
			} else if state.InToolCall && tc.Function.Arguments != "" {
				// Tool call arguments delta
				state.PendingArgs.WriteString(tc.Function.Arguments)
				argDelta := map[string]interface{}{
					"type":  "content_block_delta",
					"index": state.BlockIndex,
					"delta": map[string]interface{}{
						"type":         "input_json_delta",
						"partial_json": tc.Function.Arguments,
					},
				}
				argJSON, _ := json.Marshal(argDelta)
				events = append(events, fmt.Sprintf("event: content_block_delta\ndata: %s", argJSON))
			}
		}

		// Handle finish_reason
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			// Close any open tool call
			if state.InToolCall {
				closeEvents := closeCurrentToolCall(state)
				events = append(events, closeEvents...)
			} else {
				// Close text block
				blockStop := map[string]interface{}{
					"type":  "content_block_stop",
					"index": state.BlockIndex,
				}
				stopJSON, _ := json.Marshal(blockStop)
				events = append(events, fmt.Sprintf("event: content_block_stop\ndata: %s", stopJSON))
			}

			// Emit message_delta with stop_reason
			stopReason := openAIStopReasonToAnthropic(*choice.FinishReason)
			msgDelta := map[string]interface{}{
				"type": "message_delta",
				"delta": map[string]interface{}{
					"stop_reason": stopReason,
				},
			}
			if openaiResp.Usage != nil {
				msgDelta["usage"] = map[string]interface{}{
					"input_tokens":  openaiResp.Usage.PromptTokens,
					"output_tokens": openaiResp.Usage.CompletionTokens,
				}
			}
			deltaJSON, _ := json.Marshal(msgDelta)
			events = append(events, fmt.Sprintf("event: message_delta\ndata: %s", deltaJSON))

			// Emit message_stop
			msgStop := map[string]interface{}{"type": "message_stop"}
			stopJSON2, _ := json.Marshal(msgStop)
			events = append(events, fmt.Sprintf("event: message_stop\ndata: %s", stopJSON2))
			state.HasStopped = true
		}
	}

	return events, nil
}

// closeCurrentToolCall closes the current tool call block and increments block index
func closeCurrentToolCall(state *AnthropicStreamState) []string {
	var events []string
	// Emit content_block_stop for tool_use
	blockStop := map[string]interface{}{
		"type":  "content_block_stop",
		"index": state.BlockIndex,
	}
	stopJSON, _ := json.Marshal(blockStop)
	events = append(events, fmt.Sprintf("event: content_block_stop\ndata: %s", stopJSON))
	state.BlockIndex++
	state.InToolCall = false
	return events
}

// generateShortID generates a short random ID for message IDs
func generateShortID() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	result := make([]byte, 10)
	for i := range result {
		n, err := cryptorand.Int(cryptorand.Reader, big.NewInt(int64(len(chars))))
		if err != nil {
			// Extremely unlikely; fall back to a uuid-style hex segment
			var b [5]byte
			cryptorand.Read(b[:]) //nolint:errcheck
			return fmt.Sprintf("%x", b)
		}
		result[i] = chars[n.Int64()]
	}
	return string(result)
}

// AnthropicStreamDone returns the final SSE done marker for Anthropic format
func AnthropicStreamDone() string {
	return "event: message_stop\ndata: {\"type\":\"message_stop\"}\n"
}

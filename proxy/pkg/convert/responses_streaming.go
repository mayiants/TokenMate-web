package convert

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ai-proxy/ai-proxy-go/dto"
)

// ResponsesStreamState tracks incremental state for OpenAI SSE → Responses SSE conversion.
type ResponsesStreamState struct {
	ResponseID   string
	Model        string
	HasStarted   bool
	OutputIndex  int
	ContentIndex int
	InToolCall   bool
	ToolIndex    int
	ToolID       string
	ToolName     string
	MessageID    string
	// Items accumulates completed output items (message, function_call) for
	// the final response.completed event's output array.
	Items       []interface{}
	PendingArgs strings.Builder
	TextSoFar   strings.Builder
	// CreatedAt is the unix timestamp stamped on every Response object we
	// emit. OpenAI's spec marks created_at required; strict clients (codex
	// CLI) fail deserialization without it and report the stream as
	// disconnected (2026-10-24).
	CreatedAt int64
}

// OpenAIChunkToResponsesEvent converts a single OpenAI SSE chunk to Responses SSE event(s).
// Returns a slice of SSE lines (each line is a complete event).
func OpenAIChunkToResponsesEvent(chunk []byte, state *ResponsesStreamState) ([]string, error) {
	var openaiResp dto.ChatCompletionsStreamResponse
	if err := json.Unmarshal(chunk, &openaiResp); err != nil {
		return nil, err
	}

	var events []string

	// Handle response.created on first chunk
	if !state.HasStarted && len(openaiResp.Choices) > 0 {
		state.HasStarted = true
		state.ResponseID = openaiResp.ID
		if state.Model == "" {
			state.Model = openaiResp.Model
		}
		if state.ResponseID == "" {
			state.ResponseID = "resp_" + generateShortID()
		}

		// Emit response.created
		createdEvent := map[string]interface{}{
			"type": "response.created",
			"response": map[string]interface{}{
				"id":         state.ResponseID,
				"object":     "response",
				"created_at": state.CreatedAt,
				"status":     "in_progress",
				"model":      state.Model,
				"output":     []interface{}{},
			},
		}
		createdJSON, _ := json.Marshal(createdEvent)
		events = append(events, fmt.Sprintf("event: response.created\ndata: %s", createdJSON))

		// Emit response.output_item.added for message. The item id is required
		// by the spec (items are addressable); strict clients (codex CLI) fail
		// deserialization without it.
		if state.MessageID == "" {
			state.MessageID = "msg_" + generateShortID()
		}
		itemAdded := map[string]interface{}{
			"type":         "response.output_item.added",
			"output_index": state.OutputIndex,
			"item": map[string]interface{}{
				"id":      state.MessageID,
				"type":    "message",
				"role":    "assistant",
				"status":  "in_progress",
				"content": []interface{}{},
			},
		}
		itemJSON, _ := json.Marshal(itemAdded)
		events = append(events, fmt.Sprintf("event: response.output_item.added\ndata: %s", itemJSON))

		// Emit content_part.added for output_text
		partAdded := map[string]interface{}{
			"type":          "content_part.added",
			"output_index":  state.OutputIndex,
			"content_index": state.ContentIndex,
			"part": map[string]interface{}{
				"type": "output_text",
				"text": "",
			},
		}
		partJSON, _ := json.Marshal(partAdded)
		events = append(events, fmt.Sprintf("event: content_part.added\ndata: %s", partJSON))
	}

	for _, choice := range openaiResp.Choices {
		delta := choice.Delta

		// Handle text content delta
		if delta.Content != nil && *delta.Content != "" {
			state.TextSoFar.WriteString(*delta.Content)
			textDelta := map[string]interface{}{
				"type":          "output_text.delta",
				"output_index":  state.OutputIndex,
				"content_index": state.ContentIndex,
				"delta": map[string]interface{}{
					"type": "output_text_delta",
					"text": *delta.Content,
				},
			}
			deltaJSON, _ := json.Marshal(textDelta)
			events = append(events, fmt.Sprintf("event: output_text.delta\ndata: %s", deltaJSON))
		}

		// Handle tool_calls
		for _, tc := range delta.ToolCalls {
			if tc.ID != "" && tc.Function.Name != "" {
				// New tool call
				if state.InToolCall {
					closeEvents := closeCurrentResponsesToolCall(state)
					events = append(events, closeEvents...)
				}
				state.InToolCall = true
				state.ToolID = tc.ID
				state.ToolName = tc.Function.Name
				state.ToolIndex = tc.Index
				state.PendingArgs.Reset()

				// Emit response.output_item.added for function_call
				itemAdded := map[string]interface{}{
					"type":         "response.output_item.added",
					"output_index": state.OutputIndex,
					"item": map[string]interface{}{
						"type":      "function_call",
						"id":        tc.ID,
						"call_id":   tc.ID,
						"name":      tc.Function.Name,
						"arguments": "",
						"status":    "in_progress",
					},
				}
				itemJSON, _ := json.Marshal(itemAdded)
				events = append(events, fmt.Sprintf("event: response.output_item.added\ndata: %s", itemJSON))
			} else if state.InToolCall && tc.Function.Arguments != "" {
				state.PendingArgs.WriteString(tc.Function.Arguments)
				argDelta := map[string]interface{}{
					"type":         "response.function_call_arguments.delta",
					"output_index": state.OutputIndex,
					"item_id":      state.ToolID,
					"delta":        tc.Function.Arguments,
				}
				argJSON, _ := json.Marshal(argDelta)
				events = append(events, fmt.Sprintf("event: response.function_call_arguments.delta\ndata: %s", argJSON))
			}
		}

		// Handle finish_reason
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			// Close any open tool call
			if state.InToolCall {
				closeEvents := closeCurrentResponsesToolCall(state)
				events = append(events, closeEvents...)
			}

			// Emit content_part.done for text
			if state.TextSoFar.Len() > 0 {
				partDone := map[string]interface{}{
					"type":          "content_part.done",
					"output_index":  state.OutputIndex,
					"content_index": state.ContentIndex,
					"part": map[string]interface{}{
						"type": "output_text",
						"text": state.TextSoFar.String(),
					},
				}
				partJSON, _ := json.Marshal(partDone)
				events = append(events, fmt.Sprintf("event: content_part.done\ndata: %s", partJSON))
			}

			// Emit response.output_item.done for message
			itemDone := map[string]interface{}{
				"type":         "response.output_item.done",
				"output_index": state.OutputIndex,
				"item": map[string]interface{}{
					"id":     state.MessageID,
					"type":   "message",
					"role":   "assistant",
					"status": "completed",
					"content": []map[string]interface{}{
						{
							"type": "output_text",
							"text": state.TextSoFar.String(),
						},
					},
				},
			}
			itemJSON, _ := json.Marshal(itemDone)
			events = append(events, fmt.Sprintf("event: response.output_item.done\ndata: %s", itemJSON))

			// Track the message item for the final response.completed output.
			state.Items = append(state.Items, itemDone["item"])

			// Emit response.completed
			status := "completed"
			if *choice.FinishReason == "length" {
				status = "incomplete"
			}
			output := state.Items
			if output == nil {
				output = []interface{}{}
			}
			completed := map[string]interface{}{
				"type": "response.completed",
				"response": map[string]interface{}{
					"id":         state.ResponseID,
					"object":     "response",
					"created_at": state.CreatedAt,
					"status":     status,
					"model":      state.Model,
					"output":     output,
				},
			}
			if openaiResp.Usage != nil {
				completed["response"].(map[string]interface{})["usage"] = map[string]interface{}{
					"input_tokens":  openaiResp.Usage.PromptTokens,
					"output_tokens": openaiResp.Usage.CompletionTokens,
					"total_tokens":  openaiResp.Usage.TotalTokens,
				}
			}
			completedJSON, _ := json.Marshal(completed)
			events = append(events, fmt.Sprintf("event: response.completed\ndata: %s", completedJSON))
		}
	}

	return events, nil
}

// closeCurrentResponsesToolCall closes the current tool call in Responses stream.
func closeCurrentResponsesToolCall(state *ResponsesStreamState) []string {
	var events []string

	// Emit response.function_call_arguments.done
	argDone := map[string]interface{}{
		"type":         "response.function_call_arguments.done",
		"output_index": state.OutputIndex,
		"item_id":      state.ToolID,
		"arguments":    state.PendingArgs.String(),
	}
	argJSON, _ := json.Marshal(argDone)
	events = append(events, fmt.Sprintf("event: response.function_call_arguments.done\ndata: %s", argJSON))

	// Emit response.output_item.done for function_call
	item := map[string]interface{}{
		"type":      "function_call",
		"id":        state.ToolID,
		"call_id":   state.ToolID,
		"name":      state.ToolName,
		"arguments": state.PendingArgs.String(),
		"status":    "completed",
	}
	itemDone := map[string]interface{}{
		"type":         "response.output_item.done",
		"output_index": state.OutputIndex,
		"item":         item,
	}
	itemJSON, _ := json.Marshal(itemDone)
	events = append(events, fmt.Sprintf("event: response.output_item.done\ndata: %s", itemJSON))

	// Track for the final response.completed output array. Strict clients
	// (codex CLI) expect the completed Response to carry its output items.
	state.Items = append(state.Items, item)

	state.OutputIndex++
	state.InToolCall = false
	return events
}

// ResponsesStreamDone returns the final SSE done marker for Responses format.
func ResponsesStreamDone() string {
	return "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n"
}

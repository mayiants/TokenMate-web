package server

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ai-proxy/ai-proxy-go/dto"
)

func TestJoinAgentToolNames(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  string
	}{
		{"empty", nil, ""},
		{"all empty", []string{"", ""}, ""},
		{"single", []string{"get_weather"}, "get_weather"},
		{"dedup preserves order", []string{"read_file", "write_file", "read_file", "run_command"}, "read_file,write_file,run_command"},
		{"skips empties", []string{"", "a", "", "b"}, "a,b"},
		{"caps at 20", func() []string {
			n := make([]string, 0, 25)
			for i := 0; i < 25; i++ {
				n = append(n, string(rune('a'+i%26))+string(rune('0'+i/26)))
			}
			return n
		}(), func() string {
			out := ""
			for i := 0; i < 20; i++ {
				if i > 0 {
					out += ","
				}
				out += string(rune('a'+i%26)) + string(rune('0'+i/26))
			}
			return out
		}()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := joinAgentToolNames(tt.input); got != tt.want {
				t.Errorf("joinAgentToolNames(%v) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func newTestGinContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	return c
}

func TestCalledToolsFromContext(t *testing.T) {
	// nil context value
	c := newTestGinContext()
	if got := calledToolsFromContext(c); got != "" {
		t.Errorf("empty context: got %q", got)
	}
	// counts sorted desc, then name asc; serialized as name:count
	noteCalledTool(c, "write_file")
	noteCalledTool(c, "read_file")
	noteCalledTool(c, "read_file")
	noteCalledTool(c, "run_command")
	if got := calledToolsFromContext(c); got != "read_file:2,run_command:1,write_file:1" {
		t.Errorf("got %q", got)
	}
}

func TestTrackOpenAIStreamToolCalls(t *testing.T) {
	frags := map[int]*strings.Builder{}
	chunk := func(nameFrag string, idx int) *dto.ChatCompletionsStreamResponse {
		return &dto.ChatCompletionsStreamResponse{
			Choices: []dto.ChatCompletionsStreamResponseChoice{{
				Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
					ToolCalls: []dto.ToolCall{{
						Index:    idx,
						Function: dto.FunctionCall{Name: nameFrag},
					}},
				},
			}},
		}
	}
	trackOpenAIStreamToolCalls(frags, chunk("read_", 0))
	trackOpenAIStreamToolCalls(frags, chunk("file", 0))
	trackOpenAIStreamToolCalls(frags, chunk("get_weather", 1))
	trackOpenAIStreamToolCalls(frags, nil) // must not panic
	c := newTestGinContext()
	flushStreamToolCalls(c, frags)
	flushStreamToolCalls(c, frags) // idempotent: second flush adds nothing
	if got := calledToolsFromContext(c); got != "get_weather:1,read_file:1" {
		t.Errorf("got %q", got)
	}
}

func TestTrackOpenAIMessageToolCalls(t *testing.T) {
	c := newTestGinContext()
	msg := dto.Message{ToolCalls: []dto.ToolCall{
		{Function: dto.FunctionCall{Name: "get_weather"}},
		{Function: dto.FunctionCall{Name: "get_weather"}},
	}}
	trackOpenAIMessageToolCalls(c, msg)
	if got := calledToolsFromContext(c); got != "get_weather:2" {
		t.Errorf("got %q", got)
	}
}

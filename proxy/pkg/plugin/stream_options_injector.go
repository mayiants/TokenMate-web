package plugin

import (
	"encoding/json"
)

// StreamOptionsInjector injects stream_options.include_usage into OpenAI-format
// streaming requests when the client has not already set stream_options.
type StreamOptionsInjector struct{}

func (StreamOptionsInjector) Name() string { return "stream_options_injector" }

func (StreamOptionsInjector) Transform(body []byte, ctx Context) ([]byte, error) {
	if ctx.UpstreamFormat != "openai" {
		return body, nil
	}

	var req struct {
		Stream        *bool `json:"stream,omitempty"`
		StreamOptions *struct {
			IncludeUsage bool `json:"include_usage,omitempty"`
		} `json:"stream_options,omitempty"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return body, err
	}

	if req.Stream == nil || !*req.Stream {
		return body, nil
	}
	if req.StreamOptions != nil {
		return body, nil
	}

	// Inject stream_options.include_usage into the raw JSON without redefining
	// the full request struct.
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return body, err
	}
	raw["stream_options"] = map[string]any{"include_usage": true}
	return json.Marshal(raw)
}

package server

import (
	"net/http"
	"sort"
	"time"

	"github.com/mayiants/ai-proxy-go/pkg/models"
	"github.com/gin-gonic/gin"
)

// modelListItem is the OpenAI-compatible model object returned by GET /v1/models.
type modelListItem struct {
	ID           string                 `json:"id"`
	Object       string                 `json:"object"`
	Created      int64                  `json:"created"`
	OwnedBy      string                 `json:"owned_by"`
	Capabilities map[string]interface{} `json:"capabilities,omitempty"`
}

// keyLimits exposes the current API key's rate/concurrency limits.
type keyLimits struct {
	Concurrent int `json:"concurrent"`
	RPM        int `json:"rpm"`
	TPM        int `json:"tpm"`
}

// handleListModels returns the list of active models available to the current API key.
// It mirrors the OpenAI /v1/models response shape and extends each model with a
// capabilities object plus a key_limits summary.
//
// The returned list respects the key's routing mode:
//   - single        -> current_model (or allowed_models if no current_model)
//   - round_robin   -> selected_models (or allowed_models if none selected)
//   - priority      -> selected_models (or allowed_models if none selected)
func (s *HandlerService) handleListModels(c *gin.Context) {
	var allowedModels, selectedModels []string
	if v, ok := c.Get("allowed_models"); ok {
		allowedModels, _ = v.([]string)
	}
	if v, ok := c.Get("selected_models"); ok {
		selectedModels, _ = v.([]string)
	}
	currentModel, _ := c.Get("current_model")
	routingMode, _ := c.Get("model_routing_mode")

	// Determine the effective model set the client is supposed to use.
	effectiveModels := allowedModels
	switch routingMode {
	case "single":
		if cm, ok := currentModel.(string); ok && cm != "" {
			effectiveModels = []string{cm}
		}
	case "round_robin", "priority":
		if len(selectedModels) > 0 {
			effectiveModels = selectedModels
		}
	}

	allModels := s.modelRegistry.ListModels()

	data := make([]modelListItem, 0, len(allModels))
	now := time.Now().Unix()

	for _, m := range allModels {
		// Use api_model_name as the public id when available; fall back to product_id.
		publicID := m.APIModelName
		if publicID == "" {
			publicID = m.ProductID
		}

		// Filter by the effective model list when one is configured.
		if len(effectiveModels) > 0 {
			if !containsString(effectiveModels, publicID) {
				continue
			}
		}

		caps := modelCapabilitiesToMap(m.Capabilities)

		item := modelListItem{
			ID:      publicID,
			Object:  "model",
			Created: now,
			// owned_by is always "woodgit": the upstream provider
			// (openrouter/laozhang/tokenrouter/amazon-*) is a sourcing detail
			// and must never leak to API clients.
			OwnedBy: "woodgit",
		}
		if len(caps) > 0 {
			item.Capabilities = caps
		}
		data = append(data, item)
	}

	// Deduplicate by model ID — same model may exist across multiple providers.
	// Keep the first occurrence (arbitrary provider) since users only see the model name.
	seen := make(map[string]bool, len(data))
	deduped := make([]modelListItem, 0, len(data))
	for _, item := range data {
		if seen[item.ID] {
			continue
		}
		seen[item.ID] = true
		deduped = append(deduped, item)
	}
	data = deduped

	// Deterministic ordering for easier client consumption and tests.
	sort.Slice(data, func(i, j int) bool {
		return data[i].ID < data[j].ID
	})

	resp := gin.H{
		"object": "list",
		"data":   data,
	}

	limits := buildKeyLimits(c)
	if limits.Concurrent > 0 || limits.RPM > 0 || limits.TPM > 0 {
		resp["key_limits"] = limits
	}

	c.JSON(http.StatusOK, resp)
}

// modelCapabilitiesToMap flattens ModelCapabilities into a JSON-serializable map.
// Zero values are omitted to keep the response concise.
func modelCapabilitiesToMap(caps models.ModelCapabilities) map[string]interface{} {
	m := make(map[string]interface{})
	if caps.ContextWindow > 0 {
		m["context_window"] = caps.ContextWindow
	}
	if caps.MaxOutputTokens > 0 {
		m["max_output_tokens"] = caps.MaxOutputTokens
	}
	if caps.MaxInputTokens > 0 {
		m["max_input_tokens"] = caps.MaxInputTokens
	}
	if caps.SupportsThinking {
		m["supports_thinking"] = true
	}
	if len(caps.ReasoningEfforts) > 0 {
		m["reasoning_efforts"] = caps.ReasoningEfforts
	}
	if len(caps.SpeedTiers) > 0 {
		m["speed_tiers"] = caps.SpeedTiers
	}
	if caps.SupportsVision {
		m["supports_vision"] = true
	}
	if caps.SupportsAudio {
		m["supports_audio"] = true
	}
	if caps.SupportsToolCalls {
		m["supports_tool_calls"] = true
	}
	if caps.SupportsJSONMode {
		m["supports_json_mode"] = true
	}
	if caps.SupportsParallelToolCalls {
		m["supports_parallel_tool_calls"] = true
	}
	if caps.SupportsStreaming {
		m["supports_streaming"] = true
	}
	return m
}

// buildKeyLimits derives the most restrictive limits from key/subscription/group
// context, matching the semantics used by the rate-limit middleware (minPositive).
// Previously used maxInt (most permissive), which told clients they could burst
// at the highest limit across all dimensions — but then 429 was inevitable.
func buildKeyLimits(c *gin.Context) keyLimits {
	var l keyLimits

	if v, ok := c.Get("sub_concurrent_limit"); ok {
		if n, ok := v.(int); ok {
			l.Concurrent = n
		}
	}

	keyRPM, _ := c.Get("rate_limit_rpm")
	subRPM, _ := c.Get("sub_rpm_limit")
	groupRPM, _ := c.Get("group_rpm")
	l.RPM = minPositiveInt(keyRPM, subRPM, groupRPM)

	keyTPM, _ := c.Get("rate_limit_tpm")
	subTPM, _ := c.Get("sub_tpm_limit")
	groupTPM, _ := c.Get("group_tpm")
	l.TPM = minPositiveInt(keyTPM, subTPM, groupTPM)

	return l
}

// minPositiveInt returns the smallest positive (non-zero) value among the
// provided interface{} values (which must be int or float64). If all are
// zero/nil/non-numeric, it returns 0. This matches the minPositive semantics
// used by the rate-limit middleware in ratelimit.go.
func minPositiveInt(vals ...interface{}) int {
	result := 0
	for _, v := range vals {
		var n int
		switch tv := v.(type) {
		case int:
			n = tv
		case float64:
			n = int(tv)
		case int64:
			n = int(tv)
		default:
			continue
		}
		if n <= 0 {
			continue
		}
		if result == 0 || n < result {
			result = n
		}
	}
	return result
}

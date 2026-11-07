package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mayiants/ai-proxy-go/pkg/token"
)

// Bedrock embeddings support (InvokeModel, not Converse).
//
// OpenAI /v1/embeddings is forwarded to Bedrock's InvokeModel endpoint with
// SigV4 signing. The request/response formats differ per model family:
//
//   - cohere.embed-*:   {"texts": [...], "input_type": ...} → {"embeddings": [[...]] | {"float": [[...]]}, "meta": {"billed_units": {"input_tokens": N}}}
//   - amazon.titan-embed-*: {"inputText": "..."} (single string only) → {"embedding": [...], "inputTextTokenCount": N}
//
// Cross-region inference profile aliases (global.*, us.*, ...) are rejected
// by InvokeModel with a 400, so the bare model ID is always used here.

// openAIEmbeddingsRequest is the client-facing OpenAI embeddings request shape.
type openAIEmbeddingsRequest struct {
	Model string      `json:"model"`
	Input interface{} `json:"input"`
}

// extractEmbeddingsInput parses the OpenAI request body and normalizes the
// input to a list of texts. OpenAI also allows token IDs ([]int / [][]int);
// those are rejected because Bedrock embeddings accept text only.
func extractEmbeddingsInput(openAIReq []byte) ([]string, error) {
	var req openAIEmbeddingsRequest
	if err := json.Unmarshal(openAIReq, &req); err != nil {
		return nil, fmt.Errorf("invalid embeddings request: %w", err)
	}
	switch v := req.Input.(type) {
	case string:
		if v == "" {
			return nil, fmt.Errorf("input must not be empty")
		}
		return []string{v}, nil
	case []interface{}:
		texts := make([]string, 0, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("input[%d]: token ID arrays are not supported, only text", i)
			}
			if s == "" {
				return nil, fmt.Errorf("input[%d] must not be empty", i)
			}
			texts = append(texts, s)
		}
		if len(texts) == 0 {
			return nil, fmt.Errorf("input must not be empty")
		}
		return texts, nil
	default:
		return nil, fmt.Errorf("input must be a string or an array of strings")
	}
}

// embeddingsFamily classifies a Bedrock model ID into a payload family.
// Region prefixes are stripped first: InvokeModel requires bare IDs and the
// family is determined by the vendor prefix.
func embeddingsFamily(modelID string) string {
	m := strings.ToLower(stripInferenceProfilePrefix(modelID))
	switch {
	case strings.HasPrefix(m, "cohere.embed"):
		return "cohere"
	case strings.HasPrefix(m, "amazon.titan-embed"):
		return "titan"
	}
	return ""
}

// buildEmbeddingsPayload converts normalized input texts to the provider-native
// InvokeModel body for the given model family.
func buildEmbeddingsPayload(modelID string, texts []string) ([]byte, error) {
	switch embeddingsFamily(modelID) {
	case "cohere":
		// input_type is required by Cohere embed v3; "search_document" is the
		// neutral default for generic embedding workloads (matches OpenAI's
		// document-oriented usage). Omitting embedding_types keeps the
		// response "embeddings" field a plain array — passing it would switch
		// the response to an object keyed by type ("embeddings.float").
		return json.Marshal(map[string]interface{}{
			"texts":      texts,
			"input_type": "search_document",
		})
	case "titan":
		// Titan Embed Text accepts a single inputText string per request.
		if len(texts) > 1 {
			return nil, fmt.Errorf("model %s supports a single input text per request", modelID)
		}
		return json.Marshal(map[string]interface{}{"inputText": texts[0]})
	default:
		return nil, fmt.Errorf("unsupported Bedrock embeddings model: %s (supported: cohere.embed-*, amazon.titan-embed-*)", modelID)
	}
}

// stripInferenceProfilePrefix removes cross-region inference profile prefixes
// (global./us./eu./ap.) that InvokeModel rejects for embeddings models.
func stripInferenceProfilePrefix(modelID string) string {
	for _, p := range []string{"global.", "us.", "eu.", "ap."} {
		if strings.HasPrefix(modelID, p) {
			return strings.TrimPrefix(modelID, p)
		}
	}
	return modelID
}

// parseEmbeddingsResponse extracts the embedding vectors and input token
// count from a Bedrock InvokeModel response. Embedding vectors are kept as
// json.RawMessage to re-emit them verbatim (no float precision drift).
func parseEmbeddingsResponse(modelID string, body []byte) ([]json.RawMessage, int, error) {
	family := embeddingsFamily(modelID)

	var vectors []json.RawMessage
	promptTokens := 0

	switch family {
	case "cohere":
		// Embed v3 returns "embeddings" as a plain array. Embed v4 returns an
		// object keyed by embedding type even without an explicit
		// embedding_types request field ("embeddings": {"float": [[...]]}).
		// Accept both shapes.
		var resp struct {
			Embeddings json.RawMessage `json:"embeddings"`
			Meta       struct {
				BilledUnits struct {
					InputTokens int `json:"input_tokens"`
				} `json:"billed_units"`
			} `json:"meta"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, 0, fmt.Errorf("parse cohere embeddings response: %w", err)
		}
		var list []json.RawMessage
		if err := json.Unmarshal(resp.Embeddings, &list); err != nil {
			var byType map[string]json.RawMessage
			if err2 := json.Unmarshal(resp.Embeddings, &byType); err2 != nil {
				return nil, 0, fmt.Errorf("parse cohere embeddings: %w", err)
			}
			if f, ok := byType["float"]; ok {
				if err := json.Unmarshal(f, &list); err != nil {
					return nil, 0, fmt.Errorf("parse cohere float embeddings: %w", err)
				}
			}
		}
		vectors = list
		promptTokens = resp.Meta.BilledUnits.InputTokens
	case "titan":
		var resp struct {
			Embedding           json.RawMessage `json:"embedding"`
			InputTextTokenCount int             `json:"inputTextTokenCount"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, 0, fmt.Errorf("parse titan embeddings response: %w", err)
		}
		vectors = []json.RawMessage{resp.Embedding}
		promptTokens = resp.InputTextTokenCount
	default:
		return nil, 0, fmt.Errorf("unsupported Bedrock embeddings model: %s", modelID)
	}

	if len(vectors) == 0 || len(vectors[0]) == 0 || string(vectors[0]) == "null" {
		return nil, 0, fmt.Errorf("upstream returned no embeddings")
	}
	return vectors, promptTokens, nil
}

// marshalEmbeddingsResponse builds the OpenAI embeddings response body.
func marshalEmbeddingsResponse(vectors []json.RawMessage, promptTokens int, logicalModel string) ([]byte, error) {
	data := make([]map[string]interface{}, 0, len(vectors))
	for i, v := range vectors {
		data = append(data, map[string]interface{}{
			"object":    "embedding",
			"index":     i,
			"embedding": v,
		})
	}
	out, err := json.Marshal(map[string]interface{}{
		"object": "list",
		"data":   data,
		"model":  logicalModel,
		"usage": map[string]interface{}{
			"prompt_tokens": promptTokens,
			"total_tokens":  promptTokens,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal openai embeddings response: %w", err)
	}
	return out, nil
}

// InvokeEmbeddings dispatches a SigV4-signed Bedrock InvokeModel call for an
// embeddings model. openAIReq is the raw client request body (OpenAI format);
// the return value is the OpenAI-format response body together with the
// upstream HTTP status code. Non-2xx upstream responses are returned raw so
// the caller can classify/report them like any other upstream error.
//
// A statusCode of 0 with a non-nil error indicates the request itself was
// invalid (unsupported model, bad input) rather than an upstream failure.
func (a *AWSAdaptor) InvokeEmbeddings(ctx context.Context, modelID, logicalModel string, openAIReq []byte) ([]byte, int, error) {
	texts, err := extractEmbeddingsInput(openAIReq)
	if err != nil {
		return nil, 0, err
	}
	payload, err := buildEmbeddingsPayload(modelID, texts)
	if err != nil {
		return nil, 0, err
	}

	invokeID := stripInferenceProfilePrefix(modelID)
	url := strings.TrimSuffix(a.BaseURL, "/") + "/model/" + invokeID + "/invoke"

	resp, err := a.sendSigned(ctx, url, payload)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read embeddings response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return body, resp.StatusCode, nil
	}

	vectors, promptTokens, err := parseEmbeddingsResponse(modelID, body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if promptTokens == 0 {
		// Cohere embed v3 on Bedrock returns no billed_units metadata; fall
		// back to a client-side estimate so billing still has a token count.
		for _, t := range texts {
			promptTokens += token.CountTextToken(t, modelID)
		}
	}
	converted, err := marshalEmbeddingsResponse(vectors, promptTokens, logicalModel)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return converted, resp.StatusCode, nil
}

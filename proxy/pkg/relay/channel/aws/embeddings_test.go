package aws

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExtractEmbeddingsInput(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    []string
		wantErr string
	}{
		{name: "single string", body: `{"model":"m","input":"hello"}`, want: []string{"hello"}},
		{name: "string array", body: `{"model":"m","input":["a","b"]}`, want: []string{"a", "b"}},
		{name: "empty string", body: `{"model":"m","input":""}`, wantErr: "empty"},
		{name: "empty array", body: `{"model":"m","input":[]}`, wantErr: "empty"},
		{name: "token ids rejected", body: `{"model":"m","input":[[1,2,3]]}`, wantErr: "only text"},
		{name: "numeric array rejected", body: `{"model":"m","input":[1,2,3]}`, wantErr: "only text"},
		{name: "missing input", body: `{"model":"m"}`, wantErr: "must be a string"},
		{name: "bad json", body: `{`, wantErr: "invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractEmbeddingsInput([]byte(tt.body))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestBuildEmbeddingsPayload(t *testing.T) {
	t.Run("cohere", func(t *testing.T) {
		payload, err := buildEmbeddingsPayload("cohere.embed-multilingual-v3", []string{"a", "b"})
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]interface{}
		if err := json.Unmarshal(payload, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded["input_type"] != "search_document" {
			t.Fatalf("input_type = %v", decoded["input_type"])
		}
		if texts, ok := decoded["texts"].([]interface{}); !ok || len(texts) != 2 {
			t.Fatalf("texts = %v", decoded["texts"])
		}
	})

	t.Run("profile prefix in model id", func(t *testing.T) {
		if got := stripInferenceProfilePrefix("us.cohere.embed-v4:0"); got != "cohere.embed-v4:0" {
			t.Fatalf("got %q", got)
		}
		if got := stripInferenceProfilePrefix("global.cohere.embed-v4:0"); got != "cohere.embed-v4:0" {
			t.Fatalf("got %q", got)
		}
		if got := stripInferenceProfilePrefix("cohere.embed-v4:0"); got != "cohere.embed-v4:0" {
			t.Fatalf("got %q", got)
		}
		// Family classification must work through region prefixes.
		if got := embeddingsFamily("us.cohere.embed-v4:0"); got != "cohere" {
			t.Fatalf("family = %q", got)
		}
		if got := embeddingsFamily("global.amazon.titan-embed-text-v2:0"); got != "titan" {
			t.Fatalf("family = %q", got)
		}
	})

	t.Run("titan single", func(t *testing.T) {
		payload, err := buildEmbeddingsPayload("amazon.titan-embed-text-v2:0", []string{"hello"})
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]interface{}
		if err := json.Unmarshal(payload, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded["inputText"] != "hello" {
			t.Fatalf("inputText = %v", decoded["inputText"])
		}
	})

	t.Run("titan rejects batch", func(t *testing.T) {
		_, err := buildEmbeddingsPayload("amazon.titan-embed-text-v2:0", []string{"a", "b"})
		if err == nil || !strings.Contains(err.Error(), "single") {
			t.Fatalf("expected single-input error, got %v", err)
		}
	})

	t.Run("unsupported", func(t *testing.T) {
		_, err := buildEmbeddingsPayload("anthropic.claude-sonnet-4-6", []string{"a"})
		if err == nil || !strings.Contains(err.Error(), "unsupported") {
			t.Fatalf("expected unsupported error, got %v", err)
		}
	})
}

func TestConvertEmbeddingsResponse(t *testing.T) {
	t.Run("cohere", func(t *testing.T) {
		upstream := `{"embeddings":[[0.1,0.2],[0.3,0.4]],"meta":{"billed_units":{"input_tokens":7}}}`
		vectors, tokens, err := parseEmbeddingsResponse("cohere.embed-multilingual-v3", []byte(upstream))
		if err != nil {
			t.Fatal(err)
		}
		out, err := marshalEmbeddingsResponse(vectors, tokens, "cohere.embed-multilingual-v3")
		if err != nil {
			t.Fatal(err)
		}
		var resp struct {
			Object string `json:"object"`
			Data   []struct {
				Object    string          `json:"object"`
				Index     int             `json:"index"`
				Embedding json.RawMessage `json:"embedding"`
			} `json:"data"`
			Model string `json:"model"`
			Usage struct {
				PromptTokens int `json:"prompt_tokens"`
				TotalTokens  int `json:"total_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(out, &resp); err != nil {
			t.Fatal(err)
		}
		if resp.Object != "list" || len(resp.Data) != 2 || resp.Data[1].Index != 1 {
			t.Fatalf("bad data: %s", out)
		}
		if string(resp.Data[0].Embedding) != `[0.1,0.2]` {
			t.Fatalf("embedding verbatim mismatch: %s", resp.Data[0].Embedding)
		}
		if resp.Usage.PromptTokens != 7 || resp.Usage.TotalTokens != 7 {
			t.Fatalf("usage = %+v", resp.Usage)
		}
		if resp.Model != "cohere.embed-multilingual-v3" {
			t.Fatalf("model = %q", resp.Model)
		}
	})

	t.Run("cohere v4 object form", func(t *testing.T) {
		upstream := `{"embeddings":{"float":[[0.1,0.2],[0.3,0.4]]},"meta":{"billed_units":{"input_tokens":9}}}`
		vectors, tokens, err := parseEmbeddingsResponse("cohere.embed-v4:0", []byte(upstream))
		if err != nil {
			t.Fatal(err)
		}
		out, err := marshalEmbeddingsResponse(vectors, tokens, "m")
		if err != nil {
			t.Fatal(err)
		}
		var resp struct {
			Data []struct {
				Index int `json:"index"`
			} `json:"data"`
			Usage struct {
				PromptTokens int `json:"prompt_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(out, &resp); err != nil {
			t.Fatal(err)
		}
		if len(resp.Data) != 2 || resp.Data[1].Index != 1 || resp.Usage.PromptTokens != 9 {
			t.Fatalf("bad parse: %s", out)
		}
	})

	t.Run("titan", func(t *testing.T) {
		upstream := `{"embedding":[0.5,0.6],"inputTextTokenCount":3}`
		vectors, tokens, err := parseEmbeddingsResponse("amazon.titan-embed-text-v2:0", []byte(upstream))
		if err != nil {
			t.Fatal(err)
		}
		out, err := marshalEmbeddingsResponse(vectors, tokens, "my-model")
		if err != nil {
			t.Fatal(err)
		}
		var resp struct {
			Data []struct {
				Embedding json.RawMessage `json:"embedding"`
			} `json:"data"`
			Usage struct {
				PromptTokens int `json:"prompt_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(out, &resp); err != nil {
			t.Fatal(err)
		}
		if len(resp.Data) != 1 || string(resp.Data[0].Embedding) != `[0.5,0.6]` {
			t.Fatalf("bad data: %s", out)
		}
		if resp.Usage.PromptTokens != 3 {
			t.Fatalf("usage = %+v", resp.Usage)
		}
	})

	t.Run("empty embeddings rejected", func(t *testing.T) {
		_, _, err := parseEmbeddingsResponse("cohere.embed-multilingual-v3", []byte(`{"embeddings":[]}`))
		if err == nil {
			t.Fatal("expected error for empty embeddings")
		}
	})
}

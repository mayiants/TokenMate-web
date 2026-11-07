package aws

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mayiants/ai-proxy-go/dto"
	"github.com/mayiants/ai-proxy-go/pkg/relay/base"
)

func TestNewAWSAdaptor(t *testing.T) {
	cfg := &base.AdaptorConfig{
		BaseURL: "https://bedrock-runtime.us-west-2.amazonaws.com",
		APIKey:  "AKIAIOSFODNN7EXAMPLE:wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}
	adaptor := NewAWSAdaptor(cfg)

	if adaptor.GetProviderName() != "amazon" {
		t.Errorf("Expected provider 'amazon', got '%s'", adaptor.GetProviderName())
	}

	if adaptor.region != "us-west-2" {
		t.Errorf("Expected region 'us-west-2', got '%s'", adaptor.region)
	}

	if adaptor.accessKey != "AKIAIOSFODNN7EXAMPLE" {
		t.Errorf("Expected access key parsed, got '%s'", adaptor.accessKey)
	}
}

func TestAWSDefaultRegion(t *testing.T) {
	cfg := &base.AdaptorConfig{
		BaseURL: "",
		APIKey:  "key:secret",
	}
	adaptor := NewAWSAdaptor(cfg)
	if adaptor.region != "us-east-1" {
		t.Errorf("Expected default region 'us-east-1', got '%s'", adaptor.region)
	}
}

func TestMapModelToBedrock(t *testing.T) {
	tests := []struct {
		model    string
		expected string
	}{
		{"claude-3-opus", "us.anthropic.claude-3-opus-20240229-v1:0"},
		{"claude-3-sonnet", "us.anthropic.claude-3-sonnet-20240229-v1:0"},
		{"claude-3-haiku", "us.anthropic.claude-3-haiku-20240307-v1:0"},
		{"claude-3-5-sonnet", "us.anthropic.claude-3-5-sonnet-20240620-v1:0"},
		{"llama-3-70b", "meta.llama3-70b-instruct-v1:0"},
		{"llama-3-8b", "meta.llama3-8b-instruct-v1:0"},
		{"unknown-model", "global.anthropic.claude-sonnet-4-6"},
	}

	for _, tt := range tests {
		got := MapModelToBedrock(tt.model)
		if got != tt.expected {
			t.Errorf("MapModelToBedrock(%q) = %q, want %q", tt.model, got, tt.expected)
		}
	}
}

func TestAWSGetRequestURL(t *testing.T) {
	cfg := &base.AdaptorConfig{
		BaseURL: "https://bedrock-runtime.eu-west-1.amazonaws.com",
		APIKey:  "key:secret",
	}
	adaptor := NewAWSAdaptor(cfg)
	url := adaptor.GetRequestURL("claude-3-sonnet", false)
	if !strings.Contains(url, "/model/us.anthropic.claude-3-sonnet-20240229-v1:0/converse") {
		t.Errorf("URL should contain Bedrock model path, got: %s", url)
	}
	if !strings.HasPrefix(url, "https://bedrock-runtime.eu-west-1.amazonaws.com") {
		t.Errorf("URL should start with base URL, got: %s", url)
	}
}

func TestAWSToProvider(t *testing.T) {
	cfg := &base.AdaptorConfig{APIKey: "key:secret"}
	adaptor := NewAWSAdaptor(cfg)

	maxTokens := 100
	temp := 0.7
	req := dto.GeneralOpenAIRequest{
		Model:       "claude-3-sonnet",
		MaxTokens:   &maxTokens,
		Temperature: &temp,
		Messages: []dto.Message{
			{Role: "system", Content: "You are helpful"},
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there"},
		},
	}

	br := adaptor.ToProvider(req)
	brMap, ok := br.(bedrockRequest)
	if !ok {
		t.Fatalf("Expected bedrockRequest, got %T", br)
	}

	if brMap.InferenceConfig.MaxTokens != 100 {
		t.Errorf("Expected maxTokens 100, got %d", brMap.InferenceConfig.MaxTokens)
	}
	if len(brMap.System) != 1 {
		t.Errorf("Expected 1 system message, got %d", len(brMap.System))
	}
	if len(brMap.Messages) != 2 {
		t.Errorf("Expected 2 conversation messages, got %d", len(brMap.Messages))
	}
	if brMap.Messages[0].Role != "user" {
		t.Errorf("Expected first message role 'user', got '%s'", brMap.Messages[0].Role)
	}
}

func TestClampMaxTokensBody(t *testing.T) {
	limitResp := func(body string) *http.Response {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(strings.NewReader(body)),
		}
	}

	t.Run("clamps converse maxTokens to advertised limit", func(t *testing.T) {
		reqBody := []byte(`{"inferenceConfig":{"maxTokens":200000},"messages":[]}`)
		resp := limitResp(`{"message":"The maximum tokens you requested exceeds the model limit of 128000. Try again with a maximum tokens value that is lower than 128000."}`)

		out, limit, ok := clampMaxTokensBody(reqBody, resp)
		if !ok {
			t.Fatal("expected clamp to apply")
		}
		if limit != 128000 {
			t.Errorf("expected limit 128000, got %d", limit)
		}
		var payload map[string]interface{}
		if err := json.Unmarshal(out, &payload); err != nil {
			t.Fatalf("clamped body not valid JSON: %v", err)
		}
		ic := payload["inferenceConfig"].(map[string]interface{})
		if got := int(ic["maxTokens"].(float64)); got != 128000 {
			t.Errorf("expected maxTokens clamped to 128000, got %d", got)
		}
	})

	t.Run("clamps top-level max_tokens (anthropic-style body)", func(t *testing.T) {
		reqBody := []byte(`{"max_tokens":256000,"messages":[]}`)
		resp := limitResp(`{"message":"The maximum tokens you requested exceeds the model limit of 64000."}`)

		out, limit, ok := clampMaxTokensBody(reqBody, resp)
		if !ok || limit != 64000 {
			t.Fatalf("expected clamp to 64000, ok=%v limit=%d", ok, limit)
		}
		var payload map[string]interface{}
		if err := json.Unmarshal(out, &payload); err != nil {
			t.Fatalf("clamped body not valid JSON: %v", err)
		}
		if got := int(payload["max_tokens"].(float64)); got != 64000 {
			t.Errorf("expected max_tokens clamped to 64000, got %d", got)
		}
	})

	t.Run("no clamp when under limit", func(t *testing.T) {
		reqBody := []byte(`{"inferenceConfig":{"maxTokens":4096}}`)
		resp := limitResp(`{"message":"... exceeds the model limit of 128000."}`)
		if _, _, ok := clampMaxTokensBody(reqBody, resp); ok {
			t.Error("expected no clamp when maxTokens under limit")
		}
	})

	t.Run("no clamp on unrelated 400 and response body restored", func(t *testing.T) {
		reqBody := []byte(`{"inferenceConfig":{"maxTokens":200000}}`)
		errMsg := `{"message":"The text field in the ContentBlock object is blank."}`
		resp := limitResp(errMsg)
		if _, _, ok := clampMaxTokensBody(reqBody, resp); ok {
			t.Error("expected no clamp for unrelated 400")
		}
		restored, _ := io.ReadAll(resp.Body)
		if !bytes.Equal(restored, []byte(errMsg)) {
			t.Errorf("response body not restored: %q", restored)
		}
	})
}

func TestAWSToProvider_SkipsEmptyMessages(t *testing.T) {
	cfg := &base.AdaptorConfig{APIKey: "key:secret"}
	adaptor := NewAWSAdaptor(cfg)

	req := dto.GeneralOpenAIRequest{
		Model: "claude-3-sonnet",
		Messages: []dto.Message{
			{Role: "system", Content: ""},
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: ""},
			{Role: "assistant", Content: nil},
			{Role: "user", Content: "again"},
		},
	}

	br, ok := adaptor.ToProvider(req).(bedrockRequest)
	if !ok {
		t.Fatalf("Expected bedrockRequest, got %T", adaptor.ToProvider(req))
	}
	if len(br.System) != 0 {
		t.Errorf("Expected empty system message to be dropped, got %d", len(br.System))
	}
	if len(br.Messages) != 2 {
		t.Fatalf("Expected 2 messages (empty assistant messages dropped), got %d", len(br.Messages))
	}
	for _, m := range br.Messages {
		for _, c := range m.Content {
			if c.Text == "" && c.ToolUse == nil && c.ToolResult == nil {
				t.Errorf("Found blank text block in role %q message", m.Role)
			}
		}
	}
}

func TestAWSFromProvider(t *testing.T) {
	adaptor := NewAWSAdaptor(&base.AdaptorConfig{APIKey: "key:secret"})

	bedrockJSON := []byte(`{
		"output": {"message": {"role": "assistant", "content": [{"text": "Hello from Bedrock"}]}},
		"usage": {"inputTokens": 10, "outputTokens": 20, "totalTokens": 30},
		"stopReason": "end_turn"
	}`)

	resp := adaptor.FromProvider(bedrockJSON, dto.GeneralOpenAIRequest{})
	if resp == nil {
		t.Fatal("Expected non-nil response")
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("Expected 1 choice, got %d", len(resp.Choices))
	}
	if resp.Choices[0].Message.Content != "Hello from Bedrock" {
		t.Errorf("Unexpected content: %s", resp.Choices[0].Message.Content)
	}
	if resp.Usage.TotalTokens != 30 {
		t.Errorf("Expected total tokens 30, got %d", resp.Usage.TotalTokens)
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("Expected finish reason 'stop', got '%s'", resp.Choices[0].FinishReason)
	}
}

func TestAWSGetStreamEndpoint(t *testing.T) {
	cfg := &base.AdaptorConfig{
		BaseURL: "https://bedrock-runtime.us-east-1.amazonaws.com",
		APIKey:  "key:secret",
	}
	adaptor := NewAWSAdaptor(cfg)

	endpoint := adaptor.GetEndpoint("claude-3-sonnet", true)
	if !strings.Contains(endpoint, "converse-stream") {
		t.Errorf("Expected streaming endpoint to contain 'converse-stream', got: %s", endpoint)
	}

	endpointNonStream := adaptor.GetEndpoint("claude-3-sonnet", false)
	if strings.Contains(endpointNonStream, "converse-stream") {
		t.Errorf("Expected non-streaming endpoint to NOT contain 'converse-stream', got: %s", endpointNonStream)
	}
}

func TestAWSTransformStreamChunk(t *testing.T) {
	adaptor := NewAWSAdaptor(&base.AdaptorConfig{APIKey: "key:secret"})

	// Test contentBlockDelta
	deltaJSON := []byte(`{"contentBlockDelta":{"delta":{"text":"Hello world"},"contentBlockIndex":0}}`)
	result := adaptor.TransformStreamChunk(deltaJSON, 0)
	if result == nil {
		t.Fatal("Expected non-nil result for contentBlockDelta")
	}
	resp, ok := result.(*dto.ChatCompletionsStreamResponse)
	if !ok {
		t.Fatalf("Expected *ChatCompletionsStreamResponse, got %T", result)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("Expected 1 choice, got %d", len(resp.Choices))
	}
	if resp.Choices[0].Delta.Content == nil {
		t.Fatal("Expected content to not be nil")
	}
	if *resp.Choices[0].Delta.Content != "Hello world" {
		t.Errorf("Expected content 'Hello world', got '%s'", *resp.Choices[0].Delta.Content)
	}

	// Test messageStop
	stopJSON := []byte(`{"messageStop":{"stopReason":"max_tokens"}}`)
	result = adaptor.TransformStreamChunk(stopJSON, 0)
	if result == nil {
		t.Fatal("Expected non-nil result for messageStop")
	}
	resp, ok = result.(*dto.ChatCompletionsStreamResponse)
	if !ok {
		t.Fatalf("Expected *ChatCompletionsStreamResponse, got %T", result)
	}
	if resp.Choices[0].FinishReason == nil {
		t.Fatal("Expected finish reason to not be nil")
	}
	if *resp.Choices[0].FinishReason != "length" {
		t.Errorf("Expected finish reason 'length', got '%s'", *resp.Choices[0].FinishReason)
	}

	// Test empty/non-JSON chunk
	result = adaptor.TransformStreamChunk([]byte("not json"), 0)
	if result != nil {
		t.Error("Expected nil for non-JSON chunk")
	}
}

func TestMapBedrockStopReason(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"max_tokens", "length"},
		{"stop_sequence", "stop"},
		{"end_turn", "stop"},
		{"content_filtered", "content_filter"},
		{"tool_use", "tool_calls"},
		{"unknown", "stop"},
	}
	for _, tt := range tests {
		got := mapBedrockStopReason(tt.input)
		if got != tt.expected {
			t.Errorf("mapBedrockStopReason(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestExtractJSONPayload(t *testing.T) {
	// === Test 1: EventStream binary frame ===
	// Build a valid Amazon EventStream message:
	// [4B total_len][4B headers_len][4B prelude_crc][headers][payload][4B msg_crc]
	payload := []byte(`{"test":"value"}`)
	totalLen := uint32(16 + len(payload)) // 16 = 4+4+4+4 (prelude + prelude_crc + msg_crc)
	headersLen := uint32(0)

	frame := make([]byte, 0, totalLen)
	frame = binary.BigEndian.AppendUint32(frame, totalLen)
	frame = binary.BigEndian.AppendUint32(frame, headersLen)
	frame = binary.BigEndian.AppendUint32(frame, 0) // prelude CRC (not validated)
	frame = append(frame, payload...)
	frame = binary.BigEndian.AppendUint32(frame, 0) // message CRC (not validated)

	got := extractJSONPayload(frame)
	if string(got) != `{"test":"value"}` {
		t.Errorf("EventStream frame: expected JSON payload, got: %s", string(got))
	}

	// === Test 2: Legacy plain-text-with-braces fallback ===
	data := []byte(`some prefix{"key":"value"}some suffix`)
	got = extractJSONPayload(data)
	if string(got) != `{"key":"value"}` {
		t.Errorf("Legacy fallback: expected JSON payload, got: %s", string(got))
	}

	// === Test 3: Nested JSON (legacy fallback) ===
	data = []byte(`prefix{"outer":{"inner":true}}suffix`)
	got = extractJSONPayload(data)
	if string(got) != `{"outer":{"inner":true}}` {
		t.Errorf("Legacy fallback nested: expected JSON payload, got: %s", string(got))
	}

	// === Test 4: No JSON ===
	data = []byte("no json here")
	got = extractJSONPayload(data)
	if got != nil {
		t.Error("Expected nil for data without JSON")
	}
}

func TestSigV4Signing(t *testing.T) {
	// Test that signing produces expected header format
	req := &base.AdaptorConfig{APIKey: "AKIAIOSFODNN7EXAMPLE:wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}
	adaptor := NewAWSAdaptor(req)

	if adaptor.accessKey == "" || adaptor.secretKey == "" {
		t.Error("Expected credentials to be parsed")
	}
}

func TestAWSToProvider_CachePointInjection(t *testing.T) {
	cfg := &base.AdaptorConfig{APIKey: "key:secret"}
	adaptor := NewAWSAdaptor(cfg)

	bigSystem := strings.Repeat("s", 5000)
	bigUser := strings.Repeat("u", 5000)

	// Large system + large conversation → both cachePoints injected.
	req := dto.GeneralOpenAIRequest{
		Model: "claude-opus-4-6",
		Messages: []dto.Message{
			{Role: "system", Content: bigSystem},
			{Role: "user", Content: bigUser},
		},
	}
	br := adaptor.ToProvider(req).(bedrockRequest)
	if len(br.System) != 2 || br.System[1].CachePoint == nil {
		t.Errorf("expected system cachePoint, got %d system blocks", len(br.System))
	}
	last := br.Messages[len(br.Messages)-1]
	if last.Content[len(last.Content)-1].CachePoint == nil {
		t.Error("expected trailing message cachePoint")
	}

	// Small request → no cachePoints (below Bedrock's minimum cacheable prefix).
	small := dto.GeneralOpenAIRequest{
		Model:    "claude-opus-4-6",
		Messages: []dto.Message{{Role: "user", Content: "hi"}},
	}
	br2 := adaptor.ToProvider(small).(bedrockRequest)
	if len(br2.System) != 0 {
		t.Error("unexpected system blocks")
	}
	for _, m := range br2.Messages {
		for _, c := range m.Content {
			if c.CachePoint != nil {
				t.Error("cachePoint injected for tiny request")
			}
		}
	}
}

func TestPromptTokensForBilling(t *testing.T) {
	u := bedrockUsage{InputTokens: 100, CacheReadInputTokens: 900, CacheWriteInputTokens: 50}
	if got := promptTokensForBilling(u); got != 1000 {
		t.Errorf("expected 1000 (input + cache-read), got %d", got)
	}
}

// TestNewAWSAdaptor_BearerVsAKSK pins credential-form detection: an ABSK
// Bedrock API Key becomes a bearer token, while "ak:sk[:token]" stays AK/SK.
func TestNewAWSAdaptor_BearerVsAKSK(t *testing.T) {
	tests := []struct {
		name        string
		apiKey      string
		wantBearer  string
		wantAccess  string
		wantSecret  string
		wantSession string
	}{
		{"absk bearer", "ABSKTWFudGxlAbCdEf0=", "ABSKTWFudGxlAbCdEf0=", "", "", ""},
		{"bedrock-api-key prefix", "bedrock-api-key-xyz", "bedrock-api-key-xyz", "", "", ""},
		{"aksk pair", "AKIAEXAMPLE:secret123", "", "AKIAEXAMPLE", "secret123", ""},
		{"aksk with session", "AKIAEXAMPLE:secret123:tok456", "", "AKIAEXAMPLE", "secret123", "tok456"},
		{"single opaque token", "someopaquetoken", "someopaquetoken", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := NewAWSAdaptor(&base.AdaptorConfig{APIKey: tt.apiKey})
			if a.bearerToken != tt.wantBearer {
				t.Errorf("bearerToken = %q, want %q", a.bearerToken, tt.wantBearer)
			}
			if a.accessKey != tt.wantAccess {
				t.Errorf("accessKey = %q, want %q", a.accessKey, tt.wantAccess)
			}
			if a.secretKey != tt.wantSecret {
				t.Errorf("secretKey = %q, want %q", a.secretKey, tt.wantSecret)
			}
			if a.sessionToken != tt.wantSession {
				t.Errorf("sessionToken = %q, want %q", a.sessionToken, tt.wantSession)
			}
		})
	}
}

// TestSendSigned_BearerUsesAuthorizationHeader verifies that a Bedrock API Key
// request carries "Authorization: Bearer <token>" and is NOT SigV4-signed
// (no AWS4-HMAC-SHA256 / x-amz-date), while an AK/SK request is SigV4-signed
// with no bearer Authorization. Both hit the same runtime endpoint.
func TestSendSigned_BearerUsesAuthorizationHeader(t *testing.T) {
	var gotAuth, gotAmzDate string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAmzDate = r.Header.Get("x-amz-date")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"output":{"message":{"role":"assistant","content":[{"text":"ok"}]}},"usage":{"inputTokens":1,"outputTokens":1,"totalTokens":2},"stopReason":"end_turn"}`))
	}))
	defer srv.Close()

	t.Run("bearer", func(t *testing.T) {
		a := NewAWSAdaptor(&base.AdaptorConfig{BaseURL: srv.URL, APIKey: "ABSKtoken123"})
		a.model = "claude-sonnet-4-6"
		resp, err := a.DoRequestBytesWithContext(context.Background(), []byte(`{}`), false)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		resp.Body.Close()
		if gotAuth != "Bearer ABSKtoken123" {
			t.Errorf("Authorization = %q, want Bearer ABSKtoken123", gotAuth)
		}
		if gotAmzDate != "" {
			t.Errorf("bearer request must not be SigV4-signed, got x-amz-date=%q", gotAmzDate)
		}
	})

	t.Run("aksk", func(t *testing.T) {
		gotAuth, gotAmzDate = "", ""
		a := NewAWSAdaptor(&base.AdaptorConfig{BaseURL: srv.URL, APIKey: "AKIAEXAMPLE:secret123"})
		a.model = "claude-sonnet-4-6"
		resp, err := a.DoRequestBytesWithContext(context.Background(), []byte(`{}`), false)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		resp.Body.Close()
		if !strings.HasPrefix(gotAuth, "AWS4-HMAC-SHA256 ") {
			t.Errorf("AK/SK request must be SigV4-signed, got Authorization=%q", gotAuth)
		}
		if gotAmzDate == "" {
			t.Errorf("AK/SK request must set x-amz-date")
		}
	})
}

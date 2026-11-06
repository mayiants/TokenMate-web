package keypool

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsDefinitelyKeyInvalid(t *testing.T) {
	tests := []struct {
		name        string
		httpStatus  int
		errorCode   string
		errorType   string
		errorMsg    string
		wantInvalid bool
	}{
		// === Definite key-invalid cases ===
		{
			name:        "OpenRouter: dead key User not found (type=upstream_error)",
			httpStatus:  401,
			errorCode:   "upstream_error",
			errorType:   "upstream_error",
			errorMsg:    `{"type":"error","error":{"type":"authentication_error","message":"User not found.","error_type":"authentication"}}`,
			wantInvalid: true,
		},
		{
			name:        "OpenAI: incorrect API key",
			httpStatus:  401,
			errorCode:   "invalid_api_key",
			errorType:   "invalid_request_error",
			errorMsg:    "Incorrect API key provided: sk-1234****abcd",
			wantInvalid: true,
		},
		{
			name:        "OpenAI: invalid authentication",
			httpStatus:  401,
			errorCode:   "",
			errorType:   "authentication_error",
			errorMsg:    "Invalid Authentication",
			wantInvalid: true,
		},
		{
			name:        "Anthropic: invalid x-api-key",
			httpStatus:  401,
			errorCode:   "",
			errorType:   "authentication_error",
			errorMsg:    "invalid x-api-key",
			wantInvalid: true,
		},
		{
			name:        "OpenAI: must be member of org",
			httpStatus:  401,
			errorCode:   "",
			errorType:   "",
			errorMsg:    "You must be a member of an organization to use the API",
			wantInvalid: true,
		},
		{
			name:        "Key revoked",
			httpStatus:  401,
			errorCode:   "",
			errorType:   "",
			errorMsg:    "This API key has been revoked. Please generate a new one.",
			wantInvalid: true,
		},
		{
			name:        "Key expired",
			httpStatus:  401,
			errorCode:   "",
			errorType:   "",
			errorMsg:    "API key has expired",
			wantInvalid: true,
		},

		// === Model-level errors (should NOT invalidate key) ===
		{
			name:        "accessapi: Chinese invalid token for model",
			httpStatus:  401,
			errorCode:   "",
			errorType:   "",
			errorMsg:    "无效的令牌 (model babbage-002 is not supported)",
			wantInvalid: false,
		},
		{
			name:        "403: model not available in plan",
			httpStatus:  403,
			errorCode:   "",
			errorType:   "permission_error",
			errorMsg:    "Your API key does not have permission to use the model claude-opus-4",
			wantInvalid: false,
		},
		{
			name:        "403: country not supported",
			httpStatus:  403,
			errorCode:   "",
			errorType:   "permission_error",
			errorMsg:    "Country, region, or territory not supported",
			wantInvalid: false,
		},
		{
			name:        "401 with model not found in message",
			httpStatus:  401,
			errorCode:   "",
			errorType:   "",
			errorMsg:    "Model gpt-4-32k is not found or not available for this key",
			wantInvalid: false,
		},
		{
			name:        "401 with 'not supported' in message",
			httpStatus:  401,
			errorCode:   "",
			errorType:   "",
			errorMsg:    "This model is not supported with your current API key tier",
			wantInvalid: false,
		},
		{
			name:        "403 generic without clear key signal",
			httpStatus:  403,
			errorCode:   "",
			errorType:   "",
			errorMsg:    "Forbidden",
			wantInvalid: false,
		},
		{
			name:        "authentication_error but message says model",
			httpStatus:  401,
			errorCode:   "",
			errorType:   "authentication_error",
			errorMsg:    "Your API key does not have access to model gpt-5-pro",
			wantInvalid: false,
		},
		{
			name:        "accessapi: plain Chinese invalid token",
			httpStatus:  401,
			errorCode:   "",
			errorType:   "",
			errorMsg:    "无效的令牌",
			wantInvalid: false,
		},

		// === Edge cases ===
		{
			name:        "401 with empty message (conservative: key invalid)",
			httpStatus:  401,
			errorCode:   "",
			errorType:   "",
			errorMsg:    "",
			wantInvalid: true,
		},
		{
			name:        "403 with empty message (conservative: not key invalid)",
			httpStatus:  403,
			errorCode:   "",
			errorType:   "",
			errorMsg:    "",
			wantInvalid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isDefinitelyKeyInvalid(tt.httpStatus, tt.errorCode, tt.errorType, tt.errorMsg)
			assert.Equal(t, tt.wantInvalid, got,
				"isDefinitelyKeyInvalid(%d, %q, %q, %q) = %v, want %v",
				tt.httpStatus, tt.errorCode, tt.errorType, tt.errorMsg, got, tt.wantInvalid)
		})
	}
}

func TestContainsModelLevelSignal(t *testing.T) {
	tests := []struct {
		msg  string
		want bool
	}{
		{"model not found", true},
		{"Model gpt-4 is not available", true},
		{"not supported for this region", true},
		{"permission denied for resource", true},
		{"access denied to this endpoint", true},
		{"country restriction applies", true},
		{"incorrect api key provided", false},
		{"invalid authentication", false},
		{"rate limit exceeded", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.msg, func(t *testing.T) {
			got := containsModelLevelSignal(tt.msg)
			assert.Equal(t, tt.want, got)
		})
	}
}

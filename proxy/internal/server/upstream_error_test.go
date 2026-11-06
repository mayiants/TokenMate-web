package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestClassifyUpstreamError(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		wantStatus int
		wantType   string
		wantCode   string
	}{
		{"bad request", 400, 400, "invalid_request_error", "UPSTREAM_BAD_REQUEST"},
		{"unauthorized", 401, 401, "authentication_error", "UPSTREAM_ACCESS_DENIED"},
		{"forbidden", 403, 403, "authentication_error", "UPSTREAM_ACCESS_DENIED"},
		{"not found", 404, 404, "invalid_request_error", "MODEL_NOT_FOUND"},
		{"timeout", 408, 408, "timeout_error", "UPSTREAM_TIMEOUT"},
		{"rate limited", 429, 429, "rate_limit_error", "UPSTREAM_RATE_LIMITED"},
		{"server error maps to 502", 500, 502, "upstream_error", "UPSTREAM_ERROR"},
		{"bad gateway maps to 502", 502, 502, "upstream_error", "UPSTREAM_ERROR"},
		{"other 4xx keeps status", 422, 422, "upstream_error", "UPSTREAM_ERROR"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := ClassifyUpstreamError(tt.status)
			if e.ClientStatus != tt.wantStatus {
				t.Errorf("ClientStatus = %d, want %d", e.ClientStatus, tt.wantStatus)
			}
			if e.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", e.Type, tt.wantType)
			}
			if e.Code != tt.wantCode {
				t.Errorf("Code = %q, want %q", e.Code, tt.wantCode)
			}
			if e.Message == "" {
				t.Error("Message must not be empty")
			}

			// JSON must be a valid OpenAI-style error envelope.
			var parsed struct {
				Error struct {
					Message string `json:"message"`
					Type    string `json:"type"`
					Code    string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(e.JSON(), &parsed); err != nil {
				t.Fatalf("JSON() is not valid JSON: %v", err)
			}
			if parsed.Error.Code != tt.wantCode {
				t.Errorf("JSON code = %q, want %q", parsed.Error.Code, tt.wantCode)
			}
		})
	}
}

func TestUpstreamUnreachableError(t *testing.T) {
	e := UpstreamUnreachableError()
	if e.ClientStatus != http.StatusBadGateway {
		t.Errorf("ClientStatus = %d, want 502", e.ClientStatus)
	}
	if e.Code != "UPSTREAM_UNREACHABLE" {
		t.Errorf("Code = %q, want UPSTREAM_UNREACHABLE", e.Code)
	}
}

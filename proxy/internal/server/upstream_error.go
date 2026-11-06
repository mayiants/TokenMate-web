package server

import (
	"encoding/json"
	"net/http"
)

// UpstreamError is a sanitized, client-facing rendering of an upstream
// provider failure. Raw upstream error bodies are never forwarded to clients:
// they may leak provider internals (account IDs, request IDs, upstream key
// details, internal URLs). The raw body is still logged server-side and fed
// to the key-pool/health reporters where it is needed.
type UpstreamError struct {
	ClientStatus int    // HTTP status returned to the client
	Type         string // OpenAI-style error type
	Code         string // machine-readable error code
	Message      string // safe, human-readable message (no upstream details)
}

// ClassifyUpstreamError maps an upstream HTTP status to a sanitized client
// error. Status mapping follows docs/proxy-return-code-spec: upstream 4xx
// keeps its status code, upstream 5xx maps to 502 Bad Gateway.
func ClassifyUpstreamError(statusCode int) UpstreamError {
	clientStatus := statusCode
	if clientStatus >= 500 {
		clientStatus = http.StatusBadGateway
	}

	e := UpstreamError{ClientStatus: clientStatus}
	switch {
	case statusCode == http.StatusBadRequest:
		e.Type = "invalid_request_error"
		e.Code = "UPSTREAM_BAD_REQUEST"
		e.Message = "The request was rejected by the upstream provider. Check the model name and request parameters."
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		e.Type = "authentication_error"
		e.Code = "UPSTREAM_ACCESS_DENIED"
		e.Message = "The upstream provider denied access to this model."
	case statusCode == http.StatusNotFound:
		e.Type = "invalid_request_error"
		e.Code = "MODEL_NOT_FOUND"
		e.Message = "The requested model is not available on the upstream provider."
	case statusCode == http.StatusRequestTimeout:
		e.Type = "timeout_error"
		e.Code = "UPSTREAM_TIMEOUT"
		e.Message = "The upstream provider timed out. Please retry."
	case statusCode == http.StatusTooManyRequests:
		e.Type = "rate_limit_error"
		e.Code = "UPSTREAM_RATE_LIMITED"
		e.Message = "The upstream provider rate limit was reached. Please retry later."
	case statusCode >= 500:
		e.Type = "upstream_error"
		e.Code = "UPSTREAM_ERROR"
		e.Message = "The upstream provider returned an internal error. Please retry later."
	default:
		e.Type = "upstream_error"
		e.Code = "UPSTREAM_ERROR"
		e.Message = "The upstream provider rejected the request."
	}
	return e
}

// UpstreamUnreachableError is used when the upstream request failed at
// transport level (DNS, connect, TLS, timeout) before any HTTP status was
// received. The underlying error is logged server-side only.
func UpstreamUnreachableError() UpstreamError {
	return UpstreamError{
		ClientStatus: http.StatusBadGateway,
		Type:         "upstream_error",
		Code:         "UPSTREAM_UNREACHABLE",
		Message:      "Failed to reach the upstream provider. Please retry later.",
	}
}

// JSON renders the error as an OpenAI chat-style error envelope.
func (e UpstreamError) JSON() []byte {
	payload, _ := json.Marshal(map[string]interface{}{
		"error": map[string]string{
			"message": e.Message,
			"type":    e.Type,
			"code":    e.Code,
		},
	})
	return payload
}

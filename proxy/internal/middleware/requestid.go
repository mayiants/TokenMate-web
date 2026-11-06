package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	RequestIDHeader = "X-Request-ID"
	// TraceIDHeader always carries the internal trace id, even when the client
	// supplied its own X-Request-ID, so customers can quote it to support.
	TraceIDHeader = "X-Trace-Id"
)

// RequestID middleware assigns a proxy-unique request ID to every request.
//
// A client-provided X-Request-ID is preserved in the "client_request_id"
// context key and echoed back in the response header, but is never used as
// the internal ID: clients may reuse one ID per conversation or send an
// all-zero ID (e.g. WorkBuddy), which then collides in trace_id-keyed
// billing settlement and retention storage.
//
// trace_id is set from the same internal UUID so all downstream consumers
// (pre-consume, settle, usage records, retention) share one stable ID
// without each path falling back to uuid.New() independently.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := uuid.New().String()
		c.Set("request_id", rid)
		c.Set("trace_id", rid)
		c.Header(TraceIDHeader, rid)

		if clientRID := c.GetHeader(RequestIDHeader); clientRID != "" {
			c.Set("client_request_id", clientRID)
			c.Header(RequestIDHeader, clientRID)
		} else {
			c.Header(RequestIDHeader, rid)
		}

		c.Next()
	}
}

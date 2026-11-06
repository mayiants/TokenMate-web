package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const RequestIDHeader = "X-Request-ID"

// RequestID middleware generates or propagates a request ID
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check if client provided a request ID
		rid := c.GetHeader(RequestIDHeader)
		if rid == "" {
			rid = uuid.New().String()
		}

		// Set in context and response header
		c.Set("request_id", rid)
		c.Header(RequestIDHeader, rid)

		c.Next()
	}
}

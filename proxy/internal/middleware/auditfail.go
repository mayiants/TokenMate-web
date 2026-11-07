package middleware

import (
	"net/http"
	"time"

	"github.com/mayiants/ai-proxy-go/internal/audit"
	"github.com/gin-gonic/gin"
)

// AuditFailures records an audit row for every request that ends with a
// status >= 400 and was not already audited by a handler. It must be
// registered after RequestID and BEFORE the auth middleware so 401/402/403/429
// rejections raised by auth and rate limiting are observed too.
//
// It deliberately never touches the usage/billing buffers: failure rows carry
// zero cost and zero tokens and exist only for support and forensics.
//
// loggerFn is resolved per request because the audit logger may be attached to
// the handler service after routes are registered.
func AuditFailures(loggerFn func() *audit.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		status := c.Writer.Status()
		if status < http.StatusBadRequest {
			return
		}
		logger := loggerFn()
		if logger == nil {
			return
		}
		traceID := c.GetString("trace_id")
		if traceID == "" || logger.WasLogged(traceID) {
			return
		}

		// Only a hash is ever stored; the raw key stays in the auth layer.
		keyHash := c.GetString("key_hash")
		clientIP := c.ClientIP()
		if keyHash == "" && !logger.AllowAnonymousFailure(clientIP) {
			return
		}

		model := c.GetString("model")
		if model == "" {
			model = c.GetString("logical_model")
		}
		msg := c.GetString("error_message")
		if msg == "" {
			msg = http.StatusText(status)
			if code := c.GetString("error_code"); code != "" {
				msg += " (" + code + ")"
			}
		}

		logger.Log(audit.AuditEntry{
			TraceID:    traceID,
			ClientIP:   clientIP,
			UserAgent:  c.Request.UserAgent(),
			Timestamp:  start,
			APIKeyHash: keyHash,
			KeyID:      c.GetString("api_key_id"),
			UserID:     c.GetString("user_id"),
			Model:      model,
			StatusCode: status,
			Error:      msg,
			Endpoint:   c.Request.URL.Path,
			LatencyMs:  time.Since(start).Milliseconds(),
			IsStream:   false,
		})
	}
}

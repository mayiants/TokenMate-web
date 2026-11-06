package server

import (
	"github.com/ai-proxy/ai-proxy-go/internal/audit"
	"github.com/gin-gonic/gin"
)

// logAudit fills the request-scoped identity fields (trace id, client IP,
// user agent, endpoint) on entry and hands it to the audit logger. Centralising
// this keeps every call site joinable with usage_records via trace_id without
// repeating the same four lines ~20 times. It must be called while the gin
// context is still valid (not from a goroutine that outlives the request).
func (s *HandlerService) logAudit(c *gin.Context, entry audit.AuditEntry) {
	if s.handlerDeps == nil || s.handlerDeps.AuditLogger == nil {
		return
	}
	if entry.TraceID == "" {
		entry.TraceID = c.GetString("trace_id")
	}
	if entry.ClientIP == "" {
		entry.ClientIP = c.ClientIP()
	}
	if entry.UserAgent == "" && c.Request != nil {
		entry.UserAgent = c.Request.UserAgent()
	}
	if entry.Endpoint == "" && c.Request != nil && c.Request.URL != nil {
		entry.Endpoint = c.Request.URL.Path
	}
	s.handlerDeps.AuditLogger.Log(entry)
}

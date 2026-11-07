package middleware

import (
	"github.com/mayiants/ai-portal-go/pkg/audit"
	"github.com/gin-gonic/gin"
)

// AuditClientInfo stores the caller's IP and User-Agent in the request's
// context.Context so audit writers (audit.Logger and the enterprise audit
// helpers) can attach them automatically. Before this, only the handful of
// hand-written INSERTs recorded an IP, leaving ~all audit_logs rows without one.
//
// c.ClientIP() honours the engine's trusted-proxy configuration, so the value
// is not trivially spoofable through X-Forwarded-For.
func AuditClientInfo() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := audit.WithClientInfo(c.Request.Context(), c.ClientIP(), c.Request.UserAgent())
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

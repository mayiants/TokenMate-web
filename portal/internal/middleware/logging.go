package middleware

import (
	"time"

	"github.com/mayiants/ai-portal-go/pkg/metrics"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func StructuredLogging(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		metrics.GetMetrics().TotalRequests.Add(1)
		start := time.Now()

		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()
		requestID, _ := c.Get("request_id")
		userID, _ := c.Get("user_id")

		fields := []zap.Field{
			zap.String("request_id", toString(requestID)),
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", status),
			zap.Duration("latency", latency),
			zap.String("client_ip", c.ClientIP()),
		}

		if uid := toString(userID); uid != "" {
			fields = append(fields, zap.String("user_id", uid))
		}

		if status >= 500 {
			metrics.GetMetrics().FailedRequests.Add(1)
			logger.Error("request", fields...)
		} else if status >= 400 {
			metrics.GetMetrics().FailedRequests.Add(1)
			logger.Warn("request", fields...)
		} else if status >= 200 && status < 300 {
			metrics.GetMetrics().SuccessfulRequests.Add(1)
			logger.Info("request", fields...)
		} else {
			logger.Info("request", fields...)
		}
	}
}

func toString(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

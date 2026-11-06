package server

import (
	"time"

	"github.com/ai-proxy/ai-proxy-go/pkg/metrics"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// MetricsMiddleware records request metrics after handler completes
func MetricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path

		// Only instrument proxy API endpoints
		if path == "/health" || path == "/metrics" || path == "/api/status" {
			c.Next()
			return
		}

		isStream := c.GetHeader("Accept") == "text/event-stream"

		c.Next()

		// After handler completes — read provider/model from context here,
		// not before c.Next(). The handler sets them during processing, so
		// reading earlier always produced empty values (dead code).
		provider := c.GetString("provider")
		model := c.GetString("model")

		status := c.Writer.Status()
		success := status >= 200 && status < 400
		latency := time.Since(start)
		latencyMs := latency.Milliseconds()

		m := metrics.GetMetrics()
		m.IncrementRequests(success, isStream)
		m.AddLatency(latencyMs)

		if provider != "" {
			m.IncrementProviderRequests(provider)
		}
		if model != "" {
			m.IncrementModelRequests(model)
		}

		// Record tokens if available in context
		if tokens, ok := c.Get("tokens_consumed"); ok {
			if t, ok := tokens.(int64); ok && t > 0 {
				m.AddTokens(0, t, 0)
			}
		}

		// Prometheus client metrics
		isFallback := false
		if v, ok := c.Get("is_fallback"); ok {
			if b, ok := v.(bool); ok {
				isFallback = b
			}
		}
		if v, ok := c.Get("is_stream"); ok {
			if b, ok := v.(bool); ok {
				isStream = b
			}
		}
		m.ObserveRequestDuration(provider, model, status, isStream, isFallback, latency)
		if isFallback {
			m.IncrementFallback(provider, model)
		}
	}
}

// LoggerWithRequestID returns a child logger with request_id from context
func (s *HandlerService) LoggerWithRequestID(c *gin.Context) *zap.Logger {
	if s.handlerDeps == nil || s.handlerDeps.Logger == nil {
		return zap.NewNop()
	}
	rid, _ := c.Get("request_id")
	if rid == nil {
		return s.handlerDeps.Logger
	}
	return s.handlerDeps.Logger.With(zap.String("request_id", rid.(string)))
}

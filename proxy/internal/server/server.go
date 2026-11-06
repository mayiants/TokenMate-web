package server

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"time"

	"github.com/ai-proxy/ai-proxy-go/internal/audit"
	"github.com/ai-proxy/ai-proxy-go/internal/auth"
	"github.com/ai-proxy/ai-proxy-go/internal/config"
	"github.com/ai-proxy/ai-proxy-go/internal/middleware"
	"github.com/ai-proxy/ai-proxy-go/pkg/metrics"
	"github.com/ai-proxy/ai-proxy-go/pkg/telemetry"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.uber.org/zap"
)

// Server - Main HTTP server
type Server struct {
	cfg            *config.Config
	logger         *zap.Logger
	router         *gin.Engine
	http           *http.Server
	authMiddleware *auth.Middleware
	service        *HandlerService
}

// New - Create new server instance
func New(cfg *config.Config, logger *zap.Logger, service *HandlerService) (*Server, error) {
	// Set Gin mode
	if cfg.Server.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	// Create router
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(middleware.RequestID())
	router.Use(TelemetryMiddleware())
	router.Use(middleware.StructuredLogging(logger))
	router.Use(corsMiddleware(cfg.Server.AllowedOrigins))

	// Concurrency limiter: cap in-flight requests to avoid goroutine scheduler
	// thrashing on low-CPU instances. Default = CPUs * 128.
	maxConcurrent := runtime.GOMAXPROCS(0) * 128
	concSem := make(chan struct{}, maxConcurrent)
	router.Use(func(c *gin.Context) {
		select {
		case concSem <- struct{}{}:
			defer func() { <-concSem }()
			c.Next()
		default:
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error": gin.H{"message": "server busy, try again later", "type": "server_busy"},
			})
			c.Abort()
			return
		}
	})

	// Service is required
	if service == nil {
		return nil, fmt.Errorf("handler service is required")
	}

	// Setup middleware
	authMiddleware := auth.NewMiddleware(cfg, logger)
	if service.authMiddleware == nil {
		service.authMiddleware = authMiddleware
	}
	service.router = router

	// Setup routes
	setupRoutes(service)

	return &Server{
		cfg:            cfg,
		logger:         logger,
		router:         router,
		authMiddleware: authMiddleware,
		service:        service,
		http: &http.Server{
			Handler:           router,
			ReadHeaderTimeout: 10 * time.Second, // guard against Slowloris; body reads are unbounded for large uploads (audio etc.)
			WriteTimeout:      0,                // no total write deadline: SSE streams can run long; each request is bounded by its own context (streaming paths use 10m)
			IdleTimeout:       120 * time.Second,
		},
	}, nil
}

// Start - Start the HTTP server
func (s *Server) Start(addr string) error {
	s.http.Addr = addr
	return s.http.ListenAndServe()
}

// Shutdown - Gracefully shutdown the server
func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

// Router - Get the router for testing
func (s *Server) Router() *gin.Engine {
	return s.router
}

// SetAuthRedisClient passes Redis client to auth middleware for distributed nonce storage
func (s *Server) SetAuthRedisClient(rdb *redis.Client) {
	if s.authMiddleware != nil {
		s.authMiddleware.SetRedisClient(rdb)
	}
}

// AuthMiddleware returns the auth middleware for external setup (e.g., balance pub/sub listener)
func (s *Server) AuthMiddleware() *auth.Middleware {
	return s.service.authMiddleware
}

// setupRoutes - Configure all routes
func setupRoutes(service *HandlerService) {
	r := service.router
	if r == nil {
		r = gin.New()
		service.router = r
	}

	// Health check (no auth required)
	r.GET("/health", func(c *gin.Context) {
		health := gin.H{
			"status":    "healthy",
			"version":   "1.0.0",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}

		// Dependency checks — expose only healthy/degraded status, not
		// internal connection details (host:port, error messages) to avoid
		// leaking infrastructure topology on a public endpoint.
		dependencies := gin.H{}

		// Redis check
		if service.cfg.Redis.Host != "" {
			redisHealthy := false
			if service.redisClient != nil {
				if err := service.redisClient.Ping(c.Request.Context()).Err(); err == nil {
					redisHealthy = true
				} else {
					service.logger.Warn("Redis health check failed", zap.Error(err))
				}
			}
			dependencies["redis"] = gin.H{"status": redisHealthy}
		}

		// Key pool check with sync freshness.
		// Treat nil manager as not configured; only degrade if initialized and stale.
		keyPoolHealthy := true
		keyPoolInfo := gin.H{"status": service.keyPoolManager != nil}
		if service.keyPoolManager != nil {
			lastSync := service.keyPoolManager.GetLastSync()
			keyPoolInfo["last_sync"] = lastSync.UTC().Format(time.RFC3339)
			if !lastSync.IsZero() {
				staleThreshold := 3 * service.cfg.Portal.SyncInterval
				if staleThreshold == 0 {
					staleThreshold = 10 * time.Minute
				}
				if time.Since(lastSync) > staleThreshold {
					keyPoolHealthy = false
					age := time.Since(lastSync).Round(time.Second)
					keyPoolInfo["error"] = "sync stale"
					service.logger.Warn("Key pool sync stale", zap.Duration("age", age))
				}
			} else {
				keyPoolInfo["info"] = "sync pending"
			}
		} else {
			keyPoolInfo["info"] = "not configured"
		}
		dependencies["key_pool"] = keyPoolInfo

		// Model registry check with sync freshness.
		modelRegistryHealthy := true
		modelRegistryInfo := gin.H{"status": service.modelRegistry != nil}
		if service.modelRegistry != nil {
			lastSync := service.modelRegistry.GetLastSync()
			modelRegistryInfo["last_sync"] = lastSync.UTC().Format(time.RFC3339)
			if !lastSync.IsZero() {
				staleThreshold := 3 * service.cfg.Portal.SyncInterval
				if staleThreshold == 0 {
					staleThreshold = 10 * time.Minute
				}
				if time.Since(lastSync) > staleThreshold {
					modelRegistryHealthy = false
					age := time.Since(lastSync).Round(time.Second)
					modelRegistryInfo["error"] = "sync stale"
					service.logger.Warn("Model registry sync stale", zap.Duration("age", age))
				}
			} else {
				modelRegistryInfo["info"] = "sync pending"
			}
		} else {
			modelRegistryInfo["info"] = "not configured"
		}
		dependencies["model_registry"] = modelRegistryInfo

		// Overall health is degraded only if an initialized dependency has gone stale.
		if !keyPoolHealthy || !modelRegistryHealthy {
			health["status"] = "degraded"
			c.JSON(http.StatusServiceUnavailable, health)
			return
		}

		health["dependencies"] = dependencies
		c.JSON(200, health)
	})

	// Metrics endpoint (no auth required)
	// Returns both legacy hand-written metrics and Prometheus client metrics for backward compatibility.
	r.GET("/metrics", func(c *gin.Context) {
		rec := httptest.NewRecorder()
		promhttp.HandlerFor(metrics.GetPrometheusRegistry().Registry, promhttp.HandlerOpts{}).ServeHTTP(rec, c.Request)
		c.Header("Content-Type", "text/plain; version=0.0.4")
		c.String(200, metrics.GetMetrics().ToPrometheusFormat()+rec.Body.String())
	})

	// Status endpoints (public, minimal info for health monitoring)
	status := r.Group("/api/status")
	{
		status.GET("", service.handleGetStatus)
		status.GET("/history", service.handleGetStatusHistory)
		status.POST("/heartbeat", service.handleHeartbeat)
	}

	// Provider/model detail endpoints (require admin auth — expose infrastructure topology)
	statusAdmin := r.Group("/api/status")
	statusAdmin.Use(service.authMiddleware.AdminHandler())
	{
		statusAdmin.GET("/providers", service.handleGetStatusProviders)
	}
	r.Group("").Use(service.authMiddleware.AdminHandler()).GET("/providers/stats", service.handleGetProviderStats)
	r.Group("").Use(service.authMiddleware.AdminHandler()).GET("/models/stats", service.handleGetModelStats)
	r.Group("").Use(service.authMiddleware.AdminHandler()).GET("/models/list", service.handleGetModelList)

	// Proxy endpoints (require authentication)
	proxy := r.Group("/v1")
	// Failure audit must wrap auth so 401/402/429 rejections are recorded.
	proxy.Use(middleware.AuditFailures(func() *audit.Logger {
		if service.handlerDeps == nil {
			return nil
		}
		return service.handlerDeps.AuditLogger
	}))
	proxy.Use(service.authMiddleware.Handler())
	proxy.Use(service.rateLimitMiddleware.Handler())
	proxy.Use(MetricsMiddleware())
	{
		proxy.POST("/chat/completions", service.handleChatCompletions)
		proxy.POST("/completions", service.handleCompletions)
		proxy.POST("/embeddings", service.handleEmbeddings)
		proxy.POST("/moderations", service.handleModerations)
		proxy.POST("/images/generations", service.handleImageGenerations)
		proxy.POST("/images/edits", service.handleImageEdits)
		proxy.GET("/images/generations/:task_id", service.handleGetImageGeneration)
		proxy.POST("/video/generations", service.handleVideoGenerations)
		proxy.GET("/video/generations/:task_id", service.handleGetVideoGeneration)
		proxy.POST("/audio/speech", service.handleAudioSpeech)
		proxy.POST("/audio/music", service.handleAudioMusic)
		proxy.POST("/audio/generations", service.handleAudioGenerations)
		proxy.GET("/audio/generations/:task_id", service.handleGetAudioGeneration)
		proxy.POST("/audio/transcriptions", service.handleAudioTranscriptions)
		proxy.POST("/audio/translations", service.handleAudioTranslations)

		// Claude native endpoint
		proxy.POST("/messages", service.handleClaudeMessages)

		// Responses API (OpenAI new format)
		proxy.POST("/responses", service.handleResponses)

		// OpenAI-compatible model list
		proxy.GET("/models", service.handleListModels)
	}

	// Admin endpoints (require admin auth)
	admin := r.Group("/admin")
	admin.Use(service.authMiddleware.AdminHandler())
	{
		admin.GET("/stats", service.handleGetStats)
		admin.GET("/cache/affinity", service.handleGetAffinityStats)
		admin.DELETE("/cache/affinity", service.handleClearAffinityCache)
		admin.GET("/audit/search", service.handleAuditSearch)

		// Retention object lookup (local disk; mirrors future S3 object admin API)
		admin.GET("/retention/objects/:trace_id", service.handleGetRetentionObject)
		admin.GET("/retention/enterprises/:ent_id/objects", service.handleListRetentionEnterpriseObjects)
		admin.DELETE("/retention/enterprises/:ent_id", service.handlePurgeRetentionEnterprise)

		// Retention analytics
		admin.GET("/analytics/summary", service.handleGetAnalyticsSummary)
		admin.GET("/analytics/models", service.handleGetAnalyticsModels)
		admin.GET("/analytics/performance", service.handleGetAnalyticsPerformance)
		admin.GET("/analytics/reliability", service.handleGetAnalyticsReliability)
		admin.POST("/analytics/refresh", service.handleGetAnalyticsRefresh)

		// Key Pool management (for Portal sync)
		admin.GET("/keys/:provider/:keyId/history", service.handleGetKeyHistory)
		admin.POST("/keys", service.handleAddKey)
		admin.DELETE("/keys/:provider/:keyId", service.handleDeleteKey)
		admin.PATCH("/keys/:provider/:keyId", service.handleToggleKey)
		admin.POST("/keys/import", service.handleImportKeys)
		admin.POST("/keys/:provider/:keyId/reset-runtime", service.handleResetKeyRuntime)
		admin.GET("/keypool/selection-state", service.handleGetSelectionState)
		admin.POST("/circuit-breakers/:provider/reset", service.handleResetCircuitBreaker)

	}

	// Webhook endpoints (for Portal communication) — require SyncSecret
	webhook := r.Group("/api/webhook")
	webhook.Use(func(c *gin.Context) {
		secret := c.GetHeader("X-Sync-Secret")
		if secret == "" {
			secret = c.GetHeader("X-Admin-Secret")
		}
		if service.cfg.Portal.SyncSecret == "" || subtle.ConstantTimeCompare([]byte(secret), []byte(service.cfg.Portal.SyncSecret)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "Unauthorized"}})
			return
		}
		c.Next()
	})
	{
		webhook.POST("/heartbeat", service.handleHeartbeat)
		webhook.POST("/usage", service.handleUsageReport)
		webhook.POST("/usage-batch", service.handleBatchUsageReport)
		webhook.POST("/key-pool-status", service.handleKeyPoolStatus)
		webhook.POST("/sync", service.handleWebhookSync)
		webhook.POST("/balance-update", service.handleWebhookBalanceUpdate)
	}

	// Key Pool sync endpoint (for Portal to push keys)
	r.GET("/api/admin/key-pool/sync", service.handleSyncKeys)

	// Internal API endpoints (for Portal validation requests)
	internal := r.Group("/api/internal")
	internal.Use(func(c *gin.Context) {
		// Verify admin secret
		secret := c.GetHeader("X-Admin-Secret")
		if secret == "" || subtle.ConstantTimeCompare([]byte(secret), []byte(service.cfg.Admin.Secret)) != 1 {
			c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "Unauthorized"}})
			c.Abort()
			return
		}
		c.Next()
	})
	{
		internal.POST("/validate-model", service.handleValidateModel)
		internal.POST("/batch-validate", service.handleBatchValidate)
		internal.POST("/validate-upstream-key", service.handleValidateUpstreamKey)
	}
}

// TelemetryMiddleware starts an OpenTelemetry span for each inbound HTTP request,
// extracts any W3C traceparent header so Portal→Proxy calls are linked, and sets
// standard HTTP attributes on the span.
func TelemetryMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := telemetry.Extract(c.Request.Context(), c.Request.Header)

		spanName := c.Request.Method + " " + c.FullPath()
		if c.FullPath() == "" {
			spanName = c.Request.Method + " " + c.Request.URL.Path
		}

		ctx, span := telemetry.Tracer().Start(ctx, spanName)
		defer span.End()

		c.Request = c.Request.WithContext(ctx)
		c.Next()

		span.SetAttributes(
			semconv.HTTPMethodKey.String(c.Request.Method),
			semconv.HTTPRouteKey.String(c.FullPath()),
			semconv.HTTPStatusCodeKey.Int(c.Writer.Status()),
		)
		if len(c.Errors) > 0 {
			span.RecordError(c.Errors[0].Err)
		}
	}
}

func corsMiddleware(allowedOrigins []string) gin.HandlerFunc {
	allowAll := false
	explicit := make([]string, 0, len(allowedOrigins))
	for _, o := range allowedOrigins {
		if o == "*" {
			allowAll = true
		} else if o != "" {
			explicit = append(explicit, o)
		}
	}

	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")
		if origin == "" {
			c.Next()
			return
		}

		matched := false
		if allowAll {
			c.Header("Access-Control-Allow-Origin", "*")
			matched = true
		} else {
			for _, o := range explicit {
				if o == origin {
					c.Header("Access-Control-Allow-Origin", origin)
					c.Header("Access-Control-Allow-Credentials", "true")
					matched = true
					break
				}
			}
		}

		if !matched {
			// Cross-origin request from an unlisted origin: deny.
			if c.Request.Method == "OPTIONS" {
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
			c.Next()
			return
		}

		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key, X-Request-ID, X-Admin-Secret")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	}
}

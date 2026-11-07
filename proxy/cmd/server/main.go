package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mayiants/ai-proxy-go/internal/audit"
	"github.com/mayiants/ai-proxy-go/internal/config"
	"github.com/mayiants/ai-proxy-go/internal/ratelimit"
	"github.com/mayiants/ai-proxy-go/internal/retention"
	"github.com/mayiants/ai-proxy-go/internal/retention/analytics"
	"github.com/mayiants/ai-proxy-go/internal/server"
	"github.com/mayiants/ai-proxy-go/pkg/billing"
	"github.com/mayiants/ai-proxy-go/pkg/cache"
	"github.com/mayiants/ai-proxy-go/pkg/keypool"
	"github.com/mayiants/ai-proxy-go/pkg/models"
	"github.com/mayiants/ai-proxy-go/pkg/plugin"
	"github.com/mayiants/ai-proxy-go/pkg/routing"
	"github.com/mayiants/ai-proxy-go/pkg/telemetry"
	"github.com/mayiants/ai-proxy-go/pkg/token"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

var (
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)

func main() {
	// Root context for background goroutines; cancelled on shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize logger
	logger, _ := zap.NewProduction()
	defer func() { _ = logger.Sync() }()

	// Initialize OpenTelemetry tracer (no-op when OTEL env vars are absent)
	if _, err := telemetry.InitTracer("ai-proxy-go"); err != nil {
		logger.Warn("Failed to initialize telemetry tracer, continuing without tracing", zap.Error(err))
	}

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		logger.Fatal("Failed to load config", zap.Error(err))
	}

	// Security: validate admin secret
	if cfg.Admin.Secret == "" || cfg.Admin.Secret == "admin-secret" || len(cfg.Admin.Secret) < 32 {
		logger.Fatal("Admin secret is not configured or too weak. Set a strong admin.secret (>= 32 chars) in config.")
	}

	// Security: validate portal sync secret
	if cfg.Portal.SyncSecret == "" || cfg.Portal.SyncSecret == "admin-secret" || len(cfg.Portal.SyncSecret) < 32 {
		logger.Fatal("Portal sync secret is not configured or too weak. Set a strong portal.sync_secret (>= 32 chars) in config.")
	}

	// Security: dev_mode with empty Portal URL is a production risk
	if cfg.Portal.DevMode && cfg.Portal.URL == "" {
		logger.Fatal("Dev mode is enabled but Portal URL is not configured. This allows any API key to authenticate. Set portal.url or disable dev_mode.")
	}

	logger.Info("Starting AI Proxy",
		zap.String("version", Version),
		zap.String("build_time", BuildTime),
		zap.String("git_commit", GitCommit),
	)

	// Initialize audit logger
	var auditLogger *audit.Logger
	if cfg.Audit.Enabled {
		auditLogger, err = audit.NewLogger(cfg, logger)
		if err != nil {
			logger.Warn("Failed to initialize audit logger, continuing without audit", zap.Error(err))
		}
	}

	// Initialize key pool manager
	keyPoolMgr := keypool.NewManager(cfg, logger)

	// Create handler service and inject dependencies
	service := server.NewHandlerService(cfg, logger)
	service.SetKeyPoolManager(keyPoolMgr)

	// Initialize model registry (syncs service models from Portal)
	modelRegistry := models.NewModelRegistry(cfg, logger)
	service.SetModelRegistry(modelRegistry)

	// Start key pool sync loop (syncs from Portal via HTTP polling)
	if cfg.Portal.URL != "" && cfg.Portal.SyncSecret != "" {
		keyPoolMgr.StartSyncLoop(ctx)
		modelRegistry.StartSyncLoop(ctx)
		logger.Info("Portal sync started",
			zap.String("portal_url", cfg.Portal.URL),
			zap.Duration("interval", cfg.Portal.SyncInterval),
		)
	}

	// Start Redis Pub/Sub listener for real-time sync notifications
	keyPoolMgr.StartPubSubListener(ctx)

	// Start key pool health check (handles cooldowns and 24h purge)
	keyPoolMgr.StartHealthCheck(ctx)

	// Initialize rate limit middleware
	rateLimitMiddleware := ratelimit.NewMiddleware(cfg, logger)
	service.SetRateLimitMiddleware(rateLimitMiddleware)

	// Initialize affinity cache (with Redis if available)
	affinityCache := cache.NewAffinityCache(cfg.Cache.AffinityTTL)
	var redisClient *redis.Client
	if cfg.Redis.Host != "" {
		// Create Redis client for affinity cache and distributed nonce storage
		redisClient = redis.NewClient(cfg.Redis.Options())
		affinityCache = cache.NewAffinityCacheWithConfig(&cache.AffinityConfig{
			Redis:      redisClient,
			MaxEntries: cfg.Cache.Affinity.MaxEntries,
			DefaultTTL: cfg.Cache.Affinity.DefaultTTL,
		})
		logger.Info("Affinity cache initialized with Redis")
	} else {
		logger.Info("Affinity cache initialized without Redis (local memory only)")
	}
	affinityCache.StartEviction(ctx, logger)

	// Initialize handler dependencies
	deps := &server.HandlerDependencies{
		Logger:        logger,
		AuditLogger:   auditLogger,
		AffinityCache: affinityCache,
		TokenCounter:  token.NewCounter(),
		Billing:       billing.NewBillingWithPortal(cfg.Portal.URL, cfg.Portal.SyncSecret, cfg.Portal.SyncInterval),
		Simulate: server.SimulateConfig{
			Enabled:   cfg.Simulate.Enabled,
			LatencyMs: cfg.Simulate.LatencyMs,
		},
	}
	service.SetHandlerDependencies(deps)

	// Start pricing sync loop now that Billing is initialized.
	if cfg.Portal.URL != "" && cfg.Portal.SyncSecret != "" {
		deps.Billing.StartPricingSyncLoop(ctx)
	}

	// Set server ID for usage record attribution
	service.SetServerID(cfg.Server.ID)

	// Initialize request/response rewrite plugin chain
	if cfg.Plugin.Enabled {
		var plugins []plugin.Plugin
		if cfg.Plugin.StreamOptionsInjector {
			plugins = append(plugins, plugin.StreamOptionsInjector{})
		}
		if cfg.Plugin.ClaudeCodeCCHNormalizer {
			plugins = append(plugins, plugin.ClaudeCodeCCHNormalizer{})
		}
		if cfg.Plugin.ClaudeCodeTrackingRemover {
			plugins = append(plugins, plugin.ClaudeCodeTrackingRemover{})
		}
		if cfg.Plugin.ResponsesPromptCacheKey {
			plugins = append(plugins, plugin.ResponsesPromptCacheKeyRewriter{})
		}
		service.SetPluginChain(plugin.NewChain(logger, plugins...))
		logger.Info("Plugin chain initialized",
			zap.Int("plugin_count", len(plugins)),
		)
	} else {
		logger.Info("Plugin chain disabled")
	}

	// Initialize scene-based auto model routing (Phase 2)
	service.SetSceneMapping(routing.DefaultSceneModelMapping())
	logger.Info("Scene-based auto routing initialized")

	// Initialize budget controller (Phase 4)
	if cfg.Billing.DailyBudgetUSD > 0 {
		bc := routing.NewBudgetController(routing.BudgetConfig{
			DailyBudgetUSD:     cfg.Billing.DailyBudgetUSD,
			WarningThreshold:   0.7,
			DowngradeThreshold: 0.85,
		})
		// Connect pricing data for dynamic expensive model detection
		bc.SetPricingLookup(func(model string) float64 {
			pricing := deps.Billing.GetPricing(model, "")
			if pricing != nil && pricing.InputPricePer1M > 0 {
				return pricing.InputPricePer1M
			}
			if pricing != nil && pricing.ModelRatio > 0 {
				return pricing.ModelRatio
			}
			return 0
		})
		service.SetBudgetController(bc)
		logger.Info("Budget controller initialized",
			zap.Float64("daily_budget_usd", cfg.Billing.DailyBudgetUSD),
		)
	}

	// Initialize settle buffer for reliable non-fire-and-forget settlement
	settleBuf := server.NewSettleBuffer(deps.Billing, logger, 100)
	service.SetSettleBuffer(settleBuf)
	settleBuf.Start(ctx)

	// Initialize usage buffer for batch reporting to Portal
	usageBuf := server.NewUsageBuffer(deps.Billing, logger, 100, 3*time.Second)
	service.SetUsageBuffer(usageBuf)
	usageBuf.StartFlushLoop(ctx)

	// Initialize upstream usage buffer for provider reconciliation (30s flush)
	upstreamUsageBuf := server.NewUpstreamUsageBuffer(deps.Billing, logger, 500, 30*time.Second)
	service.SetUpstreamUsageBuffer(upstreamUsageBuf)
	upstreamUsageBuf.StartFlushLoop(ctx)

	// Initialize provider health buffer for real-time health monitoring (30s flush)
	providerHealthBuf := server.NewProviderHealthBuffer(deps.Billing, logger, 500, 30*time.Second)
	service.SetProviderHealthBuffer(providerHealthBuf)
	providerHealthBuf.StartFlushLoop(ctx)

	// Initialize local-disk retention store (disabled by default; no S3 needed)
	var retentionStore retention.Store = retention.NoopStore{}
	if cfg.Retention.Enabled {
		store, err := retention.NewDiskStore(retention.Config{
			Enabled:           cfg.Retention.Enabled,
			Directory:         cfg.Retention.Directory,
			MaxAge:            cfg.Retention.MaxAge,
			BatchSize:         cfg.Retention.BatchSize,
			FlushEvery:        cfg.Retention.FlushEvery,
			CaptureAllHeaders: cfg.Retention.CaptureAllHeaders,
			MaxDiskPercent:    cfg.Retention.MaxDiskPercent,
		}, logger)
		if err != nil {
			logger.Fatal("Failed to initialize retention store", zap.Error(err))
		}
		retentionStore = store
		// Expire old records: first pass shortly after boot (so a restart loop
		// does not hammer the disk), then hourly. Stops when ctx is cancelled.
		if cfg.Portal.URL != "" && cfg.Portal.SyncSecret != "" {
			// Cleanup follows the Portal's current per-enterprise/platform
			// retention policy, so changing a window (e.g. 30d -> 90d) applies
			// to data already on disk without moving files between <N>d dirs.
			store.SetPolicySource(retention.NewPortalPolicySource(cfg.Portal.URL, cfg.Portal.SyncSecret, nil))
		}
		retention.RunCleanupLoop(ctx, store, logger, 2*time.Minute, time.Hour)
		logger.Info("Local-disk retention store initialized",
			zap.String("directory", cfg.Retention.Directory),
			zap.Duration("max_age", cfg.Retention.MaxAge),
		)
	}
	service.SetRetentionStore(retentionStore)

	// Initialize retention analytics scheduler (runs alongside retention)
	if cfg.Retention.Enabled {
		analyticsSched := analytics.NewScheduler(analytics.SchedulerConfig{
			RetentionDir: cfg.Retention.Directory,
			Interval:     6 * time.Hour,
			MaxAge:       cfg.Retention.MaxAge,
		}, logger)
		analyticsSched.Start()
		service.SetAnalyticsScheduler(analyticsSched)
		defer analyticsSched.Stop()
		logger.Info("Retention analytics scheduler started", zap.Duration("interval", 6*time.Hour))
	}

	logger.Info("Simulate mode", zap.Bool("enabled", cfg.Simulate.Enabled), zap.Int("latency_ms", cfg.Simulate.LatencyMs))

	// Create server
	srv, err := server.New(cfg, logger, service)
	if err != nil {
		logger.Fatal("Failed to create server", zap.Error(err))
	}

	// Pass Redis to auth middleware for distributed nonce storage
	if redisClient != nil {
		srv.SetAuthRedisClient(redisClient)
		service.SetRedisClient(redisClient)
	}

	// Start balance pub/sub listener for near-instant rejection when Portal detects low balance
	srv.AuthMiddleware().StartBalanceListener(ctx, redisClient)

	// Start server in goroutine
	go func() {
		addr := fmt.Sprintf(":%d", cfg.Server.Port)
		logger.Info("Server starting", zap.String("addr", addr))

		if err := srv.Start(addr); err != nil && err != http.ErrServerClosed {
			logger.Fatal("Server failed", zap.Error(err))
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down server...")

	// Graceful shutdown with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// Close audit logger
	if auditLogger != nil {
		if err := auditLogger.Close(); err != nil {
			logger.Warn("Audit logger close error", zap.Error(err))
		}
	}

	// Close retention store to flush any buffered records
	if retentionStore != nil {
		if err := retentionStore.Close(); err != nil {
			logger.Warn("Retention store close error", zap.Error(err))
		}
	}

	// Drain in-flight HTTP requests first (handlers may still call usageBuf.Add)
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("Server shutdown error", zap.Error(err))
	}

	// NOW cancel context to trigger final flush of usage/health buffers
	// (all handlers have completed, no more Add() calls will arrive)
	cancel()

	// Wait for usage buffer and settle buffer to drain
	usageBuf.StopAndWait(5 * time.Second)
	settleBuf.StopAndWait(5 * time.Second)

	// Shutdown OpenTelemetry tracer
	if err := telemetry.Shutdown(shutdownCtx); err != nil {
		logger.Warn("Telemetry shutdown error", zap.Error(err))
	}

	// Close Redis connection
	if redisClient != nil {
		if err := redisClient.Close(); err != nil {
			logger.Warn("Redis close error", zap.Error(err))
		}
		logger.Info("Redis connection closed")
	}

	logger.Info("Server stopped")
}

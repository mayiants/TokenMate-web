package main

import (
	"context"
	"crypto/subtle"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/middleware"
	"github.com/ai-proxy/ai-portal-go/internal/sso"
	"github.com/ai-proxy/ai-portal-go/internal/web"
	"github.com/ai-proxy/ai-portal-go/webui"
	"github.com/ai-proxy/ai-portal-go/pkg/metrics"
	"github.com/gin-gonic/gin"
)

// setupRouter builds the Gin engine with middleware, static files, web pages,
// metrics, health and all API route groups.
func setupRouter(deps *AppDeps) *gin.Engine {
	if deps.cfg.Server.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(middleware.RequestID())
	router.Use(middleware.StructuredLogging(deps.logger))
	router.Use(middleware.AuditClientInfo())
	router.Use(middleware.NewCORS(deps.cfg.Server.AllowedOrigins))
	router.Use(web.SecurityHeaders())
	router.Use(web.NoIndexForGatedPaths())
	router.Use(web.BadBotBlocker())
	router.Use(web.CSRFMiddleware())

	// Static files from go:embed
	web.RegisterStaticFiles(router, deps.logger)
	web.RegisterI18nFiles(router, deps.logger)

	// SEO / AI-discoverability endpoints (public), rate-limited per IP so an
	// abusive crawler cannot hammer them. Rate is env-configurable
	// (PUBLIC_RATE_LIMIT_RPS, default 5). Redis-backed when available.
	publicRPS := 5
	if v := os.Getenv("PUBLIC_RATE_LIMIT_RPS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			publicRPS = n
		}
	}
	publicRateLimit := middleware.NewIPRateLimiter(publicRPS, deps.database.Redis)
	seoGroup := router.Group("/")
	seoGroup.Use(publicRateLimit)
	deps.webHandler.RegisterSEORoutes(seoGroup)

	// Web page routes. Default: the embedded React SPA (webui) takes over all
	// non-API paths via NoRoute. Set LEGACY_WEB_UI=true to keep serving the
	// legacy server-rendered pages from internal/web instead.
	if os.Getenv("LEGACY_WEB_UI") == "true" {
		webGroup := router.Group("/")
		deps.webHandler.RegisterRoutes(webGroup, deps.auth)
	}

	// Metrics endpoint — protected by admin secret in header
	metricsSecret := os.Getenv("ADMIN_SECRET")
	router.GET("/metrics", func(c *gin.Context) {
		if metricsSecret == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "ADMIN_SECRET not configured"}})
			return
		}
		adminSecret := c.GetHeader("X-Admin-Secret")
		if subtle.ConstantTimeCompare([]byte(adminSecret), []byte(metricsSecret)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "Unauthorized"}})
			return
		}
		metrics.GetMetrics().Handler().ServeHTTP(c.Writer, c.Request)
	})

	// Health check with dependency status
	router.GET("/api/health", func(c *gin.Context) {
		status := "ok"
		depsMap := gin.H{}

		hctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()

		if err := deps.database.Pool.Ping(hctx); err != nil {
			depsMap["postgres"] = gin.H{"status": "down"}
			status = "degraded"
		} else {
			depsMap["postgres"] = gin.H{"status": "up"}
		}

		if deps.database.Redis != nil {
			if err := deps.database.Redis.Ping(hctx).Err(); err != nil {
				depsMap["redis"] = gin.H{"status": "down"}
				status = "degraded"
			} else {
				depsMap["redis"] = gin.H{"status": "up"}
			}
		} else {
			depsMap["redis"] = gin.H{"status": "not_configured"}
		}

		httpStatus := http.StatusOK
		if status == "degraded" {
			httpStatus = http.StatusServiceUnavailable
		}
		c.JSON(httpStatus, gin.H{"status": status, "dependencies": depsMap})
	})

	// API routes
	api := router.Group("/api")
	api.Use(middleware.SanitizeInput())

	deps.authRoutes.Register(api.Group("/auth"))
	// Enterprise OIDC SSO: one client (and its discovery/JWKS caches) is shared
	// by the login flow and the admin "test connection" button. The callback
	// sets the same browser cookie as the password login.
	ssoClient := sso.NewClient(sso.Options{})
	deps.authRoutes.SetSSOClient(ssoClient)
	deps.enterpriseRoutes.SetSSOClient(ssoClient)
	deps.authRoutes.SetCookieWriter(web.SetJWTWebCookie)
	deps.authRoutes.RegisterSSO(router)
	deps.webhookRoutes.Register(api.Group("/webhook"))
	deps.agentEnrollRoutes.Register(api.Group("/v1/agent"))

	deps.apiKeyRoutes.Register(api.Group("/apikeys"))
	deps.apiKeyRoutes.Register(api.Group("/keys"))

	deps.statusRoutes.Register(api.Group("/status"))
	upstreamStatusGroup := api.Group("/status")
	upstreamStatusGroup.Use(deps.auth.RequireAuth())
	deps.statusRoutes.RegisterUpstream(upstreamStatusGroup)
	deps.dashboardRoutes.Register(api)
	deps.usageRoutes.Register(api.Group("/usage"))
	deps.usageRoutes.RegisterAdmin(api.Group("/admin/usage"))

	// Rate limiter for admin and sensitive APIs
	adminRateLimitRPS := 10
	if v := os.Getenv("ADMIN_RATE_LIMIT_RPS"); v != "" {
		if i, err := strconv.Atoi(v); err == nil && i > 0 {
			adminRateLimitRPS = i
		}
	}
	rateLimitMiddleware := middleware.NewIPRateLimiter(adminRateLimitRPS, deps.database.Redis)

	adminGroup := api.Group("/admin")
	adminGroup.Use(rateLimitMiddleware)
	deps.adminRoutes.Register(adminGroup)

	// User-facing enterprise application routes (not under /admin)
	deps.enterpriseAppRoutes.Register(api.Group("/enterprise-application"))

	deps.syncRoutes.Register(api.Group("/admin"))
	deps.profileRoutes.Register(api.Group("/profile"))
	// MFA enrolment/management. Writes the browser cookie through the same
	// writer as the web login so a refreshed session looks identical.
	deps.mfaRoutes.SetCookieWriter(web.SetJWTWebCookie)
	deps.mfaRoutes.Register(api.Group("/profile/mfa"))
	deps.gdprRoutes.Register(api.Group("/gdpr"))
	deps.billingRoutes.Register(api.Group("/billing"))
	deps.orderRoutes.Register(api.Group("/orders"))
	deps.invoiceRoutes.Register(api.Group("/invoices"))
	deps.addressRoutes.Register(api.Group("/addresses"))
	deps.settingsRoutes.Register(api.Group("/admin/settings"))
	deps.voucherRoutes.Register(api)
	deps.subscriptionRoutes.Register(api)
	deps.auditRoutes.Register(api.Group("/admin/audit-logs"))
	deps.auditRoutes.RegisterVerify(api.Group("/admin/audit"))
	deps.announcementRoutes.Register(api)
	deps.notificationRoutes.Register(api)
	deps.ticketRoutes.Register(api.Group("/tickets"))
	adminTickets := api.Group("/admin")
	adminTickets.Use(deps.auth.RequireAuth())
	adminTickets.Use(deps.auth.RequireAdmin())
	deps.ticketRoutes.RegisterAdmin(adminTickets)
	deps.groupRoutes.Register(api.Group("/admin/groups"))
	deps.groupRoutes.RegisterPublic(api.Group("/groups"))
	deps.enterpriseRoutes.Register(api)
	deps.paymentRoutes.Register(api)
	deps.downstreamRoutes.Register(api.Group("/admin/downstream-keys"))
	deps.modelRoutes.Register(api)
	deps.channelHealthRoutes.Register(api.Group("/admin/channel-health"))
	deps.deviceRoutes.Register(api.Group("/devices"))
	deps.mgmtTokenRoutes.Register(api.Group("/management-tokens"))
	deps.mgmtTokenRoutes.RegisterConsumers(api.Group("/mgmt"))

	// 404 handler for unmatched routes. Registered LAST so it wins. Legacy
	// mode answers JSON; SPA mode falls back to the embedded React app.
	if os.Getenv("LEGACY_WEB_UI") == "true" {
		router.NoRoute(func(c *gin.Context) {
			c.JSON(404, gin.H{"error": gin.H{"message": "Not found"}})
		})
	} else {
		webui.Register(router)
	}

	return router
}

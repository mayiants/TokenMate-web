package routes

import (
	"strconv"

	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/internal/middleware"
	"github.com/mayiants/ai-portal-go/pkg/audit"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type SubscriptionRoutes struct {
	db     *db.DB
	auth   *middleware.Auth
	audit  *audit.Logger
	logger *zap.Logger
}

func NewSubscriptionRoutes(db *db.DB, auth *middleware.Auth, auditLog *audit.Logger, logger *zap.Logger) *SubscriptionRoutes {
	return &SubscriptionRoutes{db: db, auth: auth, audit: auditLog, logger: logger}
}

func (r *SubscriptionRoutes) Register(rg *gin.RouterGroup) {
	// Admin subscription management
	adminGroup := rg.Group("/admin/subscriptions")
	adminGroup.Use(r.auth.RequireAuth(), r.auth.RequireAdmin())

	adminGroup.GET("/plans", r.listPlans)
	adminGroup.GET("/plans/:id", r.getPlan)
	adminGroup.POST("/plans", r.createPlan)
	adminGroup.PUT("/plans/:id", r.updatePlan)
	adminGroup.POST("/plans/:id/toggle", r.togglePlan)
	adminGroup.DELETE("/plans/:id", r.deletePlan)
	adminGroup.GET("/users", r.listUserSubscriptions)
	adminGroup.PUT("/users/:id", r.updateUserSubscription)
	adminGroup.GET("/stats", r.getStats)

	// User subscription purchase flow (public)
	userGroup := rg.Group("/subscriptions")
	userGroup.GET("/plans", r.listPublicPlans)
	userGroup.Use(r.auth.RequireAuth())
	{
		userGroup.GET("/my", r.getMySubscriptions)
		userGroup.GET("/:id/usage", r.getSubscriptionUsage)
		userGroup.POST("/purchase", r.purchasePlan)
		userGroup.POST("/:id/cancel", r.cancelSubscription)
		userGroup.POST("/:id/renew", r.renewSubscription)
	}
}

// Helper functions
func stringInt(n int) string {
	return strconv.Itoa(n)
}

func coalesceString(a, b *string) string {
	if a != nil && *a != "" {
		return *a
	}
	if b != nil {
		return *b
	}
	return ""
}

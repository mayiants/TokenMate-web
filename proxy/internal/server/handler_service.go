package server

import (
	"context"

	"github.com/mayiants/ai-proxy-go/internal/auth"
	"github.com/mayiants/ai-proxy-go/internal/config"
	"github.com/mayiants/ai-proxy-go/internal/ratelimit"
	"github.com/mayiants/ai-proxy-go/internal/retention"
	"github.com/mayiants/ai-proxy-go/internal/retention/analytics"
	"github.com/mayiants/ai-proxy-go/pkg/billing"
	"github.com/mayiants/ai-proxy-go/pkg/keypool"
	"github.com/mayiants/ai-proxy-go/pkg/models"
	"github.com/mayiants/ai-proxy-go/pkg/plugin"
	"github.com/mayiants/ai-proxy-go/pkg/routing"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// HandlerService holds all HTTP handler dependencies in one injected facade.
// Production code constructs a single instance in cmd/server/main.go and passes
// it to server.New; tests can create a minimal instance and set only the fields
// they need.
type HandlerService struct {
	cfg                  *config.Config
	logger               *zap.Logger
	authMiddleware       *auth.Middleware
	rateLimitMiddleware  *ratelimit.Middleware
	keyPoolManager       *keypool.Manager
	modelRegistry        *models.ModelRegistry
	modelValidator       *models.ModelValidator
	usageBuffer          *UsageBuffer
	upstreamUsageBuffer  *UpstreamUsageBuffer
	providerHealthBuffer *ProviderHealthBuffer
	retentionStore       retention.Store
	analyticsScheduler   *analytics.Scheduler
	handlerDeps          *HandlerDependencies
	settleBuffer         *SettleBuffer
	serverID             string
	router               *gin.Engine
	pluginChain          *plugin.Chain
	redisClient          *redis.Client
	sceneMapping         *routing.SceneModelMapping
	budgetController     *routing.BudgetController
}

// NewHandlerService creates a new HandlerService with the required core dependencies.
func NewHandlerService(cfg *config.Config, logger *zap.Logger) *HandlerService {
	WireBreakerObservability(logger)
	return &HandlerService{
		cfg:    cfg,
		logger: logger,
	}
}

// SetRateLimitMiddleware injects the rate limit middleware.
func (s *HandlerService) SetRateLimitMiddleware(m *ratelimit.Middleware) {
	s.rateLimitMiddleware = m
}

// SetKeyPoolManager injects the key pool manager.
func (s *HandlerService) SetKeyPoolManager(m *keypool.Manager) {
	s.keyPoolManager = m
}

// SetModelRegistry injects the model registry.
func (s *HandlerService) SetModelRegistry(r *models.ModelRegistry) {
	s.modelRegistry = r
}

// SetModelValidator injects the model validator.
func (s *HandlerService) SetModelValidator(v *models.ModelValidator) {
	s.modelValidator = v
}

// SetUsageBuffer injects the usage buffer for batch reporting.
func (s *HandlerService) SetUsageBuffer(b *UsageBuffer) {
	s.usageBuffer = b
}

// SetUpstreamUsageBuffer injects the upstream usage buffer for provider reconciliation.
func (s *HandlerService) SetUpstreamUsageBuffer(b *UpstreamUsageBuffer) {
	s.upstreamUsageBuffer = b
}

// SetRedisClient stores the Redis client for health check probing.
func (s *HandlerService) SetRedisClient(rdb *redis.Client) {
	s.redisClient = rdb
}

// SetProviderHealthBuffer injects the provider health buffer.
func (s *HandlerService) SetProviderHealthBuffer(b *ProviderHealthBuffer) {
	s.providerHealthBuffer = b
}

// SetRetentionStore injects the retention store used to capture request/response payloads.
func (s *HandlerService) SetRetentionStore(store retention.Store) {
	s.retentionStore = store
}

// SetAnalyticsScheduler injects the retention analytics scheduler.
func (s *HandlerService) SetAnalyticsScheduler(sched *analytics.Scheduler) {
	s.analyticsScheduler = sched
}

// SetHandlerDependencies injects the shared handler dependencies.
func (s *HandlerService) SetHandlerDependencies(d *HandlerDependencies) {
	s.handlerDeps = d
}

// SetPluginChain injects the request/response rewrite plugin chain.
func (s *HandlerService) SetPluginChain(chain *plugin.Chain) {
	s.pluginChain = chain
}

// SetServerID sets the server instance ID for usage record attribution.
func (s *HandlerService) SetServerID(id string) {
	s.serverID = id
}

// SetSceneMapping injects the scene→model mapping for auto routing.
func (s *HandlerService) SetSceneMapping(m *routing.SceneModelMapping) {
	s.sceneMapping = m
}

// SetBudgetController injects the budget controller for cost management.
func (s *HandlerService) SetBudgetController(bc *routing.BudgetController) {
	s.budgetController = bc
}

// SetSettleBuffer injects the settle buffer for non-fire-and-forget settlement.
func (s *HandlerService) SetSettleBuffer(b *SettleBuffer) {
	s.settleBuffer = b
}

// Settle enqueues a settlement request to the settle buffer for reliable delivery.
// Handlers should call this instead of "go Billing.Settle(...)".
func (s *HandlerService) Settle(ctx context.Context, req billing.SettleRequest) {
	if s.settleBuffer != nil {
		s.settleBuffer.AddSettle(ctx, req)
	} else if s.handlerDeps != nil && s.handlerDeps.Billing != nil {
		s.handlerDeps.Billing.Settle(ctx, req)
	}
}

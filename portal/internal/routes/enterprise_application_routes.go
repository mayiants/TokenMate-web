package routes

import (
	"github.com/ai-proxy/ai-portal-go/internal/config"
	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/middleware"
	"github.com/ai-proxy/ai-portal-go/internal/storage"
	"github.com/ai-proxy/ai-portal-go/pkg/audit"
	"github.com/ai-proxy/ai-portal-go/pkg/mail"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// EnterpriseApplicationRoutes exposes the user-facing enterprise application
// APIs. These endpoints were previously mixed into AdminRoutes for historical
// reasons; they are now registered independently at /api/enterprise-application.
type EnterpriseApplicationRoutes struct {
	db      *db.DB
	cfg     *config.Config
	logger  *zap.Logger
	auth    *middleware.Auth
	audit   *audit.Logger
	storage storage.Provider
	mail    *mail.Sender
}

func NewEnterpriseApplicationRoutes(database *db.DB, cfg *config.Config, logger *zap.Logger, auth *middleware.Auth, auditLog *audit.Logger, storageProvider storage.Provider, mailSender *mail.Sender) *EnterpriseApplicationRoutes {
	return &EnterpriseApplicationRoutes{
		db:      database,
		cfg:     cfg,
		logger:  logger,
		auth:    auth,
		audit:   auditLog,
		storage: storageProvider,
		mail:    mailSender,
	}
}

// Register mounts user-facing enterprise application routes on the given group.
// The group should be created at /api/enterprise-application.
func (r *EnterpriseApplicationRoutes) Register(rg *gin.RouterGroup) {
	rg.Use(r.auth.RequireAuth())

	// Application lifecycle
	rg.POST("", r.submitEnterpriseApplication)
	rg.GET("", r.getMyEnterpriseApplication)
	rg.PUT("/:id", r.updateEnterpriseApplication)
	rg.POST("/:id/submit-for-review", r.submitEnterpriseApplicationForReview)

	// Documents
	rg.POST("/:id/documents", r.uploadEnterpriseApplicationDocument)
	rg.GET("/:id/documents", r.listMyEnterpriseApplicationDocuments)
	rg.DELETE("/:id/documents/:docId", r.deleteMyEnterpriseApplicationDocument)
}

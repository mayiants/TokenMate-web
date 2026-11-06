package routes

import (
	"github.com/ai-proxy/ai-portal-go/internal/config"
	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/middleware"
	"github.com/ai-proxy/ai-portal-go/pkg/audit"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type validateKeyResponse struct {
	Valid         bool        `json:"valid"`
	UserID        string      `json:"user_id"`
	APIKeyID      string      `json:"api_key_id"`
	Role          string      `json:"role"`
	AccessMode    string      `json:"access_mode"`
	AllowedModels interface{} `json:"allowed_models"`
	RateLimitRPM  int         `json:"rate_limit_rpm"`
	RateLimitTPM  int         `json:"rate_limit_tpm"`
	// NOTE: Balance is in micro-USD (1 USD = 1,000,000 micro-USD).
	Balance            float64     `json:"balance"`
	QuotaExceeded      bool        `json:"quota_exceeded"`
	QuotaWindow        string      `json:"quota_window"`
	GroupID            *string     `json:"group_id"`
	KeyType            *string     `json:"key_type"`
	BillingMode        *string     `json:"billing_mode"`
	CurrentModel       *string     `json:"current_model"`
	PricingRatio       float64     `json:"pricing_ratio"`
	GroupPriority      interface{} `json:"group_priority"`
	ModelRoutingMode   *string     `json:"model_routing_mode"`
	SelectedModels     interface{} `json:"selected_models"`
	SubRpmLimit        int         `json:"sub_rpm_limit"`
	SubTpmLimit        int         `json:"sub_tpm_limit"`
	SubRphLimit        int         `json:"sub_rph_limit"`
	SubTphLimit        int         `json:"sub_tph_limit"`
	SubConcurrentLimit int         `json:"sub_concurrent_limit"`
	SubRp5hLimit       int         `json:"sub_rp5h_limit"`
	SubRpdLimit        int         `json:"sub_rpd_limit"`
	SubRpwLimit        int         `json:"sub_rpw_limit"`
	SubTpdLimit        int         `json:"sub_tpd_limit"`
	SubTpwLimit        int         `json:"sub_tpw_limit"`
	SubTPMonthLimit    int         `json:"sub_tpmonth_limit"`
	// Group rate limits
	GroupRPM             int `json:"group_rpm"`
	GroupRPH             int `json:"group_rph"`
	GroupRP5H            int `json:"group_rp5h"`
	GroupRPW             int `json:"group_rpw"`
	GroupRPMonth         int `json:"group_rpmonth"`
	GroupTPM             int `json:"group_tpm"`
	GroupTPH             int `json:"group_tph"`
	GroupTPD             int `json:"group_tpd"`
	GroupTPW             int `json:"group_tpw"`
	GroupTPMonth         int `json:"group_tpmonth"`
	GroupConcurrentLimit int `json:"group_concurrent_limit"`
	// Dual-track routing
	RoutingTrack string `json:"routing_track"`
	// Data retention (consumed by the Proxy). EnterpriseID is "" for keys that
	// belong to no enterprise, in which case RetentionMode/Days are the platform
	// defaults. RetentionMode is off | metadata | standard.
	EnterpriseID  string `json:"enterprise_id"`
	RetentionMode string `json:"retention_mode"`
	RetentionDays int    `json:"retention_days"`
}

type APIKeyRoutes struct {
	db     *db.DB
	cfg    *config.Config
	logger *zap.Logger
	auth   *middleware.Auth
	audit  *audit.Logger
}

func NewAPIKeyRoutes(db *db.DB, cfg *config.Config, logger *zap.Logger, auth *middleware.Auth, auditLog *audit.Logger) *APIKeyRoutes {
	return &APIKeyRoutes{db: db, cfg: cfg, logger: logger, auth: auth, audit: auditLog}
}

func (r *APIKeyRoutes) Register(rg *gin.RouterGroup) {
	rg.GET("", r.auth.RequireAuth(), r.listKeys)
	rg.POST("", r.auth.RequireAuth(), r.createKey)
	rg.GET("/:id", r.auth.RequireAuth(), r.getKey)
	rg.GET("/:id/usage", r.auth.RequireAuth(), r.getKeyUsage)
	rg.GET("/:id/models", r.auth.RequireAuth(), r.getKeyModels)
	rg.PATCH("/:id", r.auth.RequireAuth(), r.updateKey)
	rg.DELETE("/:id", r.auth.RequireAuth(), r.deleteKey)
	rg.GET("/validate", r.validateKey)
	rg.GET("/pricing", r.keyPricing)
}

func containsString(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

package routes

import (
	"crypto/rand"
	"math/big"

	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/middleware"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func generateRandomCode(charset string, length int) string {
	b := make([]byte, length)
	max := big.NewInt(int64(len(charset)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			n = big.NewInt(0)
		}
		b[i] = charset[n.Int64()]
	}
	return string(b)
}

type VoucherRoutes struct {
	db     *db.DB
	auth   *middleware.Auth
	logger *zap.Logger
}

func NewVoucherRoutes(db *db.DB, auth *middleware.Auth, logger *zap.Logger) *VoucherRoutes {
	return &VoucherRoutes{db: db, auth: auth, logger: logger}
}

func (r *VoucherRoutes) Register(rg *gin.RouterGroup) {
	// User endpoints (use a local group so we don't attach auth middleware to the
	// shared top-level router group, which would unintentionally protect public
	// routes registered afterwards).
	auth := rg.Group("")
	auth.Use(r.auth.RequireAuth())
	auth.POST("/voucher/redeem", r.redeem)
	auth.POST("/voucher/generate", r.generate)
	auth.GET("/voucher/my-codes", r.myCodes)
	auth.GET("/voucher/records", r.myRecords)

	// Admin endpoints
	admin := rg.Group("/admin/vouchers")
	admin.Use(r.auth.RequireAuth(), r.auth.RequireAdmin())
	admin.GET("", r.list)
	admin.POST("", r.create)
	admin.PATCH("/:id", r.update)
	admin.POST("/:id/toggle", r.toggle)
	admin.DELETE("/:id", r.delete)
	admin.GET("/records", r.listRecords)
	admin.GET("/stats", r.stats)
	admin.GET("/welcome", r.getWelcome)
	admin.POST("/welcome", r.saveWelcome)
	admin.GET("/settings", r.getSettings)
	admin.POST("/settings", r.saveSettings)
	admin.GET("/plans", r.listPlans)
}

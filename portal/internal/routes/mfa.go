package routes

import (
	"errors"
	"net/http"
	"strings"

	"github.com/mayiants/ai-portal-go/internal/config"
	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/internal/middleware"
	"github.com/mayiants/ai-portal-go/internal/session"
	"github.com/mayiants/ai-portal-go/pkg/audit"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// MFARoutes serves the self-service MFA management API under
// /api/profile/mfa. The handlers are thin: all rules (limiter, replay
// protection, audit, token_version bump) live in MFAService.
type MFARoutes struct {
	db     *db.DB
	auth   *middleware.Auth
	logger *zap.Logger
	mfa    *MFAService
	issuer *session.Issuer
}

// NewMFARoutes wires the MFA management API.
func NewMFARoutes(database *db.DB, cfg *config.Config, auth *middleware.Auth, logger *zap.Logger, auditLog *audit.Logger) *MFARoutes {
	encKey := ""
	if cfg != nil {
		encKey = cfg.Auth.EncryptionKey
	}
	var issuer *session.Issuer
	if auth != nil {
		issuer = session.NewIssuer(database, auth.JWTManager())
	} else {
		issuer = session.NewIssuer(database, nil)
	}
	return &MFARoutes{
		db: database, auth: auth, logger: logger,
		mfa:    NewMFAService(database, encKey, logger, auditLog),
		issuer: issuer,
	}
}

// SetCookieWriter lets the web layer register the browser cookie writer so the
// refreshed session cookie is set after enable/disable.
func (r *MFARoutes) SetCookieWriter(w session.CookieWriter) { r.issuer.SetCookieWriter(w) }

// Register mounts the endpoints. These paths are exempt from the MFA
// enrolment gate (see middleware.mfaGateExemptAPI) - they are how a gated user
// enrols.
func (r *MFARoutes) Register(rg *gin.RouterGroup) {
	rg.Use(r.auth.RequireAuth())
	rg.GET("/status", r.status)
	rg.POST("/setup", r.setup)
	rg.POST("/enable", r.enable)
	rg.POST("/disable", r.disable)
	rg.POST("/recovery-codes/regenerate", r.regenerate)
}

func mfaFail(c *gin.Context, status int, code, key string) {
	body := gin.H{"message": T(key, c)}
	if code != "" {
		body["code"] = code
	}
	c.JSON(status, gin.H{"error": body})
}

// respondMFAError maps service errors to HTTP. Wrong code, replay, used
// recovery code and lockout all share one 400 message so nothing is leaked.
func (r *MFARoutes) respondMFAError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrMFAInvalid):
		mfaFail(c, http.StatusBadRequest, "invalid_code", "error.mfa_invalid_code")
	case errors.Is(err, ErrMFAPasswordIncorrect):
		mfaFail(c, http.StatusBadRequest, "invalid_password", "error.mfa_password_incorrect")
	case errors.Is(err, ErrMFAAlreadyEnabled):
		mfaFail(c, http.StatusConflict, "already_enabled", "error.mfa_already_enabled")
	case errors.Is(err, ErrMFANotEnabled):
		mfaFail(c, http.StatusConflict, "not_enabled", "error.mfa_not_enabled")
	case errors.Is(err, ErrMFANoPendingSetup):
		mfaFail(c, http.StatusConflict, "setup_required", "error.mfa_setup_required")
	case errors.Is(err, ErrMFARequiredByPolicy):
		mfaFail(c, http.StatusForbidden, "mfa_required_by_policy", "error.mfa_required_by_policy")
	case errors.Is(err, ErrMFAUnavailable):
		mfaFail(c, http.StatusServiceUnavailable, "", "error.service_unavailable")
	default:
		r.logger.Error("MFA operation failed", zap.Error(err), zap.String("user_id", c.GetString("user_id")))
		mfaFail(c, http.StatusInternalServerError, "", "error.mfa_operation_failed")
	}
}

// refreshSession issues a new session after token_version was bumped, so the
// device that performed the change stays signed in while all others are
// revoked. The cookie is only touched when the request itself used the cookie.
func (r *MFARoutes) refreshSession(c *gin.Context) string {
	viaBearer := strings.HasPrefix(c.GetHeader("Authorization"), "Bearer ")
	// The second factor was just verified in this request, hence MethodMFA.
	res, err := r.issuer.Issue(c, c.GetString("user_id"), session.MethodMFA, session.Options{SetCookie: !viaBearer})
	if err != nil {
		r.logger.Warn("Could not refresh session after MFA change", zap.Error(err))
		return ""
	}
	return res.Token
}

// GET /api/profile/mfa/status
func (r *MFARoutes) status(c *gin.Context) {
	st, err := r.mfa.Status(c.Request.Context(), c.GetString("user_id"))
	if err != nil {
		r.respondMFAError(c, err)
		return
	}
	c.JSON(http.StatusOK, st)
}

// POST /api/profile/mfa/setup
func (r *MFARoutes) setup(c *gin.Context) {
	secret, uri, err := r.mfa.BeginSetup(c.Request.Context(), c.GetString("user_id"))
	if err != nil {
		r.respondMFAError(c, err)
		return
	}
	// The secret is shown once here; afterwards only the encrypted copy exists.
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"secret": secret, "otpauth_uri": uri, "issuer": mfaIssuer,
		"algorithm": "SHA1", "digits": 6, "period": 30,
	})
}

// POST /api/profile/mfa/enable {code}
func (r *MFARoutes) enable(c *gin.Context) {
	var req struct {
		Code string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		mfaFail(c, http.StatusBadRequest, "invalid_request", "error.mfa_code_required")
		return
	}
	codes, err := r.mfa.ConfirmEnable(c.Request.Context(), c.GetString("user_id"), req.Code)
	if err != nil {
		r.respondMFAError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"success": true, "recovery_codes": codes, "token": r.refreshSession(c)})
}

// POST /api/profile/mfa/disable {password, code | recovery_code}
func (r *MFARoutes) disable(c *gin.Context) {
	var req struct {
		Password     string `json:"password" binding:"required"`
		Code         string `json:"code"`
		RecoveryCode string `json:"recovery_code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || (req.Code == "" && req.RecoveryCode == "") {
		mfaFail(c, http.StatusBadRequest, "invalid_request", "error.mfa_password_and_code_required")
		return
	}
	if err := r.mfa.Disable(c.Request.Context(), c.GetString("user_id"), req.Password,
		SecondFactor{Code: req.Code, RecoveryCode: req.RecoveryCode}); err != nil {
		r.respondMFAError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "token": r.refreshSession(c)})
}

// POST /api/profile/mfa/recovery-codes/regenerate {password, code}
func (r *MFARoutes) regenerate(c *gin.Context) {
	var req struct {
		Password string `json:"password" binding:"required"`
		Code     string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		mfaFail(c, http.StatusBadRequest, "invalid_request", "error.mfa_password_and_code_required")
		return
	}
	codes, err := r.mfa.RegenerateRecoveryCodes(c.Request.Context(), c.GetString("user_id"), req.Password, req.Code)
	if err != nil {
		r.respondMFAError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"success": true, "recovery_codes": codes})
}

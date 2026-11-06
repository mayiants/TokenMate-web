package routes

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/config"
	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/middleware"
	"github.com/ai-proxy/ai-portal-go/internal/services"
	"github.com/ai-proxy/ai-portal-go/internal/session"
	"github.com/ai-proxy/ai-portal-go/internal/sso"
	"github.com/ai-proxy/ai-portal-go/pkg/audit"
	"github.com/ai-proxy/ai-portal-go/pkg/jwt"
	"github.com/ai-proxy/ai-portal-go/pkg/mail"
	"github.com/ai-proxy/ai-portal-go/pkg/metrics"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

type AuthRoutes struct {
	db        *db.DB
	cfg       *config.Config
	logger    *zap.Logger
	auth      *middleware.Auth
	audit     *audit.Logger
	mail      *mail.Sender
	rewardSvc *services.RewardService
	issuer    *session.Issuer
	mfa       *MFAService
	ssoClient *sso.Client // lazily created, see sso_login.go
}

func NewAuthRoutes(db *db.DB, cfg *config.Config, logger *zap.Logger, auth *middleware.Auth, auditLog *audit.Logger, mailSender *mail.Sender, rewardSvc *services.RewardService) *AuthRoutes {
	encKey := ""
	if cfg != nil {
		encKey = cfg.Auth.EncryptionKey
	}
	var mgr *jwt.Manager
	if auth != nil {
		mgr = auth.JWTManager()
	}
	return &AuthRoutes{
		db: db, cfg: cfg, logger: logger, auth: auth, audit: auditLog, mail: mailSender, rewardSvc: rewardSvc,
		issuer: session.NewIssuer(db, mgr),
		mfa:    NewMFAService(db, encKey, logger, auditLog),
	}
}

// userAuthInfo holds the result of a successful authentication or registration.
// It is returned by the shared helpers used by both the JSON API and web form handlers.
type userAuthInfo struct {
	UserID       string
	Name         string
	Email        string
	Role         string
	TokenVersion int
}

// RegisterRequest is the validated input for account creation. It is shared
// between the JSON API register endpoint and the web registration form.
type RegisterRequest struct {
	Name           string
	Email          string
	Password       string
	Code           string
	Phone          string
	Company        string
	InvitationCode string
}

// AuthenticateUser validates credentials and returns authenticated user info.
// It is the single implementation used by both POST /api/auth/login and the
// web login form, so authentication logic cannot diverge between the two paths.
func (r *AuthRoutes) AuthenticateUser(ctx context.Context, email, password string) (*userAuthInfo, error) {
	var userID, name, storedEmail, passwordHash, role, status string
	var erasureRequestedAt *time.Time
	var tokenVersion int
	err := r.db.QueryRow(ctx,
		`SELECT id::text, name, email, password_hash, role, COALESCE(status, 'active'), gdpr_erasure_requested_at, COALESCE(token_version, 0)
		 FROM users WHERE email = $1 AND deleted_at IS NULL`,
		strings.ToLower(email),
	).Scan(&userID, &name, &storedEmail, &passwordHash, &role, &status, &erasureRequestedAt, &tokenVersion)
	if err != nil {
		// Dummy bcrypt comparison to equalize timing with the existing-user path.
		_ = bcrypt.CompareHashAndPassword(
			[]byte("$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"),
			[]byte(password))
		return nil, errors.New("invalid email or password")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)); err != nil {
		return nil, errors.New("invalid email or password")
	}

	if status == "suspended" {
		return nil, errors.New("account suspended")
	}

	if erasureRequestedAt != nil {
		return nil, errors.New("account pending deletion")
	}

	// Checked only after the password is right, so it reveals nothing to someone
	// guessing credentials. Admins are exempt (break-glass), see ssoEnforcedFor.
	if enforced, err := r.ssoEnforcedFor(ctx, userID); err != nil {
		// Fail closed: if the policy cannot be read, do not open a password session.
		r.logger.Error("Failed to evaluate SSO enforcement", zap.Error(err), zap.String("user_id", userID))
		return nil, errors.New("invalid email or password")
	} else if enforced {
		return nil, ErrSSORequired
	}

	// Non-critical: update login timestamp. Use Exec (not MustExec) so a transient DB error doesn't break login.
	if _, err := r.db.Pool.Exec(ctx, `UPDATE users SET last_login_at = NOW(), updated_at = NOW() WHERE id = $1::uuid`, userID); err != nil {
		r.logger.Warn("Failed to update login timestamp", zap.Error(err), zap.String("user_id", userID))
	}

	return &userAuthInfo{
		UserID:       userID,
		Name:         name,
		Email:        storedEmail,
		Role:         role,
		TokenVersion: tokenVersion,
	}, nil
}

// CreateAccount creates a new user after verifying the registration code.
// It is the single implementation used by both POST /api/auth/register and the
// web registration form.
func (r *AuthRoutes) CreateAccount(ctx context.Context, req RegisterRequest) (*userAuthInfo, error) {
	email := strings.ToLower(strings.TrimSpace(req.Email))

	if r.db.Redis == nil {
		return nil, errors.New("registration unavailable: Redis not configured")
	}

	// Verify the code from Redis first.
	key := "verification:register:" + email
	storedCode, err := r.db.Redis.Get(ctx, key).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, errors.New("registration unavailable")
	}
	if err != nil || storedCode != req.Code {
		r.recordCodeFailure(ctx, "register", email, key)
		return nil, errors.New("invalid or expired verification code")
	}

	if err := validatePasswordStrength(req.Password); err != nil {
		return nil, err
	}

	// Check if email already exists
	var existingCount int
	if err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM users WHERE email = $1 AND deleted_at IS NULL`, email,
	).Scan(&existingCount); err != nil {
		return nil, errors.New("internal error")
	}
	if existingCount > 0 {
		return nil, errors.New("email already registered")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, errors.New("internal error")
	}

	name := req.Name
	if name == "" {
		name = strings.Split(email, "@")[0]
	}

	// Get default general group for new users
	var defaultGroupID *string
	if err := r.db.QueryRow(ctx,
		`SELECT id::text FROM user_groups WHERE group_type = 'general' AND status = 'active' ORDER BY created_at LIMIT 1`,
	).Scan(&defaultGroupID); err != nil {
		return nil, errors.New("internal error")
	}

	var userID string
	err = r.db.QueryRow(ctx,
		`INSERT INTO users (name, email, password_hash, phone, company, group_id, preferred_currency)
		 VALUES ($1, $2, $3, $4, $5, $6::uuid, 'USD')
		 RETURNING id::text`,
		name, email, string(hash), req.Phone, req.Company, defaultGroupID,
	).Scan(&userID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, errors.New("email already registered")
		}
		return nil, errors.New("registration failed")
	}

	// Delete verification code and its failure counter after successful registration
	_ = r.db.Redis.Del(ctx, key, codeFailKey("register", email)).Err()

	return &userAuthInfo{
		UserID: userID,
		Name:   name,
		Email:  email,
		Role:   "user",
	}, nil
}

func (r *AuthRoutes) Register(rg *gin.RouterGroup) {
	rg.GET("/password-requirements", r.getPasswordRequirements)
	rg.POST("/send-code", r.sendCode)
	rg.POST("/register", r.bruteForceProtection(), r.register)
	rg.POST("/login", r.bruteForceProtection(), r.login)
	// Second step of a login for users with TOTP enabled; see auth_session.go.
	rg.POST("/login/mfa", r.bruteForceProtection(), r.loginMFA)
	// Home-realm discovery for "Sign in with SSO"; the browser flow itself is
	// registered by RegisterSSO.
	rg.POST("/sso/discover", r.ssoDiscover)
	rg.GET("/me", r.auth.RequireAuth(), r.getMe)
	rg.POST("/logout", r.auth.RequireAuth(), r.logout)
	rg.POST("/change-password", r.auth.RequireAuth(), r.changePassword)
	rg.POST("/forgot-password", r.bruteForceProtection(), r.forgotPassword)
	rg.POST("/reset-password", r.bruteForceProtection(), r.resetPassword)
}

// bruteForceProtection limits login/register attempts per IP (5 per 15 min).
// Fails open when Redis is unavailable: brute-force protection is degraded
// but auth remains operational, which is safer than locking out all users.
func (r *AuthRoutes) bruteForceProtection() gin.HandlerFunc {
	return func(c *gin.Context) {
		if r.db.Redis == nil {
			// No Redis configured — skip rate limiting and proceed.
			r.logger.Warn("Brute force protection disabled: Redis not configured")
			c.Next()
			return
		}

		ip := c.ClientIP()
		key := "auth_brute:" + ip

		count, err := r.db.Redis.Get(c.Request.Context(), key).Int()
		if err != nil && !errors.Is(err, redis.Nil) {
			// Redis error — fail open to keep auth available; log for alerting.
			r.logger.Warn("Redis error in brute force protection, failing open", zap.Error(err))
			c.Next()
			return
		}
		// redis.Nil means key does not exist -> 0 attempts
		if count >= 5 {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": gin.H{"message": "Too many attempts. Please try again in 15 minutes."},
			})
			return
		}

		c.Next()

		// After handler runs, increment counter if response was 401/403 (failed auth)
		if c.Writer.Status() == http.StatusUnauthorized || c.Writer.Status() == http.StatusForbidden || c.Writer.Status() == http.StatusBadRequest {
			r.db.Redis.Incr(c.Request.Context(), key)
			r.db.Redis.Expire(c.Request.Context(), key, 15*time.Minute)
		}
	}
}

// maxCodeFailures is the number of consecutive wrong-code attempts after
// which a verification code is invalidated. A 6-digit code (10⁶ space, 10min
// TTL) cannot rely on the IP-based bruteForceProtection alone — an attacker
// rotating IPs could otherwise keep guessing until the code expires.
const maxCodeFailures = 5

// codeFailKey returns the Redis key counting wrong-code attempts.
func codeFailKey(purpose, email string) string {
	return "verification:fail:" + purpose + ":" + email
}

// recordCodeFailure counts a wrong-code attempt and invalidates the code
// (and resets the counter) once maxCodeFailures is reached.
func (r *AuthRoutes) recordCodeFailure(ctx context.Context, purpose, email, codeKey string) {
	if r.db.Redis == nil {
		return
	}
	failKey := codeFailKey(purpose, email)
	n, err := r.db.Redis.Incr(ctx, failKey).Result()
	if err != nil {
		return
	}
	if n == 1 {
		r.db.Redis.Expire(ctx, failKey, 10*time.Minute)
	}
	if n >= maxCodeFailures {
		_ = r.db.Redis.Del(ctx, codeKey, failKey).Err()
		r.logger.Warn("Verification code invalidated after too many wrong attempts",
			zap.String("purpose", purpose), zap.String("email", email))
	}
}

func (r *AuthRoutes) getPasswordRequirements(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"minLength": 8,
		"requirements": []gin.H{
			{"key": "length", "label": T("password.length", c), "regex": ".{8,}"},
			{"key": "uppercase", "label": T("password.uppercase", c), "regex": "[A-Z]"},
			{"key": "lowercase", "label": T("password.lowercase", c), "regex": "[a-z]"},
			{"key": "number", "label": T("password.number", c), "regex": "[0-9]"},
			{"key": "special", "label": T("password.special", c), "regex": "[!@#$%^&*()_+\\-=\\[\\]{};':\"\\\\|,.<>/?]"},
		},
	})
}

func (r *AuthRoutes) sendCode(c *gin.Context) {
	var req struct {
		Email string `json:"email" binding:"required,email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Valid email is required"}})
		return
	}

	if r.db.Redis == nil {
		r.logger.Error("sendCode unavailable: Redis not configured")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "Service temporarily unavailable"}})
		return
	}

	email := strings.ToLower(strings.TrimSpace(req.Email))

	// Rate limit: max 3 requests per email per hour (atomic INCR-first to prevent TOCTOU)
	rateKey := "email_rate:" + email
	newCount, incrErr := r.db.Redis.Incr(c.Request.Context(), rateKey).Result()
	if incrErr != nil {
		r.logger.Warn("Redis error in sendCode rate limit, skipping rate check", zap.Error(incrErr))
	} else {
		if newCount == 1 {
			r.db.Redis.Expire(c.Request.Context(), rateKey, 1*time.Hour)
		}
		if newCount > 3 {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": gin.H{"message": "Too many requests. Please try again later."}})
			return
		}
	}

	// Per-IP rate limit: the per-email cap alone lets an attacker cycle through
	// many addresses (email bombing / SMTP cost abuse).
	ipKey := "email_rate:ip:" + c.ClientIP()
	ipCount, ipErr := r.db.Redis.Incr(c.Request.Context(), ipKey).Result()
	if ipErr != nil {
		r.logger.Warn("Redis error in sendCode IP rate limit, skipping rate check", zap.Error(ipErr))
	} else {
		if ipCount == 1 {
			r.db.Redis.Expire(c.Request.Context(), ipKey, 1*time.Hour)
		}
		if ipCount > 10 {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": gin.H{"message": "Too many requests. Please try again later."}})
			return
		}
	}

	code := generateCode()

	// Use SETNX to avoid overwriting an existing valid code
	// (race-safe with the rate check above).
	key := "verification:register:" + email
	ok, err := r.db.Redis.SetNX(c.Request.Context(), key, code, 10*time.Minute).Result()
	if err != nil {
		r.logger.Warn("Failed to store verification code in Redis", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to generate code"}})
		return
	}
	if !ok {
		// Concurrent request already set the code — no-op, rate already consumed.
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "Verification code sent"})
		return
	}

	// Send email via SMTP
	if r.mail.IsConfigured() {
		if err := r.mail.SendVerificationCode(email, code, GetLocale(c)); err != nil {
			r.logger.Warn("Failed to send verification email", zap.Error(err), zap.String("email", email))
			// Don't fail the request - user can still see code in logs during dev
		}
	} else {
		r.logger.Debug("SMTP not configured, verification code generated",
			zap.String("email", email),
		)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Verification code sent",
	})
}

func (r *AuthRoutes) register(c *gin.Context) {
	var req struct {
		Name           string `json:"name"`
		Email          string `json:"email" binding:"required,email"`
		Password       string `json:"password" binding:"required,min=8"`
		Code           string `json:"code" binding:"required"`
		Phone          string `json:"phone"`
		Company        string `json:"company"`
		InvitationCode string `json:"invitation_code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Email, password, and verification code are required"}})
		return
	}

	info, err := r.CreateAccount(c.Request.Context(), RegisterRequest{
		Name:           req.Name,
		Email:          req.Email,
		Password:       req.Password,
		Code:           req.Code,
		Phone:          req.Phone,
		Company:        req.Company,
		InvitationCode: req.InvitationCode,
	})
	if err != nil {
		msg := err.Error()
		if msg == "email already registered" {
			c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "Email already registered"}})
			return
		}
		if msg == "invalid or expired verification code" {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid or expired verification code"}})
			return
		}
		if strings.HasPrefix(msg, "registration unavailable") {
			r.logger.Error(msg)
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "Service temporarily unavailable"}})
			return
		}
		if msg == "internal error" || msg == "registration failed" {
			r.logger.Error("Register failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Registration failed"}})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": msg}})
		return
	}

	// Best-effort active user gauge: increment on successful registration.
	metrics.GetMetrics().ActiveUsers.Add(1)

	// Apply welcome bonus and process invitation code.
	r.rewardSvc.ApplyWelcomeBonus(c.Request.Context(), info.UserID, info.Email)
	if req.InvitationCode != "" {
		r.rewardSvc.ProcessInvitationCode(c.Request.Context(), info.UserID, req.InvitationCode)
	}

	r.audit.Log(c.Request.Context(), info.UserID, info.Email, "auth", "user_registered", "info", "New user registered")

	// Auto-login goes through the shared session path like every other login;
	// a brand-new account has no MFA so this yields a full session.
	out, err := r.FinishPasswordLogin(c, info.UserID, session.Options{})
	if err != nil || out.Session == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Token generation failed"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token": out.Session.Token,
		"user":  gin.H{"id": info.UserID, "name": info.Name, "email": info.Email, "role": info.Role},
	})
}

func (r *AuthRoutes) login(c *gin.Context) {
	metrics.GetMetrics().AuthAttempts.Add(1)

	var req struct {
		Email    string `json:"email" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Email and password are required"}})
		return
	}

	info, err := r.AuthenticateUser(c.Request.Context(), req.Email, req.Password)
	if err != nil {
		metrics.GetMetrics().AuthFailures.Add(1)
		msg := err.Error()
		if msg == "account suspended" {
			r.audit.Log(c.Request.Context(), "", req.Email, "auth", "login_failed", "warning", "Login failed: account suspended")
			c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": T("error.account_suspended", c)}})
			return
		}
		if errors.Is(err, ErrSSORequired) {
			r.audit.Log(c.Request.Context(), "", req.Email, "auth", "login_failed", "warning", "Login failed: SSO required for this enterprise")
			c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": T("error.sso_required", c), "code": "sso_required"}})
			return
		}
		if msg == "account pending deletion" {
			r.audit.Log(c.Request.Context(), "", req.Email, "auth", "login_failed", "warning", "Login failed: account pending deletion")
			c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": T("error.account_pending_deletion", c)}})
			return
		}
		r.audit.Log(c.Request.Context(), "", req.Email, "auth", "login_failed", "warning",
			fmt.Sprintf("Login failed: %s (email: %s)", msg, req.Email))
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "Invalid email or password"}})
		return
	}

	out, err := r.FinishPasswordLogin(c, info.UserID, session.Options{})
	if errors.Is(err, ErrSSORequired) {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": T("error.sso_required", c), "code": "sso_required"}})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Token generation failed"}})
		return
	}
	if out.MFAToken != "" {
		// Password was right but a second factor is required: no token yet.
		r.audit.Log(c.Request.Context(), info.UserID, info.Email, "auth", "mfa_challenge_issued", "info", "Password accepted; MFA challenge issued")
		c.JSON(http.StatusOK, gin.H{
			"mfa_required": true,
			"mfa_token":    out.MFAToken,
			"expires_in":   int(session.ChallengeTTL.Seconds()),
		})
		return
	}

	r.audit.Log(c.Request.Context(), info.UserID, info.Email, "auth", "user_login", "info", "User logged in")

	c.JSON(http.StatusOK, gin.H{
		"token": out.Session.Token,
		"user":  gin.H{"id": info.UserID, "name": info.Name, "email": info.Email, "role": info.Role},
	})
}

func (r *AuthRoutes) getMe(c *gin.Context) {
	userID := c.GetString("user_id")

	var id string
	var name, email, role string
	var balance int64
	var createdAt time.Time
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT id::text, name, email, role, balance, created_at FROM users WHERE id = $1::uuid AND deleted_at IS NULL`,
		userID,
	).Scan(&id, &name, &email, &role, &balance, &createdAt)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "User not found"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"user": gin.H{
			"id":         id,
			"name":       name,
			"email":      email,
			"role":       role,
			"balance":    balance,
			"created_at": createdAt,
		},
	})
}

func (r *AuthRoutes) logout(c *gin.Context) {
	userID := c.GetString("user_id")
	userEmail := c.GetString("email")
	if r.audit != nil {
		r.audit.Log(c.Request.Context(), userID, userEmail, "auth", "user_logout", "info", "User logged out")
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *AuthRoutes) changePassword(c *gin.Context) {
	var req struct {
		OldPassword string `json:"old_password" binding:"required"`
		NewPassword string `json:"new_password" binding:"required,min=8"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Old and new password required"}})
		return
	}

	userID := c.GetString("user_id")

	// Validate new password strength server-side
	if err := validatePasswordStrength(req.NewPassword); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}

	var passwordHash string
	err := r.db.QueryRow(c.Request.Context(), `SELECT password_hash FROM users WHERE id = $1::uuid`, userID).Scan(&passwordHash)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "User not found"}})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.OldPassword)); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Current password is incorrect"}})
		return
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		r.logger.Error("bcrypt hash generation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Password processing failed"}})
		return
	}
	if err := r.db.Exec(c.Request.Context(), `UPDATE users SET password_hash = $1, token_version = COALESCE(token_version, 0) + 1, updated_at = NOW() WHERE id = $2::uuid`, string(newHash), userID); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Password changed"})
}

func (r *AuthRoutes) forgotPassword(c *gin.Context) {
	var req struct {
		Email string `json:"email" binding:"required,email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Valid email is required"}})
		return
	}

	email := strings.ToLower(strings.TrimSpace(req.Email))

	if r.db.Redis == nil {
		// Reset codes cannot be issued without Redis; return the generic
		// success message anyway to avoid email enumeration.
		r.logger.Error("forgotPassword unavailable: Redis not configured")
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "If the email exists, a reset link has been sent"})
		return
	}

	// Rate limit: max 3 reset requests per email per hour (atomic INCR-first to prevent TOCTOU)
	rateKey := "email_rate:reset:" + email
	newCount, incrErr := r.db.Redis.Incr(c.Request.Context(), rateKey).Result()
	if incrErr != nil {
		r.logger.Warn("Redis error in forgotPassword rate limit, skipping rate check", zap.Error(incrErr))
	} else {
		if newCount == 1 {
			r.db.Redis.Expire(c.Request.Context(), rateKey, 1*time.Hour)
		}
		if newCount > 3 {
			// Return 200 with generic message to avoid email enumeration
			c.JSON(http.StatusOK, gin.H{"success": true, "message": "If the email exists, a reset link has been sent"})
			return
		}
	}

	// Always return success to prevent email enumeration.
	// Check user exists silently; generate code and send only if found.
	var exists bool
	if r.db.Pool != nil {
		_ = r.db.Pool.QueryRow(c.Request.Context(),
			`SELECT EXISTS(SELECT 1 FROM users WHERE email=$1 AND deleted_at IS NULL)`, email,
		).Scan(&exists)
	}

	if exists {
		// Reuse the verification code flow: store in Redis, send via SMTP.
		code := generateCode()
		key := "verification:reset:" + email
		if err := r.db.Redis.Set(c.Request.Context(), key, code, 10*time.Minute).Err(); err != nil {
			r.logger.Warn("Failed to store password reset code", zap.Error(err))
		} else if r.mail.IsConfigured() {
			if err := r.mail.SendVerificationCode(email, code, GetLocale(c)); err != nil {
				r.logger.Warn("Failed to send password reset email", zap.Error(err), zap.String("email", email))
			}
		} else {
			r.logger.Debug("SMTP not configured, reset code generated",
				zap.String("email", email))
		}
	}

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), "", email, "security", "password_reset_requested", "info",
			fmt.Sprintf("Password reset requested for %s", email))
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "If the email exists, a reset link has been sent"})
}

func (r *AuthRoutes) resetPassword(c *gin.Context) {
	var req struct {
		Email    string `json:"email" binding:"required,email"`
		Code     string `json:"code" binding:"required"`
		Password string `json:"password" binding:"required,min=8"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "All fields required"}})
		return
	}

	email := strings.ToLower(strings.TrimSpace(req.Email))

	if r.db.Redis == nil {
		r.logger.Error("resetPassword unavailable: Redis not configured")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "Service temporarily unavailable"}})
		return
	}

	key := "verification:reset:" + email
	stored, err := r.db.Redis.Get(c.Request.Context(), key).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		r.logger.Error("Redis error in resetPassword code check", zap.Error(err))
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "Service temporarily unavailable"}})
		return
	}
	if err != nil || stored != req.Code {
		r.recordCodeFailure(c.Request.Context(), "reset", email, key)
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid or expired code"}})
		return
	}

	// Validate password strength server-side
	if err := validatePasswordStrength(req.Password); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		r.logger.Error("bcrypt hash generation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Password processing failed"}})
		return
	}
	if err := r.db.Exec(c.Request.Context(), `UPDATE users SET password_hash = $1, token_version = COALESCE(token_version, 0) + 1, updated_at = NOW() WHERE email = $2 AND deleted_at IS NULL`, string(hash), email); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
		return
	}
	_ = r.db.Redis.Del(c.Request.Context(), key, codeFailKey("reset", email)).Err()

	// Send confirmation email
	if r.mail.IsConfigured() {
		_ = r.mail.SendPasswordReset(email, GetLocale(c))
	}

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), "", email, "security", "password_reset_completed", "warning",
			fmt.Sprintf("Password reset completed for %s", email))
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Password reset successfully"})
}

// validatePasswordStrength checks password against declared requirements
func validatePasswordStrength(password string) error {
	if len(password) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	hasUpper := false
	hasLower := false
	hasNumber := false
	hasSpecial := false
	for _, ch := range password {
		switch {
		case ch >= 'A' && ch <= 'Z':
			hasUpper = true
		case ch >= 'a' && ch <= 'z':
			hasLower = true
		case ch >= '0' && ch <= '9':
			hasNumber = true
		case strings.ContainsRune("!@#$%^&*()_+-=[]{};':\"\\|,.<>/?", ch):
			hasSpecial = true
		}
	}
	if !hasUpper || !hasLower || !hasNumber || !hasSpecial {
		return fmt.Errorf("password must contain uppercase, lowercase, number and special character")
	}
	return nil
}

func generateCode() string {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		// crypto/rand failure is a system-level error; fail closed rather than use predictable fallback
		panic(fmt.Sprintf("crypto/rand unavailable: %v", err))
	}
	return fmt.Sprintf("%06d", n.Int64())
}

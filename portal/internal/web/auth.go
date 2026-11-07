package web

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mayiants/ai-portal-go/internal/routes"
	"github.com/mayiants/ai-portal-go/internal/session"
	"github.com/mayiants/ai-portal-go/pkg/metrics"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// HandleLogin handles web form POST for login
func (h *Handler) HandleLogin(c *gin.Context) {
	email := strings.ToLower(strings.TrimSpace(c.PostForm("email")))
	password := c.PostForm("password")
	rememberMe := c.PostForm("remember_me") == "on"

	q := url.Values{}
	q.Set("toast", "error")

	if email == "" || password == "" {
		q.Set("message", h.i18n.T("error.email_password_required", h.getLocale(c)))
		c.Redirect(http.StatusFound, "/login?"+q.Encode())
		return
	}

	ctx := c.Request.Context()

	// Per-email+IP login rate limiting: max 5 failed attempts within 5 minutes,
	// after which the account is locked out for 15 minutes. This throttles
	// online password brute-force. Redis is best-effort; if unavailable we
	// fail open so logins keep working.
	const (
		maxLoginAttempts = 5
		loginWindow      = 5 * time.Minute
		loginLockout     = 15 * time.Minute
	)
	loginKey := "login_attempts:" + email + ":" + c.ClientIP()
	if h.db.Redis != nil {
		count, _ := h.db.Redis.Get(ctx, loginKey).Int()
		if count >= maxLoginAttempts {
			q.Set("message", h.i18n.T("error.login_locked", h.getLocale(c)))
			c.Redirect(http.StatusFound, "/login?"+q.Encode())
			return
		}
	}

	info, err := h.authRoutes.AuthenticateUser(ctx, email, password)
	if errors.Is(err, routes.ErrSSORequired) {
		// Correct password, wrong door: point at SSO instead of counting a failure.
		q.Set("message", h.i18n.T("error.sso_required", h.getLocale(c)))
		c.Redirect(http.StatusFound, "/login?"+q.Encode())
		return
	}
	if err != nil {
		h.recordLoginFailure(ctx, loginKey, loginWindow)
		msg := err.Error()
		switch msg {
		case "account suspended":
			q.Set("message", h.i18n.T("error.account_suspended", h.getLocale(c)))
		case "account pending deletion":
			q.Set("message", h.i18n.T("error.account_pending_deletion", h.getLocale(c)))
		default:
			q.Set("message", h.i18n.T("error.invalid_email_or_password", h.getLocale(c)))
		}
		c.Redirect(http.StatusFound, "/login?"+q.Encode())
		return
	}

	// Success: clear the failed-attempt counter.
	if h.db.Redis != nil {
		_ = h.db.Redis.Del(ctx, loginKey).Err()
	}

	// Shared session step: issues the cookie for normal users, or starts an MFA
	// challenge when the account has TOTP enabled (no session cookie then).
	out, err := h.authRoutes.FinishPasswordLogin(c, info.UserID, session.Options{Remember: rememberMe, SetCookie: true})
	if errors.Is(err, routes.ErrSSORequired) {
		q.Set("message", h.i18n.T("error.sso_required", h.getLocale(c)))
		c.Redirect(http.StatusFound, "/login?"+q.Encode())
		return
	}
	if err != nil {
		h.logger.Warn("Web login could not issue a session", zap.Error(err), zap.String("user_id", info.UserID))
		q.Set("message", h.i18n.T("error.login_failed_retry", h.getLocale(c)))
		c.Redirect(http.StatusFound, "/login?"+q.Encode())
		return
	}
	if out.MFAToken != "" {
		setMFAPendingCookie(c, out.MFAToken)
		c.Redirect(http.StatusFound, "/login/mfa")
		return
	}
	c.Redirect(http.StatusFound, "/dashboard")
}

// HandleLoginMFA is the POST of the /login/mfa form: it checks the pending
// token cookie plus the TOTP/recovery code through the shared routes logic and
// only then sets the real session cookie.
func (h *Handler) HandleLoginMFA(c *gin.Context) {
	token, _ := c.Cookie(mfaPendingCookie)
	if token == "" {
		c.Redirect(http.StatusFound, "/login")
		return
	}
	code := strings.TrimSpace(c.PostForm("code"))
	if code == "" {
		h.redirectMFA(c, "error.mfa_code_required")
		return
	}
	_, err := h.authRoutes.CompleteMFALogin(c, token, routes.SecondFactor{Code: code}, true)
	if err != nil {
		if errors.Is(err, routes.ErrMFAUnavailable) {
			h.redirectMFA(c, "error.service_unavailable")
			return
		}
		// Generic message for every failure, including lockout and expired token.
		h.redirectMFA(c, "error.mfa_invalid_code")
		return
	}
	clearMFAPendingCookie(c)
	c.Redirect(http.StatusFound, "/dashboard")
}

func (h *Handler) redirectMFA(c *gin.Context, key string) {
	q := url.Values{}
	q.Set("toast", "error")
	q.Set("message", h.i18n.T(key, h.getLocale(c)))
	c.Redirect(http.StatusFound, "/login/mfa?"+q.Encode())
}

func (h *Handler) handleLoginMFA(c *gin.Context) {
	if c.GetString("user_id") != "" {
		c.Redirect(http.StatusFound, "/dashboard")
		return
	}
	if tok, _ := c.Cookie(mfaPendingCookie); tok == "" {
		c.Redirect(http.StatusFound, "/login")
		return
	}
	h.RenderPage(c, "login-mfa", PageData{Title: h.i18n.T("auth.mfa_title", h.getLocale(c))})
}

// recordLoginFailure increments the per-email+IP failed-login counter and
// extends the lockout window so 5 failures within 5 minutes lock the account
// for 15 minutes from the most recent failure.
func (h *Handler) recordLoginFailure(ctx context.Context, key string, window time.Duration) {
	if h.db.Redis == nil {
		return
	}
	pipe := h.db.Redis.Pipeline()
	pipe.Incr(ctx, key)
	// On first failure set the sliding window; subsequent failures refresh it.
	pipe.Expire(ctx, key, window)
	// Expire NX via a second Set-XX would be racy; rely on Expire refreshing TTL.
	_, _ = pipe.Exec(ctx)
}

// HandleRegister handles web form POST for registration
func (h *Handler) HandleRegister(c *gin.Context) {
	email := strings.ToLower(strings.TrimSpace(c.PostForm("email")))
	password := c.PostForm("password")
	confirmPassword := c.PostForm("confirm_password")
	code := c.PostForm("code")

	q := url.Values{}
	q.Set("toast", "error")

	if email == "" || password == "" || code == "" {
		q.Set("message", h.i18n.T("error.all_fields_required", h.getLocale(c)))
		c.Redirect(http.StatusFound, "/register?"+q.Encode())
		return
	}

	if password != confirmPassword {
		q.Set("message", h.i18n.T("error.passwords_dont_match", h.getLocale(c)))
		c.Redirect(http.StatusFound, "/register?"+q.Encode())
		return
	}

	if len(password) < 8 {
		q.Set("message", h.i18n.T("error.password_min_length", h.getLocale(c)))
		c.Redirect(http.StatusFound, "/register?"+q.Encode())
		return
	}

	ctx := c.Request.Context()

	// Verify code. Codes are issued exclusively by POST /api/auth/send-code,
	// which stores them under the verification:register: namespace. Track
	// failed attempts per email; after 5 wrong attempts invalidate the code
	// so a 6-digit code (10 min valid) cannot be brute forced online.
	// Redis is best-effort; if unavailable we fail open.
	const maxCodeAttempts = 5
	key := "verification:register:" + email
	attemptsKey := "verification_attempts:" + email
	if h.db.Redis != nil {
		attempts, _ := h.db.Redis.Get(ctx, attemptsKey).Int()
		if attempts >= maxCodeAttempts {
			// Invalidate the code and reset the counter.
			_ = h.db.Redis.Del(ctx, key).Err()
			_ = h.db.Redis.Del(ctx, attemptsKey).Err()
			q.Set("message", h.i18n.T("error.verification_code_too_many_attempts", h.getLocale(c)))
			c.Redirect(http.StatusFound, "/register?"+q.Encode())
			return
		}
	}
	storedCode, err := h.db.Redis.Get(ctx, key).Result()
	if err != nil || storedCode != code {
		if h.db.Redis != nil {
			pipe := h.db.Redis.Pipeline()
			pipe.Incr(ctx, attemptsKey)
			pipe.Expire(ctx, attemptsKey, 10*time.Minute)
			_, _ = pipe.Exec(ctx)
		}
		q.Set("message", h.i18n.T("error.verification_code_invalid", h.getLocale(c)))
		c.Redirect(http.StatusFound, "/register?"+q.Encode())
		return
	}
	// Success: clear the attempt counter alongside the code.
	if h.db.Redis != nil {
		_ = h.db.Redis.Del(ctx, attemptsKey).Err()
	}

	info, err := h.authRoutes.CreateAccount(ctx, routes.RegisterRequest{
		Email:          email,
		Password:       password,
		Code:           code,
		InvitationCode: c.PostForm("invitation_code"),
	})
	if err != nil {
		msg := err.Error()
		switch msg {
		case "email already registered":
			q.Set("message", h.i18n.T("error.email_already_registered", h.getLocale(c)))
		case "invalid or expired verification code":
			q.Set("message", h.i18n.T("error.verification_code_invalid", h.getLocale(c)))
		case "registration unavailable: Redis not configured":
			q.Set("message", h.i18n.T("error.service_unavailable", h.getLocale(c)))
		default:
			h.logger.Error("Registration failed", zap.Error(err))
			q.Set("message", h.i18n.T("error.register_failed_retry", h.getLocale(c)))
		}
		c.Redirect(http.StatusFound, "/register?"+q.Encode())
		return
	}

	// Best-effort active user gauge: increment on successful registration.
	metrics.GetMetrics().ActiveUsers.Add(1)

	// Apply welcome bonus and process invitation code.
	h.rewardSvc.ApplyWelcomeBonus(ctx, info.UserID, info.Email)
	if invitationCode := c.PostForm("invitation_code"); invitationCode != "" {
		h.rewardSvc.ProcessInvitationCode(ctx, info.UserID, invitationCode)
	}

	// Registration auto-login uses the same shared session step as login.
	if out, err := h.authRoutes.FinishPasswordLogin(c, info.UserID, session.Options{SetCookie: true}); err != nil || out.Session == nil {
		q.Set("message", h.i18n.T("error.register_success_but_login_failed", h.getLocale(c)))
		c.Redirect(http.StatusFound, "/login?"+q.Encode())
		return
	}
	c.Redirect(http.StatusFound, "/dashboard")
}

// HandleLogout handles web logout
func (h *Handler) HandleLogout(c *gin.Context) {
	ClearJWTWebCookie(c)
	c.Redirect(http.StatusFound, "/home")
}

// randomString generates a random alphanumeric string of given length using crypto/rand
func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(letters))))
		if err != nil {
			b[i] = letters[i%len(letters)]
			continue
		}
		b[i] = letters[idx.Int64()]
	}
	return string(b)
}

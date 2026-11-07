package routes

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/mayiants/ai-portal-go/internal/session"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// LoginOutcome is the result of FinishPasswordLogin: exactly one of Session
// (full session issued) or MFAToken (second factor still required) is set.
type LoginOutcome struct {
	Session  *session.Result
	MFAToken string
}

// SessionIssuer exposes the shared issuer so other login mechanisms (the SSO
// stage) mint sessions through the same choke point. SSO logins call
// Issue(..., session.MethodSSO, ...) because the IdP already authenticated the
// user; everything password-based must go through FinishPasswordLogin.
func (r *AuthRoutes) SessionIssuer() *session.Issuer { return r.issuer }

// MFA returns the shared MFA service.
func (r *AuthRoutes) MFA() *MFAService { return r.mfa }

// SetCookieWriter lets the web layer register how browser cookies are written.
func (r *AuthRoutes) SetCookieWriter(w session.CookieWriter) { r.issuer.SetCookieWriter(w) }

// FinishPasswordLogin is the single "password verified -> what now?" step used
// by the API login, the web login form and the registration auto-login. For a
// user with TOTP enabled it starts a 5 minute MFA challenge bound to the user
// and client IP instead of issuing a session; otherwise it issues the session.
// Callers must have verified the password (or created the account) already.
func (r *AuthRoutes) FinishPasswordLogin(c *gin.Context, userID string, opts session.Options) (*LoginOutcome, error) {
	// Backstop for enforce_sso: AuthenticateUser already refuses, but every
	// password-based session passes through here, so a future login path that
	// forgets that check still cannot bypass the enterprise policy.
	if enforced, err := r.ssoEnforcedFor(c.Request.Context(), userID); err != nil {
		return nil, err
	} else if enforced {
		return nil, ErrSSORequired
	}
	res, err := r.issuer.Issue(c, userID, session.MethodPassword, opts)
	if err == nil {
		return &LoginOutcome{Session: res}, nil
	}
	if !errors.Is(err, session.ErrMFARequired) {
		return nil, err
	}
	token, err := r.issuer.BeginChallenge(c.Request.Context(), userID, c.ClientIP(), opts.Remember)
	if err != nil {
		// Redis is down: MFA users cannot be challenged, so they cannot log in.
		// Failing closed is deliberate; never fall back to a password-only session.
		return nil, err
	}
	return &LoginOutcome{MFAToken: token}, nil
}

// CompleteMFALogin validates the pending token and the second factor and, only
// if both are good, consumes the token and issues the real session. setCookie
// selects the browser (cookie) or API (token in body) flavour. Every failure is
// the same generic ErrMFAInvalid (or ErrMFAUnavailable when the stores are down).
func (r *AuthRoutes) CompleteMFALogin(c *gin.Context, pendingToken string, in SecondFactor, setCookie bool) (*session.Result, error) {
	ctx := c.Request.Context()
	ch, err := r.issuer.PeekChallenge(ctx, pendingToken, c.ClientIP())
	if err != nil {
		padFailure(time.Now())
		if errors.Is(err, session.ErrChallengeUnavailable) {
			return nil, ErrMFAUnavailable
		}
		return nil, ErrMFAInvalid
	}
	if err := r.mfa.VerifySecondFactor(ctx, ch.UserID, in, "login"); err != nil {
		return nil, err
	}
	// Single use: exactly one concurrent request wins the DEL.
	won, err := r.issuer.ConsumeChallenge(ctx, pendingToken)
	if err != nil {
		padFailure(time.Now())
		return nil, ErrMFAUnavailable
	}
	if !won {
		padFailure(time.Now())
		return nil, ErrMFAInvalid
	}
	res, err := r.issuer.Issue(c, ch.UserID, session.MethodMFA, session.Options{Remember: ch.Remember, SetCookie: setCookie})
	if err != nil {
		return nil, ErrMFAInvalid
	}
	if r.audit != nil {
		r.audit.Log(ctx, res.UserID, res.Email, "auth", "user_login", "info", "User logged in (MFA)")
	}
	return res, nil
}

// loginMFA is POST /api/auth/login/mfa {mfa_token, code | recovery_code}.
func (r *AuthRoutes) loginMFA(c *gin.Context) {
	var req struct {
		MFAToken     string `json:"mfa_token" binding:"required"`
		Code         string `json:"code"`
		RecoveryCode string `json:"recovery_code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || (req.Code == "" && req.RecoveryCode == "") {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "mfa_token and code are required"}})
		return
	}
	res, err := r.CompleteMFALogin(c, strings.TrimSpace(req.MFAToken),
		SecondFactor{Code: req.Code, RecoveryCode: req.RecoveryCode}, false)
	if err != nil {
		if errors.Is(err, ErrMFAUnavailable) {
			r.logger.Warn("MFA login unavailable", zap.Error(err))
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "Service temporarily unavailable"}})
			return
		}
		// One message for wrong code, replay, lockout, expired/foreign token.
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "Invalid or expired verification code"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"token": res.Token,
		"user":  gin.H{"id": res.UserID, "name": res.Name, "email": res.Email, "role": res.Role},
	})
}

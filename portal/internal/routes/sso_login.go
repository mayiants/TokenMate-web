package routes

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mayiants/ai-portal-go/internal/session"
	"github.com/mayiants/ai-portal-go/internal/sso"
	"github.com/mayiants/ai-portal-go/pkg/crypto"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

// ErrSSORequired is returned by password login for non-admin members of an
// enterprise that enforces SSO.
var ErrSSORequired = errors.New("sso required")

const ssoRateLimitPerMinute = 60

// SetSSOClient replaces the OIDC client (tests, or sharing caches).
func (r *AuthRoutes) SetSSOClient(c *sso.Client) { r.ssoClient = c }

func (r *AuthRoutes) sso() *sso.Client {
	if r.ssoClient == nil {
		r.ssoClient = sso.NewClient(sso.Options{})
	}
	return r.ssoClient
}

func (r *AuthRoutes) ssoStates() *sso.StateStore {
	return sso.NewStateStore(r.db.Redis, r.cfg.Auth.JWTSecret)
}

// RegisterSSO wires the browser-facing SSO endpoints onto the engine root
// (/auth/sso/*). POST /api/auth/sso/discover is registered by Register with the
// other /api/auth routes.
func (r *AuthRoutes) RegisterSSO(root gin.IRouter) {
	root.GET("/auth/sso/start", r.ssoStart)
	root.GET("/auth/sso/callback", r.ssoCallback)
}

// ssoEnforcedFor reports whether SSO is mandatory for this user's password
// login: the user belongs to an enterprise with enabled + enforce_sso and is a
// plain member. enterprise_admin / super_admin / system_admin (and anyone with
// an admin membership) keep password login as the break-glass path.
func (r *AuthRoutes) ssoEnforcedFor(ctx context.Context, userID string) (bool, error) {
	var enforced bool
	err := r.db.QueryRow(ctx,
		`SELECT EXISTS (
		   SELECT 1 FROM users u
		     JOIN enterprise_sso_configs c ON c.enterprise_id = u.enterprise_id AND c.enabled AND c.enforce_sso
		    WHERE u.id = $1::uuid AND u.deleted_at IS NULL AND COALESCE(u.role, 'user') = 'user'
		      AND NOT EXISTS (SELECT 1 FROM enterprise_members m
		                       WHERE m.user_id = u.id AND m.enterprise_id = u.enterprise_id
		                         AND m.role = 'admin' AND m.removed_at IS NULL))`, userID).Scan(&enforced)
	return enforced, err
}

// ---- rate limit ------------------------------------------------------------

// ssoAllow is a small per-IP fixed-window limiter for the unauthenticated SSO
// endpoints. It fails open when Redis is down, like the other auth limiters.
func (r *AuthRoutes) ssoAllow(c *gin.Context) bool {
	if r.db.Redis == nil {
		return true
	}
	key := "sso:rl:" + c.ClientIP() + ":" + strconv.FormatInt(time.Now().Unix()/60, 10)
	n, err := r.db.Redis.Incr(c.Request.Context(), key).Result()
	if err != nil {
		return true
	}
	if n == 1 {
		_ = r.db.Redis.Expire(c.Request.Context(), key, 2*time.Minute).Err()
	}
	return n <= ssoRateLimitPerMinute
}

// ---- discovery ("Sign in with SSO") -------------------------------------------

// POST /api/auth/sso/discover {email}
//
// The response has the same shape for every input - {"sso": bool, "start_url":
// string} with HTTP 200 - so it does not distinguish unknown users, unknown
// domains or malformed input. It necessarily reveals whether a domain has SSO
// (the browser has to be redirected), but nothing about individual accounts.
func (r *AuthRoutes) ssoDiscover(c *gin.Context) {
	none := gin.H{"sso": false, "start_url": ""}
	if !r.ssoAllow(c) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": gin.H{"message": "Too many requests"}})
		return
	}
	var req struct {
		Email string `json:"email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, none)
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	at := strings.LastIndex(email, "@")
	if at < 1 || at == len(email)-1 || len(email) > 255 {
		c.JSON(http.StatusOK, none)
		return
	}
	domain := email[at+1:]
	// Exactly one enabled enterprise may match; an ambiguous domain gets no SSO
	// rather than an arbitrary pick.
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT c.enterprise_id::text FROM enterprise_sso_configs c
		   JOIN enterprises e ON e.id = c.enterprise_id AND e.deleted_at IS NULL
		  WHERE c.enabled AND c.issuer IS NOT NULL AND c.client_id IS NOT NULL AND c.client_secret_enc IS NOT NULL
		    AND $1 = ANY(c.allowed_email_domains) LIMIT 2`, domain)
	if err != nil {
		r.logger.Warn("SSO discovery lookup failed", zap.Error(err))
		c.JSON(http.StatusOK, none)
		return
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	if len(ids) != 1 {
		c.JSON(http.StatusOK, none)
		return
	}
	c.JSON(http.StatusOK, gin.H{"sso": true, "start_url": "/auth/sso/start?enterprise=" + url.QueryEscape(ids[0])})
}

// ---- start -------------------------------------------------------------------

// ssoFail redirects to the login page with a generic message. Details go to the
// audit log only, so the browser learns nothing about why a login failed.
func (r *AuthRoutes) ssoFail(c *gin.Context, msgKey string) {
	q := url.Values{}
	q.Set("toast", "error")
	q.Set("message", T(msgKey, c))
	c.Redirect(http.StatusFound, "/login?"+q.Encode())
}

func (r *AuthRoutes) ssoCookieSecure() bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.cfg.App.BaseURL)), "https://")
}

func (r *AuthRoutes) setSSOCookie(c *gin.Context, value string, maxAge int) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: sso.CookieName, Value: value, MaxAge: maxAge, Path: "/auth/sso",
		Secure: r.ssoCookieSecure(), HttpOnly: true,
		// Lax: the callback is a top-level GET navigation from the IdP, which Lax
		// cookies accompany; cross-site subrequests and POSTs do not carry it.
		SameSite: http.SameSiteLaxMode,
	})
}

// GET /auth/sso/start?enterprise=<uuid>
func (r *AuthRoutes) ssoStart(c *gin.Context) {
	ctx := c.Request.Context()
	if !r.ssoAllow(c) {
		c.String(http.StatusTooManyRequests, "Too many requests")
		return
	}
	entID := strings.ToLower(strings.TrimSpace(c.Query("enterprise")))
	if _, err := uuid.Parse(entID); err != nil {
		r.ssoFail(c, "error.sso_unavailable")
		return
	}
	cfg, err := loadSSOConfig(ctx, r.db, entID)
	if err != nil || cfg == nil || !cfg.Enabled || !cfg.complete() {
		r.ssoAudit(c, "", "", "sso_failed", entID, "config_unavailable")
		r.ssoFail(c, "error.sso_unavailable")
		return
	}
	redirectURI, ok := r.ssoRedirect()
	if !ok {
		r.logger.Error("SSO needs an https APP_BASE_URL (or SSO_ALLOW_INSECURE_HTTP=true)")
		r.ssoAudit(c, "", "", "sso_failed", entID, "insecure_base_url")
		r.ssoFail(c, "error.sso_unavailable")
		return
	}
	md, err := r.sso().Discover(ctx, cfg.Issuer)
	if err != nil {
		r.logger.Warn("SSO discovery failed", zap.String("enterprise_id", entID), zap.Error(err))
		r.ssoAudit(c, "", "", "sso_failed", entID, "discovery_failed")
		r.ssoFail(c, "error.sso_unavailable")
		return
	}
	nonce, err1 := sso.RandomToken(32)
	verifier, err2 := sso.RandomToken(48) // 64 chars: inside RFC 7636's 43..128
	if err1 != nil || err2 != nil {
		r.ssoFail(c, "error.sso_unavailable")
		return
	}
	state, cookie, err := r.ssoStates().Begin(ctx, sso.Pending{EnterpriseID: entID, Nonce: nonce, Verifier: verifier})
	if err != nil {
		r.logger.Warn("SSO state store unavailable", zap.Error(err))
		r.ssoFail(c, "error.sso_unavailable")
		return
	}
	authURL, err := md.AuthURL(sso.AuthRequest{
		ClientID: cfg.ClientID, RedirectURI: redirectURI, Scopes: cfg.Scopes,
		State: state, Nonce: nonce, Verifier: verifier,
	})
	if err != nil {
		r.ssoFail(c, "error.sso_unavailable")
		return
	}
	r.setSSOCookie(c, cookie, int(sso.StateTTL.Seconds()))
	c.Header("Cache-Control", "no-store")
	c.Redirect(http.StatusFound, authURL)
}

// ssoRedirect builds the callback URL from configuration, never from the
// request, and refuses plain http unless explicitly allowed for development.
func (r *AuthRoutes) ssoRedirect() (string, bool) {
	base := strings.TrimSpace(r.cfg.App.BaseURL)
	if base == "" {
		return "", false
	}
	if !strings.HasPrefix(strings.ToLower(base), "https://") && !r.sso().AllowInsecure() {
		return "", false
	}
	return ssoRedirectURI(base), true
}

// ---- callback ----------------------------------------------------------------

// ssoReject is a policy refusal with a short machine reason for the audit log.
type ssoReject struct{ reason string }

func (e *ssoReject) Error() string { return "sso rejected: " + e.reason }

func reject(reason string) error { return &ssoReject{reason: reason} }

// GET /auth/sso/callback?code=...&state=...
func (r *AuthRoutes) ssoCallback(c *gin.Context) {
	ctx := c.Request.Context()
	if !r.ssoAllow(c) {
		c.String(http.StatusTooManyRequests, "Too many requests")
		return
	}
	// The binding cookie is single use too: clear it whatever happens.
	cookie, _ := c.Cookie(sso.CookieName)
	r.setSSOCookie(c, "", -1)
	c.Header("Cache-Control", "no-store")

	pend, err := r.ssoStates().Consume(ctx, c.Query("state"), cookie)
	if err != nil {
		reason := "state_invalid"
		if errors.Is(err, sso.ErrStateUnavailable) {
			reason = "state_unavailable"
		}
		r.ssoAudit(c, "", "", "sso_failed", "", reason)
		r.ssoFail(c, "error.sso_failed")
		return
	}
	entID := pend.EnterpriseID
	if e := c.Query("error"); e != "" || c.Query("code") == "" {
		r.ssoAudit(c, "", "", "sso_failed", entID, "idp_error")
		r.ssoFail(c, "error.sso_failed")
		return
	}
	claims, cfg, err := r.ssoRedeem(c, pend)
	if err != nil {
		r.logger.Warn("SSO token validation failed", zap.String("enterprise_id", entID), zap.Error(err))
		r.ssoAudit(c, "", "", "sso_failed", entID, "token_rejected")
		r.ssoFail(c, "error.sso_failed")
		return
	}

	userID, how, err := r.ssoResolveUser(c, cfg, claims)
	if err != nil {
		var rej *ssoReject
		reason, key := "internal_error", "error.sso_failed"
		if errors.As(err, &rej) {
			reason, key = rej.reason, "error.sso_not_permitted"
		} else {
			r.logger.Error("SSO account mapping failed", zap.String("enterprise_id", entID), zap.Error(err))
		}
		r.ssoAudit(c, "", claims.Email, "sso_failed", entID, reason)
		r.ssoFail(c, key)
		return
	}

	res, err := r.issuer.Issue(c, userID, session.MethodSSO, session.Options{SetCookie: true})
	if err != nil {
		r.ssoAudit(c, userID, claims.Email, "sso_failed", entID, "session_unavailable")
		r.ssoFail(c, "error.sso_failed")
		return
	}
	if _, err := r.db.Pool.Exec(ctx, `UPDATE users SET last_login_at = NOW(), updated_at = NOW() WHERE id = $1::uuid`, userID); err != nil {
		r.logger.Warn("Failed to update login timestamp", zap.Error(err), zap.String("user_id", userID))
	}
	if r.audit != nil {
		r.audit.LogWithDetails(ctx, res.UserID, res.Email, "auth", "sso_login", "info", "User logged in via SSO",
			map[string]interface{}{"enterprise_id": entID, "issuer": cfg.Issuer, "mapping": how})
	}
	c.Redirect(http.StatusFound, "/dashboard")
}

// ssoRedeem loads the enterprise config, exchanges the code (with the PKCE
// verifier that never left the server) and verifies the ID token.
func (r *AuthRoutes) ssoRedeem(c *gin.Context, pend *sso.Pending) (*sso.Claims, *ssoConfig, error) {
	ctx := c.Request.Context()
	cfg, err := loadSSOConfig(ctx, r.db, pend.EnterpriseID)
	if err != nil || cfg == nil || !cfg.Enabled || !cfg.complete() {
		return nil, nil, errors.New("sso not available for enterprise")
	}
	redirectURI, ok := r.ssoRedirect()
	if !ok {
		return nil, nil, errors.New("insecure base URL")
	}
	secret, err := crypto.Decrypt(cfg.ClientSecretEnc, r.cfg.Auth.EncryptionKey)
	if err != nil || secret == "" {
		return nil, nil, errors.New("client secret unreadable")
	}
	md, err := r.sso().Discover(ctx, cfg.Issuer)
	if err != nil {
		return nil, nil, err
	}
	raw, err := r.sso().Exchange(ctx, md, sso.ExchangeRequest{
		ClientID: cfg.ClientID, ClientSecret: secret, RedirectURI: redirectURI,
		Code: c.Query("code"), Verifier: pend.Verifier,
	})
	if err != nil {
		return nil, nil, err
	}
	claims, err := r.sso().VerifyIDToken(ctx, md, raw, sso.VerifyParams{
		Issuer: cfg.Issuer, ClientID: cfg.ClientID, Nonce: pend.Nonce,
	})
	if err != nil {
		return nil, nil, err
	}
	return claims, cfg, nil
}

// ssoAudit records sso_failed and similar events. The reason is a fixed code.
func (r *AuthRoutes) ssoAudit(c *gin.Context, userID, email, action, entID, reason string) {
	if r.audit == nil {
		return
	}
	level := "warning"
	r.audit.LogWithDetails(c.Request.Context(), userID, email, "auth", action, level,
		fmt.Sprintf("SSO login failed: %s", reason),
		map[string]interface{}{"enterprise_id": entID, "reason": reason})
}

// ---- account mapping -----------------------------------------------------------

type ssoUser struct {
	ID           string
	Role         string
	Status       string
	EnterpriseID string
	Erasure      bool
}

func emailDomain(email string) string {
	if at := strings.LastIndex(email, "@"); at >= 0 {
		return email[at+1:]
	}
	return ""
}

// ssoResolveUser maps a verified identity to a local user (returned as ID plus
// how it was matched). Order matters and each step is deliberately narrow:
//
//  1. an existing (issuer, subject) identity of THIS enterprise;
//  2. an existing user with the same verified e-mail who is already a member of
//     THIS enterprise (the identity is then linked);
//  3. just-in-time provisioning when enabled and the platform approved the domain.
//
// An e-mail that belongs to a user of another enterprise, a standalone user or
// a platform admin is never taken over: the IdP is trusted for its own tenants'
// members, not for arbitrary local accounts.
func (r *AuthRoutes) ssoResolveUser(c *gin.Context, cfg *ssoConfig, cl *sso.Claims) (string, string, error) {
	ctx := c.Request.Context()
	if !containsString(cfg.AllowedDomains, emailDomain(cl.Email)) {
		return "", "", reject("domain_not_allowed")
	}
	var entOK bool
	if err := r.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM enterprises WHERE id = $1::uuid AND deleted_at IS NULL AND COALESCE(status, 'active') = 'active')`,
		cfg.EnterpriseID).Scan(&entOK); err != nil {
		return "", "", err
	}
	if !entOK {
		return "", "", reject("enterprise_inactive")
	}

	// 1. identity
	var identUser, identEnt string
	err := r.db.QueryRow(ctx,
		`SELECT user_id::text, enterprise_id::text FROM user_identities WHERE issuer = $1 AND subject = $2`,
		cfg.Issuer, cl.Subject).Scan(&identUser, &identEnt)
	switch {
	case err == nil:
		if identEnt != cfg.EnterpriseID {
			return "", "", reject("identity_other_enterprise")
		}
		u, err := r.loadSSOUser(ctx, identUser)
		if err != nil {
			return "", "", reject("user_missing")
		}
		if err := r.ssoEligible(ctx, u, cfg.EnterpriseID); err != nil {
			return "", "", err
		}
		_, _ = r.db.Pool.Exec(ctx,
			`UPDATE user_identities SET last_login_at = NOW(), email = $1 WHERE issuer = $2 AND subject = $3`,
			cl.Email, cfg.Issuer, cl.Subject)
		return u.ID, "identity", nil
	case !errors.Is(err, pgx.ErrNoRows):
		return "", "", err
	}

	// 2. existing member with the same e-mail
	var existingID string
	err = r.db.QueryRow(ctx, `SELECT id::text FROM users WHERE email = $1 AND deleted_at IS NULL`, cl.Email).Scan(&existingID)
	switch {
	case err == nil:
		u, err := r.loadSSOUser(ctx, existingID)
		if err != nil {
			return "", "", reject("user_missing")
		}
		if err := r.ssoEligible(ctx, u, cfg.EnterpriseID); err != nil {
			return "", "", err
		}
		// A user already bound to a different subject at this issuer must not be
		// re-bound by whoever now owns the same e-mail at the IdP.
		var bound bool
		if err := r.db.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM user_identities WHERE user_id = $1::uuid AND issuer = $2)`,
			u.ID, cfg.Issuer).Scan(&bound); err != nil {
			return "", "", err
		}
		if bound {
			return "", "", reject("identity_conflict")
		}
		if _, err := r.db.Pool.Exec(ctx,
			`INSERT INTO user_identities (user_id, enterprise_id, issuer, subject, email, last_login_at)
			 VALUES ($1::uuid, $2::uuid, $3, $4, $5, NOW())`,
			u.ID, cfg.EnterpriseID, cfg.Issuer, cl.Subject, cl.Email); err != nil {
			return "", "", reject("identity_conflict") // lost a race on UNIQUE(issuer, subject)
		}
		if r.audit != nil {
			r.audit.LogWithDetails(ctx, u.ID, cl.Email, "auth", "sso_identity_linked", "info", "SSO identity linked to existing member",
				map[string]interface{}{"enterprise_id": cfg.EnterpriseID, "issuer": cfg.Issuer})
		}
		return u.ID, "linked", nil
	case !errors.Is(err, pgx.ErrNoRows):
		return "", "", err
	}

	// 3. just-in-time provisioning
	id, err := r.ssoProvision(c, cfg, cl)
	return id, "provisioned", err
}

func (r *AuthRoutes) loadSSOUser(ctx context.Context, userID string) (*ssoUser, error) {
	var u ssoUser
	var ent *string
	err := r.db.QueryRow(ctx,
		`SELECT id::text, COALESCE(role, 'user'), COALESCE(status, 'active'), enterprise_id::text,
		        gdpr_erasure_requested_at IS NOT NULL
		   FROM users WHERE id = $1::uuid AND deleted_at IS NULL`, userID,
	).Scan(&u.ID, &u.Role, &u.Status, &ent, &u.Erasure)
	if err != nil {
		return nil, err
	}
	u.EnterpriseID = deref(ent)
	return &u, nil
}

// ssoEligible decides whether SSO may log this existing user in.
func (r *AuthRoutes) ssoEligible(ctx context.Context, u *ssoUser, entID string) error {
	if u.Status == "suspended" {
		return reject("user_suspended")
	}
	if u.Erasure {
		return reject("user_pending_deletion")
	}
	// Platform administrators are never reachable through a tenant's IdP.
	if u.Role != "user" && u.Role != "enterprise_admin" {
		return reject("privileged_account")
	}
	if !strings.EqualFold(u.EnterpriseID, entID) {
		return reject("user_not_in_enterprise")
	}
	var member bool
	if err := r.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM enterprise_members
		                WHERE enterprise_id = $1::uuid AND user_id = $2::uuid
		                  AND removed_at IS NULL AND deleted_at IS NULL AND COALESCE(status, 'active') = 'active')`,
		entID, u.ID).Scan(&member); err != nil {
		return err
	}
	if !member {
		return reject("user_not_in_enterprise")
	}
	return nil
}

// ssoProvision creates a new member. It follows addEnterpriseMember's rules:
// the seat limit counts members plus open invitations, and auto-join is only
// allowed for domains the platform approved in enterprises.allowed_domains
// (anything else needs the manual invitation/approval flow). The new account
// gets the plain member role, a verified e-mail and a random password nobody
// knows, so it can only be entered through SSO until the user resets it.
func (r *AuthRoutes) ssoProvision(c *gin.Context, cfg *ssoConfig, cl *sso.Claims) (string, error) {
	ctx := c.Request.Context()
	if !cfg.JIT {
		return "", reject("jit_disabled")
	}
	role, ok := normalizeSSORole(cfg.DefaultRole)
	if !ok {
		return "", reject("default_role_invalid")
	}
	seats := loadEnterpriseSeats(ctx, r.db, cfg.EnterpriseID)
	if !domainMatches(cl.Email, seats.AllowedDomains) {
		return "", reject("jit_domain_not_approved")
	}
	name := cl.Name
	if name == "" {
		name = strings.SplitN(cl.Email, "@", 2)[0]
	}
	if len(name) > 255 {
		name = name[:255]
	}
	pw := make([]byte, 32)
	if _, err := rand.Read(pw); err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(fmt.Sprintf("%x", pw)), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}

	var userID string
	err = r.db.WithTx(ctx, func(tx pgx.Tx) error {
		// Serialise concurrent first logins so the seat limit cannot be overshot.
		var locked int
		if err := tx.QueryRow(ctx, `SELECT 1 FROM enterprises WHERE id = $1::uuid FOR UPDATE`, cfg.EnterpriseID).Scan(&locked); err != nil {
			return err
		}
		fresh := loadEnterpriseSeats(ctx, tx, cfg.EnterpriseID)
		if fresh.Members+fresh.Pending >= fresh.MaxMembers {
			return reject("seat_limit_reached")
		}
		if err := tx.QueryRow(ctx,
			`INSERT INTO users (email, name, password_hash, role, enterprise_id, email_verified)
			 VALUES ($1, $2, $3, 'user', $4::uuid, true) RETURNING id::text`,
			cl.Email, name, string(hash), cfg.EnterpriseID).Scan(&userID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			// The users trigger (sync_enterprise_membership) has normally created the
			// row already; the upsert pins the role to what the policy decided.
			`INSERT INTO enterprise_members (enterprise_id, user_id, role, joined_at)
			 VALUES ($1::uuid, $2::uuid, $3, NOW())
			 ON CONFLICT (enterprise_id, user_id) DO UPDATE SET role = $3, removed_at = NULL, removed_by = NULL`,
			cfg.EnterpriseID, userID, role); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO user_identities (user_id, enterprise_id, issuer, subject, email, last_login_at)
			 VALUES ($1::uuid, $2::uuid, $3, $4, $5, NOW())`,
			userID, cfg.EnterpriseID, cfg.Issuer, cl.Subject, cl.Email)
		return err
	})
	if err != nil {
		return "", err
	}
	if r.rewardSvc != nil {
		r.rewardSvc.ApplyWelcomeBonus(ctx, userID, cl.Email)
	}
	if r.audit != nil {
		r.audit.LogWithDetails(ctx, userID, cl.Email, "auth", "sso_user_provisioned", "info", "User provisioned through SSO",
			map[string]interface{}{"enterprise_id": cfg.EnterpriseID, "issuer": cfg.Issuer, "role": role})
	}
	oldS, newS := "none", "joined"
	writeEnterpriseAudit(ctx, r.db, r.logger, enterpriseAuditEntry{
		EnterpriseID: cfg.EnterpriseID, Category: "member", ChangeType: "member_joined", FieldName: "membership",
		UserID: &userID, ChangedBy: userID, OldValueStr: &oldS, NewValueStr: &newS, TargetType: "user", TargetID: userID,
		Reason: fmt.Sprintf("Provisioned through SSO (%s)", cl.Email),
	})
	return userID, nil
}

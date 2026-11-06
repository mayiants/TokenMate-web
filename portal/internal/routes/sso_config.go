package routes

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/sso"
	"github.com/ai-proxy/ai-portal-go/pkg/crypto"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// ssoConfig is one enterprise_sso_configs row.
type ssoConfig struct {
	EnterpriseID    string
	Enabled         bool
	Issuer          string
	ClientID        string
	ClientSecretEnc string
	Scopes          string
	AllowedDomains  []string
	DefaultRole     string
	JIT             bool
	EnforceSSO      bool
	UpdatedAt       time.Time
}

const (
	defaultSSOScopes = "openid email profile"
	// ssoMemberRole is the only role JIT provisioning may hand out.
	ssoMemberRole = "member"
	maxSSODomains = 20
)

var (
	ssoDomainRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)
	ssoScopeRe  = regexp.MustCompile(`^[A-Za-z0-9:_./-]{1,64}$`)
	ssoClientRe = regexp.MustCompile(`^[\x21-\x7e]{1,255}$`)

	// Free mail providers can never be claimed by one enterprise: whoever
	// controlled such a claim could receive every user of that provider at its
	// own identity provider.
	ssoReservedDomains = map[string]bool{
		"gmail.com": true, "googlemail.com": true, "outlook.com": true, "hotmail.com": true, "live.com": true,
		"msn.com": true, "yahoo.com": true, "icloud.com": true, "me.com": true, "aol.com": true,
		"proton.me": true, "protonmail.com": true, "qq.com": true, "163.com": true, "126.com": true,
		"foxmail.com": true, "sina.com": true, "gmx.com": true, "mail.com": true, "yandex.com": true,
	}
)

// loadSSOConfig returns nil, nil when the enterprise has no configuration.
func loadSSOConfig(ctx context.Context, database *db.DB, enterpriseID string) (*ssoConfig, error) {
	var c ssoConfig
	var issuer, clientID, secret *string
	err := database.QueryRow(ctx,
		`SELECT enterprise_id::text, enabled, issuer, client_id, client_secret_enc, scopes,
		        COALESCE(allowed_email_domains, '{}'), default_role, jit_provisioning, enforce_sso, updated_at
		   FROM enterprise_sso_configs WHERE enterprise_id = $1::uuid`, enterpriseID,
	).Scan(&c.EnterpriseID, &c.Enabled, &issuer, &clientID, &secret, &c.Scopes,
		&c.AllowedDomains, &c.DefaultRole, &c.JIT, &c.EnforceSSO, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.Issuer, c.ClientID, c.ClientSecretEnc = deref(issuer), deref(clientID), deref(secret)
	return &c, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// complete reports whether every field needed to run a login is present.
func (c *ssoConfig) complete() bool {
	return c.Issuer != "" && c.ClientID != "" && c.ClientSecretEnc != "" && len(c.AllowedDomains) > 0
}

// ssoRedirectURI is derived from the configured public base URL only. It is
// never built from the Host header: a forged Host would otherwise let an
// attacker steer authorization codes to their own server.
func ssoRedirectURI(baseURL string) string {
	return strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/auth/sso/callback"
}

// ---- management API ---------------------------------------------------------

// RegisterSSO wires the per-enterprise SSO management API. Like retention and
// security it lives in its own group; resolveRetentionTarget enforces "own
// enterprise_admin, or super_admin naming the enterprise".
func (r *EnterpriseRoutes) RegisterSSO(rg *gin.RouterGroup) {
	g := rg.Group("/enterprise/sso")
	g.Use(r.auth.RequireAuth())
	{
		g.GET("", r.getEnterpriseSSO)
		g.PUT("", r.putEnterpriseSSO)
		g.DELETE("", r.deleteEnterpriseSSO)
		g.POST("/test", r.testEnterpriseSSO)
	}
}

// SetSSOClient replaces the OIDC client (tests, or sharing one client and its
// caches with AuthRoutes).
func (r *EnterpriseRoutes) SetSSOClient(c *sso.Client) { r.ssoClient = c }

var ssoClientMu sync.Mutex

func (r *EnterpriseRoutes) sso() *sso.Client {
	ssoClientMu.Lock()
	defer ssoClientMu.Unlock()
	if r.ssoClient == nil {
		r.ssoClient = sso.NewClient(sso.Options{})
	}
	return r.ssoClient
}

type ssoConfigView struct {
	EnterpriseID       string   `json:"enterprise_id"`
	Configured         bool     `json:"configured"`
	Enabled            bool     `json:"enabled"`
	Issuer             string   `json:"issuer"`
	ClientID           string   `json:"client_id"`
	ClientSecretSet    bool     `json:"client_secret_set"`
	Scopes             string   `json:"scopes"`
	AllowedEmailDomain []string `json:"allowed_email_domains"`
	DefaultRole        string   `json:"default_role"`
	JITProvisioning    bool     `json:"jit_provisioning"`
	EnforceSSO         bool     `json:"enforce_sso"`
	RedirectURI        string   `json:"redirect_uri"`
	UpdatedAt          *string  `json:"updated_at,omitempty"`
}

// view never carries the secret, only whether one is stored.
func (r *EnterpriseRoutes) ssoView(entID string, c *ssoConfig) ssoConfigView {
	v := ssoConfigView{
		EnterpriseID: entID, Scopes: defaultSSOScopes, AllowedEmailDomain: []string{},
		DefaultRole: ssoMemberRole, JITProvisioning: true,
		RedirectURI: ssoRedirectURI(r.cfg.App.BaseURL),
	}
	if c == nil {
		return v
	}
	ts := c.UpdatedAt.UTC().Format(time.RFC3339)
	v.Configured, v.Enabled, v.Issuer, v.ClientID = true, c.Enabled, c.Issuer, c.ClientID
	v.ClientSecretSet = c.ClientSecretEnc != ""
	v.Scopes, v.AllowedEmailDomain, v.DefaultRole = c.Scopes, c.AllowedDomains, c.DefaultRole
	v.JITProvisioning, v.EnforceSSO, v.UpdatedAt = c.JIT, c.EnforceSSO, &ts
	if v.AllowedEmailDomain == nil {
		v.AllowedEmailDomain = []string{}
	}
	return v
}

// GET /api/enterprise/sso[?enterprise_id=<uuid>]
func (r *EnterpriseRoutes) getEnterpriseSSO(c *gin.Context) {
	entID, ok := r.resolveRetentionTarget(c, c.Query("enterprise_id"))
	if !ok {
		return
	}
	cfg, err := loadSSOConfig(c.Request.Context(), r.db, entID)
	if err != nil {
		r.logger.Error("Failed to load SSO config", zap.String("enterprise_id", entID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to load SSO settings"}})
		return
	}
	c.JSON(http.StatusOK, r.ssoView(entID, cfg))
}

type ssoPutRequest struct {
	EnterpriseID   string    `json:"enterprise_id"`
	Enabled        *bool     `json:"enabled"`
	Issuer         *string   `json:"issuer"`
	ClientID       *string   `json:"client_id"`
	ClientSecret   *string   `json:"client_secret"`
	Scopes         *string   `json:"scopes"`
	AllowedDomains *[]string `json:"allowed_email_domains"`
	DefaultRole    *string   `json:"default_role"`
	JIT            *bool     `json:"jit_provisioning"`
	EnforceSSO     *bool     `json:"enforce_sso"`
}

// normalizeSSODomains lower-cases, strips a leading "@", validates and de-dups.
// The second return value is a user-facing problem message ("" when valid),
// returned as a string rather than an error so it can stay sentence-cased.
func normalizeSSODomains(in []string) ([]string, string) {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, d := range in {
		d = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(d), "@")))
		if d == "" {
			continue
		}
		if len(d) > 253 || !ssoDomainRe.MatchString(d) {
			return nil, fmt.Sprintf("Invalid email domain %q", d)
		}
		if ssoReservedDomains[d] {
			return nil, fmt.Sprintf("Domain %q is a public mail provider and cannot be used for SSO", d)
		}
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	if len(out) > maxSSODomains {
		return nil, fmt.Sprintf("At most %d domains are allowed", maxSSODomains)
	}
	return out, ""
}

// normalizeSSOScopes returns the normalized scope string and, like
// normalizeSSODomains, a user-facing problem message ("" when valid).
func normalizeSSOScopes(in string) (string, string) {
	fields := strings.Fields(in)
	if len(fields) == 0 {
		return defaultSSOScopes, ""
	}
	if len(fields) > 10 {
		return "", "Too many scopes"
	}
	hasOpenID := false
	for _, f := range fields {
		if !ssoScopeRe.MatchString(f) {
			return "", fmt.Sprintf("Invalid scope %q", f)
		}
		if f == "openid" {
			hasOpenID = true
		}
	}
	if !hasOpenID {
		return "", "Scopes must include openid"
	}
	return strings.Join(fields, " "), ""
}

// normalizeSSORole accepts only the plain member role. Admin roles can never be
// granted by an external identity provider.
func normalizeSSORole(in string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(in)) {
	case "", "member", "user":
		return ssoMemberRole, true
	}
	return "", false
}

// PUT /api/enterprise/sso - partial update; omitted fields keep their value.
func (r *EnterpriseRoutes) putEnterpriseSSO(c *gin.Context) {
	var req ssoPutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request body"}})
		return
	}
	entID, ok := r.resolveRetentionTarget(c, req.EnterpriseID)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	bad := func(msg string) { c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": msg}}) }

	old, err := loadSSOConfig(ctx, r.db, entID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to load SSO settings"}})
		return
	}
	cfg := ssoConfig{EnterpriseID: entID, Scopes: defaultSSOScopes, DefaultRole: ssoMemberRole, JIT: true}
	if old != nil {
		cfg = *old
	}
	if req.Issuer != nil {
		iss := strings.TrimSpace(*req.Issuer)
		if iss != "" {
			if _, err := sso.ValidateURL(iss, sso.AllowInsecureHTTPFromEnv()); err != nil {
				bad(ssoURLProblem(err))
				return
			}
		}
		cfg.Issuer = iss
	}
	if req.ClientID != nil {
		id := strings.TrimSpace(*req.ClientID)
		if id != "" && !ssoClientRe.MatchString(id) {
			bad("Invalid client_id")
			return
		}
		cfg.ClientID = id
	}
	secretChanged := false
	if req.ClientSecret != nil && *req.ClientSecret != "" {
		if len(*req.ClientSecret) > 2048 {
			bad("client_secret is too long")
			return
		}
		encKey := r.cfg.Auth.EncryptionKey
		if encKey == "" {
			r.logger.Error("SSO client secret cannot be stored: ENCRYPTION_KEY is not set")
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Secret storage is not configured"}})
			return
		}
		enc, err := crypto.Encrypt(*req.ClientSecret, encKey)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to protect client secret"}})
			return
		}
		cfg.ClientSecretEnc, secretChanged = enc, true
	}
	if req.Scopes != nil {
		sc, problem := normalizeSSOScopes(*req.Scopes)
		if problem != "" {
			bad(problem)
			return
		}
		cfg.Scopes = sc
	}
	if req.AllowedDomains != nil {
		ds, problem := normalizeSSODomains(*req.AllowedDomains)
		if problem != "" {
			bad(problem)
			return
		}
		cfg.AllowedDomains = ds
	}
	if req.DefaultRole != nil {
		role, ok := normalizeSSORole(*req.DefaultRole)
		if !ok {
			bad("default_role must be the member role; administrators cannot be provisioned through SSO")
			return
		}
		cfg.DefaultRole = role
	}
	if req.Enabled != nil {
		cfg.Enabled = *req.Enabled
	}
	if req.JIT != nil {
		cfg.JIT = *req.JIT
	}
	if req.EnforceSSO != nil {
		cfg.EnforceSSO = *req.EnforceSSO
	}
	if cfg.Enabled && !cfg.complete() {
		bad("Issuer, client ID, client secret and at least one email domain are required to enable SSO")
		return
	}
	if cfg.EnforceSSO && !cfg.Enabled {
		bad("SSO must be enabled before it can be enforced")
		return
	}
	if cfg.Enabled {
		// First come, first served: an enabled domain belongs to one enterprise.
		// This stops a second tenant from redirecting another tenant's users to
		// its own identity provider. (DNS-based ownership proof is a follow-up.)
		var clash bool
		if err := r.db.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM enterprise_sso_configs
			                WHERE enabled AND enterprise_id <> $1::uuid AND allowed_email_domains && $2::text[])`,
			entID, cfg.AllowedDomains).Scan(&clash); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to save SSO settings"}})
			return
		}
		if clash {
			c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "One of the email domains is already used by another organization"}})
			return
		}
	}

	if cfg.AllowedDomains == nil {
		cfg.AllowedDomains = []string{} // the column is NOT NULL
	}
	userID := c.GetString("user_id")
	if err := r.db.Exec(ctx,
		`INSERT INTO enterprise_sso_configs
		   (enterprise_id, enabled, issuer, client_id, client_secret_enc, scopes, allowed_email_domains,
		    default_role, jit_provisioning, enforce_sso, updated_by)
		 VALUES ($1::uuid, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''), $6, $7::text[], $8, $9, $10, $11::uuid)
		 ON CONFLICT (enterprise_id) DO UPDATE SET
		   enabled = EXCLUDED.enabled, issuer = EXCLUDED.issuer, client_id = EXCLUDED.client_id,
		   client_secret_enc = EXCLUDED.client_secret_enc, scopes = EXCLUDED.scopes,
		   allowed_email_domains = EXCLUDED.allowed_email_domains, default_role = EXCLUDED.default_role,
		   jit_provisioning = EXCLUDED.jit_provisioning, enforce_sso = EXCLUDED.enforce_sso,
		   updated_by = EXCLUDED.updated_by, updated_at = NOW()`,
		entID, cfg.Enabled, cfg.Issuer, cfg.ClientID, cfg.ClientSecretEnc, cfg.Scopes, cfg.AllowedDomains,
		cfg.DefaultRole, cfg.JIT, cfg.EnforceSSO, userID); err != nil {
		r.logger.Error("Failed to save SSO config", zap.String("enterprise_id", entID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to save SSO settings"}})
		return
	}
	// Drop cached discovery/keys so an issuer change takes effect immediately.
	if old != nil && old.Issuer != "" {
		r.sso().Forget(old.Issuer)
	}
	r.sso().Forget(cfg.Issuer)

	r.auditSSOChange(c, entID, "sso_config_updated", old, &cfg, secretChanged)
	saved, _ := loadSSOConfig(ctx, r.db, entID)
	out := r.ssoView(entID, saved)
	c.JSON(http.StatusOK, gin.H{"success": true, "sso": out})
}

// DELETE /api/enterprise/sso[?enterprise_id=<uuid>]
func (r *EnterpriseRoutes) deleteEnterpriseSSO(c *gin.Context) {
	entID, ok := r.resolveRetentionTarget(c, c.Query("enterprise_id"))
	if !ok {
		return
	}
	ctx := c.Request.Context()
	old, err := loadSSOConfig(ctx, r.db, entID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to load SSO settings"}})
		return
	}
	if old == nil {
		c.JSON(http.StatusOK, gin.H{"success": true, "deleted": false})
		return
	}
	if err := r.db.Exec(ctx, `DELETE FROM enterprise_sso_configs WHERE enterprise_id = $1::uuid`, entID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to delete SSO settings"}})
		return
	}
	if old.Issuer != "" {
		r.sso().Forget(old.Issuer)
	}
	r.auditSSOChange(c, entID, "sso_config_deleted", old, nil, false)
	c.JSON(http.StatusOK, gin.H{"success": true, "deleted": true})
}

// POST /api/enterprise/sso/test[?enterprise_id=<uuid>] - discovery + JWKS only.
// Failures return stable codes, never the provider's or the network's text:
// this endpoint makes a server-side request to an admin-supplied URL and must
// not become an oracle for the internal network.
func (r *EnterpriseRoutes) testEnterpriseSSO(c *gin.Context) {
	entID, ok := r.resolveRetentionTarget(c, c.Query("enterprise_id"))
	if !ok {
		return
	}
	ctx := c.Request.Context()
	cfg, err := loadSSOConfig(ctx, r.db, entID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to load SSO settings"}})
		return
	}
	if cfg == nil || cfg.Issuer == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "SSO is not configured"}})
		return
	}
	client := r.sso()
	client.Forget(cfg.Issuer) // always test against the live provider, not the cache
	result := gin.H{"ok": false}
	md, err := client.Discover(ctx, cfg.Issuer)
	switch {
	case err != nil:
		result["error"] = ssoProbeCode(err, "discovery_failed")
	default:
		n, kerr := client.KeyCount(ctx, md.JWKSURI)
		if kerr != nil {
			result["error"] = ssoProbeCode(kerr, "jwks_failed")
		} else {
			result = gin.H{
				"ok": true, "issuer": md.Issuer, "jwks_keys": n,
				"pkce_s256":    len(md.CodeChallengeMethods) == 0 || containsString(md.CodeChallengeMethods, "S256"),
				"redirect_uri": ssoRedirectURI(r.cfg.App.BaseURL),
			}
		}
	}
	if r.audit != nil {
		level, msg := "info", "SSO connection test succeeded"
		if result["ok"] != true {
			level, msg = "warning", fmt.Sprintf("SSO connection test failed (%v)", result["error"])
		}
		r.audit.LogStructured(ctx, c.GetString("user_id"), c.GetString("email"), "security", "sso_config_tested", level, msg,
			"enterprise", entID, nil, map[string]interface{}{"ok": result["ok"], "error": result["error"]})
	}
	c.JSON(http.StatusOK, result)
}

// ssoProbeCode maps an internal error to a short stable code.
func ssoProbeCode(err error, fallback string) string {
	switch {
	case errors.Is(err, sso.ErrBlockedAddress):
		return "address_not_allowed"
	case errors.Is(err, sso.ErrInsecureURL):
		return "https_required"
	case errors.Is(err, sso.ErrInvalidURL):
		return "invalid_url"
	case errors.Is(err, sso.ErrTooLarge):
		return "response_too_large"
	}
	return fallback
}

func ssoURLProblem(err error) string {
	switch {
	case errors.Is(err, sso.ErrInsecureURL):
		return "Issuer must use https"
	case errors.Is(err, sso.ErrBlockedAddress):
		return "Issuer points to an address that is not allowed"
	}
	return "Issuer must be a valid https URL"
}

// auditSSOChange writes the platform audit event and the enterprise audit entry.
// The secret itself is never recorded, only whether it was replaced.
func (r *EnterpriseRoutes) auditSSOChange(c *gin.Context, entID, action string, old, cur *ssoConfig, secretChanged bool) {
	snapshot := func(s *ssoConfig) map[string]interface{} {
		if s == nil {
			return nil
		}
		return map[string]interface{}{
			"enabled": s.Enabled, "issuer": s.Issuer, "client_id": s.ClientID, "client_secret_set": s.ClientSecretEnc != "",
			"scopes": s.Scopes, "allowed_email_domains": s.AllowedDomains, "default_role": s.DefaultRole,
			"jit_provisioning": s.JIT, "enforce_sso": s.EnforceSSO,
		}
	}
	newVal := snapshot(cur)
	if newVal != nil {
		newVal["client_secret_changed"] = secretChanged
	}
	userID := c.GetString("user_id")
	msg := "Enterprise SSO configuration updated"
	if action == "sso_config_deleted" {
		msg = "Enterprise SSO configuration deleted"
	}
	ctx := c.Request.Context()
	if r.audit != nil {
		r.audit.LogStructured(ctx, userID, c.GetString("email"), "security", action, "warning", msg,
			"enterprise", entID, snapshot(old), newVal)
	}
	writeEnterpriseAudit(ctx, r.db, r.logger, enterpriseAuditEntry{
		EnterpriseID: entID, Category: "settings", ChangeType: action, FieldName: "sso",
		ChangedBy: userID, TargetType: "enterprise", TargetID: entID, Reason: msg,
	})
}

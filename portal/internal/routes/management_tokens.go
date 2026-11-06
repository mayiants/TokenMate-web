package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/config"
	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/middleware"
	"github.com/ai-proxy/ai-portal-go/pkg/audit"
	"github.com/ai-proxy/ai-portal-go/pkg/crypto"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Management tokens (P1): long-lived, revocable, scoped credentials for
// programmatic account management. Distinct from sk- inference keys (no
// inference access) and JWT sessions. P1 is enterprise-scoped; only
// enterprise_admin may issue them, with scopes read:usage and read:pricing.
// The token model keeps user_id so it can later be extended to personal
// (non-enterprise) tokens without a schema change.

const managementTokenPrefix = "mgmt-"

// mgmtValidScopes is the whitelist. Creation rejects any scope outside it,
// so a token can never be granted a capability that isn't yet implemented.
//   - read:*   : read-only (usage, pricing, members)
//   - manage:* : write operations (keys, members) — all enterprise-scoped
var mgmtValidScopes = map[string]bool{
	"read:usage":     true,
	"read:pricing":   true,
	"read:members":   true,
	"manage:keys":    true,
	"manage:members": true,
}

// defaultMgmtTokenTTL is the default lifetime when the caller does not specify
// expires_in_days (365 days).
const defaultMgmtTokenTTLDays = 365

type ManagementTokenRoutes struct {
	db     *db.DB
	cfg    *config.Config
	auth   *middleware.Auth
	logger *zap.Logger
	audit  *audit.Logger
}

func NewManagementTokenRoutes(database *db.DB, cfg *config.Config, auth *middleware.Auth, logger *zap.Logger, auditLog *audit.Logger) *ManagementTokenRoutes {
	return &ManagementTokenRoutes{db: database, cfg: cfg, auth: auth, logger: logger, audit: auditLog}
}

// Register mounts the token self-management endpoints. These are governed by a
// JWT session (enterprise_admin), NOT by a management token — a token cannot
// mint or manage other tokens.
func (r *ManagementTokenRoutes) Register(rg *gin.RouterGroup) {
	g := rg.Use(r.auth.RequireAuth())
	g.POST("", r.requireEnterpriseAdmin(), r.createToken)
	g.GET("", r.requireEnterpriseAdmin(), r.listTokens)
	g.POST("/:id/revoke", r.requireEnterpriseAdmin(), r.revokeToken)
	g.DELETE("/:id", r.requireEnterpriseAdmin(), r.deleteToken)
}

// requireEnterpriseAdmin gates token management to enterprise admins (and
// platform admins). It resolves the caller's enterprise_id from the DB and
// stashes it for the handlers.
func (r *ManagementTokenRoutes) requireEnterpriseAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		role := c.GetString("role")
		if role != "enterprise_admin" && role != "system_admin" && role != "super_admin" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Only enterprise administrators can manage management tokens"}})
			return
		}
		var entID string
		_ = r.db.QueryRow(c.Request.Context(),
			`SELECT COALESCE(enterprise_id::text, '') FROM users WHERE id = $1::uuid AND deleted_at IS NULL`,
			c.GetString("user_id"),
		).Scan(&entID)
		if entID == "" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Management tokens require an enterprise account"}})
			return
		}
		c.Set("mgmt_enterprise_id", entID)
		c.Next()
	}
}

func (r *ManagementTokenRoutes) createToken(c *gin.Context) {
	var req struct {
		Name          string   `json:"name"`
		Scopes        []string `json:"scopes"`
		ExpiresInDays *int     `json:"expires_in_days"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "name is required"}})
		return
	}
	if len(req.Scopes) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "at least one scope is required (read:usage, read:pricing)"}})
		return
	}
	for _, s := range req.Scopes {
		if !mgmtValidScopes[s] {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf("unsupported scope: %s (allowed: read:usage, read:pricing)", s)}})
			return
		}
	}
	ttlDays := defaultMgmtTokenTTLDays
	if req.ExpiresInDays != nil {
		if *req.ExpiresInDays <= 0 || *req.ExpiresInDays > 3650 {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "expires_in_days must be between 1 and 3650"}})
			return
		}
		ttlDays = *req.ExpiresInDays
	}

	userID := c.GetString("user_id")
	enterpriseID := c.GetString("mgmt_enterprise_id")

	// Generate the token: mgmt-<uuid>. Plaintext is returned once and never stored.
	rawToken := managementTokenPrefix + uuid.NewString()
	tokenHash := crypto.HashKeySHA256(rawToken)
	tokenPrefix := rawToken
	if len(rawToken) > 12 {
		tokenPrefix = rawToken[:12] + "..."
	}
	scopesJSON, _ := json.Marshal(req.Scopes)
	expiresAt := time.Now().AddDate(0, 0, ttlDays)

	var id string
	err := r.db.QueryRow(c.Request.Context(),
		`INSERT INTO management_tokens (token_hash, token_prefix, name, user_id, enterprise_id, scopes, expires_at)
		 VALUES ($1, $2, $3, $4::uuid, $5::uuid, $6::jsonb, $7)
		 RETURNING id::text`,
		tokenHash, tokenPrefix, strings.TrimSpace(req.Name), userID, enterpriseID, string(scopesJSON), expiresAt,
	).Scan(&id)
	if err != nil {
		r.logger.Error("create management token failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to create token"}})
		return
	}

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), userID, c.GetString("email"), "management_token", "token_created", "warning",
			fmt.Sprintf("Management token '%s' created (scopes=%v)", req.Name, req.Scopes))
	}

	c.JSON(http.StatusOK, gin.H{
		"id":         id,
		"token":      rawToken, // shown once
		"prefix":     tokenPrefix,
		"name":       req.Name,
		"scopes":     req.Scopes,
		"expires_at": expiresAt,
	})
}

func (r *ManagementTokenRoutes) listTokens(c *gin.Context) {
	enterpriseID := c.GetString("mgmt_enterprise_id")
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT id::text, token_prefix, name, scopes, status, last_used_at, expires_at, created_at
		 FROM management_tokens
		 WHERE enterprise_id = $1::uuid AND deleted_at IS NULL
		 ORDER BY created_at DESC`,
		enterpriseID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list tokens"}})
		return
	}
	defer rows.Close()
	tokens := []gin.H{}
	for rows.Next() {
		var id, prefix, name, status string
		var scopesRaw []byte
		var lastUsed, expiresAt, createdAt interface{}
		if err := rows.Scan(&id, &prefix, &name, &scopesRaw, &status, &lastUsed, &expiresAt, &createdAt); err != nil {
			continue
		}
		var scopes []string
		_ = json.Unmarshal(scopesRaw, &scopes)
		tokens = append(tokens, gin.H{
			"id": id, "prefix": prefix, "name": name, "scopes": scopes,
			"status": status, "last_used_at": lastUsed, "expires_at": expiresAt, "created_at": createdAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"tokens": tokens})
}

func (r *ManagementTokenRoutes) revokeToken(c *gin.Context) {
	r.setStatus(c, "revoked")
}

func (r *ManagementTokenRoutes) deleteToken(c *gin.Context) {
	enterpriseID := c.GetString("mgmt_enterprise_id")
	id := c.Param("id")
	ct, err := r.db.Pool.Exec(c.Request.Context(),
		`UPDATE management_tokens SET deleted_at = NOW(), status = 'revoked', updated_at = NOW()
		 WHERE id = $1::uuid AND enterprise_id = $2::uuid AND deleted_at IS NULL`,
		id, enterpriseID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to delete token"}})
		return
	}
	if ct.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Token not found"}})
		return
	}
	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "management_token", "token_deleted", "warning",
			"Management token deleted: "+id)
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *ManagementTokenRoutes) setStatus(c *gin.Context, status string) {
	enterpriseID := c.GetString("mgmt_enterprise_id")
	id := c.Param("id")
	ct, err := r.db.Pool.Exec(c.Request.Context(),
		`UPDATE management_tokens SET status = $1, updated_at = NOW()
		 WHERE id = $2::uuid AND enterprise_id = $3::uuid AND deleted_at IS NULL`,
		status, id, enterpriseID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update token"}})
		return
	}
	if ct.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Token not found"}})
		return
	}
	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "management_token", "token_"+status, "warning",
			"Management token "+status+": "+id)
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ---------------------------------------------------------------------------
// Management-token authentication (for endpoints consumed BY a mgmt token)
// ---------------------------------------------------------------------------

// mgmtTokenContext holds the resolved identity of an authenticated mgmt token.
type mgmtTokenContext struct {
	TokenID      string
	UserID       string
	EnterpriseID string
	Scopes       []string
}

// AuthenticateManagementToken looks up a "mgmt-" bearer token, validates it is
// active and unexpired, and returns its context. Returns nil when the header is
// not a management token (so callers can fall through to other auth) and an
// error string when it is a mgmt token but invalid.
func (r *ManagementTokenRoutes) authenticate(ctx context.Context, authHeader string) (*mgmtTokenContext, bool) {
	raw := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	if !strings.HasPrefix(raw, managementTokenPrefix) {
		return nil, false
	}
	hash := crypto.HashKeySHA256(raw)
	var mc mgmtTokenContext
	var scopesRaw []byte
	var expiresAt *time.Time
	// Re-validate the OWNER on every request: a management token must stop
	// working the moment its creator is offboarded/demoted/suspended, otherwise
	// it keeps full manage:* power over the enterprise long after the person
	// lost UI access (tokens default to a 365-day TTL). Mirrors the creation
	// guard requireEnterpriseAdmin (users.role='enterprise_admin'). We require:
	//   - token active + not expired (status/deleted_at/expires_at)
	//   - owner not deleted and users.status='active'
	//   - the token's enterprise is active and not deleted
	//   - owner is a platform admin, OR an enterprise_admin who is still an
	//     active (non-removed) admin member of the token's enterprise
	err := r.db.QueryRow(ctx,
		`SELECT t.id::text, t.user_id::text, COALESCE(t.enterprise_id::text, ''), t.scopes, t.expires_at
		 FROM management_tokens t
		 JOIN users u ON u.id = t.user_id AND u.deleted_at IS NULL AND u.status = 'active'
		 JOIN enterprises e ON e.id = t.enterprise_id AND e.deleted_at IS NULL AND e.status = 'active'
		 WHERE t.token_hash = $1 AND t.status = 'active' AND t.deleted_at IS NULL
		   AND (
		     u.role IN ('super_admin', 'admin')
		     OR (
		       u.role = 'enterprise_admin'
		       AND EXISTS (
		         SELECT 1 FROM enterprise_members m
		         WHERE m.enterprise_id = t.enterprise_id
		           AND m.user_id = t.user_id
		           AND m.removed_at IS NULL
		           AND m.role = 'admin'
		       )
		     )
		   )`,
		hash,
	).Scan(&mc.TokenID, &mc.UserID, &mc.EnterpriseID, &scopesRaw, &expiresAt)
	if err != nil {
		return nil, true // is a mgmt token, but not found/active/owner-ineligible
	}
	if expiresAt != nil && time.Now().After(*expiresAt) {
		return nil, true // expired
	}
	_ = json.Unmarshal(scopesRaw, &mc.Scopes)
	// Best-effort last_used update (non-blocking, ignore errors).
	go func(id string) {
		bg, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = r.db.Pool.Exec(bg, `UPDATE management_tokens SET last_used_at = NOW() WHERE id = $1::uuid`, id)
	}(mc.TokenID)
	return &mc, true
}

// RevokeManagementTokensForUser immediately revokes (status='revoked' + soft
// delete) all active management tokens owned by a user, optionally scoped to a
// single enterprise. Call this on offboarding/role-loss events (member removal,
// quit, demote, enterprise reassignment, user/enterprise suspension) so a token
// cannot keep administering the enterprise after its creator loses access. This
// is defense-in-depth alongside authenticate()'s per-request owner re-validation.
// Pass enterpriseID="" to revoke across all enterprises (e.g. user suspension).
func RevokeManagementTokensForUser(ctx context.Context, database *db.DB, logger *zap.Logger, userID, enterpriseID string) {
	if database == nil || userID == "" {
		return
	}
	var sql string
	var args []any
	if enterpriseID != "" {
		sql = `UPDATE management_tokens SET status = 'revoked', deleted_at = NOW(), updated_at = NOW()
			 WHERE user_id = $1::uuid AND enterprise_id = $2::uuid AND status = 'active' AND deleted_at IS NULL`
		args = []any{userID, enterpriseID}
	} else {
		sql = `UPDATE management_tokens SET status = 'revoked', deleted_at = NOW(), updated_at = NOW()
			 WHERE user_id = $1::uuid AND status = 'active' AND deleted_at IS NULL`
		args = []any{userID}
	}
	ct, err := database.Pool.Exec(ctx, sql, args...)
	if err != nil {
		if logger != nil {
			logger.Warn("Failed to revoke management tokens for user",
				zap.String("user_id", userID), zap.String("enterprise_id", enterpriseID), zap.Error(err))
		}
		return
	}
	if logger != nil && ct.RowsAffected() > 0 {
		logger.Info("Revoked management tokens on offboarding/role change",
			zap.String("user_id", userID), zap.String("enterprise_id", enterpriseID),
			zap.Int64("revoked", ct.RowsAffected()))
	}
}

// RequireManagementScope returns middleware that authenticates a mgmt token and
// enforces that it carries the required scope. On success it injects
// mgmt_user_id / mgmt_enterprise_id / mgmt_scopes into the context so the
// downstream handler can scope its query to the token's enterprise.
func (r *ManagementTokenRoutes) RequireManagementScope(scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		mc, isMgmt := r.authenticate(c.Request.Context(), c.GetHeader("Authorization"))
		if !isMgmt {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "A management token is required", "type": "authentication_error"}})
			return
		}
		if mc == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "Invalid or expired management token", "type": "authentication_error"}})
			return
		}
		if !mgmtHasScope(mc.Scopes, scope) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "management token lacks required scope: " + scope}})
			return
		}
		c.Set("mgmt_user_id", mc.UserID)
		c.Set("mgmt_enterprise_id", mc.EnterpriseID)
		c.Set("mgmt_token_id", mc.TokenID)
		c.Set("mgmt_scopes", mc.Scopes)
		c.Next()
	}
}

func mgmtHasScope(scopes []string, want string) bool {
	for _, s := range scopes {
		if s == want {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Endpoints consumed BY a management token (mgmt- bearer + scope)
// ---------------------------------------------------------------------------

// RegisterConsumers mounts the read-only endpoints a management token can call.
// Each is gated by RequireManagementScope so the token must carry the matching
// scope, and every query is scoped to the token's own enterprise_id.
func (r *ManagementTokenRoutes) RegisterConsumers(rg *gin.RouterGroup) {
	rg.GET("/usage", r.RequireManagementScope("read:usage"), r.mgmtUsage)
	rg.GET("/pricing", r.RequireManagementScope("read:pricing"), r.mgmtPricing)

	// P2: manage:keys — enterprise key lifecycle (all scoped to the token's enterprise)
	rg.GET("/keys", r.RequireManagementScope("manage:keys"), r.mgmtListKeys)
	rg.POST("/keys", r.RequireManagementScope("manage:keys"), r.mgmtCreateKey)
	rg.POST("/keys/:id/disable", r.RequireManagementScope("manage:keys"), r.mgmtDisableKey)
	rg.DELETE("/keys/:id", r.RequireManagementScope("manage:keys"), r.mgmtDeleteKey)

	// P3: read:members / manage:members
	rg.GET("/members", r.RequireManagementScope("read:members"), r.mgmtListMembers)
	rg.PUT("/members/:userId/quota-limit", r.RequireManagementScope("manage:members"), r.mgmtSetMemberQuota)
	rg.PUT("/members/:userId/credit-limit", r.RequireManagementScope("manage:members"), r.mgmtSetMemberCredit)
	rg.DELETE("/members/:userId", r.RequireManagementScope("manage:members"), r.mgmtRemoveMember)
}

// mgmtUsage returns an enterprise usage summary for the token's enterprise.
// Optional query: days (default 30, max 365).
func (r *ManagementTokenRoutes) mgmtUsage(c *gin.Context) {
	entID := c.GetString("mgmt_enterprise_id")
	if entID == "" {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "token is not bound to an enterprise"}})
		return
	}
	days := 30
	if d := c.Query("days"); d != "" {
		if n, err := parseIntClamp(d, 1, 365); err == nil {
			days = n
		}
	}

	var totalReqs int64
	var totalTokens, totalCostMicro int64
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*), COALESCE(SUM(ur.input_tokens+ur.output_tokens),0), COALESCE(SUM(ur.total_cost),0)
		 FROM usage_records ur
		 WHERE ur.user_api_key_id IN (
		     SELECT id FROM api_keys WHERE enterprise_id = $1::uuid AND key_type = 'enterprise'
		 ) AND ur.created_at > NOW() - INTERVAL '1 day' * $2`,
		entID, days,
	).Scan(&totalReqs, &totalTokens, &totalCostMicro)

	// Per-model breakdown (top 20).
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT ur.model, COUNT(*), COALESCE(SUM(ur.input_tokens+ur.output_tokens),0), COALESCE(SUM(ur.total_cost),0)
		 FROM usage_records ur
		 WHERE ur.user_api_key_id IN (
		     SELECT id FROM api_keys WHERE enterprise_id = $1::uuid AND key_type = 'enterprise'
		 ) AND ur.created_at > NOW() - INTERVAL '1 day' * $2
		 GROUP BY ur.model ORDER BY SUM(ur.total_cost) DESC LIMIT 20`,
		entID, days)
	models := []gin.H{}
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var model string
			var reqs, tok, cost int64
			if rows.Scan(&model, &reqs, &tok, &cost) == nil {
				models = append(models, gin.H{"model": model, "requests": reqs, "tokens": tok, "cost_micro_usd": cost})
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"period_days":    days,
		"total_requests": totalReqs,
		"total_tokens":   totalTokens,
		"total_cost_usd": float64(totalCostMicro) / 1e6,
		"by_model":       models,
	})
}

// mgmtPricing returns the price book for the token's enterprise: models from
// the enterprise's groups with their active pricing-rule prices.
func (r *ManagementTokenRoutes) mgmtPricing(c *gin.Context) {
	entID := c.GetString("mgmt_enterprise_id")
	if entID == "" {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "token is not bound to an enterprise"}})
		return
	}
	// Union of models across the enterprise's groups (empty = show nothing to
	// avoid leaking the whole catalog).
	var models []string
	mRows, err := r.db.Query(c.Request.Context(),
		`SELECT DISTINCT jsonb_array_elements_text(models) FROM user_groups
		 WHERE enterprise_id = $1::uuid AND deleted_at IS NULL AND status = 'active'`, entID)
	if err == nil {
		defer mRows.Close()
		for mRows.Next() {
			var m string
			if mRows.Scan(&m) == nil && m != "" {
				models = append(models, m)
			}
		}
	}
	if len(models) == 0 {
		c.JSON(http.StatusOK, gin.H{"data": []gin.H{}})
		return
	}

	rows, err := r.db.Query(c.Request.Context(),
		`SELECT DISTINCT ON (sm.api_model_name)
			sm.api_model_name,
			mpr.billing_mode,
			mpr.prices->>'input', mpr.prices->>'output',
			mpr.prices->>'fixed_request', mpr.prices->'variants'
		 FROM service_models sm
		 LEFT JOIN model_pricing_rules mpr ON mpr.status='active'
		   AND (mpr.effective_to IS NULL OR mpr.effective_to > NOW())
		   AND sm.provider = mpr.provider
		   AND (sm.api_model_name = mpr.api_model_name OR sm.product_id = mpr.product_id)
		 WHERE sm.status IN ('active','eol_pending') AND sm.deleted_at IS NULL
		   AND (sm.api_model_name = ANY($1) OR sm.product_id = ANY($1))
		 ORDER BY sm.api_model_name, COALESCE((mpr.prices->>'input')::numeric, 999999) ASC`,
		models)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to query pricing"}})
		return
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var model string
		var billingMode, inp, out2, fixed *string
		var variants []byte
		if rows.Scan(&model, &billingMode, &inp, &out2, &fixed, &variants) != nil {
			continue
		}
		entry := gin.H{"model": model, "currency": "USD"}
		if billingMode != nil {
			entry["billing_mode"] = *billingMode
		}
		entry["input_price"] = parsePriceStr(inp)
		entry["output_price"] = parsePriceStr(out2)
		entry["fixed_request_price"] = parsePriceStr(fixed)
		if len(variants) > 0 && string(variants) != "null" {
			var vs []gin.H
			if json.Unmarshal(variants, &vs) == nil && len(vs) > 0 {
				entry["price_variants"] = vs
			}
		}
		out = append(out, entry)
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}

// parseIntClamp parses s and clamps to [lo, hi].
func parseIntClamp(s string, lo, hi int) (int, error) {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return 0, err
	}
	if n < lo {
		n = lo
	}
	if n > hi {
		n = hi
	}
	return n, nil
}

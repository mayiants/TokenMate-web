package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/config"
	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/middleware"
	"github.com/ai-proxy/ai-portal-go/internal/services"
	"github.com/ai-proxy/ai-portal-go/pkg/audit"
	"github.com/ai-proxy/ai-portal-go/pkg/jwt"
	"github.com/ai-proxy/ai-portal-go/pkg/mail"
	"github.com/ai-proxy/ai-portal-go/pkg/totp"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

const (
	mfaTestEncKey   = "mfa-test-encryption-key-0123456789abcdef"
	mfaTestPassword = "Sup3r$ecret-pw"
)

// mfaEnv is a full router (auth, MFA, enterprise security, admin reset,
// platform settings) on the scratch Postgres plus an in-memory Redis.
type mfaEnv struct {
	t      *testing.T
	d      *db.DB
	mr     *miniredis.Miniredis
	mgr    *jwt.Manager
	auth   *middleware.Auth
	ar     *AuthRoutes
	mfa    *MFAService
	ent    *EnterpriseRoutes // also used by the SSO tests
	router *gin.Engine
	audit  *audit.Logger
	suffix string
	entA   string
	entB   string
	clock  time.Time
}

func newMFAEnv(t *testing.T) *mfaEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)

	// Failure padding exists to equalise timing in production; tests do not wait.
	oldPad, oldNow := mfaMinFailureDuration, mfaNow
	mfaMinFailureDuration = 0
	t.Cleanup(func() { mfaMinFailureDuration, mfaNow = oldPad, oldNow })

	d := testEnterpriseDB(t)
	mr := miniredis.RunT(t)
	d.Redis = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = d.Redis.Close() })

	cfg := &config.Config{Auth: config.AuthConfig{
		JWTSecret: "test-secret-key-at-least-32-chars!!", JWTExpires: time.Hour, EncryptionKey: mfaTestEncKey,
	}}
	logger := zap.NewNop()
	mgr := jwt.NewManager(cfg.Auth.JWTSecret, cfg.Auth.JWTExpires)
	auth := middleware.NewAuthFromManager(mgr, cfg, logger)
	auth.SetDB(d)
	al := audit.NewLogger(d, logger)

	e := &mfaEnv{
		t: t, d: d, mr: mr, mgr: mgr, auth: auth, audit: al,
		suffix: fmt.Sprintf("%d", time.Now().UnixNano()),
		clock:  time.Now(),
	}
	// A controllable clock lets one test spend several distinct 30s steps.
	mfaNow = func() time.Time { return e.clock }

	e.ar = NewAuthRoutes(d, cfg, logger, auth, al, &mail.Sender{}, services.NewRewardService(d, logger, nil))
	e.mfa = e.ar.MFA()
	mfaRoutes := NewMFARoutes(d, cfg, auth, logger, al)
	er := NewEnterpriseRoutes(d, cfg, logger, auth, al, &mail.Sender{}, nil, services.NewRewardService(d, logger, nil), nil)
	e.ent = er
	adminRoutes := NewAdminRoutes(d, cfg, logger, auth, al, nil, &mail.Sender{})
	settings := NewSettingsRoutes(d, auth, &mail.Sender{}, config.SMTPConfig{}, mfaTestEncKey, logger).WithAudit(al)
	apiKeys := NewAPIKeyRoutes(d, cfg, logger, auth, al)
	webhooks := NewWebhookRoutes(d, cfg, logger, &mail.Sender{}, nil, nil)

	r := gin.New()
	r.Use(middleware.AuditClientInfo())
	api := r.Group("/api")
	e.ar.Register(api.Group("/auth"))
	mfaRoutes.Register(api.Group("/profile/mfa"))
	// The full enterprise + profile route sets are registered so Gin would panic
	// here on any path conflict with the new endpoints.
	er.Register(api)
	NewProfileRoutes(d, auth, logger, al, &mail.Sender{}).Register(api.Group("/profile"))
	adminRoutes.Register(api.Group("/admin"))
	settings.Register(api.Group("/admin/settings"))
	apiKeys.Register(api.Group("/keys"))
	webhooks.Register(api.Group("/webhook"))
	// A representative session-protected route that is not part of enrolment.
	api.GET("/gated", auth.RequireAuth(), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	e.router = r

	ctx := context.Background()
	mkEnt := func(label string) string {
		var id string
		require.NoError(t, d.Pool.QueryRow(ctx,
			`INSERT INTO enterprises (name, contact_email, status) VALUES ($1, $2, 'active') RETURNING id::text`,
			"mfa-"+label+"-"+e.suffix, "mfa-"+label+"-"+e.suffix+"@test.com").Scan(&id))
		return id
	}
	e.entA, e.entB = mkEnt("a"), mkEnt("b")

	t.Cleanup(func() {
		al.Stop(ctx)
		for _, st := range []struct {
			sql  string
			args []interface{}
		}{
			{`DELETE FROM system_settings WHERE key = 'mfa_required_roles' AND updated_by IS NOT NULL`, nil},
			{`UPDATE system_settings SET value = '', managed_by = 'seed', updated_by = NULL WHERE key = 'mfa_required_roles'`, nil},
			{`DELETE FROM audit_logs WHERE user_id IN (SELECT id FROM users WHERE email LIKE $1) OR target_id = ANY($2)`, []interface{}{"mfa-" + e.suffix + "-%", []string{e.entA, e.entB}}},
			{`DELETE FROM enterprise_audit_log WHERE enterprise_id = ANY($1::uuid[])`, []interface{}{[]string{e.entA, e.entB}}},
			{`DELETE FROM users WHERE email LIKE $1`, []interface{}{"mfa-" + e.suffix + "-%"}},
			{`DELETE FROM enterprises WHERE id = ANY($1::uuid[])`, []interface{}{[]string{e.entA, e.entB}}},
		} {
			if _, err := d.Pool.Exec(ctx, st.sql, st.args...); err != nil {
				t.Errorf("cleanup %q: %v", st.sql, err)
			}
		}
		d.Close()
	})
	return e
}

type mfaUser struct {
	ID, Email, Role, Ent string
	Secret               string
	Recovery             []string
}

// user creates an account with a known password.
func (e *mfaEnv) user(label, role, ent string) *mfaUser {
	e.t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(mfaTestPassword), bcrypt.MinCost)
	require.NoError(e.t, err)
	// Login lower-cases the address, so fixtures must be lower case too.
	u := &mfaUser{Email: strings.ToLower("mfa-" + e.suffix + "-" + label + "@test.com"), Role: role, Ent: ent}
	require.NoError(e.t, e.d.Pool.QueryRow(context.Background(),
		`INSERT INTO users (name, email, password_hash, role, enterprise_id)
		 VALUES ($1, $2, $3, $4, NULLIF($5, '')::uuid) RETURNING id::text`,
		"MFA "+label, u.Email, string(hash), role, ent).Scan(&u.ID))
	return u
}

// code returns the valid TOTP code for the env clock plus offset steps.
func (u *mfaUser) code(e *mfaEnv, offsetSteps int64) string {
	e.t.Helper()
	c, err := totp.CodeAtStep(u.Secret, totp.Step(e.clock)+offsetSteps)
	require.NoError(e.t, err)
	return c
}

// advance moves the TOTP clock one full step so the next code is fresh.
func (e *mfaEnv) advance() { e.clock = e.clock.Add(time.Duration(totp.Period) * time.Second) }

// enroll drives the real service: setup, then confirm with a valid code.
func (e *mfaEnv) enroll(u *mfaUser) {
	e.t.Helper()
	ctx := context.Background()
	secret, uri, err := e.mfa.BeginSetup(ctx, u.ID)
	require.NoError(e.t, err)
	require.True(e.t, strings.HasPrefix(uri, "otpauth://totp/"))
	u.Secret = secret
	codes, err := e.mfa.ConfirmEnable(ctx, u.ID, u.code(e, 0))
	require.NoError(e.t, err)
	require.Len(e.t, codes, 10)
	u.Recovery = codes
	e.advance() // the enrolment code's step is spent; later logins need a fresh one
}

type resp struct {
	Code int
	Body map[string]interface{}
	Raw  string
	Hdr  http.Header
}

func (r resp) errCode() string {
	if m, ok := r.Body["error"].(map[string]interface{}); ok {
		s, _ := m["code"].(string)
		return s
	}
	return ""
}

func (r resp) str(k string) string { s, _ := r.Body[k].(string); return s }

// do issues a request. token is sent as a Bearer token; ip sets RemoteAddr.
func (e *mfaEnv) do(method, path, token, ip string, body interface{}) resp {
	e.t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rdr = bytes.NewReader(raw)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if ip == "" {
		ip = "203.0.113.10"
	}
	req.RemoteAddr = ip + ":40000"
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	out := resp{Code: w.Code, Raw: w.Body.String(), Hdr: w.Header()}
	_ = json.Unmarshal(w.Body.Bytes(), &out.Body)
	return out
}

// login performs the password step and returns the response.
func (e *mfaEnv) login(u *mfaUser, ip string) resp {
	return e.do(http.MethodPost, "/api/auth/login", "", ip, map[string]string{"email": u.Email, "password": mfaTestPassword})
}

// fullLogin logs in and finishes MFA when required, returning the session token.
func (e *mfaEnv) fullLogin(u *mfaUser, ip string) string {
	e.t.Helper()
	r := e.login(u, ip)
	require.Equal(e.t, http.StatusOK, r.Code, r.Raw)
	if tok := r.str("token"); tok != "" {
		return tok
	}
	e.advance()
	r2 := e.do(http.MethodPost, "/api/auth/login/mfa", "", ip, map[string]string{"mfa_token": r.str("mfa_token"), "code": u.code(e, 0)})
	require.Equal(e.t, http.StatusOK, r2.Code, r2.Raw)
	return r2.str("token")
}

func (e *mfaEnv) userRow(id string) (enabled bool, secretEnc *string, lastStep int64, tv int) {
	e.t.Helper()
	require.NoError(e.t, e.d.Pool.QueryRow(context.Background(),
		`SELECT totp_enabled, totp_secret_enc, totp_last_step, COALESCE(token_version, 0) FROM users WHERE id = $1::uuid`, id,
	).Scan(&enabled, &secretEnc, &lastStep, &tv))
	return
}

// auditCount polls until the async audit writer has flushed (or times out).
func (e *mfaEnv) auditCount(userID, action string, want int) int {
	e.t.Helper()
	var n int
	deadline := time.Now().Add(5 * time.Second)
	for {
		require.NoError(e.t, e.d.Pool.QueryRow(context.Background(),
			`SELECT COUNT(*) FROM audit_logs WHERE user_id = $1::uuid AND action = $2`, userID, action).Scan(&n))
		if n >= want || time.Now().After(deadline) {
			return n
		}
		time.Sleep(100 * time.Millisecond)
	}
}

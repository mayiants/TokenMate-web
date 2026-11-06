package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/session"
	"github.com/ai-proxy/ai-portal-go/internal/sso"
	"github.com/ai-proxy/ai-portal-go/internal/sso/ssotest"
	"github.com/ai-proxy/ai-portal-go/pkg/crypto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const (
	ssoTestBaseURL   = "https://portal.test"
	ssoSessionCookie = "sso_test_session"
	ssoDomain        = "test.com" // all fixture e-mails end in @test.com
)

// ssoEnv is the MFA test router plus the SSO endpoints, a mock OIDC provider
// and a configured, enabled SSO setup for entA (domain test.com).
type ssoEnv struct {
	*mfaEnv
	idp    *ssotest.Provider
	client *sso.Client
	// domain is unique per test run so leftovers of an aborted run can never
	// make the "one enabled enterprise per domain" rule or discovery ambiguous.
	domain string
}

func newSSOEnv(t *testing.T) *ssoEnv {
	t.Helper()
	m := newMFAEnv(t)
	idp := ssotest.New(t)
	client := sso.NewClient(sso.Options{HTTPClient: idp.HTTPClient()})

	m.ar.cfg.App.BaseURL = ssoTestBaseURL
	m.ar.SetSSOClient(client)
	m.ent.SetSSOClient(client)
	// The web layer owns the real cookie; tests only need a recognisable one.
	m.ar.SetCookieWriter(func(c *gin.Context, token string, _ bool) {
		http.SetCookie(c.Writer, &http.Cookie{Name: ssoSessionCookie, Value: token, Path: "/", HttpOnly: true})
	})
	m.ar.RegisterSSO(m.router)

	e := &ssoEnv{mfaEnv: m, idp: idp, client: client, domain: "sso" + m.suffix + ".example.com"}
	ctx := context.Background()
	// entA: platform-approved auto-join domain + seat room.
	_, err := m.d.Pool.Exec(ctx, `UPDATE enterprises SET allowed_domains = $2, max_members = 50, max_admins = 5 WHERE id = $1::uuid`, m.entA, e.domain)
	require.NoError(t, err)
	e.configure(m.entA, true, false, true)

	t.Cleanup(func() {
		// Registered after newMFAEnv's cleanup, so it runs first: clear rows that
		// JIT provisioning (welcome bonus etc.) hangs off the new users.
		for _, stmt := range []string{
			`DELETE FROM voucher_uses WHERE user_id IN (SELECT id FROM users WHERE email LIKE $1)`,
			`DELETE FROM balance_transactions WHERE user_id IN (SELECT id FROM users WHERE email LIKE $1)`,
		} {
			_, _ = m.d.Pool.Exec(ctx, stmt, "mfa-"+m.suffix+"-%")
		}
		_, _ = m.d.Pool.Exec(ctx, `DELETE FROM audit_logs WHERE details::text LIKE '%' || $1 || '%' OR details::text LIKE '%' || $2 || '%'`, m.entA, m.entB)
		_, _ = m.d.Pool.Exec(ctx, `DELETE FROM user_identities WHERE enterprise_id = ANY($1::uuid[])`, []string{m.entA, m.entB})
	})
	return e
}

// configure writes an enabled configuration straight into the table (the API
// path is covered separately), encrypting the secret exactly like the API.
func (e *ssoEnv) configure(ent string, enabled, enforce, jit bool) {
	e.t.Helper()
	enc, err := crypto.Encrypt(e.idp.ClientSecret, e.ar.cfg.Auth.EncryptionKey)
	require.NoError(e.t, err)
	_, err = e.d.Pool.Exec(context.Background(),
		`INSERT INTO enterprise_sso_configs (enterprise_id, enabled, issuer, client_id, client_secret_enc, allowed_email_domains, jit_provisioning, enforce_sso)
		 VALUES ($1::uuid, $2, $3, $4, $5, ARRAY[$6]::text[], $7, $8)
		 ON CONFLICT (enterprise_id) DO UPDATE SET enabled = $2, issuer = $3, client_id = $4, client_secret_enc = $5,
		   allowed_email_domains = ARRAY[$6]::text[], jit_provisioning = $7, enforce_sso = $8`,
		ent, enabled, e.idp.Issuer, e.idp.ClientID, enc, e.domain, jit, enforce)
	require.NoError(e.t, err)
}

// user shadows mfaEnv.user: fixture accounts get an address in this test's SSO
// domain, which is what the IdP will assert for them.
func (e *ssoEnv) user(label, role, ent string) *mfaUser {
	e.t.Helper()
	u := e.mfaEnv.user(label, role, ent)
	u.Email = "mfa-" + e.suffix + "-" + label + "@" + e.domain
	e.sqlExec(`UPDATE users SET email = $2 WHERE id = $1::uuid`, u.ID, u.Email)
	return u
}

func (e *ssoEnv) sqlExec(sql string, args ...interface{}) {
	e.t.Helper()
	_, err := e.d.Pool.Exec(context.Background(), sql, args...)
	require.NoError(e.t, err, sql)
}

// member creates a user who is a full member of ent (users.enterprise_id and an
// enterprise_members row), the state SSO linking requires.
func (e *ssoEnv) member(label, role, ent, memberRole string) *mfaUser {
	e.t.Helper()
	u := e.user(label, role, ent)
	// users.enterprise_id already made a row through the sync trigger; pin its role.
	e.sqlExec(`INSERT INTO enterprise_members (enterprise_id, user_id, role) VALUES ($1::uuid, $2::uuid, $3)
	           ON CONFLICT (enterprise_id, user_id) DO UPDATE SET role = $3`, ent, u.ID, memberRole)
	return u
}

// idpUser is the identity the provider asserts.
func idpUser(sub, email string) ssotest.User {
	return ssotest.User{Sub: sub, Email: email, EmailVerified: true, Name: "SSO " + sub}
}

type ssoResult struct {
	Start    *httptest.ResponseRecorder
	Callback *httptest.ResponseRecorder
	Location string // callback redirect target
	Session  string // session cookie value, "" when none was issued
	Msg      string // toast message when redirected to /login
}

func (r ssoResult) ok() bool { return r.Session != "" && r.Location == "/dashboard" }

func (e *ssoEnv) raw(method, target string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req.RemoteAddr = "203.0.113.10:40000"
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}

func cookieFrom(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// start performs GET /auth/sso/start and returns the response, the provider
// authorization URL and the binding cookie.
func (e *ssoEnv) start(ent string) (w *httptest.ResponseRecorder, authURL string, bind *http.Cookie) {
	e.t.Helper()
	w = e.raw(http.MethodGet, "/auth/sso/start?enterprise="+ent, nil)
	require.Equal(e.t, http.StatusFound, w.Code)
	return w, w.Header().Get("Location"), cookieFrom(w, sso.CookieName)
}

// callback delivers the provider redirect to the portal like a browser would.
func (e *ssoEnv) callback(callbackURL string, bind *http.Cookie) *httptest.ResponseRecorder {
	u, err := url.Parse(callbackURL)
	require.NoError(e.t, err)
	hdr := map[string]string{}
	if bind != nil {
		hdr["Cookie"] = bind.Name + "=" + bind.Value
	}
	return e.raw(http.MethodGet, u.RequestURI(), hdr)
}

// flow runs a complete login: start -> provider -> callback.
func (e *ssoEnv) flow(ent string, u ssotest.User) ssoResult {
	e.t.Helper()
	sw, authURL, bind := e.start(ent)
	require.NotEmpty(e.t, authURL)
	require.NotNil(e.t, bind, "start must set the binding cookie")
	cb, err := e.idp.Authorize(authURL, u)
	require.NoError(e.t, err)
	return e.finish(sw, cb, bind)
}

func (e *ssoEnv) finish(sw *httptest.ResponseRecorder, cb string, bind *http.Cookie) ssoResult {
	cw := e.callback(cb, bind)
	res := ssoResult{Start: sw, Callback: cw, Location: cw.Header().Get("Location")}
	if c := cookieFrom(cw, ssoSessionCookie); c != nil {
		res.Session = c.Value
	}
	if strings.HasPrefix(res.Location, "/login?") {
		q, _ := url.ParseQuery(strings.TrimPrefix(res.Location, "/login?"))
		res.Msg = q.Get("message")
	}
	return res
}

func (e *ssoEnv) userIDByEmail(email string) string {
	var id string
	_ = e.d.Pool.QueryRow(context.Background(), `SELECT id::text FROM users WHERE email = $1 AND deleted_at IS NULL`, email).Scan(&id)
	return id
}

func (e *ssoEnv) identityCount(email string) int {
	var n int
	require.NoError(e.t, e.d.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM user_identities WHERE email = $1`, email).Scan(&n))
	return n
}

// auditN counts audit rows with the given action for an enterprise, waiting for
// the asynchronous writer.
func (e *ssoEnv) auditN(action, ent string, want int) int {
	e.t.Helper()
	var n int
	deadline := time.Now().Add(5 * time.Second)
	for {
		require.NoError(e.t, e.d.Pool.QueryRow(context.Background(),
			`SELECT COUNT(*) FROM audit_logs WHERE action = $1 AND details->>'enterprise_id' = $2`, action, ent).Scan(&n))
		if n >= want || time.Now().After(deadline) {
			return n
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (e *ssoEnv) sessionUser(token string) string {
	e.t.Helper()
	cl, err := e.mgr.Verify(token)
	require.NoError(e.t, err)
	return cl.UserID
}

// jsonDo is e.do for management calls with the authenticated user's token.
func (e *ssoEnv) asUser(u *mfaUser) string { return e.fullLogin(u, "") }

func mapOf(b map[string]interface{}, k string) map[string]interface{} {
	m, _ := b[k].(map[string]interface{})
	return m
}

type ssoProv = ssotest.Provider

func jsonUnmarshal(s string, v interface{}) error { return json.Unmarshal([]byte(s), v) }

func ssoTTLSeconds(e *ssoEnv, prefix string) int {
	for _, k := range e.mr.Keys() {
		if strings.HasPrefix(k, prefix) {
			return int(e.mr.TTL(k).Seconds())
		}
	}
	return -1
}

func newTestGinContext() (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	return c, w
}

func sessionOptionsNone() session.Options { return session.Options{} }

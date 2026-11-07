package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mayiants/ai-portal-go/internal/config"
	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/internal/middleware"
	"github.com/mayiants/ai-portal-go/internal/testutil"
	"github.com/mayiants/ai-portal-go/pkg/audit"
	"github.com/mayiants/ai-portal-go/pkg/jwt"
	"github.com/mayiants/ai-portal-go/pkg/totp"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

const webMFAPassword = "Sup3r$ecret-pw"

// webMFAEnv needs the scratch Postgres (CI "fast-gate" has none, so it skips there).
func webMFAEnv(t *testing.T) (*Handler, *gin.Engine, *db.DB, string, func(userID string) string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, testutil.PostgresDSN())
	if err == nil {
		err = pool.Ping(ctx)
	}
	if err != nil {
		t.Skipf("PostgreSQL not available for DB-backed web test: %v", err)
	}
	if err := db.InitTestSchema(context.Background(), testutil.PostgresDSN(), zap.NewNop()); err != nil {
		pool.Close()
		t.Fatalf("init schema: %v", err)
	}
	mr := miniredis.RunT(t)
	d := &db.DB{Pool: pool, Redis: redis.NewClient(&redis.Options{Addr: mr.Addr()})}
	t.Cleanup(func() { _ = d.Redis.Close(); pool.Close() })

	cfg := &config.Config{Auth: config.AuthConfig{
		JWTSecret: "test-secret-key-at-least-32-chars!!", JWTExpires: time.Hour,
		EncryptionKey: "web-mfa-test-encryption-key-0123456789abcdef",
	}}
	mgr := jwt.NewManager(cfg.Auth.JWTSecret, cfg.Auth.JWTExpires)
	auth := middleware.NewAuthFromManager(mgr, cfg, zap.NewNop())
	auth.SetDB(d)
	h, err := NewHandler(d, cfg, zap.NewNop(), auth, nil, audit.NewNoopLogger(zap.NewNop()), nil)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	r := gin.New()
	r.POST("/login", h.HandleLogin)
	r.POST("/login/mfa", h.HandleLoginMFA)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	email := "web-mfa-" + suffix + "@test.com"
	hash, _ := bcrypt.GenerateFromPassword([]byte(webMFAPassword), bcrypt.MinCost)
	var uid string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (name, email, password_hash, role) VALUES ('web mfa', $1, $2, 'user') RETURNING id::text`,
		email, string(hash)).Scan(&uid); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1::uuid`, uid) })

	// Enrol through the real service, returning a helper that yields a fresh
	// code (the next step, so the enrolment step is never replayed).
	secret, _, err := h.authRoutes.MFA().BeginSetup(context.Background(), uid)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	cur, _ := totp.CodeAtStep(secret, totp.Step(time.Now()))
	if _, err := h.authRoutes.MFA().ConfirmEnable(context.Background(), uid, cur); err != nil {
		t.Fatalf("enable: %v", err)
	}
	next := func(string) string {
		c, _ := totp.CodeAtStep(secret, totp.Step(time.Now())+1)
		return c
	}
	return h, r, d, email, next
}

func postForm(r http.Handler, path string, form url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "203.0.113.20:5000"
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func cookieNamed(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// The web login form must not set a session cookie after the password alone for
// an MFA user, must carry "remember me" through the second step, and must only
// set the cookie once the code was accepted.
func TestWebLoginRequiresMFAAndHonoursRememberMe(t *testing.T) {
	_, r, _, email, nextCode := webMFAEnv(t)

	w := postForm(r, "/login", url.Values{"email": {email}, "password": {webMFAPassword}, "remember_me": {"on"}})
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/login/mfa" {
		t.Fatalf("password step: %d -> %q, want redirect to /login/mfa", w.Code, w.Header().Get("Location"))
	}
	if cookieNamed(w, JWTCookieName) != nil {
		t.Fatal("no session cookie may be set after the password alone (remember-me must not bypass MFA)")
	}
	pending := cookieNamed(w, mfaPendingCookie)
	if pending == nil || pending.Value == "" || !pending.HttpOnly || pending.MaxAge != 300 {
		t.Fatalf("pending cookie missing/unsafe: %+v", pending)
	}

	// Wrong code: back to the form, still no session.
	bad := postForm(r, "/login/mfa", url.Values{"code": {"000000"}}, pending)
	if bad.Code != http.StatusFound || !strings.HasPrefix(bad.Header().Get("Location"), "/login/mfa?") || cookieNamed(bad, JWTCookieName) != nil {
		t.Fatalf("wrong code: %d -> %q cookie=%v", bad.Code, bad.Header().Get("Location"), cookieNamed(bad, JWTCookieName))
	}

	// Right code: dashboard + 30 day cookie (remember-me issued only now).
	ok := postForm(r, "/login/mfa", url.Values{"code": {nextCode("")}}, pending)
	if ok.Code != http.StatusFound || ok.Header().Get("Location") != "/dashboard" {
		t.Fatalf("right code: %d -> %q", ok.Code, ok.Header().Get("Location"))
	}
	sess := cookieNamed(ok, JWTCookieName)
	if sess == nil || sess.Value == "" || sess.MaxAge != 86400*30 || !sess.HttpOnly {
		t.Fatalf("session cookie wrong: %+v", sess)
	}

	// The pending token is single use.
	again := postForm(r, "/login/mfa", url.Values{"code": {nextCode("")}}, pending)
	if cookieNamed(again, JWTCookieName) != nil {
		t.Fatal("pending cookie replay must not yield a session")
	}
}

func TestWebMFAFormWithoutPendingCookieGoesToLogin(t *testing.T) {
	_, r, _, _, nextCode := webMFAEnv(t)
	w := postForm(r, "/login/mfa", url.Values{"code": {nextCode("")}})
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/login" || cookieNamed(w, JWTCookieName) != nil {
		t.Fatalf("got %d -> %q", w.Code, w.Header().Get("Location"))
	}
	forged := postForm(r, "/login/mfa", url.Values{"code": {nextCode("")}}, &http.Cookie{Name: mfaPendingCookie, Value: "forged-token"})
	if cookieNamed(forged, JWTCookieName) != nil {
		t.Fatal("a forged pending cookie must not yield a session")
	}
}

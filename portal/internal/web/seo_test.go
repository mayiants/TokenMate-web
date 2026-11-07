package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mayiants/ai-portal-go/internal/config"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func init() { gin.SetMode(gin.TestMode) }

func TestIsGatedPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/", false},
		{"/models", false},
		{"/dashboard", true},
		{"/admin", true},
		{"/admin/users", true},
		{"/enterprise/settings", true},
		{"/api/keys", true},
		{"/login", true},
		{"/register", true},
		{"/modelsX", false}, // not a gated prefix, not /models exactly — public-ish, not gated
	}
	for _, tt := range tests {
		if got := isGatedPath(tt.path); got != tt.want {
			t.Errorf("isGatedPath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestNoIndexForGatedPaths(t *testing.T) {
	mw := NoIndexForGatedPaths()
	run := func(path string) string {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, path, nil)
		mw(c)
		return w.Header().Get("X-Robots-Tag")
	}
	if h := run("/dashboard"); h != "noindex, nofollow" {
		t.Errorf("/dashboard X-Robots-Tag = %q, want noindex", h)
	}
	if h := run("/admin/keys"); h != "noindex, nofollow" {
		t.Errorf("/admin/keys X-Robots-Tag = %q, want noindex", h)
	}
	if h := run("/"); h != "" {
		t.Errorf("/ X-Robots-Tag = %q, want empty (indexable)", h)
	}
	if h := run("/models"); h != "" {
		t.Errorf("/models X-Robots-Tag = %q, want empty (indexable)", h)
	}
}

func TestBadBotBlocker(t *testing.T) {
	mw := BadBotBlocker()
	run := func(ua string) int {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		if ua != "" {
			c.Request.Header.Set("User-Agent", ua)
		}
		mw(c)
		return w.Code
	}
	// Named abusive commercial scrapers blocked.
	for _, ua := range []string{"AhrefsBot/7.0", "SemrushBot", "DotBot/1.2", "MJ12bot"} {
		if code := run(ua); code != http.StatusForbidden {
			t.Errorf("bad bot %q: code=%d, want 403", ua, code)
		}
	}
	// Legitimate crawlers + browsers + empty UA + generic HTTP clients
	// (health checks/monitoring/tooling) allowed (200 = not aborted).
	for _, ua := range []string{
		"Mozilla/5.0 (compatible; GPTBot/1.0)",
		"Mozilla/5.0 (compatible; ClaudeBot/1.0)",
		"Mozilla/5.0 (compatible; Googlebot/2.1)",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X)",
		"curl/8.0", "Go-http-client/1.1", "python-requests/2.31",
		"",
	} {
		if code := run(ua); code == http.StatusForbidden {
			t.Errorf("legit/generic UA %q wrongly blocked (403)", ua)
		}
	}
}

func seoTestHandler() *Handler {
	return &Handler{
		cfg:    &config.Config{App: config.AppConfig{BaseURL: "https://api.woodgit.com"}},
		logger: zap.NewNop(),
	}
}

func TestHandleRobotsTxt(t *testing.T) {
	h := seoTestHandler()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/robots.txt", nil)
	h.handleRobotsTxt(c)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	for _, want := range []string{
		"User-agent: GPTBot", "User-agent: ClaudeBot", "User-agent: Google-Extended",
		"Disallow: /admin", "Disallow: /api", "Disallow: /dashboard",
		"Crawl-delay: 10", "Sitemap: https://api.woodgit.com/sitemap.xml",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("robots.txt missing %q\n---\n%s", want, body)
		}
	}
	// Must NOT disallow the public surface.
	if strings.Contains(body, "Disallow: /models") || strings.Contains(body, "Disallow: /\n") {
		t.Errorf("robots.txt must not disallow public pages\n%s", body)
	}
}

func TestHandleSitemapXML(t *testing.T) {
	h := seoTestHandler()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/sitemap.xml", nil)
	h.handleSitemapXML(c)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	for _, want := range []string{
		`<?xml version="1.0"`, "<urlset", "https://api.woodgit.com/</loc>",
		"https://api.woodgit.com/models</loc>", "</urlset>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("sitemap.xml missing %q\n%s", want, body)
		}
	}
	// Must not leak gated URLs (check as path segments after the host, so the
	// api.woodgit.com host itself doesn't false-positive on "/api").
	for _, bad := range []string{".com/admin", ".com/dashboard", ".com/enterprise", ".com/api", ".com/login"} {
		if strings.Contains(body, bad) {
			t.Errorf("sitemap.xml leaks gated path %q", bad)
		}
	}
}

// TestBaseURLFallback verifies request-host fallback when APP_BASE_URL is unset.
func TestBaseURLFallback(t *testing.T) {
	h := &Handler{cfg: &config.Config{}, logger: zap.NewNop()}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "http://portal.local/robots.txt", nil)
	c.Request.Host = "portal.local"
	if got := h.baseURL(c); got != "http://portal.local" {
		t.Errorf("baseURL fallback = %q, want http://portal.local", got)
	}
}

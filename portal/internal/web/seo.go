package web

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// SEO / AI-discoverability + anti-abuse. See
// docs/seo-ai-discoverability-and-anti-abuse-plan-202609131310.md.
//
// Public, indexable surface: "/" (landing) and "/models" (gallery). Everything
// else (dashboard, admin, enterprise, api, auth utility pages) is noindex and
// Disallow'd. AI crawlers (GPTBot/ClaudeBot/PerplexityBot/Google-Extended, ...)
// are explicitly allowed on the public surface, and /llms.txt gives them a
// plain-text platform summary.

// publicIndexablePaths (/, /models) are the only indexable/sitemap paths; they
// are encoded directly in handleSitemapXML and the pages' Indexable flag.

// gatedPathPrefixes are noindex + Disallow'd (authenticated app + API + auth
// utility pages). Prefix match on the request path.
var gatedPathPrefixes = []string{
	"/dashboard", "/apikeys", "/billing", "/billing-details", "/voucher",
	"/invoices", "/profile", "/my-group", "/plan-and-group", "/usage-logs",
	"/status", "/admin", "/enterprise", "/super", "/api",
	"/login", "/register", "/forgot-password", "/logout",
}

// aiCrawlerUAs are explicitly welcomed on the public surface (policy = open).
var aiCrawlerUAs = []string{
	"GPTBot", "ChatGPT-User", "OAI-SearchBot", "ClaudeBot", "Claude-Web",
	"anthropic-ai", "PerplexityBot", "Google-Extended", "Googlebot",
	"Bingbot", "Applebot", "Applebot-Extended", "CCBot", "Bytespider",
	"cohere-ai", "Amazonbot", "DuckDuckBot", "YandexBot",
}

// badBotUAs are abusive/aggressive commercial scrapers that ignore robots and
// consume resources. Conservative, NAMED-crawler list only — matched
// case-insensitively as a substring. Legitimate search/AI crawlers are never
// matched. Deliberately excludes generic HTTP client UAs (curl, Go-http-client,
// python-requests, etc.) because those are used by health checks, uptime
// monitors, internal tooling and legitimate API clients — blocking them causes
// collateral damage. Volumetric abuse from generic clients is handled by the
// per-IP rate limiter instead.
var badBotUAs = []string{
	"AhrefsBot", "SemrushBot", "DotBot", "MJ12bot", "MegaIndex",
	"BLEXBot", "PetalBot", "SeznamBot", "DataForSeoBot", "MauiBot",
	"serpstatbot", "ZoominfoBot", "Barkrowler", "masscan", "zgrab",
}

// isGatedPath reports whether a path is in the noindex/Disallow set.
func isGatedPath(p string) bool {
	if p == "/" {
		return false
	}
	for _, pre := range gatedPathPrefixes {
		if p == pre || strings.HasPrefix(p, pre+"/") || p == pre {
			return true
		}
	}
	return false
}

// baseURL returns the configured public base URL (no trailing slash), falling
// back to the request's scheme+host so robots/sitemap/canonical are correct
// even if APP_BASE_URL is unset.
func (h *Handler) baseURL(c *gin.Context) string {
	if h.cfg != nil && h.cfg.App.BaseURL != "" {
		return strings.TrimRight(h.cfg.App.BaseURL, "/")
	}
	scheme := "https"
	if c.Request.TLS == nil && c.GetHeader("X-Forwarded-Proto") != "https" {
		scheme = "http"
	}
	return scheme + "://" + c.Request.Host
}

// RegisterSEORoutes mounts robots.txt, sitemap.xml, llms.txt.
func (h *Handler) RegisterSEORoutes(r gin.IRouter) {
	r.GET("/robots.txt", h.handleRobotsTxt)
	r.GET("/sitemap.xml", h.handleSitemapXML)
	r.GET("/llms.txt", h.handleLLMsTxt)
}

func (h *Handler) handleRobotsTxt(c *gin.Context) {
	base := h.baseURL(c)
	var b strings.Builder
	// Explicitly welcome known search + AI crawlers on the public surface.
	for _, ua := range aiCrawlerUAs {
		b.WriteString("User-agent: " + ua + "\n")
	}
	b.WriteString("Allow: /\n\n")
	// Everyone else: allowed on public surface, gated paths disallowed,
	// with a gentle crawl-delay to bound abusive pull rate.
	b.WriteString("User-agent: *\n")
	b.WriteString("Allow: /$\n")
	b.WriteString("Allow: /models\n")
	seen := map[string]bool{}
	for _, p := range gatedPathPrefixes {
		if seen[p] {
			continue
		}
		seen[p] = true
		b.WriteString("Disallow: " + p + "\n")
	}
	b.WriteString("Crawl-delay: 10\n\n")
	b.WriteString("Sitemap: " + base + "/sitemap.xml\n")
	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.Header("Cache-Control", "public, max-age=3600")
	c.String(http.StatusOK, b.String())
}

func (h *Handler) handleSitemapXML(c *gin.Context) {
	base := h.baseURL(c)
	today := time.Now().UTC().Format("2006-01-02")
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	entries := []struct {
		path, freq, prio string
	}{
		{"/", "weekly", "1.0"},
		{"/models", "daily", "0.9"},
	}
	for _, e := range entries {
		b.WriteString("  <url><loc>" + base + e.path + "</loc>")
		b.WriteString("<lastmod>" + today + "</lastmod>")
		b.WriteString("<changefreq>" + e.freq + "</changefreq>")
		b.WriteString("<priority>" + e.prio + "</priority></url>\n")
	}
	b.WriteString("</urlset>\n")
	c.Header("Content-Type", "application/xml; charset=utf-8")
	c.Header("Cache-Control", "public, max-age=3600")
	c.String(http.StatusOK, b.String())
}

// handleLLMsTxt serves a plain-text platform summary for LLM crawlers. Model
// info is generated from active, store-visible service models (public,
// non-sensitive: display names only — never upstream provider / key / IDs).
func (h *Handler) handleLLMsTxt(c *gin.Context) {
	base := h.baseURL(c)
	var b strings.Builder
	b.WriteString("# WOODGIT AI API Platform\n\n")
	b.WriteString("> A unified AI API gateway: one OpenAI-compatible endpoint to access many\n")
	b.WriteString("> large language, vision, image and audio models from multiple providers,\n")
	b.WriteString("> with per-key/group rate limits, usage-based billing, and enterprise\n")
	b.WriteString("> management (teams, quotas, programmatic management tokens).\n\n")
	b.WriteString("## Access\n\n")
	b.WriteString("- OpenAI-compatible API: POST /v1/chat/completions, /v1/embeddings,\n")
	b.WriteString("  /v1/images/generations, /v1/audio/* — authenticate with a platform API key.\n")
	b.WriteString("- Model catalog + pricing: " + base + "/models\n")
	b.WriteString("- Sign up: " + base + "/register\n\n")

	// Model overview grouped by type, from the public catalog.
	if h.db != nil {
		rows, err := h.db.Query(c.Request.Context(), `
			SELECT COALESCE(NULLIF(aimodels_data->>'type',''), capabilities->>'type', 'text') AS mtype,
			       COALESCE(NULLIF(api_model_name,''), product_id) AS name
			FROM service_models
			WHERE status IN ('active','eol_pending') AND COALESCE(hidden,false)=false AND show_in_store=true AND deleted_at IS NULL
			ORDER BY mtype, name`)
		if err == nil {
			defer rows.Close()
			byType := map[string][]string{}
			seen := map[string]bool{}
			for rows.Next() {
				var mtype, name string
				if rows.Scan(&mtype, &name) == nil && name != "" && !seen[mtype+"|"+name] {
					seen[mtype+"|"+name] = true
					byType[mtype] = append(byType[mtype], name)
				}
			}
			order := []string{"text", "image", "video", "audio", "embedding", "other"}
			label := map[string]string{"text": "Text / Chat", "image": "Image", "video": "Video", "audio": "Audio", "embedding": "Embedding", "other": "Other"}
			b.WriteString("## Available models\n\n")
			for _, t := range order {
				models := byType[t]
				if len(models) == 0 {
					continue
				}
				sort.Strings(models)
				lbl := label[t]
				if lbl == "" {
					lbl = t
				}
				// Cap the list to keep the file compact.
				shown := models
				extra := 0
				if len(shown) > 60 {
					extra = len(shown) - 60
					shown = shown[:60]
				}
				fmt.Fprintf(&b, "### %s (%d)\n", lbl, len(models))
				b.WriteString(strings.Join(shown, ", "))
				if extra > 0 {
					fmt.Fprintf(&b, ", and %d more", extra)
				}
				b.WriteString("\n\n")
			}
		} else {
			h.logger.Warn("llms.txt model query failed", zap.Error(err))
		}
	}
	b.WriteString("## Pages\n\n")
	b.WriteString("- Home: " + base + "/\n")
	b.WriteString("- Models & pricing: " + base + "/models\n")
	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.Header("Cache-Control", "public, max-age=3600")
	c.String(http.StatusOK, b.String())
}

// NoIndexForGatedPaths sets X-Robots-Tag: noindex,nofollow on non-public paths
// (a robust backstop to the per-page meta robots tag, and the only signal for
// JSON/API responses).
func NoIndexForGatedPaths() gin.HandlerFunc {
	return func(c *gin.Context) {
		if isGatedPath(c.Request.URL.Path) {
			c.Header("X-Robots-Tag", "noindex, nofollow")
		}
		c.Next()
	}
}

// BadBotBlocker returns 403 for known abusive scraper user-agents. Legitimate
// search/AI crawlers are never matched. Empty UA is allowed (many legitimate
// clients send none); the rate limiter handles those.
func BadBotBlocker() gin.HandlerFunc {
	lower := make([]string, len(badBotUAs))
	for i, s := range badBotUAs {
		lower[i] = strings.ToLower(s)
	}
	return func(c *gin.Context) {
		ua := strings.ToLower(c.GetHeader("User-Agent"))
		if ua != "" {
			for _, bad := range lower {
				if strings.Contains(ua, bad) {
					c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Forbidden"}})
					return
				}
			}
		}
		c.Next()
	}
}

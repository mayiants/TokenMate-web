// Package webui serves the React SPA (woodgit-web production build) embedded
// into the portal binary. The build pipeline runs deploy/publish-web.sh,
// which rsyncs web/dist into webui/dist before `go build`.
//
// Routing contract:
//   - /api/* and /backend-api/* stay with the JSON backend (404 JSON on miss)
//   - real files under dist (assets, favicon, ...) are served as-is
//   - every other path falls back to index.html for client-side routing
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

//go:embed all:dist
var dist embed.FS

func assets() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}

var contentTypes = map[string]string{
	".html": "text/html; charset=utf-8",
	".js":   "text/javascript; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".json": "application/json; charset=utf-8",
	".svg":  "image/svg+xml",
	".png":  "image/png",
	".ico":  "image/x-icon",
	".txt":  "text/plain; charset=utf-8",
	".woff2": "font/woff2",
	".woff":  "font/woff",
	".map":  "application/json",
}

// Register installs the SPA fallback on the engine's NoRoute.
func Register(r *gin.Engine) {
	static := assets()
	r.NoRoute(func(c *gin.Context) {
		p := c.Request.URL.Path
		if strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/backend-api/") {
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "not found"}})
			return
		}
		if strings.Contains(p, "..") || p == "" {
			c.Status(http.StatusNotFound)
			return
		}
		name := strings.TrimPrefix(p, "/")
		if data, err := fs.ReadFile(static, name); err == nil && !strings.HasSuffix(p, "/") {
			ct := contentTypes[filepath.Ext(p)]
			if ct == "" {
				ct = "application/octet-stream"
			}
			c.Data(http.StatusOK, ct, data)
			return
		}
		data, err := fs.ReadFile(static, "index.html")
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		c.Data(http.StatusOK, contentTypes[".html"], data)
	})
}

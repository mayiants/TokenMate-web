package web

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/ai-proxy/ai-portal-go/web"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// staticBuildTime is used as the Last-Modified timestamp for embedded static files.
// The Go embed package does not preserve file modification times, so we use the
// process start time as a conservative fallback.
var staticBuildTime = time.Now().UTC().Format(http.TimeFormat)

// RegisterStaticFiles wires the embedded static filesystem into the provided
// Gin router under /static/*filepath. Unknown files return 404.
// Supports conditional requests via ETag and Last-Modified.
func RegisterStaticFiles(router *gin.Engine, logger *zap.Logger) {
	registerEmbedFileServer(router, "/static", "static", logger)
}

// RegisterI18nFiles wires the embedded i18n JSON files into the provided
// Gin router under /i18n/*filepath. Unknown files return 404.
// Supports conditional requests via ETag and Last-Modified.
func RegisterI18nFiles(router *gin.Engine, logger *zap.Logger) {
	registerEmbedFileServer(router, "/i18n", "i18n", logger)
}

// registerEmbedFileServer serves files from a subdirectory of the embedded web.FS.
func registerEmbedFileServer(router *gin.Engine, urlPrefix, embedPath string, logger *zap.Logger) {
	subFS, err := fs.Sub(web.FS, embedPath)
	if err != nil {
		logger.Fatal("Failed to create embedded sub FS", zap.String("path", embedPath), zap.Error(err))
	}

	router.GET(urlPrefix+"/*filepath", func(c *gin.Context) {
		filepath := c.Param("filepath")
		// filepath starts with /, strip it for fs.Open
		if len(filepath) > 0 && filepath[0] == '/' {
			filepath = filepath[1:]
		}
		data, err := fs.ReadFile(subFS, filepath)
		if err != nil {
			logger.Debug("Embedded file not found", zap.String("path", filepath), zap.Error(err))
			c.Status(http.StatusNotFound)
			return
		}

		contentType := contentTypeForPath(filepath)
		sum := sha256.Sum256(data)
		etag := fmt.Sprintf(`"%s"`, hex.EncodeToString(sum[:]))

		// Handle conditional requests.
		if match := c.GetHeader("If-None-Match"); match != "" {
			if strings.Contains(match, etag) || match == "*" {
				c.Status(http.StatusNotModified)
				return
			}
		}

		cacheMaxAge := "31536000"
		if contentType == "text/css" || contentType == "application/javascript" || contentType == "application/json" {
			cacheMaxAge = "3600"
		}
		c.Header("Cache-Control", "public, max-age="+cacheMaxAge)
		c.Header("ETag", etag)
		c.Header("Last-Modified", staticBuildTime)
		c.Data(http.StatusOK, contentType, data)
	})
}

func contentTypeForPath(filepath string) string {
	switch {
	case strings.HasSuffix(filepath, ".css"):
		return "text/css"
	case strings.HasSuffix(filepath, ".js"):
		return "application/javascript"
	case strings.HasSuffix(filepath, ".html"):
		return "text/html"
	case strings.HasSuffix(filepath, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(filepath, ".png"):
		return "image/png"
	case strings.HasSuffix(filepath, ".jpg"), strings.HasSuffix(filepath, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(filepath, ".json"):
		return "application/json"
	case strings.HasSuffix(filepath, ".woff2"):
		return "font/woff2"
	case strings.HasSuffix(filepath, ".woff"):
		return "font/woff"
	default:
		return "application/octet-stream"
	}
}

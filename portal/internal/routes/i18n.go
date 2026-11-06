package routes

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ai-proxy/ai-portal-go/pkg/i18n"
	"github.com/ai-proxy/ai-portal-go/web"
	"github.com/gin-gonic/gin"
)

// routeI18n holds translations loaded at init time
var routeI18n i18n.I18n

func init() {
	i := make(i18n.I18n)
	for _, lang := range []string{"zh", "en"} {
		data, err := web.FS.ReadFile(fmt.Sprintf("i18n/%s.json", lang))
		if err != nil {
			continue
		}
		var dict map[string]string
		if err := json.Unmarshal(data, &dict); err != nil {
			continue
		}
		i[lang] = dict
	}
	routeI18n = i
}

// GetLocale extracts locale from cookie, header, or query param.
// Defaults to "en" if not provided; respects browser Chinese preference.
func GetLocale(c *gin.Context) string {
	// 1. Check locale cookie
	if cookie, err := c.Cookie("locale"); err == nil && (cookie == "zh" || cookie == "en") {
		return cookie
	}
	// 2. Check Accept-Language header or query param
	loc := c.GetHeader("Accept-Language")
	if loc == "" {
		loc = c.Query("locale")
	}
	if loc == "" {
		return "en"
	}
	loc = strings.ToLower(strings.TrimSpace(loc))
	for _, sep := range []string{"-", "_", ",", ";"} {
		if idx := strings.Index(loc, sep); idx > 0 {
			loc = loc[:idx]
		}
	}
	// Chinese browsers stay in zh; everything else falls back to en.
	if loc == "zh" {
		return "zh"
	}
	return "en"
}

// T returns a translated string for the given key based on request locale.
// Falls back to returning the key itself if translation is unavailable.
func T(key string, c *gin.Context) string {
	if routeI18n == nil {
		return key
	}
	return routeI18n.T(key, GetLocale(c))
}

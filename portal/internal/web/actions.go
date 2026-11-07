package web

import (
	"net/http"
	"net/url"

	"github.com/mayiants/ai-portal-go/internal/config"
	"github.com/mayiants/ai-portal-go/internal/routes"
	"github.com/gin-gonic/gin"
)

// --- Profile Actions ---

// HandleUpdateProfileSettings updates locale, theme and currency in DB
func (h *Handler) HandleUpdateProfileSettings(c *gin.Context) {
	userID := c.GetString("user_id")
	locale := c.PostForm("locale")
	theme := c.PostForm("theme")
	currency := c.PostForm("currency")
	timezone := c.PostForm("timezone")

	if locale != "zh" && locale != "en" {
		locale = "en"
	}
	if theme != "light" && theme != "dark" && theme != "system" {
		theme = "system"
	}
	if !config.IsSupportedCurrency(currency) {
		currency = config.DefaultCurrency
	}
	if timezone == "" {
		timezone = "UTC"
	}

	err := routes.UpdateUserPreferences(h.db, c.Request.Context(), userID, locale, theme, currency, timezone)
	q := url.Values{}
	if err != nil {
		q.Set("toast", "error")
		q.Set("message", h.i18n.T("error.save_failed", h.getLocale(c)))
	} else {
		c.SetCookie("locale", locale, 86400*365, "/", "", isSecure(c), false)
		c.SetCookie("theme", theme, 86400*365, "/", "", isSecure(c), false)
		c.SetCookie("currency", currency, 86400*365, "/", "", isSecure(c), false)
		c.SetCookie("timezone", timezone, 86400*365, "/", "", isSecure(c), false)
		q.Set("toast", "success")
		q.Set("message", h.i18n.T("msg.settings_saved", h.getLocale(c)))
	}
	c.Redirect(http.StatusFound, "/profile?"+q.Encode())
}

// --- Admin Actions ---

const quotaPerUSD = 1_000_000

func usdToQuota(usd float64) int64 {
	return int64(usd * float64(quotaPerUSD))
}

func rmbYuanToQuota(yuan float64, rate float64) int64 {
	usd := yuan / rate
	return int64(usd * float64(quotaPerUSD))
}

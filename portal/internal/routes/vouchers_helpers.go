package routes

import (
	"fmt"
	"strconv"

	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/gin-gonic/gin"
)

func generateVoucherCode(prefix string) string {
	randChars := generateRandomCode("ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", voucherCodeRandomLength)
	code := prefix + "-"
	for i := 0; i < voucherCodeRandomLength; i++ {
		if i > 0 && i%4 == 0 {
			code += "-"
		}
		code += string(randChars[i])
	}
	return code
}

func parseVoucherPagination(c *gin.Context, defaultLimit int) (page int, limit int) {
	page = 1
	limit = defaultLimit
	if p, err := strconv.Atoi(c.Query("page")); err == nil && p > 0 {
		page = p
	}
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 && l <= voucherPaginationMaxLimit {
		limit = l
	}
	return
}

func getUserCurrencyAndRate(c *gin.Context, database *db.DB, userID string) (string, float64) {
	var currency string
	_ = database.QueryRow(c.Request.Context(),
		`SELECT COALESCE(preferred_currency, 'USD') FROM users WHERE id = $1::uuid`, userID).Scan(&currency)
	var rateStr string
	_ = database.QueryRow(c.Request.Context(),
		`SELECT value FROM system_settings WHERE key = 'usd_exchange_rate'`).Scan(&rateStr)
	exRate := defaultUSDCNYExchangeRate
	if rateStr != "" {
		var rateCents int
		_, _ = fmt.Sscanf(rateStr, "%d", &rateCents)
		if rateCents > 0 {
			exRate = float64(rateCents) / 100.0
		}
	}
	return currency, exRate
}

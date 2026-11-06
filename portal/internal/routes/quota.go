package routes

import (
	"github.com/ai-proxy/ai-portal-go/internal/services"
)

// Quota unit constants
// 1 quota = 0.000001 USD (micro-dollar)
const QuotaPerUSD = services.QuotaPerUSD

// QuotaToUSD converts internal quota (micro-USD) to USD string
func QuotaToUSD(quota int64) string {
	return services.QuotaToUSD(quota)
}

// QuotaToRMB converts internal quota (micro-USD) to RMB string using exchange rate
func QuotaToRMB(quota int64, rate float64) string {
	return services.QuotaToRMB(quota, rate)
}

// QuotaToUSDFloat converts internal quota to USD float64
func QuotaToUSDFloat(quota int64) float64 {
	return services.QuotaToUSDFloat(quota)
}

// QuotaToRMBFloat converts internal quota to RMB float64 using exchange rate
func QuotaToRMBFloat(quota int64, rate float64) float64 {
	return services.QuotaToRMBFloat(quota, rate)
}

// USDtoQuota converts USD float to internal quota (micro-USD)
func USDtoQuota(usd float64) int64 {
	return services.USDtoQuota(usd)
}

// RMBYuanToQuota converts RMB yuan to internal quota using exchange rate
func RMBYuanToQuota(yuan float64, rate float64) int64 {
	return services.RMBYuanToQuota(yuan, rate)
}

// RMBCentToQuota converts RMB cents to internal quota using exchange rate
func RMBCentToQuota(cents int64, rate float64) int64 {
	return services.RMBCentToQuota(cents, rate)
}

// FormatQuota formats quota for display based on preferred currency
func FormatQuota(quota int64, preferredCurrency string, exchangeRate float64) string {
	return services.FormatQuota(quota, preferredCurrency, exchangeRate)
}

// FormatQuotaFloat returns quota as a float in the preferred currency unit
func FormatQuotaFloat(quota int64, preferredCurrency string, exchangeRate float64) float64 {
	return services.FormatQuotaFloat(quota, preferredCurrency, exchangeRate)
}

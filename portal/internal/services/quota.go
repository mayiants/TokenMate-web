package services

import "fmt"

// QuotaPerUSD defines how many quota units equal one USD.
// 1 quota = 0.000001 USD (micro-dollar).
const QuotaPerUSD int64 = 1_000_000

// USDtoQuota converts a USD amount to internal quota (micro-USD).
func USDtoQuota(usd float64) int64 {
	return int64(usd * float64(QuotaPerUSD))
}

// QuotaToUSD converts internal quota to a USD string.
func QuotaToUSD(quota int64) string {
	return fmt.Sprintf("$%.6f", float64(quota)/float64(QuotaPerUSD))
}

// QuotaToUSDFloat converts internal quota to a USD float64.
func QuotaToUSDFloat(quota int64) float64 {
	return float64(quota) / float64(QuotaPerUSD)
}

// QuotaToRMB converts internal quota to a RMB string using the exchange rate.
func QuotaToRMB(quota int64, rate float64) string {
	return fmt.Sprintf("¥%.2f", float64(quota)/float64(QuotaPerUSD)*rate)
}

// QuotaToRMBFloat converts internal quota to a RMB float64 using the exchange rate.
func QuotaToRMBFloat(quota int64, rate float64) float64 {
	return float64(quota) / float64(QuotaPerUSD) * rate
}

// RMBYuanToQuota converts RMB yuan to internal quota using the exchange rate.
func RMBYuanToQuota(yuan float64, rate float64) int64 {
	return USDtoQuota(yuan / rate)
}

// RMBCentToQuota converts RMB cents to internal quota using the exchange rate.
func RMBCentToQuota(cents int64, rate float64) int64 {
	return USDtoQuota(float64(cents) / 100.0 / rate)
}

// FormatQuota formats quota for display based on preferred currency.
func FormatQuota(quota int64, preferredCurrency string, exchangeRate float64) string {
	if preferredCurrency == "CNY" {
		return fmt.Sprintf("¥%.2f", QuotaToRMBFloat(quota, exchangeRate))
	}
	return fmt.Sprintf("$%.2f", QuotaToUSDFloat(quota))
}

// FormatQuotaFloat returns quota as a float in the preferred currency unit.
func FormatQuotaFloat(quota int64, preferredCurrency string, exchangeRate float64) float64 {
	if preferredCurrency == "CNY" {
		return QuotaToRMBFloat(quota, exchangeRate)
	}
	return QuotaToUSDFloat(quota)
}

// QuotaToCurrency converts quota to a string in the requested currency.
func QuotaToCurrency(quota int64, currency string, exchangeRate float64) string {
	if currency == "CNY" {
		return QuotaToRMB(quota, exchangeRate)
	}
	return QuotaToUSD(quota)
}

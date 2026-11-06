package config

// SupportedCurrencies maps currency codes to their display symbols.
var SupportedCurrencies = map[string]string{
	"USD": "$",
	"CNY": "¥",
	"EUR": "€",
	"GBP": "£",
	"SGD": "S$",
	"JPY": "¥",
	"KRW": "₩",
	"MYR": "RM",
	"IDR": "Rp",
	"THB": "฿",
	"VND": "₫",
	"TWD": "NT$",
	"HKD": "HK$",
	"MOP": "MOP$",
	"AUD": "A$",
	"NZD": "NZ$",
	"RUB": "₽",
}

// DefaultCurrency is the base currency used for internal storage.
const DefaultCurrency = "USD"

// IsSupportedCurrency returns true if the currency code is in the whitelist.
func IsSupportedCurrency(code string) bool {
	_, ok := SupportedCurrencies[code]
	return ok
}

// CurrencySymbol returns the symbol for a currency code, or "CODE " if unknown.
func CurrencySymbol(code string) string {
	if s, ok := SupportedCurrencies[code]; ok {
		return s
	}
	return code + " "
}

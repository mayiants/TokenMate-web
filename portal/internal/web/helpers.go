package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"strconv"
	"strings"
	"time"
)

// TemplateFuncs returns the function map for html/template
func (h *Handler) TemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"t":                   h.translate,
		"role":                h.roleLabel,
		"dict":                dict,
		"formatDate":          formatDate,
		"formatDateTime":      formatDateTime,
		"formatCurrency":      formatCurrency,
		"formatCurrencyExact": formatCurrencyExact,
		"formatMoney":         formatMoney,
		"formatMoneyExact":    formatMoneyExact,
		"formatTokens":        formatTokens,
		"fmtNum":              formatNumber,
		"mul":                 mul,
		"div":                 div,
		"add":                 add,
		"sub":                 sub,
		"slice":               slice,
		"int":                 toInt,
		"expired":             keyExpired,
		"timePast":            timePast,
		"toStringSlice":       toStringSlice,
		"json":                toJSON,
	}
}

// keyExpired reports whether an optional expiry timestamp is in the past.
// api_keys.status stays 'active' after expiry (the row is not flipped), so
// status alone is misleading — pages use this to grey out expired keys.
func keyExpired(v interface{}) bool {
	t, ok := v.(*time.Time)
	return ok && t != nil && !t.After(time.Now())
}

// timePast reports whether a (value) time.Time is in the past (zero = not past).
// Used for invitation link expiry in templates.
func timePast(t time.Time) bool {
	return !t.IsZero() && !t.After(time.Now())
}

// dict creates a map from alternating key-value pairs for template use.
// Usage: {{dict "key1" val1 "key2" val2}}
func dict(values ...interface{}) map[string]interface{} {
	m := make(map[string]interface{})
	for i := 0; i < len(values)-1; i += 2 {
		key, ok := values[i].(string)
		if !ok {
			continue
		}
		m[key] = values[i+1]
	}
	return m
}

func (h *Handler) roleLabel(role string) string {
	key := "role." + role
	msg := h.i18n.T(key, h.currentLocale)
	if msg == key {
		return role
	}
	return msg
}

func (h *Handler) translate(key string, vars ...map[string]interface{}) string {
	msg := h.i18n.T(key, h.currentLocale)
	for _, v := range vars {
		for k, val := range v {
			var repl string
			switch x := val.(type) {
			case string:
				repl = x
			case int, int64, float64:
				repl = fmt.Sprintf("%v", x)
			default:
				repl = fmt.Sprintf("%v", x)
			}
			msg = strings.ReplaceAll(msg, "{"+k+"}", repl)
		}
	}
	return msg
}

func formatDate(t time.Time, tz ...string) string {
	if t.IsZero() {
		return "-"
	}
	timezone := "UTC"
	if len(tz) > 0 && tz[0] != "" {
		timezone = tz[0]
	}
	loc, _ := time.LoadLocation(timezone)
	return t.In(loc).Format("2006-01-02")
}

func formatDateTime(t time.Time, tz ...string) string {
	if t.IsZero() {
		return "-"
	}
	timezone := "UTC"
	if len(tz) > 0 && tz[0] != "" {
		timezone = tz[0]
	}
	loc, _ := time.LoadLocation(timezone)
	return t.In(loc).Format("2006-01-02 15:04")
}

// formatCurrency converts micro-USD quota to display currency with symbol.
// User-facing amounts are rounded to 2 decimals (USD) or 2 decimals (CNY).
// For reconciliation or unit-price precision use formatCurrencyExact.
func formatCurrency(quota interface{}, currency string, exchangeRate ...float64) string {
	var val int64
	switch v := quota.(type) {
	case int:
		val = int64(v)
	case int64:
		val = v
	case float64:
		val = int64(v)
	default:
		val = 0
	}
	// Get exchange rate (use first provided or default 7.20)
	rate := 7.20
	if len(exchangeRate) > 0 && exchangeRate[0] > 0 {
		rate = exchangeRate[0]
	}
	usd := float64(val) / 1000000.0
	if currency == "USD" {
		return fmt.Sprintf("$%.2f", usd)
	}
	return fmt.Sprintf("¥%.2f", usd*rate)
}

// formatCurrencyExact converts micro-USD quota to display currency with full
// micro-USD precision (6 decimals for USD). Use for unit prices and reconciliation.
func formatCurrencyExact(quota interface{}, currency string, exchangeRate ...float64) string {
	var val int64
	switch v := quota.(type) {
	case int:
		val = int64(v)
	case int64:
		val = v
	case float64:
		val = int64(v)
	default:
		val = 0
	}
	rate := 7.20
	if len(exchangeRate) > 0 && exchangeRate[0] > 0 {
		rate = exchangeRate[0]
	}
	usd := float64(val) / 1000000.0
	if currency == "USD" {
		return fmt.Sprintf("$%.6f", usd)
	}
	return fmt.Sprintf("¥%.6f", usd*rate)
}

// formatMoney converts micro-USD quota to the target currency using exchange rate.
// User-facing amounts are rounded to 2 decimals (USD) or 2 decimals (CNY).
func formatMoney(quota interface{}, currency string, exchangeRate float64) string {
	var val int64
	switch v := quota.(type) {
	case int:
		val = int64(v)
	case int64:
		val = v
	case float64:
		val = int64(v)
	default:
		val = 0
	}
	usd := float64(val) / 1000000.0
	if currency == "USD" {
		return fmt.Sprintf("$%.2f", usd)
	}
	return fmt.Sprintf("¥%.2f", usd*exchangeRate)
}

// formatMoneyExact converts micro-USD quota to the target currency with full
// micro-USD precision (6 decimals for USD). Use for unit prices and reconciliation.
func formatMoneyExact(quota interface{}, currency string, exchangeRate float64) string {
	var val int64
	switch v := quota.(type) {
	case int:
		val = int64(v)
	case int64:
		val = v
	case float64:
		val = int64(v)
	default:
		val = 0
	}
	usd := float64(val) / 1000000.0
	if currency == "USD" {
		return fmt.Sprintf("$%.6f", usd)
	}
	return fmt.Sprintf("¥%.6f", usd*exchangeRate)
}

func formatTokens(n interface{}) string {
	var val int64
	switch v := n.(type) {
	case int:
		val = int64(v)
	case int64:
		val = v
	case float64:
		val = int64(v)
	default:
		val = 0
	}
	if val >= 1_000_000_000 {
		return fmt.Sprintf("%.2fB", float64(val)/1_000_000_000)
	}
	if val >= 1_000_000 {
		return fmt.Sprintf("%.2fM", float64(val)/1_000_000)
	}
	return fmt.Sprintf("%d", val)
}

func formatNumber(n interface{}) string {
	var val int64
	switch v := n.(type) {
	case int:
		val = int64(v)
	case int64:
		val = v
	case float64:
		val = int64(v)
	default:
		val = 0
	}
	if val == 0 {
		return "0"
	}
	negative := val < 0
	if negative {
		val = -val
	}
	s := strconv.FormatInt(val, 10)
	var result []byte
	for i, j := len(s)-1, 0; i >= 0; i, j = i-1, j+1 {
		if j > 0 && j%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, s[i])
	}
	// reverse
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	if negative {
		return "-" + string(result)
	}
	return string(result)
}

// mul multiplies two numbers (supports int, float64)
func mul(a, b interface{}) float64 {
	var af, bf float64
	switch v := a.(type) {
	case int:
		af = float64(v)
	case int64:
		af = float64(v)
	case float64:
		af = v
	default:
		af = 0
	}
	switch v := b.(type) {
	case int:
		bf = float64(v)
	case int64:
		bf = float64(v)
	case float64:
		bf = v
	default:
		bf = 0
	}
	return af * bf
}

// div divides two numbers (supports int, float64)
func div(a, b interface{}) float64 {
	var af, bf float64
	switch v := a.(type) {
	case int:
		af = float64(v)
	case int64:
		af = float64(v)
	case float64:
		af = v
	default:
		af = 0
	}
	switch v := b.(type) {
	case int:
		bf = float64(v)
	case int64:
		bf = float64(v)
	case float64:
		bf = v
	default:
		bf = 0
	}
	if bf == 0 {
		return 0
	}
	return af / bf
}

// slice slices a string from start to end
func slice(s string, start, end int) string {
	if start < 0 {
		start = 0
	}
	if end > len(s) {
		end = len(s)
	}
	if start >= end {
		return ""
	}
	return s[start:end]
}

// add adds b to a (supports int, float64)
func add(a, b interface{}) float64 {
	var af, bf float64
	switch v := a.(type) {
	case int:
		af = float64(v)
	case int64:
		af = float64(v)
	case float64:
		af = v
	default:
		af = 0
	}
	switch v := b.(type) {
	case int:
		bf = float64(v)
	case int64:
		bf = float64(v)
	case float64:
		bf = v
	default:
		bf = 0
	}
	return af + bf
}

// sub subtracts b from a (supports int, float64)
func sub(a, b interface{}) float64 {
	var af, bf float64
	switch v := a.(type) {
	case int:
		af = float64(v)
	case int64:
		af = float64(v)
	case float64:
		af = v
	default:
		af = 0
	}
	switch v := b.(type) {
	case int:
		bf = float64(v)
	case int64:
		bf = float64(v)
	case float64:
		bf = v
	default:
		bf = 0
	}
	return af - bf
}

// toInt converts a value to int
func toInt(a interface{}) int {
	switch v := a.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

// toJSON encodes a value as JSON string for use in templates.
// It returns template.JS so html/template treats it as a raw JavaScript
// expression instead of a quoted string literal.
func toJSON(v interface{}) template.JS {
	b, _ := json.Marshal(v)
	return template.JS(b)
}

// Package i18n provides translation utilities used by both web and API layers.
package i18n

import "log/slog"

// I18n holds translations: lang -> key -> value
type I18n map[string]map[string]string

// T returns a translated string for the given key and locale.
// Falls back to English, then to the key itself.
// Logs a warning when a translation is missing.
func (i I18n) T(key, locale string) string {
	if dict, ok := i[locale]; ok {
		if msg, ok := dict[key]; ok {
			return msg
		}
	}
	// Fallback to English
	if dict, ok := i["en"]; ok {
		if msg, ok := dict[key]; ok {
			if locale != "en" {
				slog.Warn("i18n: missing translation", "key", key, "locale", locale)
			}
			return msg
		}
	}
	// Last resort: return the key itself
	slog.Warn("i18n: missing translation in all locales", "key", key, "locale", locale)
	return key
}

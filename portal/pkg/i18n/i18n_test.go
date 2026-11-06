package i18n

import "testing"

// TestTBasicLookup tests basic translation lookup.
func TestTBasicLookup(t *testing.T) {
	i := I18n{
		"zh": {"hello": "你好"},
		"en": {"hello": "Hello"},
	}

	t.Run("zh translation", func(t *testing.T) {
		got := i.T("hello", "zh")
		if got != "你好" {
			t.Errorf("T(hello, zh) = %q, want %q", got, "你好")
		}
	})

	t.Run("en translation", func(t *testing.T) {
		got := i.T("hello", "en")
		if got != "Hello" {
			t.Errorf("T(hello, en) = %q, want %q", got, "Hello")
		}
	})

	t.Run("fallback to en when locale missing", func(t *testing.T) {
		got := i.T("hello", "fr")
		if got != "Hello" {
			t.Errorf("T(hello, fr) fallback = %q, want %q", got, "Hello")
		}
	})

	t.Run("fallback to key when all locales missing", func(t *testing.T) {
		got := i.T("nonexistent", "zh")
		if got != "nonexistent" {
			t.Errorf("T(nonexistent, zh) = %q, want %q", got, "nonexistent")
		}
	})
}

// TestTMissingLocale tests translation with missing locale.
func TestTMissingLocale(t *testing.T) {
	i := I18n{
		"en": {"key": "value"},
	}

	t.Run("fallback to en", func(t *testing.T) {
		got := i.T("key", "jp")
		if got != "value" {
			t.Errorf("T(key, jp) fallback = %q, want %q", got, "value")
		}
	})
}

// TestTEmptyI18n tests translation with empty I18n map.
func TestTEmptyI18n(t *testing.T) {
	i := I18n{}

	got := i.T("any_key", "zh")
	if got != "any_key" {
		t.Errorf("T(any_key, zh) with empty I18n = %q, want %q", got, "any_key")
	}
}

// TestI18nType tests I18n type operations.
func TestI18nType(t *testing.T) {
	i := make(I18n)

	i["zh"] = map[string]string{"app.name": "门户"}
	i["en"] = map[string]string{"app.name": "Portal"}

	if len(i) != 2 {
		t.Errorf("I18n has %d languages, want 2", len(i))
	}
}

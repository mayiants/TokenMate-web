package web

import (
	"testing"
	"time"
)

// TestFormatUserTime tests the relative date formatting.
func TestFormatUserTime(t *testing.T) {
	yesterdayText := "Yesterday"

	tests := []struct {
		name string
		when time.Time
	}{
		{"past date", time.Date(2024, 1, 15, 12, 30, 0, 0, time.UTC)},
		{"zero time", time.Time{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatUserTime(tt.when, "UTC", yesterdayText)
			if tt.when.IsZero() {
				// Zero time will format with date "01-01 00:00"
				if result == "" {
					t.Error("formatUserTime returned empty for zero time")
				}
			} else {
				// Should contain date and time
				if len(result) < 5 {
					t.Errorf("formatUserTime result too short: %q", result)
				}
			}
		})
	}

	// Test today formatting
	t.Run("today time", func(t *testing.T) {
		now := time.Now().In(time.UTC)
		today := time.Date(now.Year(), now.Month(), now.Day(), 15, 30, 0, 0, time.UTC)
		result := formatUserTime(today, "UTC", yesterdayText)
		// Should be just HH:MM (5 chars)
		if len(result) != 5 {
			t.Errorf("Today format expected HH:MM, got %q", result)
		}
	})

	// Test yesterday formatting
	t.Run("yesterday time", func(t *testing.T) {
		now := time.Now().In(time.UTC)
		yesterday := time.Date(now.Year(), now.Month(), now.Day()-1, 10, 15, 0, 0, time.UTC)
		result := formatUserTime(yesterday, "UTC", "昨天")
		// Should contain "昨天" and time
		if len(result) <= len("昨天") {
			t.Errorf("Yesterday format expected '昨天HH:MM', got %q", result)
		}
	})
}

// TestDerefStr tests string pointer dereference.
func TestDerefStr(t *testing.T) {
	s := "hello"
	empty := ""

	tests := []struct {
		name string
		s    *string
		want string
	}{
		{"non-nil", &s, "hello"},
		{"empty string", &empty, ""},
		{"nil", nil, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := derefStr(tt.s)
			if got != tt.want {
				t.Errorf("derefStr(%v) = %q, want %q", tt.s, got, tt.want)
			}
		})
	}
}

// TestUnmarshalStringSlice tests JSON string slice parsing.
func TestUnmarshalStringSlice(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"valid array", `["a","b","c"]`, []string{"a", "b", "c"}},
		{"empty array", `[]`, []string{}},
		{"empty string", "", nil},
		{"invalid json", `{`, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var input *string
			if tt.input != "" || tt.name == "empty array" {
				input = &tt.input
			}
			got := unmarshalStringSlice(input)
			if len(got) != len(tt.want) {
				t.Errorf("unmarshalStringSlice() = %v, want %v", got, tt.want)
				return
			}
			if tt.want == nil && got != nil {
				t.Errorf("unmarshalStringSlice() = %v, want nil", got)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("unmarshalStringSlice()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestToStringSlice tests the toStringSlice type adapter.
func TestToStringSlice(t *testing.T) {
	tests := []struct {
		name string
		v    interface{}
		want []string
	}{
		{"string slice", []string{"a", "b"}, []string{"a", "b"}},
		{"interface slice", []interface{}{"x", "y"}, []string{"x", "y"}},
		{"nil", nil, nil},
		{"empty interface slice", []interface{}{}, []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toStringSlice(tt.v)
			if len(got) != len(tt.want) {
				t.Errorf("toStringSlice() = %v, want %v", got, tt.want)
				return
			}
			if tt.want == nil && got != nil {
				t.Errorf("toStringSlice() = %v, want nil", got)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("toStringSlice()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestNewPaginatedData tests pagination helper.
func TestNewPaginatedData(t *testing.T) {
	tests := []struct {
		total     int
		page      int
		pageSize  int
		wantPages int
	}{
		{100, 1, 10, 10},
		{95, 1, 10, 10},
		{0, 1, 10, 1}, // 0 items = 1 page
		{1, 1, 10, 1},
		{101, 1, 10, 11},
	}

	for _, tt := range tests {
		result := newPaginatedData([]string{}, tt.total, tt.page, tt.pageSize)
		if result.TotalPages != tt.wantPages {
			t.Errorf("newPaginatedData(total=%d, pageSize=%d) TotalPages = %d, want %d",
				tt.total, tt.pageSize, result.TotalPages, tt.wantPages)
		}
		if result.Total != tt.total {
			t.Errorf("newPaginatedData(total=%d) Total = %d", tt.total, result.Total)
		}
		if result.Page != tt.page {
			t.Errorf("newPaginatedData(page=%d) Page = %d", tt.page, result.Page)
		}
	}
}

// TestCSVEscape tests CSV field escaping.
func TestCSVEscape(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"hello, world", `"hello, world"`},
		{"line\nbreak", "\"line\nbreak\""},
		{`has "quotes"`, `"has ""quotes"""`},
		{"normal text", "normal text"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := csvEscape(tt.input)
			if got != tt.want {
				t.Errorf("csvEscape(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestDefaultIfEmpty tests default value fallback.
func TestDefaultIfEmpty(t *testing.T) {
	tests := []struct {
		s        string
		fallback string
		want     string
	}{
		{"value", "default", "value"},
		{"", "default", "default"},
		{"", "", ""},
		{" ", "default", " "}, // space is not empty
	}

	for _, tt := range tests {
		t.Run(tt.s+"/"+tt.fallback, func(t *testing.T) {
			got := defaultIfEmpty(tt.s, tt.fallback)
			if got != tt.want {
				t.Errorf("defaultIfEmpty(%q, %q) = %q, want %q", tt.s, tt.fallback, got, tt.want)
			}
		})
	}
}

// TestUsdToQuota tests micro-USD conversion.
func TestUsdToQuota(t *testing.T) {
	tests := []struct {
		usd  float64
		want int64
	}{
		{1.0, 1000000},
		{0.5, 500000},
		{0.000001, 1},
		{0, 0},
		{100.0, 100000000},
	}

	for _, tt := range tests {
		got := usdToQuota(tt.usd)
		if got != tt.want {
			t.Errorf("usdToQuota(%v) = %d, want %d", tt.usd, got, tt.want)
		}
	}
}

// TestRmbYuanToQuota tests RMB to micro-USD conversion.
func TestRmbYuanToQuota(t *testing.T) {
	tests := []struct {
		yuan float64
		rate float64
		want int64
	}{
		{7.20, 7.20, 1000000},  // 1 USD worth
		{14.40, 7.20, 2000000}, // 2 USD worth
		{1.0, 7.20, 138888},    // ~0.138 USD
		{0, 7.20, 0},
	}

	for _, tt := range tests {
		got := rmbYuanToQuota(tt.yuan, tt.rate)
		if got != tt.want {
			t.Errorf("rmbYuanToQuota(%v, %v) = %d, want %d", tt.yuan, tt.rate, got, tt.want)
		}
	}
}

// TestContentTypeForPath tests MIME type detection.
func TestContentTypeForPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"style.css", "text/css"},
		{"app.js", "application/javascript"},
		{"page.html", "text/html"},
		{"icon.svg", "image/svg+xml"},
		{"photo.png", "image/png"},
		{"image.jpg", "image/jpeg"},
		{"image.jpeg", "image/jpeg"},
		{"data.json", "application/json"},
		{"font.woff2", "font/woff2"},
		{"font.woff", "font/woff"},
		{"unknown.xyz", "application/octet-stream"},
		{"nofile", "application/octet-stream"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := contentTypeForPath(tt.path)
			if got != tt.want {
				t.Errorf("contentTypeForPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

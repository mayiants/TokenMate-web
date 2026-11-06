package web

import (
	"fmt"
	"html/template"
	"testing"
	"time"
)

// TestDict tests the dict helper function.
func TestDict(t *testing.T) {
	tests := []struct {
		name   string
		args   []interface{}
		expect map[string]interface{}
	}{
		{
			name: "single pair",
			args: []interface{}{"key1", "value1"},
			expect: map[string]interface{}{
				"key1": "value1",
			},
		},
		{
			name: "multiple pairs",
			args: []interface{}{"a", 1, "b", "hello", "c", true},
			expect: map[string]interface{}{
				"a": 1,
				"b": "hello",
				"c": true,
			},
		},
		{
			name:   "empty args",
			args:   nil,
			expect: map[string]interface{}{},
		},
		{
			name:   "odd number of args - last value dropped",
			args:   []interface{}{"key1", "value1", "key2"},
			expect: map[string]interface{}{"key1": "value1"},
		},
		{
			name:   "non-string key skipped",
			args:   []interface{}{123, "value"},
			expect: map[string]interface{}{},
		},
		{
			name:   "mixed valid and invalid keys",
			args:   []interface{}{"valid", "yes", 456, "skipped", "also", "ok"},
			expect: map[string]interface{}{"valid": "yes", "also": "ok"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := dict(tt.args...)
			if len(result) != len(tt.expect) {
				t.Errorf("dict() length = %d, want %d", len(result), len(tt.expect))
			}
			for k, v := range tt.expect {
				if result[k] != v {
					t.Errorf("dict()[%q] = %v, want %v", k, result[k], v)
				}
			}
		})
	}
}

// TestFormatDate tests the formatDate function.
func TestFormatDate(t *testing.T) {
	t.Run("non-zero time", func(t *testing.T) {
		d := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
		result := formatDate(d)
		if result != "2025-06-15" {
			t.Errorf("formatDate() = %q, want %q", result, "2025-06-15")
		}
	})

	t.Run("zero time", func(t *testing.T) {
		result := formatDate(time.Time{})
		if result != "-" {
			t.Errorf("formatDate(zero) = %q, want %q", result, "-")
		}
	})
}

// TestFormatDateTime tests the formatDateTime function.
func TestFormatDateTime(t *testing.T) {
	t.Run("non-zero time", func(t *testing.T) {
		d := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
		result := formatDateTime(d)
		if result != "2025-06-15 10:30" {
			t.Errorf("formatDateTime() = %q, want %q", result, "2025-06-15 10:30")
		}
	})

	t.Run("zero time", func(t *testing.T) {
		result := formatDateTime(time.Time{})
		if result != "-" {
			t.Errorf("formatDateTime(zero) = %q, want %q", result, "-")
		}
	})
}

// TestFormatCurrency tests the formatCurrency function.
func TestFormatCurrency(t *testing.T) {
	tests := []struct {
		name     string
		quota    interface{}
		currency string
		want     string
	}{
		{"USD int", 1000000, "USD", "$1.00"},
		{"CNY int", 1000000, "CNY", "¥7.20"},
		{"USD int64", int64(500000), "USD", "$0.50"},
		{"CNY int64", int64(500000), "CNY", "¥3.60"},
		{"USD float64", float64(2000000), "USD", "$2.00"},
		{"zero USD", 0, "USD", "$0.00"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatCurrency(tt.quota, tt.currency)
			if result != tt.want {
				t.Errorf("formatCurrency(%v, %q) = %q, want %q", tt.quota, tt.currency, result, tt.want)
			}
		})
	}
}

// TestFormatCurrencyExact tests the formatCurrencyExact function.
func TestFormatCurrencyExact(t *testing.T) {
	tests := []struct {
		name     string
		quota    interface{}
		currency string
		want     string
	}{
		{"USD int", 1000000, "USD", "$1.000000"},
		{"CNY int", 1000000, "CNY", "¥7.200000"},
		{"USD int64", int64(500000), "USD", "$0.500000"},
		{"CNY int64", int64(500000), "CNY", "¥3.600000"},
		{"zero USD", 0, "USD", "$0.000000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatCurrencyExact(tt.quota, tt.currency)
			if result != tt.want {
				t.Errorf("formatCurrencyExact(%v, %q) = %q, want %q", tt.quota, tt.currency, result, tt.want)
			}
		})
	}
}

// TestFormatMoneyExact tests the formatMoneyExact function.
func TestFormatMoneyExact(t *testing.T) {
	tests := []struct {
		quota    interface{}
		currency string
		rate     float64
		want     string
	}{
		{2000000, "USD", 7.2, "$2.000000"},
		{2000000, "CNY", 7.2, "¥14.400000"},
		{int64(500000), "CNY", 7.0, "¥3.500000"},
		{0, "USD", 7.0, "$0.000000"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			result := formatMoneyExact(tt.quota, tt.currency, tt.rate)
			if result != tt.want {
				t.Errorf("formatMoneyExact(%v, %q, %v) = %q, want %q",
					tt.quota, tt.currency, tt.rate, result, tt.want)
			}
		})
	}
}

// TestFormatTokens tests the formatTokens function.
func TestFormatTokens(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{1, "1"},
		{500, "500"},
		{1000, "1000"},
		{1500, "1500"},
		{999999, "999999"},
		{1000000, "1.00M"},
		{2500000, "2.50M"},
		{1000000000, "1.00B"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d", tt.n), func(t *testing.T) {
			result := formatTokens(tt.n)
			if result != tt.want {
				t.Errorf("formatTokens(%d) = %q, want %q", tt.n, result, tt.want)
			}
		})
	}
}

// TestFormatNumber tests the formatNumber function.
func TestFormatNumber(t *testing.T) {
	tests := []struct {
		name string
		n    interface{}
		want string
	}{
		{"int zero", 0, "0"},
		{"int small", 123, "123"},
		{"int thousands", 1234, "1,234"},
		{"int millions", 1234567, "1,234,567"},
		{"int64", int64(50000), "50,000"},
		{"float64", float64(1000000), "1,000,000"},
		{"negative int", -12345, "-12,345"},
		{"negative int64", int64(-1000), "-1,000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatNumber(tt.n)
			if result != tt.want {
				t.Errorf("formatNumber(%v) = %q, want %q", tt.n, result, tt.want)
			}
		})
	}
}

// TestMul tests the mul function.
func TestMul(t *testing.T) {
	tests := []struct {
		a, b interface{}
		want float64
	}{
		{2, 3, 6},
		{int64(4), int64(5), 20},
		{2.5, 4.0, 10},
		{10, 0.5, 5},
		{0, 100, 0},
		{"invalid", 5, 0},
		{5, "invalid", 0},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%v*%v", tt.a, tt.b), func(t *testing.T) {
			result := mul(tt.a, tt.b)
			if result != tt.want {
				t.Errorf("mul(%v, %v) = %v, want %v", tt.a, tt.b, result, tt.want)
			}
		})
	}
}

// TestDiv tests the div function.
func TestDiv(t *testing.T) {
	tests := []struct {
		a, b interface{}
		want float64
	}{
		{6, 2, 3},
		{int64(100), int64(4), 25},
		{10.0, 4.0, 2.5},
		{10, 0, 0}, // division by zero returns 0
		{0, 10, 0},
		{"invalid", 5, 0},
		{5, "invalid", 0},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%v/%v", tt.a, tt.b), func(t *testing.T) {
			result := div(tt.a, tt.b)
			if result != tt.want {
				t.Errorf("div(%v, %v) = %v, want %v", tt.a, tt.b, result, tt.want)
			}
		})
	}
}

// TestSlice tests the slice function.
func TestSlice(t *testing.T) {
	tests := []struct {
		s     string
		start int
		end   int
		want  string
	}{
		{"hello world", 0, 5, "hello"},
		{"hello world", 6, 11, "world"},
		{"short", 0, 100, "short"}, // end beyond len
		{"short", -5, 3, "sho"},    // negative start normalized to 0
		{"test", 3, 2, ""},         // start >= end
		{"test", 0, 0, ""},         // empty result
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s[%d:%d]", tt.s, tt.start, tt.end), func(t *testing.T) {
			result := slice(tt.s, tt.start, tt.end)
			if result != tt.want {
				t.Errorf("slice(%q, %d, %d) = %q, want %q", tt.s, tt.start, tt.end, result, tt.want)
			}
		})
	}
}

// TestAdd tests the add function.
func TestAdd(t *testing.T) {
	tests := []struct {
		a, b interface{}
		want float64
	}{
		{1, 2, 3},
		{int64(10), int64(20), 30},
		{1.5, 2.5, 4.0},
		{-1, 5, 4},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%v+%v", tt.a, tt.b), func(t *testing.T) {
			result := add(tt.a, tt.b)
			if result != tt.want {
				t.Errorf("add(%v, %v) = %v, want %v", tt.a, tt.b, result, tt.want)
			}
		})
	}
}

// TestSub tests the sub function.
func TestSub(t *testing.T) {
	tests := []struct {
		a, b interface{}
		want float64
	}{
		{5, 3, 2},
		{int64(100), int64(30), 70},
		{5.5, 2.0, 3.5},
		{0, 10, -10},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%v-%v", tt.a, tt.b), func(t *testing.T) {
			result := sub(tt.a, tt.b)
			if result != tt.want {
				t.Errorf("sub(%v, %v) = %v, want %v", tt.a, tt.b, result, tt.want)
			}
		})
	}
}

// TestToInt tests the toInt function.
func TestToInt(t *testing.T) {
	tests := []struct {
		v    interface{}
		want int
	}{
		{42, 42},
		{int64(99), 99},
		{3.14, 3},
		{0, 0},
		{"hello", 0},
		{nil, 0},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%v", tt.v), func(t *testing.T) {
			result := toInt(tt.v)
			if result != tt.want {
				t.Errorf("toInt(%v) = %d, want %d", tt.v, result, tt.want)
			}
		})
	}
}

// TestToJSON tests the toJSON function.
func TestToJSON(t *testing.T) {
	t.Run("simple map", func(t *testing.T) {
		result := toJSON(map[string]int{"a": 1})
		expected := template.JS(`{"a":1}`)
		if result != expected {
			t.Errorf("toJSON(map) = %q, want %q", string(result), string(expected))
		}
	})

	t.Run("nil", func(t *testing.T) {
		result := toJSON(nil)
		expected := template.JS(`null`)
		if result != expected {
			t.Errorf("toJSON(nil) = %q, want %q", string(result), string(expected))
		}
	})

	t.Run("empty struct", func(t *testing.T) {
		result := toJSON(struct{}{})
		expected := template.JS(`{}`)
		if result != expected {
			t.Errorf("toJSON(struct) = %q, want %q", string(result), string(expected))
		}
	})
}

// TestFormatMoney tests the formatMoney function.
func TestFormatMoney(t *testing.T) {
	tests := []struct {
		quota    interface{}
		currency string
		rate     float64
		want     string
	}{
		{2000000, "USD", 7.2, "$2.00"},
		{2000000, "CNY", 7.2, "¥14.40"},
		{int64(500000), "CNY", 7.0, "¥3.50"},
		{0, "USD", 7.0, "$0.00"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			result := formatMoney(tt.quota, tt.currency, tt.rate)
			if result != tt.want {
				t.Errorf("formatMoney(%v, %q, %v) = %q, want %q",
					tt.quota, tt.currency, tt.rate, result, tt.want)
			}
		})
	}
}

// TestTemplateFuncs tests that TemplateFuncs returns all expected functions.
func TestTemplateFuncs(t *testing.T) {
	h := &Handler{}
	funcs := h.TemplateFuncs()

	expected := []string{"t", "role", "dict", "formatDate", "formatDateTime",
		"formatCurrency", "formatCurrencyExact", "formatMoney", "formatMoneyExact", "formatTokens", "fmtNum", "mul",
		"div", "add", "sub", "slice", "int", "expired", "json"}

	for _, name := range expected {
		if _, ok := funcs[name]; !ok {
			t.Errorf("TemplateFuncs() missing function %q", name)
		}
	}
}

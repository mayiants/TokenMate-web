package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// TestVouchersHelpers_GenerateVoucherCode tests the generateVoucherCode function.
func TestVouchersHelpers_GenerateVoucherCode(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
	}{
		{"VG prefix", "VG"},
		{"WC prefix", "WC"},
		{"INV prefix", "INV"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := generateVoucherCode(tt.prefix)

			// Should start with prefix + "-"
			if len(code) < len(tt.prefix)+2 {
				t.Errorf("code %q too short for prefix %q", code, tt.prefix)
			}
			expectedStart := tt.prefix + "-"
			if len(code) >= len(expectedStart) && code[:len(expectedStart)] != expectedStart {
				t.Errorf("code %q should start with %q", code, expectedStart)
			}

			// Should not be empty
			if code == "" {
				t.Error("generated code should not be empty")
			}
		})
	}
}

// TestVouchersHelpers_GenerateVoucherCode_Uniqueness tests voucher code uniqueness.
func TestVouchersHelpers_GenerateVoucherCode_Uniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		code := generateVoucherCode("VG")
		if seen[code] {
			t.Errorf("duplicate voucher code: %q", code)
		}
		seen[code] = true
	}
}

// TestVouchersHelpers_ParseVoucherPagination tests the pagination parsing helper.
func TestVouchersHelpers_ParseVoucherPagination(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantPage  int
		wantLimit int
	}{
		{"defaults", "", 1, voucherPaginationDefaultLimit},
		{"valid page and limit", "page=2&limit=30", 2, 30},
		{"page only", "page=3", 3, voucherPaginationDefaultLimit},
		{"limit only", "limit=40", 1, 40},
		{"invalid page", "page=abc", 1, voucherPaginationDefaultLimit},
		{"invalid limit", "limit=xyz", 1, voucherPaginationDefaultLimit},
		{"zero page", "page=0", 1, voucherPaginationDefaultLimit},
		{"negative page", "page=-1", 1, voucherPaginationDefaultLimit},
		{"zero limit", "limit=0", 1, voucherPaginationDefaultLimit},
		{"over max limit", "limit=100", 1, voucherPaginationDefaultLimit},
		{"exact max limit", "limit=50", 1, voucherPaginationMaxLimit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/?"+tt.query, nil)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = req

			page, limit := parseVoucherPagination(c, voucherPaginationDefaultLimit)

			if page != tt.wantPage {
				t.Errorf("page = %d, want %d", page, tt.wantPage)
			}
			if limit != tt.wantLimit {
				t.Errorf("limit = %d, want %d", limit, tt.wantLimit)
			}
		})
	}
}

// TestVouchersHelpers_Constants tests voucher-related constants.
func TestVouchersHelpers_Constants(t *testing.T) {
	tests := []struct {
		name  string
		value int
		min   int
	}{
		{"voucherCodeRandomLength", voucherCodeRandomLength, 4},
		{"voucherPaginationDefaultLimit", voucherPaginationDefaultLimit, 1},
		{"voucherPaginationMaxLimit", voucherPaginationMaxLimit, 1},
		{"maxVoucherCreateCount", maxVoucherCreateCount, 1},
		{"defaultInvitationMaxCodesPerUser", defaultInvitationMaxCodesPerUser, 1},
		{"defaultInvitationMaxUses", defaultInvitationMaxUses, 1},
		{"defaultInvitationExpireDays", defaultInvitationExpireDays, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.value < tt.min {
				t.Errorf("%s = %d, should be >= %d", tt.name, tt.value, tt.min)
			}
		})
	}
}

// TestVouchersHelpers_InvitationBounds tests invitation bounds.
func TestVouchersHelpers_InvitationBounds(t *testing.T) {
	// min <= default <= max
	if minInvitationMaxUses > defaultInvitationMaxUses {
		t.Errorf("minInvitationMaxUses (%d) > defaultInvitationMaxUses (%d)", minInvitationMaxUses, defaultInvitationMaxUses)
	}
	if defaultInvitationMaxUses > maxInvitationMaxUses {
		t.Errorf("defaultInvitationMaxUses (%d) > maxInvitationMaxUses (%d)", defaultInvitationMaxUses, maxInvitationMaxUses)
	}
	if minInvitationExpireDays > defaultInvitationExpireDays {
		t.Errorf("minInvitationExpireDays (%d) > defaultInvitationExpireDays (%d)", minInvitationExpireDays, defaultInvitationExpireDays)
	}
}

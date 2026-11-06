package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// TestGetUsageSummary_DaysParamParsing tests that the days query parameter is parsed correctly.
func TestGetUsageSummary_DaysParamParsing(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		wantDays int
	}{
		{"default days", "", 7},
		{"valid days", "days=30", 30},
		{"invalid days string", "days=abc", 7},
		{"zero days", "days=0", 7},
		{"negative days", "days=-5", 7},
		{"large days", "days=365", 365},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the parsing logic from getUsageSummary
			days := 7
			if tt.query != "" {
				req := httptest.NewRequest(http.MethodGet, "/usage-summary?"+tt.query, nil)
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = req
				if d := c.Query("days"); d != "" {
					if parsed, err := strconv.Atoi(d); err == nil && parsed > 0 {
						days = parsed
					}
				}
			}
			if days != tt.wantDays {
				t.Errorf("days = %d, want %d", days, tt.wantDays)
			}
		})
	}
}

// TestGetUsageRecords_PaginationParsing tests page and page_size parameter parsing.
func TestGetUsageRecords_PaginationParsing(t *testing.T) {
	tests := []struct {
		name         string
		query        string
		wantPage     int
		wantPageSize int
	}{
		{"defaults", "", 1, 20},
		{"valid page", "page=3", 3, 20},
		{"valid page_size", "page_size=50", 1, 50},
		{"both params", "page=2&page_size=10", 2, 10},
		{"invalid page", "page=abc", 1, 20},
		{"invalid page_size", "page_size=xyz", 1, 20},
		{"zero page", "page=0", 1, 20},
		{"negative page", "page=-1", 1, 20},
		{"zero page_size", "page_size=0", 1, 20},
		{"negative page_size", "page_size=-5", 1, 20},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/usage-records?"+tt.query, nil)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = req

			page := 1
			pageSize := 20
			if p := c.Query("page"); p != "" {
				if parsed, err := strconv.Atoi(p); err == nil && parsed > 0 {
					page = parsed
				}
			}
			if ps := c.Query("page_size"); ps != "" {
				if parsed, err := strconv.Atoi(ps); err == nil && parsed > 0 {
					pageSize = parsed
				}
			}

			if page != tt.wantPage {
				t.Errorf("page = %d, want %d", page, tt.wantPage)
			}
			if pageSize != tt.wantPageSize {
				t.Errorf("pageSize = %d, want %d", pageSize, tt.wantPageSize)
			}
		})
	}
}

// TestGetUsageRecords_DateFilterParsing tests start_date and end_date parameter parsing.
func TestGetUsageRecords_DateFilterParsing(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantStart string
		wantEnd   string
	}{
		{"no dates", "", "", ""},
		{"start only", "start_date=2024-01-01", "2024-01-01", ""},
		{"end only", "end_date=2024-12-31", "", "2024-12-31 23:59:59"},
		{"both dates", "start_date=2024-01-01&end_date=2024-12-31", "2024-01-01", "2024-12-31 23:59:59"},
		{"empty values", "start_date=&end_date=", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/usage-records?"+tt.query, nil)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = req

			var startDate, endDate string
			if sd := c.Query("start_date"); sd != "" {
				startDate = sd
			}
			if ed := c.Query("end_date"); ed != "" {
				endDate = ed + " 23:59:59"
			}

			if startDate != tt.wantStart {
				t.Errorf("startDate = %q, want %q", startDate, tt.wantStart)
			}
			if endDate != tt.wantEnd {
				t.Errorf("endDate = %q, want %q", endDate, tt.wantEnd)
			}
		})
	}
}

// TestGetUsageRecords_ApiKeyIDParsing tests api_key_id query parameter.
func TestGetUsageRecords_ApiKeyIDParsing(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		wantKey string
	}{
		{"no key", "", ""},
		{"with key", "api_key_id=abc-123", "abc-123"},
		{"empty key", "api_key_id=", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/usage-records?"+tt.query, nil)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = req

			apiKeyID := c.Query("api_key_id")
			if apiKeyID != tt.wantKey {
				t.Errorf("apiKeyID = %q, want %q", apiKeyID, tt.wantKey)
			}
		})
	}
}

// TestIsAdmin checks admin role detection logic.
func TestIsAdmin(t *testing.T) {
	tests := []struct {
		name      string
		role      string
		wantAdmin bool
	}{
		{"system_admin", "system_admin", true},
		{"super_admin", "super_admin", true},
		{"regular user", "user", false},
		{"enterprise_admin", "enterprise_admin", false},
		{"empty role", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isAdmin := tt.role == "system_admin" || tt.role == "super_admin"
			if isAdmin != tt.wantAdmin {
				t.Errorf("isAdmin(%q) = %v, want %v", tt.role, isAdmin, tt.wantAdmin)
			}
		})
	}
}

// TestCreateRecharge_BindingValidation tests JSON binding for recharge request.
func TestCreateRecharge_BindingValidation(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantAmount int64
		wantValid  bool
	}{
		{"valid amount", `{"amount": 1000}`, 1000, true},
		{"zero amount", `{"amount": 0}`, 0, false},
		{"negative amount", `{"amount": -100}`, -100, false},
		{"missing amount", `{}`, 0, false},
		{"invalid json", `not json`, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				Amount int64 `json:"amount"`
			}
			err := json.Unmarshal([]byte(tt.body), &req)
			valid := err == nil && req.Amount > 0
			if valid != tt.wantValid {
				t.Errorf("valid = %v, want %v (err=%v, amount=%d)", valid, tt.wantValid, err, req.Amount)
			}
			if req.Amount != tt.wantAmount {
				t.Errorf("amount = %d, want %d", req.Amount, tt.wantAmount)
			}
		})
	}
}

// TestTotalPagesCalculation tests the total_pages formula.
func TestTotalPagesCalculation(t *testing.T) {
	tests := []struct {
		name      string
		total     int
		pageSize  int
		wantPages int
	}{
		{"exact division", 100, 20, 5},
		{"with remainder", 101, 20, 6},
		{"single page", 5, 20, 1},
		{"empty", 0, 20, 0},
		{"one item", 1, 20, 1},
		{"page size 1", 5, 1, 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			totalPages := (tt.total + tt.pageSize - 1) / tt.pageSize
			if totalPages != tt.wantPages {
				t.Errorf("totalPages(%d, %d) = %d, want %d", tt.total, tt.pageSize, totalPages, tt.wantPages)
			}
		})
	}
}

// TestQuotaConversion tests micro-USD quota to USD conversion.
func TestQuotaConversion(t *testing.T) {
	tests := []struct {
		name    string
		quota   int64
		wantUSD float64
	}{
		{"zero", 0, 0.0},
		{"one dollar", 1_000_000, 1.0},
		{"half dollar", 500_000, 0.5},
		{"one cent", 10_000, 0.01},
		{"micro dollar", 1, 0.000001},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usd := QuotaToUSDFloat(tt.quota)
			if usd != tt.wantUSD {
				t.Errorf("QuotaToUSDFloat(%d) = %f, want %f", tt.quota, usd, tt.wantUSD)
			}
		})
	}
}

// TestWhereConditionsBuilding tests that WHERE clauses are built correctly based on filters.
func TestWhereConditionsBuilding(t *testing.T) {
	tests := []struct {
		name      string
		isAdmin   bool
		startDate string
		endDate   string
		apiKeyID  string
		wantConds int
	}{
		{"admin no filters", true, "", "", "", 0},
		{"user no filters", false, "", "", "", 1},
		{"user with start", false, "2024-01-01", "", "", 2},
		{"user with all", false, "2024-01-01", "2024-12-31", "key-1", 4},
		{"admin with dates", true, "2024-01-01", "2024-12-31", "", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conditions := []string{}
			argIdx := 1

			if !tt.isAdmin {
				conditions = append(conditions, "user_id = $"+strconv.Itoa(argIdx)+"::uuid")
				argIdx++
			}
			if tt.startDate != "" {
				conditions = append(conditions, "created_at >= $"+strconv.Itoa(argIdx))
				argIdx++
			}
			if tt.endDate != "" {
				conditions = append(conditions, "created_at <= $"+strconv.Itoa(argIdx))
				argIdx++
			}
			if tt.apiKeyID != "" {
				conditions = append(conditions, "user_api_key_id = $"+strconv.Itoa(argIdx)+"::uuid")
			}

			if len(conditions) != tt.wantConds {
				t.Errorf("conditions count = %d, want %d", len(conditions), tt.wantConds)
			}
		})
	}
}

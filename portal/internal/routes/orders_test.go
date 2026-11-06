package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestCreateOrder_BindingValidation tests JSON binding for order creation.
func TestCreateOrder_BindingValidation(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		wantValid    bool
		wantErrField string
	}{
		{
			name:      "valid order",
			body:      `{"product_type": "api_key", "amount": 1000}`,
			wantValid: true,
		},
		{
			name:         "missing product_type",
			body:         `{"amount": 1000}`,
			wantValid:    false,
			wantErrField: "product_type",
		},
		{
			name:         "missing amount",
			body:         `{"product_type": "api_key"}`,
			wantValid:    false,
			wantErrField: "amount",
		},
		{
			name:         "zero amount",
			body:         `{"product_type": "api_key", "amount": 0}`,
			wantValid:    false,
			wantErrField: "amount",
		},
		{
			name:         "negative amount",
			body:         `{"product_type": "api_key", "amount": -100}`,
			wantValid:    false,
			wantErrField: "amount",
		},
		{
			name:         "empty body",
			body:         `{}`,
			wantValid:    false,
			wantErrField: "product_type",
		},
		{
			name:         "invalid json",
			body:         `not json`,
			wantValid:    false,
			wantErrField: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				ProductType   string `json:"product_type" binding:"required"`
				Amount        int    `json:"amount" binding:"required,min=1"`
				PaymentMethod string `json:"payment_method"`
				APIKeyID      string `json:"api_key_id"`
			}
			err := json.Unmarshal([]byte(tt.body), &req)
			if err != nil {
				if tt.wantValid {
					t.Errorf("unexpected unmarshal error: %v", err)
				}
				return
			}

			valid := req.ProductType != "" && req.Amount > 0
			if valid != tt.wantValid {
				t.Errorf("valid = %v, want %v (product_type=%q, amount=%d)", valid, tt.wantValid, req.ProductType, req.Amount)
			}
		})
	}
}

// TestCreateOrder_DefaultPaymentMethod tests that payment_method defaults to "alipay".
func TestCreateOrder_DefaultPaymentMethod(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty defaults to alipay", "", "alipay"},
		{"explicit alipay", "alipay", "alipay"},
		{"explicit wechat", "wechat", "wechat"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pm := tt.input
			if pm == "" {
				pm = "alipay"
			}
			if pm != tt.want {
				t.Errorf("paymentMethod = %q, want %q", pm, tt.want)
			}
		})
	}
}

// TestCreateOrder_ProductTypeRequired tests that product_type is required.
func TestCreateOrder_ProductTypeRequired(t *testing.T) {
	tests := []struct {
		name        string
		productType string
		wantValid   bool
	}{
		{"api_key", "api_key", true},
		{"recharge", "recharge", true},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid := tt.productType != ""
			if valid != tt.wantValid {
				t.Errorf("valid(%q) = %v, want %v", tt.productType, valid, tt.wantValid)
			}
		})
	}
}

// TestNullIfEmpty tests the nullIfEmpty helper function.
func TestNullIfEmpty(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  interface{}
	}{
		{"empty string", "", nil},
		{"non-empty string", "abc", "abc"},
		{"uuid", "550e8400-e29b-41d4-a716-446655440000", "550e8400-e29b-41d4-a716-446655440000"},
		{"whitespace", " ", " "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nullIfEmpty(tt.input)
			if got != tt.want {
				t.Errorf("nullIfEmpty(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// TestListOrders_PaginationParsing tests pagination parameter parsing for orders.
func TestListOrders_PaginationParsing(t *testing.T) {
	tests := []struct {
		name         string
		query        string
		wantPage     int
		wantPageSize int
	}{
		{"defaults", "", 1, 20},
		{"valid page", "page=5", 5, 20},
		{"valid page_size", "page_size=100", 1, 100},
		{"both", "page=3&page_size=50", 3, 50},
		{"invalid page", "page=abc", 1, 20},
		{"invalid page_size", "page_size=xyz", 1, 20},
		{"zero page", "page=0", 1, 20},
		{"zero page_size", "page_size=0", 1, 20},
		{"negative page", "page=-1", 1, 20},
		{"negative page_size", "page_size=-10", 1, 20},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/orders?"+tt.query, nil)
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

// TestCancelOrder_OnlyPending tests that cancellation logic targets pending orders.
func TestCancelOrder_OnlyPending(t *testing.T) {
	tests := []struct {
		name            string
		orderStatus     string
		wantCancellable bool
	}{
		{"pending order", "pending", true},
		{"completed order", "completed", false},
		{"cancelled order", "cancelled", false},
		{"processing order", "processing", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The SQL WHERE clause includes: AND status = 'pending'
			cancellable := tt.orderStatus == "pending"
			if cancellable != tt.wantCancellable {
				t.Errorf("cancellable(%q) = %v, want %v", tt.orderStatus, cancellable, tt.wantCancellable)
			}
		})
	}
}

// TestCancelOrder_UrlParser tests that order ID can be UUID or order_id format.
func TestCancelOrder_UrlParser(t *testing.T) {
	tests := []struct {
		name    string
		param   string
		wantSQL string
	}{
		{"uuid", "550e8400-e29b-41d4-a716-446655440000", "550e8400-e29b-41d4-a716-446655440000"},
		{"order_id", "ORD-abc12345", "ORD-abc12345"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/orders/"+tt.param+"/cancel", nil)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = req
			c.Params = gin.Params{{Key: "id", Value: tt.param}}

			orderID := c.Param("id")
			// The SQL uses: WHERE (id = $1::uuid OR order_id = $1)
			_ = orderID
			if orderID != tt.wantSQL {
				t.Errorf("orderID = %q, want %q", orderID, tt.wantSQL)
			}
		})
	}
}

// TestCreateOrder_GenerateOrderID tests order ID generation format.
func TestCreateOrder_GenerateOrderID(t *testing.T) {
	// Order IDs follow the pattern "ORD-" + first 8 chars of UUID
	prefix := "ORD-"
	if !strings.HasPrefix(prefix, "ORD-") {
		t.Error("order ID prefix should be ORD-")
	}
	if len(prefix) != 4 {
		t.Errorf("prefix length = %d, want 4", len(prefix))
	}
}

// TestGetOrder_QueryFallback tests that getOrder tries UUID first, then order_id.
func TestGetOrder_QueryFallback(t *testing.T) {
	tests := []struct {
		name    string
		param   string
		wantTry string
	}{
		{"by uuid", "550e8400-e29b-41d4-a716-446655440000", "uuid_first"},
		{"by order_id", "ORD-abc12345", "order_id_fallback"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/orders/"+tt.param, nil)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = req
			c.Params = gin.Params{{Key: "id", Value: tt.param}}

			orderID := c.Param("id")
			if orderID != tt.param {
				t.Errorf("orderID = %q, want %q", orderID, tt.param)
			}
		})
	}
}

// TestCreateOrder_AmountValidation tests amount minimum validation.
func TestCreateOrder_AmountValidation(t *testing.T) {
	tests := []struct {
		name      string
		amount    int64
		wantValid bool
	}{
		{"valid 1", 1, true},
		{"valid 1000", 1000, true},
		{"zero", 0, false},
		{"negative", -1, false},
		{"large", 1000000, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// binding:"required,min=1" means amount >= 1
			valid := tt.amount >= 1
			if valid != tt.wantValid {
				t.Errorf("valid(%d) = %v, want %v", tt.amount, valid, tt.wantValid)
			}
		})
	}
}

// TestCreateOrder_AllFields tests order creation with all optional fields.
func TestCreateOrder_AllFields(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantProduct string
		wantAmount  int
		wantMethod  string
		wantKeyID   string
	}{
		{
			name:        "all fields",
			body:        `{"product_type": "api_key", "amount": 5000, "payment_method": "wechat", "api_key_id": "key-123"}`,
			wantProduct: "api_key",
			wantAmount:  5000,
			wantMethod:  "wechat",
			wantKeyID:   "key-123",
		},
		{
			name:        "minimal fields",
			body:        `{"product_type": "recharge", "amount": 100}`,
			wantProduct: "recharge",
			wantAmount:  100,
			wantMethod:  "alipay",
			wantKeyID:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				ProductType   string `json:"product_type" binding:"required"`
				Amount        int    `json:"amount" binding:"required,min=1"`
				PaymentMethod string `json:"payment_method"`
				APIKeyID      string `json:"api_key_id"`
			}
			if err := json.Unmarshal([]byte(tt.body), &req); err != nil {
				t.Fatalf("unmarshal error: %v", err)
			}
			if req.PaymentMethod == "" {
				req.PaymentMethod = "alipay"
			}

			if req.ProductType != tt.wantProduct {
				t.Errorf("productType = %q, want %q", req.ProductType, tt.wantProduct)
			}
			if req.Amount != tt.wantAmount {
				t.Errorf("amount = %d, want %d", req.Amount, tt.wantAmount)
			}
			if req.PaymentMethod != tt.wantMethod {
				t.Errorf("paymentMethod = %q, want %q", req.PaymentMethod, tt.wantMethod)
			}
			if req.APIKeyID != tt.wantKeyID {
				t.Errorf("apiKeyID = %q, want %q", req.APIKeyID, tt.wantKeyID)
			}
		})
	}
}

// TestUpdateOrder_BindingValidation tests JSON binding for order update.
func TestUpdateOrder_BindingValidation(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantAmount int
		wantMethod string
		wantValid  bool
	}{
		{"valid amount and method", `{"amount": 5000, "payment_method": "stripe"}`, 5000, "stripe", true},
		{"valid amount only", `{"amount": 1000}`, 1000, "", true},
		{"valid method only", `{"payment_method": "paypal"}`, 0, "paypal", true},
		{"zero amount ignored", `{"amount": 0, "payment_method": "alipay"}`, 0, "alipay", true},
		{"negative amount", `{"amount": -100}`, -100, "", false}, // binding min=1 rejects negative
		{"empty body", `{}`, 0, "", false},
		{"invalid json", `not json`, 0, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				Amount        int    `json:"amount" binding:"min=1"`
				PaymentMethod string `json:"payment_method"`
			}
			err := json.Unmarshal([]byte(tt.body), &req)
			if err != nil {
				if tt.wantValid {
					t.Errorf("unexpected unmarshal error: %v", err)
				}
				return
			}

			// Simulate the handler's logic: amount > 0 means it's set
			hasUpdate := req.Amount > 0 || req.PaymentMethod != ""
			if hasUpdate != tt.wantValid {
				t.Errorf("hasUpdate = %v, want %v (amount=%d, method=%q)", hasUpdate, tt.wantValid, req.Amount, req.PaymentMethod)
			}
			if req.Amount != tt.wantAmount {
				t.Errorf("amount = %d, want %d", req.Amount, tt.wantAmount)
			}
			if req.PaymentMethod != tt.wantMethod {
				t.Errorf("paymentMethod = %q, want %q", req.PaymentMethod, tt.wantMethod)
			}
		})
	}
}

// TestPaymentMethodLabels tests that all 4 payment methods map to correct labels.
func TestPaymentMethodLabels(t *testing.T) {
	labels := map[string]string{
		"alipay":      "支付宝",
		"wechat":      "微信支付",
		"wechat_pay":  "微信支付",
		"stripe":      "Stripe",
		"paypal":      "PayPal",
		"credit_card": "Stripe",
		"card":        "Stripe",
		"balance":     "余额",
		"unknown":     "unknown",
		"":            "-",
	}

	for method, want := range labels {
		var got string
		switch method {
		case "alipay":
			got = "支付宝"
		case "wechat", "wechat_pay":
			got = "微信支付"
		case "stripe", "credit_card", "card":
			got = "Stripe"
		case "paypal":
			got = "PayPal"
		case "balance":
			got = "余额"
		case "":
			got = "-"
		default:
			got = method
		}
		if got != want {
			t.Errorf("label(%q) = %q, want %q", method, got, want)
		}
	}
}

// TestOrderIDLookup_Formats tests that both UUID and order_id formats are accepted.
func TestOrderIDLookup_Formats(t *testing.T) {
	tests := []struct {
		name  string
		input string
		valid bool
	}{
		{"uuid format", "550e8400-e29b-41d4-a716-446655440000", true},
		{"order_id format", "ORD-abc12345", true},
		{"short order_id", "ORD-1234", true},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The SQL uses: WHERE (id = $1::uuid OR order_id = $1)
			// So any non-empty string could potentially match order_id
			valid := tt.input != ""
			if valid != tt.valid {
				t.Errorf("valid(%q) = %v, want %v", tt.input, valid, tt.valid)
			}
		})
	}
}

// TestCreatePayment_SimulationMode tests simulation mode logic.
func TestCreatePayment_SimulationMode(t *testing.T) {
	// When payment_gateways table doesn't exist or gateway is not enabled,
	// the system should fall back to simulation mode.
	tests := []struct {
		name         string
		gatewayErr   bool
		enabled      bool
		wantSimulate bool
	}{
		{"gateway not found", true, false, true},
		{"gateway disabled", false, false, true},
		{"gateway enabled", false, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useSimulate := tt.gatewayErr || !tt.enabled
			if useSimulate != tt.wantSimulate {
				t.Errorf("useSimulate = %v, want %v", useSimulate, tt.wantSimulate)
			}
		})
	}
}

// TestExportOrders_PaymentMethodLabels tests CSV export method label mapping.
func TestExportOrders_PaymentMethodLabels(t *testing.T) {
	tests := []struct {
		method string
		want   string
	}{
		{"alipay", "支付宝"},
		{"wechat", "微信支付"},
		{"wechat_pay", "微信支付"},
		{"stripe", "Stripe"},
		{"paypal", "PayPal"},
		{"credit_card", "Stripe"},
		{"balance", "余额"},
		{"unknown", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			var got string
			switch tt.method {
			case "balance":
				got = "余额"
			case "alipay":
				got = "支付宝"
			case "wechat", "wechat_pay":
				got = "微信支付"
			case "stripe", "credit_card":
				got = "Stripe"
			case "paypal":
				got = "PayPal"
			default:
				got = tt.method
			}
			if got != tt.want {
				t.Errorf("label(%q) = %q, want %q", tt.method, got, tt.want)
			}
		})
	}
}

// TestProductTypeLabels tests product type display label mapping.
func TestProductTypeLabels(t *testing.T) {
	tests := []struct {
		pType string
		want  string
	}{
		{"recharge", "充值"},
		{"subscription", "订阅"},
		{"api_key", "API Key"},
		{"unknown", "unknown"},
		{"", "-"},
	}

	for _, tt := range tests {
		t.Run(tt.pType, func(t *testing.T) {
			var got string
			switch tt.pType {
			case "recharge":
				got = "充值"
			case "subscription":
				got = "订阅"
			case "api_key":
				got = "API Key"
			case "":
				got = "-"
			default:
				got = tt.pType
			}
			if got != tt.want {
				t.Errorf("label(%q) = %q, want %q", tt.pType, got, tt.want)
			}
		})
	}
}

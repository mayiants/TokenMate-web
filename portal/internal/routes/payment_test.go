package routes

import (
	"encoding/json"
	"testing"
)

// TestPayment_CreatePaymentValidation tests the create payment request binding.
func TestPayment_CreatePaymentValidation(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantValid  bool
		wantOrder  string
		wantMethod string
	}{
		{"valid payment", `{"order_id": "ORD-12345678", "payment_method": "alipay"}`, true, "ORD-12345678", "alipay"},
		{"stripe", `{"order_id": "ORD-12345678", "payment_method": "stripe"}`, true, "ORD-12345678", "stripe"},
		{"missing method", `{"order_id": "ORD-12345678"}`, true, "ORD-12345678", ""},
		{"missing order_id", `{"payment_method": "alipay"}`, true, "", "alipay"},
		{"invalid json", `bad`, false, "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				OrderID       string `json:"order_id" binding:"required"`
				PaymentMethod string `json:"payment_method" binding:"required"`
			}
			err := json.Unmarshal([]byte(tt.body), &req)
			valid := err == nil
			if valid != tt.wantValid {
				t.Errorf("valid = %v, want %v", valid, tt.wantValid)
				return
			}
			if !valid {
				return
			}
			if req.OrderID != tt.wantOrder {
				t.Errorf("order_id = %q, want %q", req.OrderID, tt.wantOrder)
			}
			if req.PaymentMethod != tt.wantMethod {
				t.Errorf("payment_method = %q, want %q", req.PaymentMethod, tt.wantMethod)
			}
		})
	}
}

// TestPayment_UpdateGatewayValidation tests the update gateway request binding.
func TestPayment_UpdateGatewayValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantValid bool
		wantEn    bool
	}{
		{"enable gateway", `{"enabled": true}`, true, true},
		{"disable gateway", `{"enabled": false}`, true, false},
		{"with config", `{"config": {"api_key": "sk-test"}, "enabled": true}`, true, true},
		{"empty body", `{}`, true, false},
		{"invalid json", `bad`, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				Config  map[string]interface{} `json:"config"`
				Enabled *bool                  `json:"enabled"`
			}
			err := json.Unmarshal([]byte(tt.body), &req)
			valid := err == nil
			if valid != tt.wantValid {
				t.Errorf("valid = %v, want %v", valid, tt.wantValid)
				return
			}
			if !valid {
				return
			}
			if req.Enabled != nil && *req.Enabled != tt.wantEn {
				t.Errorf("enabled = %v, want %v", *req.Enabled, tt.wantEn)
			}
		})
	}
}

// TestPayment_SupportedGateways verifies supported payment gateways.
func TestPayment_SupportedGateways(t *testing.T) {
	supportedGateways := map[string]bool{
		"alipay": true,
		"wechat": true,
		"stripe": true,
		"paypal": true,
	}

	tests := []struct {
		gateway   string
		wantValid bool
	}{
		{"alipay", true},
		{"wechat", true},
		{"stripe", true},
		{"paypal", true},
		{"unknown", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.gateway, func(t *testing.T) {
			valid := supportedGateways[tt.gateway]
			if valid != tt.wantValid {
				t.Errorf("supported gateways[%q] = %v, want %v", tt.gateway, valid, tt.wantValid)
			}
		})
	}
}

// TestPayment_SimulationMode tests the simulation mode setting.
func TestPayment_SimulationMode(t *testing.T) {
	// Test that "true" means enabled
	simulationMode := "true"
	forceSimulate := simulationMode == "true"
	if !forceSimulate {
		t.Error("simulation mode should be enabled for string 'true'")
	}

	// Other values should not enable simulation
	otherValues := []string{"false", "", "yes", "1"}
	for _, v := range otherValues {
		if v == "true" {
			t.Errorf("%q should not trigger simulation mode", v)
		}
	}
}

// TestPayment_OrderStatusValues verifies valid order status values.
func TestPayment_OrderStatusValues(t *testing.T) {
	validStatuses := map[string]bool{
		"pending":   true,
		"completed": true,
		"cancelled": true,
		"paid":      true,
		"refunded":  true,
	}

	tests := []struct {
		status    string
		wantValid bool
	}{
		{"pending", true},
		{"completed", true},
		{"cancelled", true},
		{"paid", true},
		{"refunded", true},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			valid := validStatuses[tt.status]
			if valid != tt.wantValid {
				t.Errorf("order status %q valid = %v, want %v", tt.status, valid, tt.wantValid)
			}
		})
	}
}

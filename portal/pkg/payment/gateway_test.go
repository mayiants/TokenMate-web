package payment

import (
	"testing"
)

// TestGatewayInterface verifies that gateway types satisfy the Gateway interface.
func TestGatewayInterface(t *testing.T) {
	// Compile-time check: these types implement Gateway
	var _ Gateway = (*StripeGateway)(nil)
	var _ Gateway = (*AlipayGateway)(nil)
	var _ Gateway = (*WechatGateway)(nil)
}

// TestOrderStruct tests the Order struct fields.
func TestOrderStruct(t *testing.T) {
	order := Order{
		OrderID:     "ORD-123",
		UserID:      "user-1",
		Amount:      1000,
		Currency:    "usd",
		Description: "Test order",
		ProductName: "API Credits",
	}

	if order.OrderID != "ORD-123" {
		t.Errorf("OrderID = %q, want %q", order.OrderID, "ORD-123")
	}
	if order.Amount != 1000 {
		t.Errorf("Amount = %d, want 1000", order.Amount)
	}
}

// TestWebhookResultStruct tests the WebhookResult struct.
func TestWebhookResultStruct(t *testing.T) {
	result := WebhookResult{
		OrderID:   "ORD-456",
		Status:    "success",
		PaymentID: "pay-789",
		Amount:    1000,
	}

	if result.Status != "success" {
		t.Errorf("Status = %q, want %q", result.Status, "success")
	}
	if result.Amount != 1000 {
		t.Errorf("Amount = %d, want 1000", result.Amount)
	}
}

// TestGatewayConfigStructs tests gateway configuration structs.
func TestGatewayConfigStructs(t *testing.T) {
	gc := GatewayConfig{
		Stripe: &StripeConfig{
			SecretKey:     "sk_test",
			WebhookSecret: "whsec_test",
			SuccessURL:    "https://example.com/success",
		},
		Alipay: &AlipayConfig{
			ClientID:  "app123",
			NotifyURL: "https://example.com/notify",
		},
	}

	if gc.Stripe.SecretKey != "sk_test" {
		t.Error("Stripe config not stored correctly")
	}
	if gc.Alipay.ClientID != "app123" {
		t.Error("Alipay config not stored correctly")
	}
}

// TestQueryResultStruct tests the QueryResult struct.
func TestQueryResultStruct(t *testing.T) {
	qr := QueryResult{
		OrderID:   "ORD-001",
		Status:    "pending",
		PaymentID: "",
	}

	if qr.Status != "pending" {
		t.Errorf("Status = %q, want pending", qr.Status)
	}
	if qr.PaidAt != nil {
		t.Error("PaidAt should be nil for unpaid orders")
	}
}

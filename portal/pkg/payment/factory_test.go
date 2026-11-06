package payment

import (
	"encoding/json"
	"testing"

	"go.uber.org/zap/zaptest"
)

// TestNewFactory tests factory creation.
func TestNewFactory(t *testing.T) {
	f := NewFactory(zaptest.NewLogger(t))
	if f == nil {
		t.Fatal("NewFactory() returned nil")
	}
}

// TestCreateGatewayStripe tests creating a Stripe gateway.
func TestCreateGatewayStripe(t *testing.T) {
	f := NewFactory(zaptest.NewLogger(t))

	cfg := StripeConfig{
		SecretKey:     "sk_test_123",
		WebhookSecret: "whsec_test",
	}
	cfgJSON, _ := json.Marshal(cfg)

	gw, err := f.CreateGateway("stripe", string(cfgJSON))
	if err != nil {
		t.Fatalf("CreateGateway(stripe) error: %v", err)
	}
	if gw == nil {
		t.Fatal("CreateGateway(stripe) returned nil")
	}
	if gw.Name() != "stripe" {
		t.Errorf("gateway name = %q, want stripe", gw.Name())
	}
}

// TestCreateGatewayAlipay tests creating an Alipay gateway.
func TestCreateGatewayAlipay(t *testing.T) {
	f := NewFactory(zaptest.NewLogger(t))

	cfg := AlipayConfig{
		ClientID:           "app123",
		MerchantPrivateKey: "key",
		AlipayPublicKey:    "pub",
	}
	cfgJSON, _ := json.Marshal(cfg)

	gw, err := f.CreateGateway("alipay", string(cfgJSON))
	if err != nil {
		t.Fatalf("CreateGateway(alipay) error: %v", err)
	}
	if gw == nil {
		t.Fatal("CreateGateway(alipay) returned nil")
	}
	if gw.Name() != "alipay" {
		t.Errorf("gateway name = %q, want alipay", gw.Name())
	}
}

// TestCreateGatewayInvalidConfig tests invalid config handling.
func TestCreateGatewayInvalidConfig(t *testing.T) {
	tests := []struct {
		name       string
		gateway    string
		configJSON string
		wantErr    bool
	}{
		{"stripe no secret key", "stripe", `{}`, true},
		{"alipay no client_id", "alipay", `{"merchant_private_key":"k"}`, true},
		{"alipay no private key", "alipay", `{"client_id":"a"}`, true},
		{"wechat missing fields", "wechat", `{"app_id":"a"}`, true},
		{"invalid json", "stripe", `{bad`, true},
		{"unknown gateway", "unknown", `{}`, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := NewFactory(zaptest.NewLogger(t))
			_, err := f.CreateGateway(tt.gateway, tt.configJSON)
			if (err != nil) != tt.wantErr {
				t.Errorf("CreateGateway(%q, ...) error = %v, wantErr = %v", tt.gateway, err, tt.wantErr)
			}
		})
	}
}

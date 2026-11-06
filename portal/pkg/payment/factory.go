package payment

import (
	"encoding/json"
	"fmt"

	"go.uber.org/zap"
)

// Factory creates payment gateways from configuration
type Factory struct {
	logger *zap.Logger
}

// NewFactory creates a new gateway factory
func NewFactory(logger *zap.Logger) *Factory {
	return &Factory{logger: logger}
}

// CreateGateway creates a gateway for the given provider
func (f *Factory) CreateGateway(gatewayName string, configJSON string) (Gateway, error) {
	switch gatewayName {
	case "stripe":
		var cfg StripeConfig
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			return nil, fmt.Errorf("invalid stripe config: %w", err)
		}
		if cfg.SecretKey == "" {
			return nil, fmt.Errorf("stripe secret key is required")
		}
		return NewStripeGateway(&cfg), nil

	case "paypal":
		var cfg PayPalConfig
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			return nil, fmt.Errorf("invalid paypal config: %w", err)
		}
		if cfg.ClientID == "" || cfg.Secret == "" {
			return nil, fmt.Errorf("PayPal client_id and secret are required")
		}
		gw, err := NewPayPalGateway(&cfg)
		if err != nil {
			return nil, err
		}
		return gw, nil

	case "alipay":
		var cfg AlipayConfig
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			return nil, fmt.Errorf("invalid alipay config: %w", err)
		}
		if cfg.ClientID == "" || cfg.MerchantPrivateKey == "" {
			return nil, fmt.Errorf("alipay client_id and merchant_private_key are required")
		}
		return NewAlipayGateway(&cfg), nil

	case "wechat":
		var cfg WechatConfig
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			return nil, fmt.Errorf("invalid wechat config: %w", err)
		}
		if cfg.MchID == "" || cfg.MchSerialNo == "" || cfg.MchPrivateKey == "" || cfg.ApiV3Key == "" {
			return nil, fmt.Errorf("WeChat mch_id, mch_serial_no, mch_private_key and apiv3_key are required")
		}
		gw, err := NewWechatGateway(&cfg)
		if err != nil {
			return nil, err
		}
		return gw, nil

	default:
		return nil, fmt.Errorf("unsupported gateway: %s", gatewayName)
	}
}

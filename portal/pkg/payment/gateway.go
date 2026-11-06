package payment

import (
	"fmt"
	"strings"
	"time"
)

// Gateway defines the interface for payment providers
type Gateway interface {
	// Name returns the gateway identifier
	Name() string
	// CreateSession creates a payment session and returns the payment URL
	CreateSession(order *Order) (paymentURL string, err error)
	// HandleWebhook processes a webhook callback and returns the result
	HandleWebhook(payload []byte, signature string) (*WebhookResult, error)
	// VerifySignature verifies the webhook signature
	VerifySignature(payload []byte, signature string) bool
	// QueryOrder queries the payment status from the provider
	QueryOrder(orderID string) (*QueryResult, error)
}

// Order represents a payment order. Amount is ALWAYS stored in micro-USD
// (1 USD = 1,000,000 micro-USD). Each gateway converts Amount to its own
// native unit (cents / 分 / major currency) inside CreateSession.
type Order struct {
	OrderID        string  `json:"order_id"`
	UserID         string  `json:"user_id"`
	Amount         int64   `json:"amount"`        // micro-USD (1 USD = 1,000,000)
	Currency       string  `json:"currency"`      // "usd", "cny"
	ExchangeRate   float64 `json:"exchange_rate"` // USD→target currency rate (e.g. 7.2 for CNY); 0 when target is USD
	Description    string  `json:"description"`
	ProductName    string  `json:"product_name"`
	GatewayOrderID string  `json:"gateway_order_id"` // provider-side order ID (e.g. PayPal order ID), set by CreateSession
}

// WebhookResult is the result of processing a webhook. Amount is in the
// gateway's native minor unit (Stripe=cents, WeChat=分, Alipay=major unit);
// callers must convert back to micro-USD with the matching helper before
// comparing to an order's Amount.
type WebhookResult struct {
	OrderID   string    `json:"order_id"`
	Status    string    `json:"status"` // "success", "failed", "refunded"
	PaymentID string    `json:"payment_id"`
	Amount    int64     `json:"amount"`
	Currency  string    `json:"currency"`
	PaidAt    time.Time `json:"paid_at"`
	RawData   []byte    `json:"-"`
}

// QueryResult is the result of querying an order. Amount is in the gateway's
// native unit; callers convert back to micro-USD before comparing.
type QueryResult struct {
	OrderID   string     `json:"order_id"`
	Status    string     `json:"status"` // "pending", "success", "failed", "closed"
	PaymentID string     `json:"payment_id"`
	Amount    int64      `json:"amount"`
	PaidAt    *time.Time `json:"paid_at"`
}

// microUSDPerUSD is the number of micro-USD units in one US dollar.
const microUSDPerUSD int64 = 1_000_000

// microUSDPerCent is the number of micro-USD units in one cent (minor USD unit):
// 1 USD = 100 cents = 1,000,000 micro-USD.
const microUSDPerCent int64 = 10_000

// MicroUSDToCents converts micro-USD to cents, the minor unit expected by
// Stripe (UnitAmount) and PayPal (value = cents/100).
func MicroUSDToCents(microUSD int64) int64 {
	return microUSD / microUSDPerCent
}

// CentsToMicroUSD converts cents (Stripe/PayPal minor unit) back to micro-USD.
func CentsToMicroUSD(cents int64) int64 {
	return cents * microUSDPerCent
}

// MicroUSDToCNYFen converts micro-USD to CNY 分 (1 yuan = 100 fen) using the
// USD→CNY exchange rate. micro-USD/1e6 = USD, ×rate = yuan, ×100 = fen.
func MicroUSDToCNYFen(microUSD int64, usdToCNYRate float64) int64 {
	return int64(float64(microUSD) / float64(microUSDPerUSD) * usdToCNYRate * 100)
}

// CNYFenToMicroUSD converts CNY 分 back to micro-USD using the USD→CNY rate.
func CNYFenToMicroUSD(fen int64, usdToCNYRate float64) int64 {
	if usdToCNYRate <= 0 {
		return 0
	}
	return int64(float64(fen) / 100 / usdToCNYRate * float64(microUSDPerUSD))
}

// MicroUSDToMajorUnitString formats micro-USD as a major-unit decimal string
// with two fraction digits (e.g. "6.50"), the representation Alipay Global's
// Amount.Value expects. When the target currency is CNY the rate is applied;
// for USD-denominated orders the rate is ignored.
func MicroUSDToMajorUnitString(microUSD int64, currency string, rate float64) string {
	if strings.EqualFold(currency, "CNY") && rate > 0 {
		return fmt.Sprintf("%.2f", float64(microUSD)/float64(microUSDPerUSD)*rate)
	}
	return fmt.Sprintf("%.2f", float64(microUSD)/float64(microUSDPerUSD))
}

// MajorUnitHundredthsToMicroUSD converts hundredths of a major currency unit
// (cents for USD-like currencies, fen for CNY) back to micro-USD. This is the
// inverse of MicroUSDToMajorUnitString and preserves fractional amounts; the
// previous integer major-unit conversion truncated $6.50 to $6.
func MajorUnitHundredthsToMicroUSD(hundredths int64, currency string, rate float64) int64 {
	if strings.EqualFold(currency, "CNY") {
		return CNYFenToMicroUSD(hundredths, rate)
	}
	return CentsToMicroUSD(hundredths)
}

// CNYFenToleranceMicroUSD returns the micro-USD value of one CNY fen at the
// given rate (plus one micro to cover float truncation). CNY amounts
// round-trip through integer fen, so paid-vs-order comparisons must tolerate
// this much rounding error instead of demanding exact equality.
func CNYFenToleranceMicroUSD(usdToCNYRate float64) int64 {
	if usdToCNYRate <= 0 {
		return 0
	}
	return int64(float64(microUSDPerUSD)/100/usdToCNYRate) + 1
}

// GatewayConfig holds gateway configuration
type GatewayConfig struct {
	Stripe *StripeConfig `json:"stripe"`
	PayPal *PayPalConfig `json:"paypal"`
	Alipay *AlipayConfig `json:"alipay"`
	Wechat *WechatConfig `json:"wechat"`
}

// StripeConfig holds Stripe configuration
type StripeConfig struct {
	SecretKey     string `json:"secret_key"`
	WebhookSecret string `json:"webhook_secret"`
	SuccessURL    string `json:"success_url"`
	CancelURL     string `json:"cancel_url"`
}

// AlipayConfig holds Alipay Global configuration
type AlipayConfig struct {
	ClientID           string `json:"client_id"`
	MerchantPrivateKey string `json:"merchant_private_key"`
	AlipayPublicKey    string `json:"alipay_public_key"`
	GatewayURL         string `json:"gateway_url"`
	NotifyURL          string `json:"notify_url"`
	ReturnURL          string `json:"return_url"`
}

// WechatConfig holds WeChat Pay V3 configuration
type WechatConfig struct {
	AppID             string `json:"app_id"`
	MchID             string `json:"mch_id"`
	MchSerialNo       string `json:"mch_serial_no"`
	MchPrivateKey     string `json:"mch_private_key"`
	ApiV3Key          string `json:"apiv3_key"`
	NotifyURL         string `json:"notify_url"`
	PlatformPublicKey string `json:"platform_public_key"` // PEM public key for webhook signature verification
}

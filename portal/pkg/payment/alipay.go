package payment

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"

	alipayApi "github.com/alipay/global-open-sdk-go/com/alipay/api"
	"github.com/alipay/global-open-sdk-go/com/alipay/api/model"
	"github.com/alipay/global-open-sdk-go/com/alipay/api/request/pay"
	responsePay "github.com/alipay/global-open-sdk-go/com/alipay/api/response/pay"
	"github.com/alipay/global-open-sdk-go/com/alipay/api/tools"
)

// AlipayGateway implements Alipay Global payment using the official SDK
type AlipayGateway struct {
	config *AlipayConfig
	client *alipayApi.DefaultAlipayClient
}

// NewAlipayGateway creates a new Alipay Global gateway
func NewAlipayGateway(config *AlipayConfig) *AlipayGateway {
	gatewayURL := config.GatewayURL
	if gatewayURL == "" {
		gatewayURL = "https://open.alipayplus.com"
	}

	client := alipayApi.NewDefaultAlipayClient(gatewayURL, config.ClientID, config.MerchantPrivateKey, config.AlipayPublicKey)

	return &AlipayGateway{config: config, client: client}
}

func (g *AlipayGateway) Name() string { return "alipay" }

func (g *AlipayGateway) CreateSession(order *Order) (string, error) {
	if g.config.ClientID == "" || g.config.MerchantPrivateKey == "" {
		return "", fmt.Errorf("alipay not configured")
	}

	// Alipay Global OrderAmount.Value is the major currency unit (e.g. dollars/yuan),
	// not micro-USD. Convert using the order's currency and exchange rate.
	currency := "SGD"
	if order.Currency != "" {
		currency = order.Currency
	}
	amountStr := MicroUSDToMajorUnitString(order.Amount, currency, order.ExchangeRate)

	alipayRequest, request := pay.NewAlipayPayRequest()
	request.PaymentRequestId = order.OrderID
	request.Order = &model.Order{
		OrderDescription: order.ProductName,
		ReferenceOrderId: order.OrderID,
		OrderAmount:      model.NewAmount(amountStr, currency),
	}
	request.PaymentAmount = model.NewAmount(amountStr, currency)
	request.PaymentNotifyUrl = g.config.NotifyURL
	request.PaymentRedirectUrl = g.config.ReturnURL + "?order_id=" + order.OrderID + "&status=success&gateway=alipay"
	request.ProductCode = model.ProductCodeType_CASHIER_PAYMENT

	result, err := g.client.Execute(alipayRequest)
	if err != nil {
		return "", fmt.Errorf("alipay API error: %w", err)
	}

	response, ok := result.(*responsePay.AlipayPayResponse)
	if !ok {
		return "", fmt.Errorf("unexpected response type")
	}

	if response.NormalUrl != "" {
		return response.NormalUrl, nil
	}
	if response.SchemeUrl != "" {
		return response.SchemeUrl, nil
	}

	return "", fmt.Errorf("no payment URL in response")
}

func (g *AlipayGateway) HandleWebhook(payload []byte, signature string) (*WebhookResult, error) {
	// Parse Alipay Global async notification. Both payment-result and refund
	// notifications share NotifyType/Result; refund-specific fields are optional.
	var notification struct {
		NotifyType       string `json:"notifyType"`
		PaymentRequestId string `json:"paymentRequestId"`
		PaymentId        string `json:"paymentId"`
		PaymentAmount    *struct {
			Value    string `json:"value"`
			Currency string `json:"currency"`
		} `json:"paymentAmount,omitempty"`
		PaymentResultInfo *struct {
			ResultStatus string `json:"resultStatus"`
		} `json:"paymentResultInfo,omitempty"`
		RefundRequestId string `json:"refundRequestId,omitempty"`
		RefundId        string `json:"refundId,omitempty"`
		RefundAmount    *struct {
			Value    string `json:"value"`
			Currency string `json:"currency"`
		} `json:"refundAmount,omitempty"`
		RefundStatus string `json:"refundStatus,omitempty"`
		RefundTime   string `json:"refundTime,omitempty"`
	}
	if err := json.Unmarshal(payload, &notification); err != nil {
		return nil, fmt.Errorf("failed to parse notification: %w", err)
	}

	// Parse amount helper: major-unit decimal string -> hundredths of major unit.
	parseAmount := func(a *struct {
		Value    string `json:"value"`
		Currency string `json:"currency"`
	}, fallbackCurrency string) (int64, string) {
		if a == nil {
			return 0, fallbackCurrency
		}
		var amt int64
		if v, err := strconv.ParseFloat(a.Value, 64); err == nil {
			amt = int64(math.Round(v * 100))
		}
		currency := a.Currency
		if currency == "" {
			currency = fallbackCurrency
		}
		return amt, currency
	}

	// Refund notification
	if notification.RefundId != "" || notification.RefundRequestId != "" {
		amount, currency := parseAmount(notification.RefundAmount, "USD")
		result := &WebhookResult{
			OrderID:   firstNonEmpty(notification.RefundRequestId, notification.PaymentRequestId),
			PaymentID: firstNonEmpty(notification.PaymentId, notification.RefundId),
			Amount:    amount,
			Currency:  currency,
			PaidAt:    time.Now(),
			RawData:   payload,
		}
		if notification.RefundStatus == "SUCCESS" || notification.RefundStatus == "S" {
			result.Status = "refunded"
			if t, err := time.Parse(time.RFC3339, notification.RefundTime); err == nil {
				result.PaidAt = t
			}
		} else {
			result.Status = "pending"
		}
		return result, nil
	}

	// Payment notification
	amount, currency := parseAmount(notification.PaymentAmount, "USD")
	result := &WebhookResult{
		OrderID:   notification.PaymentRequestId,
		PaymentID: notification.PaymentId,
		Amount:    amount,
		Currency:  currency,
		PaidAt:    time.Now(),
		RawData:   payload,
	}

	if notification.PaymentResultInfo != nil && notification.PaymentResultInfo.ResultStatus == "S" {
		result.Status = "success"
	} else {
		result.Status = "pending"
	}

	return result, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func (g *AlipayGateway) VerifySignature(payload []byte, signature string) bool {
	// The generic Gateway.VerifySignature interface cannot carry the Alipay
	// Global webhook headers (client-id, request-time, request path). Real
	// verification is performed by VerifyWebhookSignature in the route handler.
	return false
}

// VerifyWebhookSignature verifies an Alipay Global async notification
// signature using the SDK's CheckSignature tool. The signature is read from
// the alipay-signature HTTP header; client-id, request-time and the request
// path are required to reconstruct the signed content. Fails closed when no
// AlipayPublicKey is configured.
func (g *AlipayGateway) VerifyWebhookSignature(httpMethod, requestURI, clientID, requestTime, body, signature string) bool {
	if g.config.AlipayPublicKey == "" || signature == "" {
		return false
	}
	ok, err := tools.CheckSignature(requestURI, httpMethod, clientID, requestTime, body, signature, g.config.AlipayPublicKey)
	if err != nil {
		return false
	}
	return ok
}

func (g *AlipayGateway) QueryOrder(orderID string) (*QueryResult, error) {
	return nil, fmt.Errorf("alipay Global order query: use webhooks")
}

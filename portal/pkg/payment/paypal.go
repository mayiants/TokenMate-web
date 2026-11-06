package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/plutov/paypal/v4"
)

// PayPalConfig holds PayPal configuration
type PayPalConfig struct {
	ClientID  string `json:"client_id"`
	Secret    string `json:"secret"`
	Mode      string `json:"mode"` // "sandbox" or "live"
	ReturnURL string `json:"return_url"`
	CancelURL string `json:"cancel_url"`
	WebhookID string `json:"webhook_id"` // required for webhook signature verification
}

// PayPalGateway implements the PayPal Orders v2 API using the official SDK
type PayPalGateway struct {
	config *PayPalConfig
	client *paypal.Client
}

// NewPayPalGateway creates a new PayPal gateway
func NewPayPalGateway(config *PayPalConfig) (*PayPalGateway, error) {
	base := paypal.APIBaseSandBox
	if config.Mode == "live" {
		base = paypal.APIBaseLive
	}

	client, err := paypal.NewClient(config.ClientID, config.Secret, base)
	if err != nil {
		return nil, fmt.Errorf("failed to create PayPal client: %w", err)
	}

	return &PayPalGateway{config: config, client: client}, nil
}

func (g *PayPalGateway) Name() string { return "paypal" }

func (g *PayPalGateway) CreateSession(order *Order) (string, error) {
	// Convert micro-USD → cents → decimal dollars string (PayPal value)
	amountStr := fmt.Sprintf("%.2f", float64(MicroUSDToCents(order.Amount))/100.0)

	currency := "USD"
	if order.Currency != "" {
		currency = order.Currency
	}

	purchaseUnits := []paypal.PurchaseUnitRequest{{
		Amount: &paypal.PurchaseUnitAmount{
			Currency: currency,
			Value:    amountStr,
		},
		Description: order.ProductName,
		CustomID:    order.OrderID,
	}}

	appCtx := &paypal.ApplicationContext{
		ReturnURL: g.config.ReturnURL + "?order_id=" + order.OrderID + "&status=success&gateway=paypal",
		CancelURL: g.config.CancelURL + "?order_id=" + order.OrderID + "&status=cancel&gateway=paypal",
	}

	result, err := g.client.CreateOrder(context.Background(), paypal.OrderIntentCapture, purchaseUnits, nil, appCtx)
	if err != nil {
		return "", fmt.Errorf("PayPal create order error: %w", err)
	}

	// Persist the PayPal-returned order ID so queryGatewayAndComplete can
	// look up the order with PayPal later (PayPal does not recognize our
	// internal order_id).
	order.GatewayOrderID = result.ID

	// Find the approval link
	for _, link := range result.Links {
		if link.Rel == "approve" {
			return link.Href, nil
		}
	}

	return "", fmt.Errorf("PayPal approval link not found")
}

func (g *PayPalGateway) HandleWebhook(payload []byte, signature string) (*WebhookResult, error) {
	// PayPal webhook payload is a JSON event envelope. Signature verification is
	// done beforehand via VerifyWebhookSignature using the raw HTTP request.
	var event struct {
		ID        string          `json:"id"`
		EventType string          `json:"event_type"`
		Resource  json.RawMessage `json:"resource"`
		Summary   string          `json:"summary"`
	}
	if err := json.Unmarshal(payload, &event); err != nil {
		return nil, fmt.Errorf("failed to parse PayPal webhook: %w", err)
	}

	switch event.EventType {
	case "CHECKOUT.ORDER.APPROVED", "CHECKOUT.ORDER.COMPLETED":
		return g.parseOrderEvent(event.ID, event.Resource, payload)
	case "PAYMENT.CAPTURE.COMPLETED":
		return g.parseCaptureEvent(event.ID, event.Resource, payload)
	case "PAYMENT.CAPTURE.REFUNDED":
		return g.parseRefundCaptureEvent(event.ID, event.Resource, payload)
	default:
		return nil, fmt.Errorf("unsupported PayPal event type: %s", event.EventType)
	}
}

func (g *PayPalGateway) parseOrderEvent(eventID string, resource json.RawMessage, payload []byte) (*WebhookResult, error) {
	var order paypal.Order
	if err := json.Unmarshal(resource, &order); err != nil {
		return nil, fmt.Errorf("failed to parse order resource: %w", err)
	}
	if len(order.PurchaseUnits) == 0 {
		return nil, fmt.Errorf("PayPal order event has no purchase units")
	}
	return &WebhookResult{
		OrderID:   order.PurchaseUnits[0].CustomID,
		PaymentID: order.ID,
		Status:    "success",
		Amount:    0, // amount verified by completeOrder via QueryOrder or captured amount
		Currency:  order.PurchaseUnits[0].Amount.Currency,
		PaidAt:    time.Now(),
		RawData:   payload,
	}, nil
}

func (g *PayPalGateway) parseCaptureEvent(eventID string, resource json.RawMessage, payload []byte) (*WebhookResult, error) {
	var capture struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Amount struct {
			CurrencyCode string `json:"currency_code"`
			Value        string `json:"value"`
		} `json:"amount"`
		CustomID string `json:"custom_id"`
	}
	if err := json.Unmarshal(resource, &capture); err != nil {
		return nil, fmt.Errorf("failed to parse capture resource: %w", err)
	}

	amountCents := paypalAmountToCents(capture.Amount.Value)
	status := "pending"
	if capture.Status == "COMPLETED" {
		status = "success"
	}
	return &WebhookResult{
		OrderID:   capture.CustomID,
		PaymentID: capture.ID,
		Status:    status,
		Amount:    amountCents,
		Currency:  capture.Amount.CurrencyCode,
		PaidAt:    time.Now(),
		RawData:   payload,
	}, nil
}

func (g *PayPalGateway) parseRefundCaptureEvent(eventID string, resource json.RawMessage, payload []byte) (*WebhookResult, error) {
	var refund struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Amount struct {
			CurrencyCode string `json:"currency_code"`
			Value        string `json:"value"`
		} `json:"amount"`
		CaptureID string `json:"capture_id"`
		CustomID  string `json:"custom_id"`
	}
	if err := json.Unmarshal(resource, &refund); err != nil {
		return nil, fmt.Errorf("failed to parse refund resource: %w", err)
	}

	amountCents := paypalAmountToCents(refund.Amount.Value)
	status := "pending"
	if refund.Status == "COMPLETED" {
		status = "refunded"
	}
	return &WebhookResult{
		OrderID:   refund.CustomID,
		PaymentID: refund.CaptureID,
		Status:    status,
		Amount:    amountCents,
		Currency:  refund.Amount.CurrencyCode,
		PaidAt:    time.Now(),
		RawData:   payload,
	}, nil
}

// paypalAmountToCents converts a decimal major-unit string (e.g. "10.50") to
// cents, rounding to the nearest integer.
func paypalAmountToCents(value string) int64 {
	if value == "" {
		return 0
	}
	var dollars float64
	if _, err := fmt.Sscanf(value, "%f", &dollars); err != nil {
		return 0
	}
	return int64(math.Round(dollars * 100))
}

func (g *PayPalGateway) VerifySignature(payload []byte, signature string) bool {
	// PayPal signature verification requires the full HTTP request. Use
	// VerifyWebhookSignature instead; this method remains for interface
	// compatibility and always fails closed.
	return false
}

// VerifyWebhookSignature verifies a PayPal webhook using PayPal's official
// verify-webhook-signature API. The webhook_id must be configured.
func (g *PayPalGateway) VerifyWebhookSignature(httpReq *http.Request) bool {
	if g.config.WebhookID == "" {
		return false
	}
	resp, err := g.client.VerifyWebhookSignature(context.Background(), httpReq, g.config.WebhookID)
	if err != nil || resp == nil {
		return false
	}
	return resp.VerificationStatus == "SUCCESS"
}

func (g *PayPalGateway) QueryOrder(orderID string) (*QueryResult, error) {
	// PayPal flow: after user approves, capture the order
	order, err := g.client.GetOrder(context.Background(), orderID)
	if err != nil {
		return nil, fmt.Errorf("PayPal get order error: %w", err)
	}

	result := &QueryResult{
		PaymentID: order.ID,
	}
	// PayPal may return an order without purchase units; indexing blindly
	// would panic the webhook/callback path.
	if len(order.PurchaseUnits) == 0 {
		return nil, fmt.Errorf("PayPal order %s has no purchase units", orderID)
	}
	result.OrderID = order.PurchaseUnits[0].CustomID

	switch order.Status {
	case "APPROVED":
		// Capture the payment
		capture, err := g.client.CaptureOrder(context.Background(), orderID, paypal.CaptureOrderRequest{})
		if err != nil {
			return nil, fmt.Errorf("PayPal capture error: %w", err)
		}
		if capture.Status == "COMPLETED" {
			result.Status = "success"
			now := time.Now()
			result.PaidAt = &now
			if len(capture.PurchaseUnits) > 0 && len(capture.PurchaseUnits[0].Payments.Captures) > 0 {
				result.PaymentID = capture.PurchaseUnits[0].Payments.Captures[0].ID
			}
		} else {
			result.Status = "pending"
		}
	case "COMPLETED":
		result.Status = "success"
		now := time.Now()
		result.PaidAt = &now
	default:
		result.Status = "pending"
	}

	return result, nil
}

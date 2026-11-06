package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/stripe/stripe-go/v86"
	"github.com/stripe/stripe-go/v86/webhook"
)

// StripeGateway implements the Stripe Checkout Session API using the official SDK
type StripeGateway struct {
	config *StripeConfig
	client *stripe.Client
}

// NewStripeGateway creates a new Stripe gateway
func NewStripeGateway(config *StripeConfig) *StripeGateway {
	return &StripeGateway{
		config: config,
		client: stripe.NewClient(config.SecretKey),
	}
}

func (g *StripeGateway) Name() string { return "stripe" }

func (g *StripeGateway) CreateSession(order *Order) (string, error) {
	if g.config.SecretKey == "" {
		return "", fmt.Errorf("stripe secret key not configured")
	}

	params := &stripe.CheckoutSessionCreateParams{
		Mode:       stripe.String(string(stripe.CheckoutSessionModePayment)),
		SuccessURL: stripe.String(g.config.SuccessURL + "?order_id=" + order.OrderID),
		CancelURL:  stripe.String(g.config.CancelURL + "?order_id=" + order.OrderID),
		LineItems: []*stripe.CheckoutSessionCreateLineItemParams{{
			PriceData: &stripe.CheckoutSessionCreateLineItemPriceDataParams{
				Currency:   stripe.String(order.Currency),
				UnitAmount: stripe.Int64(MicroUSDToCents(order.Amount)),
				ProductData: &stripe.CheckoutSessionCreateLineItemPriceDataProductDataParams{
					Name: stripe.String(order.ProductName),
				},
			},
			Quantity: stripe.Int64(1),
		}},
	}
	params.AddMetadata("order_id", order.OrderID)
	params.AddMetadata("user_id", order.UserID)

	s, err := g.client.V1CheckoutSessions.Create(context.Background(), params)
	if err != nil {
		return "", fmt.Errorf("stripe API error: %w", err)
	}

	return s.URL, nil
}

func (g *StripeGateway) HandleWebhook(payload []byte, signature string) (*WebhookResult, error) {
	event, err := webhook.ConstructEvent(payload, signature, g.config.WebhookSecret)
	if err != nil {
		return nil, fmt.Errorf("webhook verification failed: %w", err)
	}

	if event.Type == "charge.refunded" {
		// Refund notifications reference the payment intent (stored as
		// orders.payment_id at completion), not the checkout session.
		var ch struct {
			PaymentIntent  string `json:"payment_intent"`
			AmountRefunded int64  `json:"amount_refunded"`
			Currency       string `json:"currency"`
		}
		if err := json.Unmarshal(event.Data.Raw, &ch); err != nil {
			return nil, fmt.Errorf("failed to parse refunded charge: %w", err)
		}
		return &WebhookResult{
			PaymentID: ch.PaymentIntent,
			Amount:    ch.AmountRefunded,
			Currency:  ch.Currency,
			PaidAt:    time.Now(),
			RawData:   payload,
			Status:    "refunded",
		}, nil
	}

	if event.Type != "checkout.session.completed" {
		return nil, fmt.Errorf("unsupported event type: %s", event.Type)
	}

	// Parse the checkout session from event data
	var cs struct {
		PaymentIntent string            `json:"payment_intent"`
		AmountTotal   int64             `json:"amount_total"`
		Currency      string            `json:"currency"`
		Metadata      map[string]string `json:"metadata"`
	}
	if err := json.Unmarshal(event.Data.Raw, &cs); err != nil {
		return nil, fmt.Errorf("failed to parse checkout session: %w", err)
	}

	return &WebhookResult{
		OrderID:   cs.Metadata["order_id"],
		PaymentID: cs.PaymentIntent,
		Amount:    cs.AmountTotal,
		Currency:  cs.Currency,
		PaidAt:    time.Now(),
		RawData:   payload,
		Status:    "success",
	}, nil
}

func (g *StripeGateway) VerifySignature(payload []byte, signature string) bool {
	if g.config.WebhookSecret == "" {
		return false
	}
	_, err := webhook.ConstructEvent(payload, signature, g.config.WebhookSecret)
	return err == nil
}

func (g *StripeGateway) QueryOrder(orderID string) (*QueryResult, error) {
	return nil, fmt.Errorf("stripe order query not supported, use webhooks")
}

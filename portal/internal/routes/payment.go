package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mayiants/ai-portal-go/internal/config"
	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/internal/middleware"
	"github.com/mayiants/ai-portal-go/internal/storage"
	"github.com/mayiants/ai-portal-go/pkg/audit"
	"github.com/mayiants/ai-portal-go/pkg/invoicepdf"
	"github.com/mayiants/ai-portal-go/pkg/metrics"
	"github.com/mayiants/ai-portal-go/pkg/payment"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

type PaymentRoutes struct {
	db      *db.DB
	cfg     *config.Config
	logger  *zap.Logger
	auth    *middleware.Auth
	audit   *audit.Logger
	factory *payment.Factory
	storage storage.Provider
}

func NewPaymentRoutes(db *db.DB, cfg *config.Config, logger *zap.Logger, auth *middleware.Auth, auditLog *audit.Logger, storageProvider storage.Provider) *PaymentRoutes {
	return &PaymentRoutes{
		db:      db,
		cfg:     cfg,
		logger:  logger,
		auth:    auth,
		audit:   auditLog,
		factory: payment.NewFactory(logger),
		storage: storageProvider,
	}
}

func (r *PaymentRoutes) Register(rg *gin.RouterGroup) {
	// Admin payment configuration
	admin := rg.Group("/admin/payment")
	admin.Use(r.auth.RequireAuth(), r.auth.RequireAdmin())
	{
		admin.GET("/gateways", r.listGateways)
		admin.PUT("/gateways/:gateway", r.updateGateway)
		admin.POST("/gateways/:gateway/test", r.testGateway)
	}

	// User payment operations
	payment := rg.Group("/payment")
	payment.Use(r.auth.RequireAuth())
	{
		payment.POST("/create", r.createPayment)
		payment.GET("/status/:orderId", r.getPaymentStatus)
	}

	// Webhook endpoints (no auth, verified by signature)
	rg.POST("/webhook/stripe", r.handleStripeWebhook)
	rg.POST("/webhook/paypal", r.handlePayPalWebhook)
	rg.POST("/webhook/alipay", r.handleAlipayWebhook)
	rg.POST("/webhook/wechat", r.handleWechatWebhook)

	// Return-redirect callback for payment providers (browser redirect back from Alipay/WeChat/Stripe).
	// OptionalAuth lets owner-only actions (order cancel) identify the caller.
	rg.GET("/billing/callback", r.auth.OptionalAuth(), r.handleBillingCallback)
}

// listGateways returns payment gateway configurations with secret fields masked.
// Sensitive values (private keys, secrets, APIv3 keys) never leave the server;
// they are replaced with a "****1234"-style mask.
func (r *PaymentRoutes) listGateways(c *gin.Context) {
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT id::text, gateway, enabled, created_at, updated_at,
		        COALESCE(config::text, '{}') AS config
		 FROM payment_gateways ORDER BY gateway`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list gateways"}})
		return
	}
	defer rows.Close()

	gateways := []gin.H{}
	for rows.Next() {
		var id, gateway, configStr string
		var enabled bool
		var createdAt, updatedAt interface{}
		if err := rows.Scan(&id, &gateway, &enabled, &createdAt, &updatedAt, &configStr); err != nil {
			continue
		}
		config := map[string]interface{}{}
		_ = json.Unmarshal([]byte(configStr), &config)
		payment.MaskSensitiveFields(gateway, config, r.cfg.Auth.EncryptionKey)
		gateways = append(gateways, gin.H{
			"id": id, "gateway": gateway, "config": config,
			"enabled": enabled, "created_at": createdAt, "updated_at": updatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"gateways": gateways})
}

// gatewayDisplayNames maps gateway identifiers to human-readable names.
var gatewayDisplayNames = map[string]string{
	"stripe": "Stripe",
	"paypal": "PayPal",
	"alipay": "Alipay",
	"wechat": "WeChat Pay",
}

// updateGateway updates a payment gateway configuration (upsert).
// Sensitive fields are encrypted at rest; values submitted as UI masks (or
// left empty) keep the previously stored ciphertext.
func (r *PaymentRoutes) updateGateway(c *gin.Context) {
	gateway := c.Param("gateway")
	name, ok := gatewayDisplayNames[gateway]
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Unsupported gateway"}})
		return
	}

	var req struct {
		Config  map[string]interface{} `json:"config"`
		Enabled *bool                  `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	var configArg interface{}
	if req.Config != nil {
		// Load existing config so masked/empty sensitive fields keep their stored value.
		var existingStr string
		_ = r.db.QueryRow(c.Request.Context(),
			`SELECT COALESCE(config::text, '{}') FROM payment_gateways WHERE gateway = $1`, gateway,
		).Scan(&existingStr)
		existing := map[string]interface{}{}
		_ = json.Unmarshal([]byte(existingStr), &existing)

		if r.cfg.Auth.EncryptionKey == "" {
			r.logger.Warn("Payment gateway secret will be stored in plaintext — ENCRYPTION_KEY not configured",
				zap.String("gateway", gateway))
		}
		if err := payment.EncryptSensitiveFields(gateway, req.Config, existing, r.cfg.Auth.EncryptionKey); err != nil {
			r.logger.Error("Failed to encrypt payment gateway secret", zap.String("gateway", gateway), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to encrypt gateway secret"}})
			return
		}
		configArg = req.Config
	}

	var enabledArg interface{}
	if req.Enabled != nil {
		enabledArg = *req.Enabled
	}

	if err := r.db.Exec(c.Request.Context(),
		`INSERT INTO payment_gateways (name, provider, gateway, enabled, config, created_at, updated_at)
		 VALUES ($1, $2, $3, COALESCE($4::boolean, false), COALESCE($5::jsonb, '{}'), NOW(), NOW())
		 ON CONFLICT (gateway) DO UPDATE SET
		     config = COALESCE($5::jsonb, payment_gateways.config),
		     enabled = COALESCE($4::boolean, payment_gateways.enabled),
		     updated_at = NOW()`,
		name, gateway, gateway, enabledArg, configArg,
	); err != nil {
		r.logger.Error("Failed to update payment gateway", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update gateway"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// stripeConfigWithURLDefaults fills success_url/cancel_url from APP_BASE_URL when
// the saved gateway config omits them. The /super/payment form does not collect
// these URLs; without them Stripe Checkout session creation would fail.
func (r *PaymentRoutes) stripeConfigWithURLDefaults(configJSON string) string {
	var cfg map[string]interface{}
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return configJSON
	}
	base := strings.TrimRight(r.cfg.App.BaseURL, "/")
	if base == "" {
		return configJSON
	}
	if s, _ := cfg["success_url"].(string); s == "" {
		cfg["success_url"] = base + "/billing?payment=success"
	}
	if s, _ := cfg["cancel_url"].(string); s == "" {
		cfg["cancel_url"] = base + "/billing?payment=cancelled"
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		return configJSON
	}
	return string(out)
}

// testGateway tests a payment gateway connection
func (r *PaymentRoutes) testGateway(c *gin.Context) {
	gatewayName := c.Param("gateway")

	// Get gateway config
	var configStr string
	var enabled bool
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT config::text, enabled FROM payment_gateways WHERE gateway = $1`,
		gatewayName,
	).Scan(&configStr, &enabled)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Gateway not found"}})
		return
	}

	if !enabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Gateway is disabled"}})
		return
	}

	// Try to create gateway instance
	_, err = r.factory.CreateGateway(gatewayName, payment.DecryptGatewayConfig(gatewayName, configStr, r.cfg.Auth.EncryptionKey))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "Gateway configuration error",
			"gateway": gatewayName,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Gateway configuration is valid",
		"gateway": gatewayName,
		"enabled": enabled,
	})
}

// createPayment creates a payment session
// createPayment creates a payment session
func (r *PaymentRoutes) createPayment(c *gin.Context) {
	userID := c.GetString("user_id")

	var req struct {
		OrderID       string `json:"order_id" binding:"required"`
		PaymentMethod string `json:"payment_method" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Order ID and payment method are required"}})
		return
	}

	// Get order details — including currency & exchange_rate so the payment
	// gateway receives the correct native amount (P0: amounts were previously
	// passed to gateways as raw micro-USD, causing 100×–10000× overcharges).
	var orderID, orderStatus, productType, currency string
	var amount int64
	var exchangeRate float64
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT order_id, amount, status, COALESCE(product_type, ''),
		        COALESCE(currency, 'USD'), COALESCE(exchange_rate, 0)
		 FROM orders WHERE (id::text = $1 OR order_id = $1) AND user_id = $2::uuid`,
		req.OrderID, userID,
	).Scan(&orderID, &amount, &orderStatus, &productType, &currency, &exchangeRate)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Order not found"}})
		return
	}

	if orderStatus != "pending" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Order is not pending"}})
		return
	}

	// Get gateway config
	var configStr string
	var enabled bool
	gatewayErr := r.db.QueryRow(c.Request.Context(),
		`SELECT config::text, enabled FROM payment_gateways WHERE gateway = $1`,
		req.PaymentMethod,
	).Scan(&configStr, &enabled)

	// Check if simulation mode is forced by admin
	var simulationMode string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COALESCE(value, 'false') FROM system_settings WHERE key = 'payment_simulation_mode'`,
	).Scan(&simulationMode)
	forceSimulate := simulationMode == "true"

	// If simulation is forced by admin, use it
	if forceSimulate {
		r.logger.Info("Simulation mode forced by admin", zap.String("gateway", req.PaymentMethod))
	}

	// If not forced simulation, check if gateway is available
	if !forceSimulate {
		if gatewayErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.payment_gateway_not_configured", c)}})
			return
		}
		if !enabled {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.payment_gateway_disabled", c)}})
			return
		}
	}

	useSimulate := forceSimulate

	var paymentURL string
	var gatewayOrderID string
	if !useSimulate {
		// Create gateway
		configJSON := payment.DecryptGatewayConfig(req.PaymentMethod, configStr, r.cfg.Auth.EncryptionKey)
		if req.PaymentMethod == "stripe" {
			configJSON = r.stripeConfigWithURLDefaults(configJSON)
		}
		gw, gwErr := r.factory.CreateGateway(req.PaymentMethod, configJSON)
		if gwErr != nil {
			r.logger.Error("Failed to create payment gateway",
				zap.String("gateway", req.PaymentMethod),
				zap.Error(gwErr))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Payment gateway initialization failed. Please try again later."}})
			return
		}
		// Create payment session
		var sessionErr error
		payOrder := &payment.Order{
			OrderID:      orderID,
			UserID:       userID,
			Amount:       int64(amount),
			Currency:     currency,
			ExchangeRate: exchangeRate,
			Description:  productType,
			ProductName:  productType,
		}
		paymentURL, sessionErr = gw.CreateSession(payOrder)
		if sessionErr != nil {
			r.logger.Error("Failed to create payment session",
				zap.String("gateway", req.PaymentMethod),
				zap.Error(sessionErr))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Payment session creation failed. Please try again later."}})
			return
		}
		// Capture the provider-side order ID (e.g. PayPal's order ID) so the
		// callback flow can query the gateway with it later.
		gatewayOrderID = payOrder.GatewayOrderID
	}

	// Simulation mode: auto-complete the order
	if useSimulate {
		// completeOrder handles status update + balance credit atomically in a transaction
		r.completeOrder(c, orderID, "simulated", 0, "")
		paymentURL = "/orders?toast=success&message=" + url.QueryEscape(T("msg.recharge_success_simulated", c))
	} else {
		// Update payment method for real gateway
		if err := r.db.Exec(c.Request.Context(),
			`UPDATE orders SET payment_method = $1, gateway_order_id = COALESCE(NULLIF($2, ''), gateway_order_id), updated_at = NOW() WHERE order_id = $3`,
			req.PaymentMethod, gatewayOrderID, orderID); err != nil {
			r.logger.Error("Failed to update order payment method", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to process payment"}})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success":     true,
		"order_id":    orderID,
		"amount":      amount,
		"payment_url": paymentURL,
		"method":      req.PaymentMethod,
		"simulated":   useSimulate,
	})
}

// getPaymentStatus returns payment status for an order
func (r *PaymentRoutes) getPaymentStatus(c *gin.Context) {
	orderID := c.Param("orderId")
	userID := c.GetString("user_id")

	var status, paymentMethod string
	var amount int64
	var paidAt interface{}

	// orders has no paid_at column; completed_at is set when payment completes.
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT status, COALESCE(payment_method, ''), amount, completed_at
		 FROM orders WHERE id = $1::uuid AND user_id = $2::uuid`,
		orderID, userID,
	).Scan(&status, &paymentMethod, &amount, &paidAt)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Order not found"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":         status,
		"payment_method": paymentMethod,
		"amount":         amount,
		"paid_at":        paidAt,
	})
}

// handleStripeWebhook handles Stripe webhook
func (r *PaymentRoutes) handleStripeWebhook(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}

	// Get gateway config
	var configStr string
	err = r.db.QueryRow(c.Request.Context(),
		`SELECT config::text FROM payment_gateways WHERE gateway = 'stripe'`,
	).Scan(&configStr)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "stripe not configured"})
		return
	}

	gw, err := r.factory.CreateGateway("stripe", payment.DecryptGatewayConfig("stripe", configStr, r.cfg.Auth.EncryptionKey))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "gateway error"})
		return
	}

	// Verify signature
	sig := c.GetHeader("Stripe-Signature")
	if !gw.VerifySignature(body, sig) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return
	}

	// Handle webhook
	result, err := gw.HandleWebhook(body, sig)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"received": true})
		return
	}

	// Update order status
	switch result.Status {
	case "success":
		r.completeOrder(c, result.OrderID, result.PaymentID, result.Amount, "stripe")
	case "refunded":
		r.refundOrder(c, result.PaymentID)
	}

	c.JSON(http.StatusOK, gin.H{"received": true})
}

// refundOrder reverses a completed order after a gateway refund (e.g. Stripe
// charge.refunded). For recharge orders the credited balance is clawed back;
// for subscription orders the unused portion is credited to user balance and
// the subscription is cancelled. Idempotent via the conditional status flip
// (completed → refunded).
func (r *PaymentRoutes) refundOrder(c *gin.Context, paymentID string) {
	if paymentID == "" {
		return
	}
	ctx := c.Request.Context()

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		r.logger.Error("Failed to begin transaction for refund", zap.Error(err))
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orderUUID, orderID, userID, productType string
	var amount int64
	err = tx.QueryRow(ctx,
		`UPDATE orders SET status = 'refunded', updated_at = NOW()
		 WHERE payment_id = $1 AND status = 'completed'
		 RETURNING id::text, order_id, user_id::text, COALESCE(product_type, ''), amount`,
		paymentID,
	).Scan(&orderUUID, &orderID, &userID, &productType, &amount)
	if err != nil {
		// Unknown payment or already refunded — nothing to do.
		r.logger.Warn("Refund for unknown or non-completed order, ignored",
			zap.String("payment_id", paymentID))
		return
	}

	if err := r.processRefundCredits(ctx, tx, orderUUID, orderID, userID, productType, amount); err != nil {
		r.logger.Error("Failed to process refund credits", zap.String("order_id", orderID), zap.Error(err))
		return
	}

	if err := tx.Commit(ctx); err != nil {
		r.logger.Error("Failed to commit refund", zap.String("order_id", orderID), zap.Error(err))
		return
	}

	r.logger.Info("Order refunded",
		zap.String("order_id", orderID),
		zap.String("user_id", userID),
		zap.String("product_type", productType),
		zap.Int64("amount", amount))
	if r.audit != nil {
		r.audit.Log(ctx, userID, "", "billing", "order_refunded", "warning",
			fmt.Sprintf("Order %s refunded via gateway webhook", orderID))
	}
}

// refundOrderByOrderID is used by gateways (e.g. Alipay) whose refund
// notification may not carry the original payment id. It looks the order up by
// the merchant order_id and applies the same refund logic.
func (r *PaymentRoutes) refundOrderByOrderID(c *gin.Context, orderID string) {
	if orderID == "" {
		return
	}
	ctx := c.Request.Context()

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		r.logger.Error("Failed to begin transaction for refund", zap.Error(err))
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orderUUID, userID, productType string
	var amount int64
	err = tx.QueryRow(ctx,
		`UPDATE orders SET status = 'refunded', updated_at = NOW()
		 WHERE order_id = $1 AND status = 'completed'
		 RETURNING id::text, user_id::text, COALESCE(product_type, ''), amount`,
		orderID,
	).Scan(&orderUUID, &userID, &productType, &amount)
	if err != nil {
		r.logger.Warn("Refund by order_id for unknown or non-completed order, ignored",
			zap.String("order_id", orderID))
		return
	}

	if err := r.processRefundCredits(ctx, tx, orderUUID, orderID, userID, productType, amount); err != nil {
		r.logger.Error("Failed to process refund credits", zap.String("order_id", orderID), zap.Error(err))
		return
	}

	if err := tx.Commit(ctx); err != nil {
		r.logger.Error("Failed to commit refund", zap.String("order_id", orderID), zap.Error(err))
		return
	}

	r.logger.Info("Order refunded by order_id",
		zap.String("order_id", orderID),
		zap.String("user_id", userID),
		zap.String("product_type", productType),
		zap.Int64("amount", amount))
	if r.audit != nil {
		r.audit.Log(ctx, userID, "", "billing", "order_refunded", "warning",
			fmt.Sprintf("Order %s refunded via gateway webhook", orderID))
	}
}

// processRefundCredits handles the balance/subscription side effects of a
// gateway refund. It runs inside the caller's transaction.
func (r *PaymentRoutes) processRefundCredits(ctx context.Context, tx pgx.Tx, orderUUID, orderID, userID, productType string, amount int64) error {
	if productType == "subscription" || productType == "subscription_renewal" {
		// Credit the unused portion back to user balance and cancel the
		// subscription. Do not perform a gateway refund — the design policy is
		// to keep the money on platform as balance credit.
		refundAmount, subID, err := r.calculateSubscriptionRefund(ctx, tx, orderUUID, userID, amount)
		if err != nil {
			return err
		}
		if refundAmount > 0 {
			if err := r.creditUserBalanceTx(ctx, tx, userID, refundAmount, orderID, "subscription_refund", fmt.Sprintf("Subscription refund credit for order %s", orderID)); err != nil {
				return err
			}
		}
		if subID != "" {
			if _, err := tx.Exec(ctx,
				`UPDATE user_subscriptions SET status = 'cancelled', updated_at = NOW()
				 WHERE id = $1::uuid AND user_id = $2::uuid AND status = 'active'`,
				subID, userID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx,
			`UPDATE subscription_purchases SET status = 'refunded', updated_at = NOW()
			 WHERE order_id = $1::uuid AND status = 'active'`,
			orderUUID); err != nil {
			return err
		}
		return nil
	}

	// Recharge order: claw back the credited balance.
	var balanceBefore int64
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(balance, 0) FROM users WHERE id = $1::uuid FOR UPDATE`,
		userID).Scan(&balanceBefore); err != nil {
		return fmt.Errorf("lock user balance: %w", err)
	}
	balanceAfter := balanceBefore - amount
	if balanceAfter < 0 {
		balanceAfter = 0
		r.logger.Warn("Refund exceeds remaining balance",
			zap.String("order_id", orderID),
			zap.String("user_id", userID),
			zap.Int64("order_amount", amount),
			zap.Int64("balance_before", balanceBefore))
	}
	if _, err := tx.Exec(ctx,
		`UPDATE users SET balance = $1, updated_at = NOW() WHERE id = $2::uuid`,
		balanceAfter, userID); err != nil {
		return fmt.Errorf("update user balance: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO balance_transactions
		 (user_id, transaction_type, amount, balance_type, balance_before, balance_after, reference_id, description)
		 VALUES ($1::uuid, 'refund', $2, 'balance', $3, $4, $5, 'Payment refund reversal')`,
		userID, -amount, balanceBefore, balanceAfter, orderID); err != nil {
		return fmt.Errorf("log refund transaction: %w", err)
	}
	return nil
}

// calculateSubscriptionRefund finds the active subscription tied to the order
// and returns the unused portion (micro-USD) capped at the original amount, plus
// the subscription id. If no active subscription is found it returns 0 and an
// empty id (idempotent for already-cancelled subscriptions).
func (r *PaymentRoutes) calculateSubscriptionRefund(ctx context.Context, tx pgx.Tx, orderUUID, userID string, paidAmount int64) (int64, string, error) {
	var planID string
	err := tx.QueryRow(ctx,
		`SELECT plan_id FROM subscription_purchases
		 WHERE order_id = $1::uuid AND user_id = $2::uuid AND status = 'active'
		 LIMIT 1`,
		orderUUID, userID).Scan(&planID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No active purchase record; the subscription may already be cancelled.
			return 0, "", nil
		}
		return 0, "", fmt.Errorf("query subscription purchase for refund: %w", err)
	}

	var subID string
	var quotaTotal, quotaUsed int64
	err = tx.QueryRow(ctx,
		`SELECT id::text, quota_total, quota_used FROM user_subscriptions
		 WHERE user_id = $1::uuid AND plan_id = $2::uuid AND status = 'active'
		 FOR UPDATE`,
		userID, planID).Scan(&subID, &quotaTotal, &quotaUsed)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No active subscription tied to this purchase; nothing to refund.
			return 0, "", nil
		}
		return 0, "", fmt.Errorf("query active subscription for refund: %w", err)
	}

	if quotaTotal <= 0 || quotaUsed >= quotaTotal {
		return 0, subID, nil
	}
	refundAmount := int64(float64(paidAmount) * float64(quotaTotal-quotaUsed) / float64(quotaTotal))
	if refundAmount < 0 {
		refundAmount = 0
	}
	if refundAmount > paidAmount {
		refundAmount = paidAmount
	}
	return refundAmount, subID, nil
}

// creditUserBalanceTx credits amount (micro-USD) to the user's balance and
// records a balance_transaction inside the caller's transaction.
func (r *PaymentRoutes) creditUserBalanceTx(ctx context.Context, tx pgx.Tx, userID string, amount int64, referenceID, txnType, description string) error {
	var balanceBefore int64
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(balance, 0) FROM users WHERE id = $1::uuid FOR UPDATE`,
		userID).Scan(&balanceBefore); err != nil {
		return fmt.Errorf("lock user balance: %w", err)
	}
	balanceAfter := balanceBefore + amount
	if _, err := tx.Exec(ctx,
		`UPDATE users SET balance = $1, updated_at = NOW() WHERE id = $2::uuid`,
		balanceAfter, userID); err != nil {
		return fmt.Errorf("credit user balance: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO balance_transactions
		 (user_id, transaction_type, amount, balance_type, balance_before, balance_after, reference_id, description)
		 VALUES ($1::uuid, $2, $3, 'balance', $4, $5, $6, $7)`,
		userID, txnType, amount, balanceBefore, balanceAfter, referenceID, description); err != nil {
		return fmt.Errorf("log balance transaction: %w", err)
	}
	return nil
}

// handleAlipayWebhook handles Alipay async notification
func (r *PaymentRoutes) handleAlipayWebhook(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}

	// Get gateway config
	var configStr string
	err = r.db.QueryRow(c.Request.Context(),
		`SELECT config::text FROM payment_gateways WHERE gateway = 'alipay'`,
	).Scan(&configStr)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "alipay not configured"})
		return
	}

	gw, err := r.factory.CreateGateway("alipay", payment.DecryptGatewayConfig("alipay", configStr, r.cfg.Auth.EncryptionKey))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "gateway error"})
		return
	}

	// Verify signature — Alipay Global sends the RSA-SHA256 signature in the
	// alipay-signature header, along with client-id and request-time headers.
	// The signed content is: HTTP_METHOD + " " + PATH + "\n" + CLIENT_ID + "." + REQUEST_TIME + "." + BODY
	alipayGW, ok := gw.(*payment.AlipayGateway)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "gateway type mismatch"})
		return
	}
	if !alipayGW.VerifyWebhookSignature(
		c.Request.Method,
		c.Request.URL.Path,
		c.GetHeader("client-id"),
		c.GetHeader("request-time"),
		string(body),
		c.GetHeader("alipay-signature"),
	) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return
	}

	// Handle webhook
	result, err := gw.HandleWebhook(body, c.GetHeader("alipay-signature"))
	if err != nil {
		c.String(http.StatusOK, "success")
		return
	}

	// Update order status
	switch result.Status {
	case "success":
		r.completeOrder(c, result.OrderID, result.PaymentID, result.Amount, "alipay")
	case "refunded":
		// Alipay refund notifications may carry the original payment id or the
		// refund request id. Try payment id first; fall back to order id lookup.
		if result.PaymentID != "" {
			r.refundOrder(c, result.PaymentID)
		} else if result.OrderID != "" {
			r.refundOrderByOrderID(c, result.OrderID)
		}
	}

	// Alipay requires "success" response
	c.String(http.StatusOK, "success")
}

// handlePayPalWebhook handles PayPal webhook notifications
func (r *PaymentRoutes) handlePayPalWebhook(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}

	// Get gateway config
	var configStr string
	err = r.db.QueryRow(c.Request.Context(),
		`SELECT config::text FROM payment_gateways WHERE gateway = 'paypal'`,
	).Scan(&configStr)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "paypal not configured"})
		return
	}

	gw, err := r.factory.CreateGateway("paypal", payment.DecryptGatewayConfig("paypal", configStr, r.cfg.Auth.EncryptionKey))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "gateway error"})
		return
	}

	// Verify signature using PayPal's verify-webhook-signature API. The SDK
	// needs the raw HTTP request with a readable body.
	paypalGW, ok := gw.(*payment.PayPalGateway)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "gateway type mismatch"})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	if !paypalGW.VerifyWebhookSignature(c.Request) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return
	}

	// Handle webhook
	result, err := gw.HandleWebhook(body, c.GetHeader("paypal-transmission-sig"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"received": true})
		return
	}

	switch result.Status {
	case "success":
		r.completeOrder(c, result.OrderID, result.PaymentID, result.Amount, "paypal")
	case "refunded":
		r.refundOrder(c, result.PaymentID)
	}

	c.JSON(http.StatusOK, gin.H{"received": true})
}

// handleWechatWebhook handles WeChat Pay callback
func (r *PaymentRoutes) handleWechatWebhook(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}

	// Get gateway config
	var configStr string
	err = r.db.QueryRow(c.Request.Context(),
		`SELECT config::text FROM payment_gateways WHERE gateway = 'wechat'`,
	).Scan(&configStr)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "wechat not configured"})
		return
	}

	gw, err := r.factory.CreateGateway("wechat", payment.DecryptGatewayConfig("wechat", configStr, r.cfg.Auth.EncryptionKey))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "gateway error"})
		return
	}

	// Verify WeChat Pay V3 webhook RSA-SHA256 signature using HTTP headers
	wechatGW, ok := gw.(*payment.WechatGateway)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "gateway type mismatch"})
		return
	}
	if !wechatGW.VerifyWebhookSignature(
		c.GetHeader("Wechatpay-Timestamp"),
		c.GetHeader("Wechatpay-Nonce"),
		c.GetHeader("Wechatpay-Signature"),
		string(body),
	) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return
	}

	// Handle webhook
	result, err := gw.HandleWebhook(body, "")
	if err != nil {
		c.Data(http.StatusOK, "application/xml", []byte("<xml><return_code><![CDATA[SUCCESS]]></return_code><return_msg><![CDATA[OK]]></return_msg></xml>"))
		return
	}

	// Update order status
	switch result.Status {
	case "success":
		r.completeOrder(c, result.OrderID, result.PaymentID, result.Amount, "wechat")
	case "refunded":
		r.refundOrder(c, result.PaymentID)
	}

	// WeChat requires XML response
	c.Data(http.StatusOK, "application/xml", []byte("<xml><return_code><![CDATA[SUCCESS]]></return_code><return_msg><![CDATA[OK]]></return_msg></xml>"))
}

// handleBillingCallback handles the browser redirect back from payment providers.
// This complements the async webhooks by providing an immediate UX response.
// Query params: order_id, status=(success|cancel|fail), gateway, payment_id
func (r *PaymentRoutes) handleBillingCallback(c *gin.Context) {
	orderID := c.Query("order_id")
	status := c.Query("status")
	gateway := c.Query("gateway")
	// payment_id is intentionally not trusted for completion — see the
	// "success" branch below. The async webhook is authoritative.
	_ = c.Query("payment_id")

	if orderID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "order_id is required"}})
		return
	}

	// Look up order
	var orderUUID, userID, orderStatus, productType string
	var amount int64
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT id::text, user_id::text, status, COALESCE(product_type, ''), amount
		 FROM orders WHERE order_id = $1`,
		orderID,
	).Scan(&orderUUID, &userID, &orderStatus, &productType, &amount)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Order not found"}})
		return
	}

	// If already completed, just return success
	if orderStatus == "completed" {
		c.JSON(http.StatusOK, gin.H{
			"success":      true,
			"order_id":     orderID,
			"status":       "completed",
			"amount":       amount,
			"product_type": productType,
		})
		return
	}

	// Handle based on redirect status
	switch status {
	case "success":
		// P0: never credit balance from a browser redirect alone — the query
		// string is user-controlled and unsigned. A payment_id without a
		// gateway query is a zero-cost recharge vector. All success callbacks
		// must go through queryGatewayAndComplete, which verifies the payment
		// status with the provider before completing the order. The async
		// webhook remains the authoritative completion path.
		if gateway != "" {
			r.queryGatewayAndComplete(c, orderID, orderUUID, userID, gateway, amount, productType)
			return
		}

		// No gateway to verify against — leave the order pending and let the
		// async webhook (or a later status poll) complete it.
		c.JSON(http.StatusOK, gin.H{
			"success":  false,
			"order_id": orderID,
			"status":   "pending",
			"message":  "Payment is being processed. Please check again later.",
		})

	case "cancel":
		// Only the order's authenticated owner may cancel it. The callback
		// route is public (browser redirect) and order_ids are guessable, so
		// an anonymous/mismatched request must not mutate the order — the
		// cancelled page is still shown, but the order stays pending.
		if c.GetString("user_id") != "" && c.GetString("user_id") == userID {
			if err := r.db.Exec(c.Request.Context(),
				`UPDATE orders SET status = 'cancelled', updated_at = NOW() WHERE order_id = $1 AND status = 'pending'`,
				orderID); err != nil {
				r.logger.Error("Failed to cancel order", zap.Error(err))
			}
		} else {
			r.logger.Warn("Ignoring cancel callback from non-owner",
				zap.String("order_id", orderID))
		}
		c.JSON(http.StatusOK, gin.H{
			"success":  false,
			"order_id": orderID,
			"status":   "cancelled",
			"message":  "Payment was cancelled by user.",
		})

	default:
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": "Invalid status. Use 'success', 'cancel', or 'fail'.",
			},
		})
	}
}

// queryGatewayAndComplete attempts to query the payment gateway for order status and complete if successful.
func (r *PaymentRoutes) queryGatewayAndComplete(c *gin.Context, orderID, orderUUID, userID, gateway string, amount int64, productType string) {
	var configStr string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT config::text FROM payment_gateways WHERE gateway = $1`,
		gateway,
	).Scan(&configStr)
	if err != nil {
		r.logger.Warn("Gateway config not found for callback query", zap.String("gateway", gateway))
		c.JSON(http.StatusOK, gin.H{
			"success":  false,
			"order_id": orderID,
			"status":   "pending",
			"message":  "Payment status unknown. Please check again later.",
		})
		return
	}

	gw, err := r.factory.CreateGateway(gateway, payment.DecryptGatewayConfig(gateway, configStr, r.cfg.Auth.EncryptionKey))
	if err != nil {
		r.logger.Warn("Failed to create gateway for callback query", zap.String("gateway", gateway), zap.Error(err))
		c.JSON(http.StatusOK, gin.H{
			"success":  false,
			"order_id": orderID,
			"status":   "pending",
			"message":  "Payment status unknown. Please check again later.",
		})
		return
	}

	// Use the provider-side order ID (e.g. PayPal's order ID) if one was stored
	// during CreateSession; PayPal does not recognize our internal order_id.
	queryID := orderID
	var gatewayOrderID string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COALESCE(gateway_order_id, '') FROM orders WHERE order_id = $1`,
		orderID,
	).Scan(&gatewayOrderID)
	if gatewayOrderID != "" {
		queryID = gatewayOrderID
	}

	// Query order status from gateway (best-effort; not all gateways support this)
	result, err := gw.QueryOrder(queryID)
	if err != nil || result == nil || result.Status != "success" {
		c.JSON(http.StatusOK, gin.H{
			"success":  false,
			"order_id": orderID,
			"status":   "pending",
			"message":  "Payment is being processed. Please check again later.",
		})
		return
	}

	r.completeOrder(c, orderID, result.PaymentID, result.Amount, gateway)

	// Refresh status
	var newStatus string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT status FROM orders WHERE order_id = $1`, orderID,
	).Scan(&newStatus)

	c.JSON(http.StatusOK, gin.H{
		"success":      newStatus == "completed",
		"order_id":     orderID,
		"status":       newStatus,
		"amount":       amount,
		"product_type": productType,
	})
}

// generateReceiptPDF renders a payment receipt for a completed top-up order
// and stores it via the storage provider; the orders.receipt_key column
// powers the Top-Up Records download column. Best-effort by design.
func (r *PaymentRoutes) generateReceiptPDF(ctx context.Context, orderUUID, userEmail, productType string, amount int64) {
	if r.storage == nil {
		return
	}
	var status, method string
	var createdAt time.Time
	if err := r.db.QueryRow(ctx,
		`SELECT status, COALESCE(metadata->>'gateway', ''), completed_at FROM orders WHERE id = $1::uuid`,
		orderUUID).Scan(&status, &method, &createdAt); err != nil || status != "completed" {
		return
	}
	pdfBytes, err := invoicepdf.Render(invoicepdf.Invoice{
		DocType:       "RECEIPT",
		PaymentMethod: method,
		OrderID:       orderUUID,
		IssuedAt:      createdAt,
		BillTo:        userEmail,
		Description:   "Balance top-up (" + productType + ")",
		AmountUSD:     float64(amount) / 1_000_000.0,
		PlatformName:  "Woodgit",
		PlatformURL:   r.cfg.App.BaseURL,
	})
	if err != nil {
		r.logger.Warn("receipt pdf: render failed", zap.String("order", orderUUID), zap.Error(err))
		return
	}
	key := fmt.Sprintf("receipts/%s.pdf", orderUUID)
	if err := r.storage.Upload(ctx, key, bytes.NewReader(pdfBytes), storage.UploadOptions{
		ContentType: "application/pdf",
		Size:        int64(len(pdfBytes)),
	}); err != nil {
		r.logger.Warn("receipt pdf: upload failed", zap.String("order", orderUUID), zap.Error(err))
		return
	}
	if _, err := r.db.Pool.Exec(ctx,
		`UPDATE orders SET receipt_key = $2 WHERE id = $1::uuid`, orderUUID, key); err != nil {
		r.logger.Warn("receipt pdf: record key failed", zap.String("order", orderUUID), zap.Error(err))
	}
}

// completeOrder marks an order as completed and credits user balance or activates subscription.
// paidAmount is the amount reported by the gateway in its NATIVE unit (Stripe=cents,
// WeChat=分, Alipay=major unit); gateway identifies the provider for unit conversion.
// A paidAmount of 0 skips the verification (simulation or unknown-gateway path).
func (r *PaymentRoutes) completeOrder(c *gin.Context, orderID string, paymentID string, paidAmount int64, gateway string) {
	ctx := c.Request.Context()

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		r.logger.Error("Failed to begin transaction for order completion", zap.String("order_id", orderID), zap.Error(err))
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Atomically mark order as completed — RowsAffected=0 means already processed
	tag, err := tx.Exec(ctx,
		`UPDATE orders SET status = 'completed', payment_id = $1, completed_at = NOW(), updated_at = NOW()
		 WHERE order_id = $2 AND status = 'pending'`,
		paymentID, orderID)
	if err != nil {
		r.logger.Error("Failed to update order", zap.String("order_id", orderID), zap.Error(err))
		return
	}
	if tag.RowsAffected() == 0 {
		r.logger.Warn("Order already processed or not found", zap.String("order_id", orderID))
		return
	}

	// Get order details including metadata and user email
	var userID, orderUUID string
	var amount int64
	var currency string
	var exchangeRate float64
	var productType, productName string
	var userEmail string
	var metadata map[string]interface{}
	err = tx.QueryRow(ctx,
		`SELECT o.user_id::text, o.id::text, o.amount,
		        COALESCE(o.currency, 'USD'), COALESCE(o.exchange_rate, 0),
		        COALESCE(o.product_type, ''), COALESCE(o.product_name, ''),
		        COALESCE(u.email, ''), COALESCE(o.metadata, '{}')
		 FROM orders o JOIN users u ON o.user_id = u.id WHERE o.order_id = $1`,
		orderID,
	).Scan(&userID, &orderUUID, &amount, &currency, &exchangeRate, &productType, &productName, &userEmail, &metadata)
	if err != nil {
		r.logger.Error("Failed to get order details", zap.String("order_id", orderID), zap.Error(err))
		return
	}

	// P0: verify the gateway-reported paid amount matches the order amount.
	// Prevents under-payment attacks where a user pays a lower amount but
	// gets credited the order's full (possibly inflated) amount.
	if paidAmount > 0 && gateway != "" {
		var paidMicroUSD int64
		var tolerance int64 // micro-USD; cent-native gateways convert exactly
		switch gateway {
		case "stripe", "paypal":
			paidMicroUSD = payment.CentsToMicroUSD(paidAmount)
		case "wechat":
			paidMicroUSD = payment.CNYFenToMicroUSD(paidAmount, exchangeRate)
			// CNY amounts round-trip through integer fen; allow one fen of
			// rounding error instead of demanding exact equality.
			tolerance = payment.CNYFenToleranceMicroUSD(exchangeRate)
		case "alipay":
			paidMicroUSD = payment.MajorUnitHundredthsToMicroUSD(paidAmount, currency, exchangeRate)
			if currency == "CNY" {
				tolerance = payment.CNYFenToleranceMicroUSD(exchangeRate)
			}
		default:
			// Unknown gateway — cannot convert, skip verification.
		}
		if paidMicroUSD > 0 && absInt64(paidMicroUSD-amount) > tolerance {
			r.logger.Error("Paid amount mismatch — refusing to credit order",
				zap.String("order_id", orderID),
				zap.Int64("order_amount_micro_usd", amount),
				zap.Int64("paid_amount_micro_usd", paidMicroUSD),
				zap.Int64("tolerance_micro_usd", tolerance),
				zap.String("gateway", gateway))
			// tx.Rollback via defer reverts the status='completed' update above.
			return
		}
	}

	// Handle based on product type
	if productType == "subscription" || productType == "subscription_renewal" {
		// activateSubscription runs inside the same transaction so that
		// order completion and subscription creation are atomic: if anything
		// fails the whole tx rolls back and the order stays 'pending',
		// allowing the gateway to retry. Never commit before activation.
		if err := r.activateSubscriptionTx(ctx, tx, userID, orderUUID, metadata, productType == "subscription_renewal"); err != nil {
			r.logger.Error("Failed to activate subscription, rolling back order",
				zap.String("order_id", orderID),
				zap.String("user_id", userID),
				zap.Error(err))
			return
		}
		if err := tx.Commit(ctx); err != nil {
			r.logger.Error("Failed to commit order for subscription", zap.Error(err))
			return
		}
	} else {
		// Credit user balance for regular purchases (recharge)
		var balanceBefore int64
		_ = tx.QueryRow(ctx,
			`SELECT COALESCE(balance, 0) FROM users WHERE id = $1::uuid`, userID).Scan(&balanceBefore)

		if _, err := tx.Exec(ctx,
			`UPDATE users SET balance = balance + $1, updated_at = NOW() WHERE id = $2::uuid`,
			amount, userID); err != nil {
			r.logger.Error("Failed to credit balance", zap.String("user_id", userID), zap.Error(err))
			return
		}

		// Log balance transaction
		if _, err := tx.Exec(ctx,
			`INSERT INTO balance_transactions
			 (user_id, transaction_type, amount, balance_type, balance_before, balance_after, reference_id, description)
			 VALUES ($1::uuid, 'recharge', $2, 'balance', $3, $4, $5, $6)`,
			userID, amount, balanceBefore, balanceBefore+amount, orderUUID,
			fmt.Sprintf("Recharge via %s", productType)); err != nil {
			r.logger.Error("Failed to log balance transaction", zap.Error(err))
			return
		}

		if err := tx.Commit(ctx); err != nil {
			r.logger.Error("Failed to commit order completion", zap.Error(err))
			return
		}
		// Balance changed — invalidate the cached validation result for this
		// user so the validate endpoint sees the new balance immediately
		// (a recharged user would otherwise stay 402 within the cache TTL).
		invalidateUserValidateCaches(ctx, r.db, r.logger, userID)
		// Best-effort payment receipt for the Top-Up Records download column;
		// a failure here never blocks the recharge itself.
		r.generateReceiptPDF(ctx, orderUUID, userEmail, productType, amount)
	}

	r.logger.Info("Order completed",
		zap.String("order_id", orderID),
		zap.String("user_id", userID),
		zap.Int64("amount", amount),
		zap.String("product_type", productType))

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), userID, userEmail, "billing", "payment_completed", "info",
			fmt.Sprintf("Payment completed: %s (%s, type: %s)", orderID, QuotaToUSD(amount), productType))
	}
}

// activateSubscriptionTx creates or extends a user subscription after payment,
// executing all DB writes inside the caller's transaction so that order
// completion and subscription activation are fully atomic.
func (r *PaymentRoutes) activateSubscriptionTx(ctx context.Context, tx pgx.Tx, userID string, orderUUID string, metadata map[string]interface{}, isRenewal bool) error {
	// Extract plan details from metadata
	planID, _ := metadata["plan_id"].(string)
	planName, _ := metadata["plan_name"].(string)
	planType, _ := metadata["plan_type"].(string)
	quota, _ := metadata["quota"].(float64)
	durationDays, _ := metadata["duration_days"].(float64)
	dailyLimit, _ := metadata["daily_limit"].(float64)
	subscriptionID, _ := metadata["subscription_id"].(string)

	// Rate limits from plan metadata
	rphLimit, _ := metadata["rph_limit"].(float64)
	rp5hLimit, _ := metadata["rp5h_limit"].(float64)
	rpdLimit, _ := metadata["rpd_limit"].(float64)
	rpwLimit, _ := metadata["rpw_limit"].(float64)
	tphLimit, _ := metadata["tph_limit"].(float64)
	tpdLimit, _ := metadata["tpd_limit"].(float64)
	tpwLimit, _ := metadata["tpw_limit"].(float64)
	concurrentLimit, _ := metadata["concurrent_limit"].(float64)

	if planID == "" {
		r.logger.Error("Missing plan_id in order metadata", zap.String("order_id", orderUUID))
		return fmt.Errorf("missing plan_id in order metadata")
	}

	// Renewal orders (created by renewSubscription / ProcessAutoRenewals) only
	// carry {plan_id, subscription_id, is_renewal} in their metadata — none of
	// the quota/duration/limit fields the purchase flow embeds. Without this
	// fallback a paid renewal would add quota=0 and NULL the expiry (the user
	// pays but gets nothing). Load the missing fields from the plan so both
	// flows behave identically.
	if quota == 0 || durationDays == 0 {
		var pQuota, pDuration, pDaily int64
		var pType string
		var pRph, pRp5h, pRpd, pRpw, pTph, pTpd, pTpw, pConc int64
		if err := tx.QueryRow(ctx,
			`SELECT quota, duration_days, COALESCE(plan_type,'token'), COALESCE(daily_limit,0),
			        COALESCE(rph_limit,0), COALESCE(rp5h_limit,0), COALESCE(rpd_limit,0), COALESCE(rpw_limit,0),
			        COALESCE(tph_limit,0), COALESCE(tpd_limit,0), COALESCE(tpw_limit,0), COALESCE(concurrent_limit,0)
			 FROM subscription_plans WHERE id = $1::uuid`,
			planID,
		).Scan(&pQuota, &pDuration, &pType, &pDaily,
			&pRph, &pRp5h, &pRpd, &pRpw, &pTph, &pTpd, &pTpw, &pConc); err != nil {
			r.logger.Error("Failed to load plan for subscription activation",
				zap.String("plan_id", planID), zap.String("order_id", orderUUID), zap.Error(err))
			return fmt.Errorf("load plan for activation: %w", err)
		}
		if quota == 0 {
			quota = float64(pQuota)
		}
		if durationDays == 0 {
			durationDays = float64(pDuration)
		}
		if planType == "" {
			planType = pType
		}
		if dailyLimit == 0 {
			dailyLimit = float64(pDaily)
		}
		if rphLimit == 0 {
			rphLimit = float64(pRph)
		}
		if rp5hLimit == 0 {
			rp5hLimit = float64(pRp5h)
		}
		if rpdLimit == 0 {
			rpdLimit = float64(pRpd)
		}
		if rpwLimit == 0 {
			rpwLimit = float64(pRpw)
		}
		if tphLimit == 0 {
			tphLimit = float64(pTph)
		}
		if tpdLimit == 0 {
			tpdLimit = float64(pTpd)
		}
		if tpwLimit == 0 {
			tpwLimit = float64(pTpw)
		}
		if concurrentLimit == 0 {
			concurrentLimit = float64(pConc)
		}
	}

	// Calculate expiry date
	var expiresAt interface{}
	if durationDays > 0 {
		expiresAt = time.Now().AddDate(0, 0, int(durationDays))
	}

	if isRenewal && subscriptionID != "" {
		// Extend existing subscription
		_, err := tx.Exec(ctx,
			`UPDATE user_subscriptions SET
				quota_total = quota_total + $1,
				quota_remaining = quota_remaining + $1,
				status = 'active',
				expires_at = CASE
					-- Extend from whichever is later: the current expiry or now.
					-- An already-expired subscription must get a full new period
					-- from today, not a window anchored in the past.
					WHEN $2::timestamp IS NOT NULL AND expires_at IS NOT NULL
						THEN GREATEST(expires_at, NOW()) + INTERVAL '1 day' * $3
					WHEN $2::timestamp IS NOT NULL THEN $2::timestamp
					ELSE expires_at
				END,
				updated_at = NOW()
			WHERE id = $4::uuid AND user_id = $5::uuid`,
			int64(quota), expiresAt, int(durationDays), subscriptionID, userID)
		if err != nil {
			r.logger.Error("Failed to extend subscription", zap.String("subscription_id", subscriptionID), zap.Error(err))
			return err
		}
		r.logger.Info("Subscription renewed",
			zap.String("subscription_id", subscriptionID),
			zap.String("user_id", userID),
			zap.Int64("added_quota", int64(quota)))
	} else {
		// Create new subscription
		var existingSubID string
		err := tx.QueryRow(ctx,
			`SELECT id::text FROM user_subscriptions WHERE user_id = $1::uuid AND plan_id = $2::uuid AND status = 'active'`,
			userID, planID,
		).Scan(&existingSubID)

		if err == nil && existingSubID != "" {
			// Extend existing active subscription
			_, err = tx.Exec(ctx,
				`UPDATE user_subscriptions SET
					quota_total = quota_total + $1,
					quota_remaining = quota_remaining + $1,
					updated_at = NOW()
				WHERE id = $2::uuid`,
				int64(quota), existingSubID)
			if err != nil {
				r.logger.Error("Failed to extend existing subscription", zap.String("subscription_id", existingSubID), zap.Error(err))
				return err
			}
		} else {
			// Create new subscription with rate limits from plan.
			// daily_quota_reset is initialized to the next midnight so daily_limit
			// enforcement starts fresh from today.
			_, err = tx.Exec(ctx,
				`INSERT INTO user_subscriptions (
				 user_id, plan_id, plan_name, plan_type,
				 quota_total, quota_used, quota_remaining,
				 daily_limit, daily_quota_reset, duration_days,
				 rph_limit, rp5h_limit, rpd_limit, rpw_limit,
				 tph_limit, tpd_limit, tpw_limit,
				 concurrent_limit,
				 status, started_at, expires_at)
				VALUES (
				 $1::uuid, $2::uuid, $3, $4,
				 $5, 0, $5,
				 $6, CURRENT_DATE + INTERVAL '1 day', $7,
				 $8, $9, $10, $11,
				 $12, $13, $14,
				 $15,
				 'active', NOW(), $16)`,
				userID, planID, planName, planType,
				int64(quota), int64(dailyLimit), int(durationDays),
				int(rphLimit), int(rp5hLimit), int(rpdLimit), int(rpwLimit),
				int(tphLimit), int(tpdLimit), int(tpwLimit),
				int(concurrentLimit),
				expiresAt)
			if err != nil {
				r.logger.Error("Failed to create subscription", zap.String("user_id", userID), zap.String("plan_id", planID), zap.Error(err))
				return err
			}
			r.logger.Info("Subscription activated",
				zap.String("user_id", userID),
				zap.String("plan_id", planID),
				zap.String("plan_name", planName),
				zap.Int64("quota", int64(quota)))
			metrics.GetMetrics().SubscriptionCreated.Add(1)
		}
	}

	// Update subscription_purchases if exists
	if _, err := tx.Exec(ctx,
		`UPDATE subscription_purchases SET status = 'active', starts_at = NOW(), expires_at = $1, updated_at = NOW()
		 WHERE order_id = $2::uuid`,
		expiresAt, orderUUID); err != nil {
		r.logger.Error("Failed to update subscription purchase", zap.Error(err))
		return err
	}
	return nil
}

// absInt64 returns the absolute value of v.
func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

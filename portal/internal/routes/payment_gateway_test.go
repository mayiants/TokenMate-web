package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ai-proxy/ai-portal-go/internal/config"
	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/testutil"
	"github.com/ai-proxy/ai-portal-go/pkg/crypto"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

const testGatewayEncKey = "test-payment-gateway-encryption-key"

// newGatewayTestRoutes builds PaymentRoutes against the test database with a
// fixed encryption key so secret fields are encrypted at rest.
func newGatewayTestRoutes(t *testing.T) (*PaymentRoutes, *pgxpool.Pool) {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), testutil.PostgresDSN())
	if err != nil {
		t.Fatalf("connect to test db: %v", err)
	}
	t.Cleanup(pool.Close)
	cfg := &config.Config{}
	cfg.Auth.EncryptionKey = testGatewayEncKey
	return NewPaymentRoutes(&db.DB{Pool: pool}, cfg, zap.NewNop(), nil, nil, nil), pool
}

// resetGatewayRows restores the seeded disabled state for gateways a test touched.
func resetGatewayRows(t *testing.T, pool *pgxpool.Pool, gateways ...string) {
	t.Helper()
	t.Cleanup(func() {
		for _, g := range gateways {
			_, _ = pool.Exec(context.Background(),
				`UPDATE payment_gateways SET enabled = false, config = '{}', updated_at = NOW() WHERE gateway = $1`, g)
		}
	})
}

func callUpdateGateway(t *testing.T, r *PaymentRoutes, gateway, body string) (int, map[string]interface{}) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "gateway", Value: gateway}}
	c.Request = httptest.NewRequest(http.MethodPut, "/api/admin/payment/gateways/"+gateway, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	r.updateGateway(c)
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w.Code, resp
}

func readGatewayRow(t *testing.T, pool *pgxpool.Pool, gateway string) (string, bool) {
	t.Helper()
	var configStr string
	var enabled bool
	err := pool.QueryRow(context.Background(),
		`SELECT COALESCE(config::text, '{}'), enabled FROM payment_gateways WHERE gateway = $1`, gateway,
	).Scan(&configStr, &enabled)
	if err != nil {
		t.Fatalf("read gateway %s: %v", gateway, err)
	}
	return configStr, enabled
}

// TestUpdateGateway_EncryptsSensitiveFields verifies that secrets are stored
// encrypted (AES-256-GCM) and never land in the database as plaintext.
func TestUpdateGateway_EncryptsSensitiveFields(t *testing.T) {
	r, pool := newGatewayTestRoutes(t)
	resetGatewayRows(t, pool, "stripe")

	code, resp := callUpdateGateway(t, r, "stripe",
		`{"config":{"secret_key":"sk_live_1234567890","webhook_secret":"whsec_abcdef"},"enabled":true}`)
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("update gateway: code = %d, resp = %v", code, resp)
	}

	configStr, enabled := readGatewayRow(t, pool, "stripe")
	if !enabled {
		t.Error("gateway should be enabled after update")
	}
	if strings.Contains(configStr, "sk_live_1234567890") || strings.Contains(configStr, "whsec_abcdef") {
		t.Errorf("stored config contains plaintext secret: %s", configStr)
	}

	var stored map[string]interface{}
	if err := json.Unmarshal([]byte(configStr), &stored); err != nil {
		t.Fatalf("stored config is not valid JSON: %v", err)
	}
	for _, key := range []string{"secret_key", "webhook_secret"} {
		enc, _ := stored[key].(string)
		dec, err := crypto.Decrypt(enc, testGatewayEncKey)
		if err != nil {
			t.Errorf("%s is not decryptable with the configured key: %v", key, err)
			continue
		}
		want := map[string]string{"secret_key": "sk_live_1234567890", "webhook_secret": "whsec_abcdef"}[key]
		if dec != want {
			t.Errorf("decrypted %s = %q, want %q", key, dec, want)
		}
	}
}

// TestUpdateGateway_MaskDoesNotOverwrite verifies the common UI pattern where a
// masked (or emptied) secret field must keep the previously stored ciphertext.
func TestUpdateGateway_MaskDoesNotOverwrite(t *testing.T) {
	r, pool := newGatewayTestRoutes(t)
	resetGatewayRows(t, pool, "stripe")

	if code, _ := callUpdateGateway(t, r, "stripe",
		`{"config":{"secret_key":"sk_live_1234567890","webhook_secret":"whsec_abcdef"},"enabled":true}`); code != http.StatusOK {
		t.Fatalf("initial update failed: %d", code)
	}
	before, _ := readGatewayRow(t, pool, "stripe")

	// UI echoes "****7890" / "****cdef"; saving without changes must not clobber secrets.
	if code, _ := callUpdateGateway(t, r, "stripe",
		`{"config":{"secret_key":"****7890","webhook_secret":""}}`); code != http.StatusOK {
		t.Fatalf("masked update failed: %d", code)
	}
	after, _ := readGatewayRow(t, pool, "stripe")
	if after != before {
		t.Errorf("masked/empty secret overwrote stored config:\nbefore: %s\nafter:  %s", before, after)
	}

	// A real new value must replace the ciphertext.
	if code, _ := callUpdateGateway(t, r, "stripe",
		`{"config":{"secret_key":"sk_live_NEWVALUE9999"}}`); code != http.StatusOK {
		t.Fatalf("secret rotation failed: %d", code)
	}
	rotated, _ := readGatewayRow(t, pool, "stripe")
	var stored map[string]interface{}
	if err := json.Unmarshal([]byte(rotated), &stored); err != nil {
		t.Fatalf("stored config is not valid JSON: %v", err)
	}
	dec, err := crypto.Decrypt(stored["secret_key"].(string), testGatewayEncKey)
	if err != nil || dec != "sk_live_NEWVALUE9999" {
		t.Errorf("rotated secret decrypt = %q, err = %v; want sk_live_NEWVALUE9999", dec, err)
	}
}

// TestListGateways_MasksSecrets verifies that secrets are never returned in
// plaintext or ciphertext; only a last-4 mask is exposed.
func TestListGateways_MasksSecrets(t *testing.T) {
	r, pool := newGatewayTestRoutes(t)
	resetGatewayRows(t, pool, "stripe")

	if code, _ := callUpdateGateway(t, r, "stripe",
		`{"config":{"secret_key":"sk_live_1234567890","webhook_secret":"whsec_abcdef"},"enabled":true}`); code != http.StatusOK {
		t.Fatalf("update gateway failed: %d", code)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/admin/payment/gateways", nil)
	r.listGateways(c)

	if w.Code != http.StatusOK {
		t.Fatalf("list gateways: code = %d, body = %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, "sk_live_1234567890") || strings.Contains(body, "whsec_abcdef") {
		t.Errorf("list response leaks plaintext secret: %s", body)
	}

	var resp struct {
		Gateways []map[string]interface{} `json:"gateways"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse list response: %v", err)
	}
	var stripe map[string]interface{}
	for _, g := range resp.Gateways {
		if g["gateway"] == "stripe" {
			stripe = g
		}
	}
	if stripe == nil {
		t.Fatal("stripe gateway missing from list response")
	}
	cfg, _ := stripe["config"].(map[string]interface{})
	if cfg["secret_key"] != "****7890" {
		t.Errorf("secret_key mask = %v, want ****7890", cfg["secret_key"])
	}
	if cfg["webhook_secret"] != "****cdef" {
		t.Errorf("webhook_secret mask = %v, want ****cdef", cfg["webhook_secret"])
	}
	if stripe["enabled"] != true {
		t.Errorf("enabled = %v, want true", stripe["enabled"])
	}
}

// TestUpdateGateway_RejectsUnsupportedGateway guards the upsert against
// arbitrary gateway rows.
func TestUpdateGateway_RejectsUnsupportedGateway(t *testing.T) {
	r, pool := newGatewayTestRoutes(t)

	code, _ := callUpdateGateway(t, r, "bogus", `{"enabled":true}`)
	if code != http.StatusBadRequest {
		t.Errorf("unsupported gateway: code = %d, want 400", code)
	}
	var count int
	_ = pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM payment_gateways WHERE gateway = 'bogus'`).Scan(&count)
	if count != 0 {
		t.Error("unsupported gateway row was inserted")
	}
}

// TestCreatePayment_SimulationEndToEnd drives createPayment with simulation
// mode forced by admin: the order must complete and the balance be credited.
func TestCreatePayment_SimulationEndToEnd(t *testing.T) {
	r, pool := newGatewayTestRoutes(t)
	ctx := context.Background()

	const userID = "550e8400-e29b-41d4-a716-446655440099"
	const orderID = "ORD-GWSIM-0001"

	// Clean leftovers from earlier runs.
	_, _ = pool.Exec(ctx, `DELETE FROM balance_transactions WHERE user_id = $1::uuid`, userID)
	_, _ = pool.Exec(ctx, `DELETE FROM orders WHERE user_id = $1::uuid`, userID)
	_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, userID)
	_, _ = pool.Exec(ctx, `DELETE FROM system_settings WHERE key = 'payment_simulation_mode'`)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM balance_transactions WHERE user_id = $1::uuid`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM orders WHERE user_id = $1::uuid`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM system_settings WHERE key = 'payment_simulation_mode'`)
		_, _ = pool.Exec(ctx, `UPDATE payment_gateways SET enabled = false, config = '{}', updated_at = NOW() WHERE gateway = 'alipay'`)
	})

	// Enable a gateway row (payment_gateways is the table the payment link reads).
	if code, _ := callUpdateGateway(t, r, "alipay",
		`{"config":{"client_id":"SANDBOX_TEST","merchant_private_key":"unused-in-sim"},"enabled":true}`); code != http.StatusOK {
		t.Fatalf("enable alipay gateway failed: %d", code)
	}

	// Force simulation mode.
	if _, err := pool.Exec(ctx,
		`INSERT INTO system_settings (key, value, category) VALUES ('payment_simulation_mode', 'true', 'payment')
		 ON CONFLICT (key) DO UPDATE SET value = 'true'`); err != nil {
		t.Fatalf("set simulation mode: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO users (id, name, email, password_hash, role, balance, created_at, updated_at)
		 VALUES ($1::uuid, 'GW Sim', 'gw-sim@example.com', 'hash', 'user', 0, NOW(), NOW())`, userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	// createPayment looks orders up by (id = $1::uuid OR order_id = $1::text);
	// the uuid cast rejects text order ids, so the UUID form is used here.
	var orderUUID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO orders (user_id, order_id, amount, status, product_type, metadata, created_at, updated_at)
		 VALUES ($1::uuid, $2, 1000, 'pending', 'recharge', '{}', NOW(), NOW())
		 RETURNING id::text`, userID, orderID).Scan(&orderUUID); err != nil {
		t.Fatalf("insert order: %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user_id", userID)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/payment/create",
		strings.NewReader(`{"order_id":"`+orderUUID+`","payment_method":"alipay"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	r.createPayment(c)

	if w.Code != http.StatusOK {
		t.Fatalf("create payment: code = %d, body = %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp["success"] != true || resp["simulated"] != true {
		t.Errorf("resp = %v, want success=true simulated=true", resp)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM orders WHERE order_id = $1`, orderID).Scan(&status); err != nil {
		t.Fatalf("read order: %v", err)
	}
	if status != "completed" {
		t.Errorf("order status = %q, want completed", status)
	}
	var balance int64
	if err := pool.QueryRow(ctx, `SELECT balance FROM users WHERE id = $1::uuid`, userID).Scan(&balance); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	if balance != 1000 {
		t.Errorf("balance = %d, want 1000", balance)
	}
}

// TestStripeConfigWithURLDefaults verifies that missing success/cancel URLs are
// derived from APP_BASE_URL while explicitly configured URLs are preserved.
func TestStripeConfigWithURLDefaults(t *testing.T) {
	cfg := &config.Config{}
	cfg.App.BaseURL = "https://portal.example.com/"
	r := &PaymentRoutes{cfg: cfg}

	// Empty URLs get defaults (trailing slash on base is trimmed).
	out := r.stripeConfigWithURLDefaults(`{"secret_key":"sk_test_x"}`)
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if parsed["success_url"] != "https://portal.example.com/billing?payment=success" {
		t.Errorf("success_url = %v", parsed["success_url"])
	}
	if parsed["cancel_url"] != "https://portal.example.com/billing?payment=cancelled" {
		t.Errorf("cancel_url = %v", parsed["cancel_url"])
	}
	if parsed["secret_key"] != "sk_test_x" {
		t.Errorf("secret_key was not preserved: %v", parsed["secret_key"])
	}

	// Configured URLs are left untouched.
	out = r.stripeConfigWithURLDefaults(`{"success_url":"https://custom/ok","cancel_url":"https://custom/no"}`)
	_ = json.Unmarshal([]byte(out), &parsed)
	if parsed["success_url"] != "https://custom/ok" || parsed["cancel_url"] != "https://custom/no" {
		t.Errorf("configured URLs were overwritten: %v", parsed)
	}

	// Invalid JSON passes through unchanged.
	if got := r.stripeConfigWithURLDefaults("not-json"); got != "not-json" {
		t.Errorf("invalid JSON not passed through: %q", got)
	}
}

// TestRefundOrder_RechargeClawback verifies the Stripe refund path: a
// completed recharge order is flipped to refunded and the credited balance
// is clawed back exactly once (idempotent on redelivery).
func TestRefundOrder_RechargeClawback(t *testing.T) {
	r, pool := newGatewayTestRoutes(t)
	ctx := context.Background()
	if err := db.InitTestSchema(ctx, testutil.PostgresDSN(), zap.NewNop()); err != nil {
		t.Fatalf("init test schema: %v", err)
	}

	userID := "b1b2c3d4-0000-4000-8000-0000000000f1"
	orderID := "ORD-refund-test-1"
	paymentID := "pi_refund_test_1"
	_, _ = pool.Exec(ctx, "DELETE FROM balance_transactions WHERE user_id = $1::uuid", userID)
	_, _ = pool.Exec(ctx, "DELETE FROM orders WHERE order_id = $1", orderID)
	_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1::uuid", userID)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM balance_transactions WHERE user_id = $1::uuid", userID)
		_, _ = pool.Exec(ctx, "DELETE FROM orders WHERE order_id = $1", orderID)
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1::uuid", userID)
	})

	if _, err := pool.Exec(ctx,
		"INSERT INTO users (id, name, email, password_hash, role, balance, created_at, updated_at) VALUES ($1::uuid, 'R', $2, 'h', 'user', 1000000, NOW(), NOW())",
		userID, "refund-test-1@example.com"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO orders (user_id, order_id, amount, currency, status, payment_method, payment_id, product_type, product_name, created_at, updated_at)
		 VALUES ($1::uuid, $2, 600000, 'USD', 'completed', 'stripe', $3, 'recharge', 'Top up', NOW(), NOW())`,
		userID, orderID, paymentID); err != nil {
		t.Fatalf("insert order: %v", err)
	}

	call := func() {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/webhook/stripe", nil)
		r.refundOrder(c, paymentID)
	}
	call()

	var status string
	var bal int64
	if err := pool.QueryRow(ctx, "SELECT status FROM orders WHERE order_id = $1", orderID).Scan(&status); err != nil {
		t.Fatalf("read order: %v", err)
	}
	if status != "refunded" {
		t.Errorf("order status = %q, want refunded", status)
	}
	if err := pool.QueryRow(ctx, "SELECT balance FROM users WHERE id = $1::uuid", userID).Scan(&bal); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	if bal != 400000 {
		t.Errorf("balance = %d, want 400000 (600000 clawed back)", bal)
	}
	var refundTxns int
	if err := pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM balance_transactions WHERE user_id = $1::uuid AND transaction_type = 'refund'",
		userID).Scan(&refundTxns); err != nil {
		t.Fatalf("count refund txns: %v", err)
	}
	if refundTxns != 1 {
		t.Errorf("refund transactions = %d, want 1", refundTxns)
	}

	// Redelivery must be a no-op.
	call()
	_ = pool.QueryRow(ctx, "SELECT balance FROM users WHERE id = $1::uuid", userID).Scan(&bal)
	if bal != 400000 {
		t.Errorf("after redelivery balance = %d, want 400000", bal)
	}
	_ = pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM balance_transactions WHERE user_id = $1::uuid AND transaction_type = 'refund'",
		userID).Scan(&refundTxns)
	if refundTxns != 1 {
		t.Errorf("after redelivery refund transactions = %d, want 1", refundTxns)
	}
}

// TestRefundOrder_SubscriptionCreditBalance verifies that when a gateway
// refund webhook arrives for a subscription order, the unused quota portion is
// credited to user balance and the subscription is cancelled instead of
// clawing back balance.
func TestRefundOrder_SubscriptionCreditBalance(t *testing.T) {
	r, pool := newGatewayTestRoutes(t)
	ctx := context.Background()
	if err := db.InitTestSchema(ctx, testutil.PostgresDSN(), zap.NewNop()); err != nil {
		t.Fatalf("init test schema: %v", err)
	}

	userID := "b1b2c3d4-0000-4000-8000-0000000000f2"
	orderID := "ORD-refund-test-2"
	paymentID := "pi_refund_test_2"
	planID := "a1a2a3a4-0000-4000-8000-000000000002"
	subID := "c1c2c3c4-0000-4000-8000-000000000002"

	cleanup := func() {
		_, _ = pool.Exec(ctx, "DELETE FROM balance_transactions WHERE user_id = $1::uuid", userID)
		_, _ = pool.Exec(ctx, "DELETE FROM subscription_purchases WHERE user_id = $1::uuid", userID)
		_, _ = pool.Exec(ctx, "DELETE FROM user_subscriptions WHERE id = $1::uuid", subID)
		_, _ = pool.Exec(ctx, "DELETE FROM subscription_plans WHERE id = $1::uuid", planID)
		_, _ = pool.Exec(ctx, "DELETE FROM orders WHERE order_id = $1", orderID)
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1::uuid", userID)
	}
	cleanup()
	t.Cleanup(cleanup)

	if _, err := pool.Exec(ctx,
		"INSERT INTO users (id, name, email, password_hash, role, balance, created_at, updated_at) VALUES ($1::uuid, 'R', $2, 'h', 'user', 1000000, NOW(), NOW())",
		userID, "refund-test-2@example.com"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := pool.Exec(ctx,
		"INSERT INTO subscription_plans (id, name, price, quota, duration_days, status, created_at, updated_at) VALUES ($1::uuid, 'Pro', 600000, 1000000, 30, 'active', NOW(), NOW())",
		planID); err != nil {
		t.Fatalf("insert plan: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO user_subscriptions (id, user_id, plan_id, plan_name, plan_type, quota_total, quota_used, quota_remaining, status, created_at, updated_at)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, 'Pro', 'monthly', 1000000, 250000, 750000, 'active', NOW(), NOW())`,
		subID, userID, planID); err != nil {
		t.Fatalf("insert subscription: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO orders (user_id, order_id, amount, currency, status, payment_method, payment_id, product_type, product_name, created_at, updated_at)
		 VALUES ($1::uuid, $2, 600000, 'USD', 'completed', 'stripe', $3, 'subscription', 'Pro plan', NOW(), NOW())`,
		userID, orderID, paymentID); err != nil {
		t.Fatalf("insert order: %v", err)
	}
	var orderUUID string
	if err := pool.QueryRow(ctx, "SELECT id::text FROM orders WHERE order_id = $1", orderID).Scan(&orderUUID); err != nil {
		t.Fatalf("read order uuid: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO subscription_purchases (id, user_id, plan_id, order_id, amount, status, created_at, updated_at)
		 VALUES (gen_random_uuid(), $1::uuid, $2::uuid, $3::uuid, 600000, 'active', NOW(), NOW())`,
		userID, planID, orderUUID); err != nil {
		t.Fatalf("insert subscription purchase: %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/webhook/stripe", nil)
	r.refundOrder(c, paymentID)

	var status string
	if err := pool.QueryRow(ctx, "SELECT status FROM orders WHERE order_id = $1", orderID).Scan(&status); err != nil {
		t.Fatalf("read order: %v", err)
	}
	if status != "refunded" {
		t.Errorf("order status = %q, want refunded", status)
	}
	var bal int64
	if err := pool.QueryRow(ctx, "SELECT balance FROM users WHERE id = $1::uuid", userID).Scan(&bal); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	// 75% unused -> 450000 credit on top of initial 1000000.
	if bal != 1450000 {
		t.Errorf("balance = %d, want 1450000", bal)
	}
	var subStatus string
	if err := pool.QueryRow(ctx, "SELECT status FROM user_subscriptions WHERE id = $1::uuid", subID).Scan(&subStatus); err != nil {
		t.Fatalf("read subscription: %v", err)
	}
	if subStatus != "cancelled" {
		t.Errorf("subscription status = %q, want cancelled", subStatus)
	}
	var refundTxns int
	if err := pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM balance_transactions WHERE user_id = $1::uuid AND transaction_type = 'subscription_refund'",
		userID).Scan(&refundTxns); err != nil {
		t.Fatalf("count refund txns: %v", err)
	}
	if refundTxns != 1 {
		t.Errorf("subscription_refund transactions = %d, want 1", refundTxns)
	}
}

// TestCompleteOrder_AmountVerification pins the P0 paid-vs-order check:
// under-payment must not be credited, exact payment must be, CNY round-trip
// must pass within the one-fen tolerance, and redelivery must not double-credit.
func TestCompleteOrder_AmountVerification(t *testing.T) {
	r, pool := newGatewayTestRoutes(t)
	ctx := context.Background()
	if err := db.InitTestSchema(ctx, testutil.PostgresDSN(), zap.NewNop()); err != nil {
		t.Fatalf("init test schema: %v", err)
	}

	userID := "b1b2c3d4-0000-4000-8000-0000000000f3"
	_, _ = pool.Exec(ctx, "DELETE FROM balance_transactions WHERE user_id = $1::uuid", userID)
	_, _ = pool.Exec(ctx, "DELETE FROM orders WHERE user_id = $1::uuid", userID)
	_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1::uuid", userID)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM balance_transactions WHERE user_id = $1::uuid", userID)
		_, _ = pool.Exec(ctx, "DELETE FROM orders WHERE user_id = $1::uuid", userID)
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1::uuid", userID)
	})
	if _, err := pool.Exec(ctx,
		"INSERT INTO users (id, name, email, password_hash, role, balance, created_at, updated_at) VALUES ($1::uuid, 'V', $2, 'h', 'user', 0, NOW(), NOW())",
		userID, "verify-test@example.com"); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	mkOrder := func(orderID string, amount int64, currency string, rate float64) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`INSERT INTO orders (user_id, order_id, amount, currency, exchange_rate, status, payment_method, product_type, product_name, created_at, updated_at)
			 VALUES ($1::uuid, $2, $3, $4, $5, 'pending', 'stripe', 'recharge', 'Top up', NOW(), NOW())`,
			userID, orderID, amount, currency, rate); err != nil {
			t.Fatalf("insert order %s: %v", orderID, err)
		}
	}
	call := func(orderID, paymentID string, paidAmount int64, gateway string) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/webhook/x", nil)
		r.completeOrder(c, orderID, paymentID, paidAmount, gateway)
	}
	orderState := func(orderID string) (string, int64) {
		var status string
		var balance int64
		if err := pool.QueryRow(ctx, "SELECT status FROM orders WHERE order_id = $1", orderID).Scan(&status); err != nil {
			t.Fatalf("read order %s: %v", orderID, err)
		}
		_ = pool.QueryRow(ctx, "SELECT balance FROM users WHERE id = $1::uuid", userID).Scan(&balance)
		return status, balance
	}

	// 1. Under-payment (59 cents vs $0.60 order) must be refused.
	mkOrder("ORD-verify-under", 600_000, "USD", 1.0)
	call("ORD-verify-under", "pi_under", 59, "stripe")
	if status, bal := orderState("ORD-verify-under"); status != "pending" || bal != 0 {
		t.Errorf("under-payment: status=%q balance=%d, want pending/0", status, bal)
	}

	// 2. Exact payment completes and credits once; redelivery is a no-op.
	mkOrder("ORD-verify-exact", 600_000, "USD", 1.0)
	call("ORD-verify-exact", "pi_exact", 60, "stripe")
	if status, bal := orderState("ORD-verify-exact"); status != "completed" || bal != 600_000 {
		t.Errorf("exact payment: status=%q balance=%d, want completed/600000", status, bal)
	}
	call("ORD-verify-exact", "pi_exact", 60, "stripe")
	if _, bal := orderState("ORD-verify-exact"); bal != 600_000 {
		t.Errorf("redelivery double-credited: balance=%d, want 600000", bal)
	}

	// 3. CNY round-trip within one-fen tolerance completes.
	// $1.00 order at 7.2 -> 720 fen -> back-conversion may differ by a few micro.
	mkOrder("ORD-verify-cny", 1_000_000, "CNY", 7.2)
	call("ORD-verify-cny", "pi_cny", 720, "wechat")
	if status, _ := orderState("ORD-verify-cny"); status != "completed" {
		t.Errorf("CNY within tolerance: status=%q, want completed", status)
	}

	// 4. CNY clearly under-paid (700 fen) must be refused.
	mkOrder("ORD-verify-cny-under", 1_000_000, "CNY", 7.2)
	call("ORD-verify-cny-under", "pi_cny_u", 700, "wechat")
	if status, _ := orderState("ORD-verify-cny-under"); status != "pending" {
		t.Errorf("CNY under-payment: status=%q, want pending", status)
	}
}

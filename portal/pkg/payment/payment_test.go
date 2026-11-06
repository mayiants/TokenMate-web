package payment

import (
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"github.com/alipay/global-open-sdk-go/com/alipay/api/tools"
	"strings"
	"testing"
	"time"
)

// TestNewStripeGateway tests Stripe gateway creation.
func TestNewStripeGateway(t *testing.T) {
	cfg := &StripeConfig{
		SecretKey:     "sk_test_123",
		WebhookSecret: "whsec_abc",
	}
	gw := NewStripeGateway(cfg)
	if gw == nil {
		t.Fatal("NewStripeGateway returned nil")
	}
	if gw.Name() != "stripe" {
		t.Errorf("Name() = %q, want stripe", gw.Name())
	}
}

// TestStripeGatewayCreateSessionNoConfig tests that CreateSession returns error without config.
func TestStripeGatewayCreateSessionNoConfig(t *testing.T) {
	gw := NewStripeGateway(&StripeConfig{})
	_, err := gw.CreateSession(&Order{OrderID: "test"})
	if err == nil {
		t.Error("CreateSession without secret key should return error")
	}
}

// TestStripeGatewayVerifySignatureNoConfig tests signature verification without config.
func TestStripeGatewayVerifySignatureNoConfig(t *testing.T) {
	gw := NewStripeGateway(&StripeConfig{})
	if gw.VerifySignature([]byte("test"), "sig") {
		t.Error("VerifySignature without webhook secret should return false")
	}
}

// TestStripeGatewayQueryOrderNotSupported tests that QueryOrder is not supported.
func TestStripeGatewayQueryOrderNotSupported(t *testing.T) {
	gw := NewStripeGateway(&StripeConfig{})
	_, err := gw.QueryOrder("ORD-123")
	if err == nil {
		t.Error("QueryOrder should return error (not supported)")
	}
}

// TestNewAlipayGateway tests Alipay gateway creation.
func TestNewAlipayGateway(t *testing.T) {
	cfg := &AlipayConfig{
		ClientID:           "app123",
		MerchantPrivateKey: "key",
		AlipayPublicKey:    "pub",
	}
	gw := NewAlipayGateway(cfg)
	if gw == nil {
		t.Fatal("NewAlipayGateway returned nil")
	}
	if gw.Name() != "alipay" {
		t.Errorf("Name() = %q, want alipay", gw.Name())
	}
}

// TestAlipayGatewayCreateSessionNoConfig tests that CreateSession returns error without config.
func TestAlipayGatewayCreateSessionNoConfig(t *testing.T) {
	gw := NewAlipayGateway(&AlipayConfig{})
	_, err := gw.CreateSession(&Order{OrderID: "test"})
	if err == nil {
		t.Error("CreateSession without config should return error")
	}
}

// TestAlipayGatewayVerifySignatureNoKey tests signature verification without public key.
func TestAlipayGatewayVerifySignatureNoKey(t *testing.T) {
	gw := NewAlipayGateway(&AlipayConfig{})
	if gw.VerifySignature([]byte("test"), "") {
		t.Error("VerifySignature with empty signature should return false")
	}
}

// TestNewWechatGateway tests WeChat gateway Name method.
func TestNewWechatGateway(t *testing.T) {
	// NewWechatGateway requires valid PEM key, test Name() via direct struct
	gw := &WechatGateway{config: &WechatConfig{
		AppID:         "wx123",
		MchID:         "mch456",
		MchSerialNo:   "serial",
		MchPrivateKey: "key",
		ApiV3Key:      "v3key",
	}}
	if gw.Name() != "wechat" {
		t.Errorf("Name() = %q, want wechat", gw.Name())
	}
}

// TestWechatGatewayVerifyWebhookSignatureNoKey ensures verification fails closed
// when no platform public key is configured.
func TestWechatGatewayVerifyWebhookSignatureNoKey(t *testing.T) {
	gw := &WechatGateway{config: &WechatConfig{ApiV3Key: "v3key"}}
	if gw.VerifyWebhookSignature("123", "nonce", "sig", "body") {
		t.Error("VerifyWebhookSignature without platform public key should return false")
	}
}

// TestWechatGatewayVerifyWebhookSignatureValid verifies a correctly signed webhook.
func TestWechatGatewayVerifyWebhookSignatureValid(t *testing.T) {
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	pubDER, err := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))

	body := `{"resource":{"ciphertext":"test"}}`
	timestamp := "1710000000"
	nonce := "nonce123"
	message := fmt.Sprintf("%s\n%s\n%s\n", timestamp, nonce, body)
	hashed := sha256.Sum256([]byte(message))
	sigBytes, err := rsa.SignPKCS1v15(rand.Reader, privKey, crypto.SHA256, hashed[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	signature := base64.StdEncoding.EncodeToString(sigBytes)

	gw := &WechatGateway{config: &WechatConfig{PlatformPublicKey: pubPEM}}
	if !gw.VerifyWebhookSignature(timestamp, nonce, signature, body) {
		t.Error("VerifyWebhookSignature should accept a valid signature")
	}
	if gw.VerifyWebhookSignature(timestamp, nonce, signature, body+"tampered") {
		t.Error("VerifyWebhookSignature should reject a tampered body")
	}
}

func TestMicroUSDToMajorUnitString(t *testing.T) {
	// USD: fractional amounts must survive (previously truncated to "6").
	if got := MicroUSDToMajorUnitString(6_500_000, "USD", 0); got != "6.50" {
		t.Errorf("USD: got %q, want %q", got, "6.50")
	}
	// CNY: rate applied, two decimals.
	if got := MicroUSDToMajorUnitString(6_500_000, "CNY", 7.2); got != "46.80" {
		t.Errorf("CNY: got %q, want %q", got, "46.80")
	}
}

func TestMajorUnitHundredthsToMicroUSD(t *testing.T) {
	if got := MajorUnitHundredthsToMicroUSD(650, "USD", 0); got != 6_500_000 {
		t.Errorf("USD: got %d, want 6500000", got)
	}
	// CNY round-trips through integer fen with float division, so the result
	// may be a few micro-USD off; it must stay within the one-fen tolerance
	// that completeOrder applies.
	got := MajorUnitHundredthsToMicroUSD(4680, "CNY", 7.2)
	if diff := got - 6_500_000; diff < -CNYFenToleranceMicroUSD(7.2) || diff > CNYFenToleranceMicroUSD(7.2) {
		t.Errorf("CNY: got %d, want 6500000 ± %d", got, CNYFenToleranceMicroUSD(7.2))
	}
}

func TestCNYFenToleranceMicroUSD(t *testing.T) {
	// One fen at 7.2 ≈ 1388.9 micro-USD; helper must cover it (+1 truncation).
	got := CNYFenToleranceMicroUSD(7.2)
	if got < 1389 || got > 1400 {
		t.Errorf("got %d, want ~1389", got)
	}
	if CNYFenToleranceMicroUSD(0) != 0 {
		t.Error("zero rate must yield zero tolerance")
	}
}

// TestAlipayHandleWebhookFractionalAmount pins the fix for the integer
// truncation bug: a "10.50" payment must be carried as 1050 hundredths,
// not truncated to 10.
func TestAlipayHandleWebhookFractionalAmount(t *testing.T) {
	g := &AlipayGateway{}
	payload := []byte(`{
		"notifyType": "PAYMENT_RESULT",
		"paymentRequestId": "ORD-abc123",
		"paymentId": "pay-1",
		"paymentAmount": {"value": "10.50", "currency": "USD"},
		"paymentResultInfo": {"resultStatus": "S"}
	}`)
	result, err := g.HandleWebhook(payload, "")
	if err != nil {
		t.Fatalf("HandleWebhook: %v", err)
	}
	if result.Amount != 1050 {
		t.Errorf("Amount = %d, want 1050 (hundredths of major unit)", result.Amount)
	}
	if result.Status != "success" || result.OrderID != "ORD-abc123" {
		t.Errorf("unexpected result: %+v", result)
	}
}

// TestPayPalVerifySignatureFailsClosed pins fail-closed behavior: no signature
// value is accepted until real verify-webhook-signature API verification is
// implemented (previously any non-empty signature passed).
func TestPayPalVerifySignatureFailsClosed(t *testing.T) {
	g := &PayPalGateway{}
	if g.VerifySignature([]byte(`{}`), "forged-signature") {
		t.Error("non-empty signature must not pass verification")
	}
	if g.VerifySignature([]byte(`{}`), "") {
		t.Error("empty signature must not pass verification")
	}
}

// stripeTestSignature builds a valid Stripe-Signature header for payload.
func stripeTestSignature(secret string, payload []byte) string {
	ts := time.Now().Unix()
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.", ts)
	mac.Write(payload)
	return fmt.Sprintf("t=%d,v1=%s", ts, hex.EncodeToString(mac.Sum(nil)))
}

// TestStripeHandleWebhookChargeRefunded verifies charge.refunded events are
// parsed into a refunded WebhookResult keyed by payment intent.
func TestStripeHandleWebhookChargeRefunded(t *testing.T) {
	secret := "whsec_test_refund"
	g := NewStripeGateway(&StripeConfig{WebhookSecret: secret})

	payload := []byte(`{
		"id": "evt_1",
		"object": "event",
		"api_version": "2026-05-27.dahlia",
		"type": "charge.refunded",
		"data": {"object": {
			"id": "ch_1",
			"object": "charge",
			"payment_intent": "pi_123",
			"amount_refunded": 650,
			"currency": "usd"
		}}
	}`)
	sig := stripeTestSignature(secret, payload)

	result, err := g.HandleWebhook(payload, sig)
	if err != nil {
		t.Fatalf("HandleWebhook: %v", err)
	}
	if result.Status != "refunded" {
		t.Errorf("Status = %q, want refunded", result.Status)
	}
	if result.PaymentID != "pi_123" {
		t.Errorf("PaymentID = %q, want pi_123", result.PaymentID)
	}
	if result.Amount != 650 {
		t.Errorf("Amount = %d, want 650", result.Amount)
	}

	// A tampered payload must be rejected by signature verification.
	tampered := []byte(strings.Replace(string(payload), "pi_123", "pi_evil", 1))
	if g.VerifySignature(tampered, sig) {
		t.Error("tampered payload passed signature verification")
	}
}

// TestAlipayVerifyWebhookSignature exercises the real RSA-SHA256 path via the
// SDK: a signature produced with GenSign must verify, tampering must not.
func TestAlipayVerifyWebhookSignature(t *testing.T) {
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	// The SDK expects the public key as base64 DER (no PEM headers).
	pubKeyB64 := base64.StdEncoding.EncodeToString(pubDER)

	privDER, err := x509.MarshalPKCS8PrivateKey(privKey)
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	privPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}))

	gw := &AlipayGateway{config: &AlipayConfig{AlipayPublicKey: pubKeyB64}}

	method, path, clientID, reqTime, body := "POST", "/api/webhook/alipay", "client-1", "1710000000", `{"paymentRequestId":"ORD-1"}`
	rawSig, err := tools.GenSign(method, path, clientID, reqTime, body, privPEM)
	if err != nil {
		t.Fatalf("GenSign: %v", err)
	}
	// The alipay-signature header carries the signature in this envelope.
	sig := "algorithm=RSA256,keyVersion=1,signature=" + rawSig

	if !gw.VerifyWebhookSignature(method, path, clientID, reqTime, body, sig) {
		t.Error("valid signature should verify")
	}
	if gw.VerifyWebhookSignature(method, path, clientID, reqTime, body+"tampered", sig) {
		t.Error("tampered body should not verify")
	}
	if gw.VerifyWebhookSignature(method, path, clientID, "1710000001", body, sig) {
		t.Error("tampered request-time should not verify")
	}
	// Fails closed with no public key configured.
	gwNoKey := &AlipayGateway{config: &AlipayConfig{}}
	if gwNoKey.VerifyWebhookSignature(method, path, clientID, reqTime, body, sig) {
		t.Error("missing public key should fail closed")
	}
	if gw.VerifyWebhookSignature(method, path, clientID, reqTime, body, "") {
		t.Error("empty signature should fail closed")
	}
}

// TestDecryptAESGCM covers WeChat V3 resource decryption: roundtrip succeeds,
// wrong key or malformed input fails.
func TestDecryptAESGCM(t *testing.T) {
	apiV3Key := "0123456789abcdef0123456789abcdef" // 32 bytes
	nonce := "abc123nonce!"
	associated := "transaction"
	plaintext := []byte(`{"amount":{"total":4680},"out_trade_no":"ORD-1"}`)

	block, err := aes.NewCipher([]byte(apiV3Key))
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("gcm: %v", err)
	}
	ciphertext := base64.StdEncoding.EncodeToString(gcm.Seal(nil, []byte(nonce), plaintext, []byte(associated)))

	got, err := decryptAESGCM(apiV3Key, nonce, ciphertext, associated)
	if err != nil {
		t.Fatalf("decryptAESGCM: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Errorf("decrypted %q, want %q", got, plaintext)
	}

	if _, err := decryptAESGCM("ffffffffffffffffffffffffffffffff", nonce, ciphertext, associated); err == nil {
		t.Error("wrong apiV3Key should fail")
	}
	if _, err := decryptAESGCM(apiV3Key, nonce, "not-base64!!!", associated); err == nil {
		t.Error("malformed base64 should fail")
	}
	if _, err := decryptAESGCM(apiV3Key, nonce, ciphertext, "wrong-ad"); err == nil {
		t.Error("wrong associated data should fail")
	}
}

// TestWechatHandleWebhookRefundSuccess verifies REFUND.SUCCESS callbacks are
// decrypted and parsed into a refunded WebhookResult.
func TestWechatHandleWebhookRefundSuccess(t *testing.T) {
	apiV3Key := "0123456789abcdef0123456789abcdef"
	nonce := "nonce1234567" // 12 bytes for AES-GCM
	associated := "refund"
	plaintext := []byte(`{
		"out_trade_no": "ORD-123",
		"out_refund_no": "REF-123",
		"refund_id": "refund-1",
		"refund_status": "SUCCESS",
		"amount": {"refund": 4680},
		"success_time": "2026-08-04T10:00:00+08:00"
	}`)

	block, err := aes.NewCipher([]byte(apiV3Key))
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("gcm: %v", err)
	}
	ciphertext := base64.StdEncoding.EncodeToString(gcm.Seal(nil, []byte(nonce), plaintext, []byte(associated)))

	payload := []byte(fmt.Sprintf(`{
		"event_type": "REFUND.SUCCESS",
		"resource": {
			"algorithm": "AEAD_AES_256_GCM",
			"ciphertext": "%s",
			"nonce": "%s",
			"associated_data": "%s"
		}
	}`, ciphertext, nonce, associated))

	gw := &WechatGateway{config: &WechatConfig{ApiV3Key: apiV3Key}}
	result, err := gw.HandleWebhook(payload, "")
	if err != nil {
		t.Fatalf("HandleWebhook: %v", err)
	}
	if result.Status != "refunded" {
		t.Errorf("Status = %q, want refunded", result.Status)
	}
	if result.OrderID != "ORD-123" {
		t.Errorf("OrderID = %q, want ORD-123", result.OrderID)
	}
	if result.PaymentID != "refund-1" {
		t.Errorf("PaymentID = %q, want refund-1", result.PaymentID)
	}
	if result.Amount != 4680 {
		t.Errorf("Amount = %d, want 4680", result.Amount)
	}
}

// TestAlipayHandleWebhookRefundSuccess verifies Alipay Global refund
// notifications are parsed into a refunded WebhookResult.
func TestAlipayHandleWebhookRefundSuccess(t *testing.T) {
	g := &AlipayGateway{}
	payload := []byte(`{
		"notifyType": "REFUND_RESULT",
		"refundRequestId": "ORD-abc123",
		"refundId": "refund-1",
		"paymentId": "pay-1",
		"refundAmount": {"value": "5.50", "currency": "USD"},
		"refundStatus": "SUCCESS",
		"refundTime": "2026-08-04T10:00:00+08:00"
	}`)
	result, err := g.HandleWebhook(payload, "")
	if err != nil {
		t.Fatalf("HandleWebhook: %v", err)
	}
	if result.Status != "refunded" {
		t.Errorf("Status = %q, want refunded", result.Status)
	}
	if result.OrderID != "ORD-abc123" {
		t.Errorf("OrderID = %q, want ORD-abc123", result.OrderID)
	}
	if result.PaymentID != "pay-1" {
		t.Errorf("PaymentID = %q, want pay-1", result.PaymentID)
	}
	if result.Amount != 550 {
		t.Errorf("Amount = %d, want 550", result.Amount)
	}
}

// TestPayPalHandleWebhookCaptureCompleted verifies PayPal webhook event parsing.
func TestPayPalHandleWebhookCaptureCompleted(t *testing.T) {
	g := &PayPalGateway{}
	payload := []byte(`{
		"id": "evt-1",
		"event_type": "PAYMENT.CAPTURE.COMPLETED",
		"resource": {
			"id": "capture-1",
			"status": "COMPLETED",
			"amount": {"currency_code": "USD", "value": "10.50"},
			"custom_id": "ORD-paypal-1"
		}
	}`)
	result, err := g.HandleWebhook(payload, "")
	if err != nil {
		t.Fatalf("HandleWebhook: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("Status = %q, want success", result.Status)
	}
	if result.OrderID != "ORD-paypal-1" {
		t.Errorf("OrderID = %q, want ORD-paypal-1", result.OrderID)
	}
	if result.PaymentID != "capture-1" {
		t.Errorf("PaymentID = %q, want capture-1", result.PaymentID)
	}
	if result.Amount != 1050 {
		t.Errorf("Amount = %d, want 1050", result.Amount)
	}
}

// TestPayPalHandleWebhookCaptureRefunded verifies PayPal refund webhook parsing.
func TestPayPalHandleWebhookCaptureRefunded(t *testing.T) {
	g := &PayPalGateway{}
	payload := []byte(`{
		"id": "evt-2",
		"event_type": "PAYMENT.CAPTURE.REFUNDED",
		"resource": {
			"id": "refund-1",
			"status": "COMPLETED",
			"amount": {"currency_code": "USD", "value": "10.50"},
			"capture_id": "capture-1",
			"custom_id": "ORD-paypal-1"
		}
	}`)
	result, err := g.HandleWebhook(payload, "")
	if err != nil {
		t.Fatalf("HandleWebhook: %v", err)
	}
	if result.Status != "refunded" {
		t.Errorf("Status = %q, want refunded", result.Status)
	}
	if result.PaymentID != "capture-1" {
		t.Errorf("PaymentID = %q, want capture-1", result.PaymentID)
	}
	if result.Amount != 1050 {
		t.Errorf("Amount = %d, want 1050", result.Amount)
	}
}

package query

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/pkg/payment"
)

// SuperAdminData holds a super admin record.
type SuperAdminData struct {
	ID        string
	Email     string
	Name      string
	Role      string
	CreatedAt time.Time
}

// GetSuperAdmins fetches all super admins.
func GetSuperAdmins(ctx context.Context, database *db.DB) ([]SuperAdminData, error) {
	rows, err := database.Query(ctx,
		`SELECT id::text, email, COALESCE(name, ''), role, created_at
		 FROM users WHERE role IN ('super_admin', 'system_admin') ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var admins []SuperAdminData
	for rows.Next() {
		var a SuperAdminData
		if err := rows.Scan(&a.ID, &a.Email, &a.Name, &a.Role, &a.CreatedAt); err == nil {
			admins = append(admins, a)
		}
	}
	return admins, nil
}

// PaymentConfig holds payment gateway configuration for the admin UI.
// Sensitive fields hold masked values (e.g. "****7890"), never the plaintext.
type PaymentConfig struct {
	StripeSecretKey     string // masked
	StripeWebhookSecret string // masked
	PayPalClientID      string
	PayPalSecret        string // masked
	PayPalMode          string
	PayPalWebhookID     string
	AlipayClientID      string
	AlipayPrivateKey    string // masked (merchant private key)
	AlipayPublicKey     string
	WeChatAppID         string
	WeChatMchID         string
	WeChatMchSerialNo   string
	WeChatPrivateKey    string // masked (merchant private key)
	WeChatAPIV3Key      string // masked
	GatewayEnabled      map[string]bool
}

// GetPaymentConfig fetches payment gateway configuration from payment_gateways,
// which is the table the payment link actually reads. Secret values are masked.
func GetPaymentConfig(ctx context.Context, database *db.DB, encryptionKey string) (*PaymentConfig, error) {
	d := &PaymentConfig{
		PayPalMode:     "sandbox",
		GatewayEnabled: map[string]bool{},
	}

	rows, err := database.Query(ctx,
		`SELECT gateway, enabled, COALESCE(config::text, '{}') FROM payment_gateways`)
	if err != nil {
		// Table might not exist, return empty config
		return d, nil
	}
	defer rows.Close()

	configs := map[string]map[string]interface{}{}
	for rows.Next() {
		var gateway, configStr string
		var enabled bool
		if err := rows.Scan(&gateway, &enabled, &configStr); err != nil {
			continue
		}
		d.GatewayEnabled[gateway] = enabled
		cfg := map[string]interface{}{}
		if err := json.Unmarshal([]byte(configStr), &cfg); err == nil {
			payment.MaskSensitiveFields(gateway, cfg, encryptionKey)
			configs[gateway] = cfg
		}
	}

	get := func(gateway, key string) string {
		s, _ := configs[gateway][key].(string)
		return s
	}

	d.StripeSecretKey = get("stripe", "secret_key")
	d.StripeWebhookSecret = get("stripe", "webhook_secret")
	d.PayPalClientID = get("paypal", "client_id")
	d.PayPalSecret = get("paypal", "secret")
	if mode := get("paypal", "mode"); mode != "" {
		d.PayPalMode = mode
	}
	d.PayPalWebhookID = get("paypal", "webhook_id")
	d.AlipayClientID = get("alipay", "client_id")
	d.AlipayPrivateKey = get("alipay", "merchant_private_key")
	d.AlipayPublicKey = get("alipay", "alipay_public_key")
	d.WeChatAppID = get("wechat", "app_id")
	d.WeChatMchID = get("wechat", "mch_id")
	d.WeChatMchSerialNo = get("wechat", "mch_serial_no")
	d.WeChatPrivateKey = get("wechat", "mch_private_key")
	d.WeChatAPIV3Key = get("wechat", "apiv3_key")

	return d, nil
}

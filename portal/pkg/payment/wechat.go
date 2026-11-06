package payment

import (
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/core/option"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments/native"
	"github.com/wechatpay-apiv3/wechatpay-go/utils"
)

// WechatGateway implements WeChat Pay V3 API using the official SDK
type WechatGateway struct {
	config *WechatConfig
	client *core.Client
}

// NewWechatGateway creates a new WeChat Pay V3 gateway
func NewWechatGateway(config *WechatConfig) (*WechatGateway, error) {
	mchPrivateKey, err := utils.LoadPrivateKey(config.MchPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to load merchant private key: %w", err)
	}

	ctx := context.Background()
	client, err := core.NewClient(ctx,
		option.WithWechatPayAutoAuthCipher(config.MchID, config.MchSerialNo, mchPrivateKey, config.ApiV3Key),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create wechat pay client: %w", err)
	}

	return &WechatGateway{config: config, client: client}, nil
}

func (g *WechatGateway) Name() string { return "wechat" }

func (g *WechatGateway) CreateSession(order *Order) (string, error) {
	svc := native.NativeApiService{Client: g.client}

	// WeChat Pay expects CNY 分 (1 yuan = 100 fen). Convert from micro-USD.
	totalFen := MicroUSDToCNYFen(order.Amount, order.ExchangeRate)
	resp, _, err := svc.Prepay(context.Background(), native.PrepayRequest{
		Appid:       core.String(g.config.AppID),
		Mchid:       core.String(g.config.MchID),
		Description: core.String(order.ProductName),
		OutTradeNo:  core.String(order.OrderID),
		NotifyUrl:   core.String(g.config.NotifyURL),
		Amount:      &native.Amount{Total: core.Int64(totalFen)},
	})
	if err != nil {
		return "", fmt.Errorf("WeChat Pay prepay error: %w", err)
	}

	if resp.CodeUrl == nil {
		return "", fmt.Errorf("WeChat Pay returned empty code_url")
	}
	return *resp.CodeUrl, nil
}

func (g *WechatGateway) HandleWebhook(payload []byte, signature string) (*WebhookResult, error) {
	// V3 callback body format: {"resource":{"algorithm":"AEAD_AES_256_GCM","ciphertext":"...","nonce":"...","associated_data":"..."}}
	var notification struct {
		EventType string `json:"event_type"`
		Resource  struct {
			Algorithm      string `json:"algorithm"`
			Ciphertext     string `json:"ciphertext"`
			Nonce          string `json:"nonce"`
			AssociatedData string `json:"associated_data"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(payload, &notification); err != nil {
		return nil, fmt.Errorf("failed to parse notification: %w", err)
	}

	// Decrypt with AES-256-GCM
	plaintext, err := decryptAESGCM(g.config.ApiV3Key, notification.Resource.Nonce, notification.Resource.Ciphertext, notification.Resource.AssociatedData)
	if err != nil {
		return nil, fmt.Errorf("decrypt failed: %w", err)
	}

	switch notification.EventType {
	case "TRANSACTION.SUCCESS":
		return g.parseTransactionSuccess(plaintext, payload)
	case "REFUND.SUCCESS":
		return g.parseRefundSuccess(plaintext, payload)
	case "REFUND.ABNORMAL", "REFUND.CLOSED":
		return nil, fmt.Errorf("refund event %s ignored", notification.EventType)
	default:
		return nil, fmt.Errorf("unsupported event: %s", notification.EventType)
	}
}

func (g *WechatGateway) parseTransactionSuccess(plaintext, payload []byte) (*WebhookResult, error) {
	var tx struct {
		OutTradeNo    string `json:"out_trade_no"`
		TransactionID string `json:"transaction_id"`
		TradeState    string `json:"trade_state"`
		Amount        struct {
			Total int64 `json:"total"`
		} `json:"amount"`
		SuccessTime string `json:"success_time"`
	}
	if err := json.Unmarshal(plaintext, &tx); err != nil {
		return nil, fmt.Errorf("failed to parse transaction: %w", err)
	}

	result := &WebhookResult{
		OrderID:   tx.OutTradeNo,
		PaymentID: tx.TransactionID,
		Amount:    tx.Amount.Total,
		Currency:  "cny",
		PaidAt:    time.Now(),
		RawData:   payload,
	}

	if tx.TradeState == "SUCCESS" {
		result.Status = "success"
		if t, err := time.Parse(time.RFC3339, tx.SuccessTime); err == nil {
			result.PaidAt = t
		}
	} else {
		result.Status = "pending"
	}

	return result, nil
}

func (g *WechatGateway) parseRefundSuccess(plaintext, payload []byte) (*WebhookResult, error) {
	var refund struct {
		OutTradeNo   string `json:"out_trade_no"`
		OutRefundNo  string `json:"out_refund_no"`
		RefundID     string `json:"refund_id"`
		RefundStatus string `json:"refund_status"`
		Amount       struct {
			Refund int64 `json:"refund"`
		} `json:"amount"`
		SuccessTime string `json:"success_time"`
	}
	if err := json.Unmarshal(plaintext, &refund); err != nil {
		return nil, fmt.Errorf("failed to parse refund: %w", err)
	}

	result := &WebhookResult{
		OrderID:   refund.OutTradeNo,
		PaymentID: refund.RefundID,
		Amount:    refund.Amount.Refund,
		Currency:  "cny",
		PaidAt:    time.Now(),
		RawData:   payload,
	}

	if refund.RefundStatus == "SUCCESS" {
		result.Status = "refunded"
		if t, err := time.Parse(time.RFC3339, refund.SuccessTime); err == nil {
			result.PaidAt = t
		}
	} else {
		result.Status = "pending"
	}

	return result, nil
}

func (g *WechatGateway) VerifySignature(payload []byte, signature string) bool {
	// The generic Gateway.VerifySignature interface cannot carry WeChat V3 webhook
	// headers (timestamp, nonce, serial). Real verification is performed by
	// VerifyWebhookSignature in the route handler.
	return false
}

// VerifyWebhookSignature verifies a WeChat Pay V3 webhook RSA-SHA256 signature.
// Headers required: Wechatpay-Timestamp, Wechatpay-Nonce, Wechatpay-Serial,
// Wechatpay-Signature. The PlatformPublicKey must be configured in PEM format.
func (g *WechatGateway) VerifyWebhookSignature(timestamp, nonce, signature, body string) bool {
	if g.config.PlatformPublicKey == "" {
		return false
	}

	pubKey, err := utils.LoadPublicKey(g.config.PlatformPublicKey)
	if err != nil {
		return false
	}

	sigBytes, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return false
	}

	message := fmt.Sprintf("%s\n%s\n%s\n", timestamp, nonce, body)
	hashed := sha256.Sum256([]byte(message))

	if err := rsa.VerifyPKCS1v15(pubKey, crypto.SHA256, hashed[:], sigBytes); err != nil {
		return false
	}
	return true
}

func (g *WechatGateway) QueryOrder(orderID string) (*QueryResult, error) {
	svc := native.NativeApiService{Client: g.client}

	resp, _, err := svc.QueryOrderByOutTradeNo(context.Background(), native.QueryOrderByOutTradeNoRequest{
		OutTradeNo: core.String(orderID),
		Mchid:      core.String(g.config.MchID),
	})
	if err != nil {
		return nil, fmt.Errorf("WeChat query error: %w", err)
	}

	if resp.TransactionId == nil {
		return nil, fmt.Errorf("WeChat query returned empty transaction_id")
	}
	if resp.Amount == nil || resp.Amount.Total == nil {
		return nil, fmt.Errorf("WeChat query returned empty amount")
	}
	if resp.TradeState == nil {
		return nil, fmt.Errorf("WeChat query returned empty trade_state")
	}

	result := &QueryResult{
		OrderID:   orderID,
		PaymentID: *resp.TransactionId,
		Amount:    *resp.Amount.Total,
	}

	switch *resp.TradeState {
	case "SUCCESS":
		result.Status = "success"
		if resp.SuccessTime != nil {
			if t, err := time.Parse(time.RFC3339, *resp.SuccessTime); err == nil {
				result.PaidAt = &t
			}
		}
	case "NOTPAY", "USERPAYING":
		result.Status = "pending"
	default:
		result.Status = "closed"
	}

	return result, nil
}

// decryptAESGCM decrypts WeChat Pay V3 notification resource
func decryptAESGCM(apiV3Key, nonce, ciphertext, associatedData string) ([]byte, error) {
	key := []byte(apiV3Key)
	ciphertextBytes, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	return gcm.Open(nil, []byte(nonce), ciphertextBytes, []byte(associatedData))
}

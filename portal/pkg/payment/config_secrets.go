package payment

import (
	"encoding/json"
	"strings"

	"github.com/mayiants/ai-portal-go/pkg/crypto"
)

// gatewaySensitiveFields lists the config keys holding secrets per gateway.
// These are encrypted at rest and masked when echoed back to the admin UI.
var gatewaySensitiveFields = map[string][]string{
	"stripe": {"secret_key", "webhook_secret"},
	"paypal": {"secret"},
	"alipay": {"merchant_private_key"},
	"wechat": {"mch_private_key", "apiv3_key"},
}

const maskPrefix = "****"

// IsMaskedValue reports whether v is a UI mask placeholder (e.g. "****7890"),
// or equals the currently stored masked value.
func IsMaskedValue(v string, existing ...string) bool {
	if strings.HasPrefix(v, maskPrefix) {
		return true
	}
	for _, e := range existing {
		if v == e {
			return true
		}
	}
	return false
}

// MaskValue renders a mask revealing only the last 4 characters.
func MaskValue(v string) string {
	if len(v) > 4 {
		return maskPrefix + v[len(v)-4:]
	}
	return maskPrefix
}

// EncryptSensitiveFields encrypts sensitive config values in place before storage.
// Values submitted as the UI mask (or left empty) keep the previously stored
// value from existing; when there is none the key is dropped. Without an
// encryption key configured, values are stored as plaintext.
func EncryptSensitiveFields(gateway string, config, existing map[string]interface{}, encryptionKey string) error {
	for _, key := range gatewaySensitiveFields[gateway] {
		raw, ok := config[key]
		if !ok {
			continue
		}
		s, _ := raw.(string)
		var existingVal string
		if prev, ok := existing[key]; ok {
			existingVal, _ = prev.(string)
		}
		if s == "" || IsMaskedValue(s, existingVal) {
			if prev, ok := existing[key]; ok {
				config[key] = prev
			} else {
				delete(config, key)
			}
			continue
		}
		if encryptionKey == "" {
			continue
		}
		enc, err := crypto.Encrypt(s, encryptionKey)
		if err != nil {
			return err
		}
		config[key] = enc
	}
	return nil
}

// MaskSensitiveFields replaces stored secret values in config with masks (in place).
func MaskSensitiveFields(gateway string, config map[string]interface{}, encryptionKey string) {
	for _, key := range gatewaySensitiveFields[gateway] {
		raw, ok := config[key]
		if !ok {
			continue
		}
		s, _ := raw.(string)
		if s == "" {
			continue
		}
		plain := s
		if encryptionKey != "" {
			if dec, err := crypto.Decrypt(s, encryptionKey); err == nil {
				plain = dec
			}
		}
		config[key] = MaskValue(plain)
	}
}

// DecryptGatewayConfig returns configJSON with sensitive fields decrypted,
// suitable for Factory.CreateGateway. Undecryptable values (e.g. legacy
// plaintext or a missing key) are passed through unchanged.
func DecryptGatewayConfig(gateway, configJSON, encryptionKey string) string {
	if encryptionKey == "" || configJSON == "" {
		return configJSON
	}
	var config map[string]interface{}
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return configJSON
	}
	changed := false
	for _, key := range gatewaySensitiveFields[gateway] {
		raw, ok := config[key]
		if !ok {
			continue
		}
		s, _ := raw.(string)
		if s == "" {
			continue
		}
		dec, err := crypto.Decrypt(s, encryptionKey)
		if err != nil {
			continue // legacy plaintext or wrong key: use as-is
		}
		config[key] = dec
		changed = true
	}
	if !changed {
		return configJSON
	}
	out, err := json.Marshal(config)
	if err != nil {
		return configJSON
	}
	return string(out)
}

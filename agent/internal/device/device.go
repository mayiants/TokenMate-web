// Package device derives the stable device fingerprint sent at enrollment.
// The server stores only its SHA-256; the raw material lives in the
// credential store. Windows keys off the machine GUID so reinstalling the
// agent under the same user still identifies the same device.
package device

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/mayiants/ai-gateway-go/agent/internal/creds"
)

// Fingerprint returns the stable raw fingerprint for this device, creating
// and persisting one on first use.
func Fingerprint() (string, error) {
	if raw, err := creds.GetDeviceSecret(); err == nil && raw != "" {
		return raw, nil
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate device secret: %w", err)
	}
	value := platformSeed() + "-" + hex.EncodeToString(raw)
	if err := creds.PutDeviceSecret(value); err != nil {
		return "", err
	}
	return value, nil
}

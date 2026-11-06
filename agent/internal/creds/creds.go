// Package creds stores the agent's own secrets. On Windows it uses the
// Credential Manager (DPAPI); elsewhere it falls back to a 0600 file so the
// agent can be developed and smoke-tested on Linux. The key written into
// client configs is intentionally separate: third-party clients read plain
// files, and the gateway (not the local store) is the enforcement point.
package creds

const (
	serviceName  = "WoodgitAgent"
	keyUser      = "api_key"
	secretUser   = "device_secret" // raw device fingerprint material
)

func PutAPIKey(key string) error    { return put(keyUser, key) }
func GetAPIKey() (string, error)    { return get(keyUser) }
func PutDeviceSecret(s string) error { return put(secretUser, s) }
func GetDeviceSecret() (string, error) { return get(secretUser) }

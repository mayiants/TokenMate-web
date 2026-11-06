//go:build !windows

package creds

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mayiants/ai-gateway-go/agent/internal/state"
)

// File fallback for non-Windows builds (development and smoke tests).
// Permissions are tightened to 0600; production storage is Windows
// Credential Manager (creds_windows.go).

func filePath(user string) string {
	return filepath.Join(state.Dir(), "cred-"+user)
}

func put(user, secret string) error {
	if err := os.MkdirAll(state.Dir(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(filePath(user), []byte(secret), 0o600)
}

func get(user string) (string, error) {
	raw, err := os.ReadFile(filePath(user))
	if err != nil {
		return "", fmt.Errorf("secret %s not found (was the agent enrolled?): %w", user, err)
	}
	return string(raw), nil
}

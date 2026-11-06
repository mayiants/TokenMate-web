//go:build windows

package creds

import (
	"fmt"

	"github.com/danieljoos/wincred"
)

func put(user, secret string) error {
	cred := wincred.NewGenericCredential(serviceName + "/" + user)
	cred.UserName = user
	cred.CredentialBlob = []byte(secret)
	if err := cred.Write(); err != nil {
		return fmt.Errorf("Credential Manager write failed: %w", err)
	}
	return nil
}

func get(user string) (string, error) {
	cred, err := wincred.GetGenericCredential(serviceName + "/" + user)
	if err != nil {
		return "", fmt.Errorf("Credential Manager read failed (was the agent enrolled?): %w", err)
	}
	return string(cred.CredentialBlob), nil
}

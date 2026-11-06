package sso

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"
)

func ecJWK(t *testing.T, pub *ecdsa.PublicKey) jwk {
	t.Helper()
	raw, err := pub.Bytes() // 0x04 || X || Y
	require.NoError(t, err)
	enc := base64.RawURLEncoding.EncodeToString
	return jwk{Kty: "EC", Crv: "P-256", X: enc(raw[1:33]), Y: enc(raw[33:])}
}

func TestJWKPublicKeyEC(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	got, err := ecJWK(t, &priv.PublicKey).publicKey()
	require.NoError(t, err)
	require.True(t, priv.PublicKey.Equal(got), "round-tripped key must equal the original")

	// Coordinates with stripped leading zero bytes are padded, not rejected.
	k := ecJWK(t, &priv.PublicKey)
	x, _ := base64.RawURLEncoding.DecodeString(k.X)
	for len(x) > 1 && x[0] == 0 {
		x = x[1:]
	}
	k.X = base64.RawURLEncoding.EncodeToString(x)
	_, err = k.publicKey()
	require.NoError(t, err)

	// A point that is not on the curve is rejected.
	bad := ecJWK(t, &priv.PublicKey)
	y, _ := base64.RawURLEncoding.DecodeString(bad.Y)
	y[len(y)-1] ^= 0x01
	bad.Y = base64.RawURLEncoding.EncodeToString(y)
	_, err = bad.publicKey()
	require.Error(t, err)

	// Oversized coordinates and other curves are rejected.
	over := ecJWK(t, &priv.PublicKey)
	over.X = base64.RawURLEncoding.EncodeToString(make([]byte, 33))
	_, err = over.publicKey()
	require.Error(t, err)

	p384 := ecJWK(t, &priv.PublicKey)
	p384.Crv = "P-384"
	_, err = p384.publicKey()
	require.Error(t, err)
}

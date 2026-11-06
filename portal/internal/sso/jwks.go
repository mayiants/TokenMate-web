package sso

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"time"
)

const (
	// jwksRefreshGap is the minimum time between two network refreshes of one
	// key set. Without it, a stream of tokens carrying random kids would turn the
	// portal into a request amplifier against the identity provider.
	jwksRefreshGap = 30 * time.Second
	// jwksTTL bounds how long keys are trusted without a refresh, so a revoked
	// key stops working even if no unknown kid ever shows up.
	jwksTTL = time.Hour
	// maxJWKSKeys guards memory against absurd key sets.
	maxJWKSKeys = 50
)

type keySet struct {
	keys      map[string]crypto.PublicKey // by kid ("" holds the key of a kid-less set)
	fetchedAt time.Time
	attempted time.Time
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

type jwksDoc struct {
	Keys []jwk `json:"keys"`
}

// ErrUnknownKey means no key with the requested kid exists, even after a refresh.
var ErrUnknownKey = errors.New("sso: signing key not found")

// Key returns the public key for kid, refreshing the cached JWKS when the kid is
// unknown (key rotation) or the cache is older than jwksTTL.
func (c *Client) Key(ctx context.Context, jwksURI, kid string) (crypto.PublicKey, error) {
	c.mu.Lock()
	ks := c.keys[jwksURI]
	var fetchedAt, attempted time.Time
	if ks != nil {
		fetchedAt, attempted = ks.fetchedAt, ks.attempted
	}
	c.mu.Unlock()

	if ks != nil && c.now().Sub(fetchedAt) < jwksTTL {
		if k, ok := lookupKey(ks, kid); ok {
			return k, nil
		}
	}
	// Unknown kid or stale set: refresh, but not more often than jwksRefreshGap.
	if ks != nil && c.now().Sub(attempted) < jwksRefreshGap {
		if k, ok := lookupKey(ks, kid); ok {
			return k, nil
		}
		return nil, ErrUnknownKey
	}
	fresh, err := c.fetchKeySet(ctx, jwksURI)
	c.mu.Lock()
	if err != nil {
		if ks != nil {
			ks.attempted = c.now()
		}
		c.mu.Unlock()
		return nil, err
	}
	c.keys[jwksURI] = fresh
	c.mu.Unlock()
	if k, ok := lookupKey(fresh, kid); ok {
		return k, nil
	}
	return nil, ErrUnknownKey
}

// KeyCount fetches the key set and reports how many usable signing keys it
// holds. It backs the admin "test connection" button.
func (c *Client) KeyCount(ctx context.Context, jwksURI string) (int, error) {
	ks, err := c.fetchKeySet(ctx, jwksURI)
	if err != nil {
		return 0, err
	}
	c.mu.Lock()
	c.keys[jwksURI] = ks
	c.mu.Unlock()
	return len(ks.keys), nil
}

func lookupKey(ks *keySet, kid string) (crypto.PublicKey, bool) {
	if kid == "" {
		// A token without kid is only unambiguous when the set has one key.
		if len(ks.keys) == 1 {
			for _, k := range ks.keys {
				return k, true
			}
		}
		return nil, false
	}
	k, ok := ks.keys[kid]
	return k, ok
}

func (c *Client) fetchKeySet(ctx context.Context, jwksURI string) (*keySet, error) {
	var doc jwksDoc
	if err := c.getJSON(ctx, jwksURI, &doc); err != nil {
		return nil, err
	}
	if len(doc.Keys) > maxJWKSKeys {
		return nil, ErrTooLarge
	}
	now := c.now()
	ks := &keySet{keys: map[string]crypto.PublicKey{}, fetchedAt: now, attempted: now}
	for _, k := range doc.Keys {
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		pub, err := k.publicKey()
		if err != nil {
			continue // skip keys we cannot or will not use (e.g. EC P-384, OKP)
		}
		ks.keys[k.Kid] = pub
	}
	if len(ks.keys) == 0 {
		return nil, errors.New("sso: JWKS contains no usable signing key")
	}
	return ks, nil
}

// publicKey converts RSA and P-256 EC keys; everything else is unsupported
// because ID tokens are limited to RS256 and ES256.
func (k jwk) publicKey() (crypto.PublicKey, error) {
	switch k.Kty {
	case "RSA":
		n, err := b64BigInt(k.N)
		if err != nil {
			return nil, err
		}
		e, err := b64BigInt(k.E)
		if err != nil || !e.IsInt64() || e.Int64() < 3 || e.Int64() > 1<<31-1 {
			return nil, errors.New("sso: bad RSA exponent")
		}
		if n.BitLen() < 2048 {
			return nil, errors.New("sso: RSA key too small")
		}
		return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
	case "EC":
		if k.Crv != "P-256" {
			return nil, fmt.Errorf("sso: unsupported curve %q", k.Crv)
		}
		x, err := b64Coord(k.X, 32)
		if err != nil {
			return nil, err
		}
		y, err := b64Coord(k.Y, 32)
		if err != nil {
			return nil, err
		}
		// ParseUncompressedPublicKey performs the on-curve check, replacing the
		// deprecated big.Int coordinate fields and Curve.IsOnCurve.
		point := append(append([]byte{0x04}, x...), y...)
		pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), point)
		if err != nil {
			return nil, errors.New("sso: EC point not on curve")
		}
		return pub, nil
	}
	return nil, fmt.Errorf("sso: unsupported key type %q", k.Kty)
}

// b64Coord decodes a base64url EC coordinate and left-pads it to size bytes
// (RFC 7518 requires full-length coordinates, but some IdPs strip leading zeros).
func b64Coord(s string, size int) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(raw) == 0 || len(raw) > size {
		return nil, errors.New("sso: bad EC coordinate")
	}
	out := make([]byte, size)
	copy(out[size-len(raw):], raw)
	return out, nil
}

func b64BigInt(s string) (*big.Int, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(raw) == 0 {
		return nil, errors.New("sso: bad base64 integer")
	}
	return new(big.Int).SetBytes(raw), nil
}

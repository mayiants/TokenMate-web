package sso

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"
)

// ClockSkew is the tolerance applied to exp/nbf/iat.
const ClockSkew = 2 * time.Minute

// Claims are the verified identity attributes taken from an ID token.
type Claims struct {
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
}

// VerifyParams describes what a valid ID token must look like.
type VerifyParams struct {
	Issuer   string
	ClientID string
	Nonce    string
}

// ErrInvalidToken is returned for every verification failure. The detailed
// reason is wrapped for logs/audit only and must not be shown to end users.
var ErrInvalidToken = errors.New("sso: invalid ID token")

func invalid(format string, a ...interface{}) error {
	return fmt.Errorf("%w: %s", ErrInvalidToken, fmt.Sprintf(format, a...))
}

// allowedAlgs is deliberately tiny: asymmetric signatures only. "none" and the
// HS* family are rejected before any key is looked up, which also rules out the
// classic attack of signing with HMAC using the provider's public key.
var allowedAlgs = []string{"RS256", "ES256"}

// VerifyIDToken validates signature and claims of raw against md's key set.
// Required: iss == configured issuer, aud contains client_id, azp == client_id
// when present (and mandatory for multi-valued aud), exp/iat/nbf within a two
// minute skew, nonce equal to the one bound to this login, a non-empty sub and
// an e-mail address the provider marks as verified.
func (c *Client) VerifyIDToken(ctx context.Context, md *Metadata, raw string, p VerifyParams) (*Claims, error) {
	if p.Issuer == "" || p.ClientID == "" || p.Nonce == "" {
		return nil, invalid("incomplete verification parameters")
	}
	if len(raw) > 16*1024 {
		return nil, invalid("token too large")
	}
	var keyErr error
	parser := jwtlib.NewParser(
		jwtlib.WithValidMethods(allowedAlgs),
		jwtlib.WithExpirationRequired(),
		jwtlib.WithLeeway(ClockSkew),
		jwtlib.WithTimeFunc(c.now),
	)
	claims := jwtlib.MapClaims{}
	tok, err := parser.ParseWithClaims(raw, claims, func(t *jwtlib.Token) (interface{}, error) {
		kid, _ := t.Header["kid"].(string)
		key, err := c.Key(ctx, md.JWKSURI, kid)
		if err != nil {
			keyErr = err
			return nil, err
		}
		// The jwt library ties key type to algorithm (RS256 needs *rsa.PublicKey,
		// ES256 needs *ecdsa.PublicKey), so a mismatched header alg is refused.
		return key, nil
	})
	if err != nil {
		if keyErr != nil {
			return nil, invalid("signing key unavailable: %v", keyErr)
		}
		return nil, invalid("%v", err)
	}
	if !tok.Valid {
		return nil, invalid("not valid")
	}

	now := c.now()
	if iss, _ := claims["iss"].(string); iss != p.Issuer {
		return nil, invalid("issuer mismatch")
	}
	auds, err := audiences(claims["aud"])
	if err != nil || !contains(auds, p.ClientID) {
		return nil, invalid("audience mismatch")
	}
	azp, hasAzp := claims["azp"].(string)
	if hasAzp && azp != p.ClientID {
		return nil, invalid("authorized party mismatch")
	}
	if len(auds) > 1 && !hasAzp {
		return nil, invalid("azp required for multiple audiences")
	}
	// iat is not mandatory in the library; when present it must not lie in the future.
	if v, ok := claims["iat"]; ok {
		iat, ok := v.(float64)
		if !ok {
			return nil, invalid("bad iat")
		}
		if time.Unix(int64(iat), 0).After(now.Add(ClockSkew)) {
			return nil, invalid("iat in the future")
		}
	}
	if nonce, _ := claims["nonce"].(string); nonce == "" || !constantTimeEqual(nonce, p.Nonce) {
		return nil, invalid("nonce mismatch")
	}
	sub, _ := claims["sub"].(string)
	if sub == "" || len(sub) > 255 {
		return nil, invalid("missing subject")
	}
	email, _ := claims["email"].(string)
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || len(email) > 255 || strings.Count(email, "@") != 1 {
		return nil, invalid("missing email")
	}
	if !emailVerified(claims["email_verified"]) {
		return nil, invalid("email not verified")
	}
	name, _ := claims["name"].(string)
	return &Claims{Subject: sub, Email: email, EmailVerified: true, Name: strings.TrimSpace(name)}, nil
}

// audiences accepts both the string and the array form of aud.
func audiences(v interface{}) ([]string, error) {
	switch a := v.(type) {
	case string:
		return []string{a}, nil
	case []interface{}:
		out := make([]string, 0, len(a))
		for _, e := range a {
			s, ok := e.(string)
			if !ok {
				return nil, errors.New("non-string audience")
			}
			out = append(out, s)
		}
		return out, nil
	}
	return nil, errors.New("missing audience")
}

// emailVerified accepts true and the string "true" some providers emit.
func emailVerified(v interface{}) bool {
	switch b := v.(type) {
	case bool:
		return b
	case string:
		return strings.EqualFold(b, "true")
	}
	return false
}

func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

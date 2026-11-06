package jwt

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	UserID       string `json:"user_id"`
	Email        string `json:"email"`
	Role         string `json:"role"`
	AccessMode   string `json:"access_mode,omitempty"`
	TokenVersion int    `json:"token_version,omitempty"`
	jwt.RegisteredClaims
}

type Manager struct {
	secret  []byte
	expires time.Duration
}

const (
	tokenIssuer   = "ai-portal"
	tokenAudience = "ai-portal"
)

func NewManager(secret string, expires time.Duration) *Manager {
	return &Manager{
		secret:  []byte(secret),
		expires: expires,
	}
}

// Generate issues a signed JWT. tokenVersion is mandatory and must reflect the
// user's current token_version from the database; callers that issue tokens
// without it produce version-0 tokens that bypass revocation checks.
func (m *Manager) Generate(userID string, email, role string, tokenVersion int) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:       userID,
		Email:        email,
		Role:         role,
		TokenVersion: tokenVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(m.expires)),
			IssuedAt:  jwt.NewNumericDate(now),
			Issuer:    tokenIssuer,
			Audience:  jwt.ClaimStrings{tokenAudience},
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(m.secret)
}

func (m *Manager) Verify(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return m.secret, nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, jwt.ErrSignatureInvalid
	}
	// New tokens carry iss/aud; legacy tokens without them are still accepted.
	if claims.Issuer != "" && claims.Issuer != tokenIssuer {
		return nil, jwt.ErrTokenInvalidIssuer
	}
	if len(claims.Audience) > 0 && !audienceContains(claims.Audience, tokenAudience) {
		return nil, jwt.ErrTokenInvalidAudience
	}

	return claims, nil
}

func audienceContains(aud jwt.ClaimStrings, target string) bool {
	for _, a := range aud {
		if a == target {
			return true
		}
	}
	return false
}

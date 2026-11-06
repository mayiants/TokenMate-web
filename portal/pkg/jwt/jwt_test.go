package jwt

import (
	"testing"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewManager(t *testing.T) {
	m := NewManager("my-secret-key-min-32-chars!!", time.Hour)
	assert.NotNil(t, m)
	assert.Equal(t, []byte("my-secret-key-min-32-chars!!"), m.secret)
	assert.Equal(t, time.Hour, m.expires)
}

func TestGenerateAndVerify(t *testing.T) {
	m := NewManager("test-secret-key-32-chars-long!", 30*time.Minute)

	token, err := m.Generate("user-001", "user@example.com", "user", 0)
	require.NoError(t, err)
	assert.NotEmpty(t, token)

	claims, err := m.Verify(token)
	require.NoError(t, err)
	assert.Equal(t, "user-001", claims.UserID)
	assert.Equal(t, "user@example.com", claims.Email)
	assert.Equal(t, "user", claims.Role)
}

func TestGenerateAndVerify_DifferentRoles(t *testing.T) {
	m := NewManager("test-secret-key-32-chars-long!", time.Hour)

	tests := []struct {
		name   string
		userID string
		email  string
		role   string
	}{
		{"user role", "u-1", "user@test.com", "user"},
		{"system_admin role", "u-2", "admin@test.com", "system_admin"},
		{"super_admin role", "u-3", "super@test.com", "super_admin"},
		{"enterprise_admin role", "u-4", "ent@test.com", "enterprise_admin"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token, err := m.Generate(tt.userID, tt.email, tt.role, 0)
			require.NoError(t, err)

			claims, err := m.Verify(token)
			require.NoError(t, err)
			assert.Equal(t, tt.userID, claims.UserID)
			assert.Equal(t, tt.email, claims.Email)
			assert.Equal(t, tt.role, claims.Role)
		})
	}
}

func TestVerify_ExpiredToken(t *testing.T) {
	m := NewManager("test-secret-key-32-chars-long!", -time.Hour) // negative expiry → already expired
	token, err := m.Generate("user-001", "user@example.com", "user", 0)
	require.NoError(t, err)

	_, err = m.Verify(token)
	assert.Error(t, err, "expired token should fail verification")
}

func TestVerify_WrongSecret(t *testing.T) {
	m1 := NewManager("secret-key-for-generator-32-chars", time.Hour)
	token, err := m1.Generate("user-001", "user@example.com", "user", 0)
	require.NoError(t, err)

	m2 := NewManager("different-secret-key-for-verify", time.Hour)
	_, err = m2.Verify(token)
	assert.Error(t, err, "token signed with different secret should fail verification")
}

func TestVerify_InvalidToken(t *testing.T) {
	m := NewManager("test-secret-key-32-chars-long!", time.Hour)

	tests := []struct {
		name  string
		token string
	}{
		{"empty token", ""},
		{"garbage", "not-a-jwt-token"},
		{"malformed", "eyJhbGciOiJIUzI1NiJ9.INVALID"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := m.Verify(tt.token)
			assert.Error(t, err, "invalid token should fail verification")
		})
	}
}

func TestGenerate_UniqueTokens(t *testing.T) {
	m := NewManager("test-secret-key-32-chars-long!", time.Hour)
	token1, _ := m.Generate("user-001", "user@example.com", "user", 0)
	// Wait to ensure different iat timestamps
	time.Sleep(1 * time.Second)
	token2, _ := m.Generate("user-001", "user@example.com", "user", 0)
	assert.NotEqual(t, token1, token2, "tokens generated at different times should differ (different iat)")
}

func TestGenerate_SameInputDifferentUser(t *testing.T) {
	m := NewManager("test-secret-key-32-chars-long!", time.Hour)
	token1, _ := m.Generate("user-001", "a@example.com", "user", 0)
	token2, _ := m.Generate("user-002", "b@example.com", "user", 0)
	assert.NotEqual(t, token1, token2, "different user IDs should produce different tokens")
}

func TestGenerate_IncludesIssuerAndAudience(t *testing.T) {
	m := NewManager("test-secret-key-32-chars-long!", time.Hour)
	token, err := m.Generate("user-001", "user@example.com", "user", 0)
	require.NoError(t, err)

	claims, err := m.Verify(token)
	require.NoError(t, err)
	assert.Equal(t, tokenIssuer, claims.Issuer)
	assert.Contains(t, []string(claims.Audience), tokenAudience)
}

func TestVerify_RejectsWrongIssuer(t *testing.T) {
	m := NewManager("test-secret-key-32-chars-long!", time.Hour)
	claims := Claims{
		UserID: "user-001",
		Email:  "user@example.com",
		Role:   "user",
		RegisteredClaims: jwtv5.RegisteredClaims{
			ExpiresAt: jwtv5.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwtv5.NewNumericDate(time.Now()),
			Issuer:    "evil-issuer",
			Audience:  jwtv5.ClaimStrings{tokenAudience},
		},
	}
	token, err := jwtv5.NewWithClaims(jwtv5.SigningMethodHS256, claims).SignedString(m.secret)
	require.NoError(t, err)

	_, err = m.Verify(token)
	assert.Error(t, err)
}

func TestVerify_AllowsLegacyWithoutIssuerAudience(t *testing.T) {
	m := NewManager("test-secret-key-32-chars-long!", time.Hour)
	claims := Claims{
		UserID: "user-001",
		Email:  "user@example.com",
		Role:   "user",
		RegisteredClaims: jwtv5.RegisteredClaims{
			ExpiresAt: jwtv5.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwtv5.NewNumericDate(time.Now()),
		},
	}
	token, err := jwtv5.NewWithClaims(jwtv5.SigningMethodHS256, claims).SignedString(m.secret)
	require.NoError(t, err)

	got, err := m.Verify(token)
	require.NoError(t, err)
	assert.Equal(t, "user-001", got.UserID)
}

package sso

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// RandomToken returns n random bytes, base64url encoded (state, nonce, verifier).
func RandomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("sso: random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// PKCEChallenge is the S256 transform of a code verifier (RFC 7636 4.2).
func PKCEChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// AuthRequest carries everything needed to build the authorization URL.
type AuthRequest struct {
	ClientID    string
	RedirectURI string
	Scopes      string
	State       string
	Nonce       string
	Verifier    string
}

// AuthURL builds the authorization endpoint URL for the code flow with PKCE S256.
func (md *Metadata) AuthURL(r AuthRequest) (string, error) {
	u, err := url.Parse(md.AuthorizationEndpoint)
	if err != nil {
		return "", ErrInvalidURL
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", r.ClientID)
	q.Set("redirect_uri", r.RedirectURI)
	q.Set("scope", r.Scopes)
	q.Set("state", r.State)
	q.Set("nonce", r.Nonce)
	q.Set("code_challenge", PKCEChallenge(r.Verifier))
	q.Set("code_challenge_method", "S256")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// ExchangeRequest is a token endpoint call for the authorization code grant.
type ExchangeRequest struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	Code         string
	Verifier     string
}

type tokenResponse struct {
	IDToken string `json:"id_token"`
	Error   string `json:"error"`
}

// Exchange redeems the authorization code and returns the raw ID token. The
// client secret is sent with HTTP Basic unless the provider only advertises
// client_secret_post.
func (c *Client) Exchange(ctx context.Context, md *Metadata, r ExchangeRequest) (string, error) {
	u, err := ValidateURL(md.TokenEndpoint, c.allowInsecure)
	if err != nil {
		return "", err
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", r.Code)
	form.Set("redirect_uri", r.RedirectURI)
	form.Set("code_verifier", r.Verifier)

	post := len(md.TokenAuthMethods) > 0 && !contains(md.TokenAuthMethods, "client_secret_basic") &&
		contains(md.TokenAuthMethods, "client_secret_post")
	if post {
		form.Set("client_id", r.ClientID)
		form.Set("client_secret", r.ClientSecret)
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return "", ErrInvalidURL
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if !post {
		// RFC 6749 2.3.1: credentials are form-urlencoded before Basic encoding.
		req.SetBasicAuth(url.QueryEscape(r.ClientID), url.QueryEscape(r.ClientSecret))
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("sso: token request failed: %w", unwrapURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()

	var tr tokenResponse
	if err := decodeCapped(resp.Body, &tr); err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK || tr.IDToken == "" {
		// The provider's error text is not forwarded: it is outside our control.
		return "", errors.New("sso: token endpoint rejected the request")
	}
	return tr.IDToken, nil
}

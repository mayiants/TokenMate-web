// Package ssotest provides an in-process OpenID provider for tests: discovery,
// JWKS, authorization (PKCE aware) and token endpoints on an httptest TLS
// server, plus knobs to produce deliberately broken ID tokens.
//
// The server certificate is valid for "example.com", so the provider advertises
// https://example.com:<port> as its issuer. That host name passes the SSO URL
// policy (a literal 127.0.0.1 would be refused by design) and HTTPClient routes
// every connection to the local listener.
package ssotest

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"
)

// User is the identity the provider asserts for one authorization.
type User struct {
	Sub           string
	Email         string
	EmailVerified interface{} // bool, or a string such as "true"
	Name          string
}

type grant struct {
	challenge   string
	nonce       string
	redirectURI string
	user        User
	used        bool
}

// Provider is a running mock identity provider.
type Provider struct {
	Server       *httptest.Server
	Issuer       string
	ClientID     string
	ClientSecret string
	Kid          string
	Key          *rsa.PrivateKey

	// Mutate may edit the claims just before signing.
	Mutate func(claims jwtlib.MapClaims)
	// Sign overrides token creation entirely (alg none / HS256 confusion ...).
	Sign func(p *Provider, claims jwtlib.MapClaims) string
	// Now lets tests shift the clock the provider stamps tokens with.
	Now func() time.Time

	mu     sync.Mutex
	grants map[string]*grant
	// TokenCalls counts hits on the token endpoint.
	TokenCalls int
	// JWKSCalls counts hits on the JWKS endpoint.
	JWKSCalls int
	// ExtraKeys are published in the JWKS in addition to Key (rotation tests).
	ExtraKeys map[string]*rsa.PublicKey
	// HideKey removes Key from the JWKS (simulates a rotated-away key).
	HideKey bool
}

// New starts a provider and registers cleanup with t.
func New(t testing.TB) *Provider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	p := &Provider{
		ClientID:     "portal-client",
		ClientSecret: "s3cr3t-client-secret",
		Kid:          "kid-1",
		Key:          key,
		Now:          time.Now,
		grants:       map[string]*grant{},
		ExtraKeys:    map[string]*rsa.PublicKey{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", p.discovery)
	mux.HandleFunc("/jwks", p.jwks)
	mux.HandleFunc("/token", p.token)
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	p.Server = srv
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	p.Issuer = "https://example.com:" + port
	return p
}

// HTTPClient returns a client that trusts the server certificate and sends every
// connection to the local listener regardless of the requested host.
func (p *Provider) HTTPClient() *http.Client {
	base := p.Server.Client().Transport.(*http.Transport).Clone()
	addr := p.Server.Listener.Addr().String()
	base.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}
	if base.TLSClientConfig == nil {
		base.TLSClientConfig = &tls.Config{}
	}
	base.TLSClientConfig.ServerName = "example.com"
	return &http.Client{Transport: base, Timeout: 5 * time.Second}
}

func (p *Provider) discovery(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]interface{}{
		"issuer":                                p.Issuer,
		"authorization_endpoint":                p.Issuer + "/authorize",
		"token_endpoint":                        p.Issuer + "/token",
		"jwks_uri":                              p.Issuer + "/jwks",
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
	})
}

func (p *Provider) jwks(w http.ResponseWriter, _ *http.Request) {
	p.mu.Lock()
	p.JWKSCalls++
	p.mu.Unlock()
	keys := []map[string]string{}
	if !p.HideKey {
		keys = append(keys, rsaJWK(p.Kid, &p.Key.PublicKey))
	}
	for kid, k := range p.ExtraKeys {
		keys = append(keys, rsaJWK(kid, k))
	}
	writeJSON(w, map[string]interface{}{"keys": keys})
}

func rsaJWK(kid string, k *rsa.PublicKey) map[string]string {
	return map[string]string{
		"kty": "RSA", "use": "sig", "alg": "RS256", "kid": kid,
		"n": base64.RawURLEncoding.EncodeToString(k.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.E)).Bytes()),
	}
}

// Authorize plays the user's visit to the authorization endpoint: it validates
// the request that the portal built and returns the callback URL the browser
// would be redirected to.
func (p *Provider) Authorize(authURL string, u User) (callbackURL string, err error) {
	parsed, err := url.Parse(authURL)
	if err != nil {
		return "", err
	}
	q := parsed.Query()
	if q.Get("response_type") != "code" || q.Get("client_id") != p.ClientID ||
		q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" ||
		q.Get("state") == "" || q.Get("nonce") == "" || q.Get("redirect_uri") == "" {
		return "", fmt.Errorf("provider rejected authorization request: %v", q)
	}
	code, _ := randomString()
	p.mu.Lock()
	p.grants[code] = &grant{challenge: q.Get("code_challenge"), nonce: q.Get("nonce"), redirectURI: q.Get("redirect_uri"), user: u}
	p.mu.Unlock()
	cb, _ := url.Parse(q.Get("redirect_uri"))
	cq := cb.Query()
	cq.Set("code", code)
	cq.Set("state", q.Get("state"))
	cb.RawQuery = cq.Encode()
	return cb.String(), nil
}

// ChangeChallenge swaps the PKCE challenge recorded for every open grant, so
// the verifier the portal later sends no longer matches (PKCE mismatch test).
func (p *Provider) ChangeChallenge() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, g := range p.grants {
		g.challenge = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	}
}

func (p *Provider) token(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.TokenCalls++
	p.mu.Unlock()
	if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "authorization_code" {
		tokenError(w, "invalid_request")
		return
	}
	id, secret, basic := r.BasicAuth()
	if basic {
		id, _ = url.QueryUnescape(id)
		secret, _ = url.QueryUnescape(secret)
	} else {
		id, secret = r.Form.Get("client_id"), r.Form.Get("client_secret")
	}
	if id != p.ClientID || secret != p.ClientSecret {
		tokenError(w, "invalid_client")
		return
	}
	p.mu.Lock()
	g := p.grants[r.Form.Get("code")]
	if g == nil || g.used {
		p.mu.Unlock()
		tokenError(w, "invalid_grant")
		return
	}
	g.used = true // codes are single-use
	p.mu.Unlock()
	sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge || r.Form.Get("redirect_uri") != g.redirectURI {
		tokenError(w, "invalid_grant")
		return
	}
	now := p.Now()
	claims := jwtlib.MapClaims{
		"iss": p.Issuer, "sub": g.user.Sub, "aud": p.ClientID,
		"iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(),
		"nonce": g.nonce, "email": g.user.Email, "email_verified": g.user.EmailVerified,
	}
	if g.user.Name != "" {
		claims["name"] = g.user.Name
	}
	if p.Mutate != nil {
		p.Mutate(claims)
	}
	var tok string
	if p.Sign != nil {
		tok = p.Sign(p, claims)
	} else {
		tok = p.SignRS256(claims)
	}
	writeJSON(w, map[string]interface{}{"access_token": "at", "token_type": "Bearer", "id_token": tok})
}

// SignRS256 signs claims with the provider key.
func (p *Provider) SignRS256(claims jwtlib.MapClaims) string {
	t := jwtlib.NewWithClaims(jwtlib.SigningMethodRS256, claims)
	t.Header["kid"] = p.Kid
	s, err := t.SignedString(p.Key)
	if err != nil {
		panic(err)
	}
	return s
}

// SignWithKey signs with an arbitrary key (an attacker's key, same kid).
func (p *Provider) SignWithKey(claims jwtlib.MapClaims, key *rsa.PrivateKey) string {
	t := jwtlib.NewWithClaims(jwtlib.SigningMethodRS256, claims)
	t.Header["kid"] = p.Kid
	s, err := t.SignedString(key)
	if err != nil {
		panic(err)
	}
	return s
}

// SignNone builds an unsigned token ("alg":"none").
func (p *Provider) SignNone(claims jwtlib.MapClaims) string {
	t := jwtlib.NewWithClaims(jwtlib.SigningMethodNone, claims)
	t.Header["kid"] = p.Kid
	s, err := t.SignedString(jwtlib.UnsafeAllowNoneSignatureType)
	if err != nil {
		panic(err)
	}
	return s
}

// SignHS256WithPublicKey is the key-confusion attack: an HMAC token whose
// secret is the provider's public key material.
func (p *Provider) SignHS256WithPublicKey(claims jwtlib.MapClaims) string {
	t := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, claims)
	t.Header["kid"] = p.Kid
	secret := base64.RawURLEncoding.EncodeToString(p.Key.N.Bytes())
	s, err := t.SignedString([]byte(secret))
	if err != nil {
		panic(err)
	}
	return s
}

func tokenError(w http.ResponseWriter, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func randomString() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

package sso

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Metadata is the subset of the OpenID Provider Configuration we rely on.
type Metadata struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	JWKSURI               string   `json:"jwks_uri"`
	CodeChallengeMethods  []string `json:"code_challenge_methods_supported"`
	TokenAuthMethods      []string `json:"token_endpoint_auth_methods_supported"`
	IDTokenSigningAlgs    []string `json:"id_token_signing_alg_values_supported"`
}

// Options configures a Client.
type Options struct {
	// AllowInsecureHTTP permits http:// provider URLs (SSO_ALLOW_INSECURE_HTTP).
	AllowInsecureHTTP bool
	// HTTPClient replaces the guarded client. TEST ONLY: it disables the
	// address filtering, so production code must leave it nil.
	HTTPClient *http.Client
	// Now overrides the clock (tests).
	Now func() time.Time

	dialFilter dialFilter
}

// Client talks to OpenID providers. It is safe for concurrent use.
type Client struct {
	http          *http.Client
	allowInsecure bool
	now           func() time.Time

	mu       sync.Mutex
	metadata map[string]cachedMetadata
	keys     map[string]*keySet
}

type cachedMetadata struct {
	md      *Metadata
	expires time.Time
}

const metadataTTL = 10 * time.Minute

// NewClient creates a Client with the guarded HTTP client.
func NewClient(opts Options) *Client {
	c := &Client{
		allowInsecure: opts.AllowInsecureHTTP || AllowInsecureHTTPFromEnv(),
		now:           opts.Now,
		metadata:      map[string]cachedMetadata{},
		keys:          map[string]*keySet{},
	}
	if c.now == nil {
		c.now = time.Now
	}
	c.http = opts.HTTPClient
	if c.http == nil {
		c.http = newGuardedClient(c.allowInsecure, opts.dialFilter)
	}
	return c
}

// AllowInsecure reports whether http:// URLs are accepted.
func (c *Client) AllowInsecure() bool { return c.allowInsecure }

// getJSON fetches rawURL and decodes a size-capped JSON body into out.
func (c *Client) getJSON(ctx context.Context, rawURL string, out interface{}) error {
	u, err := ValidateURL(rawURL, c.allowInsecure)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return ErrInvalidURL
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("sso: request failed: %w", unwrapURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sso: unexpected status %d", resp.StatusCode)
	}
	return decodeCapped(resp.Body, out)
}

// unwrapURLError drops the request URL from net/http errors; callers log the
// result and the URL is admin-supplied.
func unwrapURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func decodeCapped(r io.Reader, out interface{}) error {
	raw, err := io.ReadAll(io.LimitReader(r, maxBodyBytes+1))
	if err != nil {
		return fmt.Errorf("sso: read body: %w", err)
	}
	if len(raw) > maxBodyBytes {
		return ErrTooLarge
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("sso: decode body: %w", err)
	}
	return nil
}

// Discover fetches <issuer>/.well-known/openid-configuration and checks that
// the document really belongs to issuer (OIDC Discovery 4.3: the issuer value
// must match exactly), that the endpoints satisfy the same URL policy, and that
// the provider can do PKCE S256 when it advertises its methods.
func (c *Client) Discover(ctx context.Context, issuer string) (*Metadata, error) {
	issuer = strings.TrimSpace(issuer)
	if _, err := ValidateURL(issuer, c.allowInsecure); err != nil {
		return nil, err
	}
	c.mu.Lock()
	if m, ok := c.metadata[issuer]; ok && c.now().Before(m.expires) {
		c.mu.Unlock()
		return m.md, nil
	}
	c.mu.Unlock()

	var md Metadata
	if err := c.getJSON(ctx, strings.TrimRight(issuer, "/")+"/.well-known/openid-configuration", &md); err != nil {
		return nil, err
	}
	if md.Issuer != issuer {
		return nil, errors.New("sso: discovery issuer mismatch")
	}
	for _, ep := range []string{md.AuthorizationEndpoint, md.TokenEndpoint, md.JWKSURI} {
		if _, err := ValidateURL(ep, c.allowInsecure); err != nil {
			return nil, fmt.Errorf("sso: discovery endpoint rejected: %w", err)
		}
	}
	if len(md.CodeChallengeMethods) > 0 && !contains(md.CodeChallengeMethods, "S256") {
		return nil, errors.New("sso: provider does not support PKCE S256")
	}
	c.mu.Lock()
	c.metadata[issuer] = cachedMetadata{md: &md, expires: c.now().Add(metadataTTL)}
	c.mu.Unlock()
	return &md, nil
}

// Forget drops cached metadata and keys for an issuer, used after an admin
// changes the configuration so stale endpoints are not reused.
func (c *Client) Forget(issuer string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if m, ok := c.metadata[issuer]; ok {
		delete(c.keys, m.md.JWKSURI)
	}
	delete(c.metadata, issuer)
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

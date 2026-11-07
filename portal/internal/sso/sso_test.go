package sso_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mayiants/ai-portal-go/internal/sso"
	"github.com/mayiants/ai-portal-go/internal/sso/ssotest"
	"github.com/alicebob/miniredis/v2"
	jwtlib "github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const nonce = "n-0123456789"

type fixture struct {
	p      *ssotest.Provider
	c      *sso.Client
	md     *sso.Metadata
	params sso.VerifyParams
	now    time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	p := ssotest.New(t)
	f := &fixture{p: p, now: time.Now()}
	f.c = sso.NewClient(sso.Options{HTTPClient: p.HTTPClient(), Now: func() time.Time { return f.now }})
	p.Now = func() time.Time { return f.now }
	md, err := f.c.Discover(context.Background(), p.Issuer)
	require.NoError(t, err)
	f.md = md
	f.params = sso.VerifyParams{Issuer: p.Issuer, ClientID: p.ClientID, Nonce: nonce}
	return f
}

func (f *fixture) claims() jwtlib.MapClaims {
	return jwtlib.MapClaims{
		"iss": f.p.Issuer, "sub": "user-1", "aud": f.p.ClientID, "nonce": nonce,
		"iat": f.now.Unix(), "exp": f.now.Add(5 * time.Minute).Unix(),
		"email": "Alice@Corp.Example", "email_verified": true, "name": "Alice",
	}
}

func (f *fixture) verify(raw string) (*sso.Claims, error) {
	return f.c.VerifyIDToken(context.Background(), f.md, raw, f.params)
}

func TestDiscover_Policy(t *testing.T) {
	p := ssotest.New(t)
	// http issuer without the opt-in is refused before any request.
	c := sso.NewClient(sso.Options{HTTPClient: p.HTTPClient()})
	_, err := c.Discover(context.Background(), strings.Replace(p.Issuer, "https://", "http://", 1))
	assert.ErrorIs(t, err, sso.ErrInsecureURL)
	// Private literal issuers are refused.
	for _, iss := range []string{"https://127.0.0.1", "https://169.254.169.254", "https://10.0.0.1:8443", "https://localhost"} {
		_, err := c.Discover(context.Background(), iss)
		assert.ErrorIs(t, err, sso.ErrBlockedAddress, iss)
	}
	md, err := c.Discover(context.Background(), p.Issuer)
	require.NoError(t, err)
	assert.Equal(t, p.Issuer+"/token", md.TokenEndpoint)
}

func TestVerify_HappyPath(t *testing.T) {
	f := newFixture(t)
	cl, err := f.verify(f.p.SignRS256(f.claims()))
	require.NoError(t, err)
	assert.Equal(t, "user-1", cl.Subject)
	assert.Equal(t, "alice@corp.example", cl.Email, "e-mail is normalised to lower case")
	assert.True(t, cl.EmailVerified)
}

func TestVerify_Rejections(t *testing.T) {
	f := newFixture(t)
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	mut := func(fn func(c jwtlib.MapClaims)) string {
		c := f.claims()
		fn(c)
		return f.p.SignRS256(c)
	}
	cases := map[string]string{
		"wrong nonce":         mut(func(c jwtlib.MapClaims) { c["nonce"] = "other" }),
		"missing nonce":       mut(func(c jwtlib.MapClaims) { delete(c, "nonce") }),
		"wrong audience":      mut(func(c jwtlib.MapClaims) { c["aud"] = "someone-else" }),
		"audience list":       mut(func(c jwtlib.MapClaims) { c["aud"] = []string{"a", f.p.ClientID} }),
		"wrong issuer":        mut(func(c jwtlib.MapClaims) { c["iss"] = "https://evil.example" }),
		"expired":             mut(func(c jwtlib.MapClaims) { c["exp"] = f.now.Add(-3 * time.Minute).Unix() }),
		"no exp":              mut(func(c jwtlib.MapClaims) { delete(c, "exp") }),
		"nbf in future":       mut(func(c jwtlib.MapClaims) { c["nbf"] = f.now.Add(10 * time.Minute).Unix() }),
		"iat in future":       mut(func(c jwtlib.MapClaims) { c["iat"] = f.now.Add(10 * time.Minute).Unix() }),
		"azp mismatch":        mut(func(c jwtlib.MapClaims) { c["azp"] = "other-client" }),
		"unverified email":    mut(func(c jwtlib.MapClaims) { c["email_verified"] = false }),
		"email_verified str":  mut(func(c jwtlib.MapClaims) { c["email_verified"] = "false" }),
		"email_verified gone": mut(func(c jwtlib.MapClaims) { delete(c, "email_verified") }),
		"no email":            mut(func(c jwtlib.MapClaims) { delete(c, "email") }),
		"no subject":          mut(func(c jwtlib.MapClaims) { delete(c, "sub") }),
		"alg none":            f.p.SignNone(f.claims()),
		"hs256 confusion":     f.p.SignHS256WithPublicKey(f.claims()),
		"foreign signature":   f.p.SignWithKey(f.claims(), other),
		"garbage":             "not.a.jwt",
		"empty":               "",
	}
	for name, tok := range cases {
		_, err := f.verify(tok)
		require.Error(t, err, name)
		assert.ErrorIs(t, err, sso.ErrInvalidToken, name)
	}
}

func TestVerify_AcceptedEdges(t *testing.T) {
	f := newFixture(t)
	// Inside the two minute skew: slightly expired, slightly future nbf/iat.
	c := f.claims()
	c["exp"] = f.now.Add(-90 * time.Second).Unix()
	c["iat"] = f.now.Add(90 * time.Second).Unix()
	c["nbf"] = f.now.Add(90 * time.Second).Unix()
	_, err := f.verify(f.p.SignRS256(c))
	assert.NoError(t, err)

	// "true" as a string is accepted (some providers emit it), azp matching is fine.
	c = f.claims()
	c["email_verified"] = "true"
	c["aud"] = []string{f.p.ClientID, "other"}
	c["azp"] = f.p.ClientID
	_, err = f.verify(f.p.SignRS256(c))
	assert.NoError(t, err)

	// Multi-valued aud without azp is refused.
	c = f.claims()
	c["aud"] = []string{f.p.ClientID, "other"}
	_, err = f.verify(f.p.SignRS256(c))
	assert.Error(t, err)
}

func TestJWKS_RefreshOnUnknownKidIsRateLimited(t *testing.T) {
	f := newFixture(t)
	_, err := f.verify(f.p.SignRS256(f.claims()))
	require.NoError(t, err)
	calls := f.p.JWKSCalls

	// Rotation: provider moves to a new kid. The first unknown kid triggers a
	// refresh once the refresh gap has elapsed.
	newKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	f.p.ExtraKeys["kid-2"] = &newKey.PublicKey
	rotated := jwtlib.NewWithClaims(jwtlib.SigningMethodRS256, f.claims())
	rotated.Header["kid"] = "kid-2"
	tok, err := rotated.SignedString(newKey)
	require.NoError(t, err)

	// Within the refresh gap the unknown kid does not hit the network.
	_, err = f.verify(tok)
	require.Error(t, err)
	assert.Equal(t, calls, f.p.JWKSCalls)

	f.now = f.now.Add(time.Minute)
	c := f.claims()
	rot := jwtlib.NewWithClaims(jwtlib.SigningMethodRS256, c)
	rot.Header["kid"] = "kid-2"
	tok, err = rot.SignedString(newKey)
	require.NoError(t, err)
	_, err = f.verify(tok)
	require.NoError(t, err)
	assert.Equal(t, calls+1, f.p.JWKSCalls)

	// A random kid after the gap costs exactly one more fetch, then is refused.
	f.now = f.now.Add(time.Minute)
	bogus := jwtlib.NewWithClaims(jwtlib.SigningMethodRS256, f.claims())
	bogus.Header["kid"] = "nope"
	tok, err = bogus.SignedString(newKey)
	require.NoError(t, err)
	_, err = f.verify(tok)
	require.Error(t, err)
	assert.Equal(t, calls+2, f.p.JWKSCalls)
}

func TestAuthURLAndPKCE(t *testing.T) {
	f := newFixture(t)
	verifier, err := sso.RandomToken(32)
	require.NoError(t, err)
	raw, err := f.md.AuthURL(sso.AuthRequest{ClientID: "cid", RedirectURI: "https://portal.test/auth/sso/callback",
		Scopes: "openid email", State: "st", Nonce: "no", Verifier: verifier})
	require.NoError(t, err)
	u, _ := url.Parse(raw)
	q := u.Query()
	assert.Equal(t, "code", q.Get("response_type"))
	assert.Equal(t, "S256", q.Get("code_challenge_method"))
	assert.Equal(t, sso.PKCEChallenge(verifier), q.Get("code_challenge"))
	assert.NotContains(t, raw, verifier, "the verifier never travels through the browser")
	// RFC 7636 appendix B vector.
	assert.Equal(t, "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM",
		sso.PKCEChallenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"))
}

func TestStateStore_SingleUseAndBinding(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = rdb.Close() }()
	s := sso.NewStateStore(rdb, "server-secret")
	ctx := context.Background()
	pend := sso.Pending{EnterpriseID: "e1", Nonce: "n", Verifier: "v"}

	state, cookie, err := s.Begin(ctx, pend)
	require.NoError(t, err)
	got, err := s.Consume(ctx, state, cookie)
	require.NoError(t, err)
	assert.Equal(t, pend, *got)
	// Replay: the record is gone.
	_, err = s.Consume(ctx, state, cookie)
	assert.ErrorIs(t, err, sso.ErrStateInvalid)

	// Missing / foreign / forged cookie: rejected, and the state is burnt.
	for name, ck := range map[string]string{"none": "", "forged mac": state + ".deadbeef", "other state": "zzz." + strings.SplitN(cookie, ".", 2)[1]} {
		st, _, err := s.Begin(ctx, pend)
		require.NoError(t, err)
		_, err = s.Consume(ctx, st, ck)
		assert.ErrorIs(t, err, sso.ErrStateInvalid, name)
		_, _, _ = st, ck, name
	}
	// A cookie minted with a different server secret does not verify.
	other := sso.NewStateStore(rdb, "other-secret")
	st, ck, _ := other.Begin(ctx, pend)
	_, err = s.Consume(ctx, st, ck)
	assert.ErrorIs(t, err, sso.ErrStateInvalid)

	// Expiry after the 10 minute TTL.
	st, ck, _ = s.Begin(ctx, pend)
	mr.FastForward(sso.StateTTL + time.Second)
	_, err = s.Consume(ctx, st, ck)
	assert.ErrorIs(t, err, sso.ErrStateInvalid)

	// Redis down fails closed.
	mr.Close()
	_, err = s.Consume(ctx, st, ck)
	assert.Error(t, err)
}

func TestExchange_UsesClientAuthAndVerifier(t *testing.T) {
	f := newFixture(t)
	verifier, _ := sso.RandomToken(32)
	authURL, err := f.md.AuthURL(sso.AuthRequest{ClientID: f.p.ClientID, RedirectURI: "https://portal.test/cb",
		Scopes: "openid", State: "s", Nonce: nonce, Verifier: verifier})
	require.NoError(t, err)
	cb, err := f.p.Authorize(authURL, ssotest.User{Sub: "u", Email: "a@b.c", EmailVerified: true})
	require.NoError(t, err)
	code := mustQuery(t, cb, "code")

	// Wrong secret.
	_, err = f.c.Exchange(context.Background(), f.md, sso.ExchangeRequest{ClientID: f.p.ClientID, ClientSecret: "wrong",
		RedirectURI: "https://portal.test/cb", Code: code, Verifier: verifier})
	assert.Error(t, err)
	// Wrong verifier (PKCE mismatch).
	_, err = f.c.Exchange(context.Background(), f.md, sso.ExchangeRequest{ClientID: f.p.ClientID, ClientSecret: f.p.ClientSecret,
		RedirectURI: "https://portal.test/cb", Code: code, Verifier: "wrong-verifier-wrong-verifier-wrong-verifier"})
	assert.Error(t, err)
	// Codes are single use, so mint another for the success case.
	cb, _ = f.p.Authorize(authURL, ssotest.User{Sub: "u", Email: "a@b.c", EmailVerified: true})
	code = mustQuery(t, cb, "code")
	tok, err := f.c.Exchange(context.Background(), f.md, sso.ExchangeRequest{ClientID: f.p.ClientID, ClientSecret: f.p.ClientSecret,
		RedirectURI: "https://portal.test/cb", Code: code, Verifier: verifier})
	require.NoError(t, err)
	assert.NotEmpty(t, tok)
	_ = http.StatusOK
}

func mustQuery(t *testing.T, raw, key string) string {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u.Query().Get(key)
}

package sso

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// StateTTL is how long a login attempt may take at the identity provider.
const StateTTL = 10 * time.Minute

// CookieName is the browser binding cookie.
const CookieName = "sso_login"

// ErrStateInvalid covers unknown, expired, replayed and mis-bound states. It is
// one error on purpose so a probing client cannot tell which condition hit.
var ErrStateInvalid = errors.New("sso: invalid or expired login state")

// ErrStateUnavailable means Redis cannot be used; SSO fails closed.
var ErrStateUnavailable = errors.New("sso: state store unavailable")

// Pending is the server-side record of one in-flight login.
type Pending struct {
	EnterpriseID string `json:"e"`
	Nonce        string `json:"n"`
	Verifier     string `json:"v"`
}

// StateStore keeps state, nonce and PKCE verifier in Redis and binds each login
// to the initiating browser with a signed cookie.
type StateStore struct {
	rdb    *redis.Client
	macKey []byte
}

// NewStateStore derives the cookie MAC key from a server secret (never sent to
// the client) so cookies cannot be forged without it.
func NewStateStore(rdb *redis.Client, secret string) *StateStore {
	sum := sha256.Sum256([]byte("sso-login-cookie|" + secret))
	return &StateStore{rdb: rdb, macKey: sum[:]}
}

func stateKey(state string) string {
	sum := sha256.Sum256([]byte(state))
	return "sso:state:" + hex.EncodeToString(sum[:])
}

func (s *StateStore) mac(state string) string {
	h := hmac.New(sha256.New, s.macKey)
	h.Write([]byte(state))
	return hex.EncodeToString(h.Sum(nil))
}

// Begin stores a new login attempt and returns the state parameter plus the
// value for the binding cookie ("<state>.<hmac>").
func (s *StateStore) Begin(ctx context.Context, p Pending) (state, cookie string, err error) {
	if s == nil || s.rdb == nil {
		return "", "", ErrStateUnavailable
	}
	if state, err = RandomToken(32); err != nil {
		return "", "", err
	}
	raw, _ := json.Marshal(p)
	if err := s.rdb.Set(ctx, stateKey(state), raw, StateTTL).Err(); err != nil {
		return "", "", ErrStateUnavailable
	}
	return state, state + "." + s.mac(state), nil
}

// Consume validates the browser cookie against the state from the callback URL
// and atomically deletes the record (GETDEL), so a state works exactly once even
// under concurrent requests, and an attacker-supplied state without the victim's
// cookie is rejected (login CSRF). The record is destroyed even when the cookie
// check fails: a half-valid replay must not leave a usable state behind.
func (s *StateStore) Consume(ctx context.Context, state, cookie string) (*Pending, error) {
	if s == nil || s.rdb == nil {
		return nil, ErrStateUnavailable
	}
	if state == "" || len(state) > 128 {
		return nil, ErrStateInvalid
	}
	raw, err := s.rdb.GetDel(ctx, stateKey(state)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrStateInvalid
	}
	if err != nil {
		return nil, ErrStateUnavailable
	}
	parts := strings.SplitN(cookie, ".", 2)
	if len(parts) != 2 || subtle.ConstantTimeCompare([]byte(parts[0]), []byte(state)) != 1 ||
		subtle.ConstantTimeCompare([]byte(parts[1]), []byte(s.mac(state))) != 1 {
		return nil, ErrStateInvalid
	}
	var p Pending
	if json.Unmarshal(raw, &p) != nil || p.EnterpriseID == "" || p.Nonce == "" || p.Verifier == "" {
		return nil, ErrStateInvalid
	}
	return &p, nil
}

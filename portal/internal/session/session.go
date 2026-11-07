// Package session is the single place where an authenticated Portal session
// (JWT and, for browser flows, the cookie) is created.
//
// Every login path - password (web and API), registration auto-login, the MFA
// challenge completion and the SSO stage - must call Issuer.Issue. Issue
// re-checks the user in the database and refuses to hand out a session after a
// bare password check when the user has TOTP enabled (ErrMFARequired), so a new
// login path that forgets the second factor fails closed instead of silently
// bypassing MFA. A source-level test (see internal/routes/login_paths_test.go)
// additionally fails if anything outside this package mints JWTs directly.
package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/pkg/audit"
	"github.com/mayiants/ai-portal-go/pkg/jwt"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// Method describes how the caller authenticated the user.
type Method string

const (
	// MethodPassword: only a password (or an equivalent single factor such as a
	// fresh registration) was verified. Issue refuses it for MFA users.
	MethodPassword Method = "password"
	// MethodMFA: a TOTP/recovery code was verified by routes.MFAService in this
	// request. Only the MFA challenge completion and the MFA management
	// endpoints may use it.
	MethodMFA Method = "mfa"
	// MethodSSO: an external IdP authenticated the user, so the local second
	// factor is intentionally skipped. Reserved for the SSO login stage.
	MethodSSO Method = "sso"
)

var (
	// ErrMFARequired is returned by Issue when a password-only login is attempted
	// for a user who has TOTP enabled. Callers turn it into an MFA challenge.
	ErrMFARequired = errors.New("session: second factor required")
	// ErrUserUnavailable covers missing, deleted, suspended and erasure-pending users.
	ErrUserUnavailable = errors.New("session: user unavailable")
	// ErrChallengeInvalid is returned for unknown, expired, used or mis-bound
	// pending tokens. It is intentionally one error so callers cannot leak which.
	ErrChallengeInvalid = errors.New("session: invalid or expired MFA challenge")
	// ErrChallengeUnavailable means the challenge store (Redis) cannot be used.
	// MFA users cannot log in then: failing closed is the only safe option.
	ErrChallengeUnavailable = errors.New("session: MFA challenge store unavailable")
)

// ChallengeTTL bounds how long a user has to enter the second factor.
const ChallengeTTL = 5 * time.Minute

// CookieWriter writes the browser session cookie. It is injected by the web
// layer (which owns cookie domain/secure logic) so this package stays free of
// an import cycle.
type CookieWriter func(c *gin.Context, token string, remember bool)

// Options tune a single Issue call.
type Options struct {
	// Remember asks for the long-lived (30 day) cookie. It only affects the
	// cookie; the JWT lifetime is unchanged. Because it is applied here, i.e.
	// after the second factor, a remember-me cookie can never exist for an MFA
	// user without a successful MFA step.
	Remember bool
	// SetCookie also writes the browser cookie. API clients read the token from
	// the JSON body instead.
	SetCookie bool
}

// Result is a freshly issued session.
type Result struct {
	Token        string
	UserID       string
	Name         string
	Email        string
	Role         string
	TokenVersion int
}

// Issuer creates sessions and MFA challenges.
type Issuer struct {
	db     *db.DB
	jwt    *jwt.Manager
	cookie CookieWriter
}

// NewIssuer creates an Issuer. mgr may be nil only in tests that never issue.
func NewIssuer(database *db.DB, mgr *jwt.Manager) *Issuer {
	return &Issuer{db: database, jwt: mgr}
}

// SetCookieWriter installs the browser cookie writer (web layer).
func (i *Issuer) SetCookieWriter(w CookieWriter) { i.cookie = w }

// Issue is the only function that mints a Portal session.
//
// The user's current role and token_version are read from the database rather
// than trusted from the caller, so a stale in-memory snapshot cannot produce a
// token that is older than a concurrent revocation.
func (i *Issuer) Issue(c *gin.Context, userID string, method Method, opts Options) (*Result, error) {
	if i == nil || i.db == nil || i.jwt == nil {
		return nil, errors.New("session: issuer not configured")
	}
	switch method {
	case MethodPassword, MethodMFA, MethodSSO:
	default:
		return nil, fmt.Errorf("session: unknown method %q", method)
	}

	var r Result
	var status string
	var totpEnabled, erasure bool
	err := i.db.QueryRow(c.Request.Context(),
		`SELECT id::text, COALESCE(name, ''), email, COALESCE(role, 'user'), COALESCE(token_version, 0),
		        COALESCE(status, 'active'), COALESCE(totp_enabled, false), gdpr_erasure_requested_at IS NOT NULL
		   FROM users WHERE id = $1::uuid AND deleted_at IS NULL`, userID,
	).Scan(&r.UserID, &r.Name, &r.Email, &r.Role, &r.TokenVersion, &status, &totpEnabled, &erasure)
	if err != nil || status == "suspended" || erasure {
		return nil, ErrUserUnavailable
	}
	// The choke point: a password alone never opens a session for an MFA user.
	if totpEnabled && method == MethodPassword {
		return nil, ErrMFARequired
	}

	token, err := i.jwt.Generate(r.UserID, r.Email, r.Role, r.TokenVersion)
	if err != nil {
		return nil, fmt.Errorf("session: sign token: %w", err)
	}
	r.Token = token
	if opts.SetCookie && i.cookie != nil {
		i.cookie(c, token, opts.Remember)
	}
	return &r, nil
}

// Challenge is the server-side record behind a pending MFA token.
type Challenge struct {
	UserID   string `json:"u"`
	IPHash   string `json:"i"`
	Remember bool   `json:"r"`
}

func challengeKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "mfa:pending:" + hex.EncodeToString(sum[:])
}

// HashIP binds a challenge to the client address without storing the address.
func HashIP(ip string) string {
	sum := sha256.Sum256([]byte("mfa-ip:" + audit.NormalizeIP(ip)))
	return hex.EncodeToString(sum[:])
}

// BeginChallenge stores a 5 minute, single-purpose pending token for userID and
// returns it. The token is 256 bits of randomness; only its hash is stored.
func (i *Issuer) BeginChallenge(ctx context.Context, userID, clientIP string, remember bool) (string, error) {
	if i == nil || i.db == nil || i.db.Redis == nil {
		return "", ErrChallengeUnavailable
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("session: random: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	val, _ := json.Marshal(Challenge{UserID: userID, IPHash: HashIP(clientIP), Remember: remember})
	if err := i.db.Redis.Set(ctx, challengeKey(token), val, ChallengeTTL).Err(); err != nil {
		return "", ErrChallengeUnavailable
	}
	return token, nil
}

// PeekChallenge returns the challenge behind token without consuming it, so a
// mistyped code does not force the user to re-enter the password (the per-user
// failure limiter bounds guessing instead). A token presented from a different
// client address is destroyed: it was either stolen or the user moved networks,
// and in both cases a fresh password login is the right answer.
func (i *Issuer) PeekChallenge(ctx context.Context, token, clientIP string) (*Challenge, error) {
	if i == nil || i.db == nil || i.db.Redis == nil {
		return nil, ErrChallengeUnavailable
	}
	if token == "" || len(token) > 128 {
		return nil, ErrChallengeInvalid
	}
	raw, err := i.db.Redis.Get(ctx, challengeKey(token)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrChallengeInvalid
	}
	if err != nil {
		return nil, ErrChallengeUnavailable
	}
	var ch Challenge
	if json.Unmarshal(raw, &ch) != nil || ch.UserID == "" {
		return nil, ErrChallengeInvalid
	}
	if subtle.ConstantTimeCompare([]byte(ch.IPHash), []byte(HashIP(clientIP))) != 1 {
		_ = i.db.Redis.Del(ctx, challengeKey(token)).Err()
		return nil, ErrChallengeInvalid
	}
	return &ch, nil
}

// ConsumeChallenge atomically deletes the token. Exactly one caller observes
// true, which is what makes the token single-use even under concurrent requests.
func (i *Issuer) ConsumeChallenge(ctx context.Context, token string) (bool, error) {
	if i == nil || i.db == nil || i.db.Redis == nil {
		return false, ErrChallengeUnavailable
	}
	n, err := i.db.Redis.Del(ctx, challengeKey(token)).Result()
	if err != nil {
		return false, ErrChallengeUnavailable
	}
	return n == 1, nil
}

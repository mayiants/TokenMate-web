// Package totp implements RFC 6238 time-based one-time passwords with the
// parameters every mainstream authenticator app supports by default:
// HMAC-SHA1, 6 digits, 30 second step.
//
// It is deliberately dependency-free (stdlib only) and has no notion of
// storage: replay protection is expressed through the lastStep argument of
// Validate, and the caller persists the returned step atomically.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // #nosec G505 -- RFC 6238 default; every authenticator app speaks HMAC-SHA1
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	// Digits is the code length shown by authenticator apps.
	Digits = 6
	// Period is the step length in seconds.
	Period = 30
	// SecretBytes is the raw secret size: 160 bits, the RFC 4226 recommendation.
	SecretBytes = 20
	// Skew is the number of steps accepted on either side of "now" to tolerate
	// clock drift between the phone and the server.
	Skew = 1
)

var (
	// ErrInvalidSecret is returned for secrets that are not valid base32.
	ErrInvalidSecret = errors.New("totp: invalid base32 secret")

	// b32 is the unpadded alphabet used by otpauth:// URIs.
	b32 = base32.StdEncoding.WithPadding(base32.NoPadding)
)

// GenerateSecret returns a new random 160-bit secret, base32 encoded without
// padding (the form authenticator apps expect).
func GenerateSecret() (string, error) {
	buf := make([]byte, SecretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("totp: read random: %w", err)
	}
	return b32.EncodeToString(buf), nil
}

// decodeSecret accepts the secret the way users paste it: any case, with
// spaces/dashes and optional '=' padding.
func decodeSecret(secret string) ([]byte, error) {
	s := strings.ToUpper(strings.NewReplacer(" ", "", "-", "", "=", "").Replace(secret))
	if s == "" {
		return nil, ErrInvalidSecret
	}
	key, err := b32.DecodeString(s)
	if err != nil || len(key) == 0 {
		return nil, ErrInvalidSecret
	}
	return key, nil
}

// Step returns the RFC 6238 time-step counter for t.
func Step(t time.Time) int64 {
	return t.Unix() / Period
}

// hotp is RFC 4226 with dynamic truncation, parameterised on digits so the
// RFC 6238 test vectors (8 digits) can be checked against the same code path.
func hotp(key []byte, counter uint64, digits int) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := (uint32(sum[off])&0x7f)<<24 | uint32(sum[off+1])<<16 | uint32(sum[off+2])<<8 | uint32(sum[off+3])
	mod := uint32(1)
	for i := 0; i < digits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, bin%mod)
}

// CodeAtStep returns the 6-digit code for the given step counter.
func CodeAtStep(secret string, step int64) (string, error) {
	key, err := decodeSecret(secret)
	if err != nil {
		return "", err
	}
	if step < 0 {
		return "", errors.New("totp: negative step")
	}
	return hotp(key, uint64(step), Digits), nil
}

// NormalizeCode strips the spaces/dashes users type into code boxes. It does
// not validate; Validate rejects anything that is not exactly 6 digits.
func NormalizeCode(code string) string {
	return strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimSpace(code))
}

// Validate checks code against the steps now-Skew..now+Skew and returns the
// step that matched.
//
// Replay protection: a step <= lastStep is never accepted, so a code (or any
// older code inside the skew window) cannot be used twice. The caller must
// persist the returned step with a compare-and-set against lastStep; Validate
// itself holds no state.
//
// All candidate steps are always evaluated and compared in constant time so the
// response time does not reveal which step (if any) was close.
func Validate(secret, code string, now time.Time, lastStep int64) (int64, bool) {
	key, err := decodeSecret(secret)
	if err != nil {
		return 0, false
	}
	code = NormalizeCode(code)
	// Length is public (always 6); only the digits are secret-dependent.
	if len(code) != Digits {
		return 0, false
	}
	cur := Step(now)
	var matched int64
	found := 0
	for s := cur - Skew; s <= cur+Skew; s++ {
		if s < 0 {
			continue
		}
		expect := hotp(key, uint64(s), Digits)
		eq := subtle.ConstantTimeCompare([]byte(expect), []byte(code))
		// Replay window check is folded into the same branch-free tally.
		fresh := 0
		if s > lastStep {
			fresh = 1
		}
		if eq&fresh == 1 {
			matched = s
			found = 1
		}
	}
	if found == 0 {
		return 0, false
	}
	return matched, true
}

// URI builds the otpauth:// provisioning URI understood by authenticator apps
// and QR scanners. The issuer is included both as a label prefix and as the
// issuer parameter, as the Key URI Format recommends.
func URI(issuer, account, secret string) string {
	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	q := url.Values{}
	q.Set("secret", strings.ToUpper(strings.ReplaceAll(secret, " ", "")))
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprintf("%d", Digits))
	q.Set("period", fmt.Sprintf("%d", Period))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

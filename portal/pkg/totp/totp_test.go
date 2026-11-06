package totp

import (
	"encoding/base32"
	"net/url"
	"strings"
	"testing"
	"time"
)

// RFC 6238 Appendix B uses the ASCII secret "12345678901234567890" and 8-digit
// codes. Our 6-digit code is the last six digits of the same HOTP value, so the
// RFC vectors validate both the 8-digit core and the 6-digit public API.
const rfcSecretASCII = "12345678901234567890"

var rfcVectors = []struct {
	unix  int64
	code8 string
}{
	{59, "94287082"},
	{1111111109, "07081804"},
	{1111111111, "14050471"},
	{1234567890, "89005924"},
	{2000000000, "69279037"},
	{20000000000, "65353130"},
}

func rfcSecretB32() string {
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte(rfcSecretASCII))
}

func TestRFC6238Vectors(t *testing.T) {
	for _, v := range rfcVectors {
		step := v.unix / Period
		if got := hotp([]byte(rfcSecretASCII), uint64(step), 8); got != v.code8 {
			t.Errorf("t=%d: 8-digit code = %s, want %s", v.unix, got, v.code8)
		}
		want6 := v.code8[2:]
		got6, err := CodeAtStep(rfcSecretB32(), step)
		if err != nil || got6 != want6 {
			t.Errorf("t=%d: 6-digit code = %q (%v), want %s", v.unix, got6, err, want6)
		}
		s, ok := Validate(rfcSecretB32(), want6, time.Unix(v.unix, 0), 0)
		if !ok || s != step {
			t.Errorf("t=%d: Validate = (%d,%v), want (%d,true)", v.unix, s, ok, step)
		}
	}
}

func TestValidateSkewWindow(t *testing.T) {
	secret := rfcSecretB32()
	now := time.Unix(1111111111, 0)
	cur := Step(now)
	for _, tc := range []struct {
		name string
		step int64
		ok   bool
	}{
		{"previous step accepted", cur - 1, true},
		{"current step accepted", cur, true},
		{"next step accepted", cur + 1, true},
		{"two steps back rejected", cur - 2, false},
		{"two steps ahead rejected", cur + 2, false},
	} {
		code, _ := CodeAtStep(secret, tc.step)
		if _, ok := Validate(secret, code, now, 0); ok != tc.ok {
			t.Errorf("%s: ok=%v want %v", tc.name, ok, tc.ok)
		}
	}
}

func TestValidateReplayRejected(t *testing.T) {
	secret := rfcSecretB32()
	now := time.Unix(1111111111, 0)
	cur := Step(now)
	code, _ := CodeAtStep(secret, cur)

	step, ok := Validate(secret, code, now, 0)
	if !ok || step != cur {
		t.Fatalf("first use must succeed, got (%d,%v)", step, ok)
	}
	// Same code again once the step was recorded as used.
	if _, ok := Validate(secret, code, now, step); ok {
		t.Fatal("replay of the same code must be rejected")
	}
	// An older code inside the skew window is also dead after a newer step was used.
	older, _ := CodeAtStep(secret, cur-1)
	if _, ok := Validate(secret, older, now, step); ok {
		t.Fatal("older step must be rejected once a newer one was consumed")
	}
	// A newer step (clock ahead) is still allowed.
	newer, _ := CodeAtStep(secret, cur+1)
	if s, ok := Validate(secret, newer, now, step); !ok || s != cur+1 {
		t.Fatalf("newer step must still be accepted, got (%d,%v)", s, ok)
	}
}

func TestValidateRejectsMalformed(t *testing.T) {
	secret := rfcSecretB32()
	now := time.Unix(59, 0)
	for _, code := range []string{"", "12345", "1234567", "abcdef", "28 7082x", "287082 "} {
		if code == "287082 " { // trailing space is trimmed and then valid
			if _, ok := Validate(secret, code, now, 0); !ok {
				t.Errorf("code %q should be accepted after trimming", code)
			}
			continue
		}
		if _, ok := Validate(secret, code, now, 0); ok {
			t.Errorf("code %q must be rejected", code)
		}
	}
	if _, ok := Validate("not base32!!", "287082", now, 0); ok {
		t.Error("invalid secret must never validate")
	}
	if _, ok := Validate("", "287082", now, 0); ok {
		t.Error("empty secret must never validate")
	}
}

func TestValidateAcceptsFormattedCodeAndSecret(t *testing.T) {
	secret := strings.ToLower(rfcSecretB32())
	spaced := secret[:8] + " " + secret[8:]
	if _, ok := Validate(spaced, "287 082", time.Unix(59, 0), 0); !ok {
		t.Error("lower-case/spaced secret and spaced code should be accepted")
	}
}

func TestGenerateSecret(t *testing.T) {
	a, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := GenerateSecret()
	if a == b {
		t.Fatal("secrets must be random")
	}
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(a)
	if err != nil {
		t.Fatalf("secret is not unpadded base32: %v", err)
	}
	if len(raw) != 20 {
		t.Fatalf("secret is %d bytes, want 20 (160 bits)", len(raw))
	}
}

func TestURI(t *testing.T) {
	u := URI("AI Portal", "alice@example.com", "JBSWY3DPEHPK3PXP")
	if !strings.HasPrefix(u, "otpauth://totp/AI%20Portal:alice@example.com?") {
		t.Fatalf("unexpected label in %q", u)
	}
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatal(err)
	}
	q := parsed.Query()
	for k, want := range map[string]string{
		"secret": "JBSWY3DPEHPK3PXP", "issuer": "AI Portal",
		"algorithm": "SHA1", "digits": "6", "period": "30",
	} {
		if q.Get(k) != want {
			t.Errorf("param %s = %q, want %q", k, q.Get(k), want)
		}
	}
}

func TestInvalidSecretErrors(t *testing.T) {
	if _, err := CodeAtStep("!!!", 1); err != ErrInvalidSecret {
		t.Fatalf("want ErrInvalidSecret, got %v", err)
	}
}

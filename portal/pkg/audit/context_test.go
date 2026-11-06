package audit

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestClientInfoContextRoundTrip(t *testing.T) {
	ctx := WithClientInfo(context.Background(), "203.0.113.7", "curl/8.0")
	if got := ClientIPFromContext(ctx); got != "203.0.113.7" {
		t.Errorf("ClientIPFromContext = %q", got)
	}
	if got := UserAgentFromContext(ctx); got != "curl/8.0" {
		t.Errorf("UserAgentFromContext = %q", got)
	}
	// A bare context (e.g. background jobs) yields empty values, never panics.
	if ClientIPFromContext(context.Background()) != "" || UserAgentFromContext(context.Background()) != "" {
		t.Error("expected empty values for a context without client info")
	}
	//nolint:staticcheck // deliberately exercising the nil-context guard
	if ClientIPFromContext(nil) != "" || UserAgentFromContext(nil) != "" {
		t.Error("expected empty values for a nil context")
	}
}

func TestNormalizeIP(t *testing.T) {
	cases := map[string]string{
		"":                     "",
		"   ":                  "",
		"203.0.113.7":          "203.0.113.7",
		" 203.0.113.7 ":        "203.0.113.7",
		"2001:db8::1":          "2001:db8::1",
		"::ffff:203.0.113.7":   "203.0.113.7", // IPv4-mapped is stored as plain IPv4
		"not-an-ip":            "",
		"203.0.113.7, 1.2.3.4": "", // a raw X-Forwarded-For list is not an INET value
		"999.1.1.1":            "",
		"1.2.3.4/24":           "", // INET would accept a CIDR; a client address is never one
	}
	for in, want := range cases {
		if got := NormalizeIP(in); got != want {
			t.Errorf("NormalizeIP(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeUserAgent(t *testing.T) {
	if got := NormalizeUserAgent("  Mozilla/5.0\r\nX-Injected: 1\x00 "); strings.ContainsAny(got, "\r\n\x00") {
		t.Errorf("control characters survived: %q", got)
	}
	long := strings.Repeat("é", 600) // 2-byte runes: a byte cut would split one
	got := NormalizeUserAgent(long)
	if len(got) > maxUserAgentLen {
		t.Errorf("len = %d, want <= %d", len(got), maxUserAgentLen)
	}
	if strings.ContainsRune(got, '\uFFFD') || strings.ToValidUTF8(got, "?") != got {
		t.Error("truncation produced invalid UTF-8")
	}
}

// TestLoggerLogFillsIPAndUserAgentFromContext is the regression test for the
// production gap (ip_address set in 1 of 1,122 rows): rows written through the
// ordinary Log/LogWithDetails/LogStructured calls must carry the request's
// client info, an explicit LogWithIP value must win, and an invalid IP must
// become NULL instead of failing the batch.
func TestLoggerLogFillsIPAndUserAgentFromContext(t *testing.T) {
	database := testAuditDB(t)
	t.Cleanup(database.Close) // registered first => runs after the row cleanup below
	bg := context.Background()
	logger := NewLogger(database, nil)

	tag := uuid.NewString()
	reqCtx := WithClientInfo(bg, "198.51.100.23", "UnitTest/1.0")
	badCtx := WithClientInfo(bg, "garbage", "UnitTest/bad-ip")

	logger.Log(reqCtx, "", "", "ctxtest", "log_"+tag, "info", "plain Log")
	logger.LogWithDetails(reqCtx, "", "", "ctxtest", "details_"+tag, "info", "with details", map[string]string{"k": "v"})
	logger.LogStructured(reqCtx, "", "", "ctxtest", "structured_"+tag, "info", "structured", "t", "id", nil, map[string]int{"n": 1})
	logger.LogWithIP(reqCtx, "", "", "ctxtest", "explicit_"+tag, "info", "explicit ip wins", "192.0.2.99")
	logger.Log(badCtx, "", "", "ctxtest", "badip_"+tag, "info", "invalid ip")
	logger.Log(bg, "", "", "ctxtest", "noctx_"+tag, "info", "no request context")

	stopCtx, cancel := context.WithTimeout(bg, 5*time.Second)
	defer cancel()
	logger.Stop(stopCtx)
	t.Cleanup(func() {
		_, _ = database.Pool.Exec(bg, `DELETE FROM audit_logs WHERE category = 'ctxtest' AND action LIKE '%' || $1`, tag)
	})

	type row struct {
		ip, ua *string
	}
	get := func(action string) row {
		t.Helper()
		var r row
		if err := database.Pool.QueryRow(bg,
			`SELECT host(ip_address), user_agent FROM audit_logs WHERE category = 'ctxtest' AND action = $1`,
			action+"_"+tag).Scan(&r.ip, &r.ua); err != nil {
			t.Fatalf("select %s row: %v", action, err)
		}
		return r
	}
	eq := func(p *string, want string) bool { return p != nil && *p == want }

	for _, name := range []string{"log", "details", "structured"} {
		r := get(name)
		if !eq(r.ip, "198.51.100.23") || !eq(r.ua, "UnitTest/1.0") {
			t.Errorf("%s: ip=%v ua=%v, want 198.51.100.23 / UnitTest/1.0", name, deref(r.ip), deref(r.ua))
		}
	}
	if r := get("explicit"); !eq(r.ip, "192.0.2.99") || !eq(r.ua, "UnitTest/1.0") {
		t.Errorf("explicit: ip=%v ua=%v, want explicit IP preserved and UA from ctx", deref(r.ip), deref(r.ua))
	}
	if r := get("badip"); r.ip != nil || !eq(r.ua, "UnitTest/bad-ip") {
		t.Errorf("badip: ip=%v ua=%v, want NULL ip with UA kept", deref(r.ip), deref(r.ua))
	}
	if r := get("noctx"); r.ip != nil || r.ua != nil {
		t.Errorf("noctx: ip=%v ua=%v, want both NULL", deref(r.ip), deref(r.ua))
	}
}

func deref(p *string) string {
	if p == nil {
		return "<NULL>"
	}
	return *p
}

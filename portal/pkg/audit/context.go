package audit

import (
	"context"
	"net/netip"
	"strings"
)

// ctxKey is unexported so no other package can collide with (or forge) the
// client-info values stored in a context.
type ctxKey int

const (
	ctxKeyClientIP ctxKey = iota
	ctxKeyUserAgent
)

// maxUserAgentLen bounds the stored User-Agent. Clients control this header, so
// an unbounded value would let a caller bloat audit rows.
const maxUserAgentLen = 512

// WithClientInfo returns a context carrying the caller's IP and User-Agent.
// A request-scoped middleware sets it once so every audit writer further down
// the stack (audit.Logger, enterprise audit helpers) records who made the
// request without each handler having to thread the values through.
func WithClientInfo(ctx context.Context, ip, userAgent string) context.Context {
	ctx = context.WithValue(ctx, ctxKeyClientIP, ip)
	return context.WithValue(ctx, ctxKeyUserAgent, userAgent)
}

// ClientIPFromContext returns the client IP stored by WithClientInfo, or "".
func ClientIPFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(ctxKeyClientIP).(string)
	return v
}

// UserAgentFromContext returns the User-Agent stored by WithClientInfo, or "".
func UserAgentFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(ctxKeyUserAgent).(string)
	return v
}

// NormalizeIP returns the canonical text form of ip, or "" when it is empty or
// not a valid address. audit_logs.ip_address is an INET column, so a single bad
// value (e.g. a spoofed header echoed back by a proxy) would otherwise fail the
// whole COPY batch and drop every other entry in it.
func NormalizeIP(ip string) string {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return ""
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	return addr.Unmap().String()
}

// NormalizeUserAgent trims and bounds a User-Agent for storage.
func NormalizeUserAgent(ua string) string {
	ua = strings.TrimSpace(ua)
	// Control characters have no place in an audit trail and complicate log viewers.
	ua = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, ua)
	if len(ua) > maxUserAgentLen {
		ua = ua[:maxUserAgentLen]
	}
	// A byte-wise cut can split a multi-byte rune; Postgres rejects invalid UTF-8.
	return strings.ToValidUTF8(ua, "")
}

// nullableIP converts a raw IP string to a COPY/INSERT argument: the canonical
// address, or nil (SQL NULL) when empty or invalid.
func nullableIP(ip string) interface{} {
	if n := NormalizeIP(ip); n != "" {
		return n
	}
	return nil
}

// nullableText converts an optional string to nil when empty.
func nullableText(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

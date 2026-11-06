// Package sso implements the OpenID Connect relying-party side of per-enterprise
// single sign-on (authorization code flow with PKCE).
//
// The package is deliberately free of database and HTTP-router code: it fetches
// provider metadata and keys, builds authorization requests, exchanges codes and
// verifies ID tokens. Account mapping and session issuance live in
// internal/routes so they share the audited login choke point.
//
// Every outbound request goes through a guarded HTTP client. The issuer URL is
// entered by an enterprise admin, i.e. it is attacker-influenced, so the server
// must not be usable as a proxy into the cloud metadata service or the VPC.
package sso

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"
)

// Network limits. Provider endpoints are small JSON documents; anything larger
// is hostile or broken.
const (
	requestTimeout = 5 * time.Second
	maxBodyBytes   = 1 << 20 // 1 MiB for discovery, JWKS and token responses
	maxRedirects   = 3
)

// EnvAllowInsecureHTTP lets development setups talk to an http:// issuer. It
// only relaxes the scheme check; address filtering always applies.
const EnvAllowInsecureHTTP = "SSO_ALLOW_INSECURE_HTTP"

// Sentinel errors. Callers map them to coarse, non-reflective messages so the
// responses of a probing client reveal nothing about the internal network.
var (
	ErrBlockedAddress = errors.New("sso: address is not allowed")
	ErrInsecureURL    = errors.New("sso: URL must use https")
	ErrInvalidURL     = errors.New("sso: invalid URL")
	ErrTooLarge       = errors.New("sso: response too large")
)

// blockedPrefixes are ranges that net/netip's helpers do not classify as
// private/loopback/link-local but which still reach internal infrastructure.
var blockedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, p := range []string{
		"0.0.0.0/8",       // "this network"
		"100.64.0.0/10",   // carrier-grade NAT, often VPC-internal
		"192.0.0.0/24",    // IETF protocol assignments
		"192.0.2.0/24",    // TEST-NET-1
		"198.18.0.0/15",   // benchmarking
		"198.51.100.0/24", // TEST-NET-2
		"203.0.113.0/24",  // TEST-NET-3
		"240.0.0.0/4",     // reserved
		"fd00:ec2::/32",   // AWS IPv6 metadata / DNS
		"2001:db8::/32",   // documentation
	} {
		out = append(out, netip.MustParsePrefix(p))
	}
	return out
}()

// CheckIP returns ErrBlockedAddress for any address an SSO fetch must never
// reach: loopback, link-local (including 169.254.169.254), private, multicast,
// unspecified and the extra ranges above. IPv4-mapped and NAT64/6to4 forms are
// unwrapped first so they cannot smuggle a private v4 address past the check.
func CheckIP(ip net.IP) error {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return fmt.Errorf("%w: unparsable address", ErrBlockedAddress)
	}
	return checkAddr(addr.Unmap())
}

func checkAddr(addr netip.Addr) error {
	if embedded, ok := embeddedV4(addr); ok {
		if err := checkAddr(embedded); err != nil {
			return err
		}
	}
	if addr.IsUnspecified() || addr.IsLoopback() || addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsInterfaceLocalMulticast() || addr.IsMulticast() {
		return ErrBlockedAddress
	}
	for _, p := range blockedPrefixes {
		if p.Contains(addr) {
			return ErrBlockedAddress
		}
	}
	return nil
}

// embeddedV4 extracts the IPv4 address carried by NAT64 (64:ff9b::/96) and
// 6to4 (2002::/16) addresses.
func embeddedV4(addr netip.Addr) (netip.Addr, bool) {
	if !addr.Is6() {
		return netip.Addr{}, false
	}
	b := addr.As16()
	if netip.MustParsePrefix("64:ff9b::/96").Contains(addr) {
		return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}), true
	}
	if b[0] == 0x20 && b[1] == 0x02 {
		return netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}), true
	}
	return netip.Addr{}, false
}

// AllowInsecureHTTPFromEnv reports whether SSO_ALLOW_INSECURE_HTTP=true.
func AllowInsecureHTTPFromEnv() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(EnvAllowInsecureHTTP)), "true")
}

// ValidateURL checks scheme and host of a provider URL without resolving DNS:
// https only (unless allowInsecure), no credentials, no fragment, and no
// literal blocked IPs or obvious local host names. DNS-resolved addresses are
// checked again at connect time by the guarded dialer, which is what defeats
// DNS rebinding.
func ValidateURL(raw string, allowInsecure bool) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.Hostname() == "" {
		return nil, ErrInvalidURL
	}
	if u.User != nil || u.Fragment != "" {
		return nil, ErrInvalidURL
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
	case "http":
		if !allowInsecure {
			return nil, ErrInsecureURL
		}
	default:
		return nil, ErrInvalidURL
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".internal") ||
		strings.HasSuffix(host, ".local") {
		return nil, ErrBlockedAddress
	}
	if ip := net.ParseIP(host); ip != nil {
		if err := CheckIP(ip); err != nil {
			return nil, err
		}
	}
	return u, nil
}

// dialFilter vets one resolved destination right before connecting. The default
// is CheckIP; tests may widen it to reach an httptest server on loopback while
// still exercising the real redirect and dial plumbing.
type dialFilter func(ip net.IP, port string) error

func defaultDialFilter(ip net.IP, _ string) error { return CheckIP(ip) }

// newGuardedClient builds the only HTTP client used for provider traffic.
//
//   - Proxy settings from the environment are ignored: a proxy would be dialled
//     instead of the target and bypass the address checks.
//   - net.Dialer.Control runs for every connection attempt with the already
//     resolved IP, so a hostname that resolves to (or later rebinds to) an
//     internal address is refused, as is every redirect hop.
//   - Redirects are capped and each hop is re-validated (scheme, literal IPs);
//     an https -> http downgrade is never followed.
func newGuardedClient(allowInsecure bool, filter dialFilter) *http.Client {
	if filter == nil {
		filter = defaultDialFilter
	}
	dialer := &net.Dialer{
		Timeout: requestTimeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return ErrBlockedAddress
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return ErrBlockedAddress
			}
			return filter(ip, port)
		},
	}
	tr := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   requestTimeout,
		ResponseHeaderTimeout: requestTimeout,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		DisableCompression:    false,
	}
	return &http.Client{
		Transport: tr,
		Timeout:   requestTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return errors.New("sso: too many redirects")
			}
			if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https" {
				return ErrInsecureURL
			}
			if _, err := ValidateURL(req.URL.String(), allowInsecure); err != nil {
				return err
			}
			return nil
		},
	}
}

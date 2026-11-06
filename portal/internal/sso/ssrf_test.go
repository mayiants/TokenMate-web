package sso

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckIP_BlocksInternalRanges(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "127.8.9.10", "::1", "0.0.0.0", "::",
		"10.0.0.5", "172.16.0.1", "172.31.255.255", "192.168.1.1", "fd12:3456::1",
		"169.254.169.254", "169.254.0.1", "fe80::1", "fd00:ec2::254",
		"100.64.0.1", "224.0.0.1", "ff02::1", "240.0.0.1", "198.18.0.1",
		"::ffff:127.0.0.1", "::ffff:10.0.0.1", "::ffff:169.254.169.254",
		"64:ff9b::7f00:1", "64:ff9b::a9fe:a9fe", "2002:7f00:1::1",
	}
	for _, s := range blocked {
		ip := net.ParseIP(s)
		require.NotNil(t, ip, s)
		assert.ErrorIs(t, CheckIP(ip), ErrBlockedAddress, s)
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:4700:4700::1111"} {
		assert.NoError(t, CheckIP(net.ParseIP(s)), s)
	}
}

func TestValidateURL(t *testing.T) {
	cases := []struct {
		raw      string
		insecure bool
		want     error
	}{
		{"https://login.example.org/realm", false, nil},
		{"http://login.example.org", false, ErrInsecureURL},
		{"http://login.example.org", true, nil},
		{"ftp://login.example.org", true, ErrInvalidURL},
		{"https://user:pw@login.example.org", false, ErrInvalidURL},
		{"https://login.example.org/#frag", false, ErrInvalidURL},
		{"https://", false, ErrInvalidURL},
		{"https://127.0.0.1/", false, ErrBlockedAddress},
		{"https://localhost/", false, ErrBlockedAddress},
		{"https://foo.localhost/", false, ErrBlockedAddress},
		{"https://metadata.internal/", false, ErrBlockedAddress},
		{"https://169.254.169.254/latest/meta-data", false, ErrBlockedAddress},
		{"http://169.254.169.254/", true, ErrBlockedAddress}, // insecure flag never relaxes address checks
		{"https://[::1]/", false, ErrBlockedAddress},
		{"https://10.1.2.3:8443/", false, ErrBlockedAddress},
	}
	for _, tc := range cases {
		_, err := ValidateURL(tc.raw, tc.insecure)
		if tc.want == nil {
			assert.NoError(t, err, tc.raw)
		} else {
			assert.ErrorIs(t, err, tc.want, tc.raw)
		}
	}
}

// The default guarded client refuses to connect to a loopback server at dial
// time, which is the check that also defeats DNS rebinding.
func TestGuardedClient_RefusesLoopbackAtDial(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits++ }))
	defer srv.Close()

	c := NewClient(Options{AllowInsecureHTTP: true})
	// ValidateURL would reject the literal address first, so go through the
	// guarded client directly to prove the dialer itself refuses loopback.
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	_, err := c.http.Do(req)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrBlockedAddress) || strings.Contains(err.Error(), ErrBlockedAddress.Error()), err.Error())
	assert.Zero(t, hits, "the request must never reach the server")
}

// A public-looking first hop that redirects into the internal network must not
// be followed: literal addresses are refused by CheckRedirect, and hosts that
// merely resolve internally are refused by the dial filter.
func TestGuardedClient_RedirectsToInternalAreRefused(t *testing.T) {
	var internalHits int
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { internalHits++ }))
	defer internal.Close()
	_, internalPort, _ := net.SplitHostPort(internal.Listener.Addr().String())

	for name, target := range map[string]string{
		"metadata literal": "http://169.254.169.254/latest/meta-data/",
		"loopback literal": internal.URL,
		"private literal":  "http://10.0.0.1/",
	} {
		target := target
		first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target, http.StatusFound)
		}))
		_, firstPort, _ := net.SplitHostPort(first.Listener.Addr().String())
		// Test filter: the first server on loopback is reachable, everything else
		// follows the production rule. Redirect validation is the real one.
		c := NewClient(Options{AllowInsecureHTTP: true, dialFilter: func(ip net.IP, port string) error {
			if ip.IsLoopback() && port == firstPort {
				return nil
			}
			return CheckIP(ip)
		}})
		req, _ := http.NewRequest(http.MethodGet, first.URL, nil)
		resp, err := c.http.Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		require.Error(t, err, name)
		first.Close()
	}
	assert.Zero(t, internalHits, "no redirect hop may reach the internal server (port %s)", internalPort)
}

func TestGuardedClient_RedirectToHostnameResolvingInternalIsRefusedAtDial(t *testing.T) {
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer internal.Close()
	_, internalPort, _ := net.SplitHostPort(internal.Listener.Addr().String())
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 127.0.0.1.nip.io style names are blocked at dial, not by name; "localtest.me"
		// style resolution is environment dependent, so redirect by a literal that
		// bypasses CheckRedirect only through the dialer: an IPv4-mapped IPv6 literal.
		http.Redirect(w, r, "http://[::ffff:127.0.0.1]:"+internalPort+"/", http.StatusFound)
	}))
	defer first.Close()
	_, firstPort, _ := net.SplitHostPort(first.Listener.Addr().String())
	c := NewClient(Options{AllowInsecureHTTP: true, dialFilter: func(ip net.IP, port string) error {
		if ip.IsLoopback() && port == firstPort {
			return nil
		}
		return CheckIP(ip)
	}})
	req, _ := http.NewRequest(http.MethodGet, first.URL, nil)
	resp, err := c.http.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err)
}

func TestCheckRedirect_RefusesHTTPSDowngradeAndLoops(t *testing.T) {
	c := NewClient(Options{AllowInsecureHTTP: true})
	httpsReq, _ := http.NewRequest(http.MethodGet, "https://idp.example.org/a", nil)
	httpReq, _ := http.NewRequest(http.MethodGet, "http://idp.example.org/b", nil)
	assert.ErrorIs(t, c.http.CheckRedirect(httpReq, []*http.Request{httpsReq}), ErrInsecureURL)
	assert.NoError(t, c.http.CheckRedirect(httpsReq, []*http.Request{httpsReq}))

	via := make([]*http.Request, maxRedirects+1)
	for i := range via {
		via[i] = httpsReq
	}
	assert.Error(t, c.http.CheckRedirect(httpsReq, via), "redirect chains are capped")
}

func TestGetJSON_SizeCapAndStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/err" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"pad":"` + strings.Repeat("a", maxBodyBytes+10) + `"}`))
	}))
	defer srv.Close()
	// Route a public-looking host name to the local server; this bypasses the
	// address filter on purpose, because only the body handling is under test.
	addr := srv.Listener.Addr().String()
	c := NewClient(Options{AllowInsecureHTTP: true, HTTPClient: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		}}}})
	var out map[string]interface{}
	assert.ErrorIs(t, c.getJSON(context.Background(), "http://idp.example.org/big", &out), ErrTooLarge)
	assert.Error(t, c.getJSON(context.Background(), "http://idp.example.org/err", &out))
}

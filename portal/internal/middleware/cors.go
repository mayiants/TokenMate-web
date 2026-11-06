package middleware

import (
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// NewCORS returns a Gin middleware that validates the Origin header against
// allowedOrigins and sets the corresponding CORS headers. A "*" entry in
// allowedOrigins permits any origin.
func NewCORS(allowedOrigins []string) gin.HandlerFunc {
	allowAll := false
	for _, o := range allowedOrigins {
		if o == "*" {
			allowAll = true
			break
		}
	}

	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")
		if origin == "" {
			c.Next()
			return
		}

		allowed := allowAll
		if !allowed {
			if origin == sameOrigin(c) {
				allowed = true
				c.Header("Access-Control-Allow-Origin", origin)
			}
		}
		if !allowed {
			for _, o := range allowedOrigins {
				if o == origin {
					allowed = true
					c.Header("Access-Control-Allow-Origin", origin)
					break
				}
			}
		}
		if !allowed {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Origin not allowed"}})
			return
		}
		if allowAll {
			c.Header("Access-Control-Allow-Origin", origin)
		}

		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key")
		if !strings.HasPrefix(c.Request.URL.Path, "/static/") {
			c.Header("Cache-Control", "no-store, no-cache, must-revalidate, proxy-revalidate")
			c.Header("Pragma", "no-cache")
			c.Header("Expires", "0")
		}

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

// sameOrigin reconstructs the request origin from the incoming request so that
// same-origin CORS requests are always allowed.
//
// X-Forwarded-Proto / X-Forwarded-Host are forgeable by clients. They are only
// honored when the direct TCP peer (RemoteAddr) is a loopback or private
// address, i.e. the request is coming from a trusted reverse proxy on the same
// host / private network. Otherwise the headers are ignored and the origin is
// reconstructed from the raw request, so an off-path attacker cannot forge an
// Origin to bypass the CORS allowlist.
func sameOrigin(c *gin.Context) string {
	scheme := c.Request.URL.Scheme
	host := c.Request.Host

	if peer := remoteIP(c.Request); peer != nil && isLocalTrusted(peer) {
		// Behind a trusted reverse proxy: honor forwarded hints.
		if scheme == "" {
			scheme = c.Request.Header.Get("X-Forwarded-Proto")
		}
		if fwdHost := c.Request.Header.Get("X-Forwarded-Host"); fwdHost != "" {
			host = fwdHost
		}
	}

	if scheme == "" {
		if c.Request.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	return scheme + "://" + host
}

// remoteIP parses the client IP from Request.RemoteAddr (without the port).
func remoteIP(req *http.Request) net.IP {
	host, _, err := net.SplitHostPort(strings.TrimSpace(req.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(req.RemoteAddr)
	}
	return net.ParseIP(host)
}

// isLocalTrusted reports whether ip is a loopback or private (RFC 1918 / ULA)
// address. Reverse proxies in our deployment sit on loopback or a private
// network, so only those peers are trusted to supply X-Forwarded-* headers.
func isLocalTrusted(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	// IsPrivate covers RFC 1918, IPv6 ULA and CGNAT 100.64.0.0/10, all of
	// which indicate an internal network rather than a public client.
	return ip.IsPrivate()
}

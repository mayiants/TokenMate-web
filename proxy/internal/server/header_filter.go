package server

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// applyForwardedHeaders merges filtered client headers into the upstream
// request. Call this after setting adapter/provider auth headers so that
// forwarded headers cannot override Authorization or x-api-key.
func (s *HandlerService) applyForwardedHeaders(req *http.Request, c *gin.Context) {
	for k, values := range s.filterClientHeaders(c) {
		for _, v := range values {
			req.Header.Add(k, v)
		}
	}
}

// excludedUpstreamHeaders lists headers that must never be forwarded from the
// client to the upstream provider. They are either:
//   - client authentication headers (Authorization, x-api-key, etc.)
//   - hop-by-hop headers (Connection, Transfer-Encoding, etc.)
//   - browser security headers (Sec-*)
//   - Cloudflare-specific headers (Cf-*)
//   - host/origin metadata that upstreams should not receive
var excludedUpstreamHeaders = []string{
	"authorization",
	"x-api-key",
	"anthropic-version",
	"x-admin-secret",
	"content-length",
	"host",
	"origin",
	"referer",
	"connection",
	"keep-alive",
	"proxy-authenticate",
	"proxy-authorization",
	"te",
	"trailer",
	"transfer-encoding",
	"upgrade",
	"accept-encoding",
}

// forwardedUpstreamHeaders is the allow-list of headers that are safe and
// useful to forward. If empty, the filter forwards any header not in the
// excluded list. Populate this to restrict forwarding to a known set.
var forwardedUpstreamHeaders = map[string]struct{}{
	"x-request-id":      {},
	"x-correlation-id":  {},
	"x-trace-id":        {},
	"user-agent":        {},
	"accept":            {},
	"accept-language":   {},
	"x-forwarded-for":   {},
	"x-forwarded-proto": {},
}

// filterClientHeaders extracts headers from the incoming request that are safe
// to forward to the upstream provider. It drops auth, hop-by-hop, Cloudflare,
// and browser security headers, and only retains headers in the allow-list
// (or non-excluded headers when the allow-list is empty).
func (s *HandlerService) filterClientHeaders(c *gin.Context) http.Header {
	out := make(http.Header)

	// Only collect header names for the debug log when debug is actually enabled.
	// This avoids per-request slice allocations on the hot path in production.
	logger := s.LoggerWithRequestID(c)
	logDebug := logger.Core().Enabled(zap.DebugLevel)
	var dropped, forwarded []string
	if logDebug {
		dropped = make([]string, 0)
		forwarded = make([]string, 0)
	}

	for key, values := range c.Request.Header {
		lower := strings.ToLower(key)

		// Never forward Cloudflare or browser security prefixes.
		if strings.HasPrefix(lower, "cf-") || strings.HasPrefix(lower, "sec-") {
			if logDebug {
				dropped = append(dropped, key)
			}
			continue
		}

		// Never forward explicitly excluded headers.
		if isExcludedHeader(lower) {
			if logDebug {
				dropped = append(dropped, key)
			}
			continue
		}

		// If an allow-list is defined, only forward allowed headers.
		if len(forwardedUpstreamHeaders) > 0 {
			if _, ok := forwardedUpstreamHeaders[lower]; !ok {
				if logDebug {
					dropped = append(dropped, key)
				}
				continue
			}
		}

		if logDebug {
			forwarded = append(forwarded, key)
		}
		for _, v := range values {
			out.Add(key, v)
		}
	}

	if logDebug {
		logger.Debug("filtered client headers for upstream forwarding",
			zap.Strings("dropped", dropped),
			zap.Strings("forwarded", forwarded),
		)
	}

	return out
}

func isExcludedHeader(lower string) bool {
	for _, h := range excludedUpstreamHeaders {
		if lower == h {
			return true
		}
	}
	return false
}

package web

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/ai-proxy/ai-portal-go/internal/middleware"
	"github.com/ai-proxy/ai-portal-go/internal/session"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const (
	JWTCookieName = "jwt"
	// mfaPendingCookie carries the 5 minute pending-MFA token between the
	// password step and /login/mfa. It is HttpOnly, never put in a URL, and
	// scoped to the MFA endpoint.
	mfaPendingCookie = "mfa_pending"
)

func isSecure(c *gin.Context) bool {
	if c.Request.TLS != nil {
		return true
	}
	if c.GetHeader("X-Forwarded-Proto") == "https" {
		return true
	}
	return false
}

// getRequestHost returns the original host (respects X-Forwarded-Host from reverse proxy)
func getRequestHost(c *gin.Context) string {
	if host := c.GetHeader("X-Forwarded-Host"); host != "" {
		return host
	}
	return c.Request.Host
}

// isIPHost checks if the host string is an IP address (IPv4 or IPv6).
// IP addresses are not valid cookie Domain values per RFC 6265.
func isIPHost(host string) bool {
	return net.ParseIP(host) != nil
}

// getCookieDomain extracts domain without port from the request host for cookie Domain attribute.
// Uses X-Forwarded-Host header (if behind reverse proxy) or Request.Host directly.
// Never depends on APP_BASE_URL, so it works correctly with dynamic IPs (e.g. AWS EC2).
// Returns empty string if the host is an IP address — cookies without Domain are scoped to
// the origin server, which is correct for IP-based access (RFC 6265 §5.1.3).
func getCookieDomain(c *gin.Context) string {
	host := getRequestHost(c)
	if colonIdx := strings.Index(host, ":"); colonIdx != -1 {
		host = host[:colonIdx]
	}
	if isIPHost(host) {
		return ""
	}
	return host
}

// SetJWTWebCookie sets the JWT token as an HTTP-only cookie.
// Default session is kept for 7 days; "remember me" keeps it for 30 days
// to avoid frequent random logouts during normal use.
func SetJWTWebCookie(c *gin.Context, token string, rememberMe bool) {
	maxAge := 86400 * 7 // 7 days
	if rememberMe {
		maxAge = 86400 * 30 // 30 days
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     JWTCookieName,
		Value:    token,
		MaxAge:   maxAge,
		Path:     "/",
		Domain:   getCookieDomain(c),
		Secure:   isSecure(c),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearJWTWebCookie clears the JWT cookie
func ClearJWTWebCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     JWTCookieName,
		Value:    "",
		MaxAge:   -1,
		Path:     "/",
		Domain:   getCookieDomain(c),
		Secure:   isSecure(c),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// JWTMiddlewareWeb reads JWT from cookie and sets user context
func JWTMiddlewareWeb(auth *middleware.Auth, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie(JWTCookieName)
		if err != nil || token == "" {
			c.Next()
			return
		}

		claims, err := auth.JWTManager().Verify(token)
		if err != nil {
			logger.Warn("JWT verify failed",
				zap.String("path", c.Request.URL.Path),
				zap.Error(err),
			)
			c.Next()
			return
		}

		// Validate the user and take the role and token_version from the DB (not
		// the token) in a single query, so that role changes apply immediately
		// and tokens minted before a password change are rejected (same
		// token_version semantics as RequireAuth).
		dbRole, dbTokenVersion, ok := auth.ValidateWebSession(c, claims.UserID)
		if !ok {
			// User deleted/suspended or doesn't exist — clear cookie and continue unauthenticated
			ClearJWTWebCookie(c)
			c.Next()
			return
		}
		if claims.TokenVersion < dbTokenVersion {
			// Token invalidated by a password change — clear the cookie and
			// continue unauthenticated so RequireAuthWeb redirects to login.
			ClearJWTWebCookie(c)
			c.Next()
			return
		}

		role := claims.Role
		if dbRole != "" {
			role = dbRole
		}

		c.Set("user_id", claims.UserID)
		c.Set("user_email", claims.Email)
		c.Set("user_role", role)

		// Flag users who must still enrol in MFA; RequireAuthWeb confines them
		// to the profile page. An unevaluable policy fails closed.
		if required, ok := auth.MFAEnrollmentRequired(c, claims.UserID); required || !ok {
			c.Set("mfa_enrollment_required", true)
		}
		c.Next()
	}
}

// RequireAuthWeb redirects to login if not authenticated
func RequireAuthWeb() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetString("user_id") == "" {
			q := url.Values{}
			q.Set("toast", "error")
			q.Set("message", "Please login first")
			c.Redirect(http.StatusFound, "/login?"+q.Encode())
			c.Abort()
			return
		}
		// MFA policy gate: until the user has enrolled, only the profile page
		// (which hosts the enrolment UI) is reachable.
		if c.GetBool("mfa_enrollment_required") && !middleware.MFAGateExemptWeb(c.Request.URL.Path) {
			q := url.Values{}
			q.Set("toast", "warning")
			q.Set("message", "Please set up two-factor authentication to continue")
			c.Redirect(http.StatusFound, "/profile?"+q.Encode()+"#security")
			c.Abort()
			return
		}
		c.Next()
	}
}

// setMFAPendingCookie stores the pending MFA token for the challenge page.
func setMFAPendingCookie(c *gin.Context, token string) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     mfaPendingCookie,
		Value:    token,
		MaxAge:   int(session.ChallengeTTL.Seconds()),
		Path:     "/login/mfa",
		Domain:   getCookieDomain(c),
		Secure:   isSecure(c),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

func clearMFAPendingCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     mfaPendingCookie,
		Value:    "",
		MaxAge:   -1,
		Path:     "/login/mfa",
		Domain:   getCookieDomain(c),
		Secure:   isSecure(c),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// SecurityHeaders returns a gin middleware that sets security headers
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("Permissions-Policy",
			"accelerometer=(), camera=(), geolocation=(), gyroscope=(), microphone=(), payment=()")

		// Dynamic CSP based on route
		csp := "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; font-src 'self'; connect-src 'self' https://static.cloudflareinsights.com; frame-ancestors 'none'; base-uri 'self'"
		csp += "; script-src 'self' 'unsafe-inline' 'unsafe-eval' https://cdn.tailwindcss.com https://static.cloudflareinsights.com"

		c.Header("Content-Security-Policy", csp)
		c.Next()
	}
}

// CSRFMiddleware generates and validates CSRF tokens
func CSRFMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// For GET requests: set token only if not present, preserving existing token
		if c.Request.Method == http.MethodGet {
			existing, err := c.Cookie("csrf_token")
			if err != nil || existing == "" {
				token := generateCSRFToken()
				cookie := &http.Cookie{
					Name:     "csrf_token",
					Value:    token,
					MaxAge:   86400,
					Path:     "/",
					Domain:   getCookieDomain(c),
					Secure:   isSecure(c),
					HttpOnly: false,
					SameSite: http.SameSiteLaxMode,
				}
				http.SetCookie(c.Writer, cookie)
				c.Set("csrf_token", token)
			} else {
				c.Set("csrf_token", existing)
			}
			c.Next()
			return
		}

		// Skip CSRF for API routes and proxy API routes (they use JWT/API Key)
		if strings.HasPrefix(c.Request.URL.Path, "/api/") ||
			strings.HasPrefix(c.Request.URL.Path, "/v1/") {
			c.Next()
			return
		}

		// Validate CSRF for state-changing requests
		if c.Request.Method == http.MethodPost ||
			c.Request.Method == http.MethodPut ||
			c.Request.Method == http.MethodPatch ||
			c.Request.Method == http.MethodDelete {

			cookieToken, _ := c.Cookie("csrf_token")
			formToken := c.PostForm("csrf_token")
			headerToken := c.GetHeader("X-CSRF-Token")

			if cookieToken == "" || (subtle.ConstantTimeCompare([]byte(cookieToken), []byte(formToken)) != 1 && subtle.ConstantTimeCompare([]byte(cookieToken), []byte(headerToken)) != 1) {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "CSRF token invalid"})
				return
			}
		}

		c.Next()
	}
}

func generateCSRFToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

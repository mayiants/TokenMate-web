package middleware

import (
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/ai-proxy/ai-portal-go/internal/config"
	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/pkg/jwt"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type Auth struct {
	jwt    *jwt.Manager
	cfg    *config.Config
	logger *zap.Logger
	db     *db.DB
}

func NewAuth(cfg *config.Config, logger *zap.Logger) *Auth {
	return &Auth{
		jwt:    jwt.NewManager(cfg.Auth.JWTSecret, cfg.Auth.JWTExpires),
		cfg:    cfg,
		logger: logger,
	}
}

// SanitizeInput removes null bytes and other dangerous characters from request body.
// Also enforces a 10 MB body size limit to prevent DoS via unbounded reads.
func SanitizeInput() gin.HandlerFunc {
	const maxBodyBytes = 10 << 20 // 10 MB
	return func(c *gin.Context) {
		// Check Content-Type
		contentType := c.GetHeader("Content-Type")
		if strings.Contains(contentType, "application/json") {
			// Enforce size limit before reading
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
			// Read body
			body, err := io.ReadAll(c.Request.Body)
			if err != nil {
				c.AbortWithStatus(http.StatusRequestEntityTooLarge)
				return
			}
			defer func() { _ = c.Request.Body.Close() }()

			// Remove null bytes
			cleaned := strings.ReplaceAll(string(body), "\x00", "")

			// Recreate body with correct Content-Length
			c.Request.Body = io.NopCloser(strings.NewReader(cleaned))
			c.Request.ContentLength = int64(len(cleaned))
			c.Request.Header.Set("Content-Length", strconv.Itoa(len(cleaned)))
		}
		c.Next()
	}
}

// NewAuthFromManager creates an Auth with an externally provided JWT manager (for tests).
func NewAuthFromManager(mgr *jwt.Manager, cfg *config.Config, logger *zap.Logger) *Auth {
	return &Auth{jwt: mgr, cfg: cfg, logger: logger}
}

// SetDB sets the database connection for user validation
func (a *Auth) SetDB(database *db.DB) {
	a.db = database
}

// ValidateUserExists checks if the user ID exists in the database and the
// account is usable (not soft-deleted, not suspended). Public for web middleware.
func (a *Auth) ValidateUserExists(c *gin.Context, userID string) bool {
	if a.db == nil || userID == "" {
		return true
	}
	var exists bool
	err := a.db.QueryRowContext(c.Request.Context(),
		`SELECT EXISTS(SELECT 1 FROM users WHERE id = $1::uuid AND deleted_at IS NULL
		 AND (status IS NULL OR status != 'suspended'))`,
		userID).Scan(&exists)
	return err == nil && exists
}

// userSuspended reports whether the user exists but is suspended.
func (a *Auth) userSuspended(c *gin.Context, userID string) bool {
	if a.db == nil || userID == "" {
		return false
	}
	var suspended bool
	err := a.db.QueryRowContext(c.Request.Context(),
		`SELECT EXISTS(SELECT 1 FROM users WHERE id = $1::uuid AND deleted_at IS NULL AND status = 'suspended')`,
		userID).Scan(&suspended)
	return err == nil && suspended
}

// CurrentRole returns the user's role as stored in the database, so callers
// authenticating via long-lived tokens honor role changes immediately.
// Returns false when the role cannot be determined (no DB or query error).
func (a *Auth) CurrentRole(c *gin.Context, userID string) (string, bool) {
	if a.db == nil || userID == "" {
		return "", false
	}
	var role string
	if err := a.db.QueryRowContext(c.Request.Context(),
		`SELECT COALESCE(role, 'user') FROM users WHERE id = $1::uuid`,
		userID).Scan(&role); err != nil {
		return "", false
	}
	return role, true
}

// ValidateWebSession validates that the user still exists (not deleted, not
// suspended) and returns the user's role and token_version from the database
// in a single query, so web callers authenticating via long-lived cookie JWTs
// can honor role changes and password resets immediately without extra
// round-trips. ok is false when the user is deleted/suspended or the query
// fails; when no DB is configured there is nothing to validate against and ok
// is true with zero values.
func (a *Auth) ValidateWebSession(c *gin.Context, userID string) (role string, tokenVersion int, ok bool) {
	if a.db == nil || userID == "" {
		return "", 0, true
	}
	err := a.db.QueryRowContext(c.Request.Context(),
		`SELECT COALESCE(role, 'user'), COALESCE(token_version, 0)
		 FROM users WHERE id = $1::uuid AND deleted_at IS NULL
		 AND (status IS NULL OR status != 'suspended')`,
		userID).Scan(&role, &tokenVersion)
	if err != nil {
		return "", 0, false
	}
	return role, tokenVersion, true
}

func (a *Auth) JWTManager() *jwt.Manager {
	return a.jwt
}

// RequireAuth validates JWT token
func (a *Auth) RequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractToken(c)
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{"message": "Authentication required"},
			})
			return
		}

		claims, err := a.jwt.Verify(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{"message": "Invalid or expired token"},
			})
			return
		}

		if !a.ValidateUserExists(c, claims.UserID) {
			if a.userSuspended(c, claims.UserID) {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
					"error": gin.H{"message": "Account suspended"},
				})
				return
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{"message": "User not found or deleted"},
			})
			return
		}

		// Validate token version (invalidated on password change).
		// Always check, even for legacy tokens with TokenVersion=0: if the user
		// has changed their password (currentVersion > 0), a legacy token
		// (0 < currentVersion) is treated as stale and rejected.
		// The role is also read from the DB here so that role changes (e.g. an
		// admin being demoted) take effect immediately instead of waiting for
		// the JWT (valid up to 7 days) to expire.
		if a.db != nil {
			var currentVersion int
			var currentRole string
			if err := a.db.QueryRowContext(c.Request.Context(),
				`SELECT COALESCE(token_version, 0), COALESCE(role, 'user') FROM users WHERE id = $1::uuid`,
				claims.UserID).Scan(&currentVersion, &currentRole); err == nil {
				if claims.TokenVersion < currentVersion {
					c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
						"error": gin.H{"message": "Token invalidated. Please login again."},
					})
					return
				}
				if currentRole != "" {
					claims.Role = currentRole
				}
			}
		}

		// Users blocked by an MFA policy they have not satisfied may only reach
		// the enrolment endpoints.
		if a.enforceMFAGate(c, claims.UserID) {
			return
		}

		c.Set("user_id", claims.UserID)
		c.Set("email", claims.Email)
		c.Set("role", claims.Role)
		c.Next()
	}
}

// OptionalAuth validates JWT token if present.
// If a token is present and verifies but the user has been deleted, suspended,
// or the token version is stale, the request is rejected with 401 (rather than
// silently degrading to anonymous) to force a clean re-login.
func (a *Auth) OptionalAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractToken(c)
		if token == "" {
			c.Next()
			return
		}

		claims, err := a.jwt.Verify(token)
		if err != nil {
			// Malformed/expired token: treat as anonymous rather than blocking.
			c.Next()
			return
		}

		if !a.ValidateUserExists(c, claims.UserID) {
			if a.userSuspended(c, claims.UserID) {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
					"error": gin.H{"message": "Account suspended"},
				})
				return
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{"message": "User not found or deleted"},
			})
			return
		}

		// Validate token version (same logic as RequireAuth), and take the role
		// from the DB so role changes apply immediately.
		if a.db != nil {
			var currentVersion int
			var currentRole string
			if err := a.db.QueryRowContext(c.Request.Context(),
				`SELECT COALESCE(token_version, 0), COALESCE(role, 'user') FROM users WHERE id = $1::uuid`,
				claims.UserID).Scan(&currentVersion, &currentRole); err == nil {
				if claims.TokenVersion < currentVersion {
					c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
						"error": gin.H{"message": "Token invalidated. Please login again."},
					})
					return
				}
				if currentRole != "" {
					claims.Role = currentRole
				}
			}
		}

		// A user who still has to enrol in MFA is treated as anonymous on
		// optional-auth endpoints rather than getting personalised data.
		if !mfaGateExemptAPI(c.Request.URL.Path) {
			if required, ok := a.MFAEnrollmentRequired(c, claims.UserID); !ok || required {
				c.Next()
				return
			}
		}

		c.Set("user_id", claims.UserID)
		c.Set("email", claims.Email)
		c.Set("role", claims.Role)
		c.Next()
	}
}

// RequireAdmin requires system_admin or super_admin role
func (a *Auth) RequireAdmin() gin.HandlerFunc {
	return a.requireRole("system_admin", "super_admin")
}

// RequireSuperAdmin requires super_admin role
func (a *Auth) RequireSuperAdmin() gin.HandlerFunc {
	return a.requireRole("super_admin")
}

// RequireEnterpriseAdmin requires enterprise_admin or above
func (a *Auth) RequireEnterpriseAdmin() gin.HandlerFunc {
	return a.requireMinRole("enterprise_admin")
}

func (a *Auth) requireRole(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		role := c.GetString("role")
		if role == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{"message": "Authentication required"},
			})
			return
		}

		for _, r := range roles {
			if role == r {
				c.Next()
				return
			}
		}

		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": gin.H{"message": "Insufficient permissions"},
		})
	}
}

var roleHierarchy = map[string]int{
	"user":             1,
	"enterprise_admin": 2,
	"system_admin":     10,
	"super_admin":      100,
}

func (a *Auth) requireMinRole(minRole string) gin.HandlerFunc {
	return func(c *gin.Context) {
		role := c.GetString("role")
		if role == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{"message": "Authentication required"},
			})
			return
		}

		userLevel := roleHierarchy[role]
		requiredLevel := roleHierarchy[minRole]

		if userLevel < requiredLevel {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": gin.H{"message": "Insufficient permissions"},
			})
			return
		}

		c.Next()
	}
}

// CanPerformSensitiveOps checks if user can perform sensitive operations
func (a *Auth) CanPerformSensitiveOps(role string) bool {
	if role == "super_admin" {
		return true
	}
	if a.cfg.Auth.AdminMode == "mvp" && role == "system_admin" {
		return true
	}
	return false
}

func extractToken(c *gin.Context) string {
	// First check Authorization header
	auth := c.GetHeader("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	// Then check cookie
	token, err := c.Cookie("jwt")
	if err == nil && token != "" {
		return token
	}
	return ""
}

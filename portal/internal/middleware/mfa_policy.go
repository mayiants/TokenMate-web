package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/gin-gonic/gin"
)

// MFAErrorCode is the machine-readable code returned (HTTP 403) to a user who
// must enrol in MFA before using anything else.
const MFAErrorCode = "mfa_enrollment_required"

// SettingMFARequiredRoles is the system_settings key holding the comma
// separated list of roles that must use MFA. Empty means nobody is forced.
const SettingMFARequiredRoles = "mfa_required_roles"

// ValidMFARoles are the values accepted in mfa_required_roles.
var ValidMFARoles = []string{"user", "enterprise_admin", "system_admin", "super_admin"}

// MFAPolicy is the effective MFA situation of one user.
type MFAPolicy struct {
	TOTPEnabled       bool
	Role              string
	EnterpriseRequire bool // the user's enterprise has require_mfa = true
	RoleRequired      bool // the user's role is listed in mfa_required_roles
}

// Required reports whether the user must have MFA (by enterprise or role policy).
func (p MFAPolicy) Required() bool { return p.EnterpriseRequire || p.RoleRequired }

// EnrollmentRequired is true when a policy applies and the user has not enrolled
// yet: that user may only reach the enrolment page/API.
func (p MFAPolicy) EnrollmentRequired() bool { return p.Required() && !p.TOTPEnabled }

// ParseMFARoles splits and de-duplicates a stored mfa_required_roles value,
// dropping blanks. Unknown values are kept so the gate never silently ignores a
// typo in the other direction (an unknown role simply matches nobody).
func ParseMFARoles(csv string) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range strings.Split(csv, ",") {
		r = strings.ToLower(strings.TrimSpace(r))
		if r == "" || seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	return out
}

func roleListed(csv, role string) bool {
	for _, r := range ParseMFARoles(csv) {
		if r == strings.ToLower(role) {
			return true
		}
	}
	return false
}

// LoadMFAPolicy reads the user's enrolment state, enterprise switch and the
// platform role list in one round trip. The role comes from the database so a
// demotion/promotion applies immediately.
func LoadMFAPolicy(ctx context.Context, database *db.DB, userID string) (MFAPolicy, error) {
	var p MFAPolicy
	if database == nil || userID == "" {
		return p, errors.New("mfa policy: no database")
	}
	var rolesCSV string
	err := database.QueryRow(ctx,
		`SELECT COALESCE(u.totp_enabled, false), COALESCE(u.role, 'user'), COALESCE(e.require_mfa, false),
		        COALESCE((SELECT value FROM system_settings WHERE key = $2), '')
		   FROM users u LEFT JOIN enterprises e ON e.id = u.enterprise_id AND e.deleted_at IS NULL
		  WHERE u.id = $1::uuid AND u.deleted_at IS NULL`, userID, SettingMFARequiredRoles,
	).Scan(&p.TOTPEnabled, &p.Role, &p.EnterpriseRequire, &rolesCSV)
	if err != nil {
		return p, err
	}
	p.RoleRequired = roleListed(rolesCSV, p.Role)
	return p, nil
}

// mfaGateExemptAPI lists the authenticated API paths an un-enrolled user may
// still call: the enrolment endpoints themselves plus identity/logout so the UI
// can explain the situation and let the user sign out.
func mfaGateExemptAPI(path string) bool {
	return strings.HasPrefix(path, "/api/profile/mfa/") || path == "/api/profile/mfa" ||
		path == "/api/auth/me" || path == "/api/auth/logout"
}

// MFAGateExemptWeb lists page paths reachable while enrolment is pending. The
// profile page hosts the Security section used to enrol.
func MFAGateExemptWeb(path string) bool {
	return path == "/profile" || path == "/profile/"
}

// MFAEnrollmentRequired reports whether userID is blocked by an MFA policy
// they have not satisfied. ok is false when the policy could not be evaluated;
// callers must fail closed.
func (a *Auth) MFAEnrollmentRequired(c *gin.Context, userID string) (required, ok bool) {
	if a.db == nil || userID == "" {
		return false, true
	}
	p, err := LoadMFAPolicy(c.Request.Context(), a.db, userID)
	if err != nil {
		return false, false
	}
	return p.EnrollmentRequired(), true
}

// enforceMFAGate aborts the request with 403 mfa_enrollment_required when the
// user must enrol and the path is not an enrolment path. It returns true when
// the request was aborted. It only runs for session-authenticated routes
// (RequireAuth); API-key, Proxy validate and webhook endpoints never pass here,
// so they are unaffected.
func (a *Auth) enforceMFAGate(c *gin.Context, userID string) bool {
	if a.db == nil || mfaGateExemptAPI(c.Request.URL.Path) {
		return false
	}
	required, ok := a.MFAEnrollmentRequired(c, userID)
	if !ok {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{"message": "Unable to verify security policy"},
		})
		return true
	}
	if required {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": gin.H{
				"code":    MFAErrorCode,
				"message": "Multi-factor authentication enrolment is required before you can continue",
			},
		})
		return true
	}
	return false
}

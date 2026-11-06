package middleware

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestParseMFARoles(t *testing.T) {
	for in, want := range map[string][]string{
		"":                              nil,
		"  ":                            nil,
		"user":                          {"user"},
		"User, enterprise_admin ,,user": {"user", "enterprise_admin"},
		"super_admin,system_admin":      {"super_admin", "system_admin"},
		",,":                            nil,
	} {
		if got := ParseMFARoles(in); !reflect.DeepEqual(got, want) {
			t.Errorf("ParseMFARoles(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestMFAPolicyDecision(t *testing.T) {
	cases := []struct {
		name                   string
		p                      MFAPolicy
		required, mustEnrolNow bool
	}{
		{"nothing configured forces nobody", MFAPolicy{}, false, false},
		{"enterprise policy, not enrolled", MFAPolicy{EnterpriseRequire: true}, true, true},
		{"role policy, not enrolled", MFAPolicy{RoleRequired: true}, true, true},
		{"policy but already enrolled", MFAPolicy{EnterpriseRequire: true, RoleRequired: true, TOTPEnabled: true}, true, false},
		{"enrolled without policy", MFAPolicy{TOTPEnabled: true}, false, false},
	}
	for _, c := range cases {
		if c.p.Required() != c.required || c.p.EnrollmentRequired() != c.mustEnrolNow {
			t.Errorf("%s: Required=%v EnrollmentRequired=%v", c.name, c.p.Required(), c.p.EnrollmentRequired())
		}
	}
	if !roleListed("user, enterprise_admin", "Enterprise_Admin") || roleListed("", "user") || roleListed("user", "super_admin") {
		t.Error("roleListed must be case-insensitive and exact")
	}
}

func TestMFAGateExemptPaths(t *testing.T) {
	for _, p := range []string{
		"/api/profile/mfa/status", "/api/profile/mfa/setup", "/api/profile/mfa/enable",
		"/api/profile/mfa/disable", "/api/profile/mfa/recovery-codes/regenerate",
		"/api/auth/me", "/api/auth/logout",
	} {
		if !mfaGateExemptAPI(p) {
			t.Errorf("%s must stay reachable while enrolment is pending", p)
		}
	}
	for _, p := range []string{
		"/api/keys", "/api/profile", "/api/profile/password", "/api/admin/users", "/api/billing/balance",
		"/api/profile/mfa-evil", "/api/auth/login", "/api/enterprise/security",
	} {
		if mfaGateExemptAPI(p) {
			t.Errorf("%s must be gated", p)
		}
	}
	if !MFAGateExemptWeb("/profile") || MFAGateExemptWeb("/dashboard") || MFAGateExemptWeb("/profile/settings") {
		t.Error("web exemptions must be exactly the profile page")
	}
}

// Without a database the gate is inert (unit-test / MVP wiring), never a blanket 403.
func TestEnforceMFAGateWithoutDBIsNoop(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := newTestAuth("production")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/keys", nil)
	if a.enforceMFAGate(c, "u1") {
		t.Fatal("gate must not abort when no database is configured")
	}
}

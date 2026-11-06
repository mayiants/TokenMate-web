package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func gateRouter(flag bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", "u1")
		if flag {
			c.Set("mfa_enrollment_required", true)
		}
		c.Next()
	})
	r.Use(RequireAuthWeb())
	for _, p := range []string{"/dashboard", "/apikeys", "/profile", "/admin"} {
		r.GET(p, func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	}
	return r
}

// A user who must enrol in MFA can reach only the profile page (which hosts the
// enrolment UI); every other authenticated page redirects there.
func TestRequireAuthWebMFAEnrolmentGate(t *testing.T) {
	r := gateRouter(true)
	for _, p := range []string{"/dashboard", "/apikeys", "/admin"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, p, nil))
		if w.Code != http.StatusFound {
			t.Errorf("%s: status %d, want 302", p, w.Code)
		}
		if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "/profile?") || !strings.HasSuffix(loc, "#security") {
			t.Errorf("%s: redirected to %q, want the profile security section", p, loc)
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/profile", nil))
	if w.Code != http.StatusOK {
		t.Errorf("/profile: status %d, want 200 (enrolment page)", w.Code)
	}
}

func TestRequireAuthWebNoGateWhenNotRequired(t *testing.T) {
	r := gateRouter(false)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 when no MFA policy applies", w.Code)
	}
}

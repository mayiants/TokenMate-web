package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mayiants/ai-portal-go/pkg/audit"
	"github.com/gin-gonic/gin"
)

func TestAuditClientInfo_StoresIPAndUserAgentInRequestContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuditClientInfo())
	var gotIP, gotUA string
	r.GET("/x", func(c *gin.Context) {
		gotIP = audit.ClientIPFromContext(c.Request.Context())
		gotUA = audit.UserAgentFromContext(c.Request.Context())
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.RemoteAddr = "203.0.113.50:44321"
	req.Header.Set("User-Agent", "Playwright/1.4")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d", w.Code)
	}
	if gotIP != "203.0.113.50" {
		t.Errorf("client ip = %q, want 203.0.113.50", gotIP)
	}
	if gotUA != "Playwright/1.4" {
		t.Errorf("user agent = %q, want Playwright/1.4", gotUA)
	}
}

// TestAuditClientInfo_IgnoresSpoofedForwardedForFromUntrustedPeer documents
// that the stored IP is c.ClientIP(): with the default trusted-proxy set a
// directly connected client cannot choose its audit IP via X-Forwarded-For
// when the engine trusts no proxies.
func TestAuditClientInfo_IgnoresSpoofedForwardedForFromUntrustedPeer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	_ = r.SetTrustedProxies(nil)
	r.Use(AuditClientInfo())
	var gotIP string
	r.GET("/x", func(c *gin.Context) {
		gotIP = audit.ClientIPFromContext(c.Request.Context())
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.RemoteAddr = "203.0.113.50:44321"
	req.Header.Set("X-Forwarded-For", "10.9.9.9")
	r.ServeHTTP(httptest.NewRecorder(), req)

	if gotIP != "203.0.113.50" {
		t.Errorf("client ip = %q, want the socket peer 203.0.113.50", gotIP)
	}
}

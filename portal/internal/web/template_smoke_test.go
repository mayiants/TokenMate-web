package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"go.uber.org/zap"

	"github.com/ai-proxy/ai-portal-go/internal/query"
	"github.com/ai-proxy/ai-portal-go/web"
)

// TestTemplatesParse verifies all registered page templates can be parsed and
// that the pages expected by the enterprise P1 routes are actually loaded.
func TestTemplatesParse(t *testing.T) {
	h, err := NewHandler(nil, nil, zap.NewNop(), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewHandler failed to parse templates: %v", err)
	}

	required := []string{
		"enterprise/member-detail",
		"enterprise/key-detail",
		"enterprise/alerts",
		"enterprise/settings",
		"login-mfa",
		"profile",
		"support",
		"admin-tickets",
	}
	for _, name := range required {
		if _, ok := h.pages[name]; !ok {
			t.Errorf("page template %q was not loaded (parse likely failed)", name)
		}
	}
}

// TestTemplatesNoBareJSOutsideScript guards against a regression where a JS
// fragment is inserted into a template outside any <script> tag and renders
// as visible page text (observed with syncGroupToParent in admin-groups.html).
// Any non-whitespace line starting with "function " or "//" must be inside a
// <script> block or an HTML comment.
func TestTemplatesNoBareJSOutsideScript(t *testing.T) {
	var files []string
	err := fs.WalkDir(web.FS, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".html") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk templates: %v", err)
	}

	for _, path := range files {
		b, err := web.FS.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		inScript, inComment := false, false
		for i, line := range strings.Split(string(b), "\n") {
			if inComment {
				if strings.Contains(line, "-->") {
					inComment = false
				}
			} else if strings.Contains(line, "<!--") && !strings.Contains(line, "-->") {
				inComment = true
			}
			if strings.Contains(line, "</script>") {
				inScript = false
				continue
			}
			if inScript {
				continue
			}
			if strings.Contains(line, "<script") {
				inScript = true
				continue
			}
			if inComment {
				continue
			}
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "function ") || strings.HasPrefix(trimmed, "//") {
				t.Errorf("%s:%d: bare JS outside <script>: %q", path, i+1, trimmed)
			}
		}
	}
}

// enterpriseSettingsTestData mirrors the fields enterprise/settings.html reads
// from handleEnterpriseSettings' local settingsData type.
type enterpriseSettingsTestData struct {
	ID, Name, LegalName, ContactEmail, ContactName, ContactPhone string
	Country, TaxNumber, RegisteredAddress, BankName, BankAccount string
	CreatedAt                                                    interface{}
	VerifiedAt                                                   interface{}
	MemberCount, MaxMembers                                      int
	PricingRatio                                                 float64
	BillingCycle                                                 string
	QuotaTotal                                                   int64
	CreditTotalUSD                                               float64
}

func renderEnterpriseSettings(t *testing.T, h *Handler, role string) string {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/enterprise/settings", nil)
	c.Set("user_id", "00000000-0000-0000-0000-000000000001")
	c.Set("user_role", role)
	h.RenderPage(c, "enterprise/settings", PageData{
		Title: "Enterprise Settings",
		Data: &enterpriseSettingsTestData{
			ID: "11111111-2222-3333-4444-555555555555", Name: "Acme", BillingCycle: "monthly",
			MaxMembers: 10, PricingRatio: 1,
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("render enterprise/settings as %s: status %d body %q", role, w.Code, w.Body.String())
	}
	return w.Body.String()
}

// TestEnterpriseSettingsRetentionSection verifies the Data retention section
// renders for enterprise admins (with the enterprise id the purge modal asks
// the user to retype, accessible labels and dialog semantics) and is absent
// for plain members.
func TestEnterpriseSettingsRetentionSection(t *testing.T) {
	h, err := NewHandler(nil, nil, zap.NewNop(), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	admin := renderEnterpriseSettings(t, h, "enterprise_admin")
	for _, want := range []string{
		`id="retention-card"`,
		`data-enterprise-id="11111111-2222-3333-4444-555555555555"`,
		`name="retention-mode"`,
		`value="off"`, `value="metadata"`, `value="standard"`,
		`<legend class="form-label">Retention mode</legend>`,
		`for="retention-days"`,
		`id="retention-purge-modal"`, `role="dialog"`, `aria-modal="true"`, `aria-labelledby="retention-purge-title"`,
		`for="retention-purge-confirm"`,
		`/api/enterprise/retention`,
	} {
		if !strings.Contains(admin, want) {
			t.Errorf("enterprise_admin settings page missing %q", want)
		}
	}

	member := renderEnterpriseSettings(t, h, "user")
	if strings.Contains(member, `id="retention-card"`) {
		t.Error("retention section must not render for non-admin members")
	}
}

// TestAdminAuditLogsVerifyButtonIsSuperAdminOnly renders the audit-logs page
// and checks the "Verify integrity" control (tamper-evidence check) is offered
// to super_admin only; the API enforces the same, this keeps the UI honest.
func TestAdminAuditLogsVerifyButtonIsSuperAdminOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, err := NewHandler(nil, nil, zap.NewNop(), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	render := func(role string) string {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/admin/audit-logs", nil)
		c.Set("user_id", "u1")
		c.Set("user_role", role)
		h.RenderPage(c, "admin-audit-logs", PageData{
			Title: "Audit Logs",
			Data: &PaginatedData{
				Items: struct {
					Logs         []query.AdminAuditLog
					ErrorCount   int
					WarningCount int
					InfoCount    int
					Search       string
					Category     string
					Level        string
					FilterQuery  string
				}{Logs: []query.AdminAuditLog{}},
				Page: 1, PageSize: 50, TotalPages: 1,
			},
		})
		if w.Code != http.StatusOK {
			t.Fatalf("render as %s: status %d body %q", role, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	if body := render("super_admin"); !strings.Contains(body, `id="verify-audit-btn"`) || !strings.Contains(body, "/api/admin/audit/verify") {
		t.Error("super_admin must see the Verify integrity button wired to /api/admin/audit/verify")
	}
	if body := render("system_admin"); strings.Contains(body, `id="verify-audit-btn"`) {
		t.Error("system_admin must not be offered the Verify integrity button")
	}
}

func renderPage(t *testing.T, h *Handler, page, path, role string, data interface{}) string {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, path, nil)
	if role != "" {
		c.Set("user_id", "00000000-0000-0000-0000-000000000001")
		c.Set("user_role", role)
	}
	h.RenderPage(c, page, PageData{Title: page, Data: data})
	if w.Code != http.StatusOK {
		t.Fatalf("render %s: status %d body %q", page, w.Code, w.Body.String())
	}
	return w.Body.String()
}

// TestLoginMFAPageRenders covers the MFA challenge page: it must post the code
// to /login/mfa with CSRF protection and an autofill-friendly one-time-code input.
func TestLoginMFAPageRenders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, err := NewHandler(nil, nil, zap.NewNop(), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	body := renderPage(t, h, "login-mfa", "/login/mfa", "", nil)
	for _, want := range []string{
		`action="/login/mfa"`, `method="POST"`, `name="csrf_token"`, `name="code"`,
		`autocomplete="one-time-code"`, `for="mfa-code"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("login-mfa page missing %q", want)
		}
	}
}

// TestProfileSecuritySectionRenders checks the Security card on the profile
// page (the enrolment page for users blocked by an MFA policy) and that it
// talks to the documented API and shows the secret/URI as text (no QR library
// is available in this repository).
func TestProfileSecuritySectionRenders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, err := NewHandler(nil, nil, zap.NewNop(), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	body := renderPage(t, h, "profile", "/profile", "user", &ProfileData{Email: "u@example.test", Role: "user"})
	for _, want := range []string{
		`id="security"`, `id="mfa-secret"`, `id="mfa-uri"`, `for="mfa-confirm-code"`,
		`for="mfa-password"`, `id="mfa-codes-box"`,
		`/api/profile/mfa/`, `recovery-codes/regenerate`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("profile page missing %q", want)
		}
	}
}

// TestEnterpriseSettingsSecurityCard: the require-MFA switch is for enterprise
// admins only.
func TestEnterpriseSettingsSecurityCard(t *testing.T) {
	h, err := NewHandler(nil, nil, zap.NewNop(), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	admin := renderEnterpriseSettings(t, h, "enterprise_admin")
	for _, want := range []string{`id="security-card"`, `id="require-mfa"`, `/api/enterprise/security`} {
		if !strings.Contains(admin, want) {
			t.Errorf("enterprise_admin settings page missing %q", want)
		}
	}
	if member := renderEnterpriseSettings(t, h, "user"); strings.Contains(member, `id="security-card"`) {
		t.Error("security policy card must not render for plain members")
	}
}

// TestLoginPageOffersSSO: the login page carries the "Sign in with SSO" form,
// which asks /api/auth/sso/discover and only follows start URLs that live under
// /auth/sso/start (never an arbitrary URL from the response).
func TestLoginPageOffersSSO(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, err := NewHandler(nil, nil, zap.NewNop(), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	body := renderPage(t, h, "login", "/login", "", nil)
	for _, want := range []string{
		`id="sso-form"`, `for="sso-email"`, `id="sso-email"`, `type="email"`, `id="sso-btn"`, `id="sso-msg"`,
		`/api/auth/sso/discover`, `/auth/sso/start?`, `Sign in with SSO`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("login page missing %q", want)
		}
	}
	// The password form must still be there and unchanged.
	if !strings.Contains(body, `action="/login"`) || !strings.Contains(body, `name="password"`) {
		t.Error("password login form disappeared")
	}
}

// TestEnterpriseSettingsSSOCard: the SSO configuration is for enterprise admins
// only, talks to the documented API and never pre-fills the client secret.
func TestEnterpriseSettingsSSOCard(t *testing.T) {
	h, err := NewHandler(nil, nil, zap.NewNop(), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	admin := renderEnterpriseSettings(t, h, "enterprise_admin")
	for _, want := range []string{
		`id="sso-card"`, `for="sso-issuer"`, `for="sso-client-id"`, `for="sso-client-secret"`, `type="password"`,
		`for="sso-domains"`, `id="sso-enabled"`, `id="sso-enforce"`, `id="sso-jit"`,
		`/api/enterprise/sso`, `/api/enterprise/sso/test`, `id="sso-redirect-uri"`,
	} {
		if !strings.Contains(admin, want) {
			t.Errorf("enterprise_admin settings page missing %q", want)
		}
	}
	if strings.Contains(admin, `id="sso-client-secret" class="form-input" value=`) {
		t.Error("the client secret field must never be pre-filled")
	}
	if member := renderEnterpriseSettings(t, h, "user"); strings.Contains(member, `id="sso-card"`) {
		t.Error("SSO card must not render for plain members")
	}
}

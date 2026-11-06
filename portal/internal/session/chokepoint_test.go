package session

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// repoRoot is the module root (this file lives in internal/session).
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// goFiles returns non-test Go files of the module's own code.
func goFiles(t *testing.T, includeTests bool) []string {
	t.Helper()
	root := repoRoot(t)
	var out []string
	for _, dir := range []string{"cmd", "internal", "pkg"} {
		_ = filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // unreadable dirs (e.g. uploads) are not code
			}
			if d.IsDir() || !strings.HasSuffix(p, ".go") {
				return nil
			}
			if !includeTests && strings.HasSuffix(p, "_test.go") {
				return nil
			}
			out = append(out, p)
			return nil
		})
	}
	return out
}

type call struct{ file, fn, name string }

// callsOf finds calls to <anything>.<method>(...) per enclosing function.
func callsOf(t *testing.T, method string) []call {
	t.Helper()
	root := repoRoot(t)
	var found []call
	for _, f := range goFiles(t, false) {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				ce, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if sel, ok := ce.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == method {
					rel, _ := filepath.Rel(root, f)
					found = append(found, call{file: filepath.ToSlash(rel), fn: fd.Name.Name, name: method})
				}
				return true
			})
		}
	}
	return found
}

func keys(cs []call) []string {
	set := map[string]bool{}
	for _, c := range cs {
		set[c.file+":"+c.fn] = true
	}
	var out []string
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestOnlyTheIssuerMintsSessionTokens: JWTs for user sessions are created in
// exactly one place, session.Issuer.Issue. If this fails, a new login path
// bypasses the MFA choke point; route it through Issuer.Issue instead.
func TestOnlyTheIssuerMintsSessionTokens(t *testing.T) {
	got := keys(callsOf(t, "Generate"))
	want := []string{"internal/session/session.go:Issue"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("jwt Manager.Generate callers = %v, want only %v", got, want)
	}
}

// TestLoginPathsEnumerated lists every function that asks the Issuer for a
// session. Adding a login path means consciously extending this list (and
// proving the new path cannot skip MFA). The SSO callback is listed with
// session.MethodSSO.
func TestLoginPathsEnumerated(t *testing.T) {
	got := keys(callsOf(t, "Issue"))
	want := []string{
		// MFA management refreshes the acting session after token_version bumps (MethodMFA).
		"internal/routes/mfa.go:refreshSession",
		// Password-verified flows (web login, API login, registration auto-login) share this.
		"internal/routes/auth_session.go:CompleteMFALogin",
		"internal/routes/auth_session.go:FinishPasswordLogin",
		// OIDC SSO callback (MethodSSO): only reached after state/cookie/PKCE, ID-token and
		// account-mapping checks in internal/routes/sso_login.go; the IdP is the first factor.
		"internal/routes/sso_login.go:ssoCallback",
	}
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Issuer.Issue callers changed:\n got  %v\n want %v\nreview the new login path for MFA bypass, then update this list", got, want)
	}

	// The three entry points that previously minted tokens now all end in
	// FinishPasswordLogin.
	callers := keys(callsOf(t, "FinishPasswordLogin"))
	wantCallers := []string{
		"internal/routes/auth.go:login",
		"internal/routes/auth.go:register",
		"internal/web/auth.go:HandleLogin",
		"internal/web/auth.go:HandleRegister",
	}
	sort.Strings(wantCallers)
	if strings.Join(callers, ",") != strings.Join(wantCallers, ",") {
		t.Fatalf("FinishPasswordLogin callers changed:\n got  %v\n want %v", callers, wantCallers)
	}
}

// TestPrivilegedMethodsAreConfined: MethodMFA and MethodSSO skip the password
// check, so only the audited files may reference them.
func TestPrivilegedMethodsAreConfined(t *testing.T) {
	allowed := map[string]bool{
		"internal/session/session.go":         true,
		"internal/routes/auth_session.go":     true, // MFA completion
		"internal/routes/mfa.go":              true, // refresh after MFA change
		"internal/routes/sso_login.go":        true, // OIDC SSO callback
		"internal/session/chokepoint_test.go": true,
	}
	root := repoRoot(t)
	for _, f := range goFiles(t, true) {
		rel, _ := filepath.Rel(root, f)
		rel = filepath.ToSlash(rel)
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		for _, needle := range []string{"session.MethodMFA", "session.MethodSSO"} {
			if strings.Contains(src, needle) && !allowed[rel] && !strings.HasSuffix(rel, "_test.go") {
				t.Errorf("%s references %s; only %v may (add the caller here deliberately)", rel, needle, allowed)
			}
		}
	}
}

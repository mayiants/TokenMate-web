package routes

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/sso"
	"github.com/ai-proxy/ai-portal-go/pkg/crypto"
	jwtlib "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func (e *ssoEnv) email(label string) string { return "mfa-" + e.suffix + "-" + label + "@" + e.domain }

func (e *ssoEnv) assertDenied(r ssoResult, why string) {
	e.t.Helper()
	assert.Empty(e.t, r.Session, "%s: no session may be issued", why)
	assert.True(e.t, strings.HasPrefix(r.Location, "/login?"), "%s: redirected to /login, got %q", why, r.Location)
}

// ---- happy paths ---------------------------------------------------------------

func TestSSO_JITProvisioningHappyPath(t *testing.T) {
	e := newSSOEnv(t)
	em := e.email("jit1")
	res := e.flow(e.entA, idpUser("sub-jit1", em))
	require.True(t, res.ok(), "%+v", res.Location)

	uid := e.userIDByEmail(em)
	require.NotEmpty(t, uid)
	assert.Equal(t, uid, e.sessionUser(res.Session), "the session belongs to the new user")

	var role, ent, hash string
	var verified bool
	require.NoError(t, e.d.Pool.QueryRow(context.Background(),
		`SELECT role, enterprise_id::text, password_hash, email_verified FROM users WHERE id = $1::uuid`, uid).Scan(&role, &ent, &hash, &verified))
	assert.Equal(t, "user", role, "JIT never grants an admin role")
	assert.Equal(t, e.entA, ent)
	assert.True(t, verified)
	assert.Error(t, bcrypt.CompareHashAndPassword([]byte(hash), []byte("")), "random unusable password")

	var memberRole string
	require.NoError(t, e.d.Pool.QueryRow(context.Background(),
		`SELECT role FROM enterprise_members WHERE enterprise_id = $1::uuid AND user_id = $2::uuid AND removed_at IS NULL`, e.entA, uid).Scan(&memberRole))
	assert.Equal(t, "member", memberRole)
	assert.Equal(t, 1, e.identityCount(em))

	// The password is unusable: nobody can log in with a guessable one.
	assert.Equal(t, http.StatusUnauthorized, e.do(http.MethodPost, "/api/auth/login", "", "",
		map[string]string{"email": em, "password": mfaTestPassword}).Code)

	assert.GreaterOrEqual(t, e.auditN("sso_user_provisioned", e.entA, 1), 1)
	assert.GreaterOrEqual(t, e.auditN("sso_login", e.entA, 1), 1)

	// Second login: matched through the identity, no second account.
	res2 := e.flow(e.entA, idpUser("sub-jit1", em))
	require.True(t, res2.ok())
	assert.Equal(t, uid, e.sessionUser(res2.Session))
}

func TestSSO_LinksExistingMemberAndThenUsesIdentity(t *testing.T) {
	e := newSSOEnv(t)
	u := e.member("linkme", "user", e.entA, "member")
	res := e.flow(e.entA, idpUser("sub-link", u.Email))
	require.True(t, res.ok())
	assert.Equal(t, u.ID, e.sessionUser(res.Session))
	assert.Equal(t, 1, e.identityCount(u.Email))
	assert.GreaterOrEqual(t, e.auditN("sso_identity_linked", e.entA, 1), 1)
	assert.Equal(t, 0, e.auditN("sso_user_provisioned", e.entA, 0), "an existing member is not re-provisioned")

	// Identity first: the IdP now reports a different (but allowed) address and
	// the person still lands in the same account.
	res2 := e.flow(e.entA, idpUser("sub-link", e.email("renamed")))
	require.True(t, res2.ok())
	assert.Equal(t, u.ID, e.sessionUser(res2.Session))
	assert.Empty(t, e.userIDByEmail(e.email("renamed")), "no second account for the new address")
}

func TestSSO_EnterpriseAdminMemberCanLinkButStaysEnterpriseAdmin(t *testing.T) {
	e := newSSOEnv(t)
	ea := e.member("ea", "enterprise_admin", e.entA, "admin")
	res := e.flow(e.entA, idpUser("sub-ea", ea.Email))
	require.True(t, res.ok())
	cl, err := e.mgr.Verify(res.Session)
	require.NoError(t, err)
	assert.Equal(t, "enterprise_admin", cl.Role)
}

func TestSSO_SessionSkipsLocalTOTP(t *testing.T) {
	e := newSSOEnv(t)
	u := e.member("totp", "user", e.entA, "member")
	e.enroll(u)
	// Password login still demands the second factor...
	assert.Equal(t, true, e.login(u, "").Body["mfa_required"])
	// ...while the IdP login (which authenticated the user) does not.
	res := e.flow(e.entA, idpUser("sub-totp", u.Email))
	require.True(t, res.ok())
	assert.Equal(t, u.ID, e.sessionUser(res.Session))
}

// ---- token validation ------------------------------------------------------------

func TestSSO_TokenValidationFailures(t *testing.T) {
	e := newSSOEnv(t)
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	cases := map[string]func(){
		"wrong nonce": func() { e.idp.Mutate = func(c jwtlib.MapClaims) { c["nonce"] = "attacker-nonce" } },
		"wrong aud":   func() { e.idp.Mutate = func(c jwtlib.MapClaims) { c["aud"] = "another-client" } },
		"wrong iss":   func() { e.idp.Mutate = func(c jwtlib.MapClaims) { c["iss"] = "https://evil.example" } },
		"expired": func() {
			e.idp.Mutate = func(c jwtlib.MapClaims) { c["exp"] = time.Now().Add(-10 * time.Minute).Unix() }
		},
		"azp mismatch": func() { e.idp.Mutate = func(c jwtlib.MapClaims) { c["azp"] = "someone-else" } },
		"alg none": func() {
			e.idp.Sign = func(p *ssoProv, c jwtlib.MapClaims) string { return p.SignNone(c) }
		},
		"hs256 confusion": func() {
			e.idp.Sign = func(p *ssoProv, c jwtlib.MapClaims) string { return p.SignHS256WithPublicKey(c) }
		},
		"foreign key": func() {
			e.idp.Sign = func(p *ssoProv, c jwtlib.MapClaims) string { return p.SignWithKey(c, other) }
		},
	}
	for name, setup := range cases {
		e.idp.Mutate, e.idp.Sign = nil, nil
		setup()
		em := e.email("tv-" + strings.ReplaceAll(name, " ", ""))
		res := e.flow(e.entA, idpUser("sub-"+name, em))
		e.assertDenied(res, name)
		assert.Empty(t, e.userIDByEmail(em), "%s: nothing may be provisioned", name)
	}
	assert.GreaterOrEqual(t, e.auditN("sso_failed", e.entA, len(cases)), len(cases))
	e.idp.Mutate, e.idp.Sign = nil, nil
}

func TestSSO_UnverifiedEmailRejected(t *testing.T) {
	e := newSSOEnv(t)
	for name, v := range map[string]interface{}{"false": false, "string false": "false", "absent": nil} {
		em := e.email("unv-" + strings.ReplaceAll(name, " ", ""))
		u := idpUser("sub-unv-"+name, em)
		u.EmailVerified = v
		e.assertDenied(e.flow(e.entA, u), name)
		assert.Empty(t, e.userIDByEmail(em), name)
	}
}

func TestSSO_DomainNotAllowed(t *testing.T) {
	e := newSSOEnv(t)
	res := e.flow(e.entA, idpUser("sub-dom", "someone@elsewhere.example"))
	e.assertDenied(res, "domain")
	assert.Empty(t, e.userIDByEmail("someone@elsewhere.example"))
	// Also for an existing identity: the domain policy always applies.
	u := e.member("domid", "user", e.entA, "member")
	require.True(t, e.flow(e.entA, idpUser("sub-domid", u.Email)).ok())
	e.assertDenied(e.flow(e.entA, idpUser("sub-domid", "moved@elsewhere.example")), "identity with disallowed domain")
}

// ---- account takeover ----------------------------------------------------------------

func TestSSO_NoTakeoverOfOtherAccounts(t *testing.T) {
	e := newSSOEnv(t)
	otherEnt := e.member("othent", "user", e.entB, "member")
	standalone := e.user("standalone", "user", "")
	superAdmin := e.user("superadm", "super_admin", "")
	sysAdmin := e.user("sysadm", "system_admin", "")
	otherAdmin := e.member("othadm", "enterprise_admin", e.entB, "admin")
	for name, u := range map[string]*mfaUser{
		"user of another enterprise": otherEnt, "standalone user": standalone,
		"super_admin": superAdmin, "system_admin": sysAdmin, "enterprise_admin of another enterprise": otherAdmin,
	} {
		res := e.flow(e.entA, idpUser("sub-take-"+u.ID[:8], u.Email))
		e.assertDenied(res, name)
		assert.Zero(t, e.identityCount(u.Email), "%s: must not be linked", name)
	}
	// Their records are untouched.
	var ent *string
	require.NoError(t, e.d.Pool.QueryRow(context.Background(), `SELECT enterprise_id::text FROM users WHERE id = $1::uuid`, otherEnt.ID).Scan(&ent))
	require.NotNil(t, ent)
	assert.Equal(t, e.entB, *ent)
}

func TestSSO_RemovedOrInactiveMemberCannotLogin(t *testing.T) {
	e := newSSOEnv(t)
	u := e.member("removed", "user", e.entA, "member")
	require.True(t, e.flow(e.entA, idpUser("sub-removed", u.Email)).ok())
	e.sqlExec(`UPDATE enterprise_members SET removed_at = NOW() WHERE user_id = $1::uuid`, u.ID)
	e.assertDenied(e.flow(e.entA, idpUser("sub-removed", u.Email)), "removed member")
	e.sqlExec(`UPDATE enterprise_members SET removed_at = NULL, status = 'inactive' WHERE user_id = $1::uuid`, u.ID)
	e.assertDenied(e.flow(e.entA, idpUser("sub-removed", u.Email)), "inactive member")
}

func TestSSO_IdentityBoundToOtherSubjectIsNotRebound(t *testing.T) {
	e := newSSOEnv(t)
	u := e.member("rebind", "user", e.entA, "member")
	require.True(t, e.flow(e.entA, idpUser("sub-original", u.Email)).ok())
	// A different IdP subject asserting the same e-mail must not hijack the account.
	e.assertDenied(e.flow(e.entA, idpUser("sub-impostor", u.Email)), "rebinding")
}

func TestSSO_IdentityOfAnotherEnterpriseIsRejected(t *testing.T) {
	e := newSSOEnv(t)
	u := e.member("xident", "user", e.entB, "member")
	e.sqlExec(`INSERT INTO user_identities (user_id, enterprise_id, issuer, subject, email) VALUES ($1::uuid, $2::uuid, $3, 'sub-x', $4)`,
		u.ID, e.entB, e.idp.Issuer, u.Email)
	e.assertDenied(e.flow(e.entA, idpUser("sub-x", u.Email)), "identity bound to enterprise B")
}

func TestSSO_SuspendedAndPendingDeletionUsersRejected(t *testing.T) {
	e := newSSOEnv(t)
	susp := e.member("susp", "user", e.entA, "member")
	pend := e.member("pend", "user", e.entA, "member")
	linkedSusp := e.member("linkedsusp", "user", e.entA, "member")
	require.True(t, e.flow(e.entA, idpUser("sub-linkedsusp", linkedSusp.Email)).ok())

	e.sqlExec(`UPDATE users SET status = 'suspended' WHERE id = ANY($1::uuid[])`, []string{susp.ID, linkedSusp.ID})
	e.sqlExec(`UPDATE users SET gdpr_erasure_requested_at = NOW() WHERE id = $1::uuid`, pend.ID)

	e.assertDenied(e.flow(e.entA, idpUser("sub-susp", susp.Email)), "suspended, unlinked")
	e.assertDenied(e.flow(e.entA, idpUser("sub-linkedsusp", linkedSusp.Email)), "suspended, linked")
	e.assertDenied(e.flow(e.entA, idpUser("sub-pend", pend.Email)), "pending deletion")
	assert.Zero(t, e.identityCount(susp.Email), "a suspended user is not linked")
	assert.Zero(t, e.identityCount(pend.Email))
}

// ---- JIT policy -----------------------------------------------------------------------

func TestSSO_JITPolicy(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		e := newSSOEnv(t)
		e.configure(e.entA, true, false, false)
		em := e.email("nojit")
		e.assertDenied(e.flow(e.entA, idpUser("sub-nojit", em)), "jit off")
		assert.Empty(t, e.userIDByEmail(em))
		// Existing members still link when JIT is off.
		u := e.member("nojitmember", "user", e.entA, "member")
		assert.True(t, e.flow(e.entA, idpUser("sub-nojitm", u.Email)).ok())
	})
	t.Run("domain not approved by the platform", func(t *testing.T) {
		e := newSSOEnv(t)
		e.sqlExec(`UPDATE enterprises SET allowed_domains = 'only-this.example' WHERE id = $1::uuid`, e.entA)
		em := e.email("unapproved")
		e.assertDenied(e.flow(e.entA, idpUser("sub-unapproved", em)), "domain not in enterprises.allowed_domains")
		assert.Empty(t, e.userIDByEmail(em))
	})
	t.Run("seat limit", func(t *testing.T) {
		e := newSSOEnv(t)
		e.member("seat1", "user", e.entA, "member")
		e.sqlExec(`UPDATE enterprises SET max_members = 1 WHERE id = $1::uuid`, e.entA)
		em := e.email("noseat")
		e.assertDenied(e.flow(e.entA, idpUser("sub-noseat", em)), "no free seat")
		assert.Empty(t, e.userIDByEmail(em))
	})
	t.Run("admin default_role in the database is never honoured", func(t *testing.T) {
		e := newSSOEnv(t)
		e.sqlExec(`UPDATE enterprise_sso_configs SET default_role = 'admin' WHERE enterprise_id = $1::uuid`, e.entA)
		em := e.email("adminrole")
		e.assertDenied(e.flow(e.entA, idpUser("sub-adminrole", em)), "admin default role")
		assert.Empty(t, e.userIDByEmail(em))
	})
	t.Run("disabled config", func(t *testing.T) {
		e := newSSOEnv(t)
		e.configure(e.entA, false, false, true)
		w := e.raw(http.MethodGet, "/auth/sso/start?enterprise="+e.entA, nil)
		assert.Equal(t, http.StatusFound, w.Code)
		assert.True(t, strings.HasPrefix(w.Header().Get("Location"), "/login?"))
	})
}

// ---- state, PKCE, binding ----------------------------------------------------------------

func TestSSO_StateIsSingleUse(t *testing.T) {
	e := newSSOEnv(t)
	u := e.member("replay", "user", e.entA, "member")
	sw, authURL, bind := e.start(e.entA)
	cb, err := e.idp.Authorize(authURL, idpUser("sub-replay", u.Email))
	require.NoError(t, err)
	first := e.finish(sw, cb, bind)
	require.True(t, first.ok())
	// Same URL, same cookie again.
	e.assertDenied(e.finish(sw, cb, bind), "replayed callback")
	assert.Equal(t, 1, e.identityCount(u.Email))
}

func TestSSO_StateMustBeBoundToTheBrowser(t *testing.T) {
	e := newSSOEnv(t)
	u := e.member("bound", "user", e.entA, "member")

	// Login CSRF: the attacker completes a login at the IdP and tricks the victim's
	// browser into delivering the callback - without the cookie that start set.
	sw, authURL, _ := e.start(e.entA)
	cb, _ := e.idp.Authorize(authURL, idpUser("sub-bound", u.Email))
	e.assertDenied(e.finish(sw, cb, nil), "no binding cookie")

	// A binding cookie from a different flow does not help.
	_, _, bind2 := e.start(e.entA)
	sw1, authURL1, _ := e.start(e.entA)
	cb1, _ := e.idp.Authorize(authURL1, idpUser("sub-bound", u.Email))
	e.assertDenied(e.finish(sw1, cb1, bind2), "cookie of another flow")

	// Forged cookie (valid state, wrong MAC).
	sw3, authURL3, bind3 := e.start(e.entA)
	cb3, _ := e.idp.Authorize(authURL3, idpUser("sub-bound", u.Email))
	forged := *bind3
	forged.Value = strings.SplitN(bind3.Value, ".", 2)[0] + ".00ff"
	e.assertDenied(e.finish(sw3, cb3, &forged), "forged cookie")

	// Unknown state.
	_, _, bind4 := e.start(e.entA)
	w := e.callback(ssoTestBaseURL+"/auth/sso/callback?code=abc&state=not-a-real-state", bind4)
	assert.True(t, strings.HasPrefix(w.Header().Get("Location"), "/login?"))
	assert.Zero(t, e.identityCount(u.Email))
}

func TestSSO_StateExpires(t *testing.T) {
	e := newSSOEnv(t)
	u := e.member("expire", "user", e.entA, "member")
	sw, authURL, bind := e.start(e.entA)
	cb, _ := e.idp.Authorize(authURL, idpUser("sub-expire", u.Email))
	e.mr.FastForward(sso.StateTTL + time.Second)
	e.assertDenied(e.finish(sw, cb, bind), "state past its TTL")
}

func TestSSO_PKCEMismatchFails(t *testing.T) {
	e := newSSOEnv(t)
	u := e.member("pkce", "user", e.entA, "member")
	sw, authURL, bind := e.start(e.entA)
	cb, _ := e.idp.Authorize(authURL, idpUser("sub-pkce", u.Email))
	e.idp.ChangeChallenge() // the provider now expects a different verifier
	e.assertDenied(e.finish(sw, cb, bind), "pkce mismatch")
	assert.Zero(t, e.identityCount(u.Email))
}

func TestSSO_AuthorizationRequestShape(t *testing.T) {
	e := newSSOEnv(t)
	// A hostile Host header must not influence the redirect URI.
	w := e.raw(http.MethodGet, "/auth/sso/start?enterprise="+e.entA, map[string]string{"Host": "evil.test", "X-Forwarded-Host": "evil.test"})
	require.Equal(t, http.StatusFound, w.Code)
	loc := w.Header().Get("Location")
	assert.Contains(t, loc, "redirect_uri=https%3A%2F%2Fportal.test%2Fauth%2Fsso%2Fcallback")
	assert.NotContains(t, loc, "evil.test")
	assert.Contains(t, loc, "code_challenge_method=S256")
	assert.Contains(t, loc, "response_type=code")
	assert.Contains(t, loc, "nonce=")
	assert.Contains(t, loc, "scope=openid+email+profile")

	ck := cookieFrom(w, sso.CookieName)
	require.NotNil(t, ck)
	assert.True(t, ck.HttpOnly)
	assert.True(t, ck.Secure, "secure because the public base URL is https")
	assert.Equal(t, http.SameSiteLaxMode, ck.SameSite)
	assert.Equal(t, "/auth/sso", ck.Path)
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))

	// The PKCE verifier and nonce are never in the URL; only the challenge is.
	var pending sso.Pending
	for _, k := range e.mr.Keys() {
		if strings.HasPrefix(k, "sso:state:") {
			raw, _ := e.mr.Get(k)
			require.NoError(t, jsonUnmarshal(raw, &pending))
		}
	}
	require.NotEmpty(t, pending.Verifier)
	assert.NotContains(t, loc, pending.Verifier)
	assert.Contains(t, loc, sso.PKCEChallenge(pending.Verifier))
	assert.Equal(t, int(sso.StateTTL.Seconds()), ssoTTLSeconds(e, "sso:state:"))
}

func TestSSO_InsecureBaseURLIsRefused(t *testing.T) {
	e := newSSOEnv(t)
	e.ar.cfg.App.BaseURL = "http://portal.test"
	w := e.raw(http.MethodGet, "/auth/sso/start?enterprise="+e.entA, nil)
	assert.True(t, strings.HasPrefix(w.Header().Get("Location"), "/login?"), "plain-http callback URLs are refused by default")
}

// ---- SSRF through stored configuration ---------------------------------------------------------

func TestSSO_StartRefusesInternalIssuers(t *testing.T) {
	// A fresh client with the real (guarded) transport.
	e := newSSOEnv(t)
	e.ar.SetSSOClient(sso.NewClient(sso.Options{}))
	for _, issuer := range []string{
		"https://169.254.169.254", "https://127.0.0.1:8443", "https://10.0.0.5", "https://localhost",
		"http://169.254.169.254/latest/meta-data", "https://[::1]",
	} {
		e.sqlExec(`UPDATE enterprise_sso_configs SET issuer = $2 WHERE enterprise_id = $1::uuid`, e.entA, issuer)
		w := e.raw(http.MethodGet, "/auth/sso/start?enterprise="+e.entA, nil)
		assert.Equal(t, http.StatusFound, w.Code, issuer)
		assert.True(t, strings.HasPrefix(w.Header().Get("Location"), "/login?"), "%s must not start a flow", issuer)
		assert.Nil(t, cookieFrom(w, sso.CookieName), issuer)
	}
}

// ---- enforce_sso ------------------------------------------------------------------------------------

func TestSSO_EnforceBlocksMemberPasswordLoginButNotAdmins(t *testing.T) {
	e := newSSOEnv(t)
	member := e.member("enf-member", "user", e.entA, "member")
	ea := e.member("enf-ea", "enterprise_admin", e.entA, "admin")
	sa := e.user("enf-sa", "super_admin", "")
	outsider := e.user("enf-out", "user", "")
	otherEntMember := e.member("enf-b", "user", e.entB, "member")

	// Before enforcement everyone can use the password.
	assert.Equal(t, http.StatusOK, e.login(member, "").Code)

	e.configure(e.entA, true, true, true)
	r := e.login(member, "")
	assert.Equal(t, http.StatusForbidden, r.Code)
	assert.Equal(t, "sso_required", r.errCode())
	assert.Empty(t, r.str("token"))
	msg, _ := r.Body["error"].(map[string]interface{})["message"].(string)
	assert.Contains(t, strings.ToLower(msg), "single sign-on", "the message points at SSO")

	// Break-glass and unaffected accounts keep password login.
	for name, u := range map[string]*mfaUser{"enterprise_admin": ea, "super_admin": sa, "standalone user": outsider, "other enterprise": otherEntMember} {
		got := e.login(u, "")
		assert.Equal(t, http.StatusOK, got.Code, name)
		assert.NotEmpty(t, got.str("token"), name)
	}

	// The same member can still sign in through SSO.
	res := e.flow(e.entA, idpUser("sub-enf", member.Email))
	require.True(t, res.ok())

	// Enforcement only applies while SSO is enabled (no lock-out by a disabled setup).
	e.configure(e.entA, false, true, true)
	assert.Equal(t, http.StatusOK, e.login(member, "").Code)
}

func TestSSO_PasswordResetDoesNotBypassEnforcement(t *testing.T) {
	e := newSSOEnv(t)
	member := e.member("enf-reset", "user", e.entA, "member")
	e.configure(e.entA, true, true, true)

	require.NoError(t, e.d.Redis.Set(context.Background(), "verification:reset:"+member.Email, "123456", time.Minute).Err())
	newPw := "Br4nd-New-Passw0rd!"
	reset := e.do(http.MethodPost, "/api/auth/reset-password", "", "",
		map[string]string{"email": member.Email, "code": "123456", "password": newPw})
	require.Equal(t, http.StatusOK, reset.Code, reset.Raw)
	assert.Empty(t, reset.str("token"), "reset never logs the user in")

	r := e.do(http.MethodPost, "/api/auth/login", "", "", map[string]string{"email": member.Email, "password": newPw})
	assert.Equal(t, http.StatusForbidden, r.Code)
	assert.Equal(t, "sso_required", r.errCode())
	assert.Empty(t, r.str("token"))
}

func TestSSO_FinishPasswordLoginIsABackstop(t *testing.T) {
	e := newSSOEnv(t)
	member := e.member("enf-backstop", "user", e.entA, "member")
	e.configure(e.entA, true, true, true)
	c, _ := newTestGinContext()
	_, err := e.ar.FinishPasswordLogin(c, member.ID, sessionOptionsNone())
	assert.ErrorIs(t, err, ErrSSORequired)
}

// ---- discovery ----------------------------------------------------------------------------------------

func TestSSO_DiscoverHasUniformShape(t *testing.T) {
	e := newSSOEnv(t)
	shape := func(r resp) []string {
		var keys []string
		for k := range r.Body {
			keys = append(keys, k)
		}
		return keys
	}
	known := e.do(http.MethodPost, "/api/auth/sso/discover", "", "", map[string]string{"email": "anyone@" + e.domain})
	require.Equal(t, http.StatusOK, known.Code)
	assert.Equal(t, true, known.Body["sso"])
	assert.Equal(t, "/auth/sso/start?enterprise="+e.entA, known.Body["start_url"])

	// Whether the account exists is irrelevant, and the same for unknown domains
	// and junk: always 200 with the same two keys.
	for name, body := range map[string]interface{}{
		"unknown domain": map[string]string{"email": "x@unknown.example"},
		"malformed":      map[string]string{"email": "not-an-email"},
		"empty":          map[string]string{"email": ""},
		"wrong type":     map[string]int{"email": 5},
	} {
		r := e.do(http.MethodPost, "/api/auth/sso/discover", "", "", body)
		assert.Equal(t, http.StatusOK, r.Code, name)
		assert.Equal(t, false, r.Body["sso"], name)
		assert.Equal(t, "", r.Body["start_url"], name)
		assert.ElementsMatch(t, shape(known), shape(r), name)
	}
	ghost := e.do(http.MethodPost, "/api/auth/sso/discover", "", "", map[string]string{"email": "ghost-user-that-does-not-exist@" + e.domain})
	assert.Equal(t, known.Raw, ghost.Raw, "existing and non-existing users are indistinguishable")

	// Disabled, and ambiguous domains, offer nothing.
	e.configure(e.entA, false, false, true)
	assert.Equal(t, false, e.do(http.MethodPost, "/api/auth/sso/discover", "", "", map[string]string{"email": "a@" + e.domain}).Body["sso"])
	e.configure(e.entA, true, false, true)
	e.configure(e.entB, true, false, true) // same domain on purpose, inserted directly
	assert.Equal(t, false, e.do(http.MethodPost, "/api/auth/sso/discover", "", "", map[string]string{"email": "a@" + e.domain}).Body["sso"],
		"two enterprises claiming one domain: nobody is redirected")
}

// ---- management API ----------------------------------------------------------------------------------------

func (e *ssoEnv) sso(method, token string, body interface{}, query string) resp {
	return e.do(method, "/api/enterprise/sso"+query, token, "", body)
}

func (e *ssoEnv) auditCountByAction(action, ent string) int {
	e.t.Helper()
	var n int
	deadline := time.Now().Add(5 * time.Second)
	for {
		require.NoError(e.t, e.d.Pool.QueryRow(context.Background(),
			`SELECT COUNT(*) FROM audit_logs WHERE action = $1 AND target_id = $2`, action, ent).Scan(&n))
		if n > 0 || time.Now().After(deadline) {
			return n
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestSSOManagementAPI_PermissionMatrix(t *testing.T) {
	e := newSSOEnv(t)
	member := e.member("mgmt-member", "user", e.entA, "member")
	ea := e.member("mgmt-ea", "enterprise_admin", e.entA, "admin")
	eaB := e.member("mgmt-eab", "enterprise_admin", e.entB, "admin")
	sa := e.user("mgmt-sa", "super_admin", "")
	sys := e.user("mgmt-sys", "system_admin", "")
	tok := map[string]string{
		"member": e.asUser(member), "ea": e.asUser(ea), "eaB": e.asUser(eaB), "sa": e.asUser(sa), "sys": e.asUser(sys),
	}
	body := map[string]interface{}{"scopes": "openid email"}
	q := "?enterprise_id=" + e.entA

	cases := []struct {
		name, method, token, query string
		body                       interface{}
		want                       int
	}{
		{"anonymous GET", http.MethodGet, "", "", nil, http.StatusUnauthorized},
		{"member GET", http.MethodGet, tok["member"], "", nil, http.StatusForbidden},
		{"member PUT", http.MethodPut, tok["member"], "", body, http.StatusForbidden},
		{"member DELETE", http.MethodDelete, tok["member"], "", nil, http.StatusForbidden},
		{"member TEST", http.MethodPost, tok["member"], "/test", nil, http.StatusForbidden},
		{"system_admin GET", http.MethodGet, tok["sys"], q, nil, http.StatusForbidden},
		{"other enterprise's admin GET", http.MethodGet, tok["eaB"], q, nil, http.StatusForbidden},
		{"other enterprise's admin PUT", http.MethodPut, tok["eaB"], "", map[string]interface{}{"enterprise_id": e.entA, "scopes": "openid"}, http.StatusForbidden},
		{"other enterprise's admin DELETE", http.MethodDelete, tok["eaB"], q, nil, http.StatusForbidden},
		{"other enterprise's admin TEST", http.MethodPost, tok["eaB"], "/test" + q, nil, http.StatusForbidden},
		{"own admin GET", http.MethodGet, tok["ea"], "", nil, http.StatusOK},
		{"own admin GET explicit id", http.MethodGet, tok["ea"], q, nil, http.StatusOK},
		{"own admin PUT", http.MethodPut, tok["ea"], "", body, http.StatusOK},
		{"own admin TEST", http.MethodPost, tok["ea"], "/test", nil, http.StatusOK},
		{"super_admin without enterprise", http.MethodGet, tok["sa"], "", nil, http.StatusBadRequest},
		{"super_admin GET", http.MethodGet, tok["sa"], q, nil, http.StatusOK},
		{"super_admin PUT", http.MethodPut, tok["sa"], "", map[string]interface{}{"enterprise_id": e.entA, "scopes": "openid profile"}, http.StatusOK},
		{"super_admin unknown enterprise", http.MethodGet, tok["sa"], "?enterprise_id=00000000-0000-0000-0000-000000000099", nil, http.StatusNotFound},
		{"super_admin bad id", http.MethodGet, tok["sa"], "?enterprise_id=nope", nil, http.StatusBadRequest},
	}
	for _, tc := range cases {
		got := e.sso(tc.method, tc.token, tc.body, tc.query)
		assert.Equal(t, tc.want, got.Code, "%s: %s", tc.name, got.Raw)
	}
	// Denied calls changed nothing: the last permitted PUT (super_admin) is what is stored.
	cfg, err := loadSSOConfig(context.Background(), e.d, e.entA)
	require.NoError(t, err)
	assert.Equal(t, "openid profile", cfg.Scopes)

	// DELETE by the own admin works, and the audit trail has it.
	del := e.sso(http.MethodDelete, tok["ea"], nil, "")
	assert.Equal(t, http.StatusOK, del.Code)
	assert.Equal(t, true, del.Body["deleted"])
	gone, _ := loadSSOConfig(context.Background(), e.d, e.entA)
	assert.Nil(t, gone)
	assert.GreaterOrEqual(t, e.auditCountByAction("sso_config_deleted", e.entA), 1)
	assert.GreaterOrEqual(t, e.auditCountByAction("sso_config_updated", e.entA), 1)
}

func TestSSOManagementAPI_SecretIsNeverReturnedAndIsEncrypted(t *testing.T) {
	e := newSSOEnv(t)
	ea := e.member("sec-ea", "enterprise_admin", e.entA, "admin")
	token := e.asUser(ea)
	const secret = "TOP-SECRET-client-secret-value-123"

	put := e.sso(http.MethodPut, token, map[string]interface{}{
		"issuer": e.idp.Issuer, "client_id": "cid-1", "client_secret": secret, "enabled": true,
		"allowed_email_domains": []string{"Example.COM", "@example.org"},
	}, "")
	require.Equal(t, http.StatusOK, put.Code, put.Raw)
	assert.NotContains(t, put.Raw, secret)
	view := mapOf(put.Body, "sso")
	assert.Equal(t, true, view["client_secret_set"])
	assert.NotContains(t, view, "client_secret")
	assert.NotContains(t, view, "client_secret_enc")
	assert.Equal(t, []interface{}{"example.com", "example.org"}, view["allowed_email_domains"], "domains are normalised")
	assert.Equal(t, ssoTestBaseURL+"/auth/sso/callback", view["redirect_uri"])

	get := e.sso(http.MethodGet, token, nil, "")
	assert.NotContains(t, get.Raw, secret)
	assert.NotContains(t, get.Raw, "client_secret_enc")
	assert.Equal(t, true, get.Body["client_secret_set"])

	// At rest it is AES-GCM ciphertext that decrypts with ENCRYPTION_KEY.
	var enc string
	require.NoError(t, e.d.Pool.QueryRow(context.Background(),
		`SELECT client_secret_enc FROM enterprise_sso_configs WHERE enterprise_id = $1::uuid`, e.entA).Scan(&enc))
	assert.NotContains(t, enc, secret)
	plain, err := crypto.Decrypt(enc, e.ar.cfg.Auth.EncryptionKey)
	require.NoError(t, err)
	assert.Equal(t, secret, plain)

	// A PUT without client_secret keeps it; an empty one does too.
	keep := e.sso(http.MethodPut, token, map[string]interface{}{"scopes": "openid email", "client_secret": ""}, "")
	require.Equal(t, http.StatusOK, keep.Code)
	var enc2 string
	require.NoError(t, e.d.Pool.QueryRow(context.Background(),
		`SELECT client_secret_enc FROM enterprise_sso_configs WHERE enterprise_id = $1::uuid`, e.entA).Scan(&enc2))
	assert.Equal(t, enc, enc2)

	// The audit trail records that it changed, never what it is.
	assert.GreaterOrEqual(t, e.auditCountByAction("sso_config_updated", e.entA), 1)
	var leaked int
	require.NoError(t, e.d.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM audit_logs WHERE (new_value::text LIKE '%' || $1 || '%' OR old_value::text LIKE '%' || $1 || '%' OR message LIKE '%' || $1 || '%' OR details::text LIKE '%' || $1 || '%')`,
		secret).Scan(&leaked))
	assert.Zero(t, leaked, "the secret must never reach the audit log")

	// And decryption works for a real login with the provider's actual secret.
	u := e.member("sec-login", "user", e.entA, "member")
	e.configure(e.entA, true, false, true)
	assert.True(t, e.flow(e.entA, idpUser("sub-sec", u.Email)).ok())
}

func TestSSOManagementAPI_Validation(t *testing.T) {
	e := newSSOEnv(t)
	ea := e.member("val-ea", "enterprise_admin", e.entA, "admin")
	token := e.asUser(ea)
	put := func(body map[string]interface{}) resp { return e.sso(http.MethodPut, token, body, "") }

	assert.Equal(t, http.StatusBadRequest, put(map[string]interface{}{"issuer": "http://idp.example.com"}).Code, "plain http")
	for _, bad := range []string{"https://127.0.0.1", "https://169.254.169.254", "https://10.0.0.1", "https://localhost", "https://[::1]", "ftp://x.example.com", "https://u:p@idp.example.com"} {
		assert.Equal(t, http.StatusBadRequest, put(map[string]interface{}{"issuer": bad}).Code, bad)
	}
	for _, role := range []string{"enterprise_admin", "super_admin", "admin", "system_admin"} {
		assert.Equal(t, http.StatusBadRequest, put(map[string]interface{}{"default_role": role}).Code, role)
	}
	assert.Equal(t, http.StatusBadRequest, put(map[string]interface{}{"allowed_email_domains": []string{"gmail.com"}}).Code, "public mail provider")
	assert.Equal(t, http.StatusBadRequest, put(map[string]interface{}{"allowed_email_domains": []string{"not a domain"}}).Code)
	assert.Equal(t, http.StatusBadRequest, put(map[string]interface{}{"scopes": "email profile"}).Code, "openid is mandatory")
	assert.Equal(t, http.StatusBadRequest, put(map[string]interface{}{"enforce_sso": true, "enabled": false}).Code)

	// Enabling a half-configured setup is refused.
	e.sqlExec(`DELETE FROM enterprise_sso_configs WHERE enterprise_id = $1::uuid`, e.entA)
	assert.Equal(t, http.StatusBadRequest, put(map[string]interface{}{"enabled": true, "issuer": e.idp.Issuer, "client_id": "c"}).Code, "no secret, no domain")

	// A domain is exclusive among enabled enterprises.
	full := func(domain string) map[string]interface{} {
		return map[string]interface{}{"enabled": true, "issuer": e.idp.Issuer, "client_id": "c", "client_secret": "s", "allowed_email_domains": []string{domain}}
	}
	assert.Equal(t, http.StatusOK, put(full("claimed.example")).Code)
	eaB := e.member("val-eab", "enterprise_admin", e.entB, "admin")
	clash := e.sso(http.MethodPut, e.asUser(eaB), full("claimed.example"), "")
	assert.Equal(t, http.StatusConflict, clash.Code)
	assert.Equal(t, http.StatusOK, e.sso(http.MethodPut, e.asUser(eaB), full("unclaimed.example"), "").Code)
}

func TestSSOManagementAPI_TestEndpoint(t *testing.T) {
	e := newSSOEnv(t)
	ea := e.member("tst-ea", "enterprise_admin", e.entA, "admin")
	token := e.asUser(ea)

	ok := e.sso(http.MethodPost, token, nil, "/test")
	require.Equal(t, http.StatusOK, ok.Code, ok.Raw)
	assert.Equal(t, true, ok.Body["ok"])
	assert.EqualValues(t, 1, ok.Body["jwks_keys"])
	assert.Equal(t, true, ok.Body["pkce_s256"])
	assert.Zero(t, e.idp.TokenCalls, "the test never touches the token endpoint")

	// Blocked targets: ok=false with a fixed code, not network error text.
	e.ent.SetSSOClient(sso.NewClient(sso.Options{}))
	for issuer, code := range map[string]string{
		"https://169.254.169.254": "address_not_allowed", "https://127.0.0.1:9": "address_not_allowed",
		"http://idp.example.com": "https_required",
	} {
		e.sqlExec(`UPDATE enterprise_sso_configs SET issuer = $2 WHERE enterprise_id = $1::uuid`, e.entA, issuer)
		r := e.sso(http.MethodPost, token, nil, "/test")
		assert.Equal(t, http.StatusOK, r.Code)
		assert.Equal(t, false, r.Body["ok"], issuer)
		assert.Equal(t, code, r.Body["error"], issuer)
	}

	e.sqlExec(`DELETE FROM enterprise_sso_configs WHERE enterprise_id = $1::uuid`, e.entA)
	assert.Equal(t, http.StatusNotFound, e.sso(http.MethodPost, token, nil, "/test").Code)
}

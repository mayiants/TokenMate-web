package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/internal/session"
	"github.com/mayiants/ai-portal-go/pkg/crypto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const bad401Message = "Invalid or expired verification code"

// ---- login flow ------------------------------------------------------------

func TestMFALogin_PasswordAloneIssuesNoSession(t *testing.T) {
	e := newMFAEnv(t)
	u := e.user("login1", "user", "")
	e.enroll(u)

	r := e.login(u, "")
	require.Equal(t, http.StatusOK, r.Code, r.Raw)
	assert.Equal(t, true, r.Body["mfa_required"])
	assert.NotEmpty(t, r.str("mfa_token"))
	_, hasToken := r.Body["token"]
	assert.False(t, hasToken, "a password-only login must not return a session token")
	_, hasUser := r.Body["user"]
	assert.False(t, hasUser)

	// Wrong code: generic 401, no token.
	bad := e.do(http.MethodPost, "/api/auth/login/mfa", "", "", map[string]string{"mfa_token": r.str("mfa_token"), "code": "000000"})
	assert.Equal(t, http.StatusUnauthorized, bad.Code)
	assert.Empty(t, bad.str("token"))

	// Right code: session issued, and it actually works.
	e.advance()
	ok := e.do(http.MethodPost, "/api/auth/login/mfa", "", "", map[string]string{"mfa_token": r.str("mfa_token"), "code": u.code(e, 0)})
	require.Equal(t, http.StatusOK, ok.Code, ok.Raw)
	require.NotEmpty(t, ok.str("token"))
	assert.Equal(t, http.StatusOK, e.do(http.MethodGet, "/api/gated", ok.str("token"), "", nil).Code)
}

func TestMFALogin_PendingTokenIsSingleUse(t *testing.T) {
	e := newMFAEnv(t)
	u := e.user("single", "user", "")
	e.enroll(u)

	r := e.login(u, "")
	tok := r.str("mfa_token")
	e.advance()
	first := e.do(http.MethodPost, "/api/auth/login/mfa", "", "", map[string]string{"mfa_token": tok, "code": u.code(e, 0)})
	require.Equal(t, http.StatusOK, first.Code, first.Raw)

	// Same token again with a *different*, valid code: the token is gone.
	e.advance()
	second := e.do(http.MethodPost, "/api/auth/login/mfa", "", "", map[string]string{"mfa_token": tok, "code": u.code(e, 0)})
	assert.Equal(t, http.StatusUnauthorized, second.Code)
	assert.Empty(t, second.str("token"))
}

func TestMFALogin_PendingTokenBoundToClientIP(t *testing.T) {
	e := newMFAEnv(t)
	u := e.user("ipbind", "user", "")
	e.enroll(u)

	r := e.login(u, "198.51.100.1")
	tok := r.str("mfa_token")
	e.advance()
	code := u.code(e, 0)

	// Presented from another address: rejected even with a correct code...
	other := e.do(http.MethodPost, "/api/auth/login/mfa", "", "198.51.100.2", map[string]string{"mfa_token": tok, "code": code})
	assert.Equal(t, http.StatusUnauthorized, other.Code)
	assert.Empty(t, other.str("token"))

	// ...and the token is destroyed, so the original address cannot use it either.
	orig := e.do(http.MethodPost, "/api/auth/login/mfa", "", "198.51.100.1", map[string]string{"mfa_token": tok, "code": code})
	assert.Equal(t, http.StatusUnauthorized, orig.Code)

	// A fresh password login from the right address still works.
	assert.NotEmpty(t, e.fullLogin(u, "198.51.100.1"))
}

func TestMFALogin_PendingTokenExpiresAfterFiveMinutes(t *testing.T) {
	e := newMFAEnv(t)
	u := e.user("expiry", "user", "")
	e.enroll(u)
	r := e.login(u, "")
	e.mr.FastForward(session.ChallengeTTL + time.Second)
	e.advance()
	late := e.do(http.MethodPost, "/api/auth/login/mfa", "", "", map[string]string{"mfa_token": r.str("mfa_token"), "code": u.code(e, 0)})
	assert.Equal(t, http.StatusUnauthorized, late.Code)
}

func TestMFALogin_TOTPReplayRejected(t *testing.T) {
	e := newMFAEnv(t)
	u := e.user("replay", "user", "")
	e.enroll(u)

	e.advance()
	code := u.code(e, 0)
	r1 := e.login(u, "")
	ok := e.do(http.MethodPost, "/api/auth/login/mfa", "", "", map[string]string{"mfa_token": r1.str("mfa_token"), "code": code})
	require.Equal(t, http.StatusOK, ok.Code, ok.Raw)

	// A second, legitimate login with the very same (still time-valid) code is a replay.
	r2 := e.login(u, "")
	replay := e.do(http.MethodPost, "/api/auth/login/mfa", "", "", map[string]string{"mfa_token": r2.str("mfa_token"), "code": code})
	assert.Equal(t, http.StatusUnauthorized, replay.Code)

	// The previous step (inside the skew window) is rejected as well.
	older := e.do(http.MethodPost, "/api/auth/login/mfa", "", "", map[string]string{"mfa_token": r2.str("mfa_token"), "code": u.code(e, -1)})
	assert.Equal(t, http.StatusUnauthorized, older.Code)

	// The next step is fine.
	next := e.do(http.MethodPost, "/api/auth/login/mfa", "", "", map[string]string{"mfa_token": r2.str("mfa_token"), "code": u.code(e, 1)})
	assert.Equal(t, http.StatusOK, next.Code, next.Raw)
}

func TestMFALogin_RateLimitLocksOutAfterFiveFailures(t *testing.T) {
	e := newMFAEnv(t)
	u := e.user("lockout", "user", "")
	e.enroll(u)
	e.advance()
	good := u.code(e, 0)
	ctx := context.Background()

	// The per-user limiter is driven through the service: the HTTP route also has
	// the older per-IP limiter (5 failures -> 429), which would mask it here.
	for i := 0; i < 5; i++ {
		err := e.mfa.VerifySecondFactor(ctx, u.ID, SecondFactor{Code: "111111"}, "login")
		require.ErrorIs(t, err, ErrMFAInvalid)
	}
	assert.True(t, e.mr.Exists(mfaLockKey(u.ID)), "lock key must exist after 5 failures")

	// Locked: even the correct code gets the same generic error as a wrong one.
	err := e.mfa.VerifySecondFactor(ctx, u.ID, SecondFactor{Code: good}, "login")
	assert.ErrorIs(t, err, ErrMFAInvalid)

	// ...and over HTTP the answer is the generic 401, with a valid pending token.
	r := e.login(u, "")
	locked := e.do(http.MethodPost, "/api/auth/login/mfa", "", "", map[string]string{"mfa_token": r.str("mfa_token"), "code": good})
	assert.Equal(t, http.StatusUnauthorized, locked.Code)
	assert.Equal(t, bad401Message, locked.Body["error"].(map[string]interface{})["message"], "same message as a wrong code")
	assert.Empty(t, locked.str("token"))

	// After the 15 minute lock a normal login works again.
	e.mr.FastForward(mfaLockout + time.Second)
	e.advance()
	assert.NotEmpty(t, e.fullLogin(u, ""))

	// Failures (including the rejected attempts while locked) are audited under category auth.
	assert.GreaterOrEqual(t, e.auditCount(u.ID, "mfa_failed", 7), 7)
	assert.GreaterOrEqual(t, e.auditCount(u.ID, "mfa_locked", 1), 1)
}

func TestMFALogin_FailureCounterWindow(t *testing.T) {
	e := newMFAEnv(t)
	u := e.user("window", "user", "")
	e.enroll(u)
	ctx := context.Background()
	// 4 failures, then the 10 minute window lapses: the counter restarts, no lockout.
	for i := 0; i < 4; i++ {
		require.ErrorIs(t, e.mfa.VerifySecondFactor(ctx, u.ID, SecondFactor{Code: "222222"}, "login"), ErrMFAInvalid)
	}
	e.mr.FastForward(mfaFailWindow + time.Second)
	require.ErrorIs(t, e.mfa.VerifySecondFactor(ctx, u.ID, SecondFactor{Code: "222222"}, "login"), ErrMFAInvalid)
	assert.False(t, e.mr.Exists(mfaLockKey(u.ID)))
	// A success clears the counter.
	e.advance()
	require.NoError(t, e.mfa.VerifySecondFactor(ctx, u.ID, SecondFactor{Code: u.code(e, 0)}, "login"))
	assert.False(t, e.mr.Exists(mfaFailKey(u.ID)))
}

func TestMFALogin_RecoveryCodeWorksOnce(t *testing.T) {
	e := newMFAEnv(t)
	u := e.user("recovery", "user", "")
	e.enroll(u)
	rc := u.Recovery[0]

	r := e.login(u, "")
	ok := e.do(http.MethodPost, "/api/auth/login/mfa", "", "", map[string]string{"mfa_token": r.str("mfa_token"), "recovery_code": rc})
	require.Equal(t, http.StatusOK, ok.Code, ok.Raw)

	// Same code again, with a new valid pending token: refused.
	r2 := e.login(u, "")
	again := e.do(http.MethodPost, "/api/auth/login/mfa", "", "", map[string]string{"mfa_token": r2.str("mfa_token"), "recovery_code": rc})
	assert.Equal(t, http.StatusUnauthorized, again.Code)

	// A recovery code typed into the single login field works too (once).
	r3 := e.login(u, "")
	other := e.do(http.MethodPost, "/api/auth/login/mfa", "", "", map[string]string{"mfa_token": r3.str("mfa_token"), "code": u.Recovery[1]})
	assert.Equal(t, http.StatusOK, other.Code, other.Raw)

	var remaining int
	require.NoError(t, e.d.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM user_recovery_codes WHERE user_id = $1::uuid AND used_at IS NULL`, u.ID).Scan(&remaining))
	assert.Equal(t, 8, remaining)
	// Only hashes are stored, never the code itself.
	var n int
	require.NoError(t, e.d.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM user_recovery_codes WHERE user_id = $1::uuid AND code_hash = $2`, u.ID, rc).Scan(&n))
	assert.Equal(t, 0, n)
}

func newGinCtx() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	c.Request.RemoteAddr = "203.0.113.10:40000"
	return c
}

// TestEveryLoginPathIsBlockedWithoutMFA walks each way a session can be minted
// and proves none of them yields one for an MFA user without the second step.
func TestEveryLoginPathIsBlockedWithoutMFA(t *testing.T) {
	e := newMFAEnv(t)
	u := e.user("paths", "user", "")
	e.enroll(u)

	t.Run("api password login", func(t *testing.T) {
		r := e.login(u, "")
		assert.Empty(t, r.str("token"))
		assert.Equal(t, true, r.Body["mfa_required"])
	})
	t.Run("shared FinishPasswordLogin (web login, registration auto-login)", func(t *testing.T) {
		out, err := e.ar.FinishPasswordLogin(newGinCtx(), u.ID, session.Options{SetCookie: true, Remember: true})
		require.NoError(t, err)
		assert.Nil(t, out.Session)
		assert.NotEmpty(t, out.MFAToken)
	})
	t.Run("issuer refuses password-only for MFA users", func(t *testing.T) {
		_, err := e.ar.SessionIssuer().Issue(newGinCtx(), u.ID, session.MethodPassword, session.Options{})
		assert.ErrorIs(t, err, session.ErrMFARequired)
	})
	t.Run("issuer allows SSO (IdP authenticated) and MFA proof", func(t *testing.T) {
		for _, m := range []session.Method{session.MethodSSO, session.MethodMFA} {
			res, err := e.ar.SessionIssuer().Issue(newGinCtx(), u.ID, m, session.Options{})
			require.NoError(t, err)
			assert.NotEmpty(t, res.Token)
		}
	})
	t.Run("MFA completion with bogus or missing pending token", func(t *testing.T) {
		for _, tok := range []string{"", "nope", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
			_, err := e.ar.CompleteMFALogin(newGinCtx(), tok, SecondFactor{Code: u.code(e, 0)}, false)
			assert.ErrorIs(t, err, ErrMFAInvalid)
		}
	})
	t.Run("challenge store down fails closed", func(t *testing.T) {
		e.mr.Close()
		out, err := e.ar.FinishPasswordLogin(newGinCtx(), u.ID, session.Options{})
		assert.Error(t, err)
		assert.Nil(t, out)
	})
}

func TestMFAChallengeBlockedWhenUserSuspended(t *testing.T) {
	e := newMFAEnv(t)
	u := e.user("suspended", "user", "")
	e.enroll(u)
	r := e.login(u, "")
	_, err := e.d.Pool.Exec(context.Background(), `UPDATE users SET status = 'suspended' WHERE id = $1::uuid`, u.ID)
	require.NoError(t, err)
	e.advance()
	done := e.do(http.MethodPost, "/api/auth/login/mfa", "", "", map[string]string{"mfa_token": r.str("mfa_token"), "code": u.code(e, 0)})
	assert.Equal(t, http.StatusUnauthorized, done.Code, "a suspended account must not get a session from a pending token")
	assert.Empty(t, done.str("token"))
}

// ---- enrolment / management --------------------------------------------------

func TestMFAEnrolDisableFlow(t *testing.T) {
	e := newMFAEnv(t)
	u := e.user("flow", "user", "")
	tok := e.fullLogin(u, "")

	// Setup returns the secret + otpauth URI but does not enable anything.
	setup := e.do(http.MethodPost, "/api/profile/mfa/setup", tok, "", nil)
	require.Equal(t, http.StatusOK, setup.Code, setup.Raw)
	secret := setup.str("secret")
	require.NotEmpty(t, secret)
	assert.Contains(t, setup.str("otpauth_uri"), "otpauth://totp/")
	assert.Contains(t, setup.str("otpauth_uri"), "secret="+secret)
	u.Secret = secret
	enabled, enc, _, tv0 := e.userRow(u.ID)
	assert.False(t, enabled, "totp_enabled must stay false until confirmed")
	require.NotNil(t, enc)
	assert.NotContains(t, *enc, secret, "secret must not be stored in clear")
	plain, err := crypto.Decrypt(*enc, mfaTestEncKey)
	require.NoError(t, err)
	assert.Equal(t, secret, plain)

	// A password-only login still works while setup is pending.
	assert.NotEmpty(t, e.login(u, "").str("token"))

	// Wrong code does not enable.
	assert.Equal(t, http.StatusBadRequest, e.do(http.MethodPost, "/api/profile/mfa/enable", tok, "", map[string]string{"code": "000000"}).Code)
	enabled, _, _, _ = e.userRow(u.ID)
	assert.False(t, enabled)

	// Right code enables, returns 10 recovery codes once, bumps token_version.
	en := e.do(http.MethodPost, "/api/profile/mfa/enable", tok, "", map[string]string{"code": u.code(e, 0)})
	require.Equal(t, http.StatusOK, en.Code, en.Raw)
	codes, _ := en.Body["recovery_codes"].([]interface{})
	require.Len(t, codes, 10)
	seen := map[string]bool{}
	for _, c := range codes {
		s := c.(string)
		assert.Len(t, s, 19, "XXXX-XXXX-XXXX-XXXX")
		seen[s] = true
	}
	assert.Len(t, seen, 10, "recovery codes must be distinct")
	enabled, _, _, tv1 := e.userRow(u.ID)
	assert.True(t, enabled)
	assert.Equal(t, tv0+1, tv1, "enabling MFA must invalidate other sessions")
	newTok := en.str("token")
	require.NotEmpty(t, newTok)
	assert.Equal(t, http.StatusUnauthorized, e.do(http.MethodGet, "/api/gated", tok, "", nil).Code, "pre-existing session is revoked")
	assert.Equal(t, http.StatusOK, e.do(http.MethodGet, "/api/gated", newTok, "", nil).Code, "the acting session is refreshed")

	// Setup again is refused while enabled.
	assert.Equal(t, http.StatusConflict, e.do(http.MethodPost, "/api/profile/mfa/setup", newTok, "", nil).Code)

	// ---- disable requires password AND a second factor ----
	e.advance()
	d1 := e.do(http.MethodPost, "/api/profile/mfa/disable", newTok, "", map[string]string{"code": u.code(e, 0)})
	assert.Equal(t, http.StatusBadRequest, d1.Code, "missing password")
	d2 := e.do(http.MethodPost, "/api/profile/mfa/disable", newTok, "", map[string]string{"password": "wrong-password", "code": u.code(e, 0)})
	assert.Equal(t, http.StatusBadRequest, d2.Code, "wrong password")
	d3 := e.do(http.MethodPost, "/api/profile/mfa/disable", newTok, "", map[string]string{"password": mfaTestPassword, "code": "000000"})
	assert.Equal(t, http.StatusBadRequest, d3.Code, "wrong code")
	d4 := e.do(http.MethodPost, "/api/profile/mfa/disable", newTok, "", map[string]string{"password": mfaTestPassword})
	assert.Equal(t, http.StatusBadRequest, d4.Code, "no second factor")
	enabled, _, _, _ = e.userRow(u.ID)
	require.True(t, enabled, "none of the rejected attempts may disable MFA")

	d5 := e.do(http.MethodPost, "/api/profile/mfa/disable", newTok, "", map[string]string{"password": mfaTestPassword, "recovery_code": codes[0].(string)})
	require.Equal(t, http.StatusOK, d5.Code, d5.Raw)
	enabled, enc, last, tv2 := e.userRow(u.ID)
	assert.False(t, enabled)
	assert.Nil(t, enc, "secret is wiped on disable")
	assert.Zero(t, last)
	assert.Equal(t, tv1+1, tv2)
	var left int
	require.NoError(t, e.d.Pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM user_recovery_codes WHERE user_id = $1::uuid`, u.ID).Scan(&left))
	assert.Zero(t, left)
	// And password login is a plain session again.
	assert.NotEmpty(t, e.login(u, "").str("token"))
}

func TestMFARegenerateRecoveryCodes(t *testing.T) {
	e := newMFAEnv(t)
	u := e.user("regen", "user", "")
	e.enroll(u)
	tok := e.fullLogin(u, "")
	e.advance()

	// Password is mandatory, and a recovery code cannot stand in for the TOTP code.
	regen := "/api/profile/mfa/recovery-codes/regenerate"
	assert.Equal(t, http.StatusBadRequest, e.do(http.MethodPost, regen, tok, "", map[string]string{"code": u.code(e, 0)}).Code)
	assert.Equal(t, http.StatusBadRequest, e.do(http.MethodPost, regen, tok, "", map[string]string{"password": "nope", "code": u.code(e, 0)}).Code)
	assert.Equal(t, http.StatusBadRequest, e.do(http.MethodPost, regen, tok, "", map[string]string{"password": mfaTestPassword, "code": u.Recovery[0]}).Code)

	ok := e.do(http.MethodPost, regen, tok, "", map[string]string{"password": mfaTestPassword, "code": u.code(e, 0)})
	require.Equal(t, http.StatusOK, ok.Code, ok.Raw)
	newCodes, _ := ok.Body["recovery_codes"].([]interface{})
	require.Len(t, newCodes, 10)

	// Old codes are dead, new ones work.
	r := e.login(u, "")
	oldTry := e.do(http.MethodPost, "/api/auth/login/mfa", "", "", map[string]string{"mfa_token": r.str("mfa_token"), "recovery_code": u.Recovery[2]})
	assert.Equal(t, http.StatusUnauthorized, oldTry.Code)
	newTry := e.do(http.MethodPost, "/api/auth/login/mfa", "", "", map[string]string{"mfa_token": r.str("mfa_token"), "recovery_code": newCodes[0].(string)})
	assert.Equal(t, http.StatusOK, newTry.Code, newTry.Raw)
}

func TestMFAWrongPasswordsCountTowardLockout(t *testing.T) {
	e := newMFAEnv(t)
	u := e.user("pwlock", "user", "")
	e.enroll(u)
	tok := e.fullLogin(u, "")
	e.advance()
	for i := 0; i < 5; i++ {
		r := e.do(http.MethodPost, "/api/profile/mfa/disable", tok, "", map[string]string{"password": "bad-password", "code": u.code(e, 0)})
		require.Equal(t, http.StatusBadRequest, r.Code)
	}
	// Now even the right password + code is refused, and MFA stays on.
	r := e.do(http.MethodPost, "/api/profile/mfa/disable", tok, "", map[string]string{"password": mfaTestPassword, "code": u.code(e, 0)})
	assert.Equal(t, http.StatusBadRequest, r.Code)
	enabled, _, _, _ := e.userRow(u.ID)
	assert.True(t, enabled)
}

// ---- policy gate ---------------------------------------------------------------

func TestMFAPolicy_DefaultForcesNobody(t *testing.T) {
	e := newMFAEnv(t)
	for _, role := range []string{"user", "enterprise_admin", "system_admin", "super_admin"} {
		u := e.user("default-"+role, role, "")
		tok := e.fullLogin(u, "")
		assert.Equal(t, http.StatusOK, e.do(http.MethodGet, "/api/gated", tok, "", nil).Code, role)
	}
}

func TestMFAPolicy_EnterpriseRequireGatesUnenrolledMembers(t *testing.T) {
	e := newMFAEnv(t)
	admin := e.user("polAdmin", "enterprise_admin", e.entA)
	member := e.user("polMember", "user", e.entA)
	outsider := e.user("polOutsider", "user", e.entB)
	adminTok := e.fullLogin(admin, "")
	memberTok := e.fullLogin(member, "")
	outsiderTok := e.fullLogin(outsider, "")

	on := e.do(http.MethodPut, "/api/enterprise/security", adminTok, "", map[string]interface{}{"require_mfa": true})
	require.Equal(t, http.StatusOK, on.Code, on.Raw)

	gated := e.do(http.MethodGet, "/api/gated", memberTok, "", nil)
	assert.Equal(t, http.StatusForbidden, gated.Code)
	assert.Equal(t, "mfa_enrollment_required", gated.errCode())
	// Another tenant is unaffected.
	assert.Equal(t, http.StatusOK, e.do(http.MethodGet, "/api/gated", outsiderTok, "", nil).Code)

	// The gated user can still use the enrolment endpoints and identity/logout.
	st := e.do(http.MethodGet, "/api/profile/mfa/status", memberTok, "", nil)
	require.Equal(t, http.StatusOK, st.Code, st.Raw)
	assert.Equal(t, true, st.Body["required"])
	assert.Equal(t, http.StatusOK, e.do(http.MethodGet, "/api/auth/me", memberTok, "", nil).Code)
	assert.Equal(t, http.StatusOK, e.do(http.MethodPost, "/api/auth/logout", memberTok, "", nil).Code)
	// ...but not other authenticated API groups.
	assert.Equal(t, http.StatusForbidden, e.do(http.MethodGet, "/api/keys", memberTok, "", nil).Code)

	// Enrol through the API; afterwards the session works again.
	setup := e.do(http.MethodPost, "/api/profile/mfa/setup", memberTok, "", nil)
	require.Equal(t, http.StatusOK, setup.Code)
	member.Secret = setup.str("secret")
	en := e.do(http.MethodPost, "/api/profile/mfa/enable", memberTok, "", map[string]string{"code": member.code(e, 0)})
	require.Equal(t, http.StatusOK, en.Code, en.Raw)
	assert.Equal(t, http.StatusOK, e.do(http.MethodGet, "/api/gated", en.str("token"), "", nil).Code)

	// While the policy applies the user cannot switch MFA off again.
	e.advance()
	dis := e.do(http.MethodPost, "/api/profile/mfa/disable", en.str("token"), "",
		map[string]string{"password": mfaTestPassword, "code": member.code(e, 0)})
	assert.Equal(t, http.StatusForbidden, dis.Code)
	assert.Equal(t, "mfa_required_by_policy", dis.errCode())

	// Switching the policy off lifts the gate for the other (unenrolled) account.
	// (The admin's own session is gated until they enrol, but PUT is an enterprise
	// API; enrol the admin first as the product flow would.)
	adminSetup := e.do(http.MethodPost, "/api/profile/mfa/setup", adminTok, "", nil)
	admin.Secret = adminSetup.str("secret")
	adminEn := e.do(http.MethodPost, "/api/profile/mfa/enable", adminTok, "", map[string]string{"code": admin.code(e, 0)})
	require.Equal(t, http.StatusOK, adminEn.Code, adminEn.Raw)
	off := e.do(http.MethodPut, "/api/enterprise/security", adminEn.str("token"), "", map[string]interface{}{"require_mfa": false})
	assert.Equal(t, http.StatusOK, off.Code, off.Raw)
}

func TestMFAPolicy_RoleListGatesOnlyListedRoles(t *testing.T) {
	e := newMFAEnv(t)
	super := e.user("polSuper", "super_admin", "")
	ent := e.user("polEntAdmin", "enterprise_admin", e.entA)
	plain := e.user("polPlain", "user", "")
	superTok, entTok, plainTok := e.fullLogin(super, ""), e.fullLogin(ent, ""), e.fullLogin(plain, "")

	put := e.do(http.MethodPut, "/api/admin/settings/mfa-required-roles", superTok, "", map[string]interface{}{"roles": []string{"enterprise_admin"}})
	require.Equal(t, http.StatusOK, put.Code, put.Raw)

	assert.Equal(t, http.StatusForbidden, e.do(http.MethodGet, "/api/gated", entTok, "", nil).Code)
	assert.Equal(t, http.StatusOK, e.do(http.MethodGet, "/api/gated", plainTok, "", nil).Code)
	assert.Equal(t, http.StatusOK, e.do(http.MethodGet, "/api/gated", superTok, "", nil).Code)

	// Clearing the list lifts it again.
	e.do(http.MethodPut, "/api/admin/settings/mfa-required-roles", superTok, "", map[string]interface{}{"roles": []string{}})
	assert.Equal(t, http.StatusOK, e.do(http.MethodGet, "/api/gated", entTok, "", nil).Code)
}

// The gate lives in the session middleware only: API-key, Proxy and webhook
// endpoints must not be affected by a user's enrolment state.
func TestMFAPolicy_APIKeyValidateAndWebhooksUnaffected(t *testing.T) {
	e := newMFAEnv(t)
	super := e.user("unaffSuper", "super_admin", "")
	superTok := e.fullLogin(super, "")
	e.do(http.MethodPut, "/api/admin/settings/mfa-required-roles", superTok, "", map[string]interface{}{"roles": []string{"user", "enterprise_admin", "system_admin", "super_admin"}})
	require.Equal(t, http.StatusForbidden, e.do(http.MethodGet, "/api/gated", superTok, "", nil).Code)

	// Key validation (Proxy) is authenticated by the key itself: an unknown key
	// is a plain auth failure, never an MFA error.
	v := e.do(http.MethodGet, "/api/keys/validate", "", "", nil)
	assert.NotEqual(t, "mfa_enrollment_required", v.errCode())
	assert.NotEqual(t, http.StatusForbidden, v.Code)
	// Webhooks are HMAC signed; an unsigned call is rejected for that reason.
	w := e.do(http.MethodPost, "/api/webhook/usage", "", "", map[string]string{})
	assert.NotEqual(t, "mfa_enrollment_required", w.errCode())
}

// ---- permission matrix --------------------------------------------------------

func TestEnterpriseSecurityPermissionMatrix(t *testing.T) {
	e := newMFAEnv(t)
	adminA := e.user("mxAdminA", "enterprise_admin", e.entA)
	adminB := e.user("mxAdminB", "enterprise_admin", e.entB)
	memberA := e.user("mxMemberA", "user", e.entA)
	sys := e.user("mxSys", "system_admin", "")
	super := e.user("mxSuper", "super_admin", "")
	tokOf := func(u *mfaUser) string { return e.fullLogin(u, "") }

	type tc struct {
		name string
		who  *mfaUser
		body map[string]interface{}
		want int
	}
	// Order matters: once require_mfa is on, the (unenrolled) own admin is
	// itself gated, so the cases that need adminA's session run first.
	for _, c := range []tc{
		{"own enterprise admin (no change)", adminA, map[string]interface{}{"require_mfa": false}, http.StatusOK},
		{"missing field never defaults to off", adminA, map[string]interface{}{}, http.StatusBadRequest},
		{"other enterprise admin targeting A", adminB, map[string]interface{}{"require_mfa": true, "enterprise_id": e.entA}, http.StatusForbidden},
		{"plain member", memberA, map[string]interface{}{"require_mfa": true}, http.StatusForbidden},
		{"system admin", sys, map[string]interface{}{"require_mfa": true, "enterprise_id": e.entA}, http.StatusForbidden},
		{"super admin must name enterprise", super, map[string]interface{}{"require_mfa": true}, http.StatusBadRequest},
		{"super admin naming enterprise", super, map[string]interface{}{"require_mfa": false, "enterprise_id": e.entA}, http.StatusOK},
		{"own admin naming own enterprise", adminA, map[string]interface{}{"require_mfa": true, "enterprise_id": e.entA}, http.StatusOK},
	} {
		r := e.do(http.MethodPut, "/api/enterprise/security", tokOf(c.who), "", c.body)
		assert.Equal(t, c.want, r.Code, "%s: %s", c.name, r.Raw)
	}
	assert.Equal(t, http.StatusUnauthorized, e.do(http.MethodPut, "/api/enterprise/security", "", "", map[string]interface{}{"require_mfa": true}).Code)

	// Reads follow the same matrix.
	assert.Equal(t, http.StatusBadRequest, e.do(http.MethodGet, "/api/enterprise/security", tokOf(super), "", nil).Code, "super_admin must name an enterprise on reads too")
	assert.Equal(t, http.StatusOK, e.do(http.MethodGet, "/api/enterprise/security?enterprise_id="+e.entA, tokOf(super), "", nil).Code)
	assert.Equal(t, http.StatusForbidden, e.do(http.MethodGet, "/api/enterprise/security", tokOf(memberA), "", nil).Code)
}

func TestPlatformMFARolesPermissionMatrix(t *testing.T) {
	e := newMFAEnv(t)
	adminA := e.user("prAdminA", "enterprise_admin", e.entA)
	memberA := e.user("prMemberA", "user", e.entA)
	sys := e.user("prSys", "system_admin", "")
	super := e.user("prSuper", "super_admin", "")
	tokOf := func(u *mfaUser) string { return e.fullLogin(u, "") }

	rolesURL := "/api/admin/settings/mfa-required-roles"
	superTok := tokOf(super)
	assert.Equal(t, http.StatusOK, e.do(http.MethodPut, rolesURL, superTok, "", map[string]interface{}{"roles": []string{"enterprise_admin", "user", "user"}}).Code)
	got := e.do(http.MethodGet, rolesURL, superTok, "", nil)
	assert.Equal(t, []interface{}{"user", "enterprise_admin"}, got.Body["roles"], "normalised, de-duplicated, privilege ordered")
	assert.Equal(t, http.StatusBadRequest, e.do(http.MethodPut, rolesURL, superTok, "", map[string]interface{}{"roles": []string{"root"}}).Code)
	assert.Equal(t, http.StatusBadRequest, e.do(http.MethodPut, rolesURL, superTok, "", map[string]interface{}{}).Code)
	// Clear before logging the other accounts in (the list above would gate them).
	e.do(http.MethodPut, rolesURL, superTok, "", map[string]interface{}{"roles": []string{}})
	for _, u := range []*mfaUser{sys, adminA, memberA} {
		assert.Equal(t, http.StatusForbidden, e.do(http.MethodPut, rolesURL, tokOf(u), "", map[string]interface{}{"roles": []string{"user"}}).Code, u.Role)
		assert.Equal(t, http.StatusForbidden, e.do(http.MethodGet, rolesURL, tokOf(u), "", nil).Code, u.Role)
	}
	assert.Equal(t, http.StatusUnauthorized, e.do(http.MethodPut, rolesURL, "", "", map[string]interface{}{"roles": []string{"user"}}).Code)
}

func TestAdminMFAResetPermissionMatrix(t *testing.T) {
	e := newMFAEnv(t)
	adminA := e.user("rsAdminA", "enterprise_admin", e.entA)
	adminA2 := e.user("rsAdminA2", "enterprise_admin", e.entA)
	memberA := e.user("rsMemberA", "user", e.entA)
	memberB := e.user("rsMemberB", "user", e.entB)
	sys := e.user("rsSys", "system_admin", "")
	super := e.user("rsSuper", "super_admin", "")
	target := func(id string) string { return "/api/admin/users/" + id + "/mfa/reset" }

	// Sessions first, then enrol the targets (enrolment revokes their old sessions).
	toks := map[string]string{}
	for _, u := range []*mfaUser{adminA, sys, super, memberA} {
		toks[u.ID] = e.fullLogin(u, "")
	}
	for _, u := range []*mfaUser{memberA, memberB, adminA2} {
		e.enroll(u)
	}
	toks[memberA.ID] = e.fullLogin(memberA, "")

	reset := func(who *mfaUser, id string) resp {
		return e.do(http.MethodPost, target(id), toks[who.ID], "", nil)
	}
	assert.Equal(t, http.StatusForbidden, reset(sys, memberA.ID).Code, "system_admin may not reset")
	assert.Equal(t, http.StatusForbidden, reset(memberA, memberB.ID).Code, "plain user may not reset")
	assert.Equal(t, http.StatusForbidden, reset(adminA, memberB.ID).Code, "enterprise admin: other tenant")
	assert.Equal(t, http.StatusForbidden, reset(adminA, adminA2.ID).Code, "enterprise admin: another admin of the same enterprise")
	assert.Equal(t, http.StatusForbidden, reset(adminA, adminA.ID).Code, "no self reset")
	assert.Equal(t, http.StatusForbidden, reset(super, super.ID).Code, "no self reset for super_admin either")
	assert.Equal(t, http.StatusNotFound, reset(super, "00000000-0000-0000-0000-00000000dead").Code)
	assert.Equal(t, http.StatusNotFound, reset(super, "not-a-uuid").Code)
	assert.Equal(t, http.StatusUnauthorized, e.do(http.MethodPost, target(memberA.ID), "", "", nil).Code)
	for _, u := range []*mfaUser{memberA, memberB, adminA2} {
		en, _, _, _ := e.userRow(u.ID)
		require.True(t, en, "rejected resets must leave MFA intact")
	}

	_, _, _, tvBefore := e.userRow(memberA.ID)
	ok := reset(adminA, memberA.ID)
	require.Equal(t, http.StatusOK, ok.Code, ok.Raw)
	en, enc, _, tvAfter := e.userRow(memberA.ID)
	assert.False(t, en)
	assert.Nil(t, enc)
	assert.Equal(t, tvBefore+1, tvAfter, "reset must end the user's sessions")
	assert.Equal(t, http.StatusUnauthorized, e.do(http.MethodGet, "/api/gated", toks[memberA.ID], "", nil).Code)
	// The user can log in with the password alone again, and re-enrol.
	assert.NotEmpty(t, e.login(memberA, "").str("token"))

	// super_admin may reset anyone else.
	assert.Equal(t, http.StatusOK, reset(super, memberB.ID).Code)
	assert.Equal(t, http.StatusOK, reset(super, adminA2.ID).Code)

	// Audit: platform log for actor and target, enterprise log for the tenant.
	assert.GreaterOrEqual(t, e.auditCount(adminA.ID, "mfa_reset_by_admin", 1), 1)
	assert.GreaterOrEqual(t, e.auditCount(memberA.ID, "mfa_reset", 1), 1)
	var n int
	require.NoError(t, e.d.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM enterprise_audit_log WHERE enterprise_id = $1::uuid AND change_type = 'mfa_reset'`, e.entA).Scan(&n))
	assert.GreaterOrEqual(t, n, 1)
}

// ---- audit ---------------------------------------------------------------------

func TestMFAAuditEventsWrittenAndChainIntact(t *testing.T) {
	e := newMFAEnv(t)
	// Verify only rows written by this test: other tests' cleanups run with
	// the audit purge GUC (testutil) and may leave gaps earlier in the chain.
	var since time.Time
	require.NoError(t, e.d.Pool.QueryRow(context.Background(), `SELECT now()`).Scan(&since))
	adminA := e.user("auAdmin", "enterprise_admin", e.entA)
	u := e.user("auUser", "user", e.entA)
	tok := e.fullLogin(adminA, "")

	e.enroll(u) // mfa_setup_started + mfa_enabled
	r := e.login(u, "")
	e.do(http.MethodPost, "/api/auth/login/mfa", "", "", map[string]string{"mfa_token": r.str("mfa_token"), "code": "123456"}) // failure
	e.advance()
	e.do(http.MethodPost, "/api/auth/login/mfa", "", "", map[string]string{"mfa_token": r.str("mfa_token"), "code": u.code(e, 0)}) // success
	e.do(http.MethodPut, "/api/enterprise/security", tok, "", map[string]interface{}{"require_mfa": true})

	assert.Equal(t, 1, e.auditCount(u.ID, "mfa_setup_started", 1))
	assert.Equal(t, 1, e.auditCount(u.ID, "mfa_enabled", 1))
	assert.GreaterOrEqual(t, e.auditCount(u.ID, "mfa_failed", 1), 1)
	assert.GreaterOrEqual(t, e.auditCount(u.ID, "mfa_success", 1), 1)
	assert.Equal(t, 1, e.auditCount(adminA.ID, "enterprise_mfa_policy_updated", 1))

	// Failures/successes use category "auth", as specified.
	var cat string
	require.NoError(t, e.d.Pool.QueryRow(context.Background(),
		`SELECT category FROM audit_logs WHERE user_id = $1::uuid AND action = 'mfa_failed' LIMIT 1`, u.ID).Scan(&cat))
	assert.Equal(t, "auth", cat)

	// No secret or recovery-code material in any audit message for this user.
	var leaked int
	require.NoError(t, e.d.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM audit_logs WHERE user_id = $1::uuid AND (message LIKE '%' || $2 || '%' OR message LIKE '%' || $3 || '%')`,
		u.ID, u.Secret, u.Recovery[0]).Scan(&leaked))
	assert.Zero(t, leaked)

	// The hash chain written through the trigger is still verifiable.
	e.audit.Stop(context.Background())
	for _, table := range []string{"audit_logs", "enterprise_audit_log"} {
		res, err := e.d.VerifyAuditChain(context.Background(), db.VerifyOptions{Table: table, From: &since, AnchorKey: mfaTestEncKey})
		require.NoError(t, err)
		assert.True(t, res.OK, "%s chain broken at %v: %s", table, res.FirstBrokenID, res.Reason)
	}
}

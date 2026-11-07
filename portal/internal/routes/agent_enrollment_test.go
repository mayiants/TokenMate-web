package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mayiants/ai-portal-go/internal/config"
	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/pkg/audit"
	"github.com/mayiants/ai-portal-go/pkg/crypto"
	"github.com/mayiants/ai-portal-go/pkg/mail"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type agentTestEnv struct {
	routes *AgentEnrollmentRoutes
	mr     *miniredis.Miniredis
	d      *db.DB
}

func newAgentTestEnv(t *testing.T) *agentTestEnv {
	t.Helper()
	d := testEnterpriseDB(t)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	d.Redis = rdb
	cfg := &config.Config{
		App:  config.AppConfig{BaseURL: "https://api.adaii.com"},
		Auth: config.AuthConfig{EncryptionKey: "test-encryption-key-32-characters!!"},
	}
	return &agentTestEnv{
		routes: NewAgentEnrollmentRoutes(d, cfg, zap.NewNop(), &audit.Logger{}, &mail.Sender{}),
		mr:     mr,
		d:      d,
	}
}

// invoke dispatches to a named agent handler so call sites stay readable.
func (e *agentTestEnv) invoke(name string, body map[string]interface{}, headers map[string]string) (int, map[string]interface{}) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	raw, _ := json.Marshal(body)
	method := http.MethodPost
	if name == "getConfig" {
		method = http.MethodGet
	}
	c.Request = httptest.NewRequest(method, "/api/v1/agent/"+name, bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		c.Request.Header.Set(k, v)
	}
	r := e.routes
	switch name {
	case "sendOTP":
		r.sendOTP(c)
	case "verifyOTP":
		r.verifyOTP(c)
	case "exchange":
		r.exchange(c)
	case "heartbeat":
		r.heartbeat(c)
	case "getConfig":
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/agent/config?client=workbuddy&os=windows", nil)
		r.getConfig(c)
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w.Code, resp
}

func (e *agentTestEnv) issueSession(t *testing.T, email string) string {
	t.Helper()
	code, _ := e.invoke("sendOTP", map[string]interface{}{"email": email}, nil)
	if code != http.StatusOK {
		t.Fatalf("sendOTP: code=%d", code)
	}
	otp, err := e.mr.Get("verification:agent_enroll:" + email)
	if err != nil {
		t.Fatalf("read OTP from miniredis: %v", err)
	}
	code, body := e.invoke("verifyOTP", map[string]interface{}{"email": email, "code": otp}, nil)
	if code != http.StatusOK {
		t.Fatalf("verifyOTP: code=%d body=%v", code, body)
	}
	token, _ := body["session_token"].(string)
	if token == "" {
		t.Fatalf("verifyOTP returned no session_token: %v", body)
	}
	return token
}

func (e *agentTestEnv) exchange(t *testing.T, session, fingerprint, hostname string) (int, map[string]interface{}) {
	t.Helper()
	return e.invoke("exchange", map[string]interface{}{
		"session_token":      session,
		"device_fingerprint": fingerprint,
		"hostname":           hostname,
		"os_version":         "Windows 11 Pro",
		"agent_version":      "0.1.0",
	}, nil)
}

func cleanupAgentTestData(t *testing.T, d *db.DB, suffix string) {
	t.Helper()
	ctx := context.Background()
	emails := "agen-" + suffix + "%@test.com"
	_, _ = d.Pool.Exec(ctx, `DELETE FROM agent_enroll_sessions WHERE user_id IN (SELECT id FROM users WHERE email LIKE $1)`, emails)
	_, _ = d.Pool.Exec(ctx, `DELETE FROM agent_devices WHERE user_id IN (SELECT id FROM users WHERE email LIKE $1)`, emails)
	_, _ = d.Pool.Exec(ctx, `DELETE FROM api_keys WHERE name LIKE '[agent] %' AND assigned_user_id IN (SELECT id FROM users WHERE email LIKE $1)`, emails)
	_, _ = d.Pool.Exec(ctx, `DELETE FROM enterprise_members WHERE user_id IN (SELECT id FROM users WHERE email LIKE $1)`, emails)
	_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE email LIKE $1`, emails)
	_, _ = d.Pool.Exec(ctx, `DELETE FROM enterprises WHERE contact_email LIKE $1`, "%agen-"+suffix+"%@test.com")
}

// TestAgentEnroll_FullFlow covers OTP -> session -> exchange -> heartbeat ->
// config, verifying the key lands in api_keys as an enterprise key and the
// device row links back to it.
func TestAgentEnroll_FullFlow(t *testing.T) {
	e := newAgentTestEnv(t)
	defer e.d.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	defer cleanupAgentTestData(t, e.d, suffix)

	ent := createApprovalTestEnterprise(t, e.d, suffix, "")
	userID := createGateTestUser(t, e.d, "agen-"+suffix+"-mbr@test.com", 0, ent)

	session := e.issueSession(t, "agen-"+suffix+"-mbr@test.com")
	code, body := e.exchange(t, session, "fp-fullflow-"+suffix, "PC-DEV-01")
	if code != http.StatusOK {
		t.Fatalf("exchange: code=%d body=%v", code, body)
	}
	apiKey, _ := body["api_key"].(string)
	if len(apiKey) < 20 || body["api_base_url"] != "https://api.adaii.com" {
		t.Fatalf("unexpected exchange payload: %v", body)
	}
	keyID, _ := body["key_id"].(string)
	deviceID, _ := body["device_id"].(string)

	var gotKey struct {
		Status      string
		KeyType     string
		AssignedTo  string
		Enterprise  string
	}
	err := e.d.Pool.QueryRow(context.Background(),
		`SELECT status, key_type, assigned_user_id::text, enterprise_id::text FROM api_keys WHERE id = $1::uuid`, keyID,
	).Scan(&gotKey.Status, &gotKey.KeyType, &gotKey.AssignedTo, &gotKey.Enterprise)
	if err != nil {
		t.Fatalf("lookup issued key: %v", err)
	}
	if gotKey.Status != "active" || gotKey.KeyType != "enterprise" || gotKey.AssignedTo != userID || gotKey.Enterprise != ent {
		t.Fatalf("issued key mismatch: %+v", gotKey)
	}

	adapter, ok := body["adapter"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected adapter payload, got: %v", body["adapter"])
	}
	manifest, ok := adapter["manifest"].(map[string]interface{})
	if !ok || manifest["status"] != "pending_p0_verification" {
		t.Fatalf("expected placeholder adapter manifest, got: %v", body["adapter"])
	}

	// Heartbeat with the issued key.
	code, hb := e.invoke("heartbeat", map[string]interface{}{
		"device_id":          deviceID,
		"device_fingerprint": "fp-fullflow-" + suffix,
		"agent_version":      "0.1.0",
	}, map[string]string{"Authorization": "Bearer " + apiKey})
	if code != http.StatusOK || hb["key_status"] != "active" {
		t.Fatalf("heartbeat: code=%d body=%v", code, hb)
	}

	code, cfgBody := e.invoke("getConfig", nil, nil)
	if code != http.StatusOK || cfgBody["client"] != "workbuddy" {
		t.Fatalf("getConfig: code=%d body=%v", code, cfgBody)
	}
}

// TestAgentEnroll_ReplayProtection verifies a consumed session cannot be
// exchanged twice.
func TestAgentEnroll_ReplayProtection(t *testing.T) {
	e := newAgentTestEnv(t)
	defer e.d.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	defer cleanupAgentTestData(t, e.d, suffix)

	ent := createApprovalTestEnterprise(t, e.d, suffix, "")
	createGateTestUser(t, e.d, "agen-"+suffix+"-mbr@test.com", 0, ent)

	session := e.issueSession(t, "agen-"+suffix+"-mbr@test.com")
	code, _ := e.exchange(t, session, "fp-replay-"+suffix, "PC-DEV-02")
	if code != http.StatusOK {
		t.Fatalf("first exchange: code=%d", code)
	}
	code, body := e.exchange(t, session, "fp-replay-"+suffix, "PC-DEV-02")
	if code != http.StatusConflict {
		t.Fatalf("replayed session: code=%d body=%v, want 409", code, body)
	}

	code, _ = e.exchange(t, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "fp-replay-"+suffix, "PC-DEV-02")
	if code != http.StatusUnauthorized {
		t.Fatalf("unknown session: code=%d, want 401", code)
	}
}

// TestAgentEnroll_VerifyOTPGuards covers wrong codes and the non-member oracle.
func TestAgentEnroll_VerifyOTPGuards(t *testing.T) {
	e := newAgentTestEnv(t)
	defer e.d.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	defer cleanupAgentTestData(t, e.d, suffix)

	ent := createApprovalTestEnterprise(t, e.d, suffix, "")
	memberEmail := "agen-" + suffix + "-mbr@test.com"
	createGateTestUser(t, e.d, memberEmail, 0, ent)

	code, _ := e.invoke("sendOTP", map[string]interface{}{"email": memberEmail}, nil)
	if code != http.StatusOK {
		t.Fatalf("sendOTP: code=%d", code)
	}
	otp, _ := e.mr.Get("verification:agent_enroll:" + memberEmail)

	code, _ = e.invoke("verifyOTP", map[string]interface{}{"email": memberEmail, "code": "000000"}, nil)
	if code != http.StatusBadRequest {
		t.Fatalf("wrong code: code=%d, want 400", code)
	}

	code, _ = e.invoke("verifyOTP", map[string]interface{}{"email": memberEmail, "code": otp}, nil)
	if code != http.StatusOK {
		t.Fatalf("correct code: code=%d, want 200", code)
	}

	// Non-member with a freshly generated code gets the identical error.
	stranger := "agen-" + suffix + "-stranger@test.com"
	createGateTestUser(t, e.d, stranger, 0, "")
	code, _ = e.invoke("sendOTP", map[string]interface{}{"email": stranger}, nil)
	if code != http.StatusOK {
		t.Fatalf("sendOTP stranger: code=%d", code)
	}
	strangerOTP, _ := e.mr.Get("verification:agent_enroll:" + stranger)
	code, body := e.invoke("verifyOTP", map[string]interface{}{"email": stranger, "code": strangerOTP}, nil)
	if code != http.StatusBadRequest {
		t.Fatalf("non-member verify: code=%d body=%v, want 400", code, body)
	}
	errMsg, _ := body["error"].(map[string]interface{})
	if errMsg["message"] != "Invalid or expired code" {
		t.Fatalf("non-member error should be indistinguishable: %v", body)
	}
}

// TestAgentEnroll_ReenrollRevokesOldKey verifies that re-enrolling the same
// device fingerprint revokes the previous key and reuses the device row.
func TestAgentEnroll_ReenrollRevokesOldKey(t *testing.T) {
	e := newAgentTestEnv(t)
	defer e.d.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	defer cleanupAgentTestData(t, e.d, suffix)

	ent := createApprovalTestEnterprise(t, e.d, suffix, "")
	createGateTestUser(t, e.d, "agen-"+suffix+"-mbr@test.com", 0, ent)

	session := e.issueSession(t, "agen-"+suffix+"-mbr@test.com")
	_, first := e.exchange(t, session, "fp-reenroll-"+suffix, "PC-DEV-03")
	oldKeyID, _ := first["key_id"].(string)
	oldDeviceID, _ := first["device_id"].(string)

	session = e.issueSession(t, "agen-"+suffix+"-mbr@test.com")
	_, second := e.exchange(t, session, "fp-reenroll-"+suffix, "PC-DEV-03")
	if second["device_id"] != oldDeviceID {
		t.Fatalf("re-enroll should reuse the device row: %v vs %v", second["device_id"], oldDeviceID)
	}

	var oldStatus string
	_ = e.d.Pool.QueryRow(context.Background(),
		`SELECT status FROM api_keys WHERE id = $1::uuid`, oldKeyID).Scan(&oldStatus)
	if oldStatus != "revoked" {
		t.Fatalf("old key status=%q, want revoked", oldStatus)
	}
	newKeyID, _ := second["key_id"].(string)
	if newKeyID == oldKeyID {
		t.Fatalf("re-enroll must issue a fresh key")
	}
}

// TestAgentEnroll_HeartbeatDetectsRevocationAndTampering verifies heartbeat
// reports key_status faithfully and rejects fingerprint mismatches.
func TestAgentEnroll_HeartbeatDetectsRevocationAndTampering(t *testing.T) {
	e := newAgentTestEnv(t)
	defer e.d.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	defer cleanupAgentTestData(t, e.d, suffix)

	ent := createApprovalTestEnterprise(t, e.d, suffix, "")
	createGateTestUser(t, e.d, "agen-"+suffix+"-mbr@test.com", 0, ent)

	session := e.issueSession(t, "agen-"+suffix+"-mbr@test.com")
	_, body := e.exchange(t, session, "fp-hb-"+suffix, "PC-DEV-04")
	apiKey, _ := body["api_key"].(string)
	keyID, _ := body["key_id"].(string)
	deviceID, _ := body["device_id"].(string)

	// Tampered fingerprint must not authenticate.
	code, _ := e.invoke("heartbeat", map[string]interface{}{
		"device_id":          deviceID,
		"device_fingerprint": "fp-tampered-" + suffix,
	}, map[string]string{"Authorization": "Bearer " + apiKey})
	if code != http.StatusForbidden {
		t.Fatalf("tampered fingerprint: code=%d, want 403", code)
	}

	// Revoke the key; heartbeat must surface it.
	_, _ = e.d.Pool.Exec(context.Background(),
		`UPDATE api_keys SET status = 'revoked' WHERE id = $1::uuid`, keyID)
	code, hb := e.invoke("heartbeat", map[string]interface{}{
		"device_id":          deviceID,
		"device_fingerprint": "fp-hb-" + suffix,
	}, map[string]string{"Authorization": "Bearer " + apiKey})
	if code != http.StatusOK || hb["key_status"] != "revoked" {
		t.Fatalf("revoked heartbeat: code=%d body=%v", code, hb)
	}

	// Unknown bearer key is rejected.
	code, _ = e.invoke("heartbeat", map[string]interface{}{
		"device_id":          deviceID,
		"device_fingerprint": "fp-hb-" + suffix,
	}, map[string]string{"Authorization": "Bearer sk-wgit-en-nonexistent"})
	if code != http.StatusUnauthorized {
		t.Fatalf("unknown key: code=%d, want 401", code)
	}
}

func TestSanitizeHostname(t *testing.T) {
	cases := map[string]string{
		"DESKTOP-ABC123":   "DESKTOP-ABC123",
		"my pc/\\:*?":      "my-pc-----",
		"very-long-hostname-with-lots-of-chars-that-should-be-truncated-0123456789": "very-long-hostname-with-lots-of-chars-that-should-be-truncated-0",
	}
	for in, want := range cases {
		got := hostnameSanitizer.ReplaceAllString(in, "-")
		if len(got) > maxHostnameLen {
			got = got[:maxHostnameLen]
		}
		if got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAgentEnroll_KeyHashRoundTrip(t *testing.T) {
	raw := "sk-wgit-en-test-roundtrip"
	if crypto.HashKeySHA256(raw) == raw {
		t.Fatalf("HashKeySHA256 must not return plaintext")
	}
	if crypto.HashKeySHA256(raw) != crypto.HashKeySHA256(raw) {
		t.Fatalf("HashKeySHA256 must be deterministic")
	}
}

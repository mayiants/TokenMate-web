package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ai-proxy/ai-proxy-go/dto"
	"github.com/ai-proxy/ai-proxy-go/internal/audit"
	"github.com/ai-proxy/ai-proxy-go/internal/auth"
	"github.com/ai-proxy/ai-proxy-go/internal/config"
	"github.com/ai-proxy/ai-proxy-go/internal/retention"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

const testEnt = "3f2b8c1e-5d6a-4e7f-9a0b-1c2d3e4f5a6b"

func TestRetentionCapture_PolicyModes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name         string
		mode         string // "" => context key unset (older Portal)
		isStream     bool
		nilResponse  bool
		wantRecord   bool
		wantReqBody  bool
		wantRespBody bool
		wantCaptured bool
		wantTrunc    bool
		wantMode     string
	}{
		{"unset defaults to standard", "", false, false, true, true, true, true, false, "standard"},
		{"standard non-stream", "standard", false, false, true, true, true, true, false, "standard"},
		{"standard stream has no response body", "standard", true, true, true, true, false, false, true, "standard"},
		{"metadata blanks bodies", "metadata", false, false, true, false, false, false, true, "metadata"},
		{"off writes nothing", "off", false, false, false, false, false, false, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockRetentionStore{}
			svc := NewHandlerService(&config.Config{Server: config.ServerConfig{Mode: "test"}}, noopLogger())
			svc.SetRetentionStore(mock)

			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request, _ = http.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(nil))
			c.Request.Header.Set("User-Agent", "ua/1")
			c.Set("trace_id", "trace-p")
			c.Set("enterprise_id", testEnt)
			c.Set("retention_days", 14)
			if tt.mode != "" {
				c.Set("retention_mode", tt.mode)
			}

			resp := []byte("resp")
			if tt.nilResponse {
				resp = nil // streaming handlers never buffer the response
			}
			svc.captureRetention(c, []byte("req"), resp, "openai", "gpt-4o", "uk", &dto.Usage{PromptTokens: 1}, 5, 200, tt.isStream, "chat_completions")

			if !tt.wantRecord {
				assert.Empty(t, mock.records)
				return
			}
			require.Len(t, mock.records, 1)
			r := mock.records[0]
			assert.Equal(t, tt.wantReqBody, len(r.Request.Body) > 0)
			assert.Equal(t, tt.wantRespBody, len(r.Response.Body) > 0)
			assert.Equal(t, tt.wantCaptured, r.Metadata.ResponseCaptured)
			assert.Equal(t, tt.wantTrunc, r.Metadata.Truncated)
			assert.Equal(t, tt.wantMode, r.Metadata.RetentionMode)
			assert.Equal(t, testEnt, r.Metadata.EnterpriseID)
			assert.Equal(t, 14, r.Metadata.RetentionDays)
			if tt.mode == "metadata" {
				assert.Empty(t, r.Request.Headers, "metadata mode keeps no request headers")
				assert.Equal(t, "openai", r.Metadata.Provider, "metadata still recorded")
			}
		})
	}
}

func TestRetentionCapture_WritesTenantLayoutAndRejectsBadEnterprise(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name    string
		entID   string
		wantDir string
	}{
		{"valid enterprise", testEnt, filepath.Join("ent", testEnt, "7d")},
		{"traversal attempt lands in _personal", "../../x", filepath.Join("ent", "_personal", "7d")},
		{"no enterprise lands in _personal", "", filepath.Join("ent", "_personal", "7d")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			store, err := retention.NewDiskStore(retention.Config{Enabled: true, Directory: dir, BatchSize: 1, FlushEvery: time.Hour}, zap.NewNop())
			require.NoError(t, err)
			defer store.Close()
			svc := NewHandlerService(&config.Config{Server: config.ServerConfig{Mode: "test"}}, noopLogger())
			svc.SetRetentionStore(store)

			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request, _ = http.NewRequest("POST", "/v1/x", nil)
			c.Set("trace_id", "trace-l")
			c.Set("enterprise_id", tt.entID)
			c.Set("retention_mode", "standard")
			c.Set("retention_days", 7)
			svc.captureRetention(c, []byte("r"), []byte("s"), "p", "m", "", nil, 1, 200, false, "t")

			matches, _ := filepath.Glob(filepath.Join(dir, tt.wantDir, "*", "*", "*", "*", "trace-l.json.gz"))
			assert.Len(t, matches, 1)
			files, _ := listFiles(dir)
			assert.Len(t, files, 1)
		})
	}
}

func TestLogAudit_FillsIdentityFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var got audit.BatchUsageReport
	portal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
	}))
	defer portal.Close()
	cfg := &config.Config{
		Portal: config.PortalConfig{URL: portal.URL},
		Audit:  config.AuditConfig{Enabled: true, BatchSize: 100, FlushInterval: time.Hour},
	}
	al, err := audit.NewLogger(cfg, zap.NewNop())
	require.NoError(t, err)
	defer al.Close()

	svc := NewHandlerService(cfg, noopLogger())
	svc.handlerDeps = &HandlerDependencies{AuditLogger: al}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request, _ = http.NewRequest("POST", "/v1/chat/completions", nil)
	c.Request.RemoteAddr = "203.0.113.9:5555"
	c.Request.Header.Set("User-Agent", "sdk/2.0")
	c.Set("trace_id", "trace-join")

	svc.logAudit(c, audit.AuditEntry{Timestamp: time.Now(), Model: "m", APIKeyHash: "hash"})
	svc.logAudit(c, audit.AuditEntry{TraceID: "explicit", Timestamp: time.Now(), Endpoint: "/custom"})
	require.NoError(t, al.Flush())

	require.Len(t, got.Records, 2)
	assert.Equal(t, "trace-join", got.Records[0].TraceID)
	assert.Equal(t, "203.0.113.9", got.Records[0].ClientIP)
	assert.Equal(t, "sdk/2.0", got.Records[0].UserAgent)
	assert.Equal(t, "/v1/chat/completions", got.Records[0].Endpoint)
	assert.Equal(t, "explicit", got.Records[1].TraceID, "explicit values win")
	assert.Equal(t, "/custom", got.Records[1].Endpoint)
	assert.True(t, al.WasLogged("trace-join"))
}

// adminFixture wires the real admin auth middleware and a disk store.
func adminFixture(t *testing.T) (*HandlerService, *retention.DiskStore, string, *observer.ObservedLogs) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	store, err := retention.NewDiskStore(retention.Config{Enabled: true, Directory: dir, BatchSize: 1, FlushEvery: time.Hour}, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	core, logs := observer.New(zap.InfoLevel)
	cfg := &config.Config{
		Server:    config.ServerConfig{Mode: "test"},
		RateLimit: config.RateLimitConfig{Enabled: false},
		Admin:     config.AdminConfig{Secret: "admin-secret-that-is-long-enough-for-tests"},
	}
	svc := NewHandlerService(cfg, zap.New(core))
	svc.authMiddleware = auth.NewMiddleware(cfg, zap.NewNop())
	svc.rateLimitMiddleware = noopRateLimitMiddleware()
	svc.SetRetentionStore(store)
	svc.router = gin.New()
	setupRoutes(svc)
	return svc, store, dir, logs
}

func adminDo(svc *HandlerService, method, path string, signed bool) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = "198.51.100.7:4000"
	if signed {
		// The signature covers the path without the query string.
		signAdminRequest(req, svc.cfg.Admin.Secret)
	}
	svc.router.ServeHTTP(w, req)
	return w
}

func TestRetentionAdmin_RequiresAuth(t *testing.T) {
	svc, _, _, logs := adminFixture(t)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/admin/retention/enterprises/" + testEnt + "/objects"},
		{"DELETE", "/admin/retention/enterprises/" + testEnt + "?confirm=" + testEnt},
		{"GET", "/admin/retention/objects/x"},
	} {
		w := adminDo(svc, tc.method, tc.path, false)
		assert.Equal(t, http.StatusForbidden, w.Code, tc.method+" "+tc.path)
	}
	assert.Zero(t, logs.Len(), "unauthenticated calls never reach the handlers")
}

func TestRetentionAdmin_ListGetPurge(t *testing.T) {
	svc, store, dir, logs := adminFixture(t)
	store.Record(&retention.Record{TraceID: "t-1", Metadata: retention.Metadata{EnterpriseID: testEnt, RetentionDays: 5, UserID: "u1"}})
	store.Record(&retention.Record{TraceID: "t-2", Metadata: retention.Metadata{EnterpriseID: testEnt, RetentionDays: 5}})
	other := "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	store.Record(&retention.Record{TraceID: "t-other", Metadata: retention.Metadata{EnterpriseID: other, RetentionDays: 5}})

	// List
	w := adminDo(svc, "GET", "/admin/retention/enterprises/"+testEnt+"/objects?limit=1", true)
	require.Equal(t, 200, w.Code, w.Body.String())
	var list struct {
		Count   int                    `json:"count"`
		Objects []retention.ObjectInfo `json:"objects"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &list))
	assert.Equal(t, 1, list.Count)

	w = adminDo(svc, "GET", "/admin/retention/enterprises/"+testEnt+"/objects", true)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &list))
	assert.Equal(t, 2, list.Count)

	// Bad inputs
	assert.Equal(t, 400, adminDo(svc, "GET", "/admin/retention/enterprises/not-a-uuid/objects", true).Code)
	assert.Equal(t, 400, adminDo(svc, "GET", "/admin/retention/enterprises/"+testEnt+"/objects?limit=abc", true).Code)

	// Read: tenant-scoped fast path, and tenant isolation
	w = adminDo(svc, "GET", "/admin/retention/objects/t-1?enterprise_id="+testEnt, true)
	require.Equal(t, 200, w.Code)
	assert.Equal(t, 404, adminDo(svc, "GET", "/admin/retention/objects/t-other?enterprise_id="+testEnt, true).Code)
	assert.Equal(t, 200, adminDo(svc, "GET", "/admin/retention/objects/t-other", true).Code, "legacy global lookup still works")

	// Purge needs confirm
	assert.Equal(t, 400, adminDo(svc, "DELETE", "/admin/retention/enterprises/"+testEnt, true).Code)
	assert.Equal(t, 400, adminDo(svc, "DELETE", "/admin/retention/enterprises/"+testEnt+"?confirm="+other, true).Code)
	_, err := store.GetForEnterprise(testEnt, "t-1")
	require.NoError(t, err, "nothing deleted without a matching confirm")

	w = adminDo(svc, "DELETE", "/admin/retention/enterprises/"+testEnt+"?confirm="+testEnt, true)
	require.Equal(t, 200, w.Code, w.Body.String())
	var purge map[string]int64
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &purge))
	assert.Equal(t, int64(2), purge["deleted_files"])
	assert.Positive(t, purge["deleted_bytes"])
	_, err = store.GetForEnterprise(other, "t-other")
	assert.NoError(t, err)

	// Traversal-style tenant id is refused even with a matching confirm.
	assert.Equal(t, 400, adminDo(svc, "DELETE", "/admin/retention/enterprises/not-a-uuid?confirm=not-a-uuid", true).Code)
	assert.NotEqual(t, 200, adminDo(svc, "DELETE", "/admin/retention/enterprises/..%2F..%2Fx?confirm=..%2F..%2Fx", true).Code)

	// Every access was logged (structured + file).
	entries := logs.FilterMessage("retention_admin_access").All()
	require.NotEmpty(t, entries)
	byAction := map[string]int{}
	for _, e := range entries {
		f := e.ContextMap()
		byAction[f["action"].(string)]++
		assert.Equal(t, "198.51.100.7", f["client_ip"])
		assert.NotEmpty(t, f["result"])
	}
	assert.Positive(t, byAction["read"])
	assert.Positive(t, byAction["purge"])
	assert.Positive(t, byAction["list"])

	raw, err := os.ReadFile(filepath.Join(dir, "retention-access.log"))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	assert.Len(t, lines, len(entries))
	var last retention.AccessEntry
	for _, l := range lines {
		require.NoError(t, json.Unmarshal([]byte(l), &last))
		assert.Equal(t, "198.51.100.7", last.ClientIP)
	}
}

func TestRetentionAdmin_UnsupportedStore(t *testing.T) {
	svc, _, _, _ := adminFixture(t)
	svc.SetRetentionStore(&mockRetentionStore{})
	w := adminDo(svc, "GET", "/admin/retention/enterprises/"+testEnt+"/objects", true)
	assert.Equal(t, http.StatusNotImplemented, w.Code)
}

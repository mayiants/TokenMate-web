package server

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/mayiants/ai-proxy-go/internal/auth"
	"github.com/mayiants/ai-proxy-go/internal/config"
	"github.com/mayiants/ai-proxy-go/internal/retention"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestRetention_EndToEnd_SimulateChatCompletions(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dir := t.TempDir()
	store, err := retention.NewDiskStore(retention.Config{
		Enabled:    true,
		Directory:  dir,
		BatchSize:  1,
		FlushEvery: time.Hour,
	}, zap.NewNop())
	require.NoError(t, err)
	defer store.Close()

	// Auth now always goes through the Portal validate API — stand up a mock.
	const testAPIKey = "sk-wgit-retentiontestkey123456"
	portalSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"valid":true,"user_id":"user-ret-1","api_key_id":"key-ret-1","role":"user","access_mode":"general","balance":100,"key_type":"general"}`))
	}))
	defer portalSrv.Close()

	cfg := &config.Config{
		Server:    config.ServerConfig{Mode: "test"},
		RateLimit: config.RateLimitConfig{Enabled: false},
		Simulate:  config.SimulateConfig{Enabled: true, LatencyMs: 0},
		Admin:     config.AdminConfig{Secret: "admin-secret-that-is-long-enough-for-tests"},
		Portal:    config.PortalConfig{URL: portalSrv.URL},
	}

	service := NewHandlerService(cfg, noopLogger())
	service.authMiddleware = auth.NewMiddleware(cfg, noopLogger())
	service.rateLimitMiddleware = noopRateLimitMiddleware()
	service.SetHandlerDependencies(&HandlerDependencies{
		Logger:       noopLogger(),
		TokenCounter: nil,
		Billing:      nil,
		Simulate:     SimulateConfig{Enabled: cfg.Simulate.Enabled, LatencyMs: cfg.Simulate.LatencyMs},
	})
	service.SetRetentionStore(store)
	service.router = gin.New()
	setupRoutes(service)

	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")
	service.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "response body: %s", w.Body.String())

	// The retention store should now contain exactly one record.
	files, err := listFiles(dir)
	require.NoError(t, err)
	require.Len(t, files, 1, "expected one retention file")

	// Read and verify the stored record.
	traceID := filepath.Base(files[0])
	traceID = traceID[:len(traceID)-len(".json.gz")]
	rec, err := store.Get(traceID)
	require.NoError(t, err)
	// The proxy stores sha256(apiKey) as the key hash.
	keyHashSum := sha256.Sum256([]byte(testAPIKey))
	assert.Equal(t, "user-ret-1", rec.Metadata.UserID)
	assert.Equal(t, hex.EncodeToString(keyHashSum[:]), rec.Metadata.APIKeyHash)
	assert.Equal(t, "gpt-4o", rec.Metadata.Model)
	assert.Equal(t, 200, rec.Metadata.StatusCode)
	assert.False(t, rec.Metadata.IsStream)
	assert.Equal(t, "simulate", rec.Metadata.RequestType)
	assert.Equal(t, string(body), string(rec.Request.Body))
	assert.Contains(t, string(rec.Response.Body), "Simulated response")
}

func TestRetention_AdminEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dir := t.TempDir()
	store, err := retention.NewDiskStore(retention.Config{
		Enabled:    true,
		Directory:  dir,
		BatchSize:  1,
		FlushEvery: time.Hour,
	}, zap.NewNop())
	require.NoError(t, err)
	defer store.Close()

	store.Record(&retention.Record{
		TraceID: "trace-admin-1",
		Metadata: retention.Metadata{
			UserID:   "user-admin-1",
			Provider: "openai",
			Model:    "gpt-4o",
		},
	})
	store.Flush()

	cfg := &config.Config{
		Server:    config.ServerConfig{Mode: "test"},
		RateLimit: config.RateLimitConfig{Enabled: false},
		Admin:     config.AdminConfig{Secret: "admin-secret-that-is-long-enough-for-tests"},
	}

	service := NewHandlerService(cfg, noopLogger())
	service.authMiddleware = auth.NewMiddleware(cfg, noopLogger())
	service.rateLimitMiddleware = noopRateLimitMiddleware()
	service.SetRetentionStore(store)
	service.router = gin.New()
	setupRoutes(service)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/admin/retention/objects/trace-admin-1", nil)
	signAdminRequest(req, cfg.Admin.Secret)
	service.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "response body: %s", w.Body.String())

	var rec retention.Record
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rec))
	assert.Equal(t, "trace-admin-1", rec.TraceID)
	assert.Equal(t, "user-admin-1", rec.Metadata.UserID)
}

func listFiles(root string) ([]string, error) {
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func signAdminRequest(req *http.Request, secret string) {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := strconv.FormatInt(time.Now().UnixNano(), 10)
	message := fmt.Sprintf("%s%s%s%s", req.Method, req.URL.Path, timestamp, nonce)
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(message))
	signature := hex.EncodeToString(h.Sum(nil))
	req.Header.Set("X-Timestamp", timestamp)
	req.Header.Set("X-Nonce", nonce)
	req.Header.Set("X-Signature", signature)
}

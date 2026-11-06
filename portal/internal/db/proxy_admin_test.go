package db

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeProxy mimics the Proxy admin middleware (HMAC over METHOD+PATH+ts+nonce,
// replay-protected nonces) plus DELETE /admin/retention/enterprises/:id.
type fakeProxy struct {
	*httptest.Server
	mu    sync.Mutex
	calls []string // "METHOD path?query"
	nonce map[string]bool
}

func newFakeProxy(t *testing.T, secret string, status int, body string) *fakeProxy {
	t.Helper()
	fp := &fakeProxy{nonce: map[string]bool{}}
	fp.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ts, nonce, sig := r.Header.Get("X-Timestamp"), r.Header.Get("X-Nonce"), r.Header.Get("X-Signature")
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(r.Method + r.URL.Path + ts + nonce))
		fp.mu.Lock()
		replay := fp.nonce[nonce]
		fp.nonce[nonce] = true
		fp.calls = append(fp.calls, r.Method+" "+r.URL.RequestURI())
		fp.mu.Unlock()
		sec, err := strconv.ParseInt(ts, 10, 64)
		if err != nil || nonce == "" || replay || time.Since(time.Unix(sec, 0)) > time.Minute ||
			!hmac.Equal([]byte(sig), []byte(hex.EncodeToString(mac.Sum(nil)))) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(fp.Close)
	return fp
}

func TestPurgeEnterpriseRetention_Success(t *testing.T) {
	const secret = "proxy-admin-secret"
	fp := newFakeProxy(t, secret, http.StatusOK, `{"deleted_files":3,"deleted_bytes":1234}`)
	t.Setenv("PROXY_URL", fp.URL)
	t.Setenv("PROXY_CALLBACK_URLS", "")
	t.Setenv("PROXY_ADMIN_SECRET", "")
	t.Setenv("PROXY_SYNC_SECRET", secret)

	const ent = "11111111-2222-3333-4444-555555555555"
	res, err := (&DB{}).PurgeEnterpriseRetention(context.Background(), ent)
	require.NoError(t, err)
	assert.Equal(t, int64(3), res.DeletedFiles)
	assert.Equal(t, int64(1234), res.DeletedBytes)
	require.Len(t, res.Targets, 1)
	assert.Equal(t, http.StatusOK, res.Targets[0].Status)
	// Method, path and the confirm guard the Proxy endpoint requires.
	assert.Equal(t, []string{"DELETE /admin/retention/enterprises/" + ent + "?confirm=" + ent}, fp.calls)
}

func TestPurgeEnterpriseRetention_AdminSecretOverrideAndMultipleProxies(t *testing.T) {
	a := newFakeProxy(t, "admin-secret", http.StatusOK, `{}`)
	b := newFakeProxy(t, "admin-secret", http.StatusOK, `{"deleted_files":2}`)
	t.Setenv("PROXY_URL", a.URL+"/")
	t.Setenv("PROXY_CALLBACK_URLS", a.URL+", "+b.URL) // a is a duplicate of PROXY_URL
	t.Setenv("PROXY_ADMIN_SECRET", "admin-secret")
	t.Setenv("PROXY_SYNC_SECRET", "something-else")

	res, err := (&DB{}).PurgeEnterpriseRetention(context.Background(), "11111111-2222-3333-4444-555555555555")
	require.NoError(t, err)
	assert.Len(t, res.Targets, 2, "duplicate proxy URLs must be called once")
	assert.Len(t, a.calls, 1)
	assert.Len(t, b.calls, 1)
	assert.Equal(t, int64(2), res.DeletedFiles)
}

func TestPurgeEnterpriseRetention_Failures(t *testing.T) {
	const secret = "s3cret"
	const ent = "11111111-2222-3333-4444-555555555555"

	t.Run("not configured", func(t *testing.T) {
		t.Setenv("PROXY_URL", "")
		t.Setenv("PROXY_CALLBACK_URLS", "")
		t.Setenv("PROXY_SYNC_SECRET", secret)
		_, err := (&DB{}).PurgeEnterpriseRetention(context.Background(), ent)
		assert.ErrorIs(t, err, ErrProxyNotConfigured)
	})
	t.Run("secret missing", func(t *testing.T) {
		t.Setenv("PROXY_URL", "http://127.0.0.1:1")
		t.Setenv("PROXY_ADMIN_SECRET", "")
		t.Setenv("PROXY_SYNC_SECRET", "")
		_, err := (&DB{}).PurgeEnterpriseRetention(context.Background(), ent)
		assert.ErrorIs(t, err, ErrProxySecretMissing)
	})
	t.Run("unreachable proxy does not leak its URL", func(t *testing.T) {
		t.Setenv("PROXY_URL", "http://127.0.0.1:1") // nothing listens on port 1
		t.Setenv("PROXY_CALLBACK_URLS", "")
		t.Setenv("PROXY_SYNC_SECRET", secret)
		res, err := (&DB{}).PurgeEnterpriseRetention(context.Background(), ent)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "127.0.0.1")
		require.NotNil(t, res)
		assert.Equal(t, "proxy unreachable", res.Targets[0].Error)
	})
	t.Run("non-2xx response", func(t *testing.T) {
		fp := newFakeProxy(t, secret, http.StatusInternalServerError, `boom`)
		t.Setenv("PROXY_URL", fp.URL)
		t.Setenv("PROXY_CALLBACK_URLS", "")
		t.Setenv("PROXY_SYNC_SECRET", secret)
		res, err := (&DB{}).PurgeEnterpriseRetention(context.Background(), ent)
		require.Error(t, err)
		assert.Equal(t, http.StatusInternalServerError, res.Targets[0].Status)
	})
	t.Run("wrong secret is rejected by the proxy", func(t *testing.T) {
		fp := newFakeProxy(t, "right-secret", http.StatusOK, `{}`)
		t.Setenv("PROXY_URL", fp.URL)
		t.Setenv("PROXY_CALLBACK_URLS", "")
		t.Setenv("PROXY_SYNC_SECRET", "wrong-secret")
		res, err := (&DB{}).PurgeEnterpriseRetention(context.Background(), ent)
		require.Error(t, err)
		assert.Equal(t, http.StatusForbidden, res.Targets[0].Status)
	})
	t.Run("rejects ids that could alter the request path", func(t *testing.T) {
		t.Setenv("PROXY_URL", "http://127.0.0.1:1")
		t.Setenv("PROXY_SYNC_SECRET", secret)
		for _, bad := range []string{"", "../x", "a/b", "a?b", "a b"} {
			_, err := (&DB{}).PurgeEnterpriseRetention(context.Background(), bad)
			assert.Error(t, err, "id %q", bad)
		}
	})
}

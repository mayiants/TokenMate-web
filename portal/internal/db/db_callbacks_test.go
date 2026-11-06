package db

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestNotifyProxies_SendsHTTPSCallback(t *testing.T) {
	var gotSecret string
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/webhook/sync", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		gotSecret = r.Header.Get("X-Sync-Secret")
		var err error
		gotBody, err = io.ReadAll(r.Body)
		require.NoError(t, err)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	t.Setenv("PROXY_CALLBACK_URLS", server.URL)
	t.Setenv("PROXY_SYNC_SECRET", "test-secret")

	db := &DB{logger: zap.NewNop()}
	payload, _ := json.Marshal(SyncMessage{Type: "sync_needed", Source: "portal"})
	db.notifyProxies(context.Background(), "/api/webhook/sync", payload)

	// Wait for the goroutine to finish the HTTP request.
	time.Sleep(200 * time.Millisecond)

	assert.Equal(t, "test-secret", gotSecret)
	assert.JSONEq(t, string(payload), string(gotBody))
}

func TestNotifyProxies_MultipleCallbacks(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	t.Setenv("PROXY_CALLBACK_URLS", server.URL+","+server.URL)

	db := &DB{logger: zap.NewNop()}
	db.notifyProxies(context.Background(), "/api/webhook/sync", []byte(`{}`))

	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, 2, calls)
}

func TestNotifyProxies_NoEnv_DoesNothing(t *testing.T) {
	// Ensure env is empty for this test.
	_ = os.Unsetenv("PROXY_CALLBACK_URLS")

	db := &DB{logger: zap.NewNop()}
	// Should not panic or block.
	db.notifyProxies(context.Background(), "/api/webhook/sync", []byte(`{}`))
}

func TestPublishSyncEvent_CallbackIncludesConfigVersion(t *testing.T) {
	ctx := context.Background()
	if err := InitTestSchema(ctx, testutil.PostgresDSN(), zap.NewNop()); err != nil {
		t.Fatalf("init test schema: %v", err)
	}
	pool, err := pgxpool.New(ctx, testutil.PostgresDSN())
	if err != nil {
		t.Fatalf("connect to test db: %v", err)
	}
	defer pool.Close()

	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/webhook/sync", r.URL.Path)
		var err error
		gotBody, err = io.ReadAll(r.Body)
		require.NoError(t, err)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	t.Setenv("PROXY_CALLBACK_URLS", server.URL)

	database := &DB{Pool: pool, Redis: nil, logger: zap.NewNop()}
	database.PublishSyncEvent(ctx)

	time.Sleep(300 * time.Millisecond)

	require.NotEmpty(t, gotBody)
	var msg SyncMessage
	require.NoError(t, json.Unmarshal(gotBody, &msg))
	assert.Equal(t, "sync_needed", msg.Type)
	assert.Equal(t, "portal", msg.Source)
	assert.NotEmpty(t, msg.ConfigVersion, "callback payload must include config_version")
}

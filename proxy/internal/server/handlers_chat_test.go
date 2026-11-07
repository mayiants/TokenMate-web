package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mayiants/ai-proxy-go/internal/config"
	"github.com/mayiants/ai-proxy-go/pkg/keypool"
	"github.com/mayiants/ai-proxy-go/pkg/plugin"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestHandleChatCompletions_BodyFieldsNotLoggedAtInfo(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"created": 1234567890,
			"model":   "gpt-4o",
			"choices": []map[string]interface{}{
				{
					"index": 0,
					"message": map[string]interface{}{
						"role":    "assistant",
						"content": "hello back",
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]interface{}{
				"prompt_tokens":     5,
				"completion_tokens": 5,
				"total_tokens":      10,
			},
		})
	}))
	defer upstream.Close()

	obs, logs := observer.New(zap.DebugLevel)
	logger := zap.New(obs)

	cfg := &config.Config{
		Auth:   config.AuthConfig{EncryptionKey: "test-encryption-key-32-bytes-long!!"},
		Server: config.ServerConfig{Mode: "test"},
		Queue:  config.QueueConfig{Enabled: false},
	}

	kp := keypool.NewManager(cfg, zap.NewNop())
	defer kp.Close()
	require.NoError(t, kp.AddKey(&keypool.Key{
		KeyStatic: keypool.KeyStatic{
			ID:       "key-1",
			Provider: "openai",
			Key:      "sk-test",
			Priority: "normal",
			Models:   []string{"gpt-4o"},
			BaseURL:  upstream.URL,
		},
	}))

	svc := NewHandlerService(cfg, zap.NewNop())
	svc.SetKeyPoolManager(kp)
	svc.SetPluginChain(plugin.NewChain(zap.NewNop()))
	svc.SetHandlerDependencies(&HandlerDependencies{
		Logger:   logger,
		Simulate: SimulateConfig{Enabled: false},
	})

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/v1/chat/completions", bytes.NewReader([]byte(body)))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(keyIDKey), "key-1")
	c.Set(string(userIDKey), "user-1")
	c.Set("key_hash", "hash-1")
	c.Set("access_mode", "general")
	c.Set("key_type", "general")

	svc.handleChatCompletions(c)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var sawRequestBody, sawBodyPrefix bool
	for _, entry := range logs.All() {
		for _, f := range entry.Context {
			switch f.Key {
			case "request_body":
				sawRequestBody = true
				assert.Equal(t, zapcore.DebugLevel, entry.Level, "request_body must only be logged at Debug level")
			case "body_prefix":
				sawBodyPrefix = true
				assert.Equal(t, zapcore.DebugLevel, entry.Level, "body_prefix must only be logged at Debug level")
			}
		}
	}
	assert.True(t, sawRequestBody, "expected request_body in debug logs")
	assert.True(t, sawBodyPrefix, "expected body_prefix in debug logs")

	for _, entry := range logs.All() {
		if entry.Level < zapcore.InfoLevel {
			continue
		}
		for _, f := range entry.Context {
			if f.Key == "request_body" || f.Key == "body_prefix" {
				t.Errorf("body field %q emitted at %s level", f.Key, entry.Level)
			}
		}
	}
}

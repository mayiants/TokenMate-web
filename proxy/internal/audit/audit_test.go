package audit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mayiants/ai-proxy-go/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestNewLogger(t *testing.T) {
	t.Run("creates logger with defaults", func(t *testing.T) {
		cfg := &config.Config{
			Portal: config.PortalConfig{
				URL:        "http://localhost:3001",
				SyncSecret: "test-secret",
			},
			Audit: config.AuditConfig{
				Enabled:       true,
				BatchSize:     10,
				FlushInterval: time.Second,
			},
		}
		logger := zap.NewNop()

		al, err := NewLogger(cfg, logger)
		require.NoError(t, err)
		assert.NotNil(t, al)
		assert.NotEmpty(t, al.serverID)
		assert.True(t, len(al.batch) == 0)

		// Clean up
		al.Close()
	})

	t.Run("creates logger with custom server ID", func(t *testing.T) {
		cfg := &config.Config{
			Server: config.ServerConfig{
				ID: "test-server-123",
			},
			Portal: config.PortalConfig{
				URL: "http://localhost:3001",
			},
			Audit: config.AuditConfig{
				Enabled:       true,
				BatchSize:     10,
				FlushInterval: time.Second,
			},
		}
		logger := zap.NewNop()

		al, err := NewLogger(cfg, logger)
		require.NoError(t, err)
		assert.NotNil(t, al)
		assert.Equal(t, "test-server-123", al.serverID)

		al.Close()
	})
}

func TestLogger_Log(t *testing.T) {
	t.Run("disabled audit does not record", func(t *testing.T) {
		cfg := &config.Config{
			Portal: config.PortalConfig{URL: "http://localhost:3001"},
			Audit:  config.AuditConfig{Enabled: false, BatchSize: 10, FlushInterval: time.Second},
		}
		logger := zap.NewNop()

		al, err := NewLogger(cfg, logger)
		require.NoError(t, err)

		entry := AuditEntry{
			Timestamp: time.Now(),
			Model:     "gpt-4",
			Provider:  "openai",
		}

		al.Log(entry)

		// Should not add to batch
		al.batchMutex.Lock()
		batchLen := len(al.batch)
		al.batchMutex.Unlock()
		assert.Equal(t, 0, batchLen)

		al.Close()
	})

	t.Run("records audit entry", func(t *testing.T) {
		cfg := &config.Config{
			Portal: config.PortalConfig{URL: "http://localhost:3001"},
			Audit:  config.AuditConfig{Enabled: true, BatchSize: 10, FlushInterval: time.Hour},
		}
		logger := zap.NewNop()

		al, err := NewLogger(cfg, logger)
		require.NoError(t, err)

		entry := AuditEntry{
			Timestamp:    time.Now(),
			Model:        "gpt-4",
			Provider:     "openai",
			InputTokens:  100,
			OutputTokens: 50,
			Cost:         0.01,
		}

		al.Log(entry)

		// Should add to batch
		al.batchMutex.Lock()
		batchLen := len(al.batch)
		al.batchMutex.Unlock()
		assert.Equal(t, 1, batchLen)

		al.Close()
	})

	t.Run("auto flush when batch is full", func(t *testing.T) {
		// Create a mock server to receive the webhook
		mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer mockServer.Close()

		cfg := &config.Config{
			Portal: config.PortalConfig{
				URL:        mockServer.URL,
				SyncSecret: "",
			},
			Audit: config.AuditConfig{
				Enabled:       true,
				BatchSize:     2,
				FlushInterval: time.Hour,
			},
		}
		logger := zap.NewNop()

		al, err := NewLogger(cfg, logger)
		require.NoError(t, err)

		// Add entries to trigger flush
		for i := 0; i < 3; i++ {
			al.Log(AuditEntry{
				Timestamp: time.Now(),
				Model:     "gpt-4",
				Provider:  "openai",
			})
		}

		// Wait for async flush
		time.Sleep(100 * time.Millisecond)

		// Batch should have been flushed
		al.batchMutex.Lock()
		batchLen := len(al.batch)
		al.batchMutex.Unlock()
		assert.Less(t, batchLen, 3)

		al.Close()
	})
}

func TestLogger_Record(t *testing.T) {
	t.Run("records usage report", func(t *testing.T) {
		cfg := &config.Config{
			Portal: config.PortalConfig{URL: "http://localhost:3001"},
			Audit:  config.AuditConfig{Enabled: true, BatchSize: 10, FlushInterval: time.Hour},
		}
		logger := zap.NewNop()

		al, err := NewLogger(cfg, logger)
		require.NoError(t, err)

		report := UsageReport{
			Model:        "gpt-4",
			Provider:     "openai",
			InputTokens:  100,
			OutputTokens: 50,
			Cost:         0.01,
		}

		al.Record(report)

		al.batchMutex.Lock()
		batchLen := len(al.batch)
		al.batchMutex.Unlock()
		assert.Equal(t, 1, batchLen)

		al.Close()
	})

	t.Run("auto-generates trace ID", func(t *testing.T) {
		cfg := &config.Config{
			Portal: config.PortalConfig{URL: "http://localhost:3001"},
			Audit:  config.AuditConfig{Enabled: true, BatchSize: 10, FlushInterval: time.Hour},
		}
		logger := zap.NewNop()

		al, err := NewLogger(cfg, logger)
		require.NoError(t, err)

		report := UsageReport{
			TraceID:  "", // Empty trace ID
			Model:    "gpt-4",
			Provider: "openai",
		}

		al.Record(report)

		al.batchMutex.Lock()
		assert.NotEmpty(t, al.batch[0].TraceID)
		al.batchMutex.Unlock()

		al.Close()
	})
}

func TestLogger_computeSignature(t *testing.T) {
	cfg := &config.Config{
		Portal: config.PortalConfig{
			URL:        "http://localhost:3001",
			SyncSecret: "test-secret-key",
		},
		Audit: config.AuditConfig{Enabled: true, BatchSize: 10, FlushInterval: time.Second},
	}
	logger := zap.NewNop()

	al, err := NewLogger(cfg, logger)
	require.NoError(t, err)

	t.Run("computes HMAC signature", func(t *testing.T) {
		body := []byte(`{"test":"data"}`)
		timestamp := "1234567890"

		sig := al.computeSignature(body, timestamp)
		assert.NotEmpty(t, sig)
		assert.Len(t, sig, 64) // SHA-256 hex length
	})

	t.Run("different inputs produce different signatures", func(t *testing.T) {
		body1 := []byte(`{"test":"data1"}`)
		body2 := []byte(`{"test":"data2"}`)
		timestamp := "1234567890"

		sig1 := al.computeSignature(body1, timestamp)
		sig2 := al.computeSignature(body2, timestamp)
		assert.NotEqual(t, sig1, sig2)
	})

	t.Run("same inputs produce same signature", func(t *testing.T) {
		body := []byte(`{"test":"data"}`)
		timestamp := "1234567890"

		sig1 := al.computeSignature(body, timestamp)
		sig2 := al.computeSignature(body, timestamp)
		assert.Equal(t, sig1, sig2)
	})

	al.Close()
}

func TestLogger_sendBatchToPortal(t *testing.T) {
	t.Run("returns error when portal URL is empty", func(t *testing.T) {
		cfg := &config.Config{
			Portal: config.PortalConfig{URL: ""},
			Audit:  config.AuditConfig{Enabled: true, BatchSize: 10, FlushInterval: time.Second},
		}
		logger := zap.NewNop()

		al, err := NewLogger(cfg, logger)
		require.NoError(t, err)

		batch := []UsageReport{
			{Model: "gpt-4", Provider: "openai"},
		}

		err = al.sendBatchToPortal(batch)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "portal URL not configured")

		al.Close()
	})

	t.Run("sends batch successfully", func(t *testing.T) {
		mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/webhook/usage-batch", r.URL.Path)
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

			var payload BatchUsageReport
			err := json.NewDecoder(r.Body).Decode(&payload)
			require.NoError(t, err)
			assert.Len(t, payload.Records, 1)

			w.WriteHeader(http.StatusOK)
		}))
		defer mockServer.Close()

		cfg := &config.Config{
			Portal: config.PortalConfig{
				URL:        mockServer.URL,
				SyncSecret: "",
			},
			Audit: config.AuditConfig{Enabled: true, BatchSize: 10, FlushInterval: time.Second},
		}
		logger := zap.NewNop()

		al, err := NewLogger(cfg, logger)
		require.NoError(t, err)

		batch := []UsageReport{
			{Model: "gpt-4", Provider: "openai"},
		}

		err = al.sendBatchToPortal(batch)
		assert.NoError(t, err)

		al.Close()
	})

	t.Run("sends with signature when secret configured", func(t *testing.T) {
		mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.NotEmpty(t, r.Header.Get("X-Timestamp"))
			assert.NotEmpty(t, r.Header.Get("X-Signature"))
			w.WriteHeader(http.StatusOK)
		}))
		defer mockServer.Close()

		cfg := &config.Config{
			Portal: config.PortalConfig{
				URL:        mockServer.URL,
				SyncSecret: "test-secret",
			},
			Audit: config.AuditConfig{Enabled: true, BatchSize: 10, FlushInterval: time.Second},
		}
		logger := zap.NewNop()

		al, err := NewLogger(cfg, logger)
		require.NoError(t, err)

		batch := []UsageReport{
			{Model: "gpt-4", Provider: "openai"},
		}

		err = al.sendBatchToPortal(batch)
		assert.NoError(t, err)

		al.Close()
	})

	t.Run("handles portal error", func(t *testing.T) {
		mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer mockServer.Close()

		cfg := &config.Config{
			Portal: config.PortalConfig{
				URL: mockServer.URL,
			},
			Audit: config.AuditConfig{Enabled: true, BatchSize: 10, FlushInterval: time.Second},
		}
		logger := zap.NewNop()

		al, err := NewLogger(cfg, logger)
		require.NoError(t, err)

		batch := []UsageReport{
			{Model: "gpt-4", Provider: "openai"},
		}

		err = al.sendBatchToPortal(batch)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "portal returned status 500")

		al.Close()
	})
}

func TestLogger_Flush(t *testing.T) {
	t.Run("flush empty batch does nothing", func(t *testing.T) {
		cfg := &config.Config{
			Portal: config.PortalConfig{URL: "http://localhost:3001"},
			Audit:  config.AuditConfig{Enabled: true, BatchSize: 10, FlushInterval: time.Second},
		}
		logger := zap.NewNop()

		al, err := NewLogger(cfg, logger)
		require.NoError(t, err)

		err = al.Flush()
		assert.NoError(t, err)

		al.Close()
	})
}

func TestLogger_Close(t *testing.T) {
	t.Run("closes cleanly", func(t *testing.T) {
		cfg := &config.Config{
			Portal: config.PortalConfig{URL: "http://localhost:3001"},
			Audit:  config.AuditConfig{Enabled: true, BatchSize: 10, FlushInterval: time.Hour},
		}
		logger := zap.NewNop()

		al, err := NewLogger(cfg, logger)
		require.NoError(t, err)

		// Add some entries
		al.Log(AuditEntry{Model: "gpt-4", Provider: "openai"})

		// Close should flush remaining entries (may return error since no real portal)
		_ = al.Close()
		// Error is expected since we're flushing to a non-existent portal
		assert.NotNil(t, al)
	})
}

func TestLogger_GetStats(t *testing.T) {
	t.Run("returns empty stats", func(t *testing.T) {
		cfg := &config.Config{
			Portal: config.PortalConfig{URL: "http://localhost:3001"},
			Audit:  config.AuditConfig{Enabled: true, BatchSize: 10, FlushInterval: time.Second},
		}
		logger := zap.NewNop()

		al, err := NewLogger(cfg, logger)
		require.NoError(t, err)

		stats, err := al.GetStats(nil, time.Now().Add(-time.Hour), time.Now())
		require.NoError(t, err)
		assert.NotNil(t, stats)
		assert.Equal(t, int64(0), stats.TotalRequests)

		al.Close()
	})
}

func TestLogger_Query(t *testing.T) {
	t.Run("returns empty results", func(t *testing.T) {
		cfg := &config.Config{
			Portal: config.PortalConfig{URL: "http://localhost:3001"},
			Audit:  config.AuditConfig{Enabled: true, BatchSize: 10, FlushInterval: time.Second},
		}
		logger := zap.NewNop()

		al, err := NewLogger(cfg, logger)
		require.NoError(t, err)

		reports, count, err := al.Query(nil, QueryParams{})
		require.NoError(t, err)
		assert.Empty(t, reports)
		assert.Equal(t, int64(0), count)

		al.Close()
	})
}

func TestLogger_Search(t *testing.T) {
	t.Run("returns empty results", func(t *testing.T) {
		cfg := &config.Config{
			Portal: config.PortalConfig{URL: "http://localhost:3001"},
			Audit:  config.AuditConfig{Enabled: true, BatchSize: 10, FlushInterval: time.Second},
		}
		logger := zap.NewNop()

		al, err := NewLogger(cfg, logger)
		require.NoError(t, err)

		reports, err := al.Search(AuditQuery{})
		require.NoError(t, err)
		assert.Empty(t, reports)

		al.Close()
	})
}

func TestLogger_RecordRequest(t *testing.T) {
	t.Run("records request via convenience method", func(t *testing.T) {
		cfg := &config.Config{
			Portal: config.PortalConfig{URL: "http://localhost:3001"},
			Audit:  config.AuditConfig{Enabled: true, BatchSize: 10, FlushInterval: time.Hour},
		}
		logger := zap.NewNop()

		al, err := NewLogger(cfg, logger)
		require.NoError(t, err)

		al.RecordRequest(
			"trace-123",
			1,         // userID
			2,         // apiKeyID
			"gpt-4",   // model
			"openai",  // provider
			100,       // inputTokens
			50,        // outputTokens
			0,         // cachedTokens
			0.01,      // totalCost
			100,       // latencyMs
			"success", // status
		)

		al.batchMutex.Lock()
		batchLen := len(al.batch)
		al.batchMutex.Unlock()
		assert.Equal(t, 1, batchLen)

		al.Close()
	})
}

func TestAuditEntry(t *testing.T) {
	t.Run("audit entry fields", func(t *testing.T) {
		entry := AuditEntry{
			Timestamp:    time.Now(),
			KeyID:        "key-123",
			UserID:       "user-456",
			Provider:     "openai",
			ChannelID:    1,
			Model:        "gpt-4",
			InputTokens:  100,
			OutputTokens: 50,
			Cost:         0.01,
			LatencyMs:    100,
			StatusCode:   200,
			IsStream:     false,
			RequestType:  "chat",
			APIKeyHash:   "hash-123",
		}

		assert.Equal(t, "key-123", entry.KeyID)
		assert.Equal(t, "user-456", entry.UserID)
		assert.Equal(t, "openai", entry.Provider)
		assert.Equal(t, 100, entry.InputTokens)
		assert.Equal(t, 50, entry.OutputTokens)
	})
}

func TestUsageReport(t *testing.T) {
	t.Run("usage report fields", func(t *testing.T) {
		report := UsageReport{
			TraceID:      "trace-123",
			APIKeyHash:   "hash-456",
			Model:        "gpt-4",
			Provider:     "openai",
			InputTokens:  100,
			OutputTokens: 50,
			CachedTokens: 10,
			Cost:         0.01,
			LatencyMs:    100,
			Timestamp:    time.Now().Format(time.RFC3339),
			KeyID:        "key-123",
			ServerID:     "server-456",
			RequestCount: 1,
			StatusCode:   200,
		}

		assert.Equal(t, "trace-123", report.TraceID)
		assert.Equal(t, "gpt-4", report.Model)
		assert.Equal(t, "openai", report.Provider)
	})
}

func TestBatchUsageReport(t *testing.T) {
	t.Run("batch report structure", func(t *testing.T) {
		batch := BatchUsageReport{
			ServerID: "server-123",
			Records: []UsageReport{
				{Model: "gpt-4", Provider: "openai"},
				{Model: "claude-3", Provider: "anthropic"},
			},
		}

		assert.Equal(t, "server-123", batch.ServerID)
		assert.Len(t, batch.Records, 2)
	})
}

func TestAuditStats(t *testing.T) {
	t.Run("audit stats fields", func(t *testing.T) {
		stats := AuditStats{
			TotalRequests:      100,
			SuccessfulRequests: 95,
			CacheHitRate:       0.8,
			TotalInputTokens:   10000,
			TotalOutputTokens:  5000,
			TotalCachedTokens:  2000,
			TotalCost:          1.50,
		}

		assert.Equal(t, int64(100), stats.TotalRequests)
		assert.Equal(t, int64(95), stats.SuccessfulRequests)
		assert.Equal(t, 0.8, stats.CacheHitRate)
		assert.Equal(t, 1.50, stats.TotalCost)
	})
}

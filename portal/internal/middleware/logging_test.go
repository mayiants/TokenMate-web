package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func newTestLogger() (*zap.Logger, *observer.ObservedLogs) {
	core, observed := observer.New(zapcore.DebugLevel)
	return zap.New(core), observed
}

func TestStructuredLogging_InfoOnSuccess(t *testing.T) {
	logger, observed := newTestLogger()
	handler := StructuredLogging(logger)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/health", nil)

	handler(c)

	assert.Equal(t, http.StatusOK, w.Code)
	logs := observed.All()
	assert.GreaterOrEqual(t, len(logs), 1)

	entry := logs[0]
	assert.Equal(t, zapcore.InfoLevel, entry.Level)
	assert.Equal(t, "request", entry.Message)

	fields := entry.ContextMap()
	assert.Contains(t, fields, "method")
	assert.Equal(t, "GET", fields["method"])
	assert.Contains(t, fields, "path")
	assert.Equal(t, "/api/health", fields["path"])
	assert.Contains(t, fields, "status")
	assert.Contains(t, fields, "latency")
	assert.Contains(t, fields, "client_ip")
}

func TestStructuredLogging_LogsFields(t *testing.T) {
	logger, observed := newTestLogger()
	handler := StructuredLogging(logger)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/data", nil)
	c.Set("request_id", "req-abc-123")
	c.Set("user_id", "user-456")

	handler(c)

	logs := observed.All()
	assert.GreaterOrEqual(t, len(logs), 1)
	fields := logs[0].ContextMap()

	assert.Equal(t, "POST", fields["method"])
	assert.Equal(t, "/api/data", fields["path"])
	assert.Equal(t, "req-abc-123", fields["request_id"])
	assert.Equal(t, "user-456", fields["user_id"])
}

func TestStructuredLogging_NoUserIDWhenNotSet(t *testing.T) {
	logger, observed := newTestLogger()
	handler := StructuredLogging(logger)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/test", nil)

	handler(c)

	logs := observed.All()
	assert.GreaterOrEqual(t, len(logs), 1)

	fields := logs[0].ContextMap()
	if uid, ok := fields["user_id"]; ok {
		assert.Equal(t, "", uid)
	}
}

func TestStructuredLogging_Statuses(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		wantLevel zapcore.Level
	}{
		{"200 info", http.StatusOK, zapcore.InfoLevel},
		{"201 info", http.StatusCreated, zapcore.InfoLevel},
		{"400 warn", http.StatusBadRequest, zapcore.WarnLevel},
		{"404 warn", http.StatusNotFound, zapcore.WarnLevel},
		{"500 error", http.StatusInternalServerError, zapcore.ErrorLevel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger, observed := newTestLogger()
			handler := StructuredLogging(logger)

			router := gin.New()
			router.Use(handler)
			router.GET("/test", func(c *gin.Context) {
				c.Status(tt.status)
			})

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			router.ServeHTTP(w, req)

			logs := observed.All()
			assert.GreaterOrEqual(t, len(logs), 1)
			assert.Equal(t, tt.wantLevel, logs[0].Level, "level mismatch for status %d", tt.status)
			assert.Equal(t, int64(tt.status), logs[0].ContextMap()["status"])
		})
	}
}

func TestToString_Helper(t *testing.T) {
	tests := []struct {
		name string
		val  interface{}
		want string
	}{
		{"nil", nil, ""},
		{"string", "hello", "hello"},
		{"int", 42, ""},
		{"empty string", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toString(tt.val)
			assert.Equal(t, tt.want, got)
		})
	}
}

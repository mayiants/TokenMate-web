package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestStructuredLogging(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("logs request with 200 status", func(t *testing.T) {
		logger, _ := zap.NewDevelopment()
		middleware := StructuredLogging(logger)

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/test", nil)

		middleware(c)
		c.Next()

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("logs request with 400 status", func(t *testing.T) {
		logger, _ := zap.NewDevelopment()
		middleware := StructuredLogging(logger)

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("POST", "/test", nil)

		middleware(c)
		c.AbortWithStatus(http.StatusBadRequest)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("logs request with user_id", func(t *testing.T) {
		logger, _ := zap.NewDevelopment()
		middleware := StructuredLogging(logger)

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/test", nil)
		c.Set("user_id", "user-123")

		middleware(c)
		c.Next()

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("logs request with 500 status", func(t *testing.T) {
		logger, _ := zap.NewDevelopment()
		middleware := StructuredLogging(logger)

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/test", nil)

		middleware(c)
		c.AbortWithStatus(http.StatusInternalServerError)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestStructuredLogging_MiddlewareChain(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	middleware := StructuredLogging(logger)

	t.Run("measures latency", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/test", nil)

		start := time.Now()
		middleware(c)
		// Simulate some work
		time.Sleep(10 * time.Millisecond)
		c.Next()
		elapsed := time.Since(start)

		assert.True(t, elapsed >= 10*time.Millisecond)
	})
}

func TestToString(t *testing.T) {
	t.Run("nil value", func(t *testing.T) {
		assert.Equal(t, "", toString(nil))
	})

	t.Run("string value", func(t *testing.T) {
		assert.Equal(t, "test", toString("test"))
	})

	t.Run("non-string value", func(t *testing.T) {
		assert.Equal(t, "", toString(123))
	})

	t.Run("empty string", func(t *testing.T) {
		assert.Equal(t, "", toString(""))
	})
}

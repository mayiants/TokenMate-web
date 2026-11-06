package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestFilterClientHeaders_AllowsXRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/v1/chat/completions", nil)
	c.Request.Header.Set("X-Request-ID", "req-123")
	c.Request.Header.Set("Content-Type", "application/json")

	s := NewHandlerService(nil, noopLogger())
	filtered := s.filterClientHeaders(c)
	assert.Equal(t, "req-123", filtered.Get("X-Request-ID"))
}

func TestFilterClientHeaders_DropsAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/v1/chat/completions", nil)
	c.Request.Header.Set("Authorization", "Bearer user-token")
	c.Request.Header.Set("X-API-Key", "user-key")

	s := NewHandlerService(nil, noopLogger())
	filtered := s.filterClientHeaders(c)
	assert.Empty(t, filtered.Get("Authorization"))
	assert.Empty(t, filtered.Get("X-API-Key"))
}

func TestFilterClientHeaders_DropsCloudflareAndSecHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/v1/chat/completions", nil)
	c.Request.Header.Set("CF-Connecting-IP", "1.2.3.4")
	c.Request.Header.Set("Sec-Fetch-Site", "same-origin")
	c.Request.Header.Set("X-Request-ID", "req-123")

	s := NewHandlerService(nil, noopLogger())
	filtered := s.filterClientHeaders(c)
	assert.Empty(t, filtered.Get("CF-Connecting-IP"))
	assert.Empty(t, filtered.Get("Sec-Fetch-Site"))
	assert.Equal(t, "req-123", filtered.Get("X-Request-ID"))
}

func TestFilterClientHeaders_DropsHopByHop(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/v1/chat/completions", nil)
	c.Request.Header.Set("Connection", "keep-alive")
	c.Request.Header.Set("Transfer-Encoding", "chunked")
	c.Request.Header.Set("X-Request-ID", "req-123")

	s := NewHandlerService(nil, noopLogger())
	filtered := s.filterClientHeaders(c)
	assert.Empty(t, filtered.Get("Connection"))
	assert.Empty(t, filtered.Get("Transfer-Encoding"))
	assert.Equal(t, "req-123", filtered.Get("X-Request-ID"))
}

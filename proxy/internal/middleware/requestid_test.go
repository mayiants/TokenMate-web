package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("generates new request ID", func(t *testing.T) {
		middleware := RequestID()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/test", nil)

		middleware(c)
		c.Next()

		// Check that a request ID was generated
		rid, exists := c.Get("request_id")
		assert.True(t, exists)
		assert.NotEmpty(t, rid)

		// Check that it's a valid UUID format
		ridStr, ok := rid.(string)
		assert.True(t, ok)
		assert.Len(t, ridStr, 36) // UUID v4 length
	})

	t.Run("echoes client request ID, assigns internal unique ID", func(t *testing.T) {
		middleware := RequestID()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/test", nil)
		c.Request.Header.Set(RequestIDHeader, "existing-request-id-123")

		middleware(c)
		c.Next()

		// Internal request ID is always generated (never the client's):
		// clients may reuse or zero-out their IDs, which breaks trace_id-keyed billing
		rid := c.GetString("request_id")
		assert.NotEmpty(t, rid)
		assert.NotEqual(t, "existing-request-id-123", rid)
		assert.Len(t, rid, 36)

		// Client ID is preserved for correlation and echoed in the response
		assert.Equal(t, "existing-request-id-123", c.GetString("client_request_id"))
		assert.Equal(t, "existing-request-id-123", w.Header().Get(RequestIDHeader))
	})

	t.Run("sets request ID in response header", func(t *testing.T) {
		middleware := RequestID()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/test", nil)

		middleware(c)
		c.Next()

		// Check response header
		headerRid := w.Header().Get(RequestIDHeader)
		assert.NotEmpty(t, headerRid)
		assert.Equal(t, headerRid, c.GetString("request_id"))
	})

	t.Run("request ID is unique", func(t *testing.T) {
		middleware := RequestID()
		ids := make(map[string]bool)

		for i := 0; i < 100; i++ {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request, _ = http.NewRequest("GET", "/test", nil)

			middleware(c)
			c.Next()

			rid := c.GetString("request_id")
			assert.False(t, ids[rid], "Request ID should be unique")
			ids[rid] = true
		}
	})
}

func TestRequestIDHeader(t *testing.T) {
	t.Run("header constant is correct", func(t *testing.T) {
		assert.Equal(t, "X-Request-ID", RequestIDHeader)
	})
}

func TestRequestID_TraceIDHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name      string
		clientRID string
	}{
		{"no client request id", ""},
		{"client request id is echoed but trace id stays internal", "client-supplied-id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gin.New()
			r.Use(RequestID())
			r.GET("/", func(c *gin.Context) { c.String(200, c.GetString("trace_id")) })

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.clientRID != "" {
				req.Header.Set(RequestIDHeader, tt.clientRID)
			}
			r.ServeHTTP(w, req)

			assert.Equal(t, w.Body.String(), w.Header().Get(TraceIDHeader))
			if tt.clientRID != "" {
				assert.Equal(t, tt.clientRID, w.Header().Get(RequestIDHeader))
				assert.NotEqual(t, tt.clientRID, w.Header().Get(TraceIDHeader))
			} else {
				assert.Equal(t, w.Header().Get(RequestIDHeader), w.Header().Get(TraceIDHeader))
			}
		})
	}
}

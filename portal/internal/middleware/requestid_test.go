package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestRequestID_GeneratesWhenMissing(t *testing.T) {
	handler := RequestID()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/test", nil)

	handler(c)

	rid := c.GetString("request_id")
	assert.NotEmpty(t, rid, "request_id should be generated")
	assert.Len(t, rid, 36, "UUID should be 36 characters")

	respRID := w.Header().Get(RequestIDHeader)
	assert.Equal(t, rid, respRID)
}

func TestRequestID_PropagatesExisting(t *testing.T) {
	handler := RequestID()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/test", nil)
	c.Request.Header.Set(RequestIDHeader, "my-custom-request-id-123")

	handler(c)

	rid := c.GetString("request_id")
	assert.Equal(t, "my-custom-request-id-123", rid)

	respRID := w.Header().Get(RequestIDHeader)
	assert.Equal(t, "my-custom-request-id-123", respRID)
}

func TestRequestID_MultipleCallsUnique(t *testing.T) {
	handler := RequestID()

	rids := make(map[string]bool)
	for i := 0; i < 10; i++ {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/test", nil)
		handler(c)

		rid := c.GetString("request_id")
		assert.NotEmpty(t, rid)
		if rids[rid] {
			t.Error("duplicate UUID generated")
		}
		rids[rid] = true
	}
	assert.Len(t, rids, 10, "should generate 10 unique request IDs")
}

func TestRequestID_HeaderConstant(t *testing.T) {
	assert.Equal(t, "X-Request-ID", RequestIDHeader)
}

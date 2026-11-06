package middleware

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestNewIPRateLimiter_AllowsWhenUnderLimit(t *testing.T) {
	handler := NewIPRateLimiter(100, nil) // 100 req/s

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/test", nil)
	c.Request.RemoteAddr = "192.168.1.1:12345"

	handler(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestNewIPRateLimiter_DefaultRate(t *testing.T) {
	// ratePerSec <= 0 should default to 10
	handler := NewIPRateLimiter(0, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/test", nil)
	c.Request.RemoteAddr = "192.168.1.2:12345"

	handler(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestNewIPRateLimiter_NegativeRate(t *testing.T) {
	handler := NewIPRateLimiter(-5, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/test", nil)
	c.Request.RemoteAddr = "192.168.1.3:12345"

	handler(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestNewIPRateLimiter_BlocksWhenOverLimit(t *testing.T) {
	// Very low rate so burst easily exceeds it
	handler := NewIPRateLimiter(1, nil) // 1 token/s, capacity 1

	// Use up the single token
	w1 := httptest.NewRecorder()
	c1, _ := gin.CreateTestContext(w1)
	c1.Request = httptest.NewRequest(http.MethodGet, "/api/test", nil)
	c1.Request.RemoteAddr = "192.168.1.4:12345"
	handler(c1)

	// Second request should be rate limited (no time elapsed for refill)
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest(http.MethodGet, "/api/test", nil)
	c2.Request.RemoteAddr = "192.168.1.4:12345"
	handler(c2)

	assert.Equal(t, http.StatusTooManyRequests, w2.Code)
}

func TestNewIPRateLimiter_DifferentIPsIndependent(t *testing.T) {
	handler := NewIPRateLimiter(1, nil)

	// IP 1 uses its token
	w1 := httptest.NewRecorder()
	c1, _ := gin.CreateTestContext(w1)
	c1.Request = httptest.NewRequest(http.MethodGet, "/api/test", nil)
	c1.Request.RemoteAddr = "10.0.0.1:12345"
	handler(c1)
	assert.Equal(t, http.StatusOK, w1.Code)

	// IP 2 should still have its token
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest(http.MethodGet, "/api/test", nil)
	c2.Request.RemoteAddr = "10.0.0.2:12345"
	handler(c2)
	assert.Equal(t, http.StatusOK, w2.Code)
}

func TestNewIPRateLimiter_RefillsOverTime(t *testing.T) {
	handler := NewIPRateLimiter(100, nil) // 100 tokens/s, capacity 100

	ip := "192.168.2.1:12345"

	// Use up all tokens (capacity=100), need 101 requests to exhaust capacity
	for i := 0; i < 101; i++ {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/test", nil)
		c.Request.RemoteAddr = ip
		handler(c)
		if w.Code == http.StatusTooManyRequests {
			// Exhausted, stop
			break
		}
	}

	// Wait for refill
	time.Sleep(50 * time.Millisecond)

	// Now should allow one request
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/test", nil)
	c.Request.RemoteAddr = ip
	handler(c)
	assert.Equal(t, http.StatusOK, w.Code, "should allow request after refill period")
}

func TestNewIPRateLimiter_ConcurrencySafe(t *testing.T) {
	handler := NewIPRateLimiter(10000, nil) // generous limit
	ip := "192.168.3.1:12345"

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/test", nil)
			c.Request.RemoteAddr = ip
			handler(c)
		}()
	}
	wg.Wait()

	// All should pass without panic under generous limit
	finalW := httptest.NewRecorder()
	finalC, _ := gin.CreateTestContext(finalW)
	finalC.Request = httptest.NewRequest(http.MethodGet, "/api/test", nil)
	finalC.Request.RemoteAddr = ip
	handler(finalC)
	assert.Equal(t, http.StatusOK, finalW.Code)
}

func TestNewIPRateLimiter_CleanupDoesNotPanic(t *testing.T) {
	handler := NewIPRateLimiter(10, nil)
	// Trigger some requests to populate buckets
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/test", nil)
		c.Request.RemoteAddr = "192.168.4.1:12345"
		handler(c)
	}
	// Cleanup goroutine runs in background – test just verifies no panics
}

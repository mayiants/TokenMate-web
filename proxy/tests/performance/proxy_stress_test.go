package performance

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ai-proxy/ai-proxy-go/internal/middleware"
	"github.com/ai-proxy/ai-proxy-go/pkg/circuitbreaker"
	"github.com/ai-proxy/ai-proxy-go/pkg/metrics"
	"github.com/gin-gonic/gin"
)

func setupProxyBenchRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "healthy"})
	})

	r.GET("/metrics", func(c *gin.Context) {
		c.String(http.StatusOK, metrics.GetMetrics().ToPrometheusFormat())
	})

	r.POST("/v1/chat/completions", func(c *gin.Context) {
		var req map[string]interface{}
		json.NewDecoder(c.Request.Body).Decode(&req)
		c.JSON(http.StatusOK, gin.H{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"model":   req["model"],
			"choices": []map[string]interface{}{},
		})
	})

	return r
}

// BenchmarkProxyThroughput measures max proxy requests per second
func BenchmarkProxyThroughput(b *testing.B) {
	router := setupProxyBenchRouter()
	body := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hello"}]}`)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req, _ := http.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
		}
	})
}

// TestProxyConcurrentRequests tests concurrent proxy request handling
func TestProxyConcurrentRequests(t *testing.T) {
	router := setupProxyBenchRouter()
	concurrency := 100
	requestsPerGoroutine := 50
	body := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hello"}]}`)

	var successCount atomic.Int64
	var wg sync.WaitGroup

	start := time.Now()
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < requestsPerGoroutine; j++ {
				req, _ := http.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				if w.Code == http.StatusOK {
					successCount.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	total := successCount.Load()
	rps := float64(total) / elapsed.Seconds()

	t.Logf("Proxy concurrent test: %d goroutines x %d requests", concurrency, requestsPerGoroutine)
	t.Logf("Success: %d, RPS: %.0f, Elapsed: %v", total, rps, elapsed)
}

// TestProxyLatencyDistribution measures P50/P90/P99 latency
func TestProxyLatencyDistribution(t *testing.T) {
	router := setupProxyBenchRouter()
	body := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hello"}]}`)
	totalRequests := 1000

	latencies := make([]time.Duration, totalRequests)
	var wg sync.WaitGroup

	for i := 0; i < totalRequests; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			start := time.Now()
			req, _ := http.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			latencies[idx] = time.Since(start)
		}(i)
	}
	wg.Wait()

	sort.Slice(latencies, func(i, j int) bool {
		return latencies[i] < latencies[j]
	})

	t.Logf("Proxy latency distribution (%d requests):", totalRequests)
	t.Logf("  P50: %v", latencies[totalRequests*50/100])
	t.Logf("  P90: %v", latencies[totalRequests*90/100])
	t.Logf("  P99: %v", latencies[totalRequests*99/100])
	t.Logf("  Min: %v, Max: %v", latencies[0], latencies[totalRequests-1])
}

// TestCircuitBreakerBehavior verifies circuit breaker state transitions
func TestCircuitBreakerBehavior(t *testing.T) {
	cb := circuitbreaker.NewCircuitBreaker(circuitbreaker.Config{
		Threshold:    3,
		ResetTimeout: 100 * time.Millisecond,
		HalfOpenMax:  1,
	})

	// Should start closed
	if cb.State() != circuitbreaker.Closed {
		t.Error("Expected initial state to be Closed")
	}

	// Record failures until open
	for i := 0; i < 3; i++ {
		cb.RecordFailure()
	}
	if cb.State() != circuitbreaker.Open {
		t.Error("Expected state to be Open after 3 failures")
	}

	// Should not allow requests when open
	if cb.Allow() {
		t.Error("Expected Allow() to return false when Open")
	}

	// Wait for reset timeout
	time.Sleep(150 * time.Millisecond)

	// Should transition to half-open after timeout
	if !cb.Allow() {
		t.Error("Expected Allow() to return true after reset timeout (HalfOpen)")
	}
	if cb.State() != circuitbreaker.HalfOpen {
		t.Error("Expected state to be HalfOpen after timeout")
	}

	// Record success to close
	cb.RecordSuccess()
	cb.RecordSuccess()
	cb.RecordSuccess()
	if cb.State() != circuitbreaker.Closed {
		t.Error("Expected state to be Closed after successes in HalfOpen")
	}
}

// TestProviderBreakers tests per-provider circuit breakers
func TestProviderBreakers(t *testing.T) {
	pb := circuitbreaker.NewProviderBreakers(circuitbreaker.Config{
		Threshold:    2,
		ResetTimeout: 50 * time.Millisecond,
	})

	// Open circuit for openai
	pb.RecordFailure("openai")
	pb.RecordFailure("openai")

	if pb.Allow("openai") {
		t.Error("Expected openai circuit to be open")
	}

	// anthropic should still be closed
	if !pb.Allow("anthropic") {
		t.Error("Expected anthropic circuit to be closed")
	}

	status := pb.Status()
	t.Logf("Circuit breaker status: %v", status)
}

// TestProxyResourceUsage measures memory/goroutine usage under load
func TestProxyResourceUsage(t *testing.T) {
	router := setupProxyBenchRouter()
	body := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hello"}]}`)

	var m1, m2 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)

	concurrency := 50
	requestsPerGoroutine := 200
	var wg sync.WaitGroup

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < requestsPerGoroutine; j++ {
				req, _ := http.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
			}
		}()
	}
	wg.Wait()

	runtime.GC()
	runtime.ReadMemStats(&m2)

	t.Logf("Proxy resource usage:")
	t.Logf("  Memory allocated: %d KB", (int64(m2.Alloc)-int64(m1.Alloc))/1024)
	t.Logf("  Heap objects: %d", m2.HeapObjects)
	t.Logf("  Goroutines: %d", runtime.NumGoroutine())
	t.Logf("  GC cycles: %d", m2.NumGC-m1.NumGC)
}

// TestProxyMetricsEndpoint verifies Prometheus metrics format
func TestProxyMetricsEndpoint(t *testing.T) {
	router := setupProxyBenchRouter()

	m := metrics.GetMetrics()
	m.IncrementRequests(true, false)
	m.AddTokens(100, 50, 0)

	req, _ := http.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}

	expectedMetrics := []string{
		"proxy_requests_total",
		"proxy_tokens_input",
		"proxy_tokens_output",
	}
	for _, name := range expectedMetrics {
		if !bytes.Contains(w.Body.Bytes(), []byte(name)) {
			t.Errorf("Missing metric: %s", name)
		}
	}
}

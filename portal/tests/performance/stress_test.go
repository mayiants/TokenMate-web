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

	"github.com/ai-proxy/ai-portal-go/internal/middleware"
	"github.com/ai-proxy/ai-portal-go/pkg/metrics"
	"github.com/gin-gonic/gin"
)

func setupBenchRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())

	r.GET("/api/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	r.GET("/metrics", func(c *gin.Context) {
		c.String(http.StatusOK, metrics.GetMetrics().ToPrometheusFormat())
	})

	r.GET("/api/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "ok"})
	})

	r.POST("/api/test", func(c *gin.Context) {
		var req map[string]interface{}
		_ = json.NewDecoder(c.Request.Body).Decode(&req)
		c.JSON(http.StatusOK, req)
	})

	return r
}

// BenchmarkThroughput measures max requests per second
func BenchmarkThroughput(b *testing.B) {
	router := setupBenchRouter()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req, _ := http.NewRequest("GET", "/api/test", nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
		}
	})
}

// BenchmarkJSONPost measures JSON request/response throughput
func BenchmarkJSONPost(b *testing.B) {
	router := setupBenchRouter()
	body := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hello"}]}`)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req, _ := http.NewRequest("POST", "/api/test", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
		}
	})
}

// BenchmarkMetricsEndpoint measures /metrics endpoint performance
func BenchmarkMetricsEndpoint(b *testing.B) {
	router := setupBenchRouter()

	// Pre-populate some metrics
	m := metrics.GetMetrics()
	m.TotalRequests.Add(10000)
	m.SuccessfulRequests.Add(9500)
	m.FailedRequests.Add(500)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req, _ := http.NewRequest("GET", "/metrics", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
	}
}

// TestConcurrentRequests tests handling of concurrent requests
func TestConcurrentRequests(t *testing.T) {
	router := setupBenchRouter()
	concurrency := 100
	requestsPerGoroutine := 100

	var successCount atomic.Int64
	var failCount atomic.Int64
	var wg sync.WaitGroup

	start := time.Now()
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < requestsPerGoroutine; j++ {
				req, _ := http.NewRequest("GET", "/api/test", nil)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				if w.Code == http.StatusOK {
					successCount.Add(1)
				} else {
					failCount.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	total := successCount.Load() + failCount.Load()
	var rps float64
	if elapsed.Seconds() > 0 {
		rps = float64(total) / elapsed.Seconds()
	}

	t.Logf("Concurrent test: %d goroutines x %d requests = %d total", concurrency, requestsPerGoroutine, total)
	t.Logf("Success: %d, Failed: %d", successCount.Load(), failCount.Load())
	t.Logf("Elapsed: %v, RPS: %.0f", elapsed, rps)

	if failCount.Load() > 0 {
		t.Errorf("Expected 0 failures, got %d", failCount.Load())
	}

	minRPS := float64(1000)
	if rps < minRPS {
		t.Logf("WARNING: RPS %.0f is below target %.0f (may be CPU-limited in test env)", rps, minRPS)
	}
}

// TestLatencyDistribution measures P50, P90, P99 latency
func TestLatencyDistribution(t *testing.T) {
	router := setupBenchRouter()
	totalRequests := 1000

	latencies := make([]time.Duration, totalRequests)
	var wg sync.WaitGroup

	for i := 0; i < totalRequests; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			start := time.Now()
			req, _ := http.NewRequest("GET", "/api/test", nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			latencies[idx] = time.Since(start)
		}(i)
	}
	wg.Wait()

	sort.Slice(latencies, func(i, j int) bool {
		return latencies[i] < latencies[j]
	})

	p50 := latencies[totalRequests*50/100]
	p90 := latencies[totalRequests*90/100]
	p99 := latencies[totalRequests*99/100]

	t.Logf("Latency distribution (%d requests):", totalRequests)
	t.Logf("  P50: %v", p50)
	t.Logf("  P90: %v", p90)
	t.Logf("  P99: %v", p99)
	t.Logf("  Min: %v", latencies[0])
	t.Logf("  Max: %v", latencies[totalRequests-1])

	if p99 > 100*time.Millisecond {
		t.Logf("WARNING: P99 latency %v exceeds 100ms target", p99)
	}
}

// TestResourceUsage measures memory and goroutine usage under load
func TestResourceUsage(t *testing.T) {
	router := setupBenchRouter()

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
				req, _ := http.NewRequest("GET", "/api/test", nil)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
			}
		}()
	}
	wg.Wait()

	runtime.GC()
	runtime.ReadMemStats(&m2)

	allocDiff := int64(m2.Alloc) - int64(m1.Alloc)
	goroutines := runtime.NumGoroutine()

	t.Logf("Resource usage under load:")
	t.Logf("  Memory allocated: %d KB", allocDiff/1024)
	t.Logf("  Heap objects: %d", m2.HeapObjects)
	t.Logf("  Goroutines: %d", goroutines)
	t.Logf("  GC cycles: %d", m2.NumGC-m1.NumGC)

	if goroutines > 1000 {
		t.Logf("WARNING: High goroutine count %d (possible leak)", goroutines)
	}
}

// TestHealthCheckPerformance measures health check response time
func TestHealthCheckPerformance(t *testing.T) {
	router := setupBenchRouter()

	latencies := make([]time.Duration, 100)
	for i := 0; i < 100; i++ {
		start := time.Now()
		req, _ := http.NewRequest("GET", "/api/health", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		latencies[i] = time.Since(start)
	}

	sort.Slice(latencies, func(i, j int) bool {
		return latencies[i] < latencies[j]
	})

	t.Logf("Health check latency: P50=%v, P99=%v", latencies[50], latencies[99])

	if latencies[99] > 10*time.Millisecond {
		t.Logf("WARNING: Health check P99 %v exceeds 10ms target", latencies[99])
	}
}

// TestMetricsEndpointAccuracy verifies Prometheus format output
func TestMetricsEndpointAccuracy(t *testing.T) {
	router := setupBenchRouter()

	m := metrics.GetMetrics()
	m.TotalRequests.Add(42)
	m.ActiveUsers.Store(10)
	m.ActiveKeys.Store(25)

	req, _ := http.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if len(body) == 0 {
		t.Error("Empty metrics response")
	}

	expectedMetrics := []string{
		"portal_requests_total",
		"portal_active_users",
		"portal_active_keys",
	}
	for _, name := range expectedMetrics {
		if !bytes.Contains(w.Body.Bytes(), []byte(name)) {
			t.Errorf("Missing metric: %s", name)
		}
	}
}

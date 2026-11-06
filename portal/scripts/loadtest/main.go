package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type TestConfig struct {
	Users       int
	RPMPerUser  int
	DurationSec int
	ProxyURL    string
	PortalURL   string
	Warmup      bool
	TestMode    string // "concurrency" or "throughput"
}

type TestResults struct {
	TotalRequests   int64
	SuccessRequests int64
	RateLimited     int64
	FailedRequests  int64
	TotalLatencyMs  int64
	MinLatencyMs    int64
	MaxLatencyMs    int64
	P50LatencyMs    int64
	P90LatencyMs    int64
	P99LatencyMs    int64
}

type LatencyRecord struct {
	latencies []int64
	mu        sync.Mutex
}

type APIKeyResponse struct {
	Key    string `json:"key"`
	APIKey string `json:"api_key"`
	ID     string `json:"id"`
}

type AuthResponse struct {
	Token       string `json:"token"`
	AccessToken string `json:"access_token"`
}

func main() {
	// Parse arguments
	users := flag.Int("users", 5000, "Number of user keys to create")
	rpmLimit := flag.Int("rpm", 100000, "RPM limit per key")
	duration := flag.Int("duration", 30, "Test duration in seconds")
	concurrency := flag.Int("concurrency", 300, "Concurrent connections for throughput test")
	warmup := flag.Bool("warmup", true, "Warm up cache before burst test")
	testMode := flag.String("mode", "both", "Test mode: concurrency, throughput, or both")
	flag.Parse()

	fmt.Println("========================================")
	fmt.Println("  Rate Limit Performance Test (Enhanced)")
	fmt.Println("========================================")
	fmt.Println()

	// Common setup
	proxyURL := "http://127.0.0.1:8080"
	portalURL := "http://127.0.0.1:3001"

	// Check services once
	if !checkServices(proxyURL, portalURL) {
		os.Exit(1)
	}

	// Get admin token once
	token, err := getAdminToken(portalURL)
	if err != nil {
		fmt.Printf("✗ Failed to get admin token: %v\n", err)
		os.Exit(1)
	}

	// Create API keys ONCE and reuse for both tests
	fmt.Printf("Creating %d API keys (reused for all tests)...\n", *users)
	keys, err := createAPIKeys(portalURL, token, *users, *rpmLimit)
	if err != nil {
		fmt.Printf("✗ Failed to create API keys: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Created %d API keys\n", len(keys))
	fmt.Println()

	// Warmup once if enabled
	if *warmup {
		fmt.Println("--- Warmup Phase (Cache Pre-population) ---")
		warmupKeys(proxyURL, keys, *concurrency)
		fmt.Println("✓ Cache warmed for all keys")
		fmt.Println()
	}

	// Test 1: High Concurrency Test
	if *testMode == "concurrency" || *testMode == "both" {
		runConcurrencyTestWithKeys(proxyURL, keys, *warmup)
		fmt.Println()
	}

	// Test 2: High Throughput Test
	if *testMode == "throughput" || *testMode == "both" {
		runThroughputTestWithKeys(proxyURL, keys, *concurrency, *duration)
	}
}

// runConcurrencyTestWithKeys - Test with high concurrency using existing keys
func runConcurrencyTestWithKeys(proxyURL string, keys []string, warmup bool) {
	users := len(keys)
	fmt.Println("========================================")
	fmt.Println("  Test 1: High Concurrency")
	fmt.Println("========================================")
	fmt.Printf("Scenario: %d users, each with own key\n", users)
	fmt.Printf("Goal: Test system handling many concurrent connections\n")
	fmt.Println()

	// Burst test - each user makes 5 requests concurrently
	fmt.Println("--- Burst Test ---")
	fmt.Printf("Each of %d users making 5 requests concurrently\n", users)

	results := runConcurrencyBurst(proxyURL, keys, 5)
	printResults("Concurrency Burst", results, 0)
}

// runThroughputTestWithKeys - Test with high throughput using existing keys
func runThroughputTestWithKeys(proxyURL string, keys []string, concurrency, duration int) {
	users := len(keys)
	fmt.Println("========================================")
	fmt.Println("  Test 2: High Throughput")
	fmt.Println("========================================")
	fmt.Printf("Scenario: %d concurrent connections, %d seconds\n", concurrency, duration)
	fmt.Printf("Using %d user keys (rotated)\n", users)
	fmt.Printf("Goal: Test sustained throughput under load\n")
	fmt.Println()

	results := runSustainedLoad(proxyURL, keys, concurrency, duration)
	printResults("Sustained Load", results, duration)
}

// warmupKeys - Send one request per key to warm up cache
func warmupKeys(proxyURL string, keys []string, concurrency int) {
	fmt.Printf("Warming up cache with %d keys (concurrency: %d)...\n", len(keys), concurrency)

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        10000,
			MaxIdleConnsPerHost: 1000,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	reqBody := `{"model":"gpt-3.5-turbo","messages":[{"role":"user","content":"warmup"}],"max_tokens":5}`

	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)
	completed := int64(0)

	for i, key := range keys {
		wg.Add(1)
		go func(idx int, k string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			req, _ := http.NewRequest("POST", proxyURL+"/v1/chat/completions", stringReader(reqBody))
			req.Header.Set("Authorization", "Bearer "+k)
			req.Header.Set("Content-Type", "application/json")

			resp, err := client.Do(req)
			if err == nil {
				_ = resp.Body.Close()
			}

			if atomic.AddInt64(&completed, 1)%500 == 0 {
				fmt.Printf("  Warmed %d / %d keys...\n", completed, len(keys))
			}
		}(i, key)
	}

	wg.Wait()
}

// runConcurrencyBurst - Each user makes N requests concurrently
func runConcurrencyBurst(proxyURL string, keys []string, requestsPerUser int) *TestResults {
	results := &TestResults{
		MinLatencyMs: 1<<63 - 1,
	}
	latencyRecords := &LatencyRecord{latencies: make([]int64, 0, len(keys)*requestsPerUser)}

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        10000,
			MaxIdleConnsPerHost: 1000,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	reqBody := `{"model":"gpt-3.5-turbo","messages":[{"role":"user","content":"hi"}],"max_tokens":5}`

	var wg sync.WaitGroup
	start := time.Now()

	for _, key := range keys {
		for i := 0; i < requestsPerUser; i++ {
			wg.Add(1)
			go func(k string) {
				defer wg.Done()

				reqStart := time.Now()
				req, _ := http.NewRequest("POST", proxyURL+"/v1/chat/completions", stringReader(reqBody))
				req.Header.Set("Authorization", "Bearer "+k)
				req.Header.Set("Content-Type", "application/json")

				resp, err := client.Do(req)
				latency := time.Since(reqStart).Milliseconds()

				atomic.AddInt64(&results.TotalRequests, 1)
				atomic.AddInt64(&results.TotalLatencyMs, latency)

				latencyRecords.mu.Lock()
				latencyRecords.latencies = append(latencyRecords.latencies, latency)
				latencyRecords.mu.Unlock()

				if err != nil {
					atomic.AddInt64(&results.FailedRequests, 1)
				} else {
					switch resp.StatusCode {
					case 200:
						atomic.AddInt64(&results.SuccessRequests, 1)
					case 429:
						atomic.AddInt64(&results.RateLimited, 1)
					default:
						atomic.AddInt64(&results.FailedRequests, 1)
					}
					_ = resp.Body.Close()
				}
			}(key)
		}
	}

	wg.Wait()
	duration := time.Since(start).Seconds()

	calculatePercentiles(results, latencyRecords)
	printProgress("Burst Complete", results, duration)

	return results
}

// runSustainedLoad - Sustained load with fixed concurrency
func runSustainedLoad(proxyURL string, keys []string, concurrency, durationSec int) *TestResults {
	results := &TestResults{
		MinLatencyMs: 1<<63 - 1,
	}
	latencyRecords := &LatencyRecord{latencies: make([]int64, 0, 100000)}

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        10000,
			MaxIdleConnsPerHost: 1000,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	reqBody := `{"model":"gpt-3.5-turbo","messages":[{"role":"user","content":"hi"}],"max_tokens":5}`

	var wg sync.WaitGroup
	start := time.Now()
	end := start.Add(time.Duration(durationSec) * time.Second)

	// Progress reporter
	stopReporter := make(chan bool)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopReporter:
				return
			case <-ticker.C:
				elapsed := time.Since(start).Seconds()
				total := atomic.LoadInt64(&results.TotalRequests)
				success := atomic.LoadInt64(&results.SuccessRequests)
				rateLimited := atomic.LoadInt64(&results.RateLimited)
				avgLat := int64(0)
				if total > 0 {
					avgLat = atomic.LoadInt64(&results.TotalLatencyMs) / total
				}
				rps := int64(0)
				if elapsed > 0 {
					rps = int64(float64(total) / elapsed)
				}
				fmt.Printf("  [%ds] Total: %d, Success: %d, Rate-limited: %d, RPS: %d, Latency: %dms\n",
					int(elapsed), total, success, rateLimited, rps, avgLat)
			}
		}
	}()

	// Launch workers
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			key := keys[workerID%len(keys)]

			for time.Now().Before(end) {
				reqStart := time.Now()

				req, _ := http.NewRequest("POST", proxyURL+"/v1/chat/completions", stringReader(reqBody))
				req.Header.Set("Authorization", "Bearer "+key)
				req.Header.Set("Content-Type", "application/json")

				resp, err := client.Do(req)
				latency := time.Since(reqStart).Milliseconds()

				atomic.AddInt64(&results.TotalRequests, 1)
				atomic.AddInt64(&results.TotalLatencyMs, latency)

				latencyRecords.mu.Lock()
				latencyRecords.latencies = append(latencyRecords.latencies, latency)
				latencyRecords.mu.Unlock()

				if err != nil {
					atomic.AddInt64(&results.FailedRequests, 1)
				} else {
					switch resp.StatusCode {
					case 200:
						atomic.AddInt64(&results.SuccessRequests, 1)
					case 429:
						atomic.AddInt64(&results.RateLimited, 1)
					default:
						atomic.AddInt64(&results.FailedRequests, 1)
					}
					_ = resp.Body.Close()
				}

				time.Sleep(time.Microsecond * 100)
			}
		}(i)
	}

	wg.Wait()
	close(stopReporter)

	calculatePercentiles(results, latencyRecords)

	return results
}

func calculatePercentiles(results *TestResults, records *LatencyRecord) {
	records.mu.Lock()
	defer records.mu.Unlock()

	if len(records.latencies) == 0 {
		return
	}

	sort.Slice(records.latencies, func(i, j int) bool {
		return records.latencies[i] < records.latencies[j]
	})

	results.MinLatencyMs = records.latencies[0]
	results.MaxLatencyMs = records.latencies[len(records.latencies)-1]
	results.P50LatencyMs = records.latencies[len(records.latencies)*50/100]
	results.P90LatencyMs = records.latencies[len(records.latencies)*90/100]
	results.P99LatencyMs = records.latencies[len(records.latencies)*99/100]
}

func printResults(testName string, results *TestResults, durationSec int) {
	fmt.Println()
	fmt.Println("--- Results ---")
	fmt.Printf("Test: %s\n", testName)
	fmt.Println()

	total := results.TotalRequests
	if total == 0 {
		fmt.Println("No requests completed")
		return
	}

	fmt.Printf("Total Requests:     %d\n", total)
	fmt.Printf("Successful (200):   %d (%.1f%%)\n", results.SuccessRequests, float64(results.SuccessRequests)*100/float64(total))
	fmt.Printf("Rate Limited (429): %d (%.1f%%)\n", results.RateLimited, float64(results.RateLimited)*100/float64(total))
	fmt.Printf("Failed:             %d\n", results.FailedRequests)
	fmt.Println()

	avgLatency := results.TotalLatencyMs / total
	fmt.Printf("Latency:\n")
	fmt.Printf("  Min:  %dms\n", results.MinLatencyMs)
	fmt.Printf("  Avg:  %dms\n", avgLatency)
	fmt.Printf("  P50:  %dms\n", results.P50LatencyMs)
	fmt.Printf("  P90:  %dms\n", results.P90LatencyMs)
	fmt.Printf("  P99:  %dms\n", results.P99LatencyMs)
	fmt.Printf("  Max:  %dms\n", results.MaxLatencyMs)
	fmt.Println()

	if durationSec > 0 {
		rps := total / int64(durationSec)
		rpm := rps * 60
		fmt.Printf("Throughput:\n")
		fmt.Printf("  RPS: %d\n", rps)
		fmt.Printf("  RPM: %d\n", rpm)
		fmt.Println()
	}

	// Assessment
	fmt.Println("Assessment:")
	if results.P99LatencyMs < 200 {
		fmt.Printf("  ✓ Excellent P99 latency (%dms < 200ms)\n", results.P99LatencyMs)
	} else if results.P99LatencyMs < 500 {
		fmt.Printf("  ⚠ Good P99 latency (%dms)\n", results.P99LatencyMs)
	} else {
		fmt.Printf("  ✗ High P99 latency (%dms > 500ms)\n", results.P99LatencyMs)
	}

	if results.RateLimited > 0 {
		rateLimitRate := float64(results.RateLimited) * 100 / float64(total)
		if rateLimitRate < 5 {
			fmt.Printf("  ✓ Rate limiter working (%.1f%% rate limited)\n", rateLimitRate)
		} else {
			fmt.Printf("  ⚠ High rate limiting (%.1f%%)\n", rateLimitRate)
		}
	}
}

func printProgress(stage string, results *TestResults, duration float64) {
	total := atomic.LoadInt64(&results.TotalRequests)
	success := atomic.LoadInt64(&results.SuccessRequests)
	rateLimited := atomic.LoadInt64(&results.RateLimited)
	avgLat := int64(0)
	if total > 0 {
		avgLat = atomic.LoadInt64(&results.TotalLatencyMs) / total
	}
	rps := int64(0)
	if duration > 0 {
		rps = int64(float64(total) / duration)
	}
	fmt.Printf("  %s: Total=%d, Success=%d, Rate-limited=%d, RPS=%d, Latency=%dms\n",
		stage, total, success, rateLimited, rps, avgLat)
}

func checkServices(proxyURL, portalURL string) bool {
	fmt.Println("Checking services...")

	if !checkHealth(proxyURL + "/health") {
		fmt.Println("✗ Proxy not running")
		return false
	}
	fmt.Println("✓ Proxy is running")

	if !checkHealth(portalURL + "/api/health") {
		fmt.Println("✗ Portal not running")
		return false
	}
	fmt.Println("✓ Portal is running")
	fmt.Println()

	return true
}

func checkHealth(url string) bool {
	resp, err := http.Get(url)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode == 200
}

func getAdminToken(portalURL string) (string, error) {
	email := os.Getenv("LOADTEST_ADMIN_EMAIL")
	if email == "" {
		email = "admin@example.com"
	}
	password := os.Getenv("LOADTEST_ADMIN_PASSWORD")
	if password == "" {
		password = "Admin@123456!"
	}
	body := fmt.Sprintf(`{"email":"%s","password":"%s"}`, email, password)
	resp, err := http.Post(portalURL+"/api/auth/login", "application/json", stringReader(body))
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	var auth AuthResponse
	if err := json.NewDecoder(resp.Body).Decode(&auth); err != nil {
		return "", err
	}
	if auth.Token != "" {
		return auth.Token, nil
	}
	return auth.AccessToken, nil
}

func createAPIKeys(portalURL, token string, count, rpm int) ([]string, error) {
	keys := make([]string, 0, count)
	client := &http.Client{Timeout: 5 * time.Second}

	batchSize := 100
	for i := 0; i < count; i++ {
		body := fmt.Sprintf(`{"name":"perf-test-%d","rate_limit_rpm":%d,"rate_limit_tpm":100000}`, i, rpm)
		req, _ := http.NewRequest("POST", portalURL+"/api/apikeys", stringReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			continue
		}

		var keyResp APIKeyResponse
		if err := json.NewDecoder(resp.Body).Decode(&keyResp); err == nil {
			if keyResp.Key != "" {
				keys = append(keys, keyResp.Key)
			} else if keyResp.APIKey != "" {
				keys = append(keys, keyResp.APIKey)
			}
		}
		_ = resp.Body.Close()

		if (i+1)%batchSize == 0 {
			fmt.Printf("  Created %d / %d keys...\n", i+1, count)
		}
	}

	return keys, nil
}

func stringReader(s string) io.Reader {
	return &stringReadCloser{s: s}
}

type stringReadCloser struct {
	s   string
	pos int
}

func (r *stringReadCloser) Read(p []byte) (n int, err error) {
	if r.pos >= len(r.s) {
		return 0, io.EOF
	}
	n = copy(p, r.s[r.pos:])
	r.pos += n
	return n, nil
}

func (r *stringReadCloser) Close() error {
	return nil
}

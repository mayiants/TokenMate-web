package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// StressTestConfig - Configuration for stress test
type StressTestConfig struct {
	BaseURL        string
	APIKey         string
	Concurrency    int
	TotalRequests  int
	RPM            int
	Duration       time.Duration
	ReportInterval time.Duration
}

// Metrics - Stress test metrics
type Metrics struct {
	TotalRequests  int64
	SuccessCount   int64
	RateLimited    int64
	ErrorCount     int64
	TotalLatency   int64
	MinLatency     int64
	MaxLatency     int64
	RequestsPerSec float64
}

func main() {
	baseURL := flag.String("url", "http://localhost:8080", "Base URL of the proxy")
	apiKey := flag.String("key", "", "API key to use")
	concurrency := flag.Int("c", 10, "Concurrency level")
	totalRequests := flag.Int("n", 1000, "Total number of requests")
	rpm := flag.Int("rpm", 60, "Target RPM for rate limiting test")
	duration := flag.Duration("d", 60*time.Second, "Duration of the test")
	reportInterval := flag.Duration("report", 1*time.Second, "Report interval")
	testType := flag.String("test", "burst", "Test type: burst, sustained, ramp")
	flag.Parse()

	cfg := StressTestConfig{
		BaseURL:        *baseURL,
		APIKey:         *apiKey,
		Concurrency:    *concurrency,
		TotalRequests:  *totalRequests,
		RPM:            *rpm,
		Duration:       *duration,
		ReportInterval: *reportInterval,
	}

	if cfg.APIKey == "" {
		fmt.Println("Error: API key is required. Use -key flag")
		os.Exit(1)
	}

	fmt.Printf("Starting rate limiter stress test\n")
	fmt.Printf("URL: %s\n", cfg.BaseURL)
	fmt.Printf("Concurrency: %d\n", cfg.Concurrency)
	fmt.Printf("Total Requests: %d\n", cfg.TotalRequests)
	fmt.Printf("Target RPM: %d\n", cfg.RPM)
	fmt.Printf("Duration: %v\n", cfg.Duration)
	fmt.Printf("Test Type: %s\n", *testType)
	fmt.Println("---")

	switch *testType {
	case "burst":
		runBurstTest(cfg)
	case "sustained":
		runSustainedTest(cfg)
	case "ramp":
		runRampTest(cfg)
	default:
		fmt.Printf("Unknown test type: %s\n", *testType)
		os.Exit(1)
	}
}

// runBurstTest - Send all requests as fast as possible
func runBurstTest(cfg StressTestConfig) {
	var metrics Metrics
	metrics.MinLatency = int64(^uint64(0) >> 1) // Max int64

	var wg sync.WaitGroup
	requestsPerWorker := cfg.TotalRequests / cfg.Concurrency
	remaining := cfg.TotalRequests % cfg.Concurrency

	start := time.Now()
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	// Start reporter
	stopReport := make(chan bool)
	go reportMetrics(&metrics, start, cfg.ReportInterval, stopReport)

	// Start workers
	for i := 0; i < cfg.Concurrency; i++ {
		count := requestsPerWorker
		if i < remaining {
			count++
		}
		wg.Add(1)
		go func(workerID, reqCount int) {
			defer wg.Done()
			for j := 0; j < reqCount; j++ {
				makeRequest(client, cfg, &metrics)
			}
		}(i, count)
	}

	wg.Wait()
	close(stopReport)

	printFinalReport(&metrics, time.Since(start))
}

// runSustainedTest - Send requests at a sustained rate
func runSustainedTest(cfg StressTestConfig) {
	var metrics Metrics
	metrics.MinLatency = int64(^uint64(0) >> 1)

	start := time.Now()
	end := start.Add(cfg.Duration)

	var wg sync.WaitGroup
	requestInterval := time.Second / time.Duration(cfg.RPM)

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	// Start reporter
	stopReport := make(chan bool)
	go reportMetrics(&metrics, start, cfg.ReportInterval, stopReport)

	// Distribute requests across concurrency level
	requestsPerWorker := cfg.TotalRequests / cfg.Concurrency

	for i := 0; i < cfg.Concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			ticker := time.NewTicker(requestInterval)
			defer ticker.Stop()

			reqCount := 0
			for time.Now().Before(end) && reqCount < requestsPerWorker {
				makeRequest(client, cfg, &metrics)
				reqCount++
				<-ticker.C
			}
		}(i)
	}

	wg.Wait()
	close(stopReport)

	printFinalReport(&metrics, time.Since(start))
}

// runRampTest - Gradually increase request rate
func runRampTest(cfg StressTestConfig) {
	var metrics Metrics
	metrics.MinLatency = int64(^uint64(0) >> 1)

	start := time.Now()
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	// Start reporter
	stopReport := make(chan bool)
	go reportMetrics(&metrics, start, cfg.ReportInterval, stopReport)

	// Ramp from 1 RPS to target RPM/60 RPS over duration
	steps := 10
	stepDuration := cfg.Duration / time.Duration(steps)

	for step := 1; step <= steps; step++ {
		targetRPS := (cfg.RPM / 60) * step / steps
		if targetRPS < 1 {
			targetRPS = 1
		}

		fmt.Printf("Step %d: Target %d RPS\n", step, targetRPS)

		var wg sync.WaitGroup
		requestInterval := time.Second / time.Duration(targetRPS)

		stepEnd := time.Now().Add(stepDuration)
		for time.Now().Before(stepEnd) {
			for i := 0; i < cfg.Concurrency && time.Now().Before(stepEnd); i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					makeRequest(client, cfg, &metrics)
				}()
				time.Sleep(requestInterval / time.Duration(cfg.Concurrency))
			}
		}
		wg.Wait()
	}

	close(stopReport)
	printFinalReport(&metrics, time.Since(start))
}

// makeRequest - Make a single request and update metrics
func makeRequest(client *http.Client, cfg StressTestConfig, metrics *Metrics) {
	reqStart := time.Now()

	req, err := http.NewRequest("POST", cfg.BaseURL+"/v1/chat/completions", nil)
	if err != nil {
		atomic.AddInt64(&metrics.ErrorCount, 1)
		return
	}

	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		atomic.AddInt64(&metrics.ErrorCount, 1)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	latency := time.Since(reqStart).Microseconds()
	atomic.AddInt64(&metrics.TotalRequests, 1)
	atomic.AddInt64(&metrics.TotalLatency, latency)

	// Update min/max latency
	for {
		currentMin := atomic.LoadInt64(&metrics.MinLatency)
		if latency >= currentMin || atomic.CompareAndSwapInt64(&metrics.MinLatency, currentMin, latency) {
			break
		}
	}
	for {
		currentMax := atomic.LoadInt64(&metrics.MaxLatency)
		if latency <= currentMax || atomic.CompareAndSwapInt64(&metrics.MaxLatency, currentMax, latency) {
			break
		}
	}

	switch resp.StatusCode {
	case 200:
		atomic.AddInt64(&metrics.SuccessCount, 1)
	case 429:
		atomic.AddInt64(&metrics.RateLimited, 1)
	default:
		atomic.AddInt64(&metrics.ErrorCount, 1)
	}

	// Read and discard body
	_, _ = io.Copy(io.Discard, resp.Body)
}

// reportMetrics - Periodically report metrics
func reportMetrics(metrics *Metrics, start time.Time, interval time.Duration, stop <-chan bool) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var lastTotal int64

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			total := atomic.LoadInt64(&metrics.TotalRequests)
			success := atomic.LoadInt64(&metrics.SuccessCount)
			rateLimited := atomic.LoadInt64(&metrics.RateLimited)
			errors := atomic.LoadInt64(&metrics.ErrorCount)

			elapsed := time.Since(start).Seconds()
			rps := float64(total-lastTotal) / interval.Seconds()
			_ = elapsed // used for potential future reporting
			lastTotal = total

			avgLatency := int64(0)
			if total > 0 {
				avgLatency = atomic.LoadInt64(&metrics.TotalLatency) / total
			}

			fmt.Printf("[%s] Total: %d | Success: %d | Rate Limited: %d | Errors: %d | RPS: %.1f | Avg Latency: %dμs\n",
				time.Since(start).Round(time.Second),
				total, success, rateLimited, errors, rps, avgLatency)
		}
	}
}

// printFinalReport - Print final test report
func printFinalReport(metrics *Metrics, duration time.Duration) {
	fmt.Println("\n=== Final Report ===")
	fmt.Printf("Duration: %v\n", duration)
	fmt.Printf("Total Requests: %d\n", metrics.TotalRequests)
	if metrics.TotalRequests > 0 {
		fmt.Printf("Successful: %d (%.1f%%)\n", metrics.SuccessCount, float64(metrics.SuccessCount)/float64(metrics.TotalRequests)*100)
		fmt.Printf("Rate Limited: %d (%.1f%%)\n", metrics.RateLimited, float64(metrics.RateLimited)/float64(metrics.TotalRequests)*100)
		fmt.Printf("Errors: %d (%.1f%%)\n", metrics.ErrorCount, float64(metrics.ErrorCount)/float64(metrics.TotalRequests)*100)
	}
	fmt.Printf("Requests/sec: %.2f\n", float64(metrics.TotalRequests)/duration.Seconds())

	if metrics.TotalRequests > 0 {
		avgLatency := time.Duration(metrics.TotalLatency / metrics.TotalRequests * 1000)
		minLatency := time.Duration(metrics.MinLatency * 1000)
		maxLatency := time.Duration(metrics.MaxLatency * 1000)
		fmt.Printf("Avg Latency: %v\n", avgLatency)
		fmt.Printf("Min Latency: %v\n", minLatency)
		fmt.Printf("Max Latency: %v\n", maxLatency)
	}

	// Rate limiter effectiveness
	if metrics.RateLimited > 0 {
		fmt.Printf("\nRate Limiter Effectiveness:\n")
		fmt.Printf("- Blocked %d requests (%.1f%%)\n", metrics.RateLimited, float64(metrics.RateLimited)/float64(metrics.TotalRequests)*100)
		fmt.Printf("- Allowed %d requests (%.1f%%)\n", metrics.SuccessCount, float64(metrics.SuccessCount)/float64(metrics.TotalRequests)*100)
	}
}

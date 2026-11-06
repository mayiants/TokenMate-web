// Package main provides an E2E load testing tool for the AI Proxy platform.
//
// It sends real chat completion requests through the proxy to validate:
// 1. Burst scenarios (N users connect simultaneously)
// 2. Sustained load (N concurrent requests for a specified duration)
// 3. Latency distribution (P50/P90/P95/P99)
// 4. Error rates and categorization
//
// Usage:
//
//	go run tests/loadtest/main.go -mode=burst -users=500 -key=sk-wgit-xxx
//	go run tests/loadtest/main.go -mode=sustained -concurrency=500 -duration=30s -key=sk-wgit-xxx
//	go run tests/loadtest/main.go -mode=rampup -max-concurrency=500 -duration=60s -key=sk-wgit-xxx
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// --- Configuration ---

type Config struct {
	Mode           string        // burst, sustained, rampup
	ProxyURL       string        // Proxy base URL
	APIKey         string        // API key for authentication
	Model          string        // Model to test
	Users          int           // Number of concurrent users (burst mode)
	Concurrency    int           // Concurrency level (sustained mode)
	Duration       time.Duration // Test duration (sustained/rampup mode)
	MaxConcurrency int           // Max concurrency (rampup mode)
	Prompt         string        // Custom prompt
	MaxTokens      int           // Max tokens per response
	Stream         bool          // Use streaming
	Timeout        time.Duration // Per-request timeout
	MultiKey       bool          // Use multiple keys (from comma-separated -key)
	Keys           []string      // Parsed list of keys
}

// --- Result types ---

type RequestResult struct {
	StatusCode   int
	Latency      time.Duration
	TTFB         time.Duration // Time to first byte (streaming)
	Error        string
	Model        string
	InputTokens  int
	OutputTokens int
}

type Summary struct {
	TotalRequests  int64
	SuccessCount   int64
	FailCount      int64
	TimeoutCount   int64
	RateLimitCount int64
	ServerErrCount int64
	OtherErrCount  int64
	TotalDuration  time.Duration
	Latencies      []time.Duration
	ErrorSummary   map[string]int
	RPS            float64
	SuccessRate    float64
	P50            time.Duration
	P90            time.Duration
	P95            time.Duration
	P99            time.Duration
	Min            time.Duration
	Max            time.Duration
	Avg            time.Duration
}

// --- Main ---

func main() {
	cfg := parseFlags()

	fmt.Println("========================================")
	fmt.Println("  AI Proxy Go — E2E Load Test")
	fmt.Println("========================================")
	fmt.Printf("Mode:        %s\n", cfg.Mode)
	fmt.Printf("Proxy:       %s\n", cfg.ProxyURL)
	fmt.Printf("Model:       %s\n", cfg.Model)
	fmt.Printf("Stream:      %v\n", cfg.Stream)
	fmt.Printf("Timeout:     %v\n", cfg.Timeout)
	fmt.Printf("Keys:        %d\n", len(cfg.Keys))

	switch cfg.Mode {
	case "burst":
		fmt.Printf("Users:       %d\n", cfg.Users)
		runBurst(cfg)
	case "sustained":
		fmt.Printf("Concurrency: %d\n", cfg.Concurrency)
		fmt.Printf("Duration:    %v\n", cfg.Duration)
		runSustained(cfg)
	case "rampup":
		fmt.Printf("Max Conc:    %d\n", cfg.MaxConcurrency)
		fmt.Printf("Duration:    %v\n", cfg.Duration)
		runRampUp(cfg)
	default:
		fmt.Fprintf(os.Stderr, "Unknown mode: %s\n", cfg.Mode)
		os.Exit(1)
	}
}

func parseFlags() *Config {
	cfg := &Config{}
	flag.StringVar(&cfg.Mode, "mode", "burst", "Test mode: burst, sustained, rampup")
	flag.StringVar(&cfg.ProxyURL, "proxy", "http://127.0.0.1:8080", "Proxy base URL")
	flag.StringVar(&cfg.APIKey, "key", "", "API key(s), comma-separated for multi-key")
	flag.StringVar(&cfg.Model, "model", "gpt-5.5", "Model to test")
	flag.IntVar(&cfg.Users, "users", 100, "Number of concurrent users (burst mode)")
	flag.IntVar(&cfg.Concurrency, "concurrency", 50, "Concurrency level (sustained mode)")
	flag.DurationVar(&cfg.Duration, "duration", 30*time.Second, "Test duration")
	flag.IntVar(&cfg.MaxConcurrency, "max-concurrency", 200, "Max concurrency (rampup mode)")
	flag.StringVar(&cfg.Prompt, "prompt", "Say hello in one word.", "User prompt")
	flag.IntVar(&cfg.MaxTokens, "max-tokens", 10, "Max tokens per response")
	flag.BoolVar(&cfg.Stream, "stream", false, "Use streaming responses")
	flag.DurationVar(&cfg.Timeout, "timeout", 60*time.Second, "Per-request timeout")
	flag.Parse()

	if cfg.APIKey == "" {
		cfg.APIKey = os.Getenv("API_KEY")
	}
	if cfg.APIKey == "" {
		fmt.Fprintln(os.Stderr, "Error: -key or API_KEY env required")
		os.Exit(1)
	}

	cfg.Keys = strings.Split(cfg.APIKey, ",")
	for i := range cfg.Keys {
		cfg.Keys[i] = strings.TrimSpace(cfg.Keys[i])
	}

	return cfg
}

// --- HTTP Client ---

var httpClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConnsPerHost: 1000,
		MaxConnsPerHost:     1000,
		IdleConnTimeout:     90 * time.Second,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: false},
		DisableKeepAlives:   false,
	},
}

// --- Burst Mode ---
// Simulates N users connecting at the exact same time.

func runBurst(cfg *Config) {
	fmt.Printf("\n--- Burst: %d simultaneous requests ---\n", cfg.Users)

	results := make([]RequestResult, cfg.Users)
	var wg sync.WaitGroup
	barrier := make(chan struct{})

	for i := 0; i < cfg.Users; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-barrier // Wait until all goroutines are ready
			results[idx] = doRequest(cfg, idx)
		}(i)
	}

	// Release all goroutines at once
	time.Sleep(100 * time.Millisecond) // Let goroutines park
	fmt.Printf("Firing %d requests simultaneously...\n", cfg.Users)
	start := time.Now()
	close(barrier)
	wg.Wait()
	elapsed := time.Since(start)

	summary := computeSummary(results, elapsed)
	printSummary(summary, "Burst")
}

// --- Sustained Mode ---
// Maintains N concurrent requests for a specified duration.

func runSustained(cfg *Config) {
	fmt.Printf("\n--- Sustained: %d concurrent for %v ---\n", cfg.Concurrency, cfg.Duration)

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Duration)
	defer cancel()

	var (
		mu      sync.Mutex
		results []RequestResult
		total   atomic.Int64
	)

	start := time.Now()
	var wg sync.WaitGroup

	for i := 0; i < cfg.Concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				idx := int(total.Add(1)) - 1
				r := doRequest(cfg, idx)
				mu.Lock()
				results = append(results, r)
				mu.Unlock()

				// Print progress every 100 requests
				if n := total.Load(); n%100 == 0 {
					elapsed := time.Since(start)
					fmt.Printf("  [%v] %d requests completed...\n", elapsed.Round(time.Second), n)
				}
			}
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(start)

	summary := computeSummary(results, elapsed)
	printSummary(summary, "Sustained")
}

// --- Ramp Up Mode ---
// Gradually increases concurrency from 1 to max over the duration.

func runRampUp(cfg *Config) {
	fmt.Printf("\n--- Ramp Up: 1 → %d over %v ---\n", cfg.MaxConcurrency, cfg.Duration)

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Duration)
	defer cancel()

	var (
		mu      sync.Mutex
		results []RequestResult
		total   atomic.Int64
		active  atomic.Int64
	)

	start := time.Now()
	var wg sync.WaitGroup

	// Spawn workers gradually
	go func() {
		step := cfg.Duration / time.Duration(cfg.MaxConcurrency)
		if step < 10*time.Millisecond {
			step = 10 * time.Millisecond
		}

		for i := 0; i < cfg.MaxConcurrency; i++ {
			select {
			case <-ctx.Done():
				return
			case <-time.After(step):
			}

			wg.Add(1)
			active.Add(1)
			go func(workerID int) {
				defer wg.Done()
				defer active.Add(-1)
				for {
					select {
					case <-ctx.Done():
						return
					default:
					}
					idx := int(total.Add(1)) - 1
					r := doRequest(cfg, idx)
					mu.Lock()
					results = append(results, r)
					mu.Unlock()
				}
			}(i)

			if (i+1)%50 == 0 {
				elapsed := time.Since(start)
				fmt.Printf("  [%v] Ramped to %d workers, %d requests done\n",
					elapsed.Round(time.Second), active.Load(), total.Load())
			}
		}
	}()

	<-ctx.Done()
	wg.Wait()
	elapsed := time.Since(start)

	summary := computeSummary(results, elapsed)
	printSummary(summary, "Ramp Up")
}

// --- Request execution ---

func doRequest(cfg *Config, idx int) RequestResult {
	key := cfg.Keys[idx%len(cfg.Keys)]

	body := map[string]interface{}{
		"model": cfg.Model,
		"messages": []map[string]string{
			{"role": "user", "content": cfg.Prompt},
		},
		"max_tokens": cfg.MaxTokens,
		"stream":     cfg.Stream,
	}
	bodyBytes, _ := json.Marshal(body)

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST",
		cfg.ProxyURL+"/v1/chat/completions",
		bytes.NewReader(bodyBytes))
	if err != nil {
		return RequestResult{Error: "req_create: " + err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	start := time.Now()
	resp, err := httpClient.Do(req)
	ttfb := time.Since(start)

	if err != nil {
		if ctx.Err() != nil {
			return RequestResult{Error: "timeout", Latency: ttfb, StatusCode: 0}
		}
		return RequestResult{Error: "transport: " + err.Error(), Latency: ttfb}
	}

	respBody, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	latency := time.Since(start)

	result := RequestResult{
		StatusCode: resp.StatusCode,
		Latency:    latency,
		TTFB:       ttfb,
		Model:      cfg.Model,
	}

	if resp.StatusCode != 200 {
		var errResp map[string]interface{}
		if json.Unmarshal(respBody, &errResp) == nil {
			if errObj, ok := errResp["error"].(map[string]interface{}); ok {
				result.Error = fmt.Sprintf("%d: %v", resp.StatusCode, errObj["message"])
			} else {
				result.Error = fmt.Sprintf("%d: %s", resp.StatusCode, string(respBody[:min(200, len(respBody))]))
			}
		} else {
			result.Error = fmt.Sprintf("%d: %s", resp.StatusCode, string(respBody[:min(200, len(respBody))]))
		}
	} else {
		// Parse token usage from response
		var chatResp struct {
			Usage struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(respBody, &chatResp) == nil {
			result.InputTokens = chatResp.Usage.PromptTokens
			result.OutputTokens = chatResp.Usage.CompletionTokens
		}
	}

	return result
}

// --- Summary computation ---

func computeSummary(results []RequestResult, elapsed time.Duration) *Summary {
	s := &Summary{
		TotalRequests: int64(len(results)),
		TotalDuration: elapsed,
		ErrorSummary:  make(map[string]int),
	}

	for _, r := range results {
		if r.Error == "" && r.StatusCode == 200 {
			s.SuccessCount++
			s.Latencies = append(s.Latencies, r.Latency)
		} else {
			s.FailCount++
			errKey := r.Error
			if r.StatusCode == 429 {
				s.RateLimitCount++
				errKey = "429 rate_limited"
			} else if r.StatusCode == 503 {
				s.ServerErrCount++
				errKey = "503 key_cooldown"
			} else if r.StatusCode >= 500 {
				s.ServerErrCount++
			} else if r.Error == "timeout" {
				s.TimeoutCount++
				errKey = "timeout"
			} else {
				s.OtherErrCount++
			}
			s.ErrorSummary[errKey]++
		}
	}

	if elapsed.Seconds() > 0 {
		s.RPS = float64(s.TotalRequests) / elapsed.Seconds()
	}
	if s.TotalRequests > 0 {
		s.SuccessRate = float64(s.SuccessCount) / float64(s.TotalRequests) * 100
	}

	if len(s.Latencies) > 0 {
		sort.Slice(s.Latencies, func(i, j int) bool { return s.Latencies[i] < s.Latencies[j] })
		s.Min = s.Latencies[0]
		s.Max = s.Latencies[len(s.Latencies)-1]
		s.P50 = percentile(s.Latencies, 50)
		s.P90 = percentile(s.Latencies, 90)
		s.P95 = percentile(s.Latencies, 95)
		s.P99 = percentile(s.Latencies, 99)

		var sum time.Duration
		for _, l := range s.Latencies {
			sum += l
		}
		s.Avg = sum / time.Duration(len(s.Latencies))
	}

	return s
}

func percentile(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(float64(p)/100*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// --- Output ---

func printSummary(s *Summary, mode string) {
	fmt.Println()
	fmt.Println("========================================")
	fmt.Printf("  %s Test Results\n", mode)
	fmt.Println("========================================")
	fmt.Printf("  Duration:     %v\n", s.TotalDuration.Round(time.Millisecond))
	fmt.Printf("  Total Reqs:   %d\n", s.TotalRequests)
	fmt.Printf("  Success:      %d (%.1f%%)\n", s.SuccessCount, s.SuccessRate)
	fmt.Printf("  Failed:       %d\n", s.FailCount)
	fmt.Printf("  RPS:          %.1f\n", s.RPS)
	fmt.Println()

	if len(s.Latencies) > 0 {
		fmt.Println("  Latency (successful requests):")
		fmt.Printf("    Min:  %v\n", s.Min.Round(time.Millisecond))
		fmt.Printf("    Avg:  %v\n", s.Avg.Round(time.Millisecond))
		fmt.Printf("    P50:  %v\n", s.P50.Round(time.Millisecond))
		fmt.Printf("    P90:  %v\n", s.P90.Round(time.Millisecond))
		fmt.Printf("    P95:  %v\n", s.P95.Round(time.Millisecond))
		fmt.Printf("    P99:  %v\n", s.P99.Round(time.Millisecond))
		fmt.Printf("    Max:  %v\n", s.Max.Round(time.Millisecond))
		fmt.Println()
	}

	if s.FailCount > 0 {
		fmt.Println("  Error Breakdown:")
		fmt.Printf("    Rate Limited (429): %d\n", s.RateLimitCount)
		fmt.Printf("    Server Error (5xx): %d\n", s.ServerErrCount)
		fmt.Printf("    Timeout:            %d\n", s.TimeoutCount)
		fmt.Printf("    Other:              %d\n", s.OtherErrCount)
		fmt.Println()

		// Top errors
		type errEntry struct {
			msg   string
			count int
		}
		var entries []errEntry
		for msg, count := range s.ErrorSummary {
			entries = append(entries, errEntry{msg, count})
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].count > entries[j].count })
		fmt.Println("  Top Errors:")
		for i, e := range entries {
			if i >= 10 {
				break
			}
			fmt.Printf("    [%d] %s\n", e.count, e.msg)
		}
	}

	fmt.Println("========================================")

	// Exit code based on success rate
	if s.SuccessRate < 50 {
		fmt.Println("  ⚠️  WARN: Success rate below 50%")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

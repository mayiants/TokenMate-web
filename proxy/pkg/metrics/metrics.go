package metrics

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Metrics holds all Prometheus-style metrics for the proxy
type Metrics struct {
	// Request counters
	TotalRequests        atomic.Int64
	SuccessfulRequests   atomic.Int64
	FailedRequests       atomic.Int64
	StreamingRequests    atomic.Int64
	NonStreamingRequests atomic.Int64

	// Provider-specific counters
	ProviderRequests sync.Map // map[string]*atomic.Int64

	// Model-specific counters
	ModelRequests sync.Map // map[string]*atomic.Int64

	// Token counters
	TotalInputTokens  atomic.Int64
	TotalOutputTokens atomic.Int64
	TotalCacheTokens  atomic.Int64

	// Cost tracking
	TotalCostCents atomic.Int64 // Cost in cents to avoid floating point

	// Latency tracking (milliseconds)
	TotalLatencyMs atomic.Int64
	RequestCount   atomic.Int64

	// Key pool stats
	ActiveKeys   atomic.Int32
	CooldownKeys atomic.Int32
	InactiveKeys atomic.Int32

	// Cache stats
	CacheHits   atomic.Int64
	CacheMisses atomic.Int64

	// Rate limit stats
	RateLimitHits atomic.Int64

	// Start time
	StartTime time.Time
}

// Global metrics instance
var globalMetrics = &Metrics{
	StartTime: time.Now(),
}

// GetMetrics returns the global metrics instance
func GetMetrics() *Metrics {
	return globalMetrics
}

// IncrementRequests increments request counters
func (m *Metrics) IncrementRequests(success bool, streaming bool) {
	m.TotalRequests.Add(1)
	if success {
		m.SuccessfulRequests.Add(1)
	} else {
		m.FailedRequests.Add(1)
	}
	if streaming {
		m.StreamingRequests.Add(1)
	} else {
		m.NonStreamingRequests.Add(1)
	}
}

// IncrementProviderRequests increments provider-specific counter
func (m *Metrics) IncrementProviderRequests(provider string) {
	if v, ok := m.ProviderRequests.Load(provider); ok {
		v.(*atomic.Int64).Add(1)
	} else {
		counter := &atomic.Int64{}
		actual, loaded := m.ProviderRequests.LoadOrStore(provider, counter)
		actual.(*atomic.Int64).Add(1)
		_ = loaded
	}
}

// IncrementModelRequests increments model-specific counter
func (m *Metrics) IncrementModelRequests(model string) {
	if v, ok := m.ModelRequests.Load(model); ok {
		v.(*atomic.Int64).Add(1)
	} else {
		counter := &atomic.Int64{}
		actual, loaded := m.ModelRequests.LoadOrStore(model, counter)
		actual.(*atomic.Int64).Add(1)
		_ = loaded
	}
}

// AddTokens adds token counts
func (m *Metrics) AddTokens(input, output, cache int64) {
	m.TotalInputTokens.Add(input)
	m.TotalOutputTokens.Add(output)
	m.TotalCacheTokens.Add(cache)
}

// AddCost adds cost in cents
func (m *Metrics) AddCost(costDollars float64) {
	costCents := int64(costDollars * 100)
	m.TotalCostCents.Add(costCents)
}

// AddLatency adds latency for averaging
func (m *Metrics) AddLatency(latencyMs int64) {
	m.TotalLatencyMs.Add(latencyMs)
	m.RequestCount.Add(1)
}

// UpdateKeyStats updates key pool statistics
func (m *Metrics) UpdateKeyStats(active, cooldown, inactive int32) {
	m.ActiveKeys.Store(active)
	m.CooldownKeys.Store(cooldown)
	m.InactiveKeys.Store(inactive)
}

// IncrementRateLimitHits increments rate limit hit counter
func (m *Metrics) IncrementRateLimitHits() {
	m.RateLimitHits.Add(1)
}

// GetAverageLatency returns average latency in milliseconds
func (m *Metrics) GetAverageLatency() float64 {
	count := m.RequestCount.Load()
	if count == 0 {
		return 0
	}
	return float64(m.TotalLatencyMs.Load()) / float64(count)
}

// GetTotalCost returns total cost in dollars
func (m *Metrics) GetTotalCost() float64 {
	return float64(m.TotalCostCents.Load()) / 100.0
}

// GetUptime returns uptime duration
func (m *Metrics) GetUptime() time.Duration {
	return time.Since(m.StartTime)
}

// ToPrometheusFormat returns metrics in Prometheus text format
func (m *Metrics) ToPrometheusFormat() string {
	var result string

	// Request metrics
	result += "# HELP proxy_requests_total Total number of requests\n"
	result += "# TYPE proxy_requests_total counter\n"
	result += formatMetric("proxy_requests_total", m.TotalRequests.Load())
	result += formatMetric("proxy_requests_successful", m.SuccessfulRequests.Load())
	result += formatMetric("proxy_requests_failed", m.FailedRequests.Load())
	result += formatMetric("proxy_requests_streaming", m.StreamingRequests.Load())
	result += formatMetric("proxy_requests_non_streaming", m.NonStreamingRequests.Load())

	// Token metrics
	result += "\n# HELP proxy_tokens_total Total tokens processed\n"
	result += "# TYPE proxy_tokens_total counter\n"
	result += formatMetric("proxy_tokens_input", m.TotalInputTokens.Load())
	result += formatMetric("proxy_tokens_output", m.TotalOutputTokens.Load())
	result += formatMetric("proxy_tokens_cache", m.TotalCacheTokens.Load())

	// Cost metric
	result += "\n# HELP proxy_cost_total Total cost in dollars\n"
	result += "# TYPE proxy_cost_total counter\n"
	result += formatMetricFloat("proxy_cost_total", m.GetTotalCost())

	// Latency metric
	result += "\n# HELP proxy_latency_avg_ms Average latency in milliseconds\n"
	result += "# TYPE proxy_latency_avg_ms gauge\n"
	result += formatMetricFloat("proxy_latency_avg_ms", m.GetAverageLatency())

	// Key pool metrics
	result += "\n# HELP proxy_keys Number of keys by status\n"
	result += "# TYPE proxy_keys gauge\n"
	result += formatMetric("proxy_keys_active", int64(m.ActiveKeys.Load()))
	result += formatMetric("proxy_keys_cooldown", int64(m.CooldownKeys.Load()))
	result += formatMetric("proxy_keys_inactive", int64(m.InactiveKeys.Load()))

	// Cache metrics
	result += "\n# HELP proxy_cache Cache hits and misses\n"
	result += "# TYPE proxy_cache counter\n"
	result += formatMetric("proxy_cache_hits", m.CacheHits.Load())
	result += formatMetric("proxy_cache_misses", m.CacheMisses.Load())

	// Rate limit metric
	result += "\n# HELP proxy_rate_limit_hits_total Rate limit hits\n"
	result += "# TYPE proxy_rate_limit_hits_total counter\n"
	result += formatMetric("proxy_rate_limit_hits_total", m.RateLimitHits.Load())

	// Uptime metric
	result += "\n# HELP proxy_uptime_seconds Uptime in seconds\n"
	result += "# TYPE proxy_uptime_seconds gauge\n"
	result += formatMetricFloat("proxy_uptime_seconds", m.GetUptime().Seconds())

	// Provider metrics
	result += "\n# HELP proxy_provider_requests_total Requests per provider\n"
	result += "# TYPE proxy_provider_requests_total counter\n"
	m.ProviderRequests.Range(func(key, value interface{}) bool {
		result += formatMetricWithLabel("proxy_provider_requests_total", "provider", key.(string), value.(*atomic.Int64).Load())
		return true
	})

	// Model metrics
	result += "\n# HELP proxy_model_requests_total Requests per model\n"
	result += "# TYPE proxy_model_requests_total counter\n"
	m.ModelRequests.Range(func(key, value interface{}) bool {
		result += formatMetricWithLabel("proxy_model_requests_total", "model", key.(string), value.(*atomic.Int64).Load())
		return true
	})

	return result
}

func formatMetric(name string, value int64) string {
	return name + " " + formatInt(value) + "\n"
}

func formatMetricFloat(name string, value float64) string {
	return name + " " + formatFloat(value) + "\n"
}

func formatMetricWithLabel(name, labelKey, labelValue string, value int64) string {
	return name + "{" + labelKey + "=\"" + labelValue + "\"} " + formatInt(value) + "\n"
}

func formatInt(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [32]byte
	i := len(buf)
	negative := v < 0
	if negative {
		v = -v
	}
	for v > 0 {
		i--
		buf[i] = byte(v%10 + '0')
		v /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func formatFloat(v float64) string {
	return fmt.Sprintf("%.6f", v)
}

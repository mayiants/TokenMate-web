package metrics

import (
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

func TestFormatInt(t *testing.T) {
	assert.Equal(t, "0", formatInt(0))
	assert.Equal(t, "1", formatInt(1))
	assert.Equal(t, "123", formatInt(123))
	assert.Equal(t, "1234567890", formatInt(1234567890))
	assert.Equal(t, "-42", formatInt(-42))
}

func TestFormatFloat(t *testing.T) {
	assert.Contains(t, formatFloat(0.0), "0.")
	assert.Contains(t, formatFloat(1.5), "1.5")
	assert.Contains(t, formatFloat(123.456), "123.456")
}

func TestMetricsIncrementRequests(t *testing.T) {
	m := GetMetrics()
	m.IncrementRequests(true, false)
	assert.Equal(t, int64(1), m.TotalRequests.Load())
	assert.Equal(t, int64(1), m.SuccessfulRequests.Load())
	assert.Equal(t, int64(0), m.FailedRequests.Load())
	assert.Equal(t, int64(0), m.StreamingRequests.Load())
	assert.Equal(t, int64(1), m.NonStreamingRequests.Load())
}

func TestMetricsProviderRequests(t *testing.T) {
	m := GetMetrics()
	m.IncrementProviderRequests("openai")
	m.IncrementProviderRequests("openai")
	m.IncrementProviderRequests("anthropic")

	v, ok := m.ProviderRequests.Load("openai")
	assert.True(t, ok)
	assert.Equal(t, int64(2), v.(*atomic.Int64).Load())
}

func TestMetricsToPrometheusFormat(t *testing.T) {
	m := GetMetrics()
	m.IncrementRequests(true, false)
	m.AddTokens(100, 50, 0)
	m.AddLatency(150)
	m.IncrementProviderRequests("openai")

	output := m.ToPrometheusFormat()
	assert.Contains(t, output, "proxy_requests_total")
	assert.Contains(t, output, "proxy_tokens_input")
	assert.Contains(t, output, "proxy_latency_avg_ms")
	assert.Contains(t, output, "proxy_provider_requests_total")
	assert.Contains(t, output, "proxy_uptime_seconds")
}

func TestSetDefaultRegistry_Isolation(t *testing.T) {
	orig := GetPrometheusRegistry()
	defer SetDefaultRegistry(orig)

	counterValue := func(reg *prometheus.Registry, name string) float64 {
		families, err := reg.Gather()
		if err != nil {
			return 0
		}
		for _, mf := range families {
			if mf.GetName() == name && len(mf.GetMetric()) > 0 {
				return mf.GetMetric()[0].GetCounter().GetValue()
			}
		}
		return 0
	}

	reg1 := NewPrometheusRegistry()
	SetDefaultRegistry(reg1)
	GetMetrics().IncrementCacheHits(3)
	GetMetrics().IncrementCacheMisses(5)

	reg2 := NewPrometheusRegistry()
	SetDefaultRegistry(reg2)
	GetMetrics().IncrementCacheMisses(7)

	assert.Equal(t, 3.0, counterValue(reg1.Registry, "proxy_cache_hits_total"), "reg1 should keep its hits")
	assert.Equal(t, 5.0, counterValue(reg1.Registry, "proxy_cache_misses_total"), "reg1 should keep its misses")
	assert.Equal(t, 0.0, counterValue(reg2.Registry, "proxy_cache_hits_total"), "reg2 should not see reg1 hits")
	assert.Equal(t, 7.0, counterValue(reg2.Registry, "proxy_cache_misses_total"), "reg2 should only see its own misses")
}

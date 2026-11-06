package metrics

import (
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// PrometheusRegistry holds the prometheus registry and metrics.
type PrometheusRegistry struct {
	Registry *prometheus.Registry

	RequestDuration         *prometheus.HistogramVec
	FallbackTotal           *prometheus.CounterVec
	KeyCooldownTotal        *prometheus.CounterVec
	CacheHitsTotal          prometheus.Counter
	CacheMissesTotal        prometheus.Counter
	PreconsumeRejectedTotal prometheus.Counter
	SettleChargedTotal      *prometheus.CounterVec

	// AuditAnonDroppedTotal counts failure audits dropped by the per-IP flood limiter.
	AuditAnonDroppedTotal prometheus.Counter
	// RetentionDiskSkippedTotal counts retention writes skipped by the disk-usage guard.
	RetentionDiskSkippedTotal prometheus.Counter
}

var defaultRegistry atomic.Pointer[PrometheusRegistry]

func init() {
	defaultRegistry.Store(newPrometheusRegistry())
}

// NewPrometheusRegistry creates a fresh Prometheus registry with all proxy
// metrics registered. It is useful in tests to avoid global-state pollution.
func NewPrometheusRegistry() *PrometheusRegistry {
	return newPrometheusRegistry()
}

// SetDefaultRegistry swaps the global registry used by the metric helpers.
// Callers are responsible for ensuring the previous registry is shut down if
// needed.
func SetDefaultRegistry(r *PrometheusRegistry) {
	if r == nil {
		r = newPrometheusRegistry()
	}
	defaultRegistry.Store(r)
}

// GetPrometheusRegistry returns the current global Prometheus registry wrapper.
func GetPrometheusRegistry() *PrometheusRegistry {
	if r := defaultRegistry.Load(); r != nil {
		return r
	}
	return newPrometheusRegistry()
}

func newPrometheusRegistry() *PrometheusRegistry {
	reg := prometheus.NewRegistry()

	requestDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "proxy_request_duration_seconds",
		Help:    "Request duration in seconds",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
	}, []string{"provider", "model", "status_code", "is_stream", "is_fallback"})

	fallbackTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "proxy_fallback_total",
		Help: "Total number of fallback requests",
	}, []string{"provider", "model"})

	keyCooldownTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "proxy_key_cooldown_total",
		Help: "Total number of times keys entered cooldown",
	}, []string{"provider"})

	cacheHitsTotal := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "proxy_cache_hits_total",
		Help: "Total number of cache hits",
	})

	cacheMissesTotal := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "proxy_cache_misses_total",
		Help: "Total number of cache misses",
	})

	preconsumeRejectedTotal := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "proxy_preconsume_rejected_total",
		Help: "Total number of pre-consume rejected requests",
	})

	settleChargedTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "proxy_settle_charged_total",
		Help: "Total number of settle charges",
	}, []string{"provider", "model", "success"})

	auditAnonDroppedTotal := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "proxy_audit_anon_failures_dropped_total",
		Help: "Failure audits without a resolved key dropped by the per-IP limiter",
	})

	retentionDiskSkippedTotal := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "proxy_retention_disk_skipped_total",
		Help: "Retention writes skipped because the disk usage guard tripped",
	})

	reg.MustRegister(
		auditAnonDroppedTotal,
		retentionDiskSkippedTotal,
		requestDuration,
		fallbackTotal,
		keyCooldownTotal,
		cacheHitsTotal,
		cacheMissesTotal,
		preconsumeRejectedTotal,
		settleChargedTotal,
	)

	return &PrometheusRegistry{
		Registry:                reg,
		RequestDuration:         requestDuration,
		FallbackTotal:           fallbackTotal,
		KeyCooldownTotal:        keyCooldownTotal,
		CacheHitsTotal:          cacheHitsTotal,
		CacheMissesTotal:        cacheMissesTotal,
		PreconsumeRejectedTotal: preconsumeRejectedTotal,
		SettleChargedTotal:      settleChargedTotal,

		AuditAnonDroppedTotal:     auditAnonDroppedTotal,
		RetentionDiskSkippedTotal: retentionDiskSkippedTotal,
	}
}

// ObserveRequestDuration records a request duration observation.
func (m *Metrics) ObserveRequestDuration(provider, model string, statusCode int, isStream, isFallback bool, duration time.Duration) {
	GetPrometheusRegistry().RequestDuration.WithLabelValues(
		provider,
		model,
		strconv.Itoa(statusCode),
		fmt.Sprintf("%t", isStream),
		fmt.Sprintf("%t", isFallback),
	).Observe(duration.Seconds())
}

// IncrementFallback increments the fallback counter.
func (m *Metrics) IncrementFallback(provider, model string) {
	GetPrometheusRegistry().FallbackTotal.WithLabelValues(provider, model).Inc()
}

// IncrementKeyCooldown increments the key cooldown counter.
func (m *Metrics) IncrementKeyCooldown(provider string) {
	GetPrometheusRegistry().KeyCooldownTotal.WithLabelValues(provider).Inc()
}

// IncrementCacheHits increments the cache hits counter.
func (m *Metrics) IncrementCacheHits(hits int64) {
	m.CacheHits.Add(hits)
	GetPrometheusRegistry().CacheHitsTotal.Add(float64(hits))
}

// IncrementCacheMisses increments the cache misses counter.
func (m *Metrics) IncrementCacheMisses(misses int64) {
	m.CacheMisses.Add(misses)
	GetPrometheusRegistry().CacheMissesTotal.Add(float64(misses))
}

// IncrementPreconsumeRejected increments the pre-consume rejected counter.
func (m *Metrics) IncrementPreconsumeRejected() {
	GetPrometheusRegistry().PreconsumeRejectedTotal.Inc()
}

// IncrementSettleCharged increments the settle charged counter.
func (m *Metrics) IncrementSettleCharged(provider, model string, success bool) {
	GetPrometheusRegistry().SettleChargedTotal.WithLabelValues(provider, model, fmt.Sprintf("%t", success)).Inc()
}

package metrics

import (
	"fmt"
	"net/http"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds portal operational counters and gauges. The struct fields keep
// the existing atomic.Int64 API used throughout the codebase, while the
// embedded prometheus collectors expose the same values in standard Prometheus
// exposition format via the /metrics endpoint.
type Metrics struct {
	TotalRequests           atomic.Int64
	SuccessfulRequests      atomic.Int64
	FailedRequests          atomic.Int64
	ActiveUsers             atomic.Int64
	ActiveKeys              atomic.Int64
	DBQueryCount            atomic.Int64
	DBQueryLatencyMs        atomic.Int64
	RedisOpsCount           atomic.Int64
	AuthAttempts            atomic.Int64
	AuthFailures            atomic.Int64
	EmailSent               atomic.Int64
	EmailFailed             atomic.Int64
	WebhookReceived         atomic.Int64
	WebhookFailed           atomic.Int64
	WebhookRedisQueueFailed atomic.Int64
	SubscriptionCreated     atomic.Int64
	APIKeyValidated         atomic.Int64
	PricingFailures         atomic.Int64

	registry *prometheus.Registry
}

var global = newMetrics()

// newMetrics creates a Metrics instance backed by a private Prometheus registry
// with collectors that mirror the atomic counters and gauges.
func newMetrics() *Metrics {
	m := &Metrics{registry: prometheus.NewRegistry()}
	m.registerCollectors()
	return m
}

// GetMetrics returns the package-level metrics singleton.
func GetMetrics() *Metrics {
	return global
}

// ToPrometheusFormat returns the metrics in Prometheus text exposition format.
// It is kept for backward compatibility with callers that do not use HTTP
// handlers (e.g., test helpers). The /metrics endpoint should use Handler().
func (m *Metrics) ToPrometheusFormat() string {
	return fmt.Sprintf(`# HELP portal_requests_total Total HTTP requests processed
# TYPE portal_requests_total counter
portal_requests_total %d

# HELP portal_requests_success_total Successful HTTP requests
# TYPE portal_requests_success_total counter
portal_requests_success_total %d

# HELP portal_requests_failed_total Failed HTTP requests
# TYPE portal_requests_failed_total counter
portal_requests_failed_total %d

# HELP portal_active_users Current active users
# TYPE portal_active_users gauge
portal_active_users %d

# HELP portal_active_keys Current active API keys
# TYPE portal_active_keys gauge
portal_active_keys %d

# HELP portal_db_queries_total Total database queries
# TYPE portal_db_queries_total counter
portal_db_queries_total %d

# HELP portal_db_latency_ms_total Cumulative DB query latency in ms
# TYPE portal_db_latency_ms_total counter
portal_db_latency_ms_total %d

# HELP portal_redis_ops_total Total Redis operations
# TYPE portal_redis_ops_total counter
portal_redis_ops_total %d

# HELP portal_auth_attempts_total Total authentication attempts
# TYPE portal_auth_attempts_total counter
portal_auth_attempts_total %d

# HELP portal_auth_failures_total Total authentication failures
# TYPE portal_auth_failures_total counter
portal_auth_failures_total %d

# HELP portal_email_sent_total Emails sent successfully
# TYPE portal_email_sent_total counter
portal_email_sent_total %d

# HELP portal_email_failed_total Email send failures
# TYPE portal_email_failed_total counter
portal_email_failed_total %d

# HELP portal_webhook_received_total Webhooks received
# TYPE portal_webhook_received_total counter
portal_webhook_received_total %d

# HELP portal_webhook_failed_total Webhook processing failures
# TYPE portal_webhook_failed_total counter
portal_webhook_failed_total %d

# HELP portal_webhook_redis_queue_failed_total Webhook Redis queue push failures
# TYPE portal_webhook_redis_queue_failed_total counter
portal_webhook_redis_queue_failed_total %d

# HELP portal_subscriptions_created_total Subscriptions created
# TYPE portal_subscriptions_created_total counter
portal_subscriptions_created_total %d

# HELP portal_apikey_validated_total API key validations
# TYPE portal_apikey_validated_total counter
portal_apikey_validated_total %d

# HELP portal_pricing_failures_total Pricing calculation failures
# TYPE portal_pricing_failures_total counter
portal_pricing_failures_total %d
`,
		m.TotalRequests.Load(),
		m.SuccessfulRequests.Load(),
		m.FailedRequests.Load(),
		m.ActiveUsers.Load(),
		m.ActiveKeys.Load(),
		m.DBQueryCount.Load(),
		m.DBQueryLatencyMs.Load(),
		m.RedisOpsCount.Load(),
		m.AuthAttempts.Load(),
		m.AuthFailures.Load(),
		m.EmailSent.Load(),
		m.EmailFailed.Load(),
		m.WebhookReceived.Load(),
		m.WebhookFailed.Load(),
		m.WebhookRedisQueueFailed.Load(),
		m.SubscriptionCreated.Load(),
		m.APIKeyValidated.Load(),
		m.PricingFailures.Load(),
	)
}

// Handler returns an http.Handler that exposes metrics in Prometheus text
// exposition format using the prometheus/client_golang registry.
func (m *Metrics) Handler() http.Handler {
	if m.registry == nil {
		m.registry = prometheus.NewRegistry()
		m.registerCollectors()
	}
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{
		EnableOpenMetrics: false,
	})
}

// registerCollectors registers the prometheus collectors that read from the
// atomic metric fields. It must only be called once per Metrics instance.
func (m *Metrics) registerCollectors() {
	for _, c := range []struct {
		name  string
		help  string
		value func() float64
	}{
		{"portal_requests_total", "Total HTTP requests processed", func() float64 { return float64(m.TotalRequests.Load()) }},
		{"portal_requests_success_total", "Successful HTTP requests", func() float64 { return float64(m.SuccessfulRequests.Load()) }},
		{"portal_requests_failed_total", "Failed HTTP requests", func() float64 { return float64(m.FailedRequests.Load()) }},
		{"portal_db_queries_total", "Total database queries", func() float64 { return float64(m.DBQueryCount.Load()) }},
		{"portal_db_latency_ms_total", "Cumulative DB query latency in ms", func() float64 { return float64(m.DBQueryLatencyMs.Load()) }},
		{"portal_redis_ops_total", "Total Redis operations", func() float64 { return float64(m.RedisOpsCount.Load()) }},
		{"portal_auth_attempts_total", "Total authentication attempts", func() float64 { return float64(m.AuthAttempts.Load()) }},
		{"portal_auth_failures_total", "Total authentication failures", func() float64 { return float64(m.AuthFailures.Load()) }},
		{"portal_email_sent_total", "Emails sent successfully", func() float64 { return float64(m.EmailSent.Load()) }},
		{"portal_email_failed_total", "Email send failures", func() float64 { return float64(m.EmailFailed.Load()) }},
		{"portal_webhook_received_total", "Webhooks received", func() float64 { return float64(m.WebhookReceived.Load()) }},
		{"portal_webhook_failed_total", "Webhook processing failures", func() float64 { return float64(m.WebhookFailed.Load()) }},
		{"portal_webhook_redis_queue_failed_total", "Webhook Redis queue push failures", func() float64 { return float64(m.WebhookRedisQueueFailed.Load()) }},
		{"portal_subscriptions_created_total", "Subscriptions created", func() float64 { return float64(m.SubscriptionCreated.Load()) }},
		{"portal_apikey_validated_total", "API key validations", func() float64 { return float64(m.APIKeyValidated.Load()) }},
		{"portal_pricing_failures_total", "Pricing calculation failures", func() float64 { return float64(m.PricingFailures.Load()) }},
	} {
		_ = m.registry.Register(prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: c.name,
			Help: c.help,
		}, c.value))
	}

	for _, g := range []struct {
		name  string
		help  string
		value func() float64
	}{
		{"portal_active_users", "Current active users", func() float64 { return float64(m.ActiveUsers.Load()) }},
		{"portal_active_keys", "Current active API keys", func() float64 { return float64(m.ActiveKeys.Load()) }},
	} {
		_ = m.registry.Register(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: g.name,
			Help: g.help,
		}, g.value))
	}
}

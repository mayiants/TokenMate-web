package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestMetricsIncrement tests that atomic counters work.
func TestMetricsIncrement(t *testing.T) {
	m := &Metrics{}

	m.TotalRequests.Add(1)
	m.SuccessfulRequests.Add(1)
	m.FailedRequests.Add(3)

	if m.TotalRequests.Load() != 1 {
		t.Errorf("TotalRequests = %d, want 1", m.TotalRequests.Load())
	}
	if m.SuccessfulRequests.Load() != 1 {
		t.Errorf("SuccessfulRequests = %d, want 1", m.SuccessfulRequests.Load())
	}
	if m.FailedRequests.Load() != 3 {
		t.Errorf("FailedRequests = %d, want 3", m.FailedRequests.Load())
	}
}

// TestMetricsMultipleIncrements tests multiple increments.
func TestMetricsMultipleIncrements(t *testing.T) {
	m := &Metrics{}

	for i := 0; i < 10; i++ {
		m.TotalRequests.Add(1)
	}
	for i := 0; i < 7; i++ {
		m.SuccessfulRequests.Add(1)
	}
	for i := 0; i < 3; i++ {
		m.FailedRequests.Add(1)
	}

	if m.TotalRequests.Load() != 10 {
		t.Errorf("TotalRequests = %d, want 10", m.TotalRequests.Load())
	}
	if m.SuccessfulRequests.Load() != 7 {
		t.Errorf("SuccessfulRequests = %d, want 7", m.SuccessfulRequests.Load())
	}
	if m.FailedRequests.Load() != 3 {
		t.Errorf("FailedRequests = %d, want 3", m.FailedRequests.Load())
	}
}

// TestGetMetrics tests the global metrics instance.
func TestGetMetrics(t *testing.T) {
	m1 := GetMetrics()
	m2 := GetMetrics()

	if m1 != m2 {
		t.Error("GetMetrics() should return the same instance")
	}
}

// TestToPrometheusFormat tests Prometheus text format output.
func TestToPrometheusFormat(t *testing.T) {
	m := &Metrics{}

	m.TotalRequests.Add(42)
	m.SuccessfulRequests.Add(40)
	m.FailedRequests.Add(2)
	m.ActiveUsers.Add(5)
	m.ActiveKeys.Add(12)

	output := m.ToPrometheusFormat()

	// Check for Prometheus format markers
	if !strings.Contains(output, "# HELP portal_requests_total") {
		t.Error("Prometheus output missing HELP line for requests_total")
	}
	if !strings.Contains(output, "# TYPE portal_requests_total counter") {
		t.Error("Prometheus output missing TYPE line for requests_total")
	}
	if !strings.Contains(output, "portal_requests_total 42") {
		t.Error("Prometheus output missing or wrong requests_total value")
	}
	if !strings.Contains(output, "portal_requests_success_total 40") {
		t.Error("Prometheus output missing or wrong requests_success_total value")
	}
	if !strings.Contains(output, "portal_requests_failed_total 2") {
		t.Error("Prometheus output missing or wrong requests_failed_total value")
	}
	if !strings.Contains(output, "portal_active_users 5") {
		t.Error("Prometheus output missing or wrong active_users value")
	}
	if !strings.Contains(output, "portal_active_keys 12") {
		t.Error("Prometheus output missing or wrong active_keys value")
	}

	// Should contain all metric families
	expectedMetrics := []string{
		"portal_requests_total",
		"portal_requests_success_total",
		"portal_requests_failed_total",
		"portal_active_users",
		"portal_active_keys",
		"portal_db_queries_total",
		"portal_db_latency_ms_total",
		"portal_redis_ops_total",
		"portal_auth_attempts_total",
		"portal_auth_failures_total",
		"portal_email_sent_total",
		"portal_email_failed_total",
		"portal_webhook_received_total",
		"portal_webhook_failed_total",
		"portal_subscriptions_created_total",
		"portal_apikey_validated_total",
	}

	for _, metric := range expectedMetrics {
		if !strings.Contains(output, metric) {
			t.Errorf("Prometheus output missing metric: %s", metric)
		}
	}
}

// TestToPrometheusFormatEmpty tests Prometheus format with zero values.
func TestToPrometheusFormatEmpty(t *testing.T) {
	m := &Metrics{}
	output := m.ToPrometheusFormat()

	// All values should be 0
	if !strings.Contains(output, "portal_requests_total 0") {
		t.Error("All zero Prometheus output should show 0 for requests_total")
	}
}

// TestHandler exposes metrics via the prometheus/client_golang handler and
// verifies the output contains the expected metric families and values.
func TestHandler(t *testing.T) {
	m := &Metrics{}
	m.TotalRequests.Add(7)
	m.ActiveUsers.Store(3)

	req, _ := http.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Handler returned status %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
	if !strings.Contains(body, "portal_requests_total 7") {
		t.Errorf("Handler output missing expected requests_total value: %s", body)
	}
	if !strings.Contains(body, "portal_active_users 3") {
		t.Errorf("Handler output missing expected active_users value: %s", body)
	}
	if !strings.Contains(body, "# HELP portal_db_queries_total") {
		t.Error("Handler output missing HELP line")
	}
}

// TestHandlerSingleton verifies the global singleton handler exposes values
// incremented through GetMetrics().
func TestHandlerSingleton(t *testing.T) {
	m := GetMetrics()
	m.APIKeyValidated.Add(1)

	req, _ := http.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "portal_apikey_validated_total 1") {
		t.Errorf("Singleton handler output missing apikey_validated value: %s", body)
	}
}

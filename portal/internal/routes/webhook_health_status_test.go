package routes

import "testing"

// TestComputeStatus_ExcludesClientErrors verifies the bucket writer's health
// verdict ignores client/credential errors (auth_error, invalid_request,
// error_4xx) and applies the 10 effective-sample floor.
func TestComputeStatus_ExcludesClientErrors(t *testing.T) {
	cases := []struct {
		name string
		c    healthCounters
		want string
	}{
		{
			name: "below effective sample -> unknown",
			c:    healthCounters{requestCount: 9, successCount: 9, latencyMs: 9 * 500},
			want: "unknown",
		},
		{
			name: "2 reqs both invalid_request -> unknown (0 effective)",
			c:    healthCounters{requestCount: 2, failureCount: 2, invalidRequest: 2, latencyMs: 2 * 500},
			want: "unknown",
		},
		{
			name: "20 reqs, all 'errors' are client/cred -> healthy",
			c:    healthCounters{requestCount: 20, successCount: 14, failureCount: 6, invalidRequest: 3, err4xx: 2, authErr: 1, latencyMs: 20 * 500},
			want: "healthy",
		},
		{
			name: "20 reqs, 5 real 5xx failures -> unhealthy (25%)",
			c:    healthCounters{requestCount: 20, successCount: 15, failureCount: 5, err5xx: 5, latencyMs: 20 * 500},
			want: "unhealthy",
		},
		{
			name: "20 reqs, 2 real failures -> degraded (10%)",
			c:    healthCounters{requestCount: 20, successCount: 18, failureCount: 2, err5xx: 2, latencyMs: 20 * 500},
			want: "degraded",
		},
		{
			name: "healthy but slow text -> degraded",
			c:    healthCounters{requestCount: 20, successCount: 20, latencyMs: 20 * 20000},
			want: "degraded",
		},
		{
			name: "extreme latency 0 errors -> degraded, never unhealthy",
			c:    healthCounters{requestCount: 20, successCount: 20, latencyMs: 20 * 200000},
			want: "degraded",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := computeStatus(tc.c); got != tc.want {
				t.Errorf("computeStatus(%+v) = %q, want %q", tc.c, got, tc.want)
			}
		})
	}
}

// TestCountersFromRecord_ClientErrorsStillCounted verifies the raw category
// counts are still recorded (so they remain auditable/viewable) even though the
// verdict excludes them.
func TestCountersFromRecord_ClientErrorsStillCounted(t *testing.T) {
	c := countersFromRecord(&ProviderHealthRecord{ErrorType: "invalid_request", LatencyMs: 100})
	if c.failureCount != 1 || c.invalidRequest != 1 || c.requestCount != 1 {
		t.Errorf("invalid_request record: got failure=%d invalid=%d req=%d, want 1/1/1", c.failureCount, c.invalidRequest, c.requestCount)
	}
	c = countersFromRecord(&ProviderHealthRecord{ErrorType: "error_4xx", LatencyMs: 100})
	if c.err4xx != 1 || c.failureCount != 1 {
		t.Errorf("error_4xx record: got 4xx=%d failure=%d, want 1/1", c.err4xx, c.failureCount)
	}
	c = countersFromRecord(&ProviderHealthRecord{ErrorType: "", LatencyMs: 100})
	if c.successCount != 1 || c.failureCount != 0 {
		t.Errorf("success record: got success=%d failure=%d, want 1/0", c.successCount, c.failureCount)
	}
}

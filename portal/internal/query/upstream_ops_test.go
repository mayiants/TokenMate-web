package query

import "testing"

// TestUpstreamOpsQueryCompile ensures the upstream ops query package compiles.
func TestUpstreamOpsQueryCompile(t *testing.T) {
	t.Skip("requires DB - compile guard only")
}

// TestDeriveInternalStatus covers the health classifier semantics:
//   - minimum effective sample size of 10 (below it -> "unknown", never red);
//   - client/credential errors (auth_error + invalid_request + error_4xx,
//     passed combined as excludedCount) are removed from both numerator and
//     denominator — they are the caller's/credential's fault, not the model's;
//   - text vs media latency thresholds.
func TestDeriveInternalStatus(t *testing.T) {
	cases := []struct {
		name      string
		modelType string
		req, fail int
		excluded  int // auth_error + invalid_request + error_4xx
		latencyMs int
		want      string
	}{
		// --- minimum sample size (10 effective requests) ---
		{"1 req 1 real fail -> unknown (below sample)", "text", 1, 1, 0, 500, "unknown"},
		{"9 effective -> unknown (below sample)", "text", 9, 0, 0, 500, "unknown"},
		{"2 reqs both invalid_request -> unknown (0 effective)", "text", 2, 2, 2, 500, "unknown"},
		{"no requests -> unknown", "text", 0, 0, 0, 0, "unknown"},

		// --- client/credential errors excluded from the rate ---
		// 12 requests, all 3 "failures" are excluded (invalid/4xx/auth) ->
		// 0 real failures over 9 effective... 9<10 -> unknown. Bump to 20.
		{"20 reqs, 3 excluded errors only -> healthy", "text", 20, 3, 3, 500, "healthy"},
		{"all excluded -> unknown (0 effective)", "text", 15, 15, 15, 500, "unknown"},
		{"mixed: 1 real + 4 excluded of 30 -> healthy (1/26<5%)", "text", 30, 5, 4, 500, "healthy"},

		// --- genuine model failures still classify ---
		{"text 30% real errors -> unhealthy", "text", 100, 30, 0, 500, "unhealthy"},
		{"text 10% real errors -> degraded", "text", 100, 10, 0, 500, "degraded"},
		{"text 3% real errors -> healthy", "text", 100, 3, 0, 500, "healthy"},

		// --- latency: with a HEALTHY error rate, latency can only downgrade to
		// 'degraded', never 'unhealthy'. A slow-but-working model is not "down".
		{"text slow 20s, 0 err -> degraded", "text", 100, 0, 0, 20000, "degraded"},
		{"text very slow 60s, 0 err -> degraded (not unhealthy)", "text", 100, 0, 0, 60000, "degraded"},
		{"text extreme 95s, 0 err -> degraded (qwen3.8-max case)", "text", 55, 0, 0, 95000, "degraded"},
		// But a bad error rate IS unhealthy regardless of latency.
		{"text 30% err fast -> unhealthy", "text", 100, 30, 0, 500, "unhealthy"},
		{"text 30% err slow -> unhealthy", "text", 100, 30, 0, 60000, "unhealthy"},

		// --- media relaxed latency; same latency-cap rule ---
		{"media 20s -> healthy", "image", 100, 0, 0, 20000, "healthy"},
		{"media 80s -> healthy", "video", 100, 0, 0, 80000, "healthy"},
		{"media 100s, 0 err -> degraded", "image", 100, 0, 0, 100000, "degraded"},
		{"media 200s, 0 err -> degraded (not unhealthy)", "audio", 100, 0, 0, 200000, "degraded"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DeriveInternalStatus(tc.modelType, tc.req, tc.fail, tc.excluded, tc.latencyMs)
			if got != tc.want {
				t.Errorf("DeriveInternalStatus(%s, req=%d fail=%d excluded=%d lat=%d) = %q, want %q",
					tc.modelType, tc.req, tc.fail, tc.excluded, tc.latencyMs, got, tc.want)
			}
		})
	}
}

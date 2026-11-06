package server

import "testing"

func TestEmbeddedBaseRespStatus(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{"minimax error envelope", `{"base_resp":{"status_code":2013,"status_msg":"invalid params"},"choices":null}`, 2013},
		{"minimax success envelope", `{"base_resp":{"status_code":0},"choices":[{"index":0}]}`, 0},
		{"plain openai response", `{"id":"x","choices":[{"index":0}],"usage":{}}`, 0},
		{"non-json body", `not json at all`, 0},
		{"empty body", ``, 0},
		{"null body", `null`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := embeddedBaseRespStatus([]byte(tc.body)); got != tc.want {
				t.Fatalf("embeddedBaseRespStatus(%q) = %d, want %d", tc.body, got, tc.want)
			}
		})
	}
}

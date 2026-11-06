package server

import (
	"net/http"
	"testing"
	"time"
)

func TestParseRetryAfter(t *testing.T) {
	mk := func(v string) *http.Response {
		r := &http.Response{Header: http.Header{}}
		if v != "" {
			r.Header.Set("Retry-After", v)
		}
		return r
	}
	if got := parseRetryAfter(nil); got != 0 {
		t.Errorf("nil -> %d, want 0", got)
	}
	if got := parseRetryAfter(mk("")); got != 0 {
		t.Errorf("empty -> %d, want 0", got)
	}
	if got := parseRetryAfter(mk("120")); got != 120 {
		t.Errorf("delta-seconds 120 -> %d, want 120", got)
	}
	if got := parseRetryAfter(mk("-5")); got != 0 {
		t.Errorf("negative -> %d, want 0", got)
	}
	if got := parseRetryAfter(mk("garbage")); got != 0 {
		t.Errorf("garbage -> %d, want 0", got)
	}
	// HTTP-date ~30s in the future should parse to a positive, small value.
	future := time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(mk(future)); got < 20 || got > 31 {
		t.Errorf("http-date +30s -> %d, want ~30", got)
	}
}

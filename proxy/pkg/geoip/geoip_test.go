package geoip

import (
	"os"
	"testing"
)

func TestLookupWithoutDB(t *testing.T) {
	os.Unsetenv("GEOIP_DB_PATH")
	// Reset lazy init state for test
	mu.Lock()
	reader = nil
	initErr = nil
	initDone = false
	mu.Unlock()

	if got := Lookup("8.8.8.8"); got != "" {
		t.Fatalf("expected empty country without DB, got %q", got)
	}
}

func TestLookupInvalidIP(t *testing.T) {
	if got := Lookup("not-an-ip"); got != "" {
		t.Fatalf("expected empty country for invalid IP, got %q", got)
	}
}

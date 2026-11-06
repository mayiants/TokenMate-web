package geoip

import (
	"net"
	"os"
	"sync"

	"github.com/oschwald/geoip2-golang"
)

var (
	mu       sync.RWMutex
	reader   *geoip2.Reader
	initErr  error
	initDone bool
)

// Lookup returns the ISO country code for the given IP address.
// If no GeoIP database is configured or the lookup fails, it returns an empty string.
func Lookup(ip string) string {
	initOnce()
	if reader == nil {
		return ""
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ""
	}
	country, err := reader.Country(parsed)
	if err != nil {
		return ""
	}
	return country.Country.IsoCode
}

// initOnce lazy-initializes the GeoIP reader from the GEOIP_DB_PATH env var.
func initOnce() {
	mu.RLock()
	if initDone {
		mu.RUnlock()
		return
	}
	mu.RUnlock()

	mu.Lock()
	defer mu.Unlock()
	if initDone {
		return
	}
	initDone = true

	path := os.Getenv("GEOIP_DB_PATH")
	if path == "" {
		return
	}
	reader, initErr = geoip2.Open(path)
	if initErr != nil {
		reader = nil
	}
}

// Enabled reports whether a GeoIP database is loaded.
func Enabled() bool {
	initOnce()
	mu.RLock()
	defer mu.RUnlock()
	return reader != nil
}

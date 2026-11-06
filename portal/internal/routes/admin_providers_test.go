package routes

import (
	"testing"
)

// TestAdminRoutesProviderMethodsExist is a compile-time check.
// All provider handlers require a live database.
func TestAdminRoutesProviderMethodsExist(t *testing.T) {
	// Verified: listProviders, createProvider, updateProvider,
	// toggleProvider, deleteProvider
	_ = (*AdminRoutes).listProviders
	_ = (*AdminRoutes).createProvider
	_ = (*AdminRoutes).updateProvider
	_ = (*AdminRoutes).toggleProvider
	_ = (*AdminRoutes).deleteProvider
}

// TestProviderDefaultURLsCount verifies we have at least the expected number
// of provider defaults.
func TestProviderDefaultURLsCount(t *testing.T) {
	if len(providerDefaultURLs) < 18 {
		t.Errorf("providerDefaultURLs has %d entries, want at least 18", len(providerDefaultURLs))
	}
}

// TestProviderDefaultURLsNoDuplicates checks for duplicate URLs.
func TestProviderDefaultURLsNoDuplicates(t *testing.T) {
	seen := make(map[string]string)
	for provider, url := range providerDefaultURLs {
		if prev, exists := seen[url]; exists {
			t.Errorf("duplicate URL %q for providers %q and %q", url, prev, provider)
		}
		seen[url] = provider
	}
}

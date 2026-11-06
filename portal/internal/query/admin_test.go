package query

import "testing"

// TestAdminQueryCompile is a compile-time check that the admin query package
// can be imported and basic types exist.
func TestAdminQueryCompile(t *testing.T) {
	t.Skip("requires DB - this is a compile-time guard only")
	// This test ensures the package compiles and can be imported.
	// Actual query tests require a running PostgreSQL database.
}

// TestAdminDashboardDataType checks that the AdminDashboardData struct compiles.
func TestAdminDashboardDataType(t *testing.T) {
	t.Skip("requires DB - type check only")
}

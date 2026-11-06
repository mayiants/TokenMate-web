package main

import (
	"testing"
)

// TestMainCompiles is a compile-time check that the server package builds.
func TestMainCompiles(t *testing.T) {
	// This test just ensures the package compiles without errors.
	// Actual server integration tests need a running database, Redis, etc.
	t.Log("main package compiles successfully")
}

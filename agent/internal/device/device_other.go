//go:build !windows

package device

import "runtime"

func platformSeed() string { return "devhost" }

// OSVersion returns the platform name on non-Windows builds.
func OSVersion() string { return runtime.GOOS }

//go:build !windows

package device

func platformSeed() string { return "devhost" }

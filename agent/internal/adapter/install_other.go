//go:build !windows

package adapter

// candidateInstallPaths is Windows-only for now; other platforms rely on the
// manifest's install_paths list.
func candidateInstallPaths() []string { return nil }

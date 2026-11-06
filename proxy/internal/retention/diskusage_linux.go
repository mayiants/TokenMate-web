//go:build linux

package retention

import "syscall"

// diskUsedPercent returns the used percentage (df-style: used/(used+avail))
// of the filesystem holding path.
func diskUsedPercent(path string) (float64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return usedPercent(uint64(st.Blocks), uint64(st.Bfree), uint64(st.Bavail)), nil //nolint:unconvert // field widths differ per arch
}

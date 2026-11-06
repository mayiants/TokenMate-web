//go:build !linux

package retention

import "errors"

// diskUsedPercent is unsupported off Linux; the guard then fails open so
// retention keeps working on developer machines.
func diskUsedPercent(string) (float64, error) {
	return 0, errors.New("disk usage check unsupported on this OS")
}

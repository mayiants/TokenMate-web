package retention

// usedPercent mirrors df: reserved blocks (bfree-bavail) count as used so the
// figure matches what operators see.
func usedPercent(blocks, bfree, bavail uint64) float64 {
	used := blocks - bfree
	total := used + bavail
	if total == 0 {
		return 0
	}
	return float64(used) / float64(total) * 100
}

package store

import "math"

// round2Money rounds money to two decimals (half away from zero), mirroring
// pricing.Round2 without importing the pricing package (dependency direction:
// pricing must not be imported by store; see repo-layout.md). Non-finite or
// out-of-range inputs collapse to 0.
func round2Money(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v >= 1e15 || v <= -1e15 {
		return 0
	}
	if v >= 0 {
		return float64(int64(v*100+0.5)) / 100
	}
	return float64(int64(v*100-0.5)) / 100
}

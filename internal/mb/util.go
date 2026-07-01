package mb

import (
	"math"
	"strconv"
)

func maxZeroInt(v int) int {
	if v < 0 {
		return 0
	}
	return v
}
func boolTo01(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
func isFinitePositive(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0) && f > 0
}
func isFiniteNonNegative(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0) && f >= 0
}
func formatFloat(f float64) string {
	if !isFiniteNonNegative(f) {
		return "0"
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

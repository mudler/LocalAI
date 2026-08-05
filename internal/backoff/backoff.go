// Package backoff provides bounded retry-delay calculations.
package backoff

import "time"

// Exponential returns base*2^exponent capped at maximum. It saturates before
// multiplying so time.Duration cannot overflow.
func Exponential(base, maximum time.Duration, exponent uint) time.Duration {
	if base <= 0 || maximum <= 0 {
		return 0
	}
	if base >= maximum {
		return maximum
	}

	for ; exponent > 0; exponent-- {
		if base > maximum/2 {
			return maximum
		}
		base *= 2
	}
	return base
}

package auth

import "time"

// SetFeatureDefaultsClock replaces the clock of the feature defaults cache and
// returns a function that restores it.
func SetFeatureDefaultsClock(now func() time.Time) func() {
	prev := featureDefaultsClock
	featureDefaultsClock = now
	return func() { featureDefaultsClock = prev }
}

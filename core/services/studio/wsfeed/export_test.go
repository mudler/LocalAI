package wsfeed

import "time"

// SetTicks makes each value of ticks start one read of the journal in place
// of the interval ticker. Call it before the first Subscribe.
func SetTicks(f *Feed, ticks <-chan time.Time) { f.ticks = ticks }

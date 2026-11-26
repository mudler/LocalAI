package pairclient

import "time"

// SetPunchWait shortens the wait for a hole punch and returns a function
// that restores it.
func SetPunchWait(d time.Duration) func() {
	old := punchWait
	punchWait = d
	return func() { punchWait = old }
}

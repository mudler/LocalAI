//go:build windows

package backendtest

// processRuns is not implemented on Windows: the run.sh fixtures do not run
// there, so no spec asks.
func processRuns(int) bool { return false }

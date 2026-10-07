//go:build windows

package worker

import "errors"

// killProcessGroup is not supported on Windows: backends there are not started
// in process groups, and the ledger never holds a start time (it is read from
// /proc), so no entry is ever swept.
func killProcessGroup(int) error { return errors.New("process groups are not supported on windows") }

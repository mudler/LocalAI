//go:build windows

package workerledger

import "errors"

// killProcessGroup is not supported on Windows: backends there are not started
// in process groups, so no entry is ever swept.
func killProcessGroup(int) error { return errors.New("process groups are not supported on windows") }

// GroupAlive is always false: there are no process groups to own on Windows.
func GroupAlive(int) bool { return false }

//go:build windows

package cli

import "errors"

// shutdownListenerOnPort is not supported on Windows: the fixture that uses it
// reads /proc/self/fd and is skipped there.
func shutdownListenerOnPort(int) (bool, error) {
	return false, errors.New("not supported on windows")
}

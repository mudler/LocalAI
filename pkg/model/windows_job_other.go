//go:build !windows

package model

// Windows job objects do not exist on other platforms. The backendProcessRuntime
// methods call these unconditionally; the stubs keep the package compiling
// everywhere and are no-ops outside Windows.
func createWindowsJobObject(_ int) (uintptr, error) {
	return 0, nil
}

func terminateWindowsJobObject(_ uintptr) {}

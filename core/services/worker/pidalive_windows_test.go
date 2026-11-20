//go:build windows

package worker

import (
	"strconv"

	"golang.org/x/sys/windows"
)

// pidAlive asks the OS whether the process still runs. Windows has no signal 0,
// so it reads the exit code of the process: STILL_ACTIVE means it runs.
func pidAlive(pid string) bool {
	n, err := strconv.Atoi(pid)
	if err != nil {
		return false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(n))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}

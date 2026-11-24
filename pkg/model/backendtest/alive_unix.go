//go:build !windows

package backendtest

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

// processRuns reports whether pid names a process that runs and is not a
// zombie waiting to be reaped.
func processRuns(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	// #nosec G304 -- a /proc path built from a numeric PID.
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		// No /proc (macOS): the signal check is all there is.
		return true
	}
	fields := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:]))
	return len(fields) == 0 || fields[0] != "Z"
}

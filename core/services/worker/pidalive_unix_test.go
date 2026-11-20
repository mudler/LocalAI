//go:build !windows

package worker

import (
	"os"
	"strconv"
	"syscall"
)

// pidAlive probes the OS directly for a process ID. The supervisor's own
// liveness helpers all go through go-processmanager's pidfile, which Stop
// deletes as part of releasing the handle, so they report "not alive" even if
// the signal never landed. Only asking the kernel proves termination.
func pidAlive(pid string) bool {
	n, err := strconv.Atoi(pid)
	if err != nil {
		return false
	}
	proc, err := os.FindProcess(n)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

//go:build windows

package worker

import "os"

// syscallKill ends a process the way a crash would, with no chance to clean up.
func syscallKill(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

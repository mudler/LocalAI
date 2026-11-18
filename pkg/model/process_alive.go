package model

import (
	"runtime"

	process "github.com/mudler/go-processmanager"
)

// ProcessAlive reports whether a backend process started by this package is
// still running.
//
// go-processmanager's IsAlive sends signal 0, which Go does not support on
// Windows, so there it always reports false. On Windows this function uses
// the process manager's Done() channel instead: the monitor goroutine closes
// it when Wait returns. On other platforms it calls IsAlive.
func ProcessAlive(p *process.Process) bool {
	if p == nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return aliveFromDone(p)
	}
	return p.IsAlive()
}

// aliveFromDone reports a started process as alive until its monitor has seen
// it exit. A handle that was never started has no PID and is not alive.
func aliveFromDone(p *process.Process) bool {
	if p.CurrentPID() == "" {
		return false
	}
	select {
	case <-p.Done():
		return false
	default:
		return true
	}
}

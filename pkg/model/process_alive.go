package model

import (
	process "github.com/mudler/go-processmanager"
)

// ProcessAlive reports whether a backend process started by this package is
// still running. It is a nil-safe wrapper around the process manager's
// IsAlive, which works on every platform, Windows included.
//
// IsAlive reads the pidfile. After the process exits and its pidfile is
// removed (by Stop) it reports false. A pidfile left behind by a process that
// exited on its own can name a PID that the OS has reused for another
// process, so callers that need an exit signal should also watch Done().
func ProcessAlive(p *process.Process) bool {
	if p == nil {
		return false
	}
	return p.IsAlive()
}

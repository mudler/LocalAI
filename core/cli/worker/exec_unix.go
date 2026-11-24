//go:build !windows

package worker

import "syscall"

// execBackend replaces the worker process with the backend process, so the
// backend receives the signals of the service manager directly.
func execBackend(path string, argv, env []string) error {
	return syscall.Exec(path, argv, env)
}

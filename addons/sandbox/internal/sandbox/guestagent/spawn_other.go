//go:build !linux

package guestagent

import "errors"

// LinuxSpawner starts processes. It works only on Linux.
type LinuxSpawner struct {
	Env  []string
	Dir  string
	Reap bool
}

// Spawn fails outside Linux.
func (sp *LinuxSpawner) Spawn(req ExecRequest) (Proc, error) {
	return nil, errors.New("guestagent: processes need Linux")
}

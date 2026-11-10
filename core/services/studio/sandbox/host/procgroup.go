//go:build linux || darwin

package host

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// A job runs in its own process group. The host backend records the group
// (the pid of the leader and its start time) in a file under the root, and
// kills the group when the leader returns and when the job ends. When the
// process of the backend dies, the groups of its jobs stay alive; the next
// backend on the same root kills them from these files.

func (b *Backend) groupDir() string { return filepath.Join(b.root, "jobgroups") }

// recordGroup writes the file of a job group. A failure is not fatal: the
// group is still killed by the normal paths.
func (b *Backend) recordGroup(pgid int) {
	start, ok := procStart(pgid)
	if !ok {
		return
	}
	if err := os.MkdirAll(b.groupDir(), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(b.groupDir(), strconv.Itoa(pgid)), []byte(start), 0o600)
}

func (b *Backend) forgetGroup(pgid int) {
	_ = os.Remove(filepath.Join(b.groupDir(), strconv.Itoa(pgid)))
}

// reapGroups kills the process groups that an earlier run recorded and forgets
// them. It kills only a group it recorded, and never a reused pid: a leader
// that still runs must have the recorded start time. A leader that is gone
// leaves its group alive only while members exist, and the kernel does not
// reuse a pid that is the id of a live group, so the group is ours.
func reapGroups(dir string) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, f := range files {
		path := filepath.Join(dir, f.Name())
		pgid, err := strconv.Atoi(f.Name())
		b, rerr := os.ReadFile(path) // #nosec G304 -- a file of the backend's own folder
		_ = os.Remove(path)
		if err != nil || rerr != nil || pgid <= 1 {
			continue
		}
		if now, alive := procStart(pgid); alive && now != strings.TrimSpace(string(b)) {
			continue // the pid belongs to another process now
		}
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
}

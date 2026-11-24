//go:build windows

// SPDX-License-Identifier: MIT
package runtime

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/mudler/LocalAI/pkg/jobobject"
	"golang.org/x/sys/windows"
)

// reportEndpointDefault is true: Windows cannot pass numbered descriptors,
// so the child gets named pipe handles and reports its own endpoint.
const reportEndpointDefault = true

// inheritFiles marks the handles inheritable and lists them for the child.
// Go passes PROC_THREAD_ATTRIBUTE_HANDLE_LIST, so the child inherits only the
// listed handles and its standard streams. The values are the same in the
// child.
func inheritFiles(cmd *exec.Cmd, files ...*os.File) ([]string, error) {
	names := make([]string, len(files))
	handles := make([]syscall.Handle, len(files))
	for i, f := range files {
		h := windows.Handle(f.Fd())
		if e := windows.SetHandleInformation(h, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); e != nil {
			return nil, e
		}
		handles[i] = syscall.Handle(h)
		names[i] = strconv.FormatUint(uint64(h), 10)
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.AdditionalInheritedHandles = append(cmd.SysProcAttr.AdditionalInheritedHandles, handles...)
	return names, nil
}

// platformEnv gives the child a temporary folder. Without TMP and TEMP,
// Windows answers the Windows folder, which a normal user cannot write. Go
// adds SYSTEMROOT, which the network stack needs, by itself.
func platformEnv() []string {
	t := os.TempDir()
	return []string{"TMP=" + t, "TEMP=" + t}
}

// executablePath returns the add-on executable when it is a regular .exe
// file, reached through no link inside the package. Windows has no execute
// bit: the .exe extension is what makes a file a program there. The package
// directory is compared after its own resolution, because Windows may spell
// one folder in more than one way (short 8.3 names, letter case).
func executablePath(dir, rel string) (string, bool) {
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if !strings.EqualFold(filepath.Ext(path), ".exe") {
		return "", false
	}
	info, e := os.Lstat(path)
	if e != nil || !info.Mode().IsRegular() {
		return "", false
	}
	base, e := filepath.EvalSymlinks(dir)
	if e != nil {
		return "", false
	}
	resolved, e := filepath.EvalSymlinks(path)
	if e != nil || !strings.EqualFold(resolved, filepath.Join(base, filepath.FromSlash(rel))) {
		return "", false
	}
	return path, true
}

// processTree is a job object with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE that
// holds the add-on process and everything it starts. Windows has no process
// groups: terminating the job stops the whole tree, and the job also stops it
// when LocalAI itself dies, since the system then closes the last handle.
type processTree struct {
	once sync.Once
	job  windows.Handle
}

// newProcessTree puts pid in a new job. A failure (LocalAI already runs in a
// job that forbids it) leaves the tree empty: the process is still killed
// by its own handle, but what it started is not.
func newProcessTree(pid int) *processTree {
	t := &processTree{}
	if job, e := jobobject.AssignKillOnClose(pid); e == nil {
		t.job = job
	}
	return t
}

// terminate stops every process left in the job and releases it. It is safe
// to call more than once.
func (t *processTree) terminate() {
	t.once.Do(func() {
		if t.job == 0 {
			return
		}
		_ = windows.TerminateJobObject(t.job, 1)
		_ = windows.CloseHandle(t.job)
		t.job = 0
	})
}

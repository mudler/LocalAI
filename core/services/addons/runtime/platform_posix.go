//go:build !windows

// SPDX-License-Identifier: MIT
package runtime

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

// reportEndpointDefault is false: the owner passes descriptors 3 and 4.
const reportEndpointDefault = false

// inheritFiles has the child inherit files as descriptors 3, 4, ... It serves
// the reported way, which the specs run on POSIX systems too.
func inheritFiles(cmd *exec.Cmd, files ...*os.File) ([]string, error) {
	cmd.ExtraFiles = files
	names := make([]string, len(files))
	for i := range files {
		names[i] = strconv.Itoa(3 + i)
	}
	return names, nil
}

// platformEnv adds nothing to the fixed environment of the child.
func platformEnv() []string { return nil }

// executablePath returns the add-on executable when it is a regular file with
// an execute bit, reached through no symbolic link.
func executablePath(dir, rel string) (string, bool) {
	path := filepath.Join(dir, filepath.FromSlash(rel))
	resolved, e := filepath.EvalSymlinks(path)
	if e != nil || resolved != path {
		return "", false
	}
	info, e := os.Stat(path)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", false
	}
	return path, true
}

// processTree stops what an add-on process started. POSIX systems have
// nothing to add to the kill of the process: installed code is trusted and
// must not detach workers (see the runtime docs).
type processTree struct{}

func newProcessTree(int) processTree { return processTree{} }
func (processTree) terminate()       {}

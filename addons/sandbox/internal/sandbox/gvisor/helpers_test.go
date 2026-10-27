//go:build linux

package gvisor

import (
	"os"
	"testing"
)

// shortDir is a temporary folder with a short path, for Unix sockets.
func shortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "gv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removeAllForTest(d) })
	return d
}

func removeAllForTest(d string) { _ = removeAll(d) }

// sessionFolders lists the entries of a StateDir except its lock file.
func sessionFolders(dir string) []string {
	ents, _ := os.ReadDir(dir)
	var out []string
	for _, e := range ents {
		if e.Name() != lockName {
			out = append(out, e.Name())
		}
	}
	return out
}

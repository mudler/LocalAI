//go:build linux

package gvisor

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// shortDir is a temporary folder with a short path, for Unix sockets.
func shortDir() string {
	d, err := os.MkdirTemp("", "gv")
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { removeAllForTest(d) })
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

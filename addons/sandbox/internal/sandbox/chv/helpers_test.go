package chv

import (
	"os"
	"os/exec"
	"testing"
)

func needTools(t *testing.T, tools ...string) {
	t.Helper()
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not found", tool) //nolint:forbidigo // plain testing.T test; this package has no Ginkgo suite
		}
	}
}

// shortDir is a temporary folder with a short path, for Unix sockets.
func shortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "chv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

//go:build linux || darwin

package sbtest

import (
	"os/exec"
	"testing"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
)

func TestSuiteWithFakeBackend(t *testing.T) {
	for _, tool := range []string{"sh", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not found", tool)
		}
	}
	RunBackendSuite(t, func(t *testing.T) sandbox.Backend {
		return NewFakeBackend(t.TempDir())
	})
}

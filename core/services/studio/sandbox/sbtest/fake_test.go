//go:build linux || darwin

package sbtest

import (
	"os/exec"

	. "github.com/onsi/ginkgo/v2"

	"github.com/mudler/LocalAI/core/services/studio/sandbox"
)

var _ = Describe("the conformance suite with the fake backend", func() {
	BeforeEach(func() {
		for _, tool := range []string{"sh", "git"} {
			if _, err := exec.LookPath(tool); err != nil {
				Skip(tool + " not found")
			}
		}
	})
	RunBackendSuite(func() sandbox.Backend {
		return NewFakeBackend(GinkgoT().TempDir())
	})
})

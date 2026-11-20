package worker

import (
	"os/exec"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// skipOnWindows skips the running spec on a Windows host. Use it only for a
// spec that needs a Unix feature, and say which one in the reason.
func skipOnWindows(reason string) {
	GinkgoHelper()
	if runtime.GOOS == "windows" {
		Skip(reason)
	}
}

// skipWithoutExactRemoval skips a spec that depends on safefile.RemoveExact,
// which fails closed on platforms without component-relative no-follow
// operations, Windows among them.
func skipWithoutExactRemoval() {
	GinkgoHelper()
	skipOnWindows("safefile.RemoveExact is not implemented on Windows and fails closed")
}

// sleepBinary finds a sleep program to stand in for a long running backend.
func sleepBinary() string {
	GinkgoHelper()
	path, err := exec.LookPath("sleep")
	Expect(err).NotTo(HaveOccurred())
	return path
}

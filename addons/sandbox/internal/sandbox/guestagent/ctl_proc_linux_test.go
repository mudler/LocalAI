package guestagent

import (
	"os"
	"os/exec"
	"strings"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// ctlProcChildEnv marks the re-executed test process that changes its own
// process state (dumpable flag, ambient set), so the test binary keeps its own.
const ctlProcChildEnv = "SANDBOX_GUESTAGENT_CTLPROC_CHILD"

// runCtlProcChild re-runs the spec called name in a child process. The child
// prints "[SKIPPED]" in its verbose report when the spec skips itself.
func runCtlProcChild(name string) {
	cmd := exec.Command(os.Args[0], "-ginkgo.focus="+name, "-ginkgo.v", "-ginkgo.no-color")
	cmd.Env = append(os.Environ(), ctlProcChildEnv+"=1")
	out, err := cmd.CombinedOutput()
	Expect(err).ToNot(HaveOccurred(), "child: %s", out)
	if strings.Contains(string(out), "[SKIPPED]") {
		// The child says why: ClearAmbient needs a binary without cgo.
		Skip("the child skipped (run with CGO_ENABLED=0 to run it):\n" + string(out))
	}
	Expect(string(out)).To(ContainSubstring("1 Passed"), "child did not run its checks")
}

var _ = Describe("process state", func() {
	It("SetNotDumpable", func() { runCtlProcChild("SetNotDumpable child") })

	It("SetNotDumpable child", func() {
		if os.Getenv(ctlProcChildEnv) == "" {
			Skip("runs only as the child of the SetNotDumpable spec")
		}
		const prGetDumpable = 3
		Expect(SetNotDumpable()).To(Succeed())
		got, _, e := syscall.RawSyscall(syscall.SYS_PRCTL, prGetDumpable, 0, 0)
		Expect(e).To(BeZero())
		Expect(got).To(BeZero(), "PR_GET_DUMPABLE")
	})

	It("ClearAmbient", func() { runCtlProcChild("ClearAmbient child") })

	// The test user holds no capability, so the ambient set is empty before and
	// after; the check proves the call works on every thread of a binary without
	// cgo and leaves CapAmb at zero.
	It("ClearAmbient child", func() {
		if os.Getenv(ctlProcChildEnv) == "" {
			Skip("runs only as the child of the ClearAmbient spec")
		}
		if err := ClearAmbient(); err != nil {
			Skip("ClearAmbient needs a binary without cgo (run with CGO_ENABLED=0): " + err.Error())
		}
		status, err := os.ReadFile("/proc/self/status")
		Expect(err).ToNot(HaveOccurred())
		for l := range strings.SplitSeq(string(status), "\n") {
			if after, ok := strings.CutPrefix(l, "CapAmb:"); ok {
				v := strings.TrimSpace(after)
				Expect(strings.Trim(v, "0")).To(BeEmpty(), "ambient set = %s", v)
				return
			}
		}
		Fail("no CapAmb line in /proc/self/status")
	})
})

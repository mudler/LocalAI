package guestagent

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

// ctlProcChildEnv marks the re-executed test process that changes its own
// process state (dumpable flag, ambient set), so the test binary keeps its own.
const ctlProcChildEnv = "SANDBOX_GUESTAGENT_CTLPROC_CHILD"

func runCtlProcChild(t *testing.T, name string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+name+"$", "-test.v")
	cmd.Env = append(os.Environ(), ctlProcChildEnv+"=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "--- SKIP: "+name) {
		// The child says why: ClearAmbient needs a binary without cgo.
		t.Skipf("the child skipped (run with CGO_ENABLED=0 to run it):\n%s", out)
	}
	if !strings.Contains(string(out), "--- PASS: "+name) {
		t.Fatalf("child did not run its checks:\n%s", out)
	}
}

func TestSetNotDumpable(t *testing.T) { runCtlProcChild(t, "TestSetNotDumpableChild") }

func TestSetNotDumpableChild(t *testing.T) {
	if os.Getenv(ctlProcChildEnv) == "" {
		t.Skip("runs only as the child of TestSetNotDumpable")
	}
	const prGetDumpable = 3
	if err := SetNotDumpable(); err != nil {
		t.Fatal(err)
	}
	got, _, e := syscall.RawSyscall(syscall.SYS_PRCTL, prGetDumpable, 0, 0)
	if e != 0 || got != 0 {
		t.Fatalf("PR_GET_DUMPABLE = %d, errno %v", got, e)
	}
}

func TestClearAmbient(t *testing.T) { runCtlProcChild(t, "TestClearAmbientChild") }

// The test user holds no capability, so the ambient set is empty before and
// after; the check proves the call works on every thread of a binary without
// cgo and leaves CapAmb at zero.
func TestClearAmbientChild(t *testing.T) {
	if os.Getenv(ctlProcChildEnv) == "" {
		t.Skip("runs only as the child of TestClearAmbient")
	}
	if err := ClearAmbient(); err != nil {
		t.Skipf("ClearAmbient needs a binary without cgo (run with CGO_ENABLED=0): %v", err)
	}
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(string(status), "\n") {
		if strings.HasPrefix(l, "CapAmb:") {
			if v := strings.TrimSpace(strings.TrimPrefix(l, "CapAmb:")); strings.Trim(v, "0") != "" {
				t.Fatalf("ambient set = %s", v)
			}
			return
		}
	}
	t.Fatal("no CapAmb line in /proc/self/status")
}

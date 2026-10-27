// SPDX-License-Identifier: MIT
package actione2e_test

import (
	"os"
	"strings"
	"testing"
)

// unavailableAnswer is what the Sandbox tools answer on a host where no
// backend can run commands (see the Sandbox section of the add-ons page).
const unavailableAnswer = "This host cannot run commands."

// TestSmokeThePackagedSandboxRunsACommandOrSaysItCannot is the release smoke
// test of the Sandbox package. scripts/addons/sandbox_smoke.sh builds the
// package, extracts it and sets:
//
//	SANDBOX_SMOKE_PACKAGE  the extracted package directory
//	SANDBOX_SMOKE_REQUIRE  "isolated" when the host must run commands in gVisor
//	                       or KVM; empty accepts a host that cannot, as long as
//	                       the tools say so in the documented words
//
// The add-on starts from the files of the package under the real supervisor,
// with the default settings and real workspace operations, and a chat user
// calls its tools. The test skips without the package.
func TestSmokeThePackagedSandboxRunsACommandOrSaysItCannot(t *testing.T) {
	pkg := os.Getenv("SANDBOX_SMOKE_PACKAGE")
	if pkg == "" {
		t.Skip("set SANDBOX_SMOKE_PACKAGE to an extracted Sandbox package (scripts/addons/sandbox_smoke.sh does)")
	}
	w := startAddonFrom(t, "sandbox", pkg)
	w.put("alice", "/smoke.txt", "from the user's files\n")

	st := w.read("status", "alice", nil)
	adminStatus := w.read("status", "root", nil)
	t.Logf("status for a user: %v", st)
	t.Logf("status for an administrator: %v", adminStatus)
	if st["hostNotice"] == true {
		t.Fatalf("the default settings run commands on the host with no isolation: %v", st)
	}

	got := w.act("bash", "alice", "convsmoke", map[string]any{"command": "echo smoke-ok; cat home/smoke.txt; echo written > home/smoke-out.txt"})
	out, _ := got["output"].(string)
	if st["canRun"] != true {
		if os.Getenv("SANDBOX_SMOKE_REQUIRE") == "isolated" {
			t.Fatalf("this host was expected to run commands in gVisor or KVM, but cannot: %v", adminStatus)
		}
		if out != unavailableAnswer {
			t.Fatalf("a host that cannot run commands answered %q, want %q", out, unavailableAnswer)
		}
		if _, made := w.text("alice", "/smoke-out.txt"); made {
			t.Fatal("a command ran with no backend")
		}
		return
	}
	if active, _ := st["active"].(string); active != "gvisor" && active != "kvm" {
		t.Fatalf("the active backend is %q, want gvisor or kvm", active)
	}
	if !strings.Contains(out, "smoke-ok") || !strings.Contains(out, "from the user's files") {
		t.Fatalf("the command did not run in the sandbox with the user's files: %q (%v)", out, got)
	}
	if text, made := w.text("alice", "/smoke-out.txt"); !made || !strings.Contains(text, "written") {
		t.Fatalf("the file the command wrote did not reach the user's files: %q %v", text, made)
	}
}

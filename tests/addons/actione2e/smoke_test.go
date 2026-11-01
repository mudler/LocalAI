// SPDX-License-Identifier: MIT
package actione2e_test

import (
	"fmt"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// unavailableAnswer is what the Sandbox tools answer on a host where no
// backend can run commands (see the Sandbox section of the add-ons page).
const unavailableAnswer = "This host cannot run commands."

var _ = Describe("Packaged Sandbox smoke", func() {
	// This is the release smoke
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
	It("runs a command or says it cannot", func() {
		pkg := os.Getenv("SANDBOX_SMOKE_PACKAGE")
		if pkg == "" {
			Skip("set SANDBOX_SMOKE_PACKAGE to an extracted Sandbox package (scripts/addons/sandbox_smoke.sh does)")
		}
		w := startAddonFrom("sandbox", pkg)
		w.put("alice", "/smoke.txt", "from the user's files\n")

		st := w.read("status", "alice", nil)
		adminStatus := w.read("status", "root", nil)
		_, _ = fmt.Fprintf(GinkgoWriter, "status for a user: %v\n", st)
		_, _ = fmt.Fprintf(GinkgoWriter, "status for an administrator: %v\n", adminStatus)
		Expect(st["hostNotice"]).NotTo(Equal(true), "the default settings run commands on the host with no isolation: %v", st)

		got := w.act("bash", "alice", "convsmoke", map[string]any{"command": "echo smoke-ok; cat smoke.txt; echo written > smoke-out.txt"})
		out, _ := got["output"].(string)
		if st["canRun"] != true {
			if os.Getenv("SANDBOX_SMOKE_REQUIRE") == "isolated" {
				Fail(fmt.Sprintf("this host was expected to run commands in gVisor or KVM, but cannot: %v", adminStatus))
			}
			Expect(out).To(Equal(unavailableAnswer), "a host that cannot run commands")
			_, made := w.text("alice", "/smoke-out.txt")
			Expect(made).To(BeFalse(), "a command ran with no backend")
			return
		}
		active, _ := st["active"].(string)
		Expect(active).To(BeElementOf("gvisor", "kvm"), "the active backend")
		Expect(out).To(ContainSubstring("smoke-ok"), "the command did not run in the sandbox with the user's files: (%v)", got)
		Expect(out).To(ContainSubstring("from the user's files"), "the command did not run in the sandbox with the user's files: (%v)", got)
		text, made := w.text("alice", "/smoke-out.txt")
		Expect(made).To(BeTrue(), "the file the command wrote did not reach the user's files")
		Expect(text).To(ContainSubstring("written"))
	})
})

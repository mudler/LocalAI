// SPDX-License-Identifier: MIT
package main

import (
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/addons/sandbox/internal/backends"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/runner"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("The host backend in WSL 2", func() {
	inWSL := func() {
		old := ownerInWSL
		ownerInWSL = func() bool { return true }
		DeferCleanup(func() { ownerInWSL = old })
	}
	It("cannot be chosen when a Windows host runs the add-on in WSL, even after its phrase, and says why", func() {
		inWSL()
		sel, err := newSelector(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		Expect(sel.ConfirmHost(backends.HostPhrase)).To(Succeed())
		err = sel.Set(backends.Host)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("Windows drives"))
	})
	It("does not run a host choice saved before, and runs nothing in its place", func() {
		inWSL()
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"backend":"host","hostConfirmed":true}`), 0o600)).To(Succeed())
		sel, err := newSelector(dir)
		Expect(err).NotTo(HaveOccurred())
		_, err = sel.Backend()
		Expect(err).To(MatchError(runner.ErrNoSandbox))
		st := sel.Status()
		Expect(st.Active).To(BeEmpty())
		Expect(st.HostNotice).To(BeFalse())
		var host backends.BackendState
		for _, b := range st.Backends {
			if b.Name == string(backends.Host) {
				host = b
			}
		}
		Expect(host.Usable).To(BeFalse())
		Expect(host.Reason).To(ContainSubstring("Windows drives"))
	})
})

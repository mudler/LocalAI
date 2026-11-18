// SPDX-License-Identifier: MIT
package contract

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Execution environment", func() {
	windows := func() HostCompatibility {
		h := compatible()
		h.OS, h.Arch = "windows", "amd64"
		return h
	}
	wsl := func() Manifest {
		m, e := ParseManifest(fixture())
		Expect(e).NotTo(HaveOccurred())
		m.OS, m.Arch = "windows", "amd64"
		m.Execution = ExecutionWSL2
		m.FeatureLevel = 10
		return m
	}

	It("is native when absent, and a native manifest is unchanged", func() {
		m, e := ParseManifest(fixture())
		Expect(e).NotTo(HaveOccurred())
		Expect(m.Execution).To(BeEmpty())
		Expect(m.RunsInWSL()).To(BeFalse())
		Expect(ValidateManifest(m, compatible())).To(Succeed())
	})
	It("accepts wsl2 on a windows/amd64 host at feature level 10", func() {
		m := wsl()
		Expect(m.RunsInWSL()).To(BeTrue())
		Expect(ValidateManifest(m, windows())).To(Succeed())
	})
	It("requires the manifest to declare feature level 10", func() {
		m := wsl()
		m.FeatureLevel = 0
		Expect(ValidateManifest(m, windows())).To(MatchError(ContainSubstring("must declare featureLevel 10")))
	})
	It("is refused by a host of an older level", func() {
		h := windows()
		h.FeatureLevel = 9
		Expect(ValidateManifest(wsl(), h)).To(MatchError("needs a newer LocalAI"))
	})
	It("refuses wsl2 for any platform but windows/amd64, and unknown values", func() {
		m := wsl()
		m.OS, m.Arch = "linux", "amd64"
		Expect(ValidateManifest(m, compatible())).To(MatchError(ContainSubstring("execution")))
		m = wsl()
		m.Arch = "arm64"
		h := windows()
		h.Arch = "arm64"
		Expect(ValidateManifest(m, h)).To(MatchError(ContainSubstring("execution")))
		m = wsl()
		m.Execution = "wsl1"
		Expect(ValidateManifest(m, windows())).To(MatchError(ContainSubstring("execution")))
	})
})

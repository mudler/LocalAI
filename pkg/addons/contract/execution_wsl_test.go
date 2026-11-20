// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("OS settings", func() {
	windows := func() HostCompatibility {
		h := compatible()
		h.OS, h.Arch = "windows", "amd64"
		return h
	}
	wsl := func() Manifest {
		m, e := ParseManifest(fixture())
		Expect(e).NotTo(HaveOccurred())
		m.OS, m.Arch = "windows", "amd64"
		m.OSSettings = &OSSettings{Windows: &WindowsSettings{Execution: ExecutionWSL2}}
		m.FeatureLevel = 10
		return m
	}
	// manifestJSON is the service fixture with the given top-level keys set.
	manifestJSON := func(set map[string]any) []byte {
		var m map[string]any
		Expect(json.Unmarshal(fixture(), &m)).To(Succeed())
		for k, v := range set {
			m[k] = v
		}
		b, e := json.Marshal(m)
		Expect(e).NotTo(HaveOccurred())
		return b
	}
	block := func(raw string) any { return json.RawMessage(raw) }

	It("is absent for a native package, and a native manifest is unchanged", func() {
		m, e := ParseManifest(fixture())
		Expect(e).NotTo(HaveOccurred())
		Expect(m.OSSettings).To(BeNil())
		Expect(m.RunsInWSL()).To(BeFalse())
		Expect(ValidateManifest(m, compatible())).To(Succeed())
		b, e := json.Marshal(m)
		Expect(e).NotTo(HaveOccurred())
		Expect(string(b)).NotTo(ContainSubstring(`"osSettings"`))
	})

	It("reads osSettings.windows.execution wsl2 for a windows/amd64 package at feature level 10", func() {
		m, e := ParseManifest(manifestJSON(map[string]any{"os": "windows", "arch": "amd64", "featureLevel": 10,
			"osSettings": block(`{"windows":{"execution":"wsl2"}}`)}))
		Expect(e).NotTo(HaveOccurred())
		Expect(m.RunsInWSL()).To(BeTrue())
		Expect(ValidateManifest(m, windows())).To(Succeed())
		b, e := json.Marshal(m)
		Expect(e).NotTo(HaveOccurred())
		Expect(string(b)).To(ContainSubstring(`"osSettings":{"windows":{"execution":"wsl2"}}`))
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

	It("is refused by the strict reader of an older host, which knows no osSettings key", func() {
		type olderManifest struct {
			ID   string `json:"id"`
			OS   string `json:"os"`
			Arch string `json:"arch"`
		}
		var o olderManifest
		Expect(strictJSON([]byte(`{"id":"a","os":"windows","arch":"amd64","osSettings":{"windows":{"execution":"wsl2"}}}`), &o, 1024)).NotTo(Succeed())
	})

	It("refuses the block on a package for any other platform", func() {
		_, e := ParseManifest(manifestJSON(map[string]any{"featureLevel": 10,
			"osSettings": block(`{"windows":{"execution":"wsl2"}}`)}))
		Expect(e).To(MatchError(ContainSubstring("osSettings.windows")))
		m := wsl()
		m.OS, m.Arch = "linux", "amd64"
		Expect(ValidateManifest(m, compatible())).To(MatchError(ContainSubstring("osSettings.windows")))
		m = wsl()
		m.Arch = "arm64"
		h := windows()
		h.Arch = "arm64"
		Expect(ValidateManifest(m, h)).To(MatchError(ContainSubstring("osSettings.windows.execution")))
	})

	DescribeTable("refuses an unknown OS key, an unknown field or an unknown value",
		func(raw string) {
			_, e := ParseManifest(manifestJSON(map[string]any{"os": "windows", "arch": "amd64", "featureLevel": 10,
				"osSettings": block(raw)}))
			Expect(e).To(HaveOccurred())
		},
		Entry("an unknown OS key", `{"linux":{"execution":"wsl2"}}`),
		Entry("an unknown OS key beside windows", `{"windows":{"execution":"wsl2"},"darwin":{}}`),
		Entry("an unknown field", `{"windows":{"execution":"wsl2","distro":"x"}}`),
		Entry("an unknown value", `{"windows":{"execution":"wsl1"}}`),
		Entry("an empty value", `{"windows":{"execution":""}}`),
		Entry("an empty windows block", `{"windows":{}}`),
		Entry("an empty block", `{}`),
		Entry("a null windows block", `{"windows":null}`),
	)

	It("refuses the former top-level execution field", func() {
		_, e := ParseManifest(manifestJSON(map[string]any{"os": "windows", "arch": "amd64", "featureLevel": 10, "execution": "wsl2"}))
		Expect(e).To(HaveOccurred())
	})

	It("refuses the block under the key platform", func() {
		_, e := ParseManifest(manifestJSON(map[string]any{"os": "windows", "arch": "amd64", "featureLevel": 10,
			"platform": block(`{"windows":{"execution":"wsl2"}}`)}))
		Expect(e).To(HaveOccurred())
	})
})

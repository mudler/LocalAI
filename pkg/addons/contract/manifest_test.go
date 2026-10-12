// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"os"
	"strings"
)

func fixture() []byte {
	b, e := os.ReadFile("../../../tests/addons/fixtures/contracts/service.json")
	Expect(e).NotTo(HaveOccurred())
	return b
}
func compatible() HostCompatibility {
	return HostCompatibility{Host: 1, API: 1, Protocol: 1, SDK: 1, DataFormat: 1, OS: "linux", Arch: "amd64"}
}

var _ = Describe("Manifest", func() {
	It("accepts independent service tool page and chat capabilities", func() {
		for _, name := range []string{"service", "headless", "page", "chat"} {
			b, e := os.ReadFile("../../../tests/addons/fixtures/contracts/" + name + ".json")
			Expect(e).NotTo(HaveOccurred())
			m, e := ParseManifest(b)
			Expect(e).NotTo(HaveOccurred())
			Expect(ValidateManifest(m, compatible())).To(Succeed())
		}
		m, e := ParseManifest(fixture())
		Expect(e).NotTo(HaveOccurred())
		h := compatible()
		h.SDK = 0
		Expect(ValidateManifest(m, h)).To(Succeed())
		m.Capabilities = Capabilities{Tools: []string{"query"}}
		Expect(ValidateManifest(m, h)).To(Succeed())
	})
	It("rejects invalid namespace IDs and executable paths", func() {
		for _, id := range []string{"", "../sample", "a/b", "A", "a.b", strings.Repeat("a", 65)} {
			m, _ := ParseManifest(fixture())
			m.ID = id
			Expect(ValidateManifest(m, compatible())).NotTo(Succeed(), id)
		}
		for _, p := range []string{"", "/bin/x", "..", "../x", "bin/../x", "bin//x", "C:/x", `bin\x`, "https://x", "bin/%2e/x"} {
			m, _ := ParseManifest(fixture())
			m.Executable = p
			Expect(ValidateManifest(m, compatible())).NotTo(Succeed(), p)
		}
	})
	It("rejects invalid versions ranges and capabilities", func() {
		for _, v := range []string{"", "latest", "development", "v1.0.0", "01.2.3", "1.2", "1.2.3+mutable", "1.2.3-a..b", "1.2.3-a."} {
			m, _ := ParseManifest(fixture())
			m.Version = v
			Expect(ValidateManifest(m, compatible())).NotTo(Succeed(), v)
		}
		m, _ := ParseManifest(fixture())
		m.SchemaVersion = 2
		Expect(ValidateManifest(m, compatible())).NotTo(Succeed())
		m, _ = ParseManifest(fixture())
		m.API = VersionRange{Min: 2, Max: 1}
		Expect(ValidateManifest(m, compatible())).NotTo(Succeed())
		m, _ = ParseManifest(fixture())
		m.Capabilities = Capabilities{}
		Expect(ValidateManifest(m, compatible())).NotTo(Succeed())
		m.Capabilities.Tools = []string{"query", "query"}
		Expect(ValidateManifest(m, compatible())).NotTo(Succeed())
	})
	It("rejects unknown duplicate trailing and oversized manifest JSON", func() {
		for _, b := range [][]byte{[]byte(strings.Replace(string(fixture()), `"service":true`, `"service":true,"shell":true`, 1)), []byte(strings.Replace(string(fixture()), `"id":"sample"`, `"id":"sample","id":"other"`, 1)), append(fixture(), []byte(` {}`)...), []byte(`null`), []byte(strings.Repeat(" ", int(DefaultLimits().ManifestBytes)+1))} {
			_, e := ParseManifest(b)
			Expect(e).To(HaveOccurred())
		}
	})
	It("rejects incompatible host API protocol SDK data and platform fixtures", func() {
		b, e := os.ReadFile("../../../tests/addons/fixtures/contracts/incompatible.json")
		Expect(e).NotTo(HaveOccurred())
		var hosts []HostCompatibility
		Expect(json.Unmarshal(b, &hosts)).To(Succeed())
		page, e := os.ReadFile("../../../tests/addons/fixtures/contracts/page.json")
		Expect(e).NotTo(HaveOccurred())
		m, e := ParseManifest(page)
		Expect(e).NotTo(HaveOccurred())
		for _, h := range hosts {
			Expect(ValidateManifest(m, h)).NotTo(Succeed())
		}
	})
	It("validates UI only when declared and rejects inconsistent contributions", func() {
		m, _ := ParseManifest(fixture())
		m.Capabilities.UI = &UI{Entry: "ui/main.js", SDK: VersionRange{Min: 1, Max: 1}, ChatActions: []string{"open"}}
		Expect(ValidateManifest(m, compatible())).To(Succeed())
		for _, u := range []*UI{{}, {Entry: "../x", SDK: VersionRange{1, 1}}, {Entry: "ui/main.js", SDK: VersionRange{1, 1}, CSS: []string{"/x"}}, {Entry: "ui/main.js", SDK: VersionRange{1, 1}, Navigation: []string{"missing"}}, {Entry: "ui/main.js", SDK: VersionRange{1, 1}, ChatActions: []string{"open", "open"}}} {
			m.Capabilities.UI = u
			Expect(ValidateManifest(m, compatible())).NotTo(Succeed())
		}
	})
})

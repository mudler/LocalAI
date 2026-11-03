// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the files ability of a connector kind", func() {
	var m Manifest
	BeforeEach(func() {
		b, err := os.ReadFile("../../../tests/addons/fixtures/contracts/page.json")
		Expect(err).NotTo(HaveOccurred())
		m, err = ParseManifest(b)
		Expect(err).NotTo(HaveOccurred())
		m.API = VersionRange{Min: 2, Max: 2}
		m.Capabilities.UI = nil
		m.Capabilities.Connector = &ConnectorCapability{Kinds: []ConnectorKind{{ID: "telegram", Title: "Telegram", Delivery: DeliveryPull}}}
	})
	host := func() HostCompatibility {
		h := compatible()
		h.SDK = HostSDKVersion
		return h
	}
	declare := func(f *ConnectorFiles) { m.Capabilities.Connector.Kinds[0].Files = f }

	It("accepts a kind that declares what it receives and sends", func() {
		declare(&ConnectorFiles{
			Receive: &FileAbility{MaxBytes: 20 << 20},
			Send:    &FileAbility{MaxBytes: 50 << 20, Types: []string{"application/pdf", "image/*"}},
		})
		Expect(ValidateManifest(m, host())).To(Succeed())
	})

	It("accepts a kind that declares one direction only, or nothing", func() {
		declare(&ConnectorFiles{Send: &FileAbility{MaxBytes: 1 << 20}})
		Expect(ValidateManifest(m, host())).To(Succeed())
		declare(nil)
		Expect(ValidateManifest(m, host())).To(Succeed())
	})

	DescribeTable("refuses",
		func(f *ConnectorFiles) {
			declare(f)
			Expect(ValidateManifest(m, host())).NotTo(Succeed())
		},
		Entry("a limit of zero", &ConnectorFiles{Receive: &FileAbility{}}),
		Entry("a negative limit", &ConnectorFiles{Send: &FileAbility{MaxBytes: -1}}),
		Entry("a limit above the largest the host knows", &ConnectorFiles{Send: &FileAbility{MaxBytes: MaxDeclaredFileBytes + 1}}),
		Entry("an empty ability", &ConnectorFiles{}),
		Entry("a type that is no media type", &ConnectorFiles{Send: &FileAbility{MaxBytes: 1, Types: []string{"pdf"}}}),
		Entry("a repeated type", &ConnectorFiles{Send: &FileAbility{MaxBytes: 1, Types: []string{"image/*", "image/*"}}}),
		Entry("too many types", &ConnectorFiles{Send: &FileAbility{MaxBytes: 1, Types: manyTypes(MaxFileTypes + 1)}}),
	)

	It("reads files from JSON and refuses an unknown field in it", func() {
		var k ConnectorKind
		Expect(json.Unmarshal([]byte(`{"id":"x","title":"X","delivery":"pull","files":{"receive":{"maxBytes":10,"types":["text/plain"]}}}`), &k)).To(Succeed())
		Expect(k.Files.Receive.MaxBytes).To(Equal(int64(10)))
		Expect(k.Files.Send).To(BeNil())
		declare(&ConnectorFiles{Receive: &FileAbility{MaxBytes: 1}})
		raw, err := json.Marshal(m)
		Expect(err).NotTo(HaveOccurred())
		_, err = ParseManifest(raw)
		Expect(err).NotTo(HaveOccurred())
		bad := strings.Replace(string(raw), `"maxBytes":1`, `"maxBytes":1,"bogus":2`, 1)
		_, err = ParseManifest([]byte(bad))
		Expect(err).To(HaveOccurred())
	})
})

func manyTypes(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "text/x-" + strings.Repeat("a", i+1)
	}
	return out
}

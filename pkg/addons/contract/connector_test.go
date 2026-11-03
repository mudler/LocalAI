// SPDX-License-Identifier: MIT
package contract

import (
	"os"
	"strings"
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the connector capability", func() {
	var m Manifest
	BeforeEach(func() {
		b, err := os.ReadFile("../../../tests/addons/fixtures/contracts/page.json")
		Expect(err).NotTo(HaveOccurred())
		m, err = ParseManifest(b)
		Expect(err).NotTo(HaveOccurred())
		m.Capabilities.UI.SDK = VersionRange{Min: 1, Max: 2}
		m.Capabilities.UI.Pages = []string{"bot"}
		m.Capabilities.UI.Navigation = nil
		m.Capabilities.UI.MainPage = ""
		m.Capabilities.Connector = &ConnectorCapability{Kinds: []ConnectorKind{{ID: "telegram", Title: "Telegram", Delivery: DeliveryPull, Page: "bot"}}}
	})
	host := func() HostCompatibility {
		h := compatible()
		h.SDK = HostSDKVersion
		return h
	}

	It("accepts a pull kind with a declared page", func() {
		Expect(ValidateManifest(m, host())).To(Succeed())
	})
	It("accepts drafts and a preview interval within the bounds", func() {
		m.Capabilities.Connector.Kinds[0].Drafts = true
		m.Capabilities.Connector.Kinds[0].PreviewMs = MinPreviewMs
		Expect(ValidateManifest(m, host())).To(Succeed())
	})
	It("lets an add-on declare nothing but the connector", func() {
		m.Capabilities.UI = nil
		m.Capabilities.Connector.Kinds[0].Page = ""
		Expect(ValidateManifest(m, host())).To(Succeed())
	})
	Describe("the history ability", func() {
		kind := func() *ConnectorKind { return &m.Capabilities.Connector.Kinds[0] }
		BeforeEach(func() { m.API = VersionRange{Min: 1, Max: 2} })

		It("accepts a thread history with the manifest api 2", func() {
			kind().History = &ConnectorHistory{Thread: true, MaxMessages: 50}
			Expect(ValidateManifest(m, host())).To(Succeed())
			Expect(kind().ServesThreadHistory()).To(BeTrue())
		})
		It("refuses it under the manifest api 1", func() {
			m.API = VersionRange{Min: 1, Max: 1}
			kind().History = &ConnectorHistory{Thread: true}
			Expect(ValidateManifest(m, host())).NotTo(Succeed())
		})
		It("refuses an ability that says nothing and a limit out of bounds", func() {
			kind().History = &ConnectorHistory{}
			Expect(ValidateManifest(m, host())).NotTo(Succeed())
			kind().History = &ConnectorHistory{Thread: true, MaxMessages: MaxHistoryMessages + 1}
			Expect(ValidateManifest(m, host())).NotTo(Succeed())
			kind().History = &ConnectorHistory{Thread: true, MaxMessages: -1}
			Expect(ValidateManifest(m, host())).NotTo(Succeed())
		})
		It("serves nothing for a kind that declares nothing", func() {
			Expect(kind().ServesThreadHistory()).To(BeFalse())
		})
		It("cuts the limit to what the kind declares", func() {
			k := ConnectorKind{History: &ConnectorHistory{Thread: true, MaxMessages: 10}}
			Expect(k.HistoryLimit(0)).To(Equal(10))
			Expect(k.HistoryLimit(5)).To(Equal(5))
			Expect(k.HistoryLimit(500)).To(Equal(10))
			Expect(ConnectorKind{History: &ConnectorHistory{Thread: true}}.HistoryLimit(0)).To(Equal(DefaultHistoryMessages))
		})
	})
	DescribeTable("refuses",
		func(edit func(*ConnectorCapability)) {
			edit(m.Capabilities.Connector)
			Expect(ValidateManifest(m, host())).NotTo(Succeed())
		},
		Entry("no kind", func(c *ConnectorCapability) { c.Kinds = nil }),
		Entry("a bad kind id", func(c *ConnectorCapability) { c.Kinds[0].ID = "Tele gram" }),
		Entry("a repeated kind", func(c *ConnectorCapability) { c.Kinds = append(c.Kinds, c.Kinds[0]) }),
		Entry("markup in the title", func(c *ConnectorCapability) { c.Kinds[0].Title = "<b>x</b>" }),
		Entry("an unknown delivery", func(c *ConnectorCapability) { c.Kinds[0].Delivery = "webhook" }),
		Entry("a page the UI does not declare", func(c *ConnectorCapability) { c.Kinds[0].Page = "other" }),
		Entry("a preview interval too short", func(c *ConnectorCapability) { c.Kinds[0].PreviewMs = MinPreviewMs - 1 }),
		Entry("a preview interval too long", func(c *ConnectorCapability) { c.Kinds[0].PreviewMs = MaxPreviewMs + 1 }),
	)
})

var _ = DescribeTable("SafeFileName",
	func(in, want string) { Expect(SafeFileName(in)).To(Equal(want)) },
	Entry("a plain name", "report.pdf", "report.pdf"),
	Entry("a path keeps its last element", "/home/u/out/chart.png", "chart.png"),
	Entry("a Windows path", `C:\tmp\a.txt`, "a.txt"),
	Entry("control characters and quotes", "a\x00b\"c\n.txt", "a_b_c_.txt"),
	Entry("dots only", "..", "file"),
	Entry("empty", "", "file"),
	Entry("a hidden name loses its leading dot", ".env", "env"),
	Entry("a long name keeps its extension", strings.Repeat("é", 100)+".csv", strings.Repeat("é", 62)+".csv"),
)

var _ = It("cuts a long name on a character boundary", func() {
	got := SafeFileName("a" + strings.Repeat("é", 100) + ".csv")
	Expect(len(got)).To(BeNumerically("<=", MaxConnectorFileName))
	Expect(utf8.ValidString(got)).To(BeTrue())
	Expect(got).To(HaveSuffix(".csv"))
})

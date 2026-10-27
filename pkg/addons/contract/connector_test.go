// SPDX-License-Identifier: MIT
package contract

import (
	"os"

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
	It("lets an add-on declare nothing but the connector", func() {
		m.Capabilities.UI = nil
		m.Capabilities.Connector.Kinds[0].Page = ""
		Expect(ValidateManifest(m, host())).To(Succeed())
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
	)
})

// SPDX-License-Identifier: MIT
package contract

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the roles and permissions of a connector kind", func() {
	var m Manifest
	BeforeEach(func() {
		b, err := os.ReadFile("../../../tests/addons/fixtures/contracts/page.json")
		Expect(err).NotTo(HaveOccurred())
		m, err = ParseManifest(b)
		Expect(err).NotTo(HaveOccurred())
		m.API = VersionRange{Min: 2, Max: 2}
		m.Capabilities.UI = nil
		m.Capabilities.Connector = &ConnectorCapability{Kinds: []ConnectorKind{{ID: "chat", Title: "Chat", Delivery: DeliveryPull}}}
	})
	host := func() HostCompatibility {
		h := compatible()
		h.SDK = HostSDKVersion
		return h
	}
	kind := func() *ConnectorKind { return &m.Capabilities.Connector.Kinds[0] }

	It("accepts roles and known permissions", func() {
		kind().Roles = []ConnectorRole{{ID: "admin", Title: "Admin", Description: "Workspace admin"}}
		kind().Permissions = []string{CapProposalsConfirm, CapMemoryWrite, CapProfileWrite}
		Expect(ValidateManifest(m, host())).To(Succeed())
	})

	It("refuses an unknown or repeated permission", func() {
		kind().Permissions = []string{"root.everything"}
		Expect(ValidateManifest(m, host())).NotTo(Succeed())
		kind().Permissions = []string{CapMemoryWrite, CapMemoryWrite}
		Expect(ValidateManifest(m, host())).NotTo(Succeed())
	})

	It("refuses too many roles or permissions, a bad role ID and a repeated role", func() {
		kind().Roles = nil
		for i := 0; i <= MaxConnectorRoles; i++ {
			kind().Roles = append(kind().Roles, ConnectorRole{ID: "r" + string(rune('a'+i)), Title: "R"})
		}
		Expect(ValidateManifest(m, host())).NotTo(Succeed())
		kind().Roles = []ConnectorRole{{ID: "Bad Id", Title: "R"}}
		Expect(ValidateManifest(m, host())).NotTo(Succeed())
		kind().Roles = []ConnectorRole{{ID: "a", Title: "A"}, {ID: "a", Title: "B"}}
		Expect(ValidateManifest(m, host())).NotTo(Succeed())
		kind().Roles = nil
		kind().Permissions = ConnectorCapabilityNames()
		Expect(ValidateManifest(m, host())).To(Succeed()) // the whole list fits
		kind().Permissions = append(kind().Permissions, "x.1", "x.2")
		Expect(ValidateManifest(m, host())).NotTo(Succeed())
	})

	It("describes every capability, and never makes a sensitive one usual", func() {
		list := ConnectorCapabilities()
		Expect(list).To(HaveLen(len(ConnectorCapabilityNames())))
		usual := 0
		for _, c := range list {
			Expect(c.Title).NotTo(BeEmpty(), c.Name)
			Expect(c.Description).NotTo(BeEmpty(), c.Name)
			Expect(c.Sensitive).To(Equal(SensitiveCapability(c.Name)))
			if c.Usual {
				usual++
				Expect(c.Sensitive).To(BeFalse(), c.Name)
			}
		}
		Expect(usual).To(BeNumerically(">", 0))
		Expect(capabilityText).To(HaveLen(len(capabilities)))
	})

	It("never grants a sensitive capability to a role", func() {
		kind().Permissions = []string{CapProfileWrite, CapMemoryWrite}
		Expect(kind().Grantable(CapProfileWrite)).To(BeFalse())
		Expect(kind().Grantable(CapMemoryWrite)).To(BeTrue())
		Expect(kind().Grantable(CapFilesUpload)).To(BeFalse(), "not listed by the kind")
	})

	It("keeps only declared, distinct roles of a message", func() {
		kind().Roles = []ConnectorRole{{ID: "admin", Title: "Admin"}}
		Expect(kind().FilterRoles([]string{"admin", "root", "admin"})).To(Equal([]string{"admin"}))
		Expect((ConnectorKind{}).FilterRoles([]string{"admin"})).To(BeEmpty())
	})
})

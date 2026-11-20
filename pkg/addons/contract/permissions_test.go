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

	It("never grants a loop capability to a role, even one the kind lists", func() {
		// A kind may list them (validation accepts any known name), but they
		// belong to the binding alone, like the switches of the settings page.
		kind().Permissions = []string{CapLoopsWait, CapLoopsStart, CapMemoryWrite}
		Expect(ValidateManifest(m, host())).To(Succeed())
		Expect(kind().Grantable(CapLoopsWait)).To(BeFalse())
		Expect(kind().Grantable(CapLoopsStart)).To(BeFalse())
		Expect(kind().Grantable(CapMemoryWrite)).To(BeTrue())
		Expect(BindingOnlyCapability(CapLoopsWait)).To(BeTrue())
		Expect(BindingOnlyCapability(CapLoopsStart)).To(BeTrue())
		Expect(BindingOnlyCapability(CapMemoryWrite)).To(BeFalse())
	})

	It("marks the loop capabilities binding-only, keeps waits usual and timers sensitive", func() {
		for _, c := range ConnectorCapabilities() {
			Expect(c.BindingOnly).To(Equal(BindingOnlyCapability(c.Name)), c.Name)
			switch c.Name {
			case CapLoopsWait:
				Expect([]bool{c.Usual, c.Sensitive}).To(Equal([]bool{true, false}))
				Expect(c.Description).To(ContainSubstring("command"), "the text says that a wait on a command needs a verified person")
			case CapLoopsStart:
				Expect([]bool{c.Usual, c.Sensitive}).To(Equal([]bool{false, true}))
				Expect(c.Description).To(ContainSubstring("fifth"), "the text says how much sooner the loop ends")
			}
		}
	})

	It("keeps only declared, distinct roles of a message", func() {
		kind().Roles = []ConnectorRole{{ID: "admin", Title: "Admin"}}
		Expect(kind().FilterRoles([]string{"admin", "root", "admin"})).To(Equal([]string{"admin"}))
		Expect((ConnectorKind{}).FilterRoles([]string{"admin"})).To(BeEmpty())
	})
})

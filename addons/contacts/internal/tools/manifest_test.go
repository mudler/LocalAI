// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/contacts/internal/store"
	"github.com/mudler/LocalAI/addons/contacts/internal/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The manifest is what the host reads to decide who may call which tool in a
// messaging chat. These specs hold the contacts manifest to the sender rule:
// the owner and paired senders change the book, every other sender reads it.
var _ = Describe("contacts manifest", func() {
	var m contract.Manifest
	BeforeEach(func() {
		raw, err := os.ReadFile("../../manifest.json")
		Expect(err).NotTo(HaveOccurred())
		m, err = contract.ParseManifest(raw)
		Expect(err).NotTo(HaveOccurred())
	})

	It("keeps the owner's own data, with no credential and no resource list", func() {
		c := m.Capabilities
		Expect(c.OwnerData).To(BeTrue())
		Expect(c.Actions).To(BeTrue())
		Expect(c.Resources).To(BeNil())
		Expect(m.FeatureLevel).To(BeNumerically(">=", 3))
	})

	It("holds every tool that changes the book to tools.write and leaves the reads open", func() {
		writes := map[string]bool{tools.ToolContactCreate: true, tools.ToolContactUpdate: true, tools.ToolContactDelete: true}
		offered := 0
		for _, d := range m.Capabilities.ToolDefinitions {
			if d.UIOnly {
				// The page calls these as the signed-in person; no model or
				// chat is ever offered them.
				Expect(d.Kind).To(Equal(contract.ToolKindRead), d.Name)
				continue
			}
			offered++
			got := contract.ToolPermission(m.Capabilities.OwnerData, d)
			if writes[d.Name] {
				Expect(d.Kind).To(Equal(contract.ToolKindAction), d.Name)
				Expect(got).To(Equal(contract.CapToolsWrite), d.Name)
			} else {
				Expect(d.Kind).To(Equal(contract.ToolKindRead), d.Name)
				Expect(got).To(BeEmpty(), d.Name)
			}
		}
		Expect(offered).To(Equal(5))
	})

	It("names no resource in any tool", func() {
		for _, d := range m.Capabilities.ToolDefinitions {
			Expect(string(d.InputSchema)).NotTo(ContainSubstring("resourceId"), d.Name)
		}
	})

	It("declares exactly the tools the runtime serves", func() {
		st, err := store.Open(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = st.Close() })
		rt := tools.New(st)
		for _, name := range m.Capabilities.Tools {
			args := map[string]any{"tool": name}
			var err error
			if d := definition(m, name); d.Kind == contract.ToolKindAction {
				_, err = rt.Act(context.Background(), env("ann", "act", "act", args))
			} else {
				_, err = rt.Read(context.Background(), env("ann", "read", "read", args))
			}
			Expect(err).NotTo(HaveOccurred(), "%s is declared but not served", name)
		}
	})
})

func definition(m contract.Manifest, name string) contract.ToolDefinition {
	for _, d := range m.Capabilities.ToolDefinitions {
		if d.Name == name {
			return d
		}
	}
	Fail("no definition for " + name)
	return contract.ToolDefinition{}
}

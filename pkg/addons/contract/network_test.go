// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("The network capability", func() {
	const bashSchema = `{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`

	// withNetwork is a manifest that asks for the network-policy
	// control. It runs no command tool unless shadowsBash is set, so what is
	// drawn can only come from the declared capability.
	withNetwork := func(n *NetworkCapability, shadowsBash bool) Manifest {
		m, err := ParseManifest(fixture())
		Expect(err).NotTo(HaveOccurred())
		m.API = VersionRange{Min: 2, Max: 2}
		m.Capabilities = Capabilities{
			Service:        true,
			Network:        n,
			HostOperations: &HostOperations{Optional: []string{"egress.check"}},
			UI:             &UI{Entry: "ui/main.js", SDK: VersionRange{Min: 2, Max: 2}, Controls: []string{ControlNetworkPolicy}},
		}
		if shadowsBash {
			m.Capabilities.Actions = true
			m.Capabilities.Tools = []string{"run"}
			m.Capabilities.ToolDefinitions = []ToolDefinition{{Name: "run", Description: "Run a command.", Kind: ToolKindAction,
				InputSchema: json.RawMessage(bashSchema), Shadows: "bash"}}
		}
		return m
	}
	host := func() HostCompatibility {
		h := compatible()
		h.SDK = HostSDKVersion
		return h
	}
	declared := &NetworkCapability{Kind: NetworkKindEgressCheck, Title: "Runbox network",
		Presets: []NetworkPreset{{ID: "off", Title: "Closed"}, {ID: "all", Title: "Open"}}}

	It("is valid when the manifest lists the egress check", func() {
		Expect(ValidateManifest(withNetwork(declared, false), host())).To(Succeed())
	})

	It("needs the egress check among the host operations", func() {
		m := withNetwork(declared, false)
		m.Capabilities.HostOperations = nil
		Expect(ValidateManifest(m, host())).To(MatchError(ContainSubstring("egress.check")))
		m.Capabilities.HostOperations = &HostOperations{Required: []string{"egress.check"}}
		Expect(ValidateManifest(m, host())).To(Succeed())
	})

	It("refuses an unknown kind, a bad title and a bad or repeated preset", func() {
		for _, bad := range []*NetworkCapability{
			{Kind: "firewall"},
			{Kind: ""},
			{Kind: NetworkKindEgressCheck, Title: "<b>Runbox</b>"},
			{Kind: NetworkKindEgressCheck, Title: strings.Repeat("x", MaxNetworkTitleChars+1)},
			{Kind: NetworkKindEgressCheck, Presets: []NetworkPreset{{ID: "everything", Title: "All"}}},
			{Kind: NetworkKindEgressCheck, Presets: []NetworkPreset{{ID: "off", Title: "A"}, {ID: "off", Title: "B"}}},
			{Kind: NetworkKindEgressCheck, Presets: []NetworkPreset{{ID: "off"}}},
		} {
			Expect(ValidateManifest(withNetwork(bad, false), host())).NotTo(Succeed(), "%+v", bad)
		}
	})

	It("reads capabilities.network from JSON and refuses an unknown field in it", func() {
		var c Capabilities
		Expect(json.Unmarshal([]byte(`{"service":true,"network":{"kind":"egress-check","title":"Runbox network","presets":[{"id":"off","title":"Closed"}]}}`), &c)).To(Succeed())
		Expect(c.Network).To(Equal(&NetworkCapability{Kind: NetworkKindEgressCheck, Title: "Runbox network", Presets: []NetworkPreset{{ID: "off", Title: "Closed"}}}))
		Expect(json.Unmarshal([]byte(`{"service":true,"network":{"kind":"egress-check","proxy":true}}`), &c)).NotTo(Succeed())
	})

	Describe("the network-policy control", func() {
		grants := []string{"egress."}

		It("is drawn for a declared capability that holds the grant, with no bash tool", func() {
			Expect(EntitledControls(withNetwork(declared, false), grants)).To(Equal([]string{ControlNetworkPolicy}))
		})
		It("is not drawn for a declared capability without the grant", func() {
			Expect(EntitledControls(withNetwork(declared, false), []string{"workspace."})).To(BeEmpty())
			Expect(EntitledControls(withNetwork(declared, true), nil)).To(BeEmpty())
		})
		It("is not drawn for a manifest that declares no network, whether or not it replaces bash", func() {
			Expect(EntitledControls(withNetwork(nil, true), grants)).To(BeEmpty())
			Expect(EntitledControls(withNetwork(nil, false), grants)).To(BeEmpty())
		})
		It("does not read the add-on ID", func() {
			a, b := withNetwork(declared, false), withNetwork(declared, false)
			a.ID, b.ID = "alpha", "beta"
			Expect(EntitledControls(a, grants)).To(Equal(EntitledControls(b, grants)))
		})
	})

	Describe("NetworkTitles", func() {
		It("gives the declared title of a preset and the host title for the others", func() {
			Expect(declared.PresetTitle("off", "Off")).To(Equal("Closed"))
			Expect(declared.PresetTitle("registries", "Package registries")).To(Equal("Package registries"))
			var none *NetworkCapability
			Expect(none.PresetTitle("all", "All public sites")).To(Equal("All public sites"))
		})
	})

	Describe("the shipped Sandbox manifest", func() {
		load := func() Manifest {
			b, err := os.ReadFile("../../../addons/sandbox/manifest.json")
			Expect(err).NotTo(HaveOccurred())
			m, err := ParseManifest(b)
			Expect(err).NotTo(HaveOccurred())
			return m
		}
		It("declares its network and keeps the control it asked for", func() {
			m := load()
			Expect(m.Capabilities.Network).To(Equal(&NetworkCapability{Kind: NetworkKindEgressCheck, Title: "Sandbox"}))
			Expect(EntitledControls(m, []string{"egress."})).To(ContainElement(ControlNetworkPolicy))
		})
		It("loses the control when it declares no network", func() {
			m := load()
			m.Capabilities.Network = nil
			Expect(EntitledControls(m, []string{"egress."})).NotTo(ContainElement(ControlNetworkPolicy))
		})
	})
})

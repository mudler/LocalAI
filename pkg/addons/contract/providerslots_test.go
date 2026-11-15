// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("The provider slot declaration", func() {
	hostWithSDK := func() HostCompatibility {
		h := compatible()
		h.SDK = HostSDKVersion
		return h
	}
	schema := json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"additionalProperties":false}`)
	// A write tool names the resource it changes, as the contract requires.
	withResource := json.RawMessage(`{"type":"object","properties":{"resourceId":{"type":"string"},"text":{"type":"string"}},"required":["resourceId"],"additionalProperties":false}`)
	tool := func(name, kind string) ToolDefinition {
		in := schema
		if kind == ToolKindWrite {
			in = withResource
		}
		return ToolDefinition{Name: name, Description: "does " + name, Kind: kind, InputSchema: in}
	}
	// declareNetwork gives a manifest the network access a remote provider owes.
	declareNetwork := func(m *Manifest) {
		m.Capabilities.Network = &NetworkCapability{Kind: NetworkKindEgressCheck, Title: "Notes service"}
		m.Capabilities.HostOperations = &HostOperations{Optional: []string{"egress.check"}}
		m.Capabilities.UI = &UI{Entry: "ui/main.js", SDK: VersionRange{Min: 2, Max: 2}, Controls: []string{ControlNetworkPolicy}}
	}
	// provider is a manifest that declares the given provider entries and the
	// tools they name, on top of the fixture.
	provider := func(d ProviderDeclaration) Manifest {
		m, err := ParseManifest(fixture())
		Expect(err).NotTo(HaveOccurred())
		m.Capabilities.Tools = append(m.Capabilities.Tools, "recall", "remember", "export-notes")
		m.Capabilities.ToolDefinitions = append(m.Capabilities.ToolDefinitions,
			tool("recall", ToolKindRead), tool("remember", ToolKindWrite), tool("export-notes", ToolKindRead))
		m.Capabilities.Provides = []ProviderDeclaration{d}
		if remote, _ := d.Properties["remote"].(bool); remote {
			declareNetwork(&m)
		}
		return m
	}
	good := func() ProviderDeclaration {
		return ProviderDeclaration{Slot: "memory", SlotVersion: 1,
			Roles:      map[string]ProviderTools{"read": {"recall"}, "write": {"remember"}, "export": {"export-notes"}},
			Properties: map[string]any{"remote": true, "undo": "none"}}
	}

	It("reads the slots of the host from data and keeps them closed", func() {
		slot, ok := ProviderSlotByID("memory")
		Expect(ok).To(BeTrue())
		Expect(slot.Selection.OffAllowed).To(BeTrue())
		Expect(slot.Builtin.ID).To(Equal(ProviderBuiltin))
		Expect(slot.RoleNames()).To(ConsistOf("context", "read", "write", "export", "forget"))
		Expect(ProviderSlots()).NotTo(BeEmpty())
		_, ok = ProviderSlotByID("calendar")
		Expect(ok).To(BeFalse())
		// A caller cannot change the table.
		slot.Roles["read"] = ProviderRole{}
		again, _ := ProviderSlotByID("memory")
		Expect(again.Roles["read"].Kinds).To(Equal([]string{ToolKindRead}))
	})

	It("accepts an add-on whose roles name its own tools of the right kind", func() {
		m := provider(good())
		Expect(ValidateManifest(m, hostWithSDK())).To(Succeed())
		Expect(m.Capabilities.ProvidesSlot("memory")).To(BeTrue())
		Expect(m.Capabilities.ProvidedSlots()).To(Equal([]string{"memory"}))
	})

	It("accepts a declaration with no roles, as a file-based provider has none", func() {
		Expect(ValidateManifest(provider(ProviderDeclaration{Slot: "memory", SlotVersion: 1}), hostWithSDK())).To(Succeed())
	})

	It("reads one tool as a string and several as a list, and refuses anything else", func() {
		var d ProviderDeclaration
		Expect(json.Unmarshal([]byte(`{"slot":"memory","slotVersion":1,"roles":{"read":"recall","write":["remember"]}}`), &d)).To(Succeed())
		Expect(d.Roles["read"]).To(Equal(ProviderTools{"recall"}))
		Expect(d.Roles["write"]).To(Equal(ProviderTools{"remember"}))
		Expect(json.Unmarshal([]byte(`{"slot":"memory","roles":{"read":5}}`), &d)).NotTo(Succeed())
	})

	It("is parsed strictly from a manifest file", func() {
		m := provider(good())
		raw, err := json.Marshal(m)
		Expect(err).NotTo(HaveOccurred())
		var generic map[string]any
		Expect(json.Unmarshal(raw, &generic)).To(Succeed())
		generic["capabilities"].(map[string]any)["provides"].([]any)[0].(map[string]any)["extra"] = 1
		bad, _ := json.Marshal(generic)
		_, err = ParseManifest(bad)
		Expect(err).To(HaveOccurred(), "an unknown field of a declaration fails")
	})

	It("refuses what the descriptor does not allow", func() {
		for name, edit := range map[string]func(*ProviderDeclaration){
			"an unknown slot":              func(d *ProviderDeclaration) { d.Slot = "calendar" },
			"a version from the future":    func(d *ProviderDeclaration) { d.SlotVersion = 2 },
			"no version":                   func(d *ProviderDeclaration) { d.SlotVersion = 0 },
			"an unknown role":              func(d *ProviderDeclaration) { d.Roles["sync"] = ProviderTools{"recall"} },
			"a tool that is not declared":  func(d *ProviderDeclaration) { d.Roles["read"] = ProviderTools{"missing"} },
			"a write tool in a read role":  func(d *ProviderDeclaration) { d.Roles["read"] = ProviderTools{"remember"} },
			"a read tool in a write role":  func(d *ProviderDeclaration) { d.Roles["write"] = ProviderTools{"recall"} },
			"too many tools in a role":     func(d *ProviderDeclaration) { d.Roles["export"] = ProviderTools{"export-notes", "recall"} },
			"a role with no tool":          func(d *ProviderDeclaration) { d.Roles["read"] = ProviderTools{} },
			"one tool in two roles":        func(d *ProviderDeclaration) { d.Roles["export"] = ProviderTools{"recall"} },
			"an unknown property":          func(d *ProviderDeclaration) { d.Properties["colour"] = true },
			"a property of the wrong type": func(d *ProviderDeclaration) { d.Properties["remote"] = "yes" },
			"a value not in the list":      func(d *ProviderDeclaration) { d.Properties["undo"] = "maybe" },
			"undo by the add-on without the undo capability": func(d *ProviderDeclaration) {
				d.Properties["undo"] = "provider"
			},
		} {
			d := good()
			edit(&d)
			Expect(ValidateManifest(provider(d), hostWithSDK())).NotTo(Succeed(), name)
		}
	})

	It("refuses a remote provider that does not declare its network access", func() {
		m := provider(good())
		Expect(ValidateManifest(m, hostWithSDK())).To(Succeed())
		m.Capabilities.Network = nil
		err := ValidateManifest(m, hostWithSDK())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("network"))
		// A provider that keeps everything on this machine owes no declaration.
		d := good()
		d.Properties["remote"] = false
		local := provider(d)
		Expect(local.Capabilities.Network).To(BeNil())
		Expect(ValidateManifest(local, hostWithSDK())).To(Succeed())
	})

	It("keeps an alias of the built-in provider out of the providers and maps it to the built-in", func() {
		slot, _ := ProviderSlotByID("memory")
		Expect(slot.Aliases).NotTo(BeEmpty())
		Expect(slot.IsAlias(slot.Aliases[0])).To(BeTrue())
		Expect(slot.Canonical(slot.Aliases[0])).To(Equal(ProviderBuiltin))
		Expect(slot.Canonical("other-notes")).To(Equal("other-notes"))
		Expect(slot.Canonical(ProviderOff)).To(Equal(ProviderOff))
	})

	It("refuses a tool that only the add-on's page may call", func() {
		m := provider(good())
		for i := range m.Capabilities.ToolDefinitions {
			if m.Capabilities.ToolDefinitions[i].Name == "recall" {
				m.Capabilities.ToolDefinitions[i].UIOnly = true
			}
		}
		Expect(ValidateManifest(m, hostWithSDK())).NotTo(Succeed())
	})

	It("accepts undo by the add-on when it keeps change sets", func() {
		m := provider(good())
		m.Capabilities.Provides[0].Properties["undo"] = "provider"
		m.Capabilities.Undo = true
		Expect(ValidateManifest(m, hostWithSDK())).To(Succeed())
	})

	It("refuses a repeated slot and more slots than the bound", func() {
		m := provider(good())
		m.Capabilities.Provides = append(m.Capabilities.Provides, ProviderDeclaration{Slot: "memory", SlotVersion: 1})
		Expect(ValidateManifest(m, hostWithSDK())).NotTo(Succeed())
		m.Capabilities.Provides = nil
		for range MaxProvides + 1 {
			m.Capabilities.Provides = append(m.Capabilities.Provides, ProviderDeclaration{Slot: "memory", SlotVersion: 1})
		}
		Expect(ValidateManifest(m, hostWithSDK())).NotTo(Succeed())
	})

	It("limits the context files to the scopes the slot allows", func() {
		m := provider(ProviderDeclaration{Slot: "memory", SlotVersion: 1})
		m.Capabilities.ContextFiles = []ContextFile{{Scope: ScopeUser, Path: ContextFilesRoot + "index.md"}}
		Expect(ValidateManifest(m, hostWithSDK())).To(Succeed())
		m.Capabilities.ContextFiles = []ContextFile{{Scope: ChangeScope("team"), Path: ContextFilesRoot + "index.md"}}
		Expect(ValidateManifest(m, hostWithSDK())).NotTo(Succeed())
	})

	It("keeps api 2 as the only manifest api", func() {
		m := provider(good())
		m.API = VersionRange{Min: 1, Max: 1}
		err := ValidateManifest(m, hostWithSDK())
		Expect(err).To(HaveOccurred())
		Expect(strings.ToLower(err.Error())).To(ContainSubstring("api"))
	})
})

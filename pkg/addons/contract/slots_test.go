// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Slots served by skills", func() {
	host := func() HostCompatibility {
		h := compatible()
		h.SDK = 0
		h.API, h.APIMin = HostAPIVersion, HostAPIMin
		return h
	}
	manifestWith := func(api VersionRange, defs ...SkillDefinition) Manifest {
		m, err := ParseManifest(fixture())
		Expect(err).NotTo(HaveOccurred())
		m.API = api
		m.Capabilities.SkillDefinitions = defs
		return m
	}
	odt := SkillDefinition{Name: "odt", Shadows: "odt", Slot: &SlotDefinition{Kind: SlotKindDocuments, Extensions: []string{".odt"}, Words: []string{"odt", "opendocument"}, Library: "odfpy"}}

	It("reads the default slots from data", func() {
		Expect(ReplaceableSkills).To(ConsistOf("xlsx", "docx", "pptx", "pdf"))
		s, ok := DefaultSlot("docx")
		Expect(ok).To(BeTrue())
		Expect(s.Extensions).To(Equal([]string{".docx"}))
		Expect(s.Library).To(Equal("python-docx"))
	})

	It("lets an add-on declare a slot the contract has never heard of", func() {
		Expect(ValidateManifest(manifestWith(VersionRange{Min: 1, Max: 2}, odt), host())).To(Succeed())
		Expect(ShadowedSkills([]SkillDefinition{odt})).To(Equal([]string{"odt"}))
		Expect(SlotsOf([]SkillDefinition{odt})[0].Library).To(Equal("odfpy"))
		Expect(IsolatedSkills([]ToolDefinition{{Name: "run", Kind: ToolKindAction, Shadows: "bash", InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`)}}, []SkillDefinition{odt})).To(Equal([]string{"odt"}))
	})

	It("needs the manifest to accept API 2 to declare a slot", func() {
		Expect(ValidateManifest(manifestWith(VersionRange{Min: 1, Max: 1}, odt), host())).NotTo(Succeed())
	})

	It("keeps a skill that shadows a default slot and declares nothing working", func() {
		d := SkillDefinition{Name: "docx", Shadows: "docx"}
		Expect(ValidateManifest(manifestWith(VersionRange{Min: 1, Max: 1}, d), host())).To(Succeed())
		Expect(SlotsOf([]SkillDefinition{d})[0].Library).To(Equal("python-docx"))
	})

	It("refuses a shadow of an unknown slot without a declaration, and bad declarations", func() {
		Expect(validateSkillDefinitions("sample", []SkillDefinition{{Name: "odt", Shadows: "odt"}})).NotTo(Succeed())
		Expect(validateSkillDefinitions("sample", []SkillDefinition{{Name: "sample", Slot: odt.Slot}})).NotTo(Succeed())
		Expect(validateSkillDefinitions("sample", []SkillDefinition{{Name: "writing-skills", Shadows: "writing-skills", Slot: odt.Slot}})).NotTo(Succeed())
		for _, bad := range []*SlotDefinition{
			{Kind: "games"},
			{Kind: SlotKindDocuments, Extensions: []string{"odt"}},
			{Kind: SlotKindDocuments, Extensions: []string{".ODT"}},
			{Kind: SlotKindDocuments, Words: []string{"<b>"}},
			{Kind: SlotKindDocuments, Library: "<x>"},
		} {
			Expect(validateSkillDefinitions("sample", []SkillDefinition{{Name: "odt", Shadows: "odt", Slot: bad}})).NotTo(Succeed())
		}
	})
})

var _ = Describe("Legacy arguments of a tool", func() {
	const schema = `{"type":"object","properties":{"path":{"type":"string"},"markdown":{"type":"string"}},"required":["path"],"additionalProperties":false}`
	def := func(l ...LegacyArgument) ToolDefinition {
		return ToolDefinition{Name: "create", Description: "Create.", Kind: ToolKindCreate, InputSchema: json.RawMessage(schema), LegacyArgs: l}
	}
	blocks := LegacyArgument{Name: "blocks", Hint: "use the markdown string"}

	It("answers with the hint when a call carries the old argument", func() {
		Expect(LegacyMessage(def(blocks), json.RawMessage(`{"path":"/a","blocks":[1]}`))).To(Equal("blocks is no longer supported; use the markdown string"))
	})
	It("says nothing without the argument, without a declaration or for a forbidden property", func() {
		Expect(LegacyMessage(def(blocks), json.RawMessage(`{"path":"/a"}`))).To(BeEmpty())
		Expect(LegacyMessage(def(), json.RawMessage(`{"blocks":1}`))).To(BeEmpty())
		Expect(LegacyMessage(def(blocks), json.RawMessage(`{"blocks":1,"approved":true}`))).To(BeEmpty())
		Expect(LegacyMessage(def(blocks), json.RawMessage(`nope`))).To(BeEmpty())
	})
	It("is validated: bounded, plain, not in the schema, and API 2", func() {
		Expect(validateLegacyArgs(def(blocks))).To(Succeed())
		Expect(validateLegacyArgs(def(LegacyArgument{Name: "markdown", Hint: "x"}))).NotTo(Succeed())
		Expect(validateLegacyArgs(def(LegacyArgument{Name: "blocks", Hint: "<b>"}))).NotTo(Succeed())
		Expect(validateLegacyArgs(def(blocks, blocks))).NotTo(Succeed())
		m, err := ParseManifest(fixture())
		Expect(err).NotTo(HaveOccurred())
		h := compatible()
		h.SDK = 0
		h.API, h.APIMin = HostAPIVersion, HostAPIMin
		m.Capabilities.Tools = []string{"create"}
		m.Capabilities.ToolDefinitions = []ToolDefinition{def(blocks)}
		m.API = VersionRange{Min: 1, Max: 1}
		Expect(ValidateManifest(m, h)).NotTo(Succeed())
		m.API = VersionRange{Min: 1, Max: 2}
		Expect(ValidateManifest(m, h)).To(Succeed())
	})
})

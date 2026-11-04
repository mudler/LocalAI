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
		return h
	}
	manifestWith := func(defs ...SkillDefinition) Manifest {
		m, err := ParseManifest(fixture())
		Expect(err).NotTo(HaveOccurred())
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
		Expect(ValidateManifest(manifestWith(odt), host())).To(Succeed())
		Expect(ShadowedSkills([]SkillDefinition{odt})).To(Equal([]string{"odt"}))
		Expect(SlotsOf([]SkillDefinition{odt})[0].Library).To(Equal("odfpy"))
		Expect(IsolatedSkills([]ToolDefinition{{Name: "run", Kind: ToolKindAction, Shadows: "bash", InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`)}}, []SkillDefinition{odt})).To(Equal([]string{"odt"}))
	})

	It("keeps a skill that shadows a default slot and declares nothing working", func() {
		d := SkillDefinition{Name: "docx", Shadows: "docx"}
		Expect(ValidateManifest(manifestWith(d), host())).To(Succeed())
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

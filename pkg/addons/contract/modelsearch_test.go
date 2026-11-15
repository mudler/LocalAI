// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"
	"fmt"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("The model-search provider slot", func() {
	host := func() HostCompatibility {
		h := compatible()
		h.SDK = HostSDKVersion
		return h
	}
	// source is a manifest of a sample add-on that provides the slot.
	source := func() Manifest {
		m, err := ParseManifest(fixture())
		Expect(err).NotTo(HaveOccurred())
		m.FeatureLevel = 9
		m.Capabilities.Tools = append(m.Capabilities.Tools, "search")
		m.Capabilities.ToolDefinitions = append(m.Capabilities.ToolDefinitions, ToolDefinition{
			Name: "search", Description: "search models", Kind: ToolKindRead,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}},"additionalProperties":false}`)})
		m.Capabilities.Provides = []ProviderDeclaration{{Slot: SlotModelSearch, SlotVersion: 1, Roles: map[string]ProviderTools{RoleSearch: {"search"}}}}
		m.Capabilities.ModelSearch = &ModelSearch{
			Source: ModelSearchSource{ID: "sample", Title: "Sample"},
			Sorts:  []string{"downloads", "likes"},
			Filters: []SearchFilter{
				{ID: "kind", Type: FilterEnum, Label: "Kind", Values: []string{"a", "b"}, Default: "a"},
				{ID: "fresh", Type: FilterBool, Label: "Fresh"},
				{ID: "seeds", Type: FilterRange, Label: "Seeds", Min: 0, Max: 100, Step: 1, Unit: "seeds"},
				{ID: "author", Type: FilterText, Label: "Author", MaxLength: 40},
			},
		}
		return m
	}

	It("is a set slot with Hugging Face as its built-in source", func() {
		slot, ok := ProviderSlotByID(SlotModelSearch)
		Expect(ok).To(BeTrue())
		Expect(slot.IsSet()).To(BeTrue())
		Expect(slot.Selection.Default).To(Equal(ProviderBuiltin))
		Expect(slot.Selection.OffAllowed).To(BeFalse())
		Expect(slot.Roles[RoleSearch].Required).To(BeTrue())
		mem, _ := ProviderSlotByID("memory")
		Expect(mem.IsSet()).To(BeFalse())
	})

	It("accepts a source with every filter type", func() {
		Expect(ValidateManifest(source(), host())).To(Succeed())
	})

	It("needs feature level 9 and a host that has it", func() {
		m := source()
		m.FeatureLevel = 8
		Expect(ValidateManifest(m, host())).To(MatchError(ContainSubstring("must declare featureLevel 9")))
		m.FeatureLevel = 9
		h := host()
		h.FeatureLevel = 8
		Expect(ValidateManifest(m, h)).To(MatchError("needs a newer LocalAI"))
	})

	DescribeTable("refuses a bad declaration",
		func(edit func(*Manifest), text string) {
			m := source()
			edit(&m)
			Expect(ValidateManifest(m, host())).To(MatchError(ContainSubstring(text)))
		},
		Entry("unknown filter type", func(m *Manifest) { m.Capabilities.ModelSearch.Filters[1].Type = "slider" }, "unknown type"),
		Entry("too many filters", func(m *Manifest) {
			m.Capabilities.ModelSearch.Filters = nil
			for i := range MaxSearchFilters + 1 {
				m.Capabilities.ModelSearch.Filters = append(m.Capabilities.ModelSearch.Filters, SearchFilter{ID: fmt.Sprintf("f%d", i), Type: FilterBool, Label: "F"})
			}
		}, "at most 8 filters"),
		Entry("too many enum values", func(m *Manifest) {
			f := &m.Capabilities.ModelSearch.Filters[0]
			f.Values = nil
			for i := range MaxFilterValues + 1 {
				f.Values = append(f.Values, fmt.Sprintf("v%d", i))
			}
			f.Default = "v0"
		}, "1 to 32 values"),
		Entry("duplicate filter id", func(m *Manifest) { m.Capabilities.ModelSearch.Filters[1].ID = "kind" }, "declared twice"),
		Entry("enum default outside its values", func(m *Manifest) { m.Capabilities.ModelSearch.Filters[0].Default = "z" }, "default"),
		Entry("range with no span", func(m *Manifest) { m.Capabilities.ModelSearch.Filters[2].Max = 0 }, "invalid range"),
		Entry("filter id that no tool argument may use", func(m *Manifest) { m.Capabilities.ModelSearch.Filters[1].ID = "approved" }, "invalid id"),
		Entry("filter id of a reserved argument", func(m *Manifest) { m.Capabilities.ModelSearch.Filters[1].ID = "q" }, "invalid id"),
		Entry("markup in a label", func(m *Manifest) { m.Capabilities.ModelSearch.Filters[1].Label = "<b>x</b>" }, "invalid label"),
		Entry("source id of the built-in provider", func(m *Manifest) { m.Capabilities.ModelSearch.Source.ID = "builtin" }, "source id"),
		Entry("unknown sort", func(m *Manifest) { m.Capabilities.ModelSearch.Sorts = []string{"price"} }, "sort"),
		Entry("search tool not declared", func(m *Manifest) {
			m.Capabilities.Provides[0].Roles = map[string]ProviderTools{RoleSearch: {"nope"}}
		}, "not a declared tool"),
		Entry("search tool defined but absent from capabilities.tools", func(m *Manifest) {
			m.Capabilities.Tools = slices.DeleteFunc(m.Capabilities.Tools, func(t string) bool { return t == "search" })
		}, "undeclared or repeated tool"),
		Entry("repeated sort", func(m *Manifest) { m.Capabilities.ModelSearch.Sorts = []string{"likes", "likes"} }, "repeated sort"),
		Entry("a filter value label with markup", func(m *Manifest) {
			m.Capabilities.ModelSearch.Filters[0].Values = []string{"<a>", "b"}
			m.Capabilities.ModelSearch.Filters[0].Default = "b"
		}, "invalid or repeated value"),
		Entry("search role missing", func(m *Manifest) { m.Capabilities.Provides[0].Roles = nil }, "role search is required"),
		Entry("modelSearch without the slot", func(m *Manifest) { m.Capabilities.Provides = nil }, "needs a provides entry"),
		Entry("slot without modelSearch", func(m *Manifest) { m.Capabilities.ModelSearch = nil; m.FeatureLevel = 1 }, "needs capabilities.modelSearch"),
		Entry("translation of an unknown filter", func(m *Manifest) {
			m.I18n = map[string]Translation{"it": {ModelSearch: &ModelSearchText{Filters: map[string]SearchFilterText{"zzz": {Label: "X"}}}}}
		}, "does not declare"),
	)

	It("takes the source properties hasVariants and p2p, which the host sends to the panel", func() {
		m := source()
		m.Capabilities.ModelSearch.Source.HasVariants = true
		m.Capabilities.ModelSearch.Source.P2P = true
		Expect(ValidateManifest(m, host())).To(Succeed())
		raw, err := json.Marshal(m.Capabilities.ModelSearch.Source)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).To(And(ContainSubstring(`"hasVariants":true`), ContainSubstring(`"p2p":true`)))
	})

	It("takes translations of the title and the filters", func() {
		m := source()
		m.I18n = map[string]Translation{"it": {ModelSearch: &ModelSearchText{Title: "Esempio",
			Filters: map[string]SearchFilterText{"kind": {Label: "Tipo", Values: map[string]string{"a": "Uno"}}}}}}
		Expect(ValidateManifest(m, host())).To(Succeed())
	})

	It("refuses mode set on a slot that is pick-one by its descriptor", func() {
		bad := []ProviderSlot{{ID: "pick", Version: 1, MinVersion: 1, Title: "Pick", Description: "d",
			Selection: ProviderSelection{Scope: "user", Default: ProviderBuiltin, OffAllowed: true, Mode: SelectionSet},
			Builtin:   ProviderBuiltinInfo{ID: ProviderBuiltin, Title: "B", Description: "d"}}}
		Expect(validateProviderSlots(bad)).To(MatchError(ContainSubstring("set slot")))
		bad[0].Selection.OffAllowed = false
		Expect(validateProviderSlots(bad)).To(Succeed())
		bad[0].Selection.Mode = "many"
		Expect(validateProviderSlots(bad)).To(MatchError(ContainSubstring("selection mode")))
	})
})

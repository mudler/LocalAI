// SPDX-License-Identifier: MIT
package contract

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Event declarations", func() {
	withEvents := func(ds ...EventDeclaration) Manifest {
		m, e := ParseManifest(fixture())
		Expect(e).NotTo(HaveOccurred())
		m.FeatureLevel = 12
		m.Capabilities.Events = ds
		return m
	}
	good := func() EventDeclaration {
		return EventDeclaration{Type: "sample.item.created", Lifecycle: EventCreated, Key: "item.id", Title: "item.name",
			Anchor: &EventAnchor{Time: "item.due", Zone: "item.zone", AllDay: "item.allDay"}, Link: "items?item={key}",
			Label: Localized{ByLanguage: map[string]string{"en": "An item is added", "it": "Si aggiunge un elemento"}}}
	}

	It("accepts a declaration with paths, an anchor and a label, and needs feature level 12", func() {
		m := withEvents(good())
		Expect(m.ID).To(Equal("sample"))
		Expect(ValidateManifest(m, compatible())).To(Succeed())
		m.FeatureLevel = 11
		Expect(ValidateManifest(m, compatible())).To(MatchError(ContainSubstring("must declare featureLevel 12")))
		h := compatible()
		h.FeatureLevel = 11
		m.FeatureLevel = 12
		Expect(ValidateManifest(m, h)).To(MatchError("needs a newer LocalAI"))
		d, ok := m.Capabilities.FindEvent("sample.item.created")
		Expect(ok).To(BeTrue())
		Expect(d.Key).To(Equal("item.id"))
		_, ok = m.Capabilities.FindEvent("sample.item.gone")
		Expect(ok).To(BeFalse())
	})

	DescribeTable("refuses a declaration that the host cannot read",
		func(change func(*EventDeclaration), want string) {
			d := good()
			change(&d)
			Expect(ValidateManifest(withEvents(d), compatible())).To(MatchError(ContainSubstring(want)))
		},
		Entry("an unknown lifecycle", func(d *EventDeclaration) { d.Lifecycle = "moved" }, "lifecycle"),
		Entry("an empty lifecycle", func(d *EventDeclaration) { d.Lifecycle = "" }, "lifecycle"),
		Entry("an empty key path", func(d *EventDeclaration) { d.Key = "" }, "key"),
		Entry("a key path with an empty segment", func(d *EventDeclaration) { d.Key = "item..id" }, "key"),
		Entry("a key path through a prototype", func(d *EventDeclaration) { d.Key = "__proto__.id" }, "key"),
		Entry("an empty title path", func(d *EventDeclaration) { d.Title = "" }, "title"),
		Entry("an empty anchor time", func(d *EventDeclaration) { d.Anchor.Time = "" }, "anchor.time"),
		Entry("a bad zone path", func(d *EventDeclaration) { d.Anchor.Zone = "a b" }, "anchor.zone"),
		Entry("a bad moves path", func(d *EventDeclaration) { d.Anchor.Moves = []string{"item.end", "a..b"} }, "anchor.moves"),
		Entry("a bad following path", func(d *EventDeclaration) { d.Anchor.Following = "item..later" }, "anchor.following"),
		Entry("a type of another add-on", func(d *EventDeclaration) { d.Type = "calendar.event.created" }, "must start with"),
		Entry("a link that leaves the page", func(d *EventDeclaration) { d.Link = "https://example.com/x" }, "link"),
		Entry("no label", func(d *EventDeclaration) { d.Label = Localized{} }, "label"),
	)

	It("refuses the same type twice", func() {
		Expect(ValidateManifest(withEvents(good(), good()), compatible())).To(MatchError(ContainSubstring("declared twice")))
	})

	It("reads the declarations from JSON with a plain or a translated label", func() {
		raw := `{"type":"sample.item.deleted","lifecycle":"deleted","key":"id","title":"name","label":"An item is removed"}`
		var d EventDeclaration
		Expect(strictJSON([]byte(raw), &d, 4096)).To(Succeed())
		Expect(d.Label.Default()).To(Equal("An item is removed"))
		Expect(d.Anchor).To(BeNil())
	})
})

var _ = Describe("Message actions", func() {
	It("takes up to four buttons with short unique ids and labels", func() {
		ok := []MessageAction{{ID: "reminder:r1:snooze", Label: "Snooze 5 min"}, {ID: "reminder:r1:skip", Label: "Skip"}}
		Expect(ValidMessageActions(ok)).To(Succeed())
		Expect(ValidMessageActions(nil)).To(Succeed())
		five := append(ok, MessageAction{ID: "a", Label: "A"}, MessageAction{ID: "b", Label: "B"}, MessageAction{ID: "c", Label: "C"})
		Expect(ValidMessageActions(five)).To(MatchError(ContainSubstring("at most 4")))
		Expect(ValidMessageActions([]MessageAction{{ID: "x", Label: "A"}, {ID: "x", Label: "B"}})).NotTo(Succeed())
		Expect(ValidMessageActions([]MessageAction{{ID: "has space", Label: "A"}})).NotTo(Succeed())
		Expect(ValidMessageActions([]MessageAction{{ID: "x", Label: ""}})).NotTo(Succeed())
		Expect(ValidMessageActions([]MessageAction{{ID: strings.Repeat("x", 65), Label: "A"}})).NotTo(Succeed())
	})
})

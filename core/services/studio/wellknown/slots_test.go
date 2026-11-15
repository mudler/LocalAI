package wellknown_test

import (
	"context"
	"encoding/json"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/wellknown"
)

func slotsDoc(slots map[string]any) map[string]any { return map[string]any{"slots": slots} }

func pageItem(name string) map[string]any { return map[string]any{"type": "page", "page": name} }

var _ = Describe("the slots of ui.json", func() {
	ctx := context.Background()

	It("accepts page tiles, tool buttons and commands in each slot", func() {
		Expect(validate(wellknown.UI, slotsDoc(map[string]any{
			"chatHeader": []any{
				pageItem("weather"),
				map[string]any{"type": "page", "page": "standup", "folder": 4, "label": "Standup", "icon": "users"},
			},
			"sidebarFooter": []any{pageItem("focus-timer")},
			"composerToolbar": []any{
				map[string]any{"type": "tool", "tool": "summarize_chat", "label": "Summarize chat", "icon": "wand", "arguments": map[string]any{"words": 120, "bullets": true, "tone": "plain"}},
				map[string]any{"type": "command", "command": "translate"},
			},
		}))).To(BeEmpty())
		Expect(validate(wellknown.UI, slotsDoc(map[string]any{}))).To(BeEmpty(), "no slot at all")
		Expect(validate(wellknown.UI, map[string]any{"theme": "dark"})).To(BeEmpty(), "a file without slots")
	})

	It("keeps the caps of the schema and of the code in step", func() {
		data, err := wellknown.Schema(wellknown.UI)
		Expect(err).NotTo(HaveOccurred())
		var s struct {
			Properties struct {
				Slots struct {
					Properties map[string]struct {
						MaxItems int `json:"maxItems"`
					} `json:"properties"`
				} `json:"slots"`
			} `json:"properties"`
		}
		Expect(json.Unmarshal(data, &s)).To(Succeed())
		Expect(s.Properties.Slots.Properties).To(HaveLen(len(wellknown.SlotNames)))
		for _, n := range wellknown.SlotNames {
			Expect(s.Properties.Slots.Properties).To(HaveKey(n))
			Expect(s.Properties.Slots.Properties[n].MaxItems).To(Equal(wellknown.SlotCaps[n]), n)
		}
	})

	DescribeTable("refuses a slot that is wrong, and says what to change",
		func(slots map[string]any, want string) {
			errs := validate(wellknown.UI, slotsDoc(slots))
			Expect(strings.Join(errs, "\n")).To(ContainSubstring(want))
		},
		Entry("an unknown slot", map[string]any{"footer": []any{}}, "slots.footer: not a slot; the slots are chatHeader, sidebarFooter, composerToolbar"),
		Entry("too many items",
			map[string]any{"chatHeader": []any{pageItem("a"), pageItem("b"), pageItem("c"), pageItem("d"), pageItem("e")}},
			"slots.chatHeader: this slot takes at most 4 items; it has 5, so remove 1"),
		Entry("a page tile without its page", map[string]any{"chatHeader": []any{map[string]any{"type": "page"}}}, "slots.chatHeader[0].page: a page tile names its page"),
		Entry("a page that is not a page name", map[string]any{"chatHeader": []any{pageItem("../etc")}}, "slots"),
		Entry("a page with a path", map[string]any{"chatHeader": []any{pageItem("weather/index.html")}}, "slots"),
		Entry("a tool button without a label", map[string]any{"composerToolbar": []any{map[string]any{"type": "tool", "tool": "summarize_chat"}}}, "slots.composerToolbar[0].label: a tool button needs the text it shows"),
		Entry("a tool button without its tool", map[string]any{"composerToolbar": []any{map[string]any{"type": "tool", "label": "Go"}}}, "slots.composerToolbar[0].tool: a tool button names the tool it runs"),
		Entry("a command without its name", map[string]any{"composerToolbar": []any{map[string]any{"type": "command"}}}, "slots.composerToolbar[0].command: a command item names the command"),
		Entry("a field of another type", map[string]any{"chatHeader": []any{map[string]any{"type": "page", "page": "weather", "tool": "x"}}}, "slots.chatHeader[0]: an item of type page has no tool"),
		Entry("the same page twice", map[string]any{"chatHeader": []any{pageItem("weather"), pageItem("weather")}}, "slots.chatHeader[1]: the same item as slots.chatHeader[0]"),
		Entry("an unknown type", map[string]any{"chatHeader": []any{map[string]any{"type": "iframe", "url": "https://x"}}}, "slots"),
		Entry("arguments that are not plain values", map[string]any{"composerToolbar": []any{map[string]any{"type": "tool", "tool": "t", "label": "T", "arguments": map[string]any{"nested": map[string]any{"a": 1}}}}}, "slots"),
		Entry("arguments that are too big", map[string]any{"composerToolbar": []any{map[string]any{"type": "tool", "tool": "t", "label": "T", "arguments": map[string]any{"text": strings.Repeat("x", 2100)}}}}, "slots.composerToolbar[0].arguments: at most 2048 bytes"),
		Entry("a label that is too long", map[string]any{"composerToolbar": []any{map[string]any{"type": "tool", "tool": "t", "label": strings.Repeat("l", 41)}}}, "slots"),
		Entry("an icon with markup", map[string]any{"composerToolbar": []any{map[string]any{"type": "tool", "tool": "t", "label": "T", "icon": "<svg>"}}}, "slots"),
		Entry("a folder that is not an id", map[string]any{"chatHeader": []any{map[string]any{"type": "page", "page": "weather", "folder": "/shared/team"}}}, "slots"),
	)

	It("lets one page sit in two slots, and the same tool with other arguments", func() {
		Expect(validate(wellknown.UI, slotsDoc(map[string]any{
			"chatHeader":    []any{pageItem("weather")},
			"sidebarFooter": []any{pageItem("weather")},
			"composerToolbar": []any{
				map[string]any{"type": "tool", "tool": "translate", "label": "To Italian", "arguments": map[string]any{"to": "it"}},
				map[string]any{"type": "tool", "tool": "translate", "label": "To German", "arguments": map[string]any{"to": "de"}},
			},
		}))).To(BeEmpty())
	})

	It("a save of the slots keeps the other keys of the file", func() {
		s, f := newService()
		raw(f, alice, "/LocalAI/ui.json", `{"theme":"dark","x_mood":"calm","strings":{"nav.home":"Start"}}`)
		_, err := s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.UI, Values: map[string]any{
			"slots": map[string]any{"chatHeader": []any{pageItem("weather")}},
		}})
		Expect(err).NotTo(HaveOccurred())
		on := readRaw(f, alice, "/LocalAI/ui.json")
		Expect(on).To(HaveKeyWithValue("theme", "dark"))
		Expect(on).To(HaveKeyWithValue("x_mood", "calm"))
		Expect(on).To(HaveKey("strings"))
		Expect(on).To(HaveKey("slots"))
	})

	It("a file with a broken slot is not used and the last good version stays", func() {
		s, f := newService()
		raw(f, alice, "/LocalAI/ui.json", `{"slots":{"chatHeader":[{"type":"page","page":"weather"}]}}`)
		doc, err := s.Read(ctx, alice, wellknown.UI, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Valid).To(BeTrue())
		raw(f, alice, "/LocalAI/ui.json", `{"slots":{"chatHeader":[{"type":"page"}]}}`)
		doc, err = s.Read(ctx, alice, wellknown.UI, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Valid).To(BeFalse())
		Expect(strings.Join(doc.Errors, "\n")).To(ContainSubstring("a page tile names its page"))
	})
})

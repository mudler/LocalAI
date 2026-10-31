package skills

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

var _ = Describe("the add-on layer", func() {
	var (
		st    workspace.Store
		roots []Root
	)
	replaceable := func(n string) bool { return n == "xlsx" || n == "docx" }
	BeforeEach(func() {
		st = newStore()
		grant(st, "alice", "/users/alice", workspace.ModeWrite)
		grant(st, "alice", "/builtin-skills", workspace.ModeRead)
		grant(st, "alice", "/addon-skills", workspace.ModeRead)
		put(st, "/builtin-skills/xlsx/SKILL.md", skillText("xlsx", "built-in xlsx"))
		put(st, "/builtin-skills/docx/SKILL.md", skillText("docx", "built-in docx"))
		put(st, "/builtin-skills/writing-skills/SKILL.md", skillText("writing-skills", "built-in writing"))
		put(st, "/addon-skills/office/xlsx/SKILL.md", skillText("xlsx", "office xlsx"))
		put(st, "/addon-skills/office/docx/SKILL.md", skillText("docx", "office docx"))
		put(st, "/addon-skills/office/office-tips/SKILL.md", skillText("office-tips", "office tips"))
		put(st, "/addon-skills/sheets/xlsx/SKILL.md", skillText("xlsx", "sheets xlsx"))
		put(st, "/addon-skills/sheets/writing-skills/SKILL.md", skillText("writing-skills", "a take-over"))
		roots = []Root{
			{Layer: LayerBuiltin, Path: "/builtin-skills", Guest: "/workspace/shared-builtin-skills"},
			{Layer: LayerAddon, Addon: "office", AddonTitle: "Office", Path: "/addon-skills/office", Guest: "/workspace/shared-addon-skills/office"},
			{Layer: LayerAddon, Addon: "sheets", AddonTitle: "Sheets", Path: "/addon-skills/sheets", Guest: "/workspace/shared-addon-skills/sheets"},
			{Layer: LayerPersonal, Path: "/users/alice/.skills", Guest: "/workspace/home/.skills"},
		}
	})
	find := func(list []Skill, layer Layer, addon, name string) Skill {
		for _, sk := range list {
			if sk.Layer == layer && sk.Addon == addon && sk.Name == name {
				return sk
			}
		}
		Fail("no skill " + string(layer) + "/" + addon + "/" + name)
		return Skill{}
	}
	resolve := func(o Options) []Skill {
		list, _, err := Resolve(context.Background(), st, alice, roots, o)
		Expect(err).NotTo(HaveOccurred())
		return list
	}

	It("puts the add-on layer above the built-in one and marks who replaced it", func() {
		list := resolve(Options{Replaceable: replaceable})
		Expect(find(list, LayerAddon, "office", "docx").Shadowed).To(BeEmpty())
		bd := find(list, LayerBuiltin, "", "docx")
		Expect(bd.Shadowed).To(Equal(string(LayerAddon)))
		Expect(bd.ShadowedBy).To(Equal("office"))
		Expect(find(list, LayerAddon, "office", "office-tips").Addon).To(Equal("office"))
		Expect(find(list, LayerAddon, "office", "office-tips").AddonTitle).To(Equal("Office"))
		// Two add-ons and no decision: the first by add-on ID serves.
		Expect(find(list, LayerAddon, "office", "xlsx").Shadowed).To(BeEmpty())
		Expect(find(list, LayerAddon, "sheets", "xlsx").ShadowedBy).To(Equal("office"))
	})

	It("never lets an add-on take over a built-in skill that is not replaceable", func() {
		list := resolve(Options{Replaceable: replaceable})
		Expect(find(list, LayerAddon, "sheets", "writing-skills").Invalid).To(Equal(ErrNotReplaceable.Error()))
		Expect(find(list, LayerBuiltin, "", "writing-skills").Shadowed).To(BeEmpty())
		list = resolve(Options{})
		Expect(find(list, LayerAddon, "office", "xlsx").Invalid).NotTo(BeEmpty(), "with no replaceable list, nothing is replaced")
	})

	It("serves a name from the chosen add-on, or from none", func() {
		pick := map[string]string{"xlsx": "sheets", "docx": ""}
		list := resolve(Options{Replaceable: replaceable, AddonProvider: func(n string) (string, bool) {
			p, ok := pick[n]
			return p, ok
		}})
		Expect(find(list, LayerAddon, "sheets", "xlsx").Shadowed).To(BeEmpty())
		o := find(list, LayerAddon, "office", "xlsx")
		Expect(o.Shadowed).To(Equal(string(LayerAddon)))
		Expect(o.ShadowedBy).To(Equal("sheets"))
		Expect(find(list, LayerBuiltin, "", "xlsx").ShadowedBy).To(Equal("sheets"))
		// No add-on serves docx: the built-in one does.
		Expect(find(list, LayerAddon, "office", "docx").Shadowed).To(Equal(string(LayerBuiltin)))
		Expect(find(list, LayerBuiltin, "", "docx").Shadowed).To(BeEmpty())
		Expect(Index(list)).To(ContainSubstring("built-in docx"))
		Expect(Index(list)).To(ContainSubstring("sheets xlsx"))
		Expect(Index(list)).NotTo(ContainSubstring("office xlsx"))
	})

	It("keeps a person's own skill above the add-on's, and a switch uncovers the built-in one", func() {
		put(st, "/users/alice/.skills/docx/SKILL.md", skillText("docx", "mine"))
		list := resolve(Options{Replaceable: replaceable})
		Expect(find(list, LayerPersonal, "", "docx").Shadowed).To(BeEmpty())
		Expect(find(list, LayerAddon, "office", "docx").Shadowed).To(Equal(string(LayerPersonal)))
		list = resolve(Options{Replaceable: replaceable, Off: func(sk Skill) bool { return sk.Layer == LayerAddon && sk.Name == "xlsx" }})
		Expect(find(list, LayerBuiltin, "", "xlsx").Shadowed).To(BeEmpty(), "switching the add-on's xlsx off brings back the built-in one")
	})

	It("costs no more index lines when the add-on replaces built-in skills", func() {
		without, _, err := Resolve(context.Background(), st, alice, roots[:1], Options{Replaceable: replaceable})
		Expect(err).NotTo(HaveOccurred())
		with := resolve(Options{Replaceable: replaceable})
		lines := func(s string) int { return len(splitLines(s)) }
		// The office add-on replaces two lines and adds one (office-tips).
		Expect(lines(Index(with))).To(Equal(lines(Index(without)) + 1))
	})
})

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := range len(s) {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

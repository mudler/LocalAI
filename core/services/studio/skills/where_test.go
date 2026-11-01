package skills

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Where", func() {
	list := []Skill{
		sk(LayerBuiltin, "docx", "Word files."),
		{Name: "tips", Description: "Tips.", Layer: LayerAddon, Addon: "office", GuestPath: "/workspace/shared-addon-skills/office/tips/SKILL.md"},
		sk(LayerPersonal, "notes", "My notes."),
		{Name: "old", Description: "Old.", Layer: LayerBuiltin, GuestPath: "/w/old/SKILL.md", Shadowed: "personal"},
		{Name: "off", Description: "Off.", Layer: LayerBuiltin, GuestPath: "/w/off/SKILL.md", Off: true},
	}

	DescribeTable("finds a listed skill from a path that names it",
		func(p, name string) {
			got, ok := Where(list, p)
			Expect(ok).To(BeTrue(), p)
			Expect(got.Name).To(Equal(name))
		},
		Entry("the user folder", "/workspace/home/.skills/docx/SKILL.md", "docx"),
		Entry("a skills folder without the dot", "/workspace/home/skills/docx", "docx"),
		Entry("a folder of a layer", "/workspace/shared-addon-skills/tips/SKILL.md", "tips"),
		Entry("a short path", "notes/SKILL.md", "notes"),
		Entry("a leading slash", "/notes/SKILL.md", "notes"),
	)

	DescribeTable("gives nothing for any other path",
		func(p string) {
			_, ok := Where(list, p)
			Expect(ok).To(BeFalse(), p)
		},
		Entry("an unknown name", "/workspace/home/.skills/nope/SKILL.md"),
		Entry("a shadowed skill", "/workspace/home/.skills/old/SKILL.md"),
		Entry("a skill that is off", "off/SKILL.md"),
		Entry("a plain file", "/workspace/home/report.md"),
		Entry("a skill name without a skills folder", "/workspace/home/docx/report.md"),
		Entry("empty", ""),
	)
})

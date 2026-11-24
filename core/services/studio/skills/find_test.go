package skills

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Find and Suggest", func() {
	list := []Skill{
		sk(LayerBuiltin, "docx", "Word files."),
		sk(LayerBuiltin, "xlsx", "Sheets."),
		sk(LayerAddon, "office-notes", "Notes of the office add-on."),
		sk(LayerBuiltin, "writing-skills", "How to write a skill."),
		{Name: "old", Description: "Old.", Layer: LayerBuiltin, Shadowed: "personal"},
		{Name: "quiet", Description: "Off.", Layer: LayerBuiltin, Off: true},
		{Name: "broken", Layer: LayerBuiltin, Invalid: "no description"},
	}

	It("finds a usable skill by its exact name only", func() {
		got, ok := Find(list, " docx ")
		Expect(ok).To(BeTrue())
		Expect(got.Name).To(Equal("docx"))
		for _, n := range []string{"DOCX", "old", "quiet", "broken", "nope", ""} {
			_, ok := Find(list, n)
			Expect(ok).To(BeFalse(), n)
		}
	})

	It("names the usable skills in index order", func() {
		Expect(Names(list)).To(Equal([]string{"office-notes", "docx", "writing-skills", "xlsx"}))
	})

	DescribeTable("suggests near names",
		func(asked string, want []string) {
			Expect(Suggest(list, asked)).To(Equal(want))
		},
		Entry("other case", "DOCX", []string{"docx"}),
		Entry("a layer prefix", "addon:xlsx", []string{"xlsx"}),
		Entry("without the add-on prefix", "notes", []string{"office-notes"}),
		Entry("with a prefix added", "anthropic-docx", []string{"docx"}),
		Entry("one edit on a short name", "docs", []string{"docx"}),
		Entry("two edits on a long name", "writng-skill", []string{"writing-skills"}),
		Entry("nothing near", "calendar", nil),
		Entry("two edits on a short name are too many", "dxcs", nil),
		Entry("not a shadowed skill", "olds", nil),
		Entry("empty", "", nil),
	)

	It("returns the body after the frontmatter", func() {
		Expect(Body([]byte("---\nname: a\ndescription: b\n---\n\n# Title\nDo this.\n"))).To(Equal("# Title\nDo this."))
		Expect(Body([]byte("\xef\xbb\xbf---\r\nname: a\r\n---\r\nBody.\r\n"))).To(Equal("Body."))
		Expect(Body([]byte("No frontmatter."))).To(Equal("No frontmatter."))
		Expect(Body([]byte("---\nname: a\nnever closed"))).To(Equal("---\nname: a\nnever closed"))
	})
})

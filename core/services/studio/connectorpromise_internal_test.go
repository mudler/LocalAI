package studio

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("promisesFile", func() {
	DescribeTable("finds the paperclip line of an answer that presents a file",
		func(answer string, want bool) { Expect(promisesFile(answer)).To(Equal(want)) },
		Entry("paperclip after a sentence", "Here it is, I'll send you one. 📎 `design-notes.pdf`", true),
		Entry("paperclip line", "Done.\n📎 `report.xlsx`", true),
		Entry("paperclip with a variation selector", "📎️ summary.docx", true),
		Entry("plain name", "📎 data.csv", true),
		Entry("bold name", "📎 **notes.md**", true),
		Entry("name with spaces in code quotes", "📎 `Q3 plan.pdf`", true),
		Entry("upper case extension", "📎 `SCAN.PDF`", true),
		Entry("a name with a trailing full stop", "📎 report.pdf.", true),
		Entry("paperclip with no file", "I attached it 📎", false),
		Entry("paperclip with prose after it", "📎 attached below, see the summary", false),
		Entry("an extension that is not sent", "📎 `setup.exe`", false),
		Entry("a file name in prose", "The file is `report.pdf` in your folder.", false),
		Entry("a file name in a code block", "```\nreport.pdf\n```", false),
		Entry("ordinary prose", "I will send you a summary of the file.", false),
		Entry("empty", "", false),
	)
})

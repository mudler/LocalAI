// SPDX-License-Identifier: MIT
package textinput_test

import (
	"bytes"
	"context"

	"github.com/mudler/LocalAI/addons/office/internal/docx"
	"github.com/mudler/LocalAI/addons/office/internal/docxedit"
	"github.com/mudler/LocalAI/addons/office/internal/textinput"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Markdown DOCX edit compatibility", func() {
	DescribeTable("keeps same-style parser fragments editable as actual text",
		func(markdown, find, replacement string, before, after []docx.Run) {
			parsed, err := textinput.ParseMarkdown(context.Background(), markdown)
			Expect(err).NotTo(HaveOccurred())
			// Exercise the generated file, not just the intermediate run model.
			var buf bytes.Buffer
			Expect(docx.Write(&buf, docx.Document{Blocks: parsed.Docx})).To(Succeed())
			editor, err := docxedit.Open(buf.Bytes())
			Expect(err).NotTo(HaveOccurred())
			paragraphs := editor.Paragraphs()
			Expect(paragraphs[len(paragraphs)-1].Text).To(ContainSubstring(find))
			n, err := editor.ReplaceAll(find, replacement)
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(Equal(1))
			Expect(parsed.Docx[len(parsed.Docx)-1].(docx.Paragraph).Runs).To(Equal(before))

			data, err := editor.Bytes()
			Expect(err).NotTo(HaveOccurred())
			reopened, err := docxedit.Open(data)
			Expect(err).NotTo(HaveOccurred())
			paragraphs = reopened.Paragraphs()
			Expect(paragraphs[len(paragraphs)-1].Text).To(ContainSubstring(replacement))
			// Compare against a fresh rendering to prove emphasis is preserved.
			expected := parsed.Docx
			expected[len(expected)-1] = docx.Paragraph{Runs: after}
			var want bytes.Buffer
			Expect(docx.Write(&want, docx.Document{Blocks: expected})).To(Succeed())
			Expect(parts(data)["word/document.xml"]).To(Equal(parts(want.Bytes())["word/document.xml"]))
		},
		Entry("plain paragraph with punctuation", "# Summary\n\nAll good.", "All good.", "Mostly good.",
			[]docx.Run{{Text: "All good."}}, []docx.Run{{Text: "Mostly good."}}),
		Entry("punctuation inside a bold run", "A **very good.** finish.", "very good.", "mostly good.",
			[]docx.Run{{Text: "A "}, {Text: "very good.", Bold: true}, {Text: " finish."}},
			[]docx.Run{{Text: "A "}, {Text: "mostly good.", Bold: true}, {Text: " finish."}}),
		Entry("label inside an italic run", "A *very [good](https://example.invalid).* finish.", "very good.", "mostly good.",
			[]docx.Run{{Text: "A "}, {Text: "very good.", Italic: true}, {Text: " finish."}},
			[]docx.Run{{Text: "A "}, {Text: "mostly good.", Italic: true}, {Text: " finish."}}),
		Entry("label inside a bold italic run", "A ***very [good](https://example.invalid).*** finish.", "very good.", "mostly good.",
			[]docx.Run{{Text: "A "}, {Text: "very good.", Bold: true, Italic: true}, {Text: " finish."}},
			[]docx.Run{{Text: "A "}, {Text: "mostly good.", Bold: true, Italic: true}, {Text: " finish."}}),
		Entry("label and literal code with the same style", "All [good](https://example.invalid) `today`.", "All good today.", "Mostly good.",
			[]docx.Run{{Text: "All good today."}}, []docx.Run{{Text: "Mostly good."}}),
	)
})

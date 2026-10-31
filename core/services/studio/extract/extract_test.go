package extract_test

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/extract"
)

func TestExtract(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Studio document extraction")
}

var ctx = context.Background()

var _ = Describe("Extract", func() {
	It("reads a workbook sheet by sheet as tab-separated rows", func() {
		data := inlineWorkbook(map[string][][]string{
			"Files":  {{"name", "size"}, {"report.pdf", "120"}, {"notes.txt", "8"}},
			"Totals": {{"count", "2"}},
		}, "Files", "Totals")
		text, st, err := extract.Extract(ctx, "index.xlsx", data)
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Kind).To(Equal(extract.KindTable))
		Expect(st.Truncated).To(BeFalse())
		Expect(st.Preview.Sheets).To(HaveLen(2))
		Expect(text).To(Equal("## Sheet \"Files\" (3 rows, 2 columns)\nname\tsize\nreport.pdf\t120\nnotes.txt\t8\n\n" +
			"## Sheet \"Totals\" (1 rows, 2 columns)\ncount\t2"))
	})

	It("says how much of a large sheet it left out", func() {
		rows := [][]string{}
		for i := range 30 {
			rows = append(rows, []string{fmt.Sprintf("r%d", i), "a", "b", "c"})
		}
		data := inlineWorkbook(map[string][][]string{"Big": rows}, "Big")
		text, st, err := extract.ExtractWith(ctx, "big.xlsx", data, extract.Limits{Rows: 10, Cols: 2, Text: 1000})
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Truncated).To(BeTrue())
		Expect(text).To(ContainSubstring("r9\ta\n"))
		Expect(text).NotTo(ContainSubstring("r10"))
		Expect(text).To(HaveSuffix(`[Sheet "Big" is cut: 10 of 30 rows and 2 of 4 columns shown.]`))
	})

	It("gives an empty workbook no text and no error", func() {
		text, _, err := extract.Extract(ctx, "empty.xlsx", inlineWorkbook(map[string][][]string{"S": nil}, "S"))
		Expect(err).NotTo(HaveOccurred())
		Expect(text).To(Equal(`## Sheet "S" (0 rows, 0 columns)`))
	})

	It("reads the headings, paragraphs and tables of a Word document", func() {
		body := wp("Heading1", "Plan") + wp("", "First we read the files.") +
			`<w:tbl><w:tr><w:tc>` + wp("", "file") + `</w:tc><w:tc>` + wp("", "pages") + `</w:tc></w:tr>` +
			`<w:tr><w:tc>` + wp("", "a.pdf") + `</w:tc><w:tc>` + wp("", "3") + `</w:tc></w:tr></w:tbl>` +
			wp("", "Done.")
		text, st, err := extract.Extract(ctx, "plan.docx", docx(body))
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Kind).To(Equal(extract.KindDocument))
		Expect(text).To(Equal("# Plan\n\nFirst we read the files.\n\n[Table]\nfile\tpages\na.pdf\t3\n\nDone."))
	})

	It("reads a presentation slide by slide", func() {
		text, st, err := extract.Extract(ctx, "deck.pptx", pptx([2]string{"Intro", "Why we do it"}, [2]string{"Plan", "Three steps"}))
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Kind).To(Equal(extract.KindSlides))
		Expect(st.Preview.Slides).To(HaveLen(2))
		Expect(text).To(Equal("## Slide 1: Intro\n- Why we do it\n\n## Slide 2: Plan\n- Three steps"))
	})

	It("tells a PDF with no text layer apart", func() {
		_, st, err := extract.Extract(ctx, "scan.pdf", blankPDF())
		Expect(err).To(MatchError(extract.ErrNoText))
		Expect(st.Kind).To(Equal(extract.KindPages))
	})

	It("refuses what it cannot read", func() {
		_, _, err := extract.Extract(ctx, "notes.txt", []byte("hello"))
		Expect(err).To(MatchError(extract.ErrUnsupported))
		_, _, err = extract.Extract(ctx, "broken.xlsx", []byte("not a zip"))
		Expect(err).To(MatchError(extract.ErrInvalid))
		_, _, err = extract.ExtractWith(ctx, "big.docx", make([]byte, 100), extract.Limits{MaxBytes: 10})
		Expect(err).To(HaveOccurred())
		Expect(extract.Supported("a.XLSM")).To(BeTrue())
		Expect(extract.Supported("a.csv")).To(BeFalse())
	})
})

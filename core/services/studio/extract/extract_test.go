package extract_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/extract"
	"github.com/mudler/LocalAI/core/services/studio/extract/extracttest"
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

	It("reads a PDF page by page and says where each page starts", func() {
		text, st, err := extract.Extract(ctx, "report.pdf", extracttest.TextPDF("Quarterly summary", "Budget overview\nTravel costs rose"))
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Kind).To(Equal(extract.KindPages))
		Expect(st.Pages).To(Equal(2))
		Expect(text).To(HavePrefix("## Page 1\nQuarterly summary"))
		Expect(text).To(ContainSubstring("## Page 2\nBudget overview"))
		Expect(st.Sections).To(HaveLen(2))
		lines := strings.Split(text, "\n")
		for _, sec := range st.Sections {
			Expect(sec.Kind).To(Equal(extract.SectionPage))
			Expect(lines[sec.Line-1]).To(Equal("## Page " + sec.Name))
		}
		travel := slices.IndexFunc(lines, func(l string) bool { return strings.Contains(l, "Travel") }) + 1
		at, ok := extract.SectionAt(st.Sections, travel)
		Expect(ok).To(BeTrue())
		Expect(at.Name).To(Equal("2"))
	})

	It("stops a PDF at the page and text limits", func() {
		data := extracttest.TextPDF("one", "two", "three")
		text, st, err := extract.ExtractWith(ctx, "long.pdf", data, extract.Limits{Pages: 2, MaxBytes: 1 << 20})
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Truncated).To(BeTrue())
		Expect(st.Pages).To(Equal(2))
		Expect(text).NotTo(ContainSubstring("three"))
		Expect(text).To(HaveSuffix("[The text stops here: only the first 2 of 3 pages were read.]"))

		text, st, err = extract.ExtractWith(ctx, "long.pdf", extracttest.TextPDF("alpha beta gamma delta", "never read"), extract.Limits{Text: 10, MaxBytes: 1 << 20})
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Truncated).To(BeTrue())
		Expect(text).NotTo(ContainSubstring("never"))
		Expect(text).To(ContainSubstring("the document has more than 10 characters"))
	})

	It("names the sheets and slides where they start", func() {
		data := extracttest.SharedWorkbook(map[string][][]string{
			"Budget": {{"item", "cost"}, {"rent", "900"}},
			"Notes":  {{"call the bank"}},
		}, "Budget", "Notes")
		text, st, err := extract.Extract(ctx, "book.xlsx", data)
		Expect(err).NotTo(HaveOccurred())
		Expect(text).To(ContainSubstring("call the bank"))
		Expect(st.Sections).To(Equal([]extract.Section{{Kind: extract.SectionSheet, Name: "Budget", Line: 1}, {Kind: extract.SectionSheet, Name: "Notes", Line: 5}}))
		_, st, err = extract.Extract(ctx, "deck.pptx", pptx([2]string{"A", "x"}, [2]string{"B", "y"}))
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Sections).To(Equal([]extract.Section{{Kind: extract.SectionSlide, Name: "1", Line: 1}, {Kind: extract.SectionSlide, Name: "2", Line: 4}}))
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

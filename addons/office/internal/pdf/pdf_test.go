package pdf

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// sampleSpec has two pages and every block kind.
func sampleSpec() Spec {
	return Spec{
		Title:  "Quarterly review",
		Author: "Test",
		Pages: []Page{
			{Blocks: []Block{
				Heading{Level: 1, Text: "Quarterly report"},
				Paragraph{Text: "The team shipped the release on time and closed the open reviews."},
				Heading{Level: 2, Text: "Highlights"},
				Bullets{Items: []string{"First highlight", "Second highlight"}},
				Heading{Level: 3, Text: "Figures"},
				Table{
					Header: []string{"Item", "Count"},
					Rows:   [][]string{{"Revenue", "42"}, {"Costs", "7"}},
				},
			}},
			{Blocks: []Block{
				Heading{Level: 1, Text: "Appendix"},
				Paragraph{Text: "Second page marker."},
			}},
		},
	}
}

// mustCreate creates a PDF or fails the spec.
func mustCreate(spec Spec) []byte {
	GinkgoHelper()
	out, err := Create(spec)
	Expect(err).ToNot(HaveOccurred(), "Create")
	return out
}

// onePageSpec returns a spec of n pages, each with one short paragraph.
func onePageSpec(n int, text string) Spec {
	s := Spec{Title: "t"}
	for i := range n {
		s.Pages = append(s.Pages, Page{Blocks: []Block{Paragraph{Text: fmt.Sprintf("%s %d", text, i+1)}}})
	}
	return s
}

// pageContent returns the decoded content stream of one page.
func pageContent(data []byte, page int) string {
	GinkgoHelper()
	ctx, err := api.ReadValidateAndOptimize(bytes.NewReader(data), model.NewDefaultConfiguration())
	Expect(err).ToNot(HaveOccurred(), "read")
	r, err := pdfcpu.ExtractPageContent(ctx, page)
	Expect(err).ToNot(HaveOccurred(), "content of page %d", page)
	b, err := io.ReadAll(r)
	Expect(err).ToNot(HaveOccurred(), "read content")
	return string(b)
}

// rotationOf returns the /Rotate value of one page, 0 when it is not set.
func rotationOf(data []byte, page int) int {
	GinkgoHelper()
	ctx, err := api.ReadValidateAndOptimize(bytes.NewReader(data), model.NewDefaultConfiguration())
	Expect(err).ToNot(HaveOccurred(), "read")
	d, _, _, err := ctx.PageDict(page, false)
	Expect(err).ToNot(HaveOccurred(), "page %d dict", page)
	if r := d.IntEntry("Rotate"); r != nil {
		return *r
	}
	return 0
}

// formPDF builds a one-page PDF with one AcroForm text field per name. It is
// written by hand so the test does not depend on a fixture file.
func formPDF(names ...string) []byte {
	var fields, annots string
	for i := range names {
		fields += fmt.Sprintf("%d 0 R ", 5+i)
		annots += fmt.Sprintf("%d 0 R ", 5+i)
	}
	objs := []string{
		fmt.Sprintf("<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [%s] /DA (/Helv 0 Tf 0 g) /DR << /Font << /Helv 4 0 R >> >> >> >>", fields),
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] /Resources << /Font << /Helv 4 0 R >> >> /Annots [%s] >>", annots),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	for i, name := range names {
		y := 150 - 30*i
		objs = append(objs, fmt.Sprintf("<< /Type /Annot /Subtype /Widget /FT /Tx /T (%s) /Rect [20 %d 200 %d] /F 4 /DA (/Helv 12 Tf 0 g) /P 3 0 R >>", name, y, y+20))
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n")
	offs := make([]int, len(objs))
	for i, o := range objs {
		offs[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, o := range offs {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return b.Bytes()
}

var _ = Describe("PDF creation and editing", func() {
	Describe("Create", func() {
		It("creates every block kind across two pages", func() {
			data := mustCreate(sampleSpec())
			Expect(Check(data)).To(Succeed())
			n, err := Pages(data)
			Expect(err).ToNot(HaveOccurred())
			Expect(n).To(Equal(2))
			first := pageContent(data, 1)
			for _, want := range []string{"Quarterly report", "Highlights", "First highlight", "Revenue"} {
				Expect(first).To(ContainSubstring(want), "page 1 content")
			}
			Expect(pageContent(data, 2)).To(ContainSubstring("Second page marker"))
		})

		It("starts a page for each spec page", func() {
			data := mustCreate(onePageSpec(3, "short"))
			n, err := Pages(data)
			Expect(err).ToNot(HaveOccurred())
			Expect(n).To(Equal(3))
		})

		It("wraps long text across pages", func() {
			long := strings.Repeat("lorem ipsum dolor sit amet ", 800)
			data := mustCreate(Spec{Pages: []Page{{Blocks: []Block{Paragraph{Text: long}}}}})
			n, err := Pages(data)
			Expect(err).ToNot(HaveOccurred())
			Expect(n).To(BeNumerically(">=", 2))
			Expect(Check(data)).To(Succeed())
		})

		It("breaks table rows across pages", func() {
			var rows [][]string
			for i := range 200 {
				rows = append(rows, []string{fmt.Sprintf("row %d", i), "value"})
			}
			data := mustCreate(Spec{Pages: []Page{{Blocks: []Block{
				Table{Header: []string{"Name", "Value"}, Rows: rows},
			}}}})
			n, err := Pages(data)
			Expect(err).ToNot(HaveOccurred())
			Expect(n).To(BeNumerically(">=", 2))
			Expect(Check(data)).To(Succeed())
		})

		It("accepts Latin-1 text", func() {
			data := mustCreate(Spec{Pages: []Page{{Blocks: []Block{
				Paragraph{Text: "Caffè, città, für Ön: ¡olé! Ünïcödé"},
			}}}})
			Expect(Check(data)).To(Succeed())
			// The built-in font takes single bytes (cp1252), not UTF-8.
			c := pageContent(data, 1)
			Expect(c).To(ContainSubstring("Caff\xe8"), "content holds the single byte of è")
			Expect(c).ToNot(ContainSubstring("Caff\xc3\xa8"), "content holds UTF-8 bytes for è")
		})

		It("writes the bullet glyph as the cp1252 bullet byte", func() {
			data := mustCreate(Spec{Pages: []Page{{Blocks: []Block{Bullets{Items: []string{"alpha"}}}}}})
			Expect(pageContent(data, 1)).To(ContainSubstring("(\x95)Tj"))
		})

		It("rejects control characters", func() {
			for _, s := range []string{"bell\a", "c1 \u0085 control"} {
				spec := Spec{Pages: []Page{{Blocks: []Block{Paragraph{Text: s}}}}}
				_, err := Create(spec)
				Expect(err).To(HaveOccurred(), "%q: Create succeeded, want an error", s)
			}
		})

		It("rejects non-Latin-1 text without a font", func() {
			spec := Spec{Pages: []Page{{Blocks: []Block{Paragraph{Text: "price 5 €"}}}}}
			_, err := Create(spec)
			Expect(err).To(MatchError(ContainSubstring("U+20AC")))
		})

		It("rejects an invalid font", func() {
			spec := Spec{FontTTF: []byte("not a font"), Pages: []Page{{Blocks: []Block{Paragraph{Text: "x"}}}}}
			_, err := Create(spec)
			Expect(err).To(HaveOccurred())
		})

		It("refuses a font file that is not a TrueType font", func() {
			// A file that is not a font is refused before fpdf sees it.
			spec := Spec{FontTTF: []byte("%PDF-1.4 not a font at all"), Pages: []Page{{Blocks: []Block{Paragraph{Text: "x"}}}}}
			_, err := Create(spec)
			Expect(err).To(MatchError(ContainSubstring("TrueType")))
		})

		// Runs only when OFFICE_PDF_TEST_TTF names a TrueType font file. The
		// module ships no font.
		It("creates a document with a Unicode font", func() {
			path := os.Getenv("OFFICE_PDF_TEST_TTF")
			if path == "" {
				Skip("set OFFICE_PDF_TEST_TTF to a TrueType font to run this test")
			}
			ttf, err := os.ReadFile(path)
			Expect(err).ToNot(HaveOccurred(), "read font")
			data := mustCreate(Spec{FontTTF: ttf, Pages: []Page{{Blocks: []Block{
				Heading{Level: 1, Text: "中文 and αβγ"},
				Paragraph{Text: "Any text is allowed with a Unicode font."},
			}}}})
			n, err := Pages(data)
			Expect(err).ToNot(HaveOccurred())
			Expect(n).To(Equal(1))
		})

		It("validates the spec", func() {
			big := make([]Page, MaxPages+1)
			for i := range big {
				big[i] = Page{}
			}
			manyBlocks := make([]Block, MaxBlocks+1)
			for i := range manyBlocks {
				manyBlocks[i] = Paragraph{Text: "x"}
			}
			cases := map[string]Spec{
				"no pages":          {},
				"heading level 4":   {Pages: []Page{{Blocks: []Block{Heading{Level: 4, Text: "x"}}}}},
				"heading level 0":   {Pages: []Page{{Blocks: []Block{Heading{Level: 0, Text: "x"}}}}},
				"empty heading":     {Pages: []Page{{Blocks: []Block{Heading{Level: 1, Text: " "}}}}},
				"empty bullets":     {Pages: []Page{{Blocks: []Block{Bullets{}}}}},
				"empty bullet item": {Pages: []Page{{Blocks: []Block{Bullets{Items: []string{"a", ""}}}}}},
				"table no header":   {Pages: []Page{{Blocks: []Block{Table{}}}}},
				"table ragged row":  {Pages: []Page{{Blocks: []Block{Table{Header: []string{"a", "b"}, Rows: [][]string{{"x"}}}}}}},
				"nil block":         {Pages: []Page{{Blocks: []Block{nil}}}},
				"too many pages":    {Pages: big},
				"too many blocks":   {Pages: []Page{{Blocks: manyBlocks}}},
			}
			for name, spec := range cases {
				_, err := Create(spec)
				Expect(err).To(HaveOccurred(), "%s: Create succeeded, want an error", name)
			}
		})
	})

	Describe("Merge", func() {
		It("counts the pages of both parts", func() {
			a := mustCreate(onePageSpec(2, "a"))
			b := mustCreate(onePageSpec(3, "b"))
			m, err := Merge(a, b)
			Expect(err).ToNot(HaveOccurred())
			n, err := Pages(m)
			Expect(err).ToNot(HaveOccurred())
			Expect(n).To(Equal(5))
			Expect(Check(m)).To(Succeed())
			Expect(pageContent(m, 3)).To(ContainSubstring("b 1"), "merged page 3 is the first page of the second part")
		})
	})

	Describe("Split", func() {
		It("returns one page per part", func() {
			data := mustCreate(onePageSpec(3, "part"))
			parts, err := Split(data)
			Expect(err).ToNot(HaveOccurred())
			Expect(parts).To(HaveLen(3))
			for i, p := range parts {
				n, err := Pages(p)
				Expect(err).ToNot(HaveOccurred(), "part %d", i+1)
				Expect(n).To(Equal(1), "part %d", i+1)
				Expect(Check(p)).To(Succeed(), "part %d", i+1)
			}
			Expect(pageContent(parts[1], 1)).To(ContainSubstring("part 2"), "part 2 holds page 2")
		})
	})

	Describe("Rotate", func() {
		It("changes only the selected pages", func() {
			data := mustCreate(onePageSpec(2, "r"))
			out, err := Rotate(data, []int{1}, 90)
			Expect(err).ToNot(HaveOccurred())
			Expect(rotationOf(out, 1)).To(Equal(90))
			Expect(rotationOf(out, 2)).To(Equal(0))
			all, err := Rotate(data, nil, 270)
			Expect(err).ToNot(HaveOccurred())
			for p := 1; p <= 2; p++ {
				Expect(rotationOf(all, p)).To(Equal(270), "page %d rotation", p)
			}
		})

		It("rejects bad degrees and pages", func() {
			data := mustCreate(onePageSpec(2, "r"))
			cases := map[string]struct {
				pages   []int
				degrees int
			}{
				"degrees 45":    {nil, 45},
				"degrees 0":     {nil, 0},
				"degrees -90":   {nil, -90},
				"page 0":        {[]int{0}, 90},
				"page past end": {[]int{3}, 90},
				"page negative": {[]int{-1}, 90},
			}
			for name, c := range cases {
				_, err := Rotate(data, c.pages, c.degrees)
				Expect(err).To(HaveOccurred(), "%s: Rotate succeeded, want an error", name)
			}
		})
	})

	Describe("Watermark", func() {
		It("stamps every page", func() {
			data := mustCreate(onePageSpec(2, "w"))
			out, err := Watermark(data, "DRAFT")
			Expect(err).ToNot(HaveOccurred())
			Expect(Check(out)).To(Succeed())
			Expect(len(out)).To(BeNumerically(">", len(data)), "watermarked output is larger than the input")
		})

		It("rejects empty and non-Latin-1 text", func() {
			data := mustCreate(onePageSpec(1, "w"))
			_, err := Watermark(data, "")
			Expect(err).To(HaveOccurred(), "empty text")
			_, err = Watermark(data, "€")
			Expect(err).To(HaveOccurred(), "non-Latin-1 text")
		})
	})

	Describe("form fields", func() {
		It("lists the fields and fills them", func() {
			form := formPDF("customer", "notes")
			Expect(Check(form)).To(Succeed(), "fixture Check")
			fields, err := Fields(form)
			Expect(err).ToNot(HaveOccurred())
			Expect(fields).To(HaveLen(2))
			Expect(fields[0].Name).To(Equal("customer"))
			Expect(fields[1].Name).To(Equal("notes"))

			out, err := FillFields(form, map[string]string{"customer": "Ada Lovelace"})
			Expect(err).ToNot(HaveOccurred())
			Expect(Check(out)).To(Succeed(), "Check after fill")
			filled, err := Fields(out)
			Expect(err).ToNot(HaveOccurred())
			for _, f := range filled {
				if f.Name == "customer" {
					Expect(f.Value).To(Equal("Ada Lovelace"))
				}
				if f.Name == "notes" {
					Expect(f.Value).To(BeEmpty(), "notes stays untouched")
				}
			}
		})

		It("rejects unknown field names and empty values", func() {
			form := formPDF("customer")
			_, err := FillFields(form, map[string]string{"unknown": "x"})
			Expect(err).To(HaveOccurred(), "unknown field name")
			_, err = FillFields(form, nil)
			Expect(err).To(HaveOccurred(), "no values")
		})

		It("returns no fields for a PDF without a form", func() {
			fields, err := Fields(mustCreate(onePageSpec(1, "f")))
			Expect(err).ToNot(HaveOccurred())
			Expect(fields).To(BeEmpty())
		})
	})

	Describe("input checks", func() {
		It("returns an error for input that is not a PDF", func() {
			junk := []byte("this is not a pdf file")
			checks := map[string]func() error{
				"Check":      func() error { return Check(junk) },
				"Pages":      func() error { _, err := Pages(junk); return err },
				"Split":      func() error { _, err := Split(junk); return err },
				"Merge":      func() error { _, err := Merge(junk); return err },
				"Rotate":     func() error { _, err := Rotate(junk, nil, 90); return err },
				"Watermark":  func() error { _, err := Watermark(junk, "x"); return err },
				"Fields":     func() error { _, err := Fields(junk); return err },
				"FillFields": func() error { _, err := FillFields(junk, map[string]string{"a": "b"}); return err },
				"empty":      func() error { return Check(nil) },
			}
			for name, f := range checks {
				Expect(f()).To(HaveOccurred(), "%s: no error for bad input", name)
			}
		})

		It("returns an error for input over the limit", func() {
			big := make([]byte, MaxInputBytes+1)
			Expect(Check(big)).To(HaveOccurred())
		})

		// Cuts a good PDF at many lengths and calls every operation on each cut.
		// A truncated file may still be readable, because pdfcpu rebuilds a
		// broken cross-reference table, so only the absence of a panic is checked.
		It("does not panic on truncated prefixes", func() {
			good := mustCreate(sampleSpec())
			step := max(len(good)/150, 1)
			for n := 0; n <= len(good); n += step {
				p := good[:n]
				_ = Check(p)
				_, _ = Pages(p)
				_, _ = Split(p)
				_, _ = Merge(p, good)
				_, _ = Rotate(p, []int{1}, 90)
				_, _ = Watermark(p, "x")
				_, _ = Fields(p)
				_, _ = FillFields(p, map[string]string{"x": "y"})
			}
		})
	})
})

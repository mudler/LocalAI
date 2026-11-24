// SPDX-License-Identifier: MIT
package textinput_test

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/addons/office/internal/pdf"
	"github.com/mudler/LocalAI/addons/office/internal/textinput"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type layoutWord struct {
	Text string  `xml:",chardata"`
	XMin float64 `xml:"xMin,attr"`
	XMax float64 `xml:"xMax,attr"`
	YMin float64 `xml:"yMin,attr"`
	YMax float64 `xml:"yMax,attr"`
}
type layoutPage struct {
	Words []layoutWord `xml:"word"`
}

// These fixtures use only public renderer/parser APIs. Poppler measures the
// actual PDF glyph placement; no renderer layout helper is used as an oracle.
func inspectLayout(blocks []pdf.Block) ([]layoutPage, []byte) {
	data, err := pdf.Create(pdf.Spec{Pages: []pdf.Page{{Blocks: blocks}}})
	Expect(err).NotTo(HaveOccurred())
	dir, err := os.MkdirTemp("", "office-layout-")
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(os.RemoveAll, dir)
	path := filepath.Join(dir, "layout.pdf")
	Expect(os.WriteFile(path, data, 0600)).To(Succeed())
	out, err := exec.Command("pdftotext", "-bbox", path, "-").CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), string(out))
	var doc struct {
		Pages []layoutPage `xml:"body>doc>page"`
	}
	Expect(xml.Unmarshal(out, &doc)).To(Succeed())
	Expect(doc.Pages).NotTo(BeEmpty())
	for _, page := range doc.Pages {
		for _, w := range page.Words {
			Expect(w.XMin).To(BeNumerically(">=", 56.0), w.Text)
			Expect(w.XMax).To(BeNumerically("<=", 540.0), w.Text)
			Expect(w.YMin).To(BeNumerically(">=", 56.0), w.Text)
			Expect(w.YMax).To(BeNumerically("<=", 786.0), w.Text)
		}
	}
	return doc.Pages, data
}
func layoutText(p layoutPage) string {
	var words []string
	for _, w := range p.Words {
		words = append(words, w.Text)
	}
	return strings.Join(words, " ")
}

var _ = Describe("review: actual rich PDF pagination", func() {
	It("wraps mixed emphasis and long words across pages without text loss", func() {
		var source strings.Builder
		for i := 0; i < 180; i++ {
			fmt.Fprintf(&source, "regular%03d **bold%03d** *italic%03d* ", i, i, i)
		}
		source.WriteString("\n\n**" + strings.Repeat("W", 180) + "** tail")
		d, err := textinput.ParseMarkdown(context.Background(), source.String())
		Expect(err).NotTo(HaveOccurred())
		pages, data := inspectLayout(d.PDF)
		Expect(len(pages)).To(BeNumerically(">", 1))
		var text strings.Builder
		for _, p := range pages {
			text.WriteString(layoutText(p))
			text.WriteByte(' ')
		}
		for i := 0; i < 180; i++ {
			for _, prefix := range []string{"regular", "bold", "italic"} {
				Expect(strings.Count(text.String(), fmt.Sprintf("%s%03d", prefix, i))).To(Equal(1))
			}
		}
		Expect(strings.Count(text.String(), "W")).To(Equal(180))
		Expect(text.String()).To(ContainSubstring("tail"))
		Expect(string(data)).To(ContainSubstring("Helvetica-Bold"))
		Expect(string(data)).To(ContainSubstring("Helvetica-Oblique"))
	})
	It("repeats table headers and keeps each rich row together across pages", func() {
		var source strings.Builder
		source.WriteString("| **HeaderLeft** | *HeaderRight* |\n|---|---|\n")
		for i := 0; i < 100; i++ {
			fmt.Fprintf(&source, "| **left%03d** | *right%03d* |\n", i, i)
		}
		d, err := textinput.ParseMarkdown(context.Background(), source.String())
		Expect(err).NotTo(HaveOccurred())
		pages, _ := inspectLayout(d.PDF)
		Expect(len(pages)).To(BeNumerically(">", 2))
		counts := map[string]int{}
		for _, p := range pages {
			text := layoutText(p)
			Expect(strings.Count(text, "HeaderLeft")).To(Equal(1))
			Expect(strings.Count(text, "HeaderRight")).To(Equal(1))
			for _, w := range p.Words {
				counts[w.Text]++
			}
			for i := 0; i < 100; i++ {
				left, right := fmt.Sprintf("left%03d", i), fmt.Sprintf("right%03d", i)
				Expect(strings.Contains(text, left)).To(Equal(strings.Contains(text, right)))
			}
		}
		for i := 0; i < 100; i++ {
			Expect(counts[fmt.Sprintf("left%03d", i)]).To(Equal(1))
			Expect(counts[fmt.Sprintf("right%03d", i)]).To(Equal(1))
		}
	})
	It("moves a near-bottom multi-line row below a repeated header", func() {
		blocks := []pdf.Block{pdf.Paragraph{Runs: []pdf.Run{{Text: strings.Repeat("filler\n", 47) + "filler", Italic: true}}}, pdf.Table{Header: []string{"NearHeader", "OtherHeader"}, Rows: [][]string{{"rowtop\nrowmiddle\nrowbottom", "neighbor"}}}}
		pages, _ := inspectLayout(blocks)
		Expect(pages).To(HaveLen(2))
		Expect(layoutText(pages[0])).To(ContainSubstring("NearHeader"))
		Expect(layoutText(pages[0])).NotTo(ContainSubstring("rowtop"))
		Expect(layoutText(pages[1])).To(ContainSubstring("NearHeader"))
		for _, word := range []string{"rowtop", "rowmiddle", "rowbottom", "neighbor"} {
			Expect(layoutText(pages[1])).To(ContainSubstring(word))
		}
		var headerY, rowY float64
		for _, w := range pages[1].Words {
			if w.Text == "NearHeader" {
				headerY = w.YMax
			}
			if w.Text == "rowtop" {
				rowY = w.YMin
			}
		}
		Expect(rowY).To(BeNumerically(">", headerY))
	})
	It("rejects an oversized rich row with no partial PDF bytes", func() {
		data, err := pdf.Create(pdf.Spec{Pages: []pdf.Page{{Blocks: []pdf.Block{pdf.Table{Header: []string{"Header"}, Rows: [][]string{{""}}, RowRuns: [][][]pdf.Run{{{{Text: strings.Repeat("secret-row\n", 80), Bold: true}}}}}}}}})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("table row does not fit"))
		Expect(err.Error()).NotTo(ContainSubstring("secret-row"))
		Expect(data).To(BeEmpty())
	})
	It("keeps mixed-style cell glyphs inside their own column at wraps", func() {
		left := []pdf.Run{{Text: strings.Repeat("W", 35), Bold: true}, {Text: strings.Repeat("i", 35), Italic: true}, {Text: strings.Repeat("M", 35), Bold: true, Italic: true}}
		pages, data := inspectLayout([]pdf.Block{pdf.Table{Header: []string{"Left", "Right"}, Rows: [][]string{{"", "neighbor"}}, RowRuns: [][][]pdf.Run{{left, {{Text: "neighbor"}}}}}})
		Expect(pages).To(HaveLen(1))
		var body strings.Builder
		lines := map[float64]bool{}
		for _, w := range pages[0].Words {
			if strings.Trim(w.Text, "WiM") == "" {
				body.WriteString(w.Text)
				lines[w.YMin] = true
				Expect(w.XMin).To(BeNumerically(">=", 60.0))
				Expect(w.XMax).To(BeNumerically("<", 294.0))
			}
			if w.Text == "neighbor" {
				// A4 midpoint (105 mm) plus the explicit 1.5 mm cell padding.
				Expect(w.XMin).To(BeNumerically("~", 106.5*72/25.4, 0.1))
			}
		}
		Expect(strings.Count(body.String(), "W")).To(Equal(35))
		Expect(strings.Count(body.String(), "i")).To(Equal(35))
		Expect(strings.Count(body.String(), "M")).To(Equal(35))
		Expect(len(lines)).To(BeNumerically(">", 1))
		Expect(string(data)).To(ContainSubstring("Helvetica-BoldOblique"))
	})
})

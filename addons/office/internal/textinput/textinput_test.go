// SPDX-License-Identifier: MIT
package textinput_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/addons/office/internal/docx"
	"github.com/mudler/LocalAI/addons/office/internal/pdf"
	"github.com/mudler/LocalAI/addons/office/internal/textinput"
	"github.com/mudler/LocalAI/addons/office/internal/xlsx"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// cancelAfterChecks makes cancellation during conversion deterministic, without
// timing-sensitive sleeps or leaked goroutines.
type cancelAfterChecks struct {
	context.Context
	cancel    context.CancelFunc
	remaining int
}

func (c *cancelAfterChecks) Err() error {
	c.remaining--
	if c.remaining == 0 {
		c.cancel()
	}
	return c.Context.Err()
}

func nestedList(n int, emphasis bool) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		text := "x"
		if emphasis && i == n-1 {
			text = "*x*"
		}
		fmt.Fprintf(&b, "%s- %s\n", strings.Repeat("  ", i), text)
	}
	return b.String()
}

func parts(data []byte) map[string]string {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	Expect(err).NotTo(HaveOccurred())
	out := map[string]string{}
	for _, f := range z.File {
		r, e := f.Open()
		Expect(e).NotTo(HaveOccurred())
		b, e := io.ReadAll(r)
		Expect(e).NotTo(HaveOccurred())
		Expect(r.Close()).To(Succeed())
		out[f.Name] = string(b)
	}
	return out
}
func table(cols, rows int) string {
	line := "|" + strings.Repeat(" x |", cols) + "\n"
	return line + "|" + strings.Repeat("---|", cols) + "\n" + strings.Repeat(line, rows)
}
func csvGrid(rows, cols int) string {
	return strings.Repeat("x"+strings.Repeat(",", cols-1)+"\n", rows)
}

var _ = Describe("bounded Office text conversion", func() {
	It("renders actual headings, rich text, nested and ordered lists and tables in both files", func() {
		source := ""
		for i := 1; i <= 6; i++ {
			source += strings.Repeat("#", i) + fmt.Sprintf(" H%d *italic*\n\n", i)
		}
		source += "Body **strong** *slant* ***both*** [label](https://invalid.example/secret) `literal` &amp; \\*star\\*.\n\n3. **first**\n4. second\n   - *nested*\n\n- bullet\n\n| **Name** | Value |\n|---|---|\n| *cell* | **data** |\n\n```sh\nnot executed\n```\n"
		d, err := textinput.ParseMarkdown(context.Background(), source)
		Expect(err).NotTo(HaveOccurred())
		var out bytes.Buffer
		Expect(docx.Write(&out, docx.Document{Blocks: d.Docx})).To(Succeed())
		p := parts(out.Bytes())
		xml := p["word/document.xml"]
		for i := 1; i <= 6; i++ {
			Expect(xml).To(ContainSubstring(fmt.Sprintf(`w:val="Heading%d"`, i)))
		}
		for _, want := range []string{"<w:b/>", "<w:i/>", "<w:tbl>", "<w:numPr>", "nested", "literal", "not executed", "label", "&amp;", "*star*"} {
			Expect(xml).To(ContainSubstring(want))
		}
		Expect(xml).NotTo(ContainSubstring("invalid.example"))
		Expect(p["word/numbering.xml"]).To(ContainSubstring(`w:val="decimal"`))
		Expect(p["word/numbering.xml"]).To(ContainSubstring(`w:val="3"`))
		data, err := pdf.Create(pdf.Spec{Pages: []pdf.Page{{Blocks: d.PDF}}})
		Expect(err).NotTo(HaveOccurred())
		for _, font := range []string{"Helvetica-Bold", "Helvetica-Oblique", "Helvetica-BoldOblique"} {
			Expect(string(data)).To(ContainSubstring(font))
		}
		dir, err := os.MkdirTemp("", "office-text-output-")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(os.RemoveAll, dir)
		path := filepath.Join(dir, "out.pdf")
		Expect(os.WriteFile(path, data, 0600)).To(Succeed())
		cmd := exec.Command("pdftotext", "-layout", path, "-")
		extracted, err := cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(extracted))
		text := string(extracted)
		for _, want := range []string{"H1", "H6", "strong", "slant", "both", "3.", "4.", "first", "nested", "bullet", "Name", "Value", "cell", "data", "not executed"} {
			Expect(text).To(ContainSubstring(want))
		}
		Expect(text).To(MatchRegexp(`Name\s+Value`))
		Expect(text).To(MatchRegexp(`cell\s+data`))
		Expect(text).NotTo(ContainSubstring("invalid.example"))
	})
	It("keeps CSV strings, quoting, newlines and blank coordinates in actual XLSX", func() {
		w, e := textinput.ParseCSV(context.Background(), "00123,=x,+x,-x,@x,\r\n\"a,b\",\"say \"\"hi\"\"\",\"multi\r\nline\",,,end\r\n", "", "Title")
		Expect(e).NotTo(HaveOccurred())
		Expect(w.Sheets).To(HaveLen(1))
		Expect(w.Sheets[0].Name).To(Equal("Sheet1"))
		Expect(w.Sheets[0].HeaderRow).To(BeZero())
		Expect(w.Title).To(Equal("Title"))
		var out bytes.Buffer
		Expect(xlsx.Write(&out, w)).To(Succeed())
		p := parts(out.Bytes())
		sheet := p["xl/worksheets/sheet1.xml"]
		Expect(sheet).NotTo(ContainSubstring("<f"))
		Expect(sheet).NotTo(ContainSubstring(`r="F1"`))
		Expect(sheet).To(ContainSubstring(`r="F2"`))
		Expect(strings.Count(sheet, `t="s"`)).To(Equal(9))
		var shared struct {
			Items []struct {
				Text string `xml:"t"`
			} `xml:"si"`
		}
		Expect(xml.Unmarshal([]byte(p["xl/sharedStrings.xml"]), &shared)).To(Succeed())
		values := []string{}
		for _, item := range shared.Items {
			values = append(values, item.Text)
		}
		for _, s := range []string{"00123", "=x", "+x", "-x", "@x", "a,b", `say "hi"`, "multi\nline", "end"} {
			Expect(values).To(ContainElement(s))
		}
		Expect(sheet).NotTo(ContainSubstring(` s="1"`))
	})
	DescribeTable("rejects unsafe/unsupported Markdown without echo", func(s string) {
		_, e := textinput.ParseMarkdown(context.Background(), s)
		Expect(e).To(HaveOccurred())
		Expect(e.Error()).NotTo(ContainSubstring("PRIVATE"))
	},
		Entry("HTML block", "<div>PRIVATE</div>"), Entry("inline HTML", "a <b>PRIVATE</b>"), Entry("image", "![PRIVATE](x)"), Entry("blockquote", "> PRIVATE"), Entry("thematic break", "PRIVATE\n\n---\n"), Entry("include", "!include PRIVATE"), Entry("include directive", "{{< include PRIVATE >}}"), Entry("include split by emphasis", "!**include** PRIVATE"), Entry("include split by link", "{{[<](https://invalid.example) include PRIVATE >}}"), Entry("empty", " \n\t"), Entry("XML control", "PRIVATE\x00"), Entry("invalid UTF8", "PRIVATE\xff"), Entry("ragged table", "| a | b |\n|---|---|\n| PRIVATE |\n"))
	DescribeTable("rejects CSV safely", func(source, sheet, title string) {
		_, e := textinput.ParseCSV(context.Background(), source, sheet, title)
		Expect(e).To(HaveOccurred())
		Expect(e.Error()).NotTo(ContainSubstring("PRIVATE"))
	},
		Entry("empty", "", "", ""), Entry("all empty", ",\n,\n", "", ""), Entry("ragged", "a,b\nPRIVATE\n", "", ""), Entry("quotes", "\"PRIVATE", "", ""), Entry("bare quote", "PRI\"VATE", "", ""), Entry("long sheet", "a", strings.Repeat("x", 32), ""), Entry("bad sheet", "a", "PRIVATE/", ""), Entry("apostrophe", "a", "'PRIVATE", ""), Entry("sheet control", "a", "PRIVATE\x00", ""), Entry("title control", "a", "", "PRIVATE\x00"), Entry("cell control", "PRIVATE\x00", "", ""), Entry("invalid UTF8", "PRIVATE\xff", "", ""))
	It("accepts a 31-rune sheet and XML-safe Unicode metadata", func() {
		w, e := textinput.ParseCSV(context.Background(), "a", strings.Repeat("é", 31), "<&é")
		Expect(e).NotTo(HaveOccurred())
		var b bytes.Buffer
		Expect(xlsx.Write(&b, w)).To(Succeed())
	})
	It("checks cancellation before parsing", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, e := textinput.ParseMarkdown(ctx, "hello")
		Expect(e).To(MatchError(context.Canceled))
		_, e = textinput.ParseCSV(ctx, "hello", "", "")
		Expect(e).To(MatchError(context.Canceled))
	})
	DescribeTable("preserves cancellation during conversion", func(checks int) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		md, e := textinput.ParseMarkdown(&cancelAfterChecks{ctx, cancel, checks}, strings.Repeat("- x\n", 50))
		Expect(e).To(MatchError(context.Canceled))
		Expect(md.Docx).To(BeEmpty())
		Expect(md.PDF).To(BeEmpty())

		ctx, cancel = context.WithCancel(context.Background())
		defer cancel()
		wb, e := textinput.ParseCSV(&cancelAfterChecks{ctx, cancel, checks}, csvGrid(100, 2), "", "")
		Expect(e).To(MatchError(context.Canceled))
		Expect(wb.Sheets).To(BeEmpty())
	}, Entry("during traversal/records", 10), Entry("during mapping/cells", 150))
	DescribeTable("bounds full AST nesting at depth 64", func(levels int, emphasis, ok bool) {
		_, e := textinput.ParseMarkdown(context.Background(), nestedList(levels, emphasis))
		if ok {
			Expect(e).NotTo(HaveOccurred())
		} else {
			Expect(e).To(MatchError(ContainSubstring("nesting exceeds 64")))
		}
	}, Entry("below: depth 63", 30, false, true), Entry("at: depth 64", 30, true, true), Entry("above: depth 65", 31, false, false))
	DescribeTable("counts list children toward semantic blocks", func(total int, ok bool) {
		// Nine lists contribute 9*(1 list + 100 items + 100 text blocks).
		s := strings.Repeat(strings.Repeat("- x\n", 100)+"\nparagraph\n\n", 9)
		// Each separator paragraph contributes one additional block.
		s += strings.Repeat("x\n\n", total-9*202)
		_, e := textinput.ParseMarkdown(context.Background(), s)
		if ok {
			Expect(e).NotTo(HaveOccurred())
		} else {
			Expect(e).To(MatchError(ContainSubstring("semantic blocks exceed 2000")))
		}
	}, Entry("below", 1999, true), Entry("at", 2000, true), Entry("above", 2001, false))
	It("keeps include-looking code literal and links label-only", func() {
		for _, s := range []string{"`!include PRIVATE`", "`{{< include PRIVATE >}}`", "```\n!include PRIVATE\n{{% include PRIVATE %}}\n```", "[safe](file:///PRIVATE)", "[safe](https://invalid.example/!include)"} {
			_, e := textinput.ParseMarkdown(context.Background(), s)
			Expect(e).NotTo(HaveOccurred())
		}
	})
	It("preserves PDF encoding validation for every rich context", func() {
		for _, s := range []string{"**中文**", "# *中文*", "- **中文**", "| a |\n|---|\n| *中文* |"} {
			d, e := textinput.ParseMarkdown(context.Background(), s)
			Expect(e).NotTo(HaveOccurred())
			_, e = pdf.Create(pdf.Spec{Pages: []pdf.Page{{Blocks: d.PDF}}})
			Expect(e).To(HaveOccurred())
		}
	})
	DescribeTable("bounds Markdown bytes", func(n int, ok bool) {
		_, e := textinput.ParseMarkdown(context.Background(), strings.Repeat("x", n))
		if ok {
			Expect(e).NotTo(HaveOccurred())
		} else {
			Expect(e).To(HaveOccurred())
		}
	}, Entry("below", (1<<20)-1, true), Entry("at", 1<<20, true), Entry("above", (1<<20)+1, false))
	DescribeTable("bounds semantic blocks", func(n int, ok bool) {
		_, e := textinput.ParseMarkdown(context.Background(), strings.Repeat("x\n\n", n))
		if ok {
			Expect(e).NotTo(HaveOccurred())
		} else {
			Expect(e).To(HaveOccurred())
		}
	}, Entry("below", 1999, true), Entry("at", 2000, true), Entry("above", 2001, false))
	DescribeTable("bounds list items", func(n int, ok bool) {
		_, e := textinput.ParseMarkdown(context.Background(), strings.Repeat("- x\n", n))
		if ok {
			Expect(e).NotTo(HaveOccurred())
		} else {
			Expect(e).To(HaveOccurred())
		}
	}, Entry("below", 99, true), Entry("at", 100, true), Entry("above", 101, false))
	DescribeTable("bounds table columns and body rows", func(c, r int, ok bool) {
		_, e := textinput.ParseMarkdown(context.Background(), table(c, r))
		if ok {
			Expect(e).NotTo(HaveOccurred())
		} else {
			Expect(e).To(HaveOccurred())
		}
	}, Entry("columns below", 19, 1, true), Entry("columns at", 20, 1, true), Entry("columns above", 21, 1, false), Entry("rows below", 1, 499, true), Entry("rows at", 1, 500, true), Entry("rows above", 1, 501, false))
	DescribeTable("bounds CSV rows columns and grid including blanks", func(r, c int, ok bool) {
		s := csvGrid(r, c)
		Expect(len(s)).To(BeNumerically("<=", 1<<20))
		_, e := textinput.ParseCSV(context.Background(), s, "", "")
		if ok {
			Expect(e).NotTo(HaveOccurred())
		} else {
			Expect(e).To(HaveOccurred())
		}
	}, Entry("rows below", 4999, 1, true), Entry("rows at", 5000, 1, true), Entry("rows above", 5001, 1, false), Entry("columns below", 1, 199, true), Entry("columns at", 1, 200, true), Entry("columns above", 1, 201, false), Entry("grid below", 999, 100, true), Entry("grid at", 1000, 100, true), Entry("grid above", 1001, 100, false))
	DescribeTable("bounds CSV bytes", func(n int, ok bool) {
		_, e := textinput.ParseCSV(context.Background(), strings.Repeat("x", n), "", "")
		if ok {
			Expect(e).NotTo(HaveOccurred())
		} else {
			Expect(e).To(HaveOccurred())
		}
	}, Entry("below", (1<<20)-1, true), Entry("at", 1<<20, true), Entry("above", (1<<20)+1, false))
})

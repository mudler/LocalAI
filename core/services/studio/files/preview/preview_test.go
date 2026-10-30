package preview_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/studio/files/preview"
)

func build(name string, data []byte) (schema.ChatsFilePreview, error) {
	return preview.Build(context.Background(), name, bytes.NewReader(data), int64(len(data)))
}

func mustBuild(name string, data []byte) schema.ChatsFilePreview {
	out, err := build(name, data)
	Expect(err).NotTo(HaveOccurred())
	return out
}

// cell writes one <c> element.
func cell(ref, typ, style, inner string) string {
	attrs := ` r="` + ref + `"`
	if typ != "" {
		attrs += ` t="` + typ + `"`
	}
	if style != "" {
		attrs += ` s="` + style + `"`
	}
	return `<c` + attrs + `>` + inner + `</c>`
}

func row(n int, cells ...string) string {
	return fmt.Sprintf(`<row r="%d">`, n) + strings.Join(cells, "") + `</row>`
}

const dateStyles = `<numFmts count="1"><numFmt numFmtId="164" formatCode="yyyy-mm-dd hh:mm"/></numFmts>` +
	`<cellXfs count="4"><xf numFmtId="0"/><xf numFmtId="14"/><xf numFmtId="164"/><xf numFmtId="2"/></cellXfs>`

var _ = Describe("Supported", func() {
	It("names the types that have a server preview, in any case", func() {
		for _, n := range []string{"a.xlsx", "B.XLSM", "c.csv", "d.tsv", "e.docx", "f.pptx"} {
			Expect(preview.Supported(n)).To(BeTrue(), n)
		}
		for _, n := range []string{"a.xls", "b.doc", "c.pdf", "d.txt", "xlsx", ""} {
			Expect(preview.Supported(n)).To(BeFalse(), n)
		}
	})
})

var _ = Describe("Spreadsheets", func() {
	It("reads every sheet in workbook order with shared strings, numbers, booleans, errors and formulas", func() {
		data := workbook(
			[]string{"Item", "Cost", `<r><t>Rich </t></r><r><t>text</t></r>`, "Total"},
			dateStyles, false,
			sheetDef{"Budget", row(1, cell("A1", "s", "", "<v>0</v>"), cell("B1", "s", "", "<v>1</v>")) +
				row(2, cell("A2", "s", "", "<v>2</v>"), cell("B2", "", "", "<v>0.30000000000000004</v>")) +
				row(3, cell("A3", "inlineStr", "", "<is><t>Inline</t></is>"), cell("B3", "", "", "<v>1234567</v>"), cell("C3", "b", "", "<v>1</v>")) +
				row(4, cell("A4", "s", "", "<v>3</v>"), cell("B4", "", "", "<f>SUM(B2:B3)</f><v>1234567.3</v>"), cell("C4", "str", "", "<f>A1&amp;\"!\"</f><v>Item!</v>"), cell("D4", "e", "", "<v>#DIV/0!</v>"))},
			sheetDef{"Notes", row(1, cell("A1", "", "", "<v>1E-3</v>"), cell("C1", "b", "", "<v>0</v>"))},
		)
		out := mustBuild("budget.xlsx", data)
		Expect(out.Kind).To(Equal("table"))
		Expect(out.Sheets).To(HaveLen(2))
		Expect(out.Sheets[0].Name).To(Equal("Budget"))
		Expect(out.Sheets[0].Rows).To(Equal([][]string{
			{"Item", "Cost", "", ""},
			{"Rich text", "0.3", "", ""},
			{"Inline", "1234567", "TRUE", ""},
			{"Total", "1234567.3", "Item!", "#DIV/0!"},
		}))
		Expect(out.Sheets[0].TotalRows).To(Equal(4))
		Expect(out.Sheets[0].TotalCols).To(Equal(4))
		Expect(out.Sheets[1].Name).To(Equal("Notes"))
		Expect(out.Sheets[1].Rows).To(Equal([][]string{{"0.001", "", "FALSE"}}))
	})

	It("shows dates and times from the cell style, in the 1900 and the 1904 systems", func() {
		data := workbook(nil, dateStyles, false, sheetDef{"S", row(1,
			cell("A1", "", "1", "<v>45292</v>"),
			cell("B1", "", "2", "<v>45292.5</v>"),
			cell("C1", "", "3", "<v>45292</v>"),
			cell("D1", "", "1", "<v>not a number</v>"),
		)})
		Expect(mustBuild("d.xlsx", data).Sheets[0].Rows).To(Equal([][]string{{"2024-01-01", "2024-01-01 12:00", "45292", "not a number"}}))

		data = workbook(nil, dateStyles, true, sheetDef{"S", row(1, cell("A1", "", "1", "<v>43830</v>"))})
		Expect(mustBuild("d.xlsm", data).Sheets[0].Rows).To(Equal([][]string{{"2024-01-01"}}))
	})

	It("keeps the row numbers of a sheet with gaps", func() {
		data := workbook(nil, "", false, sheetDef{"S", row(1, cell("A1", "", "", "<v>1</v>")) + row(3, cell("B3", "", "", "<v>3</v>"))})
		out := mustBuild("g.xlsx", data)
		Expect(out.Sheets[0].Rows).To(Equal([][]string{{"1", ""}, {"", ""}, {"", "3"}}))
		Expect(out.Sheets[0].TotalRows).To(Equal(3))
	})

	It("gives an empty sheet no rows", func() {
		out := mustBuild("e.xlsx", workbook(nil, "", false, sheetDef{"Empty", ""}, sheetDef{"One", row(1, cell("A1", "", "", "<v>1</v>"))}))
		Expect(out.Sheets[0].Name).To(Equal("Empty"))
		Expect(out.Sheets[0].Rows).To(BeEmpty())
		Expect(out.Sheets[0].Rows).NotTo(BeNil(), "an empty list, not null")
		Expect(out.Sheets[0].TotalRows).To(BeZero())
		Expect(out.Sheets[0].TotalCols).To(BeZero())
	})

	It("keeps the first 200 rows and 30 columns of a huge sheet and counts the rest", func() {
		var sb strings.Builder
		for r := 1; r <= 500; r++ {
			cells := []string{}
			for c := range 40 {
				cells = append(cells, cell(colName(c)+fmt.Sprint(r), "", "", fmt.Sprintf("<v>%d</v>", r*100+c)))
			}
			sb.WriteString(row(r, cells...))
		}
		out := mustBuild("big.xlsx", workbook(nil, "", false, sheetDef{"Big", sb.String()}))
		s := out.Sheets[0]
		Expect(s.Rows).To(HaveLen(200))
		for _, r := range s.Rows {
			Expect(r).To(HaveLen(30))
		}
		Expect(s.Rows[199][29]).To(Equal("20029"))
		Expect(s.TotalRows).To(Equal(500))
		Expect(s.TotalCols).To(Equal(40))
	})

	It("refuses a package that is not a workbook", func() {
		_, err := build("x.xlsx", []byte("not a zip"))
		Expect(err).To(MatchError(preview.ErrInvalid))
		_, err = build("x.xlsx", zipOf("a.txt", "hello"))
		Expect(err).To(MatchError(preview.ErrInvalid))
	})

	It("stops at a part that unpacks past the limit", func() {
		huge := `<?xml version="1.0"?><sst ` + nsMain + `>` + strings.Repeat("<si><t>aaaaaaaaaaaaaaaa</t></si>", 1<<16) + `</sst>`
		data := zipOf("xl/workbook.xml", `<workbook `+nsMain+` `+nsRel+`><sheets><sheet name="S" r:id="rId1"/></sheets></workbook>`,
			"xl/_rels/workbook.xml.rels", `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>`,
			"xl/worksheets/sheet1.xml", `<worksheet `+nsMain+`><sheetData/></worksheet>`,
			"xl/sharedStrings.xml", huge)
		_, err := preview.BuildWith(context.Background(), "x.xlsx", bytes.NewReader(data), int64(len(data)), preview.Limits{Rows: 200, Cols: 30, PartBytes: 1 << 20, Text: 1000})
		Expect(err).To(MatchError(preview.ErrTooLarge))
	})

	It("stops when the context ends", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		data := workbook(nil, "", false, sheetDef{"S", row(1, cell("A1", "", "", "<v>1</v>"))})
		_, err := preview.Build(ctx, "x.xlsx", bytes.NewReader(data), int64(len(data)))
		Expect(err).To(MatchError(context.Canceled))
	})
})

func colName(i int) string {
	s := ""
	for i++; i > 0; i = (i - 1) / 26 {
		s = string(rune('A'+(i-1)%26)) + s
	}
	return s
}

var _ = Describe("CSV and TSV", func() {
	It("reads quotes, embedded newlines, ragged rows and a byte order mark", func() {
		data := []byte("\xef\xbb\xbfname,note\n\"Smith, J\",\"said \"\"hi\"\"\"\n\"two\nlines\",x,extra\nshort\n")
		out := mustBuild("people.csv", data)
		Expect(out.Kind).To(Equal("table"))
		Expect(out.Sheets).To(HaveLen(1))
		s := out.Sheets[0]
		Expect(s.Name).To(Equal("people.csv"))
		Expect(s.Rows).To(Equal([][]string{
			{"name", "note"},
			{"Smith, J", `said "hi"`},
			{"two\nlines", "x", "extra"},
			{"short"},
		}))
		Expect(s.TotalRows).To(Equal(4))
		Expect(s.TotalCols).To(Equal(3))
	})

	It("reads a stray quote leniently and splits a TSV on tabs", func() {
		out := mustBuild("a.tsv", []byte("a\tb \"c\nd\te\n"))
		Expect(out.Sheets[0].Rows).To(Equal([][]string{{"a", `b "c`}, {"d", "e"}}))
	})

	It("keeps the first 200 rows and 30 columns and counts the rest", func() {
		var sb strings.Builder
		for r := range 450 {
			cols := make([]string, 35)
			for c := range cols {
				cols[c] = fmt.Sprint(r, "-", c)
			}
			sb.WriteString(strings.Join(cols, ",") + "\n")
		}
		s := mustBuild("big.csv", []byte(sb.String())).Sheets[0]
		Expect(s.Rows).To(HaveLen(200))
		Expect(s.Rows[0]).To(HaveLen(30))
		Expect(s.TotalRows).To(Equal(450))
		Expect(s.TotalCols).To(Equal(35))
	})

	It("gives an empty file an empty sheet", func() {
		s := mustBuild("e.csv", nil).Sheets[0]
		Expect(s.Rows).To(BeEmpty())
		Expect(s.TotalRows).To(BeZero())
	})
})

var _ = Describe("Word documents", func() {
	p := func(style, inner string) string {
		pr := ""
		if style != "" {
			pr = `<w:pPr><w:pStyle w:val="` + style + `"/></w:pPr>`
		}
		return `<w:p>` + pr + inner + `</w:p>`
	}
	r := func(t string) string { return `<w:r><w:t xml:space="preserve">` + t + `</w:t></w:r>` }

	It("gives headings, list items and paragraphs, in order", func() {
		styles := `<w:style w:type="paragraph" w:styleId="berschrift2"><w:name w:val="heading 2"/></w:style>` +
			`<w:style w:type="paragraph" w:styleId="Titel"><w:name w:val="Title"/></w:style>` +
			`<w:style w:type="paragraph" w:styleId="ListBullet"><w:name w:val="List Bullet"/><w:pPr><w:numPr><w:numId w:val="1"/></w:numPr></w:pPr></w:style>`
		body := p("Titel", r("Quarterly report")) +
			p("Heading1", r("Summary")) +
			p("", r("Revenue ")+r("grew.")) +
			p("", "") +
			`<w:p><w:pPr><w:numPr><w:ilvl w:val="1"/><w:numId w:val="3"/></w:numPr></w:pPr>` + r("Second level") + `</w:p>` +
			p("ListBullet", r("Styled item")) +
			p("berschrift2", r("Details")) +
			p("", `<w:r><w:t>a</w:t><w:tab/><w:t>b</w:t><w:br/><w:t>c</w:t></w:r>`) +
			`<w:tbl><w:tr><w:tc>` + p("", r("cell one")) + `</w:tc><w:tc>` + p("", r("cell two")) + `</w:tc></w:tr></w:tbl>`
		out := mustBuild("report.docx", docx(body, styles))
		Expect(out.Kind).To(Equal("document"))
		Expect(out.Truncated).To(BeFalse())
		Expect(out.Blocks).To(Equal([]schema.ChatsFilePreviewBlock{
			{Type: "heading", Level: 1, Text: "Quarterly report"},
			{Type: "heading", Level: 1, Text: "Summary"},
			{Type: "paragraph", Text: "Revenue grew."},
			{Type: "item", Level: 1, Text: "Second level"},
			{Type: "item", Text: "Styled item"},
			{Type: "heading", Level: 2, Text: "Details"},
			{Type: "paragraph", Text: "a\tb\nc"},
			{Type: "paragraph", Text: "cell one"},
			{Type: "paragraph", Text: "cell two"},
		}))
	})

	It("cuts a long document and says so", func() {
		body := strings.Repeat(p("", r(strings.Repeat("x", 100))), 50)
		data := docx(body, "")
		out, err := preview.BuildWith(context.Background(), "long.docx", bytes.NewReader(data), int64(len(data)), preview.Limits{Rows: 200, Cols: 30, PartBytes: 64 << 20, Text: 1000})
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Truncated).To(BeTrue())
		Expect(len(out.Blocks)).To(BeNumerically("<=", 11))
	})

	It("refuses a package with no document", func() {
		_, err := build("x.docx", zipOf("word/other.xml", "<a/>"))
		Expect(err).To(MatchError(preview.ErrInvalid))
	})
})

var _ = Describe("Presentations", func() {
	It("gives each slide's title, bullet text with levels and speaker notes, in presentation order", func() {
		data := pptx(
			slideDef{shapes: shape("ctrTitle", para("Launch plan", 0)) + shape("subTitle", para("October", 0))},
			slideDef{
				shapes: shape("title", para("Timeline", 0)) + shape("body", para("Design", 0), para("Review", 1), `<a:p></a:p>`) + shape("", para("Free text box", 0)),
				notes:  "Mention the date",
			},
		)
		out := mustBuild("deck.pptx", data)
		Expect(out.Kind).To(Equal("slides"))
		Expect(out.Slides).To(Equal([]schema.ChatsFilePreviewSlide{
			{N: 1, Title: "Launch plan", Lines: []schema.ChatsFilePreviewLine{{Text: "October"}}},
			{N: 2, Title: "Timeline", Lines: []schema.ChatsFilePreviewLine{{Text: "Design"}, {Text: "Review", Level: 1}, {Text: "Free text box"}}, Notes: "Mention the date"},
		}))
	})

	It("refuses a package with no presentation", func() {
		_, err := build("x.pptx", zipOf("ppt/slides/slide1.xml", "<a/>"))
		Expect(err).To(MatchError(preview.ErrInvalid))
	})
})

var _ = Describe("Build", func() {
	It("refuses a type it does not read", func() {
		_, err := build("a.pdf", []byte("%PDF"))
		Expect(err).To(MatchError(preview.ErrUnsupported))
	})
})

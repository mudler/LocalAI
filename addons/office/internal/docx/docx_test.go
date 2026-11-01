package docx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"path"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// attrVal reads the w:val attribute of an element.
type attrVal struct {
	V string `xml:"val,attr"`
}

type xRun struct {
	Bold   *struct{} `xml:"rPr>b"`
	Italic *struct{} `xml:"rPr>i"`
	Text   string    `xml:"t"`
}

type xPara struct {
	Style attrVal `xml:"pPr>pStyle"`
	NumID attrVal `xml:"pPr>numPr>numId"`
	Runs  []xRun  `xml:"r"`
}

type xCell struct {
	Width struct {
		W string `xml:"w,attr"`
	} `xml:"tcPr>tcW"`
	Fill struct {
		F string `xml:"fill,attr"`
	} `xml:"tcPr>shd"`
	Paras []xPara `xml:"p"`
}

type xTable struct {
	Grid []struct {
		W string `xml:"w,attr"`
	} `xml:"tblGrid>gridCol"`
	Rows []struct {
		Cells []xCell `xml:"tc"`
	} `xml:"tr"`
}

// element is one child of w:body, in document order.
type element struct {
	kind  string // "p", "tbl" or "sectPr"
	para  xPara
	table xTable
}

func writeDoc(d Document) map[string][]byte {
	GinkgoHelper()
	var buf bytes.Buffer
	Expect(Write(&buf, d)).To(Succeed())
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	Expect(err).ToNot(HaveOccurred(), "not a zip")
	files := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		Expect(err).ToNot(HaveOccurred())
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		Expect(err).ToNot(HaveOccurred())
		files[f.Name] = data
	}
	return files
}

func body(files map[string][]byte) []element {
	GinkgoHelper()
	dec := xml.NewDecoder(bytes.NewReader(files["word/document.xml"]))
	var out []element
	depth := 0
	inBody := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		Expect(err).ToNot(HaveOccurred(), "document.xml")
		se, ok := tok.(xml.StartElement)
		if !ok {
			if _, end := tok.(xml.EndElement); end {
				depth--
			}
			continue
		}
		depth++
		if se.Name.Local == "body" {
			inBody = true
			continue
		}
		if !inBody || depth != 3 {
			continue
		}
		e := element{kind: se.Name.Local}
		switch se.Name.Local {
		case "p":
			Expect(dec.DecodeElement(&e.para, &se)).To(Succeed())
		case "tbl":
			Expect(dec.DecodeElement(&e.table, &se)).To(Succeed())
		default:
			Expect(dec.Skip()).To(Succeed())
		}
		depth--
		out = append(out, e)
	}
	return out
}

func paraText(p xPara) string {
	var s string
	for _, r := range p.Runs {
		s += r.Text
	}
	return s
}

func sampleDoc() Document {
	return Document{
		Title:  "Quarterly report",
		Author: "A. Writer",
		Style:  Style{Font: "Georgia", BodySize: 24, HeadingColor: "1f2937", TableHeaderFill: "dbeafe"},
		Blocks: []Block{
			Heading{Level: 1, Text: "Overview"},
			Paragraph{Text: "Plain text."},
			Heading{Level: 2, Text: "Details"},
			Paragraph{Runs: []Run{{Text: "bold", Bold: true}, {Text: " and "}, {Text: "italic", Italic: true}}},
			Bullets{Items: []string{"one", "two", "three"}},
			Heading{Level: 3, Text: "Numbers"},
			Table{
				Header: []string{"Name", "Count"},
				Rows:   [][]string{{"a", "1"}, {"b", "2"}},
				Widths: []int{3000, 1500},
			},
		},
	}
}

var _ = Describe("docx writer", func() {
	It("writes well-formed parts with consistent content types and relationships", func() {
		files := writeDoc(sampleDoc())
		for _, name := range []string{
			"[Content_Types].xml", "_rels/.rels", "word/document.xml", "word/styles.xml",
			"word/numbering.xml", "word/_rels/document.xml.rels", "docProps/core.xml", "docProps/app.xml",
		} {
			data, ok := files[name]
			Expect(ok).To(BeTrue(), "missing part %s", name)
			Expect(xml.Unmarshal(data, new(struct{}))).To(Succeed(), "%s is not well-formed XML", name)
		}

		var ct struct {
			Overrides []struct {
				PartName    string `xml:"PartName,attr"`
				ContentType string `xml:"ContentType,attr"`
			} `xml:"Override"`
		}
		Expect(xml.Unmarshal(files["[Content_Types].xml"], &ct)).To(Succeed())
		types := map[string]string{}
		for _, o := range ct.Overrides {
			types[o.PartName] = o.ContentType
			Expect(files).To(HaveKey(strings.TrimPrefix(o.PartName, "/")), "content type override for missing part %s", o.PartName)
		}
		for _, name := range []string{"word/document.xml", "word/styles.xml", "word/numbering.xml", "docProps/core.xml", "docProps/app.xml"} {
			Expect(types["/"+name]).ToNot(BeEmpty(), "[Content_Types].xml does not list %s", name)
		}
		Expect(types["/word/document.xml"]).To(ContainSubstring("wordprocessingml.document.main+xml"))

		checkRels := func(relsName, baseDir string) {
			var rels struct {
				Rels []struct {
					ID     string `xml:"Id,attr"`
					Target string `xml:"Target,attr"`
				} `xml:"Relationship"`
			}
			Expect(xml.Unmarshal(files[relsName], &rels)).To(Succeed())
			Expect(rels.Rels).ToNot(BeEmpty(), "%s has no relationship", relsName)
			ids := map[string]bool{}
			for _, r := range rels.Rels {
				Expect(ids[r.ID]).To(BeFalse(), "%s: duplicate id %s", relsName, r.ID)
				ids[r.ID] = true
				Expect(files).To(HaveKey(path.Join(baseDir, r.Target)), "%s: target %s does not exist", relsName, r.Target)
			}
		}
		checkRels("_rels/.rels", "")
		checkRels("word/_rels/document.xml.rels", "word")
	})

	It("writes no numbering part without a bullet list", func() {
		files := writeDoc(Document{Blocks: []Block{Paragraph{Text: "x"}}})
		Expect(files).ToNot(HaveKey("word/numbering.xml"))
		Expect(files["[Content_Types].xml"]).ToNot(ContainSubstring("numbering"))
		Expect(files["word/_rels/document.xml.rels"]).ToNot(ContainSubstring("numbering"))
	})

	It("writes the blocks in document order", func() {
		els := body(writeDoc(sampleDoc()))
		type want struct{ kind, style, text string }
		wants := []want{
			{"p", "Heading1", "Overview"},
			{"p", "", "Plain text."},
			{"p", "Heading2", "Details"},
			{"p", "", "bold and italic"},
			{"p", "ListBullet", "one"},
			{"p", "ListBullet", "two"},
			{"p", "ListBullet", "three"},
			{"p", "Heading3", "Numbers"},
			{"tbl", "", ""},
			{"p", "", ""}, // spacer after the table
			{"sectPr", "", ""},
		}
		Expect(els).To(HaveLen(len(wants)))
		for i, w := range wants {
			e := els[i]
			Expect(e.kind).To(Equal(w.kind), "element %d", i)
			if e.kind == "p" {
				Expect(e.para.Style.V).To(Equal(w.style), "element %d style", i)
				Expect(paraText(e.para)).To(Equal(w.text), "element %d text", i)
			}
		}
	})

	It("marks bold and italic runs", func() {
		els := body(writeDoc(sampleDoc()))
		runs := els[3].para.Runs
		Expect(runs).To(HaveLen(3))
		Expect(runs[0].Bold).ToNot(BeNil(), "run 0 should be bold only")
		Expect(runs[0].Italic).To(BeNil(), "run 0 should be bold only")
		Expect(runs[1].Bold).To(BeNil(), "run 1 should be plain")
		Expect(runs[1].Italic).To(BeNil(), "run 1 should be plain")
		Expect(runs[2].Bold).To(BeNil(), "run 2 should be italic only")
		Expect(runs[2].Italic).ToNot(BeNil(), "run 2 should be italic only")
	})

	It("ties bullets to a bullet numbering definition", func() {
		files := writeDoc(sampleDoc())
		var num struct {
			Abstract []struct {
				ID     string  `xml:"abstractNumId,attr"`
				Format attrVal `xml:"lvl>numFmt"`
			} `xml:"abstractNum"`
			Nums []struct {
				ID       string  `xml:"numId,attr"`
				Abstract attrVal `xml:"abstractNumId"`
			} `xml:"num"`
		}
		Expect(xml.Unmarshal(files["word/numbering.xml"], &num)).To(Succeed())
		Expect(num.Nums).To(HaveLen(1))
		Expect(num.Abstract).To(HaveLen(1))
		Expect(num.Abstract[0].Format.V).To(Equal("bullet"))
		Expect(num.Nums[0].Abstract.V).To(Equal(num.Abstract[0].ID), "num does not refer to the abstract definition")
		count := 0
		for _, e := range body(files) {
			if e.kind == "p" && e.para.Style.V == "ListBullet" {
				count++
				Expect(e.para.NumID.V).To(Equal(num.Nums[0].ID))
			}
		}
		Expect(count).To(Equal(3))
	})

	It("writes a table with header emphasis, fill, grid and widths", func() {
		els := body(writeDoc(sampleDoc()))
		tbl := els[8].table
		Expect(tbl.Rows).To(HaveLen(3))
		var got [][]string
		for _, r := range tbl.Rows {
			var row []string
			for _, c := range r.Cells {
				row = append(row, paraText(c.Paras[0]))
			}
			got = append(got, row)
		}
		want := [][]string{{"Name", "Count"}, {"a", "1"}, {"b", "2"}}
		Expect(got).To(Equal(want))
		Expect(tbl.Rows[0].Cells[0].Paras[0].Runs[0].Bold).ToNot(BeNil(), "header cell is not bold")
		Expect(tbl.Rows[1].Cells[0].Paras[0].Runs[0].Bold).To(BeNil(), "body cell is bold")
		Expect(tbl.Rows[0].Cells[0].Fill.F).To(Equal("DBEAFE"))
		Expect(tbl.Rows[0].Cells[1].Fill.F).To(Equal("DBEAFE"))
		Expect(tbl.Rows[1].Cells[0].Fill.F).To(BeEmpty(), "body cell has a fill")
		Expect(tbl.Grid).To(HaveLen(2))
		Expect(tbl.Grid[0].W).To(Equal("3000"))
		Expect(tbl.Grid[1].W).To(Equal("1500"))
		Expect(tbl.Rows[1].Cells[1].Width.W).To(Equal("1500"))
	})

	It("gives table columns an equal share of the width by default", func() {
		d := Document{Blocks: []Block{Table{Header: []string{"a", "b", "c"}}}}
		tbl := body(writeDoc(d))[0].table
		Expect(tbl.Grid).To(HaveLen(3))
		for _, g := range tbl.Grid {
			Expect(g.W).To(Equal(tbl.Grid[0].W), "grid widths are not an equal share: %+v", tbl.Grid)
			Expect(g.W).ToNot(Equal("0"))
			Expect(g.W).ToNot(BeEmpty())
		}
	})

	It("writes the style values into styles.xml", func() {
		files := writeDoc(sampleDoc())
		var st struct {
			Font struct {
				A string `xml:"ascii,attr"`
			} `xml:"docDefaults>rPrDefault>rPr>rFonts"`
			Size   attrVal `xml:"docDefaults>rPrDefault>rPr>sz"`
			Styles []struct {
				ID    string  `xml:"styleId,attr"`
				Color attrVal `xml:"rPr>color"`
			} `xml:"style"`
		}
		Expect(xml.Unmarshal(files["word/styles.xml"], &st)).To(Succeed())
		Expect(st.Font.A).To(Equal("Georgia"))
		Expect(st.Size.V).To(Equal("24"))
		colours := map[string]string{}
		for _, s := range st.Styles {
			colours[s.ID] = s.Color.V
		}
		for _, id := range []string{"Heading1", "Heading2", "Heading3"} {
			Expect(colours[id]).To(Equal("1F2937"), id)
		}
		for _, id := range []string{"Normal", "ListBullet", "TableGrid"} {
			Expect(colours).To(HaveKey(id), "styles.xml has no %s style", id)
		}
		Expect(files["word/document.xml"]).To(ContainSubstring(`w:fill="DBEAFE"`), "document.xml has no header fill")
	})

	It("uses default style values when none are set", func() {
		files := writeDoc(Document{Blocks: []Block{
			Heading{Level: 1, Text: "h"},
			Table{Header: []string{"a"}},
		}})
		s := string(files["word/styles.xml"])
		for _, want := range []string{`w:ascii="Calibri"`, `<w:sz w:val="22"/>`, `<w:color w:val="000000"/>`} {
			Expect(s).To(ContainSubstring(want), "styles.xml lacks %s", want)
		}
		Expect(files["word/document.xml"]).ToNot(ContainSubstring("w:fill="), "header fill written although none was set")
	})

	It("names an application only when the caller sets one", func() {
		files := writeDoc(Document{})
		Expect(files["docProps/app.xml"]).ToNot(ContainSubstring("Application"))
		files = writeDoc(Document{Application: `Tool <1> & "co"`})
		var app struct {
			Application string `xml:"Application"`
		}
		Expect(xml.Unmarshal(files["docProps/app.xml"], &app)).To(Succeed())
		Expect(app.Application).To(Equal(`Tool <1> & "co"`))
	})

	It("writes the title and author into the core properties", func() {
		files := writeDoc(Document{Title: `A & B`, Author: `"Me" <x>`})
		var core struct {
			Title   string `xml:"title"`
			Creator string `xml:"creator"`
		}
		Expect(xml.Unmarshal(files["docProps/core.xml"], &core)).To(Succeed())
		Expect(core.Title).To(Equal(`A & B`))
		Expect(core.Creator).To(Equal(`"Me" <x>`))
	})

	DescribeTable("rejecting an invalid document",
		func(doc Document) {
			var buf bytes.Buffer
			err := Write(&buf, doc)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(HavePrefix("docx: "), "error lacks the package prefix")
			Expect(buf.Len()).To(BeZero(), "output written although validation failed")
		},
		Entry("empty heading", Document{Blocks: []Block{Heading{Level: 1, Text: ""}}}),
		Entry("blank heading", Document{Blocks: []Block{Heading{Level: 1, Text: "  "}}}),
		Entry("level 0", Document{Blocks: []Block{Heading{Level: 0, Text: "x"}}}),
		Entry("level 4", Document{Blocks: []Block{Heading{Level: 4, Text: "x"}}}),
		Entry("bad heading colour", Document{Style: Style{HeadingColor: "12345"}, Blocks: nil}),
		Entry("colour with hash", Document{Style: Style{HeadingColor: "#112233"}}),
		Entry("non-hex colour", Document{Style: Style{HeadingColor: "GG0000"}}),
		Entry("bad fill", Document{Style: Style{TableHeaderFill: "xyz"}}),
		Entry("negative body size", Document{Style: Style{BodySize: -2}}),
		Entry("ragged row", Document{Blocks: []Block{Table{Header: []string{"a", "b"}, Rows: [][]string{{"1", "2"}, {"3"}}}}}),
		Entry("no header", Document{Blocks: []Block{Table{Rows: [][]string{{"1"}}}}}),
		Entry("negative width", Document{Blocks: []Block{Table{Header: []string{"a", "b"}, Widths: []int{100, -1}}}}),
		Entry("width count", Document{Blocks: []Block{Table{Header: []string{"a", "b"}, Widths: []int{100}}}}),
		Entry("empty bullets", Document{Blocks: []Block{Bullets{}}}),
		Entry("empty bullet item", Document{Blocks: []Block{Bullets{Items: []string{"a", ""}}}}),
		Entry("text and runs", Document{Blocks: []Block{Paragraph{Text: "a", Runs: []Run{{Text: "b"}}}}}),
		Entry("nil block", Document{Blocks: []Block{nil}}),
	)

	It("escapes special characters everywhere they are written", func() {
		nasty := `a < b && c > "d" 'e'`
		d := Document{
			Title: nasty,
			Style: Style{Font: `Font "X" & <Y>`},
			Blocks: []Block{
				Heading{Level: 1, Text: nasty},
				Paragraph{Text: nasty},
				Paragraph{Runs: []Run{{Text: nasty, Bold: true}}},
				Bullets{Items: []string{nasty}},
				Table{Header: []string{nasty}, Rows: [][]string{{nasty}}},
			},
		}
		files := writeDoc(d)
		for name, data := range files {
			Expect(xml.Unmarshal(data, new(struct{}))).To(Succeed(), name)
		}
		els := body(files)
		for i := range 4 {
			Expect(paraText(els[i].para)).To(Equal(nasty), "element %d text", i)
		}
		tbl := els[4].table
		Expect(paraText(tbl.Rows[0].Cells[0].Paras[0])).To(Equal(nasty), "table cell text did not round-trip")
		Expect(paraText(tbl.Rows[1].Cells[0].Paras[0])).To(Equal(nasty), "table cell text did not round-trip")
		var st struct {
			Font struct {
				A string `xml:"ascii,attr"`
			} `xml:"docDefaults>rPrDefault>rPr>rFonts"`
		}
		Expect(xml.Unmarshal(files["word/styles.xml"], &st)).To(Succeed())
		Expect(st.Font.A).To(Equal(`Font "X" & <Y>`))
	})

	It("writes only the section properties for an empty document", func() {
		els := body(writeDoc(Document{}))
		Expect(els).To(HaveLen(1))
		Expect(els[0].kind).To(Equal("sectPr"))
	})
})

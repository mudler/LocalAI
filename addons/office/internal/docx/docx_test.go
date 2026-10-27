package docx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"path"
	"strings"
	"testing"
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

func writeDoc(t *testing.T, d Document) map[string][]byte {
	t.Helper()
	var buf bytes.Buffer
	if err := Write(&buf, d); err != nil {
		t.Fatalf("Write: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	files := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[f.Name] = data
	}
	return files
}

func body(t *testing.T, files map[string][]byte) []element {
	t.Helper()
	dec := xml.NewDecoder(bytes.NewReader(files["word/document.xml"]))
	var out []element
	depth := 0
	inBody := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("document.xml: %v", err)
		}
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
			if err := dec.DecodeElement(&e.para, &se); err != nil {
				t.Fatal(err)
			}
		case "tbl":
			if err := dec.DecodeElement(&e.table, &se); err != nil {
				t.Fatal(err)
			}
		default:
			if err := dec.Skip(); err != nil {
				t.Fatal(err)
			}
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

func TestPackageParts(t *testing.T) {
	files := writeDoc(t, sampleDoc())
	for _, name := range []string{
		"[Content_Types].xml", "_rels/.rels", "word/document.xml", "word/styles.xml",
		"word/numbering.xml", "word/_rels/document.xml.rels", "docProps/core.xml", "docProps/app.xml",
	} {
		data, ok := files[name]
		if !ok {
			t.Errorf("missing part %s", name)
			continue
		}
		if err := xml.Unmarshal(data, new(struct{})); err != nil {
			t.Errorf("%s is not well-formed XML: %v", name, err)
		}
	}

	var ct struct {
		Overrides []struct {
			PartName    string `xml:"PartName,attr"`
			ContentType string `xml:"ContentType,attr"`
		} `xml:"Override"`
	}
	if err := xml.Unmarshal(files["[Content_Types].xml"], &ct); err != nil {
		t.Fatal(err)
	}
	types := map[string]string{}
	for _, o := range ct.Overrides {
		types[o.PartName] = o.ContentType
		if _, ok := files[strings.TrimPrefix(o.PartName, "/")]; !ok {
			t.Errorf("content type override for missing part %s", o.PartName)
		}
	}
	for _, name := range []string{"word/document.xml", "word/styles.xml", "word/numbering.xml", "docProps/core.xml", "docProps/app.xml"} {
		if types["/"+name] == "" {
			t.Errorf("[Content_Types].xml does not list %s", name)
		}
	}
	if !strings.Contains(types["/word/document.xml"], "wordprocessingml.document.main+xml") {
		t.Errorf("document content type = %q", types["/word/document.xml"])
	}

	checkRels := func(relsName, baseDir string) {
		var rels struct {
			Rels []struct {
				ID     string `xml:"Id,attr"`
				Target string `xml:"Target,attr"`
			} `xml:"Relationship"`
		}
		if err := xml.Unmarshal(files[relsName], &rels); err != nil {
			t.Fatal(err)
		}
		if len(rels.Rels) == 0 {
			t.Errorf("%s has no relationship", relsName)
		}
		ids := map[string]bool{}
		for _, r := range rels.Rels {
			if ids[r.ID] {
				t.Errorf("%s: duplicate id %s", relsName, r.ID)
			}
			ids[r.ID] = true
			if _, ok := files[path.Join(baseDir, r.Target)]; !ok {
				t.Errorf("%s: target %s does not exist", relsName, r.Target)
			}
		}
	}
	checkRels("_rels/.rels", "")
	checkRels("word/_rels/document.xml.rels", "word")
}

func TestNoNumberingWithoutBullets(t *testing.T) {
	files := writeDoc(t, Document{Blocks: []Block{Paragraph{Text: "x"}}})
	if _, ok := files["word/numbering.xml"]; ok {
		t.Error("numbering.xml written without a bullet list")
	}
	if bytes.Contains(files["[Content_Types].xml"], []byte("numbering")) {
		t.Error("content types list numbering without a bullet list")
	}
	if bytes.Contains(files["word/_rels/document.xml.rels"], []byte("numbering")) {
		t.Error("relationships point at numbering without a bullet list")
	}
}

func TestBlocksInOrder(t *testing.T) {
	els := body(t, writeDoc(t, sampleDoc()))
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
	if len(els) != len(wants) {
		t.Fatalf("got %d body elements, want %d", len(els), len(wants))
	}
	for i, w := range wants {
		e := els[i]
		if e.kind != w.kind {
			t.Errorf("element %d is %s, want %s", i, e.kind, w.kind)
			continue
		}
		if e.kind == "p" && (e.para.Style.V != w.style || paraText(e.para) != w.text) {
			t.Errorf("element %d = style %q text %q, want style %q text %q", i, e.para.Style.V, paraText(e.para), w.style, w.text)
		}
	}
}

func TestRunsEmphasis(t *testing.T) {
	els := body(t, writeDoc(t, sampleDoc()))
	runs := els[3].para.Runs
	if len(runs) != 3 {
		t.Fatalf("got %d runs", len(runs))
	}
	if runs[0].Bold == nil || runs[0].Italic != nil {
		t.Error("run 0 should be bold only")
	}
	if runs[1].Bold != nil || runs[1].Italic != nil {
		t.Error("run 1 should be plain")
	}
	if runs[2].Bold != nil || runs[2].Italic == nil {
		t.Error("run 2 should be italic only")
	}
}

func TestBulletsUseNumbering(t *testing.T) {
	files := writeDoc(t, sampleDoc())
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
	if err := xml.Unmarshal(files["word/numbering.xml"], &num); err != nil {
		t.Fatal(err)
	}
	if len(num.Nums) != 1 || len(num.Abstract) != 1 {
		t.Fatalf("numbering has %d nums and %d abstract nums", len(num.Nums), len(num.Abstract))
	}
	if num.Abstract[0].Format.V != "bullet" {
		t.Errorf("list format = %q, want bullet", num.Abstract[0].Format.V)
	}
	if num.Nums[0].Abstract.V != num.Abstract[0].ID {
		t.Error("num does not refer to the abstract definition")
	}
	count := 0
	for _, e := range body(t, files) {
		if e.kind == "p" && e.para.Style.V == "ListBullet" {
			count++
			if e.para.NumID.V != num.Nums[0].ID {
				t.Errorf("bullet numId = %q, want %q", e.para.NumID.V, num.Nums[0].ID)
			}
		}
	}
	if count != 3 {
		t.Errorf("got %d bullet paragraphs, want 3", count)
	}
}

func TestTable(t *testing.T) {
	els := body(t, writeDoc(t, sampleDoc()))
	tbl := els[8].table
	if len(tbl.Rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(tbl.Rows))
	}
	var got [][]string
	for _, r := range tbl.Rows {
		var row []string
		for _, c := range r.Cells {
			row = append(row, paraText(c.Paras[0]))
		}
		got = append(got, row)
	}
	want := [][]string{{"Name", "Count"}, {"a", "1"}, {"b", "2"}}
	for i := range want {
		if strings.Join(got[i], "|") != strings.Join(want[i], "|") {
			t.Errorf("row %d = %v, want %v", i, got[i], want[i])
		}
	}
	if tbl.Rows[0].Cells[0].Paras[0].Runs[0].Bold == nil {
		t.Error("header cell is not bold")
	}
	if tbl.Rows[1].Cells[0].Paras[0].Runs[0].Bold != nil {
		t.Error("body cell is bold")
	}
	if tbl.Rows[0].Cells[0].Fill.F != "DBEAFE" || tbl.Rows[0].Cells[1].Fill.F != "DBEAFE" {
		t.Errorf("header fill = %q", tbl.Rows[0].Cells[0].Fill.F)
	}
	if tbl.Rows[1].Cells[0].Fill.F != "" {
		t.Error("body cell has a fill")
	}
	if len(tbl.Grid) != 2 || tbl.Grid[0].W != "3000" || tbl.Grid[1].W != "1500" {
		t.Errorf("grid = %+v", tbl.Grid)
	}
	if tbl.Rows[1].Cells[1].Width.W != "1500" {
		t.Errorf("cell width = %q", tbl.Rows[1].Cells[1].Width.W)
	}
}

func TestTableDefaultWidths(t *testing.T) {
	d := Document{Blocks: []Block{Table{Header: []string{"a", "b", "c"}}}}
	tbl := body(t, writeDoc(t, d))[0].table
	if len(tbl.Grid) != 3 {
		t.Fatalf("grid has %d columns", len(tbl.Grid))
	}
	for _, g := range tbl.Grid {
		if g.W != tbl.Grid[0].W || g.W == "0" || g.W == "" {
			t.Errorf("grid widths are not an equal share: %+v", tbl.Grid)
		}
	}
}

func TestStyleValues(t *testing.T) {
	files := writeDoc(t, sampleDoc())
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
	if err := xml.Unmarshal(files["word/styles.xml"], &st); err != nil {
		t.Fatal(err)
	}
	if st.Font.A != "Georgia" || st.Size.V != "24" {
		t.Errorf("font %q size %q, want Georgia 24", st.Font.A, st.Size.V)
	}
	colours := map[string]string{}
	for _, s := range st.Styles {
		colours[s.ID] = s.Color.V
	}
	for _, id := range []string{"Heading1", "Heading2", "Heading3"} {
		if colours[id] != "1F2937" {
			t.Errorf("%s colour = %q", id, colours[id])
		}
	}
	for _, id := range []string{"Normal", "ListBullet", "TableGrid"} {
		if _, ok := colours[id]; !ok {
			t.Errorf("styles.xml has no %s style", id)
		}
	}
	if !bytes.Contains(files["word/document.xml"], []byte(`w:fill="DBEAFE"`)) {
		t.Error("document.xml has no header fill")
	}
}

func TestStyleDefaults(t *testing.T) {
	files := writeDoc(t, Document{Blocks: []Block{
		Heading{Level: 1, Text: "h"},
		Table{Header: []string{"a"}},
	}})
	s := string(files["word/styles.xml"])
	for _, want := range []string{`w:ascii="Calibri"`, `<w:sz w:val="22"/>`, `<w:color w:val="000000"/>`} {
		if !strings.Contains(s, want) {
			t.Errorf("styles.xml lacks %s", want)
		}
	}
	if bytes.Contains(files["word/document.xml"], []byte("w:fill=")) {
		t.Error("header fill written although none was set")
	}
}

func TestApplicationOptional(t *testing.T) {
	files := writeDoc(t, Document{})
	if bytes.Contains(files["docProps/app.xml"], []byte("Application")) {
		t.Error("Application element present although the field is empty")
	}
	files = writeDoc(t, Document{Application: `Tool <1> & "co"`})
	var app struct {
		Application string `xml:"Application"`
	}
	if err := xml.Unmarshal(files["docProps/app.xml"], &app); err != nil {
		t.Fatal(err)
	}
	if app.Application != `Tool <1> & "co"` {
		t.Errorf("Application = %q", app.Application)
	}
}

func TestCoreProperties(t *testing.T) {
	files := writeDoc(t, Document{Title: `A & B`, Author: `"Me" <x>`})
	var core struct {
		Title   string `xml:"title"`
		Creator string `xml:"creator"`
	}
	if err := xml.Unmarshal(files["docProps/core.xml"], &core); err != nil {
		t.Fatal(err)
	}
	if core.Title != `A & B` || core.Creator != `"Me" <x>` {
		t.Errorf("core = %+v", core)
	}
}

func TestValidation(t *testing.T) {
	tests := []struct {
		name string
		doc  Document
	}{
		{"empty heading", Document{Blocks: []Block{Heading{Level: 1, Text: ""}}}},
		{"blank heading", Document{Blocks: []Block{Heading{Level: 1, Text: "  "}}}},
		{"level 0", Document{Blocks: []Block{Heading{Level: 0, Text: "x"}}}},
		{"level 4", Document{Blocks: []Block{Heading{Level: 4, Text: "x"}}}},
		{"bad heading colour", Document{Style: Style{HeadingColor: "12345"}, Blocks: nil}},
		{"colour with hash", Document{Style: Style{HeadingColor: "#112233"}}},
		{"non-hex colour", Document{Style: Style{HeadingColor: "GG0000"}}},
		{"bad fill", Document{Style: Style{TableHeaderFill: "xyz"}}},
		{"negative body size", Document{Style: Style{BodySize: -2}}},
		{"ragged row", Document{Blocks: []Block{Table{Header: []string{"a", "b"}, Rows: [][]string{{"1", "2"}, {"3"}}}}}},
		{"no header", Document{Blocks: []Block{Table{Rows: [][]string{{"1"}}}}}},
		{"negative width", Document{Blocks: []Block{Table{Header: []string{"a", "b"}, Widths: []int{100, -1}}}}},
		{"width count", Document{Blocks: []Block{Table{Header: []string{"a", "b"}, Widths: []int{100}}}}},
		{"empty bullets", Document{Blocks: []Block{Bullets{}}}},
		{"empty bullet item", Document{Blocks: []Block{Bullets{Items: []string{"a", ""}}}}},
		{"text and runs", Document{Blocks: []Block{Paragraph{Text: "a", Runs: []Run{{Text: "b"}}}}}},
		{"nil block", Document{Blocks: []Block{nil}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := Write(&buf, tc.doc)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.HasPrefix(err.Error(), "docx: ") {
				t.Errorf("error %q lacks the package prefix", err)
			}
			if buf.Len() != 0 {
				t.Error("output written although validation failed")
			}
		})
	}
}

func TestEscaping(t *testing.T) {
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
	files := writeDoc(t, d)
	for name, data := range files {
		if err := xml.Unmarshal(data, new(struct{})); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	els := body(t, files)
	for i := 0; i < 4; i++ {
		if got := paraText(els[i].para); got != nasty {
			t.Errorf("element %d text = %q", i, got)
		}
	}
	tbl := els[4].table
	if paraText(tbl.Rows[0].Cells[0].Paras[0]) != nasty || paraText(tbl.Rows[1].Cells[0].Paras[0]) != nasty {
		t.Error("table cell text did not round-trip")
	}
	var st struct {
		Font struct {
			A string `xml:"ascii,attr"`
		} `xml:"docDefaults>rPrDefault>rPr>rFonts"`
	}
	if err := xml.Unmarshal(files["word/styles.xml"], &st); err != nil {
		t.Fatal(err)
	}
	if st.Font.A != `Font "X" & <Y>` {
		t.Errorf("font = %q", st.Font.A)
	}
}

func TestEmptyDocument(t *testing.T) {
	els := body(t, writeDoc(t, Document{}))
	if len(els) != 1 || els[0].kind != "sectPr" {
		t.Errorf("empty document body = %+v", els)
	}
}

package preview_test

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"

	. "github.com/onsi/gomega"
)

// zipOf builds an Office package from its parts, in the order given.
func zipOf(parts ...string) []byte {
	Expect(len(parts)%2).To(Equal(0), "name and content pairs")
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for i := 0; i < len(parts); i += 2 {
		f, err := w.Create(parts[i])
		Expect(err).NotTo(HaveOccurred())
		_, err = f.Write([]byte(parts[i+1]))
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(w.Close()).To(Succeed())
	return buf.Bytes()
}

const (
	nsMain = `xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"`
	nsRel  = `xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`
)

// sheetDef is one sheet of a workbook fixture: its name and the <sheetData>
// body.
type sheetDef struct{ name, data string }

// workbook builds an .xlsx with the given sheets, shared strings and styles
// (the <cellXfs> and <numFmts> parts of styles.xml). date1904 sets the 1904
// date system.
func workbook(shared []string, styles string, date1904 bool, sheets ...sheetDef) []byte {
	var wb, rels strings.Builder
	pr := ""
	if date1904 {
		pr = `<workbookPr date1904="1"/>`
	}
	wb.WriteString(`<?xml version="1.0" encoding="UTF-8"?><workbook ` + nsMain + ` ` + nsRel + `>` + pr + `<sheets>`)
	rels.WriteString(`<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	parts := []string{}
	for i, s := range sheets {
		id := fmt.Sprintf("rId%d", i+1)
		wb.WriteString(fmt.Sprintf(`<sheet name="%s" sheetId="%d" r:id="%s"/>`, s.name, i+1, id))
		rels.WriteString(fmt.Sprintf(`<Relationship Id="%s" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, id, i+1))
		parts = append(parts, fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1),
			`<?xml version="1.0" encoding="UTF-8"?><worksheet `+nsMain+`><sheetData>`+s.data+`</sheetData></worksheet>`)
	}
	wb.WriteString(`</sheets></workbook>`)
	rels.WriteString(`</Relationships>`)
	parts = append(parts, "xl/workbook.xml", wb.String(), "xl/_rels/workbook.xml.rels", rels.String())
	if shared != nil {
		var sst strings.Builder
		sst.WriteString(`<?xml version="1.0" encoding="UTF-8"?><sst ` + nsMain + `>`)
		for _, s := range shared {
			if strings.HasPrefix(s, "<") {
				sst.WriteString(`<si>` + s + `</si>`)
			} else {
				sst.WriteString(`<si><t>` + s + `</t></si>`)
			}
		}
		sst.WriteString(`</sst>`)
		parts = append(parts, "xl/sharedStrings.xml", sst.String())
	}
	if styles != "" {
		parts = append(parts, "xl/styles.xml", `<?xml version="1.0" encoding="UTF-8"?><styleSheet `+nsMain+`>`+styles+`</styleSheet>`)
	}
	return zipOf(parts...)
}

const (
	nsW = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"`
	nsA = `xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"`
	nsP = `xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"`
)

func docx(body, styles string) []byte {
	parts := []string{"word/document.xml", `<?xml version="1.0" encoding="UTF-8"?><w:document ` + nsW + `><w:body>` + body + `</w:body></w:document>`}
	if styles != "" {
		parts = append(parts, "word/styles.xml", `<?xml version="1.0" encoding="UTF-8"?><w:styles `+nsW+`>`+styles+`</w:styles>`)
	}
	return zipOf(parts...)
}

// slideDef is one slide of a presentation fixture: the shapes of its tree and,
// when not empty, the text of its notes.
type slideDef struct{ shapes, notes string }

func pptx(slides ...slideDef) []byte {
	var pres, rels strings.Builder
	pres.WriteString(`<?xml version="1.0" encoding="UTF-8"?><p:presentation ` + nsP + ` ` + nsRel + `><p:sldIdLst>`)
	rels.WriteString(`<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	parts := []string{}
	for i, s := range slides {
		n := i + 1
		pres.WriteString(fmt.Sprintf(`<p:sldId id="%d" r:id="rId%d"/>`, 255+n, n))
		rels.WriteString(fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide%d.xml"/>`, n, n))
		parts = append(parts, fmt.Sprintf("ppt/slides/slide%d.xml", n),
			`<?xml version="1.0" encoding="UTF-8"?><p:sld `+nsP+` `+nsA+`><p:cSld><p:spTree>`+s.shapes+`</p:spTree></p:cSld></p:sld>`)
		if s.notes != "" {
			parts = append(parts,
				fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", n),
				fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId9" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/notesSlide" Target="../notesSlides/notesSlide%d.xml"/></Relationships>`, n),
				fmt.Sprintf("ppt/notesSlides/notesSlide%d.xml", n),
				`<?xml version="1.0" encoding="UTF-8"?><p:notes `+nsP+` `+nsA+`><p:cSld><p:spTree>`+
					`<p:sp><p:nvSpPr><p:cNvPr id="2" name="Slide Image"/><p:cNvSpPr/><p:nvPr><p:ph type="sldImg"/></p:nvPr></p:nvSpPr></p:sp>`+
					`<p:sp><p:nvSpPr><p:cNvPr id="3" name="Notes"/><p:cNvSpPr/><p:nvPr><p:ph type="body" idx="1"/></p:nvPr></p:nvSpPr><p:txBody><a:p><a:r><a:t>`+s.notes+`</a:t></a:r></a:p></p:txBody></p:sp>`+
					`<p:sp><p:nvSpPr><p:cNvPr id="4" name="Number"/><p:cNvSpPr/><p:nvPr><p:ph type="sldNum"/></p:nvPr></p:nvSpPr><p:txBody><a:p><a:r><a:t>`+fmt.Sprint(n)+`</a:t></a:r></a:p></p:txBody></p:sp>`+
					`</p:spTree></p:cSld></p:notes>`)
		}
	}
	pres.WriteString(`</p:sldIdLst></p:presentation>`)
	rels.WriteString(`</Relationships>`)
	parts = append(parts, "ppt/presentation.xml", pres.String(), "ppt/_rels/presentation.xml.rels", rels.String())
	return zipOf(parts...)
}

// shape is a text shape of a slide; ph is the placeholder type ("" for none).
func shape(ph string, paras ...string) string {
	nv := `<p:nvPr/>`
	if ph != "" {
		nv = `<p:nvPr><p:ph type="` + ph + `"/></p:nvPr>`
	}
	return `<p:sp><p:nvSpPr><p:cNvPr id="1" name="s"/><p:cNvSpPr/>` + nv + `</p:nvSpPr><p:txBody>` + strings.Join(paras, "") + `</p:txBody></p:sp>`
}

func para(text string, lvl int) string {
	pr := ""
	if lvl > 0 {
		pr = fmt.Sprintf(`<a:pPr lvl="%d"/>`, lvl)
	}
	return `<a:p>` + pr + `<a:r><a:t>` + text + `</a:t></a:r></a:p>`
}

package extract_test

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
)

// zipOf builds a zip from name, content pairs.
func zipOf(parts ...string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i := 0; i+1 < len(parts); i += 2 {
		w, err := zw.Create(parts[i])
		if err != nil {
			panic(err)
		}
		if _, err := w.Write([]byte(parts[i+1])); err != nil {
			panic(err)
		}
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

const (
	nsS = `xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`
	nsW = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"`
	nsP = `xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`
)

// inlineWorkbook is a workbook written the way some Python libraries write
// one: absolute relationship targets and inline strings, with no shared
// strings part. Each sheet is a name and its rows.
func inlineWorkbook(sheets map[string][][]string, order ...string) []byte {
	var wb, rels strings.Builder
	parts := []string{}
	for i, name := range order {
		fmt.Fprintf(&wb, `<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, name, i+1, i+1)
		fmt.Fprintf(&rels, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="/xl/worksheets/sheet%d.xml"/>`, i+1, i+1)
		var data strings.Builder
		for r, row := range sheets[name] {
			fmt.Fprintf(&data, `<row r="%d">`, r+1)
			for c, v := range row {
				ref := fmt.Sprintf("%c%d", 'A'+c, r+1)
				if _, err := fmt.Sscanf(v, "%g", new(float64)); err == nil && !strings.ContainsAny(v, " /") {
					fmt.Fprintf(&data, `<c r="%s"><v>%s</v></c>`, ref, v)
				} else {
					fmt.Fprintf(&data, `<c r="%s" t="inlineStr"><is><t>%s</t></is></c>`, ref, v)
				}
			}
			data.WriteString(`</row>`)
		}
		parts = append(parts, fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1),
			`<?xml version="1.0" encoding="UTF-8"?><worksheet `+nsS+`><sheetData>`+data.String()+`</sheetData></worksheet>`)
	}
	parts = append(parts,
		"xl/workbook.xml", `<?xml version="1.0" encoding="UTF-8"?><workbook `+nsS+`><sheets>`+wb.String()+`</sheets></workbook>`,
		"xl/_rels/workbook.xml.rels", `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+rels.String()+`</Relationships>`)
	return zipOf(parts...)
}

func docx(body string) []byte {
	return zipOf("word/document.xml", `<?xml version="1.0" encoding="UTF-8"?><w:document `+nsW+`><w:body>`+body+`</w:body></w:document>`,
		"word/styles.xml", `<?xml version="1.0" encoding="UTF-8"?><w:styles `+nsW+`><w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/></w:style></w:styles>`)
}

func wp(style, text string) string {
	ppr := ""
	if style != "" {
		ppr = `<w:pPr><w:pStyle w:val="` + style + `"/></w:pPr>`
	}
	return `<w:p>` + ppr + `<w:r><w:t xml:space="preserve">` + text + `</w:t></w:r></w:p>`
}

// pptx is a presentation with one slide per title, each with one body line.
func pptx(slides ...[2]string) []byte {
	var list, rels strings.Builder
	parts := []string{}
	for i, s := range slides {
		fmt.Fprintf(&list, `<p:sldId id="%d" r:id="rId%d"/>`, 256+i, i+1)
		fmt.Fprintf(&rels, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide%d.xml"/>`, i+1, i+1)
		shape := func(ph, text string) string {
			return `<p:sp><p:nvSpPr><p:cNvPr id="1" name="s"/><p:cNvSpPr/><p:nvPr><p:ph type="` + ph + `"/></p:nvPr></p:nvSpPr><p:txBody><a:p><a:r><a:t>` + text + `</a:t></a:r></a:p></p:txBody></p:sp>`
		}
		parts = append(parts, fmt.Sprintf("ppt/slides/slide%d.xml", i+1),
			`<?xml version="1.0" encoding="UTF-8"?><p:sld `+nsP+`><p:cSld><p:spTree>`+shape("title", s[0])+shape("body", s[1])+`</p:spTree></p:cSld></p:sld>`)
	}
	parts = append(parts,
		"ppt/presentation.xml", `<?xml version="1.0" encoding="UTF-8"?><p:presentation `+nsP+`><p:sldIdLst>`+list.String()+`</p:sldIdLst></p:presentation>`,
		"ppt/_rels/presentation.xml.rels", `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+rels.String()+`</Relationships>`)
	return zipOf(parts...)
}

// blankPDF is a valid one-page PDF with no text on the page, as a scan
// without OCR has none.
func blankPDF() []byte {
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Contents 4 0 R /Resources << >> >>",
		"<< /Length 0 >>\nstream\n\nendstream",
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
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

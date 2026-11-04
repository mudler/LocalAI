package docx

import (
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/addons/office/internal/ooxml"
)

const (
	xmlHeader = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"
	nsW       = `http://schemas.openxmlformats.org/wordprocessingml/2006/main`
	nsR       = `http://schemas.openxmlformats.org/officeDocument/2006/relationships`

	relBase      = `http://schemas.openxmlformats.org/officeDocument/2006/relationships/`
	relOfficeDoc = relBase + "officeDocument"
	relCoreProps = `http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties`
	relExtProps  = relBase + "extended-properties"
	relStyles    = relBase + "styles"
	relNumbering = relBase + "numbering"

	ctBase      = `application/vnd.openxmlformats-officedocument.`
	ctDocument  = ctBase + "wordprocessingml.document.main+xml"
	ctStyles    = ctBase + "wordprocessingml.styles+xml"
	ctNumbering = ctBase + "wordprocessingml.numbering+xml"
	ctCoreProps = `application/vnd.openxmlformats-package.core-properties+xml`
	ctExtProps  = ctBase + "extended-properties+xml"
)

// Page layout in twips: A4 with 2.5 cm margins. The text width is the page
// width minus both margins.
const (
	pageWidth  = 11906
	pageHeight = 16838
	margin     = 1417
	textWidth  = pageWidth - 2*margin
)

// bulletNumID is the w:numId of the bullet list in numbering.xml.
const bulletNumID = 1

type part struct {
	name string
	data []byte
}

type rel struct{ id, typ, target string }

func relsXML(rels []rel) []byte {
	var b strings.Builder
	b.WriteString(xmlHeader)
	b.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for _, r := range rels {
		fmt.Fprintf(&b, `<Relationship Id="%s" Type="%s" Target="%s"/>`, r.id, r.typ, ooxml.Escape(r.target))
	}
	b.WriteString(`</Relationships>`)
	return []byte(b.String())
}

// parts lays out every part of the package, [Content_Types].xml first.
func (b *builder) parts() []part {
	var ct strings.Builder
	ct.WriteString(xmlHeader)
	ct.WriteString(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	ct.WriteString(`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`)
	ct.WriteString(`<Default Extension="xml" ContentType="application/xml"/>`)

	var parts []part
	add := func(name string, data []byte, typ string) {
		parts = append(parts, part{name: name, data: data})
		if typ != "" {
			fmt.Fprintf(&ct, `<Override PartName="/%s" ContentType="%s"/>`, name, typ)
		}
	}

	add("_rels/.rels", relsXML([]rel{
		{"rId1", relOfficeDoc, "word/document.xml"},
		{"rId2", relCoreProps, "docProps/core.xml"},
		{"rId3", relExtProps, "docProps/app.xml"},
	}), "")
	add("docProps/core.xml", coreXML(b.doc.Title, b.doc.Author), ctCoreProps)
	add("docProps/app.xml", appXML(b.doc.Application), ctExtProps)

	rels := []rel{{"rId1", relStyles, "styles.xml"}}
	if b.hasBullets {
		rels = append(rels, rel{"rId2", relNumbering, "numbering.xml"})
	}
	add("word/document.xml", b.documentXML(), ctDocument)
	add("word/_rels/document.xml.rels", relsXML(rels), "")
	add("word/styles.xml", b.stylesXML(), ctStyles)
	if b.hasBullets {
		add("word/numbering.xml", b.numberingXML(), ctNumbering)
	}

	ct.WriteString(`</Types>`)
	return append([]part{{name: "[Content_Types].xml", data: []byte(ct.String())}}, parts...)
}

func coreXML(title, author string) []byte {
	var s strings.Builder
	s.WriteString(xmlHeader + `<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" ` +
		`xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" ` +
		`xmlns:dcmitype="http://purl.org/dc/dcmitype/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">`)
	s.WriteString(`<dc:title>` + ooxml.Escape(title) + `</dc:title>`)
	s.WriteString(`<dc:creator>` + ooxml.Escape(author) + `</dc:creator>`)
	s.WriteString(`</cp:coreProperties>`)
	return []byte(s.String())
}

func appXML(application string) []byte {
	var app string
	if application != "" {
		app = "<Application>" + ooxml.Escape(application) + "</Application>"
	}
	return []byte(xmlHeader + `<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties" ` +
		`xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes">` + app + `</Properties>`)
}

func (b *builder) numberingXML() []byte {
	var ordered strings.Builder
	id := 2
	for _, blk := range b.doc.Blocks {
		if v, ok := blk.(Bullets); ok && v.Ordered {
			start := v.Start
			if start == 0 && !v.StartSet {
				start = 1
			}
			fmt.Fprintf(&ordered, `<w:abstractNum w:abstractNumId="%d"><w:multiLevelType w:val="singleLevel"/><w:lvl w:ilvl="0"><w:start w:val="%d"/><w:numFmt w:val="decimal"/><w:lvlText w:val="%%1."/><w:lvlJc w:val="left"/></w:lvl></w:abstractNum><w:num w:numId="%d"><w:abstractNumId w:val="%d"/></w:num>`, id, start, id, id)
			id++
		}
	}
	return []byte(xmlHeader + `<w:numbering xmlns:w="` + nsW + `">` +
		`<w:abstractNum w:abstractNumId="0"><w:multiLevelType w:val="hybridMultilevel"/>` +
		`<w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="bullet"/><w:lvlText w:val="&#8226;"/><w:lvlJc w:val="left"/>` +
		`<w:pPr><w:ind w:left="720" w:hanging="360"/></w:pPr></w:lvl>` +
		`</w:abstractNum>` +
		fmt.Sprintf(`<w:num w:numId="%d"><w:abstractNumId w:val="0"/></w:num>`, bulletNumID) +
		ordered.String() + `</w:numbering>`)
}

// Heading sizes in half-points.
var headingSize = map[int]int{1: 32, 2: 28, 3: 26, 4: 24, 5: 22, 6: 22}

func (b *builder) stylesXML() []byte {
	st := b.style
	font := ooxml.Escape(st.Font)
	var s strings.Builder
	s.WriteString(xmlHeader)
	s.WriteString(`<w:styles xmlns:w="` + nsW + `">`)
	fmt.Fprintf(&s, `<w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:ascii="%[1]s" w:hAnsi="%[1]s" w:cs="%[1]s" w:eastAsia="%[1]s"/>`+
		`<w:sz w:val="%[2]d"/><w:szCs w:val="%[2]d"/></w:rPr></w:rPrDefault>`+
		`<w:pPrDefault><w:pPr><w:spacing w:after="120" w:line="264" w:lineRule="auto"/></w:pPr></w:pPrDefault></w:docDefaults>`,
		font, st.BodySize)
	s.WriteString(`<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/><w:qFormat/></w:style>`)
	for lvl := 1; lvl <= maxHeadingLevel; lvl++ {
		fmt.Fprintf(&s, `<w:style w:type="paragraph" w:styleId="Heading%[1]d"><w:name w:val="heading %[1]d"/>`+
			`<w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/>`+
			`<w:pPr><w:keepNext/><w:spacing w:before="240" w:after="120"/><w:outlineLvl w:val="%[2]d"/></w:pPr>`+
			`<w:rPr><w:b/><w:color w:val="%[3]s"/><w:sz w:val="%[4]d"/><w:szCs w:val="%[4]d"/></w:rPr></w:style>`,
			lvl, lvl-1, strings.ToUpper(st.HeadingColor), headingSize[lvl])
	}
	fmt.Fprintf(&s, `<w:style w:type="paragraph" w:styleId="ListBullet"><w:name w:val="List Bullet"/><w:basedOn w:val="Normal"/><w:qFormat/>`+
		`<w:pPr><w:numPr><w:numId w:val="%d"/></w:numPr><w:spacing w:after="60"/><w:contextualSpacing/></w:pPr></w:style>`, bulletNumID)
	s.WriteString(`<w:style w:type="table" w:default="1" w:styleId="TableNormal"><w:name w:val="Normal Table"/>` +
		`<w:tblPr><w:tblInd w:w="0" w:type="dxa"/><w:tblCellMar><w:top w:w="0" w:type="dxa"/><w:left w:w="108" w:type="dxa"/>` +
		`<w:bottom w:w="0" w:type="dxa"/><w:right w:w="108" w:type="dxa"/></w:tblCellMar></w:tblPr></w:style>`)
	s.WriteString(`<w:style w:type="table" w:styleId="TableGrid"><w:name w:val="Table Grid"/><w:basedOn w:val="TableNormal"/>` +
		`<w:tblPr><w:tblBorders>`)
	for _, side := range []string{"top", "left", "bottom", "right", "insideH", "insideV"} {
		fmt.Fprintf(&s, `<w:%s w:val="single" w:sz="4" w:space="0" w:color="auto"/>`, side)
	}
	s.WriteString(`</w:tblBorders></w:tblPr></w:style>`)
	s.WriteString(`</w:styles>`)
	return []byte(s.String())
}

func (b *builder) documentXML() []byte {
	var x strings.Builder
	x.WriteString(xmlHeader)
	x.WriteString(`<w:document xmlns:w="` + nsW + `" xmlns:r="` + nsR + `"><w:body>`)
	orderedID := 2
	for _, blk := range b.doc.Blocks {
		switch v := blk.(type) {
		case Heading:
			fmt.Fprintf(&x, `<w:p><w:pPr><w:pStyle w:val="Heading%d"/></w:pPr>%s</w:p>`, v.Level, runsXML(v.Text, v.Runs, false))
		case Paragraph:
			x.WriteString(`<w:p>`)
			if v.Text != "" {
				x.WriteString(runXML(Run{Text: v.Text}))
			}
			for _, r := range v.Runs {
				x.WriteString(runXML(r))
			}
			x.WriteString(`</w:p>`)
		case Bullets:
			id := bulletNumID
			style := `<w:pStyle w:val="ListBullet"/>`
			if v.Ordered {
				style = ""
				id = orderedID
				orderedID++
			}
			for i, it := range v.texts() {
				var runs []Run
				if len(v.RichItems) > 0 {
					runs = v.RichItems[i]
					it = ""
				}
				fmt.Fprintf(&x, `<w:p><w:pPr>%s<w:numPr><w:ilvl w:val="0"/><w:numId w:val="%d"/></w:numPr><w:ind w:left="%d" w:hanging="360"/></w:pPr>%s</w:p>`, style, id, 720*(v.Indent+1), runsXML(it, runs, false))
			}
		case Table:
			b.tableXML(&x, v)
			x.WriteString(`<w:p/>`)
		}
	}
	fmt.Fprintf(&x, `<w:sectPr><w:pgSz w:w="%d" w:h="%d"/><w:pgMar w:top="%d" w:right="%d" w:bottom="%d" w:left="%d" w:header="708" w:footer="708" w:gutter="0"/></w:sectPr>`,
		pageWidth, pageHeight, margin, margin, margin, margin)
	x.WriteString(`</w:body></w:document>`)
	return []byte(x.String())
}

func runXML(r Run) string {
	var rpr string
	if r.Bold {
		rpr += `<w:b/>`
	}
	if r.Italic {
		rpr += `<w:i/>`
	}
	if rpr != "" {
		rpr = `<w:rPr>` + rpr + `</w:rPr>`
	}
	// WordprocessingML represents layout controls as elements, not character
	// references inside w:t. Keep them in the same run to retain emphasis.
	var content strings.Builder
	start := 0
	for i, ch := range r.Text {
		if ch != '\n' && ch != '\t' {
			continue
		}
		if i > start {
			content.WriteString(`<w:t xml:space="preserve">` + ooxml.Escape(r.Text[start:i]) + `</w:t>`)
		}
		if ch == '\n' {
			content.WriteString(`<w:br/>`)
		} else {
			content.WriteString(`<w:tab/>`)
		}
		start = i + 1
	}
	if start < len(r.Text) || len(r.Text) == 0 {
		content.WriteString(`<w:t xml:space="preserve">` + ooxml.Escape(r.Text[start:]) + `</w:t>`)
	}
	return `<w:r>` + rpr + content.String() + `</w:r>`
}

// columnWidths returns the grid widths: the given widths, or an equal split of
// the text width. A zero width in a partly set list takes an equal share of
// the text width too.
func columnWidths(t Table) []int {
	n := len(t.Header)
	w := make([]int, n)
	for i := range w {
		if t.Widths != nil && t.Widths[i] > 0 {
			w[i] = t.Widths[i]
		} else {
			w[i] = textWidth / n
		}
	}
	return w
}

func (b *builder) tableXML(x *strings.Builder, t Table) {
	widths := columnWidths(t)
	x.WriteString(`<w:tbl><w:tblPr><w:tblStyle w:val="TableGrid"/><w:tblW w:w="0" w:type="auto"/><w:tblLook w:val="04A0" w:firstRow="1"/></w:tblPr><w:tblGrid>`)
	for _, w := range widths {
		fmt.Fprintf(x, `<w:gridCol w:w="%d"/>`, w)
	}
	x.WriteString(`</w:tblGrid>`)
	row := func(cells []string, rich [][]Run, header bool) {
		x.WriteString(`<w:tr>`)
		if header {
			x.WriteString(`<w:trPr><w:tblHeader/></w:trPr>`)
		}
		for i, c := range cells {
			fmt.Fprintf(x, `<w:tc><w:tcPr><w:tcW w:w="%d" w:type="dxa"/>`, widths[i])
			if header && b.style.TableHeaderFill != "" {
				fmt.Fprintf(x, `<w:shd w:val="clear" w:color="auto" w:fill="%s"/>`, strings.ToUpper(b.style.TableHeaderFill))
			}
			var runs []Run
			if rich != nil {
				runs = rich[i]
				c = ""
			}
			x.WriteString(`</w:tcPr><w:p>` + runsXML(c, runs, header) + `</w:p></w:tc>`)
		}
		x.WriteString(`</w:tr>`)
	}
	row(t.Header, t.HeaderRuns, true)
	for i, r := range t.Rows {
		var rich [][]Run
		if t.RowRuns != nil {
			rich = t.RowRuns[i]
		}
		row(r, rich, false)
	}
	x.WriteString(`</w:tbl>`)
}

func runsXML(text string, runs []Run, bold bool) string {
	if len(runs) == 0 {
		return runXML(Run{Text: text, Bold: bold})
	}
	var b strings.Builder
	for _, r := range runs {
		r.Bold = r.Bold || bold
		b.WriteString(runXML(r))
	}
	return b.String()
}

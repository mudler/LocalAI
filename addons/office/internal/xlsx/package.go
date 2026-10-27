package xlsx

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
)

const (
	xmlHeader = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"
	nsMain    = `http://schemas.openxmlformats.org/spreadsheetml/2006/main`
	nsR       = `http://schemas.openxmlformats.org/officeDocument/2006/relationships`

	relBase      = `http://schemas.openxmlformats.org/officeDocument/2006/relationships/`
	relOfficeDoc = relBase + "officeDocument"
	relCoreProps = `http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties`
	relExtProps  = relBase + "extended-properties"
	relSheet     = relBase + "worksheet"
	relStyles    = relBase + "styles"
	relSharedStr = relBase + "sharedStrings"

	ctBase      = `application/vnd.openxmlformats-officedocument.`
	ctWorkbook  = ctBase + "spreadsheetml.sheet.main+xml"
	ctWorksheet = ctBase + "spreadsheetml.worksheet+xml"
	ctStyles    = ctBase + "spreadsheetml.styles+xml"
	ctSharedStr = ctBase + "spreadsheetml.sharedStrings+xml"
	ctCoreProps = `application/vnd.openxmlformats-package.core-properties+xml`
	ctExtProps  = ctBase + "extended-properties+xml"
)

// firstCustomNumFmt is the lowest id for a custom number format.
const firstCustomNumFmt = 164

type part struct {
	name string
	data []byte
}

type rel struct{ id, typ, target string }

func esc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func relsXML(rels []rel) []byte {
	var b strings.Builder
	b.WriteString(xmlHeader)
	b.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for _, r := range rels {
		fmt.Fprintf(&b, `<Relationship Id="%s" Type="%s" Target="%s"/>`, r.id, r.typ, esc(r.target))
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
		{"rId1", relOfficeDoc, "xl/workbook.xml"},
		{"rId2", relCoreProps, "docProps/core.xml"},
		{"rId3", relExtProps, "docProps/app.xml"},
	}), "")
	add("docProps/core.xml", coreXML(b.wb.Title), ctCoreProps)
	add("docProps/app.xml", appXML(b.wb.Application), ctExtProps)

	var wbx strings.Builder
	wbx.WriteString(xmlHeader)
	wbx.WriteString(`<workbook xmlns="` + nsMain + `" xmlns:r="` + nsR + `"><sheets>`)
	var rels []rel
	for i, s := range b.sheets {
		n := i + 1
		fmt.Fprintf(&wbx, `<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, esc(s.sheet.Name), n, n)
		rels = append(rels, rel{fmt.Sprintf("rId%d", n), relSheet, fmt.Sprintf("worksheets/sheet%d.xml", n)})
	}
	wbx.WriteString(`</sheets></workbook>`)
	rels = append(rels,
		rel{fmt.Sprintf("rId%d", len(b.sheets)+1), relStyles, "styles.xml"},
		rel{fmt.Sprintf("rId%d", len(b.sheets)+2), relSharedStr, "sharedStrings.xml"},
	)
	add("xl/workbook.xml", []byte(wbx.String()), ctWorkbook)
	add("xl/_rels/workbook.xml.rels", relsXML(rels), "")
	for i, s := range b.sheets {
		add(fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1), b.sheetXML(s), ctWorksheet)
	}
	add("xl/styles.xml", b.stylesXML(), ctStyles)
	add("xl/sharedStrings.xml", b.sharedStringsXML(), ctSharedStr)

	ct.WriteString(`</Types>`)
	return append([]part{{name: "[Content_Types].xml", data: []byte(ct.String())}}, parts...)
}

func coreXML(title string) []byte {
	return []byte(xmlHeader + `<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" ` +
		`xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" ` +
		`xmlns:dcmitype="http://purl.org/dc/dcmitype/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">` +
		`<dc:title>` + esc(title) + `</dc:title></cp:coreProperties>`)
}

func appXML(application string) []byte {
	var app string
	if application != "" {
		app = "<Application>" + esc(application) + "</Application>"
	}
	return []byte(xmlHeader + `<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties" ` +
		`xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes">` + app + `</Properties>`)
}

func (b *builder) sharedStringsXML() []byte {
	var s strings.Builder
	s.WriteString(xmlHeader)
	fmt.Fprintf(&s, `<sst xmlns="%s" count="%d" uniqueCount="%d">`, nsMain, len(b.strings), len(b.strings))
	for _, str := range b.strings {
		s.WriteString(`<si><t xml:space="preserve">` + esc(str) + `</t></si>`)
	}
	s.WriteString(`</sst>`)
	return []byte(s.String())
}

func fmtFloat(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

func (b *builder) stylesXML() []byte {
	h := b.wb.HeaderStyle
	var s strings.Builder
	s.WriteString(xmlHeader)
	s.WriteString(`<styleSheet xmlns="` + nsMain + `">`)
	if len(b.numFmts) > 0 {
		fmt.Fprintf(&s, `<numFmts count="%d">`, len(b.numFmts))
		for i, f := range b.numFmts {
			fmt.Fprintf(&s, `<numFmt numFmtId="%d" formatCode="%s"/>`, firstCustomNumFmt+i, esc(f))
		}
		s.WriteString(`</numFmts>`)
	}
	// Font 0 is the default, font 1 is the header font.
	s.WriteString(`<fonts count="2"><font><sz val="11"/><name val="Calibri"/></font><font>`)
	if h.Bold {
		s.WriteString(`<b/>`)
	}
	s.WriteString(`<sz val="11"/>`)
	if h.FontColor != "" {
		s.WriteString(`<color rgb="FF` + strings.ToUpper(h.FontColor) + `"/>`)
	}
	s.WriteString(`<name val="Calibri"/></font></fonts>`)
	// Fills 0 and 1 are reserved by the format. Fill 2 is the header fill.
	s.WriteString(`<fills count="3"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill>`)
	if h.FillColor != "" {
		s.WriteString(`<fill><patternFill patternType="solid"><fgColor rgb="FF` + strings.ToUpper(h.FillColor) + `"/><bgColor indexed="64"/></patternFill></fill>`)
	} else {
		s.WriteString(`<fill><patternFill patternType="none"/></fill>`)
	}
	s.WriteString(`</fills>`)
	s.WriteString(`<borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders>`)
	s.WriteString(`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>`)
	fmt.Fprintf(&s, `<cellXfs count="%d">`, len(b.xfs))
	for _, k := range b.xfs {
		switch {
		case k.header:
			s.WriteString(`<xf numFmtId="0" fontId="1" fillId="2" borderId="0" xfId="0" applyFont="1" applyFill="1"/>`)
		case k.numFmt != "":
			fmt.Fprintf(&s, `<xf numFmtId="%d" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/>`, firstCustomNumFmt+b.fmtIdx[k.numFmt])
		default:
			s.WriteString(`<xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>`)
		}
	}
	s.WriteString(`</cellXfs>`)
	s.WriteString(`<cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles>`)
	s.WriteString(`</styleSheet>`)
	return []byte(s.String())
}

func (b *builder) sheetXML(bs builtSheet) []byte {
	s := bs.sheet
	var x strings.Builder
	x.WriteString(xmlHeader)
	x.WriteString(`<worksheet xmlns="` + nsMain + `" xmlns:r="` + nsR + `">`)
	if s.FreezeTopRow {
		x.WriteString(`<sheetViews><sheetView workbookViewId="0">` +
			`<pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/>` +
			`<selection pane="bottomLeft"/></sheetView></sheetViews>`)
	} else {
		x.WriteString(`<sheetViews><sheetView workbookViewId="0"/></sheetViews>`)
	}
	x.WriteString(`<sheetFormatPr defaultRowHeight="15"/>`)
	var cols strings.Builder
	for i, c := range s.Columns {
		if c.Width == 0 && c.NumberFormat == "" {
			continue
		}
		fmt.Fprintf(&cols, `<col min="%d" max="%d"`, i+1, i+1)
		if c.Width > 0 {
			fmt.Fprintf(&cols, ` width="%s" customWidth="1"`, fmtFloat(c.Width))
		} else {
			cols.WriteString(` width="8.43"`)
		}
		if c.NumberFormat != "" {
			fmt.Fprintf(&cols, ` style="%d"`, b.xfIdx[xfKey{numFmt: c.NumberFormat}])
		}
		cols.WriteString(`/>`)
	}
	if cols.Len() > 0 {
		x.WriteString(`<cols>` + cols.String() + `</cols>`)
	}
	x.WriteString(`<sheetData>`)
	curRow := 0
	for _, c := range bs.cells {
		if c.row != curRow {
			if curRow != 0 {
				x.WriteString(`</row>`)
			}
			fmt.Fprintf(&x, `<row r="%d">`, c.row)
			curRow = c.row
		}
		b.cellXML(&x, c)
	}
	if curRow != 0 {
		x.WriteString(`</row>`)
	}
	x.WriteString(`</sheetData></worksheet>`)
	return []byte(x.String())
}

func (b *builder) cellXML(x *strings.Builder, c builtCell) {
	fmt.Fprintf(x, `<c r="%s"`, esc(c.cell.Ref))
	if c.xf != 0 {
		fmt.Fprintf(x, ` s="%d"`, c.xf)
	}
	v := c.cell.Value
	if c.cell.Formula != "" {
		switch v.Kind {
		case KindString:
			x.WriteString(` t="str"`)
		case KindBool:
			x.WriteString(` t="b"`)
		}
		x.WriteString(`><f>` + esc(c.cell.Formula) + `</f>`)
		switch v.Kind {
		case KindString:
			x.WriteString(`<v>` + esc(v.String) + `</v>`)
		case KindNumber:
			x.WriteString(`<v>` + fmtFloat(v.Number) + `</v>`)
		case KindBool:
			x.WriteString(`<v>` + boolStr(v.Bool) + `</v>`)
		}
		x.WriteString(`</c>`)
		return
	}
	switch v.Kind {
	case KindString:
		fmt.Fprintf(x, ` t="s"><v>%d</v></c>`, b.strIdx[v.String])
	case KindNumber:
		x.WriteString(`><v>` + fmtFloat(v.Number) + `</v></c>`)
	case KindBool:
		x.WriteString(` t="b"><v>` + boolStr(v.Bool) + `</v></c>`)
	default:
		x.WriteString(`/>`)
	}
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

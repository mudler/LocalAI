// Package xlsxedit edits an existing spreadsheet (.xlsx, OOXML) with the
// standard library only. The xlsx package builds a workbook from scratch;
// this package opens one that already exists, changes single cells, adds an
// empty sheet and writes the package again.
//
// # How the XML is edited
//
// The package does not decode and re-encode a part. It scans the part to find
// the byte range of the element it needs (a row, a cell, the sheets list) and
// replaces or inserts only that range as text. Everything else in the part,
// such as columns, merged cells, conditional formats, drawings and
// extensions, stays byte for byte as it was. Text is escaped with
// xml.EscapeText.
//
// # What an edit does not do
//
// The package does not edit styles, merged cells, charts, pivot tables, data
// validation, defined names or the shared strings table. It does not move
// formula references when a cell changes. A new cell has no style. A cell
// that is replaced keeps its style index.
package xlsxedit

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"time"
)

const (
	nsMain = `http://schemas.openxmlformats.org/spreadsheetml/2006/main`
	nsR    = `http://schemas.openxmlformats.org/officeDocument/2006/relationships`

	relSheet    = `http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet`
	ctWorksheet = `application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml`

	maxEntries  = 10000
	maxPartSize = 64 << 20
)

// SheetInfo describes one sheet of the workbook.
type SheetInfo struct {
	// Name is the sheet name.
	Name string
	// Index is the 1-based position in the workbook order.
	Index int
	// Dimension is the dimension ref of the sheet (such as "A1:C5") when the
	// sheet has one, else empty.
	Dimension string
}

type sheetEntry struct {
	name    string
	sheetID int
	rid     string
	part    string
}

// Workbook is an open .xlsx file. It is not safe for concurrent use.
type Workbook struct {
	zr      *zip.Reader
	files   map[string]*zip.File
	changed map[string][]byte
	added   []string // names of new parts, in order
	wbPath  string
	sheets  []sheetEntry
}

func guard(err *error) {
	if r := recover(); r != nil {
		*err = fmt.Errorf("xlsxedit: internal error: %v", r)
	}
}

// Open reads an .xlsx package. It fails when data is not a zip file or has no
// workbook part. The bytes are not copied: do not change data while the
// Workbook is in use.
func Open(data []byte) (wb *Workbook, err error) {
	defer guard(&err)
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("xlsxedit: not a valid zip file: %w", err)
	}
	if len(zr.File) > maxEntries {
		return nil, fmt.Errorf("xlsxedit: package has more than %d entries", maxEntries)
	}
	w := &Workbook{zr: zr, files: map[string]*zip.File{}, changed: map[string][]byte{}}
	for _, f := range zr.File {
		if _, dup := w.files[f.Name]; dup {
			return nil, fmt.Errorf("xlsxedit: duplicate zip entry %q", f.Name)
		}
		w.files[f.Name] = f
	}
	if err := w.readWorkbook(); err != nil {
		return nil, err
	}
	return w, nil
}

// part returns the current bytes of a part.
func (w *Workbook) part(name string) ([]byte, error) {
	if b, ok := w.changed[name]; ok {
		return b, nil
	}
	f, ok := w.files[name]
	if !ok {
		return nil, fmt.Errorf("xlsxedit: package has no part %q", name)
	}
	if f.UncompressedSize64 > maxPartSize {
		return nil, fmt.Errorf("xlsxedit: part %q is larger than %d bytes", name, maxPartSize)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("xlsxedit: open %s: %w", name, err)
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(io.LimitReader(rc, maxPartSize+1))
	if err != nil {
		return nil, fmt.Errorf("xlsxedit: read %s: %w", name, err)
	}
	if len(b) > maxPartSize {
		return nil, fmt.Errorf("xlsxedit: part %q is larger than %d bytes", name, maxPartSize)
	}
	return b, nil
}

type relInfo struct{ id, typ, target string }

// readRels reads the internal relationships of the part owner. Targets are
// resolved to package part names.
func (w *Workbook) readRels(owner string) ([]relInfo, error) {
	name := path.Join(path.Dir(owner), "_rels", path.Base(owner)+".rels")
	if owner == "" {
		name = "_rels/.rels"
	}
	data, err := w.part(name)
	if err != nil {
		return nil, err
	}
	var out []relInfo
	err = scan(data, func(tok xml.Token, _, _ int) error {
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "Relationship" {
			return nil
		}
		if mode, _ := attrOf(se, "TargetMode"); strings.EqualFold(mode, "External") {
			return nil
		}
		id, _ := attrOf(se, "Id")
		typ, _ := attrOf(se, "Type")
		target, _ := attrOf(se, "Target")
		if strings.HasPrefix(target, "/") {
			target = path.Clean(strings.TrimPrefix(target, "/"))
		} else if owner != "" {
			target = path.Join(path.Dir(owner), target)
		} else {
			target = path.Clean(target)
		}
		out = append(out, relInfo{id, typ, target})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("xlsxedit: %s: %w", name, err)
	}
	return out, nil
}

func (w *Workbook) readWorkbook() error {
	rels, err := w.readRels("")
	if err != nil {
		return err
	}
	for _, r := range rels {
		if strings.HasSuffix(r.typ, "/officeDocument") {
			w.wbPath = r.target
			break
		}
	}
	if w.wbPath == "" {
		return errors.New("xlsxedit: package has no workbook relationship in _rels/.rels")
	}
	wbData, err := w.part(w.wbPath)
	if err != nil {
		return err
	}
	wrels, err := w.readRels(w.wbPath)
	if err != nil {
		return err
	}
	target := map[string]string{}
	for _, r := range wrels {
		target[r.id] = r.target
	}
	depth, root := 0, false
	err = scan(wbData, func(tok xml.Token, _, _ int) error {
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth == 1 {
				if t.Name.Local != "workbook" {
					return fmt.Errorf("root element is %q, not workbook", t.Name.Local)
				}
				root = true
			}
			if depth == 3 && t.Name.Local == "sheet" {
				name, _ := attrOf(t, "name")
				id, _ := attrOf(t, "sheetId")
				rid, _ := attrOf(t, "id")
				n, _ := strconv.Atoi(id)
				p, ok := target[rid]
				if !ok {
					return fmt.Errorf("sheet %q has no relationship %q", name, rid)
				}
				w.sheets = append(w.sheets, sheetEntry{name: name, sheetID: n, rid: rid, part: p})
			}
		case xml.EndElement:
			depth--
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("xlsxedit: %s: %w", w.wbPath, err)
	}
	if !root {
		return fmt.Errorf("xlsxedit: %s is empty", w.wbPath)
	}
	return nil
}

// Sheets lists the sheets in workbook order.
func (w *Workbook) Sheets() []SheetInfo {
	out := make([]SheetInfo, len(w.sheets))
	for i, s := range w.sheets {
		out[i] = SheetInfo{Name: s.name, Index: i + 1}
		if data, err := w.part(s.part); err == nil {
			if sc, err := scanSheet(data); err == nil && sc.hasDim {
				out[i].Dimension = sc.dimRef
			}
		}
	}
	return out
}

func (w *Workbook) lookup(sheet string) (*sheetEntry, error) {
	for i := range w.sheets {
		if strings.EqualFold(w.sheets[i].name, sheet) {
			return &w.sheets[i], nil
		}
	}
	return nil, fmt.Errorf("xlsxedit: no sheet named %q", sheet)
}

// SetCell writes value to the cell ref of the sheet. The sheet name is
// matched without regard to case.
//
// Text is written as an inline string (t="inlineStr"). The shared strings
// table is not changed, and a text cell that was a shared string becomes an
// inline one. A formula is written with SetCell as well: use Formula or
// FormulaCached. There is no separate SetFormula.
//
// The row and the cell are created in the right place when they are missing,
// so rows stay sorted by number and cells by column. A cell that exists is
// replaced and keeps its style index. A new cell has no style. The dimension
// ref of the sheet grows when the cell is outside it. A cell that is the
// master of a shared or array formula is refused.
//
// Every edit also sets fullCalcOnLoad in the workbook, see Bytes.
func (w *Workbook) SetCell(sheet, ref string, value Value) (err error) {
	defer guard(&err)
	col, row, err := parseRef(ref)
	if err != nil {
		return fmt.Errorf("xlsxedit: %w", err)
	}
	if err := validateValue(value); err != nil {
		return fmt.Errorf("xlsxedit: cell %s: %w", ref, err)
	}
	return w.editCell(sheet, ref, col, row, &value)
}

// ClearCell removes the value and the formula of one cell. The cell keeps its
// style, so a styled cell stays as an empty cell and a cell with no style is
// removed. A cell that does not exist is left as it is. The cached values of
// other formulas are not changed.
func (w *Workbook) ClearCell(sheet, ref string) (err error) {
	defer guard(&err)
	col, row, err := parseRef(ref)
	if err != nil {
		return fmt.Errorf("xlsxedit: %w", err)
	}
	return w.editCell(sheet, ref, col, row, nil)
}

func (w *Workbook) editCell(sheet, ref string, col, row int, v *Value) error {
	se, err := w.lookup(sheet)
	if err != nil {
		return err
	}
	data, err := w.part(se.part)
	if err != nil {
		return err
	}
	sc, err := scanSheet(data)
	if err != nil {
		return fmt.Errorf("xlsxedit: %s: %w", se.part, err)
	}
	if !sc.hasSD {
		return fmt.Errorf("xlsxedit: sheet %q has no sheetData", se.name)
	}
	p := sc.prefix
	var edits []edit
	ri := sc.rowIndex(row)
	var rs *rowSpan
	if ri >= 0 {
		rs = &sc.rows[ri]
	}
	ci := -1
	if rs != nil {
		for i, c := range rs.cells {
			if c.col == col {
				ci = i
				break
			}
		}
	}
	if ci >= 0 && rs.cells[ci].multi {
		return fmt.Errorf("xlsxedit: cell %s is the master of a shared or array formula, which is not supported", ref)
	}
	if v == nil { // clear
		if ci < 0 {
			return nil
		}
		c := rs.cells[ci]
		repl := ""
		if c.style != "" {
			repl = "<" + p + `c r="` + ref + `" s="` + esc(c.style) + `"/>`
		}
		edits = append(edits, edit{c.start, c.end, repl})
	} else {
		style := ""
		if ci >= 0 {
			style = rs.cells[ci].style
		}
		cx := valueXML(p, ref, style, *v)
		switch {
		case ci >= 0:
			edits = append(edits, edit{rs.cells[ci].start, rs.cells[ci].end, cx})
		case rs != nil && rs.selfClosed:
			tag := bytes.TrimRight(data[rs.start:rs.end-2], " \t\r\n")
			tag = spansRe.ReplaceAll(tag, nil)
			qn, _ := elemNames(data, rs.start)
			edits = append(edits, edit{rs.start, rs.end, string(tag) + ">" + cx + "</" + qn + ">"})
		case rs != nil:
			at := rs.closeStart
			for _, c := range rs.cells {
				if c.col > col {
					at = c.start
					break
				}
			}
			edits = append(edits, edit{at, at, cx})
			if tag := data[rs.start:rs.openEnd]; spansRe.Match(tag) {
				edits = append(edits, edit{rs.start, rs.openEnd, string(spansRe.ReplaceAll(tag, nil))})
			}
		default:
			rx := "<" + p + `row r="` + strconv.Itoa(row) + `">` + cx + "</" + p + "row>"
			at := -1
			for _, r := range sc.rows {
				if r.r > row {
					at = r.start
					break
				}
			}
			switch {
			case at >= 0:
				edits = append(edits, edit{at, at, rx})
			case sc.sd.selfClosed:
				edits = append(edits, edit{sc.sd.start, sc.sd.end,
					"<" + sc.sd.qname + ">" + rx + "</" + sc.sd.qname + ">"})
			default:
				edits = append(edits, edit{sc.sd.closeStart, sc.sd.closeStart, rx})
			}
		}
		if e, ok := sc.dimensionEdit(data, col, row); ok {
			edits = append(edits, e)
		}
	}
	out := applyEdits(data, edits)
	if err := w.ensureFullCalc(); err != nil {
		return err
	}
	w.changed[se.part] = out
	return nil
}

var topLevelAfterCalcPr = map[string]bool{
	"oleSize": true, "customWorkbookViews": true, "pivotCaches": true,
	"smartTagPr": true, "smartTagTypes": true, "webPublishing": true,
	"fileRecoveryPr": true, "webPublishObjects": true, "extLst": true,
}

// fullCalcEdit returns the workbook.xml bytes with fullCalcOnLoad="1" set.
func fullCalcEdit(data []byte) ([]byte, error) {
	depth := 0
	var calc *elem
	insertAt, rootClose := -1, -1
	var prefix string
	var rootSelf bool
	var root elem
	err := scan(data, func(tok xml.Token, start, end int) error {
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch {
			case depth == 1:
				root = elem{start: start, openEnd: end}
				root.qname, root.prefix = elemNames(data, start)
				prefix = root.prefix
			case depth == 2 && t.Name.Local == "calcPr" && calc == nil:
				calc = &elem{start: start, end: end}
			case depth == 2 && insertAt < 0 && topLevelAfterCalcPr[t.Name.Local]:
				insertAt = start
			}
		case xml.EndElement:
			if depth == 1 {
				rootClose = start
				rootSelf = end == root.openEnd
				root.end = end
			}
			depth--
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if root.end == 0 {
		return nil, errors.New("workbook has no root element")
	}
	if calc != nil {
		tag := data[calc.start:calc.end]
		if m := calcRe.Find(tag); m != nil && (bytes.Contains(m, []byte(`"1"`)) || bytes.Contains(m, []byte(`'1'`)) ||
			bytes.Contains(m, []byte(`"true"`)) || bytes.Contains(m, []byte(`'true'`))) {
			return data, nil
		}
		tag = calcRe.ReplaceAll(tag, nil)
		qn, _ := elemNames(tag, 0)
		ins := 1 + len(qn)
		nt := string(tag[:ins]) + ` fullCalcOnLoad="1"` + string(tag[ins:])
		return splice(data, calc.start, calc.end, nt), nil
	}
	calcXML := "<" + prefix + `calcPr fullCalcOnLoad="1"/>`
	switch {
	case rootSelf:
		return appendChild(data, root, calcXML), nil
	case insertAt >= 0:
		return splice(data, insertAt, insertAt, calcXML), nil
	}
	return splice(data, rootClose, rootClose, calcXML), nil
}

func (w *Workbook) ensureFullCalc() error {
	data, err := w.part(w.wbPath)
	if err != nil {
		return err
	}
	out, err := fullCalcEdit(data)
	if err != nil {
		return fmt.Errorf("xlsxedit: %s: %w", w.wbPath, err)
	}
	w.changed[w.wbPath] = out
	return nil
}

// AddSheet appends an empty sheet. The name must have 1 to 31 characters,
// none of [ ] : * ? / \, no apostrophe at the start or the end, and must not
// match another sheet name without regard to case.
//
// The package gets a new worksheet part, a sheet entry in the workbook with a
// new sheetId, a relationship with a new Id, and a content type override.
// docProps/app.xml is not changed, so its list of sheet titles can be out of
// date. Spreadsheet apps rebuild it on save.
func (w *Workbook) AddSheet(name string) (err error) {
	defer guard(&err)
	if err := validateSheetName(name); err != nil {
		return fmt.Errorf("xlsxedit: %w", err)
	}
	for _, s := range w.sheets {
		if strings.EqualFold(s.name, name) {
			return fmt.Errorf("xlsxedit: a sheet named %q already exists", s.name)
		}
	}
	wbData, err := w.part(w.wbPath)
	if err != nil {
		return err
	}
	wbRelsName := path.Join(path.Dir(w.wbPath), "_rels", path.Base(w.wbPath)+".rels")
	relsData, err := w.part(wbRelsName)
	if err != nil {
		return err
	}
	ctData, err := w.part("[Content_Types].xml")
	if err != nil {
		return err
	}

	// New identifiers.
	sheetID := 0
	for _, s := range w.sheets {
		sheetID = max(sheetID, s.sheetID)
	}
	sheetID++
	rels, err := w.readRels(w.wbPath)
	if err != nil {
		return err
	}
	used := map[string]bool{}
	maxN := 0
	for _, r := range rels {
		used[r.id] = true
		if n, err := strconv.Atoi(strings.TrimPrefix(r.id, "rId")); err == nil && strings.HasPrefix(r.id, "rId") {
			maxN = max(maxN, n)
		}
	}
	rid := "rId" + strconv.Itoa(maxN+1)
	for used[rid] {
		maxN++
		rid = "rId" + strconv.Itoa(maxN+1)
	}
	var partName string
	for n := len(w.sheets) + 1; ; n++ {
		partName = path.Join(path.Dir(w.wbPath), "worksheets", "sheet"+strconv.Itoa(n)+".xml")
		if _, ok := w.files[partName]; ok {
			continue
		}
		if _, ok := w.changed[partName]; ok {
			continue
		}
		break
	}

	// workbook.xml
	sheetsEl, ok, err := findElement(wbData, "sheets", 2)
	if err != nil {
		return fmt.Errorf("xlsxedit: %s: %w", w.wbPath, err)
	}
	if !ok {
		return fmt.Errorf("xlsxedit: %s has no sheets element", w.wbPath)
	}
	rp := ""
	_ = scan(wbData, func(tok xml.Token, _, _ int) error {
		if se, ok := tok.(xml.StartElement); ok && rp == "" {
			for _, a := range se.Attr {
				if a.Name.Space == "xmlns" && a.Value == nsR {
					rp = a.Name.Local
				}
			}
		}
		return nil
	})
	decl := ""
	if rp == "" {
		rp, decl = "r", ` xmlns:r="`+nsR+`"`
	}
	sheetXML := "<" + sheetsEl.prefix + `sheet name="` + esc(name) + `" sheetId="` + strconv.Itoa(sheetID) + `"` +
		decl + " " + rp + `:id="` + rid + `"/>`
	newWB := appendChild(wbData, sheetsEl, sheetXML)

	// workbook rels
	relsEl, ok, err := findElement(relsData, "Relationships", 1)
	if err != nil || !ok {
		return fmt.Errorf("xlsxedit: %s: no Relationships element", wbRelsName)
	}
	target := path.Join("worksheets", path.Base(partName))
	newRels := appendChild(relsData, relsEl, "<"+relsEl.prefix+`Relationship Id="`+rid+`" Type="`+relSheet+`" Target="`+esc(target)+`"/>`)

	// content types
	typesEl, ok, err := findElement(ctData, "Types", 1)
	if err != nil || !ok {
		return errors.New("xlsxedit: [Content_Types].xml has no Types element")
	}
	newCT := appendChild(ctData, typesEl, "<"+typesEl.prefix+`Override PartName="/`+esc(partName)+`" ContentType="`+ctWorksheet+`"/>`)

	newWB, err = fullCalcEdit(newWB)
	if err != nil {
		return fmt.Errorf("xlsxedit: %s: %w", w.wbPath, err)
	}
	sheetPart := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<worksheet xmlns="` + nsMain + `" xmlns:r="` + nsR + `"><sheetData/></worksheet>`

	w.changed[w.wbPath] = newWB
	w.changed[wbRelsName] = newRels
	w.changed["[Content_Types].xml"] = newCT
	w.changed[partName] = []byte(sheetPart)
	w.added = append(w.added, partName)
	w.sheets = append(w.sheets, sheetEntry{name: name, sheetID: sheetID, rid: rid, part: partName})
	return nil
}

// Bytes writes the workbook as an .xlsx package.
//
// Only the parts that an edit changed are written again: the sheets that
// were edited, xl/workbook.xml, and, after AddSheet, the workbook
// relationships, [Content_Types].xml and the new sheet. Every other part is
// copied as it is, with its compressed bytes, and the entries keep their
// original order. New parts come last.
//
// The cached values of formulas that an edit did not touch are left as they
// are, so they can be out of date. To make up for this, every edit sets
// fullCalcOnLoad="1" on calcPr in xl/workbook.xml (calcPr is added when it is
// missing), so spreadsheet apps recalculate all formulas when they open the
// file. A reader that does not calculate, such as officeread, still sees the
// old cached values.
func (w *Workbook) Bytes() (out []byte, err error) {
	defer guard(&err)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name string, mod time.Time, data []byte) error {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.Modified = mod
		f, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		return err
	}
	for _, f := range w.zr.File {
		if data, ok := w.changed[f.Name]; ok {
			if err := write(f.Name, f.Modified, data); err != nil {
				return nil, fmt.Errorf("xlsxedit: write %s: %w", f.Name, err)
			}
			continue
		}
		if err := zw.Copy(f); err != nil {
			return nil, fmt.Errorf("xlsxedit: copy %s: %w", f.Name, err)
		}
	}
	for _, name := range w.added {
		if err := write(name, time.Time{}, w.changed[name]); err != nil {
			return nil, fmt.Errorf("xlsxedit: write %s: %w", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("xlsxedit: close: %w", err)
	}
	return buf.Bytes(), nil
}

// Package docxedit edits an existing Word document (.docx, OOXML) with the
// standard library only. It is the edit side of the docx writer, the
// officeread reader and the officecheck checker.
//
// Open parses the package and Bytes writes it back. An edit rewrites only the
// bytes it touches inside word/document.xml. Every other part of the package
// is copied without change, and the zip entries keep their order.
//
// Scope of this version. Only top-level body paragraphs are addressable: a
// w:p that is a direct child of w:body. Paragraphs inside tables, text boxes,
// content controls, headers, footers, footnotes and comments are not listed
// and not edited. Images, tables, comments and other parts are never changed.
// Tracked changes are not interpreted: the text of inserted runs counts as
// text and deleted text (w:delText) does not.
package docxedit

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/mudler/LocalAI/addons/office/internal/ooxml"
)

const (
	nsW       = `http://schemas.openxmlformats.org/wordprocessingml/2006/main`
	relOffice = `http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument`
	relStyles = `http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles`
)

// Limits on what is unpacked.
const (
	maxEntry int64 = 64 << 20
	maxTotal int64 = 256 << 20
)

// ParagraphInfo describes one top-level body paragraph.
type ParagraphInfo struct {
	// Index is the 0-based position among the top-level body paragraphs.
	Index int
	// Style is the w:pStyle id, empty when the paragraph has none.
	Style string
	// Text is the text of the paragraph: w:t text, with a tab for w:tab and a
	// newline for w:br and w:cr.
	Text string
}

// Document is an opened .docx file. It is not safe for concurrent use.
type Document struct {
	raw     []byte // the original package
	zr      *zip.Reader
	main    string // name of the main document part
	body    []byte // current content of the main part
	changed bool
	styles  map[string]bool // paragraph style ids defined in the styles part
}

// Open parses data as a .docx file. It returns an error when data is not a
// zip, has an unsafe or duplicate entry name, is too large, has no main
// document part, or the main part is not well-formed WordprocessingML. It
// never panics.
func Open(data []byte) (doc *Document, err error) {
	defer recoverErr(&err)
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("docxedit: not a zip file: %w", err)
	}
	files := map[string]*zip.File{}
	var total int64
	for _, f := range zr.File {
		name := f.Name
		if strings.HasPrefix(name, "/") || strings.HasPrefix(name, "\\") || ooxml.HasDotDot(name) {
			return nil, fmt.Errorf("docxedit: unsafe entry name %q", name)
		}
		if _, dup := files[name]; dup {
			return nil, fmt.Errorf("docxedit: duplicate entry %q", name)
		}
		files[name] = f
		if f.UncompressedSize64 > uint64(maxEntry) {
			return nil, fmt.Errorf("docxedit: entry %q is larger than %d bytes", name, maxEntry)
		}
		total += int64(f.UncompressedSize64)
		if total > maxTotal {
			return nil, fmt.Errorf("docxedit: package is larger than %d bytes", maxTotal)
		}
	}
	d := &Document{raw: append([]byte(nil), data...), zr: zr}
	d.zr, err = zip.NewReader(bytes.NewReader(d.raw), int64(len(d.raw)))
	if err != nil {
		return nil, fmt.Errorf("docxedit: not a zip file: %w", err)
	}
	read := func(name string) ([]byte, bool, error) {
		f, ok := files[name]
		if !ok {
			return nil, false, nil
		}
		for _, g := range d.zr.File {
			if g.Name == name {
				f = g
				break
			}
		}
		rc, err := f.Open()
		if err != nil {
			return nil, true, fmt.Errorf("docxedit: cannot open %q: %w", name, err)
		}
		defer func() { _ = rc.Close() }()
		b, err := io.ReadAll(io.LimitReader(rc, maxEntry+1))
		if err != nil {
			return nil, true, fmt.Errorf("docxedit: cannot read %q: %w", name, err)
		}
		if int64(len(b)) > maxEntry {
			return nil, true, fmt.Errorf("docxedit: entry %q is larger than %d bytes", name, maxEntry)
		}
		return b, true, nil
	}

	rels, ok, err := read("_rels/.rels")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("docxedit: not a docx file: missing _rels/.rels")
	}
	target, err := findRel(rels, relOffice)
	if err != nil {
		return nil, err
	}
	d.main = resolve("", target)
	if d.main == "" {
		return nil, errors.New("docxedit: not a docx file: bad main part target")
	}
	d.body, ok, err = read(d.main)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("docxedit: main part %q is missing", d.main)
	}
	if _, err := scan(d.body); err != nil {
		return nil, err
	}

	// Paragraph styles come from the styles part of the main part, if any.
	relName := path.Join(path.Dir(d.main), "_rels", path.Base(d.main)+".rels")
	if mrels, ok, err := read(relName); err != nil {
		return nil, err
	} else if ok {
		if st, err := findRel(mrels, relStyles); err == nil {
			if sdata, ok, err := read(resolve(path.Dir(d.main), st)); err != nil {
				return nil, err
			} else if ok {
				d.styles, err = styleIDs(sdata)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	return d, nil
}

// Paragraphs lists the top-level body paragraphs in body order. Paragraphs
// inside tables are not listed.
func (d *Document) Paragraphs() []ParagraphInfo {
	s, err := scan(d.body)
	if err != nil {
		return nil
	}
	out := make([]ParagraphInfo, len(s.paras))
	for i, p := range s.paras {
		out[i] = ParagraphInfo{Index: i, Style: p.style, Text: p.text.String()}
	}
	return out
}

// SetText replaces the text of the top-level paragraph at index. The
// paragraph start tag and its paragraph properties (w:pPr) are kept, and so
// are the run properties (w:rPr) of the first text run. The text becomes one
// run; all other content of the paragraph (other runs, hyperlinks,
// bookmarks, fields) is dropped. It is an error when index is out of range or
// the paragraph has no text run (no w:t), for example an empty paragraph.
func (d *Document) SetText(index int, text string) (err error) {
	defer recoverErr(&err)
	s, err := scan(d.body)
	if err != nil {
		return err
	}
	p, err := s.para(index)
	if err != nil {
		return err
	}
	if !p.hasText {
		return fmt.Errorf("docxedit: paragraph %d has no text run", index)
	}
	var b bytes.Buffer
	b.Write(d.body[p.start:p.tagEnd])
	if p.hasPPr {
		b.Write(d.body[p.pPr[0]:p.pPr[1]])
	}
	b.WriteString(runXML(s.prefix, d.rPrBytes(p), text))
	b.WriteString("</" + p.name + ">")
	d.replace(p.start, p.end, b.Bytes())
	return nil
}

func (d *Document) rPrBytes(p *para) []byte {
	if p.hasRPr {
		return d.body[p.rPr[0]:p.rPr[1]]
	}
	return nil
}

// InsertParagraph inserts a new top-level paragraph after the paragraph at
// index after; -1 inserts at the start of the body. When style is not empty
// it must be a paragraph style id defined in the styles part (word/styles.xml)
// of the document, otherwise it is an error. An empty style gives a paragraph
// with no w:pStyle. An empty text gives a paragraph with no run.
func (d *Document) InsertParagraph(after int, style string, text string) (err error) {
	defer recoverErr(&err)
	s, err := scan(d.body)
	if err != nil {
		return err
	}
	pos := s.bodyTagEnd
	if after != -1 {
		p, err := s.para(after)
		if err != nil {
			return err
		}
		pos = p.end
	}
	if style != "" && !d.styles[style] {
		return fmt.Errorf("docxedit: paragraph style %q is not defined in the styles part", style)
	}
	var b strings.Builder
	pf := s.prefix
	b.WriteString("<" + pf + "p>")
	if style != "" {
		b.WriteString("<" + pf + "pPr><" + pf + `pStyle ` + pf + `val="` + ooxml.Escape(style) + `"/></` + pf + "pPr>")
	}
	if text != "" {
		b.WriteString(runXML(pf, nil, text))
	}
	b.WriteString("</" + pf + "p>")
	d.replace(pos, pos, []byte(b.String()))
	return nil
}

// DeleteParagraph removes the top-level paragraph at index. It is an error
// when index is out of range, or when the removal would leave the body empty
// or end it with a table (Word needs a paragraph after a final table). A
// paragraph whose properties hold a section break (w:sectPr) takes the break
// with it.
func (d *Document) DeleteParagraph(index int) (err error) {
	defer recoverErr(&err)
	s, err := scan(d.body)
	if err != nil {
		return err
	}
	p, err := s.para(index)
	if err != nil {
		return err
	}
	last := len(s.blocks) - 1
	if last == 0 {
		return errors.New("docxedit: cannot delete the only block of the body")
	}
	if p.block == last && s.blocks[last-1] == "tbl" {
		return fmt.Errorf("docxedit: cannot delete paragraph %d: it is the paragraph after the last table", index)
	}
	d.replace(p.start, p.end, nil)
	return nil
}

// ReplaceAll replaces every occurrence of old with new in the text of the
// top-level paragraphs and returns the number of replacements. Matching is
// done on the text of one w:t element at a time, so old is found only when it
// lies entirely inside one run's text. Text that Word split across several
// runs is not matched. Paragraphs inside tables, text boxes, headers and
// footers are not searched. Run properties are not changed. It is an error
// when old is empty.
func (d *Document) ReplaceAll(old, new string) (count int, err error) {
	defer recoverErr(&err)
	if old == "" {
		return 0, errors.New("docxedit: the text to replace is empty")
	}
	s, err := scan(d.body)
	if err != nil {
		return 0, err
	}
	var edits []edit
	for _, t := range s.ts {
		if t.selfClose || !strings.Contains(t.text, old) {
			continue
		}
		count += strings.Count(t.text, old)
		if !t.hasSpace {
			edits = append(edits, edit{t.bodyStart - 1, t.bodyStart - 1, []byte(` xml:space="preserve"`)})
		}
		edits = append(edits, edit{t.bodyStart, t.bodyEnd, []byte(ooxml.Escape(strings.ReplaceAll(t.text, old, new)))})
	}
	if count == 0 {
		return 0, nil
	}
	d.body = applyEdits(d.body, edits)
	d.changed = true
	return count, nil
}

// Bytes returns the document as a .docx package. Only word/document.xml is
// rewritten, and only when an edit changed it. Every other entry is copied as
// it was, in the original order.
func (d *Document) Bytes() (out []byte, err error) {
	defer recoverErr(&err)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range d.zr.File {
		if d.changed && f.Name == d.main {
			h := &zip.FileHeader{Name: f.Name, Method: zip.Deflate, Modified: f.Modified}
			w, err := zw.CreateHeader(h)
			if err != nil {
				return nil, fmt.Errorf("docxedit: create %s: %w", f.Name, err)
			}
			if _, err := w.Write(d.body); err != nil {
				return nil, fmt.Errorf("docxedit: write %s: %w", f.Name, err)
			}
			continue
		}
		if err := zw.Copy(f); err != nil {
			return nil, fmt.Errorf("docxedit: copy %s: %w", f.Name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("docxedit: close: %w", err)
	}
	return buf.Bytes(), nil
}

// ---- edits ----

type edit struct {
	start, end int
	data       []byte
}

func (d *Document) replace(start, end int, data []byte) {
	d.body = applyEdits(d.body, []edit{{start, end, data}})
	d.changed = true
}

func applyEdits(src []byte, edits []edit) []byte {
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
	var out bytes.Buffer
	pos := 0
	for _, e := range edits {
		out.Write(src[pos:e.start])
		out.Write(e.data)
		pos = e.end
	}
	out.Write(src[pos:])
	return out.Bytes()
}

func runXML(prefix string, rPr []byte, text string) string {
	return "<" + prefix + "r>" + string(rPr) + "<" + prefix + `t xml:space="preserve">` + ooxml.Escape(text) + "</" + prefix + "t></" + prefix + "r>"
}

func recoverErr(err *error) {
	if r := recover(); r != nil {
		*err = fmt.Errorf("docxedit: internal error: %v", r)
	}
}

// ---- package helpers ----

// resolve turns a relationship target into a part name. A target that starts
// with "/" is from the package root, any other is relative to base.
func resolve(base, target string) string {
	var p string
	if strings.HasPrefix(target, "/") {
		p = path.Clean(strings.TrimPrefix(target, "/"))
	} else {
		p = path.Join(base, target)
	}
	if p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return ""
	}
	return p
}

// findRel returns the target of the first relationship of the given type.
func findRel(data []byte, typ string) (string, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("docxedit: bad relationships part: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "Relationship" {
			continue
		}
		var t, target, mode string
		for _, a := range se.Attr {
			switch a.Name.Local {
			case "Type":
				t = a.Value
			case "Target":
				target = a.Value
			case "TargetMode":
				mode = a.Value
			}
		}
		if t == typ && mode != "External" && target != "" {
			return target, nil
		}
	}
	return "", fmt.Errorf("docxedit: no relationship of type %s", path.Base(typ))
}

// styleIDs returns the ids of the paragraph styles defined in a styles part.
func styleIDs(data []byte) (map[string]bool, error) {
	ids := map[string]bool{}
	d := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return ids, nil
		}
		if err != nil {
			return nil, fmt.Errorf("docxedit: bad styles part: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "style" || se.Name.Space != nsW {
			continue
		}
		var id, typ string
		for _, a := range se.Attr {
			switch a.Name.Local {
			case "styleId":
				id = a.Value
			case "type":
				typ = a.Value
			}
		}
		if id != "" && (typ == "" || typ == "paragraph") {
			ids[id] = true
		}
	}
}

// ---- scanning the main part ----

type para struct {
	start, tagEnd, end int    // byte range, and end of the start tag
	name               string // qualified name of the start tag
	block              int    // position among the body blocks
	hasPPr             bool
	pPr                [2]int
	hasRPr             bool
	rPr                [2]int // run properties of the first text run
	hasText            bool
	style              string
	text               strings.Builder
}

// tspan is one w:t element inside a top-level paragraph.
type tspan struct {
	bodyStart, bodyEnd int // range of the text between the tags
	selfClose          bool
	hasSpace           bool // the start tag has an xml:space attribute
	text               string
}

type scanned struct {
	paras      []*para
	blocks     []string // local names of the body children, without w:sectPr
	ts         []tspan
	bodyTagEnd int
	prefix     string // prefix of WordprocessingML elements, with the colon
}

func (s *scanned) para(i int) (*para, error) {
	if i < 0 || i >= len(s.paras) {
		return nil, fmt.Errorf("docxedit: paragraph index %d is out of range (0 to %d)", i, len(s.paras)-1)
	}
	return s.paras[i], nil
}

// rawName returns the qualified name of the element that starts at data[0]=='<'.
func rawName(data []byte) string {
	i := 1
	for i < len(data) && !strings.ContainsRune(" \t\r\n/>", rune(data[i])) {
		i++
	}
	return string(data[1:i])
}

func scan(data []byte) (*scanned, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	s := &scanned{bodyTagEnd: -1}
	var (
		stack   []xml.Name
		cur     *para
		nested  int // paragraphs nested inside cur (text boxes)
		inT     bool
		tIdx    = -1
		runRPr  [2]int
		hasRun  bool
		chosen  bool
		rPrFrom int
	)
	isW := func(n xml.Name, local string) bool { return n.Space == nsW && n.Local == local }
	for {
		off0 := int(d.InputOffset())
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("docxedit: bad document XML: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			stack = append(stack, t.Name)
			depth := len(stack)
			if depth == 1 && !isW(t.Name, "document") {
				return nil, errors.New("docxedit: the main part is not a WordprocessingML document")
			}
			if depth == 2 && isW(t.Name, "body") {
				s.bodyTagEnd = int(d.InputOffset())
				rn := rawName(data[off0:])
				s.prefix = ""
				if i := strings.IndexByte(rn, ':'); i >= 0 {
					s.prefix = rn[:i+1]
				}
				continue
			}
			if depth == 3 && isW(stack[1], "body") {
				switch {
				case isW(t.Name, "p"):
					cur = &para{start: off0, tagEnd: int(d.InputOffset()), name: rawName(data[off0:]), block: len(s.blocks)}
					nested, chosen, hasRun = 0, false, false
					s.paras = append(s.paras, cur)
					s.blocks = append(s.blocks, "p")
				case isW(t.Name, "sectPr"):
				default:
					s.blocks = append(s.blocks, t.Name.Local)
				}
				continue
			}
			if cur == nil || depth <= 3 {
				continue
			}
			if isW(t.Name, "p") {
				nested++
				continue
			}
			if nested > 0 {
				continue
			}
			parent := stack[depth-2]
			switch {
			case depth == 4 && isW(t.Name, "pPr"):
				cur.hasPPr, cur.pPr[0] = true, off0
			case depth == 5 && isW(t.Name, "pStyle") && isW(parent, "pPr"):
				for _, a := range t.Attr {
					if a.Name.Local == "val" {
						cur.style = a.Value
					}
				}
			case isW(t.Name, "r"):
				hasRun = false
			case isW(t.Name, "rPr") && isW(parent, "r"):
				rPrFrom = off0
			case isW(t.Name, "t"):
				ts := tspan{bodyStart: int(d.InputOffset())}
				for _, a := range t.Attr {
					if a.Name.Local == "space" {
						ts.hasSpace = true
					}
				}
				s.ts = append(s.ts, ts)
				tIdx, inT = len(s.ts)-1, true
			case (isW(t.Name, "tab") || isW(t.Name, "br") || isW(t.Name, "cr")) && isW(parent, "r"):
				if t.Name.Local == "tab" {
					cur.text.WriteByte('\t')
				} else {
					cur.text.WriteByte('\n')
				}
			}
		case xml.EndElement:
			depth := len(stack)
			if depth == 0 {
				return nil, errors.New("docxedit: bad document XML: unbalanced tags")
			}
			name := stack[depth-1]
			stack = stack[:depth-1]
			if cur != nil && depth == 3 && isW(name, "p") {
				cur.end = int(d.InputOffset())
				cur = nil
			}
			if cur == nil || depth <= 3 {
				continue
			}
			if isW(name, "p") {
				if nested > 0 {
					nested--
				}
				continue
			}
			if nested > 0 {
				continue
			}
			switch {
			case depth == 4 && isW(name, "pPr"):
				cur.pPr[1] = int(d.InputOffset())
			case isW(name, "rPr") && depth >= 5 && isW(stack[depth-2], "r"):
				runRPr, hasRun = [2]int{rPrFrom, int(d.InputOffset())}, true
			case isW(name, "t"):
				ts := &s.ts[tIdx]
				ts.bodyEnd = off0
				ts.selfClose = ts.bodyEnd == ts.bodyStart && data[ts.bodyStart-1] == '>' && data[ts.bodyStart-2] == '/'
				cur.hasText = true
				if !chosen {
					chosen = true
					if hasRun {
						cur.hasRPr, cur.rPr = true, runRPr
					}
				}
				inT = false
			}
		case xml.CharData:
			if inT && cur != nil && nested == 0 {
				s.ts[tIdx].text += string(t)
				cur.text.Write(t)
			}
		}
	}
	if s.bodyTagEnd < 0 {
		return nil, errors.New("docxedit: the document has no body")
	}
	return s, nil
}

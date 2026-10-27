package xlsxedit

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// scan walks the XML tokens of data. fn gets each token with the byte offset
// where it starts and the offset where it ends. A self-closing element gives
// a start token and an end token with the same end offset.
func scan(data []byte, fn func(tok xml.Token, start, end int) error) error {
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		prev := int(dec.InputOffset())
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := fn(tok, prev, int(dec.InputOffset())); err != nil {
			return err
		}
	}
}

func attrOf(se xml.StartElement, local string) (string, bool) {
	for _, a := range se.Attr {
		if a.Name.Local == local && (a.Name.Space == "" || a.Name.Space == nsR) {
			return a.Value, true
		}
	}
	return "", false
}

// elem is the byte range of one element in a document.
type elem struct {
	start, openEnd, closeStart, end int
	selfClosed                      bool
	qname                           string // name with its prefix, as written
	prefix                          string // "p:" or ""
}

func elemNames(data []byte, start int) (qname, prefix string) {
	i := start + 1
	for i < len(data) && !strings.ContainsRune(" \t\r\n/>", rune(data[i])) {
		i++
	}
	qname = string(data[start+1 : i])
	if k := strings.IndexByte(qname, ':'); k >= 0 {
		prefix = qname[:k+1]
	}
	return qname, prefix
}

// findElement finds the first element with the given local name at the given
// depth (the root element has depth 1).
func findElement(data []byte, local string, depth int) (elem, bool, error) {
	var e elem
	found, open, d := false, false, 0
	err := scan(data, func(tok xml.Token, start, end int) error {
		switch t := tok.(type) {
		case xml.StartElement:
			d++
			if !found && d == depth && t.Name.Local == local {
				found, open = true, true
				e = elem{start: start, openEnd: end}
				e.qname, e.prefix = elemNames(data, start)
			}
		case xml.EndElement:
			if open && d == depth {
				open = false
				e.closeStart, e.end = start, end
				e.selfClosed = end == e.openEnd
			}
			d--
		}
		return nil
	})
	return e, found, err
}

// appendChild adds child text as the last child of e.
func appendChild(data []byte, e elem, child string) []byte {
	if e.selfClosed {
		open := bytes.TrimRight(data[e.start:e.end-2], " \t\r\n")
		repl := string(open) + ">" + child + "</" + e.qname + ">"
		return splice(data, e.start, e.end, repl)
	}
	return splice(data, e.closeStart, e.closeStart, child)
}

func splice(data []byte, start, end int, repl string) []byte {
	out := make([]byte, 0, len(data)-(end-start)+len(repl))
	out = append(out, data[:start]...)
	out = append(out, repl...)
	return append(out, data[end:]...)
}

type edit struct {
	start, end int
	repl       string
}

// applyEdits applies non-overlapping edits, from the last to the first.
func applyEdits(data []byte, edits []edit) []byte {
	for i := 1; i < len(edits); i++ {
		for j := i; j > 0 && edits[j].start > edits[j-1].start; j-- {
			edits[j], edits[j-1] = edits[j-1], edits[j]
		}
	}
	for _, e := range edits {
		data = splice(data, e.start, e.end, e.repl)
	}
	return data
}

var (
	spansRe = regexp.MustCompile(`\sspans\s*=\s*("[^"]*"|'[^']*')`)
	refRe   = regexp.MustCompile(`(\sref\s*=\s*)("[^"]*"|'[^']*')`)
	calcRe  = regexp.MustCompile(`\sfullCalcOnLoad\s*=\s*("[^"]*"|'[^']*')`)
)

func esc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

type cellSpan struct {
	start, end int
	col        int
	style      string
	multi      bool // master of a shared or array formula
}

type rowSpan struct {
	start, end, openEnd, closeStart int
	r                               int
	selfClosed                      bool
	cells                           []cellSpan
}

// sheetScan is the layout of one worksheet part.
type sheetScan struct {
	sd        elem
	hasSD     bool
	prefix    string
	rows      []rowSpan
	dimStart  int
	dimEnd    int
	dimRef    string
	hasDim    bool
	dimIsNode bool
}

func scanSheet(data []byte) (*sheetScan, error) {
	s := &sheetScan{}
	depth := 0
	var row *rowSpan
	var cell *cellSpan
	lastRow, lastCol := 0, 0
	err := scan(data, func(tok xml.Token, start, end int) error {
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch {
			case depth == 1:
				if t.Name.Local != "worksheet" {
					return fmt.Errorf("root element is %q, not worksheet", t.Name.Local)
				}
			case depth == 2 && t.Name.Local == "dimension" && !s.hasDim:
				s.hasDim, s.dimStart, s.dimEnd = true, start, end
				s.dimRef, _ = attrOf(t, "ref")
			case depth == 2 && t.Name.Local == "sheetData" && !s.hasSD:
				s.hasSD = true
				s.sd = elem{start: start, openEnd: end}
				s.sd.qname, s.sd.prefix = elemNames(data, start)
				s.prefix = s.sd.prefix
			case depth == 3 && s.hasSD && s.sd.end == 0 && t.Name.Local == "row":
				r := lastRow + 1
				if v, ok := attrOf(t, "r"); ok {
					n, err := parseRowNumber(v)
					if err != nil {
						return err
					}
					r = n
				}
				lastRow, lastCol = r, 0
				s.rows = append(s.rows, rowSpan{start: start, openEnd: end, r: r})
				row = &s.rows[len(s.rows)-1]
			case depth == 4 && row != nil && t.Name.Local == "c":
				col := lastCol + 1
				if v, ok := attrOf(t, "r"); ok {
					c, r, err := parseRef(v)
					if err != nil {
						return err
					}
					if r != row.r {
						return fmt.Errorf("cell %s is inside row %d", v, row.r)
					}
					col = c
				}
				lastCol = col
				style, _ := attrOf(t, "s")
				row.cells = append(row.cells, cellSpan{start: start, col: col, style: style})
				cell = &row.cells[len(row.cells)-1]
			case depth == 5 && cell != nil && t.Name.Local == "f":
				ft, _ := attrOf(t, "t")
				_, hasRef := attrOf(t, "ref")
				if ft == "array" || (ft == "shared" && hasRef) {
					cell.multi = true
				}
			}
		case xml.EndElement:
			switch {
			case depth == 4 && cell != nil:
				cell.end = end
				cell = nil
			case depth == 3 && row != nil:
				row.end, row.closeStart = end, start
				row.selfClosed = end == row.openEnd
				row = nil
			case depth == 2 && s.hasSD && s.sd.end == 0 && t.Name.Local == "sheetData":
				s.sd.closeStart, s.sd.end = start, end
				s.sd.selfClosed = end == s.sd.openEnd
			}
			depth--
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if depth != 0 {
		return nil, fmt.Errorf("unexpected end of worksheet")
	}
	return s, nil
}

// rowIndex returns the index of row r, or -1.
func (s *sheetScan) rowIndex(r int) int {
	for i := range s.rows {
		if s.rows[i].r == r {
			return i
		}
	}
	return -1
}

// dimensionEdit widens the dimension ref so that it holds col and row.
func (s *sheetScan) dimensionEdit(data []byte, col, row int) (edit, bool) {
	if !s.hasDim || s.dimRef == "" {
		return edit{}, false
	}
	parts := strings.Split(s.dimRef, ":")
	if len(parts) > 2 {
		return edit{}, false
	}
	c1, r1, err := parseRef(parts[0])
	if err != nil {
		return edit{}, false
	}
	c2, r2 := c1, r1
	if len(parts) == 2 {
		if c2, r2, err = parseRef(parts[1]); err != nil {
			return edit{}, false
		}
	}
	nc1, nr1, nc2, nr2 := min(c1, col), min(r1, row), max(c2, col), max(r2, row)
	if nc1 == c1 && nr1 == r1 && nc2 == c2 && nr2 == r2 {
		return edit{}, false
	}
	ref := cellName(nc1, nr1)
	if nc1 != nc2 || nr1 != nr2 {
		ref += ":" + cellName(nc2, nr2)
	}
	tag := data[s.dimStart:s.dimEnd]
	out := refRe.ReplaceAll(tag, []byte(`${1}"`+ref+`"`))
	return edit{s.dimStart, s.dimEnd, string(out)}, true
}

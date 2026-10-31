package officeread

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
)

type sheetRef struct{ name, rid string }

func readXLSX(p parts, main string) ([]Section, error) {
	var refs []sheetRef
	err := walkPart(p, main, func(t xml.Token) {
		if se, ok := t.(xml.StartElement); ok && se.Name.Local == "sheet" {
			name, _ := attr(se, "name")
			refs = append(refs, sheetRef{name, relID(se)})
		}
	})
	if err != nil {
		return nil, err
	}
	rels, err := relsOf(p, main)
	if err != nil {
		return nil, err
	}
	shared, err := sharedStrings(p, rels)
	if err != nil {
		return nil, err
	}
	var out []Section
	for _, ref := range refs {
		r, ok := rels[ref.rid]
		if !ok {
			return nil, fmt.Errorf("officeread: sheet %q has no relationship %q", ref.name, ref.rid)
		}
		grid, err := readSheet(p, r.target, shared)
		if err != nil {
			return nil, err
		}
		out = append(out, Section{Title: ref.name, Text: gridText(grid), Tables: [][][]string{grid}})
	}
	return out, nil
}

// sharedStrings reads the optional shared strings table.
func sharedStrings(p parts, rels map[string]rel) ([]string, error) {
	name := "xl/sharedStrings.xml"
	for _, r := range rels {
		if strings.HasSuffix(r.typ, "/sharedStrings") {
			name = r.target
		}
	}
	if _, ok := p[name]; !ok {
		return nil, nil
	}
	var out []string
	var cur strings.Builder
	inSI, inT, phonetic := false, false, 0
	err := walkPart(p, name, func(t xml.Token) {
		switch t := t.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "si":
				inSI = true
				cur.Reset()
			case "rPh":
				phonetic++
			case "t":
				inT = inSI && phonetic == 0
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "si":
				out = append(out, cur.String())
				inSI = false
			case "rPh":
				phonetic--
			case "t":
				inT = false
			}
		case xml.CharData:
			if inT {
				cur.Write(t)
			}
		}
	})
	return out, err
}

// readSheet builds the grid of one worksheet from the cell references.
func readSheet(p parts, name string, shared []string) ([][]string, error) {
	type pos struct{ row, col int }
	cells := map[pos]string{}
	maxRow, maxCol := 0, 0
	var (
		cur         pos
		typ         string
		val         strings.Builder
		inCell, inV bool
		inF         bool
		formula     strings.Builder
		inIS, inT   bool
		nextRow     int
		nextCol     int
		sheetErr    error
	)
	err := walkPart(p, name, func(t xml.Token) {
		switch t := t.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "row":
				nextRow++
				if r, ok := attr(t, "r"); ok {
					if n, err := strconv.Atoi(r); err == nil && n > 0 {
						nextRow = n
					}
				}
				nextCol = 0
			case "c":
				inCell = true
				typ, _ = attr(t, "t")
				val.Reset()
				formula.Reset()
				nextCol++
				cur = pos{nextRow, nextCol}
				if r, ok := attr(t, "r"); ok {
					if row, col, ok := splitRef(r); ok {
						cur = pos{row, col}
					} else if sheetErr == nil {
						sheetErr = fmt.Errorf("officeread: %s: bad cell reference %q", name, r)
					}
				}
				nextRow, nextCol = cur.row, cur.col
			case "v":
				inV = inCell
			case "f":
				inF = inCell
			case "is":
				inIS = inCell
			case "t":
				inT = inIS
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v":
				inV = false
			case "f":
				inF = false
			case "is":
				inIS = false
			case "t":
				inT = false
			case "c":
				if inCell {
					text := cellText(typ, val.String(), shared)
					if text == "" && formula.Len() > 0 {
						text = "=" + strings.TrimPrefix(formula.String(), "=")
					}
					cells[cur] = text
					maxRow, maxCol = max(maxRow, cur.row), max(maxCol, cur.col)
				}
				inCell = false
			}
		case xml.CharData:
			if inV || inT {
				val.Write(t)
			}
			if inF {
				formula.Write(t)
			}
		}
	})
	if err != nil {
		return nil, err
	}
	if sheetErr != nil {
		return nil, sheetErr
	}
	if maxRow*maxCol > maxGridCells {
		return nil, fmt.Errorf("officeread: %s: sheet is too large (%d rows, %d columns)", name, maxRow, maxCol)
	}
	grid := make([][]string, maxRow)
	for r := range grid {
		grid[r] = make([]string, maxCol)
	}
	for k, v := range cells {
		grid[k.row-1][k.col-1] = v
	}
	return grid, nil
}

func cellText(typ, v string, shared []string) string {
	switch typ {
	case "s":
		i, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || i < 0 || i >= len(shared) {
			return ""
		}
		return shared[i]
	case "b":
		if strings.TrimSpace(v) == "1" {
			return "TRUE"
		}
		return "FALSE"
	}
	return v
}

// splitRef turns "B3" into row 3 and column 2.
func splitRef(ref string) (row, col int, ok bool) {
	i := 0
	for ; i < len(ref); i++ {
		c := ref[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		if c < 'A' || c > 'Z' {
			break
		}
		col = col*26 + int(c-'A') + 1
		if col > 16384 {
			return 0, 0, false
		}
	}
	n, err := strconv.Atoi(ref[i:])
	if i == 0 || err != nil || n < 1 || n > 1048576 {
		return 0, 0, false
	}
	return n, col, true
}

// gridText joins the cells with tabs, one row per line, without trailing
// empty cells.
func gridText(grid [][]string) string {
	lines := make([]string, len(grid))
	for i, row := range grid {
		end := len(row)
		for end > 0 && row[end-1] == "" {
			end--
		}
		lines[i] = strings.Join(row[:end], "\t")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// Package xlsx writes a spreadsheet (.xlsx, OOXML) with the standard library
// only. It builds the package with archive/zip and plain text, in the same way
// as deck/pptx. It has no reader.
//
// Text cells are stored as shared strings (xl/sharedStrings.xml). Each distinct
// string is written once and cells refer to it by index.
package xlsx

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
)

// Limits of the format.
const (
	maxSheetName = 31
	maxRow       = 1048576
	maxCol       = 16384
)

// Kind tells which field of a Value is set.
type Kind int

// Value kinds. KindEmpty is the zero value. On a formula cell it means that
// the formula has no cached value.
const (
	KindEmpty Kind = iota
	KindString
	KindNumber
	KindBool
)

// Value is the content of a cell, or the cached result of a formula.
type Value struct {
	Kind   Kind
	String string
	Number float64
	Bool   bool
}

// Str returns a string value.
func Str(s string) Value { return Value{Kind: KindString, String: s} }

// Num returns a number value.
func Num(n float64) Value { return Value{Kind: KindNumber, Number: n} }

// Boolean returns a boolean value.
func Boolean(b bool) Value { return Value{Kind: KindBool, Bool: b} }

// Cell is one cell. Ref is an A1 reference such as "B3" (upper case).
// When Formula is not empty the cell is a formula cell and Value is its
// optional cached result. A leading "=" in Formula is dropped.
type Cell struct {
	Ref     string
	Value   Value
	Formula string
}

// Column sets the options of one column. Columns are matched to Sheet.Columns
// by position: the first entry is column A.
type Column struct {
	// Width is the width in characters. Zero keeps the default width.
	Width float64
	// NumberFormat is a number format string such as "#,##0.00". It applies to
	// number and formula cells of the column. Empty means General.
	NumberFormat string
}

// Style is the look of a header row. Colours are six digit hex RGB values
// such as "1F2937", with no "#". Empty colours are left out.
type Style struct {
	Bold      bool
	FontColor string
	FillColor string
}

// Sheet is one worksheet.
type Sheet struct {
	Name  string
	Cells []Cell
	// Columns sets column widths and number formats by position.
	Columns []Column
	// HeaderRow is the 1-based row that gets Workbook.HeaderStyle. Zero means
	// no header row.
	HeaderRow int
	// FreezeTopRow keeps the first row visible when the sheet scrolls.
	FreezeTopRow bool
}

// Workbook is a whole spreadsheet file.
type Workbook struct {
	// Title is written to docProps/core.xml.
	Title string
	// Application is written to docProps/app.xml as the producing application.
	// Empty means the element is left out. The library has no default name.
	Application string
	// HeaderStyle is the style of the header row of each sheet.
	HeaderStyle Style
	Sheets      []Sheet
}

// Write validates wb and writes it to w as an .xlsx package.
func Write(w io.Writer, wb Workbook) error {
	b, err := build(wb)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(w)
	for _, p := range b.parts() {
		f, err := zw.Create(p.name)
		if err != nil {
			return fmt.Errorf("xlsx: create %s: %w", p.name, err)
		}
		if _, err := f.Write(p.data); err != nil {
			return fmt.Errorf("xlsx: write %s: %w", p.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("xlsx: close: %w", err)
	}
	return nil
}

// validateSheetName checks the rules of the format.
func validateSheetName(name string) error {
	if name == "" {
		return errors.New("sheet name is empty")
	}
	if n := len([]rune(name)); n > maxSheetName {
		return fmt.Errorf("sheet name %q is longer than %d characters", name, maxSheetName)
	}
	if strings.ContainsAny(name, `[]:*?/\`) {
		return fmt.Errorf("sheet name %q contains one of []:*?/\\", name)
	}
	if strings.HasPrefix(name, "'") || strings.HasSuffix(name, "'") {
		return fmt.Errorf("sheet name %q starts or ends with an apostrophe", name)
	}
	return nil
}

// parseRef splits an A1 reference into a 1-based column and row.
func parseRef(ref string) (col, row int, err error) {
	i := 0
	for i < len(ref) && ref[i] >= 'A' && ref[i] <= 'Z' {
		col = col*26 + int(ref[i]-'A') + 1
		i++
		if col > maxCol {
			return 0, 0, fmt.Errorf("cell ref %q: column out of range", ref)
		}
	}
	if i == 0 || i == len(ref) || ref[i] == '0' {
		return 0, 0, fmt.Errorf("cell ref %q is not valid", ref)
	}
	for ; i < len(ref); i++ {
		if ref[i] < '0' || ref[i] > '9' {
			return 0, 0, fmt.Errorf("cell ref %q is not valid", ref)
		}
		row = row*10 + int(ref[i]-'0')
		if row > maxRow {
			return 0, 0, fmt.Errorf("cell ref %q: row out of range", ref)
		}
	}
	return col, row, nil
}

func colName(col int) string {
	var s []byte
	for ; col > 0; col = (col - 1) / 26 {
		s = append([]byte{byte('A' + (col-1)%26)}, s...)
	}
	return string(s)
}

func validHex(s string) bool {
	if s == "" {
		return true
	}
	if len(s) != 6 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

func validateValue(ref string, v Value) error {
	if v.Kind == KindNumber && (math.IsNaN(v.Number) || math.IsInf(v.Number, 0)) {
		return fmt.Errorf("cell %s: number is not finite", ref)
	}
	if v.Kind < KindEmpty || v.Kind > KindBool {
		return fmt.Errorf("cell %s: unknown value kind %d", ref, v.Kind)
	}
	return nil
}

// builtCell is a validated cell with its position.
type builtCell struct {
	col, row int
	cell     Cell
	xf       int
}

type builtSheet struct {
	sheet Sheet
	cells []builtCell // sorted by row, then column
}

type builder struct {
	wb      Workbook
	sheets  []builtSheet
	strings []string
	strIdx  map[string]int
	xfs     []xfKey
	xfIdx   map[xfKey]int
	numFmts []string // custom formats, id is 164 + index
	fmtIdx  map[string]int
}

// xfKey identifies one cell format.
type xfKey struct {
	numFmt string
	header bool
}

func (b *builder) xf(k xfKey) int {
	if i, ok := b.xfIdx[k]; ok {
		return i
	}
	if k.numFmt != "" {
		if _, ok := b.fmtIdx[k.numFmt]; !ok {
			b.fmtIdx[k.numFmt] = len(b.numFmts)
			b.numFmts = append(b.numFmts, k.numFmt)
		}
	}
	b.xfs = append(b.xfs, k)
	b.xfIdx[k] = len(b.xfs) - 1
	return len(b.xfs) - 1
}

func (b *builder) sharedString(s string) int {
	if i, ok := b.strIdx[s]; ok {
		return i
	}
	b.strings = append(b.strings, s)
	b.strIdx[s] = len(b.strings) - 1
	return len(b.strings) - 1
}

func build(wb Workbook) (*builder, error) {
	if len(wb.Sheets) == 0 {
		return nil, errors.New("xlsx: workbook has no sheet")
	}
	if !validHex(wb.HeaderStyle.FontColor) || !validHex(wb.HeaderStyle.FillColor) {
		return nil, errors.New("xlsx: header style colour must be six hex digits")
	}
	b := &builder{
		wb:     wb,
		strIdx: map[string]int{},
		xfIdx:  map[xfKey]int{},
		fmtIdx: map[string]int{},
	}
	b.xf(xfKey{}) // xf 0 is the default format
	seen := map[string]bool{}
	for _, s := range wb.Sheets {
		if err := validateSheetName(s.Name); err != nil {
			return nil, fmt.Errorf("xlsx: %w", err)
		}
		key := strings.ToLower(s.Name)
		if seen[key] {
			return nil, fmt.Errorf("xlsx: duplicate sheet name %q", s.Name)
		}
		seen[key] = true
		if s.HeaderRow < 0 || s.HeaderRow > maxRow {
			return nil, fmt.Errorf("xlsx: sheet %q: header row out of range", s.Name)
		}
		if len(s.Columns) > maxCol {
			return nil, fmt.Errorf("xlsx: sheet %q: too many columns", s.Name)
		}
		for i, c := range s.Columns {
			if c.Width < 0 || math.IsNaN(c.Width) || math.IsInf(c.Width, 0) || c.Width > 255 {
				return nil, fmt.Errorf("xlsx: sheet %q: column %s width out of range", s.Name, colName(i+1))
			}
		}
		bs := builtSheet{sheet: s}
		pos := map[[2]int]bool{}
		for _, c := range s.Cells {
			col, row, err := parseRef(c.Ref)
			if err != nil {
				return nil, fmt.Errorf("xlsx: sheet %q: %w", s.Name, err)
			}
			if pos[[2]int{col, row}] {
				return nil, fmt.Errorf("xlsx: sheet %q: duplicate cell %s", s.Name, c.Ref)
			}
			pos[[2]int{col, row}] = true
			if err := validateValue(c.Ref, c.Value); err != nil {
				return nil, fmt.Errorf("xlsx: sheet %q: %w", s.Name, err)
			}
			c.Formula = strings.TrimPrefix(c.Formula, "=")
			bc := builtCell{col: col, row: row, cell: c}
			switch {
			case row == s.HeaderRow:
				bc.xf = b.xf(xfKey{header: true})
			case col <= len(s.Columns) && s.Columns[col-1].NumberFormat != "" &&
				(c.Formula != "" || c.Value.Kind == KindNumber):
				bc.xf = b.xf(xfKey{numFmt: s.Columns[col-1].NumberFormat})
			}
			if c.Formula == "" && c.Value.Kind == KindString {
				b.sharedString(c.Value.String)
			}
			bs.cells = append(bs.cells, bc)
		}
		sort.Slice(bs.cells, func(i, j int) bool {
			if bs.cells[i].row != bs.cells[j].row {
				return bs.cells[i].row < bs.cells[j].row
			}
			return bs.cells[i].col < bs.cells[j].col
		})
		b.sheets = append(b.sheets, bs)
	}
	// Column default formats are registered after the cells so xf ids of
	// cells stay stable.
	for _, s := range wb.Sheets {
		for _, c := range s.Columns {
			if c.NumberFormat != "" {
				b.xf(xfKey{numFmt: c.NumberFormat})
			}
		}
	}
	return b, nil
}

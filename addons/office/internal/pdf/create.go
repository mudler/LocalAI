package pdf

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/go-pdf/fpdf"
)

// Heading levels the writer supports.
const (
	minHeadingLevel = 1
	maxHeadingLevel = 3
)

// Layout in millimetres (A4, portrait). Body text is 11 pt, table text 9 pt.
const (
	pageMargin   = 20.0
	pageHeight   = 297.0
	bodySize     = 11.0
	bodyLine     = 5.0
	tableSize    = 9.0
	tableLine    = 4.2
	tablePad     = 1.5
	bulletIndent = 6.0
	coreFamily   = "Helvetica"
	userFamily   = "pdfuser"
)

// headingSize and headingLine give the font size (pt) and line height (mm)
// of each heading level.
var (
	headingSize = [...]float64{0, 20, 16, 13}
	headingLine = [...]float64{0, 9, 7.5, 6.5}
)

// Block is one element of a page. The block types are Heading, Paragraph,
// Bullets and Table, as values (not pointers). The method set is private,
// so no other type can be a Block.
type Block interface {
	validate() error
	texts() []string
	render(l *layout)
}

// Heading is a section title. Level is 1 to 3.
type Heading struct {
	Level int
	Text  string
}

// Paragraph is a body paragraph. It wraps to the page width. An empty Text
// is an empty line.
type Paragraph struct {
	Text string
}

// Bullets is a bulleted list. It needs at least one item and no item may be
// empty.
type Bullets struct {
	Items []string
}

// Table is a grid with a header row and body rows. Every row has as many
// cells as Header. Columns share the text width equally. Cell text wraps,
// and the row grows to fit it.
type Table struct {
	Header []string
	Rows   [][]string
}

// Page is one page of a Spec. Each Page starts on a new PDF page. Text that
// does not fit goes on further pages, so a Page can take more than one.
type Page struct {
	Blocks []Block
}

// Spec is a whole PDF document.
type Spec struct {
	// Title and Author are written to the document information.
	Title  string
	Author string
	Pages  []Page
	// FontTTF is an optional Unicode TrueType font. With it, any text is
	// allowed. Without it, the built-in Helvetica font is used and text
	// outside Latin-1 (U+0000 to U+00FF) is an error.
	FontTTF []byte
}

func (h Heading) validate() error {
	if h.Level < minHeadingLevel || h.Level > maxHeadingLevel {
		return fmt.Errorf("heading level %d is not between %d and %d", h.Level, minHeadingLevel, maxHeadingLevel)
	}
	if strings.TrimSpace(h.Text) == "" {
		return errors.New("heading text is empty")
	}
	return nil
}

func (p Paragraph) validate() error { return nil }

func (b Bullets) validate() error {
	if len(b.Items) == 0 {
		return errors.New("bullet list has no item")
	}
	for i, it := range b.Items {
		if strings.TrimSpace(it) == "" {
			return fmt.Errorf("bullet item %d is empty", i+1)
		}
	}
	return nil
}

func (t Table) validate() error {
	if len(t.Header) == 0 {
		return errors.New("table has no header")
	}
	for i, r := range t.Rows {
		if len(r) != len(t.Header) {
			return fmt.Errorf("table row %d has %d cells, header has %d", i+1, len(r), len(t.Header))
		}
	}
	return nil
}

func (h Heading) texts() []string   { return []string{h.Text} }
func (p Paragraph) texts() []string { return []string{p.Text} }
func (b Bullets) texts() []string   { return b.Items }

func (t Table) texts() []string {
	out := append([]string{}, t.Header...)
	for _, r := range t.Rows {
		out = append(out, r...)
	}
	return out
}

// validate checks the spec, its limits and, without FontTTF, the characters.
func (s Spec) validate() error {
	if len(s.Pages) == 0 {
		return errors.New("spec has no pages")
	}
	if len(s.Pages) > MaxPages {
		return fmt.Errorf("spec has %d pages, the limit is %d", len(s.Pages), MaxPages)
	}
	blocks := 0
	for i, p := range s.Pages {
		blocks += len(p.Blocks)
		for j, b := range p.Blocks {
			if b == nil {
				return fmt.Errorf("page %d block %d is nil", i+1, j+1)
			}
			if err := b.validate(); err != nil {
				return fmt.Errorf("page %d block %d: %w", i+1, j+1, err)
			}
		}
	}
	if blocks > MaxBlocks {
		return fmt.Errorf("spec has %d blocks, the limit is %d", blocks, MaxBlocks)
	}
	if len(s.FontTTF) > MaxInputBytes {
		return fmt.Errorf("font is %d bytes, the limit is %d", len(s.FontTTF), MaxInputBytes)
	}
	if s.FontTTF != nil {
		if !isSFNT(s.FontTTF) {
			return errors.New("font is not a TrueType or OpenType file")
		}
		return nil
	}
	for i, p := range s.Pages {
		for _, b := range p.Blocks {
			for _, t := range b.texts() {
				if err := checkLatin1(t); err != nil {
					return fmt.Errorf("page %d: %w", i+1, err)
				}
			}
		}
	}
	return nil
}

// isSFNT reports whether b starts with a TrueType or OpenType signature.
func isSFNT(b []byte) bool {
	if len(b) < 4 {
		return false
	}
	switch string(b[:4]) {
	case "\x00\x01\x00\x00", "true", "OTTO":
		return true
	}
	return false
}

// checkLatin1 refuses any rune above U+00FF and the control characters, which
// the built-in font cannot draw. Tab and newline are allowed. It does not echo
// the text.
func checkLatin1(s string) error {
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
		case r < 0x20 || (r >= 0x7F && r <= 0x9F):
			return fmt.Errorf("character U+%04X is a control character", r)
		case r > 0xFF:
			return fmt.Errorf("character U+%04X is outside Latin-1: set Spec.FontTTF to a Unicode font", r)
		}
	}
	return nil
}

// latin1Bytes turns Latin-1 text into the single-byte form fpdf expects for
// the built-in fonts (cp1252). For Latin-1 the code point is the byte. The
// text was checked by checkLatin1 first.
func latin1Bytes(s string) string {
	b := make([]byte, 0, len(s))
	for _, r := range s {
		b = append(b, byte(r)) // #nosec G115 -- checkLatin1 accepted the text, so every rune is below 256
	}
	return string(b)
}

// Create writes a new PDF from spec. The output is checked with Check
// before it is returned.
func Create(spec Spec) ([]byte, error) {
	if err := spec.validate(); err != nil {
		return nil, fmt.Errorf("pdf: create: %w", err)
	}
	var out []byte
	err := guard("create", func() error {
		b, err := render(spec)
		if err != nil {
			return err
		}
		out = b
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := Check(out); err != nil {
		return nil, fmt.Errorf("pdf: create: output failed the check: %w", err)
	}
	return out, nil
}

// layout holds the drawing state of one document.
type layout struct {
	pdf    *fpdf.Fpdf
	family string
	ttf    bool
	left   float64
	width  float64
	err    error
}

// text returns s in the encoding fpdf expects for the current font: UTF-8
// for the TrueType font, single-byte Latin-1 for the built-in font.
func (l *layout) text(s string) string {
	if l.ttf {
		return s
	}
	return latin1Bytes(s)
}

// bullet is the glyph that starts a list item, in the current encoding.
func (l *layout) bullet() string {
	if l.ttf {
		return "\u2022"
	}
	return "\x95" // bullet in cp1252, the built-in font encoding
}

// font selects the family and size. Bold is used only with the built-in font,
// because a TrueType font is embedded in one style.
func (l *layout) font(size float64, bold bool) {
	style := ""
	if bold && !l.ttf {
		style = "B"
	}
	l.pdf.SetFont(l.family, style, size)
}

// room is the height of the text area of one page.
func (l *layout) room() float64 {
	_, ph := l.pdf.GetPageSize()
	return ph - 2*pageMargin
}

// fail records the first layout error. Later blocks are skipped.
func (l *layout) fail(msg string) {
	if l.err == nil {
		l.err = errors.New(msg)
	}
}

// ensure starts a new page when h millimetres do not fit above the bottom
// margin. It reports whether it did.
func (l *layout) ensure(h float64) bool {
	_, ph := l.pdf.GetPageSize()
	if l.pdf.GetY()+h > ph-pageMargin {
		l.pdf.AddPage()
		l.pdf.SetX(l.left)
		return true
	}
	return false
}

func render(spec Spec) ([]byte, error) {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetTitle(spec.Title, true)
	pdf.SetAuthor(spec.Author, true)
	pdf.SetMargins(pageMargin, pageMargin, pageMargin)
	pdf.SetAutoPageBreak(true, pageMargin)

	l := &layout{pdf: pdf, family: coreFamily, left: pageMargin, width: 210 - 2*pageMargin}
	if spec.FontTTF != nil {
		l.family = userFamily
		l.ttf = true
		pdf.AddUTF8FontFromBytes(userFamily, "", spec.FontTTF)
		if err := pdf.Error(); err != nil {
			return nil, fmt.Errorf("pdf: create: font: %w", err)
		}
	}

	for _, p := range spec.Pages {
		pdf.AddPage()
		pdf.SetX(l.left)
		for _, b := range p.Blocks {
			if l.err == nil {
				b.render(l)
			}
		}
	}
	if l.err != nil {
		return nil, fmt.Errorf("pdf: create: %w", l.err)
	}
	if err := pdf.Error(); err != nil {
		return nil, fmt.Errorf("pdf: create: %w", err)
	}
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("pdf: create: write: %w", err)
	}
	return buf.Bytes(), nil
}

func (h Heading) render(l *layout) {
	size, line := headingSize[h.Level], headingLine[h.Level]
	l.pdf.Ln(2)
	l.ensure(2 * line)
	l.pdf.SetX(l.left)
	l.font(size, true)
	l.pdf.MultiCell(0, line, l.text(h.Text), "", "L", false)
	l.pdf.Ln(2)
}

func (p Paragraph) render(l *layout) {
	l.ensure(bodyLine)
	l.pdf.SetX(l.left)
	l.font(bodySize, false)
	if p.Text == "" {
		l.pdf.Ln(bodyLine)
		return
	}
	l.pdf.MultiCell(0, bodyLine, l.text(p.Text), "", "L", false)
	l.pdf.Ln(2)
}

func (b Bullets) render(l *layout) {
	l.font(bodySize, false)
	for _, item := range b.Items {
		l.ensure(bodyLine)
		y := l.pdf.GetY()
		l.pdf.SetXY(l.left+1, y)
		l.pdf.CellFormat(4, bodyLine, l.bullet(), "", 0, "L", false, 0, "")
		l.pdf.SetXY(l.left+bulletIndent, y)
		l.pdf.MultiCell(l.width-bulletIndent, bodyLine, l.text(item), "", "L", false)
	}
	l.pdf.SetX(l.left)
	l.pdf.Ln(2)
}

func (t Table) render(l *layout) {
	cw := l.width / float64(len(t.Header))
	hh := l.rowHeight(t.Header, cw, true)
	if hh > l.room() {
		l.fail("table header is taller than a page")
		return
	}
	l.ensure(hh)
	l.drawRow(t.Header, cw, hh, true)
	for _, row := range t.Rows {
		h := l.rowHeight(row, cw, false)
		if h+hh > l.room() {
			l.fail("table row does not fit on a page together with the header")
			return
		}
		if l.ensure(h) {
			// The row starts a new page, so the header repeats there.
			l.drawRow(t.Header, cw, hh, true)
		}
		l.drawRow(row, cw, h, false)
	}
	l.pdf.SetX(l.left)
	l.pdf.Ln(3)
}

// rowHeight returns the height of a row, which fits its tallest wrapped cell.
func (l *layout) rowHeight(cells []string, cw float64, header bool) float64 {
	l.font(tableSize, header)
	n := 1
	for _, c := range cells {
		if k := len(l.pdf.SplitLines([]byte(l.text(c)), cw-2*tablePad)); k > n {
			n = k
		}
	}
	return float64(n)*tableLine + 2*tablePad
}

// drawRow draws one row of h millimetres at the current y, then moves below it.
func (l *layout) drawRow(cells []string, cw, h float64, header bool) {
	y := l.pdf.GetY()
	l.font(tableSize, header)
	border := "D"
	if header {
		border = "DF"
		l.pdf.SetFillColor(230, 230, 230)
	}
	for i, c := range cells {
		x := l.left + float64(i)*cw
		l.pdf.Rect(x, y, cw, h, border)
		l.pdf.SetXY(x+tablePad, y+tablePad)
		l.pdf.MultiCell(cw-2*tablePad, tableLine, l.text(c), "", "L", false)
	}
	l.pdf.SetXY(l.left, y+h)
}

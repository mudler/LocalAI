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
	maxHeadingLevel = 6
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
	headingSize = [...]float64{0, 20, 16, 13, 12, 11, 11}
	headingLine = [...]float64{0, 9, 7.5, 6.5, 6, 5.5, 5.5}
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
	Runs  []Run
}

// Paragraph is a body paragraph. It wraps to the page width. An empty Text
// is an empty line.
type Run struct {
	Text   string
	Bold   bool
	Italic bool
}

type Paragraph struct {
	Text string
	Runs []Run
}

// Bullets is a bulleted list. It needs at least one item and no item may be
// empty.
type Bullets struct {
	Items     []string
	RichItems [][]Run
	Ordered   bool
	Start     int
	// StartSet distinguishes an explicit zero from the legacy default of one.
	StartSet bool
	Indent   int
}

// Table is a grid with a header row and body rows. Every row has as many
// cells as Header. Columns share the text width equally. Cell text wraps,
// and the row grows to fit it.
type Table struct {
	Header     []string
	Rows       [][]string
	HeaderRuns [][]Run
	RowRuns    [][][]Run
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
	if h.Text != "" && len(h.Runs) > 0 {
		return errors.New("heading sets both Text and Runs")
	}
	if h.Level < minHeadingLevel || h.Level > maxHeadingLevel {
		return fmt.Errorf("heading level %d is not between %d and %d", h.Level, minHeadingLevel, maxHeadingLevel)
	}
	if strings.TrimSpace(plain(h.Text, h.Runs)) == "" {
		return errors.New("heading text is empty")
	}
	return nil
}

func (p Paragraph) validate() error {
	if p.Text != "" && len(p.Runs) > 0 {
		return errors.New("paragraph sets both Text and Runs")
	}
	return nil
}

func (b Bullets) validate() error {
	if len(b.Items) > 0 && len(b.RichItems) > 0 {
		return errors.New("list sets both Items and RichItems")
	}
	if b.Start < 0 || b.Indent < 0 || b.Indent > 64 {
		return errors.New("invalid list position")
	}
	if len(b.texts()) == 0 {
		return errors.New("bullet list has no item")
	}
	for i, it := range b.texts() {
		if strings.TrimSpace(it) == "" {
			return fmt.Errorf("bullet item %d is empty", i+1)
		}
	}
	return nil
}

func (t Table) validate() error {
	if t.HeaderRuns != nil && len(t.HeaderRuns) != len(t.Header) {
		return errors.New("table header runs shape mismatch")
	}
	if t.RowRuns != nil {
		if len(t.RowRuns) != len(t.Rows) {
			return errors.New("table row runs shape mismatch")
		}
		for i, r := range t.RowRuns {
			if len(r) != len(t.Rows[i]) {
				return errors.New("table cell runs shape mismatch")
			}
		}
	}

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

func plain(text string, runs []Run) string {
	var b strings.Builder
	b.WriteString(text)
	for _, r := range runs {
		b.WriteString(r.Text)
	}
	return b.String()
}
func rich(text string, runs []Run) []Run {
	if len(runs) > 0 {
		return runs
	}
	return []Run{{Text: text}}
}
func (h Heading) texts() []string   { return []string{plain(h.Text, h.Runs)} }
func (p Paragraph) texts() []string { return []string{plain(p.Text, p.Runs)} }
func (b Bullets) texts() []string {
	if len(b.RichItems) == 0 {
		return b.Items
	}
	out := make([]string, len(b.RichItems))
	for i, r := range b.RichItems {
		out[i] = plain("", r)
	}
	return out
}
func (t Table) texts() []string {
	out := []string{}
	for i, c := range t.Header {
		var r []Run
		if t.HeaderRuns != nil {
			r = t.HeaderRuns[i]
		}
		out = append(out, plain(c, r))
	}
	for i, row := range t.Rows {
		for j, c := range row {
			var r []Run
			if t.RowRuns != nil {
				r = t.RowRuns[i][j]
			}
			out = append(out, plain(c, r))
		}
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

// styledFont selects a built-in emphasis variant. Existing single-style TTF
// users keep their existing behaviour; Markdown creation uses the core fonts.
func (l *layout) styledFont(size float64, bold, italic bool) {
	style := ""
	if !l.ttf {
		if bold {
			style += "B"
		}
		if italic {
			style += "I"
		}
	}
	l.pdf.SetFont(l.family, style, size)
}

// richLines wraps using the actual font metrics of every run, retaining style
// on both sides of a wrap. Long words split at rune boundaries.
func (l *layout) richLines(runs []Run, width, size float64, bold bool) [][]Run {
	lines := [][]Run{{}}
	used := 0.0
	add := func(r Run) {
		last := len(lines) - 1
		line := lines[last]
		if len(line) > 0 && line[len(line)-1].Bold == r.Bold && line[len(line)-1].Italic == r.Italic {
			line[len(line)-1].Text += r.Text
			lines[last] = line
		} else {
			lines[last] = append(line, r)
		}
	}
	newline := func() { lines = append(lines, []Run{}); used = 0 }
	// Words include their trailing spaces, allowing normal word wrapping.
	for _, run := range runs {
		l.styledFont(size, bold || run.Bold, run.Italic)
		var word strings.Builder
		flush := func() {
			s := word.String()
			word.Reset()
			if s == "" {
				return
			}
			w := l.pdf.GetStringWidth(l.text(s))
			if used > 0 && used+w > width {
				newline()
			}
			for _, ch := range s {
				t := string(ch)
				cw := l.pdf.GetStringWidth(l.text(t))
				if used > 0 && used+cw > width {
					newline()
				}
				r := run
				r.Text = t
				add(r)
				used += cw
			}
		}
		for _, ch := range run.Text {
			if ch == '\n' {
				flush()
				newline()
			} else {
				word.WriteRune(ch)
				if ch == ' ' || ch == '\t' {
					flush()
				}
			}
		}
		flush()
	}
	return lines
}
func (l *layout) drawLines(lines [][]Run, x, line, size float64, bold, paginate bool) {
	// Widths above are glyph advances, and table padding is already explicit.
	// CellFormat's implicit inset otherwise pushes the final glyph past the
	// measured right edge, especially when mixed styles meet at a cell wrap.
	margin := l.pdf.GetCellMargin()
	l.pdf.SetCellMargin(0)
	defer l.pdf.SetCellMargin(margin)
	for _, runs := range lines {
		if paginate {
			l.ensure(line)
		}
		y := l.pdf.GetY()
		l.pdf.SetX(x)
		for _, r := range runs {
			l.styledFont(size, bold || r.Bold, r.Italic)
			s := l.text(r.Text)
			l.pdf.CellFormat(l.pdf.GetStringWidth(s), line, s, "", 0, "L", false, 0, "")
		}
		l.pdf.SetXY(x, y+line)
	}
}
func (h Heading) render(l *layout) {
	size, line := headingSize[h.Level], headingLine[h.Level]
	l.pdf.Ln(2)
	l.ensure(2 * line)
	l.drawLines(l.richLines(rich(h.Text, h.Runs), l.width, size, true), l.left, line, size, true, true)
	l.pdf.Ln(2)
}
func (p Paragraph) render(l *layout) {
	l.drawLines(l.richLines(rich(p.Text, p.Runs), l.width, bodySize, false), l.left, bodyLine, bodySize, false, true)
	l.pdf.Ln(2)
}
func (b Bullets) render(l *layout) {
	number := b.Start
	if number == 0 && !b.StartSet {
		number = 1
	}
	x := l.left + float64(b.Indent)*bulletIndent
	for i, item := range b.texts() {
		var r []Run
		if len(b.RichItems) > 0 {
			r = b.RichItems[i]
			item = ""
		}
		width := l.width - (x - l.left) - bulletIndent
		if width < 10 {
			l.fail("list nesting leaves insufficient text width")
			return
		}
		l.ensure(bodyLine)
		y := l.pdf.GetY()
		l.styledFont(bodySize, false, false)
		l.pdf.SetXY(x+1, y)
		marker := l.bullet()
		if b.Ordered {
			marker = fmt.Sprintf("%d.", number+i)
		}
		l.pdf.CellFormat(bulletIndent, bodyLine, marker, "", 0, "L", false, 0, "")
		l.pdf.SetY(y)
		l.drawLines(l.richLines(rich(item, r), width, bodySize, false), x+bulletIndent, bodyLine, bodySize, false, true)
	}
	l.pdf.SetX(l.left)
	l.pdf.Ln(2)
}
func (t Table) render(l *layout) {
	cw := l.width / float64(len(t.Header))
	header := l.tableLines(t.Header, t.HeaderRuns, cw, true)
	hh := rowHeight(header)
	if hh > l.room() {
		l.fail("table header is taller than a page")
		return
	}
	l.ensure(hh)
	l.drawRow(header, cw, hh, true)
	for i, row := range t.Rows {
		var runs [][]Run
		if t.RowRuns != nil {
			runs = t.RowRuns[i]
		}
		lines := l.tableLines(row, runs, cw, false)
		h := rowHeight(lines)
		if h+hh > l.room() {
			l.fail("table row does not fit on a page together with the header")
			return
		}
		if l.ensure(h) {
			l.drawRow(header, cw, hh, true)
		}
		l.drawRow(lines, cw, h, false)
	}
	l.pdf.SetX(l.left)
	l.pdf.Ln(3)
}
func (l *layout) tableLines(cells []string, runs [][]Run, cw float64, header bool) [][][]Run {
	out := make([][][]Run, len(cells))
	for i, c := range cells {
		var r []Run
		if runs != nil {
			r = runs[i]
			c = ""
		}
		out[i] = l.richLines(rich(c, r), cw-2*tablePad, tableSize, header)
	}
	return out
}
func rowHeight(cells [][][]Run) float64 {
	n := 1
	for _, lines := range cells {
		if len(lines) > n {
			n = len(lines)
		}
	}
	return float64(n)*tableLine + 2*tablePad
}
func (l *layout) drawRow(cells [][][]Run, cw, h float64, header bool) {
	y := l.pdf.GetY()
	border := "D"
	if header {
		border = "DF"
		l.pdf.SetFillColor(230, 230, 230)
	}
	for i, lines := range cells {
		x := l.left + float64(i)*cw
		l.pdf.Rect(x, y, cw, h, border)
		l.pdf.SetXY(x+tablePad, y+tablePad)
		l.drawLines(lines, x+tablePad, tableLine, tableSize, header, false)
	}
	l.pdf.SetXY(l.left, y+h)
}

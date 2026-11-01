// Package docx writes a Word document (.docx, OOXML) with the standard
// library only. It builds the package with archive/zip and plain text, in the
// same way as xlsx and deck/pptx. It has no reader.
//
// A Document is an ordered list of blocks: Heading, Paragraph, Bullets and
// Table. Bullets use real Word numbering (word/numbering.xml), so Word shows
// native lists. Text is plain strings. Page size is A4 with 2.5 cm margins.
package docx

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Defaults used when a Style field is left at its zero value.
const (
	DefaultFont         = "Calibri"
	DefaultBodySize     = 22 // half-points, so 11 pt
	DefaultHeadingColor = "000000"
)

// Heading levels the writer supports.
const (
	minHeadingLevel = 1
	maxHeadingLevel = 3
)

// Block is one element of the document body. The block types of this package
// are Heading, Paragraph, Bullets and Table, as values (not pointers).
type Block interface {
	validate() error
}

// Heading is a section title. Level is 1 to 3.
type Heading struct {
	Level int
	Text  string
}

// Run is a piece of paragraph text with its own emphasis.
type Run struct {
	Text   string
	Bold   bool
	Italic bool
}

// Paragraph is a body paragraph. Set either Text (plain, no emphasis) or
// Runs (text with bold and italic pieces), not both. A paragraph with
// neither is an empty line.
type Paragraph struct {
	Text string
	Runs []Run
}

// Bullets is a bulleted list. It needs at least one item and no item may be
// empty.
type Bullets struct {
	Items []string
}

// Table is a table with a header row and body rows. Every row has as many
// cells as Header. Widths are column widths in twips (1/20 of a point). When
// Widths is set it has one entry per column and no entry is negative. A zero
// width gives that column an automatic share. An empty paragraph follows each
// table, because Word needs a paragraph after a table at the end of the body.
type Table struct {
	Header []string
	Rows   [][]string
	Widths []int
}

// Style is the house look. Colours are six digit hex RGB values such as
// "1F2937", with no "#". Zero values mean the defaults:
//
//   - Font: DefaultFont ("Calibri")
//   - BodySize: DefaultBodySize (22 half-points, 11 pt)
//   - HeadingColor: DefaultHeadingColor ("000000", black)
//   - TableHeaderFill: no fill
type Style struct {
	Font            string
	BodySize        int
	HeadingColor    string
	TableHeaderFill string
}

// Document is a whole Word file.
type Document struct {
	// Title and Author are written to docProps/core.xml.
	Title  string
	Author string
	// Application is written to docProps/app.xml as the producing application.
	// Empty means the element is left out. The library has no default name.
	Application string
	Style       Style
	Blocks      []Block
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

func (p Paragraph) validate() error {
	if p.Text != "" && len(p.Runs) > 0 {
		return errors.New("paragraph sets both Text and Runs")
	}
	return nil
}

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
	if t.Widths != nil {
		if len(t.Widths) != len(t.Header) {
			return fmt.Errorf("table has %d widths, header has %d columns", len(t.Widths), len(t.Header))
		}
		for i, w := range t.Widths {
			if w < 0 {
				return fmt.Errorf("table column %d width is negative", i+1)
			}
		}
	}
	return nil
}

func validHex(s string) bool {
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

// resolve fills the defaults and validates the style.
func (s Style) resolve() (Style, error) {
	if s.Font == "" {
		s.Font = DefaultFont
	}
	if s.BodySize < 0 {
		return s, errors.New("body size is negative")
	}
	if s.BodySize == 0 {
		s.BodySize = DefaultBodySize
	}
	if s.HeadingColor == "" {
		s.HeadingColor = DefaultHeadingColor
	}
	if !validHex(s.HeadingColor) {
		return s, fmt.Errorf("heading colour %q must be six hex digits", s.HeadingColor)
	}
	if s.TableHeaderFill != "" && !validHex(s.TableHeaderFill) {
		return s, fmt.Errorf("table header fill %q must be six hex digits", s.TableHeaderFill)
	}
	return s, nil
}

type builder struct {
	doc        Document
	style      Style
	hasBullets bool
}

func build(d Document) (*builder, error) {
	st, err := d.Style.resolve()
	if err != nil {
		return nil, fmt.Errorf("docx: %w", err)
	}
	b := &builder{doc: d, style: st}
	for i, blk := range d.Blocks {
		if blk == nil {
			return nil, fmt.Errorf("docx: block %d is nil", i+1)
		}
		if err := blk.validate(); err != nil {
			return nil, fmt.Errorf("docx: block %d: %w", i+1, err)
		}
		if _, ok := blk.(Bullets); ok {
			b.hasBullets = true
		}
	}
	return b, nil
}

// Write validates d and writes it to w as a .docx package.
func Write(w io.Writer, d Document) error {
	b, err := build(d)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(w)
	for _, p := range b.parts() {
		f, err := zw.Create(p.name)
		if err != nil {
			return fmt.Errorf("docx: create %s: %w", p.name, err)
		}
		if _, err := f.Write(p.data); err != nil {
			return fmt.Errorf("docx: write %s: %w", p.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("docx: close: %w", err)
	}
	return nil
}

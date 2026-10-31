// Package extract turns a document into text that a model can read: the
// cells of a spreadsheet sheet by sheet, the headings, paragraphs and tables
// of a Word document, the slides of a presentation, and the pages of a PDF or
// an EPUB. It also gives the structure it read, for a caller that wants more
// than the text (an index of the file, a preview).
//
// The Office formats are read by the files preview reader, so a file reads
// the same in the preview pane and in the chat. PDF and EPUB go through the
// text extractor of the chat harness.
package extract

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mudler/nib/extraction"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/studio/files/preview"
)

var (
	// ErrUnsupported is a file type that has no text extractor.
	ErrUnsupported = errors.New("extract: this type of file cannot be read as text")
	// ErrNoText is a PDF with no text layer: a scan or pictures of pages.
	ErrNoText = errors.New("extract: the document has no text layer")
	// ErrInvalid is a file that is not what its name says.
	ErrInvalid = errors.New("extract: the file could not be read")
)

// Limits bound what Extract reads.
type Limits struct {
	// Rows and Cols are the cells of each sheet that are kept.
	Rows, Cols int
	// Text is the most characters of a document or presentation.
	Text int
	// MaxBytes is the largest input.
	MaxBytes int
}

// DefaultLimits are the limits of Extract.
var DefaultLimits = Limits{Rows: 2000, Cols: 50, Text: 400_000, MaxBytes: 50 << 20}

// Kinds of Structure.
const (
	KindTable    = "table"    // a spreadsheet: Preview.Sheets
	KindDocument = "document" // a Word document: Preview.Blocks
	KindSlides   = "slides"   // a presentation: Preview.Slides
	KindPages    = "pages"    // a PDF or an EPUB: Pages
)

// Structure is what Extract read, besides the text.
type Structure struct {
	Kind string
	// Preview holds the sheets, blocks or slides of an Office file.
	Preview schema.ChatsFilePreview
	// Pages is the number of pages of a PDF or an EPUB with text.
	Pages int
	// Truncated says that the text leaves part of the file out (rows,
	// columns or characters past Limits). The text ends with a note that says
	// what was left out.
	Truncated bool
}

var kinds = map[string]string{
	".xlsx": KindTable, ".xlsm": KindTable,
	".docx": KindDocument,
	".pptx": KindSlides,
	".pdf":  KindPages, ".epub": KindPages,
}

// Supported reports whether Extract reads files of this name.
func Supported(name string) bool { return kinds[strings.ToLower(path.Ext(name))] != "" }

// extractTimeout bounds the extraction of a PDF or an EPUB.
const extractTimeout = 60 * time.Second

// Extract returns the text of a document and the structure it read, with
// DefaultLimits. The name gives the type. A spreadsheet, document or
// presentation with no text gives an empty string and no error; a PDF with no
// text layer gives ErrNoText.
func Extract(ctx context.Context, name string, data []byte) (string, Structure, error) {
	return ExtractWith(ctx, name, data, DefaultLimits)
}

// ExtractWith is Extract with the given limits.
func ExtractWith(ctx context.Context, name string, data []byte, l Limits) (string, Structure, error) {
	kind := kinds[strings.ToLower(path.Ext(name))]
	if kind == "" {
		return "", Structure{}, ErrUnsupported
	}
	if l.MaxBytes > 0 && len(data) > l.MaxBytes {
		return "", Structure{}, fmt.Errorf("extract: %s is %d bytes, more than %d", path.Base(name), len(data), l.MaxBytes)
	}
	if kind == KindPages {
		return pages(ctx, name, data)
	}
	pl := preview.Limits{Rows: l.Rows, Cols: l.Cols, PartBytes: 64 << 20, Text: l.Text}
	p, err := preview.BuildWith(ctx, name, bytes.NewReader(data), int64(len(data)), pl)
	switch {
	case errors.Is(err, preview.ErrInvalid):
		return "", Structure{}, ErrInvalid
	case err != nil:
		return "", Structure{}, err
	}
	st := Structure{Kind: kind, Preview: p, Truncated: p.Truncated}
	var text string
	switch kind {
	case KindTable:
		text, st.Truncated = sheetsText(p.Sheets, l.Cols)
	case KindDocument:
		text = blocksText(p.Blocks)
	default:
		text = slidesText(p.Slides)
	}
	if p.Truncated {
		text += fmt.Sprintf("\n[The text stops here: the file has more than %d characters.]", l.Text)
	}
	return text, st, nil
}

// sheetsText writes each sheet as a heading and its rows as tab-separated
// lines. A sheet that the limits cut says how much it left out.
func sheetsText(sheets []schema.ChatsFilePreviewSheet, maxCols int) (string, bool) {
	var b strings.Builder
	cut := false
	for i, s := range sheets {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "## Sheet %q (%d rows, %d columns)\n", s.Name, s.TotalRows, s.TotalCols)
		for _, row := range s.Rows {
			b.WriteString(tsvLine(row))
			b.WriteByte('\n')
		}
		shownCols := s.TotalCols
		if maxCols > 0 {
			shownCols = min(shownCols, maxCols)
		}
		if len(s.Rows) < s.TotalRows || shownCols < s.TotalCols {
			cut = true
			fmt.Fprintf(&b, "[Sheet %q is cut: %d of %d rows and %d of %d columns shown.]\n", s.Name, len(s.Rows), s.TotalRows, shownCols, s.TotalCols)
		}
	}
	return strings.TrimRight(b.String(), "\n"), cut
}

// tsvLine joins cells with tabs; a tab or a line break inside a cell becomes
// a space, so one line is one row.
func tsvLine(cells []string) string {
	clean := strings.NewReplacer("\t", " ", "\r\n", " ", "\n", " ", "\r", " ")
	out := make([]string, len(cells))
	for i, c := range cells {
		out[i] = clean.Replace(c)
	}
	return strings.TrimRight(strings.Join(out, "\t"), "\t")
}

// blocksText writes a document as Markdown-like text: # headings, - list
// items indented by level, paragraphs and tab-separated table rows.
func blocksText(blocks []schema.ChatsFilePreviewBlock) string {
	var parts []string
	for _, bl := range blocks {
		switch bl.Type {
		case "heading":
			parts = append(parts, strings.Repeat("#", min(max(bl.Level, 1), 6))+" "+bl.Text)
		case "item":
			parts = append(parts, strings.Repeat("  ", bl.Level)+"- "+bl.Text)
		case "table":
			lines := make([]string, len(bl.Rows))
			for i, r := range bl.Rows {
				lines[i] = tsvLine(r)
			}
			parts = append(parts, "[Table]\n"+strings.Join(lines, "\n"))
		default:
			parts = append(parts, bl.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// slidesText writes each slide as a heading with its title, its text indented
// by level, and its speaker notes.
func slidesText(slides []schema.ChatsFilePreviewSlide) string {
	var parts []string
	for _, s := range slides {
		var b strings.Builder
		b.WriteString("## Slide " + strconv.Itoa(s.N))
		if s.Title != "" {
			b.WriteString(": " + s.Title)
		}
		for _, l := range s.Lines {
			b.WriteString("\n" + strings.Repeat("  ", l.Level) + "- " + l.Text)
		}
		if s.Notes != "" {
			b.WriteString("\nNotes: " + s.Notes)
		}
		parts = append(parts, b.String())
	}
	return strings.Join(parts, "\n\n")
}

// pages reads a PDF or an EPUB with the harness's extractor, which reads a
// file, not a stream.
func pages(ctx context.Context, name string, data []byte) (string, Structure, error) {
	dir, err := os.MkdirTemp("", "studio-extract-")
	if err != nil {
		return "", Structure{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	file := filepath.Join(dir, "document"+strings.ToLower(path.Ext(name)))
	if err := os.WriteFile(file, data, 0o600); err != nil {
		return "", Structure{}, err
	}
	type result struct {
		text  string
		pages int
		err   error
	}
	done := make(chan result, 1)
	go func() {
		doc, err := extraction.Open(file)
		if err != nil {
			done <- result{err: err}
			return
		}
		defer func() { _ = doc.Close() }()
		var b strings.Builder
		n := 0
		for i := range doc.NumPage() {
			t, err := doc.Text(i)
			if err != nil {
				continue
			}
			if strings.TrimSpace(t) != "" {
				n++
			}
			b.WriteString(t)
		}
		done <- result{text: strings.ToValidUTF8(b.String(), " "), pages: n}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			return "", Structure{}, fmt.Errorf("%w: %v", ErrInvalid, r.err)
		}
		text := strings.TrimSpace(r.text)
		if text == "" && strings.EqualFold(path.Ext(name), ".pdf") {
			return "", Structure{Kind: KindPages}, ErrNoText
		}
		return text, Structure{Kind: KindPages, Pages: r.pages}, nil
	case <-ctx.Done():
		return "", Structure{}, ctx.Err()
	case <-time.After(extractTimeout):
		return "", Structure{}, fmt.Errorf("extract: reading %s took too long", path.Base(name))
	}
}

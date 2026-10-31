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
	"unicode/utf8"

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
	// Pages is the most pages of a PDF or an EPUB that are read.
	Pages int
}

// DefaultLimits are the limits of Extract.
var DefaultLimits = Limits{Rows: 2000, Cols: 50, Text: 400_000, MaxBytes: 50 << 20, Pages: 200}

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
	// columns, pages or characters past Limits). The text ends with a note
	// that says what was left out.
	Truncated bool
	// Sections says where each page, sheet or slide starts in the text, in
	// order, so a line of the text can be told apart by its page or sheet.
	// A Word document has none.
	Sections []Section
}

// Kinds of Section.
const (
	SectionPage    = "page"
	SectionChapter = "chapter"
	SectionSheet   = "sheet"
	SectionSlide   = "slide"
)

// Section is a page, a chapter, a sheet or a slide of the text.
type Section struct {
	Kind string `json:"kind"`
	// Name is the sheet name, or the number of a page, chapter or slide.
	Name string `json:"name"`
	// Line is the 1-based line of the text where the section starts.
	Line int `json:"line"`
}

// SectionAt returns the section that holds a 1-based line of the text, and
// false before the first section.
func SectionAt(sections []Section, line int) (Section, bool) {
	var at Section
	found := false
	for _, s := range sections {
		if s.Line > line {
			break
		}
		at, found = s, true
	}
	return at, found
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
		return pages(ctx, name, data, l)
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
		st.Sections = headingSections(text, "## Sheet ", SectionSheet, sheetNames(p.Sheets))
	case KindDocument:
		text = blocksText(p.Blocks)
	default:
		text = slidesText(p.Slides)
		st.Sections = headingSections(text, "## Slide ", SectionSlide, slideNumbers(p.Slides))
	}
	if p.Truncated {
		text += fmt.Sprintf("\n[The text stops here: the file has more than %d characters.]", l.Text)
	}
	return text, st, nil
}

// headingSections finds the lines of text that start with prefix, the
// headings of the sheets or slides, and names them in order.
func headingSections(text, prefix, kind string, names []string) []Section {
	var out []Section
	for i, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, prefix) && len(out) < len(names) {
			out = append(out, Section{Kind: kind, Name: names[len(out)], Line: i + 1})
		}
	}
	return out
}

func sheetNames(sheets []schema.ChatsFilePreviewSheet) []string {
	out := make([]string, len(sheets))
	for i, s := range sheets {
		out[i] = s.Name
	}
	return out
}

func slideNumbers(slides []schema.ChatsFilePreviewSlide) []string {
	out := make([]string, len(slides))
	for i, s := range slides {
		out[i] = strconv.Itoa(s.N)
	}
	return out
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
// file, not a stream. Each page with text starts with a "## Page N" line (an
// EPUB: "## Chapter N"), and the reading stops at l.Pages pages or l.Text
// characters.
func pages(ctx context.Context, name string, data []byte, l Limits) (string, Structure, error) {
	dir, err := os.MkdirTemp("", "studio-extract-")
	if err != nil {
		return "", Structure{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	ext := strings.ToLower(path.Ext(name))
	file := filepath.Join(dir, "document"+ext)
	if err := os.WriteFile(file, data, 0o600); err != nil {
		return "", Structure{}, err
	}
	kind, label := SectionPage, "Page"
	if ext == ".epub" {
		kind, label = SectionChapter, "Chapter"
	}
	type result struct {
		text     string
		sections []Section
		pages    int
		cut      string
		err      error
	}
	done := make(chan result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- result{err: fmt.Errorf("the reader failed: %v", r)}
			}
		}()
		doc, err := extraction.Open(file)
		if err != nil {
			done <- result{err: err}
			return
		}
		defer func() { _ = doc.Close() }()
		var r result
		var b strings.Builder
		lines, chars := 0, 0
		total := doc.NumPage()
		for i := range total {
			if ctx.Err() != nil {
				break
			}
			if l.Pages > 0 && i >= l.Pages {
				r.cut = fmt.Sprintf("[The text stops here: only the first %d of %d pages were read.]", l.Pages, total)
				break
			}
			t, err := doc.Text(i)
			if err != nil {
				continue
			}
			t = strings.TrimSpace(strings.ToValidUTF8(t, " "))
			if t == "" {
				continue
			}
			if l.Text > 0 && chars+len(t) > l.Text {
				t = cutRunes(t, max(l.Text-chars, 0))
				r.cut = fmt.Sprintf("[The text stops here, on page %d: the document has more than %d characters.]", i+1, l.Text)
			}
			if b.Len() > 0 {
				b.WriteString("\n\n")
				lines++
			}
			r.sections = append(r.sections, Section{Kind: kind, Name: strconv.Itoa(i + 1), Line: lines + 1})
			heading := fmt.Sprintf("## %s %d\n", label, i+1)
			b.WriteString(heading)
			b.WriteString(t)
			lines += 1 + strings.Count(t, "\n") + 1
			chars += len(t)
			r.pages++
			if r.cut != "" {
				break
			}
		}
		r.text = b.String()
		done <- r
	}()
	select {
	case r := <-done:
		if r.err != nil {
			return "", Structure{}, fmt.Errorf("%w: %v", ErrInvalid, r.err)
		}
		if r.text == "" && ext == ".pdf" {
			return "", Structure{Kind: KindPages}, ErrNoText
		}
		st := Structure{Kind: KindPages, Pages: r.pages, Sections: r.sections, Truncated: r.cut != ""}
		text := r.text
		if r.cut != "" {
			text += "\n" + r.cut
		}
		return text, st, nil
	case <-ctx.Done():
		return "", Structure{}, ctx.Err()
	case <-time.After(extractTimeout):
		return "", Structure{}, fmt.Errorf("extract: reading %s took too long", path.Base(name))
	}
}

// cutRunes keeps at most n bytes of s without cutting a character.
func cutRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

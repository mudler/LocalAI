// Package preview reads the content of files that a browser cannot show by
// itself: spreadsheets (.xlsx, .xlsm), delimited text (.csv, .tsv), Word
// documents (.docx) and PowerPoint presentations (.pptx). It keeps the values
// and the text, not the formatting, so the chat's preview pane can show what
// a file holds without the application that made it.
//
// It uses only the standard library: an Office file is a zip of XML parts, and
// a preview needs only a few elements of a few parts.
package preview

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/mudler/LocalAI/core/schema"
)

var (
	// ErrUnsupported is a type that has no server preview.
	ErrUnsupported = errors.New("preview: this type of file has no preview")
	// ErrInvalid is a file that is not what its name says (a broken zip, a
	// package without its main part).
	ErrInvalid = errors.New("preview: the file could not be read")
	// ErrTooLarge is a part of the file that unpacks past Limits.PartBytes.
	ErrTooLarge = errors.New("preview: the file is too large to preview")
)

// Limits bound what a preview reads and keeps.
type Limits struct {
	// Rows and Cols are the cells of a sheet that are kept.
	Rows, Cols int
	// PartBytes is the most a part of a zip may unpack to. A zip bomb stops
	// here and not when the memory runs out.
	PartBytes int64
	// Text is the most characters of a document or presentation that are kept.
	Text int
}

// DefaultLimits are the limits of Build.
var DefaultLimits = Limits{Rows: 200, Cols: 30, PartBytes: 64 << 20, Text: 200_000}

const (
	kindTable    = "table"
	kindDocument = "document"
	kindSlides   = "slides"
)

func kindOf(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".xlsx", ".xlsm":
		return "xlsx"
	case ".csv":
		return "csv"
	case ".tsv":
		return "tsv"
	case ".docx":
		return "docx"
	case ".pptx":
		return "pptx"
	}
	return ""
}

// Supported says whether a file of this name has a server preview.
func Supported(name string) bool { return kindOf(name) != "" }

// Build reads a preview of the file with DefaultLimits. name gives the type.
func Build(ctx context.Context, name string, r io.ReaderAt, size int64) (schema.ChatsFilePreview, error) {
	return BuildWith(ctx, name, r, size, DefaultLimits)
}

// BuildWith reads a preview of the file with the given limits. It checks ctx
// while it reads, so a deadline bounds the work on a large file.
func BuildWith(ctx context.Context, name string, r io.ReaderAt, size int64, l Limits) (schema.ChatsFilePreview, error) {
	if err := ctx.Err(); err != nil {
		return schema.ChatsFilePreview{}, err
	}
	k := kindOf(name)
	switch k {
	case "csv", "tsv":
		sep := ','
		if k == "tsv" {
			sep = '\t'
		}
		s, err := readDelimited(ctx, io.NewSectionReader(r, 0, size), path.Base(name), sep, l)
		if err != nil {
			return schema.ChatsFilePreview{}, err
		}
		return schema.ChatsFilePreview{Kind: kindTable, Sheets: []schema.ChatsFilePreviewSheet{s}}, nil
	case "xlsx", "docx", "pptx":
		zr, err := zip.NewReader(r, size)
		if err != nil {
			return schema.ChatsFilePreview{}, ErrInvalid
		}
		p := &pkg{ctx: ctx, zr: zr, limits: l, parts: map[string]*zip.File{}}
		for _, f := range zr.File {
			p.parts[strings.TrimPrefix(f.Name, "/")] = f
		}
		switch k {
		case "xlsx":
			return p.workbook()
		case "docx":
			return p.document()
		default:
			return p.presentation()
		}
	}
	return schema.ChatsFilePreview{}, ErrUnsupported
}

// pkg is an open Office package.
type pkg struct {
	ctx    context.Context
	zr     *zip.Reader
	limits Limits
	parts  map[string]*zip.File
}

func (p *pkg) has(name string) bool { return p.parts[name] != nil }

// open opens a part for reading, limited to PartBytes. A part that is longer
// fails the read with ErrTooLarge; ctx is checked as the part is read.
func (p *pkg) open(name string) (io.ReadCloser, error) {
	f := p.parts[name]
	if f == nil {
		return nil, ErrInvalid
	}
	rc, err := f.Open()
	if err != nil {
		return nil, ErrInvalid
	}
	return &limited{ctx: p.ctx, r: rc, left: p.limits.PartBytes}, nil
}

type limited struct {
	ctx  context.Context
	r    io.ReadCloser
	left int64
}

func (l *limited) Read(b []byte) (int, error) {
	if err := l.ctx.Err(); err != nil {
		return 0, err
	}
	if l.left <= 0 {
		// One byte more says whether the part ends here or is too long.
		var one [1]byte
		if n, _ := l.r.Read(one[:]); n > 0 {
			return 0, ErrTooLarge
		}
		return 0, io.EOF
	}
	if int64(len(b)) > l.left {
		b = b[:l.left]
	}
	n, err := l.r.Read(b)
	l.left -= int64(n)
	return n, err
}

func (l *limited) Close() error { return l.r.Close() }

// decode walks the XML of a part, calling fn on every start element; fn reads
// what it needs from the decoder. A read error of the limits (too large,
// cancelled) is returned as it is; any other XML error is ErrInvalid.
func (p *pkg) decode(name string, fn func(d *xml.Decoder, se xml.StartElement) error) error {
	rc, err := p.open(name)
	if err != nil {
		return err
	}
	defer rc.Close()
	d := xml.NewDecoder(rc)
	d.Strict = false
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return readErr(err)
		}
		if se, ok := tok.(xml.StartElement); ok {
			if err := fn(d, se); err != nil {
				return readErr(err)
			}
		}
	}
}

func readErr(err error) error {
	switch {
	case errors.Is(err, ErrTooLarge), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded), errors.Is(err, ErrInvalid), errors.Is(err, errStop):
		return err
	}
	return fmt.Errorf("%w: %v", ErrInvalid, err)
}

// rels reads a relationships part: Id to the target, resolved against the
// folder of the part the relationships belong to.
func (p *pkg) rels(owner string) (map[string]string, error) {
	dir, file := path.Split(owner)
	name := dir + "_rels/" + file + ".rels"
	out := map[string]string{}
	if !p.has(name) {
		return out, nil
	}
	err := p.decode(name, func(_ *xml.Decoder, se xml.StartElement) error {
		if se.Name.Local != "Relationship" {
			return nil
		}
		id, target := attr(se, "Id"), attr(se, "Target")
		if after, ok := strings.CutPrefix(target, "/"); ok {
			out[id] = after
		} else {
			out[id] = path.Clean(dir + target)
		}
		return nil
	})
	return out, err
}

// attr is the value of an attribute by its local name, whatever its prefix.
func attr(se xml.StartElement, local string) string {
	for _, a := range se.Attr {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

// relID is the r:id attribute: the local name "id" in the relationships
// namespace, so it is not confused with a plain id attribute.
func relID(se xml.StartElement) string {
	for _, a := range se.Attr {
		if a.Name.Local == "id" && strings.Contains(a.Name.Space, "relationships") {
			return a.Value
		}
	}
	return ""
}

// textBudget counts the characters a document keeps.
type textBudget struct {
	left      int
	truncated bool
}

// take says whether text of n characters still fits, and counts it.
func (b *textBudget) take(n int) bool {
	if n > b.left {
		b.truncated = true
		return false
	}
	b.left -= n
	return true
}

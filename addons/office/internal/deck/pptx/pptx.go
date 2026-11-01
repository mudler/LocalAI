// Package pptx writes studio slides as a PowerPoint (.pptx) file.
//
// The output is a plain OOXML package built with archive/zip and text
// templates: one slide master, two layouts (Title and Content, and Blank), one
// theme, and one slide per finished studio slide. A slide with a title uses
// the Title and Content layout: its title is the title placeholder and its
// bullets or subtitle are the body placeholder. A slide without a title uses
// Blank. Every text is a real, editable text box. Charts
// are drawn with native shapes (bars as rectangles, lines as connectors), so
// the receiver can still edit them. Images come from an ImageLoader; an
// image that cannot be loaded leaves a captioned placeholder.
package pptx

import (
	"archive/zip"
	"fmt"
	"io"
	"strings"

	"github.com/mudler/LocalAI/addons/office/internal/deck"
)

// SourceKind is how well a line of the sources slide is supported.
type SourceKind string

const (
	SourceOK       SourceKind = "ok"
	SourceWeak     SourceKind = "weak"
	SourceNoSource SourceKind = "nosrc"
	SourceOwn      SourceKind = "own"
)

// SourceLine is one line of the closing sources slide.
type SourceLine struct {
	Label  string
	Detail string
	Kind   SourceKind
}

// Options controls the export.
type Options struct {
	Title string
	// Application is written to docProps/app.xml as the producing application.
	// Empty means the element is left out. The library has no default name.
	Application  string
	Style        deck.StudioStyle
	Notes        bool
	SourcesSlide bool
	Sources      []SourceLine
}

// ImageLoader returns the bytes and the media type of an image reference.
// Only JPEG and PNG images are embedded.
type ImageLoader func(ref string) ([]byte, string, error)

// Slide size, 16:9, in EMU.
const (
	slideW = 12192000
	slideH = 6858000
)

// firstSlideID is the lowest id PowerPoint accepts in sldIdLst.
const firstSlideID = 256

// part is one file of the package.
type part struct {
	name string
	data []byte
}

// builtSlide is one slide with its relationships and optional notes. titled
// is true when the slide uses the Title and Content layout.
type builtSlide struct {
	xml    string
	rels   []rel
	notes  string
	titled bool
}

// rel is one relationship of a part.
type rel struct {
	id     string
	typ    string
	target string
}

// media is one embedded image.
type media struct {
	name string // e.g. image1.png
	ext  string // png or jpeg
	data []byte
}

// Write writes the finished slides (status ok) as a .pptx package to w.
// Queued, working and failed slides are skipped. load may be nil, in which
// case every image becomes a placeholder.
func Write(w io.Writer, slides []deck.StudioSlide, opts Options, load ImageLoader) error {
	pal := paletteFor(opts.Style)
	var media []media
	var built []builtSlide
	for _, s := range slides {
		if s.Status != deck.SlideOK {
			continue
		}
		b := newSlideBuilder(pal, load, &media)
		b.studioSlide(s)
		bs := b.finish()
		if opts.Notes {
			bs.notes = strings.TrimSpace(s.Notes)
		}
		built = append(built, bs)
	}
	if opts.SourcesSlide {
		b := newSlideBuilder(pal, load, &media)
		b.sourcesSlide(opts.Sources)
		built = append(built, b.finish())
	}

	parts := packageParts(opts, pal, built, media)
	zw := zip.NewWriter(w)
	for _, p := range parts {
		f, err := zw.Create(p.name)
		if err != nil {
			return fmt.Errorf("pptx: create %s: %w", p.name, err)
		}
		if _, err := f.Write(p.data); err != nil {
			return fmt.Errorf("pptx: write %s: %w", p.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("pptx: close: %w", err)
	}
	return nil
}

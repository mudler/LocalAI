// Package pptxedit edits an existing .pptx file: change a title or one
// paragraph, list the shapes of a slide and change the text of any text
// shape by its id, add a slide from a layout of the deck, delete a slide.
//
// It is the editing counterpart of deck/pptx (writes a deck from scratch),
// officeread (reads a deck as text) and officecheck (checks the structure).
// Every result of Bytes passes officecheck.Check.
//
// How XML is edited: each part is parsed once to find the byte range of the
// elements to change, and only those ranges are replaced in the original
// text. Nothing else is re-serialized, so unrelated markup, namespace
// prefixes and white space stay as they were. A part that no edit touched is
// copied from the source zip entry without being decompressed, so it is
// byte for byte the same, and entries keep their original order. New parts
// go at the end.
//
// What it does not do: no images, tables, charts, shape geometry or
// speaker notes. A shape is a p:sp element, found by its name. The notes of
// a deleted slide are deleted with it; the notes of the other slides are
// never touched. docProps/app.xml gets the new slide count; its other
// counters are left as they are.
//
// Standard library only. It never panics on malformed input.
package pptxedit

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/addons/office/internal/ooxml"
)

const (
	maxEntry = 64 << 20  // bytes per part
	maxTotal = 256 << 20 // declared bytes in the whole package
	maxParts = 20000

	nsP = "http://schemas.openxmlformats.org/presentationml/2006/main"
	nsA = "http://schemas.openxmlformats.org/drawingml/2006/main"
	nsR = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"

	relBase        = nsR + "/"
	relOfficeDoc   = relBase + "officeDocument"
	relSlide       = relBase + "slide"
	relSlideLayout = relBase + "slideLayout"
	ctSlide        = "application/vnd.openxmlformats-officedocument.presentationml.slide+xml"
	contentTypes   = "[Content_Types].xml"
)

// SlideInfo describes one slide in presentation order.
type SlideInfo struct {
	Index int      // 1-based position in the slide list
	Title string   // text of the title placeholder, "" when there is none
	Text  []string // one entry per non-empty paragraph, as officeread reads them
}

// Deck is an open presentation. It is not safe for concurrent use.
type Deck struct {
	data    []byte
	files   map[string]*zip.File // entries of the source zip
	order   []string             // names in output order
	mod     map[string][]byte    // new or changed parts
	presDir string
	pres    string // main presentation part name
}

// Open reads a .pptx file. The bytes are kept until the Deck is dropped.
func Open(data []byte) (*Deck, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("pptxedit: not a zip file: %w", err)
	}
	if len(zr.File) > maxParts {
		return nil, errors.New("pptxedit: too many parts")
	}
	d := &Deck{data: data, files: map[string]*zip.File{}, mod: map[string][]byte{}}
	var total uint64
	for _, f := range zr.File {
		name := f.Name
		if strings.HasPrefix(name, "/") || strings.HasPrefix(name, "\\") || ooxml.HasDotDot(name) {
			return nil, fmt.Errorf("pptxedit: unsafe entry name %q", name)
		}
		if _, dup := d.files[name]; dup {
			return nil, fmt.Errorf("pptxedit: duplicate entry %q", name)
		}
		if f.UncompressedSize64 > maxEntry {
			return nil, fmt.Errorf("pptxedit: entry %q is larger than %d bytes", name, maxEntry)
		}
		total += f.UncompressedSize64
		if total > maxTotal {
			return nil, fmt.Errorf("pptxedit: package is larger than %d bytes", maxTotal)
		}
		d.files[name] = f
		d.order = append(d.order, name)
	}
	if !d.has(contentTypes) {
		return nil, errors.New("pptxedit: not a pptx file: no [Content_Types].xml")
	}
	if err := d.findMain(); err != nil {
		return nil, err
	}
	refs, err := d.slideRefs()
	if err != nil {
		return nil, err
	}
	if len(refs) == 0 {
		return nil, errors.New("pptxedit: the deck has no slides")
	}
	return d, nil
}

func (d *Deck) has(name string) bool {
	if _, ok := d.mod[name]; ok {
		return true
	}
	_, ok := d.files[name]
	if !ok {
		return false
	}
	return slices.Contains(d.order, name)
}

// get returns the current content of a part.
func (d *Deck) get(name string) ([]byte, error) {
	if b, ok := d.mod[name]; ok {
		return b, nil
	}
	f, ok := d.files[name]
	if !ok || !d.has(name) {
		return nil, fmt.Errorf("pptxedit: missing part %q", name)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("pptxedit: cannot open %q: %w", name, err)
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(io.LimitReader(rc, maxEntry+1))
	if err != nil {
		return nil, fmt.Errorf("pptxedit: cannot read %q: %w", name, err)
	}
	if len(b) > maxEntry {
		return nil, fmt.Errorf("pptxedit: entry %q is larger than %d bytes", name, maxEntry)
	}
	return b, nil
}

func (d *Deck) set(name string, b []byte) {
	if !d.has(name) {
		d.order = append(d.order, name)
	}
	d.mod[name] = b
}

func (d *Deck) remove(name string) {
	delete(d.mod, name)
	for i, n := range d.order {
		if n == name {
			d.order = append(d.order[:i:i], d.order[i+1:]...)
			return
		}
	}
}

// tx runs fn on a copy and keeps the copy only when fn succeeds, so a failed
// edit leaves the deck as it was.
func (d *Deck) tx(fn func(*Deck) error) error {
	c := *d
	c.order = append([]string(nil), d.order...)
	c.mod = make(map[string][]byte, len(d.mod))
	maps.Copy(c.mod, d.mod)
	if err := fn(&c); err != nil {
		return err
	}
	*d = c
	return nil
}

// Bytes returns the edited deck. Parts no edit touched are copied as they
// are, in their original order.
func (d *Deck) Bytes() ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range d.order {
		if b, ok := d.mod[name]; ok {
			h := &zip.FileHeader{Name: name, Method: zip.Deflate}
			if f, ok := d.files[name]; ok {
				h.Modified = f.Modified
			}
			w, err := zw.CreateHeader(h)
			if err != nil {
				return nil, fmt.Errorf("pptxedit: %w", err)
			}
			if _, err := w.Write(b); err != nil {
				return nil, fmt.Errorf("pptxedit: %w", err)
			}
			continue
		}
		if err := zw.Copy(d.files[name]); err != nil {
			return nil, fmt.Errorf("pptxedit: copy %q: %w", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("pptxedit: %w", err)
	}
	return buf.Bytes(), nil
}

// ---- relationships ----

type relationship struct {
	id, typ, target string
	external        bool
	n               *node
}

func relsName(part string) string {
	return path.Join(path.Dir(part), "_rels", path.Base(part)+".rels")
}

// resolve turns a relationship target into a part name.
func resolve(base, target string) (string, bool) {
	if i := strings.IndexAny(target, "#?"); i >= 0 {
		target = target[:i]
	}
	if strings.HasPrefix(target, "/") {
		target = target[1:]
	} else {
		target = path.Join(base, target)
	}
	target = path.Clean(target)
	if target == "." || target == ".." || strings.HasPrefix(target, "../") {
		return "", false
	}
	return target, true
}

func parseRels(raw []byte) ([]relationship, *node, error) {
	root, err := parseTree(raw)
	if err != nil {
		return nil, nil, err
	}
	var out []relationship
	for _, k := range root.kids {
		if k.name.Local != "Relationship" {
			continue
		}
		var r relationship
		r.id, _ = k.attrLocal("Id")
		r.typ, _ = k.attrLocal("Type")
		r.target, _ = k.attrLocal("Target")
		mode, _ := k.attrLocal("TargetMode")
		r.external = strings.EqualFold(mode, "External")
		r.n = k
		out = append(out, r)
	}
	return out, root, nil
}

func (d *Deck) relsOf(part string) ([]relationship, []byte, *node, error) {
	name := relsName(part)
	if !d.has(name) {
		return nil, nil, nil, nil
	}
	raw, err := d.get(name)
	if err != nil {
		return nil, nil, nil, err
	}
	rels, root, err := parseRels(raw)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("pptxedit: %s: %w", name, err)
	}
	return rels, raw, root, nil
}

func (d *Deck) findMain() error {
	raw, err := d.get("_rels/.rels")
	if err != nil {
		return errors.New("pptxedit: not a pptx file: no _rels/.rels")
	}
	rels, _, err := parseRels(raw)
	if err != nil {
		return fmt.Errorf("pptxedit: _rels/.rels: %w", err)
	}
	for _, r := range rels {
		if r.typ != relOfficeDoc || r.external {
			continue
		}
		name, ok := resolve("", r.target)
		if !ok || !d.has(name) {
			return errors.New("pptxedit: the main document part is missing")
		}
		pr, err := d.get(name)
		if err != nil {
			return err
		}
		root, err := parseTree(pr)
		if err != nil {
			return fmt.Errorf("pptxedit: %s: %w", name, err)
		}
		if !root.is(nsP, "presentation") {
			return errors.New("pptxedit: not a pptx file: the main part is not a presentation")
		}
		d.pres = name
		d.presDir = path.Dir(name)
		return nil
	}
	return errors.New("pptxedit: not a pptx file: no presentation part")
}

// slideRef is one entry of the slide list.
type slideRef struct {
	rid  string
	id   string
	part string
	n    *node // the p:sldId element
}

// presentation parses the presentation part.
func (d *Deck) presentation() ([]byte, *node, error) {
	raw, err := d.get(d.pres)
	if err != nil {
		return nil, nil, err
	}
	root, err := parseTree(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("pptxedit: %s: %w", d.pres, err)
	}
	return raw, root, nil
}

func (d *Deck) slideRefs() ([]slideRef, error) {
	_, root, err := d.presentation()
	if err != nil {
		return nil, err
	}
	rels, _, _, err := d.relsOf(d.pres)
	if err != nil {
		return nil, err
	}
	byID := map[string]relationship{}
	for _, r := range rels {
		byID[r.id] = r
	}
	var out []slideRef
	for _, k := range root.child(nsP, "sldIdLst").children(nsP, "sldId") {
		rid := k.relAttr("id")
		r, ok := byID[rid]
		if !ok || r.external {
			return nil, fmt.Errorf("pptxedit: slide %d has no relationship %q", len(out)+1, rid)
		}
		part, ok := resolve(d.presDir, r.target)
		if !ok || !d.has(part) {
			return nil, fmt.Errorf("pptxedit: slide %d points at a missing part", len(out)+1)
		}
		id, _ := k.attrLocal("id")
		out = append(out, slideRef{rid: rid, id: id, part: part, n: k})
	}
	return out, nil
}

// slide returns the part of slide number n (1-based).
func (d *Deck) slide(n int) (slideRef, error) {
	refs, err := d.slideRefs()
	if err != nil {
		return slideRef{}, err
	}
	if n < 1 || n > len(refs) {
		return slideRef{}, fmt.Errorf("pptxedit: slide %d out of range (the deck has %d)", n, len(refs))
	}
	return refs[n-1], nil
}

var slidesCountRe = regexp.MustCompile(`<Slides>\d+</Slides>`)

// setSlideCount updates the slide counter of docProps/app.xml, if present.
func (d *Deck) setSlideCount(n int) {
	const name = "docProps/app.xml"
	if !d.has(name) {
		return
	}
	raw, err := d.get(name)
	if err != nil || !slidesCountRe.Match(raw) {
		return
	}
	d.set(name, slidesCountRe.ReplaceAll(raw, []byte(fmt.Sprintf("<Slides>%d</Slides>", n))))
}

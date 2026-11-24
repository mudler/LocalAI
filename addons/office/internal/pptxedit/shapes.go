package pptxedit

import (
	"fmt"
	"strconv"
)

// ShapeInfo describes one shape of a slide.
type ShapeInfo struct {
	ID   int    // the p:cNvPr id, unique within the slide
	Name string // the p:cNvPr name
	// Kind is title (title or centred title placeholder), body (body,
	// subtitle or object placeholder), text (a shape with a text body that
	// is not a placeholder), picture, or other.
	Kind       string
	X, Y, W, H int64    // position and size in EMU; zero when the shape has none
	Text       []string // one entry per a:p, empty paragraphs included, so the index is the paragraph number of SetShapeText
}

// shapeElements are the elements that hold a slide shape.
var shapeElements = map[string]bool{"sp": true, "pic": true, "cxnSp": true, "graphicFrame": true}

// slideShapes returns every shape element of a slide in document order.
func slideShapes(root *node) []*node {
	var out []*node
	root.walk(func(n *node) {
		if n.name.Space == nsP && shapeElements[n.name.Local] {
			out = append(out, n)
		}
	})
	return out
}

// cNvID returns the p:cNvPr id of a shape, 0 when it has none.
func cNvID(sp *node) int {
	if len(sp.kids) == 0 {
		return 0
	}
	v, _ := sp.kids[0].child(nsP, "cNvPr").attrLocal("id")
	id, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return id
}

func shapeKind(sp *node) string {
	switch sp.name.Local {
	case "pic":
		return "picture"
	case "sp":
	default:
		return "other"
	}
	if typ, _, ok := placeholder(sp); ok {
		switch typ {
		case "title", "ctrTitle":
			return "title"
		case "", "body", "obj", "subTitle":
			return "body"
		default:
			return "other"
		}
	}
	if sp.child(nsP, "txBody") != nil {
		return "text"
	}
	return "other"
}

// shapeGeometry reads the offset and extent of a shape. A graphic frame keeps
// its transform in p:xfrm, every other shape in p:spPr/a:xfrm.
func shapeGeometry(sp *node) (x, y, w, h int64) {
	xf := sp.child(nsP, "spPr").child(nsA, "xfrm")
	if xf == nil {
		xf = sp.child(nsP, "xfrm")
	}
	num := func(n *node, attr string) int64 {
		v, _ := n.attrLocal(attr)
		i, _ := strconv.ParseInt(v, 10, 64)
		return i
	}
	off, ext := xf.child(nsA, "off"), xf.child(nsA, "ext")
	return num(off, "x"), num(off, "y"), num(ext, "cx"), num(ext, "cy")
}

// Shapes lists the shapes of slide number slide (1-based) in document order.
// It returns an error when the slide does not exist or cannot be parsed.
func (d *Deck) Shapes(slide int) ([]ShapeInfo, error) {
	ref, err := d.slide(slide)
	if err != nil {
		return nil, err
	}
	raw, err := d.get(ref.part)
	if err != nil {
		return nil, err
	}
	root, err := parseTree(raw)
	if err != nil {
		return nil, fmt.Errorf("pptxedit: %s: %w", ref.part, err)
	}
	var out []ShapeInfo
	for _, sp := range slideShapes(root) {
		info := ShapeInfo{ID: cNvID(sp), Kind: shapeKind(sp)}
		info.Name, _ = sp.kids[0].child(nsP, "cNvPr").attrLocal("name")
		info.X, info.Y, info.W, info.H = shapeGeometry(sp)
		for _, p := range paragraphs(sp) {
			info.Text = append(info.Text, paraText(p))
		}
		out = append(out, info)
	}
	return out, nil
}

// SetShapeText replaces the text of one paragraph of the shape with this id
// on a slide. It works on any shape with a text body, placeholder or not. The
// paragraph number counts every a:p of the shape, from 0, as in SetParagraph,
// and the properties of the paragraph and of its first run are kept. A newline
// in text becomes a line break. An unknown id, a shape without a text body and
// a paragraph out of range are errors.
func (d *Deck) SetShapeText(slide int, shapeID int, paragraph int, text string) error {
	return d.tx(func(d *Deck) error {
		ref, err := d.slide(slide)
		if err != nil {
			return err
		}
		raw, err := d.get(ref.part)
		if err != nil {
			return err
		}
		root, err := parseTree(raw)
		if err != nil {
			return fmt.Errorf("pptxedit: %s: %w", ref.part, err)
		}
		var sp *node
		for _, c := range slideShapes(root) {
			if id := cNvID(c); id != 0 && id == shapeID {
				sp = c
				break
			}
		}
		if sp == nil {
			return fmt.Errorf("pptxedit: slide %d has no shape with id %d", slide, shapeID)
		}
		if sp.child(nsP, "txBody") == nil {
			return fmt.Errorf("pptxedit: shape %d on slide %d has no text body", shapeID, slide)
		}
		ps := paragraphs(sp)
		if paragraph < 0 || paragraph >= len(ps) {
			return fmt.Errorf("pptxedit: shape %d has %d paragraphs, no paragraph %d", shapeID, len(ps), paragraph)
		}
		p := ps[paragraph]
		out, err := splice(raw, []edit{{p.start, p.end, rewriteParagraph(raw, p, text)}})
		if err != nil {
			return fmt.Errorf("pptxedit: %w", err)
		}
		d.set(ref.part, out)
		return nil
	})
}

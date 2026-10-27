package pptxedit

import (
	"fmt"
	"strings"
)

// Slides lists the slides in presentation order. A slide that cannot be
// parsed is listed with no title and no text.
func (d *Deck) Slides() []SlideInfo {
	refs, err := d.slideRefs()
	if err != nil {
		return nil
	}
	out := make([]SlideInfo, len(refs))
	for i, r := range refs {
		out[i].Index = i + 1
		raw, err := d.get(r.part)
		if err != nil {
			continue
		}
		root, err := parseTree(raw)
		if err != nil {
			continue
		}
		if sp := titleShape(root); sp != nil {
			var parts []string
			for _, p := range paragraphs(sp) {
				parts = append(parts, paraText(p))
			}
			out[i].Title = strings.Join(parts, "\n")
		}
		out[i].Text = slideText(root)
	}
	return out
}

// shapes returns every p:sp element of a slide in document order.
func shapes(root *node) []*node {
	var out []*node
	root.walk(func(n *node) {
		if n.is(nsP, "sp") {
			out = append(out, n)
		}
	})
	return out
}

func shapeName(sp *node) string {
	v, _ := sp.child(nsP, "nvSpPr").child(nsP, "cNvPr").attrLocal("name")
	return v
}

func placeholder(sp *node) (typ, idx string, ok bool) {
	ph := sp.child(nsP, "nvSpPr").child(nsP, "nvPr").child(nsP, "ph")
	if ph == nil {
		return "", "", false
	}
	typ, _ = ph.attrLocal("type")
	idx, _ = ph.attrLocal("idx")
	return typ, idx, true
}

func titleShape(root *node) *node {
	for _, sp := range shapes(root) {
		if t, _, ok := placeholder(sp); ok && (t == "title" || t == "ctrTitle") {
			return sp
		}
	}
	return nil
}

func paragraphs(sp *node) []*node {
	return sp.child(nsP, "txBody").children(nsA, "p")
}

// slideText lists the non-empty paragraphs the way officeread does: text in
// slide number, date, footer, header and slide image placeholders is left out.
func slideText(root *node) []string {
	var out []string
	root.walk(func(n *node) {
		if !n.is(nsA, "p") {
			return
		}
		for a := n.parent; a != nil; a = a.parent {
			if a.is(nsP, "sp") {
				switch t, _, _ := placeholder(a); t {
				case "sldNum", "dt", "ftr", "hdr", "sldImg":
					return
				}
				break
			}
		}
		if s := paraText(n); s != "" {
			out = append(out, s)
		}
	})
	return out
}

// textRuns renders the runs of a paragraph: one run per line, an a:br between
// lines. rPr is the verbatim run properties to reuse, or "".
func textRuns(pfx, text, rPr string) string {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	var b strings.Builder
	for i, line := range strings.Split(text, "\n") {
		if i > 0 {
			b.WriteString("<" + qual(pfx, "br") + "/>")
		}
		if line == "" {
			continue
		}
		b.WriteString("<" + qual(pfx, "r") + ">" + rPr + "<" + qual(pfx, "t") + ">" + esc(line) + "</" + qual(pfx, "t") + "></" + qual(pfx, "r") + ">")
	}
	return b.String()
}

// rewriteParagraph returns the replacement for paragraph p: the same open
// tag, paragraph properties and end properties, and one run (more with line
// breaks) that carries the run properties of the first run of p.
func rewriteParagraph(raw []byte, p *node, text string) string {
	open := string(raw[p.start:p.openEnd])
	if p.self {
		open = strings.TrimSuffix(open, "/>") + ">"
	}
	pfx := prefixOf(raw, p)
	var pPr, rPr, endPr string
	seenRun := false
	for _, k := range p.kids {
		switch {
		case k.is(nsA, "pPr") && pPr == "":
			pPr = string(raw[k.start:k.end])
		case k.is(nsA, "r") && !seenRun:
			seenRun = true
			if rp := k.child(nsA, "rPr"); rp != nil {
				rPr = string(raw[rp.start:rp.end])
			}
		case k.is(nsA, "endParaRPr") && endPr == "":
			endPr = string(raw[k.start:k.end])
		}
	}
	return open + pPr + textRuns(pfx, text, rPr) + endPr + "</" + tagName(raw, p) + ">"
}

// newParagraph builds a paragraph from scratch with the a: prefix. With decl
// the element declares the prefix itself, for use inside a part that may not.
func newParagraph(text string, decl bool) string {
	open := "<a:p>"
	if decl {
		open = `<a:p xmlns:a="` + nsA + `">`
	}
	runs := textRuns("a", text, `<a:rPr lang="en-US"/>`)
	if runs == "" {
		return open + `<a:endParaRPr lang="en-US"/></a:p>`
	}
	return open + runs + `</a:p>`
}

// SetTitle replaces the text of the title placeholder (type title or
// ctrTitle) of a slide. The formatting of the first run is kept and any
// further paragraphs of the title are removed.
func (d *Deck) SetTitle(slide int, text string) error {
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
		sp := titleShape(root)
		if sp == nil {
			return fmt.Errorf("pptxedit: slide %d has no title placeholder", slide)
		}
		var edits []edit
		tb := sp.child(nsP, "txBody")
		switch ps := paragraphs(sp); {
		case tb == nil:
			pfx := prefixOf(raw, sp)
			edits = append(edits, edit{sp.closeStart, sp.closeStart,
				"<" + qual(pfx, "txBody") + ` xmlns:a="` + nsA + `"><a:bodyPr/><a:lstStyle/>` + newParagraph(text, false) + "</" + qual(pfx, "txBody") + ">"})
		case len(ps) == 0:
			edits = append(edits, edit{tb.closeStart, tb.closeStart, newParagraph(text, true)})
		default:
			edits = append(edits, edit{ps[0].start, ps[0].end, rewriteParagraph(raw, ps[0], text)})
			for _, p := range ps[1:] {
				edits = append(edits, edit{p.start, p.end, ""})
			}
		}
		out, err := splice(raw, edits)
		if err != nil {
			return fmt.Errorf("pptxedit: %w", err)
		}
		d.set(ref.part, out)
		return nil
	})
}

// SetParagraph replaces the text of one paragraph of the first shape with
// this name on a slide. The paragraph number counts every a:p of the shape,
// from 0. The properties of the paragraph and of its first run are kept and
// the other runs are dropped. A newline in text becomes a line break.
func (d *Deck) SetParagraph(slide int, shape string, paragraph int, text string) error {
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
		for _, c := range shapes(root) {
			if shapeName(c) == shape {
				sp = c
				break
			}
		}
		if sp == nil {
			return fmt.Errorf("pptxedit: slide %d has no shape named %q", slide, shape)
		}
		ps := paragraphs(sp)
		if paragraph < 0 || paragraph >= len(ps) {
			return fmt.Errorf("pptxedit: shape %q has %d paragraphs, no paragraph %d", shape, len(ps), paragraph)
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

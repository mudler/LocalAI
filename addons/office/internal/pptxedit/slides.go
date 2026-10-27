package pptxedit

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
)

const groupHeader = `<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>` +
	`<p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>`

var layoutNameRe = regexp.MustCompile(`^slideLayout\d+\.xml$`)

// layoutPlaceholders are the placeholders of a layout that a new slide fills.
type layoutPlaceholder struct{ typ, idx string }

func (l layoutPlaceholder) xml() string {
	s := "<p:ph"
	if l.typ != "" {
		s += ` type="` + esc(l.typ) + `"`
	}
	if l.idx != "" {
		s += ` idx="` + esc(l.idx) + `"`
	}
	return s + "/>"
}

// findLayout finds a layout part by the name in its p:cSld element.
func (d *Deck) findLayout(name string) (part string, title, body *layoutPlaceholder, err error) {
	dir := d.presDir + "/slideLayouts/"
	if d.presDir == "." {
		dir = "slideLayouts/"
	}
	for _, n := range d.order {
		if !strings.HasPrefix(n, dir) || !layoutNameRe.MatchString(strings.TrimPrefix(n, dir)) {
			continue
		}
		raw, gerr := d.get(n)
		if gerr != nil {
			continue
		}
		root, perr := parseTree(raw)
		if perr != nil || !root.is(nsP, "sldLayout") {
			continue
		}
		if v, _ := root.child(nsP, "cSld").attrLocal("name"); v != name {
			continue
		}
		var bodies, subs, objs []layoutPlaceholder
		for _, sp := range shapes(root) {
			typ, idx, ok := placeholder(sp)
			if !ok {
				continue
			}
			ph := layoutPlaceholder{typ, idx}
			switch typ {
			case "title", "ctrTitle":
				if title == nil {
					title = &ph
				}
			case "body":
				bodies = append(bodies, ph)
			case "subTitle":
				subs = append(subs, ph)
			case "", "obj":
				objs = append(objs, ph)
			}
		}
		for _, list := range [][]layoutPlaceholder{bodies, subs, objs} {
			if len(list) > 0 {
				body = &list[0]
				break
			}
		}
		return n, title, body, nil
	}
	return "", nil, nil, fmt.Errorf("pptxedit: the deck has no layout named %q", name)
}

// AddSlide appends a slide that uses the layout with this name (the name in
// the layout's p:cSld). The title goes in the layout's title placeholder and
// each bullet is one paragraph of its body placeholder (a body, subtitle or
// content placeholder, in that order of preference). A layout without the
// placeholder a non-empty title or bullets need is an error.
func (d *Deck) AddSlide(layoutName, title string, bullets []string) error {
	return d.tx(func(d *Deck) error {
		refs, err := d.slideRefs()
		if err != nil {
			return err
		}
		layoutPart, titlePh, bodyPh, err := d.findLayout(layoutName)
		if err != nil {
			return err
		}
		if title != "" && titlePh == nil {
			return fmt.Errorf("pptxedit: layout %q has no title placeholder", layoutName)
		}
		if len(bullets) > 0 && bodyPh == nil {
			return fmt.Errorf("pptxedit: layout %q has no body placeholder", layoutName)
		}

		// The new slide part.
		slidesDir := d.presDir + "/slides"
		if d.presDir == "." {
			slidesDir = "slides"
		}
		var slidePart string
		for n := len(refs) + 1; ; n++ {
			slidePart = fmt.Sprintf("%s/slide%d.xml", slidesDir, n)
			if !d.has(slidePart) && !d.has(relsName(slidePart)) {
				break
			}
		}
		var sps strings.Builder
		if titlePh != nil {
			sps.WriteString(`<p:sp><p:nvSpPr><p:cNvPr id="2" name="Title 1"/><p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr><p:nvPr>` +
				titlePh.xml() + `</p:nvPr></p:nvSpPr><p:spPr/><p:txBody><a:bodyPr/><a:lstStyle/>` + newParagraph(title, false) + `</p:txBody></p:sp>`)
		}
		if bodyPh != nil {
			sps.WriteString(`<p:sp><p:nvSpPr><p:cNvPr id="3" name="Content Placeholder 2"/><p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr><p:nvPr>` +
				bodyPh.xml() + `</p:nvPr></p:nvSpPr><p:spPr/><p:txBody><a:bodyPr/><a:lstStyle/>`)
			if len(bullets) == 0 {
				sps.WriteString(newParagraph("", false))
			}
			for _, b := range bullets {
				sps.WriteString(newParagraph(b, false))
			}
			sps.WriteString(`</p:txBody></p:sp>`)
		}
		const header = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"
		d.set(slidePart, []byte(header+`<p:sld xmlns:a="`+nsA+`" xmlns:r="`+nsR+`" xmlns:p="`+nsP+`"><p:cSld><p:spTree>`+
			groupHeader+sps.String()+`</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sld>`))
		d.set(relsName(slidePart), []byte(header+`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
			`<Relationship Id="rId1" Type="`+relSlideLayout+`" Target="../slideLayouts/`+esc(path.Base(layoutPart))+`"/></Relationships>`))

		// presentation.xml.rels
		rels, relsRaw, relsRoot, err := d.relsOf(d.pres)
		if err != nil {
			return err
		}
		if relsRoot == nil || relsRoot.self {
			return errors.New("pptxedit: the presentation has no relationships part")
		}
		used := map[string]bool{}
		for _, r := range rels {
			used[r.id] = true
		}
		rid := ""
		for i := 1; ; i++ {
			if rid = "rId" + strconv.Itoa(i); !used[rid] {
				break
			}
		}
		rp := prefixOf(relsRaw, relsRoot)
		out, err := splice(relsRaw, []edit{{relsRoot.closeStart, relsRoot.closeStart,
			"<" + qual(rp, "Relationship") + ` Id="` + rid + `" Type="` + relSlide + `" Target="slides/` + esc(path.Base(slidePart)) + `"/>`}})
		if err != nil {
			return fmt.Errorf("pptxedit: %w", err)
		}
		d.set(relsName(d.pres), out)

		// presentation.xml: the slide list.
		raw, root, err := d.presentation()
		if err != nil {
			return err
		}
		maxID := 255
		for _, r := range refs {
			if v, err := strconv.Atoi(r.id); err == nil && v > maxID {
				maxID = v
			}
		}
		if maxID+1 >= 1<<31 {
			return errors.New("pptxedit: no free slide id")
		}
		pp := prefixOf(raw, root)
		rpfx, decl := "r", ` xmlns:r="`+nsR+`"`
		for _, a := range root.attr {
			if a.Name.Space == "xmlns" && a.Value == nsR {
				rpfx, decl = a.Name.Local, ""
			}
		}
		sldID := "<" + qual(pp, "sldId") + ` id="` + strconv.Itoa(maxID+1) + `" ` + rpfx + `:id="` + rid + `"` + decl + "/>"
		var e edit
		switch lst := root.child(nsP, "sldIdLst"); {
		case lst != nil && !lst.self:
			e = edit{lst.closeStart, lst.closeStart, sldID}
		case lst != nil:
			tag := tagName(raw, lst)
			e = edit{lst.start, lst.end, "<" + tag + ">" + sldID + "</" + tag + ">"}
		default:
			anchor := -1
			for _, k := range root.kids {
				if k.is(nsP, "sldMasterIdLst") || k.is(nsP, "notesMasterIdLst") || k.is(nsP, "handoutMasterIdLst") {
					anchor = k.end
				}
			}
			if anchor < 0 {
				return errors.New("pptxedit: the presentation has no place for a slide list")
			}
			t := qual(pp, "sldIdLst")
			e = edit{anchor, anchor, "<" + t + ">" + sldID + "</" + t + ">"}
		}
		if out, err = splice(raw, []edit{e}); err != nil {
			return fmt.Errorf("pptxedit: %w", err)
		}
		d.set(d.pres, out)

		// [Content_Types].xml
		ct, err := d.get(contentTypes)
		if err != nil {
			return err
		}
		ctRoot, err := parseTree(ct)
		if err != nil || ctRoot.self {
			return errors.New("pptxedit: [Content_Types].xml is not usable")
		}
		cp := prefixOf(ct, ctRoot)
		if out, err = splice(ct, []edit{{ctRoot.closeStart, ctRoot.closeStart,
			"<" + qual(cp, "Override") + ` PartName="/` + esc(slidePart) + `" ContentType="` + ctSlide + `"/>`}}); err != nil {
			return fmt.Errorf("pptxedit: %w", err)
		}
		d.set(contentTypes, out)
		d.setSlideCount(len(refs) + 1)
		return nil
	})
}

// protected relationship types are never removed with a slide: they are
// shared with the rest of the deck.
var protectedRel = map[string]bool{
	relBase + "slideLayout":   true,
	relBase + "slideMaster":   true,
	relBase + "theme":         true,
	relBase + "notesMaster":   true,
	relBase + "handoutMaster": true,
	relSlide:                  true,
	relOfficeDoc:              true,
}

// refCounts counts, per part, the internal relationships that point at it.
func (d *Deck) refCounts() (map[string]int, error) {
	counts := map[string]int{}
	for _, n := range d.order {
		if !strings.HasSuffix(n, ".rels") {
			continue
		}
		raw, err := d.get(n)
		if err != nil {
			return nil, err
		}
		rels, _, err := parseRels(raw)
		if err != nil {
			return nil, fmt.Errorf("pptxedit: %s: %w", n, err)
		}
		base := path.Dir(path.Dir(n))
		for _, r := range rels {
			if t, ok := resolve(base, r.target); ok && !r.external {
				counts[t]++
			}
		}
	}
	return counts, nil
}

// DeleteSlide removes slide number slide (1-based): its entry in the slide
// list (and in any section list), its relationship in the presentation, its
// part, its relationships, and its content type. Parts only that slide used
// (its notes page, its images) go too; layouts, masters and themes stay. The
// last slide of a deck cannot be deleted.
func (d *Deck) DeleteSlide(slide int) error {
	return d.tx(func(d *Deck) error {
		refs, err := d.slideRefs()
		if err != nil {
			return err
		}
		if slide < 1 || slide > len(refs) {
			return fmt.Errorf("pptxedit: slide %d out of range (the deck has %d)", slide, len(refs))
		}
		if len(refs) == 1 {
			return errors.New("pptxedit: cannot delete the last slide")
		}
		ref := refs[slide-1]
		for i, r := range refs {
			if i != slide-1 && (r.rid == ref.rid || r.part == ref.part) {
				return errors.New("pptxedit: another slide shares this slide part")
			}
		}

		// presentation.xml: the slide list and the section lists.
		raw, root, err := d.presentation()
		if err != nil {
			return err
		}
		edits := []edit{{ref.n.start, ref.n.end, ""}}
		root.walk(func(n *node) {
			if n.name.Local == "sldId" && n.parent != nil && n.parent.name.Local == "sldIdLst" && n.parent.name.Space != nsP {
				if v, _ := n.attrLocal("id"); v == ref.id {
					edits = append(edits, edit{n.start, n.end, ""})
				}
			}
		})
		out, err := splice(raw, edits)
		if err != nil {
			return fmt.Errorf("pptxedit: %w", err)
		}
		d.set(d.pres, out)

		// presentation.xml.rels
		rels, relsRaw, _, err := d.relsOf(d.pres)
		if err != nil {
			return err
		}
		removedRel := false
		for _, r := range rels {
			if r.id == ref.rid {
				if out, err = splice(relsRaw, []edit{{r.n.start, r.n.end, ""}}); err != nil {
					return fmt.Errorf("pptxedit: %w", err)
				}
				d.set(relsName(d.pres), out)
				removedRel = true
				break
			}
		}
		if !removedRel {
			return errors.New("pptxedit: the slide relationship is missing")
		}

		// The slide part, then whatever only it used.
		counts, err := d.refCounts()
		if err != nil {
			return err
		}
		var gone []string
		queue := []string{ref.part}
		counts[ref.part] = 0
		for len(queue) > 0 {
			name := queue[0]
			queue = queue[1:]
			if !d.has(name) || counts[name] > 0 {
				continue
			}
			prels, _, _, err := d.relsOf(name)
			if err != nil {
				return err
			}
			d.remove(name)
			d.remove(relsName(name))
			gone = append(gone, name)
			base := path.Dir(name)
			for _, r := range prels {
				if r.external {
					continue
				}
				t, ok := resolve(base, r.target)
				if !ok {
					continue
				}
				counts[t]--
				if !protectedRel[r.typ] {
					queue = append(queue, t)
				}
			}
		}

		// [Content_Types].xml
		ct, err := d.get(contentTypes)
		if err != nil {
			return err
		}
		ctRoot, err := parseTree(ct)
		if err != nil {
			return fmt.Errorf("pptxedit: %s: %w", contentTypes, err)
		}
		drop := map[string]bool{}
		for _, n := range gone {
			drop["/"+n] = true
		}
		var ctEdits []edit
		for _, k := range ctRoot.kids {
			if k.name.Local != "Override" {
				continue
			}
			if v, _ := k.attrLocal("PartName"); drop[v] {
				ctEdits = append(ctEdits, edit{k.start, k.end, ""})
			}
		}
		if len(ctEdits) > 0 {
			if out, err = splice(ct, ctEdits); err != nil {
				return fmt.Errorf("pptxedit: %w", err)
			}
			d.set(contentTypes, out)
		}
		d.setSlideCount(len(refs) - 1)
		return nil
	})
}

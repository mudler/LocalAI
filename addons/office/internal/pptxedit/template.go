// SPDX-License-Identifier: MIT
package pptxedit

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/mudler/LocalAI/addons/office/internal/ooxml"
)

const (
	ctPresMain     = "application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"
	ctTemplateMain = "application/vnd.openxmlformats-officedocument.presentationml.template.main+xml"
)

// OpenTemplate reads a .pptx or .potx file to build a new deck on: its slide
// masters, layouts and theme are kept as they are. Unlike Open it accepts a
// deck without slides, which is how a template usually comes. A .potx main
// part is declared as a presentation, so that the result is a .pptx file.
func OpenTemplate(data []byte) (*Deck, error) {
	d, err := open(data, true)
	if err != nil {
		return nil, err
	}
	ct, err := d.get(contentTypes)
	if err != nil {
		return nil, err
	}
	if bytes.Contains(ct, []byte(ctTemplateMain)) {
		d.set(contentTypes, bytes.ReplaceAll(ct, []byte(ctTemplateMain), []byte(ctPresMain)))
	}
	return d, nil
}

// LayoutOfType returns the name of the first layout whose type attribute is
// one of types, tried in that order. Layout names are localized by the office
// application that made the deck; the type is not.
func (d *Deck) LayoutOfType(types ...string) (string, bool) {
	dir := d.presDir + "/slideLayouts/"
	if d.presDir == "." {
		dir = "slideLayouts/"
	}
	byType := map[string]string{}
	for _, n := range d.order {
		if !strings.HasPrefix(n, dir) || !layoutNameRe.MatchString(strings.TrimPrefix(n, dir)) {
			continue
		}
		raw, err := d.get(n)
		if err != nil {
			continue
		}
		root, err := parseTree(raw)
		if err != nil || !root.is(nsP, "sldLayout") {
			continue
		}
		typ, _ := root.attrLocal("type")
		name, ok := root.child(nsP, "cSld").attrLocal("name")
		if _, seen := byType[typ]; !seen && ok && name != "" {
			byType[typ] = name
		}
	}
	for _, t := range types {
		if name, ok := byType[t]; ok {
			return name, true
		}
	}
	return "", false
}

// SlideCount is the number of slides in the slide list.
func (d *Deck) SlideCount() int {
	refs, err := d.slideRefs()
	if err != nil {
		return 0
	}
	return len(refs)
}

var docTitleRe = regexp.MustCompile(`<dc:title\s*/>|<dc:title>[^<]*</dc:title>`)

// SetDocTitle sets the title in docProps/core.xml when the deck has one.
func (d *Deck) SetDocTitle(title string) {
	const name = "docProps/core.xml"
	if !d.has(name) {
		return
	}
	raw, err := d.get(name)
	if err != nil {
		return
	}
	loc := docTitleRe.FindIndex(raw)
	if loc == nil {
		return
	}
	out := append(append(append([]byte{}, raw[:loc[0]]...), "<dc:title>"+ooxml.Escape(title)+"</dc:title>"...), raw[loc[1]:]...)
	d.set(name, out)
}

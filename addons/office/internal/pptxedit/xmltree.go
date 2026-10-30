package pptxedit

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// maxDepth bounds element nesting so a hostile part cannot exhaust memory.
const maxDepth = 256

// node is one element of a parsed part with its byte range in the source.
// The tree is read only: edits are byte splices of the original text, so
// everything outside an edited range stays exactly as it was.
type node struct {
	name   xml.Name
	attr   []xml.Attr
	parent *node
	kids   []*node
	// start is the offset of '<' of the open tag, openEnd the offset after
	// its '>', closeStart the offset of '<' of the close tag (equal to
	// openEnd for an empty element) and end the offset after the element.
	start, openEnd, closeStart, end int
	self                            bool // written as <x/>
	text                            string
}

func parseTree(raw []byte) (*node, error) {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	root := &node{}
	cur := root
	depth := 0
	for {
		pos := int(dec.InputOffset())
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		off := int(dec.InputOffset())
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth > maxDepth {
				return nil, errors.New("elements are nested too deep")
			}
			n := &node{name: t.Name, attr: append([]xml.Attr(nil), t.Attr...), parent: cur, start: pos, openEnd: off}
			n.self = off >= 2 && raw[off-2] == '/' && raw[off-1] == '>'
			cur.kids = append(cur.kids, n)
			cur = n
		case xml.EndElement:
			if cur == root {
				return nil, errors.New("unbalanced end element")
			}
			cur.closeStart = pos
			cur.end = off
			cur = cur.parent
			depth--
		case xml.CharData:
			if cur.name.Local == "t" {
				cur.text += string(t)
			}
		}
	}
	if cur != root || len(root.kids) != 1 {
		return nil, errors.New("not a single well-formed element")
	}
	return root.kids[0], nil
}

func (n *node) is(space, local string) bool {
	return n != nil && n.name.Local == local && n.name.Space == space
}

func (n *node) child(space, local string) *node {
	if n == nil {
		return nil
	}
	for _, k := range n.kids {
		if k.is(space, local) {
			return k
		}
	}
	return nil
}

func (n *node) children(space, local string) []*node {
	var out []*node
	if n == nil {
		return nil
	}
	for _, k := range n.kids {
		if k.is(space, local) {
			out = append(out, k)
		}
	}
	return out
}

// walk visits n and every descendant in document order.
func (n *node) walk(fn func(*node)) {
	fn(n)
	for _, k := range n.kids {
		k.walk(fn)
	}
}

// attrLocal returns the value of the attribute with this local name and no
// namespace.
func (n *node) attrLocal(local string) (string, bool) {
	if n == nil {
		return "", false
	}
	for _, a := range n.attr {
		if a.Name.Local == local && a.Name.Space == "" {
			return a.Value, true
		}
	}
	return "", false
}

// relAttr returns the r:id style attribute, which lives in the
// relationships namespace.
func (n *node) relAttr(local string) string {
	for _, a := range n.attr {
		if a.Name.Local == local && strings.HasSuffix(a.Name.Space, "/relationships") {
			return a.Value
		}
	}
	return ""
}

// tagName returns the qualified name of n as written in raw.
func tagName(raw []byte, n *node) string {
	rest := raw[n.start+1 : n.openEnd]
	i := bytes.IndexAny(rest, " \t\r\n/>")
	if i < 0 {
		i = len(rest)
	}
	return string(rest[:i])
}

// prefixOf returns the prefix of the qualified name of n ("" for none).
func prefixOf(raw []byte, n *node) string {
	q := tagName(raw, n)
	if before, _, ok := strings.Cut(q, ":"); ok {
		return before
	}
	return ""
}

// qual joins a prefix and a local name.
func qual(prefix, local string) string {
	if prefix == "" {
		return local
	}
	return prefix + ":" + local
}

// paraText returns the text of a paragraph the way officeread reads it: the
// text runs in order, with a line break for every a:br.
func paraText(n *node) string {
	var b strings.Builder
	n.walk(func(k *node) {
		switch k.name.Local {
		case "t":
			b.WriteString(k.text)
		case "br":
			b.WriteByte('\n')
		}
	})
	return b.String()
}

// edit replaces raw[start:end] with text.
type edit struct {
	start, end int
	text       string
}

func splice(raw []byte, edits []edit) ([]byte, error) {
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
	var out bytes.Buffer
	last := 0
	for _, e := range edits {
		if e.start < last || e.end < e.start || e.end > len(raw) {
			return nil, fmt.Errorf("overlapping or invalid edit at %d", e.start)
		}
		out.Write(raw[last:e.start])
		out.WriteString(e.text)
		last = e.end
	}
	out.Write(raw[last:])
	return out.Bytes(), nil
}

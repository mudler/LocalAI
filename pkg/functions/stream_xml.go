package functions

import "strings"

// anchorWatch tells whether one of its anchors occurs in an output that
// grows, reading each byte of the output about once.
type anchorWatch struct {
	anchors []string
	longest int
	seen    bool
	checked int
}

func newAnchorWatch(anchors []string) anchorWatch {
	w := anchorWatch{anchors: anchors}
	for _, a := range anchors {
		w.longest = max(w.longest, len(a))
	}
	return w
}

func (w *anchorWatch) reset() {
	w.seen, w.checked = false, 0
}

// update reads what was written to s since the last call. A new occurrence
// ends in the new bytes, so it starts at most longest-1 bytes before them.
func (w *anchorWatch) update(s string) {
	if w.seen || len(s) <= w.checked {
		return
	}
	window := s[max(0, w.checked-w.longest+1):]
	for _, a := range w.anchors {
		if strings.Contains(window, a) {
			w.seen = true
			break
		}
	}
	w.checked = len(s)
}

// xmlFormatAnchors returns the strings without which ParseXMLIterative finds
// no call of the format: the start that tryParseXMLFromScopeStart looks for,
// and the first literal or pattern that the parse from position 0 must
// match. It returns false for a format whose parse can fail or find a call
// without them, such as a format that lacks a required field.
func xmlFormatAnchors(f *XMLToolCallFormat) ([]string, bool) {
	var second string
	switch {
	case f.KeyStart == "" && f.ToolStart == "<function=":
		// parseFunctionaryFormat: its pattern starts with "<function=".
		second = f.ToolStart
	case f.ToolStart != "" && strings.Contains(f.ToolStart, "{\"name\""):
		// parseJSONLikeXMLFormat: its pattern starts with the scope start,
		// or with the tool start when there is no scope.
		second = f.ScopeStart
		if second == "" {
			second = f.ToolStart
		}
	default:
		if f.ToolStart == "" || f.KeyStart == "" || f.KeyValSep == "" || f.ValEnd == "" || f.ToolEnd == "" {
			return nil, false
		}
		second = f.ToolStart
		if f.ScopeStart != "" && !AllSpace(f.ScopeStart) {
			second = f.ScopeStart
		}
	}
	first := getScopeOrToolStart(f)
	if first == "" || second == "" {
		return nil, false
	}
	return []string{first, second}, true
}

// xmlParseAnchors returns the anchors of the format, or of every preset when
// the format is nil, as ParseXMLIterative then tries them all.
func xmlParseAnchors(format *XMLToolCallFormat) ([]string, bool) {
	if format != nil {
		return xmlFormatAnchors(format)
	}
	var all []string
	for _, preset := range getAllXMLFormats() {
		anchors, ok := xmlFormatAnchors(preset.format)
		if !ok {
			return nil, false
		}
		all = append(all, anchors...)
	}
	return all, true
}

// xmlStreamGate tells when ParseXMLIterative(s, format, true) of an output
// that grows can only return an empty result, so the parse can be skipped.
//
// The parse needs an anchor of the format in the output. Without one, the
// only text it reacts to is the start of an anchor at the very end, and only
// when the text before it is blank: the parse from position 0 then takes it for
// a block that starts. A solid byte (one that allSpaceOrEscapedNewlines keeps)
// longer than an anchor before the end rules that out.
type xmlStreamGate struct {
	usable     bool
	watch      anchorWatch
	firstSolid int // -1 until a solid byte is found
	scanned    int
}

func newXMLStreamGate(format *XMLToolCallFormat) xmlStreamGate {
	anchors, ok := xmlParseAnchors(format)
	return xmlStreamGate{usable: ok, watch: newAnchorWatch(anchors), firstSolid: -1}
}

func (g *xmlStreamGate) reset() {
	g.watch.reset()
	g.firstSolid, g.scanned = -1, 0
}

// solid reports whether allSpaceOrEscapedNewlines keeps the byte in any text:
// printable ASCII other than the backslash and the letters of the "\n" and
// "\r" escapes it removes.
func solid(c byte) bool {
	return c > ' ' && c < 0x7f && c != '\\' && c != 'n' && c != 'r'
}

// empty reports whether ParseXMLIterative(s, format, true) returns an empty
// result. s must start with the output of the last call.
func (g *xmlStreamGate) empty(s string) bool {
	if !g.usable {
		return false
	}
	g.watch.update(s)
	if g.watch.seen {
		return false
	}
	for ; g.firstSolid < 0 && g.scanned < len(s); g.scanned++ {
		if solid(s[g.scanned]) {
			g.firstSolid = g.scanned
		}
	}
	return g.firstSolid >= 0 && g.firstSolid+g.watch.longest <= len(s)
}

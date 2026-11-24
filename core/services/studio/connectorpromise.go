package studio

import (
	"strings"
	"unicode"
)

// paperclip is the marker a model writes in place of a file it did not send.
const paperclip = "📎"

// promisesFile says whether an answer presents a file as if it went with it:
// a paperclip followed at once by a file name of a type that is sent, such as
// "📎 `report.pdf`". The host attaches files itself, so a model that writes
// such a line has made the file up or forgot to call attach_file. The test is
// narrow on purpose: a file name in prose, a name in a code block and a
// paperclip alone do not match.
func promisesFile(answer string) bool {
	rest := answer
	for {
		i := strings.Index(rest, paperclip)
		if i < 0 {
			return false
		}
		rest = rest[i+len(paperclip):]
		if _, ok := outboundType(nameAfterClip(rest)); ok {
			return true
		}
	}
}

// nameAfterClip reads the file name that starts a text after the paperclip:
// optional spaces, the variation selector of the emoji and light markup, then
// a name in code quotes (it may hold spaces) or up to the next space.
func nameAfterClip(s string) string {
	s = strings.TrimLeftFunc(strings.TrimPrefix(s, "️"), func(r rune) bool {
		return r == ' ' || r == '\t' || r == '*' || r == '_' || r == '"' || r == '\''
	})
	if rest, ok := strings.CutPrefix(s, "`"); ok {
		name, _, _ := strings.Cut(rest, "`")
		if strings.ContainsAny(name, "\r\n") {
			return ""
		}
		return strings.TrimSpace(name)
	}
	if i := strings.IndexFunc(s, unicode.IsSpace); i >= 0 {
		s = s[:i]
	}
	return strings.TrimRight(s, ".,;:!?*_\"')]")
}

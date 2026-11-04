// SPDX-License-Identifier: MIT
package gdrivetest

import (
	"fmt"
	"strings"
	"time"
)

// cond is one condition of a q expression.
type cond struct {
	kind  string // name, fulltext, mime, parent, trashed, after, before, prop
	value string
	extra string
}

func (c cond) match(f *File) bool {
	switch c.kind {
	case "name":
		return strings.Contains(strings.ToLower(f.Name), strings.ToLower(c.value))
	case "fulltext":
		l := strings.ToLower(c.value)
		return strings.Contains(strings.ToLower(f.Name), l) || strings.Contains(strings.ToLower(string(f.Content)), l)
	case "mime":
		return f.MimeType == c.value
	case "parent":
		for _, p := range f.Parents {
			if p == c.value || (c.value == "root" && p == "root-id") {
				return true
			}
		}
		return false
	case "trashed":
		return f.Trashed == (c.value == "true")
	case "after", "before":
		t, err := time.Parse(time.RFC3339, c.value)
		if err != nil {
			return false
		}
		if c.kind == "after" {
			return f.Modified.After(t)
		}
		return f.Modified.Before(t)
	case "prop":
		return f.AppProperties[c.value] == c.extra
	}
	return false
}

// scanner reads a q expression. Inside a string literal only \\ and \' are
// escapes; any other backslash, or text after the closing quote, is an error,
// as the service says "Invalid Value" for it.
type scanner struct {
	s string
	i int
}

func (sc *scanner) space() {
	for sc.i < len(sc.s) && sc.s[sc.i] == ' ' {
		sc.i++
	}
}

func (sc *scanner) word() string {
	sc.space()
	start := sc.i
	for sc.i < len(sc.s) && sc.s[sc.i] != ' ' && sc.s[sc.i] != '\'' {
		sc.i++
	}
	return sc.s[start:sc.i]
}

func (sc *scanner) str() (string, error) {
	sc.space()
	if sc.i >= len(sc.s) || sc.s[sc.i] != '\'' {
		return "", fmt.Errorf("string expected")
	}
	sc.i++
	var b strings.Builder
	for sc.i < len(sc.s) {
		c := sc.s[sc.i]
		switch {
		case c == '\\':
			if sc.i+1 >= len(sc.s) || (sc.s[sc.i+1] != '\\' && sc.s[sc.i+1] != '\'') {
				return "", fmt.Errorf("bad escape")
			}
			b.WriteByte(sc.s[sc.i+1])
			sc.i += 2
		case c == '\'':
			sc.i++
			// A literal ends before a space or the end.
			if sc.i < len(sc.s) && sc.s[sc.i] != ' ' {
				return "", fmt.Errorf("text after a string")
			}
			return b.String(), nil
		default:
			b.WriteByte(c)
			sc.i++
		}
	}
	return "", fmt.Errorf("unterminated string")
}

func (sc *scanner) expect(w string) error {
	if got := sc.word(); got != w {
		return fmt.Errorf("expected %s", w)
	}
	return nil
}

func (sc *scanner) done() bool { sc.space(); return sc.i >= len(sc.s) }

// parseQuery parses conditions joined by "and".
func parseQuery(q string) ([]cond, error) {
	var out []cond
	sc := &scanner{s: q}
	for first := true; ; first = false {
		if !first {
			if sc.done() {
				return out, nil
			}
			if err := sc.expect("and"); err != nil {
				return nil, err
			}
		} else if sc.done() {
			return out, nil
		}
		sc.space()
		var c cond
		if sc.i < len(sc.s) && sc.s[sc.i] == '\'' {
			v, err := sc.str()
			if err != nil {
				return nil, err
			}
			if err := sc.expect("in"); err != nil {
				return nil, err
			}
			if err := sc.expect("parents"); err != nil {
				return nil, err
			}
			c = cond{kind: "parent", value: v}
		} else {
			field := sc.word()
			op := sc.word()
			switch {
			case field == "name" && op == "contains", field == "fullText" && op == "contains":
				v, err := sc.str()
				if err != nil {
					return nil, err
				}
				c = cond{kind: strings.ToLower(field), value: v}
			case field == "mimeType" && op == "=":
				v, err := sc.str()
				if err != nil {
					return nil, err
				}
				c = cond{kind: "mime", value: v}
			case field == "trashed" && op == "=":
				v := sc.word()
				if v != "true" && v != "false" {
					return nil, fmt.Errorf("bool expected")
				}
				c = cond{kind: "trashed", value: v}
			case field == "modifiedTime" && (op == ">" || op == "<"):
				v, err := sc.str()
				if err != nil {
					return nil, err
				}
				c = cond{kind: map[string]string{">": "after", "<": "before"}[op], value: v}
			case field == "appProperties" && op == "has":
				if err := sc.expect("{"); err != nil {
					return nil, err
				}
				if err := sc.expect("key="); err != nil {
					// key='k' is read as the word "key=" then a string.
					return nil, err
				}
				k, err := sc.str()
				if err != nil {
					return nil, err
				}
				if err := sc.expect("and"); err != nil {
					return nil, err
				}
				if err := sc.expect("value="); err != nil {
					return nil, err
				}
				v, err := sc.str()
				if err != nil {
					return nil, err
				}
				sc.space()
				if err := sc.expect("}"); err != nil {
					return nil, err
				}
				c = cond{kind: "prop", value: k, extra: v}
			default:
				return nil, fmt.Errorf("unknown condition")
			}
		}
		out = append(out, c)
	}
}

// SPDX-License-Identifier: MIT
package gmailtest

import (
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Term is one element of a search expression: an operator with its value, or
// a bare word or phrase (Op empty).
type Term struct{ Op, Value string }

// operators are the search operators the fake understands. Any other word
// followed by a colon is read as text, as the service does.
var operators = map[string]bool{
	"from": true, "to": true, "subject": true, "has": true, "is": true, "after": true, "before": true,
	"label": true, "in": true, "cc": true, "bcc": true, "filename": true, "larger": true, "smaller": true,
	"older_than": true, "newer_than": true, "category": true, "list": true, "deliveredto": true, "rfc822msgid": true,
}

// ParseQuery splits a q expression into terms. A double quoted run is one
// value and holds no operator. The characters ( ) { } - and OR outside quotes
// are returned as terms of their own with Op "group", so a test can see that
// a value did not smuggle them in.
func ParseQuery(q string) []Term {
	var out []Term
	rs := []rune(q)
	i := 0
	for i < len(rs) {
		switch r := rs[i]; {
		case unicode.IsSpace(r):
			i++
		case r == '(' || r == ')' || r == '{' || r == '}':
			out = append(out, Term{Op: "group", Value: string(r)})
			i++
		default:
			start := i
			for i < len(rs) && !unicode.IsSpace(rs[i]) && rs[i] != '"' && rs[i] != '(' && rs[i] != ')' && rs[i] != '{' && rs[i] != '}' {
				i++
			}
			word := string(rs[start:i])
			op := ""
			if k := strings.IndexByte(word, ':'); k > 0 && operators[strings.ToLower(word[:k])] {
				op, word = strings.ToLower(word[:k]), word[k+1:]
			}
			if i < len(rs) && rs[i] == '"' && word == "" {
				j := i + 1
				for j < len(rs) && rs[j] != '"' {
					j++
				}
				out = append(out, Term{Op: op, Value: string(rs[i+1 : min(j, len(rs))])})
				i = min(j+1, len(rs))
				continue
			}
			switch {
			case word == "OR" || word == "AND":
				out = append(out, Term{Op: "group", Value: word})
			case strings.HasPrefix(word, "-") && op == "":
				out = append(out, Term{Op: "group", Value: "-"})
				out = append(out, Term{Value: word[1:]})
			case word != "" || op != "":
				out = append(out, Term{Op: op, Value: word})
			}
		}
	}
	return out
}

func lower(s string) string { return strings.ToLower(s) }

// text is every searchable piece of a message.
func plainText(pt *Part) string {
	if pt == nil {
		return ""
	}
	var b strings.Builder
	if strings.HasPrefix(pt.MimeType, "text/") && pt.Filename == "" {
		b.Write(pt.Data)
	}
	for _, c := range pt.Parts {
		b.WriteString(" " + plainText(c))
	}
	return b.String()
}

func hasAttachment(pt *Part) bool {
	if pt == nil {
		return false
	}
	if pt.Filename != "" || pt.AttachmentID != "" {
		return true
	}
	for _, c := range pt.Parts {
		if hasAttachment(c) {
			return true
		}
	}
	return false
}

// matches reports whether the message satisfies every term. A "group" term
// makes the fake match nothing, so a test that lets one through sees an empty
// result and the call log shows why.
func matches(m *Message, terms []Term) bool {
	for _, t := range terms {
		v := lower(t.Value)
		switch t.Op {
		case "":
			all := lower(header(m, "From") + " " + header(m, "To") + " " + header(m, "Subject") + " " + m.Snippet + " " + plainText(m.Payload))
			if !strings.Contains(all, v) {
				return false
			}
		case "from", "to", "subject", "cc":
			if !strings.Contains(lower(header(m, t.Op)), v) {
				return false
			}
		case "has":
			if v != "attachment" || !hasAttachment(m.Payload) {
				return false
			}
		case "is":
			if v != "unread" || !hasLabel(m, "UNREAD") {
				return false
			}
		case "after", "before":
			n, err := strconv.ParseInt(t.Value, 10, 64)
			if err != nil {
				return false
			}
			at := time.Unix(n, 0)
			if (t.Op == "after" && !m.Internal.After(at)) || (t.Op == "before" && !m.Internal.Before(at)) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// SPDX-License-Identifier: MIT
package tools

import (
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// quote makes one value of the q expression of a search. The value becomes a
// phrase in double quotes. Inside a phrase Gmail reads no operator, no group
// and no minus sign, so the only way out is the double quote and the
// backslash: both are removed, and so is every control character. Nothing the
// model wrote can end the phrase and add an operator. An empty result is
// refused.
func quote(s string) (string, bool) {
	s = strings.Map(func(r rune) rune {
		if r == '"' || r == '\\' || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return "", false
	}
	return `"` + s + `"`, true
}

// queryFilters are the values a search turns into a q expression.
type queryFilters struct {
	From, To, Subject, Text, After, Before string
	HasAttachment, UnreadOnly              bool
}

// epoch reads a date (2026-01-31) or an RFC 3339 time and returns Unix
// seconds, which the search reads as an exact time.
func epoch(s string) (string, bool) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return strconv.FormatInt(t.Unix(), 10), true
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return strconv.FormatInt(t.Unix(), 10), true
	}
	return "", false
}

// buildQuery makes the q expression. Every value is checked and quoted; the
// operators are fixed words of this function.
func buildQuery(f queryFilters) (string, error) {
	var terms []string
	for _, v := range []struct{ what, op, val string }{
		{"from", "from:", f.From}, {"to", "to:", f.To}, {"subject", "subject:", f.Subject}, {"text", "", f.Text},
	} {
		if v.val == "" {
			continue
		}
		if utf8.RuneCountInString(v.val) > 200 {
			return "", bad("%s has at most 200 characters", v.what)
		}
		q, ok := quote(v.val)
		if !ok {
			return "", bad("%s holds no usable character", v.what)
		}
		terms = append(terms, v.op+q)
	}
	for _, v := range []struct{ what, op, val string }{{"after", "after:", f.After}, {"before", "before:", f.Before}} {
		if v.val == "" {
			continue
		}
		n, ok := epoch(v.val)
		if !ok {
			return "", bad("%s is a date or a time like 2026-01-31", v.what)
		}
		terms = append(terms, v.op+n)
	}
	if f.HasAttachment {
		terms = append(terms, "has:attachment")
	}
	if f.UnreadOnly {
		terms = append(terms, "is:unread")
	}
	return strings.Join(terms, " "), nil
}

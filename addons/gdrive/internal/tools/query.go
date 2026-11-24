// SPDX-License-Identifier: MIT
package tools

import (
	"strings"
	"time"
)

// literal quotes a value as a string literal of the q expression of a file
// list. The service reads a backslash and a single quote inside a literal as
// escapes, so both are escaped; nothing the model wrote can end the literal
// and add a condition. A value with a control character is refused before
// it gets here (see plain).
func literal(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return "'" + s + "'"
}

// condition is one clause of a list query, built from checked arguments.
type conditions []string

func (c *conditions) add(format string, args ...string) {
	parts := strings.Split(format, "%s")
	var b strings.Builder
	for i, p := range parts {
		b.WriteString(p)
		if i < len(args) {
			b.WriteString(literal(args[i]))
		}
	}
	*c = append(*c, b.String())
}

func (c conditions) String() string { return strings.Join(c, " and ") }

// timeLiteral reads an RFC 3339 time or a date and returns the RFC 3339 form
// the service expects, in UTC.
func timeLiteral(s string) (string, bool) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format(time.RFC3339), true
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.UTC().Format(time.RFC3339), true
	}
	return "", false
}

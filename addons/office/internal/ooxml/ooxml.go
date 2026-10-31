// Package ooxml holds the small pieces that every Office Open XML writer and
// reader of the add-on shares: text escaping and the check of a part
// name.
package ooxml

import (
	"bytes"
	"encoding/xml"
	"slices"
	"strings"
)

// Escape returns s escaped for XML character data and attribute values.
func Escape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// HasDotDot reports whether a part name has a ".." segment, with "/" or "\"
// as the separator. A package that names such a part could write or read
// outside its folder once it is unpacked.
func HasDotDot(name string) bool {
	return slices.Contains(strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' }), "..")
}

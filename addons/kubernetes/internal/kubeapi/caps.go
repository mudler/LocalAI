// SPDX-License-Identifier: MIT
package kubeapi

import (
	"strings"
	"unicode/utf8"
)

// MaxText is the most bytes of one text field of a result.
const MaxText = 32 << 10

// Cap makes s valid UTF-8 and cuts it to at most max bytes on a rune
// boundary. It reports whether it cut.
func Cap(s string, max int) (string, bool) {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	if len(s) <= max {
		return s, false
	}
	n := max
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], true
}

// Short cuts a message for an error to 300 bytes.
func Short(s string) string {
	out, _ := Cap(s, 300)
	return out
}

// CapTail is Cap that keeps the end of s: the newest lines of a log.
func CapTail(s string, max int) (string, bool) {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	if len(s) <= max {
		return s, false
	}
	n := len(s) - max
	for n < len(s) && !utf8.RuneStart(s[n]) {
		n++
	}
	return s[n:], true
}

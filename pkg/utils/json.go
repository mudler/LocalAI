package utils

import "regexp"

var matchNewlines = regexp.MustCompile(`[\r\n]`)

const doubleQuote = `"[^"\\]*(?:\\[\s\S][^"\\]*)*"`

func EscapeNewLines(s string) string {
	return regexp.MustCompile(doubleQuote).ReplaceAllStringFunc(s, func(s string) string {
		return matchNewlines.ReplaceAllString(s, "\\n")
	})
}

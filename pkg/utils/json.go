package utils

import "regexp"

var matchNewlines = regexp.MustCompile(`[\r\n]`)

const doubleQuote = `"[^"\\]*(?:\\[\s\S][^"\\]*)*"`

var matchQuotedStrings = regexp.MustCompile(doubleQuote)

func EscapeNewLines(s string) string {
	return matchQuotedStrings.ReplaceAllStringFunc(s, func(s string) string {
		return matchNewlines.ReplaceAllString(s, "\\n")
	})
}

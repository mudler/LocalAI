package utils

import (
	"unicode/utf8"
)

func Unique(arr []string) []string {
	unique := make(map[string]bool)
	var result []string
	for _, item := range arr {
		if _, ok := unique[item]; !ok {
			unique[item] = true
			result = append(result, item)
		}
	}
	return result
}

// TruncateUTF8 cuts s to at most n bytes without splitting a character, so the
// result is still valid UTF-8 when s was.
func TruncateUTF8(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

package utils

import (
	"math/rand/v2"
	"unicode/utf8"
)

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

func RandString(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.IntN(len(letterRunes))]
	}
	return string(b)
}

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

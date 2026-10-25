package session

import (
	"encoding/json"
	"unicode/utf8"
)

// Summarize returns a short label for a tool call: the first of the string
// arguments command, path or pattern, at most 200 bytes.
func Summarize(args json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(args, &m) != nil {
		return ""
	}
	for _, k := range []string{"command", "path", "pattern"} {
		if s, ok := m[k].(string); ok && s != "" {
			return CapText(s, 200)
		}
	}
	return ""
}

// CapText cuts s to at most n bytes, on a rune boundary.
func CapText(s string, n int) string {
	if n < 0 {
		n = 0
	}
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

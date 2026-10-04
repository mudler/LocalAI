// SPDX-License-Identifier: MIT
package systemone

import "unicode/utf8"

func spendJSONBytes(left *int, n int) bool {
	if n > *left {
		return false
	}
	*left -= n
	return true
}

// SpendJSONString budgets encoding/json string escaping without allocating output.
func SpendJSONString(s string, left *int) bool {
	if !spendJSONBytes(left, 2) || len(s) > *left {
		return false
	}
	for i := 0; i < len(s); {
		c := s[i]
		n := 1
		switch {
		case c == '"' || c == '\\':
			n = 2
		case c == '\n' || c == '\r' || c == '\t' || c == '\b' || c == '\f':
			n = 2
		case c < 0x20 || c == '<' || c == '>' || c == '&':
			n = 6
		case c >= utf8.RuneSelf:
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size == 1 {
				n = 6
			} else if r == '\u2028' || r == '\u2029' {
				n = 6
				i += size - 1
			} else {
				n = size
				i += size - 1
			}
		}
		if !spendJSONBytes(left, n) {
			return false
		}
		i++
	}
	return true
}

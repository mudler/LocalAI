package transport

import "strings"

// Split cuts text into parts of at most max characters. It cuts at a line
// break when there is one in the last half of the part, else at a space, else at
// max, and it never cuts a character in two.
func Split(text string, max int) []string {
	r := []rune(strings.TrimSpace(text))
	if len(r) == 0 {
		return nil
	}
	var out []string
	for len(r) > max {
		cut := max
		for i := max; i > max/2; i-- {
			if r[i-1] == '\n' {
				cut = i
				break
			}
		}
		if cut == max {
			for i := max; i > max/2; i-- {
				if r[i-1] == ' ' {
					cut = i
					break
				}
			}
		}
		out = append(out, strings.TrimRight(string(r[:cut]), " \n"))
		r = []rune(strings.TrimLeft(string(r[cut:]), " \n"))
	}
	if len(r) > 0 {
		out = append(out, string(r))
	}
	return out
}

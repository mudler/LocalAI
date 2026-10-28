// SPDX-License-Identifier: MIT
package connector

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

// fence is the line that opens and closes a code block in Markdown and in
// the markup of the platforms that borrow it.
const fence = "```"

// SplitFenced is Split for text with code blocks. A part that ends inside a
// code block gets the block closed, and the next part opens it again, so each
// message shows its code as code. Parts stay within max characters.
func SplitFenced(text string, max int) []string {
	// Room for the fence that closes a part and the one that opens the next.
	room := 2 * (len(fence) + 1)
	if max <= 2*room || !strings.Contains(text, fence) {
		return Split(text, max)
	}
	parts := Split(text, max-room)
	open := false
	for i, p := range parts {
		reopen := open
		for _, line := range strings.Split(p, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), fence) {
				open = !open
			}
		}
		if reopen {
			p = fence + "\n" + p
		}
		if open && i < len(parts)-1 {
			p += "\n" + fence
		}
		parts[i] = p
	}
	return parts
}

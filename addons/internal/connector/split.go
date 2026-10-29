// SPDX-License-Identifier: MIT
package connector

import (
	"strings"
	"unicode/utf8"
)

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
		n := cut(r, max)
		out = append(out, strings.TrimRight(string(r[:n]), " \n"))
		r = []rune(strings.TrimLeft(string(r[n:]), " \n"))
	}
	if len(r) > 0 {
		out = append(out, string(r))
	}
	return out
}

// cut is where Split cuts r, which is longer than max: after a line break in
// the last half of the part, else after a space there, else at max.
func cut(r []rune, max int) int {
	for _, sep := range []rune{'\n', ' '} {
		for i := max; i > max/2; i-- {
			if r[i-1] == sep {
				return i
			}
		}
	}
	return max
}

// fence is the line that opens and closes a code block in Markdown and in
// the markup of the platforms that borrow it.
const fence = "```"

// SplitFenced is Split for text with code blocks. A part that ends inside a
// code block gets the block closed, and the next part opens it again with the
// language of the block, so each message shows its code as code. Inside a
// block a cut keeps the indentation of the next line. Parts stay within max
// characters.
func SplitFenced(text string, max int) []string {
	// Room for the fence that closes a part.
	closing := len(fence) + 1
	if max <= 4*closing || !strings.Contains(text, fence) {
		return Split(text, max)
	}
	r := []rune(strings.TrimSpace(text))
	var out []string
	open, lang := false, ""
	for len(r) > 0 {
		head := ""
		if open {
			head = fence + lang + "\n"
		}
		limit := max - utf8.RuneCountInString(head)
		n := len(r)
		if n > limit {
			n = cut(r, limit-closing)
		}
		part, rest := string(r[:n]), r[n:]
		open, lang = fenceState(open, lang, part, max/4)
		trim := " \n"
		if open {
			// A space at a cut inside a block is code: keep it.
			trim = "\n"
		}
		part = head + strings.TrimRight(part, trim)
		if open && len(rest) > 0 {
			part += "\n" + fence
		}
		out = append(out, part)
		r = []rune(strings.TrimLeft(string(rest), trim))
	}
	return out
}

// fenceState follows the fences of part from the state open, with the
// language lang of the open block. A language longer than maxLang is not
// carried, so the fence that opens the block again stays short.
func fenceState(open bool, lang, part string, maxLang int) (bool, string) {
	for _, line := range strings.Split(part, "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, fence) {
			continue
		}
		if open {
			open, lang = false, ""
			continue
		}
		open, lang = true, strings.TrimSpace(strings.TrimPrefix(t, fence))
		if utf8.RuneCountInString(lang) > maxLang || strings.Contains(lang, "`") {
			lang = ""
		}
	}
	return open, lang
}

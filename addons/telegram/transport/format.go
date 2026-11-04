package transport

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// A model writes Markdown. Telegram reads its own dialect, MarkdownV2, where
// every reserved character outside an entity must be escaped. The answer is
// converted to it, and to plain text for when Telegram still refuses it.

var (
	// A link target may hold one level of parentheses, as many wiki
	// addresses do.
	mdLink = regexp.MustCompile(`\[([^\]\n]+)\]\(((?:[^()\s]|\([^()\s]*\))+)\)`)
	mdBold = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	// Italics hug their text, so the star of a list item opens none.
	mdItalic  = regexp.MustCompile(`\*([^*\s](?:[^*\n]*[^*\s])?)\*`)
	mdFence   = regexp.MustCompile("(?m)^[ \t]*```[^\n]*\n?")
	mdHeading = regexp.MustCompile(`(?m)^#{1,6}[ \t]+(.+)$`)
	mdCode    = regexp.MustCompile("`([^`\n]+)`")
)

// reservedV2 are the characters that MarkdownV2 wants escaped in text.
const reservedV2 = "_*[]()~`>#+-=|{}.!\\"

// MarkdownV2 converts Markdown to Telegram's MarkdownV2: headings and bold
// become bold, single stars italics, links stay links, inline code and code
// blocks stay code, and everything else is escaped. A code block keeps its
// language.
func MarkdownV2(md string) string {
	segs := strings.Split(md, fence)
	for i, s := range segs {
		if i%2 == 1 {
			segs[i] = escapeCode(s)
			continue
		}
		segs[i] = textV2(s)
	}
	return strings.Join(segs, fence)
}

// fence opens and closes a code block.
const fence = "```"

// textV2 converts Markdown outside code blocks. The entities it finds are
// put aside while the rest is escaped, so their own markup stays.
func textV2(s string) string {
	var kept []string
	keep := func(entity string) string {
		kept = append(kept, entity)
		return "\x00" + strconv.Itoa(len(kept)-1) + "\x00"
	}
	s = mdCode.ReplaceAllStringFunc(s, func(m string) string {
		return keep("`" + escapeCode(m[1:len(m)-1]) + "`")
	})
	s = mdLink.ReplaceAllStringFunc(s, func(m string) string {
		g := mdLink.FindStringSubmatch(m)
		return keep("[" + escapeV2(g[1]) + "](" + escapeURL(g[2]) + ")")
	})
	s = mdHeading.ReplaceAllStringFunc(s, func(m string) string {
		// A heading is bold already: stars inside it would close it.
		h := mdBold.ReplaceAllString(mdHeading.FindStringSubmatch(m)[1], "$1")
		return keep("*" + escapeV2(h) + "*")
	})
	s = mdBold.ReplaceAllStringFunc(s, func(m string) string {
		return keep("*" + escapeV2(mdBold.FindStringSubmatch(m)[1]) + "*")
	})
	s = mdItalic.ReplaceAllStringFunc(s, func(m string) string {
		return keep("_" + escapeV2(mdItalic.FindStringSubmatch(m)[1]) + "_")
	})
	s = escapeV2(s)
	// Last first: a later entity (bold) may hold an earlier one (code).
	for i, k := range slices.Backward(kept) {
		s = strings.Replace(s, "\x00"+strconv.Itoa(i)+"\x00", k, 1)
	}
	return s
}

// escapeV2 escapes text for MarkdownV2. The placeholders of textV2 are made
// of NUL and digits, which are not reserved, so they survive it.
func escapeV2(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if strings.ContainsRune(reservedV2, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// escapeCode escapes code: inside it only the backtick and the backslash
// are reserved.
func escapeCode(s string) string {
	return strings.NewReplacer(`\`, `\\`, "`", "\\`").Replace(s)
}

// escapeURL escapes the target of a link: inside it only the closing
// parenthesis and the backslash are reserved. An opening parenthesis is
// escaped too, so the pair stays balanced.
func escapeURL(s string) string {
	return strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`).Replace(s)
}

// PlainText is Markdown with its markup taken out, for when Telegram refuses
// the MarkdownV2 of an answer: a link shows its address after its label.
func PlainText(md string) string {
	s := mdLink.ReplaceAllString(md, "$1 ($2)")
	s = mdHeading.ReplaceAllString(s, "$1")
	s = mdBold.ReplaceAllString(s, "$1")
	s = mdItalic.ReplaceAllString(s, "$1")
	s = mdFence.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, fence, "")
	return strings.ReplaceAll(s, "`", "")
}

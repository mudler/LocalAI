package peg

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The ginkgo DSL is not dot-imported here: its Fail would hide the Fail
// result type of this package.

// untilByCodepoint is UntilParser.parse before the ASCII skip: it decodes
// every codepoint and walks the trie at each one.
func untilByCodepoint(matcher *trie, ctx *ParseContext, start int) ParseResult {
	pos := start
	lastValidPos := start
	for pos < len(ctx.Input) {
		_, size, status := parseUTF8Codepoint(ctx.Input, pos)
		if status == utf8Incomplete {
			if !ctx.IsPartial {
				return NewParseResult(Fail, start)
			}
			return NewParseResultRange(NeedMoreInput, start, lastValidPos)
		}
		if status == utf8Invalid {
			return NewParseResult(Fail, start)
		}
		match := matcher.checkAt(ctx.Input, pos)
		if match == trieCompleteMatch || match == triePartialMatch {
			return NewParseResultRange(Success, start, pos)
		}
		pos += size
		lastValidPos = pos
	}
	if lastValidPos == len(ctx.Input) && ctx.IsPartial {
		return NewParseResultRange(NeedMoreInput, start, lastValidPos)
	}
	return NewParseResultRange(Success, start, lastValidPos)
}

var untilFragments = []string{
	"word", " ", "\n", "<", "</", "</tool_call>", "<tool_call>", "<function=", "</function>", "{", "}",
	"ü", "日本", "🎉", "\xff", "\xc3", "\xe6\x97", "|", "<|tool_call_end|>", "tool", "</par", "</parameter>",
}

var untilDelimiters = [][]string{
	nil,
	{"</tool_call>"},
	{"</function>", "</parameter>"},
	{"<|tool_call_end|>", "<|tool_calls_section_end|>"},
	{"日本"},
	{"ü>", "\n"},
	{""},
}

var _ = ginkgo.Describe("UntilParser", func() {
	ginkgo.It("parses as a decode and trie walk at every codepoint", func() {
		r := rand.New(rand.NewSource(17))
		for run := range 20000 {
			var b strings.Builder
			for range r.Intn(12) {
				b.WriteString(untilFragments[r.Intn(len(untilFragments))])
			}
			input := b.String()
			delims := untilDelimiters[run%len(untilDelimiters)]
			partial := run%2 == 0
			start := 0
			if len(input) > 0 {
				start = r.Intn(len(input) + 1)
			}
			p := &UntilParser{Delimiters: delims}
			got := p.parse(nil, NewParseContext(input, partial), start)
			want := untilByCodepoint(newTrie(delims), NewParseContext(input, partial), start)
			Expect(got).To(Equal(want), fmt.Sprintf("run %d delimiters %q partial %v start %d input %q", run, delims, partial, start, input))
		}
	})
})

// BenchmarkUntilParser scans 1.5 KB of prose for a closing tag, as the tool
// call parsers do for each parse of a streamed output.
func BenchmarkUntilParser(b *testing.B) {
	input := strings.Repeat(" word", 300) + "</tool_call>"
	delims := []string{"</tool_call>"}
	b.Run("codepoint", func(b *testing.B) {
		b.ReportAllocs()
		matcher := newTrie(delims)
		for b.Loop() {
			untilByCodepoint(matcher, NewParseContext(input, false), 0)
		}
	})
	b.Run("ascii-skip", func(b *testing.B) {
		b.ReportAllocs()
		p := &UntilParser{Delimiters: delims}
		for b.Loop() {
			p.parse(nil, NewParseContext(input, false), 0)
		}
	})
}

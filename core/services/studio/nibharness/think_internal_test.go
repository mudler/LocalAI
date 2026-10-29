package nibharness

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// run feeds the chunks and returns the text and the reasoning, each joined.
func run(chunks ...string) (text, reasoning string) {
	var s thinkSplitter
	add := func(ps []piece) {
		for _, p := range ps {
			if p.reasoning {
				reasoning += p.text
			} else {
				text += p.text
			}
		}
	}
	for _, c := range chunks {
		add(s.feed(c))
	}
	add(s.flush())
	return text, reasoning
}

var _ = Describe("think splitter", func() {
	DescribeTable("separates reasoning from the answer",
		func(chunks []string, wantText, wantReasoning string) {
			text, reasoning := run(chunks...)
			Expect(text).To(Equal(wantText))
			Expect(reasoning).To(Equal(wantReasoning))
		},
		Entry("no tags", []string{"hello ", "world"}, "hello world", ""),
		Entry("one chunk", []string{"<think>plan</think>answer"}, "answer", "plan"),
		Entry("tags cut across chunks", []string{"<thi", "nk>pla", "n</th", "ink>ans", "wer"}, "answer", "plan"),
		Entry("text before the tag", []string{"Sure. <think>why</think> Done."}, "Sure.  Done.", "why"),
		Entry("two blocks", []string{"<think>a</think>x<think>b</think>y"}, "xy", "ab"),
		Entry("a lone angle bracket is text", []string{"1 < 2 and <b>bold</b>"}, "1 < 2 and <b>bold</b>", ""),
		Entry("a cut tag that never completes is text", []string{"done <thi"}, "done <thi", ""),
		Entry("an unclosed block stays reasoning", []string{"<think>still thinking"}, "", "still thinking"),
	)
})

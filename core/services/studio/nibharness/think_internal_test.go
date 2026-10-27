package nibharness

import (
	"reflect"
	"testing"
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

func TestThinkSplitterSeparatesReasoningFromTheAnswer(t *testing.T) {
	tests := []struct {
		name            string
		chunks          []string
		wantText, wantR string
	}{
		{"no tags", []string{"hello ", "world"}, "hello world", ""},
		{"one chunk", []string{"<think>plan</think>answer"}, "answer", "plan"},
		{"tags cut across chunks", []string{"<thi", "nk>pla", "n</th", "ink>ans", "wer"}, "answer", "plan"},
		{"text before the tag", []string{"Sure. <think>why</think> Done."}, "Sure.  Done.", "why"},
		{"two blocks", []string{"<think>a</think>x<think>b</think>y"}, "xy", "ab"},
		{"a lone angle bracket is text", []string{"1 < 2 and <b>bold</b>"}, "1 < 2 and <b>bold</b>", ""},
		{"a cut tag that never completes is text", []string{"done <thi"}, "done <thi", ""},
		{"an unclosed block stays reasoning", []string{"<think>still thinking"}, "", "still thinking"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			text, reasoning := run(tc.chunks...)
			if !reflect.DeepEqual([]string{text, reasoning}, []string{tc.wantText, tc.wantR}) {
				t.Fatalf("got text %q reasoning %q, want %q and %q", text, reasoning, tc.wantText, tc.wantR)
			}
		})
	}
}

package reasoning_test

import (
	"fmt"
	"testing"

	. "github.com/mudler/LocalAI/pkg/reasoning"
)

// streamTokens returns n tokens of a reasoning model's output: a thinking
// block over the first half, then the answer.
func streamTokens(n int) []string {
	tokens := make([]string, 0, n)
	tokens = append(tokens, "<think>")
	for i := 1; i < n/2; i++ {
		tokens = append(tokens, fmt.Sprintf(" step%d", i))
	}
	tokens = append(tokens, "</think>")
	for len(tokens) < n {
		tokens = append(tokens, fmt.Sprintf(" word%d", len(tokens)))
	}
	return tokens
}

var benchSizes = []int{1024, 4096, 16384}

// BenchmarkProcessToken streams a whole output through ProcessToken and
// reports the cost per token, which stays flat only when the work per token
// does not grow with the output.
func BenchmarkProcessToken(b *testing.B) {
	for _, n := range benchSizes {
		tokens := streamTokens(n)
		b.Run(fmt.Sprintf("tokens=%d", n), func(b *testing.B) {
			for b.Loop() {
				ext := NewReasoningExtractor("<think>", Config{})
				for _, tok := range tokens {
					ext.ProcessToken(tok)
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*n), "ns/token")
		})
	}
}

// BenchmarkProcessChatDeltaReasoning streams reasoning deltas as the C++
// autoparser delivers them: already stripped of tags.
func BenchmarkProcessChatDeltaReasoning(b *testing.B) {
	for _, n := range benchSizes {
		tokens := streamTokens(n)[1 : n/2]
		b.Run(fmt.Sprintf("tokens=%d", n), func(b *testing.B) {
			for b.Loop() {
				ext := NewReasoningExtractor("<think>", Config{})
				for _, tok := range tokens {
					ext.ProcessChatDeltaReasoning(tok)
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*len(tokens)), "ns/token")
		})
	}
}

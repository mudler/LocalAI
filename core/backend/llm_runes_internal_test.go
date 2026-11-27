package backend

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// assembleRunesByCopy is the token assembly of the stream callback before
// runeAssembler: a new slice for each reply and the reply text appended to the
// whole output with +=.
func assembleRunesByCopy(replies [][]byte) (tokens []string, output string) {
	var partialRune []byte
	for _, msg := range replies {
		partialRune = append(partialRune, msg...)
		var completeRunes []byte
		for len(partialRune) > 0 {
			r, size := utf8.DecodeRune(partialRune)
			if r == utf8.RuneError {
				break
			}
			completeRunes = append(completeRunes, partialRune[:size]...)
			partialRune = partialRune[size:]
		}
		if len(completeRunes) > 0 {
			tokens = append(tokens, string(completeRunes))
			output += string(completeRunes)
		}
	}
	return tokens, output
}

func assembleRunes(replies [][]byte) (tokens []string, output string) {
	var a runeAssembler
	var b strings.Builder
	for _, msg := range replies {
		if token := a.write(msg); token != "" {
			tokens = append(tokens, token)
			b.WriteString(token)
		}
	}
	return tokens, b.String()
}

var _ = Describe("runeAssembler", func() {
	It("passes on the tokens and the output of the old assembly", func() {
		r := rand.New(rand.NewSource(3))
		text := []byte("plain ascii, ü, 日本語, emoji 🎉, \xff bad, U+FFFD �, end")
		for run := range 3000 {
			src := text[r.Intn(len(text)):]
			var replies [][]byte
			for len(src) > 0 {
				n := min(r.Intn(6), len(src))
				replies = append(replies, src[:n])
				src = src[n:]
			}
			wantTokens, wantOutput := assembleRunesByCopy(replies)
			gotTokens, gotOutput := assembleRunes(replies)
			at := fmt.Sprintf("run %d replies %q", run, replies)
			Expect(gotTokens).To(Equal(wantTokens), at)
			Expect(gotOutput).To(Equal(wantOutput), at)
		}
	})
})

func benchmarkReplies(n int) [][]byte {
	replies := make([][]byte, n)
	for i := range replies {
		replies[i] = []byte(" word")
	}
	return replies
}

// BenchmarkStreamTokenAssembly measures the per-token assembly of a stream of
// n replies.
func BenchmarkStreamTokenAssembly(b *testing.B) {
	for _, n := range []int{512, 4096} {
		replies := benchmarkReplies(n)
		b.Run(fmt.Sprintf("copy/tokens=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				assembleRunesByCopy(replies)
			}
		})
		b.Run(fmt.Sprintf("assembler/tokens=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				assembleRunes(replies)
			}
		})
	}
}

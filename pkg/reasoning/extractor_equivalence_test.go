package reasoning

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fragments are the pieces random streams are built from: whole and partial
// tags of every built-in pair, a custom pair, whitespace, multibyte text and a
// rune split across two tokens.
var fragments = []string{
	"<think>", "</think>", "<thi", "nk>", "</th", "ink>", "<", ">", "/",
	"<thinking>", "</thinking>", "[THINK]", "[/THINK]", "[TH", "INK]",
	"<|channel>thought", "<channel|>", "<|channel>", "thought",
	"<|START_THINKING|>", "<|END_THINKING|>", "<seed:think>", "</seed:think>",
	"<|think|>", "<|end|><|begin|>assistant<|content|>", "<|inner_prefix|>", "<|inner_suffix|>",
	"<r>", "</r>",
	" ", "  ", "\n", "\n\n", "\t", "\r\n",
	"hello", " world", "step", " 42", "{\"a\":1}", "ünï", "日本", "　", " ",
	"\xe3\x80", "\x80", // U+3000 split across two tokens
}

var startTokens = []string{"", "<think>", "<|channel>thought", "[THINK]", "<r>", "<thinking>"}

func boolPtr(b bool) *bool { return &b }

func randomConfig(r *rand.Rand) Config {
	var cfg Config
	pick := func() *bool {
		switch r.Intn(3) {
		case 0:
			return nil
		case 1:
			return boolPtr(false)
		}
		return boolPtr(true)
	}
	cfg.DisableReasoning = pick()
	if r.Intn(4) == 0 {
		cfg.DisableReasoning = boolPtr(true)
	} else if cfg.DisableReasoning != nil && *cfg.DisableReasoning && r.Intn(2) == 0 {
		cfg.DisableReasoning = nil
	}
	cfg.DisableReasoningTagPrefill = pick()
	cfg.StripReasoningOnly = pick()
	if r.Intn(2) == 0 {
		cfg.TagPairs = []TagPair{{Start: "<r>", End: "</r>"}}
	}
	return cfg
}

func randomStream(r *rand.Rand, n int) []string {
	tokens := make([]string, n)
	for i := range tokens {
		if r.Intn(10) == 0 {
			tokens[i] = ""
			continue
		}
		var b strings.Builder
		for range 1 + r.Intn(2) {
			b.WriteString(fragments[r.Intn(len(fragments))])
		}
		tokens[i] = b.String()
	}
	return tokens
}

// The incremental extractor must give, at every step, the deltas and totals
// of the extractor that parses the whole output for each token.
var _ = Describe("ReasoningExtractor against the whole-output parse", func() {
	It("matches on random streams of tags, partial tags, whitespace and split runes", func() {
		r := rand.New(rand.NewSource(1))
		for run := range 4000 {
			cfg := randomConfig(r)
			start := startTokens[r.Intn(len(startTokens))]
			tokens := randomStream(r, 1+r.Intn(60))
			got := NewReasoningExtractor(start, cfg)
			want := newLegacyExtractor(start, cfg)
			for i, tok := range tokens {
				where := fmt.Sprintf("run %d start %q config %s tokens %q step %d", run, start, describe(cfg), tokens[:i+1], i)
				if r.Intn(80) == 0 {
					got.ResetAndSuppressReasoning()
					want.ResetAndSuppressReasoning()
				} else if r.Intn(80) == 0 {
					got.Reset()
					want.Reset()
				}

				gr, gc := got.ProcessToken(tok)
				wr, wc := want.ProcessToken(tok)
				Expect([]string{gr, gc}).To(Equal([]string{wr, wc}), "ProcessToken: "+where)
				Expect([]string{got.Reasoning(), got.CleanedContent(), got.Accumulated()}).To(
					Equal([]string{want.Reasoning(), want.CleanedContent(), want.Accumulated()}), "totals: "+where)

				cd := tokens[(i*7+run)%len(tokens)]
				Expect(got.ProcessChatDeltaReasoning(cd)).To(Equal(want.ProcessChatDeltaReasoning(cd)),
					fmt.Sprintf("ProcessChatDeltaReasoning(%q): %s", cd, where))
			}
		}
	})

	It("matches on long streams, where the fast path does nearly all the work", func() {
		r := rand.New(rand.NewSource(2))
		words := []string{" alpha", " beta", "\n", " γ", "  ", "<", "/think", "think>", "</", "<think>", "</think>"}
		for run := range 40 {
			cfg := randomConfig(r)
			start := startTokens[r.Intn(len(startTokens))]
			got := NewReasoningExtractor(start, cfg)
			want := newLegacyExtractor(start, cfg)
			for i := range 1500 {
				tok := words[r.Intn(len(words))]
				if r.Intn(3) != 0 {
					tok = words[r.Intn(4)]
				}
				where := fmt.Sprintf("run %d step %d token %q", run, i, tok)
				gr, gc := got.ProcessToken(tok)
				wr, wc := want.ProcessToken(tok)
				Expect([]string{gr, gc}).To(Equal([]string{wr, wc}), "ProcessToken: "+where)
				Expect(got.ProcessChatDeltaReasoning(tok)).To(Equal(want.ProcessChatDeltaReasoning(tok)), "ProcessChatDeltaReasoning: "+where)
			}
			Expect(got.Reasoning()).To(Equal(want.Reasoning()))
			Expect(got.CleanedContent()).To(Equal(want.CleanedContent()))
		}
	})
})

// FuzzExtractorMatchesLegacy splits an input into tokens at the given cut
// points and compares the two extractors.
func FuzzExtractorMatchesLegacy(f *testing.F) {
	f.Add("<think>a b</think> c", uint64(0x1234), uint8(1))
	f.Add("  <|channel>thought x<channel|>y", uint64(0x9876), uint8(2))
	f.Add("[THINK] q [/THINK]　", uint64(7), uint8(3))
	f.Fuzz(func(t *testing.T, s string, cuts uint64, which uint8) {
		g := NewWithT(t)
		start := startTokens[int(which)%len(startTokens)]
		cfg := Config{}
		if which&8 != 0 {
			cfg.TagPairs = []TagPair{{Start: "<r>", End: "</r>"}}
		}
		got := NewReasoningExtractor(start, cfg)
		want := newLegacyExtractor(start, cfg)
		for len(s) > 0 {
			n := 1 + int(cuts%7)
			cuts = cuts/7 + 3
			if n > len(s) {
				n = len(s)
			}
			tok := s[:n]
			s = s[n:]
			gr, gc := got.ProcessToken(tok)
			wr, wc := want.ProcessToken(tok)
			g.Expect([]string{gr, gc}).To(Equal([]string{wr, wc}), "ProcessToken(%q)", tok)
			g.Expect(got.ProcessChatDeltaReasoning(tok)).To(Equal(want.ProcessChatDeltaReasoning(tok)), "ProcessChatDeltaReasoning(%q)", tok)
		}
	})
}

func describe(cfg Config) string {
	b := func(p *bool) string {
		if p == nil {
			return "nil"
		}
		return fmt.Sprint(*p)
	}
	return fmt.Sprintf("{disable:%s prefill-off:%s strip:%s pairs:%v}", b(cfg.DisableReasoning), b(cfg.DisableReasoningTagPrefill), b(cfg.StripReasoningOnly), cfg.TagPairs)
}

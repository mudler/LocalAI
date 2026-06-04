package router

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/mudler/LocalAI/core/backend"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type stubScorer struct {
	results []backend.CandidateScore
	err     error
	calls   int
	lastP   string
	lastC   []string
}

func (s *stubScorer) Score(_ context.Context, prompt string, candidates []string) ([]backend.CandidateScore, error) {
	s.calls++
	s.lastP = prompt
	s.lastC = append(s.lastC[:0], candidates...)
	if s.err != nil {
		return nil, s.err
	}
	return s.results, nil
}

func testPolicies() []ScorePolicy {
	return []ScorePolicy{
		{Label: "code-generation", Description: "writing, debugging, or explaining code"},
		{Label: "casual-chat", Description: "small talk and general conversation"},
		{Label: "math-reasoning", Description: "arithmetic, equations, word problems"},
	}
}

func sortedLabels(d Decision) []string {
	out := append([]string(nil), d.Labels...)
	sort.Strings(out)
	return out
}

func equalLabels(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

var _ = Describe("ScoreClassifier", func() {
	It("returns a single dominant label", func() {
		// Raw mode: code's joint log-prob is far above the rest, so
		// softmax collapses to ~1.0 on code and the others fall below
		// the activation threshold (default 0.15).
		s := &stubScorer{results: []backend.CandidateScore{
			{LogProb: -0.05, NumTokens: 6},
			{LogProb: -8.0, NumTokens: 6},
			{LogProb: -10.0, NumTokens: 6},
		}}
		c := NewScoreClassifier(testPolicies(), s, ScoreClassifierOptions{})
		d, err := c.Classify(context.Background(), Probe{Prompt: "fix this null pointer"})
		Expect(err).NotTo(HaveOccurred(), "Classify")
		Expect(equalLabels(d.Labels, []string{"code-generation"})).To(BeTrue(), "Labels = %v, want [code-generation]", d.Labels)
		Expect(d.Score).To(BeNumerically(">=", 0.8), "want >= 0.8 for dominant single label")
	})

	It("activates multiple labels", func() {
		// Raw mode two-way tie: code and math share ~0.49 each, chat
		// far behind. Both must activate so the router can pick a
		// candidate covering both capabilities.
		s := &stubScorer{results: []backend.CandidateScore{
			{LogProb: -1.0, NumTokens: 6}, // code  ~0.488
			{LogProb: -4.0, NumTokens: 6}, // chat  ~0.024
			{LogProb: -1.0, NumTokens: 6}, // math  ~0.488
		}}
		c := NewScoreClassifier(testPolicies(), s, ScoreClassifierOptions{})
		d, err := c.Classify(context.Background(), Probe{Prompt: "write code that solves this word problem"})
		Expect(err).NotTo(HaveOccurred(), "Classify")
		got := sortedLabels(d)
		want := []string{"code-generation", "math-reasoning"}
		Expect(equalLabels(got, want)).To(BeTrue(), "Labels = %v, want %v", got, want)
	})

	It("falls back to argmax on flat distribution", func() {
		// All three labels score identically — softmax flat at ~0.333
		// each. Threshold above that forces the fallback path, which
		// must still return one label so the router has something to
		// route on.
		s := &stubScorer{results: []backend.CandidateScore{
			{LogProb: -3.0, NumTokens: 6},
			{LogProb: -3.0, NumTokens: 6},
			{LogProb: -3.0, NumTokens: 6},
		}}
		c := NewScoreClassifier(testPolicies(), s, ScoreClassifierOptions{ActivationThreshold: 0.5})
		d, err := c.Classify(context.Background(), Probe{Prompt: "x"})
		Expect(err).NotTo(HaveOccurred(), "Classify")
		Expect(d.Labels).To(HaveLen(1), "want fallback to argmax (single label)")
	})

	It("mean normalisation rescales by token count", func() {
		// Raw joint log-probs (-8, -15, -6) without normalisation would
		// pick chat — because chat's two tokens give it a smaller
		// summed negative. With mean normalisation the per-token
		// quality wins: code and math are -2.0/token, chat is
		// -7.5/token and falls out.
		s := &stubScorer{results: []backend.CandidateScore{
			{LogProb: -8.0, NumTokens: 4},  // -2.0 per token
			{LogProb: -15.0, NumTokens: 2}, // -7.5 per token
			{LogProb: -6.0, NumTokens: 3},  // -2.0 per token
		}}
		c := NewScoreClassifier(testPolicies(), s, ScoreClassifierOptions{Normalization: ScoreNormalizationMean})
		d, err := c.Classify(context.Background(), Probe{Prompt: "x"})
		Expect(err).NotTo(HaveOccurred(), "Classify")
		got := sortedLabels(d)
		want := []string{"code-generation", "math-reasoning"}
		Expect(equalLabels(got, want)).To(BeTrue(), "Labels = %v, want %v", got, want)
	})

	It("builds Arch-Router system prompt with route-listing and JSON output schema", func() {
		s := &stubScorer{results: []backend.CandidateScore{
			{LogProb: -1, NumTokens: 6},
			{LogProb: -2, NumTokens: 6},
			{LogProb: -3, NumTokens: 6},
		}}
		c := NewScoreClassifier(testPolicies(), s, ScoreClassifierOptions{})
		_, err := c.Classify(context.Background(), Probe{Prompt: "hello world"})
		Expect(err).NotTo(HaveOccurred(), "Classify")
		// System prompt structure
		Expect(s.lastP).To(ContainSubstring("<routes>"))
		Expect(s.lastP).To(ContainSubstring(`{"name": "code-generation", "description": "writing, debugging, or explaining code"}`))
		Expect(s.lastP).To(ContainSubstring("</routes>"))
		Expect(s.lastP).To(ContainSubstring(`{"route": "<name>"}`))
		// ChatML envelope from the fallback renderer
		Expect(s.lastP).To(ContainSubstring("<|im_start|>user\nhello world<|im_end|>"))
		Expect(strings.HasSuffix(s.lastP, "<|im_start|>assistant\n")).To(BeTrue(), "prompt does not end at assistant marker: %q", s.lastP)
		// Candidates: JSON output + turn-end token, in policy order
		Expect(s.lastC).To(Equal([]string{
			`{"route": "code-generation"}<|im_end|>`,
			`{"route": "casual-chat"}<|im_end|>`,
			`{"route": "math-reasoning"}<|im_end|>`,
		}))
	})

	It("honours custom stop token", func() {
		s := &stubScorer{results: []backend.CandidateScore{
			{LogProb: -1, NumTokens: 6},
			{LogProb: -2, NumTokens: 6},
			{LogProb: -3, NumTokens: 6},
		}}
		c := NewScoreClassifier(testPolicies(), s, ScoreClassifierOptions{StopToken: "<|eot_id|>"})
		_, err := c.Classify(context.Background(), Probe{Prompt: "x"})
		Expect(err).NotTo(HaveOccurred(), "Classify")
		Expect(s.lastC[0]).To(Equal(`{"route": "code-generation"}<|eot_id|>`))
	})

	It("honours custom prompt renderer", func() {
		called := false
		renderer := func(system, user string) (string, error) {
			called = true
			return "SYSTEM:" + system + "\nUSER:" + user + "\nASSISTANT:", nil
		}
		s := &stubScorer{results: []backend.CandidateScore{
			{LogProb: -1, NumTokens: 6},
			{LogProb: -2, NumTokens: 6},
			{LogProb: -3, NumTokens: 6},
		}}
		c := NewScoreClassifier(testPolicies(), s, ScoreClassifierOptions{PromptRenderer: renderer})
		_, err := c.Classify(context.Background(), Probe{Prompt: "hi"})
		Expect(err).NotTo(HaveOccurred(), "Classify")
		Expect(called).To(BeTrue(), "renderer must be invoked")
		Expect(s.lastP).To(HavePrefix("SYSTEM:"))
		Expect(s.lastP).To(HaveSuffix("\nASSISTANT:"))
		Expect(s.lastP).To(ContainSubstring("\nUSER:hi\n"))
	})

	It("caches by normalised prompt", func() {
		s := &stubScorer{results: []backend.CandidateScore{
			{LogProb: -0.1, NumTokens: 6},
			{LogProb: -5, NumTokens: 6},
			{LogProb: -6, NumTokens: 6},
		}}
		c := NewScoreClassifier(testPolicies(), s, ScoreClassifierOptions{CacheCap: 64})
		_, err := c.Classify(context.Background(), Probe{Prompt: "Fix Bug"})
		Expect(err).NotTo(HaveOccurred(), "classify 1")
		_, err = c.Classify(context.Background(), Probe{Prompt: " fix bug "})
		Expect(err).NotTo(HaveOccurred(), "classify 2")
		Expect(s.calls).To(Equal(1), "second classify should hit cache")
		Expect(c.CacheLen()).To(Equal(1))
	})

	It("cache disabled when cap zero", func() {
		s := &stubScorer{results: []backend.CandidateScore{
			{LogProb: -1, NumTokens: 6},
			{LogProb: -5, NumTokens: 6},
			{LogProb: -6, NumTokens: 6},
		}}
		c := NewScoreClassifier(testPolicies(), s, ScoreClassifierOptions{})
		for i := 0; i < 3; i++ {
			_, err := c.Classify(context.Background(), Probe{Prompt: "same"})
			Expect(err).NotTo(HaveOccurred(), "classify")
		}
		Expect(s.calls).To(Equal(3), "cache disabled")
	})

	It("propagates scorer error", func() {
		scorerErr := errors.New("boom")
		c := NewScoreClassifier(testPolicies(), &stubScorer{err: scorerErr}, ScoreClassifierOptions{})
		_, err := c.Classify(context.Background(), Probe{Prompt: "x"})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("boom"), "expected scorer error to propagate")
	})

	It("returns result-count mismatch as error", func() {
		s := &stubScorer{results: []backend.CandidateScore{
			{LogProb: -1, NumTokens: 6},
		}}
		c := NewScoreClassifier(testPolicies(), s, ScoreClassifierOptions{})
		_, err := c.Classify(context.Background(), Probe{Prompt: "x"})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("returned 1 results for 3 policies"))
	})

	It("errors when every candidate has zero tokens", func() {
		// Scorer regression: backend forgets to populate NumTokens.
		// Without the guard this would softmax to uniform 1/N and the
		// activation threshold would let every label through, masking
		// the bug as multi-label intent.
		s := &stubScorer{results: []backend.CandidateScore{
			{LogProb: -1, NumTokens: 0},
			{LogProb: -2, NumTokens: 0},
			{LogProb: -3, NumTokens: 0},
		}}
		c := NewScoreClassifier(testPolicies(), s, ScoreClassifierOptions{})
		_, err := c.Classify(context.Background(), Probe{Prompt: "x"})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("zero tokens"))
	})

	It("zero-token candidate scores -inf", func() {
		// A NumTokens=0 candidate must contribute zero softmax mass and
		// never win, even if its raw log-prob looks favourable.
		s := &stubScorer{results: []backend.CandidateScore{
			{LogProb: 100, NumTokens: 0}, // degenerate
			{LogProb: -2, NumTokens: 6},
			{LogProb: -3, NumTokens: 6},
		}}
		c := NewScoreClassifier(testPolicies(), s, ScoreClassifierOptions{})
		d, err := c.Classify(context.Background(), Probe{Prompt: "x"})
		Expect(err).NotTo(HaveOccurred(), "Classify")
		for _, l := range d.Labels {
			Expect(l).NotTo(Equal("code-generation"), "NumTokens=0 label must not be active")
		}
	})

	It("panics on empty policies", func() {
		Expect(func() { NewScoreClassifier(nil, &stubScorer{}, ScoreClassifierOptions{}) }).To(Panic())
	})

	It("panics on nil scorer", func() {
		Expect(func() { NewScoreClassifier(testPolicies(), nil, ScoreClassifierOptions{}) }).To(Panic())
	})

	It("panics on missing description", func() {
		Expect(func() {
			NewScoreClassifier([]ScorePolicy{{Label: "x"}}, &stubScorer{}, ScoreClassifierOptions{})
		}).To(Panic())
	})

	It("panics on unknown normalization", func() {
		Expect(func() {
			NewScoreClassifier(testPolicies(), &stubScorer{}, ScoreClassifierOptions{Normalization: "nonsense"})
		}).To(Panic())
	})

	It("renders a custom SystemPromptTemplate with .Policies", func() {
		// Operator-supplied template with sprig (`join`) — substitutes
		// the built-in Arch-Router-shaped prompt entirely. Asserts
		// that .Policies reaches the template and the rendered output
		// reaches the scorer.
		tmpl := `Pick one of: {{range $i, $p := .Policies}}{{if $i}}, {{end}}{{$p.Label}}{{end}}.
Reply: {"route": "<name>"}`
		s := &stubScorer{results: []backend.CandidateScore{
			{LogProb: -1, NumTokens: 6},
			{LogProb: -2, NumTokens: 6},
			{LogProb: -3, NumTokens: 6},
		}}
		c := NewScoreClassifier(testPolicies(), s, ScoreClassifierOptions{
			SystemPromptTemplate: tmpl,
		})
		_, err := c.Classify(context.Background(), Probe{Prompt: "hi"})
		Expect(err).NotTo(HaveOccurred(), "Classify")
		Expect(s.lastP).To(ContainSubstring("Pick one of: code-generation, casual-chat, math-reasoning."))
		Expect(s.lastP).To(ContainSubstring(`Reply: {"route": "<name>"}`))
		// Built-in default must NOT leak when override is supplied.
		Expect(s.lastP).NotTo(ContainSubstring("<routes>"))
	})

	It("renders a custom template with sprig functions", func() {
		// Sprig's `upper` confirms the funcmap is wired through.
		s := &stubScorer{results: []backend.CandidateScore{
			{LogProb: -1, NumTokens: 6},
			{LogProb: -2, NumTokens: 6},
			{LogProb: -3, NumTokens: 6},
		}}
		c := NewScoreClassifier(testPolicies(), s, ScoreClassifierOptions{
			SystemPromptTemplate: `{{range .Policies}}{{upper .Label}}: {{.Description}}
{{end}}`,
		})
		_, err := c.Classify(context.Background(), Probe{Prompt: "x"})
		Expect(err).NotTo(HaveOccurred(), "Classify")
		Expect(s.lastP).To(ContainSubstring("CODE-GENERATION: writing, debugging, or explaining code"))
	})

	It("panics on a malformed SystemPromptTemplate", func() {
		// Last line of defence: ModelConfig.Validate should catch
		// this earlier, but direct programmatic callers that bypass
		// validation still get a clear failure.
		Expect(func() {
			NewScoreClassifier(testPolicies(), &stubScorer{}, ScoreClassifierOptions{
				SystemPromptTemplate: "{{ broken",
			})
		}).To(Panic())
	})

	It("Name returns the classifier identifier", func() {
		c := NewScoreClassifier(testPolicies(), &stubScorer{}, ScoreClassifierOptions{})
		Expect(c.Name()).To(Equal(ClassifierScore))
	})
})

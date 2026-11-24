package router

import (
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"github.com/mudler/xlog"
)

// pretrimRunesPerToken is deliberately high (most text is 3–5 runes/token,
// tokenisers rarely exceed 6) so the cheap rune pre-trim keeps a superset of
// what fits before any tokenize call.
const pretrimRunesPerToken = 6

// tokenBudgetMargin absorbs BPE-boundary drift and the framing tokens a
// renderer adds, so a prompt measured at exactly the budget still fits n_ctx.
const tokenBudgetMargin = 16

// JoinTurns joins per-turn texts oldest→newest with a trailing newline each.
// The probe builder, the trimmer, and every classifier share this so the text
// a model sees has one canonical shape.
func JoinTurns(turns []string) string {
	var b strings.Builder
	for _, m := range turns {
		b.WriteString(m)
		b.WriteByte('\n')
	}
	return b.String()
}

// promptTrimmer fits an oldest→newest turn list into a token budget for one
// model: optimistic rune pre-trim, tokenize once, then recalibrate with the
// real runes/token and drop whole turns oldest-first until the rendered prompt
// fits. The newest turn is never dropped — if it alone overflows it's sent
// whole and the backend's n_ctx guard is the backstop.
//
// render wraps the joined turns into what the model actually tokenizes: a chat
// template for the scorer, identityRender for an embedder/reranker on raw text.
type promptTrimmer struct {
	tokenize func(string) (int, error)
	render   func(joined string) (string, error)
	budget   int
}

func identityRender(s string) (string, error) { return s, nil }

func (t promptTrimmer) fit(turns []string) string {
	if len(turns) == 0 {
		return ""
	}
	kept := turns[runePretrimStart(turns, t.budget*pretrimRunesPerToken):]

	joined := JoinTurns(kept)
	rendered, err := t.render(joined)
	if err != nil {
		return joined
	}
	total, err := t.tokenize(rendered)
	if err != nil || total <= t.budget {
		return joined
	}

	runesPerToken := float64(utf8.RuneCountInString(rendered)) / float64(total)
	if runesPerToken <= 0 {
		runesPerToken = 1
	}
	est := total
	keep := 0
	for keep < len(kept)-1 && est > t.budget {
		est -= int(math.Ceil(float64(utf8.RuneCountInString(kept[keep])) / runesPerToken))
		keep++
	}

	for {
		tail := JoinTurns(kept[keep:])
		rendered, err := t.render(tail)
		if err != nil {
			return tail
		}
		n, err := t.tokenize(rendered)
		if err != nil || n <= t.budget {
			return tail
		}
		if keep >= len(kept)-1 {
			xlog.Warn("router: newest turn alone exceeds model context; sending it whole — backend n_ctx guard is the backstop",
				"tokens", n, "budget", t.budget)
			return tail
		}
		keep++
	}
}

// runePretrimStart returns the oldest index to keep so the joined tail stays
// within budgetRunes. The newest turn is always kept; older ones are added
// while they fit.
func runePretrimStart(turns []string, budgetRunes int) int {
	if budgetRunes <= 0 || len(turns) == 0 {
		return 0
	}
	start := len(turns) - 1
	total := utf8.RuneCountInString(turns[start])
	for i := len(turns) - 2; i >= 0; i-- {
		r := utf8.RuneCountInString(turns[i])
		if total+r > budgetRunes {
			break
		}
		total += r
		start = i
	}
	return start
}

// lazyBudget computes a model's probe token budget once, on first use, caching
// the result: maxContext minus the longest per-call extra (scorer candidates,
// reranker documents; none for a plain embed) minus tokenBudgetMargin. A
// tokenizer error leaves it uncomputed so a transient failure (model still
// loading) recovers on a later call; extras that already fill the context are
// cached as disabled.
type lazyBudget struct {
	tokenize   func(string) (int, error)
	maxContext int
	extras     []string
	reserve    int

	mu    sync.Mutex
	value atomic.Int64 // 0=unset, >0=budget, -1=disabled
}

func (l *lazyBudget) get() int {
	if l == nil || l.tokenize == nil || l.maxContext <= 0 {
		return 0
	}
	if v := l.value.Load(); v != 0 {
		if v < 0 {
			return 0
		}
		return int(v)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if v := l.value.Load(); v != 0 {
		if v < 0 {
			return 0
		}
		return int(v)
	}
	longest := 0
	for _, e := range l.extras {
		n, err := l.tokenize(e)
		if err != nil {
			return 0 // transient: leave unset so a later call retries
		}
		if n > longest {
			longest = n
		}
	}
	b := l.maxContext - longest - l.reserve - tokenBudgetMargin
	if b <= 0 {
		l.value.Store(-1)
		return 0
	}
	l.value.Store(int64(b))
	return b
}

// trimmedProbeText returns the text to feed a model: the most recent turns
// that fit its token budget, or p.Prompt when trimming is disabled (no
// tokenizer/context wired, or a single-input probe with no Messages).
func trimmedProbeText(p Probe, b *lazyBudget, render func(string) (string, error)) string {
	if len(p.Messages) > 0 {
		if budget := b.get(); budget > 0 {
			return promptTrimmer{tokenize: b.tokenize, render: render, budget: budget}.fit(p.Messages)
		}
	}
	return p.Prompt
}

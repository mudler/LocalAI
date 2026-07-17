package router

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/Masterminds/sprig/v3"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/xlog"
)

// ScorePolicy mirrors config.RouterPolicy at the classifier boundary —
// a label string plus its natural-language description for the
// routing system prompt.
type ScorePolicy struct {
	Label       string
	Description string
}

// defaultActivationThreshold is the softmax-probability floor a policy
// must clear to be considered "active." Picked low enough that two
// reasonably-confident labels (each ~0.4) both activate, high enough
// that a flat distribution doesn't activate everything.
const defaultActivationThreshold = 0.15

// defaultStopToken is the assistant-turn-end marker used when the
// classifier model's stop token isn't configured. ChatML/Qwen
// (Arch-Router's base) uses <|im_end|>; non-ChatML routing models
// should set Router.classifier_model.stopwords.
const defaultStopToken = "<|im_end|>"

// Score normalisation modes. The default mode (raw) feeds joint
// log-probs directly into the softmax — that's the distribution the
// classifier model was trained against. "mean" divides by candidate
// token count, which is fairer to long labels but off-distribution
// for models trained to emit fixed-format outputs.
const (
	ScoreNormalizationRaw  = "raw"
	ScoreNormalizationMean = "mean"
)

// PromptRenderer renders the chat envelope around the routing
// system prompt + user probe. The returned string ends right at the
// assistant-open marker so the scorer's first predicted token is the
// start of the candidate output. Wired from the classifier model's
// chat template by middleware — kept as a callback so the router
// package doesn't depend on core/templates.
type PromptRenderer func(system, user string) (string, error)

// ScoreClassifierOptions groups the optional knobs that have grown
// past the comfortable positional-arg threshold. Zero-value gives
// production defaults (raw normalisation, ChatML stop token, built-in
// ChatML renderer, package activation threshold, 1024-entry cache).
type ScoreClassifierOptions struct {
	// PromptRenderer wraps the routing system + user prompt in the
	// classifier model's chat template. Nil falls back to a built-in
	// ChatML renderer — fine for Arch-Router/Qwen and for tests, but
	// the production wiring should pass through the templates
	// evaluator so non-Qwen routing models render correctly.
	PromptRenderer PromptRenderer

	// StopToken is appended to each candidate before scoring so the
	// model's assistant-turn-end log-prob is included in the joint.
	// That token's probability is the model's explicit "I'm done"
	// signal — including it folds completion-confidence into the
	// candidate score. Empty falls back to ChatML's <|im_end|>.
	StopToken string

	// Normalization picks how candidate joint log-probs feed into
	// softmax. See package consts ScoreNormalizationRaw (default) and
	// ScoreNormalizationMean.
	Normalization string

	// CacheCap bounds the per-prompt memo cache. 0 disables.
	CacheCap int

	// ActivationThreshold is the softmax-probability floor a policy
	// must clear to activate. 0 picks defaultActivationThreshold.
	ActivationThreshold float64

	// SystemPromptTemplate overrides the routing system prompt at
	// construction time. Go text/template + Sprig, executed with
	// `.Policies []ScorePolicy`. Empty falls back to the built-in
	// Arch-Router-shaped template (buildScoreSystemPrompt). The
	// candidate format `{"route": "<label>"}` is NOT templated — an
	// override that instructs the model to emit a different schema
	// would silently desync from what the scorer actually scores.
	SystemPromptTemplate string

	// TokenCounter + MaxContextTokens drive conversation trimming: when
	// both are set, Classify drops the oldest turns until the rendered
	// prompt fits the classifier's context. Nil/0 disables — Classify
	// sends Probe.Prompt as-is and relies on the backend's n_ctx guard.
	TokenCounter     func(string) (int, error)
	MaxContextTokens int

	// CompletionReserveTokens reserves additional context beyond the longest
	// scoring candidate. Classifier slot filling uses this to ensure the prompt
	// scored here can be continued without overflowing the model context.
	CompletionReserveTokens int
}

// ScoreClassifier scores every policy label as the model's actual
// trained output ({"route": "<label>"} + turn-end) under the routing
// prompt, converts log-probabilities into a softmax distribution,
// and returns the set of labels whose probability passes the
// activation threshold.
//
// This is the Arch-Router approach extended for multi-label. The
// classifier model is trained to emit a single policy as a JSON
// route name, but its output distribution still spreads probability
// mass across competing labels when more than one applies. Reading
// the distribution rather than the argmax lets us route conjunctive
// intents ("debug this code AND explain the math") to a candidate
// that can serve both.
type ScoreClassifier struct {
	scorer              backend.Scorer
	activationThreshold float64
	normalization       string
	renderer            PromptRenderer

	// systemPrompt is built once at construction. The same prompt is
	// reused on every classification — only the user-turn body changes.
	systemPrompt string

	// labelOrder mirrors the configured policy ordering — the scorer
	// receives candidates in this order and the softmax distribution
	// indexes back into it.
	labelOrder []string

	// candidates are the pre-built scoring strings — the JSON output
	// Arch-Router was trained to emit, suffixed with the model's
	// turn-end token so its probability folds into the joint
	// log-prob. Built once at construction; same list every call.
	candidates []string

	// budget caps the rendered prompt at the classifier's context minus the
	// longest candidate; nil/disabled sends Probe.Prompt as-is.
	budget *lazyBudget

	cache *labelSetCache

	// stablePrefix is the rendered-prompt prefix shared by every probe:
	// the chat template's preamble plus the option-list system prompt,
	// up to where the per-turn text begins. Computed once (the byte-wise
	// common prefix of two synthetic probes) and sent with each Score
	// call as a state-reuse boundary hint — on backends whose models
	// cannot rewind state (hybrid/recurrent), a snapshot at this
	// boundary is what keeps repeat scoring at probe-size cost instead
	// of a full option-list re-prefill.
	stablePrefixOnce sync.Once
	stablePrefix     string
}

// NewScoreClassifier panics on caller errors at construction (empty
// policies, missing description, nil scorer) — same rationale as the
// other classifiers. See ScoreClassifierOptions for the optional
// knobs and their zero-value defaults.
func NewScoreClassifier(policies []ScorePolicy, scorer backend.Scorer, opts ScoreClassifierOptions) *ScoreClassifier {
	if len(policies) == 0 {
		panic("router/score: at least one policy is required")
	}
	if scorer == nil {
		panic("router/score: scorer is required (configure router.classifier_model)")
	}
	for _, p := range policies {
		if p.Label == "" {
			panic("router/score: policy has empty label")
		}
		if p.Description == "" {
			panic(fmt.Sprintf("router/score: policy %q has no description", p.Label))
		}
	}
	labels := make([]string, 0, len(policies))
	for _, p := range policies {
		labels = append(labels, p.Label)
	}
	if opts.ActivationThreshold <= 0 {
		opts.ActivationThreshold = defaultActivationThreshold
	}
	if opts.StopToken == "" {
		opts.StopToken = defaultStopToken
	}
	if opts.PromptRenderer == nil {
		opts.PromptRenderer = chatMLRenderer
	}
	switch opts.Normalization {
	case "", ScoreNormalizationRaw:
		opts.Normalization = ScoreNormalizationRaw
	case ScoreNormalizationMean:
		// ok
	default:
		panic(fmt.Sprintf("router/score: unknown score_normalization %q (want %q or %q)",
			opts.Normalization, ScoreNormalizationRaw, ScoreNormalizationMean))
	}
	candidates := make([]string, len(labels))
	for i, l := range labels {
		candidates[i] = buildCandidate(l, opts.StopToken)
	}
	systemPrompt, err := renderSystemPrompt(opts.SystemPromptTemplate, policies)
	if err != nil {
		// Parse-time error here means the operator-supplied template
		// is malformed. Config-load validation (ModelConfig.Validate)
		// should have caught it earlier; reaching this is either a
		// direct programmatic construction or a validation gap.
		panic(fmt.Sprintf("router/score: system_prompt_template: %v", err))
	}
	return &ScoreClassifier{
		scorer:              scorer,
		activationThreshold: opts.ActivationThreshold,
		normalization:       opts.Normalization,
		renderer:            opts.PromptRenderer,
		systemPrompt:        systemPrompt,
		labelOrder:          labels,
		candidates:          candidates,
		budget: &lazyBudget{
			tokenize:   opts.TokenCounter,
			maxContext: opts.MaxContextTokens,
			extras:     candidates,
			reserve:    opts.CompletionReserveTokens,
		},
		cache: newLabelSetCache(opts.CacheCap),
	}
}

// renderSystemPrompt returns the routing system prompt for the given
// policies. When tmpl is empty it falls back to the built-in
// Arch-Router-shaped buildScoreSystemPrompt. Otherwise it parses tmpl
// as Go text/template + Sprig and executes against {Policies}.
func renderSystemPrompt(tmpl string, policies []ScorePolicy) (string, error) {
	if tmpl == "" {
		return buildScoreSystemPrompt(policies), nil
	}
	t, err := template.New("router_system_prompt").Funcs(sprig.FuncMap()).Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("parse: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, struct{ Policies []ScorePolicy }{Policies: policies}); err != nil {
		return "", fmt.Errorf("execute: %w", err)
	}
	return buf.String(), nil
}

func (c *ScoreClassifier) Name() string { return ClassifierScore }

// renderProbe returns the exact prompt Classify scores for p (system
// prompt + trimmed user turns through the model's chat template), plus the
// trimmed user text used as the memo-cache key.
func (c *ScoreClassifier) renderProbe(p Probe) (prompt, userText string, err error) {
	// Trim oldest turns until the rendered prompt fits the classifier's
	// context. Cache-keyed on the trimmed text so conversations that
	// trim to the same tail share an entry.
	userText = trimmedProbeText(p, c.budget, func(joined string) (string, error) {
		return c.renderer(c.systemPrompt, joined)
	})
	prompt, err = c.renderer(c.systemPrompt, userText)
	return prompt, userText, err
}

// stablePrefixLen returns the byte length of prompt's leading run that
// is invariant across probes, by rendering two synthetic probes with no
// common text and taking their byte-wise common prefix. Clamped against
// the actual prompt so a template that (unexpectedly) varies its
// preamble degrades to a shorter hint, never a wrong one.
func (c *ScoreClassifier) stablePrefixLen(prompt string) int {
	c.stablePrefixOnce.Do(func() {
		a, aErr := c.renderer(c.systemPrompt, "\x02")
		b, bErr := c.renderer(c.systemPrompt, "\x03")
		if aErr != nil || bErr != nil {
			return
		}
		n := 0
		for n < len(a) && n < len(b) && a[n] == b[n] {
			n++
		}
		c.stablePrefix = a[:n]
	})
	n := 0
	limit := min(len(c.stablePrefix), len(prompt))
	for n < limit && prompt[n] == c.stablePrefix[n] {
		n++
	}
	return n
}

// SlotFillPrompt returns the completion prompt for filling a chosen
// label's argument slots: the identical prompt Classify scored — so the
// backend's prompt cache is already warm with it — continued by the
// label's route JSON re-opened at its first slot field:
//
//	…<prompt>{"route": "up", "distance":
//
// The caller constrains the remaining tokens with a grammar and closes
// the object; keeping the field style byte-identical to the scored
// candidates keeps the continuation on-distribution.
func (c *ScoreClassifier) SlotFillPrompt(p Probe, label, firstSlot string) (string, error) {
	prompt, _, err := c.renderProbe(p)
	if err != nil {
		return "", fmt.Errorf("score slot fill: render prompt: %w", err)
	}
	return prompt + `{"route": "` + escapeJSONString(label) + `", "` + escapeJSONString(firstSlot) + `": `, nil
}

func (c *ScoreClassifier) Classify(ctx context.Context, p Probe) (Decision, error) {
	start := time.Now()

	prompt, userText, err := c.renderProbe(p)
	if err != nil {
		return errDecision(start, fmt.Errorf("score classify: render prompt: %w", err))
	}
	key := cacheKey(userText)
	if hit, ok := c.cache.get(key); ok {
		return Decision{Labels: hit, Score: 1.0, Latency: time.Since(start)}, nil
	}
	results, err := c.scorer.Score(ctx, prompt, c.stablePrefixLen(prompt), c.candidates)
	if err != nil {
		xlog.Warn("router: score classifier failed", "error", err, "labels", c.labelOrder)
		return errDecision(start, fmt.Errorf("score classify: %w", err))
	}
	if len(results) != len(c.labelOrder) {
		return errDecision(start, fmt.Errorf("score classify: scorer returned %d results for %d policies", len(results), len(c.labelOrder)))
	}

	// Convert per-candidate joint log-probs to the softmax inputs the
	// activation threshold reads. Default mode (raw) is on-distribution
	// for Arch-Router: longer candidates score lower for legitimate
	// reasons (the model assigns less probability to outputs that span
	// more tokens). Mean normalisation is available for operators with
	// highly uneven label token counts who'd rather spend the off-
	// distribution cost than the length bias.
	logProbs := make([]float64, len(results))
	nonZero := 0
	for i, r := range results {
		if r.NumTokens == 0 {
			logProbs[i] = math.Inf(-1)
			continue
		}
		nonZero++
		switch c.normalization {
		case ScoreNormalizationMean:
			logProbs[i] = r.LogProb / float64(r.NumTokens)
		default:
			logProbs[i] = r.LogProb
		}
	}
	// All-zero NumTokens means the backend never tokenised any
	// candidate — almost certainly a Scorer regression (forgot to
	// populate NumTokens) rather than a real distribution. Without
	// this check softmax degenerates to uniform 1/N, every label
	// clears the activation threshold, and the router silently treats
	// the bug as "multi-label intent". Fail loud instead so the
	// operator sees the cause.
	if nonZero == 0 {
		return errDecision(start, fmt.Errorf("score classify: backend returned zero tokens for every candidate (scorer regression?)"))
	}
	probs := softmax(logProbs)

	active, bestIdx := selectActive(probs, c.labelOrder, c.activationThreshold)
	c.cache.put(key, active)
	latency := time.Since(start)
	labelScores := NewLabelScores(c.labelOrder, probs)
	xlog.Info("router: score classified",
		"labels", active,
		"top_label", c.labelOrder[bestIdx],
		"top_prob", probs[bestIdx],
		"latency_ms", latency.Milliseconds())
	return Decision{
		Labels:              active,
		Score:               probs[bestIdx],
		Latency:             latency,
		LabelScores:         labelScores,
		ActivationThreshold: c.activationThreshold,
	}, nil
}

// softmax converts an array of log-probabilities into a probability
// distribution. -inf inputs are handled (their exp contributes 0).
// Uses the standard max-subtraction trick for numerical stability.
func softmax(logProbs []float64) []float64 {
	if len(logProbs) == 0 {
		return nil
	}
	maxLP := math.Inf(-1)
	for _, lp := range logProbs {
		if lp > maxLP {
			maxLP = lp
		}
	}
	if math.IsInf(maxLP, -1) {
		// All -inf: return a uniform distribution as a sensible
		// degenerate result.
		out := make([]float64, len(logProbs))
		for i := range out {
			out[i] = 1.0 / float64(len(logProbs))
		}
		return out
	}
	out := make([]float64, len(logProbs))
	sum := 0.0
	for i, lp := range logProbs {
		out[i] = math.Exp(lp - maxLP)
		sum += out[i]
	}
	if sum == 0 {
		// Shouldn't happen given the maxLP check above, but guard
		// against pathological inputs.
		for i := range out {
			out[i] = 1.0 / float64(len(out))
		}
		return out
	}
	for i := range out {
		out[i] /= sum
	}
	return out
}

func (c *ScoreClassifier) CacheLen() int { return c.cache.len() }

// probeTokenBudget returns the token ceiling for the rendered prompt (context
// − longest candidate − margin), computed once via the shared lazyBudget. 0
// means trimming is off (no tokenizer/context) or impossible (candidates fill
// the context).
func (c *ScoreClassifier) probeTokenBudget() int { return c.budget.get() }

// buildScoreSystemPrompt renders the Arch-Router-style routing
// instructions: routes listed in a structured block, output schema
// declared as JSON {"route": "<name>"}. Candidates are scored as
// realisations of that schema (see buildCandidate), so the system
// prompt and candidate format have to agree.
func buildScoreSystemPrompt(policies []ScorePolicy) string {
	var b strings.Builder
	b.WriteString("You are a helpful assistant that selects the best route for the user's request.\n")
	b.WriteString("Routes are listed inside <routes></routes> tags with a name and a short description.\n\n")
	b.WriteString("<routes>\n")
	for _, p := range policies {
		// Both label and description need escaping — YAML accepts
		// any characters in either, and an unescaped quote/backslash
		// breaks the JSON-lines structure the classifier model was
		// trained to parse.
		b.WriteString(`{"name": "`)
		b.WriteString(escapeJSONString(p.Label))
		b.WriteString(`", "description": "`)
		b.WriteString(escapeJSONString(p.Description))
		b.WriteString("\"}\n")
	}
	b.WriteString("</routes>\n\n")
	b.WriteString("Respond with exactly one JSON object of the form ")
	b.WriteString(`{"route": "<name>"}`)
	b.WriteString(", where <name> is one of the route names above.")
	return b.String()
}

// buildCandidate forms the scoring string for one policy label. The
// JSON envelope is what Arch-Router was trained to emit; suffixing
// the model's turn-end token folds completion-confidence into the
// joint log-prob (the model puts more probability on the turn-end
// token when the preceding output feels like a complete answer).
//
// Tokenisation: the gRPC scorer re-tokenises prompt+candidate as one
// string with parse_special=true, so a literal "<|im_end|>" collapses
// to the single special token ID rather than being tokenised as raw
// characters (see backend/cpp/llama-cpp/grpc-server.cpp Score).
func buildCandidate(label, stopToken string) string {
	// Escape so the candidate stays valid JSON when a label
	// (uncommonly) contains a quote or backslash. Keeps the scoring
	// string consistent with the listing in buildScoreSystemPrompt —
	// both describe the same route token-for-token.
	return `{"route": "` + escapeJSONString(label) + `"}` + stopToken
}

// chatMLRenderer is the fallback PromptRenderer used when the caller
// doesn't wire a template-evaluator-backed renderer. ChatML is what
// Arch-Router (Qwen-2.5-1.5B-Instruct) was trained against, so this
// default is safe for the canonical setup. Production wiring should
// still pass a real renderer so a non-ChatML routing model works
// without operators having to know about this fallback.
func chatMLRenderer(system, user string) (string, error) {
	var b strings.Builder
	b.WriteString("<|im_start|>system\n")
	b.WriteString(system)
	b.WriteString("<|im_end|>\n<|im_start|>user\n")
	b.WriteString(user)
	b.WriteString("<|im_end|>\n<|im_start|>assistant\n")
	return b.String(), nil
}

// escapeJSONString does the minimal escaping needed to embed a policy
// description inside a JSON string literal in the route-listing
// system prompt. The list is rendered as ad-hoc JSON-lines (one
// {"name":..,"description":..} per route) rather than via
// encoding/json because the surrounding system prompt is plain text;
// using a real marshaller would force us to encode the whole block
// or wrap each route in a separate call.
func escapeJSONString(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

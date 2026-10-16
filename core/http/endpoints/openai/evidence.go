package openai

import (
	"regexp"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/xlog"
)

var numberToken = regexp.MustCompile(`\b\d+(?:[.,]\d+)?\b`)

// numberKey makes "3,5" and "3.5" the same number, so an answer is not
// flagged for writing a decimal with the other separator than its source.
func numberKey(token string) string {
	return strings.ReplaceAll(token, ",", ".")
}

// evidenceNumbers collects the numbers of the request as received. It is
// taken before the request is compressed or extended with tool results, so
// it holds only what the client and the configuration supplied.
func evidenceNumbers(systemPrompt string, messages []schema.Message) map[string]struct{} {
	numbers := map[string]struct{}{}
	add := func(text string) {
		for _, token := range numberToken.FindAllString(text, -1) {
			numbers[numberKey(token)] = struct{}{}
		}
	}
	add(systemPrompt)
	for _, m := range messages {
		add(messageText(m))
	}
	return numbers
}

// hasUnsupportedNumber reports whether answer contains a number that occurs in
// no input message. A successful server-side tool run counts as evidence for
// the whole answer: its results are in the model's context but never reach the
// client, so they cannot be checked against the request.
func hasUnsupportedNumber(answer string, evidence map[string]struct{}, toolExecuted bool) bool {
	if toolExecuted {
		return false
	}
	for _, token := range numberToken.FindAllString(answer, -1) {
		if _, ok := evidence[numberKey(token)]; !ok {
			return true
		}
	}
	return false
}

// evidenceGate carries the per-request state of a profile's evidence check.
// A nil gate is disabled, so callers need no separate enabled check.
type evidenceGate struct {
	note         string
	evidence     map[string]struct{}
	toolExecuted bool
}

func newEvidenceGate(cfg *config.ModelConfig, messages []schema.Message) *evidenceGate {
	check := cfg.EvidenceCheck()
	if !check.Numbers {
		return nil
	}
	return &evidenceGate{note: check.EvidenceNote(), evidence: evidenceNumbers(cfg.SystemPrompt, messages)}
}

func (g *evidenceGate) markToolExecuted() {
	if g != nil {
		g.toolExecuted = true
	}
}

// suffix returns the text to append to answer, or "" when it needs none.
func (g *evidenceGate) suffix(answer string) string {
	if g == nil || !hasUnsupportedNumber(answer, g.evidence, g.toolExecuted) {
		return ""
	}
	xlog.Debug("Evidence check: answer has numbers without evidence in the request, appending note")
	return "\n\n" + g.note
}

// apply appends the note to every final answer choice. Choices that hand tool
// calls to the client are not final answers and stay untouched.
func (g *evidenceGate) apply(choices []schema.Choice) {
	if g == nil {
		return
	}
	for i := range choices {
		msg := choices[i].Message
		if msg == nil || len(msg.ToolCalls) > 0 || msg.FunctionCall != nil {
			continue
		}
		var content string
		switch v := msg.Content.(type) {
		case string:
			content = v
		case *string:
			if v == nil {
				continue
			}
			content = *v
		default:
			continue
		}
		if s := g.suffix(content); s != "" {
			content += s
			msg.Content = &content
		}
	}
}

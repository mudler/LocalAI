package openai

import (
	"regexp"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/xlog"
)

var numberToken = regexp.MustCompile(`\b\d+(?:[.,]\d+)?\b`)

// urlToken matches an http(s) URL up to the first character that cannot be
// part of one in tool output: whitespace, a quote, a closing bracket of
// Markdown or JSON, or the backslash of a JSON escape.
var urlToken = regexp.MustCompile(`https?://[^\s"'<>()\[\]{}\\]+`)

// maxSourceURLs caps the appended source line; a search tool can return
// dozens of links, and the first ones are the ones it ranked highest.
const maxSourceURLs = 5

// sourceURLs returns the distinct URLs of texts in the order they first
// appear, at most maxSourceURLs. Punctuation that ends a sentence is not part
// of the URL.
func sourceURLs(texts []string) []string {
	var urls []string
	seen := map[string]struct{}{}
	for _, text := range texts {
		for _, token := range urlToken.FindAllString(text, -1) {
			token = strings.TrimRight(token, ".,;:!?")
			if _, ok := seen[token]; ok || strings.HasSuffix(token, "://") {
				continue
			}
			seen[token] = struct{}{}
			urls = append(urls, token)
			if len(urls) == maxSourceURLs {
				return urls
			}
		}
	}
	return urls
}

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
	// evidence is nil when the number check is off.
	note         string
	evidence     map[string]struct{}
	toolExecuted bool

	sources      bool
	sourcesLabel string
	toolResults  []string
}

func newEvidenceGate(cfg *config.ModelConfig, messages []schema.Message) *evidenceGate {
	check := cfg.EvidenceCheck()
	if !check.Numbers && !check.Sources {
		return nil
	}
	g := &evidenceGate{sources: check.Sources, sourcesLabel: check.SourcesHeading()}
	if check.Numbers {
		g.note = check.EvidenceNote()
		g.evidence = evidenceNumbers(cfg.SystemPrompt, messages)
	}
	return g
}

// markToolExecuted records a successful server-side tool call. Its result is
// the only place the source line may take a URL from.
func (g *evidenceGate) markToolExecuted(result string) {
	if g == nil {
		return
	}
	g.toolExecuted = true
	if g.sources {
		g.toolResults = append(g.toolResults, result)
	}
}

// suffix returns the text to append to answer, or "" when it needs none.
func (g *evidenceGate) suffix(answer string) string {
	if g == nil {
		return ""
	}
	var suffix string
	// An answer that links anything is left alone: it cites in its own way,
	// and a second list would contradict or repeat it.
	if g.sources && !urlToken.MatchString(answer) {
		if urls := sourceURLs(g.toolResults); len(urls) > 0 {
			xlog.Debug("Evidence check: answer names no URL, appending the tool results' URLs", "count", len(urls))
			suffix += "\n\n" + g.sourcesLabel + ": " + strings.Join(urls, ", ")
		}
	}
	if g.evidence != nil && hasUnsupportedNumber(answer, g.evidence, g.toolExecuted) {
		xlog.Debug("Evidence check: answer has numbers without evidence in the request, appending note")
		suffix += "\n\n" + g.note
	}
	return suffix
}

// apply appends the suffix to every final answer choice. Choices that hand tool
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

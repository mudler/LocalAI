package openai

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/xlog"
)

var numberToken = regexp.MustCompile(`\b\d+(?:[.,]\d+)?\b`)

// urlToken matches an http(s) URL up to the first character that cannot be
// part of one in tool output. Parentheses are allowed inside URL paths;
// sourceURLs removes unmatched closing parentheses from Markdown links.
var urlToken = regexp.MustCompile(`https?://[^\s"'<>\[\]{}\\]+`)

// maxSourceURLs caps the appended source line; a search tool can return
// dozens of links, and the first ones are the ones it ranked highest.
const maxSourceURLs = 5

// extractURLs returns every http(s) URL of text in order, duplicates
// included. Punctuation that ends a sentence and unmatched closing parentheses
// of Markdown links are not part of the URL.
func extractURLs(text string) []string {
	// The MCP tool call path JSON-encodes multiple TextContent results.
	// Decode that envelope so escaped query separators remain intact.
	var parts []string
	var single string
	if json.Unmarshal([]byte(text), &parts) == nil {
		text = strings.Join(parts, "\n")
	} else if json.Unmarshal([]byte(text), &single) == nil {
		text = single
	}
	// Some JSON encoders escape slashes inside string values.
	text = strings.ReplaceAll(text, `\/`, "/")
	var urls []string
	for _, token := range urlToken.FindAllString(text, -1) {
		token = strings.TrimRight(token, ".,;:!?")
		for strings.HasSuffix(token, ")") && strings.Count(token, ")") > strings.Count(token, "(") {
			token = strings.TrimRight(strings.TrimSuffix(token, ")"), ".,;:!?")
		}
		if strings.HasSuffix(token, "://") {
			continue
		}
		urls = append(urls, token)
	}
	return urls
}

// sourceURLs returns the distinct URLs of texts in the order they first
// appear, at most maxSourceURLs.
func sourceURLs(texts []string) []string {
	var urls []string
	seen := map[string]struct{}{}
	for _, text := range texts {
		for _, token := range extractURLs(text) {
			if _, ok := seen[token]; ok {
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

// urlKey reduces a URL to what decides whether two links name the same page:
// no scheme, no fragment, lower-case host without "www.", no trailing slash.
// The path and query stay case-sensitive.
func urlKey(raw string) string {
	u := raw
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	if i := strings.IndexByte(u, '#'); i >= 0 {
		u = u[:i]
	}
	host, rest := u, ""
	if i := strings.IndexAny(u, "/?"); i >= 0 {
		host, rest = u[:i], u[i:]
	}
	host = strings.TrimPrefix(strings.ToLower(host), "www.")
	path, query, hasQuery := strings.Cut(rest, "?")
	key := host + strings.TrimRight(path, "/")
	if hasQuery && query != "" {
		key += "?" + query
	}
	return key
}

// urlEvidenced reports whether the link key is backed by one of the evidence
// keys: the same page, or a shorter form of a link the evidence holds (the
// site or a parent path of a page that was retrieved). A deeper or different
// path than any evidence is not backed: an invented deep link on a known site
// is exactly what the check is for.
func urlEvidenced(key string, evidence map[string]struct{}) bool {
	if _, ok := evidence[key]; ok {
		return true
	}
	for e := range evidence {
		if strings.HasPrefix(e, key) && len(e) > len(key) && (e[len(key)] == '/' || e[len(key)] == '?') {
			return true
		}
	}
	return false
}

// evidenceURLs collects the link keys of the request as received: the
// profile's system prompt and the system, user and tool messages. Assistant
// messages are left out, so a link the model invented on an earlier turn does
// not become evidence once the client sends it back.
func evidenceURLs(systemPrompt string, messages []schema.Message) map[string]struct{} {
	keys := map[string]struct{}{}
	add := func(text string) {
		for _, u := range extractURLs(text) {
			keys[urlKey(u)] = struct{}{}
		}
	}
	add(systemPrompt)
	for _, m := range messages {
		if m.Role == "assistant" {
			continue
		}
		add(messageText(m))
	}
	return keys
}

// unverifiedURLs returns the distinct links of answer, at most
// maxSourceURLs, that are backed neither by the request nor by toolResults.
func unverifiedURLs(answer string, request map[string]struct{}, toolResults []string) []string {
	links := extractURLs(answer)
	if len(links) == 0 {
		return nil
	}
	evidence := make(map[string]struct{}, len(request))
	for k := range request {
		evidence[k] = struct{}{}
	}
	for _, r := range toolResults {
		for _, u := range extractURLs(r) {
			evidence[urlKey(u)] = struct{}{}
		}
	}
	var out []string
	seen := map[string]struct{}{}
	for _, u := range links {
		key := urlKey(u)
		if _, ok := seen[key]; ok || urlEvidenced(key, evidence) {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, u)
		if len(out) == maxSourceURLs {
			break
		}
	}
	return out
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

	// requestURLs is nil when the link check is off.
	requestURLs map[string]struct{}
	urlsNote    func([]string) string
}

func newEvidenceGate(cfg *config.ModelConfig, messages []schema.Message) *evidenceGate {
	check := cfg.EvidenceCheck()
	if !check.Numbers && !check.Sources && !check.URLs {
		return nil
	}
	g := &evidenceGate{sources: check.Sources, sourcesLabel: check.SourcesHeading()}
	if check.Numbers {
		g.note = check.EvidenceNote()
		g.evidence = evidenceNumbers(cfg.SystemPrompt, messages)
	}
	if check.URLs {
		g.requestURLs = evidenceURLs(cfg.SystemPrompt, messages)
		g.urlsNote = check.UnverifiedURLsNote
	}
	return g
}

// markToolExecuted records a successful server-side tool call. Its result is
// the only place the source line may take a URL from, and evidence for the
// links the answer names.
func (g *evidenceGate) markToolExecuted(result string) {
	if g == nil {
		return
	}
	g.toolExecuted = true
	if g.sources || g.requestURLs != nil {
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
	if g.requestURLs != nil {
		if urls := unverifiedURLs(answer, g.requestURLs, g.toolResults); len(urls) > 0 {
			xlog.Debug("Evidence check: answer names links no tool returned, appending note", "count", len(urls))
			suffix += "\n\n" + g.urlsNote(urls)
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

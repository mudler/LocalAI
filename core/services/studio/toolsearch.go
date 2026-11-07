package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// Tool search: progressive disclosure for the large LocalAI tool groups.
//
// Every tool schema of a chat is sent to the model on every turn. The LocalAI
// client and admin groups together are about nine thousand tokens, which does
// not fit a small context window and costs a lot on a large one. When their
// schemas reach a tenth of the model's window (or the setting chats_tool_search
// says on), the chat sends three small tools in their place: tool_search finds
// a tool by words, tool_describe shows its schema, tool_call runs it.
//
// tool_call runs the very tool the chat would have offered. The deferred tools
// are built by localaiTurn, so the setting, the chat's switches, the role, the
// agent's ceiling and the acting user are already applied, and
// gateBuiltinActs wraps them before the bridge sees them, so the action policy
// of an unattended run applies at each call. The bridge only holds what the
// chat may call: any other name is "no such tool", whether it exists or not.

// Names of the tool-search tools.
const (
	toolSearchName   = "tool_search"
	toolDescribeName = "tool_describe"
	toolCallName     = "tool_call"
)

// toolSearchShare is the share of the context window, in percent, at which the
// auto mode defers the groups.
const toolSearchShare = 10

// Limits of tool_search.
const (
	toolSearchDefault = 5
	toolSearchMax     = 20
	// toolSearchLine is the longest description tool_search shows per tool.
	toolSearchLine = 160
)

// errNoSuchTool is what the bridge answers for a name outside the chat's
// deferred tools, whether the tool exists or the user may not call it.
func errNoSuchTool(name string) error {
	return fmt.Errorf("no such tool %q; use tool_search to find tools", name)
}

// toolSearchMode returns the setting chats_tool_search: auto, on or off.
func (s *Service) toolSearchMode() string {
	if s.d.ToolSearchMode == nil {
		return config.ToolSearchAuto
	}
	switch m := s.d.ToolSearchMode(); m {
	case config.ToolSearchOn, config.ToolSearchOff:
		return m
	}
	return config.ToolSearchAuto
}

// windowOf returns the context window of a chat in tokens: the chat's own
// setting, else the model's configured size, else 0 (unknown).
func (s *Service) windowOf(rec session.ChatRecord) int {
	if n := rec.Settings.ContextSize; n > 0 {
		return n
	}
	if s.d.ContextTokens != nil {
		return s.d.ContextTokens(rec.Model)
	}
	return 0
}

// schemaTokens estimates the tokens that the schemas of tools cost: the bytes
// of the tool JSON a model API receives, divided by four.
func schemaTokens(tools []session.Tool) int {
	bytes := 0
	for _, t := range tools {
		raw, err := json.Marshal(struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		}{t.Name, t.Description, t.Schema})
		if err != nil {
			continue
		}
		bytes += len(raw)
	}
	return (bytes + 3) / 4
}

// shouldDefer decides whether the deferrable tools go behind the tool search.
// The answer depends only on its inputs, so a chat gets the same answer at
// every turn while the tools it is offered and its window stay the same: the
// tool list must not change from turn to turn, or the prompt cache is lost.
//
//   - off: never. on: whenever there is a tool to defer.
//   - auto: when the schemas take at least toolSearchShare percent of the
//     window. An unknown window (0) never defers.
func shouldDefer(mode string, tools []session.Tool, window int) bool {
	if len(tools) == 0 {
		return false
	}
	switch mode {
	case config.ToolSearchOff:
		return false
	case config.ToolSearchOn:
		return true
	}
	if window <= 0 {
		return false
	}
	return schemaTokens(tools)*100 >= window*toolSearchShare
}

// toolSearchTools returns the three bridge tools for the deferred tools of a
// chat. The tools must already be gated: the bridge adds no check of its own,
// it only limits the names to this list. groups says which groups are behind
// the search, for the one-line hint of what can be found.
func toolSearchTools(deferred []session.Tool, groups []string) []session.Tool {
	if len(deferred) == 0 {
		return nil
	}
	byName := make(map[string]session.Tool, len(deferred))
	for _, t := range deferred {
		byName[t.Name] = t
	}
	idx := newToolIndex(deferred)

	hint := ""
	var what []string
	if slices.Contains(groups, builtins.GroupLocalAIClient) {
		what = append(what, "make images, speech, sound, video, embeddings; transcribe; rerank; detect; voices and faces")
	}
	if slices.Contains(groups, builtins.GroupLocalAIAdmin) {
		what = append(what, "manage models, backends, settings and server state")
	}
	if len(what) > 0 {
		hint = " It has tools to " + strings.Join(what, "; ") + "."
	}

	return []session.Tool{
		{
			Name:        toolSearchName,
			Description: "Find LocalAI tools that are not listed in full." + hint + " Give a few words. Then call tool_describe and tool_call.",
			Schema:      json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"limit":{"type":"integer","description":"Default 5, at most 20."}},"required":["query"]}`),
			Run: func(_ context.Context, raw json.RawMessage) (string, error) {
				var in struct {
					Query string `json:"query"`
					Limit int    `json:"limit"`
				}
				if err := json.Unmarshal(raw, &in); err != nil {
					return "", fmt.Errorf("bad arguments: %w", err)
				}
				if strings.TrimSpace(in.Query) == "" {
					return "", errors.New("query is required")
				}
				limit := in.Limit
				if limit <= 0 {
					limit = toolSearchDefault
				}
				limit = min(limit, toolSearchMax)
				hits := idx.search(in.Query, limit)
				if len(hits) == 0 {
					return "No tool matches. Try other words.", nil
				}
				var b strings.Builder
				for _, h := range hits {
					fmt.Fprintf(&b, "%s: %s\n", h.Name, firstSentence(h.Description, toolSearchLine))
				}
				return strings.TrimRight(b.String(), "\n"), nil
			},
		},
		{
			Name:        toolDescribeName,
			Description: "Show the full description and parameters of a tool found with tool_search.",
			Schema:      json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`),
			Run: func(_ context.Context, raw json.RawMessage) (string, error) {
				var in struct {
					Name string `json:"name"`
				}
				if err := json.Unmarshal(raw, &in); err != nil {
					return "", fmt.Errorf("bad arguments: %w", err)
				}
				t, ok := byName[in.Name]
				if !ok {
					return "", errNoSuchTool(in.Name)
				}
				schema := t.Schema
				if len(schema) == 0 {
					schema = json.RawMessage(`{"type":"object"}`)
				}
				out, err := json.Marshal(struct {
					Name        string          `json:"name"`
					Description string          `json:"description"`
					Parameters  json.RawMessage `json:"parameters"`
				}{t.Name, t.Description, schema})
				return string(out), err
			},
		},
		{
			Name:        toolCallName,
			Description: "Run a tool found with tool_search. Give its name and an arguments object that follows its tool_describe parameters.",
			Schema:      json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"arguments":{"type":"object"}},"required":["name"]}`),
			Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
				var in struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				}
				if err := json.Unmarshal(raw, &in); err != nil {
					return "", fmt.Errorf("bad arguments: %w", err)
				}
				t, ok := byName[in.Name]
				if !ok {
					return "", errNoSuchTool(in.Name)
				}
				args, err := callArguments(in.Arguments)
				if err != nil {
					return "", fmt.Errorf("%s: %w", in.Name, err)
				}
				// The tool is the one the chat would have offered: its Run
				// carries the acting user and, for an unattended run, the
				// action policy.
				return t.Run(ctx, args)
			},
		},
	}
}

// callArguments turns the arguments of tool_call into the object the tool
// takes. Absent or null is an empty object. A model sometimes sends the object
// as a JSON string; that is read as the object it holds.
func callArguments(raw json.RawMessage) (json.RawMessage, error) {
	trim := strings.TrimSpace(string(raw))
	switch {
	case trim == "" || trim == "null":
		return json.RawMessage(`{}`), nil
	case strings.HasPrefix(trim, `"`):
		var inner string
		if err := json.Unmarshal(raw, &inner); err != nil {
			return nil, errors.New("arguments must be an object")
		}
		trim = strings.TrimSpace(inner)
		if trim == "" {
			return json.RawMessage(`{}`), nil
		}
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trim), &obj); err != nil {
		return nil, errors.New("arguments must be an object")
	}
	return json.RawMessage(trim), nil
}

// oneLine cuts a description to its first sentence or line, at most n runes.
func firstSentence(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\n"); i >= 0 {
		s = s[:i]
	}
	if i := strings.Index(s, ". "); i >= 0 {
		s = s[:i+1]
	}
	if r := []rune(s); len(r) > n {
		s = string(r[:n-1]) + "…"
	}
	return s
}

// toolIndex is a small BM25 index over the name, the description and the
// parameter names of a list of tools. Nothing else is indexed, and the list is
// the tools the chat may call, so a search cannot name any other tool.
type toolIndex struct {
	docs   []indexedTool
	idf    map[string]float64
	avgLen float64
}

type indexedTool struct {
	name, desc string
	tf         map[string]float64
	length     float64
}

// BM25 constants and the weight of each field: a word in the name says more
// than one in the description.
const (
	bm25K1       = 1.2
	bm25B        = 0.75
	weightName   = 3.0
	weightParams = 2.0
	weightDesc   = 1.0
)

// searchStop are words that say nothing about which tool is meant.
var searchStop = map[string]bool{"a": true, "an": true, "the": true, "to": true, "of": true, "for": true, "and": true,
	"or": true, "in": true, "on": true, "with": true, "is": true, "it": true, "my": true, "me": true, "i": true,
	"from": true, "this": true, "that": true, "be": true, "how": true, "do": true, "can": true, "you": true}

// terms splits text into lower-case words. The underscore splits too, so
// install_model is "install" and "model"; a trailing s is dropped, so
// models and model meet.
func terms(text string) []string {
	f := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	out := f[:0]
	for _, w := range f {
		if len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") {
			w = w[:len(w)-1]
		}
		if len(w) < 2 || searchStop[w] {
			continue
		}
		out = append(out, w)
	}
	return out
}

// paramNames lists the property names of a JSON schema.
func paramNames(schema json.RawMessage) []string {
	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(schema, &s) != nil {
		return nil
	}
	names := make([]string, 0, len(s.Properties))
	for n := range s.Properties {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func newToolIndex(tools []session.Tool) *toolIndex {
	ix := &toolIndex{idf: map[string]float64{}}
	df := map[string]int{}
	var total float64
	for _, t := range tools {
		d := indexedTool{name: t.Name, desc: t.Description, tf: map[string]float64{}}
		add := func(text string, w float64) {
			for _, term := range terms(text) {
				d.tf[term] += w
				d.length += w
			}
		}
		add(t.Name, weightName)
		add(strings.Join(paramNames(t.Schema), " "), weightParams)
		add(t.Description, weightDesc)
		for term := range d.tf {
			df[term]++
		}
		total += d.length
		ix.docs = append(ix.docs, d)
	}
	n := float64(len(ix.docs))
	if n > 0 {
		ix.avgLen = total / n
	}
	for term, c := range df {
		ix.idf[term] = math.Log(1 + (n-float64(c)+0.5)/(float64(c)+0.5))
	}
	return ix
}

// toolHit is one result of a search.
type toolHit struct {
	Name, Description string
	Score             float64
}

// search returns the best tools for the query, best first, at most limit.
// The order is the score, then the name, so equal scores always come out in the
// same order. A tool with no matching word is not a result.
func (ix *toolIndex) search(query string, limit int) []toolHit {
	q := terms(query)
	var hits []toolHit
	for _, d := range ix.docs {
		var score float64
		for _, term := range q {
			tf := d.tf[term]
			if tf == 0 {
				continue
			}
			norm := 1 - bm25B + bm25B*d.length/max(ix.avgLen, 1)
			score += ix.idf[term] * tf * (bm25K1 + 1) / (tf + bm25K1*norm)
		}
		if score > 0 {
			hits = append(hits, toolHit{Name: d.name, Description: d.desc, Score: score})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Name < hits[j].Name
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

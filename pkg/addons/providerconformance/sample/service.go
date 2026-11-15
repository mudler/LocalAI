// Package sample is a small external provider of the notes slot, used as the
// fixture of the conformance test. Its manifest declares every role of the
// slot; Service stands for the web service behind the add-on, in memory, so a
// test needs no network. A provider author reads it as the smallest example of
// the tool contract (docs/content/addons/contract-reference.md, "Write a provider").
package sample

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

//go:embed manifest.json
var Manifest []byte

// Service is the notes of one person, kept in memory.
type Service struct {
	mu    sync.Mutex
	notes map[string][]string // scope -> notes
}

// New returns a service with two notes, as if the person had saved them earlier.
func New() *Service {
	return &Service{notes: map[string][]string{"user": {"likes green tea", "lives in Rome"}}}
}

// Call is the handler of the add-on: one tool name, its JSON arguments, and a
// JSON payload as the answer. A real add-on does the same behind the add-on
// protocol; the host adds the person, which the arguments never carry.
func (s *Service) Call(_ context.Context, tool string, args json.RawMessage) (json.RawMessage, error) {
	var in struct {
		Query       string   `json:"query"`
		Scopes      []string `json:"scopes"`
		BudgetBytes int      `json:"budgetBytes"`
		Text        string   `json:"text"`
		Group       string   `json:"group"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch tool {
	case "notes-context":
		return reply(map[string]any{"text": s.render(in.Scopes, in.BudgetBytes)})
	case "notes-recall":
		var hits []string
		for _, scope := range s.scopes() {
			for _, n := range s.notes[scope] {
				if strings.Contains(strings.ToLower(n), strings.ToLower(in.Query)) {
					hits = append(hits, n)
				}
			}
		}
		return reply(map[string]any{"text": strings.Join(hits, "\n")})
	case "notes-remember":
		if strings.TrimSpace(in.Text) == "" {
			return nil, errors.New("a note needs text")
		}
		s.notes["user"] = append(s.notes["user"], in.Text)
		return reply(map[string]any{"saved": true})
	case "notes-export":
		var files []map[string]string
		for _, scope := range s.scopes() {
			files = append(files, map[string]string{"path": scope + "/notes.md", "content": "- " + strings.Join(s.notes[scope], "\n- ")})
		}
		return reply(map[string]any{"files": files})
	case "notes-forget":
		n := 0
		for scope, list := range s.notes {
			if in.Group == "" || in.Group == scope {
				n += len(list)
				delete(s.notes, scope)
			}
		}
		return reply(map[string]any{"forgotten": n})
	}
	return nil, fmt.Errorf("unknown tool %q", tool)
}

func (s *Service) scopes() []string {
	out := make([]string, 0, len(s.notes))
	for k := range s.notes {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// render is the text for the start of a turn: the notes of the scopes the host
// allows, cut to the budget. A scope that is not listed is not read.
func (s *Service) render(scopes []string, budget int) string {
	var b strings.Builder
	for _, scope := range s.scopes() {
		if len(scopes) > 0 && !contains(scopes, scope) {
			continue
		}
		for _, n := range s.notes[scope] {
			line := "- " + n + "\n"
			if budget > 0 && b.Len()+len(line) > budget {
				return strings.TrimRight(b.String(), "\n")
			}
			b.WriteString(line)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func reply(v any) (json.RawMessage, error) { return json.Marshal(v) }

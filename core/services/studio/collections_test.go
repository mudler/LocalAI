package studio_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// fakeCollections keeps collections per user, as the real backend does.
type fakeCollections struct {
	mine    map[string]map[string][]studio.CollectionHit
	blocked map[string]bool
	asked   []string
}

func (f *fakeCollections) Allowed(_ context.Context, user string) bool { return !f.blocked[user] }

func (f *fakeCollections) List(_ context.Context, user string) ([]string, error) {
	var out []string
	for n := range f.mine[user] {
		out = append(out, n)
	}
	slices.Sort(out)
	return out, nil
}

func (f *fakeCollections) Search(_ context.Context, user, collection, query string, limit int) ([]studio.CollectionHit, error) {
	f.asked = append(f.asked, user+"|"+collection+"|"+query)
	hits, ok := f.mine[user][collection]
	if !ok {
		return nil, studio.ErrNoCollection
	}
	if limit > len(hits) {
		limit = len(hits)
	}
	return hits[:limit], nil
}

func collectionChat(t *testing.T, fc *fakeCollections, script func(workspace.Actor, string) []studiotest.ToolStep) (*studio.Service, *studiotest.ToolHarness) {
	t.Helper()
	return newToolService(t, script, func(d *studio.Deps) { d.Collections = fc })
}

func TestAUserSearchesTheirOwnCollectionsOnly(t *testing.T) {
	fc := &fakeCollections{mine: map[string]map[string][]studio.CollectionHit{
		alice: {"notes": {{Content: "the plan is blue", Similarity: 0.9}}},
		"bob": {"secrets": {{Content: "bob's secret"}}},
	}}
	svc, h := collectionChat(t, fc, func(workspace.Actor, string) []studiotest.ToolStep {
		return []studiotest.ToolStep{
			{Tool: "list_collections", Args: `{}`},
			{Tool: "search_collection", Args: `{"collection":"notes","query":"plan"}`},
			{Tool: "search_collection", Args: `{"collection":"secrets","query":"x"}`},
			{Say: "done"},
		}
	})
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "go")
	idle(t, svc, alice, id)
	list := h.ResultsFor("list_collections")
	if len(list) != 1 || !strings.Contains(list[0].Out, "notes") || strings.Contains(list[0].Out, "secrets") {
		t.Fatalf("list_collections = %+v, want notes only", list)
	}
	found := h.ResultsFor("search_collection")
	if len(found) != 2 || !strings.Contains(found[0].Out, "the plan is blue") {
		t.Fatalf("search results = %+v", found)
	}
	if found[1].Err == nil || found[1].Out != "" || strings.Contains(found[1].Err.Error(), "bob") || !strings.Contains(found[1].Err.Error(), "not found") {
		t.Fatalf("another user's collection answered %q, %v, want a plain not found", found[1].Out, found[1].Err)
	}
	for _, a := range fc.asked {
		if !strings.HasPrefix(a, alice+"|") {
			t.Errorf("the backend was asked as %q", a)
		}
	}
}

func TestACollectionAnswerIsBounded(t *testing.T) {
	var hits []studio.CollectionHit
	for i := 0; i < 100; i++ {
		hits = append(hits, studio.CollectionHit{Content: strings.Repeat("x", 5000)})
	}
	fc := &fakeCollections{mine: map[string]map[string][]studio.CollectionHit{alice: {"big": hits}}}
	svc, h := collectionChat(t, fc, func(workspace.Actor, string) []studiotest.ToolStep {
		return []studiotest.ToolStep{{Tool: "search_collection", Args: `{"collection":"big","query":"q","limit":1000}`}, {Say: "done"}}
	})
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "go")
	idle(t, svc, alice, id)
	out := h.ResultsFor("search_collection")[0].Out
	if len(out) > 20*1024 {
		t.Fatalf("the answer is %d bytes, want at most 20 KiB", len(out))
	}
	var parsed struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil || len(parsed.Results) == 0 || len(parsed.Results) > 20 {
		t.Fatalf("results = %d, err %v, want 1 to 20 valid hits", len(parsed.Results), err)
	}
}

func TestWithoutCollectionsTheToolsAreNotOffered(t *testing.T) {
	plain, hp := newToolService(t, func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: "ok"}} })
	id := create(t, plain, alice)
	sent(t, plain, alice, id, "hi")
	idle(t, plain, alice, id)
	if got := hp.ToolNames(id); slices.Contains(got, "list_collections") || slices.Contains(got, "search_collection") {
		t.Fatalf("tools without the service = %v", got)
	}
	fc := &fakeCollections{blocked: map[string]bool{alice: true}}
	svc, h := collectionChat(t, fc, func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: "ok"}} })
	id = create(t, svc, alice)
	sent(t, svc, alice, id, "hi")
	idle(t, svc, alice, id)
	if got := h.ToolNames(id); slices.Contains(got, "list_collections") {
		t.Fatalf("a user without the feature got %v", got)
	}
}

func TestASubAgentGetsCollectionToolsOnlyWhenNamed(t *testing.T) {
	fc := &fakeCollections{mine: map[string]map[string][]studio.CollectionHit{alice: {"notes": nil}}}
	for _, tc := range []struct {
		args string
		want bool
	}{
		{`{"task":"job","wait":true}`, false},
		{`{"task":"job","wait":true,"tools":["search_collection"]}`, true},
	} {
		svc, h := collectionChat(t, fc, func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "go" {
				return []studiotest.ToolStep{{Tool: "spawn_agent", Args: tc.args}, {Say: "done"}}
			}
			return []studiotest.ToolStep{{Say: "child done"}}
		})
		id := create(t, svc, alice)
		sent(t, svc, alice, id, "go")
		idle(t, svc, alice, id)
		kids := childrenOf(t, svc, alice, id)
		if len(kids) != 1 {
			t.Fatalf("children = %d", len(kids))
		}
		if got := slices.Contains(h.ToolNames(kids[0].ID), "search_collection"); got != tc.want {
			t.Errorf("args %s: sub-agent has search_collection = %v, want %v", tc.args, got, tc.want)
		}
	}
}

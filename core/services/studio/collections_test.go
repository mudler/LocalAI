package studio_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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

func collectionChat(fc *fakeCollections, script func(workspace.Actor, string) []studiotest.ToolStep) (*studio.Service, *studiotest.ToolHarness) {
	return newToolService(script, func(d *studio.Deps) { d.Collections = fc })
}

var _ = Describe("collections", func() {
	It("a user searches their own collections only", func() {
		fc := &fakeCollections{mine: map[string]map[string][]studio.CollectionHit{
			alice: {"notes": {{Content: "the plan is blue", Similarity: 0.9}}},
			"bob": {"secrets": {{Content: "bob's secret"}}},
		}}
		svc, h := collectionChat(fc, func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{
				{Tool: "list_collections", Args: `{}`},
				{Tool: "search_collection", Args: `{"collection":"notes","query":"plan"}`},
				{Tool: "search_collection", Args: `{"collection":"secrets","query":"x"}`},
				{Say: "done"},
			}
		})
		id := create(svc, alice)
		sent(svc, alice, id, "go")
		idle(svc, alice, id)
		list := h.ResultsFor("list_collections")
		Expect(list).To(HaveLen(1), "list_collections = %+v, want notes only", list)
		Expect(list[0].Out).To(ContainSubstring("notes"), "list_collections = %+v, want notes only", list)
		Expect(list[0].Out).NotTo(ContainSubstring("secrets"), "list_collections = %+v, want notes only", list)
		found := h.ResultsFor("search_collection")
		Expect(found).To(HaveLen(2), "search results = %+v", found)
		Expect(found[0].Out).To(ContainSubstring("the plan is blue"), "search results = %+v", found)
		Expect(found[1].Err).NotTo(BeNil(), "another user's collection answered %q, %v, want a plain not found", found[1].Out, found[1].Err)
		Expect(found[1].Out).To(Equal(""), "another user's collection answered %q, %v, want a plain not found", found[1].Out, found[1].Err)
		Expect(found[1].Err.Error()).NotTo(ContainSubstring("bob"), "another user's collection answered %q, %v, want a plain not found", found[1].Out, found[1].Err)
		Expect(found[1].Err.Error()).To(ContainSubstring("not found"), "another user's collection answered %q, %v, want a plain not found", found[1].Out, found[1].Err)
		for _, a := range fc.asked {
			Expect(a).To(HavePrefix(alice+"|"), "the backend was asked as %q", a)
		}
	})

	It("a collection answer is bounded", func() {
		var hits []studio.CollectionHit
		for range 100 {
			hits = append(hits, studio.CollectionHit{Content: strings.Repeat("x", 5000)})
		}
		fc := &fakeCollections{mine: map[string]map[string][]studio.CollectionHit{alice: {"big": hits}}}
		svc, h := collectionChat(fc, func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Tool: "search_collection", Args: `{"collection":"big","query":"q","limit":1000}`}, {Say: "done"}}
		})
		id := create(svc, alice)
		sent(svc, alice, id, "go")
		idle(svc, alice, id)
		out := h.ResultsFor("search_collection")[0].Out
		Expect(len(out)).To(BeNumerically("<=", 20*1024), "the answer is %d bytes, want at most 20 KiB", len(out))
		var parsed struct {
			Results []map[string]any `json:"results"`
		}
		err := json.Unmarshal([]byte(out), &parsed)
		Expect(err).NotTo(HaveOccurred(), "results = %d, err %v, want 1 to 20 valid hits", len(parsed.Results), err)
		Expect(parsed.Results).NotTo(BeEmpty(), "results = %d, err %v, want 1 to 20 valid hits", len(parsed.Results), err)
		Expect(len(parsed.Results)).To(BeNumerically("<=", 20), "results = %d, err %v, want 1 to 20 valid hits", len(parsed.Results), err)
	})

	It("without collections the tools are not offered", func() {
		plain, hp := newToolService(func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: "ok"}} })
		id := create(plain, alice)
		sent(plain, alice, id, "hi")
		idle(plain, alice, id)
		got := hp.ToolNames(id)
		Expect(got).NotTo(ContainElement("list_collections"), "tools without the service = %v", got)
		Expect(got).NotTo(ContainElement("search_collection"), "tools without the service = %v", got)
		fc := &fakeCollections{blocked: map[string]bool{alice: true}}
		svc, h := collectionChat(fc, func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: "ok"}} })
		id = create(svc, alice)
		sent(svc, alice, id, "hi")
		idle(svc, alice, id)
		got = h.ToolNames(id)
		Expect(got).NotTo(ContainElement("list_collections"), "a user without the feature got %v", got)
	})

	It("a sub agent gets collection tools only when named", func() {
		fc := &fakeCollections{mine: map[string]map[string][]studio.CollectionHit{alice: {"notes": nil}}}
		for _, tc := range []struct {
			args string
			want bool
		}{
			{`{"task":"job","wait":true}`, false},
			{`{"task":"job","wait":true,"tools":["search_collection"]}`, true},
		} {
			svc, h := collectionChat(fc, func(_ workspace.Actor, text string) []studiotest.ToolStep {
				if text == "go" {
					return []studiotest.ToolStep{{Tool: "spawn_agent", Args: tc.args}, {Say: "done"}}
				}
				return []studiotest.ToolStep{{Say: "child done"}}
			})
			id := create(svc, alice)
			sent(svc, alice, id, "go")
			idle(svc, alice, id)
			kids := childrenOf(svc, alice, id)
			Expect(kids).To(HaveLen(1), "children = %d", len(kids))
			Expect(slices.Contains(h.ToolNames(kids[0].ID), "search_collection")).To(Equal(tc.want))
		}
	})
})

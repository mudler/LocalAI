// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"context"
	"net/http"

	"github.com/mudler/LocalAI/core/services/studio"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// cappedAddons offers two tools of each of three add-ons, with room for four:
// the cap leaves out the last add-on unless one of the others is excluded.
type cappedAddons struct{ routeAddons }

func (cappedAddons) tools() []studio.AddonTool {
	var out []studio.AddonTool
	for _, id := range []string{"aaa", "bbb", "ccc"} {
		out = append(out, studio.AddonTool{Name: "addon__" + id + "__one", AddonID: id, Tool: "one"}, studio.AddonTool{Name: "addon__" + id + "__two", AddonID: id, Tool: "two"})
	}
	return out
}

func (c cappedAddons) Tools(ctx context.Context, user string) ([]studio.AddonTool, error) {
	t, _, err := c.ToolsScoped(ctx, user, nil)
	return t, err
}

func (c cappedAddons) ToolsScoped(_ context.Context, _ string, excluded func(string) bool) ([]studio.AddonTool, *studio.ToolCap, error) {
	var kept []studio.AddonTool
	cut := 0
	for _, t := range c.tools() {
		switch {
		case excluded != nil && excluded(t.AddonID):
		case len(kept) < 4:
			kept = append(kept, t)
		default:
			cut++
		}
	}
	if cut == 0 {
		return kept, nil, nil
	}
	return kept, &studio.ToolCap{Cap: 4, Offered: len(kept), Cut: cut, Addons: []studio.ToolCapAddon{{ID: "ccc", Title: "Third", Cut: cut}}}, nil
}

var _ = Describe("The tool_cap field of the tools panel", func() {
	var s *studioServer
	BeforeEach(func() {
		s = newStudioServerWith(newTestFiles(), func(d *studio.Deps) { d.AddonTools = cappedAddons{} })
	})

	It("names what the cap left out, for a draft and for a chat", func() {
		draft := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/draft/tools?model=test-model", nil))
		Expect(draft).To(HaveKeyWithValue("tool_cap", map[string]any{
			"limit": 4.0, "offered": 4.0, "total": 6.0, "cut": 2.0,
			"addons": []any{map[string]any{"id": "ccc", "title": "Third", "cut": 2.0}},
		}))

		chat := s.do("alice", http.MethodPost, "/api/chats", map[string]any{"model": "test-model"})
		Expect(chat.Code).To(Equal(http.StatusCreated), chat.Body.String())
		id := decodeJSON(chat)["id"].(string)
		panel := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/"+id+"/tools", nil))
		Expect(panel).To(HaveKey("tool_cap"))
	})

	It("is absent once a switched-off add-on frees room", func() {
		draft := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/draft/tools?model=test-model&addons_off=aaa", nil))
		Expect(draft).NotTo(HaveKey("tool_cap"))

		chat := s.do("alice", http.MethodPost, "/api/chats", map[string]any{"model": "test-model"})
		id := decodeJSON(chat)["id"].(string)
		Expect(s.do("alice", http.MethodPut, "/api/chats/"+id+"/addons", map[string]any{"off": []string{"bbb"}}).Code).To(Equal(http.StatusOK))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/"+id+"/tools", nil))).NotTo(HaveKey("tool_cap"))
		Expect(s.do("alice", http.MethodPut, "/api/chats/"+id+"/addons", map[string]any{"off": []string{}}).Code).To(Equal(http.StatusOK))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/"+id+"/tools", nil))).To(HaveKey("tool_cap"))
	})
})

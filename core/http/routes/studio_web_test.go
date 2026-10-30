// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/pkg/web/search"
)

// routeWeb is the built-in web tools with both tools on and a test search
// that answers or fails as the spec says.
type routeWeb struct{ fail error }

func (routeWeb) Enabled(string) bool { return true }
func (routeWeb) Search(context.Context, string, json.RawMessage) (string, error) {
	return "", nil
}
func (routeWeb) Fetch(context.Context, json.RawMessage) (string, error) { return "", nil }
func (w routeWeb) TestSearch(_ context.Context, _, query string) (string, string, []search.Result, error) {
	if w.fail != nil {
		return "duckduckgo", "duckduckgo.com", nil, w.fail
	}
	return "duckduckgo", "duckduckgo.com", []search.Result{{Title: query, URL: "https://localai.io/"}}, nil
}

var _ = Describe("Chats built-in web tools endpoints", func() {
	var (
		s   *studioServer
		web routeWeb
	)
	JustBeforeEach(func() {
		w := web
		s = newStudioServerWith(newTestFiles(), func(d *studio.Deps) { d.Web = w })
	})

	It("lists the web group in the tools panel and switches it per chat for its owner only", func() {
		chat := s.create("alice")
		panel := s.do("alice", http.MethodGet, "/api/chats/"+chat+"/tools", nil)
		Expect(panel.Code).To(Equal(http.StatusOK), panel.Body.String())
		// The saved agents group comes after web: the route specs keep agents.
		Expect(decodeJSON(panel)["builtins"].([]any)[0]).To(Equal(map[string]any{
			"group": "web", "tools": []any{"web_search", "web_fetch"}, "off": false, "usable": true, "provider": "builtin",
		}))

		put := s.do("alice", http.MethodPut, "/api/chats/"+chat+"/builtins", map[string]any{"off": []string{"web"}})
		Expect(put.Code).To(Equal(http.StatusOK), put.Body.String())
		Expect(decodeJSON(put)["builtins"].([]any)[0].(map[string]any)["off"]).To(BeTrue())

		bad := s.do("alice", http.MethodPut, "/api/chats/"+chat+"/builtins", map[string]any{"off": []string{"bash"}})
		Expect(bad.Code).To(Equal(http.StatusBadRequest), bad.Body.String())
		Expect(s.do("bob", http.MethodPut, "/api/chats/"+chat+"/builtins", map[string]any{"off": []string{"web"}}).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("", http.MethodPut, "/api/chats/"+chat+"/builtins", map[string]any{"off": []string{}}).Code).To(Equal(http.StatusUnauthorized))
	})

	It("runs a test search for an administrator only", func() {
		Expect(s.do("alice", http.MethodPost, "/api/chats/admin/web/test", map[string]any{"query": "x"}).Code).To(Equal(http.StatusForbidden))
		res := s.do("root", http.MethodPost, "/api/chats/admin/web/test", map[string]any{})
		Expect(res.Code).To(Equal(http.StatusOK), res.Body.String())
		body := decodeJSON(res)
		Expect(body["ok"]).To(BeTrue())
		Expect(body["provider"]).To(Equal("duckduckgo"))
		Expect(body["results"]).To(Equal([]any{map[string]any{"title": "LocalAI", "url": "https://localai.io/", "snippet": ""}}))
	})

	Context("when the provider does not work", func() {
		BeforeEach(func() { web = routeWeb{fail: errors.New("no search provider works: blocked")} })

		It("answers the error a user would read", func() {
			res := s.do("root", http.MethodPost, "/api/chats/admin/web/test", map[string]any{"query": "x"})
			Expect(res.Code).To(Equal(http.StatusOK), res.Body.String())
			body := decodeJSON(res)
			Expect(body["ok"]).To(BeFalse())
			Expect(body["error"]).To(Equal("no search provider works: blocked"))
		})
	})
})

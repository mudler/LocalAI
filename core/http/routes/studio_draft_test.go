// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The composer of a new chat lists what the chat could switch before it
// exists and sends the choices with the create request.
var _ = Describe("Chats draft switches", func() {
	var s *studioServer
	BeforeEach(func() { s = newStudioServer() })

	group := func(body map[string]any, name string) map[string]any {
		for _, g := range body["builtins"].([]any) {
			if m := g.(map[string]any); m["group"] == name {
				return m
			}
		}
		Fail("no group " + name)
		return nil
	}

	DescribeTable("require authentication",
		func(path string) {
			Expect(s.do("", http.MethodGet, path, nil).Code).To(Equal(http.StatusUnauthorized))
		},
		Entry("tools", "/api/chats/draft/tools"),
		Entry("add-ons", "/api/chats/draft/addons"),
	)

	It("lists the tools of a new chat with their default switches", func() {
		list := s.do("alice", http.MethodGet, "/api/chats/draft/tools?model=test-model", nil)
		Expect(list.Code).To(Equal(http.StatusOK), list.Body.String())
		Expect(group(decodeJSON(list), "saved_agents")).To(And(HaveKeyWithValue("off", true), HaveKeyWithValue("opt_in", true)))

		addons := s.do("alice", http.MethodGet, "/api/chats/draft/addons?model=test-model", nil)
		Expect(addons.Code).To(Equal(http.StatusOK), addons.Body.String())
		Expect(decodeJSON(addons)).To(HaveKey("commands"))
	})

	It("refuses a model the server does not have and another user's agent", func() {
		Expect(s.do("alice", http.MethodGet, "/api/chats/draft/tools?model=nope", nil).Code).To(Equal(http.StatusBadRequest))
		rec := s.do("alice", http.MethodPost, "/api/chats/agents", map[string]any{"name": "Mine"})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		id := decodeJSON(rec)["id"].(string)
		Expect(s.do("alice", http.MethodGet, "/api/chats/draft/tools?model=test-model&agent_id="+id, nil).Code).To(Equal(http.StatusOK))
		Expect(s.do("bob", http.MethodGet, "/api/chats/draft/tools?model=test-model&agent_id="+id, nil).Code).To(Equal(http.StatusNotFound))
	})

	It("starts a chat with its switches", func() {
		chat := s.do("alice", http.MethodPost, "/api/chats", map[string]any{"model": "test-model", "switches": map[string]any{
			"builtins_on": []string{"saved_agents"}, "skills_off": []string{"builtin:review"},
		}})
		Expect(chat.Code).To(Equal(http.StatusCreated), chat.Body.String())
		id := decodeJSON(chat)["id"].(string)

		list := s.do("alice", http.MethodGet, "/api/chats/"+id+"/tools", nil)
		Expect(group(decodeJSON(list), "saved_agents")).To(HaveKeyWithValue("off", false))
		Expect(s.do("alice", http.MethodGet, "/api/chats/"+id, nil).Code).To(Equal(http.StatusOK))
	})

	It("creates no chat when a switch is bad", func() {
		before := decodeJSON(s.do("alice", http.MethodGet, "/api/chats", nil))["conversations"].([]any)
		bad := s.do("alice", http.MethodPost, "/api/chats", map[string]any{"model": "test-model", "switches": map[string]any{"builtins_on": []string{"nope"}}})
		Expect(bad.Code).To(Equal(http.StatusBadRequest), bad.Body.String())
		after := decodeJSON(s.do("alice", http.MethodGet, "/api/chats", nil))["conversations"].([]any)
		Expect(after).To(HaveLen(len(before)))
	})
})

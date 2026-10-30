// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Chats built-in tool switches", func() {
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

	It("keeps the saved agents off until on names them, and on only takes opt-in groups", func() {
		chat := s.do("alice", http.MethodPost, "/api/chats", map[string]any{"model": "test-model"})
		Expect(chat.Code).To(Equal(http.StatusCreated), chat.Body.String())
		id := decodeJSON(chat)["id"].(string)
		path := "/api/chats/" + id + "/builtins"

		list := s.do("alice", http.MethodGet, "/api/chats/"+id+"/tools", nil)
		Expect(list.Code).To(Equal(http.StatusOK))
		Expect(group(decodeJSON(list), "saved_agents")).To(And(HaveKeyWithValue("off", true), HaveKeyWithValue("opt_in", true), HaveKeyWithValue("usable", true)))

		on := s.do("alice", http.MethodPut, path, map[string]any{"off": []string{}, "on": []string{"saved_agents"}})
		Expect(on.Code).To(Equal(http.StatusOK), on.Body.String())
		Expect(group(decodeJSON(on), "saved_agents")).To(HaveKeyWithValue("off", false))

		// Without on the opt-in groups stay as they are, and one in off is ignored.
		kept := s.do("alice", http.MethodPut, path, map[string]any{"off": []string{"saved_agents"}})
		Expect(kept.Code).To(Equal(http.StatusOK), kept.Body.String())
		Expect(group(decodeJSON(kept), "saved_agents")).To(HaveKeyWithValue("off", false))

		Expect(s.do("alice", http.MethodPut, path, map[string]any{"off": []string{}, "on": []string{"nope"}}).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("bob", http.MethodPut, path, map[string]any{"off": []string{}, "on": []string{"saved_agents"}}).Code).To(Equal(http.StatusNotFound))

		off := s.do("alice", http.MethodPut, path, map[string]any{"off": []string{}, "on": []string{}})
		Expect(group(decodeJSON(off), "saved_agents")).To(HaveKeyWithValue("off", true))
	})
})

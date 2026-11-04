// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Chats agents: the memory choice", func() {
	var s *studioServer
	BeforeEach(func() { s = newStudioServer() })

	create := func(body map[string]any) map[string]any {
		GinkgoHelper()
		rec := s.do("alice", http.MethodPost, "/api/chats/agents", body)
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		return decodeJSON(rec)
	}

	It("stores the choice, shows it, and keeps it when a write does not mention it", func() {
		a := create(map[string]any{"name": "Helper", "memory": "mine_and_own"})
		Expect(a).To(HaveKeyWithValue("memory", "mine_and_own"))
		id := a["id"].(string)
		put := s.do("alice", http.MethodPut, "/api/chats/agents/"+id, map[string]any{"name": "Helper 2"})
		Expect(put.Code).To(Equal(http.StatusOK), put.Body.String())
		Expect(decodeJSON(put)).To(HaveKeyWithValue("memory", "mine_and_own"))
		put = s.do("alice", http.MethodPut, "/api/chats/agents/"+id, map[string]any{"name": "Helper 2", "memory": "none"})
		Expect(decodeJSON(put)).To(HaveKeyWithValue("memory", "none"))
	})

	It("shows no choice for an agent saved without one: as before", func() {
		a := create(map[string]any{"name": "Old"})
		Expect(a).NotTo(HaveKey("memory"))
	})

	It("refuses a choice that does not exist", func() {
		rec := s.do("alice", http.MethodPost, "/api/chats/agents", map[string]any{"name": "Bad", "memory": "everything"})
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("memory"))
	})

	It("exports the choice and imports it into a new agent", func() {
		a := create(map[string]any{"name": "Source", "memory": "own"})
		exp := s.do("alice", http.MethodGet, "/api/chats/agents/"+a["id"].(string)+"/export", nil)
		Expect(exp.Code).To(Equal(http.StatusOK))
		file := decodeJSON(exp)
		Expect(file["agent"]).To(HaveKeyWithValue("memory", "own"))
		imp := s.do("alice", http.MethodPost, "/api/chats/agents/import", file)
		Expect(imp.Code).To(Equal(http.StatusCreated), imp.Body.String())
		got := decodeJSON(imp)["agent"].(map[string]any)
		Expect(got).To(HaveKeyWithValue("memory", "own"))
		Expect(got["id"]).NotTo(Equal(a["id"]))
	})

	It("takes memory=keep, export and delete on delete, and refuses another value without deleting", func() {
		a := create(map[string]any{"name": "Gone", "memory": "own"})
		id := a["id"].(string)
		Expect(s.do("alice", http.MethodDelete, "/api/chats/agents/"+id+"?memory=burn", nil).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("alice", http.MethodGet, "/api/chats/agents/"+id, nil).Code).To(Equal(http.StatusOK))
		for _, mode := range []string{"keep", "export", "delete", ""} {
			b := create(map[string]any{"name": "Gone-" + mode + "-x", "memory": "own"})
			bid := b["id"].(string)
			url := "/api/chats/agents/" + bid
			if mode != "" {
				url += "?memory=" + mode
			}
			// An agent with no notes answers 204 for every mode.
			Expect(s.do("alice", http.MethodDelete, url, nil).Code).To(Equal(http.StatusNoContent), mode)
			Expect(s.do("alice", http.MethodGet, "/api/chats/agents/"+bid, nil).Code).To(Equal(http.StatusNotFound))
		}
	})
})

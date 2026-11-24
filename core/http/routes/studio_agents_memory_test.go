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

	It("stores the save rule, keeps it when a write leaves it out, clears it with use_mine, and refuses auto", func() {
		a := create(map[string]any{"name": "Strict", "memory": "own", "memory_save": "always"})
		Expect(a).To(HaveKeyWithValue("memory_save", "always"))
		id := a["id"].(string)
		put := s.do("alice", http.MethodPut, "/api/chats/agents/"+id, map[string]any{"name": "Strict 2"})
		Expect(put.Code).To(Equal(http.StatusOK), put.Body.String())
		Expect(decodeJSON(put)).To(HaveKeyWithValue("memory_save", "always"), "a client that does not know the field cannot loosen the agent")
		put = s.do("alice", http.MethodPut, "/api/chats/agents/"+id, map[string]any{"name": "Strict 2", "memory_save": "never"})
		Expect(decodeJSON(put)).To(HaveKeyWithValue("memory_save", "never"))
		put = s.do("alice", http.MethodPut, "/api/chats/agents/"+id, map[string]any{"name": "Strict 2", "memory_save": "auto"})
		Expect(decodeJSON(put)).To(HaveKeyWithValue("memory_save", "auto"))
		put = s.do("alice", http.MethodPut, "/api/chats/agents/"+id, map[string]any{"name": "Strict 2", "memory_save": "never"})
		for _, bad := range []string{"ask", "sometimes"} {
			rec := s.do("alice", http.MethodPut, "/api/chats/agents/"+id, map[string]any{"name": "Strict 2", "memory_save": bad})
			Expect(rec.Code).To(Equal(http.StatusBadRequest), bad)
			Expect(rec.Body.String()).To(ContainSubstring("memory_save"))
		}
		put = s.do("alice", http.MethodPut, "/api/chats/agents/"+id, map[string]any{"name": "Strict 2", "memory_save": "use_mine"})
		Expect(put.Code).To(Equal(http.StatusOK), put.Body.String())
		Expect(decodeJSON(put)).NotTo(HaveKey("memory_save"))
	})

	It("shows no save rule for an agent that has none", func() {
		Expect(create(map[string]any{"name": "Plain"})).NotTo(HaveKey("memory_save"))
	})

	It("exports the save rule and imports it into a new agent", func() {
		a := create(map[string]any{"name": "Source", "memory": "own", "memory_save": "never"})
		exp := s.do("alice", http.MethodGet, "/api/chats/agents/"+a["id"].(string)+"/export", nil)
		Expect(exp.Code).To(Equal(http.StatusOK))
		file := decodeJSON(exp)
		Expect(file["agent"]).To(HaveKeyWithValue("memory_save", "never"))
		imp := s.do("alice", http.MethodPost, "/api/chats/agents/import", file)
		Expect(imp.Code).To(Equal(http.StatusCreated), imp.Body.String())
		Expect(decodeJSON(imp)["agent"]).To(HaveKeyWithValue("memory_save", "never"))
	})

	It("exports and imports Save it and tell me, and refuses a value that is not one", func() {
		a := create(map[string]any{"name": "Quick", "memory": "own", "memory_save": "auto"})
		Expect(a).To(HaveKeyWithValue("memory_save", "auto"))
		exp := s.do("alice", http.MethodGet, "/api/chats/agents/"+a["id"].(string)+"/export", nil)
		file := decodeJSON(exp)
		Expect(file["agent"]).To(HaveKeyWithValue("memory_save", "auto"))
		imp := s.do("alice", http.MethodPost, "/api/chats/agents/import", file)
		Expect(imp.Code).To(Equal(http.StatusCreated), imp.Body.String())
		Expect(decodeJSON(imp)["agent"]).To(HaveKeyWithValue("memory_save", "auto"))
		file["agent"].(map[string]any)["memory_save"] = "ask"
		bad := s.do("alice", http.MethodPost, "/api/chats/agents/import", file)
		Expect(bad.Code).To(Equal(http.StatusBadRequest), bad.Body.String())
	})
})

var _ = Describe("Chats memory settings: the levels and the messaging row", func() {
	var s *studioServer
	BeforeEach(func() { s = newStudioServer() })

	It("reads ask and ask by default and stores Always confirm and the messaging row", func() {
		got := s.do("alice", http.MethodGet, "/api/chats/memory", nil)
		Expect(got.Code).To(Equal(http.StatusOK), got.Body.String())
		Expect(decodeJSON(got)).To(And(HaveKeyWithValue("save_mode", "ask"), HaveKeyWithValue("messaging_save", "ask")))
		put := s.do("alice", http.MethodPut, "/api/chats/memory", map[string]any{"save_mode": "always", "messaging_save": "paired_private"})
		if put.Code == http.StatusServiceUnavailable {
			Skip("this server keeps no per-person settings")
		}
		Expect(put.Code).To(Equal(http.StatusOK), put.Body.String())
		Expect(decodeJSON(put)).To(And(HaveKeyWithValue("save_mode", "always"), HaveKeyWithValue("messaging_save", "paired_private")))
		put = s.do("alice", http.MethodPut, "/api/chats/memory", map[string]any{"save_mode": "never"})
		Expect(decodeJSON(put)).To(And(HaveKeyWithValue("save_mode", "never"), HaveKeyWithValue("messaging_save", "paired_private")))
	})

	It("refuses a value that is not one", func() {
		for _, body := range []map[string]any{{"save_mode": "sometimes"}, {"messaging_save": "everyone"}, {"messaging_save": "auto"}} {
			rec := s.do("alice", http.MethodPut, "/api/chats/memory", body)
			if rec.Code == http.StatusServiceUnavailable {
				Skip("this server keeps no per-person settings")
			}
			Expect(rec.Code).To(Equal(http.StatusBadRequest), rec.Body.String())
		}
	})
})

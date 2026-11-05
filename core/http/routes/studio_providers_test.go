// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Chats provider slots", func() {
	var s *studioServer
	BeforeEach(func() { s = newStudioServer() })

	It("lists the memory slot with the built-in provider and Off", func() {
		rec := s.do("alice", http.MethodGet, "/api/chats/providers", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		slots := decodeJSON(rec)["slots"].([]any)
		Expect(slots).NotTo(BeEmpty())
		var memory map[string]any
		for _, raw := range slots {
			if slot := raw.(map[string]any); slot["id"] == "memory" {
				memory = slot
			}
		}
		Expect(memory).NotTo(BeNil())
		Expect(memory["resolution"]).To(HaveKeyWithValue("active", "builtin"))
		var ids []any
		for _, p := range memory["providers"].([]any) {
			ids = append(ids, p.(map[string]any)["id"])
		}
		Expect(ids).To(Equal([]any{"builtin", "off"}))
	})

	It("reads one slot and answers 404 for another", func() {
		Expect(s.do("alice", http.MethodGet, "/api/chats/providers/memory", nil).Code).To(Equal(http.StatusOK))
		Expect(s.do("alice", http.MethodGet, "/api/chats/providers/calendar", nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("alice", http.MethodPut, "/api/chats/providers/calendar", map[string]any{"provider": "off"}).Code).To(Equal(http.StatusNotFound))
	})

	It("picks Off and the built-in provider, and refuses a provider the slot does not list", func() {
		rec := s.do("alice", http.MethodPut, "/api/chats/providers/memory", map[string]any{"provider": "off"})
		if rec.Code == http.StatusServiceUnavailable {
			Skip("this server keeps no per-person settings")
		}
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["resolution"]).To(And(HaveKeyWithValue("active", "off"), HaveKeyWithValue("off", true)))
		mem := s.do("alice", http.MethodGet, "/api/chats/memory", nil)
		Expect(decodeJSON(mem)).To(And(HaveKeyWithValue("enabled", false), HaveKeyWithValue("provider", "off")))

		rec = s.do("alice", http.MethodPut, "/api/chats/providers/memory", map[string]any{"provider": "builtin"})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["resolution"]).To(HaveKeyWithValue("active", "builtin"))

		rec = s.do("alice", http.MethodPut, "/api/chats/providers/memory", map[string]any{"provider": "no-such-addon"})
		Expect(rec.Code).To(Equal(http.StatusBadRequest), rec.Body.String())
	})
})

// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Renaming a chat", func() {
	var s *studioServer

	BeforeEach(func() { s = newStudioServer() })

	It("needs authentication", func() {
		Expect(s.do("", http.MethodPatch, "/api/chats/c1", map[string]any{"title": "x"}).Code).To(Equal(http.StatusUnauthorized))
	})

	It("renames the owner's chat and lists the new title", func() {
		id := s.create("alice")
		rec := s.do("alice", http.MethodPatch, "/api/chats/"+id, map[string]any{"title": "  Trip plans  "})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["title"]).To(Equal("Trip plans"))
		Expect(s.detail("alice", id)["title"]).To(Equal("Trip plans"))
	})

	It("refuses an empty title", func() {
		id := s.create("alice")
		Expect(s.do("alice", http.MethodPatch, "/api/chats/"+id, map[string]any{"title": "   "}).Code).To(Equal(http.StatusBadRequest))
	})

	It("does not let another user rename the chat", func() {
		id := s.create("alice")
		Expect(s.do("bob", http.MethodPatch, "/api/chats/"+id, map[string]any{"title": "mine"}).Code).To(Equal(http.StatusNotFound))
		Expect(s.detail("alice", id)["title"]).NotTo(Equal("mine"))
	})

	It("refuses while a turn runs", func() {
		id := s.create("alice")
		s.do("alice", http.MethodPost, "/api/chats/"+id+"/messages", map[string]any{"text": "start"})
		Eventually(func() int { return len(s.harness.Sent()) }, 10*time.Second).Should(Equal(1))
		Expect(s.do("alice", http.MethodPatch, "/api/chats/"+id, map[string]any{"title": "busy"}).Code).To(Equal(http.StatusConflict))
		s.harness.Gate <- struct{}{}
		Eventually(func() bool { return s.detail("alice", id)["running"].(bool) }, 10*time.Second).Should(BeFalse())
		Expect(s.do("alice", http.MethodPatch, "/api/chats/"+id, map[string]any{"title": "idle"}).Code).To(Equal(http.StatusOK))
	})
})

// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Chat loops endpoints", func() {
	var s *studioServer

	BeforeEach(func() { s = newStudioServer() })

	loopsOf := func(user, chat string) []any {
		rec := s.do(user, http.MethodGet, "/api/chats/"+chat+"/loops", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		return decodeJSON(rec)["loops"].([]any)
	}

	It("starts a loop that waits for a yes, confirms, pauses and stops it", func() {
		chat := s.create("alice")
		rec := s.do("alice", http.MethodPost, "/api/chats/"+chat+"/loops", map[string]any{"interval": "5m", "prompt": "Check the build."})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		l := decodeJSON(rec)
		Expect(l).To(HaveKeyWithValue("state", "pending"))
		Expect(l).To(HaveKeyWithValue("interval", "5m"))
		Expect(l).To(HaveKeyWithValue("interval_seconds", BeNumerically("==", 300)))
		id := l["id"].(string)

		detail := s.detail("alice", chat)
		Expect(detail["loops"]).To(HaveLen(1), "the chat view carries the loops")

		rec = s.do("alice", http.MethodPut, "/api/chats/"+chat+"/loops/"+id, map[string]any{"state": "active"})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("state", "active"))
		rec = s.do("alice", http.MethodPut, "/api/chats/"+chat+"/loops/"+id, map[string]any{"state": "paused"})
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("state", "paused"))
		Expect(s.do("alice", http.MethodPut, "/api/chats/"+chat+"/loops/"+id, map[string]any{"state": "ended"}).Code).To(Equal(http.StatusBadRequest))

		// The second loop of the chat runs without asking again.
		rec = s.do("alice", http.MethodPost, "/api/chats/"+chat+"/loops", map[string]any{"prompt": "Watch the queue."})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("state", "active"))
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("interval_seconds", BeNumerically("==", 0)))

		rec = s.do("alice", http.MethodDelete, "/api/chats/"+chat+"/loops/"+id, nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(loopsOf("alice", chat)).To(HaveLen(1))
		rec = s.do("alice", http.MethodDelete, "/api/chats/"+chat+"/loops", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("stopped", BeNumerically("==", 1)))
		Expect(loopsOf("alice", chat)).To(BeEmpty())
	})

	It("refuses a bad interval, an empty prompt and a command that is not a skill", func() {
		chat := s.create("alice")
		for _, body := range []map[string]any{
			{"interval": "10s", "prompt": "x"},
			{"interval": "soon", "prompt": "x"},
			{"interval": "5m", "prompt": "  "},
			{"interval": "5m", "prompt": "/goal ship it"},
		} {
			rec := s.do("alice", http.MethodPost, "/api/chats/"+chat+"/loops", body)
			Expect(rec.Code).To(Equal(http.StatusBadRequest), "%v: %s", body, rec.Body.String())
		}
		Expect(s.do("alice", http.MethodPost, "/api/chats/"+chat+"/loops", "not json").Code).To(Equal(http.StatusBadRequest))
	})

	It("keeps at most three loops in a chat", func() {
		chat := s.create("alice")
		for range 3 {
			Expect(s.do("alice", http.MethodPost, "/api/chats/"+chat+"/loops", map[string]any{"interval": "5m", "prompt": "x"}).Code).To(Equal(http.StatusCreated))
		}
		rec := s.do("alice", http.MethodPost, "/api/chats/"+chat+"/loops", map[string]any{"interval": "5m", "prompt": "x"})
		Expect(rec.Code).To(Equal(http.StatusConflict), rec.Body.String())
	})

	It("keeps another user's chat and loops out of reach", func() {
		chat := s.create("alice")
		rec := s.do("alice", http.MethodPost, "/api/chats/"+chat+"/loops", map[string]any{"interval": "5m", "prompt": "x"})
		id := decodeJSON(rec)["id"].(string)
		Expect(s.do("bob", http.MethodGet, "/api/chats/"+chat+"/loops", nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("bob", http.MethodPost, "/api/chats/"+chat+"/loops", map[string]any{"prompt": "x"}).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("bob", http.MethodDelete, "/api/chats/"+chat+"/loops/"+id, nil).Code).To(Equal(http.StatusNotFound))
		bobChat := s.create("bob")
		Expect(s.do("bob", http.MethodPut, "/api/chats/"+bobChat+"/loops/"+id, map[string]any{"state": "paused"}).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("bob", http.MethodDelete, "/api/chats/"+bobChat+"/loops/"+id, nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("carol", http.MethodGet, "/api/chats/"+chat+"/loops", nil).Code).To(Equal(http.StatusForbidden))
		Expect(loopsOf("alice", chat)).To(HaveLen(1))
	})
})

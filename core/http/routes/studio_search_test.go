// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"context"
	"net/http"
	"net/url"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/session"
)

var _ = Describe("Chats search endpoint", func() {
	var s *studioServer

	BeforeEach(func() { s = newStudioServer() })

	say := func(id, role, text string, at time.Time) {
		_, err := s.store.AppendEntry(context.Background(), id, session.Entry{Role: role, Text: text, At: at})
		Expect(err).NotTo(HaveOccurred())
	}
	search := func(user string, params url.Values) map[string]any {
		rec := s.do(user, http.MethodGet, "/api/chats/search?"+params.Encode(), nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		return decodeJSON(rec)
	}
	hits := func(body map[string]any) []map[string]any {
		var out []map[string]any
		for _, h := range body["hits"].([]any) {
			out = append(out, h.(map[string]any))
		}
		return out
	}

	It("returns the old fields and the new ones, with highlights", func() {
		id := s.create("alice")
		say(id, session.RoleUser, "What is the Qdrant port?", time.Now().Add(-time.Hour))
		body := search("alice", url.Values{"q": {"qdrant"}})
		Expect(body).To(HaveKey("engine"))
		h := hits(body)
		Expect(h).To(HaveLen(1))
		Expect(h[0]["chat"]).To(HaveKeyWithValue("id", id))
		Expect(h[0]).To(HaveKeyWithValue("seq", BeNumerically("==", 1)))
		Expect(h[0]).To(HaveKeyWithValue("role", "user"))
		Expect(h[0]).To(HaveKeyWithValue("snippet", "What is the Qdrant port?"))
		Expect(h[0]).To(HaveKey("at"))
		Expect(h[0]["highlights"]).To(Equal([]any{map[string]any{"start": float64(12), "end": float64(18)}}))
		Expect(h[0]).To(HaveKeyWithValue("in_title", false))
	})

	It("never returns the chats of another user", func() {
		a, b := s.create("alice"), s.create("bob")
		say(a, session.RoleUser, "zebra stripes", time.Now())
		say(b, session.RoleUser, "zebra stripes and secrets", time.Now())
		Expect(hits(search("alice", url.Values{"q": {"zebra"}}))).To(HaveLen(1))
		Expect(hits(search("alice", url.Values{"q": {"secrets"}}))).To(BeEmpty())
		Expect(hits(search("bob", url.Values{"q": {"secrets"}}))).To(HaveLen(1))
	})

	It("groups by chat unless asked for messages, and filters by role and date", func() {
		id := s.create("alice")
		old := time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC)
		say(id, session.RoleUser, "ocelot question", old)
		say(id, session.RoleAssistant, "ocelot answer", old.Add(48*time.Hour))
		Expect(hits(search("alice", url.Values{"q": {"ocelot"}}))).To(HaveLen(1))
		Expect(hits(search("alice", url.Values{"q": {"ocelot"}, "group": {"message"}}))).To(HaveLen(2))
		Expect(hits(search("alice", url.Values{"q": {"ocelot"}, "group": {"message"}, "role": {"assistant"}}))).To(HaveLen(1))
		Expect(hits(search("alice", url.Values{"q": {"ocelot"}, "group": {"message"}, "since": {"2026-01-06"}}))).To(HaveLen(1))
		// until is inclusive of the whole day.
		Expect(hits(search("alice", url.Values{"q": {"ocelot"}, "group": {"message"}, "until": {"2026-01-05"}}))).To(HaveLen(1))
		Expect(hits(search("alice", url.Values{"q": {"ocelot"}, "group": {"message"}, "since": {"2026-01-05T09:00:00Z"}, "until": {"2026-01-07"}}))).To(HaveLen(2))
	})

	It("refuses a bad parameter", func() {
		for _, p := range []url.Values{{"q": {"x"}, "role": {"tool"}}, {"q": {"x"}, "origin": {"nope"}}, {"q": {"x"}, "since": {"yesterday"}}, {"q": {"x"}, "group": {"all"}}, {}} {
			Expect(s.do("alice", http.MethodGet, "/api/chats/search?"+p.Encode(), nil).Code).To(Equal(http.StatusBadRequest), p.Encode())
		}
	})

	It("is refused to a user whose chats feature was revoked", func() {
		Expect(s.do("carol", http.MethodGet, "/api/chats/search?q=x", nil).Code).To(Equal(http.StatusForbidden))
		Expect(s.do("", http.MethodGet, "/api/chats/search?q=x", nil).Code).To(Equal(http.StatusUnauthorized))
	})
})

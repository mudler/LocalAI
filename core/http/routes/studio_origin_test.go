// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/session"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Chats list origin filter", func() {
	var s *studioServer
	seed := func(user, id, origin, originID string, age int) {
		now := time.Now().Add(time.Duration(age) * time.Minute)
		Expect(s.store.CreateChat(context.Background(), session.ChatRecord{
			ID: id, Principal: user, Kind: session.KindChat, Title: id, Origin: origin, OriginID: originID, Created: now, Updated: now,
		})).To(Succeed())
	}
	ids := func(user, query string) []string {
		rec := s.do(user, http.MethodGet, "/api/chats?"+query, nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		var out []string
		for _, c := range decodeJSON(rec)["conversations"].([]any) {
			out = append(out, c.(map[string]any)["id"].(string))
		}
		return out
	}

	BeforeEach(func() {
		s = newStudioServer()
		// Oldest first: the person's chats are older than the bot's.
		seed("alice", "mine-1", "", "", 1)
		seed("alice", "mine-2", "", "", 2)
		for i := range 5 {
			seed("alice", fmt.Sprintf("bot-%d", i), session.OriginConnector, "binding-1", 10+i)
		}
		seed("alice", "cron-1", session.OriginTrigger, "trigger-1", 20)
		seed("bob", "bob-bot", session.OriginConnector, "binding-2", 30)
	})

	It("returns every chat when origin is absent, as old clients expect", func() {
		Expect(ids("alice", "kind=chat")).To(HaveLen(8))
	})

	It("person leaves out chats that started by themselves, and the limit counts the chats kept", func() {
		// Seven newer automated chats must not push the two person chats off a page of 2.
		Expect(ids("alice", "kind=chat&origin=person&limit=2")).To(Equal([]string{"mine-2", "mine-1"}))
	})

	It("automated returns the others and a kind returns only that kind", func() {
		Expect(ids("alice", "origin=automated")).To(HaveLen(6))
		Expect(ids("alice", "origin=trigger")).To(Equal([]string{"cron-1"}))
		Expect(ids("alice", "origin=connector&limit=3")).To(HaveLen(3))
		Expect(ids("alice", "origin=job")).To(BeEmpty())
	})

	It("never shows another user's chats", func() {
		Expect(ids("alice", "origin=automated")).NotTo(ContainElement("bob-bot"))
	})
})

// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/session"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Chats goal endpoints", func() {
	var s *studioServer

	BeforeEach(func() {
		s = newStudioServerWith(newTestFiles(), func(d *studio.Deps) {
			d.GoalLimits = func() (int, int) { return 3, 50 }
		})
	})

	goal := func(user, id string) map[string]any {
		rec := s.do(user, http.MethodGet, "/api/chats/"+id+"/goal", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		g, _ := decodeJSON(rec)["goal"].(map[string]any)
		return g
	}

	It("sets, reads, pauses with Stop, resumes and clears a goal, and the chat and the transcript carry it", func() {
		id := s.create("alice")
		Expect(goal("alice", id)).To(BeNil())

		rec := s.do("alice", http.MethodPut, "/api/chats/"+id+"/goal", map[string]any{"text": "write the summary"})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		body := decodeJSON(rec)
		Expect(body["goal"]).To(HaveKeyWithValue("text", "write the summary"))
		Expect(body["goal"]).To(HaveKeyWithValue("state", "active"))
		Expect(body["goal"]).To(HaveKeyWithValue("max_turns", BeNumerically("==", 3)))
		Expect(body["goal"]).To(HaveKeyWithValue("max_steps", BeNumerically("==", 50)))
		Expect(body).NotTo(HaveKey("replaced"))
		Eventually(func() bool { return s.detail("alice", id)["running"] == true }, 5*time.Second).Should(BeTrue(), "the goal starts a turn")
		Expect(s.detail("alice", id)["goal"]).To(HaveKeyWithValue("turns", BeNumerically("==", 1)))

		rec = s.do("alice", http.MethodPut, "/api/chats/"+id+"/goal", map[string]any{"text": "write the long summary"})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["replaced"]).To(HaveKeyWithValue("text", "write the summary"))

		Expect(s.do("alice", http.MethodPost, "/api/chats/"+id+"/stop", nil).Code).To(Equal(http.StatusOK))
		Eventually(func() any { return goal("alice", id)["state"] }, 5*time.Second).Should(Equal("paused"))
		Expect(goal("alice", id)).To(HaveKeyWithValue("paused_by", "user"))
		Eventually(func() bool { return s.detail("alice", id)["running"] == false }, 5*time.Second).Should(BeTrue())

		rec = s.do("alice", http.MethodPost, "/api/chats/"+id+"/goal/resume", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["goal"]).To(HaveKeyWithValue("state", "active"))
		Expect(s.do("alice", http.MethodPost, "/api/chats/"+id+"/stop", nil).Code).To(Equal(http.StatusOK))
		Eventually(func() bool { return s.detail("alice", id)["running"] == false }, 5*time.Second).Should(BeTrue())

		rec = s.do("alice", http.MethodDelete, "/api/chats/"+id+"/goal", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["replaced"]).To(HaveKeyWithValue("text", "write the long summary"))
		Expect(goal("alice", id)).To(BeNil())
		Expect(s.detail("alice", id)).NotTo(HaveKey("goal"))

		rec = s.do("alice", http.MethodGet, "/api/chats/"+id+"/messages", nil)
		Expect(rec.Code).To(Equal(http.StatusOK))
		var events []string
		for _, m := range decodeJSON(rec)["messages"].([]any) {
			msg := m.(map[string]any)
			if msg["role"] == "goal" {
				events = append(events, msg["goal_event"].(string))
			}
		}
		Expect(events).To(ContainElements("set", "continue", "replaced", "paused", "resumed", "cleared"))

		rec = s.do("alice", http.MethodPost, "/api/chats/"+id+"/goal/resume", nil)
		Expect(rec.Code).To(Equal(http.StatusNotFound), "no goal to resume")
	})

	It("refuses bad text, another user's chat and a revoked permission", func() {
		id := s.create("alice")
		Expect(s.do("alice", http.MethodPut, "/api/chats/"+id+"/goal", map[string]any{"text": " "}).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("alice", http.MethodPut, "/api/chats/"+id+"/goal", map[string]any{"text": strings.Repeat("x", 2001)}).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("alice", http.MethodPut, "/api/chats/"+id+"/goal", "not json").Code).To(Equal(http.StatusBadRequest))
		for _, r := range []struct{ method, path string }{
			{http.MethodGet, "/goal"}, {http.MethodPut, "/goal"}, {http.MethodDelete, "/goal"}, {http.MethodPost, "/goal/resume"},
		} {
			Expect(s.do("bob", r.method, "/api/chats/"+id+r.path, map[string]any{"text": "mine"}).Code).To(Equal(http.StatusNotFound), r.method+r.path)
			Expect(s.do("carol", r.method, "/api/chats/"+id+r.path, map[string]any{"text": "mine"}).Code).To(Equal(http.StatusForbidden), r.method+r.path)
		}
		Expect(s.harness.Sent()).To(BeEmpty())
	})

	It("refuses a goal for a chat that a trigger started", func() {
		rec, err := s.svc.CreateAs(context.Background(), "alice", studio.CreateInput{Model: "test-model"}, studio.Origin{Kind: session.OriginTrigger, ID: "t1"})
		Expect(err).NotTo(HaveOccurred())
		resp := s.do("alice", http.MethodPut, "/api/chats/"+rec.ID+"/goal", map[string]any{"text": "loop"})
		Expect(resp.Code).To(Equal(http.StatusConflict), resp.Body.String())
		Expect(resp.Body.String()).To(ContainSubstring("only a chat that you started can have a goal"))
	})
})

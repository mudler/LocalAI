// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Chats trigger changes in the chat", func() {
	It("carries an edit, a pause and a delete as cards the person answers over the API", func() {
		var args string
		h := &studiotest.ToolHarness{Script: func(_ workspace.Actor, text string) []studiotest.ToolStep {
			tool, _, _ := strings.Cut(strings.TrimSpace(text[strings.LastIndex(text, "\n")+1:]), " ")
			return []studiotest.ToolStep{{Tool: tool, Args: args}, {Say: "ok"}}
		}}
		s := newStudioServerWith(newTestFiles(), func(d *studio.Deps) { d.Harness = h })
		created := s.do("alice", http.MethodPost, "/api/chats/triggers", map[string]any{
			"name": "Nightly", "prompt": "Review the open items.", "kind": "schedule", "schedule": "daily 08:00", "time_zone": "UTC", "model": "test-model", "enabled": true,
		})
		Expect(created.Code).To(Equal(http.StatusCreated), created.Body.String())
		tid := decodeJSON(created)["id"].(string)
		ref := studio.TriggerRef(tid)
		id := s.create("alice")

		lastCard := func() string {
			var cid string
			for _, m := range s.transcript("alice", id) {
				if card, ok := m["card"].(map[string]any); ok && card["kind"] == studio.ChangeCardKind {
					cid = card["id"].(string)
				}
			}
			return cid
		}
		call := func(tool, a string) string {
			args = a
			s.readEvents("alice", id, func() {
				defer GinkgoRecover()
				s.do("alice", http.MethodPost, "/api/chats/"+id+"/messages", map[string]any{"text": tool + " go"})
			}, func(kind string) bool { return kind == "turn_end" })
			return lastCard()
		}

		// An edit waits.
		cid := call("update_trigger", fmt.Sprintf(`{"ref":%q,"name":"Nightly digest"}`, ref))
		Expect(cid).NotTo(BeEmpty())
		url := "/api/chats/triggers/changes/" + cid
		got := decodeJSON(s.do("alice", http.MethodGet, url, nil))
		Expect(got).To(And(HaveKeyWithValue("state", "pending"), HaveKeyWithValue("op", "update"), HaveKeyWithValue("trigger_id", tid)))
		Expect(got["fields"]).To(Equal([]any{"name"}))
		Expect(got["before"].(map[string]any)["name"]).To(Equal("Nightly"))
		Expect(got["after"].(map[string]any)["name"]).To(Equal("Nightly digest"))
		Expect(s.do("bob", http.MethodGet, url, nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("bob", http.MethodPost, url+"/apply", nil).Code).To(Equal(http.StatusNotFound))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/triggers/"+tid, nil))["name"]).To(Equal("Nightly"))

		apply := s.do("alice", http.MethodPost, url+"/apply", nil)
		Expect(apply.Code).To(Equal(http.StatusOK), apply.Body.String())
		Expect(decodeJSON(apply)).To(And(HaveKeyWithValue("state", "applied"), HaveKeyWithValue("can_undo", true)))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/triggers/"+tid, nil))["name"]).To(Equal("Nightly digest"))
		Expect(s.do("alice", http.MethodPost, url+"/apply", nil).Code).To(Equal(http.StatusConflict))
		undo := s.do("alice", http.MethodPost, url+"/undo", nil)
		Expect(undo.Code).To(Equal(http.StatusOK), undo.Body.String())
		Expect(decodeJSON(undo)).To(HaveKeyWithValue("state", "undone"))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/triggers/"+tid, nil))["name"]).To(Equal("Nightly"))

		// A pause applies at once.
		cid = call("update_trigger", fmt.Sprintf(`{"ref":%q,"enabled":false}`, ref))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/triggers/"+tid, nil))["enabled"]).To(BeFalse())
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/triggers/changes/"+cid, nil))).To(And(HaveKeyWithValue("op", "pause"), HaveKeyWithValue("state", "applied")))
		Expect(s.do("alice", http.MethodPost, "/api/chats/triggers/changes/"+cid+"/undo", nil).Code).To(Equal(http.StatusOK))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/triggers/"+tid, nil))["enabled"]).To(BeTrue())

		// A delete waits; Keep leaves the trigger; a second ask deletes it.
		cid = call("delete_trigger", fmt.Sprintf(`{"ref":%q}`, ref))
		Expect(s.do("alice", http.MethodDelete, "/api/chats/triggers/changes/"+cid, nil).Code).To(Equal(http.StatusNoContent))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/triggers/changes/"+cid, nil))).To(HaveKeyWithValue("state", "dismissed"))
		Expect(s.do("alice", http.MethodGet, "/api/chats/triggers/"+tid, nil).Code).To(Equal(http.StatusOK))
		cid = call("delete_trigger", `{"ref":"Nightly"}`)
		Expect(s.do("alice", http.MethodPost, "/api/chats/triggers/changes/"+cid+"/apply", nil).Code).To(Equal(http.StatusOK))
		Expect(s.do("alice", http.MethodGet, "/api/chats/triggers/"+tid, nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("alice", http.MethodPost, "/api/chats/triggers/changes/"+cid+"/undo", nil).Code).To(Equal(http.StatusOK))
		Expect(s.do("alice", http.MethodGet, "/api/chats/triggers/"+tid, nil).Code).To(Equal(http.StatusOK))
		// Change records are neither triggers nor proposals.
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/triggers", nil))["triggers"]).To(HaveLen(1))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/triggers/proposals", nil))["triggers"]).To(BeEmpty())
	})
})

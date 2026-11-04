// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"encoding/json"
	"net/http"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Chats proposed changes", func() {
	It("lists, confirms, dismisses and undoes what a model proposed, for its owner only", func() {
		args := func(target, content string) string {
			raw, _ := json.Marshal(map[string]string{"kind": "memory", "scope": "user", "target": target, "content": content, "summary": "note " + target})
			return string(raw)
		}
		h := &studiotest.ToolHarness{Script: func(_ workspace.Actor, text string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Tool: "propose_change", Args: args(text, "- fact "+text+"\n")}, {Say: "ok"}}
		}}
		s := newStudioServerWith(newTestFiles(), func(d *studio.Deps) { d.Harness = h })
		id := s.create("alice")
		propose := func(target string) string {
			s.readEvents("alice", id, func() {
				defer GinkgoRecover()
				s.do("alice", http.MethodPost, "/api/chats/"+id+"/messages", map[string]any{"text": target})
			}, func(kind string) bool { return kind == "turn_end" })
			var list []map[string]any
			rec := s.do("alice", http.MethodGet, "/api/chats/changes?status=pending", nil)
			Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
			Expect(json.Unmarshal(rec.Body.Bytes(), &list)).To(Succeed())
			Expect(list).NotTo(BeEmpty())
			return list[0]["id"].(string)
		}

		a := propose("a.md")
		url := "/api/chats/changes/" + a
		Expect(s.do("bob", http.MethodGet, url, nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("bob", http.MethodPost, url+"/confirm", nil).Code).To(Equal(http.StatusNotFound))
		got := decodeJSON(s.do("alice", http.MethodGet, url, nil))
		Expect(got).To(And(HaveKeyWithValue("status", "pending"), HaveKeyWithValue("target", "Memory/a.md"), HaveKeyWithValue("v", float64(1))))
		Expect(s.do("alice", http.MethodGet, "/api/chats/files/content?path=/Memory/a.md", nil).Code).To(Equal(http.StatusNotFound))

		conf := s.do("alice", http.MethodPost, url+"/confirm", nil)
		Expect(conf.Code).To(Equal(http.StatusOK), conf.Body.String())
		Expect(decodeJSON(conf)).To(HaveKeyWithValue("status", "applied"))
		file := s.do("alice", http.MethodGet, "/api/chats/files/content?path=/Memory/a.md", nil)
		Expect(file.Code).To(Equal(http.StatusOK))
		Expect(file.Body.String()).To(Equal("- fact a.md\n"))
		Expect(s.do("alice", http.MethodPost, url+"/confirm", nil).Code).To(Equal(http.StatusConflict))

		undo := s.do("alice", http.MethodPost, url+"/undo", nil)
		Expect(undo.Code).To(Equal(http.StatusOK), undo.Body.String())
		Expect(decodeJSON(undo)).To(HaveKeyWithValue("status", "reverted"))
		Expect(s.do("alice", http.MethodGet, "/api/chats/files/content?path=/Memory/a.md", nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("alice", http.MethodPost, url+"/undo", nil).Code).To(Equal(http.StatusConflict))

		c := propose("c.md")
		edited := s.do("alice", http.MethodPost, "/api/chats/changes/"+c+"/confirm", map[string]string{"content": "- mine\n"})
		Expect(edited.Code).To(Equal(http.StatusOK), edited.Body.String())
		Expect(s.do("alice", http.MethodGet, "/api/chats/files/content?path=/Memory/c.md", nil).Body.String()).To(Equal("- mine\n"))

		b := propose("b.md")
		dis := s.do("alice", http.MethodPost, "/api/chats/changes/"+b+"/dismiss", nil)
		Expect(dis.Code).To(Equal(http.StatusOK), dis.Body.String())
		Expect(decodeJSON(dis)).To(HaveKeyWithValue("status", "dismissed"))
		Expect(s.do("alice", http.MethodGet, "/api/chats/changes?status=bogus", nil).Code).To(Equal(http.StatusBadRequest))

		var audit []map[string]any
		rec := s.do("alice", http.MethodGet, "/api/chats/changes/audit", nil)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(json.Unmarshal(rec.Body.Bytes(), &audit)).To(Succeed())
		Expect(audit).To(HaveLen(7), "propose, confirm, revert, propose, confirm, propose, dismiss: %v", audit)
		Expect(rec.Body.String()).NotTo(ContainSubstring("fact"))
		var none []map[string]any
		Expect(json.Unmarshal(s.do("bob", http.MethodGet, "/api/chats/changes", nil).Body.Bytes(), &none)).To(Succeed())
		Expect(none).To(BeEmpty())
	})

	It("lists the context files of the caller, with the caps", func() {
		s := newStudioServerWith(newTestFiles(), nil)
		rec := s.do("alice", http.MethodGet, "/api/chats/context-files", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		got := decodeJSON(rec)
		Expect(got).To(HaveKeyWithValue("groups", BeEmpty()))
		Expect(got["limits"]).To(HaveKeyWithValue("index_lines", float64(40)))
	})
})

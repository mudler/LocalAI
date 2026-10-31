// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"net/http"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

var _ = Describe("Chats file index API", func() {
	It("lists what was attached and what a tool wrote, follows a rename, and stays private", func() {
		h := &studiotest.ToolHarness{Script: func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "write it" {
				return []studiotest.ToolStep{{Tool: "write_file", Args: `{"path":"/out/summary.md","content":"# sum"}`}, {Say: "done"}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		}}
		s := newStudioServerWith(newTestFiles(), func(d *studio.Deps) { d.Harness = h })
		id := s.create("alice")

		empty := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/"+id+"/files", nil))
		Expect(empty["files"]).To(BeEmpty())
		Expect(empty).NotTo(HaveKey("folder"))

		s.attach("alice", id, "read this", "notes.txt", []byte("some notes"))
		s.say("alice", id, "write it")

		files := func() map[string]map[string]any {
			rec := s.do("alice", http.MethodGet, "/api/chats/"+id+"/files", nil)
			Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
			out := map[string]map[string]any{}
			for _, f := range decodeJSON(rec)["files"].([]any) {
				m := f.(map[string]any)
				out[m["name"].(string)] = m
			}
			return out
		}
		got := files()
		Expect(got).To(HaveLen(2))
		Expect(got["notes.txt"]).To(And(HaveKeyWithValue("origin", "user"), HaveKeyWithValue("mime", "text/plain"),
			HaveKeyWithValue("size", BeNumerically("==", 10)), HaveKeyWithValue("turn", BeNumerically("==", 1))))
		Expect(got["notes.txt"]["path"]).To(HavePrefix("/Chats/"))
		Expect(got["notes.txt"]["guest_path"]).To(Equal("/workspace/home" + got["notes.txt"]["path"].(string)))
		Expect(got["summary.md"]).To(And(HaveKeyWithValue("origin", "tool"), HaveKeyWithValue("path", "/out/summary.md"),
			HaveKeyWithValue("guest_path", "/workspace/home/out/summary.md")))
		// The path works with the files API.
		Expect(s.do("alice", http.MethodGet, "/api/chats/files/content?path="+strings.ReplaceAll(got["notes.txt"]["path"].(string), " ", "%20"), nil).Code).To(Equal(http.StatusOK))

		rename := s.do("alice", http.MethodPatch, "/api/chats/"+id, map[string]any{"title": "Renamed notes"})
		Expect(rename.Code).To(Equal(http.StatusOK), rename.Body.String())
		after := files()
		Expect(after["notes.txt"]["path"]).To(ContainSubstring("Renamed notes/notes.txt"))
		list := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/"+id+"/files", nil))
		Expect(list["folder_path"]).To(HaveSuffix("Renamed notes"))
		Expect(list["stale"]).To(HaveLen(1), "the old folder name is reported, the model was given it")

		Expect(s.do("bob", http.MethodGet, "/api/chats/"+id+"/files", nil).Code).To(Equal(http.StatusNotFound))
	})
})

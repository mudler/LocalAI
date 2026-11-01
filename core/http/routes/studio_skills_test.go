// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/files"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func skillNames(rec map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, v := range rec["skills"].([]any) {
		m := v.(map[string]any)
		out[m["name"].(string)] = m
	}
	return out
}

var _ = Describe("Chats skills endpoints", func() {
	var s *studioServer
	BeforeEach(func() { s = newStudioServer() })

	DescribeTable("require authentication on every route",
		func(method, path string) {
			Expect(s.do("", method, path, nil).Code).To(Equal(http.StatusUnauthorized))
		},
		Entry("list", http.MethodGet, "/api/chats/skills"),
		Entry("create", http.MethodPost, "/api/chats/skills"),
		Entry("read", http.MethodGet, "/api/chats/skills/personal/a"),
		Entry("update", http.MethodPut, "/api/chats/skills/personal/a"),
		Entry("delete", http.MethodDelete, "/api/chats/skills/personal/a"),
		Entry("switches", http.MethodPut, "/api/chats/c1/skills"),
	)

	create := func(user, layer, name, desc, body string) *httptest.ResponseRecorder {
		return s.do(user, http.MethodPost, "/api/chats/skills", map[string]any{"layer": layer, "name": name, "description": desc, "body": body})
	}

	It("creates, reads, updates and deletes a personal skill", func() {
		rec := create("alice", "personal", "review", "check a change", "Read the diff.")
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())

		got := s.do("alice", http.MethodGet, "/api/chats/skills/personal/review", nil)
		Expect(got.Code).To(Equal(http.StatusOK), got.Body.String())
		Expect(decodeJSON(got)).To(And(HaveKeyWithValue("description", "check a change"), HaveKeyWithValue("body", "Read the diff.\n")))

		Expect(s.do("alice", http.MethodPut, "/api/chats/skills/personal/review", map[string]any{"description": "check carefully", "body": "Changed."}).Code).To(Equal(http.StatusOK))
		list := skillNames(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/skills", nil)))
		Expect(list).To(HaveKey("review"))
		Expect(list["review"]).To(And(HaveKeyWithValue("description", "check carefully"), HaveKeyWithValue("layer", "personal"), HaveKeyWithValue("editable", true)))

		Expect(s.do("alice", http.MethodDelete, "/api/chats/skills/personal/review", nil).Code).To(Equal(http.StatusOK))
		Expect(skillNames(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/skills", nil)))).NotTo(HaveKey("review"))
		Expect(s.do("alice", http.MethodGet, "/api/chats/skills/personal/review", nil).Code).To(Equal(http.StatusNotFound))
	})

	It("keeps personal skills personal", func() {
		Expect(create("alice", "personal", "secret", "alice only", "x").Code).To(Equal(http.StatusOK))
		Expect(skillNames(decodeJSON(s.do("bob", http.MethodGet, "/api/chats/skills", nil)))).NotTo(HaveKey("secret"))
		Expect(s.do("bob", http.MethodGet, "/api/chats/skills/personal/secret", nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("bob", http.MethodDelete, "/api/chats/skills/personal/secret", nil).Code).To(Equal(http.StatusNotFound))
	})

	It("lets only an administrator write the shared skills, which everyone reads", func() {
		Expect(create("alice", "shared", "team", "for all", "x").Code).To(Equal(http.StatusForbidden))
		Expect(create("root", "shared", "team", "for all", "Team body.").Code).To(Equal(http.StatusOK))
		for _, user := range []string{"alice", "bob"} {
			list := skillNames(decodeJSON(s.do(user, http.MethodGet, "/api/chats/skills", nil)))
			Expect(list).To(HaveKey("team"))
			Expect(list["team"]).To(And(HaveKeyWithValue("layer", "shared"), HaveKeyWithValue("editable", false)))
		}
		Expect(skillNames(decodeJSON(s.do("root", http.MethodGet, "/api/chats/skills", nil)))["team"]).To(HaveKeyWithValue("editable", true))
		Expect(s.do("alice", http.MethodPut, "/api/chats/skills/shared/team", map[string]any{"description": "mine now", "body": "x"}).Code).To(Equal(http.StatusForbidden))
		Expect(s.do("alice", http.MethodDelete, "/api/chats/skills/shared/team", nil).Code).To(Equal(http.StatusForbidden))
		Expect(s.do("alice", http.MethodGet, "/api/chats/skills/shared/team", nil).Code).To(Equal(http.StatusOK))
		Expect(create("root", "builtin", "x", "d", "b").Code).To(Equal(http.StatusBadRequest))
	})

	It("answers 400 for a name or description that is not valid", func() {
		for _, name := range []string{"Has Space", "../x", "a/b", ""} {
			Expect(create("alice", "personal", name, "d", "b").Code).To(Equal(http.StatusBadRequest), name)
		}
		Expect(create("alice", "personal", "ok", "", "b").Code).To(Equal(http.StatusBadRequest))
		Expect(create("alice", "nowhere", "ok", "d", "b").Code).To(Equal(http.StatusBadRequest))
	})

	It("shows which skills a chat has switched off, and keeps the switches", func() {
		Expect(create("alice", "personal", "review", "check", "x").Code).To(Equal(http.StatusOK))
		Expect(create("alice", "personal", "notes", "keep notes", "x").Code).To(Equal(http.StatusOK))
		id := s.create("alice")
		rec := s.do("alice", http.MethodPut, "/api/chats/"+id+"/skills", map[string]any{"off": []string{"review"}})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		list := skillNames(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/skills?chat="+id, nil)))
		Expect(list["review"]).To(HaveKeyWithValue("enabled", false))
		Expect(list["notes"]).To(HaveKeyWithValue("enabled", true))
		// Without a chat everything is on.
		Expect(skillNames(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/skills", nil)))["review"]).To(HaveKeyWithValue("enabled", true))
		Expect(s.do("alice", http.MethodPut, "/api/chats/"+id+"/skills", map[string]any{"off": []string{"Not Valid"}}).Code).To(Equal(http.StatusBadRequest))
		// Another user cannot read or change them.
		Expect(s.do("bob", http.MethodPut, "/api/chats/"+id+"/skills", map[string]any{"off": []string{"x"}}).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("bob", http.MethodGet, "/api/chats/skills?chat="+id, nil).Code).To(Equal(http.StatusNotFound))
	})

	It("answers 409 with the reason when files are off", func() {
		off := newStudioServerFiles(files.Disabled("There is no data path, so files are off."))
		for _, c := range []struct{ method, path string }{
			{http.MethodGet, "/api/chats/skills"},
			{http.MethodGet, "/api/chats/skills/personal/a"},
		} {
			rec := off.do("alice", c.method, c.path, nil)
			Expect(rec.Code).To(Equal(http.StatusConflict), c.path)
			Expect(rec.Body.String()).To(ContainSubstring("no data path"))
		}
		Expect(strings.Contains(off.do("alice", http.MethodPost, "/api/chats/skills", map[string]any{"layer": "personal", "name": "a", "description": "d", "body": "b"}).Body.String(), "files_off")).To(BeTrue())
	})
})

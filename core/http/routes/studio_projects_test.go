// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"net/http"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These specs go through the real route table, so the order of the static
// /projects routes and the "/:id" chat routes is the one the server has.
var _ = Describe("Chats projects routes", func() {
	var s *studioServer
	BeforeEach(func() {
		f := newTestFiles()
		s = newStudioServerWith(f, func(d *studio.Deps) {
			ps, err := projects.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			d.Projects = projects.NewService(projects.Options{Store: ps, Files: f})
		})
	})

	DescribeTable("require authentication on every route",
		func(method, path string) {
			Expect(s.do("", method, path, nil).Code).To(Equal(http.StatusUnauthorized))
		},
		Entry("list", http.MethodGet, "/api/chats/projects"),
		Entry("create", http.MethodPost, "/api/chats/projects"),
		Entry("get", http.MethodGet, "/api/chats/projects/p1"),
		Entry("change", http.MethodPatch, "/api/chats/projects/p1"),
		Entry("delete", http.MethodDelete, "/api/chats/projects/p1"),
		Entry("archive", http.MethodPost, "/api/chats/projects/p1/archive"),
		Entry("restore", http.MethodPost, "/api/chats/projects/p1/restore"),
		Entry("memory", http.MethodGet, "/api/chats/projects/p1/memory"),
		Entry("write memory", http.MethodPut, "/api/chats/projects/p1/memory/a.md"),
		Entry("chats", http.MethodGet, "/api/chats/projects/p1/chats"),
		Entry("put a chat in a project", http.MethodPut, "/api/chats/c1/project"),
		Entry("pull projects", http.MethodPut, "/api/chats/c1/projects"),
	)

	It("reaches the project handlers, not the chat routes, and keeps the chat routes", func() {
		rec := s.do("alice", http.MethodPost, "/api/chats/projects", map[string]any{"name": "Acme"})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		pid := decodeJSON(rec)["id"].(string)
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("slug", "acme"))

		list := s.do("alice", http.MethodGet, "/api/chats/projects", nil)
		Expect(list.Code).To(Equal(http.StatusOK), list.Body.String())
		Expect(decodeJSON(list)).To(HaveKey("projects"))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/projects/"+pid, nil))).To(HaveKeyWithValue("slug", "acme"))
		Expect(s.do("alice", http.MethodGet, "/api/chats/projects/"+pid+"/memory", nil).Code).To(Equal(http.StatusOK))
		Expect(s.do("alice", http.MethodGet, "/api/chats/projects/"+pid+"/chats", nil).Code).To(Equal(http.StatusOK))

		chat := s.create("alice")
		put := s.do("alice", http.MethodPut, "/api/chats/"+chat+"/project", map[string]any{"project_id": pid})
		Expect(put.Code).To(Equal(http.StatusOK), put.Body.String())
		Expect(s.detail("alice", chat)).To(HaveKeyWithValue("project_id", pid))
		// "projects" is not a chat id, and a chat id is not a project id.
		Expect(s.do("alice", http.MethodGet, "/api/chats/"+chat, nil).Code).To(Equal(http.StatusOK))
		Expect(s.do("alice", http.MethodGet, "/api/chats/projects/"+chat, nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("bob", http.MethodPut, "/api/chats/"+chat+"/project", map[string]any{"project_id": pid}).Code).To(Equal(http.StatusNotFound))
	})
})

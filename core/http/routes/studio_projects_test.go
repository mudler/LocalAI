// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"encoding/json"
	"net/http"
	"strings"

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

	It("allows an administrator to manage projects on behalf of another user", func() {
		rec := s.do("root", http.MethodPost, "/api/chats/projects?user_id=alice", map[string]any{"name": "Acme"})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		p := decodeJSON(rec)
		Expect(p).To(HaveKeyWithValue("owner", "alice"))
		pid := p["id"].(string)

		list := s.do("root", http.MethodGet, "/api/chats/projects?user_id=alice", nil)
		Expect(list.Code).To(Equal(http.StatusOK), list.Body.String())
		Expect(decodeJSON(list)["projects"]).To(HaveLen(1))

		got := s.do("root", http.MethodGet, "/api/chats/projects/"+pid+"?user_id=alice", nil)
		Expect(got.Code).To(Equal(http.StatusOK), got.Body.String())
		Expect(decodeJSON(got)).To(HaveKeyWithValue("owner", "alice"))

		// The target user sees their own project.
		Expect(s.do("alice", http.MethodGet, "/api/chats/projects", nil).Code).To(Equal(http.StatusOK))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/projects", nil))["projects"]).To(HaveLen(1))
		// Another non-admin user cannot act on behalf of someone else.
		Expect(s.do("bob", http.MethodGet, "/api/chats/projects?user_id=alice", nil).Code).To(Equal(http.StatusForbidden))

		del := s.do("root", http.MethodDelete, "/api/chats/projects/"+pid+"?user_id=alice", nil)
		Expect(del.Code).To(Equal(http.StatusNoContent), del.Body.String())
		Expect(s.do("alice", http.MethodGet, "/api/chats/projects/"+pid, nil).Code).To(Equal(http.StatusNotFound))
	})

	Context("when a user_id is named", func() {
		var pid, chat string
		BeforeEach(func() {
			rec := s.do("alice", http.MethodPost, "/api/chats/projects", map[string]any{"name": "Acme"})
			Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
			pid = decodeJSON(rec)["id"].(string)
			chat = s.create("alice")
		})

		ids := func() *strings.Replacer { return strings.NewReplacer("{p}", pid, "{c}", chat) }
		// target fills in the ids of the project and the chat, and adds the user_id.
		target := func(path, owner string) string { return ids().Replace(path) + "?user_id=" + owner }
		// payload fills in the ids in the body.
		payload := func(body any) any {
			if body == nil {
				return nil
			}
			data, err := json.Marshal(body)
			Expect(err).NotTo(HaveOccurred())
			return ids().Replace(string(data))
		}

		DescribeTable("the routes that take a user_id",
			func(method, path string, in any, want int, archivedFirst bool) {
				body := payload(in)
				if archivedFirst {
					Expect(s.do("alice", http.MethodPost, "/api/chats/projects/"+pid+"/archive", nil).Code).To(Equal(http.StatusOK))
				}
				// Another user is refused before the user_id is looked at.
				Expect(s.do("bob", method, target(path, "alice"), body).Code).To(Equal(http.StatusForbidden))
				Expect(s.do("bob", method, target(path, "nobody"), body).Code).To(Equal(http.StatusForbidden))
				// An administrator needs a well-formed id of a known user.
				Expect(s.do("root", method, target(path, "nobody"), body).Code).To(Equal(http.StatusNotFound))
				for _, bad := range []string{"..%2Falice", "a%2Fb", "alice%2F..", "..", "al%20ice", strings.Repeat("a", 37)} {
					Expect(s.do("root", method, target(path, bad), body).Code).To(Equal(http.StatusBadRequest), "user_id %q", bad)
				}
				// The owner's own id works for the owner, and for the administrator.
				Expect(s.do("root", method, target(path, "alice"), body).Code).To(Equal(want))
				if !archivedFirst && method != http.MethodPost {
					Expect(s.do("alice", method, target(path, "alice"), body).Code).To(Equal(want))
				}
			},
			Entry("update", http.MethodPatch, "/api/chats/projects/{p}", map[string]any{"name": "Renamed"}, http.StatusOK, false),
			Entry("archive", http.MethodPost, "/api/chats/projects/{p}/archive", nil, http.StatusOK, false),
			Entry("restore", http.MethodPost, "/api/chats/projects/{p}/restore", nil, http.StatusOK, true),
			Entry("read the memory", http.MethodGet, "/api/chats/projects/{p}/memory", nil, http.StatusOK, false),
			Entry("write the memory", http.MethodPut, "/api/chats/projects/{p}/memory/index.md", map[string]any{"content": "- a fact"}, http.StatusNoContent, false),
			Entry("list the chats", http.MethodGet, "/api/chats/projects/{p}/chats", nil, http.StatusOK, false),
			Entry("put a chat in the project", http.MethodPut, "/api/chats/{c}/project", map[string]any{"project_id": "{p}"}, http.StatusOK, false),
			Entry("pull projects into a chat", http.MethodPut, "/api/chats/{c}/projects", map[string]any{"project_ids": []string{}}, http.StatusOK, false),
			Entry("list the agents", http.MethodGet, "/api/chats/projects/{p}/agents", nil, http.StatusOK, false),
			Entry("list the connections", http.MethodGet, "/api/chats/projects/{p}/connections", nil, http.StatusOK, false),
			Entry("list the triggers", http.MethodGet, "/api/chats/projects/{p}/triggers", nil, http.StatusOK, false),
			Entry("list the loops", http.MethodGet, "/api/chats/projects/{p}/loops", nil, http.StatusOK, false),
		)

		It("lets the administrator read what the owner wrote", func() {
			Expect(s.do("root", http.MethodPut, target("/api/chats/projects/{p}/memory/index.md", "alice"), map[string]any{"content": "- likes tea"}).Code).To(Equal(http.StatusNoContent))
			got := s.do("alice", http.MethodGet, "/api/chats/projects/"+pid+"/memory", nil)
			Expect(got.Code).To(Equal(http.StatusOK), got.Body.String())
			Expect(got.Body.String()).To(ContainSubstring("likes tea"))
		})

		It("keeps an administrator's own projects apart from the owner's", func() {
			Expect(s.do("root", http.MethodGet, "/api/chats/projects/"+pid, nil).Code).To(Equal(http.StatusNotFound))
			Expect(decodeJSON(s.do("root", http.MethodGet, "/api/chats/projects", nil))["projects"]).To(BeEmpty())
		})
	})
})

// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"context"
	"net/http"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Project skills endpoints", func() {
	var (
		s   *studioServer
		f   *files.Service
		pid string
	)
	BeforeEach(func() {
		f = newTestFiles()
		s = newStudioServerWith(f, func(d *studio.Deps) {
			ps, err := projects.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			d.Projects = projects.NewService(projects.Options{Store: ps, Files: f})
		})
		rec := s.do("alice", http.MethodPost, "/api/chats/projects", map[string]any{"name": "Acme"})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		pid = decodeJSON(rec)["id"].(string)
	})

	write := func(user, project, name, desc string) int {
		return s.do(user, http.MethodPost, "/api/chats/skills", map[string]any{"layer": "project", "project": project, "name": name, "description": desc, "body": "Do it."}).Code
	}
	url := func(name string) string { return "/api/chats/skills/project/" + name + "?project=" + pid }

	It("creates, lists, reads, updates and deletes a project skill as the owner", func() {
		rec := s.do("alice", http.MethodPost, "/api/chats/skills", map[string]any{"layer": "project", "project": pid, "name": "plan", "description": "plan the work", "body": "Step one."})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)).To(And(HaveKeyWithValue("layer", "project"), HaveKeyWithValue("editable", true)))

		list := s.do("alice", http.MethodGet, "/api/chats/skills?layer=project&project="+pid, nil)
		Expect(list.Code).To(Equal(http.StatusOK), list.Body.String())
		names := skillNames(decodeJSON(list))
		Expect(names).To(HaveLen(1))
		Expect(names["plan"]).To(And(HaveKeyWithValue("layer", "project"), HaveKeyWithValue("project", "acme"), HaveKeyWithValue("editable", true)))
		Expect(decodeJSON(list)).NotTo(HaveKey("skills_blocked"))

		got := s.do("alice", http.MethodGet, url("plan"), nil)
		Expect(got.Code).To(Equal(http.StatusOK), got.Body.String())
		Expect(decodeJSON(got)).To(And(HaveKeyWithValue("description", "plan the work"), HaveKeyWithValue("body", "Step one.\n")))

		up := s.do("alice", http.MethodPut, url("plan"), map[string]any{"description": "plan it well", "body": "Changed."})
		Expect(up.Code).To(Equal(http.StatusOK), up.Body.String())
		Expect(decodeJSON(s.do("alice", http.MethodGet, url("plan"), nil))).To(HaveKeyWithValue("description", "plan it well"))

		// The personal list does not hold it: it belongs to the project.
		Expect(skillNames(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/skills", nil)))).NotTo(HaveKey("plan"))

		Expect(s.do("alice", http.MethodDelete, url("plan"), nil).Code).To(Equal(http.StatusOK))
		Expect(s.do("alice", http.MethodGet, url("plan"), nil).Code).To(Equal(http.StatusNotFound))
	})

	It("lists the project's skills in a chat of the project", func() {
		Expect(write("alice", pid, "plan", "plan the work")).To(Equal(http.StatusOK))
		chat := s.do("alice", http.MethodPost, "/api/chats", map[string]any{"model": "test-model", "project_id": pid})
		Expect(chat.Code).To(Equal(http.StatusCreated), chat.Body.String())
		cid := decodeJSON(chat)["id"].(string)
		in := skillNames(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/skills?chat="+cid, nil)))
		Expect(in["plan"]).To(HaveKeyWithValue("layer", "project"))
		Expect(skillNames(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/skills", nil)))).NotTo(HaveKey("plan"))
	})

	It("is for the owner only: another user, and an unknown project, are not found", func() {
		Expect(write("alice", pid, "plan", "plan the work")).To(Equal(http.StatusOK))
		Expect(write("bob", pid, "evil", "planted")).To(Equal(http.StatusNotFound))
		Expect(s.do("bob", http.MethodGet, url("plan"), nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("bob", http.MethodPut, url("plan"), map[string]any{"description": "x", "body": "y"}).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("bob", http.MethodDelete, url("plan"), nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("bob", http.MethodGet, "/api/chats/skills?layer=project&project="+pid, nil).Code).To(Equal(http.StatusNotFound))
		Expect(write("alice", "nope", "x", "y")).To(Equal(http.StatusNotFound))
		Expect(s.do("alice", http.MethodGet, "/api/chats/skills?layer=project", nil).Code).To(Equal(http.StatusNotFound))
	})

	It("refuses a write to an archived project and a bad skill", func() {
		Expect(write("alice", pid, "Bad Name", "x")).To(Equal(http.StatusBadRequest))
		Expect(write("alice", pid, "plan", "")).To(Equal(http.StatusBadRequest))
		Expect(s.do("alice", http.MethodPost, "/api/chats/projects/"+pid+"/archive", nil).Code).To(Equal(http.StatusOK))
		Expect(write("alice", pid, "plan", "plan the work")).To(Equal(http.StatusBadRequest))
	})

	It("reports skills_blocked on the project and refuses every call while another person may write the folder", func() {
		Expect(write("alice", pid, "plan", "plan the work")).To(Equal(http.StatusOK))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/projects/"+pid, nil))).NotTo(HaveKey("skills_blocked"))

		admin := workspace.Actor{Principal: files.AdminPrincipal}
		Expect(f.Store().Grant(context.Background(), admin, "bob", "/users/alice/Projects/acme", workspace.ModeWrite)).To(Succeed())

		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/projects/"+pid, nil))).To(HaveKeyWithValue("skills_blocked", true))
		list := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/skills?layer=project&project="+pid, nil))
		Expect(list).To(HaveKeyWithValue("skills_blocked", true))
		Expect(skillNames(list)).To(BeEmpty())
		Expect(s.do("alice", http.MethodGet, url("plan"), nil).Code).To(Equal(http.StatusConflict))
		Expect(write("alice", pid, "more", "x")).To(Equal(http.StatusConflict))
		Expect(s.do("alice", http.MethodPut, url("plan"), map[string]any{"description": "x", "body": "y"}).Code).To(Equal(http.StatusConflict))
		Expect(s.do("alice", http.MethodDelete, url("plan"), nil).Code).To(Equal(http.StatusConflict))
	})
})

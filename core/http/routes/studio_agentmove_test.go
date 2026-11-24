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

var _ = Describe("Moving a saved agent to another project (API)", func() {
	var s *studioServer
	BeforeEach(func() {
		f := newTestFiles()
		s = newStudioServerWith(f, func(d *studio.Deps) {
			ps, err := projects.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			d.Projects = projects.NewService(projects.Options{Store: ps, Files: f})
		})
	})
	newProject := func(user, name string) string {
		rec := s.do(user, http.MethodPost, "/api/chats/projects", map[string]any{"name": name})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		return decodeJSON(rec)["id"].(string)
	}
	newAgent := func(user string, extra map[string]any) string {
		body := map[string]any{"name": "Helper", "model": "test-model"}
		for k, v := range extra {
			body[k] = v
		}
		rec := s.do(user, http.MethodPost, "/api/chats/agents", body)
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		return decodeJSON(rec)["id"].(string)
	}
	moveURL := func(id string) string { return "/api/chats/agents/" + id + "/project" }

	It("requires authentication and the owner", func() {
		pid := newProject("alice", "Acme")
		id := newAgent("alice", nil)
		Expect(s.do("", http.MethodPut, moveURL(id), map[string]any{"project_id": pid}).Code).To(Equal(http.StatusUnauthorized))
		Expect(s.do("bob", http.MethodPut, moveURL(id), map[string]any{"project_id": pid}).Code).To(Equal(http.StatusNotFound))
	})

	It("answers a dry run with the counts and writes nothing, then moves the agent", func() {
		pid := newProject("alice", "Acme")
		id := newAgent("alice", map[string]any{"builtins": []string{"project.memory.write"}})
		chat := decodeJSON(s.do("alice", http.MethodPost, "/api/chats", map[string]any{"agent_id": id}))["id"].(string)

		rec := s.do("alice", http.MethodPut, moveURL(id), map[string]any{"project_id": pid, "dry_run": true})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		dry := decodeJSON(rec)
		Expect(dry).To(HaveKeyWithValue("dry_run", true))
		Expect(dry).To(HaveKeyWithValue("moved", false))
		Expect(dry).To(HaveKeyWithValue("chats", BeNumerically("==", 1)))
		Expect(dry).To(HaveKeyWithValue("memory_grant_dropped", true))
		Expect(dry["blockers"]).To(BeEmpty())
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/agents/"+id, nil))).NotTo(HaveKey("project_id"))

		rec = s.do("alice", http.MethodPut, moveURL(id), map[string]any{"project_id": pid})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("moved", true))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/agents/"+id, nil))).To(HaveKeyWithValue("project_id", pid))
		// It shows on the project page, and the export carries the new project.
		list := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/projects/"+pid+"/agents", nil))["agents"].([]any)
		Expect(list).To(HaveLen(1))
		exp := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/agents/"+id+"/export", nil))
		Expect(exp["agent"]).To(HaveKey("projectRef"))
		// The chat that was open takes no new turn.
		rec = s.do("alice", http.MethodPost, "/api/chats/"+chat+"/messages", map[string]any{"text": "hi"})
		Expect(rec.Code).To(Equal(http.StatusConflict), rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring("agent_moved"))
	})

	It("refuses a bad target, a body without a way out, and a plain save that names another project", func() {
		pid := newProject("alice", "Acme")
		other := newProject("alice", "Other")
		id := newAgent("alice", map[string]any{"project_id": pid})
		Expect(s.do("alice", http.MethodPut, moveURL(id), map[string]any{"project_id": "nope"}).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("alice", http.MethodPut, moveURL(id), map[string]any{"project_id": pid}).Code).To(Equal(http.StatusBadRequest), "same project")
		Expect(s.do("alice", http.MethodPut, moveURL(id), map[string]any{"project_id": ""}).Code).To(Equal(http.StatusBadRequest), "no folders")
		Expect(s.do("alice", http.MethodPut, moveURL(id), map[string]any{"project_id": other, "files_mode": "x"}).Code).To(Equal(http.StatusBadRequest))
		rec := s.do("alice", http.MethodPut, "/api/chats/agents/"+id, map[string]any{"name": "Helper", "model": "test-model", "project_id": other})
		Expect(rec.Code).To(Equal(http.StatusBadRequest), rec.Body.String())
		rec = s.do("alice", http.MethodPut, moveURL(id), map[string]any{"project_id": "", "whole_home": true})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/agents/"+id, nil))).NotTo(HaveKey("project_id"))
	})
})

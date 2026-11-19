// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"context"
	"net/http"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Chats project agents API", func() {
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
	newAgent := func(user, pid, name string, extra map[string]any) string {
		body := map[string]any{"name": name, "model": "test-model", "project_id": pid}
		for k, v := range extra {
			body[k] = v
		}
		rec := s.do(user, http.MethodPost, "/api/chats/agents", body)
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		return decodeJSON(rec)["id"].(string)
	}
	rows := func(user, pid string) []any {
		rec := s.do(user, http.MethodGet, "/api/chats/projects/"+pid+"/agents", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		return decodeJSON(rec)["agents"].([]any)
	}

	It("requires authentication", func() {
		Expect(s.do("", http.MethodGet, "/api/chats/projects/p1/agents", nil).Code).To(Equal(http.StatusUnauthorized))
	})

	It("lists the agents of a project as brief rows sorted by name, with the files mode and the memory grant", func() {
		pid := newProject("alice", "Acme")
		newAgent("alice", pid, "Zed", map[string]any{"description": "last"})
		newAgent("alice", pid, "Amy", map[string]any{"builtins": []string{"project.memory.write"},
			"instructions": "SECRET-INSTRUCTIONS"})
		rec := s.do("alice", http.MethodPost, "/api/chats/agents", map[string]any{"name": "Plain", "model": "test-model"})
		Expect(rec.Code).To(Equal(http.StatusCreated))
		list := rows("alice", pid)
		Expect(list).To(HaveLen(2))
		first, second := list[0].(map[string]any), list[1].(map[string]any)
		Expect(first).To(And(HaveKeyWithValue("name", "Amy"), HaveKeyWithValue("model", "test-model"), HaveKeyWithValue("unavailable", false),
			HaveKeyWithValue("files_mode", "write"), HaveKeyWithValue("memory_write", true), HaveKey("id")))
		Expect(second).To(And(HaveKeyWithValue("name", "Zed"), HaveKeyWithValue("description", "last"), HaveKeyWithValue("memory_write", false)))
		Expect(s.do("alice", http.MethodGet, "/api/chats/projects/"+pid+"/agents", nil).Body.String()).NotTo(ContainSubstring("SECRET-INSTRUCTIONS"))
	})

	It("hides another owner's project, and lists none of another owner's agents of the same project id", func() {
		pid := newProject("alice", "Acme")
		newAgent("alice", pid, "Mine", nil)
		Expect(s.do("bob", http.MethodGet, "/api/chats/projects/"+pid+"/agents", nil).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("alice", http.MethodGet, "/api/chats/projects/nope/agents", nil).Code).To(Equal(http.StatusNotFound))
		// A record of bob that names alice's project id, which the API would refuse.
		Expect(s.agents.Put(context.Background(), agents.Agent{ID: "bobs", Principal: "bob", Name: "Bobs", ProjectID: pid})).To(Succeed())
		list := rows("alice", pid)
		Expect(list).To(HaveLen(1))
		Expect(list[0]).To(HaveKeyWithValue("name", "Mine"))
	})

	It("follows archive and restore with the unavailable flag, and counts the agents on the project", func() {
		pid := newProject("alice", "Acme")
		other := newProject("alice", "Other")
		newAgent("alice", pid, "A1", nil)
		newAgent("alice", pid, "A2", nil)
		count := func(id string) any {
			return decodeJSON(s.do("alice", http.MethodGet, "/api/chats/projects/"+id, nil))["agent_count"]
		}
		Expect(count(pid)).To(BeNumerically("==", 2))
		Expect(count(other)).To(BeNumerically("==", 0))
		byID := map[string]any{}
		for _, p := range decodeJSON(s.do("alice", http.MethodGet, "/api/chats/projects", nil))["projects"].([]any) {
			byID[p.(map[string]any)["id"].(string)] = p.(map[string]any)["agent_count"]
		}
		Expect(byID).To(HaveKeyWithValue(pid, BeNumerically("==", 2)))
		Expect(byID).To(HaveKeyWithValue(other, BeNumerically("==", 0)))

		Expect(s.do("alice", http.MethodPost, "/api/chats/projects/"+pid+"/archive", nil).Code).To(Equal(http.StatusOK))
		for _, r := range rows("alice", pid) {
			Expect(r).To(HaveKeyWithValue("unavailable", true))
		}
		Expect(s.do("alice", http.MethodPost, "/api/chats/projects/"+pid+"/restore", nil).Code).To(Equal(http.StatusOK))
		for _, r := range rows("alice", pid) {
			Expect(r).To(HaveKeyWithValue("unavailable", false))
		}
	})

	Describe("the agent endpoints with a project", func() {
		create := func(user string, body map[string]any) int {
			body["name"], body["model"] = "P", "test-model"
			return s.do(user, http.MethodPost, "/api/chats/agents", body).Code
		}
		It("refuses another owner's project and an unknown one with 404, and an archived one with 400", func() {
			pid := newProject("alice", "Acme")
			Expect(create("bob", map[string]any{"project_id": pid})).To(Equal(http.StatusNotFound))
			Expect(create("alice", map[string]any{"project_id": "nope"})).To(Equal(http.StatusNotFound))
			Expect(s.do("alice", http.MethodPost, "/api/chats/projects/"+pid+"/archive", nil).Code).To(Equal(http.StatusOK))
			Expect(create("alice", map[string]any{"project_id": pid})).To(Equal(http.StatusBadRequest))
		})

		It("refuses a change of the project on a replace and keeps it when the field is absent", func() {
			pid := newProject("alice", "Acme")
			other := newProject("alice", "Other")
			id := newAgent("alice", pid, "Agent", nil)
			put := func(body map[string]any) *http.Response {
				body["name"], body["model"] = "Agent", "test-model"
				rec := s.do("alice", http.MethodPut, "/api/chats/agents/"+id, body)
				return &http.Response{StatusCode: rec.Code}
			}
			Expect(put(map[string]any{"project_id": other}).StatusCode).To(Equal(http.StatusBadRequest))
			Expect(put(map[string]any{"description": "changed"}).StatusCode).To(Equal(http.StatusOK))
			got := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/agents/"+id, nil))
			Expect(got).To(And(HaveKeyWithValue("project_id", pid), HaveKeyWithValue("description", "changed")))
		})
	})
})

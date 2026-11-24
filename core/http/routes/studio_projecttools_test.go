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

// The HTTP contract of the tools a project owns, as the React UI reads it: the
// attached lists on the project, the project skills routes, and the move report.
var _ = Describe("Tools a project owns (API)", func() {
	var s *studioServer
	BeforeEach(func() {
		f := newTestFiles()
		s = newStudioServerWith(f, func(d *studio.Deps) {
			ps, err := projects.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			d.Projects = projects.NewService(projects.Options{Store: ps, Files: f})
		})
	})

	It("takes attached on create and change, returns it, refuses one outside a limit, and reports it on a move", func() {
		rec := s.do("alice", http.MethodPost, "/api/chats/projects", map[string]any{
			"name":     "Acme",
			"attached": map[string]any{"addons": []string{"ad"}, "connectors": []string{"cn"}},
		})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		pid := decodeJSON(rec)["id"].(string)
		Expect(decodeJSON(rec)["attached"]).To(HaveKeyWithValue("addons", ConsistOf("ad")))

		rec = s.do("alice", http.MethodPatch, "/api/chats/projects/"+pid, map[string]any{
			"attached": map[string]any{"addons": []string{"ad"}, "connectors": []string{"cn"}, "mcp_servers": []string{"srv"}, "collections": []string{"col"}, "skills": []string{"style"}},
		})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		got := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/projects/"+pid, nil))
		Expect(got["attached"]).To(And(
			HaveKeyWithValue("addons", ConsistOf("ad")), HaveKeyWithValue("connectors", ConsistOf("cn")),
			HaveKeyWithValue("mcp_servers", ConsistOf("srv")), HaveKeyWithValue("collections", ConsistOf("col")),
			HaveKeyWithValue("skills", ConsistOf("style"))))

		rec = s.do("alice", http.MethodPatch, "/api/chats/projects/"+pid, map[string]any{"defaults": map[string]any{"addons": []string{"other"}}})
		Expect(rec.Code).To(Equal(http.StatusBadRequest), rec.Body.String())
		Expect(rec.Body.String()).To(And(ContainSubstring("ad"), ContainSubstring("defaults.addons")))

		// A project skill is written and read with layer=project and project=<id>.
		rec = s.do("alice", http.MethodPost, "/api/chats/skills", map[string]any{"layer": "project", "project": pid, "name": "plan", "description": "plan the work", "body": "Step one."})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())

		// The agent starts with no project; the move report uses the names the UI reads.
		other := decodeJSON(s.do("alice", http.MethodPost, "/api/chats/projects", map[string]any{"name": "Beta", "attached": map[string]any{"addons": []string{"beta-ad"}}}))["id"].(string)
		agent := decodeJSON(s.do("alice", http.MethodPost, "/api/chats/agents", map[string]any{"name": "Helper", "model": "test-model"}))["id"].(string)
		moveURL := "/api/chats/agents/" + agent + "/project"
		Expect(s.do("alice", http.MethodPut, moveURL, map[string]any{"project_id": pid}).Code).To(Equal(http.StatusOK))
		rep := decodeJSON(s.do("alice", http.MethodPut, moveURL, map[string]any{"project_id": other, "dry_run": true}))
		Expect(rep).To(And(
			HaveKeyWithValue("project_skills_switch", true),
			HaveKey("attached_new"), HaveKey("attached_old")))
		Expect(rep).NotTo(HaveKey("attached_gained"))
		Expect(rep["attached_old"]).To(And(HaveKeyWithValue("addons", ConsistOf("ad")), HaveKeyWithValue("connectors", ConsistOf("cn")), HaveKeyWithValue("mcp_servers", ConsistOf("srv"))))
		Expect(rep["attached_new"]).To(HaveKeyWithValue("addons", ConsistOf("beta-ad")))
	})
})

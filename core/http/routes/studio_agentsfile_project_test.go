// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// noConnectors is a connector service where nobody has a connector.
type noConnectors struct{}

func (noConnectors) List(context.Context, string) ([]mcpconns.Connector, error) { return nil, nil }
func (noConnectors) Resolve(context.Context, string, []string, bool) ([]mcpconns.Tool, []string) {
	return nil, nil
}

var _ = Describe("Chats agent export and import of project agents (format v5)", func() {
	var s *studioServer
	BeforeEach(func() {
		f := newTestFiles()
		s = newStudioServerWith(f, func(d *studio.Deps) {
			ps, err := projects.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			d.Projects = projects.NewService(projects.Options{Store: ps, Files: f})
			d.Connectors = noConnectors{}
		})
	})

	// upload sends the file as a multipart form, with the given form fields.
	upload := func(user, query string, file map[string]any, fields map[string]string) *httptest.ResponseRecorder {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, err := mw.CreateFormFile("file", "agent.json")
		Expect(err).NotTo(HaveOccurred())
		raw, err := json.Marshal(file)
		Expect(err).NotTo(HaveOccurred())
		_, _ = fw.Write(raw)
		for k, v := range fields {
			Expect(mw.WriteField(k, v)).To(Succeed())
		}
		Expect(mw.Close()).To(Succeed())
		req := httptest.NewRequest(http.MethodPost, "/api/chats/agents/import"+query, &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.AddCookie(&http.Cookie{Name: "session", Value: s.sessions[user]})
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, req)
		return rec
	}
	projectCount := func(user string) int {
		return len(decodeJSON(s.do(user, http.MethodGet, "/api/chats/projects", nil))["projects"].([]any))
	}

	newProject := func(user, name string) string {
		rec := s.do(user, http.MethodPost, "/api/chats/projects", map[string]any{"name": name})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		return decodeJSON(rec)["id"].(string)
	}
	projectAgent := func(user, pid string, extra map[string]any) string {
		body := map[string]any{"name": "Planner", "model": "test-model", "project_id": pid, "skills": []string{"review", "ghost-skill"},
			"addons": []string{"office"}, "builtins": []string{"project.memory.write"}}
		for k, v := range extra {
			body[k] = v
		}
		rec := s.do(user, http.MethodPost, "/api/chats/agents", body)
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		return decodeJSON(rec)["id"].(string)
	}
	export := func(user, id string) map[string]any {
		rec := s.do(user, http.MethodGet, "/api/chats/agents/"+id+"/export", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		return decodeJSON(rec)
	}

	It("exports a project agent as version 5 with project-relative folders and no node ids or home paths", func() {
		pid := newProject("alice", "Acme Launch")
		id := projectAgent("alice", pid, nil)
		file := export("alice", id)
		Expect(file).To(HaveKeyWithValue("version", BeNumerically("==", 5)))
		def := file["agent"].(map[string]any)
		ref := def["projectRef"].(map[string]any)
		Expect(ref).To(And(HaveKeyWithValue("id", pid), HaveKeyWithValue("name", "Acme Launch"), HaveKeyWithValue("slug", "acme-launch")))
		Expect(def).NotTo(HaveKey("project_id"))
		Expect(def["folders"]).To(Equal([]any{
			map[string]any{"path": "files", "mode": "write"},
			map[string]any{"path": "memory", "mode": "read"},
		}))
		Expect(def["builtins"]).To(ConsistOf("project.memory.write"))
		Expect(def["skills"]).To(ConsistOf("review", "ghost-skill"))
		Expect(def["addons"]).To(ConsistOf("office"))
		raw := s.do("alice", http.MethodGet, "/api/chats/agents/"+id+"/export", nil).Body.String()
		Expect(raw).NotTo(ContainSubstring("Projects/"))
		Expect(raw).NotTo(ContainSubstring("/home"))
		Expect(raw).NotTo(ContainSubstring(`"node"`))
		Expect(raw).NotTo(ContainSubstring(`"project_id"`))
	})

	It("exports an agent without a project exactly as before", func() {
		rec := s.do("alice", http.MethodPost, "/api/chats/agents", map[string]any{"name": "Plain", "model": "test-model"})
		file := export("alice", decodeJSON(rec)["id"].(string))
		Expect(file).To(HaveKeyWithValue("version", BeNumerically("==", 1)))
		Expect(file["agent"]).NotTo(HaveKey("projectRef"))
		Expect(file["agent"]).NotTo(HaveKey("folders"))
	})

	It("answers project_required with the referenced name when no project is chosen", func() {
		pid := newProject("alice", "Acme")
		file := export("alice", projectAgent("alice", pid, nil))
		rec := s.do("bob", http.MethodPost, "/api/chats/agents/import", file)
		Expect(rec.Code).To(Equal(http.StatusConflict), rec.Body.String())
		e := decodeJSON(rec)["error"].(map[string]any)
		Expect(e).To(And(HaveKeyWithValue("type", "project_required"), HaveKeyWithValue("project_name", "Acme"), HaveKey("message")))
		Expect(decodeJSON(s.do("bob", http.MethodGet, "/api/chats/agents", nil))["agents"]).To(BeEmpty())
	})

	It("imports into the importer's own project as a project agent with the two roots, keeping tools and grants", func() {
		pid := newProject("alice", "Acme")
		file := export("alice", projectAgent("alice", pid, nil))
		mine := newProject("bob", "Mine")
		rec := s.do("bob", http.MethodPost, "/api/chats/agents/import?project_id="+mine, file)
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		a := decodeJSON(rec)["agent"].(map[string]any)
		Expect(a).To(HaveKeyWithValue("project_id", mine))
		Expect(a["builtins"]).To(ConsistOf("project.memory.write"))
		Expect(a["skills"]).To(ConsistOf("review", "ghost-skill"))
		Expect(a["folders"]).To(HaveLen(2))
		Expect(a["folders"].([]any)[0]).To(HaveKeyWithValue("mode", "write"))
		Expect(a["folders"].([]any)[1]).To(HaveKeyWithValue("mode", "read"))
		// A form field of a multipart import maps the same way.
		rec = upload("bob", "", file, map[string]string{"project_id": mine})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		Expect(decodeJSON(rec)["agent"]).To(HaveKeyWithValue("project_id", mine))
	})

	It("never reads the mapping from the file: top-level fields of a JSON body or an uploaded file cannot map or create", func() {
		pid := newProject("alice", "Acme")
		file := export("alice", projectAgent("alice", pid, nil))
		mine := newProject("bob", "Mine")
		file["project_id"], file["create_project"] = mine, true
		rec := s.do("bob", http.MethodPost, "/api/chats/agents/import", file)
		Expect(rec.Code).To(Equal(http.StatusConflict), rec.Body.String())
		Expect(decodeJSON(rec)["error"]).To(HaveKeyWithValue("type", "project_required"))
		rec = upload("bob", "", file, nil)
		Expect(rec.Code).To(Equal(http.StatusConflict), rec.Body.String())
		Expect(decodeJSON(rec)["error"]).To(HaveKeyWithValue("type", "project_required"))
		Expect(projectCount("bob")).To(Equal(1))
		Expect(decodeJSON(s.do("bob", http.MethodGet, "/api/chats/agents", nil))["agents"]).To(BeEmpty())
	})

	It("lets the request parameter alone decide create_project", func() {
		pid := newProject("alice", "Acme")
		file := export("alice", projectAgent("alice", pid, nil))
		file["create_project"] = true
		// false in the query wins over anything else: no creation, still asks.
		rec := s.do("bob", http.MethodPost, "/api/chats/agents/import?create_project=false", file)
		Expect(rec.Code).To(Equal(http.StatusConflict), rec.Body.String())
		Expect(projectCount("bob")).To(Equal(0))
		rec = upload("bob", "?create_project=false", file, map[string]string{"create_project": "true"})
		Expect(rec.Code).To(Equal(http.StatusConflict), rec.Body.String())
		Expect(projectCount("bob")).To(Equal(0))
		rec = upload("bob", "", file, map[string]string{"create_project": "true"})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		Expect(projectCount("bob")).To(Equal(1))
	})

	It("treats a version 5 file without projectRef as needing a project", func() {
		file := map[string]any{"version": 5, "kind": "localai-agent", "agent": map[string]any{"name": "NoRef", "model": "test-model"}}
		rec := s.do("bob", http.MethodPost, "/api/chats/agents/import", file)
		Expect(rec.Code).To(Equal(http.StatusConflict), rec.Body.String())
		e := decodeJSON(rec)["error"].(map[string]any)
		Expect(e).To(And(HaveKeyWithValue("type", "project_required"), HaveKeyWithValue("project_name", "")))
		Expect(e["message"]).NotTo(ContainSubstring(`""`))
		rec = s.do("bob", http.MethodPost, "/api/chats/agents/import?create_project=true", file)
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		np := s.do("bob", http.MethodGet, "/api/chats/projects/"+decodeJSON(rec)["agent"].(map[string]any)["project_id"].(string), nil)
		Expect(decodeJSON(np)).To(HaveKeyWithValue("name", "NoRef"))
	})

	It("leaves no project behind when the agent is refused: validated before the project is made", func() {
		file := map[string]any{"version": 5, "kind": "localai-agent", "agent": map[string]any{"name": "Gone", "model": "test-model",
			"projectRef": map[string]any{"name": "Acme"}, "mcp_servers": []string{"connector:gone"}}}
		rec := s.do("bob", http.MethodPost, "/api/chats/agents/import?create_project=true", file)
		Expect(rec.Code).To(Equal(http.StatusBadRequest), rec.Body.String())
		Expect(projectCount("bob")).To(Equal(0))
		file["agent"].(map[string]any)["mcp_servers"] = nil
		file["agent"].(map[string]any)["network"] = "bogus"
		rec = s.do("bob", http.MethodPost, "/api/chats/agents/import?create_project=true", file)
		Expect(rec.Code).To(Equal(http.StatusBadRequest), rec.Body.String())
		Expect(projectCount("bob")).To(Equal(0))
	})

	It("removes the project it made when the save fails afterwards", func() {
		// Every name up to the rename limit is taken, which only the save sees.
		Expect(s.do("bob", http.MethodPost, "/api/chats/agents", map[string]any{"name": "Planner", "model": "test-model"}).Code).To(Equal(http.StatusCreated))
		for i := 2; i <= 20; i++ {
			Expect(s.do("bob", http.MethodPost, "/api/chats/agents", map[string]any{"name": fmt.Sprintf("Planner (%d)", i), "model": "test-model"}).Code).To(Equal(http.StatusCreated))
		}
		pid := newProject("alice", "Acme")
		file := export("alice", projectAgent("alice", pid, nil))
		rec := s.do("bob", http.MethodPost, "/api/chats/agents/import?create_project=true", file)
		Expect(rec.Code).To(Equal(http.StatusConflict), rec.Body.String())
		Expect(projectCount("bob")).To(Equal(0))
	})

	It("refuses a bad folder mode and defaults the files mode to read without a write entry", func() {
		pid := newProject("alice", "Acme")
		file := export("alice", projectAgent("alice", pid, nil))
		mine := newProject("bob", "Mine")
		agent := file["agent"].(map[string]any)
		agent["folders"] = []any{map[string]any{"path": "files", "mode": "admin"}}
		Expect(s.do("bob", http.MethodPost, "/api/chats/agents/import?project_id="+mine, file).Code).To(Equal(http.StatusBadRequest))
		agent["folders"] = []any{map[string]any{"path": "memory", "mode": "read"}}
		rec := s.do("bob", http.MethodPost, "/api/chats/agents/import?project_id="+mine, file)
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		Expect(decodeJSON(rec)["agent"].(map[string]any)["folders"].([]any)[0]).To(HaveKeyWithValue("mode", "read"))
	})

	It("collapses the warnings of ignored folder entries into one", func() {
		file := map[string]any{"version": 4, "kind": "localai-agent", "agent": map[string]any{"name": "Many", "model": "test-model",
			"folders": []any{map[string]any{"path": "/A", "mode": "read"}, map[string]any{"path": "/B", "mode": "read"}, map[string]any{"path": "/C", "mode": "read"}}}}
		mine := newProject("bob", "Mine")
		rec := s.do("bob", http.MethodPost, "/api/chats/agents/import?project_id="+mine, file)
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		Expect(decodeJSON(rec)["warnings"]).To(HaveLen(1))
	})

	It("keeps a read files mode of the file", func() {
		pid := newProject("alice", "Acme")
		file := export("alice", projectAgent("alice", pid, nil))
		file["agent"].(map[string]any)["folders"] = []any{map[string]any{"path": "files", "mode": "read"}, map[string]any{"path": "memory", "mode": "write"}}
		mine := newProject("bob", "Mine")
		rec := s.do("bob", http.MethodPost, "/api/chats/agents/import?project_id="+mine, file)
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		f := decodeJSON(rec)["agent"].(map[string]any)["folders"].([]any)
		Expect(f[0]).To(HaveKeyWithValue("mode", "read"))
		Expect(f[1]).To(HaveKeyWithValue("mode", "read"))
	})

	It("refuses another owner's project with 404 and an archived one with 400", func() {
		pid := newProject("alice", "Acme")
		file := export("alice", projectAgent("alice", pid, nil))
		Expect(s.do("bob", http.MethodPost, "/api/chats/agents/import?project_id="+pid, file).Code).To(Equal(http.StatusNotFound))
		Expect(s.do("bob", http.MethodPost, "/api/chats/agents/import?project_id=nope", file).Code).To(Equal(http.StatusNotFound))
		mine := newProject("bob", "Mine")
		Expect(s.do("bob", http.MethodPost, "/api/chats/projects/"+mine+"/archive", nil).Code).To(Equal(http.StatusOK))
		Expect(s.do("bob", http.MethodPost, "/api/chats/agents/import?project_id="+mine, file).Code).To(Equal(http.StatusBadRequest))
		Expect(decodeJSON(s.do("bob", http.MethodGet, "/api/chats/agents", nil))["agents"]).To(BeEmpty())
	})

	It("creates a project from the reference with create_project, twice with distinct slugs", func() {
		pid := newProject("alice", "Acme")
		file := export("alice", projectAgent("alice", pid, nil))
		var slugs []string
		for i := 0; i < 2; i++ {
			rec := s.do("bob", http.MethodPost, "/api/chats/agents/import?create_project=true", file)
			Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
			a := decodeJSON(rec)["agent"].(map[string]any)
			np := s.do("bob", http.MethodGet, "/api/chats/projects/"+a["project_id"].(string), nil)
			Expect(np.Code).To(Equal(http.StatusOK), np.Body.String())
			Expect(decodeJSON(np)).To(HaveKeyWithValue("name", "Acme"))
			slugs = append(slugs, decodeJSON(np)["slug"].(string))
		}
		Expect(slugs[0]).NotTo(Equal(slugs[1]))
		Expect(strings.HasPrefix(slugs[0], "acme")).To(BeTrue())
		Expect(decodeJSON(s.do("bob", http.MethodGet, "/api/chats/projects", nil))["projects"]).To(HaveLen(2))
	})

	It("ignores a project id inside the agent body: only the request decides", func() {
		pid := newProject("alice", "Acme")
		file := export("alice", projectAgent("alice", pid, nil))
		file["agent"].(map[string]any)["project_id"] = pid
		// Bob has no mapping: still project_required, never alice's project.
		Expect(s.do("bob", http.MethodPost, "/api/chats/agents/import", file).Code).To(Equal(http.StatusConflict))
		mine := newProject("bob", "Mine")
		rec := s.do("bob", http.MethodPost, "/api/chats/agents/import?project_id="+mine, file)
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		Expect(decodeJSON(rec)["agent"]).To(HaveKeyWithValue("project_id", mine))
	})

	It("refuses a bare definition that carries project fields", func() {
		for _, body := range []map[string]any{
			{"name": "B", "project_id": "p1"},
			{"name": "B", "projectRef": map[string]any{"name": "A"}},
		} {
			Expect(s.do("alice", http.MethodPost, "/api/chats/agents/import", body).Code).To(Equal(http.StatusBadRequest))
		}
	})

	It("refuses create_project for a file that names no project", func() {
		rec := s.do("alice", http.MethodPost, "/api/chats/agents", map[string]any{"name": "Plain", "model": "test-model"})
		file := export("alice", decodeJSON(rec)["id"].(string))
		Expect(s.do("alice", http.MethodPost, "/api/chats/agents/import?create_project=true", file).Code).To(Equal(http.StatusBadRequest))
	})

	It("imports a plain file as before, with or without a project", func() {
		file := map[string]any{"version": 4, "kind": "localai-agent", "agent": map[string]any{"name": "Old", "model": "test-model", "folders": []any{map[string]any{"path": "/Docs", "mode": "read"}}}}
		rec := s.do("bob", http.MethodPost, "/api/chats/agents/import", file)
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		Expect(decodeJSON(rec)["agent"]).NotTo(HaveKey("project_id"))
		mine := newProject("bob", "Mine")
		rec = s.do("bob", http.MethodPost, "/api/chats/agents/import?project_id="+mine, file)
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		Expect(decodeJSON(rec)["agent"]).To(HaveKeyWithValue("project_id", mine))
		Expect(decodeJSON(rec)["warnings"]).NotTo(BeEmpty())
	})
})

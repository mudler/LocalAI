// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"bytes"
	"context"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Chats agent export, import, templates and history", func() {
	var s *studioServer
	BeforeEach(func() { s = newStudioServer() })

	DescribeTable("require authentication",
		func(method, path string) {
			Expect(s.do("", method, path, nil).Code).To(Equal(http.StatusUnauthorized))
		},
		Entry("export", http.MethodGet, "/api/chats/agents/a1/export"),
		Entry("import", http.MethodPost, "/api/chats/agents/import"),
		Entry("templates", http.MethodGet, "/api/chats/agents/templates"),
		Entry("builtin grants", http.MethodGet, "/api/chats/agents/builtin-grants"),
		Entry("use a template", http.MethodPost, "/api/chats/agents/templates/reviewer/create"),
		Entry("runs", http.MethodGet, "/api/chats/agents/a1/runs"),
	)

	save := func(user string) string {
		rec := s.do(user, http.MethodPost, "/api/chats/agents", map[string]any{
			"name": "Reviewer", "description": "reviews", "instructions": "Be brief.", "model": "test-model",
			"addons": []string{"office"}, "skills": []string{"review"}, "collections": []string{"docs"}, "mcp_servers": []string{"wiki"},
		})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		return decodeJSON(rec)["id"].(string)
	}

	It("exports an agent as a file without ID or owner, and imports it as a new agent, also for another user", func() {
		id := save("alice")
		exp := s.do("alice", http.MethodGet, "/api/chats/agents/"+id+"/export", nil)
		Expect(exp.Code).To(Equal(http.StatusOK))
		Expect(exp.Header().Get("Content-Disposition")).To(ContainSubstring("Reviewer.json"))
		file := decodeJSON(exp)
		Expect(file).To(And(HaveKeyWithValue("version", BeNumerically("==", 1)), HaveKeyWithValue("kind", "localai-agent")))
		def := file["agent"].(map[string]any)
		Expect(def).To(And(HaveKeyWithValue("name", "Reviewer"), HaveKeyWithValue("instructions", "Be brief."), Not(HaveKey("id")), Not(HaveKey("principal"))))
		Expect(def["addons"]).To(ConsistOf("office"))
		Expect(s.do("bob", http.MethodGet, "/api/chats/agents/"+id+"/export", nil).Code).To(Equal(http.StatusNotFound))

		// Back in for the same user: the name is taken, so the copy is numbered.
		imp := s.do("alice", http.MethodPost, "/api/chats/agents/import", file)
		Expect(imp.Code).To(Equal(http.StatusCreated), imp.Body.String())
		res := decodeJSON(imp)
		Expect(res["agent"]).To(And(HaveKeyWithValue("name", "Reviewer (2)"), HaveKeyWithValue("model", "test-model")))
		Expect(res["agent"].(map[string]any)["id"]).NotTo(Equal(id))
		Expect(res["warnings"]).To(HaveLen(1))

		// For another user the name is free.
		bob := decodeJSON(s.do("bob", http.MethodPost, "/api/chats/agents/import", file))
		Expect(bob["agent"]).To(HaveKeyWithValue("name", "Reviewer"))
		Expect(bob).NotTo(HaveKey("warnings"))
		Expect(decodeJSON(s.do("bob", http.MethodGet, "/api/chats/agents", nil))["agents"]).To(HaveLen(1))
	})

	Describe("builtin grants", func() {
		It("lists the declared grants with their sensitivity", func() {
			rec := s.do("alice", http.MethodGet, "/api/chats/agents/builtin-grants", nil)
			Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
			list := decodeJSON(rec)["grants"].([]any)
			byName := map[string]map[string]any{}
			for _, g := range list {
				m := g.(map[string]any)
				Expect(m["title"]).NotTo(BeEmpty())
				Expect(m["description"]).NotTo(BeEmpty())
				byName[m["name"].(string)] = m
			}
			Expect(byName).To(And(HaveKey("memory.write"), HaveKey("skills.write"), HaveKey("pages.write"), HaveKey("dashboard.write"), HaveKey("saved_agents")))
			Expect(byName["skills.write"]["sensitive"]).To(BeTrue())
			Expect(byName["memory.write"]["sensitive"]).To(BeFalse())
		})

		It("saves, keeps, clears, exports and imports the grants of an agent", func() {
			create := s.do("alice", http.MethodPost, "/api/chats/agents", map[string]any{"name": "Maker", "model": "test-model", "builtins": []string{"memory.write", "skills.write"}})
			Expect(create.Code).To(Equal(http.StatusCreated), create.Body.String())
			a := decodeJSON(create)
			id := a["id"].(string)
			Expect(a["builtins"]).To(ConsistOf("memory.write", "skills.write"))

			// A new agent has none, and the field is always present.
			plain := decodeJSON(s.do("alice", http.MethodPost, "/api/chats/agents", map[string]any{"name": "Plain", "model": "test-model"}))
			Expect(plain).To(HaveKeyWithValue("builtins", BeEmpty()))

			// A write that omits the field keeps the grants; an empty list clears them.
			keep := s.do("alice", http.MethodPut, "/api/chats/agents/"+id, map[string]any{"name": "Maker", "model": "test-model", "instructions": "x"})
			Expect(keep.Code).To(Equal(http.StatusOK), keep.Body.String())
			Expect(decodeJSON(keep)["builtins"]).To(ConsistOf("memory.write", "skills.write"))

			exp := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/agents/"+id+"/export", nil))
			Expect(exp["agent"].(map[string]any)["builtins"]).To(ConsistOf("memory.write", "skills.write"))
			imp := s.do("bob", http.MethodPost, "/api/chats/agents/import", exp)
			Expect(imp.Code).To(Equal(http.StatusCreated), imp.Body.String())
			Expect(decodeJSON(imp)["agent"].(map[string]any)["builtins"]).To(ConsistOf("memory.write", "skills.write"))

			clear := s.do("alice", http.MethodPut, "/api/chats/agents/"+id, map[string]any{"name": "Maker", "model": "test-model", "builtins": []string{}})
			Expect(decodeJSON(clear)["builtins"]).To(BeEmpty())
		})

		It("refuses an unknown grant on a write and drops it on an import; an old file imports as before", func() {
			bad := s.do("alice", http.MethodPost, "/api/chats/agents", map[string]any{"name": "Bad", "model": "test-model", "builtins": []string{"root.shell"}})
			Expect(bad.Code).To(Equal(http.StatusBadRequest), bad.Body.String())
			file := map[string]any{"version": 1, "kind": "localai-agent", "agent": map[string]any{"name": "Future", "model": "test-model", "builtins": []string{"memory.write", "from.the.future"}}}
			imp := s.do("alice", http.MethodPost, "/api/chats/agents/import", file)
			Expect(imp.Code).To(Equal(http.StatusCreated), imp.Body.String())
			Expect(decodeJSON(imp)["agent"].(map[string]any)["builtins"]).To(ConsistOf("memory.write"))
			old := s.do("alice", http.MethodPost, "/api/chats/agents/import", map[string]any{"version": 1, "kind": "localai-agent", "agent": map[string]any{"name": "Old", "model": "test-model"}})
			Expect(old.Code).To(Equal(http.StatusCreated), old.Body.String())
			Expect(decodeJSON(old)["agent"].(map[string]any)["builtins"]).To(BeEmpty())
		})
	})

	It("imports a file sent as a multipart form, and drops a model the server does not have", func() {
		file := map[string]any{"version": 1, "kind": "localai-agent", "agent": map[string]any{"name": "Shared", "instructions": "Hi", "model": "model-from-elsewhere"}}
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, err := mw.CreateFormFile("file", "shared.json")
		Expect(err).NotTo(HaveOccurred())
		_, err = fw.Write(mustJSON(file))
		Expect(err).NotTo(HaveOccurred())
		Expect(mw.Close()).To(Succeed())
		req := httptest.NewRequest(http.MethodPost, "/api/chats/agents/import", &buf)
		req.Header.Set(echo.HeaderContentType, mw.FormDataContentType())
		req.AddCookie(&http.Cookie{Name: "session", Value: s.sessions["alice"]})
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		res := decodeJSON(rec)
		Expect(res["agent"]).NotTo(HaveKey("model"))
		Expect(res["warnings"]).To(HaveLen(1))
	})

	It("refuses a file that is not an agent, or from a newer format", func() {
		for _, body := range []any{
			"not json at all",
			map[string]any{"version": 1, "kind": "localai-agent"},
			map[string]any{"version": 9, "kind": "localai-agent", "agent": map[string]any{"name": "x"}},
			map[string]any{"version": 1, "kind": "something-else", "agent": map[string]any{"name": "x"}},
			map[string]any{"version": 1, "kind": "localai-agent", "agent": map[string]any{"name": ""}},
		} {
			Expect(s.do("alice", http.MethodPost, "/api/chats/agents/import", body).Code).To(Equal(http.StatusBadRequest), "%v", body)
		}
		// A bare definition, as POST /api/chats/agents takes, is also welcome.
		Expect(s.do("alice", http.MethodPost, "/api/chats/agents/import", map[string]any{"name": "Bare"}).Code).To(Equal(http.StatusCreated))
	})

	It("offers starter agents and saves one as the user's own", func() {
		list := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/agents/templates", nil))["templates"].([]any)
		ids := []string{}
		for _, t := range list {
			tm := t.(map[string]any)
			ids = append(ids, tm["id"].(string))
			Expect(tm["instructions"]).NotTo(BeEmpty())
		}
		Expect(ids).To(ContainElements("researcher", "reviewer", "digest"))

		rec := s.do("alice", http.MethodPost, "/api/chats/agents/templates/reviewer/create", nil)
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		a := decodeJSON(rec)
		Expect(a).To(HaveKeyWithValue("name", "code-reviewer"))
		Expect(a["addons"]).To(BeEmpty())
		Expect(a["mcp_servers"]).To(BeEmpty())
		// The same template twice conflicts on the name; a name of one's own is fine.
		Expect(s.do("alice", http.MethodPost, "/api/chats/agents/templates/reviewer/create", nil).Code).To(Equal(http.StatusConflict))
		named := s.do("alice", http.MethodPost, "/api/chats/agents/templates/reviewer/create", map[string]any{"name": "my reviewer"})
		Expect(named.Code).To(Equal(http.StatusCreated))
		Expect(s.do("alice", http.MethodPost, "/api/chats/agents/templates/nope/create", nil).Code).To(Equal(http.StatusNotFound))
	})

	It("shows what an agent has run, to its owner only", func() {
		id := save("alice")
		first := decodeJSON(s.do("alice", http.MethodPost, "/api/chats", map[string]any{"agent_id": id}))["id"].(string)
		second := decodeJSON(s.do("alice", http.MethodPost, "/api/chats", map[string]any{"agent_id": id}))["id"].(string)
		s.create("alice") // a chat with no agent is not a run
		Expect(s.do("alice", http.MethodPost, "/api/chats/"+first+"/messages", map[string]any{"text": "go"}).Code).To(Equal(http.StatusOK))
		Eventually(func() int { return len(s.harness.Sent()) }, 10*time.Second).Should(Equal(1))
		s.harness.Gate <- struct{}{}
		Eventually(func() any { return s.detail("alice", first)["last_turn"] }, 10*time.Second).Should(Equal("done"))

		hist := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/agents/"+id+"/runs", nil))
		Expect(hist).To(And(HaveKeyWithValue("total", BeNumerically("==", 2)), HaveKeyWithValue("failed", BeNumerically("==", 0)), HaveKeyWithValue("running", BeNumerically("==", 0))))
		runs := hist["runs"].([]any)
		Expect(runs).To(HaveLen(2))
		byID := map[string]map[string]any{}
		for _, r := range runs {
			byID[r.(map[string]any)["chat_id"].(string)] = r.(map[string]any)
		}
		Expect(byID).To(HaveKey(second))
		Expect(byID[first]).To(HaveKeyWithValue("last_turn", "done"))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/agents/"+id+"/runs?limit=1", nil))["runs"]).To(HaveLen(1))

		Expect(s.do("bob", http.MethodGet, "/api/chats/agents/"+id+"/runs", nil).Code).To(Equal(http.StatusNotFound))
	})

	Describe("an agent with a folder", func() {
		mkdir := func(user, path string) {
			Expect(s.do(user, http.MethodPost, "/api/chats/files/folders", map[string]string{"path": path}).Code).To(Equal(http.StatusOK), path)
		}
		scoped := func(user string) string {
			mkdir(user, "/Projects/Acme")
			rec := s.do(user, http.MethodPost, "/api/chats/agents", map[string]any{"name": "Scoped", "folder": map[string]string{"path": "/Projects/Acme", "mode": "read"}})
			Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
			return decodeJSON(rec)["id"].(string)
		}

		It("follows a readable shared move in responses but reimports its export as unset", func() {
			id := scoped("alice")
			ctx := context.Background()
			f := s.svc.Files()
			ws := f.Store()
			admin := workspace.Actor{Principal: files.AdminPrincipal}
			_, err := ws.Mkdir(ctx, admin, "/shared/Team")
			Expect(err).NotTo(HaveOccurred())
			Expect(ws.Grant(ctx, admin, "alice", "/shared/Team", workspace.ModeRead)).To(Succeed())
			_, err = ws.Move(ctx, admin, session.HomePath("alice")+"/Projects/Acme", "/shared/Team/Acme")
			Expect(err).NotTo(HaveOccurred())
			// A home folder with the same spelling must never receive the imported scope.
			mkdir("alice", "/shared/Team/Acme")
			got := s.do("alice", http.MethodGet, "/api/chats/agents/"+id, nil)
			Expect(got.Code).To(Equal(http.StatusOK))
			Expect(decodeJSON(got)["folder"]).To(Equal(map[string]any{"path": "/shared/Team/Acme", "name": "Acme", "mode": "read", "effective": "read", "state": "ok"}))
			a, err := s.svc.Agent(ctx, "alice", id)
			Expect(err).NotTo(HaveOccurred())
			followed, ok := s.svc.FolderResolver().PathOf(ctx, "alice", a.FolderID)
			Expect(ok).To(BeTrue())
			Expect(followed).To(Equal("/shared/Team/Acme"))
			exp := s.do("alice", http.MethodGet, "/api/chats/agents/"+id+"/export", nil)
			Expect(exp.Code).To(Equal(http.StatusOK))
			file := decodeJSON(exp)
			// Exercise import before asserting the export, to expose silent remapping.
			imp := s.do("alice", http.MethodPost, "/api/chats/agents/import", file)
			Expect(imp.Code).To(Equal(http.StatusCreated), imp.Body.String())
			res := decodeJSON(imp)
			Expect(res["agent"]).To(HaveKeyWithValue("folder", Equal(map[string]any{"mode": "read", "state": "unset"})))
			Expect(res["warnings"]).To(ContainElement("The file names no folder. Choose a folder before you use this agent."))
			Expect(file).To(HaveKeyWithValue("version", BeNumerically("==", 2)))
			Expect(file["agent"]).To(HaveKeyWithValue("folder", Equal(map[string]any{"path": "", "mode": "read"})))
			importedID := res["agent"].(map[string]any)["id"].(string)
			starts := s.harness.Starts()
			chats, err := s.svc.List(ctx, "alice", 100)
			Expect(err).NotTo(HaveOccurred())
			create := s.do("alice", http.MethodPost, "/api/chats", map[string]any{"agent_id": importedID, "model": "test-model"})
			Expect(create.Code).To(Equal(http.StatusConflict))
			Expect(decodeJSON(create)).To(HaveKeyWithValue("error", HaveKeyWithValue("type", "agent_folder_missing")))
			Expect(s.harness.Starts()).To(Equal(starts), "a refused import must not start a harness session")
			after, err := s.svc.List(ctx, "alice", 100)
			Expect(err).NotTo(HaveOccurred())
			Expect(after).To(Equal(chats), "a refused import must not persist a chat")

			Expect(ws.Revoke(ctx, admin, "alice", "/shared/Team")).To(Succeed())
			got = s.do("alice", http.MethodGet, "/api/chats/agents/"+id, nil)
			Expect(decodeJSON(got)["folder"]).To(Equal(map[string]any{"mode": "read", "state": "missing"}))
			file = decodeJSON(s.do("alice", http.MethodGet, "/api/chats/agents/"+id+"/export", nil))
			Expect(file["agent"]).To(HaveKeyWithValue("folder", Equal(map[string]any{"path": "", "mode": "read"})))
		})

		It("saves several folders, shows them in order and exports them as version 3", func() {
			mkdir("alice", "/Projects/Acme")
			mkdir("alice", "/Notes")
			rec := s.do("alice", http.MethodPost, "/api/chats/agents", map[string]any{"name": "Multi",
				"folders": []map[string]string{{"path": "/Projects/Acme", "mode": "write"}, {"path": "/Notes", "mode": "read"}}})
			Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
			a := decodeJSON(rec)
			Expect(a["folders"]).To(HaveLen(2))
			Expect(a["folder"]).To(HaveKeyWithValue("path", "/Projects/Acme"), "folder is the first")
			id := a["id"].(string)

			file := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/agents/"+id+"/export", nil))
			Expect(file).To(HaveKeyWithValue("version", BeNumerically("==", 3)))
			Expect(file["agent"]).To(HaveKeyWithValue("folders", Equal([]any{
				map[string]any{"path": "/Projects/Acme", "mode": "write"}, map[string]any{"path": "/Notes", "mode": "read"}})))
			Expect(file["agent"]).NotTo(HaveKey("folder"))

			mkdir("bob", "/Projects/Acme")
			imp := decodeJSON(s.do("bob", http.MethodPost, "/api/chats/agents/import", file))
			Expect(imp["agent"]).To(HaveKeyWithValue("folders", HaveLen(2)))
			Expect(imp["warnings"]).To(ConsistOf("The folder /Notes was not found. Choose a folder before you use this agent."))
		})

		It("refuses nine folders and keeps the folders when a write has none", func() {
			var nine []map[string]string
			for i := 0; i < 9; i++ {
				p := "/F" + string(rune('A'+i))
				mkdir("alice", p)
				nine = append(nine, map[string]string{"path": p, "mode": "read"})
			}
			Expect(s.do("alice", http.MethodPost, "/api/chats/agents", map[string]any{"name": "Nine", "folders": nine}).Code).To(Equal(http.StatusBadRequest))
			rec := s.do("alice", http.MethodPost, "/api/chats/agents", map[string]any{"name": "Eight", "folders": nine[:8]})
			Expect(rec.Code).To(Equal(http.StatusCreated))
			id := decodeJSON(rec)["id"].(string)
			put := s.do("alice", http.MethodPut, "/api/chats/agents/"+id, map[string]any{"name": "Eight"})
			Expect(decodeJSON(put)["folders"]).To(HaveLen(8))
			cleared := s.do("alice", http.MethodPut, "/api/chats/agents/"+id, map[string]any{"name": "Eight", "folders": []any{}})
			Expect(decodeJSON(cleared)).NotTo(HaveKey("folders"))
		})

		It("exports an agent without a folder as version 1", func() {
			id := save("alice")
			file := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/agents/"+id+"/export", nil))
			Expect(file).To(HaveKeyWithValue("version", BeNumerically("==", 1)))
			Expect(file["agent"]).NotTo(HaveKey("folder"))
		})

		It("exports an agent with a folder as version 2 with the path, never the node", func() {
			id := scoped("alice")
			exp := s.do("alice", http.MethodGet, "/api/chats/agents/"+id+"/export", nil)
			Expect(exp.Code).To(Equal(http.StatusOK))
			file := decodeJSON(exp)
			Expect(file).To(HaveKeyWithValue("version", BeNumerically("==", 2)))
			Expect(file["agent"]).To(HaveKeyWithValue("folder", Equal(map[string]any{"path": "/Projects/Acme", "mode": "read"})))
			Expect(exp.Body.String()).NotTo(ContainSubstring("folder_id"))
			Expect(exp.Body.String()).NotTo(ContainSubstring("/users/"))
		})

		It("exports an agent whose folder is gone as version 2 with the mode", func() {
			id := scoped("alice")
			Expect(s.do("alice", http.MethodDelete, "/api/chats/files?path=%2FProjects%2FAcme", nil).Code).To(BeNumerically("<", 300))
			file := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/agents/"+id+"/export", nil))
			Expect(file).To(HaveKeyWithValue("version", BeNumerically("==", 2)))
			Expect(file["agent"]).To(HaveKeyWithValue("folder", Equal(map[string]any{"path": "", "mode": "read"})))
		})

		It("imports a version 2 file and resolves the folder in the importer's home", func() {
			file := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/agents/"+scoped("alice")+"/export", nil))
			mkdir("bob", "/Projects/Acme")
			imp := s.do("bob", http.MethodPost, "/api/chats/agents/import", file)
			Expect(imp.Code).To(Equal(http.StatusCreated), imp.Body.String())
			res := decodeJSON(imp)
			Expect(res).NotTo(HaveKey("warnings"))
			Expect(res["agent"]).To(HaveKeyWithValue("folder", Equal(map[string]any{"path": "/Projects/Acme", "name": "Acme", "mode": "read", "effective": "read", "state": "ok"})))
		})

		It("imports a version 2 file whose folder is missing as unset with a warning", func() {
			file := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/agents/"+scoped("alice")+"/export", nil))
			imp := s.do("bob", http.MethodPost, "/api/chats/agents/import", file)
			Expect(imp.Code).To(Equal(http.StatusCreated), imp.Body.String())
			res := decodeJSON(imp)
			Expect(res["agent"]).To(HaveKeyWithValue("folder", Equal(map[string]any{"mode": "read", "state": "unset"})))
			Expect(res["warnings"]).To(ConsistOf("The folder /Projects/Acme was not found. Choose a folder before you use this agent."))
		})

		It("refuses a version 2 file whose folder has no valid mode", func() {
			for _, folder := range []map[string]any{{"path": "/A"}, {"path": "/A", "mode": "all"}} {
				body := map[string]any{"version": 2, "kind": "localai-agent", "agent": map[string]any{"name": "x", "folder": folder}}
				Expect(s.do("alice", http.MethodPost, "/api/chats/agents/import", body).Code).To(Equal(http.StatusBadRequest), "%v", folder)
			}
		})

		It("still imports version 1", func() {
			body := map[string]any{"version": 1, "kind": "localai-agent", "agent": map[string]any{"name": "Old"}}
			imp := s.do("alice", http.MethodPost, "/api/chats/agents/import", body)
			Expect(imp.Code).To(Equal(http.StatusCreated), imp.Body.String())
			Expect(decodeJSON(imp)["agent"]).NotTo(HaveKey("folder"))
		})
	})
})

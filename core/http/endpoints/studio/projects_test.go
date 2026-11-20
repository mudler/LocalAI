package studio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/jsonstore"
)

var _ = Describe("projects API", func() {
	ctx := context.Background()
	const alice, bob = "alice", "bob"

	type rig struct {
		svc *studiosvc.Service
		e   *echo.Echo
	}
	newRig := func(withProjects bool) rig {
		deps := studiosvc.Deps{Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: studiotest.NewGateHarness()}
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		deps.Store = st
		if withProjects {
			b, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
			Expect(err).NotTo(HaveOccurred())
			ws, err := jsonstore.New(filepath.Join(GinkgoT().TempDir(), "ws"), b, jsonstore.Options{Admin: files.AdminPrincipal})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { Expect(ws.Close()).To(Succeed()) })
			f := files.New(ws)
			ps, err := projects.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			deps.Files = f
			deps.Projects = projects.NewService(projects.Options{Store: ps, Files: f})
		}
		svc, err := studiosvc.New(deps)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(ctx) })
		h := New(svc, nil)
		e := echo.New()
		const base = "/api/chats"
		e.GET(base+"/projects", h.ListProjects)
		e.POST(base+"/projects", h.CreateProject)
		e.GET(base+"/projects/:id", h.GetProject)
		e.PATCH(base+"/projects/:id", h.UpdateProject)
		e.DELETE(base+"/projects/:id", h.DeleteProject)
		e.POST(base+"/projects/:id/archive", h.ArchiveProject)
		e.POST(base+"/projects/:id/restore", h.RestoreProject)
		e.GET(base+"/projects/:id/memory", h.GetProjectMemory)
		e.PUT(base+"/projects/:id/memory/:file", h.PutProjectMemory)
		e.GET(base+"/projects/:id/chats", h.ListProjectChats)
		e.PUT(base+"/:id/project", h.SetChatProject)
		e.PUT(base+"/:id/projects", h.SetChatPulledProjects)
		return rig{svc, e}
	}
	do := func(r rig, user, method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		c := r.e.NewContext(req, w)
		c.Set("auth_user", &auth.User{ID: user, Role: auth.RoleUser})
		// Route through the router so params and ordering are the real ones.
		r.e.Router().Find(method, strings.SplitN(path, "?", 2)[0], c)
		Expect(c.Handler()(c)).To(Succeed())
		return w
	}
	create := func(r rig, user, name string) schema.ChatsProject {
		w := do(r, user, http.MethodPost, "/api/chats/projects", `{"name":"`+name+`"}`)
		Expect(w.Code).To(Equal(http.StatusCreated), w.Body.String())
		var p schema.ChatsProject
		Expect(json.Unmarshal(w.Body.Bytes(), &p)).To(Succeed())
		return p
	}
	errType := func(w *httptest.ResponseRecorder) string {
		var e schema.ErrorResponse
		Expect(json.Unmarshal(w.Body.Bytes(), &e)).To(Succeed())
		Expect(e.Error).NotTo(BeNil())
		return e.Error.Type
	}

	It("creates a project with a slug and lists only the caller's own", func() {
		r := newRig(true)
		p := create(r, alice, "Acme Launch")
		Expect(p.Slug).To(Equal("acme-launch"))
		Expect(p.Status).To(Equal("active"))
		create(r, bob, "Other")

		w := do(r, alice, http.MethodGet, "/api/chats/projects", "")
		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		var list schema.ChatsProjectList
		Expect(json.Unmarshal(w.Body.Bytes(), &list)).To(Succeed())
		Expect(list.Projects).To(HaveLen(1))
		Expect(list.Projects[0].ID).To(Equal(p.ID))
	})

	It("renames with PATCH and hides another user's project as not found", func() {
		r := newRig(true)
		p := create(r, alice, "Acme")
		w := do(r, alice, http.MethodPatch, "/api/chats/projects/"+p.ID, `{"name":"Acme 2","defaults":{"skills":["a"]}}`)
		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		var got schema.ChatsProject
		Expect(json.Unmarshal(w.Body.Bytes(), &got)).To(Succeed())
		Expect(got.Name).To(Equal("Acme 2"))
		Expect(got.Defaults.Skills).To(Equal([]string{"a"}))

		for _, m := range []string{http.MethodGet, http.MethodDelete} {
			w = do(r, bob, m, "/api/chats/projects/"+p.ID, "")
			Expect(w.Code).To(Equal(http.StatusNotFound), m)
		}
		w = do(r, bob, http.MethodPatch, "/api/chats/projects/"+p.ID, `{"name":"x"}`)
		Expect(w.Code).To(Equal(http.StatusNotFound))
		Expect(w.Body.String()).To(ContainSubstring("project not found"))
	})

	It("refuses an attached entry outside a limit with a 400 naming the tool and the limit", func() {
		r := newRig(true)
		p := create(r, alice, "Acme")
		w := do(r, alice, http.MethodPatch, "/api/chats/projects/"+p.ID, `{"defaults":{"addons":["a"]},"attached":{"addons":["z"]}}`)
		Expect(w.Code).To(Equal(http.StatusBadRequest), w.Body.String())
		Expect(w.Body.String()).To(ContainSubstring(`attached addons \"z\" is outside the limit defaults.addons`))
		w = do(r, alice, http.MethodGet, "/api/chats/projects/"+p.ID, "")
		var got schema.ChatsProject
		Expect(json.Unmarshal(w.Body.Bytes(), &got)).To(Succeed())
		Expect(got.Defaults.Addons).To(BeEmpty(), "a refused write changes nothing")
	})

	It("stores attached lists and returns them on read and list", func() {
		r := newRig(true)
		p := create(r, alice, "Acme")
		w := do(r, alice, http.MethodPatch, "/api/chats/projects/"+p.ID, `{"defaults":{"addons":["a","b"]},"attached":{"addons":["a"],"collections":["k"],"mcp_servers":["m"],"connectors":["c"],"skills":["s"]}}`)
		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		for _, path := range []string{"/api/chats/projects/" + p.ID, "/api/chats/projects"} {
			w = do(r, alice, http.MethodGet, path, "")
			Expect(w.Code).To(Equal(http.StatusOK))
			Expect(w.Body.String()).To(ContainSubstring(`"attached":{`), path)
		}
		var got schema.ChatsProject
		w = do(r, alice, http.MethodGet, "/api/chats/projects/"+p.ID, "")
		Expect(json.Unmarshal(w.Body.Bytes(), &got)).To(Succeed())
		Expect(got.Attached).To(Equal(schema.ChatsProjectDefaults{Skills: []string{"s"}, Addons: []string{"a"}, Connectors: []string{"c"}, MCPServers: []string{"m"}, Collections: []string{"k"}}))
		Expect(got.Defaults.Addons).To(Equal([]string{"a", "b"}))
		Expect(do(r, bob, http.MethodPatch, "/api/chats/projects/"+p.ID, `{"attached":{"addons":["a"]}}`).Code).To(Equal(http.StatusNotFound))
	})

	It("refuses an invalid body and a taken name", func() {
		r := newRig(true)
		Expect(do(r, alice, http.MethodPost, "/api/chats/projects", `{"name":""}`).Code).To(Equal(http.StatusBadRequest))
		Expect(do(r, alice, http.MethodPost, "/api/chats/projects", `{`).Code).To(Equal(http.StatusBadRequest))
	})

	It("archives and restores", func() {
		r := newRig(true)
		p := create(r, alice, "Acme")
		w := do(r, alice, http.MethodPost, "/api/chats/projects/"+p.ID+"/archive", "")
		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		var got schema.ChatsProject
		Expect(json.Unmarshal(w.Body.Bytes(), &got)).To(Succeed())
		Expect(got.Status).To(Equal("archived"))
		w = do(r, alice, http.MethodPost, "/api/chats/projects/"+p.ID+"/restore", "")
		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(json.Unmarshal(w.Body.Bytes(), &got)).To(Succeed())
		Expect(got.Status).To(Equal("active"))
		Expect(do(r, bob, http.MethodPost, "/api/chats/projects/"+p.ID+"/archive", "").Code).To(Equal(http.StatusNotFound))
	})

	It("deletes a project", func() {
		r := newRig(true)
		p := create(r, alice, "Acme")
		Expect(do(r, alice, http.MethodDelete, "/api/chats/projects/"+p.ID, "").Code).To(Equal(http.StatusNoContent))
		Expect(do(r, alice, http.MethodGet, "/api/chats/projects/"+p.ID, "").Code).To(Equal(http.StatusNotFound))
	})

	It("puts a chat in a project, and refuses another user's project", func() {
		r := newRig(true)
		mine := create(r, alice, "Mine")
		theirs := create(r, bob, "Theirs")
		rec, err := r.svc.Create(ctx, alice, studiosvc.CreateInput{Model: "m"})
		Expect(err).NotTo(HaveOccurred())

		w := do(r, alice, http.MethodPut, "/api/chats/"+rec.ID+"/project", `{"project_id":"`+theirs.ID+`"}`)
		Expect(w.Code).To(Equal(http.StatusNotFound))
		w = do(r, alice, http.MethodPut, "/api/chats/"+rec.ID+"/project", `{"project_id":"`+mine.ID+`"}`)
		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		var conv schema.ChatsConversation
		Expect(json.Unmarshal(w.Body.Bytes(), &conv)).To(Succeed())
		Expect(conv.ProjectID).To(Equal(mine.ID))

		w = do(r, alice, http.MethodGet, "/api/chats/projects/"+mine.ID+"/chats", "")
		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		var list schema.ChatsConversationList
		Expect(json.Unmarshal(w.Body.Bytes(), &list)).To(Succeed())
		Expect(list.Conversations).To(HaveLen(1))
		Expect(list.Conversations[0].ID).To(Equal(rec.ID))
		Expect(do(r, bob, http.MethodGet, "/api/chats/projects/"+mine.ID+"/chats", "").Code).To(Equal(http.StatusNotFound))

		w = do(r, alice, http.MethodGet, "/api/chats/projects/"+mine.ID, "")
		var got schema.ChatsProject
		Expect(json.Unmarshal(w.Body.Bytes(), &got)).To(Succeed())
		Expect(got.ChatCount).To(Equal(1))

		w = do(r, alice, http.MethodPut, "/api/chats/"+rec.ID+"/project", `{"project_id":""}`)
		Expect(w.Code).To(Equal(http.StatusOK))
	})

	It("sets the pulled projects, and refuses the home project", func() {
		r := newRig(true)
		home := create(r, alice, "Home")
		other := create(r, alice, "Other")
		theirs := create(r, bob, "Theirs")
		rec, err := r.svc.Create(ctx, alice, studiosvc.CreateInput{Model: "m", ProjectID: home.ID})
		Expect(err).NotTo(HaveOccurred())

		w := do(r, alice, http.MethodPut, "/api/chats/"+rec.ID+"/projects", `{"project_ids":["`+home.ID+`"]}`)
		Expect(w.Code).To(Equal(http.StatusBadRequest), w.Body.String())
		w = do(r, alice, http.MethodPut, "/api/chats/"+rec.ID+"/projects", `{"project_ids":["`+theirs.ID+`"]}`)
		Expect(w.Code).To(Equal(http.StatusNotFound))
		w = do(r, alice, http.MethodPut, "/api/chats/"+rec.ID+"/projects", `{"project_ids":["`+other.ID+`"]}`)
		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		var conv schema.ChatsConversation
		Expect(json.Unmarshal(w.Body.Bytes(), &conv)).To(Succeed())
		Expect(conv.PulledProjects).To(Equal([]string{other.ID}))
	})

	It("reads the memory, rejects a bad file name and writes a good one", func() {
		r := newRig(true)
		p := create(r, alice, "Acme")
		path := "/api/chats/projects/" + p.ID + "/memory/"

		w := do(r, alice, http.MethodPut, path+"..%2Fevil.md", `{"content":"x"}`)
		Expect(w.Code).To(Equal(http.StatusBadRequest))
		w = do(r, alice, http.MethodPut, path+"Notes.txt", `{"content":"x"}`)
		Expect(w.Code).To(Equal(http.StatusBadRequest))
		w = do(r, alice, http.MethodPut, path+"food.md", `{"content":"`+strings.Repeat("a", 17<<10)+`"}`)
		Expect(w.Code).To(Equal(http.StatusRequestEntityTooLarge))
		Expect(do(r, bob, http.MethodPut, path+"food.md", `{"content":"x"}`).Code).To(Equal(http.StatusNotFound))

		w = do(r, alice, http.MethodPut, path+"food.md", `{"content":"likes soup"}`)
		Expect(w.Code).To(Equal(http.StatusNoContent), w.Body.String())
		w = do(r, alice, http.MethodPut, path+"index.md", `{"content":"# Memory\n- food.md"}`)
		Expect(w.Code).To(Equal(http.StatusNoContent), w.Body.String())

		w = do(r, alice, http.MethodGet, "/api/chats/projects/"+p.ID+"/memory", "")
		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		var mem schema.ChatsProjectMemory
		Expect(json.Unmarshal(w.Body.Bytes(), &mem)).To(Succeed())
		Expect(mem.Index).To(ContainSubstring("food.md"))
		Expect(mem.Files).To(HaveLen(1))
		Expect(mem.Files[0].Name).To(Equal("food.md"))
		Expect(do(r, bob, http.MethodGet, "/api/chats/projects/"+p.ID+"/memory", "").Code).To(Equal(http.StatusNotFound))
	})

	It("answers 404 on every route to a user who does not own the project or the chat", func() {
		r := newRig(true)
		mine := create(r, alice, "Mine")
		bobs := create(r, bob, "Bobs")
		aliceChat, err := r.svc.Create(ctx, alice, studiosvc.CreateInput{Model: "m", ProjectID: mine.ID})
		Expect(err).NotTo(HaveOccurred())
		bobChat, err := r.svc.Create(ctx, bob, studiosvc.CreateInput{Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		base := "/api/chats/projects/" + mine.ID
		for _, rt := range [][3]string{
			{http.MethodGet, base, ""},
			{http.MethodPatch, base, `{"name":"x"}`},
			{http.MethodPost, base + "/archive", ""},
			{http.MethodPost, base + "/restore", ""},
			{http.MethodGet, base + "/memory", ""},
			{http.MethodPut, base + "/memory/food.md", `{"content":"x"}`},
			{http.MethodGet, base + "/chats", ""},
			// bob's own chat, alice's project
			{http.MethodPut, "/api/chats/" + bobChat.ID + "/project", `{"project_id":"` + mine.ID + `"}`},
			{http.MethodPut, "/api/chats/" + bobChat.ID + "/projects", `{"project_ids":["` + mine.ID + `"]}`},
			// alice's chat, bob's own project
			{http.MethodPut, "/api/chats/" + aliceChat.ID + "/project", `{"project_id":"` + bobs.ID + `"}`},
			{http.MethodPut, "/api/chats/" + aliceChat.ID + "/projects", `{"project_ids":["` + bobs.ID + `"]}`},
			// the last, so nothing is gone before the others ran
			{http.MethodDelete, base, ""},
		} {
			w := do(r, bob, rt[0], rt[1], rt[2])
			Expect(w.Code).To(Equal(http.StatusNotFound), rt[0]+" "+rt[1]+": "+w.Body.String())
		}
		got, err := r.svc.Get(ctx, alice, aliceChat.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.ProjectID).To(Equal(mine.ID), "the chat's project is unchanged")
		Expect(got.PulledProjects).To(BeEmpty())
		got, err = r.svc.Get(ctx, bob, bobChat.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.ProjectID).To(BeEmpty())
		Expect(got.PulledProjects).To(BeEmpty())
		Expect(do(r, alice, http.MethodGet, base, "").Code).To(Equal(http.StatusOK), "alice's project is still there")
	})

	It("accepts a note under the cap whose JSON escaping is long", func() {
		r := newRig(true)
		p := create(r, alice, "Acme")
		body, err := json.Marshal(map[string]string{"content": strings.Repeat("\x01", 16<<10)})
		Expect(err).NotTo(HaveOccurred())
		Expect(len(body)).To(BeNumerically(">", 4*16<<10+1024))
		w := do(r, alice, http.MethodPut, "/api/chats/projects/"+p.ID+"/memory/escaped.md", string(body))
		Expect(w.Code).To(Equal(http.StatusNoContent), w.Body.String())
	})

	It("writes the memory of an archived project", func() {
		r := newRig(true)
		p := create(r, alice, "Acme")
		Expect(do(r, alice, http.MethodPost, "/api/chats/projects/"+p.ID+"/archive", "").Code).To(Equal(http.StatusOK))
		w := do(r, alice, http.MethodPut, "/api/chats/projects/"+p.ID+"/memory/old.md", `{"content":"x"}`)
		Expect(w.Code).To(Equal(http.StatusNoContent), w.Body.String())
	})

	It("answers projects_off with a 409 when the service is missing", func() {
		r := newRig(false)
		for _, rt := range [][2]string{
			{http.MethodGet, "/api/chats/projects"}, {http.MethodPost, "/api/chats/projects"},
			{http.MethodGet, "/api/chats/projects/x"}, {http.MethodGet, "/api/chats/projects/x/memory"},
			{http.MethodPut, "/api/chats/x/project"}, {http.MethodPut, "/api/chats/x/projects"},
		} {
			w := do(r, alice, rt[0], rt[1], `{}`)
			Expect(w.Code).To(Equal(http.StatusConflict), rt[0]+" "+rt[1])
			Expect(errType(w)).To(Equal("projects_off"))
		}
	})
})

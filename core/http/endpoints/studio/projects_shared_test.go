package studio

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/jsonstore"
)

// leakyStore fails the open of a manifest with the text of an object store.
type leakyStore struct{ workspace.Store }

func (l leakyStore) Open(ctx context.Context, a workspace.Actor, p string) (io.ReadSeekCloser, workspace.Node, error) {
	if strings.HasSuffix(p, "/project.json") {
		return nil, workspace.Node{}, errors.New("open /var/blobs/secret/abc: denied; s3://host/bucket/key")
	}
	return l.Store.Open(ctx, a, p)
}

type wrappedHost struct {
	*files.Service
	st workspace.Store
}

func (h wrappedHost) Store() workspace.Store { return h.st }

var _ = Describe("projects shared with a person API", func() {
	ctx := context.Background()
	const alice, bob, carol = "alice-account", "bob-account", "carol-account"
	admin := workspace.Actor{Principal: files.AdminPrincipal}

	type rig struct {
		svc *studiosvc.Service
		e   *echo.Echo
		f   *files.Service
	}
	var wrap func(workspace.Store) workspace.Store
	newRig := func() rig {
		deps := studiosvc.Deps{Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: studiotest.NewGateHarness()}
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		deps.Store = st
		b, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
		Expect(err).NotTo(HaveOccurred())
		ws, err := jsonstore.New(filepath.Join(GinkgoT().TempDir(), "ws"), b, jsonstore.Options{Admin: files.AdminPrincipal})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(ws.Close()).To(Succeed()) })
		f := files.New(ws)
		ps, err := projects.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		names := map[string]string{alice: "Alice Rossi", carol: "Carol Bianchi", bob: "Bob"}
		deps.Files = f
		var host projects.FileHost = f
		if wrap != nil {
			host = wrappedHost{Service: f, st: wrap(f.Store())}
		}
		deps.Projects = projects.NewService(projects.Options{Store: ps, Files: host, OwnerName: func(_ context.Context, p string) string { return names[p] }})
		svc, err := studiosvc.New(deps)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(ctx) })
		h := New(svc, nil)
		e := echo.New()
		const base = "/api/chats"
		// The same order as the real route table: static "shared" first.
		e.GET(base+"/projects", h.ListProjects)
		e.POST(base+"/projects", h.CreateProject)
		e.GET(base+"/projects/shared", h.ListSharedProjects)
		e.GET(base+"/projects/shared/:ref", h.GetSharedProject)
		e.GET(base+"/projects/shared/:ref/memory", h.GetSharedProjectMemory)
		e.GET(base+"/projects/:id", h.GetProject)
		e.PUT(base+"/:id/projects", h.SetChatPulledProjects)
		return rig{svc, e, f}
	}
	do := func(r rig, user, method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		c := r.e.NewContext(req, w)
		if user != "" {
			c.Set("auth_user", &auth.User{ID: user, Role: auth.RoleUser})
		}
		r.e.Router().Find(method, req.URL.EscapedPath(), c)
		Expect(c.Handler()(c)).To(Succeed())
		return w
	}
	create := func(r rig, user, name string) projects.Project {
		p, err := r.svc.Projects().Create(ctx, r.svc.Principal(user), projects.CreateInput{Name: name, Description: "about " + name})
		Expect(err).NotTo(HaveOccurred())
		return p
	}
	grant := func(r rig, who, owner, rel string, m workspace.Mode) {
		abs, err := r.f.Abs(owner, rel)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.f.Store().Grant(ctx, admin, who, abs, m)).To(Succeed())
	}
	ref := func(p projects.Project) string { return projects.SharedRef(p.Node) }
	list := func(r rig, user string) []schema.ChatsSharedProject {
		w := do(r, user, http.MethodGet, "/api/chats/projects/shared", "")
		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		var out schema.ChatsSharedProjectList
		Expect(json.Unmarshal(w.Body.Bytes(), &out)).To(Succeed())
		return out.Projects
	}

	It("lists only the projects shared with the caller, and never another person's unshared project", func() {
		r := newRig()
		shared := create(r, alice, "Acme")
		create(r, alice, "Private")
		create(r, bob, "Mine")
		other := create(r, carol, "Elsewhere")
		grant(r, bob, alice, "Projects/"+shared.Slug, workspace.ModeRead)
		grant(r, carol, alice, "Projects/"+shared.Slug, workspace.ModeWrite)
		_ = other
		got := list(r, bob)
		Expect(got).To(HaveLen(1))
		Expect(got[0]).To(Equal(schema.ChatsSharedProject{Ref: ref(shared), Name: "Acme", Description: "about Acme", Status: "active", Mode: "read", OwnerName: "Alice Rossi"}))
		Expect(list(r, carol)[0].Mode).To(Equal("write"))
		Expect(list(r, alice)).To(BeEmpty())
	})

	It("answers an empty list when nothing is shared", func() {
		r := newRig()
		w := do(r, bob, http.MethodGet, "/api/chats/projects/shared", "")
		Expect(w.Body.String()).To(ContainSubstring(`"projects":[]`))
	})

	It("never shows an account, a path or the manifest id in the JSON", func() {
		r := newRig()
		p := create(r, alice, "Acme")
		grant(r, bob, alice, "Projects/"+p.Slug, workspace.ModeWrite)
		for _, path := range []string{"/api/chats/projects/shared", "/api/chats/projects/shared/" + ref(p), "/api/chats/projects/shared/" + ref(p) + "/memory"} {
			body := do(r, bob, http.MethodGet, path, "").Body.String()
			Expect(body).NotTo(ContainSubstring("/users/"), path)
			Expect(body).NotTo(ContainSubstring(alice), path)
			Expect(body).NotTo(ContainSubstring(p.ID), path)
			Expect(body).NotTo(ContainSubstring("workspace"), path)
		}
		Expect(do(r, bob, http.MethodGet, "/api/chats/projects/shared", "").Body.String()).NotTo(ContainSubstring(`"id"`))
		b, err := json.Marshal(list(r, bob))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(b)).NotTo(ContainSubstring(p.ID))
		Expect(string(b)).NotTo(ContainSubstring("defaults"))
	})

	It("reads one shared project by its reference, with the colon plain or encoded", func() {
		r := newRig()
		p := create(r, alice, "Acme")
		grant(r, bob, alice, "Projects/"+p.Slug, workspace.ModeRead)
		for _, ref := range []string{ref(p), strings.Replace(ref(p), ":", "%3A", 1)} {
			w := do(r, bob, http.MethodGet, "/api/chats/projects/shared/"+ref, "")
			Expect(w.Code).To(Equal(http.StatusOK), ref+" "+w.Body.String())
			var got schema.ChatsSharedProject
			Expect(json.Unmarshal(w.Body.Bytes(), &got)).To(Succeed())
			Expect(got.Ref).To(Equal(projects.SharedRef(p.Node)))
			Expect(got.Name).To(Equal("Acme"))
		}
	})

	It("answers the same 404 for an unknown reference, an unshared project and a malformed one", func() {
		r := newRig()
		p := create(r, alice, "Acme")
		mine := create(r, bob, "Mine")
		var bodies []string
		for _, path := range []string{ref(p), "s:99999", ref(mine), "s:0", "s:x", p.ID, "s%3A" + strings.TrimPrefix(ref(p), "s:")} {
			w := do(r, bob, http.MethodGet, "/api/chats/projects/shared/"+path, "")
			Expect(w.Code).To(Equal(http.StatusNotFound), path+" "+w.Body.String())
			bodies = append(bodies, w.Body.String())
			m := do(r, bob, http.MethodGet, "/api/chats/projects/shared/"+path+"/memory", "")
			Expect(m.Code).To(Equal(http.StatusNotFound), path)
			Expect(m.Body.String()).To(Equal(w.Body.String()), path)
		}
		for _, b := range bodies {
			Expect(b).To(Equal(bodies[0]))
		}
	})

	It("reads the memory as the viewer through the grant", func() {
		r := newRig()
		p := create(r, alice, "Acme")
		_, err := r.f.WriteHome(ctx, alice, "Projects/"+p.Slug+"/memory/index.md", "the owner's memory", "t", 1<<20)
		Expect(err).NotTo(HaveOccurred())
		_, err = r.f.WriteHome(ctx, alice, "Projects/"+p.Slug+"/memory/food.md", "pasta", "t", 1<<20)
		Expect(err).NotTo(HaveOccurred())
		_, err = r.f.WriteHome(ctx, alice, "Projects/"+p.Slug+"/memory/notes.txt", "not a topic", "t", 1<<20)
		Expect(err).NotTo(HaveOccurred())
		path := "/api/chats/projects/shared/" + ref(p) + "/memory"
		Expect(do(r, bob, http.MethodGet, path, "").Code).To(Equal(http.StatusNotFound), "no grant")
		grant(r, bob, alice, "Projects/"+p.Slug, workspace.ModeRead)
		w := do(r, bob, http.MethodGet, path, "")
		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		var got schema.ChatsSharedProjectMemory
		Expect(json.Unmarshal(w.Body.Bytes(), &got)).To(Succeed())
		Expect(got.Index).To(Equal("the owner's memory"))
		Expect(got.Files).To(Equal([]string{"food.md"}))
		// Another person with no grant gets the same 404: the memory is read
		// with the grants of the caller, not as the owner.
		Expect(do(r, carol, http.MethodGet, path, "").Code).To(Equal(http.StatusNotFound))
	})

	It("lists an archived project with its status", func() {
		r := newRig()
		p := create(r, alice, "Acme")
		grant(r, bob, alice, "Projects/"+p.Slug, workspace.ModeRead)
		_, err := r.svc.Projects().Archive(ctx, alice, p.ID)
		Expect(err).NotTo(HaveOccurred())
		got := list(r, bob)
		Expect(got).To(HaveLen(1))
		Expect(got[0].Status).To(Equal("archived"))
		w := do(r, bob, http.MethodGet, "/api/chats/projects/shared/"+ref(p), "")
		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
	})

	It("lists a broken project with its status and the reason", func() {
		r := newRig()
		p := create(r, alice, "Acme")
		grant(r, bob, alice, "Projects/"+p.Slug, workspace.ModeRead)
		_, err := r.f.WriteHome(ctx, alice, "Projects/"+p.Slug+"/project.json", "{not json", "t", 1<<20)
		Expect(err).NotTo(HaveOccurred())
		got := list(r, bob)
		Expect(got).To(HaveLen(1))
		Expect(got[0].Status).To(Equal("broken"))
		Expect(got[0].Error).NotTo(BeEmpty())
		Expect(got[0].Ref).To(Equal(ref(p)))
		w := do(r, bob, http.MethodGet, "/api/chats/projects/shared/"+ref(p), "")
		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		Expect(w.Body.String()).To(ContainSubstring(`"broken"`))
	})

	It("stops listing a project at once when the grant is revoked", func() {
		r := newRig()
		p := create(r, alice, "Acme")
		grant(r, bob, alice, "Projects/"+p.Slug, workspace.ModeRead)
		Expect(list(r, bob)).To(HaveLen(1))
		abs, err := r.f.Abs(alice, "Projects/"+p.Slug)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.f.Store().Revoke(ctx, admin, bob, abs)).To(Succeed())
		Expect(list(r, bob)).To(BeEmpty())
		Expect(do(r, bob, http.MethodGet, "/api/chats/projects/shared/"+ref(p), "").Code).To(Equal(http.StatusNotFound))
	})

	It("is not captured by the project id route", func() {
		r := newRig()
		w := do(r, bob, http.MethodGet, "/api/chats/projects/shared", "")
		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(w.Body.String()).To(ContainSubstring(`"projects"`))
	})

	It("behaves like the other project routes when authentication is off", func() {
		r := newRig()
		p := create(r, alice, "Acme")
		grant(r, bob, alice, "Projects/"+p.Slug, workspace.ModeRead)
		w := do(r, "", http.MethodGet, "/api/chats/projects/shared", "")
		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		Expect(w.Body.String()).To(ContainSubstring(`"projects":[]`))
		Expect(do(r, "", http.MethodGet, "/api/chats/projects/shared/"+ref(p), "").Code).To(Equal(http.StatusNotFound))
	})

	It("pulls a reference from the list into a chat, and refuses a manifest id", func() {
		r := newRig()
		p := create(r, alice, "Acme")
		grant(r, bob, alice, "Projects/"+p.Slug, workspace.ModeRead)
		Expect(r.f.EnsureHome(ctx, bob)).To(Succeed())
		rec, err := r.svc.Create(ctx, bob, studiosvc.CreateInput{Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		got := list(r, bob)
		w := do(r, bob, http.MethodPut, "/api/chats/"+rec.ID+"/projects", `{"project_ids":["`+got[0].Ref+`"]}`)
		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		Expect(w.Body.String()).To(ContainSubstring(got[0].Ref))
		w = do(r, bob, http.MethodPut, "/api/chats/"+rec.ID+"/projects", `{"project_ids":["`+p.ID+`"]}`)
		Expect(w.Code).To(Equal(http.StatusNotFound), w.Body.String())
	})

	It("answers 404 for the memory of an archived or broken project", func() {
		r := newRig()
		p := create(r, alice, "Acme")
		grant(r, bob, alice, "Projects/"+p.Slug, workspace.ModeRead)
		path := "/api/chats/projects/shared/" + ref(p) + "/memory"
		Expect(do(r, bob, http.MethodGet, path, "").Code).To(Equal(http.StatusOK))
		_, err := r.svc.Projects().Archive(ctx, alice, p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(do(r, bob, http.MethodGet, path, "").Code).To(Equal(http.StatusNotFound))
		_, err = r.f.WriteHome(ctx, alice, "Projects/"+p.Slug+"/project.json", "{not json", "t", 1<<20)
		Expect(err).NotTo(HaveOccurred())
		Expect(do(r, bob, http.MethodGet, path, "").Code).To(Equal(http.StatusNotFound))
	})

	It("reads a missing index.md as an empty index", func() {
		r := newRig()
		p := create(r, alice, "Acme")
		grant(r, bob, alice, "Projects/"+p.Slug, workspace.ModeRead)
		abs, err := r.f.Abs(alice, "Projects/"+p.Slug+"/memory/index.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(r.f.Store().Delete(ctx, workspace.Actor{Principal: files.AdminPrincipal}, abs)).To(Succeed())
		w := do(r, bob, http.MethodGet, "/api/chats/projects/shared/"+ref(p)+"/memory", "")
		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		Expect(w.Body.String()).To(ContainSubstring(`"index":""`))
	})

	It("never shows the text of a store failure in the list, the detail or the pull-in", func() {
		// The data is built in the rig below, so the failure is on from the start.
		wrap = func(s workspace.Store) workspace.Store { return leakyStore{s} }
		DeferCleanup(func() { wrap = nil })
		r := newRig()
		q := create(r, alice, "Acme")
		grant(r, bob, alice, "Projects/"+q.Slug, workspace.ModeRead)
		Expect(r.f.EnsureHome(ctx, bob)).To(Succeed())
		rec, err := r.svc.Create(ctx, bob, studiosvc.CreateInput{Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		for _, c := range [][3]string{
			{http.MethodGet, "/api/chats/projects/shared", ""},
			{http.MethodGet, "/api/chats/projects/shared/" + ref(q), ""},
			{http.MethodGet, "/api/chats/projects/shared/" + ref(q) + "/memory", ""},
			{http.MethodPut, "/api/chats/" + rec.ID + "/projects", `{"project_ids":["` + ref(q) + `"]}`},
		} {
			w := do(r, bob, c[0], c[1], c[2])
			Expect(w.Body.String()).NotTo(Or(ContainSubstring("secret"), ContainSubstring("s3://"), ContainSubstring("/var/")), c[1]+" "+w.Body.String())
			Expect(w.Body.String()).NotTo(ContainSubstring(`"broken"`), c[1])
		}
		Expect(do(r, bob, http.MethodGet, "/api/chats/projects/shared", "").Body.String()).To(ContainSubstring(`"projects":[]`))
	})
})

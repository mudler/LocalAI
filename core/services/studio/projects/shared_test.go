package projects_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/jsonstore"
)

// countingStore counts the calls that reach the index of projects.
type countingStore struct {
	projects.Store
	calls atomic.Int64
}

func (c *countingStore) Put(ctx context.Context, p projects.Project) error {
	c.calls.Add(1)
	return c.Store.Put(ctx, p)
}
func (c *countingStore) Get(ctx context.Context, o, id string) (projects.Project, error) {
	c.calls.Add(1)
	return c.Store.Get(ctx, o, id)
}
func (c *countingStore) List(ctx context.Context, o string) ([]projects.Project, error) {
	c.calls.Add(1)
	return c.Store.List(ctx, o)
}
func (c *countingStore) Delete(ctx context.Context, o, id string) error {
	c.calls.Add(1)
	return c.Store.Delete(ctx, o, id)
}

// countingWS counts the calls that read the workspace.
type countingWS struct {
	workspace.Store
	mu    sync.Mutex
	calls map[string]int
}

func (c *countingWS) hit(n string) { c.mu.Lock(); c.calls[n]++; c.mu.Unlock() }
func (c *countingWS) total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := 0
	for _, v := range c.calls {
		t += v
	}
	return t
}
func (c *countingWS) reset() { c.mu.Lock(); c.calls = map[string]int{}; c.mu.Unlock() }
func (c *countingWS) Stat(ctx context.Context, a workspace.Actor, p string) (workspace.Node, error) {
	c.hit("Stat")
	return c.Store.Stat(ctx, a, p)
}
func (c *countingWS) List(ctx context.Context, a workspace.Actor, p string) ([]workspace.Node, error) {
	c.hit("List")
	return c.Store.List(ctx, a, p)
}
func (c *countingWS) NodeByID(ctx context.Context, a workspace.Actor, id int64) (workspace.Node, error) {
	c.hit("NodeByID")
	return c.Store.NodeByID(ctx, a, id)
}
func (c *countingWS) View(ctx context.Context, a workspace.Actor, m []workspace.MountRequest) (workspace.View, error) {
	c.hit("View")
	return c.Store.View(ctx, a, m)
}
func (c *countingWS) Open(ctx context.Context, a workspace.Actor, p string) (io.ReadSeekCloser, workspace.Node, error) {
	c.hit("Open")
	return c.Store.Open(ctx, a, p)
}
func (c *countingWS) ReadablePrefixes(ctx context.Context, a workspace.Actor) ([]string, error) {
	c.hit("ReadablePrefixes")
	return c.Store.ReadablePrefixes(ctx, a)
}

type countingHost struct {
	*files.Service
	st *countingWS
}

func (h countingHost) Store() workspace.Store { return h.st }

var _ = Describe("projects shared with a person", func() {
	var (
		ctx    context.Context
		ws     *jsonstore.Store
		f      *files.Service
		index  *countingStore
		svc    *projects.Service
		admin  workspace.Actor
		cws    *countingWS
		owners atomic.Int64 // calls of the display-name hook
	)

	names := map[string]string{"alice": "Alice Rossi", "carol": "Carol Bianchi", "bob": "Bob"}

	grant := func(who, owner, rel string, m workspace.Mode) {
		abs, err := f.Abs(owner, rel)
		Expect(err).NotTo(HaveOccurred())
		Expect(ws.Grant(ctx, admin, who, abs, m)).To(Succeed())
	}
	revoke := func(who, owner, rel string) {
		abs, err := f.Abs(owner, rel)
		Expect(err).NotTo(HaveOccurred())
		Expect(ws.Revoke(ctx, admin, who, abs)).To(Succeed())
	}
	create := func(owner, name string) projects.Project {
		p, err := svc.Create(ctx, owner, projects.CreateInput{Name: name})
		Expect(err).NotTo(HaveOccurred())
		return p
	}
	write := func(owner, rel, content string) {
		_, err := f.WriteHome(ctx, owner, rel, content, "test", 1<<20)
		Expect(err).NotTo(HaveOccurred())
	}
	byID := func(list []projects.Shared, id string) (projects.Shared, bool) {
		for _, s := range list {
			if s.ID == id {
				return s, true
			}
		}
		return projects.Shared{}, false
	}

	BeforeEach(func() {
		ctx = context.Background()
		admin = workspace.Actor{Principal: files.AdminPrincipal}
		blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
		Expect(err).NotTo(HaveOccurred())
		ws, err = jsonstore.New(filepath.Join(GinkgoT().TempDir(), "ws"), blobs, jsonstore.Options{Admin: files.AdminPrincipal})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = ws.Close() })
		f = files.New(ws)
		inner, err := projects.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		index = &countingStore{Store: inner}
		cws = &countingWS{Store: f.Store(), calls: map[string]int{}}
		svc = projects.NewService(projects.Options{Store: index, Files: countingHost{Service: f, st: cws}, OwnerName: func(_ context.Context, p string) string {
			owners.Add(1)
			return names[p]
		}})
	})

	It("lists nothing and resolves nothing without a grant", func() {
		p := create("alice", "Acme")
		list, err := svc.SharedWith(ctx, "bob")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(BeEmpty())
		_, err = svc.ResolveSharedNode(ctx, "bob", p.Node)
		Expect(err).To(MatchError(projects.ErrNotFound))
	})

	It("gives a read grant on the project folder as mode read", func() {
		p := create("alice", "Acme")
		grant("bob", "alice", "/Projects/"+p.Slug, workspace.ModeRead)
		list, err := svc.SharedWith(ctx, "bob")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		s := list[0]
		Expect(s.ID).To(Equal(p.ID))
		Expect(s.Name).To(Equal("Acme"))
		Expect(s.Mode).To(Equal("read"))
		Expect(s.Status).To(Equal(projects.StatusActive))
		Expect(s.Slug).To(Equal(p.Slug))
		Expect(s.Node).To(Equal(p.Node))
		Expect(s.OwnerName).To(Equal("Alice Rossi"))
		Expect(s.Owner).To(BeEmpty(), "the embedded project must not carry the owner's account")
		Expect(s.FilesPath).To(Equal("/users/alice/Projects/acme/files"))
		Expect(s.MemoryPath).To(Equal("/users/alice/Projects/acme/memory"))
		r, err := svc.ResolveSharedNode(ctx, "bob", p.Node)
		Expect(err).NotTo(HaveOccurred())
		Expect(r).To(Equal(s))
	})

	It("gives a write grant as mode write", func() {
		p := create("alice", "Acme")
		grant("bob", "alice", "/Projects/"+p.Slug, workspace.ModeWrite)
		r, err := svc.ResolveSharedNode(ctx, "bob", p.Node)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Mode).To(Equal("write"))
	})

	It("stops at once when the grant is revoked", func() {
		p := create("alice", "Acme")
		grant("bob", "alice", "/Projects/"+p.Slug, workspace.ModeRead)
		_, err := svc.ResolveSharedNode(ctx, "bob", p.Node)
		Expect(err).NotTo(HaveOccurred())
		revoke("bob", "alice", "/Projects/"+p.Slug)
		_, err = svc.ResolveSharedNode(ctx, "bob", p.Node)
		Expect(err).To(MatchError(projects.ErrNotFound))
		list, err := svc.SharedWith(ctx, "bob")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(BeEmpty())
	})

	It("follows a change of the mode of a live grant", func() {
		p := create("alice", "Acme")
		grant("bob", "alice", "/Projects/"+p.Slug, workspace.ModeWrite)
		grant("bob", "alice", "/Projects/"+p.Slug, workspace.ModeRead)
		r, err := svc.ResolveSharedNode(ctx, "bob", p.Node)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Mode).To(Equal("read"))
	})

	It("does not leak a project that exists only in the owner's index", func() {
		p := create("alice", "Acme")
		_, unshared := svc.ResolveSharedNode(ctx, "bob", p.Node)
		_, unknown := svc.ResolveSharedNode(ctx, "bob", 999999)
		Expect(unshared).To(MatchError(projects.ErrNotFound))
		Expect(unknown).To(MatchError(projects.ErrNotFound))
		Expect(unshared.Error()).To(Equal(unknown.Error()))
	})

	It("takes only a folder named exactly Projects as the root of projects", func() {
		p := create("alice", "Acme")
		manifest, ok, err := f.ReadHome(ctx, "alice", "Projects/"+p.Slug+"/project.json", 1<<20)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		// The store keeps the case of a name but treats names of another case
		// as the same, so each owner has one root: dora's is "projects", erin's
		// is "PROJECTS".
		write("dora", "projects/lower/project.json", manifest)
		write("erin", "PROJECTS/upper/project.json", manifest)
		for owner, rels := range map[string][]string{"dora": {"/projects/lower", "/projects"}, "erin": {"/PROJECTS/upper", "/PROJECTS"}} {
			for _, rel := range rels {
				grant("bob", owner, rel, workspace.ModeRead)
			}
		}
		list, err := svc.SharedWith(ctx, "bob")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(BeEmpty(), "no folder of another case is a project")
		for owner, rel := range map[string]string{"dora": "projects/lower", "erin": "PROJECTS/upper"} {
			abs, err := f.Abs(owner, rel)
			Expect(err).NotTo(HaveOccurred())
			n, err := ws.Stat(ctx, admin, abs)
			Expect(err).NotTo(HaveOccurred())
			_, err = svc.ResolveSharedNode(ctx, "bob", n.ID)
			Expect(err).To(MatchError(projects.ErrNotFound), rel)
		}
	})

	It("treats a grant on Projects/ as a grant on each project below it", func() {
		a := create("alice", "Acme")
		b := create("alice", "Beta")
		grant("bob", "alice", "/Projects", workspace.ModeRead)
		list, err := svc.SharedWith(ctx, "bob")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(2))
		for _, id := range []string{a.ID, b.ID} {
			s, ok := byID(list, id)
			Expect(ok).To(BeTrue())
			Expect(s.Mode).To(Equal("read"))
		}
		// A write grant on one child raises the effective mode of that child only.
		grant("bob", "alice", "/Projects/"+b.Slug, workspace.ModeWrite)
		list, err = svc.SharedWith(ctx, "bob")
		Expect(err).NotTo(HaveOccurred())
		sa, _ := byID(list, a.ID)
		sb, _ := byID(list, b.ID)
		Expect(sa.Mode).To(Equal("read"))
		Expect(sb.Mode).To(Equal("write"))
	})

	It("treats a grant on the whole home as a grant on its projects", func() {
		a := create("alice", "Acme")
		grant("bob", "alice", "/", workspace.ModeWrite)
		r, err := svc.ResolveSharedNode(ctx, "bob", a.Node)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Mode).To(Equal("write"))
	})

	It("ignores a grant on a folder inside a project", func() {
		a := create("alice", "Acme")
		grant("bob", "alice", "/Projects/"+a.Slug+"/files", workspace.ModeWrite)
		list, err := svc.SharedWith(ctx, "bob")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(BeEmpty())
		_, err = svc.ResolveSharedNode(ctx, "bob", a.Node)
		Expect(err).To(MatchError(projects.ErrNotFound))
	})

	It("never lists an unrelated shared folder of an owner", func() {
		write("alice", "/Notes/todo.md", "x")
		write("alice", "/Projects/stray/readme.md", "x")
		grant("bob", "alice", "/Notes", workspace.ModeRead)
		grant("bob", "alice", "/Projects/stray", workspace.ModeRead)
		list, err := svc.SharedWith(ctx, "bob")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(BeEmpty())
	})

	It("does not list a project folder that has no manifest", func() {
		create("alice", "Acme")
		write("alice", "/Projects/junk/notes.md", "x")
		grant("bob", "alice", "/Projects", workspace.ModeRead)
		list, err := svc.SharedWith(ctx, "bob")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].Name).To(Equal("Acme"))
	})

	It("lists a folder with an unreadable manifest as broken", func() {
		write("alice", "/Projects/bad/project.json", "{not json")
		grant("bob", "alice", "/Projects/bad", workspace.ModeRead)
		list, err := svc.SharedWith(ctx, "bob")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].Status).To(Equal(projects.StatusBroken))
		Expect(list[0].Error).NotTo(BeEmpty())
		Expect(list[0].Mode).To(Equal("read"))
		Expect(list[0].ID).To(BeEmpty(), "a broken entry is keyed by its node, never by an id that could collide")
		Expect(list[0].Ref).To(Equal(fmt.Sprintf("s:%d", list[0].Node)))
		_, err = svc.ResolveSharedNode(ctx, "bob", list[0].Node)
		Expect(err).To(MatchError(projects.ErrInvalid))
	})

	It("lists a manifest with no id as broken, and a broken one with an unsafe id has no id", func() {
		write("alice", "/Projects/noid/project.json", `{"version":1,"name":"No id"}`)
		write("alice", "/Projects/evil/project.json", `{"version":1,"id":"../x","name":"Evil"}`)
		grant("bob", "alice", "/Projects", workspace.ModeRead)
		list, err := svc.SharedWith(ctx, "bob")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(2))
		for _, s := range list {
			Expect(s.Status).To(Equal(projects.StatusBroken))
			Expect(s.ID).To(BeEmpty())
		}
	})

	It("lists an archived project with its status and still resolves it", func() {
		p := create("alice", "Acme")
		_, err := svc.Archive(ctx, "alice", p.ID)
		Expect(err).NotTo(HaveOccurred())
		grant("bob", "alice", "/Projects/"+p.Slug, workspace.ModeRead)
		list, err := svc.SharedWith(ctx, "bob")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].Status).To(Equal(projects.StatusArchived))
		r, err := svc.ResolveSharedNode(ctx, "bob", p.Node)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Status).To(Equal(projects.StatusArchived))
	})

	It("reaches a project through a grant to everyone", func() {
		p := create("alice", "Acme")
		grant(files.EveryoneGroup, "alice", "/Projects/"+p.Slug, workspace.ModeRead)
		for _, who := range []string{"bob", "carol"} {
			r, err := svc.ResolveSharedNode(ctx, who, p.Node)
			Expect(err).NotTo(HaveOccurred())
			Expect(r.Mode).To(Equal("read"))
		}
		// An own grant raises the mode.
		grant("carol", "alice", "/Projects/"+p.Slug, workspace.ModeWrite)
		r, err := svc.ResolveSharedNode(ctx, "carol", p.Node)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Mode).To(Equal("write"))
	})

	It("never returns the projects of the viewer", func() {
		p := create("alice", "Acme")
		grant("alice", "alice", "/Projects/"+p.Slug, workspace.ModeWrite) // even an own grant
		grant(files.EveryoneGroup, "alice", "/Projects", workspace.ModeRead)
		list, err := svc.SharedWith(ctx, "alice")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(BeEmpty())
		_, err = svc.ResolveSharedNode(ctx, "alice", p.Node)
		Expect(err).To(MatchError(projects.ErrNotFound))
	})

	It("gives the administrator account nothing", func() {
		create("alice", "Acme")
		list, err := svc.SharedWith(ctx, files.AdminPrincipal)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(BeEmpty())
	})

	It("uses a display name for the owner and never the account", func() {
		p := create("dave", "Acme") // dave has no display name
		grant("bob", "dave", "/Projects/"+p.Slug, workspace.ModeRead)
		list, err := svc.SharedWith(ctx, "bob")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].OwnerName).NotTo(BeEmpty())
		Expect(list[0].OwnerName).NotTo(Equal("dave"))

		// A directory that answers with the account itself is not a name.
		svc2 := projects.NewService(projects.Options{Store: index, Files: f, OwnerName: func(_ context.Context, p string) string { return p }})
		list, err = svc2.SharedWith(ctx, "bob")
		Expect(err).NotTo(HaveOccurred())
		Expect(list[0].OwnerName).NotTo(Equal("dave"))
		// Without a directory at all.
		svc3 := projects.NewService(projects.Options{Store: index, Files: f})
		list, err = svc3.SharedWith(ctx, "bob")
		Expect(err).NotTo(HaveOccurred())
		Expect(list[0].OwnerName).NotTo(Equal("dave"))
	})

	It("builds the paths from the store with odd folder names", func() {
		for _, slug := range []string{"my proj é", "日本 プロジェクト", "a.b", "-x", "ünï cödé"} {
			write("alice", "/Projects/"+slug+"/project.json", fmt.Sprintf(`{"version":1,"id":"id-%d","name":"N %s"}`, len(slug), slug))
			grant("bob", "alice", "/Projects/"+slug, workspace.ModeRead)
		}
		list, err := svc.SharedWith(ctx, "bob")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(5))
		for _, s := range list {
			Expect(s.Status).To(Equal(projects.StatusActive))
			Expect(s.FilesPath).To(Equal("/users/alice/Projects/" + s.Slug + "/files"))
			Expect(s.MemoryPath).To(Equal("/users/alice/Projects/" + s.Slug + "/memory"))
			Expect(strings.Contains(s.FilesPath, "..")).To(BeFalse())
			Expect(strings.HasPrefix(s.FilesPath, "/users/alice/Projects/")).To(BeTrue())
		}
	})

	It("cannot be steered to a path by the request", func() {
		p := create("alice", "Acme")
		grant("bob", "alice", "/Projects/"+p.Slug, workspace.ModeRead)
		for _, ref := range []string{"../alice", "acme/../x", "/users/alice/Projects/acme", p.ID, "s:" + p.ID} {
			_, err := svc.ResolveSharedRef(ctx, "bob", ref)
			Expect(err).To(MatchError(projects.ErrNotFound), ref)
		}
	})

	Describe("a hostile sharer that copies a project id", func() {
		var a, c projects.Project

		BeforeEach(func() {
			a = create("alice", "Acme")
			c = create("carol", "Copy")
			text, ok, err := f.ReadHome(ctx, "alice", "/Projects/"+a.Slug+"/project.json", 1<<20)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
			write("carol", "/Projects/copy/project.json", text)
			grant("bob", "alice", "/Projects/"+a.Slug, workspace.ModeRead)
			grant("bob", "carol", "/Projects/copy", workspace.ModeWrite)
		})

		It("lists both, each with its own node and ref", func() {
			list, err := svc.SharedWith(ctx, "bob")
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(HaveLen(2))
			Expect(list[0].ID).To(Equal(list[1].ID))
			Expect(list[0].Ref).NotTo(Equal(list[1].Ref))
		})

		It("keeps the reference to the first node on that node", func() {
			r, err := svc.ResolveSharedRef(ctx, "bob", fmt.Sprintf("s:%d", a.Node))
			Expect(err).NotTo(HaveOccurred())
			Expect(r.Node).To(Equal(a.Node))
			Expect(r.OwnerName).To(Equal("Alice Rossi"))
			Expect(r.Mode).To(Equal("read"))
			Expect(r.FilesPath).To(Equal("/users/alice/Projects/acme/files"))
		})

		It("never moves the reference to the copy after a revoke", func() {
			revoke("bob", "alice", "/Projects/"+a.Slug)
			_, err := svc.ResolveSharedNode(ctx, "bob", a.Node)
			Expect(err).To(MatchError(projects.ErrNotFound))
			r, err := svc.ResolveSharedNode(ctx, "bob", c.Node)
			Expect(err).NotTo(HaveOccurred())
			Expect(r.Node).To(Equal(c.Node))
		})

		It("never moves the reference to the copy after the owner deletes the project", func() {
			Expect(svc.Delete(ctx, "alice", a.ID)).To(Succeed())
			_, err := svc.ResolveSharedNode(ctx, "bob", a.Node)
			Expect(err).To(MatchError(projects.ErrNotFound))
			list, err := svc.SharedWith(ctx, "bob")
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(HaveLen(1))
			Expect(list[0].Node).To(Equal(c.Node), "a trashed folder is not listed")
		})
	})

	Describe("a reference to a node", func() {
		It("follows a rename of the project folder", func() {
			p := create("alice", "Acme")
			grant("bob", "alice", "/Projects/"+p.Slug, workspace.ModeRead)
			from, _ := f.Abs("alice", "/Projects/"+p.Slug)
			to, _ := f.Abs("alice", "/Projects/renamed")
			_, err := ws.Move(ctx, f.Actor("alice"), from, to)
			Expect(err).NotTo(HaveOccurred())
			r, err := svc.ResolveSharedNode(ctx, "bob", p.Node)
			Expect(err).NotTo(HaveOccurred())
			Expect(r.Slug).To(Equal("renamed"))
			Expect(r.FilesPath).To(Equal("/users/alice/Projects/renamed/files"))
		})

		It("stops when the owner moves the folder out of Projects/", func() {
			p := create("alice", "Acme")
			grant("bob", "alice", "/Projects/"+p.Slug, workspace.ModeRead)
			from, _ := f.Abs("alice", "/Projects/"+p.Slug)
			to, _ := f.Abs("alice", "/Elsewhere/"+p.Slug)
			_, err := ws.Move(ctx, f.Actor("alice"), from, to)
			Expect(err).NotTo(HaveOccurred())
			_, err = svc.ResolveSharedNode(ctx, "bob", p.Node)
			Expect(err).To(MatchError(projects.ErrNotFound))
			list, err := svc.SharedWith(ctx, "bob")
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(BeEmpty())
		})

		It("stops when the owner moves it one level down, into another folder of Projects/", func() {
			p := create("alice", "Acme")
			write("alice", "/Projects/group/readme.md", "x")
			grant("bob", "alice", "/Projects/"+p.Slug, workspace.ModeRead)
			from, _ := f.Abs("alice", "/Projects/"+p.Slug)
			to, _ := f.Abs("alice", "/Projects/group/"+p.Slug)
			_, err := ws.Move(ctx, f.Actor("alice"), from, to)
			Expect(err).NotTo(HaveOccurred())
			_, err = svc.ResolveSharedNode(ctx, "bob", p.Node)
			Expect(err).To(MatchError(projects.ErrNotFound))
		})

		It("is not found without a grant, with the text of an unknown node", func() {
			p := create("alice", "Acme")
			_, unshared := svc.ResolveSharedNode(ctx, "bob", p.Node)
			_, unknown := svc.ResolveSharedNode(ctx, "bob", 987654)
			Expect(unshared).To(MatchError(projects.ErrNotFound))
			Expect(unshared.Error()).To(Equal(unknown.Error()))
		})

		It("is not found for a node that is not a project folder", func() {
			p := create("alice", "Acme")
			grant("bob", "alice", "/", workspace.ModeRead)
			for _, rel := range []string{"/Projects", "/Projects/" + p.Slug + "/files", "/Projects/" + p.Slug + "/project.json", "/"} {
				abs, _ := f.Abs("alice", rel)
				n, err := ws.Stat(ctx, f.Actor("alice"), abs)
				Expect(err).NotTo(HaveOccurred())
				_, err = svc.ResolveSharedNode(ctx, "bob", n.ID)
				Expect(err).To(MatchError(projects.ErrNotFound), rel)
			}
		})

		It("is not found for the project of the viewer", func() {
			own := create("bob", "Mine")
			grant(files.EveryoneGroup, "bob", "/Projects/"+own.Slug, workspace.ModeRead)
			_, err := svc.ResolveSharedNode(ctx, "bob", own.Node)
			Expect(err).To(MatchError(projects.ErrNotFound))
			_, err = svc.ResolveSharedRef(ctx, "bob", fmt.Sprintf("s:%d", own.Node))
			Expect(err).To(MatchError(projects.ErrNotFound))
		})

		It("builds the ref of a listed project and resolves it", func() {
			p := create("alice", "Acme")
			grant("bob", "alice", "/Projects/"+p.Slug, workspace.ModeWrite)
			list, err := svc.SharedWith(ctx, "bob")
			Expect(err).NotTo(HaveOccurred())
			Expect(list[0].Ref).To(Equal(fmt.Sprintf("s:%d", p.Node)))
			r, err := svc.ResolveSharedRef(ctx, "bob", list[0].Ref)
			Expect(err).NotTo(HaveOccurred())
			Expect(r).To(Equal(list[0]))
		})

		DescribeTable("a malformed ref is not found",
			func(ref string) {
				p := create("alice", "Acme")
				grant("bob", "alice", "/Projects/"+p.Slug, workspace.ModeRead)
				_, err := svc.ResolveSharedRef(ctx, "bob", ref)
				Expect(err).To(MatchError(projects.ErrNotFound))
				Expect(err.Error()).To(Equal(projects.ErrNotFound.Error()))
			},
			Entry("empty", ""),
			Entry("no number", "s:"),
			Entry("negative", "s:-1"),
			Entry("zero", "s:0"),
			Entry("trailing space", "s:1 "),
			Entry("leading space", " s:1"),
			Entry("hex", "s:0x10"),
			Entry("plus", "s:+1"),
			Entry("other prefix", "p:5"),
			Entry("upper prefix", "S:1"),
			Entry("two numbers", "s:1:2"),
			Entry("newline", "s:1\n"),
			Entry("unicode digits", "s:\u0661"),
			Entry("overflow", "s:99999999999999999999"),
			Entry("very long", "s:"+strings.Repeat("1", 5000)),
		)

		It("refuses a reference with leading zeros even for a shared node", func() {
			p := create("alice", "Acme")
			grant("bob", "alice", "/Projects/"+p.Slug, workspace.ModeRead)
			_, err := svc.ResolveSharedRef(ctx, "bob", fmt.Sprintf("s:%d", p.Node))
			Expect(err).NotTo(HaveOccurred())
			for _, ref := range []string{fmt.Sprintf("s:0%d", p.Node), fmt.Sprintf("s:000%d", p.Node)} {
				_, err := svc.ResolveSharedRef(ctx, "bob", ref)
				Expect(err).To(MatchError(projects.ErrNotFound), ref)
				Expect(err.Error()).To(Equal(projects.ErrNotFound.Error()))
			}
		})

		It("is not found when a folder above the project is trashed", func() {
			p := create("alice", "Acme")
			grant("bob", "alice", "/Projects/"+p.Slug, workspace.ModeRead)
			abs, err := f.Abs("alice", "/Projects")
			Expect(err).NotTo(HaveOccurred())
			Expect(ws.Delete(ctx, f.Actor("alice"), abs)).To(Succeed())
			_, err = svc.ResolveSharedNode(ctx, "bob", p.Node)
			Expect(err).To(MatchError(projects.ErrNotFound))
			list, err := svc.SharedWith(ctx, "bob")
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(BeEmpty())
		})

		It("does not treat a node at /users/alice/users/bob/Projects/x as a project of bob", func() {
			write("alice", "/users/bob/Projects/x/project.json", `{"version":1,"id":"x1","name":"X"}`)
			grant("bob", "alice", "/users/bob/Projects/x", workspace.ModeWrite)
			abs, err := f.Abs("alice", "/users/bob/Projects/x")
			Expect(err).NotTo(HaveOccurred())
			n, err := ws.Stat(ctx, f.Actor("bob"), abs)
			Expect(err).NotTo(HaveOccurred())
			_, err = svc.ResolveSharedNode(ctx, "bob", n.ID)
			Expect(err).To(MatchError(projects.ErrNotFound))
			list, err := svc.SharedWith(ctx, "bob")
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(BeEmpty())
		})

		It("costs a constant number of reads for a viewer with 200 shares", func() {
			var first int64
			for i := 0; i < 200; i++ {
				slug := fmt.Sprintf("p%03d", i)
				write("alice", "/Projects/"+slug+"/project.json", fmt.Sprintf(`{"version":1,"id":"id%03d","name":"P%03d"}`, i, i))
				grant("bob", "alice", "/Projects/"+slug, workspace.ModeRead)
				if i == 150 {
					abs, _ := f.Abs("alice", "/Projects/"+slug)
					n, err := ws.Stat(ctx, f.Actor("alice"), abs)
					Expect(err).NotTo(HaveOccurred())
					first = n.ID
				}
			}
			cws.reset()
			owners.Store(0)
			r, err := svc.ResolveSharedNode(ctx, "bob", first)
			Expect(err).NotTo(HaveOccurred())
			Expect(r.Name).To(Equal("P150"))
			Expect(cws.total()).To(BeNumerically("<=", 6))
			Expect(cws.calls["ReadablePrefixes"]).To(BeZero())
			Expect(cws.calls["List"]).To(BeZero())
			Expect(owners.Load()).To(BeNumerically("<=", 1))
		})
	})

	It("marshals a shared project without the workspace paths", func() {
		p := create("alice", "Acme")
		grant("bob", "alice", "/Projects/"+p.Slug, workspace.ModeRead)
		list, err := svc.SharedWith(ctx, "bob")
		Expect(err).NotTo(HaveOccurred())
		b, err := json.Marshal(list[0])
		Expect(err).NotTo(HaveOccurred())
		Expect(string(b)).NotTo(ContainSubstring("/users/"))
		Expect(string(b)).NotTo(ContainSubstring("alice"))
	})

	It("does not list a trashed project folder", func() {
		p := create("alice", "Acme")
		grant("bob", "alice", "/Projects/"+p.Slug, workspace.ModeRead)
		Expect(svc.Delete(ctx, "alice", p.ID)).To(Succeed())
		list, err := svc.SharedWith(ctx, "bob")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(BeEmpty())
	})

	It("lists a manifest over 1 MiB as broken", func() {
		_, err := f.WriteHome(ctx, "alice", "/Projects/big/project.json", `{"version":1,"id":"big","name":"Big","description":"`+strings.Repeat("x", 1<<20)+`"}`, "test", 4<<20)
		Expect(err).NotTo(HaveOccurred())
		grant("bob", "alice", "/Projects/big", workspace.ModeRead)
		list, err := svc.SharedWith(ctx, "bob")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].Status).To(Equal(projects.StatusBroken))
		Expect(list[0].ID).To(BeEmpty())
	})

	It("does not list a folder whose project.json is a folder", func() {
		write("alice", "/Projects/odd/project.json/x.txt", "x")
		grant("bob", "alice", "/Projects/odd", workspace.ModeRead)
		list, err := svc.SharedWith(ctx, "bob")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(BeEmpty())
	})

	It("asks the display-name hook once per owner", func() {
		for _, o := range []string{"alice", "carol"} {
			for i := 0; i < 3; i++ {
				p := create(o, fmt.Sprintf("P%d", i))
				grant("bob", o, "/Projects/"+p.Slug, workspace.ModeRead)
			}
		}
		owners.Store(0)
		list, err := svc.SharedWith(ctx, "bob")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(6))
		Expect(owners.Load()).To(Equal(int64(2)))
	})

	It("sorts by name and keeps owners apart", func() {
		a := create("alice", "Zeta")
		c := create("carol", "alpha")
		grant("bob", "alice", "/Projects/"+a.Slug, workspace.ModeRead)
		grant("bob", "carol", "/Projects/"+c.Slug, workspace.ModeRead)
		list, err := svc.SharedWith(ctx, "bob")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(2))
		Expect(list[0].Name).To(Equal("alpha"))
		Expect(list[1].Name).To(Equal("Zeta"))
	})

	It("stays cheap with many grants and never touches the index of the owner", func() {
		const n = 200
		for i := 0; i < n; i++ {
			slug := fmt.Sprintf("p%03d", i)
			write("alice", "/Projects/"+slug+"/project.json", fmt.Sprintf(`{"version":1,"id":"id%03d","name":"P%03d"}`, i, i))
			grant("bob", "alice", "/Projects/"+slug, workspace.ModeRead)
		}
		before := index.calls.Load()
		list, err := svc.SharedWith(ctx, "bob")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(n))
		Expect(index.calls.Load()).To(Equal(before), "viewer calls read no row of the owner's index")
	})

	It("honours the permission hook for the viewer", func() {
		p := create("alice", "Acme")
		grant("bob", "alice", "/Projects/"+p.Slug, workspace.ModeRead)
		off := projects.NewService(projects.Options{Store: index, Files: f, Permitted: func(_ context.Context, u string) bool { return u != "bob" }})
		_, err := off.SharedWith(ctx, "bob")
		Expect(err).To(MatchError(projects.ErrForbidden))
		_, err = off.ResolveSharedNode(ctx, "bob", p.Node)
		Expect(err).To(MatchError(projects.ErrForbidden))
	})
})

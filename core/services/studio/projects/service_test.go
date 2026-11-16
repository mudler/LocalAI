package projects_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/jsonstore"
)

var _ = Describe("the project service", func() {
	var (
		ctx   context.Context
		f     *files.Service
		store projects.Store
		svc   *projects.Service
	)

	newSvc := func(permitted func(context.Context, string) bool) *projects.Service {
		return projects.NewService(projects.Options{Store: store, Files: f, Permitted: permitted})
	}
	exists := func(owner, rel string) bool {
		abs, err := f.Abs(owner, rel)
		Expect(err).NotTo(HaveOccurred())
		_, err = f.Store().Stat(ctx, f.Actor(owner), abs)
		return err == nil
	}
	readManifest := func(owner, slug string) (projects.Manifest, error) {
		text, ok, err := f.ReadHome(ctx, owner, "/Projects/"+slug+"/project.json", 1<<20)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		return projects.ParseManifest([]byte(text))
	}
	write := func(owner, rel, content string) {
		_, err := f.WriteHome(ctx, owner, rel, content, "test", 1<<20)
		Expect(err).NotTo(HaveOccurred())
	}

	BeforeEach(func() {
		ctx = context.Background()
		blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
		Expect(err).NotTo(HaveOccurred())
		ws, err := jsonstore.New(filepath.Join(GinkgoT().TempDir(), "ws"), blobs, jsonstore.Options{Admin: files.AdminPrincipal})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = ws.Close() })
		f = files.New(ws)
		store, err = projects.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		svc = newSvc(nil)
	})

	It("creates the four nodes and a valid manifest in one change set", func() {
		p, err := svc.Create(ctx, "alice", projects.CreateInput{Name: "Acme Work", Description: "d", Defaults: projects.Defaults{Skills: []string{"s"}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Slug).To(Equal("acme-work"))
		Expect(p.Status).To(Equal(projects.StatusActive))
		Expect(projects.IDSafe(p.ID)).To(BeTrue())
		for _, rel := range []string{"/Projects/acme-work", "/Projects/acme-work/files", "/Projects/acme-work/memory", "/Projects/acme-work/memory/index.md", "/Projects/acme-work/project.json"} {
			Expect(exists("alice", rel)).To(BeTrue(), rel)
		}
		m, err := readManifest("alice", "acme-work")
		Expect(err).NotTo(HaveOccurred())
		Expect(m.ID).To(Equal(p.ID))
		Expect(m.Name).To(Equal("Acme Work"))
		Expect(m.Defaults.Skills).To(Equal([]string{"s"}))

		abs, _ := f.Abs("alice", "/Projects")
		hist, err := f.Store().History(ctx, f.Actor("alice"), workspace.HistoryQuery{Path: abs, Limit: 10})
		Expect(err).NotTo(HaveOccurred())
		var rev int64
		for _, cs := range hist {
			if strings.Contains(cs.Title, "Acme Work") {
				rev = cs.Rev
			}
		}
		Expect(rev).NotTo(BeZero(), "no change set titled after the project")
		_, conflicts, err := f.Store().Undo(ctx, f.Actor("alice"), rev, workspace.UndoOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(conflicts).To(BeEmpty())
		for _, rel := range []string{"/Projects/acme-work/files", "/Projects/acme-work/memory", "/Projects/acme-work/project.json"} {
			Expect(exists("alice", rel)).To(BeFalse(), rel)
		}
	})

	It("rejects an empty name and gives a clashing name a counter", func() {
		_, err := svc.Create(ctx, "alice", projects.CreateInput{Name: "  "})
		Expect(err).To(MatchError(projects.ErrInvalid))
		_, err = svc.Create(ctx, "alice", projects.CreateInput{Name: "Acme Work"})
		Expect(err).NotTo(HaveOccurred())
		p, err := svc.Create(ctx, "alice", projects.CreateInput{Name: "acme  work"})
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Slug).To(Equal("acme-work-2"))
	})

	It("skips a slug whose folder already exists", func() {
		write("alice", "/Projects/acme-work/notes.txt", "x")
		p, err := svc.Create(ctx, "alice", projects.CreateInput{Name: "Acme Work"})
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Slug).To(Equal("acme-work-2"))
	})

	It("lets two owners use the same name", func() {
		a, err := svc.Create(ctx, "alice", projects.CreateInput{Name: "Shared"})
		Expect(err).NotTo(HaveOccurred())
		b, err := svc.Create(ctx, "bob", projects.CreateInput{Name: "Shared"})
		Expect(err).NotTo(HaveOccurred())
		Expect(a.Slug).To(Equal(b.Slug))
		Expect(a.ID).NotTo(Equal(b.ID))
		_, err = svc.Get(ctx, "bob", a.ID)
		Expect(err).To(MatchError(projects.ErrNotFound))
		list, err := svc.List(ctx, "alice")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
	})

	It("resolves a project with home-relative paths", func() {
		p, _ := svc.Create(ctx, "alice", projects.CreateInput{Name: "Acme"})
		r, err := svc.Get(ctx, "alice", p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Manifest.ID).To(Equal(p.ID))
		Expect(r.FilesPath).To(Equal("/Projects/acme/files"))
		Expect(r.MemoryPath).To(Equal("/Projects/acme/memory"))
	})

	It("updates only the manifest", func() {
		p, _ := svc.Create(ctx, "alice", projects.CreateInput{Name: "Acme", Description: "old"})
		write("alice", "/Projects/acme/files/a.txt", "keep")
		name, desc := "Acme Two", "new"
		got, err := svc.Update(ctx, "alice", p.ID, projects.UpdateInput{Name: &name, Description: &desc, Defaults: &projects.Defaults{Addons: []string{"x"}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Name).To(Equal("Acme Two"))
		Expect(got.Slug).To(Equal("acme"), "the folder keeps its name")
		m, err := readManifest("alice", "acme")
		Expect(err).NotTo(HaveOccurred())
		Expect(m.Name).To(Equal("Acme Two"))
		Expect(m.Description).To(Equal("new"))
		Expect(m.Defaults.Addons).To(Equal([]string{"x"}))
		Expect(m.ID).To(Equal(p.ID))
		Expect(exists("alice", "/Projects/acme/files/a.txt")).To(BeTrue())
		bad := ""
		_, err = svc.Update(ctx, "alice", p.ID, projects.UpdateInput{Name: &bad})
		Expect(err).To(MatchError(projects.ErrInvalid))
	})

	Describe("attached tools", func() {
		all := projects.Defaults{Skills: []string{"s"}, Addons: []string{"a"}, Connectors: []string{"c"}, MCPServers: []string{"m"}, Collections: []string{"k"}}

		It("creates and updates a project with attached lists, and keeps them over a rewrite", func() {
			p, err := svc.Create(ctx, "alice", projects.CreateInput{Name: "Acme", Attached: all})
			Expect(err).NotTo(HaveOccurred())
			m, err := readManifest("alice", "acme")
			Expect(err).NotTo(HaveOccurred())
			Expect(m.Attached).To(Equal(all))
			name := "Acme Two"
			_, err = svc.Update(ctx, "alice", p.ID, projects.UpdateInput{Name: &name})
			Expect(err).NotTo(HaveOccurred())
			m, _ = readManifest("alice", "acme")
			Expect(m.Attached).To(Equal(all), "a field left out keeps its value")
			none := projects.Defaults{}
			_, err = svc.Update(ctx, "alice", p.ID, projects.UpdateInput{Attached: &none})
			Expect(err).NotTo(HaveOccurred())
			m, _ = readManifest("alice", "acme")
			Expect(m.Attached).To(Equal(none))
		})

		DescribeTable("refuses an entry outside a non-empty limit of its kind, naming tool and limit",
			func(limit, attached projects.Defaults, tool string) {
				_, err := svc.Create(ctx, "alice", projects.CreateInput{Name: "Bad", Defaults: limit, Attached: attached})
				Expect(err).To(MatchError(projects.ErrInvalid))
				Expect(err.Error()).To(ContainSubstring(tool))
				Expect(err.Error()).To(ContainSubstring("limit"))
			},
			Entry("skill", projects.Defaults{Skills: []string{"x"}}, projects.Defaults{Skills: []string{"y"}}, `"y"`),
			Entry("add-on", projects.Defaults{Addons: []string{"x"}}, projects.Defaults{Addons: []string{"y"}}, `"y"`),
			Entry("connector", projects.Defaults{Connectors: []string{"x"}}, projects.Defaults{Connectors: []string{"y"}}, `"y"`),
			Entry("MCP server", projects.Defaults{MCPServers: []string{"x"}}, projects.Defaults{MCPServers: []string{"y"}}, `"y"`),
			Entry("collection", projects.Defaults{Collections: []string{"x"}}, projects.Defaults{Collections: []string{"y"}}, `"y"`),
		)

		It("accepts an entry inside the limit and any entry when the limit is empty", func() {
			_, err := svc.Create(ctx, "alice", projects.CreateInput{Name: "Ok", Defaults: projects.Defaults{Addons: []string{"a", "b"}}, Attached: projects.Defaults{Addons: []string{"b"}, Skills: []string{"free"}}})
			Expect(err).NotTo(HaveOccurred())
		})

		It("refuses an update that attaches outside the limit, or narrows the limit under an attached tool", func() {
			p, err := svc.Create(ctx, "alice", projects.CreateInput{Name: "Acme", Defaults: projects.Defaults{Addons: []string{"a", "b"}}, Attached: projects.Defaults{Addons: []string{"a"}}})
			Expect(err).NotTo(HaveOccurred())
			out := projects.Defaults{Addons: []string{"a", "z"}}
			_, err = svc.Update(ctx, "alice", p.ID, projects.UpdateInput{Attached: &out})
			Expect(err).To(MatchError(projects.ErrInvalid))
			narrow := projects.Defaults{Addons: []string{"b"}}
			_, err = svc.Update(ctx, "alice", p.ID, projects.UpdateInput{Defaults: &narrow})
			Expect(err).To(MatchError(projects.ErrInvalid))
			m, _ := readManifest("alice", "acme")
			Expect(m.Defaults.Addons).To(Equal([]string{"a", "b"}), "nothing was written")
			Expect(m.Attached.Addons).To(Equal([]string{"a"}))
		})

		It("still archives a project that a hand edit left with an attached tool outside the limit", func() {
			p, _ := svc.Create(ctx, "alice", projects.CreateInput{Name: "Acme"})
			write("alice", "/Projects/acme/project.json", `{"version":1,"id":"`+p.ID+`","name":"Acme","defaults":{"addons":["a"]},"attached":{"addons":["z"]}}`)
			_, err := svc.Archive(ctx, "alice", p.ID)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	It("archives and restores, surviving a reindex", func() {
		p, _ := svc.Create(ctx, "alice", projects.CreateInput{Name: "Acme"})
		got, err := svc.Archive(ctx, "alice", p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Status).To(Equal(projects.StatusArchived))
		m, _ := readManifest("alice", "acme")
		Expect(m.Archived).To(BeTrue())
		Expect(svc.Reindex(ctx, "alice")).To(Succeed())
		got, _ = store.Get(ctx, "alice", p.ID)
		Expect(got.Status).To(Equal(projects.StatusArchived))
		got, err = svc.Restore(ctx, "alice", p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Status).To(Equal(projects.StatusActive))
		Expect(svc.Reindex(ctx, "alice")).To(Succeed())
		got, _ = store.Get(ctx, "alice", p.ID)
		Expect(got.Status).To(Equal(projects.StatusActive))
	})

	It("moves the folder to the Trash on delete and drops the row", func() {
		p, _ := svc.Create(ctx, "alice", projects.CreateInput{Name: "Acme"})
		Expect(svc.Delete(ctx, "alice", p.ID)).To(Succeed())
		Expect(exists("alice", "/Projects/acme")).To(BeFalse())
		_, err := store.Get(ctx, "alice", p.ID)
		Expect(err).To(MatchError(projects.ErrNotFound))
		trash, err := f.Trash(ctx, "alice", 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(trash).NotTo(BeEmpty())
		Expect(svc.Delete(ctx, "bob", p.ID)).To(MatchError(projects.ErrNotFound))
	})

	Describe("Reindex", func() {
		It("marks a project without a manifest broken and never deletes", func() {
			p, _ := svc.Create(ctx, "alice", projects.CreateInput{Name: "Acme"})
			Expect(f.DeleteHome(ctx, "alice", "/Projects/acme/project.json")).To(Succeed())
			Expect(svc.Reindex(ctx, "alice")).To(Succeed())
			got, err := store.Get(ctx, "alice", p.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Status).To(Equal(projects.StatusBroken))
			Expect(got.Error).NotTo(BeEmpty())
			Expect(exists("alice", "/Projects/acme/files")).To(BeTrue())
		})

		It("marks invalid JSON broken with a message", func() {
			p, _ := svc.Create(ctx, "alice", projects.CreateInput{Name: "Acme"})
			write("alice", "/Projects/acme/project.json", "{not json")
			Expect(svc.Reindex(ctx, "alice")).To(Succeed())
			got, _ := store.Get(ctx, "alice", p.ID)
			Expect(got.Status).To(Equal(projects.StatusBroken))
			Expect(got.Error).To(ContainSubstring("invalid"))
			_, err := svc.Get(ctx, "alice", p.ID)
			Expect(err).To(MatchError(projects.ErrInvalid))
			// Fixing the file heals the project.
			m := projects.Manifest{Version: 1, ID: p.ID, Name: "Acme"}
			write("alice", "/Projects/acme/project.json", string(m.Marshal()))
			Expect(svc.Reindex(ctx, "alice")).To(Succeed())
			got, _ = store.Get(ctx, "alice", p.ID)
			Expect(got.Status).To(Equal(projects.StatusActive))
			Expect(got.Error).To(BeEmpty())
		})

		It("follows a moved folder by ID", func() {
			p, _ := svc.Create(ctx, "alice", projects.CreateInput{Name: "Acme"})
			from, _ := f.Abs("alice", "/Projects/acme")
			to, _ := f.Abs("alice", "/Projects/renamed")
			_, err := f.Store().Move(ctx, f.Actor("alice"), from, to)
			Expect(err).NotTo(HaveOccurred())
			Expect(svc.Reindex(ctx, "alice")).To(Succeed())
			got, err := store.Get(ctx, "alice", p.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Slug).To(Equal("renamed"))
			Expect(got.Status).To(Equal(projects.StatusActive))
			r, err := svc.Get(ctx, "alice", p.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(r.FilesPath).To(Equal("/Projects/renamed/files"))
		})

		It("resolves a moved folder in Get without an explicit Reindex", func() {
			p, _ := svc.Create(ctx, "alice", projects.CreateInput{Name: "Acme"})
			from, _ := f.Abs("alice", "/Projects/acme")
			to, _ := f.Abs("alice", "/Projects/renamed")
			_, err := f.Store().Move(ctx, f.Actor("alice"), from, to)
			Expect(err).NotTo(HaveOccurred())
			r, err := svc.Get(ctx, "alice", p.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(r.Slug).To(Equal("renamed"))
		})

		It("indexes a folder created by hand and assigns an ID to a manifest without one", func() {
			write("alice", "/Projects/hand/project.json", `{"version":1,"name":"By Hand"}`)
			list, err := svc.List(ctx, "alice")
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(HaveLen(1))
			Expect(list[0].Name).To(Equal("By Hand"))
			Expect(projects.IDSafe(list[0].ID)).To(BeTrue())
			m, err := readManifest("alice", "hand")
			Expect(err).NotTo(HaveOccurred())
			Expect(m.ID).To(Equal(list[0].ID), "the ID is written back")
		})

		It("gives a copied folder a fresh ID and keeps the original", func() {
			p, _ := svc.Create(ctx, "alice", projects.CreateInput{Name: "Acme"})
			m, _ := readManifest("alice", "acme")
			write("alice", "/Projects/acme-copy/project.json", string(m.Marshal()))
			Expect(svc.Reindex(ctx, "alice")).To(Succeed())
			list, _ := svc.List(ctx, "alice")
			Expect(list).To(HaveLen(2))
			ids := map[string]string{}
			for _, x := range list {
				ids[x.ID] = x.Slug
			}
			Expect(ids).To(HaveLen(2))
			Expect(ids[p.ID]).To(Equal("acme"))
			cm, _ := readManifest("alice", "acme-copy")
			Expect(cm.ID).NotTo(Equal(p.ID))
			Expect(ids).To(HaveKey(cm.ID))
		})

		It("never takes an ID that another owner holds", func() {
			p, _ := svc.Create(ctx, "alice", projects.CreateInput{Name: "Acme"})
			m, _ := readManifest("alice", "acme")
			write("bob", "/Projects/stolen/project.json", string(m.Marshal()))
			Expect(svc.Reindex(ctx, "bob")).To(Succeed())
			list, _ := svc.List(ctx, "bob")
			Expect(list).To(HaveLen(1))
			Expect(list[0].ID).NotTo(Equal(p.ID))
			got, err := store.Get(ctx, "alice", p.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Slug).To(Equal("acme"))
			bm, _ := readManifest("bob", "stolen")
			Expect(bm.ID).To(Equal(list[0].ID))
		})

		It("keeps a row whose folder is gone, marked broken", func() {
			p, _ := svc.Create(ctx, "alice", projects.CreateInput{Name: "Acme"})
			Expect(f.DeleteHome(ctx, "alice", "/Projects/acme")).To(Succeed())
			Expect(svc.Reindex(ctx, "alice")).To(Succeed())
			got, err := store.Get(ctx, "alice", p.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Status).To(Equal(projects.StatusBroken))
		})
	})

	It("gives a case-only clash a counter", func() {
		_, err := svc.Create(ctx, "alice", projects.CreateInput{Name: "acme"})
		Expect(err).NotTo(HaveOccurred())
		p, err := svc.Create(ctx, "alice", projects.CreateInput{Name: "ACME"})
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Slug).To(Equal("acme-2"))
	})

	It("turns traversal-style names into safe folders under Projects", func() {
		for name, slug := range map[string]string{"../x": "x", "a/b": "a-b", "x\x00y": "x-y"} {
			p, err := svc.Create(ctx, "alice", projects.CreateInput{Name: name})
			Expect(err).NotTo(HaveOccurred(), name)
			Expect(p.Slug).To(Equal(slug))
			Expect(exists("alice", "/Projects/"+slug+"/project.json")).To(BeTrue())
		}
	})

	It("answers ErrNotFound to another owner for update, archive and delete", func() {
		p, _ := svc.Create(ctx, "alice", projects.CreateInput{Name: "Acme"})
		n := "x"
		_, err := svc.Update(ctx, "bob", p.ID, projects.UpdateInput{Name: &n})
		Expect(err).To(MatchError(projects.ErrNotFound))
		_, err = svc.Archive(ctx, "bob", p.ID)
		Expect(err).To(MatchError(projects.ErrNotFound))
		_, err = svc.Restore(ctx, "bob", p.ID)
		Expect(err).To(MatchError(projects.ErrNotFound))
		Expect(svc.Delete(ctx, "bob", p.ID)).To(MatchError(projects.ErrNotFound))
		Expect(exists("alice", "/Projects/acme")).To(BeTrue())
	})

	It("puts the deleted folder in the Trash by path", func() {
		p, _ := svc.Create(ctx, "alice", projects.CreateInput{Name: "Acme"})
		Expect(svc.Delete(ctx, "alice", p.ID)).To(Succeed())
		trash, err := f.Trash(ctx, "alice", 10)
		Expect(err).NotTo(HaveOccurred())
		var paths []string
		for _, e := range trash {
			paths = append(paths, e.Paths...)
		}
		Expect(paths).To(ContainElement("/Projects/acme"))
	})

	It("refuses to trash a folder whose manifest names another project", func() {
		p, _ := svc.Create(ctx, "alice", projects.CreateInput{Name: "Acme"})
		other := projects.Manifest{Version: 1, ID: "someone-else", Name: "Other"}
		write("alice", "/Projects/acme/project.json", string(other.Marshal()))
		Expect(svc.Delete(ctx, "alice", p.ID)).To(MatchError(projects.ErrNotFound))
		Expect(exists("alice", "/Projects/acme/files")).To(BeTrue())
	})

	Describe("List", func() {
		var p projects.Project
		BeforeEach(func() {
			p, _ = svc.Create(ctx, "alice", projects.CreateInput{Name: "Acme"})
		})
		only := func() projects.Project {
			list, err := svc.List(ctx, "alice")
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(HaveLen(1))
			return list[0]
		}
		It("sees a deleted manifest", func() {
			Expect(f.DeleteHome(ctx, "alice", "/Projects/acme/project.json")).To(Succeed())
			got := only()
			Expect(got.Status).To(Equal(projects.StatusBroken))
			Expect(got.Error).NotTo(BeEmpty())
		})
		It("sees invalid JSON", func() {
			write("alice", "/Projects/acme/project.json", "{nope")
			Expect(only().Status).To(Equal(projects.StatusBroken))
		})
		It("sees the archived flag set by hand", func() {
			m := projects.Manifest{Version: 1, ID: p.ID, Name: "Acme", Archived: true}
			write("alice", "/Projects/acme/project.json", string(m.Marshal()))
			Expect(only().Status).To(Equal(projects.StatusArchived))
		})
		It("sees a new name", func() {
			m := projects.Manifest{Version: 1, ID: p.ID, Name: "Renamed By Hand"}
			write("alice", "/Projects/acme/project.json", string(m.Marshal()))
			Expect(only().Name).To(Equal("Renamed By Hand"))
		})
	})

	Describe("Reindex edge cases", func() {
		It("frees the slug of a gone project for a new folder", func() {
			old, _ := svc.Create(ctx, "alice", projects.CreateInput{Name: "Acme"})
			Expect(f.DeleteHome(ctx, "alice", "/Projects/acme")).To(Succeed())
			write("alice", "/Projects/acme/project.json", `{"version":1,"name":"New Acme"}`)
			Expect(svc.Reindex(ctx, "alice")).To(Succeed())
			Expect(svc.Reindex(ctx, "alice")).To(Succeed())
			list, err := svc.List(ctx, "alice")
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(HaveLen(2))
			for _, x := range list {
				if x.ID == old.ID {
					Expect(x.Status).To(Equal(projects.StatusBroken))
				} else {
					Expect(x.Status).To(Equal(projects.StatusActive))
					Expect(x.Name).To(Equal("New Acme"))
					Expect(x.Slug).To(Equal("acme"))
				}
			}
		})

		It("frees the slug when the folder is already gone in the index", func() {
			_, _ = svc.Create(ctx, "alice", projects.CreateInput{Name: "Acme"})
			Expect(f.DeleteHome(ctx, "alice", "/Projects/acme")).To(Succeed())
			Expect(svc.Reindex(ctx, "alice")).To(Succeed())
			write("alice", "/Projects/acme/project.json", `{"version":1,"name":"New Acme"}`)
			Expect(svc.Reindex(ctx, "alice")).To(Succeed())
			list, _ := svc.List(ctx, "alice")
			Expect(list).To(HaveLen(2))
		})

		It("says a folder moved out of Projects is outside it", func() {
			p, _ := svc.Create(ctx, "alice", projects.CreateInput{Name: "Acme"})
			from, _ := f.Abs("alice", "/Projects/acme")
			to, _ := f.Abs("alice", "/Elsewhere/acme")
			_, err := f.Store().Move(ctx, f.Actor("alice"), from, to)
			Expect(err).NotTo(HaveOccurred())
			Expect(svc.Reindex(ctx, "alice")).To(Succeed())
			got, _ := store.Get(ctx, "alice", p.ID)
			Expect(got.Status).To(Equal(projects.StatusBroken))
			Expect(got.Error).To(ContainSubstring("outside"))
		})

		It("keeps the row of a folder whose manifest lost its ID", func() {
			p, _ := svc.Create(ctx, "alice", projects.CreateInput{Name: "Acme"})
			write("alice", "/Projects/acme/project.json", `{"version":1,"name":"Acme"}`)
			Expect(svc.Reindex(ctx, "alice")).To(Succeed())
			list, _ := svc.List(ctx, "alice")
			Expect(list).To(HaveLen(1))
			Expect(list[0].ID).To(Equal(p.ID))
			Expect(list[0].Status).To(Equal(projects.StatusActive))
			m, _ := readManifest("alice", "acme")
			Expect(m.ID).To(Equal(p.ID))
		})

		It("adopts a new manifest ID and marks the old row broken", func() {
			old, _ := svc.Create(ctx, "alice", projects.CreateInput{Name: "Acme"})
			m := projects.Manifest{Version: 1, ID: "adopted-id", Name: "Acme"}
			write("alice", "/Projects/acme/project.json", string(m.Marshal()))
			Expect(svc.Reindex(ctx, "alice")).To(Succeed())
			got, err := store.Get(ctx, "alice", "adopted-id")
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Status).To(Equal(projects.StatusActive))
			gone, err := store.Get(ctx, "alice", old.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(gone.Status).To(Equal(projects.StatusBroken))
		})
	})

	It("refuses a user who is not permitted", func() {
		svc = newSvc(func(context.Context, string) bool { return false })
		_, err := svc.Create(ctx, "alice", projects.CreateInput{Name: "Acme"})
		Expect(err).To(MatchError(projects.ErrForbidden))
		_, err = svc.List(ctx, "alice")
		Expect(err).To(MatchError(projects.ErrForbidden))
	})

	Describe("rewrites that are safe against other writers", func() {
		const unknown = `"futureField": {"a":1}`
		seed := func() projects.Project {
			p, err := svc.Create(ctx, "alice", projects.CreateInput{Name: "Acme"})
			Expect(err).NotTo(HaveOccurred())
			text, ok, err := f.ReadHome(ctx, "alice", "/Projects/acme/project.json", 1<<20)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
			m, err := projects.ParseManifest([]byte(text))
			Expect(err).NotTo(HaveOccurred())
			m.Extra = map[string]json.RawMessage{"futureField": json.RawMessage(`{"a":1}`)}
			write("alice", "/Projects/acme/project.json", string(m.Marshal()))
			return p
		}
		raw := func() string {
			text, _, err := f.ReadHome(ctx, "alice", "/Projects/acme/project.json", 1<<20)
			Expect(err).NotTo(HaveOccurred())
			return text
		}
		str := func(s string) *string { return &s }

		It("keeps an unknown field through an Update", func() {
			p := seed()
			_, err := svc.Update(ctx, "alice", p.ID, projects.UpdateInput{Name: str("Renamed")})
			Expect(err).NotTo(HaveOccurred())
			Expect(raw()).To(ContainSubstring(unknown))
			m, err := readManifest("alice", "acme")
			Expect(err).NotTo(HaveOccurred())
			Expect(m.Name).To(Equal("Renamed"))
		})

		It("keeps an unknown field through Archive and Restore", func() {
			p := seed()
			_, err := svc.Archive(ctx, "alice", p.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(raw()).To(ContainSubstring(unknown))
			_, err = svc.Restore(ctx, "alice", p.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(raw()).To(ContainSubstring(unknown))
		})

		It("lets two concurrent updates both succeed or report ErrConflict, and leaves a valid file", func() {
			p := seed()
			names := []string{"First", "Second"}
			errs := make([]error, len(names))
			var wg sync.WaitGroup
			for i, n := range names {
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer GinkgoRecover()
					_, errs[i] = svc.Update(ctx, "alice", p.ID, projects.UpdateInput{Name: str(n)})
				}()
			}
			wg.Wait()
			succeeded := 0
			for _, err := range errs {
				if err != nil {
					Expect(err).To(MatchError(projects.ErrConflict))
					continue
				}
				succeeded++
			}
			Expect(succeeded).To(BeNumerically(">=", 1))
			m, err := readManifest("alice", "acme")
			Expect(err).NotTo(HaveOccurred())
			Expect(names).To(ContainElement(m.Name))
			Expect(raw()).To(ContainSubstring(unknown))
			Expect(strings.Count(raw(), `"name"`)).To(Equal(1))
		})

		It("never loses archived when Archive races Update", func() {
			p := seed()
			var wg sync.WaitGroup
			var archErr, updErr error
			wg.Add(2)
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				_, archErr = svc.Archive(ctx, "alice", p.ID)
			}()
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				_, updErr = svc.Update(ctx, "alice", p.ID, projects.UpdateInput{Description: str("racing")})
			}()
			wg.Wait()
			Expect(archErr).NotTo(HaveOccurred())
			if updErr != nil {
				Expect(updErr).To(MatchError(projects.ErrConflict))
			}
			m, err := readManifest("alice", "acme")
			Expect(err).NotTo(HaveOccurred())
			Expect(m.Archived).To(BeTrue())
		})

		// racing wraps the files service and, after a read of the manifest,
		// changes the file the way another frontend would.
		racing := func(times int) *projects.Service {
			rh := &racingHost{FileHost: f, left: times}
			rh.mutate = func(text string) string {
				m, err := projects.ParseManifest([]byte(text))
				Expect(err).NotTo(HaveOccurred())
				rh.n++
				m.Description = fmt.Sprintf("other %d", rh.n)
				return string(m.Marshal())
			}
			return projects.NewService(projects.Options{Store: store, Files: rh})
		}

		It("re-reads once after a change and then succeeds", func() {
			p := seed()
			rsvc := racing(2)
			_, err := rsvc.Update(ctx, "alice", p.ID, projects.UpdateInput{Name: str("Mine")})
			Expect(err).NotTo(HaveOccurred())
			m, err := readManifest("alice", "acme")
			Expect(err).NotTo(HaveOccurred())
			Expect(m.Name).To(Equal("Mine"))
			Expect(m.Description).To(HavePrefix("other"), "the other writer's change is kept")
		})

		It("gives ErrConflict when the file keeps changing, and writes nothing", func() {
			p := seed()
			rsvc := racing(100)
			for _, call := range []func() error{
				func() error {
					_, err := rsvc.Update(ctx, "alice", p.ID, projects.UpdateInput{Name: str("Mine")})
					return err
				},
				func() error { _, err := rsvc.Archive(ctx, "alice", p.ID); return err },
			} {
				Expect(call()).To(MatchError(projects.ErrConflict))
			}
			m, err := readManifest("alice", "acme")
			Expect(err).NotTo(HaveOccurred())
			Expect(m.Name).To(Equal("Acme"))
			Expect(m.Archived).To(BeFalse())
		})

		It("skips the writeback when a person set an ID meanwhile, leaves no row under the minted ID, and adopts theirs next pass", func() {
			_, err := svc.Create(ctx, "alice", projects.CreateInput{Name: "Acme"})
			Expect(err).NotTo(HaveOccurred())
			write("alice", "/Projects/acme/project.json", `{"version": 1, "name": "Acme"}`)
			before, err := store.List(ctx, "alice")
			Expect(err).NotTo(HaveOccurred())
			rh := &racingHost{FileHost: f, left: 1}
			rh.mutate = func(string) string { return `{"version": 1, "id": "mine-1", "name": "Acme"}` }
			rsvc := projects.NewService(projects.Options{Store: store, Files: rh})
			Expect(rsvc.Reindex(ctx, "alice")).To(Succeed())
			rows, err := store.List(ctx, "alice")
			Expect(err).NotTo(HaveOccurred())
			Expect(rows).To(HaveLen(len(before)), "no extra row under a minted ID")
			Expect(raw()).To(ContainSubstring(`"id": "mine-1"`))
			Expect(rsvc.Reindex(ctx, "alice")).To(Succeed())
			got, err := store.Get(ctx, "alice", "mine-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Status).To(Equal(projects.StatusActive))
		})

		// transient makes the first reindex pass fail with ErrConflict: the
		// writeback of the hand-made folder sees a change twice (each attempt
		// reads the file twice, so four changes are injected). It returns
		// the host so a spec can count the passes.
		transient := func() (*projects.Service, *racingHost) {
			rh := &racingHost{FileHost: f, left: 4, match: "hand/"}
			rh.mutate = func(text string) string {
				m, err := projects.ParseManifest([]byte(text))
				Expect(err).NotTo(HaveOccurred())
				rh.n++
				m.Description = fmt.Sprintf("other %d", rh.n)
				return string(m.Marshal())
			}
			return projects.NewService(projects.Options{Store: store, Files: rh}), rh
		}

		It("lists after one transient conflict of the reindex, which takes two passes", func() {
			write("alice", "/Projects/hand/project.json", `{"version": 1, "name": "Hand"}`)
			rsvc, rh := transient()
			Expect(rsvc.Reindex(ctx, "alice")).To(MatchError(projects.ErrConflict), "the fixture does conflict")
			Expect(rh.n).To(Equal(4))
			passesPerReindex := rh.ensures
			Expect(passesPerReindex).To(BeNumerically(">", 0))

			// Same state again: the first pass conflicts and the retry heals it.
			write("alice", "/Projects/hand/project.json", `{"version": 1, "name": "Hand"}`)
			rsvc, rh = transient()
			list, err := rsvc.List(ctx, "alice")
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(HaveLen(1))
			Expect(rh.ensures).To(Equal(2*passesPerReindex), "the retry ran a second Reindex")
		})

		It("gets after one transient conflict of the reindex, which takes two passes", func() {
			p, err := svc.Create(ctx, "alice", projects.CreateInput{Name: "Acme"})
			Expect(err).NotTo(HaveOccurred())
			from, _ := f.Abs("alice", "/Projects/acme")
			to, _ := f.Abs("alice", "/Projects/renamed")
			_, err = f.Store().Move(ctx, f.Actor("alice"), from, to)
			Expect(err).NotTo(HaveOccurred())
			write("alice", "/Projects/hand/project.json", `{"version": 1, "name": "Hand"}`)
			rsvc, rh := transient()
			r, err := rsvc.Get(ctx, "alice", p.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(r.Slug).To(Equal("renamed"))
			Expect(rh.n).To(Equal(4), "the writeback saw two changes")
			Expect(rh.ensures).To(BeNumerically(">=", 2), "Get ran the reindex twice")
		})

		It("does not overwrite a concurrent edit when the reindex writes back a new ID", func() {
			_, err := svc.Create(ctx, "alice", projects.CreateInput{Name: "Acme"})
			Expect(err).NotTo(HaveOccurred())
			write("alice", "/Projects/acme/project.json", `{"version": 1, "name": "Acme", "futureField": {"a":1}}`)
			rsvc := racing(1)
			Expect(rsvc.Reindex(ctx, "alice")).To(Succeed())
			m, err := readManifest("alice", "acme")
			Expect(err).NotTo(HaveOccurred())
			Expect(m.ID).NotTo(BeEmpty())
			Expect(m.Description).To(Equal("other 1"))
			Expect(raw()).To(ContainSubstring(unknown))
		})
	})
})

// racingHost changes the manifest after a read of it, up to left times, the
// way a second frontend writing the same file would.
type racingHost struct {
	projects.FileHost
	left, n int
	mutate  func(text string) string
	// match limits the changes to paths that contain it; empty means all.
	match string
	// ensures counts EnsureHome calls, which every Reindex pass makes once.
	ensures int
}

func (h *racingHost) EnsureHome(ctx context.Context, principal string) error {
	h.ensures++
	return h.FileHost.EnsureHome(ctx, principal)
}

func (h *racingHost) ReadHome(ctx context.Context, principal, rel string, max int64) (string, bool, error) {
	text, ok, err := h.FileHost.ReadHome(ctx, principal, rel, max)
	if err == nil && ok && strings.HasSuffix(rel, "/project.json") && strings.Contains(rel, h.match) && h.left > 0 {
		h.left--
		_, err = h.FileHost.WriteHome(ctx, principal, rel, h.mutate(text), "other writer", max)
	}
	return text, ok, err
}

package skills

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

var (
	admin = workspace.Actor{Principal: "admin"}
	alice = workspace.Actor{Principal: "alice", ChatID: "c1"}
)

// newStore opens the GORM workspace store on a SQLite file, as the product does.
func newStore() workspace.Store {
	dsn := "file:" + filepath.Join(GinkgoT().TempDir(), "ws.db") + "?_busy_timeout=5000&_txlock=immediate"
	db, err := sqlitedb.Open(dsn, &gorm.Config{})
	Expect(err).NotTo(HaveOccurred())
	blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
	Expect(err).NotTo(HaveOccurred())
	st, err := gormstore.New(db, blobs, gormstore.Options{Admin: "admin"})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})
	return st
}

func skillText(name, desc string) string {
	return "---\nname: " + name + "\ndescription: " + desc + "\n---\nBody of " + name + ".\n"
}

// put writes a file as the admin, who can write everywhere.
func put(st workspace.Store, p, content string) {
	_, err := st.Put(context.Background(), admin, p, strings.NewReader(content), workspace.PutOptions{})
	Expect(err).NotTo(HaveOccurred(), "put %s", p)
}

func grant(st workspace.Store, who, p string, m workspace.Mode) {
	_, err := st.Mkdir(context.Background(), admin, p)
	if err != nil {
		Expect(errors.Is(err, workspace.ErrExists)).To(BeTrue(), "mkdir %s: %v", p, err)
	}
	Expect(st.Grant(context.Background(), admin, who, p, m)).To(Succeed())
}

// layers is a store where alice can read all four layers.
func layers() (workspace.Store, []Root) {
	st := newStore()
	grant(st, "alice", "/users/alice", workspace.ModeWrite)
	grant(st, "alice", "/skills", workspace.ModeRead)
	grant(st, "alice", "/builtin-skills", workspace.ModeRead)
	grant(st, "alice", "/Shared/Team", workspace.ModeWrite)
	roots := []Root{
		{Layer: LayerBuiltin, Path: "/builtin-skills", Guest: "/workspace/shared-builtin-skills"},
		{Layer: LayerShared, Path: "/skills", Guest: "/workspace/shared-skills"},
		{Layer: LayerPersonal, Path: "/users/alice/.skills", Guest: "/workspace/home/.skills"},
		{Layer: LayerFolder, Path: "/users/alice", Guest: "/workspace/home"},
		{Layer: LayerFolder, Path: "/Shared/Team", Guest: "/workspace/shared-Team"},
	}
	return st, roots
}

func byPath(skills []Skill) map[string]Skill {
	m := map[string]Skill{}
	for _, s := range skills {
		m[s.Path] = s
	}
	return m
}

// failStore fails List for one path, as a broken disk would.
type failStore struct {
	workspace.Store
	path string
}

func (f failStore) List(ctx context.Context, a workspace.Actor, p string) ([]workspace.Node, error) {
	if p == f.path {
		return nil, errors.New("disk on fire")
	}
	return f.Store.List(ctx, a, p)
}

// growStore reports a small size for SKILL.md but serves more bytes, as a
// file that grew between Open and read.
type growStore struct{ workspace.Store }

func (g growStore) Open(ctx context.Context, a workspace.Actor, p string) (io.ReadSeekCloser, workspace.Node, error) {
	rc, n, err := g.Store.Open(ctx, a, p)
	n.Size = 10
	return rc, n, err
}

var _ = Describe("Resolve", func() {
	It("lets the nearest layer win and shadows the others", func() {
		st, roots := layers()
		put(st, "/builtin-skills/report/SKILL.md", skillText("report", "Built-in report."))
		put(st, "/skills/report/SKILL.md", skillText("report", "Shared report."))
		put(st, "/users/alice/.skills/report/SKILL.md", skillText("report", "Personal report."))
		put(st, "/Shared/Team/Projects/.skills/report/SKILL.md", skillText("report", "Folder report."))
		put(st, "/skills/invoices/SKILL.md", skillText("invoices", "Shared invoices."))

		got, notes, err := Resolve(context.Background(), st, alice, roots, Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(notes).To(BeEmpty())
		m := byPath(got)
		want := map[string]struct {
			layer    Layer
			shadowed string
			guest    string
		}{
			"/Shared/Team/Projects/.skills/report": {LayerFolder, "", "/workspace/shared-Team/Projects/.skills/report/SKILL.md"},
			"/users/alice/.skills/report":          {LayerPersonal, "folder", "/workspace/home/.skills/report/SKILL.md"},
			"/skills/report":                       {LayerShared, "folder", "/workspace/shared-skills/report/SKILL.md"},
			"/builtin-skills/report":               {LayerBuiltin, "folder", "/workspace/shared-builtin-skills/report/SKILL.md"},
			"/skills/invoices":                     {LayerShared, "", "/workspace/shared-skills/invoices/SKILL.md"},
		}
		Expect(got).To(HaveLen(len(want)))
		for p, w := range want {
			s, ok := m[p]
			Expect(ok).To(BeTrue(), "%s: missing", p)
			Expect(s.Layer).To(Equal(w.layer), p)
			Expect(s.Shadowed).To(Equal(w.shadowed), p)
			Expect(s.GuestPath).To(Equal(w.guest), p)
			Expect(s.Invalid).To(BeEmpty(), p)
			Expect(s.Hash).To(HaveLen(64), p)
		}
		// The result is ordered by layer, nearest first, then by name.
		var order []string
		for _, s := range got {
			order = append(order, string(s.Layer)+"/"+s.Name)
		}
		Expect(strings.Join(order, " ")).To(Equal("folder/report personal/report shared/invoices shared/report builtin/report"))
	})

	It("does not duplicate the personal layer in the home walk", func() {
		st, roots := layers()
		put(st, "/users/alice/.skills/mine/SKILL.md", skillText("mine", "Mine."))
		got, _, err := Resolve(context.Background(), st, alice, roots, Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(1))
		Expect(got[0].Layer).To(Equal(LayerPersonal))
	})

	It("uncovers the skill below when a skill is switched off", func() {
		st, roots := layers()
		put(st, "/skills/report/SKILL.md", skillText("report", "Shared report."))
		put(st, "/users/alice/.skills/report/SKILL.md", skillText("report", "Personal report."))
		off := func(s Skill) bool { return s.Layer == LayerPersonal && s.Name == "report" }
		got, _, err := Resolve(context.Background(), st, alice, roots, Options{Off: off})
		Expect(err).NotTo(HaveOccurred())
		m := byPath(got)
		p := m["/users/alice/.skills/report"]
		Expect(p.Off).To(BeTrue(), "personal: %+v", p)
		Expect(p.Shadowed).To(BeEmpty(), "personal: %+v", p)
		s := m["/skills/report"]
		Expect(s.Off).To(BeFalse(), "shared should win now: %+v", s)
		Expect(s.Shadowed).To(BeEmpty(), "shared should win now: %+v", s)
	})

	It("lets a deeper folder win and sorts equal depth by path", func() {
		st, roots := layers()
		put(st, "/Shared/Team/.skills/x/SKILL.md", skillText("x", "Shallow."))
		put(st, "/Shared/Team/A/B/.skills/x/SKILL.md", skillText("x", "Deep."))
		put(st, "/Shared/Team/C/.skills/y/SKILL.md", skillText("y", "C."))
		put(st, "/Shared/Team/A/.skills/y/SKILL.md", skillText("y", "A."))
		got, _, err := Resolve(context.Background(), st, alice, roots, Options{})
		Expect(err).NotTo(HaveOccurred())
		m := byPath(got)
		Expect(m["/Shared/Team/A/B/.skills/x"].Shadowed).To(BeEmpty(), "deeper folder should win: %+v", got)
		Expect(m["/Shared/Team/.skills/x"].Shadowed).To(Equal("folder"), "deeper folder should win: %+v", got)
		Expect(m["/Shared/Team/A/.skills/y"].Shadowed).To(BeEmpty(), "the path that sorts first should win at equal depth: %+v", got)
		Expect(m["/Shared/Team/C/.skills/y"].Shadowed).To(Equal("folder"), "the path that sorts first should win at equal depth: %+v", got)
	})

	It("does not show a folder the actor cannot read", func() {
		st, roots := layers()
		put(st, "/Secret/.skills/s/SKILL.md", skillText("s", "Secret."))
		put(st, "/users/bob/.skills/b/SKILL.md", skillText("b", "Bob's."))
		roots = append(roots,
			Root{Layer: LayerFolder, Path: "/Secret", Guest: "/workspace/shared-Secret"},
			Root{Layer: LayerPersonal, Path: "/users/bob/.skills", Guest: "/workspace/other"},
		)
		// A readable skill in the same store proves that Resolve finds things at all.
		put(st, "/skills/ok/SKILL.md", skillText("ok", "Readable."))
		got, notes, err := Resolve(context.Background(), st, alice, roots, Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(1), "got %+v", got)
		Expect(got[0].Path).To(Equal("/skills/ok"))
		Expect(notes).To(BeEmpty())
	})

	It("lists an invalid skill with its reason", func() {
		st, roots := layers()
		put(st, "/skills/nofront/SKILL.md", "Just text.\n")
		put(st, "/skills/big/SKILL.md", skillText("big", "Big.")+strings.Repeat("y", MaxFileSize))
		put(st, "/skills/Bad Name/SKILL.md", skillText("Bad", "Bad."))
		put(st, "/skills/assets/logo.txt", "not a skill") // no SKILL.md: not listed at all
		put(st, "/skills/fine/SKILL.md", skillText("fine", "Fine."))
		got, _, err := Resolve(context.Background(), st, alice, roots, Options{})
		Expect(err).NotTo(HaveOccurred())
		reasons := map[string]string{}
		for _, s := range got {
			reasons[s.Path] = s.Invalid
		}
		Expect(reasons).To(Equal(map[string]string{"/skills/nofront": "no frontmatter", "/skills/big": "too large", "/skills/Bad Name": "bad name", "/skills/fine": ""}))
	})

	It("reads only two hundred files, nearest first", func() {
		old := maxSkillFiles
		maxSkillFiles = 3
		DeferCleanup(func() { maxSkillFiles = old })
		st, roots := layers()
		for i := 0; i < 3; i++ {
			put(st, fmt.Sprintf("/builtin-skills/b%d/SKILL.md", i), skillText(fmt.Sprintf("b%d", i), "Built-in."))
		}
		put(st, "/users/alice/.skills/p/SKILL.md", skillText("p", "Personal."))
		put(st, "/Shared/Team/.skills/f/SKILL.md", skillText("f", "Folder."))
		got, notes, err := Resolve(context.Background(), st, alice, roots, Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(3), "notes %v", notes)
		Expect(notes).To(Equal([]string{NoteNotRead}))
		Expect(got[0].Name).To(Equal("f"), "the nearest layers should be read first: %+v", got)
		Expect(got[1].Name).To(Equal("p"), "the nearest layers should be read first: %+v", got)
		Expect(got[2].Layer).To(Equal(LayerBuiltin), "the nearest layers should be read first: %+v", got)
	})

	Describe("the folder walk limits", func() {
		var (
			st    workspace.Store
			roots []Root
		)
		BeforeEach(func() {
			st, roots = layers()
			put(st, "/Shared/Team/a/b/c/.skills/deep/SKILL.md", skillText("deep", "Deep."))
			put(st, "/Shared/Team/.skills/top/SKILL.md", skillText("top", "Top."))
		})

		It("stops at the depth limit with a note", func() {
			old := maxWalkDepth
			maxWalkDepth = 2
			DeferCleanup(func() { maxWalkDepth = old })
			got, notes, err := Resolve(context.Background(), st, alice, roots, Options{})
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(HaveLen(1), "got %+v, notes %v", got, notes)
			Expect(got[0].Name).To(Equal("top"))
			Expect(notes).To(Equal([]string{NoteNotSearched}))
		})

		It("stops at the folder count with a note", func() {
			old := maxWalkFolders
			maxWalkFolders = 2
			DeferCleanup(func() { maxWalkFolders = old })
			_, notes, err := Resolve(context.Background(), st, alice, roots, Options{})
			Expect(err).NotTo(HaveOccurred())
			Expect(notes).To(Equal([]string{NoteNotSearched}))
		})

		It("gives no note when no limit is hit", func() {
			got, notes, err := Resolve(context.Background(), st, alice, roots, Options{})
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(HaveLen(2), "got %+v", got)
			Expect(notes).To(BeEmpty())
		})
	})

	It("does not walk a root skip and does not charge it to the budget", func() {
		st, roots := layers()
		put(st, "/Shared/Team/.skills/team/SKILL.md", skillText("team", "Team."))
		put(st, "/users/alice/Chats/c1/.skills/upload/SKILL.md", skillText("upload", "In a chat folder."))
		for i := 0; i < 5; i++ {
			put(st, fmt.Sprintf("/users/alice/Chats/c%d/f/x.txt", i), "x")
		}
		// The home walk comes first, as in a chat, and the budget is small.
		for i := range roots {
			if roots[i].Layer == LayerFolder && roots[i].Path == "/users/alice" {
				roots[i].Skip = []string{"/users/alice/Chats"}
			}
		}
		old := maxWalkFolders
		maxWalkFolders = 4
		DeferCleanup(func() { maxWalkFolders = old })
		got, notes, err := Resolve(context.Background(), st, alice, roots, Options{})
		Expect(err).NotTo(HaveOccurred())
		var names []string
		for _, sk := range got {
			names = append(names, sk.Name)
		}
		Expect(names).To(Equal([]string{"team"}))
		Expect(notes).To(BeEmpty())
	})

	It("logs the walk count at debug level", func() {
		st, roots := layers()
		put(st, "/Shared/Team/.skills/team/SKILL.md", skillText("team", "Team."))
		var buf strings.Builder
		log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		_, _, err := Resolve(context.Background(), st, alice, roots, Options{Logger: log})
		Expect(err).NotTo(HaveOccurred())
		Expect(buf.String()).To(ContainSubstring("level=DEBUG"))
		Expect(buf.String()).To(ContainSubstring("folders="))
	})

	It("returns a store error", func() {
		st, roots := layers()
		put(st, "/skills/a/SKILL.md", skillText("a", "A."))
		_, _, err := Resolve(context.Background(), failStore{st, "/skills"}, alice, roots, Options{})
		Expect(err).To(MatchError(ContainSubstring("disk on fire")))
		_, _, err = Resolve(context.Background(), failStore{st, "/Shared/Team"}, alice, roots, Options{})
		Expect(err).To(MatchError(ContainSubstring("disk on fire")), "walk err")
	})

	It("reads only skill files", func() {
		st, roots := layers()
		put(st, "/skills/a/SKILL.md", skillText("a", "A."))
		put(st, "/skills/a/references/big.md", strings.Repeat("z", 5<<20))
		got, _, err := Resolve(context.Background(), st, alice, roots, Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(1), "got %+v", got)
	})

	It("cleans a description and keeps it on the skill", func() {
		st, roots := layers()
		put(st, "/skills/a/SKILL.md", "---\nname: a\ndescription: \"Use when\\n   filing \x00 it.\"\n---\n")
		got, _, err := Resolve(context.Background(), st, alice, roots, Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(1), "got %+v", got)
		Expect(got[0].Description).To(Equal("Use when filing it."))
	})

	It("counts the frontmatter name, not the folder name", func() {
		st, roots := layers()
		put(st, "/skills/old-folder/SKILL.md", skillText("real-name", "Renamed."))
		put(st, "/builtin-skills/real-name/SKILL.md", skillText("real-name", "Built-in."))
		got, _, err := Resolve(context.Background(), st, alice, roots, Options{})
		Expect(err).NotTo(HaveOccurred())
		m := byPath(got)
		s := m["/skills/old-folder"]
		Expect(s.Name).To(Equal("real-name"), "shared: %+v", s)
		Expect(s.Shadowed).To(BeEmpty(), "shared: %+v", s)
		Expect(s.GuestPath).To(Equal("/workspace/shared-skills/old-folder/SKILL.md"), "shared: %+v", s)
		Expect(m["/builtin-skills/real-name"].Shadowed).To(Equal("shared"), "builtin should lose to the renamed skill")
	})

	It("probes only a thousand folders", func() {
		old := maxProbes
		maxProbes = 2
		DeferCleanup(func() { maxProbes = old })
		st, roots := layers()
		// Folders without a SKILL.md use up probes too.
		put(st, "/skills/a1/notes.txt", "x")
		put(st, "/skills/a2/notes.txt", "x")
		put(st, "/skills/z/SKILL.md", skillText("z", "Z."))
		got, notes, err := Resolve(context.Background(), st, alice, roots, Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeEmpty())
		Expect(notes).To(Equal([]string{NoteNotRead}))
	})

	It("counts folder depth below the walked root", func() {
		st := newStore()
		grant(st, "alice", "/users/alice", workspace.ModeWrite)
		grant(st, "alice", "/Shared", workspace.ModeWrite)
		roots := []Root{
			{Layer: LayerFolder, Path: "/users/alice", Guest: "/workspace/home"},
			{Layer: LayerFolder, Path: "/Shared", Guest: "/workspace/shared"},
		}
		// Both skills sit one folder below their root. The home one is deeper in
		// the workspace tree, but that must not decide: the path does.
		put(st, "/users/alice/P/.skills/x/SKILL.md", skillText("x", "Home."))
		put(st, "/Shared/P/.skills/x/SKILL.md", skillText("x", "Shared."))
		got, _, err := Resolve(context.Background(), st, alice, roots, Options{})
		Expect(err).NotTo(HaveOccurred())
		m := byPath(got)
		Expect(m["/Shared/P/.skills/x"].Shadowed).To(BeEmpty(), "equal relative depth should compare by path: %+v", got)
		Expect(m["/users/alice/P/.skills/x"].Shadowed).To(Equal("folder"), "equal relative depth should compare by path: %+v", got)
	})

	// The store returns ErrInvalid in two odd cases, and Resolve skips both: a
	// layer folder that is a file (List) and a SKILL.md that is a folder (Open).
	// Every other store error is returned (see "returns a store error").
	It("skips an odd shape instead of failing", func() {
		st, roots := layers()
		put(st, "/skills/dirskill/SKILL.md/inner.txt", "a folder named SKILL.md")
		put(st, "/skills/ok/SKILL.md", skillText("ok", "Fine."))
		roots = append(roots, Root{Layer: LayerBuiltin, Path: "/skills/ok/SKILL.md", Guest: "/workspace/odd"})
		got, _, err := Resolve(context.Background(), st, alice, roots, Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(1), "got %+v", got)
		Expect(got[0].Name).To(Equal("ok"))
	})

	It("gives one entry for nested folder roots", func() {
		st, roots := layers()
		put(st, "/Shared/Team/Sub/.skills/x/SKILL.md", skillText("x", "Nested."))
		roots = append(roots, Root{Layer: LayerFolder, Path: "/Shared/Team/Sub", Guest: "/workspace/sub"})
		got, _, err := Resolve(context.Background(), st, alice, roots, Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(1), "got %+v", got)
		Expect(got[0].Shadowed).To(BeEmpty())
		Expect(got[0].GuestPath).To(Equal("/workspace/sub/.skills/x/SKILL.md"))
		// With the roots the other way round the result is the same.
		rev := []Root{roots[len(roots)-1], roots[4]}
		got, _, err = Resolve(context.Background(), st, alice, rev, Options{})
		Expect(err).NotTo(HaveOccurred(), "reversed")
		Expect(got).To(HaveLen(1), "reversed: %+v", got)
		Expect(got[0].Shadowed).To(BeEmpty(), "reversed")
		Expect(got[0].GuestPath).To(Equal("/workspace/sub/.skills/x/SKILL.md"), "reversed")
	})

	It("marks a file that grew as too large and does not hash it", func() {
		st, roots := layers()
		put(st, "/skills/big/SKILL.md", skillText("big", "Big.")+strings.Repeat("y", MaxFileSize))
		got, _, err := Resolve(context.Background(), growStore{st}, alice, roots, Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(1), "got %+v", got)
		Expect(got[0].Invalid).To(Equal("too large"))
		Expect(got[0].Hash).To(BeEmpty())
	})
})

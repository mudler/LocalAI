package pages_test

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/pages"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

func newFiles() *files.Service {
	dsn := "file:" + filepath.Join(GinkgoT().TempDir(), "ws.db") + "?_busy_timeout=5000&_txlock=immediate"
	db, err := sqlitedb.Open(dsn, &gorm.Config{})
	Expect(err).NotTo(HaveOccurred())
	blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
	Expect(err).NotTo(HaveOccurred())
	ws, err := gormstore.New(db, blobs, gormstore.Options{Admin: files.AdminPrincipal})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})
	return files.New(ws)
}

// put writes a file in the user's home as the user.
func put(f *files.Service, user, rel, body string) {
	_, err := f.Store().Put(context.Background(), f.Actor(user), session.HomePath(user)+rel, strings.NewReader(body), workspace.PutOptions{Overwrite: true})
	Expect(err).NotTo(HaveOccurred())
}

// putAsChat writes a file the way a chat's tools do: a change set of the chat.
func putAsChat(f *files.Service, user, rel, body string) {
	ctx := context.Background()
	tx, err := f.Store().Begin(ctx, workspace.Actor{Principal: user, ChatID: "c1"}, "the assistant writes "+rel)
	Expect(err).NotTo(HaveOccurred())
	_, err = tx.Put(ctx, session.HomePath(user)+rel, strings.NewReader(body), workspace.PutOptions{Overwrite: true})
	Expect(err).NotTo(HaveOccurred())
	_, err = tx.Commit()
	Expect(err).NotTo(HaveOccurred())
}

var _ = Describe("page names and paths", func() {
	It("takes lowercase names of letters, digits and dashes", func() {
		for _, ok := range []string{"tasks", "a", "task-board", "0day", strings.Repeat("a", 64)} {
			Expect(pages.ValidName(ok)).To(BeTrue(), ok)
		}
		for _, bad := range []string{"", "-tasks", "Tasks", "tasks_1", "tasks/x", "..", ".", strings.Repeat("a", 65), "täsks", "a b", "tasks\n"} {
			Expect(pages.ValidName(bad)).To(BeFalse(), bad)
		}
	})

	It("keeps a path of the bridge inside the page folder", func() {
		for _, ok := range []string{"data.json", "data/items.json", "a b.txt"} {
			got, err := pages.CleanPath(ok)
			Expect(err).NotTo(HaveOccurred(), ok)
			Expect(got).To(Equal(ok))
		}
		for _, bad := range []string{
			"", "/etc/passwd", "../x", "a/../../x", "./x", "a/./x", "a//b", "a/", "..",
			"a\\b", "..\\x", "x\x00", "x\ny", "x\u0085", "C:/x", strings.Repeat("a/", 9) + "x",
			strings.Repeat("x", 300),
		} {
			_, err := pages.CleanPath(bad)
			Expect(err).To(MatchError(pages.ErrBadPath), "%q", bad)
		}
	})
})

var _ = Describe("the pages service", func() {
	var (
		ctx context.Context
		f   *files.Service
		s   *pages.Service
		now time.Time
	)

	BeforeEach(func() {
		ctx = context.Background()
		f = newFiles()
		Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
		Expect(f.EnsureHome(ctx, "bob")).To(Succeed())
		now = time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
		s = pages.New(f, pages.WithClock(func() time.Time { return now }))
	})

	It("lists the folders of pages/ that hold an index.html, with their metadata", func() {
		Expect(s.List(ctx, "alice")).To(BeEmpty())
		putAsChat(f, "alice", "/pages/tasks/index.html", "<p>tasks</p>")
		put(f, "alice", "/pages/tasks/page.json", `{"title":"Tasks board","icon":"list-checks","description":"What I do this week"}`)
		put(f, "alice", "/pages/notes/index.html", "<p>notes</p>")
		put(f, "alice", "/pages/empty/readme.txt", "no page here")
		put(f, "alice", "/pages/Bad_Name/index.html", "<p>bad</p>")
		put(f, "alice", "/pages/stray.html", "<p>not a folder</p>")

		list, err := s.List(ctx, "alice")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(2))
		Expect(list[0].Name).To(Equal("notes"))
		Expect(list[0].Title).To(Equal("notes"))
		Expect(list[0].Author).To(Equal(pages.AuthorYou))
		Expect(list[1].Name).To(Equal("tasks"))
		Expect(list[1].Title).To(Equal("Tasks board"))
		Expect(list[1].Icon).To(Equal("list-checks"))
		Expect(list[1].Description).To(Equal("What I do this week"))
		Expect(list[1].Source).To(Equal(pages.SourceOwn))
		Expect(list[1].Author).To(Equal(pages.AuthorAssistant), "the assistant wrote index.html")
		Expect(list[1].UpdatedBy).To(Equal(pages.AuthorYou), "the user wrote page.json last")
		Expect(list[1].Path).To(Equal("/pages/tasks"))
		Expect(list[1].UpdatedAt).NotTo(BeZero())
	})

	It("ignores a page.json that is not valid and caps its fields", func() {
		put(f, "alice", "/pages/tasks/index.html", "<p>tasks</p>")
		put(f, "alice", "/pages/tasks/page.json", `{"title": 12`)
		p, err := s.Get(ctx, "alice", "tasks")
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Title).To(Equal("tasks"))
		put(f, "alice", "/pages/tasks/page.json", `{"title":"`+strings.Repeat("x", 500)+`","icon":"<script>"}`)
		p, err = s.Get(ctx, "alice", "tasks")
		Expect(err).NotTo(HaveOccurred())
		Expect(len(p.Title)).To(BeNumerically("<=", 120))
		Expect(p.Icon).To(BeEmpty(), "an icon is a plain name")
	})

	It("never shows one user the pages of another", func() {
		put(f, "alice", "/pages/tasks/index.html", "<p>alice</p>")
		Expect(s.List(ctx, "bob")).To(BeEmpty())
		_, err := s.Get(ctx, "bob", "tasks")
		Expect(err).To(MatchError(pages.ErrNotFound))
		_, _, err = s.Source(ctx, "bob", "tasks")
		Expect(err).To(MatchError(pages.ErrNotFound))
		_, err = s.Read(ctx, "bob", "tasks", "index.html")
		Expect(err).To(MatchError(pages.ErrNotFound))
	})

	It("serves the source of a page up to 1 MiB", func() {
		put(f, "alice", "/pages/tasks/index.html", "<p>tasks</p>")
		p, html, err := s.Source(ctx, "alice", "tasks")
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Name).To(Equal("tasks"))
		Expect(html).To(Equal("<p>tasks</p>"))
		put(f, "alice", "/pages/big/index.html", strings.Repeat("x", pages.MaxFileSize+1))
		_, _, err = s.Source(ctx, "alice", "big")
		Expect(err).To(MatchError(pages.ErrTooLarge))
		_, _, err = s.Source(ctx, "alice", "BAD")
		Expect(err).To(MatchError(pages.ErrBadName))
	})

	Context("the bridge", func() {
		BeforeEach(func() {
			put(f, "alice", "/pages/tasks/index.html", "<p>tasks</p>")
			put(f, "alice", "/pages/tasks/tasks.json", `[]`)
			put(f, "alice", "/secret.txt", "outside")
			put(f, "alice", "/pages/other/data.json", `{"other":true}`)
			put(f, "alice", "/pages/other/index.html", "<p>other</p>")
		})

		It("reads, writes and lists files of the page's own folder", func() {
			got, err := s.Read(ctx, "alice", "tasks", "tasks.json")
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Content).To(Equal("[]"))
			Expect(got.Path).To(Equal("tasks.json"))

			w, err := s.Write(ctx, "alice", "tasks", "tasks.json", []byte(`[{"t":"a"}]`), 0)
			Expect(err).NotTo(HaveOccurred())
			Expect(w.Version).To(BeNumerically(">", got.Version))
			_, err = s.Write(ctx, "alice", "tasks", "data/archive.json", []byte(`[]`), 0)
			Expect(err).NotTo(HaveOccurred())

			list, err := s.ListFiles(ctx, "alice", "tasks", "")
			Expect(err).NotTo(HaveOccurred())
			names := []string{}
			for _, e := range list {
				names = append(names, e.Path+":"+e.Kind)
			}
			Expect(names).To(Equal([]string{"data:dir", "index.html:file", "tasks.json:file"}))
			sub, err := s.ListFiles(ctx, "alice", "tasks", "data")
			Expect(err).NotTo(HaveOccurred())
			Expect(sub).To(HaveLen(1))
			Expect(sub[0].Path).To(Equal("data/archive.json"))

			again, err := s.Read(ctx, "alice", "tasks", "tasks.json")
			Expect(err).NotTo(HaveOccurred())
			Expect(again.Content).To(Equal(`[{"t":"a"}]`))
		})

		It("refuses paths outside the folder, another page's files and bad names", func() {
			for _, bad := range []string{"../other/data.json", "../../secret.txt", "/secret.txt", "a\\..\\b", "x\x01"} {
				_, err := s.Read(ctx, "alice", "tasks", bad)
				Expect(err).To(MatchError(pages.ErrBadPath), bad)
				_, err = s.Write(ctx, "alice", "tasks", bad, []byte("x"), 0)
				Expect(err).To(MatchError(pages.ErrBadPath), bad)
				_, err = s.ListFiles(ctx, "alice", "tasks", bad)
				Expect(err).To(MatchError(pages.ErrBadPath), bad)
			}
			_, err := s.Read(ctx, "alice", "tasks", "missing.json")
			Expect(err).To(MatchError(pages.ErrNotFound))
			// A page that does not exist cannot write: a write never creates a page.
			_, err = s.Write(ctx, "alice", "ghost", "data.json", []byte("x"), 0)
			Expect(err).To(MatchError(pages.ErrNotFound))
			n, err := f.Store().Stat(ctx, f.Actor("alice"), session.HomePath("alice")+"/secret.txt")
			Expect(err).NotTo(HaveOccurred())
			Expect(n.Version).To(Equal(int64(1)))
		})

		It("does not let a page change its own code or metadata", func() {
			for _, p := range []string{"index.html", "page.json", "INDEX.HTML"} {
				_, err := s.Write(ctx, "alice", "tasks", p, []byte("<p>pwned</p>"), 0)
				Expect(err).To(MatchError(pages.ErrReadOnly), p)
			}
		})

		It("limits a file to 1 MiB and reads only text", func() {
			_, err := s.Write(ctx, "alice", "tasks", "big.txt", make([]byte, pages.MaxFileSize+1), 0)
			Expect(err).To(MatchError(pages.ErrTooLarge))
			put(f, "alice", "/pages/tasks/blob.bin", "\xff\xfe\x00")
			_, err = s.Read(ctx, "alice", "tasks", "blob.bin")
			Expect(err).To(MatchError(pages.ErrNotText))
			_, err = s.Read(ctx, "alice", "tasks", "")
			Expect(err).To(MatchError(pages.ErrBadPath))
		})

		It("reads an image of the folder as bytes, and nothing else", func() {
			png := "\x89PNG\r\n\x1a\n\x00\x00"
			put(f, "alice", "/pages/tasks/img/logo.png", png)
			put(f, "alice", "/pages/tasks/icon.SVG", "<svg/>")
			put(f, "alice", "/pages/tasks/blob.bin", "\xff\xfe\x00")
			put(f, "alice", "/pages/tasks/logo.png.js", "alert(1)")
			put(f, "alice", "/secret.png", png)

			img, err := s.ReadImage(ctx, "alice", "tasks", "img/logo.png")
			Expect(err).NotTo(HaveOccurred())
			Expect(string(img.Data)).To(Equal(png))
			Expect(img.MediaType).To(Equal("image/png"))
			Expect(img.Path).To(Equal("img/logo.png"))
			svg, err := s.ReadImage(ctx, "alice", "tasks", "icon.SVG")
			Expect(err).NotTo(HaveOccurred())
			Expect(svg.MediaType).To(Equal("image/svg+xml"))

			for _, other := range []string{"blob.bin", "tasks.json", "index.html", "logo.png.js", "logo"} {
				_, err := s.ReadImage(ctx, "alice", "tasks", other)
				Expect(err).To(MatchError(pages.ErrNotImage), other)
			}
			for _, bad := range []string{"../../secret.png", "/secret.png", "..\\..\\secret.png", "img/../../../secret.png", "", "a//b.png"} {
				_, err := s.ReadImage(ctx, "alice", "tasks", bad)
				Expect(err).To(MatchError(pages.ErrBadPath), bad)
			}
			_, err = s.ReadImage(ctx, "alice", "tasks", "missing.png")
			Expect(err).To(MatchError(pages.ErrNotFound))
			_, err = s.ReadImage(ctx, "bob", "tasks", "img/logo.png")
			Expect(err).To(MatchError(pages.ErrNotFound), "another user's page")
			put(f, "alice", "/pages/tasks/big.png", strings.Repeat("x", pages.MaxFileSize+1))
			_, err = s.ReadImage(ctx, "alice", "tasks", "big.png")
			Expect(err).To(MatchError(pages.ErrTooLarge))
		})

		It("keeps both versions when a write starts from an old version", func() {
			got, err := s.Read(ctx, "alice", "tasks", "tasks.json")
			Expect(err).NotTo(HaveOccurred())
			_, err = s.Write(ctx, "alice", "tasks", "tasks.json", []byte(`[1]`), got.Version)
			Expect(err).NotTo(HaveOccurred())
			w, err := s.Write(ctx, "alice", "tasks", "tasks.json", []byte(`[2]`), got.Version)
			Expect(err).NotTo(HaveOccurred())
			Expect(w.ConflictOf).To(Equal("tasks.json"))
			Expect(w.Path).NotTo(Equal("tasks.json"))
		})

		It("limits the rate of writes per page", func() {
			var err error
			for i := 0; i < 100 && err == nil; i++ {
				_, err = s.Write(ctx, "alice", "tasks", "tasks.json", []byte(`[]`), 0)
			}
			Expect(err).To(MatchError(pages.ErrRateLimited))
			// Another page has its own budget.
			_, err = s.Write(ctx, "alice", "other", "data.json", []byte(`{}`), 0)
			Expect(err).NotTo(HaveOccurred())
			now = now.Add(time.Minute)
			_, err = s.Write(ctx, "alice", "tasks", "tasks.json", []byte(`[]`), 0)
			Expect(err).NotTo(HaveOccurred())
		})

		It("limits the rate of chat messages per page", func() {
			allowed := 0
			for range 20 {
				if s.AllowSend("alice", "tasks") {
					allowed++
				}
			}
			Expect(allowed).To(BeNumerically(">=", 1))
			Expect(allowed).To(BeNumerically("<", 20))
			Expect(s.AllowSend("bob", "tasks")).To(BeTrue())
		})
	})

	Context("starters", func() {
		It("ships a task board and copies it into pages/ under a free name", func() {
			list := s.Starters()
			ids := []string{}
			for _, st := range list {
				Expect(st.Title).NotTo(BeEmpty())
				ids = append(ids, st.ID)
			}
			Expect(ids).To(Equal([]string{"reading-list", "task-board"}))

			p, err := s.CopyStarter(ctx, "alice", "task-board")
			Expect(err).NotTo(HaveOccurred())
			Expect(p.Name).To(Equal("task-board"))
			_, html, err := s.Source(ctx, "alice", "task-board")
			Expect(err).NotTo(HaveOccurred())
			Expect(html).To(ContainSubstring("localai.files"))
			_, err = s.Read(ctx, "alice", "task-board", "tasks.json")
			Expect(err).NotTo(HaveOccurred())

			p2, err := s.CopyStarter(ctx, "alice", "task-board")
			Expect(err).NotTo(HaveOccurred())
			Expect(p2.Name).To(Equal("task-board-2"))

			_, err = s.CopyStarter(ctx, "alice", "../x")
			Expect(err).To(MatchError(pages.ErrNotFound))
		})

		It("ships a reading list that reads and writes reading.json", func() {
			p, err := s.CopyStarter(ctx, "alice", "reading-list")
			Expect(err).NotTo(HaveOccurred())
			Expect(p.Title).To(Equal("Reading list"))
			_, html, err := s.Source(ctx, "alice", "reading-list")
			Expect(err).NotTo(HaveOccurred())
			Expect(html).To(ContainSubstring("reading.json"))
			Expect(html).NotTo(ContainSubstring("innerHTML ="), "an item is data")
			_, err = s.Read(ctx, "alice", "reading-list", "reading.json")
			Expect(err).NotTo(HaveOccurred())
		})
	})
})

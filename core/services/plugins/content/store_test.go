package content_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/plugins/content"
	"github.com/mudler/LocalAI/core/services/plugins/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/jsonstore"
)

const root = "/plugins"

// clock is the time of the workspace store's change sets, which GC reads.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) Set(t time.Time) { c.mu.Lock(); defer c.mu.Unlock(); c.now = t }

func newWorkspace(c *clock) content.Workspace {
	blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
	Expect(err).NotTo(HaveOccurred())
	ws, err := jsonstore.New("", blobs, jsonstore.Options{Admin: "admin", Now: c.Now})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = ws.Close() })
	return content.Workspace{Files: ws, Actor: workspace.Actor{Principal: "admin"}}
}

func readAll(ctx context.Context, ws content.Workspace, digest, p string) string {
	GinkgoHelper()
	r, err := content.Open(ctx, ws, root, digest, p)
	Expect(err).NotTo(HaveOccurred())
	defer r.Close()
	b, err := io.ReadAll(r)
	Expect(err).NotTo(HaveOccurred())
	return string(b)
}

func staged(ctx context.Context, ws content.Workspace) []workspace.Node {
	GinkgoHelper()
	list, err := ws.Files.List(ctx, ws.Actor, root+"/"+content.StagingDir)
	if errors.Is(err, workspace.ErrNotFound) {
		return nil
	}
	Expect(err).NotTo(HaveOccurred())
	return list
}

var _ = Describe("the package files", func() {
	var (
		ctx context.Context
		c   *clock
		ws  content.Workspace
	)
	BeforeEach(func() {
		ctx = context.Background()
		c = &clock{now: time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)}
		ws = newWorkspace(c)
	})

	checked := func(files map[string]string) (string, content.Tree, string) {
		dir := tree(files)
		t, err := content.Check(dir, content.DefaultLimits())
		Expect(err).NotTo(HaveOccurred())
		return dir, t, content.Digest(t)
	}

	It("writes a package once and reads its files", func() {
		dir, t, d := checked(map[string]string{"skills/a/SKILL.md": "# a", ".claude-plugin/plugin.json": "{}"})
		Expect(content.Write(ctx, ws, root, d, dir, t)).To(Succeed())
		Expect(readAll(ctx, ws, d, "skills/a/SKILL.md")).To(Equal("# a"))
		Expect(content.Exists(ctx, ws, root, d)).To(BeTrue())

		// A second write of the same digest finds the folder and does nothing,
		// even when the source folder is gone.
		Expect(os.RemoveAll(dir)).To(Succeed())
		Expect(content.Write(ctx, ws, root, d, dir, t)).To(Succeed())
		Expect(staged(ctx, ws)).To(BeEmpty())
	})

	It("leaves one folder when two writers race for one digest", func() {
		dir, t, d := checked(map[string]string{"skills/a/SKILL.md": "# a", "commands/b.md": "b"})
		var wg sync.WaitGroup
		errs := make([]error, 4)
		for i := range errs {
			wg.Add(1)
			go func(i int) {
				defer GinkgoRecover()
				defer wg.Done()
				errs[i] = content.Write(ctx, ws, root, d, dir, t)
			}(i)
		}
		wg.Wait()
		for _, e := range errs {
			Expect(e).NotTo(HaveOccurred())
		}
		list, err := ws.Files.List(ctx, ws.Actor, root)
		Expect(err).NotTo(HaveOccurred())
		names := []string{}
		for _, n := range list {
			if n.Name != content.StagingDir {
				names = append(names, n.Name)
			}
		}
		Expect(names).To(HaveLen(1))
		Expect(staged(ctx, ws)).To(BeEmpty(), "a losing writer must clear its staging folder")
		Expect(readAll(ctx, ws, d, "commands/b.md")).To(Equal("b"))
	})

	It("refuses a digest that is not the digest of the tree", func() {
		dir, t, _ := checked(map[string]string{"a.md": "a"})
		_, _, other := checked(map[string]string{"a.md": "b"})
		err := content.Write(ctx, ws, root, other, dir, t)
		Expect(errors.Is(err, content.ErrDigestMismatch)).To(BeTrue(), "got %v", err)
		Expect(content.Write(ctx, ws, root, "sha256:nothex", dir, t)).NotTo(Succeed())
	})

	It("refuses a file that changed after the check and leaves nothing", func() {
		dir, t, d := checked(map[string]string{"a.md": "a", "b.md": "b"})
		Expect(os.WriteFile(filepath.Join(dir, "b.md"), []byte("changed"), 0o644)).To(Succeed())
		err := content.Write(ctx, ws, root, d, dir, t)
		Expect(errors.Is(err, content.ErrDigestMismatch)).To(BeTrue(), "got %v", err)
		Expect(content.Exists(ctx, ws, root, d)).To(BeFalse())
		Expect(staged(ctx, ws)).To(BeEmpty())
	})

	It("opens only clean paths of a package", func() {
		dir, t, d := checked(map[string]string{"a.md": "a"})
		Expect(content.Write(ctx, ws, root, d, dir, t)).To(Succeed())
		_, err := content.Open(ctx, ws, root, d, "../a.md")
		Expect(errors.Is(err, content.ErrRefused)).To(BeTrue())
		_, err = content.Open(ctx, ws, root, "sha256:../../x", "a.md")
		Expect(err).To(HaveOccurred())
	})

	Describe("garbage collection", func() {
		var st *store.FileStore
		BeforeEach(func() {
			var err error
			st, err = store.NewFileStore("")
			Expect(err).NotTo(HaveOccurred())
		})

		write := func(body string, created time.Time, installed bool) string {
			dir, t, d := checked(map[string]string{"skills/a/SKILL.md": body})
			Expect(content.Write(ctx, ws, root, d, dir, t)).To(Succeed())
			Expect(st.PutPackage(ctx, store.Package{Digest: d, CreatedAt: created})).To(Succeed())
			if installed {
				Expect(st.InstallTx(ctx, func(tx store.Tx) error {
					return tx.CreateInstall(store.Install{ID: body, PluginID: body + "@m", Scope: store.ScopeInstance, Digest: d})
				})).To(Succeed())
			}
			return d
		}

		It("deletes old unreferenced packages and keeps referenced and young ones", func() {
			now := c.Now()
			old := now.Add(-48 * time.Hour)
			gone := write("gone", old, false)
			kept := write("kept", old, true)
			young := write("young", now.Add(-time.Hour), false)

			res, err := content.Collect(ctx, ws, root, st, now)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Packages).To(Equal([]string{gone}))
			Expect(content.Exists(ctx, ws, root, gone)).To(BeFalse())
			_, err = st.Package(ctx, gone)
			Expect(errors.Is(err, store.ErrNotFound)).To(BeTrue())
			for _, d := range []string{kept, young} {
				Expect(content.Exists(ctx, ws, root, d)).To(BeTrue())
				_, err = st.Package(ctx, d)
				Expect(err).NotTo(HaveOccurred())
			}
		})

		It("deletes an old folder that has no package row, and old staging folders", func() {
			c.Set(c.Now().Add(-48 * time.Hour))
			dir, t, orphan := checked(map[string]string{"x.md": "orphan"})
			Expect(content.Write(ctx, ws, root, orphan, dir, t)).To(Succeed())
			_, err := ws.Files.Put(ctx, ws.Actor, root+"/"+content.StagingDir+"/abandoned/a.md", stringsReader("x"), workspace.PutOptions{})
			Expect(err).NotTo(HaveOccurred())
			c.Set(c.Now().Add(48 * time.Hour))
			dir2, t2, fresh := checked(map[string]string{"x.md": "fresh"})
			Expect(content.Write(ctx, ws, root, fresh, dir2, t2)).To(Succeed())

			res, err := content.Collect(ctx, ws, root, st, c.Now())
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Orphans).To(Equal([]string{orphan}))
			Expect(res.Staging).To(Equal(1))
			Expect(content.Exists(ctx, ws, root, orphan)).To(BeFalse())
			Expect(content.Exists(ctx, ws, root, fresh)).To(BeTrue(), "a young folder may belong to an install in flight")
			Expect(staged(ctx, ws)).To(BeEmpty())
		})

		It("runs without a database lock on a server without a database", func() {
			gone := write("gone", c.Now().Add(-48*time.Hour), false)
			ran, err := content.GC(ctx, nil, ws, root, st, c.Now())
			Expect(err).NotTo(HaveOccurred())
			Expect(ran).To(BeTrue())
			Expect(content.Exists(ctx, ws, root, gone)).To(BeFalse())
		})

		It("does nothing before the root exists", func() {
			res, err := content.Collect(ctx, ws, root, st, c.Now())
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Packages).To(BeEmpty())
		})
	})
})

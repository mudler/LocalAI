package files_test

import (
	"context"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

func newFilesWithBlobs() *files.Service {
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
	return files.New(ws, files.WithBlobs(blobs))
}

var _ = Describe("earlier versions of a file", func() {
	put := func(f *files.Service, who, rel, body string) {
		abs, err := f.Abs(who, rel)
		Expect(err).NotTo(HaveOccurred())
		_, err = f.Store().Put(context.Background(), f.Actor(who), abs, strings.NewReader(body), workspace.PutOptions{Overwrite: true, Title: "edit"})
		Expect(err).NotTo(HaveOccurred())
	}

	It("lists the contents a file had, newest first", func() {
		ctx := context.Background()
		f := newFilesWithBlobs()
		Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
		put(f, "alice", "/persona.json", "one")
		put(f, "alice", "/other.json", "x")
		put(f, "alice", "/persona.json", "two")
		put(f, "alice", "/persona.json", "three")
		abs, _ := f.Abs("alice", "/persona.json")
		var got []string
		Expect(f.Versions(ctx, "alice", abs, 10, 1024, func(v files.FileVersion) bool {
			got = append(got, string(v.Data))
			Expect(v.Rev).To(BeNumerically(">", 0))
			return true
		})).To(Succeed())
		Expect(got).To(Equal([]string{"three", "two", "one"}))
	})

	It("stops when the callback says so and skips versions over the size cap", func() {
		ctx := context.Background()
		f := newFilesWithBlobs()
		Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
		put(f, "alice", "/a.json", "small")
		put(f, "alice", "/a.json", strings.Repeat("x", 100))
		put(f, "alice", "/a.json", "last")
		abs, _ := f.Abs("alice", "/a.json")
		var got []string
		Expect(f.Versions(ctx, "alice", abs, 10, 50, func(v files.FileVersion) bool {
			got = append(got, string(v.Data))
			return len(got) < 2
		})).To(Succeed())
		Expect(got).To(Equal([]string{"last", "small"}))
	})

	It("shows another user nothing", func() {
		ctx := context.Background()
		f := newFilesWithBlobs()
		Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
		Expect(f.EnsureHome(ctx, "bob")).To(Succeed())
		put(f, "alice", "/persona.json", "secret")
		abs, _ := f.Abs("alice", "/persona.json")
		n := 0
		_ = f.Versions(ctx, "bob", abs, 10, 1024, func(files.FileVersion) bool { n++; return true })
		Expect(n).To(BeZero())
	})

	It("says versions are unavailable without a blob store", func() {
		ctx := context.Background()
		f := newFiles()
		Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
		abs, _ := f.Abs("alice", "/persona.json")
		Expect(f.Versions(ctx, "alice", abs, 10, 1024, func(files.FileVersion) bool { return true })).To(MatchError(files.ErrNoVersions))
	})
})

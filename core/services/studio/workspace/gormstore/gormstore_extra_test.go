package gormstore_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/core/services/studio/workspace/wstest"
)

var _ = Describe("The SQLite store beyond the contract", func() {
	for _, kind := range []string{"fs", "objectstore"} {
		Describe("purge frees blob bytes on the "+kind+" blob store", func() {
			newBlobs := func() blob.Store {
				if kind == "fs" {
					b, err := blob.NewFS(GinkgoT().TempDir())
					Expect(err).ToNot(HaveOccurred())
					return b
				}
				objects, err := storage.NewFilesystemStore(GinkgoT().TempDir())
				Expect(err).ToNot(HaveOccurred())
				return blob.NewObjectStore(objects, GinkgoT().TempDir())
			}
			wstest.RunPurgeBlobs(func() (workspace.Store, blob.Store) {
				blobs := newBlobs()
				db, err := auth.OpenSQLiteFile(filepath.Join(GinkgoT().TempDir(), "ws.db"))
				Expect(err).ToNot(HaveOccurred())
				s, err := gormstore.New(db, blobs, gormstore.Options{Admin: wstest.Admin, BlobGrace: -1})
				Expect(err).ToNot(HaveOccurred())
				DeferCleanup(func() {
					if raw, err := db.DB(); err == nil {
						_ = raw.Close()
					}
				})
				return s, blobs
			})
		})
	}

	// A purge on one frontend is seen by the other at once: they share the
	// database, so there is nothing to broadcast.
	It("a purge on one frontend is seen by the other", func() {
		dir := GinkgoT().TempDir()
		dbPath := filepath.Join(dir, "ws.db")
		blobs, err := blob.NewFS(filepath.Join(dir, "blobs"))
		Expect(err).ToNot(HaveOccurred())
		open := func() *gormstore.Workspace {
			db, err := auth.OpenSQLiteFile(dbPath)
			Expect(err).ToNot(HaveOccurred())
			s, err := gormstore.New(db, blobs, gormstore.Options{Admin: wstest.Admin, BlobGrace: -1})
			Expect(err).ToNot(HaveOccurred())
			return s
		}
		a, b := open(), open()
		ctx := context.Background()
		_, err = a.Put(ctx, wstest.AdminActor, "/x.txt", strings.NewReader("x"), workspace.PutOptions{})
		Expect(err).ToNot(HaveOccurred())
		Expect(a.Delete(ctx, wstest.AdminActor, "/x.txt")).To(Succeed())
		res, err := b.PurgeAll(ctx, wstest.AdminActor, "/", false)
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Files).To(Equal(1))
		res, err = a.PurgeAll(ctx, wstest.AdminActor, "/", false)
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Files).To(BeZero(), "the first frontend purged it again")
	})

	It("a file over the limit is refused and leaves nothing", func() {
		s := sqliteStore(gormstore.Options{MaxFileSize: 10})
		_, err := s.Put(context.Background(), wstest.AdminActor, "/big.txt", strings.NewReader(strings.Repeat("x", 100)), workspace.PutOptions{})
		Expect(err).To(MatchError(workspace.ErrTooLarge), "Put over the limit")
		_, err = s.Stat(context.Background(), wstest.AdminActor, "/big.txt")
		Expect(err).To(MatchError(workspace.ErrNotFound), "the refused file exists")
	})

	// Two frontends share one database and one blob store. Each opens its own
	// Workspace; what one writes the other reads, and their change sets never
	// share a revision.
	It("two frontends share one database and keep revisions apart", func() {
		dir := GinkgoT().TempDir()
		dbPath := filepath.Join(dir, "ws.db")
		blobs, err := blob.NewFS(filepath.Join(dir, "blobs"))
		Expect(err).ToNot(HaveOccurred())
		open := func() workspace.Store {
			db, err := auth.OpenSQLiteFile(dbPath)
			Expect(err).ToNot(HaveOccurred())
			s, err := gormstore.New(db, blobs, gormstore.Options{Admin: wstest.Admin})
			Expect(err).ToNot(HaveOccurred())
			return s
		}
		a, b := open(), open()
		ctx := context.Background()
		var wg sync.WaitGroup
		for i := range 20 {
			wg.Add(1)
			go func(i int) {
				defer GinkgoRecover()
				defer wg.Done()
				s := a
				if i%2 == 1 {
					s = b
				}
				_, err := s.Put(ctx, wstest.AdminActor, fmt.Sprintf("/f%d.txt", i), strings.NewReader(fmt.Sprint(i)), workspace.PutOptions{})
				Expect(err).ToNot(HaveOccurred(), "put %d", i)
			}(i)
		}
		wg.Wait()
		Expect(wstest.MustRead(b, wstest.AdminActor, "/f0.txt")).To(Equal("0"), "frontend B reads from A's write")
		hist, err := a.History(ctx, wstest.AdminActor, workspace.HistoryQuery{Limit: 100})
		Expect(err).ToNot(HaveOccurred())
		seen := map[int64]bool{}
		for _, cs := range hist {
			Expect(seen).ToNot(HaveKey(cs.Rev), "revision %d appears twice", cs.Rev)
			seen[cs.Rev] = true
		}
		Expect(seen).To(HaveLen(20), "history change sets")
	})

	// A restart with a data path keeps the files: the database file and the blob
	// folder are all there is, and a new process reads them as they were.
	It("files survive reopening the database and the blob folder", func() {
		dir := GinkgoT().TempDir()
		dbPath := filepath.Join(dir, "ws.db")
		open := func() (workspace.Store, func()) {
			db, err := auth.OpenSQLiteFile(dbPath)
			Expect(err).ToNot(HaveOccurred())
			blobs, err := blob.NewFS(filepath.Join(dir, "blobs"))
			Expect(err).ToNot(HaveOccurred())
			s, err := gormstore.New(db, blobs, gormstore.Options{Admin: wstest.Admin})
			Expect(err).ToNot(HaveOccurred())
			return s, func() {
				if raw, err := db.DB(); err == nil {
					_ = raw.Close()
				}
			}
		}
		ctx := context.Background()
		first, closeFirst := open()
		wstest.MustPut(first, wstest.AdminActor, "/Docs/kept.txt", "still here")
		Expect(first.Delete(ctx, wstest.AdminActor, "/Docs/kept.txt")).To(Succeed())
		wstest.MustPut(first, wstest.AdminActor, "/Docs/other.txt", "me too")
		closeFirst()

		second, closeSecond := open()
		defer closeSecond()
		Expect(wstest.MustRead(second, wstest.AdminActor, "/Docs/other.txt")).To(Equal("me too"), "after reopening, other.txt")
		// What was deleted is still undoable after the restart: nothing was erased.
		hist, err := second.History(ctx, wstest.AdminActor, workspace.HistoryQuery{Path: "/Docs", Limit: 10})
		Expect(err).ToNot(HaveOccurred())
		var deleteRev int64
		for _, cs := range hist {
			for _, op := range cs.Ops {
				if op.Kind == workspace.OpDelete {
					deleteRev = cs.Rev
				}
			}
		}
		Expect(deleteRev).ToNot(BeZero(), "the delete is not in the history after reopening: %+v", hist)
		_, _, err = second.Undo(ctx, wstest.AdminActor, deleteRev, workspace.UndoOptions{})
		Expect(err).ToNot(HaveOccurred(), "undo after reopening")
		Expect(wstest.MustRead(second, wstest.AdminActor, "/Docs/kept.txt")).To(Equal("still here"), "after the undo, kept.txt")
	})
})

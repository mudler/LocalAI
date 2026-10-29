package files_test

import (
	"context"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/files"
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

var _ = Describe("the files service", func() {
	It("gives each user a home of their own", func() {
		ctx := context.Background()
		f := newFiles()
		for _, u := range []string{"alice", "bob"} {
			Expect(f.EnsureHome(ctx, u)).To(Succeed(), "EnsureHome(%s)", u)
		}
		alice, bob := workspace.Actor{Principal: "alice"}, workspace.Actor{Principal: "bob"}
		_, err := f.Store().Put(ctx, alice, session.HomePath("alice")+"/notes.txt", strings.NewReader("mine"), workspace.PutOptions{})
		Expect(err).NotTo(HaveOccurred(), "alice cannot write in her home")
		_, err = f.Store().Stat(ctx, alice, session.ChatsPath("alice"))
		Expect(err).NotTo(HaveOccurred(), "alice has no Chats folder")
		// Bob cannot see Alice's home, its files, or even that it exists.
		for _, p := range []string{session.HomePath("alice"), session.HomePath("alice") + "/notes.txt", session.UsersRoot} {
			_, err := f.Store().Stat(ctx, bob, p)
			Expect(err).To(MatchError(workspace.ErrNotFound), "bob Stat(%s)", p)
		}
		_, err = f.Store().Put(ctx, bob, session.HomePath("alice")+"/x.txt", strings.NewReader("x"), workspace.PutOptions{})
		Expect(err).To(MatchError(workspace.ErrNotFound), "bob Put into alice's home")
		// Asking again changes nothing and does not fail.
		Expect(f.EnsureHome(ctx, "alice")).To(Succeed(), "second EnsureHome")
	})

	It("lets nobody take the admin principal or an unsafe one", func() {
		f := newFiles()
		for _, p := range []string{files.AdminPrincipal, "", "a/b", "..", "a\x00b"} {
			Expect(f.EnsureHome(context.Background(), p)).NotTo(Succeed(), "EnsureHome(%q) succeeded, want an error", p)
		}
	})

	It("says why when there is no workspace", func() {
		f := files.Disabled("There is no data path, so files are off.")
		Expect(f.Available()).To(BeFalse())
		Expect(f.Unavailable()).NotTo(BeEmpty())
		Expect(f.Store()).To(BeNil())
		Expect(f.EnsureHome(context.Background(), "alice")).To(MatchError(files.ErrUnavailable), "EnsureHome on a disabled service")
	})

	It("keeps paths relative to the user's home so they cannot escape it", func() {
		f := newFiles()
		abs, err := f.Abs("alice", "/Reports/q1.txt")
		Expect(err).NotTo(HaveOccurred())
		Expect(abs).To(Equal(session.HomePath("alice") + "/Reports/q1.txt"))
		Expect(f.Rel("alice", abs)).To(Equal("/Reports/q1.txt"))
		got, _ := f.Abs("alice", "/")
		Expect(got).To(Equal(session.HomePath("alice")), "Abs(/) should be the home")
		for _, bad := range []string{"/../bob", "/a/../../bob", "/a\x00b", "/a//../..", "/.."} {
			got, err := f.Abs("alice", bad)
			Expect(err).To(HaveOccurred(), "Abs(%q) = %q, want an error", bad, got)
		}
		// A path in another home is not shown as a relative path.
		Expect(f.Rel("alice", session.HomePath("bob")+"/x")).To(BeEmpty(), "Rel of another home")
	})

	It("keeps a deleted file in the trash until it is restored", func() {
		ctx := context.Background()
		f := newFiles()
		Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
		alice := f.Actor("alice")
		put := func(rel string) {
			abs, _ := f.Abs("alice", rel)
			_, err := f.Store().Put(ctx, alice, abs, strings.NewReader("data"), workspace.PutOptions{Overwrite: true})
			Expect(err).NotTo(HaveOccurred())
		}
		put("/Docs/a.txt")
		put("/Docs/b.txt")
		absDocs, _ := f.Abs("alice", "/Docs")
		Expect(f.Store().Delete(ctx, alice, absDocs)).To(Succeed())
		trash, err := f.Trash(ctx, "alice", 50)
		Expect(err).NotTo(HaveOccurred())
		Expect(trash).To(HaveLen(1), "want one entry for the folder /Docs, not its files")
		Expect(trash[0].Paths).To(Equal([]string{"/Docs"}))
		_, _, err = f.Store().Undo(ctx, alice, trash[0].Rev, workspace.UndoOptions{})
		Expect(err).NotTo(HaveOccurred(), "restore")
		after, _ := f.Trash(ctx, "alice", 50)
		Expect(after).To(BeEmpty(), "trash after the restore")
		Expect(f.EnsureHome(ctx, "bob")).To(Succeed())
		t2, _ := f.Trash(ctx, "bob", 50)
		Expect(t2).To(BeEmpty(), "bob sees entries in his trash")
	})
})

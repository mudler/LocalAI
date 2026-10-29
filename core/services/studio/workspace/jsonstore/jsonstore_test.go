package jsonstore_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/jsonstore"
	"github.com/mudler/LocalAI/core/services/studio/workspace/wstest"
)

func TestJSONStore(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Workspace JSON Store Suite")
}

// openStore opens a store on dir, with blobs in the same folder, and closes it
// with DeferCleanup.
func openStore(dir string, o jsonstore.Options) *jsonstore.Store {
	GinkgoHelper()
	blobs, err := blob.NewFS(filepath.Join(dir, "blobs"))
	Expect(err).ToNot(HaveOccurred())
	o.Admin = wstest.Admin
	s, err := jsonstore.New(filepath.Join(dir, "index"), blobs, o)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { _ = s.Close() })
	return s
}

var _ = Describe("The JSON store", func() {
	Describe("on a folder meets the contract", func() {
		wstest.RunStoreSuite(func() workspace.Store {
			return openStore(GinkgoT().TempDir(), jsonstore.Options{})
		})
	})

	Describe("in memory meets the contract", func() {
		wstest.RunStoreSuite(func() workspace.Store {
			blobs, err := blob.NewFS(GinkgoT().TempDir())
			Expect(err).ToNot(HaveOccurred())
			s, err := jsonstore.New("", blobs, jsonstore.Options{Admin: wstest.Admin})
			Expect(err).ToNot(HaveOccurred())
			return s
		})
	})

	It("a file over the limit is refused and leaves nothing", func() {
		s := openStore(GinkgoT().TempDir(), jsonstore.Options{MaxFileSize: 10})
		_, err := s.Put(context.Background(), wstest.AdminActor, "/big.txt", strings.NewReader(strings.Repeat("x", 100)), workspace.PutOptions{})
		Expect(err).To(MatchError(workspace.ErrTooLarge), "Put over the limit")
		_, err = s.Stat(context.Background(), wstest.AdminActor, "/big.txt")
		Expect(err).To(MatchError(workspace.ErrNotFound), "the refused file exists")
	})

	// A restart keeps the files, the trash and the history: the index folder and
	// the blob folder are all there is.
	It("files survive reopening the folder", func() {
		dir := GinkgoT().TempDir()
		ctx := context.Background()
		first := openStore(dir, jsonstore.Options{})
		wstest.MustPut(first, wstest.AdminActor, "/Docs/kept.txt", "still here")
		Expect(first.Delete(ctx, wstest.AdminActor, "/Docs/kept.txt")).To(Succeed())
		wstest.MustPut(first, wstest.AdminActor, "/Docs/other.txt", "me too")
		Expect(first.Grant(ctx, wstest.AdminActor, "user:alice", "/Docs", workspace.ModeRead)).To(Succeed())
		Expect(first.Close()).To(Succeed())

		second := openStore(dir, jsonstore.Options{})
		Expect(wstest.MustRead(second, wstest.AdminActor, "/Docs/other.txt")).To(Equal("me too"), "after reopening, other.txt")
		Expect(wstest.MustRead(second, wstest.Alice, "/Docs/other.txt")).To(Equal("me too"), "after reopening, alice reads: the grant was lost")
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

	// A crash can cut the last record of the log. The store drops that record,
	// keeps a copy of the damaged log, and opens with everything before it.
	It("a torn last record is dropped", func() {
		dir := GinkgoT().TempDir()
		first := openStore(dir, jsonstore.Options{})
		wstest.MustPut(first, wstest.AdminActor, "/a.txt", "one")
		wstest.MustPut(first, wstest.AdminActor, "/b.txt", "two")
		Expect(first.Close()).To(Succeed())
		logPath := filepath.Join(dir, "index", "index.log")
		raw, err := os.ReadFile(logPath)
		Expect(err).ToNot(HaveOccurred())
		Expect(os.WriteFile(logPath, raw[:len(raw)-20], 0o600)).To(Succeed())

		second := openStore(dir, jsonstore.Options{})
		Expect(wstest.MustRead(second, wstest.AdminActor, "/a.txt")).To(Equal("one"))
		_, err = second.Stat(context.Background(), wstest.AdminActor, "/b.txt")
		Expect(err).To(MatchError(workspace.ErrNotFound), "the cut write is visible")
		damaged, _ := filepath.Glob(logPath + ".damaged-*")
		Expect(damaged).To(HaveLen(1), "damaged copies")
		// The store writes on after the cut and a third start sees all of it.
		wstest.MustPut(second, wstest.AdminActor, "/c.txt", "three")
		Expect(second.Close()).To(Succeed())
		third := openStore(dir, jsonstore.Options{})
		Expect(wstest.MustRead(third, wstest.AdminActor, "/c.txt")).To(Equal("three"))
	})

	It("a folder open twice in one process is refused", func() {
		dir := GinkgoT().TempDir()
		first := openStore(dir, jsonstore.Options{})
		blobs, err := blob.NewFS(filepath.Join(dir, "blobs"))
		Expect(err).ToNot(HaveOccurred())
		_, err = jsonstore.New(filepath.Join(dir, "index"), blobs, jsonstore.Options{})
		Expect(err).To(HaveOccurred(), "a second store on the same folder opened")
		Expect(first.Close()).To(Succeed())
		again, err := jsonstore.New(filepath.Join(dir, "index"), blobs, jsonstore.Options{})
		Expect(err).ToNot(HaveOccurred(), "reopen after close")
		_ = again.Close()
	})

	// The log is folded into a snapshot when it grows. A restart reads the
	// snapshot and the rest of the log, and a log that a crash left whole after
	// the snapshot was written does not apply twice.
	It("the log is folded into a snapshot", func() {
		dir := GinkgoT().TempDir()
		first := openStore(dir, jsonstore.Options{CompactAt: 2000})
		for i := range 30 {
			wstest.MustPut(first, wstest.AdminActor, "/f"+strings.Repeat("x", i)+".txt", "body")
		}
		Expect(first.Close()).To(Succeed())
		index := filepath.Join(dir, "index")
		_, err := os.Stat(filepath.Join(index, "index.json"))
		Expect(err).ToNot(HaveOccurred(), "no snapshot after many writes")
		log, err := os.ReadFile(filepath.Join(index, "index.log"))
		Expect(err).ToNot(HaveOccurred())
		Expect(len(log)).To(BeNumerically("<=", 4000), "the log should have been folded")

		second := openStore(dir, jsonstore.Options{CompactAt: 2000})
		Expect(wstest.Tree(second)).To(HaveLen(30), "files after reopening")
		hist, err := second.History(context.Background(), wstest.AdminActor, workspace.HistoryQuery{Limit: 100})
		Expect(err).ToNot(HaveOccurred())
		Expect(hist).To(HaveLen(30), "history change sets")
		// A log written before the snapshot (a crash between the two steps) is skipped.
		Expect(second.Close()).To(Succeed())
		Expect(os.WriteFile(filepath.Join(index, "index.log"), log, 0o600)).To(Succeed())
		third := openStore(dir, jsonstore.Options{CompactAt: 2000})
		Expect(wstest.Tree(third)).To(HaveLen(30), "files after replaying a stale log")
	})
})

package wstest

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
)

func purger(s workspace.Store) workspace.Purger {
	GinkgoHelper()
	p, ok := s.(workspace.Purger)
	Expect(ok).To(BeTrue(), "the store is not a workspace.Purger")
	return p
}

// deleteAt deletes p in one change set and returns it.
func deleteAt(s workspace.Store, a workspace.Actor, p string) workspace.ChangeSet {
	GinkgoHelper()
	return commit(s, a, func(tx workspace.Tx) error { return tx.Delete(Ctx(), p) })
}

func describePurge(newStore NewStore) {
	Describe("Purge", func() {
		It("erases a deleted file for good", func() {
			s := newStore()
			MustPut(s, AdminActor, "/a.txt", "hello")
			MustPut(s, AdminActor, "/keep.txt", "keep")
			del := deleteAt(s, AdminActor, "/a.txt")

			res, err := purger(s).Purge(Ctx(), AdminActor, "/", []int64{del.Rev}, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Files).To(Equal(1))
			Expect(res.Dirs).To(BeZero())
			Expect(res.Bytes).To(Equal(int64(5)))
			Expect(res.ByRev[del.Rev]).To(Equal(workspace.PurgeCount{Files: 1, Bytes: 5}))

			_, _, err = s.Undo(Ctx(), AdminActor, del.Rev, workspace.UndoOptions{})
			Expect(err).To(MatchError(workspace.ErrNotFound), "undo of a purged deletion")
			hist, err := s.History(Ctx(), AdminActor, workspace.HistoryQuery{Path: "/"})
			Expect(err).ToNot(HaveOccurred())
			for _, cs := range hist {
				for _, op := range cs.Ops {
					Expect(op.OldPath+op.NewPath).ToNot(ContainSubstring("a.txt"), "history keeps the purged file")
				}
			}
			Expect(Tree(s)).To(Equal(map[string]string{"/keep.txt": "keep"}))
			Expect(MustRead(s, AdminActor, "/keep.txt")).To(Equal("keep"))
			// The name is free for a new file.
			MustPut(s, AdminActor, "/a.txt", "again")
			Expect(MustRead(s, AdminActor, "/a.txt")).To(Equal("again"))
		})

		It("erases a deleted folder with everything below it", func() {
			s := newStore()
			MustPut(s, AdminActor, "/d/a.txt", "aa")
			MustPut(s, AdminActor, "/d/sub/b.txt", "bbb")
			MustPut(s, AdminActor, "/other.txt", "o")
			del := deleteAt(s, AdminActor, "/d")

			res, err := purger(s).Purge(Ctx(), AdminActor, "/", []int64{del.Rev}, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Files).To(Equal(2))
			Expect(res.Dirs).To(Equal(2))
			Expect(res.Bytes).To(Equal(int64(5)))
			Expect(Tree(s)).To(Equal(map[string]string{"/other.txt": "o"}))
		})

		It("a dry run counts and changes nothing", func() {
			s := newStore()
			MustPut(s, AdminActor, "/a.txt", "hello")
			del := deleteAt(s, AdminActor, "/a.txt")

			res, err := purger(s).Purge(Ctx(), AdminActor, "/", []int64{del.Rev}, true)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Files).To(Equal(1))
			Expect(res.Bytes).To(Equal(int64(5)))
			mustUndo(s, del.Rev)
			Expect(MustRead(s, AdminActor, "/a.txt")).To(Equal("hello"))
		})

		It("keeps a node that is live again", func() {
			s := newStore()
			MustPut(s, AdminActor, "/a.txt", "hello")
			del := deleteAt(s, AdminActor, "/a.txt")
			mustUndo(s, del.Rev)

			res, err := purger(s).Purge(Ctx(), AdminActor, "/", []int64{del.Rev}, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Files).To(BeZero())
			Expect(MustRead(s, AdminActor, "/a.txt")).To(Equal("hello"))
		})

		It("ignores a revision that deleted nothing", func() {
			s := newStore()
			cs := commit(s, AdminActor, func(tx workspace.Tx) error { return putIn(tx, "/a.txt", "a") })
			res, err := purger(s).Purge(Ctx(), AdminActor, "/", []int64{cs.Rev, 9999}, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Files).To(BeZero())
			Expect(MustRead(s, AdminActor, "/a.txt")).To(Equal("a"))
		})

		It("keeps what an actor cannot write", func() {
			s := newStore()
			MustPut(s, AdminActor, "/alice/a.txt", "a")
			MustPut(s, AdminActor, "/bob/b.txt", "b")
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/alice", workspace.ModeWrite)).To(Succeed())
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/bob", workspace.ModeRead)).To(Succeed())
			Expect(s.Grant(Ctx(), AdminActor, Bob.Principal, "/bob", workspace.ModeWrite)).To(Succeed())
			da := deleteAt(s, AdminActor, "/alice/a.txt")
			db := deleteAt(s, AdminActor, "/bob/b.txt")

			res, err := purger(s).Purge(Ctx(), Alice, "/", []int64{da.Rev, db.Rev}, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Files).To(Equal(1), "Alice purged more than her own")
			_, _, err = s.Undo(Ctx(), AdminActor, db.Rev, workspace.UndoOptions{})
			Expect(err).ToNot(HaveOccurred(), "Bob's deletion was purged by Alice")
		})

		It("stays inside the folder it is given", func() {
			s := newStore()
			MustPut(s, AdminActor, "/u1/a.txt", "a")
			MustPut(s, AdminActor, "/u2/b.txt", "b")
			both := commit(s, AdminActor, func(tx workspace.Tx) error {
				if err := tx.Delete(Ctx(), "/u1/a.txt"); err != nil {
					return err
				}
				return tx.Delete(Ctx(), "/u2/b.txt")
			})
			res, err := purger(s).Purge(Ctx(), AdminActor, "/u1", []int64{both.Rev}, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Files).To(Equal(1))
			// Only the u2 deletion is left to restore.
			_, _, err = s.Undo(Ctx(), AdminActor, both.Rev, workspace.UndoOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(Tree(s)).To(Equal(map[string]string{"/u1": "/", "/u2": "/", "/u2/b.txt": "b"}))
		})

		It("a purge of a folder keeps files it moved out earlier", func() {
			s := newStore()
			MustPut(s, AdminActor, "/d/a.txt", "a")
			MustPut(s, AdminActor, "/d/b.txt", "b")
			commit(s, AdminActor, func(tx workspace.Tx) error {
				_, err := tx.Move(Ctx(), "/d/a.txt", "/a.txt")
				return err
			})
			del := deleteAt(s, AdminActor, "/d")
			_, err := purger(s).Purge(Ctx(), AdminActor, "/", []int64{del.Rev}, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(Tree(s)).To(Equal(map[string]string{"/a.txt": "a"}))
		})
	})

	Describe("PurgeAll", func() {
		It("erases every deleted node under a folder and only there", func() {
			s := newStore()
			for _, p := range []string{"/u1/a.txt", "/u1/d/b.txt", "/u1/live.txt", "/u2/c.txt"} {
				MustPut(s, AdminActor, p, "x")
			}
			deleteAt(s, AdminActor, "/u1/a.txt")
			deleteAt(s, AdminActor, "/u1/d")
			deleteAt(s, AdminActor, "/u2/c.txt")

			res, err := purger(s).PurgeAll(Ctx(), AdminActor, "/u1", true)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Files).To(Equal(2))
			Expect(res.Dirs).To(Equal(1))

			res, err = purger(s).PurgeAll(Ctx(), AdminActor, "/u1", false)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Files).To(Equal(2))
			Expect(Tree(s)).To(Equal(map[string]string{"/u1": "/", "/u1/live.txt": "x", "/u2": "/"}))

			again, err := purger(s).PurgeAll(Ctx(), AdminActor, "/u1", false)
			Expect(err).ToNot(HaveOccurred())
			Expect(again.Files + again.Dirs).To(BeZero())
			// The other folder still has its deletion to restore.
			trash, err := s.History(Ctx(), AdminActor, workspace.HistoryQuery{Path: "/u2"})
			Expect(err).ToNot(HaveOccurred())
			Expect(trash).ToNot(BeEmpty())
		})

		It("a missing folder purges nothing", func() {
			s := newStore()
			res, err := purger(s).PurgeAll(Ctx(), AdminActor, "/nope", false)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Files + res.Dirs).To(BeZero())
		})

		It("keeps the nodes the actor cannot write", func() {
			s := newStore()
			MustPut(s, AdminActor, "/shared/a.txt", "a")
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/shared", workspace.ModeRead)).To(Succeed())
			del := deleteAt(s, AdminActor, "/shared/a.txt")
			res, err := purger(s).PurgeAll(Ctx(), Alice, "/shared", false)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Files).To(BeZero())
			mustUndo(s, del.Rev)
			Expect(MustRead(s, AdminActor, "/shared/a.txt")).To(Equal("a"))
		})
	})
	Describe("PruneVersions", func() {
		// writes is the number of write operations the history of p holds.
		writes := func(s workspace.Store, p string) []workspace.ChangeSet {
			GinkgoHelper()
			hist, err := s.History(Ctx(), AdminActor, workspace.HistoryQuery{Path: p, Limit: 1000})
			Expect(err).ToNot(HaveOccurred())
			var out []workspace.ChangeSet
			for _, cs := range hist {
				for _, op := range cs.Ops {
					if op.Kind == workspace.OpWrite {
						out = append(out, cs)
						break
					}
				}
			}
			return out
		}
		prune := func(s workspace.Store, p string, keep int) int {
			GinkgoHelper()
			n, err := purger(s).PruneVersions(Ctx(), AdminActor, p, keep)
			Expect(err).ToNot(HaveOccurred())
			return n
		}

		It("keeps the newest versions of a file that was overwritten many times", func() {
			s := newStore()
			MustPut(s, AdminActor, "/a.txt", "v0")
			for i := 1; i <= 10; i++ {
				MustPut(s, AdminActor, "/a.txt", fmt.Sprintf("v%d", i))
			}
			Expect(writes(s, "/a.txt")).To(HaveLen(10))
			Expect(prune(s, "/a.txt", 3)).To(Equal(7))
			Expect(writes(s, "/a.txt")).To(HaveLen(3))
			Expect(MustRead(s, AdminActor, "/a.txt")).To(Equal("v10"))
			Expect(prune(s, "/a.txt", 3)).To(BeZero(), "a second prune finds nothing")
			// The file is still writable and the history still grows.
			MustPut(s, AdminActor, "/a.txt", "v11")
			Expect(writes(s, "/a.txt")).To(HaveLen(4))
		})

		It("keeps the undo of every version it keeps", func() {
			s := newStore()
			MustPut(s, AdminActor, "/a.txt", "v0")
			for i := 1; i <= 6; i++ {
				MustPut(s, AdminActor, "/a.txt", fmt.Sprintf("v%d", i))
			}
			Expect(prune(s, "/a.txt", 2)).To(Equal(4))
			kept := writes(s, "/a.txt") // newest first
			Expect(kept).To(HaveLen(2))
			mustUndo(s, kept[0].Rev)
			Expect(MustRead(s, AdminActor, "/a.txt")).To(Equal("v5"))
			mustUndo(s, kept[1].Rev)
			Expect(MustRead(s, AdminActor, "/a.txt")).To(Equal("v4"))
			// A pruned version is gone for good.
			_, _, err := s.Undo(Ctx(), AdminActor, kept[1].Rev-1, workspace.UndoOptions{})
			Expect(err).To(HaveOccurred())
		})

		It("touches no other file", func() {
			s := newStore()
			for i := 0; i < 5; i++ {
				MustPut(s, AdminActor, "/a.txt", fmt.Sprintf("a%d", i))
				MustPut(s, AdminActor, "/b.txt", fmt.Sprintf("b%d", i))
			}
			Expect(prune(s, "/a.txt", 1)).To(Equal(3))
			Expect(writes(s, "/a.txt")).To(HaveLen(1))
			Expect(writes(s, "/b.txt")).To(HaveLen(4))
			Expect(MustRead(s, AdminActor, "/b.txt")).To(Equal("b4"))
		})

		It("keeps a write that shares its change set with another file", func() {
			s := newStore()
			MustPut(s, AdminActor, "/a.txt", "a0")
			MustPut(s, AdminActor, "/b.txt", "b0")
			both := commit(s, AdminActor, func(tx workspace.Tx) error {
				if err := putIn(tx, "/a.txt", "a1"); err != nil {
					return err
				}
				return putIn(tx, "/b.txt", "b1")
			})
			MustPut(s, AdminActor, "/a.txt", "a2")
			MustPut(s, AdminActor, "/a.txt", "a3")
			Expect(prune(s, "/a.txt", 1)).To(Equal(1))
			// The shared change set still undoes both files.
			Expect(MustRead(s, AdminActor, "/a.txt")).To(Equal("a3"))
			Expect(MustRead(s, AdminActor, "/b.txt")).To(Equal("b1"))
			_, _, err := s.Undo(Ctx(), AdminActor, both.Rev, workspace.UndoOptions{SkipConflicts: true})
			Expect(err).ToNot(HaveOccurred())
			Expect(MustRead(s, AdminActor, "/b.txt")).To(Equal("b0"))
		})

		It("changes nothing for a write of identical content", func() {
			s := newStore()
			MustPut(s, AdminActor, "/a.txt", "same")
			for i := 0; i < 5; i++ {
				MustPut(s, AdminActor, "/a.txt", "same")
			}
			Expect(writes(s, "/a.txt")).To(BeEmpty(), "an identical write adds no version")
			Expect(prune(s, "/a.txt", 1)).To(BeZero())
			Expect(MustRead(s, AdminActor, "/a.txt")).To(Equal("same"))
		})

		It("is a no-op for a missing path, a folder and a file with fewer versions", func() {
			s := newStore()
			MustPut(s, AdminActor, "/d/a.txt", "a0")
			MustPut(s, AdminActor, "/d/a.txt", "a1")
			Expect(prune(s, "/nope.txt", 1)).To(BeZero())
			Expect(prune(s, "/d", 1)).To(BeZero())
			Expect(prune(s, "/d/a.txt", 5)).To(BeZero())
			Expect(writes(s, "/d/a.txt")).To(HaveLen(1))
			Expect(MustRead(s, AdminActor, "/d/a.txt")).To(Equal("a1"))
		})

		It("refuses a keep below one", func() {
			s := newStore()
			MustPut(s, AdminActor, "/a.txt", "a0")
			_, err := purger(s).PruneVersions(Ctx(), AdminActor, "/a.txt", 0)
			Expect(err).To(MatchError(workspace.ErrInvalid))
		})

		It("needs write access to the file", func() {
			s := newStore()
			MustPut(s, AdminActor, "/shared/a.txt", "a0")
			MustPut(s, AdminActor, "/shared/a.txt", "a1")
			MustPut(s, AdminActor, "/shared/a.txt", "a2")
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/shared", workspace.ModeRead)).To(Succeed())
			_, err := purger(s).PruneVersions(Ctx(), Alice, "/shared/a.txt", 1)
			Expect(err).To(HaveOccurred())
			Expect(writes(s, "/shared/a.txt")).To(HaveLen(2))
		})
	})
}

// RunPurgeBlobs registers specs on the bytes a purge frees. newStore opens a
// store on blobs that removes unreferenced blobs at once (no grace period).
func RunPurgeBlobs(newStore func() (workspace.Store, blob.Store)) {
	Describe("Purge and the blob store", func() {
		exists := func(blobs blob.Store, sha string) bool {
			GinkgoHelper()
			ok, err := blobs.Exists(Ctx(), sha)
			Expect(err).ToNot(HaveOccurred())
			return ok
		}
		It("removes the bytes that nothing refers to and keeps shared ones", func() {
			s, blobs := newStore()
			solo := MustPut(s, AdminActor, "/solo.txt", "only here")
			twin1 := MustPut(s, AdminActor, "/twin1.txt", "same bytes")
			MustPut(s, AdminActor, "/twin2.txt", "same bytes")
			Expect(exists(blobs, solo.Blob)).To(BeTrue())

			ds := deleteAt(s, AdminActor, "/solo.txt")
			dt := deleteAt(s, AdminActor, "/twin1.txt")
			_, err := purger(s).Purge(Ctx(), AdminActor, "/", []int64{ds.Rev, dt.Rev}, false)
			Expect(err).ToNot(HaveOccurred())

			Expect(exists(blobs, solo.Blob)).To(BeFalse(), "the freed blob is still stored")
			Expect(exists(blobs, twin1.Blob)).To(BeTrue(), "a blob that another file uses was removed")
			Expect(MustRead(s, AdminActor, "/twin2.txt")).To(Equal("same bytes"))
		})

		It("removes every version of a file, and keeps a version a live file has", func() {
			s, blobs := newStore()
			v1 := MustPut(s, AdminActor, "/a.txt", "version one")
			v2 := MustPut(s, AdminActor, "/a.txt", "version two")
			MustPut(s, AdminActor, "/b.txt", "version one")
			del := deleteAt(s, AdminActor, "/a.txt")
			_, err := purger(s).Purge(Ctx(), AdminActor, "/", []int64{del.Rev}, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(exists(blobs, v2.Blob)).To(BeFalse())
			Expect(exists(blobs, v1.Blob)).To(BeTrue(), "b.txt lost its bytes")
			Expect(MustRead(s, AdminActor, "/b.txt")).To(Equal("version one"))
		})
		It("removes the bytes of the versions that a prune drops, and no others", func() {
			s, blobs := newStore()
			var vs []workspace.Node
			for i := 0; i < 8; i++ {
				vs = append(vs, MustPut(s, AdminActor, "/a.txt", fmt.Sprintf("version %d", i)))
			}
			other := MustPut(s, AdminActor, "/b.txt", "version 1") // shares the bytes of v1
			n, err := purger(s).PruneVersions(Ctx(), AdminActor, "/a.txt", 2)
			Expect(err).ToNot(HaveOccurred())
			Expect(n).To(Equal(5))
			// The create row keeps v0. The kept writes refer to v5..v7. The
			// bytes of v2..v4 go, and v1 stays because b.txt uses it.
			for i, v := range vs {
				switch {
				case i == 0, i >= 5: // the create, and the kept writes with the live file
					Expect(exists(blobs, v.Blob)).To(BeTrue(), "version %d", i)
				case i == 1:
					Expect(exists(blobs, v.Blob)).To(BeTrue(), "b.txt shares version 1")
				default:
					Expect(exists(blobs, v.Blob)).To(BeFalse(), "version %d is still stored", i)
				}
			}
			Expect(MustRead(s, AdminActor, "/b.txt")).To(Equal("version 1"))
			Expect(other.Blob).To(Equal(vs[1].Blob))
			Expect(MustRead(s, AdminActor, "/a.txt")).To(Equal("version 7"))
		})
	})
}

package wstest

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// commit runs fn in one Tx and returns the change set.
func commit(s workspace.Store, a workspace.Actor, fn func(tx workspace.Tx) error) workspace.ChangeSet {
	GinkgoHelper()
	tx, err := s.Begin(Ctx(), a, "test")
	Expect(err).ToNot(HaveOccurred())
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		Expect(err).ToNot(HaveOccurred())
	}
	cs, err := tx.Commit()
	Expect(err).ToNot(HaveOccurred())
	return cs
}

func putIn(tx workspace.Tx, p, content string) error {
	_, err := tx.Put(Ctx(), p, strings.NewReader(content), workspace.PutOptions{Overwrite: true})
	return err
}

func mustUndo(s workspace.Store, rev int64) workspace.ChangeSet {
	GinkgoHelper()
	cs, conflicts, err := s.Undo(Ctx(), AdminActor, rev, workspace.UndoOptions{})
	Expect(err).ToNot(HaveOccurred(), "undo %d: %v", rev, conflicts)
	return cs
}

func describeHistory(newStore NewStore) {
	Describe("History", func() {
		It("undo a create", func() {
			s := newStore()
			cs := commit(s, AdminActor, func(tx workspace.Tx) error { return putIn(tx, "/d/a.txt", "a") })
			u := mustUndo(s, cs.Rev)
			Expect(u.Kind).To(Equal(workspace.ChangeUndo))
			Expect(u.Undoes).To(Equal(cs.Rev))
			Expect(u.Title).To(Equal(fmt.Sprintf("Undo of revision %d", cs.Rev)))
			Expect(Tree(s)).To(BeEmpty())
		})
		It("undo a write", func() {
			s := newStore()
			MustPut(s, AdminActor, "/a.txt", "v1")
			cs := commit(s, AdminActor, func(tx workspace.Tx) error { return putIn(tx, "/a.txt", "v2") })
			mustUndo(s, cs.Rev)
			Expect(MustRead(s, AdminActor, "/a.txt")).To(Equal("v1"), "content not restored")
		})
		It("undo a move and a folder delete", func() {
			s := newStore()
			MustPut(s, AdminActor, "/d/a.txt", "a")
			MustPut(s, AdminActor, "/d/b.txt", "b")
			start := Tree(s)
			mv := commit(s, AdminActor, func(tx workspace.Tx) error {
				_, err := tx.Move(Ctx(), "/d/a.txt", "/e/a.txt")
				return err
			})
			del := commit(s, AdminActor, func(tx workspace.Tx) error { return tx.Delete(Ctx(), "/d") })
			mustUndo(s, del.Rev)
			mustUndo(s, mv.Rev)
			Expect(Tree(s)).To(Equal(start))
		})
		It("undo of an undo applies the change again", func() {
			s := newStore()
			cs := commit(s, AdminActor, func(tx workspace.Tx) error { return putIn(tx, "/a.txt", "a") })
			u := mustUndo(s, cs.Rev)
			mustUndo(s, u.Rev)
			Expect(MustRead(s, AdminActor, "/a.txt")).To(Equal("a"), "redo failed")
		})
		It("a later change is a conflict and nothing changes", func() {
			s := newStore()
			cs := commit(s, AdminActor, func(tx workspace.Tx) error { return putIn(tx, "/a.txt", "v1") })
			MustPut(s, AdminActor, "/a.txt", "v2")
			before := Tree(s)
			_, conflicts, err := s.Undo(Ctx(), AdminActor, cs.Rev, workspace.UndoOptions{})
			Expect(err).To(MatchError(workspace.ErrConflict))
			Expect(conflicts).To(HaveLen(1))
			Expect(conflicts[0].Path).To(Equal("/a.txt"))
			Expect(conflicts[0].Reason).To(Equal(workspace.ReasonChanged))
			Expect(Tree(s)).To(Equal(before), "tree changed")
		})
		It("skip conflicts reverts the rest", func() {
			s := newStore()
			cs := commit(s, AdminActor, func(tx workspace.Tx) error {
				if err := putIn(tx, "/a.txt", "a"); err != nil {
					return err
				}
				return putIn(tx, "/b.txt", "b")
			})
			MustPut(s, AdminActor, "/a.txt", "changed")
			u, conflicts, err := s.Undo(Ctx(), AdminActor, cs.Rev, workspace.UndoOptions{SkipConflicts: true})
			Expect(err).ToNot(HaveOccurred())
			Expect(u.Rev).ToNot(BeZero())
			Expect(conflicts).To(HaveLen(1))
			Expect(Tree(s)).To(Equal(map[string]string{"/a.txt": "changed"}))
		})
		It("a taken name is a conflict", func() {
			s := newStore()
			MustPut(s, AdminActor, "/a.txt", "a")
			mv := commit(s, AdminActor, func(tx workspace.Tx) error {
				_, err := tx.Move(Ctx(), "/a.txt", "/b.txt")
				return err
			})
			MustPut(s, AdminActor, "/A.TXT", "new")
			_, conflicts, err := s.Undo(Ctx(), AdminActor, mv.Rev, workspace.UndoOptions{})
			Expect(err).To(MatchError(workspace.ErrConflict))
			Expect(conflicts).To(HaveLen(1))
			Expect(conflicts[0].Reason).To(Equal(workspace.ReasonNameTaken))
		})
		It("a deleted parent is a conflict", func() {
			s := newStore()
			MustPut(s, AdminActor, "/d/x.txt", "x")
			delX := commit(s, AdminActor, func(tx workspace.Tx) error { return tx.Delete(Ctx(), "/d/x.txt") })
			commit(s, AdminActor, func(tx workspace.Tx) error { return tx.Delete(Ctx(), "/d") })
			_, conflicts, err := s.Undo(Ctx(), AdminActor, delX.Rev, workspace.UndoOptions{})
			Expect(err).To(MatchError(workspace.ErrConflict))
			Expect(conflicts).To(HaveLen(1))
			Expect(conflicts[0].Reason).To(Equal(workspace.ReasonParentDeleted))
		})
		It("undo after change and change back", func() {
			s := newStore()
			MustPut(s, AdminActor, "/a.txt", "v1")
			cs := commit(s, AdminActor, func(tx workspace.Tx) error { return putIn(tx, "/a.txt", "v2") })
			MustPut(s, AdminActor, "/a.txt", "v3")
			MustPut(s, AdminActor, "/a.txt", "v2")
			mustUndo(s, cs.Rev)
			Expect(MustRead(s, AdminActor, "/a.txt")).To(Equal("v1"), "content not restored")
		})
		It("undo needs access", func() {
			s := newStore()
			cs := commit(s, AdminActor, func(tx workspace.Tx) error { return putIn(tx, "/r/a.txt", "a") })
			_, _, err := s.Undo(Ctx(), Alice, cs.Rev, workspace.UndoOptions{})
			Expect(err).To(MatchError(workspace.ErrNotFound), "no grant")
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/", workspace.ModeRead)).To(Succeed())
			_, _, err = s.Undo(Ctx(), Alice, cs.Rev, workspace.UndoOptions{})
			Expect(err).To(MatchError(workspace.ErrReadOnly), "read grant")
			_, _, err = s.Undo(Ctx(), AdminActor, 999, workspace.UndoOptions{})
			Expect(err).To(MatchError(workspace.ErrNotFound), "unknown revision")
		})
		It("history and the change feed respect grants", func() {
			s := newStore()
			MustPut(s, AdminActor, "/shared/a.txt", "a")        // rev 1
			MustPut(s, AdminActor, "/private/p.txt", "p")       // rev 2
			commit(s, AdminActor, func(tx workspace.Tx) error { // rev 3
				if err := putIn(tx, "/shared/b.txt", "b"); err != nil {
					return err
				}
				return putIn(tx, "/private/q.txt", "q")
			})
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/shared", workspace.ModeRead)).To(Succeed())
			revsOf := func(css []workspace.ChangeSet) []int64 {
				var out []int64
				for _, cs := range css {
					out = append(out, cs.Rev)
				}
				return out
			}
			h, err := s.History(Ctx(), Alice, workspace.HistoryQuery{Path: "/shared"})
			Expect(err).ToNot(HaveOccurred())
			Expect(revsOf(h)).To(Equal([]int64{3, 1}), "alice history")
			Expect(h[0].Ops).To(HaveLen(1), "alice sees ops %+v", h[0].Ops)
			Expect(h[0].Ops[0].NewPath).To(Equal("/shared/b.txt"), "alice sees ops %+v", h[0].Ops)
			_, err = s.History(Ctx(), Alice, workspace.HistoryQuery{})
			Expect(err).To(MatchError(workspace.ErrNotFound), "alice history of the root")
			h, _ = s.History(Ctx(), AdminActor, workspace.HistoryQuery{Limit: 2})
			Expect(revsOf(h)).To(Equal([]int64{3, 2}), "limit")
			h, _ = s.History(Ctx(), AdminActor, workspace.HistoryQuery{BeforeRev: 3})
			Expect(revsOf(h)).To(Equal([]int64{2, 1}), "before")
			c, err := s.ChangesSince(Ctx(), Alice, 0, 10)
			Expect(err).ToNot(HaveOccurred())
			Expect(revsOf(c)).To(Equal([]int64{1, 3}), "alice feed")
			c, _ = s.ChangesSince(Ctx(), AdminActor, 0, 2)
			Expect(revsOf(c)).To(Equal([]int64{1, 2}), "feed limit")
			c, _ = s.ChangesSince(Ctx(), AdminActor, 2, 10)
			Expect(revsOf(c)).To(Equal([]int64{3}), "feed after 2")
			// Only the change sets of one chat.
			chatA := workspace.Actor{Principal: Admin, ChatID: "chat-a"}
			chatB := workspace.Actor{Principal: Admin, ChatID: "chat-b"}
			MustPut(s, chatA, "/shared/c.txt", "c")  // rev 4
			MustPut(s, chatB, "/shared/d.txt", "d")  // rev 5
			MustPut(s, chatA, "/private/e.txt", "e") // rev 6
			h, err = s.History(Ctx(), AdminActor, workspace.HistoryQuery{ChatID: "chat-a"})
			Expect(err).ToNot(HaveOccurred())
			Expect(revsOf(h)).To(Equal([]int64{6, 4}), "chat-a history")
			Expect(h[0].ChatID).To(Equal("chat-a"))
			h, _ = s.History(Ctx(), AdminActor, workspace.HistoryQuery{Path: "/shared", ChatID: "chat-a"})
			Expect(revsOf(h)).To(Equal([]int64{4}), "chat-a history of /shared")
			h, _ = s.History(Ctx(), AdminActor, workspace.HistoryQuery{ChatID: "chat-none"})
			Expect(h).To(BeEmpty(), "history of a chat with no change")
			h, _ = s.History(Ctx(), AdminActor, workspace.HistoryQuery{BeforeRev: 4})
			Expect(revsOf(h)).To(Equal([]int64{3, 2, 1}), "before after the chat writes")
		})
		It("a folder with later children is a conflict", func() {
			s := newStore()
			mk := commit(s, AdminActor, func(tx workspace.Tx) error {
				_, err := tx.Mkdir(Ctx(), "/a")
				return err
			})
			put := commit(s, AdminActor, func(tx workspace.Tx) error { return putIn(tx, "/a/x", "x") })
			before := Tree(s)
			_, conflicts, err := s.Undo(Ctx(), AdminActor, mk.Rev, workspace.UndoOptions{})
			Expect(err).To(MatchError(workspace.ErrConflict))
			Expect(conflicts).To(HaveLen(1))
			Expect(conflicts[0].Path).To(Equal("/a"))
			Expect(conflicts[0].Reason).To(Equal(workspace.ReasonChanged))
			Expect(Tree(s)).To(Equal(before), "tree changed")
			Expect(MustRead(s, AdminActor, "/a/x")).To(Equal("x"), "child hidden")
			mustUndo(s, put.Rev)
			mustUndo(s, mk.Rev)
			Expect(Tree(s)).To(BeEmpty())
		})
		It("undo that would make a cycle is a conflict", func() {
			s := newStore()
			MustPut(s, AdminActor, "/a/b/f", "f")
			mv := commit(s, AdminActor, func(tx workspace.Tx) error {
				_, err := tx.Move(Ctx(), "/a/b", "/b")
				return err
			})
			commit(s, AdminActor, func(tx workspace.Tx) error {
				_, err := tx.Move(Ctx(), "/a", "/b/a")
				return err
			})
			before := Tree(s)
			ctx, cancel := context.WithTimeout(Ctx(), 5*time.Second)
			DeferCleanup(cancel)
			_, conflicts, err := s.Undo(ctx, AdminActor, mv.Rev, workspace.UndoOptions{})
			Expect(err).To(MatchError(workspace.ErrConflict))
			Expect(conflicts).To(HaveLen(1))
			Expect(conflicts[0].Reason).To(Equal(workspace.ReasonChanged))
			Expect(Tree(s)).To(Equal(before), "tree changed")
		})
		It("undo needs write on the parents it edits", func() {
			s := newStore()
			MustPut(s, AdminActor, "/ro/x", "x")
			MustPut(s, AdminActor, "/mine/keep", "k")
			for p, m := range map[string]workspace.Mode{"/ro": workspace.ModeRead, "/mine": workspace.ModeWrite} {
				Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, p, m)).To(Succeed())
			}
			mv := commit(s, AdminActor, func(tx workspace.Tx) error {
				_, err := tx.Move(Ctx(), "/ro/x", "/mine/x")
				return err
			})
			before := Tree(s)
			_, conflicts, err := s.Undo(Ctx(), Alice, mv.Rev, workspace.UndoOptions{})
			Expect(err).To(MatchError(workspace.ErrReadOnly), "into a read-only folder")
			Expect(conflicts).To(BeEmpty(), "into a read-only folder")
			Expect(Tree(s)).To(Equal(before), "tree changed")
			_, err = s.Mkdir(Ctx(), AdminActor, "/ro/w")
			Expect(err).ToNot(HaveOccurred())
			mk := commit(s, AdminActor, func(tx workspace.Tx) error {
				_, err := tx.Mkdir(Ctx(), "/ro/w2")
				return err
			})
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/ro/w2", workspace.ModeWrite)).To(Succeed())
			_, conflicts, err = s.Undo(Ctx(), Alice, mk.Rev, workspace.UndoOptions{})
			Expect(err).To(MatchError(workspace.ErrReadOnly), "out of a read-only folder")
			Expect(conflicts).To(BeEmpty(), "out of a read-only folder")
			_, err = s.Stat(Ctx(), AdminActor, "/ro/w2")
			Expect(err).ToNot(HaveOccurred(), "w2 gone")
		})
		It("the change feed hides paths the actor cannot read", func() {
			s := newStore()
			MustPut(s, AdminActor, "/private/secret-plan.txt", "p")
			commit(s, AdminActor, func(tx workspace.Tx) error {
				_, err := tx.Move(Ctx(), "/private/secret-plan.txt", "/shared/plan.txt")
				return err
			})
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/shared", workspace.ModeRead)).To(Succeed())
			check := func(name string, css []workspace.ChangeSet) {
				GinkgoHelper()
				var ops []workspace.Op
				for _, cs := range css {
					for _, o := range cs.Ops {
						Expect(o.OldPath+o.NewPath).ToNot(ContainSubstring("private"), "%s leaks %+v", name, o)
						ops = append(ops, o)
					}
				}
				Expect(ops).To(HaveLen(1), "%s ops = %+v", name, ops)
				Expect(ops[0].Kind).To(Equal(workspace.OpCreate), "%s ops = %+v", name, ops)
				Expect(ops[0].NewPath).To(Equal("/shared/plan.txt"), "%s ops = %+v", name, ops)
				Expect(ops[0].OldPath).To(BeEmpty(), "%s ops = %+v", name, ops)
			}
			c, err := s.ChangesSince(Ctx(), Alice, 0, 10)
			Expect(err).ToNot(HaveOccurred())
			check("feed", c)
			h, err := s.History(Ctx(), Alice, workspace.HistoryQuery{Path: "/shared"})
			Expect(err).ToNot(HaveOccurred())
			check("history", h)
		})
		It("a change set title that names hidden paths is replaced", func() {
			s := newStore()
			MustPut(s, AdminActor, "/secret/clientX-plan.pdf", "p")
			MustPut(s, AdminActor, "/shared/other.txt", "o")
			_, err := s.Move(Ctx(), AdminActor, "/secret/clientX-plan.pdf", "/shared/plan.pdf")
			Expect(err).ToNot(HaveOccurred())
			mv := latestRev(s)
			mustUndo(s, mv)
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/shared", workspace.ModeRead)).To(Succeed())
			check := func(name string, css []workspace.ChangeSet) {
				GinkgoHelper()
				titles := map[int64]string{}
				for _, cs := range css {
					titles[cs.Rev] = cs.Title
					Expect(cs.Title).ToNot(ContainSubstring("secret"), "%s leaks title %q", name, cs.Title)
					Expect(cs.Title).ToNot(ContainSubstring("clientX"), "%s leaks title %q", name, cs.Title)
				}
				Expect(titles[mv]).To(Equal("Move"), "%s titles = %v", name, titles)
				Expect(titles[mv+1]).To(Equal("Undo"), "%s titles = %v", name, titles)
			}
			c, err := s.ChangesSince(Ctx(), Alice, 0, 10)
			Expect(err).ToNot(HaveOccurred())
			check("feed", c)
			h, err := s.History(Ctx(), Alice, workspace.HistoryQuery{Path: "/shared"})
			Expect(err).ToNot(HaveOccurred())
			check("history", h)
			// A change set the reader sees whole keeps its stored title.
			for _, cs := range c {
				if cs.Rev == 2 {
					Expect(cs.Title).ToNot(BeEmpty(), "title lost: %+v", cs)
				}
			}
			admin, err := s.ChangesSince(Ctx(), AdminActor, mv-1, 1)
			Expect(err).ToNot(HaveOccurred())
			Expect(admin).To(HaveLen(1), "admin title: %+v", admin)
			Expect(admin[0].Title).To(Equal("Move /secret/clientX-plan.pdf to /shared/plan.pdf"))
		})
		It("an undo title does not name the paths of the undone change set", func() {
			s := newStore()
			MustPut(s, AdminActor, "/secret/x.txt", "x")
			MustPut(s, AdminActor, "/shared/other.txt", "o")
			_, err := s.Move(Ctx(), AdminActor, "/secret/x.txt", "/shared/new/y.txt")
			Expect(err).ToNot(HaveOccurred())
			first := latestRev(s)
			_, err = s.Move(Ctx(), AdminActor, "/shared/new/y.txt", "/shared/z.txt")
			Expect(err).ToNot(HaveOccurred())
			// The node of the first move conflicts; only the folder it created is
			// removed, so every operation of the undo is visible to the reader.
			u, conflicts, err := s.Undo(Ctx(), AdminActor, first, workspace.UndoOptions{SkipConflicts: true})
			Expect(err).ToNot(HaveOccurred(), "undo: %+v", conflicts)
			Expect(conflicts).To(HaveLen(1))
			Expect(u.Title).To(Equal(fmt.Sprintf("Undo of revision %d", first)), "stored title")
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/shared", workspace.ModeRead)).To(Succeed())
			check := func(name string, css []workspace.ChangeSet) {
				GinkgoHelper()
				found := false
				for _, cs := range css {
					Expect(cs.Title).ToNot(ContainSubstring("secret"), "%s leaks title %q", name, cs.Title)
					Expect(cs.Title).ToNot(ContainSubstring("x.txt"), "%s leaks title %q", name, cs.Title)
					if cs.Rev == u.Rev {
						found = true
						Expect(cs.Ops).To(HaveLen(1), "%s undo ops = %+v", name, cs.Ops)
						Expect(cs.Ops[0].Kind).To(Equal(workspace.OpDelete), "%s undo ops = %+v", name, cs.Ops)
						Expect(cs.Ops[0].OldPath).To(Equal("/shared/new"), "%s undo ops = %+v", name, cs.Ops)
					}
				}
				Expect(found).To(BeTrue(), "%s lacks the undo", name)
			}
			c, err := s.ChangesSince(Ctx(), Alice, 0, 10)
			Expect(err).ToNot(HaveOccurred())
			check("feed", c)
			h, err := s.History(Ctx(), Alice, workspace.HistoryQuery{Path: "/shared"})
			Expect(err).ToNot(HaveOccurred())
			check("history", h)
		})
	})
}

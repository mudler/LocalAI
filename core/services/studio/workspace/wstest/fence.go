package wstest

import (
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// fenceWorld is the tree the fence specs use: two homes, a folder in
// Alice's home that the fence names, its siblings and a shared folder.
type fenceWorld struct {
	s      workspace.Store
	proj   workspace.Node // /users/alice/proj, the fence root
	sub    workspace.Node // /users/alice/proj/sub
	other  workspace.Node // /users/alice/other, a sibling
	shared workspace.Node // /shared/s, readable by Alice
}

func newFenceWorld(newStore NewStore) fenceWorld {
	GinkgoHelper()
	s := newStore()
	for p, c := range map[string]string{
		"/users/alice/proj/a.txt":     "a",
		"/users/alice/proj/sub/b.txt": "b",
		"/users/alice/other/o.txt":    "o",
		"/users/alice/top.txt":        "t",
		"/users/bob/b.txt":            "bob",
		"/shared/s/s.txt":             "s",
		"/shared/hidden/h.txt":        "h",
	} {
		MustPut(s, AdminActor, p, c)
	}
	Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/users/alice", workspace.ModeWrite)).To(Succeed())
	Expect(s.Grant(Ctx(), AdminActor, Bob.Principal, "/users/bob", workspace.ModeWrite)).To(Succeed())
	Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/shared/s", workspace.ModeRead)).To(Succeed())
	return fenceWorld{
		s:      s,
		proj:   adminStat(s, "/users/alice/proj"),
		sub:    adminStat(s, "/users/alice/proj/sub"),
		other:  adminStat(s, "/users/alice/other"),
		shared: adminStat(s, "/shared/s"),
	}
}

func adminStat(s workspace.Store, p string) workspace.Node {
	GinkgoHelper()
	n, err := s.Stat(Ctx(), AdminActor, p)
	Expect(err).ToNot(HaveOccurred(), "stat %s", p)
	return n
}

// fenced is AliceChat fenced to proj at mode m.
func (w fenceWorld) fenced(m workspace.Mode) workspace.Actor {
	return AliceChat.Fenced(workspace.FenceRoot{Node: w.proj.ID, Mode: m})
}

// expectHidden checks that the actor can neither stat, open nor list p.
func expectHidden(s workspace.Store, a workspace.Actor, p string, dir bool) {
	GinkgoHelper()
	_, err := s.Stat(Ctx(), a, p)
	Expect(err).To(MatchError(workspace.ErrNotFound), "stat %s", p)
	if dir {
		_, err = s.List(Ctx(), a, p)
		Expect(err).To(MatchError(workspace.ErrNotFound), "list %s", p)
		return
	}
	r, _, err := s.Open(Ctx(), a, p)
	if err == nil {
		_ = r.Close()
	}
	Expect(err).To(MatchError(workspace.ErrNotFound), "open %s", p)
}

func put(s workspace.Store, a workspace.Actor, p string) error {
	_, err := s.Put(Ctx(), a, p, strings.NewReader("x"), workspace.PutOptions{Overwrite: true})
	return err
}

func describeFence(newStore NewStore) {
	F, D, W := workspace.FixtureFile, workspace.FixtureDir, workspace.FixtureWhiteout
	Describe("a fenced actor", func() {
		It("reaches its root and what is below it", func() {
			w := newFenceWorld(newStore)
			a := w.fenced(workspace.ModeWrite)
			Expect(MustRead(w.s, a, "/users/alice/proj/a.txt")).To(Equal("a"))
			Expect(MustRead(w.s, a, "/users/alice/proj/sub/b.txt")).To(Equal("b"))
			kids, err := w.s.List(Ctx(), a, "/users/alice/proj")
			Expect(err).ToNot(HaveOccurred())
			Expect(kids).To(HaveLen(2))
			MustPut(w.s, a, "/users/alice/proj/new.txt", "n")
		})

		It("cannot reach a sibling, the parent, the home root or another home", func() {
			w := newFenceWorld(newStore)
			a := w.fenced(workspace.ModeWrite)
			for _, p := range []string{"/", "/users", "/users/alice", "/users/alice/other", "/users/bob", "/shared", "/shared/s"} {
				expectHidden(w.s, a, p, true)
			}
			for _, p := range []string{"/users/alice/top.txt", "/users/alice/other/o.txt", "/users/bob/b.txt", "/shared/s/s.txt"} {
				expectHidden(w.s, a, p, false)
			}
		})

		It("takes parent, relative and doubled spellings as no way out", func() {
			w := newFenceWorld(newStore)
			a := w.fenced(workspace.ModeWrite)
			for _, p := range []string{
				"/users/alice/proj/../other/o.txt",
				"/users/alice/proj/./../other/o.txt",
				"/users/alice/proj/..",
				"users/alice/other/o.txt",
				"//users/alice/other/o.txt",
				"/users/alice//other/o.txt",
				"/users/alice/proj/..%2fother/o.txt",
				"/users/alice/proj/..\\other\\o.txt",
				"/users/alice/proj/․․/other/o.txt",
				"/users/alice/proj/a.txt\x00",
			} {
				r, _, err := w.s.Open(Ctx(), a, p)
				if err == nil {
					_ = r.Close()
				}
				Expect(err).To(HaveOccurred(), "open %q", p)
				_, err = w.s.Put(Ctx(), a, p, strings.NewReader("x"), workspace.PutOptions{Overwrite: true})
				if err == nil {
					// A look-alike taken as a literal name lands inside the
					// fence, never outside it.
					n, serr := w.s.Stat(Ctx(), a, p)
					Expect(serr).ToNot(HaveOccurred(), "stat %q", p)
					Expect(workspace.Within(n.Path, "/users/alice/proj")).To(BeTrue(), "put %q landed at %s", p, n.Path)
				}
			}
			Expect(MustRead(w.s, AdminActor, "/users/alice/other/o.txt")).To(Equal("o"), "a file outside changed")
		})

		It("is confined even when its principal is the administrator", func() {
			w := newFenceWorld(newStore)
			a := AdminActor.Fenced(workspace.FenceRoot{Node: w.proj.ID, Mode: workspace.ModeWrite})
			for _, p := range []string{"/", "/users/alice", "/users/alice/other", "/users/bob", "/shared/s"} {
				expectHidden(w.s, a, p, true)
			}
			prefixes, err := w.s.ReadablePrefixes(Ctx(), a)
			Expect(err).ToNot(HaveOccurred())
			Expect(prefixes).To(BeEmpty(), "the administrator shortcut")
			Expect(w.s.Grant(Ctx(), a, Alice.Principal, "/users/alice/other", workspace.ModeRead)).To(MatchError(workspace.ErrNotAdmin))

			// With a grant of its own, the administrator principal reaches the
			// fence and still nothing else.
			Expect(w.s.Grant(Ctx(), AdminActor, Admin, "/users/alice", workspace.ModeWrite)).To(Succeed())
			Expect(MustRead(w.s, a, "/users/alice/proj/a.txt")).To(Equal("a"))
			expectHidden(w.s, a, "/users/alice/other/o.txt", false)
			prefixes, err = w.s.ReadablePrefixes(Ctx(), a)
			Expect(err).ToNot(HaveOccurred())
			Expect(prefixes).To(Equal([]string{"/users/alice/proj"}))
		})

		It("cannot write in a read-only root", func() {
			w := newFenceWorld(newStore)
			a := w.fenced(workspace.ModeRead)
			Expect(MustRead(w.s, a, "/users/alice/proj/a.txt")).To(Equal("a"))
			_, err := w.s.List(Ctx(), a, "/users/alice/proj/sub")
			Expect(err).ToNot(HaveOccurred())
			Expect(put(w.s, a, "/users/alice/proj/a.txt")).To(MatchError(workspace.ErrReadOnly), "overwrite")
			Expect(put(w.s, a, "/users/alice/proj/new.txt")).To(MatchError(workspace.ErrReadOnly), "new file")
			Expect(put(w.s, a, "/users/alice/proj/x/y.txt")).To(MatchError(workspace.ErrReadOnly), "new file in a new folder")
			_, err = w.s.Mkdir(Ctx(), a, "/users/alice/proj/d")
			Expect(err).To(MatchError(workspace.ErrReadOnly), "mkdir")
			_, err = w.s.Move(Ctx(), a, "/users/alice/proj/a.txt", "/users/alice/proj/c.txt")
			Expect(err).To(MatchError(workspace.ErrReadOnly), "move")
			Expect(w.s.Delete(Ctx(), a, "/users/alice/proj/a.txt")).To(MatchError(workspace.ErrReadOnly), "delete")
			Expect(MustRead(w.s, AdminActor, "/users/alice/proj/a.txt")).To(Equal("a"))
		})

		It("gets the lower of its grant and its fence", func() {
			w := newFenceWorld(newStore)
			a := AliceChat.Fenced(workspace.FenceRoot{Node: w.shared.ID, Mode: workspace.ModeWrite})
			Expect(MustRead(w.s, a, "/shared/s/s.txt")).To(Equal("s"))
			Expect(put(w.s, a, "/shared/s/new.txt")).To(MatchError(workspace.ErrReadOnly), "the grant is read only")
			b := Bob.Fenced(workspace.FenceRoot{Node: w.proj.ID, Mode: workspace.ModeWrite})
			expectHidden(w.s, b, "/users/alice/proj", true)
		})

		It("cannot delete or move its own root", func() {
			w := newFenceWorld(newStore)
			for _, m := range []workspace.Mode{workspace.ModeRead, workspace.ModeWrite} {
				a := w.fenced(m)
				Expect(w.s.Delete(Ctx(), a, "/users/alice/proj")).To(MatchError(workspace.ErrReadOnly), "delete, mode %d", m)
				_, err := w.s.Move(Ctx(), a, "/users/alice/proj", "/users/alice/proj/inside")
				Expect(err).To(HaveOccurred(), "move into itself, mode %d", m)
				_, err = w.s.Move(Ctx(), a, "/users/alice/proj", "/users/alice/renamed")
				Expect(err).To(MatchError(workspace.ErrReadOnly), "rename, mode %d", m)
			}
			Expect(MustRead(w.s, AdminActor, "/users/alice/proj/a.txt")).To(Equal("a"))
		})

		It("cannot take away a root that sits inside another root", func() {
			w := newFenceWorld(newStore)
			home := adminStat(w.s, "/users/alice")
			chat, err := w.s.Mkdir(Ctx(), Alice, "/users/alice/Chats/c1")
			Expect(err).ToNot(HaveOccurred())
			made := latestRev(w.s)
			a := AliceChat.Fenced(
				workspace.FenceRoot{Node: home.ID, Mode: workspace.ModeWrite},
				workspace.FenceRoot{Node: chat.ID, Mode: workspace.ModeWrite},
			)
			Expect(w.s.Delete(Ctx(), a, "/users/alice/Chats/c1")).To(MatchError(workspace.ErrReadOnly), "delete the root")
			Expect(w.s.Delete(Ctx(), a, "/users/alice/Chats")).To(MatchError(workspace.ErrReadOnly), "delete a folder above the root")
			_, err = w.s.Move(Ctx(), a, "/users/alice/Chats/c1", "/users/alice/c1")
			Expect(err).To(MatchError(workspace.ErrReadOnly), "move the root")
			_, _, err = w.s.Undo(Ctx(), a, made, workspace.UndoOptions{})
			Expect(err).To(HaveOccurred(), "undo of the change set that made the root")

			v, err := w.s.View(Ctx(), a, []workspace.MountRequest{{Path: "/users/alice", At: "home", Mode: workspace.ModeWrite}})
			Expect(err).ToNot(HaveOccurred())
			_, _, _ = w.s.CommitUpper(Ctx(), v, a, latestRev(w.s), workspace.FixtureSource{
				D("home"), D("home/Chats"), W("home/Chats/c1"),
			}, workspace.UpperOptions{Title: "x"})

			n, err := w.s.Stat(Ctx(), AdminActor, "/users/alice/Chats/c1")
			Expect(err).ToNot(HaveOccurred(), "the root is gone")
			Expect(n.ID).To(Equal(chat.ID))
		})

		It("cannot move a node out of or into the fence", func() {
			w := newFenceWorld(newStore)
			a := w.fenced(workspace.ModeWrite)
			_, err := w.s.Move(Ctx(), a, "/users/alice/proj/a.txt", "/users/alice/other/a.txt")
			Expect(err).To(MatchError(workspace.ErrNotFound), "out of the fence")
			_, err = w.s.Move(Ctx(), a, "/users/alice/proj/sub", "/users/alice/sub")
			Expect(err).To(MatchError(workspace.ErrNotFound), "a folder out of the fence")
			_, err = w.s.Move(Ctx(), a, "/users/alice/other/o.txt", "/users/alice/proj/o.txt")
			Expect(err).To(MatchError(workspace.ErrNotFound), "into the fence")
			Expect(MustRead(w.s, AdminActor, "/users/alice/proj/a.txt")).To(Equal("a"))
			Expect(MustRead(w.s, AdminActor, "/users/alice/other/o.txt")).To(Equal("o"))
		})

		It("creates missing parents only inside the fence", func() {
			w := newFenceWorld(newStore)
			a := w.fenced(workspace.ModeWrite)
			MustPut(w.s, a, "/users/alice/proj/x/y/z.txt", "z")
			_, err := w.s.Mkdir(Ctx(), a, "/users/alice/proj/m/n")
			Expect(err).ToNot(HaveOccurred())
			Expect(put(w.s, a, "/users/alice/new/z.txt")).To(MatchError(workspace.ErrNotFound), "put")
			_, err = w.s.Mkdir(Ctx(), a, "/users/alice/new2/sub")
			Expect(err).To(MatchError(workspace.ErrNotFound), "mkdir")
			_, err = w.s.Move(Ctx(), a, "/users/alice/proj/a.txt", "/users/alice/new3/a.txt")
			Expect(err).To(MatchError(workspace.ErrNotFound), "move")
			for _, p := range []string{"/users/alice/new", "/users/alice/new2", "/users/alice/new3"} {
				_, err := w.s.Stat(Ctx(), AdminActor, p)
				Expect(err).To(MatchError(workspace.ErrNotFound), "%s was created", p)
			}
		})

		It("sees only its roots in ReadablePrefixes", func() {
			w := newFenceWorld(newStore)
			hidden := adminStat(w.s, "/shared/hidden")
			bob := adminStat(w.s, "/users/bob")
			a := AliceChat.Fenced(
				workspace.FenceRoot{Node: w.proj.ID, Mode: workspace.ModeWrite},
				workspace.FenceRoot{Node: w.sub.ID, Mode: workspace.ModeRead},
				workspace.FenceRoot{Node: w.shared.ID, Mode: workspace.ModeRead},
				workspace.FenceRoot{Node: hidden.ID, Mode: workspace.ModeRead},
				workspace.FenceRoot{Node: bob.ID, Mode: workspace.ModeWrite},
			)
			prefixes, err := w.s.ReadablePrefixes(Ctx(), a)
			Expect(err).ToNot(HaveOccurred())
			Expect(prefixes).To(Equal([]string{"/shared/s", "/users/alice/proj"}))
			prefixes, err = w.s.ReadablePrefixes(Ctx(), AliceChat.Fenced())
			Expect(err).ToNot(HaveOccurred())
			Expect(prefixes).To(BeEmpty(), "a fence with no root")
		})

		It("cannot grant or revoke", func() {
			w := newFenceWorld(newStore)
			for _, a := range []workspace.Actor{w.fenced(workspace.ModeWrite), AdminActor.Fenced(workspace.FenceRoot{Node: w.proj.ID, Mode: workspace.ModeWrite})} {
				Expect(w.s.Grant(Ctx(), a, Bob.Principal, "/users/alice/proj", workspace.ModeRead)).To(MatchError(workspace.ErrNotAdmin))
				Expect(w.s.Revoke(Ctx(), a, Alice.Principal, "/users/alice")).To(MatchError(workspace.ErrNotAdmin))
				if gl, ok := w.s.(workspace.GrantLister); ok {
					_, err := gl.Grants(Ctx(), a, "/users/alice")
					Expect(err).To(MatchError(workspace.ErrNotAdmin))
				}
			}
			_, err := w.s.Stat(Ctx(), Bob, "/users/alice/proj")
			Expect(err).To(MatchError(workspace.ErrNotFound), "the grant was made")
		})

		It("filters History and ChangesSince to the fence", func() {
			w := newFenceWorld(newStore)
			base := latestRev(w.s)
			MustPut(w.s, Alice, "/users/alice/other/secret-name.txt", "x")
			MustPut(w.s, Alice, "/users/alice/proj/in.txt", "y")
			_, err := w.s.Move(Ctx(), Alice, "/users/alice/proj/a.txt", "/users/alice/other/moved-out.txt")
			Expect(err).ToNot(HaveOccurred())
			a := w.fenced(workspace.ModeWrite)

			noOutside := func(sets []workspace.ChangeSet) {
				GinkgoHelper()
				for _, cs := range sets {
					Expect(cs.Title).ToNot(ContainSubstring("other"), "title of %d", cs.Rev)
					for _, op := range cs.Ops {
						for _, p := range []string{op.OldPath, op.NewPath} {
							if p != "" {
								Expect(workspace.Within(p, "/users/alice/proj")).To(BeTrue(), "op of %d names %s", cs.Rev, p)
							}
						}
					}
				}
			}
			sets, err := w.s.ChangesSince(Ctx(), a, base, 100)
			Expect(err).ToNot(HaveOccurred())
			Expect(sets).To(HaveLen(2), "the change set outside is absent")
			noOutside(sets)
			Expect(sets[1].Ops[0].Kind).To(Equal(workspace.OpDelete), "a move out shows as a delete")

			sets, err = w.s.History(Ctx(), a, workspace.HistoryQuery{Path: "/users/alice/proj"})
			Expect(err).ToNot(HaveOccurred())
			Expect(sets).ToNot(BeEmpty())
			noOutside(sets)
			_, err = w.s.History(Ctx(), a, workspace.HistoryQuery{Path: "/users/alice"})
			Expect(err).To(MatchError(workspace.ErrNotFound), "history of the home")
			_, err = w.s.History(Ctx(), a, workspace.HistoryQuery{})
			Expect(err).To(MatchError(workspace.ErrNotFound), "history of everything")

			sets, err = w.s.ChangesSince(Ctx(), Alice, base, 100)
			Expect(err).ToNot(HaveOccurred())
			Expect(sets).To(HaveLen(3), "the owner sees all")
		})

		for _, surface := range []string{"History", "ChangesSince"} {
			It("hides historical outside paths after a folder moves into the fence in "+surface, func() {
				w := newFenceWorld(newStore)
				base := latestRev(w.s)
				MustPut(w.s, Alice, "/users/alice/secret-origin/nested/stable.txt", "v1")
				MustPut(w.s, Alice, "/users/alice/secret-origin/nested/secret-old.txt", "old")
				tx, err := w.s.Begin(Ctx(), Alice, "secret-origin and secret-old.txt")
				Expect(err).ToNot(HaveOccurred())
				historicalWrite := func(tx workspace.Tx) error {
					if err := putIn(tx, "/users/alice/secret-origin/nested/stable.txt", "v2"); err != nil {
						return err
					}
					if _, err := tx.Move(Ctx(), "/users/alice/secret-origin/nested/secret-old.txt", "/users/alice/secret-origin/nested/public.txt"); err != nil {
						return err
					}
					// Keep this revision in the folder's History query too.
					return putIn(tx, "/users/alice/proj/in.txt", "inside")
				}
				Expect(historicalWrite(tx)).To(Succeed())
				historical, err := tx.Commit()
				Expect(err).ToNot(HaveOccurred())
				before, err := w.s.ChangesSince(Ctx(), Alice, base, 100)
				Expect(err).ToNot(HaveOccurred())
				_, err = w.s.Move(Ctx(), Alice, "/users/alice/secret-origin", "/users/alice/proj/imported")
				Expect(err).ToNot(HaveOccurred())
				imported := latestRev(w.s)
				a := w.fenced(workspace.ModeWrite)
				var sets []workspace.ChangeSet
				if surface == "History" {
					sets, err = w.s.History(Ctx(), a, workspace.HistoryQuery{Path: "/users/alice/proj"})
				} else {
					sets, err = w.s.ChangesSince(Ctx(), a, base, 100)
				}
				Expect(err).ToNot(HaveOccurred())
				Expect(sets).ToNot(BeEmpty())
				for _, cs := range sets {
					Expect(cs.Title).ToNot(ContainSubstring("secret-"))
					for _, op := range cs.Ops {
						for _, p := range []string{op.OldPath, op.NewPath} {
							Expect(p).ToNot(ContainSubstring("secret-"))
							if p != "" {
								Expect(workspace.Within(p, "/users/alice/proj")).To(BeTrue(), "revision %d: %s", cs.Rev, p)
							}
						}
						if cs.Rev == imported {
							Expect(op.Kind).To(Equal(workspace.OpCreate))
							Expect(op.OldPath).To(BeEmpty())
						}
					}
				}
				// Filtering must not rewrite the journal or alter owner history.
				after, err := w.s.ChangesSince(Ctx(), Alice, base, len(before))
				Expect(err).ToNot(HaveOccurred())
				Expect(after).To(Equal(before))
				// Undo still uses identities, including the historical descendant move.
				_, conflicts, err := w.s.Undo(Ctx(), a, historical.Rev, workspace.UndoOptions{})
				Expect(err).ToNot(HaveOccurred())
				Expect(conflicts).To(BeEmpty())
				Expect(MustRead(w.s, a, "/users/alice/proj/imported/nested/stable.txt")).To(Equal("v1"))
				Expect(MustRead(w.s, a, "/users/alice/proj/imported/nested/secret-old.txt")).To(Equal("old"))
				_, _, err = w.s.Undo(Ctx(), a, imported, workspace.UndoOptions{})
				Expect(err).To(MatchError(workspace.ErrNotFound), "undo cannot move the folder outside the fence")
			})
		}

		for _, destination := range []string{"/users/alice/proj/renamed", "/users/alice/other/removed"} {
			It("hides stale descendant paths and titles after an ancestor moves to "+destination, func() {
				w := newFenceWorld(newStore)
				MustPut(w.s, Alice, "/users/alice/proj/old-ancestor/nested/a.txt", "v1")
				base := latestRev(w.s)
				tx, err := w.s.Begin(Ctx(), Alice, "old-ancestor private title")
				Expect(err).ToNot(HaveOccurred())
				Expect(putIn(tx, "/users/alice/proj/old-ancestor/nested/a.txt", "v2")).To(Succeed())
				Expect(putIn(tx, "/users/alice/proj/keep.txt", "keep")).To(Succeed())
				_, err = tx.Commit()
				Expect(err).ToNot(HaveOccurred())
				_, err = w.s.Move(Ctx(), Alice, "/users/alice/proj/old-ancestor", destination)
				Expect(err).ToNot(HaveOccurred())
				a := w.fenced(workspace.ModeWrite)
				feed, err := w.s.ChangesSince(Ctx(), a, base, 100)
				Expect(err).ToNot(HaveOccurred())
				history, err := w.s.History(Ctx(), a, workspace.HistoryQuery{Path: "/users/alice/proj"})
				Expect(err).ToNot(HaveOccurred())
				for _, sets := range [][]workspace.ChangeSet{feed, history} {
					Expect(sets).ToNot(BeEmpty())
					for _, cs := range sets {
						Expect(cs.Title).ToNot(ContainSubstring("private title"))
						for _, op := range cs.Ops {
							for _, p := range []string{op.OldPath, op.NewPath} {
								Expect(p).ToNot(ContainSubstring("old-ancestor/nested"))
								if p != "" {
									Expect(workspace.Within(p, "/users/alice/proj")).To(BeTrue())
								}
							}
						}
					}
				}
			})
		}

		It("refuses a view mount outside the fence and caps a mount inside", func() {
			w := newFenceWorld(newStore)
			a := w.fenced(workspace.ModeRead)
			for _, p := range []string{"/users/alice", "/users/alice/other", "/users/bob", "/shared/s"} {
				_, err := w.s.View(Ctx(), a, []workspace.MountRequest{{Path: p, At: "w", Mode: workspace.ModeRead}})
				Expect(err).To(MatchError(workspace.ErrNotFound), "view of %s", p)
			}
			v, err := w.s.View(Ctx(), a, []workspace.MountRequest{
				{Path: "/users/alice/proj", At: "home", Mode: workspace.ModeWrite},
				{Path: "/users/alice/proj/sub", At: "sub", Mode: workspace.ModeWrite},
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(v.Mounts[0].Mode).To(Equal(workspace.ModeRead))
			Expect(v.Mounts[1].Mode).To(Equal(workspace.ModeRead))
			Expect(v.Actor.SameFence(a)).To(BeTrue(), "the view keeps the fence")

			dir := filepath.Join(GinkgoT().TempDir(), "view")
			_, err = w.s.Materialize(Ctx(), v, dir)
			Expect(err).ToNot(HaveOccurred())
			Expect(filepath.Join(dir, "home", "a.txt")).To(BeAnExistingFile())
			Expect(filepath.Join(dir, "home", "sub", "b.txt")).To(BeAnExistingFile())
		})

		It("cannot materialize or commit a view built by hand outside the fence", func() {
			w := newFenceWorld(newStore)
			a := w.fenced(workspace.ModeWrite)
			v := workspace.View{Actor: a, Mounts: []workspace.Mount{{Path: "/users/alice/other", At: "w", Mode: workspace.ModeWrite, NodeID: w.other.ID}}}
			_, err := w.s.Materialize(Ctx(), v, filepath.Join(GinkgoT().TempDir(), "view"))
			Expect(err).To(MatchError(workspace.ErrNotFound), "materialize")
			_, _, err = w.s.CommitUpper(Ctx(), v, a, latestRev(w.s), workspace.FixtureSource{D("w"), F("w/x.txt", "x")}, workspace.UpperOptions{Title: "x"})
			Expect(err).To(MatchError(workspace.ErrNotFound), "commit")

			// A view the owner took, committed by the fenced actor.
			owner, err := w.s.View(Ctx(), Alice, []workspace.MountRequest{{Path: "/users/alice", At: "home", Mode: workspace.ModeWrite}})
			Expect(err).ToNot(HaveOccurred())
			_, _, err = w.s.CommitUpper(Ctx(), owner, a, latestRev(w.s), workspace.FixtureSource{D("home"), D("home/other"), F("home/other/x.txt", "x")}, workspace.UpperOptions{Title: "x"})
			Expect(err).To(MatchError(workspace.ErrNotFound), "commit of the owner's view")
			_, err = w.s.Stat(Ctx(), AdminActor, "/users/alice/other/x.txt")
			Expect(err).To(MatchError(workspace.ErrNotFound), "a file was written outside")
		})

		It("commits an upper entry outside every mount as an outside-view conflict", func() {
			w := newFenceWorld(newStore)
			a := w.fenced(workspace.ModeWrite)
			v, err := w.s.View(Ctx(), a, []workspace.MountRequest{{Path: "/users/alice/proj", At: "home", Mode: workspace.ModeWrite}})
			Expect(err).ToNot(HaveOccurred())
			_, conflicts, err := w.s.CommitUpper(Ctx(), v, a, latestRev(w.s), workspace.FixtureSource{
				D("home"), F("home/in.txt", "in"), workspace.FixtureSymlink("home/link"),
				D("other"), F("other/x.txt", "x"),
			}, workspace.UpperOptions{Title: "turn"})
			Expect(err).ToNot(HaveOccurred())
			Expect(reasons(conflicts)).To(ContainElements(workspace.ReasonUnsupported, workspace.ReasonOutsideView))
			Expect(reasons(conflicts)).To(HaveEach(BeElementOf(workspace.ReasonUnsupported, workspace.ReasonOutsideView)))
			Expect(MustRead(w.s, a, "/users/alice/proj/in.txt")).To(Equal("in"))
			tree := Tree(w.s)
			Expect(tree).ToNot(HaveKey("/users/alice/proj/link"))
			for p := range tree {
				Expect(p).ToNot(HaveSuffix("/x.txt"), "an entry outside landed at %s", p)
			}
		})

		It("follows a renamed and a moved root", func() {
			w := newFenceWorld(newStore)
			a := w.fenced(workspace.ModeWrite)
			_, err := w.s.Move(Ctx(), Alice, "/users/alice/proj", "/users/alice/renamed")
			Expect(err).ToNot(HaveOccurred())
			Expect(MustRead(w.s, a, "/users/alice/renamed/a.txt")).To(Equal("a"))
			MustPut(w.s, a, "/users/alice/renamed/after-rename.txt", "r")
			_, err = w.s.Move(Ctx(), Alice, "/users/alice/renamed", "/users/alice/other/deep/proj")
			Expect(err).ToNot(HaveOccurred())
			Expect(MustRead(w.s, a, "/users/alice/other/deep/proj/after-rename.txt")).To(Equal("r"))
			expectHidden(w.s, a, "/users/alice/other/o.txt", false)
			expectHidden(w.s, a, "/users/alice/other/deep", true)
			prefixes, err := w.s.ReadablePrefixes(Ctx(), a)
			Expect(err).ToNot(HaveOccurred())
			Expect(prefixes).To(Equal([]string{"/users/alice/other/deep/proj"}))
		})

		It("loses a deleted root and gets it back after Undo", func() {
			w := newFenceWorld(newStore)
			a := w.fenced(workspace.ModeWrite)
			Expect(w.s.Delete(Ctx(), Alice, "/users/alice/proj")).To(Succeed())
			del := latestRev(w.s)
			expectHidden(w.s, a, "/users/alice/proj", true)
			Expect(put(w.s, a, "/users/alice/proj/new.txt")).To(MatchError(workspace.ErrNotFound), "put recreates the folder")
			prefixes, err := w.s.ReadablePrefixes(Ctx(), a)
			Expect(err).ToNot(HaveOccurred())
			Expect(prefixes).To(BeEmpty())
			_, err = w.s.View(Ctx(), a, []workspace.MountRequest{{Path: "/users/alice", At: "home", Mode: workspace.ModeRead}})
			Expect(err).To(MatchError(workspace.ErrNotFound))
			sets, err := w.s.ChangesSince(Ctx(), a, 0, 100)
			Expect(err).ToNot(HaveOccurred())
			Expect(sets).To(BeEmpty(), "history inside a deleted root")

			_, _, err = w.s.Undo(Ctx(), Alice, del, workspace.UndoOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(MustRead(w.s, a, "/users/alice/proj/a.txt")).To(Equal("a"))
		})

		It("never widens when fenced twice", func() {
			w := newFenceWorld(newStore)
			home := adminStat(w.s, "/users/alice")
			proj := workspace.FenceRoot{Node: w.proj.ID, Mode: workspace.ModeWrite}
			subRead := workspace.FenceRoot{Node: w.sub.ID, Mode: workspace.ModeRead}

			// A wider second fence adds nothing.
			a := AliceChat.Fenced(proj).Fenced(workspace.FenceRoot{Node: home.ID, Mode: workspace.ModeWrite})
			MustPut(w.s, a, "/users/alice/proj/n.txt", "n")
			expectHidden(w.s, a, "/users/alice/top.txt", false)
			expectHidden(w.s, a, "/users/alice/other", true)

			// A narrower one, in either order, leaves the intersection.
			for _, b := range []workspace.Actor{AliceChat.Fenced(proj).Fenced(subRead), AliceChat.Fenced(subRead).Fenced(proj)} {
				Expect(MustRead(w.s, b, "/users/alice/proj/sub/b.txt")).To(Equal("b"))
				expectHidden(w.s, b, "/users/alice/proj/a.txt", false)
				Expect(put(w.s, b, "/users/alice/proj/sub/c.txt")).To(MatchError(workspace.ErrReadOnly))
				prefixes, err := w.s.ReadablePrefixes(Ctx(), b)
				Expect(err).ToNot(HaveOccurred())
				Expect(prefixes).To(Equal([]string{"/users/alice/proj/sub"}))
			}

			// Disjoint fences reach nothing.
			c := AliceChat.Fenced(proj).Fenced(workspace.FenceRoot{Node: w.other.ID, Mode: workspace.ModeWrite})
			expectHidden(w.s, c, "/users/alice/proj", true)
			expectHidden(w.s, c, "/users/alice/other", true)
		})

		It("refuses more than MaxFenceRoots roots", func() {
			w := newFenceWorld(newStore)
			roots := make([]workspace.FenceRoot, workspace.MaxFenceRoots)
			for i := range roots {
				roots[i] = workspace.FenceRoot{Node: w.proj.ID, Mode: workspace.ModeRead}
			}
			Expect(MustRead(w.s, AliceChat.Fenced(roots...), "/users/alice/proj/a.txt")).To(Equal("a"), "at the limit")

			bad := map[string]workspace.Actor{
				"too many roots": AliceChat.Fenced(append(roots, workspace.FenceRoot{Node: w.proj.ID, Mode: workspace.ModeRead})...),
				"no mode":        AliceChat.Fenced(workspace.FenceRoot{Node: w.proj.ID}),
				"no node":        AliceChat.Fenced(workspace.FenceRoot{Mode: workspace.ModeRead}),
			}
			for name, a := range bad {
				_, err := w.s.Stat(Ctx(), a, "/users/alice/proj/a.txt")
				Expect(err).To(MatchError(workspace.ErrInvalid), "stat, %s", name)
				Expect(put(w.s, a, "/users/alice/proj/a.txt")).To(MatchError(workspace.ErrInvalid), "put, %s", name)
				_, err = w.s.View(Ctx(), a, []workspace.MountRequest{{Path: "/users/alice/proj", At: "w", Mode: workspace.ModeRead}})
				Expect(err).To(MatchError(workspace.ErrInvalid), "view, %s", name)
				_, err = w.s.ReadablePrefixes(Ctx(), a)
				Expect(err).To(MatchError(workspace.ErrInvalid), "prefixes, %s", name)
			}
		})

		It("keeps the fence on a copy of the actor", func() {
			w := newFenceWorld(newStore)
			a := w.fenced(workspace.ModeWrite)
			b := a
			b.ChatID = ""
			Expect(b.SameFence(a)).To(BeTrue())
			expectHidden(w.s, b, "/users/alice/other/o.txt", false)
			Expect(MustRead(w.s, b, "/users/alice/proj/a.txt")).To(Equal("a"))
			Expect(w.s.Delete(Ctx(), b, "/users/alice/proj")).To(MatchError(workspace.ErrReadOnly))
		})
	})
}

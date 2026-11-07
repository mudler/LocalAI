// SPDX-License-Identifier: MIT
package sharedfolders_test

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/pages"
	"github.com/mudler/LocalAI/core/services/studio/sharedfolders"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
)

// directory is an account directory of active named accounts. Off holds the
// accounts that are disabled.
type directory struct {
	users
	off map[string]bool
}

// Accounts takes three characters or more, as the picker of the sharing
// routes does, and leaves the caller in so that the service's own filter is
// checked.
func (d directory) Accounts(_ context.Context, _, query string, limit int) ([]sharedfolders.User, error) {
	if utf8.RuneCountInString(query) < 3 {
		return nil, sharedfolders.ErrBadQuery
	}
	var out []sharedfolders.User
	for _, u := range d.users {
		if !d.off[u.ID] && strings.HasPrefix(strings.ToLower(u.Name), strings.ToLower(query)) {
			out = append(out, u)
		}
	}
	slices.SortFunc(out, func(a, b sharedfolders.User) int { return strings.Compare(a.Name, b.Name) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (d directory) Active(_ context.Context, id string) (sharedfolders.User, error) {
	u, ok := d.users[id]
	if !ok || d.off[id] {
		return sharedfolders.User{}, sharedfolders.ErrUnknownUser
	}
	return u, nil
}

// lost records the principals whose access an owner took away.
type lost struct {
	mu   sync.Mutex
	seen []string
}

func (l *lost) add(_ context.Context, principal string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seen = append(l.seen, principal)
}

func (l *lost) list() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.seen)
}

var _ = Describe("folders an owner shares from their home", func() {
	var (
		ctx   context.Context
		db    *gorm.DB
		f     *files.Service
		svc   *sharedfolders.Service
		dir   directory
		gone  *lost
		marta workspace.Actor
	)
	mkdir := func(who, p string) int64 {
		GinkgoHelper()
		n, err := f.Store().Mkdir(ctx, f.Actor(who), p)
		Expect(err).NotTo(HaveOccurred())
		return n.ID
	}
	put := func(who, p, body string) error {
		_, err := f.Store().Put(ctx, f.Actor(who), p, strings.NewReader(body), workspace.PutOptions{Overwrite: true})
		return err
	}
	read := func(who, p string) error {
		_, err := f.Store().Stat(ctx, f.Actor(who), p)
		return err
	}
	newService := func(fs *files.Service) *sharedfolders.Service {
		audit, err := sharedfolders.NewGORMAudit(db)
		Expect(err).NotTo(HaveOccurred())
		return sharedfolders.New(sharedfolders.Options{Files: fs, Audit: audit, Users: people, Directory: dir, AccessLost: gone.add})
	}
	filesOn := func(d *gorm.DB) *files.Service {
		blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
		Expect(err).NotTo(HaveOccurred())
		ws, err := gormstore.New(d, blobs, gormstore.Options{Admin: files.AdminPrincipal})
		Expect(err).NotTo(HaveOccurred())
		return files.New(ws)
	}

	BeforeEach(func() {
		ctx = context.Background()
		db = openDB()
		f = filesOn(db)
		dir = directory{users: people, off: map[string]bool{}}
		gone = &lost{}
		svc = newService(f)
		for _, u := range []string{"u-marta", "u-sara", "u-root"} {
			Expect(f.EnsureHome(ctx, u)).To(Succeed())
		}
		marta = files.UserActor("u-marta")
	})

	It("shares a folder of the owner's home to read and to write, and the grantee reads and writes only with write", func() {
		id := mkdir("u-marta", "/users/u-marta/Projects")
		Expect(put("u-marta", "/users/u-marta/Projects/plan.md", "# plan")).To(Succeed())
		Expect(read("u-sara", "/users/u-marta/Projects/plan.md")).To(MatchError(workspace.ErrNotFound), "nothing is shared yet")

		got, err := svc.OwnSetMember(ctx, marta, id, "u-sara", workspace.ModeRead)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Members).To(Equal([]sharedfolders.Member{{Principal: "u-sara", Name: "Sara", Mode: workspace.ModeRead}}))
		Expect(got.Path).To(Equal("/Projects"), "the owner sees the path in their home")
		Expect(read("u-sara", "/users/u-marta/Projects/plan.md")).To(Succeed())
		Expect(put("u-sara", "/users/u-marta/Projects/plan.md", "x")).To(MatchError(workspace.ErrReadOnly))

		_, err = svc.OwnSetMember(ctx, marta, id, "u-sara", workspace.ModeWrite)
		Expect(err).NotTo(HaveOccurred())
		Expect(put("u-sara", "/users/u-marta/Projects/plan.md", "edited")).To(Succeed())
		Expect(read("u-sara", "/users/u-marta/Other")).To(MatchError(workspace.ErrNotFound), "only the folder is shared")

		mine, err := svc.ForUser(ctx, "u-sara")
		Expect(err).NotTo(HaveOccurred())
		Expect(mine).To(Equal([]sharedfolders.Folder{{ID: id, Name: "Projects", Owner: "Marta", Mode: workspace.ModeWrite}}),
			"the grantee sees the owner's name, never the path of the owner's home")
		roots, err := svc.Roots(ctx, workspace.Actor{Principal: "u-sara", Groups: []string{files.EveryoneGroup}, ChatID: "c1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(roots).To(ConsistOf(HaveField("Path", "/users/u-marta/Projects")), "a chat of the grantee mounts it")

		owned, err := svc.Owned(ctx, marta)
		Expect(err).NotTo(HaveOccurred())
		Expect(owned).To(HaveLen(1))
		Expect(owned[0].ID).To(Equal(id))
		Expect(owned[0].Members).To(HaveLen(1))
	})

	It("takes a revoke and a lowering at the grantee's next call and reports the lost access", func() {
		id := mkdir("u-marta", "/users/u-marta/Projects")
		Expect(put("u-marta", "/users/u-marta/Projects/a.txt", "a")).To(Succeed())
		_, err := svc.OwnSetMember(ctx, marta, id, "u-sara", workspace.ModeWrite)
		Expect(err).NotTo(HaveOccurred())
		Expect(gone.list()).To(BeEmpty(), "a grant takes nothing away")

		_, err = svc.OwnSetMember(ctx, marta, id, "u-sara", workspace.ModeRead)
		Expect(err).NotTo(HaveOccurred())
		Expect(put("u-sara", "/users/u-marta/Projects/a.txt", "b")).To(MatchError(workspace.ErrReadOnly), "the lowering holds at once")
		Expect(gone.list()).To(Equal([]string{"u-sara"}))

		_, err = svc.OwnRemoveMember(ctx, marta, id, "u-sara")
		Expect(err).NotTo(HaveOccurred())
		Expect(read("u-sara", "/users/u-marta/Projects/a.txt")).To(MatchError(workspace.ErrNotFound), "the revoke holds at once")
		Expect(gone.list()).To(Equal([]string{"u-sara", "u-sara"}))
		mine, _ := svc.ForUser(ctx, "u-sara")
		Expect(mine).To(BeEmpty())
	})

	It("shares with everyone, and revoking everyone keeps a person's own grant", func() {
		id := mkdir("u-marta", "/users/u-marta/Team")
		_, err := svc.OwnSetMember(ctx, marta, id, sharedfolders.Everyone, workspace.ModeRead)
		Expect(err).NotTo(HaveOccurred())
		Expect(f.EnsureHome(ctx, "u-late")).To(Succeed())
		late, err := svc.ForUser(ctx, "u-late")
		Expect(err).NotTo(HaveOccurred())
		Expect(late).To(Equal([]sharedfolders.Folder{{ID: id, Name: "Team", Owner: "Marta", Everyone: true, Mode: workspace.ModeRead}}))
		mine, _ := svc.ForUser(ctx, "u-marta")
		Expect(mine).To(BeEmpty(), "the owner's own folder is not shared with the owner")

		_, err = svc.OwnSetMember(ctx, marta, id, "u-sara", workspace.ModeWrite)
		Expect(err).NotTo(HaveOccurred())
		got, err := svc.OwnRemoveMember(ctx, marta, id, sharedfolders.Everyone)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Members).To(Equal([]sharedfolders.Member{{Principal: "u-sara", Name: "Sara", Mode: workspace.ModeWrite}}))
		Expect(gone.list()).To(Equal([]string{sharedfolders.Everyone}))
		late, _ = svc.ForUser(ctx, "u-late")
		Expect(late).To(BeEmpty())
		sara, _ := svc.ForUser(ctx, "u-sara")
		Expect(sara).To(HaveLen(1))
	})

	It("refuses a folder outside the owner's home, the home itself, a managed folder, an administrator folder and a folder the owner only reads, with one answer", func() {
		own := mkdir("u-marta", "/users/u-marta/Mine")
		saras := mkdir("u-sara", "/users/u-sara/Hers")
		team, err := svc.Create(ctx, "u-root", "Team")
		Expect(err).NotTo(HaveOccurred())
		_, err = svc.SetMember(ctx, "u-root", team.ID, "u-marta", workspace.ModeWrite)
		Expect(err).NotTo(HaveOccurred())
		_, err = svc.OwnSetMember(ctx, files.UserActor("u-sara"), saras, "u-marta", workspace.ModeRead)
		Expect(err).NotTo(HaveOccurred())
		home, err := f.Store().Stat(ctx, marta, "/users/u-marta")
		Expect(err).NotTo(HaveOccurred())
		settings := mkdir("u-marta", "/users/u-marta/LocalAI")
		chats, err := f.Store().Stat(ctx, marta, "/users/u-marta/Chats")
		Expect(err).NotTo(HaveOccurred())

		for name, id := range map[string]int64{
			"another person's folder that marta reads":      saras,
			"an administrator shared folder marta may edit": team.ID,
			"marta's home itself":                           home.ID,
			"LocalAI/":                                      settings,
			"Chats/":                                        chats.ID,
			"a node that does not exist":                    987654,
		} {
			_, err := svc.OwnSetMember(ctx, marta, id, "u-sara", workspace.ModeRead)
			Expect(err).To(MatchError(sharedfolders.ErrNotShareable), name)
			_, err = svc.OwnGrants(ctx, marta, id)
			Expect(err).To(MatchError(sharedfolders.ErrNotShareable), name)
			_, err = svc.OwnRemoveMember(ctx, marta, id, "u-sara")
			Expect(err).To(MatchError(sharedfolders.ErrNotShareable), name)
		}
		Expect(svc.Get(ctx, team.ID)).To(HaveField("Members", HaveLen(2)), "the administrator folder is unchanged")
		_, err = svc.OwnSetMember(ctx, marta, own, "u-sara", workspace.ModeRead)
		Expect(err).NotTo(HaveOccurred(), "the owner's own folder is fine")
	})

	It("refuses a fenced, agent or tool actor, and a principal that is not an active named account", func() {
		id := mkdir("u-marta", "/users/u-marta/Projects")
		for name, a := range map[string]workspace.Actor{
			"a chat of the owner":  {Principal: "u-marta", Groups: []string{files.EveryoneGroup}, ChatID: "c1"},
			"a fenced agent actor": marta.Fenced(workspace.FenceRoot{Node: id, Mode: workspace.ModeWrite}),
			"the workspace admin":  {Principal: files.AdminPrincipal},
			"nobody":               {},
			"the everyone group":   {Principal: files.EveryoneGroup},
		} {
			_, err := svc.OwnSetMember(ctx, a, id, "u-sara", workspace.ModeRead)
			Expect(err).To(MatchError(sharedfolders.ErrNotShareable), name)
		}
		dir.off["u-sara"] = true
		_, err := svc.OwnSetMember(ctx, marta, id, "u-sara", workspace.ModeRead)
		Expect(err).To(MatchError(sharedfolders.ErrUnknownUser), "a disabled account")
		for _, p := range []string{"nobody", "Sara", "u-marta", files.AdminPrincipal, "@group"} {
			_, err = svc.OwnSetMember(ctx, marta, id, p, workspace.ModeRead)
			Expect(err).To(MatchError(sharedfolders.ErrUnknownUser), p)
		}
		_, err = svc.OwnSetMember(ctx, marta, id, "u-root", workspace.ModeNone)
		Expect(err).To(MatchError(sharedfolders.ErrBadMode))
		owned, err := svc.Owned(ctx, marta)
		Expect(err).NotTo(HaveOccurred())
		Expect(owned).To(BeEmpty())
	})

	It("finds people by name for the picker, without the owner", func() {
		id := mkdir("u-marta", "/users/u-marta/Projects")
		got, err := svc.SearchAccounts(ctx, marta, id, "mar", 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeEmpty(), "the owner is left out")
		got, err = svc.SearchAccounts(ctx, marta, id, "sar", 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal([]sharedfolders.User{{ID: "u-sara", Name: "Sara"}}))
		_, err = svc.SearchAccounts(ctx, marta, 987654, "sar", 10)
		Expect(err).To(MatchError(sharedfolders.ErrNotShareable))
		_, err = svc.SearchAccounts(ctx, marta, id, "sa", 10)
		Expect(err).To(MatchError(sharedfolders.ErrBadQuery), "the answer of the picker reaches the caller")
	})

	It("writes the audit with the owner as the actor", func() {
		id := mkdir("u-marta", "/users/u-marta/Projects")
		_, err := svc.OwnSetMember(ctx, marta, id, "u-sara", workspace.ModeWrite)
		Expect(err).NotTo(HaveOccurred())
		_, err = svc.OwnRemoveMember(ctx, marta, id, "u-sara")
		Expect(err).NotTo(HaveOccurred())
		evs, err := svc.Events(ctx, 2)
		Expect(err).NotTo(HaveOccurred())
		Expect(evs).To(HaveLen(2))
		Expect(evs[1]).To(And(HaveField("Actor", "u-marta"), HaveField("Action", sharedfolders.ActionGrant),
			HaveField("Principal", "u-sara"), HaveField("Mode", workspace.ModeWrite), HaveField("FolderID", id),
			HaveField("Folder", "/users/u-marta/Projects")))
		Expect(evs[0]).To(And(HaveField("Actor", "u-marta"), HaveField("Action", sharedfolders.ActionRevoke), HaveField("Principal", "u-sara")))
	})

	It("takes the grantee's access away with the folder in the Trash and gives it back with a restore", func() {
		id := mkdir("u-marta", "/users/u-marta/Projects")
		Expect(put("u-marta", "/users/u-marta/Projects/a.txt", "a")).To(Succeed())
		_, err := svc.OwnSetMember(ctx, marta, id, "u-sara", workspace.ModeRead)
		Expect(err).NotTo(HaveOccurred())
		tx, err := f.Store().Begin(ctx, marta, "Delete Projects")
		Expect(err).NotTo(HaveOccurred())
		Expect(tx.Delete(ctx, "/users/u-marta/Projects")).To(Succeed())
		cs, err := tx.Commit()
		Expect(err).NotTo(HaveOccurred())
		Expect(read("u-sara", "/users/u-marta/Projects/a.txt")).To(MatchError(workspace.ErrNotFound))
		mine, _ := svc.ForUser(ctx, "u-sara")
		Expect(mine).To(BeEmpty())
		_, _, err = f.Store().Undo(ctx, marta, cs.Rev, workspace.UndoOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(read("u-sara", "/users/u-marta/Projects/a.txt")).To(Succeed(), "the grant follows the folder")
	})

	It("keeps the pages of a folder in a home out of the shared pages", func() {
		id := mkdir("u-marta", "/users/u-marta/Site")
		Expect(put("u-marta", "/users/u-marta/Site/pages/home/index.html", "<p>hi</p>")).To(Succeed())
		_, err := svc.OwnSetMember(ctx, marta, id, "u-sara", workspace.ModeRead)
		Expect(err).NotTo(HaveOccurred())
		list, err := pages.New(f).ListShared(ctx, "u-sara")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(BeEmpty(), "a page of a person runs in nobody else's browser")
		_, err = pages.New(f).GetShared(ctx, "u-sara", id, "home")
		Expect(err).To(MatchError(pages.ErrNotFound))
		mine, _ := svc.ForUser(ctx, "u-sara")
		Expect(mine).To(ConsistOf(HaveField("Pages", 0)))
	})

	It("refuses to share the folders that LocalAI reads as the owner's own words", func() {
		for _, p := range []string{".skills", "Work/.skills", "Work/.skills/report", "Memory", "Memory/agents", "pages", "pages/home"} {
			id := mkdir("u-marta", "/users/u-marta/"+p)
			for _, m := range []workspace.Mode{workspace.ModeRead, workspace.ModeWrite} {
				_, err := svc.OwnSetMember(ctx, marta, id, "u-sara", m)
				Expect(err).To(MatchError(sharedfolders.ErrNotShareable), p)
				_, err = svc.OwnSetMember(ctx, marta, id, sharedfolders.Everyone, m)
				Expect(err).To(MatchError(sharedfolders.ErrNotShareable), p)
			}
			_, err := svc.OwnGrants(ctx, marta, id)
			Expect(err).To(MatchError(sharedfolders.ErrNotShareable), p)
			_, err = svc.SearchAccounts(ctx, marta, id, "Sar", 0)
			Expect(err).To(MatchError(sharedfolders.ErrNotShareable), p)
		}
		Expect(put("u-sara", "/users/u-marta/.skills/x/SKILL.md", "x")).To(MatchError(workspace.ErrNotFound), "nothing was shared")
		id := mkdir("u-marta", "/users/u-marta/Work/skills-notes")
		_, err := svc.OwnSetMember(ctx, marta, id, "u-sara", workspace.ModeWrite)
		Expect(err).NotTo(HaveOccurred(), "a name that only looks alike is a plain folder")
	})

	It("does not let a grant that a grantee moves in outlive the grantee's revoke", func() {
		projects := mkdir("u-marta", "/users/u-marta/Projects")
		_, err := svc.OwnSetMember(ctx, marta, projects, "u-sara", workspace.ModeWrite)
		Expect(err).NotTo(HaveOccurred())
		y := mkdir("u-sara", "/users/u-sara/Y")
		_, err = svc.OwnSetMember(ctx, files.UserActor("u-sara"), y, "u-root", workspace.ModeWrite)
		Expect(err).NotTo(HaveOccurred())

		_, err = f.Store().Move(ctx, f.Actor("u-sara"), "/users/u-sara/Y", "/users/u-marta/Projects/Y")
		Expect(err).To(MatchError(workspace.ErrSharedMove), "the share cannot follow Y into Marta's home")
		_, err = svc.OwnRemoveMember(ctx, marta, projects, "u-sara")
		Expect(err).NotTo(HaveOccurred())

		Expect(put("u-root", "/users/u-marta/Projects/Y/note.txt", "x")).To(MatchError(workspace.ErrNotFound), "Root has nothing in Marta's home")
		Expect(put("u-sara", "/users/u-marta/Projects/a.txt", "x")).To(MatchError(workspace.ErrNotFound), "the revoke holds")
		Expect(put("u-root", "/users/u-sara/Y/note.txt", "x")).To(Succeed(), "Sara's own share is unchanged")
		owned, err := svc.Owned(ctx, marta)
		Expect(err).NotTo(HaveOccurred())
		Expect(owned).To(BeEmpty())
	})

	It("lists and revokes every grant inside the owner's home, whoever made it, managed folders included", func() {
		admin := workspace.Actor{Principal: files.AdminPrincipal}
		y := mkdir("u-marta", "/users/u-marta/Projects/Y")
		old := mkdir("u-marta", "/users/u-marta/LocalAI/Old")
		chats, err := f.Store().Stat(ctx, marta, "/users/u-marta/Chats")
		Expect(err).NotTo(HaveOccurred())
		skillsDir := mkdir("u-marta", "/users/u-marta/.skills")
		// Grants that the rules no longer let anyone make: moved in before
		// them, or made by an administrator.
		Expect(f.Store().Grant(ctx, admin, "u-root", "/users/u-marta/Projects/Y", workspace.ModeWrite)).To(Succeed())
		Expect(f.Store().Grant(ctx, admin, "u-sara", "/users/u-marta/LocalAI/Old", workspace.ModeWrite)).To(Succeed())
		Expect(f.Store().Grant(ctx, admin, "u-sara", "/users/u-marta/Chats", workspace.ModeRead)).To(Succeed())
		Expect(f.Store().Grant(ctx, admin, sharedfolders.Everyone, "/users/u-marta/.skills", workspace.ModeWrite)).To(Succeed())

		owned, err := svc.Owned(ctx, marta)
		Expect(err).NotTo(HaveOccurred())
		paths := map[string]int64{}
		for _, o := range owned {
			paths[o.Path] = o.ID
		}
		Expect(paths).To(Equal(map[string]int64{"/.skills": skillsDir, "/Chats": chats.ID, "/LocalAI/Old": old, "/Projects/Y": y}))

		got, err := svc.OwnGrants(ctx, marta, old)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Members).To(Equal([]sharedfolders.Member{{Principal: "u-sara", Name: "Sara", Mode: workspace.ModeWrite}}))
		_, err = svc.OwnSetMember(ctx, marta, old, "u-sara", workspace.ModeRead)
		Expect(err).To(MatchError(sharedfolders.ErrNotShareable), "a managed folder keeps no grant: it can only be revoked")
		_, err = svc.OwnSetMember(ctx, marta, old, "u-root", workspace.ModeRead)
		Expect(err).To(MatchError(sharedfolders.ErrNotShareable))

		for _, r := range []struct {
			id        int64
			principal string
		}{{y, "u-root"}, {old, "u-sara"}, {chats.ID, "u-sara"}, {skillsDir, sharedfolders.Everyone}} {
			_, err := svc.OwnRemoveMember(ctx, marta, r.id, r.principal)
			Expect(err).NotTo(HaveOccurred(), r.principal)
		}
		Expect(put("u-root", "/users/u-marta/Projects/Y/a.txt", "x")).To(MatchError(workspace.ErrNotFound))
		Expect(put("u-sara", "/users/u-marta/LocalAI/Old/a.txt", "x")).To(MatchError(workspace.ErrNotFound))
		Expect(read("u-sara", "/users/u-marta/Chats")).To(MatchError(workspace.ErrNotFound))
		owned, err = svc.Owned(ctx, marta)
		Expect(err).NotTo(HaveOccurred())
		Expect(owned).To(BeEmpty())
		_, err = svc.OwnGrants(ctx, marta, old)
		Expect(err).To(MatchError(sharedfolders.ErrNotShareable), "without members a managed folder is not shareable again")
		Expect(gone.list()).To(ConsistOf("u-root", "u-sara", "u-sara", sharedfolders.Everyone))
	})

	It("refuses to move a shared folder into LocalAI/ or Chats/, so the grant never leaves the owner's reach", func() {
		id := mkdir("u-marta", "/users/u-marta/Projects")
		_, err := svc.OwnSetMember(ctx, marta, id, "u-sara", workspace.ModeWrite)
		Expect(err).NotTo(HaveOccurred())
		for _, to := range []string{"/users/u-marta/LocalAI/Projects", "/users/u-marta/Chats/Projects"} {
			_, err := f.Store().Move(ctx, f.Actor("u-marta"), "/users/u-marta/Projects", to)
			Expect(err).To(MatchError(workspace.ErrSharedMove), to)
		}
		owned, err := svc.Owned(ctx, marta)
		Expect(err).NotTo(HaveOccurred())
		Expect(owned).To(ConsistOf(HaveField("Path", "/Projects")))
		_, err = svc.OwnRemoveMember(ctx, marta, id, "u-sara")
		Expect(err).NotTo(HaveOccurred())
		_, err = f.Store().Move(ctx, f.Actor("u-marta"), "/users/u-marta/Projects", "/users/u-marta/LocalAI/Projects")
		Expect(err).NotTo(HaveOccurred(), "once it is not shared, it moves")
	})

	It("does not run a page that a grantee wrote as the owner's own page", func() {
		admin := workspace.Actor{Principal: files.AdminPrincipal}
		Expect(put("u-marta", "/users/u-marta/pages/mine/index.html", "<p>mine</p>")).To(Succeed())
		Expect(put("u-marta", "/users/u-marta/pages/notes/index.html", "<p>notes</p>")).To(Succeed())
		mkdir("u-marta", "/users/u-marta/pages/evil")
		// pages/ cannot be shared any more; a grant from before must still
		// not make a grantee's page the owner's.
		Expect(f.Store().Grant(ctx, admin, "u-sara", "/users/u-marta/pages/evil", workspace.ModeWrite)).To(Succeed())
		Expect(f.Store().Grant(ctx, admin, "u-sara", "/users/u-marta/pages/notes", workspace.ModeRead)).To(Succeed())
		Expect(put("u-sara", "/users/u-marta/pages/evil/index.html", "<script>/* grantee code */</script>")).To(Succeed())

		ps := pages.New(f)
		list, err := ps.List(ctx, "u-marta")
		Expect(err).NotTo(HaveOccurred())
		var names []string
		for _, p := range list {
			names = append(names, p.Name)
		}
		Expect(names).To(Equal([]string{"mine", "notes"}), "a read grant keeps the owner's page")
		_, err = ps.Get(ctx, "u-marta", "evil")
		Expect(err).To(MatchError(pages.ErrNotFound))
		_, _, err = ps.Source(ctx, "u-marta", "evil")
		Expect(err).To(MatchError(pages.ErrNotFound))
		_, err = ps.Read(ctx, "u-marta", "evil", "index.html")
		Expect(err).To(MatchError(pages.ErrNotFound))
		p, err := ps.Get(ctx, "u-marta", "mine")
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Author).To(Equal(pages.AuthorYou))
	})

	It("says sharing is off without an account directory", func() {
		audit, err := sharedfolders.NewGORMAudit(db)
		Expect(err).NotTo(HaveOccurred())
		off := sharedfolders.New(sharedfolders.Options{Files: f, Audit: audit, Users: people})
		Expect(off.SharingAvailable()).To(BeFalse())
		_, err = off.Owned(ctx, marta)
		Expect(err).To(MatchError(sharedfolders.ErrSharingOff))
		Expect(svc.SharingAvailable()).To(BeTrue())
	})

	It("is seen by a second frontend on the same database at once", func() {
		other := newService(filesOn(db))
		id := mkdir("u-marta", "/users/u-marta/Projects")
		_, err := svc.OwnSetMember(ctx, marta, id, "u-sara", workspace.ModeWrite)
		Expect(err).NotTo(HaveOccurred())
		mine, err := other.ForUser(ctx, "u-sara")
		Expect(err).NotTo(HaveOccurred())
		Expect(mine).To(HaveLen(1))
		got, err := other.OwnGrants(ctx, marta, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Members).To(HaveLen(1))
		_, err = other.OwnRemoveMember(ctx, marta, id, "u-sara")
		Expect(err).NotTo(HaveOccurred())
		mine, err = svc.ForUser(ctx, "u-sara")
		Expect(err).NotTo(HaveOccurred())
		Expect(mine).To(BeEmpty(), "the first frontend holds no copy")
	})
})

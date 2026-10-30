package sharedfolders_test

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/pages"
	"github.com/mudler/LocalAI/core/services/studio/sharedfolders"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

func openDB() *gorm.DB {
	db, err := sqlitedb.Open("file:"+filepath.Join(GinkgoT().TempDir(), "ws.db")+"?_busy_timeout=5000&_txlock=immediate", &gorm.Config{})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})
	return db
}

func newFiles() *files.Service {
	blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
	Expect(err).NotTo(HaveOccurred())
	ws, err := gormstore.New(openDB(), blobs, gormstore.Options{Admin: files.AdminPrincipal})
	Expect(err).NotTo(HaveOccurred())
	return files.New(ws)
}

// users is a user database of three people.
type users map[string]sharedfolders.User

func (u users) Find(_ context.Context, ref string) (sharedfolders.User, error) {
	for _, x := range u {
		if x.ID == ref || strings.EqualFold(x.Name, ref) {
			return x, nil
		}
	}
	return sharedfolders.User{}, sharedfolders.ErrUnknownUser
}

var people = users{
	"u-marta": {ID: "u-marta", Name: "Marta"},
	"u-sara":  {ID: "u-sara", Name: "Sara"},
	"u-root":  {ID: "u-root", Name: "Root"},
}

var _ = Describe("shared folders", func() {
	var (
		ctx      context.Context
		f        *files.Service
		consents pages.ConsentStore
		audit    sharedfolders.AuditStore
		svc      *sharedfolders.Service
	)
	put := func(p, body string) {
		GinkgoHelper()
		_, err := f.Store().Put(ctx, f.Actor("u-root"), p, strings.NewReader(body), workspace.PutOptions{Overwrite: true})
		Expect(err).NotTo(HaveOccurred())
	}
	actions := func() []string {
		GinkgoHelper()
		evs, err := svc.Events(ctx, 0)
		Expect(err).NotTo(HaveOccurred())
		out := []string{}
		for _, ev := range slices.Backward(evs) {
			out = append(out, ev.Action+" "+ev.Principal)
		}
		return out
	}

	BeforeEach(func() {
		ctx = context.Background()
		f = newFiles()
		var err error
		consents, err = pages.NewJSONConsents("")
		Expect(err).NotTo(HaveOccurred())
		audit, err = sharedfolders.NewGORMAudit(openDB())
		Expect(err).NotTo(HaveOccurred())
		svc = sharedfolders.New(sharedfolders.Options{Files: f, Consents: consents, Audit: audit, Users: people})
		for _, u := range []string{"u-marta", "u-sara", "u-root"} {
			Expect(f.EnsureHome(ctx, u)).To(Succeed())
		}
	})

	It("creates a folder under /shared with its creator as an editor, and lists it", func() {
		got, err := svc.Create(ctx, "u-root", "  Team docs ")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Name).To(Equal("Team docs"))
		Expect(got.Path).To(Equal("/shared/Team docs"))
		Expect(got.ID).NotTo(BeZero())
		Expect(got.Members).To(Equal([]sharedfolders.Member{{Principal: "u-root", Name: "Root", Mode: workspace.ModeWrite}}))
		_, err = f.Store().Stat(ctx, f.Actor("u-root"), "/shared/Team docs/pages")
		Expect(err).NotTo(HaveOccurred(), "the pages folder is ready")

		_, err = svc.Create(ctx, "u-root", "team DOCS")
		Expect(err).To(MatchError(sharedfolders.ErrExists), "names compare without case")
		for _, bad := range []string{"", " ", "a/b", "..", ".hidden", "-dash", "end.", strings.Repeat("x", 65), "two  spaces", "tab\tname"} {
			_, err = svc.Create(ctx, "u-root", bad)
			Expect(err).To(MatchError(sharedfolders.ErrBadName), "%q", bad)
		}
		list, err := svc.List(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].ID).To(Equal(got.ID))

		byAssistant, err := svc.Create(ctx, "", "Board")
		Expect(err).NotTo(HaveOccurred())
		Expect(byAssistant.Members).To(BeEmpty(), "the assistant is not a member")
		evs, err := svc.Events(ctx, 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(evs[0].Actor).To(Equal(sharedfolders.ActorAssistant))
	})

	It("keeps at most MaxFolders folders", func() {
		for i := range sharedfolders.MaxFolders {
			_, err := f.Store().Mkdir(ctx, workspace.Actor{Principal: files.AdminPrincipal}, "/shared/f"+strings.Repeat("x", i%3)+time.Duration(i).String())
			Expect(err).NotTo(HaveOccurred())
		}
		_, err := svc.Create(ctx, "u-root", "one more")
		Expect(err).To(MatchError(sharedfolders.ErrLimit))
	})

	It("adds, changes and removes members, and a user sees only the folders granted to them", func() {
		team, err := svc.Create(ctx, "u-root", "Team")
		Expect(err).NotTo(HaveOccurred())
		other, err := svc.Create(ctx, "u-root", "Other")
		Expect(err).NotTo(HaveOccurred())
		put("/shared/Team/pages/standup/index.html", "<p>standup</p>")
		put("/shared/Team/pages/empty-dir/note.txt", "not a page")

		got, err := svc.SetMember(ctx, "u-root", team.ID, "Marta", workspace.ModeRead)
		Expect(err).NotTo(HaveOccurred(), "a member by name")
		Expect(got.Members).To(ContainElement(sharedfolders.Member{Principal: "u-marta", Name: "Marta", Mode: workspace.ModeRead}))
		Expect(got.Pages).To(Equal(1))

		mine, err := svc.ForUser(ctx, "u-marta")
		Expect(err).NotTo(HaveOccurred())
		Expect(mine).To(Equal([]sharedfolders.Folder{{ID: team.ID, Name: "Team", Path: "/shared/Team", Pages: 1, Mode: workspace.ModeRead}}))
		_, err = svc.GetForUser(ctx, "u-marta", other.ID)
		Expect(err).To(MatchError(sharedfolders.ErrNotFound), "a folder without a grant is not found")
		_, err = f.Store().Put(ctx, f.Actor("u-marta"), "/shared/Team/x.txt", strings.NewReader("x"), workspace.PutOptions{})
		Expect(err).To(MatchError(workspace.ErrReadOnly), "a reader cannot write")

		_, err = svc.SetMember(ctx, "u-root", team.ID, "u-marta", workspace.ModeWrite)
		Expect(err).NotTo(HaveOccurred())
		mine, _ = svc.ForUser(ctx, "u-marta")
		Expect(mine[0].Mode).To(Equal(workspace.ModeWrite))

		_, err = svc.SetMember(ctx, "u-root", team.ID, "nobody", workspace.ModeRead)
		Expect(err).To(MatchError(sharedfolders.ErrUnknownUser))
		_, err = svc.SetMember(ctx, "u-root", team.ID, files.AdminPrincipal, workspace.ModeRead)
		Expect(err).To(MatchError(sharedfolders.ErrUnknownUser), "the workspace administrator is not a member")
		_, err = svc.SetMember(ctx, "u-root", team.ID, "u-sara", workspace.ModeNone)
		Expect(err).To(MatchError(sharedfolders.ErrBadMode))
		_, err = svc.SetMember(ctx, "u-root", 999999, "u-sara", workspace.ModeRead)
		Expect(err).To(MatchError(sharedfolders.ErrNotFound))

		got, err = svc.RemoveMember(ctx, "u-root", team.ID, "u-marta")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Members).NotTo(ContainElement(HaveField("Principal", "u-marta")))
		mine, _ = svc.ForUser(ctx, "u-marta")
		Expect(mine).To(BeEmpty())

		Expect(actions()).To(Equal([]string{"create ", "grant u-root", "create ", "grant u-root", "grant u-marta", "grant u-marta", "revoke u-marta"}))
	})

	It("shares a folder with everyone, the users who come later too", func() {
		team, err := svc.Create(ctx, "u-root", "Team")
		Expect(err).NotTo(HaveOccurred())
		got, err := svc.SetMember(ctx, "u-root", team.ID, sharedfolders.Everyone, workspace.ModeRead)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Members[0]).To(Equal(sharedfolders.Member{Principal: sharedfolders.Everyone, Name: sharedfolders.Everyone, Mode: workspace.ModeRead}), "everyone first")
		Expect(f.EnsureHome(ctx, "u-late")).To(Succeed())
		mine, err := svc.ForUser(ctx, "u-late")
		Expect(err).NotTo(HaveOccurred())
		Expect(mine).To(HaveLen(1))
		roots, err := svc.Roots(ctx, workspace.Actor{Principal: "u-late", Groups: []string{files.EveryoneGroup}, ChatID: "c1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(roots).To(HaveLen(1))
		Expect(roots[0].Path).To(Equal("/shared/Team"))
		Expect(roots[0].Mode).To(Equal(workspace.ModeRead))
		_, err = svc.RemoveMember(ctx, "u-root", team.ID, sharedfolders.Everyone)
		Expect(err).NotTo(HaveOccurred())
		mine, _ = svc.ForUser(ctx, "u-late")
		Expect(mine).To(BeEmpty())
	})

	It("renames a folder and keeps its members, pages and their consents", func() {
		team, err := svc.Create(ctx, "u-root", "Team")
		Expect(err).NotTo(HaveOccurred())
		_, err = svc.SetMember(ctx, "u-root", team.ID, "u-marta", workspace.ModeRead)
		Expect(err).NotTo(HaveOccurred())
		_, err = svc.Create(ctx, "u-root", "Taken")
		Expect(err).NotTo(HaveOccurred())
		got, err := svc.Rename(ctx, "u-root", team.ID, "Crew")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.ID).To(Equal(team.ID))
		Expect(got.Path).To(Equal("/shared/Crew"))
		Expect(got.Members).To(HaveLen(2))
		_, err = svc.Rename(ctx, "u-root", team.ID, "taken")
		Expect(err).To(MatchError(sharedfolders.ErrExists))
		_, err = svc.Rename(ctx, "u-root", team.ID, "a/b")
		Expect(err).To(MatchError(sharedfolders.ErrBadName))
	})

	It("removes a folder with a tombstone, drops the consents to its pages, and restores it with its members", func() {
		team, err := svc.Create(ctx, "u-root", "Team")
		Expect(err).NotTo(HaveOccurred())
		_, err = svc.SetMember(ctx, "u-root", team.ID, "u-marta", workspace.ModeRead)
		Expect(err).NotTo(HaveOccurred())
		put("/shared/Team/pages/standup/index.html", "<p>standup</p>")
		page, err := f.Store().Stat(ctx, f.Actor("u-root"), "/shared/Team/pages/standup")
		Expect(err).NotTo(HaveOccurred())
		at := time.Now().UTC()
		Expect(consents.Put(ctx, pages.Consent{Principal: "u-marta", PageID: page.ID, Folder: "/shared/Team", Name: "standup", Hash: "h", Kind: pages.ConsentAlways, At: at},
			pages.ConsentEvent{Principal: "u-marta", PageID: page.ID, Action: "allow", At: at})).To(Succeed())

		removed, rev, err := svc.Delete(ctx, "u-root", team.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(removed.Name).To(Equal("Team"))
		Expect(rev).NotTo(BeZero())
		list, _ := svc.List(ctx)
		Expect(list).To(BeEmpty())
		mine, _ := svc.ForUser(ctx, "u-marta")
		Expect(mine).To(BeEmpty())
		_, ok, _ := consents.Always(ctx, "u-marta", page.ID)
		Expect(ok).To(BeFalse(), "the consent to the page is gone")
		_, err = svc.Get(ctx, team.ID)
		Expect(err).To(MatchError(sharedfolders.ErrNotFound))

		// Only the removal of a shared folder can be restored here.
		other, err := f.Store().Put(ctx, f.Actor("u-marta"), "/users/u-marta/n.txt", strings.NewReader("n"), workspace.PutOptions{})
		Expect(err).NotTo(HaveOccurred())
		_, err = svc.Restore(ctx, "u-root", other.Rev)
		Expect(err).To(MatchError(sharedfolders.ErrBadRestore))
		_, err = svc.Restore(ctx, "u-root", 0)
		Expect(err).To(MatchError(sharedfolders.ErrBadRestore))

		back, err := svc.Restore(ctx, "u-root", rev)
		Expect(err).NotTo(HaveOccurred())
		Expect(back.ID).To(Equal(team.ID))
		Expect(back.Pages).To(Equal(1))
		Expect(back.Members).To(ContainElement(HaveField("Principal", "u-marta")), "the grants come back")
		mine, _ = svc.ForUser(ctx, "u-marta")
		Expect(mine).To(HaveLen(1))
		_, ok, _ = consents.Always(ctx, "u-marta", page.ID)
		Expect(ok).To(BeFalse(), "a restored page asks again")

		_, rev, err = svc.Delete(ctx, "u-root", team.ID)
		Expect(err).NotTo(HaveOccurred())
		_, err = svc.Create(ctx, "u-root", "Team")
		Expect(err).NotTo(HaveOccurred())
		_, err = svc.Restore(ctx, "u-root", rev)
		Expect(err).To(MatchError(sharedfolders.ErrConflict), "the name is taken again")
	})

	It("says so when the files are off", func() {
		off := sharedfolders.New(sharedfolders.Options{Files: files.Disabled("off")})
		_, err := off.List(ctx)
		Expect(err).To(MatchError(sharedfolders.ErrUnavailable))
		_, err = off.Create(ctx, "u-root", "Team")
		Expect(err).To(MatchError(sharedfolders.ErrUnavailable))
		roots, err := off.Roots(ctx, workspace.Actor{Principal: "u-root"})
		Expect(err).NotTo(HaveOccurred())
		Expect(roots).To(BeEmpty())
	})
})

var _ = Describe("audit stores", func() {
	ctx := context.Background()
	for name, open := range map[string]func() sharedfolders.AuditStore{
		"gorm": func() sharedfolders.AuditStore {
			s, err := sharedfolders.NewGORMAudit(openDB())
			Expect(err).NotTo(HaveOccurred())
			return s
		},
		"file": func() sharedfolders.AuditStore {
			s, err := sharedfolders.NewJSONAudit(GinkgoT().TempDir())
			Expect(err).NotTo(HaveOccurred())
			return s
		},
	} {
		It(name+": returns the newest lines first", func() {
			s := open()
			at := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
			for _, a := range []string{"create", "grant", "revoke"} {
				Expect(s.Add(ctx, sharedfolders.Event{Actor: "u-root", Action: a, FolderID: 3, Folder: "/shared/Team", Principal: "u-marta", Mode: workspace.ModeRead, At: at})).To(Succeed())
			}
			evs, err := s.Events(ctx, 2)
			Expect(err).NotTo(HaveOccurred())
			Expect(evs).To(HaveLen(2))
			Expect(evs[0].Action).To(Equal("revoke"))
			Expect(evs[1]).To(Equal(sharedfolders.Event{Actor: "u-root", Action: "grant", FolderID: 3, Folder: "/shared/Team", Principal: "u-marta", Mode: workspace.ModeRead, At: at}))
		})
	}

	It("file: the audit outlives the process", func() {
		dir := GinkgoT().TempDir()
		s, err := sharedfolders.NewJSONAudit(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Add(ctx, sharedfolders.Event{Action: "create"})).To(Succeed())
		again, err := sharedfolders.NewJSONAudit(dir)
		Expect(err).NotTo(HaveOccurred())
		evs, _ := again.Events(ctx, 10)
		Expect(evs).To(HaveLen(1))
	})
})

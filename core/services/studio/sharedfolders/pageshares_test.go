// SPDX-License-Identifier: MIT
package sharedfolders_test

import (
	"context"
	"path/filepath"
	"strings"

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

var _ = Describe("pages an owner shares from their home", func() {
	var (
		ctx      context.Context
		db       *gorm.DB
		f        *files.Service
		svc      *sharedfolders.Service
		consents pages.ConsentStore
		on       bool
		marta    workspace.Actor
	)
	put := func(who, p, body string) error {
		_, err := f.Store().Put(ctx, f.Actor(who), p, strings.NewReader(body), workspace.PutOptions{Overwrite: true})
		return err
	}
	BeforeEach(func() {
		ctx = context.Background()
		db = openDB()
		blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
		Expect(err).NotTo(HaveOccurred())
		ws, err := gormstore.New(db, blobs, gormstore.Options{Admin: files.AdminPrincipal})
		Expect(err).NotTo(HaveOccurred())
		f = files.New(ws)
		consents, err = pages.NewJSONConsents("")
		Expect(err).NotTo(HaveOccurred())
		audit, err := sharedfolders.NewGORMAudit(db)
		Expect(err).NotTo(HaveOccurred())
		on = true
		svc = sharedfolders.New(sharedfolders.Options{Files: f, Audit: audit, Users: people, Consents: consents,
			Directory: directory{users: people, off: map[string]bool{}}, PageShares: func() bool { return on }})
		for _, u := range []string{"u-marta", "u-sara", "u-root"} {
			Expect(f.EnsureHome(ctx, u)).To(Succeed())
		}
		marta = files.UserActor("u-marta")
		Expect(put("u-marta", "/users/u-marta/pages/todo/index.html", "<p>todo</p>")).To(Succeed())
		Expect(put("u-sara", "/users/u-sara/pages/hers/index.html", "<p>hers</p>")).To(Succeed())
		Expect(put("u-marta", "/users/u-marta/pages/bare/notes.txt", "no index")).To(Succeed())
		Expect(put("u-marta", "/users/u-marta/pages/todo/data/a.json", "[]")).To(Succeed())
	})

	It("shares exactly one page of the owner's home, with the page mark", func() {
		got, err := svc.PageSetMember(ctx, marta, "todo", "u-sara", workspace.ModeData)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Members).To(ConsistOf(HaveField("Mode", workspace.ModeData)))
		list, err := pages.New(f).ListShared(ctx, "u-sara")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(ConsistOf(And(HaveField("Name", "todo"), HaveField("UserShared", true), HaveField("Role", pages.RoleUse), HaveField("OwnerID", "u-marta"))))

		for _, name := range []string{"", "pages", "nope", "hers", "bare", "todo/data", "data", "../todo", "index.html", "TODO"} {
			_, err := svc.PageSetMember(ctx, marta, name, "u-root", workspace.ModeRead)
			Expect(err).To(MatchError(sharedfolders.ErrNotShareable), "name %q", name)
			if name == "bare" {
				continue // a folder of pages/ without an index.html keeps its grants listed, so they can be taken back
			}
			_, err = svc.PageGrants(ctx, marta, name)
			Expect(err).To(MatchError(sharedfolders.ErrNotShareable), "grants of %q", name)
		}
		for _, who := range []workspace.Actor{{Principal: "u-marta", ChatID: "7"}, {Principal: files.AdminPrincipal}, {}, files.UserActor("u-marta").Fenced(workspace.FenceRoot{Node: 1, Mode: workspace.ModeWrite})} {
			_, err := svc.PageSetMember(ctx, who, "todo", "u-root", workspace.ModeRead)
			Expect(err).To(MatchError(sharedfolders.ErrNotShareable), "a chat, an administrator actor, no actor and a fence share nothing")
		}
		_, err = svc.PageSetMember(ctx, files.UserActor("u-sara"), "todo", "u-root", workspace.ModeRead)
		Expect(err).To(MatchError(sharedfolders.ErrNotShareable), "another person's page")
		_, err = f.Store().Stat(ctx, f.Actor("u-root"), "/users/u-marta/pages/todo/index.html")
		Expect(err).To(MatchError(workspace.ErrNotFound))
		_, err = f.Store().Stat(ctx, f.Actor("u-sara"), "/users/u-marta/pages/bare/notes.txt")
		Expect(err).To(MatchError(workspace.ErrNotFound), "only the one page")
	})

	It("refuses the folder routes the same folder, and a general share gives no page", func() {
		n, err := f.Store().Stat(ctx, marta, "/users/u-marta/pages/todo")
		Expect(err).NotTo(HaveOccurred())
		for _, m := range []workspace.Mode{workspace.ModeRead, workspace.ModeData, workspace.ModeWrite} {
			_, err = svc.OwnSetMember(ctx, marta, n.ID, "u-sara", m)
			Expect(err).To(HaveOccurred(), "the folder routes do not share a page, mode %v", m)
		}
		_, err = f.Store().Mkdir(ctx, marta, "/users/u-marta/Site")
		Expect(err).NotTo(HaveOccurred())
		Expect(put("u-marta", "/users/u-marta/Site/pages/home/index.html", "<p>x</p>")).To(Succeed())
		site, err := svc.OwnFolderAt(ctx, marta, "/Site")
		Expect(err).NotTo(HaveOccurred())
		_, err = svc.OwnSetMember(ctx, marta, site.ID, "u-sara", workspace.ModeRead)
		Expect(err).NotTo(HaveOccurred())
		list, err := pages.New(f).ListShared(ctx, "u-sara")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(BeEmpty())
		_, err = svc.PageSetMember(ctx, marta, "todo", "u-sara", workspace.ModeRead)
		Expect(err).NotTo(HaveOccurred())
		list, _ = pages.New(f).ListShared(ctx, "u-sara")
		Expect(list).To(ConsistOf(HaveField("Name", "todo")))
	})

	It("gives everyone view and use, and edit to named accounts", func() {
		_, err := svc.PageSetMember(ctx, marta, "todo", sharedfolders.Everyone, workspace.ModeWrite)
		Expect(err).To(MatchError(sharedfolders.ErrEveryoneEdit))
		for _, m := range []workspace.Mode{workspace.ModeRead, workspace.ModeData} {
			_, err = svc.PageSetMember(ctx, marta, "todo", sharedfolders.Everyone, m)
			Expect(err).NotTo(HaveOccurred())
		}
		_, err = svc.PageSetMember(ctx, marta, "todo", "u-sara", workspace.ModeWrite)
		Expect(err).NotTo(HaveOccurred())
		_, err = svc.PageSetMember(ctx, marta, "todo", "u-sara", workspace.ModeNone)
		Expect(err).To(MatchError(sharedfolders.ErrBadMode))
		_, err = svc.PageSetMember(ctx, marta, "todo", "u-marta", workspace.ModeRead)
		Expect(err).To(MatchError(sharedfolders.ErrUnknownUser), "the owner")
	})

	It("counts the shares of each page and drops a consent with the grant", func() {
		_, err := svc.PageSetMember(ctx, marta, "todo", "u-sara", workspace.ModeData)
		Expect(err).NotTo(HaveOccurred())
		_, err = svc.PageSetMember(ctx, marta, "todo", sharedfolders.Everyone, workspace.ModeRead)
		Expect(err).NotTo(HaveOccurred())
		counts, err := svc.PageShareCounts(ctx, marta)
		Expect(err).NotTo(HaveOccurred())
		Expect(counts).To(Equal(map[string]int{"todo": 2}))

		n, _ := f.Store().Stat(ctx, marta, "/users/u-marta/pages/todo")
		Expect(consents.Put(ctx, pages.Consent{Principal: "u-sara", PageID: n.ID, Hash: "h", Kind: pages.ConsentAlways}, pages.ConsentEvent{Principal: "u-sara", PageID: n.ID, Action: "allow"})).To(Succeed())
		Expect(consents.Put(ctx, pages.Consent{Principal: "u-root", PageID: n.ID, Hash: "h", Kind: pages.ConsentAlways}, pages.ConsentEvent{Principal: "u-root", PageID: n.ID, Action: "allow"})).To(Succeed())
		_, err = svc.PageRemoveMember(ctx, marta, "todo", "u-sara")
		Expect(err).NotTo(HaveOccurred())
		_, ok, _ := consents.Always(ctx, "u-sara", n.ID)
		Expect(ok).To(BeFalse(), "the consent left with the grant")
		_, ok, _ = consents.Always(ctx, "u-root", n.ID)
		Expect(ok).To(BeTrue(), "another account keeps its consent")
		_, err = svc.PageRemoveMember(ctx, marta, "todo", sharedfolders.Everyone)
		Expect(err).NotTo(HaveOccurred())
		_, ok, _ = consents.Always(ctx, "u-root", n.ID)
		Expect(ok).To(BeFalse(), "taking back the grant to everyone drops every consent")
	})

	It("follows a rename of the page, and the Trash and a restore", func() {
		_, err := svc.PageSetMember(ctx, marta, "todo", "u-sara", workspace.ModeData)
		Expect(err).NotTo(HaveOccurred())
		before, err := pages.New(f).ListShared(ctx, "u-sara")
		Expect(err).NotTo(HaveOccurred())
		Expect(before).To(HaveLen(1))
		_, err = f.Store().Move(ctx, marta, "/users/u-marta/pages/todo", "/users/u-marta/pages/tasks")
		Expect(err).NotTo(HaveOccurred(), "a page keeps its grants when it is renamed")
		after, err := pages.New(f).ListShared(ctx, "u-sara")
		Expect(err).NotTo(HaveOccurred())
		Expect(after).To(ConsistOf(And(HaveField("Name", "tasks"), HaveField("PageID", before[0].PageID), HaveField("Role", pages.RoleUse))))
		_, err = f.Store().Move(ctx, marta, "/users/u-marta/pages/tasks", "/users/u-marta/LocalAI/tasks")
		Expect(err).To(MatchError(workspace.ErrSharedMove), "not into a folder that LocalAI manages")

		tx, err := f.Store().Begin(ctx, marta, "Delete the page")
		Expect(err).NotTo(HaveOccurred())
		Expect(tx.Delete(ctx, "/users/u-marta/pages/tasks")).To(Succeed())
		cs, err := tx.Commit()
		Expect(err).NotTo(HaveOccurred())
		gone, err := pages.New(f).ListShared(ctx, "u-sara")
		Expect(err).NotTo(HaveOccurred())
		Expect(gone).To(BeEmpty(), "nobody opens a page in the Trash")
		_, _, err = f.Store().Undo(ctx, marta, cs.Rev, workspace.UndoOptions{})
		Expect(err).NotTo(HaveOccurred())
		back, err := pages.New(f).ListShared(ctx, "u-sara")
		Expect(err).NotTo(HaveOccurred())
		Expect(back).To(HaveLen(1), "a restore shares it again")
	})

	It("takes a revoke at the next call of a second frontend on the same database", func() {
		blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs2"))
		Expect(err).NotTo(HaveOccurred())
		ws2, err := gormstore.New(db, blobs, gormstore.Options{Admin: files.AdminPrincipal})
		Expect(err).NotTo(HaveOccurred())
		other := pages.New(files.New(ws2))
		_, err = svc.PageSetMember(ctx, marta, "todo", "u-sara", workspace.ModeData)
		Expect(err).NotTo(HaveOccurred())
		list, err := other.ListShared(ctx, "u-sara")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		_, err = other.WriteShared(ctx, "u-sara", list[0].FolderID, "todo", "data/b.json", []byte("{}"), 0)
		Expect(err).NotTo(HaveOccurred(), "the grantee writes data on the other frontend")
		_, err = other.WriteShared(ctx, "u-sara", list[0].FolderID, "todo", "app.js", []byte("x"), 0)
		Expect(err).To(MatchError(pages.ErrReadOnly))
		_, err = svc.PageSetMember(ctx, marta, "todo", "u-sara", workspace.ModeRead)
		Expect(err).NotTo(HaveOccurred())
		_, err = other.WriteShared(ctx, "u-sara", list[0].FolderID, "todo", "data/c.json", []byte("{}"), 0)
		Expect(err).To(MatchError(pages.ErrFolderReadOnly), "a lowered role holds at once")
		_, err = svc.PageRemoveMember(ctx, marta, "todo", "u-sara")
		Expect(err).NotTo(HaveOccurred())
		_, err = other.WriteShared(ctx, "u-sara", list[0].FolderID, "todo", "data/d.json", []byte("{}"), 0)
		Expect(err).To(MatchError(pages.ErrNotFound), "a removed grant holds at once")
	})

	It("refuses a new share and the picker while page sharing is off, and still takes one back", func() {
		_, err := svc.PageSetMember(ctx, marta, "todo", "u-sara", workspace.ModeRead)
		Expect(err).NotTo(HaveOccurred())
		on = false
		_, err = svc.PageSetMember(ctx, marta, "todo", "u-root", workspace.ModeRead)
		Expect(err).To(MatchError(sharedfolders.ErrPageSharesOff))
		_, err = svc.PageSearchAccounts(ctx, marta, "sar", 5)
		Expect(err).To(MatchError(sharedfolders.ErrPageSharesOff))
		_, err = svc.PageGrants(ctx, marta, "todo")
		Expect(err).NotTo(HaveOccurred())
		list, err := pages.New(f, pages.WithUserShares(func() bool { return on })).ListShared(ctx, "u-sara")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(ConsistOf(And(HaveField("TurnedOff", true), HaveField("Hash", ""))))
		_, err = pages.New(f, pages.WithUserShares(func() bool { return on })).GetShared(ctx, "u-sara", list[0].FolderID, "todo")
		Expect(err).To(MatchError(pages.ErrUserSharesOff))
		_, err = svc.PageRemoveMember(ctx, marta, "todo", "u-sara")
		Expect(err).NotTo(HaveOccurred())
		on = true
		people, err := svc.PageSearchAccounts(ctx, marta, "sar", 5)
		Expect(err).NotTo(HaveOccurred())
		Expect(people).To(ConsistOf(HaveField("ID", "u-sara")))
	})
})

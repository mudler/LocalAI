package pages_test

import (
	"context"
	"io"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/pages"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The shared folder of these specs: /shared/team, written by luca, granted to
// marta (read) and sara (write); bob has no grant. luca's page "standup" and a
// second page "copy" with the very same code.
const team = "/shared/team"

type sharedRig struct {
	f     *files.Service
	svc   *pages.Service
	trust *pages.Trust
	clock time.Time
	team  int64
}

func adminPut(f *files.Service, p, body string) {
	_, err := f.Store().Put(context.Background(), workspace.Actor{Principal: "luca"}, p, strings.NewReader(body), workspace.PutOptions{Overwrite: true})
	Expect(err).NotTo(HaveOccurred())
}

func newSharedRig() *sharedRig {
	ctx := context.Background()
	f := newFiles()
	admin := workspace.Actor{Principal: files.AdminPrincipal}
	for _, u := range []string{"luca", "marta", "sara", "bob"} {
		Expect(f.EnsureHome(ctx, u)).To(Succeed())
	}
	_, err := f.Store().Mkdir(ctx, admin, team+"/pages")
	Expect(err).NotTo(HaveOccurred())
	_, err = f.Store().Mkdir(ctx, admin, "/shared/private")
	Expect(err).NotTo(HaveOccurred())
	Expect(f.Store().Grant(ctx, admin, "luca", team, workspace.ModeWrite)).To(Succeed())
	Expect(f.Store().Grant(ctx, admin, "marta", team, workspace.ModeRead)).To(Succeed())
	Expect(f.Store().Grant(ctx, admin, "sara", team, workspace.ModeWrite)).To(Succeed())
	adminPut(f, team+"/pages/standup/index.html", "<p>standup</p>\n<script>1</script>")
	adminPut(f, team+"/pages/standup/page.json", `{"title":"Team standup"}`)
	adminPut(f, team+"/pages/standup/data.json", `{"n":1}`)
	adminPut(f, team+"/pages/copy/index.html", "<p>standup</p>\n<script>1</script>")
	adminPut(f, team+"/pages/copy/page.json", `{"title":"Team standup"}`)
	adminPut(f, team+"/secret.txt", "team secret")
	_, err = f.Store().Put(ctx, admin, "/shared/private/x.txt", strings.NewReader("private"), workspace.PutOptions{})
	Expect(err).NotTo(HaveOccurred())
	put(f, "marta", "/notes.txt", "marta's notes")
	r := &sharedRig{f: f, clock: time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)}
	r.svc = pages.New(f)
	store, err := pages.NewJSONConsents("")
	Expect(err).NotTo(HaveOccurred())
	r.trust = pages.NewTrust(r.svc, store).WithNow(func() time.Time { return r.clock })
	folders, err := r.svc.SharedFolders(ctx, "marta")
	Expect(err).NotTo(HaveOccurred())
	Expect(folders).To(HaveLen(1))
	r.team = folders[0].ID
	return r
}

var _ = Describe("shared pages", func() {
	ctx := context.Background()

	It("lists the pages of the viewer's shared folders with their author and folder, and nothing else", func() {
		r := newSharedRig()
		put(r.f, "marta", "/pages/own/index.html", "<p>mine</p>")
		list, err := r.svc.ListShared(ctx, "marta")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(2))
		p := list[1]
		Expect(p.Name).To(Equal("standup"))
		Expect(p.Title).To(Equal("Team standup"))
		Expect(p.Source).To(Equal(pages.SourceShared))
		Expect(p.Folder).To(Equal(team))
		Expect(p.FolderID).To(Equal(r.team))
		Expect(p.AuthorID).To(Equal("luca"))
		Expect(p.Writable).To(BeFalse(), "marta has a read grant")
		Expect(p.Hash).To(HaveLen(64))
		Expect(p.PageID).NotTo(BeZero())
		own, err := r.svc.List(ctx, "marta")
		Expect(err).NotTo(HaveOccurred())
		Expect(own).To(HaveLen(1), "a shared page is not an own page")

		folders, _ := r.svc.SharedFolders(ctx, "sara")
		Expect(folders).To(HaveLen(1))
		Expect(folders[0].Writable).To(BeTrue())
	})

	It("a viewer without a grant sees nothing and gets not found", func() {
		r := newSharedRig()
		list, err := r.svc.ListShared(ctx, "bob")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(BeEmpty())
		_, err = r.svc.GetShared(ctx, "bob", r.team, "standup")
		Expect(err).To(MatchError(pages.ErrNotFound))
		_, err = r.svc.ReadShared(ctx, "bob", r.team, "standup", "data.json")
		Expect(err).To(MatchError(pages.ErrNotFound))
		Expect(r.trust.Check(ctx, "bob", r.team, "standup", "x", "")).To(MatchError(pages.ErrNotFound))
		// Another user's home is not a shared folder: its node ID is not found.
		home, _ := r.f.Store().Stat(ctx, r.f.Actor("marta"), "/users/marta")
		_, err = r.svc.GetShared(ctx, "bob", home.ID, "own")
		Expect(err).To(MatchError(pages.ErrNotFound))
		// A shared folder of someone else is not found for marta either.
		priv, _ := r.f.Store().Stat(ctx, workspace.Actor{Principal: files.AdminPrincipal}, "/shared/private")
		_, err = r.svc.GetShared(ctx, "marta", priv.ID, "x")
		Expect(err).To(MatchError(pages.ErrNotFound))
	})

	It("the bridge of a shared page reaches its own folder only, with traversal and case-fold aliases refused", func() {
		r := newSharedRig()
		c, err := r.svc.ReadShared(ctx, "marta", r.team, "standup", "data.json")
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Content).To(Equal(`{"n":1}`))
		for _, bad := range []string{"../secret.txt", "../../secret.txt", "../../../users/marta/notes.txt", "/shared/team/secret.txt",
			"a/../../secret.txt", "./data.json", `..\secret.txt`, "data.json/..", ""} {
			_, err := r.svc.ReadShared(ctx, "marta", r.team, "standup", bad)
			Expect(err).To(HaveOccurred(), "read %q", bad)
			Expect(err.Error()).NotTo(ContainSubstring("secret"), "read %q", bad)
			_, err = r.svc.WriteShared(ctx, "sara", r.team, "standup", bad, []byte("x"), 0)
			Expect(err).To(HaveOccurred(), "write %q", bad)
		}
		// The bridge never decodes a path: "..%2f" is a name inside the folder.
		f, err := r.svc.WriteShared(ctx, "sara", r.team, "standup", "..%2fsecret.txt", []byte("x"), 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(f.Path).To(Equal("..%2fsecret.txt"))
		got, _, err := r.f.Store().Open(ctx, workspace.Actor{Principal: "luca"}, team+"/secret.txt")
		Expect(err).NotTo(HaveOccurred())
		buf := new(strings.Builder)
		_, _ = io.Copy(buf, got)
		got.Close()
		Expect(buf.String()).To(Equal("team secret"))
		for _, bad := range []string{"..", "Standup", "STANDUP", "standup/../copy", "../team"} {
			_, err := r.svc.ReadShared(ctx, "marta", r.team, bad, "data.json")
			Expect(err).To(MatchError(pages.ErrBadName), "page %q", bad)
		}
		// A case-fold alias of the page's own code is still its code.
		for _, alias := range []string{"INDEX.HTML", "Index.html", "PAGE.JSON", "index.html"} {
			_, err := r.svc.WriteShared(ctx, "sara", r.team, "standup", alias, []byte("<p>pwned</p>"), 0)
			Expect(err).To(MatchError(pages.ErrReadOnly), "write %q", alias)
		}
		list, err := r.svc.ListSharedFiles(ctx, "marta", r.team, "standup", "")
		Expect(err).NotTo(HaveOccurred())
		for _, f := range list {
			Expect(f.Path).NotTo(ContainSubstring("/"), "a listing names a path outside the folder: %s", f.Path)
		}
		_, err = r.svc.ListSharedFiles(ctx, "marta", r.team, "standup", "..")
		Expect(err).To(MatchError(pages.ErrBadPath))
	})

	It("a viewer with a read grant cannot write, one with a write grant can", func() {
		r := newSharedRig()
		_, err := r.svc.WriteShared(ctx, "marta", r.team, "standup", "data.json", []byte(`{"n":2}`), 0)
		Expect(err).To(MatchError(pages.ErrFolderReadOnly))
		f, err := r.svc.WriteShared(ctx, "sara", r.team, "standup", "data.json", []byte(`{"n":2}`), 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(f.Path).To(Equal("data.json"))
		_, err = r.svc.WriteShared(ctx, "sara", r.team, "standup", "new/deep.json", []byte(`{}`), 0)
		Expect(err).NotTo(HaveOccurred(), "missing folders are made inside the page folder")
	})

	It("a consent holds for one version of one page, and a change asks again", func() {
		r := newSharedRig()
		p, err := r.svc.GetShared(ctx, "marta", r.team, "standup")
		Expect(err).NotTo(HaveOccurred())
		rev, err := r.trust.State(ctx, "marta", p)
		Expect(err).NotTo(HaveOccurred())
		Expect(rev.State).To(Equal(pages.StateNone))
		Expect(r.trust.Check(ctx, "marta", r.team, "standup", p.Hash, "")).To(MatchError(pages.ErrNoConsent))

		_, err = r.trust.Allow(ctx, "marta", r.team, "standup", "0000", false)
		Expect(err).To(MatchError(pages.ErrChanged), "a consent to a hash that is not the page's")
		c, err := r.trust.Allow(ctx, "marta", r.team, "standup", p.Hash, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Lines).To(Equal(2))
		Expect(r.trust.Check(ctx, "marta", r.team, "standup", p.Hash, "")).To(Succeed())
		rev, _ = r.trust.State(ctx, "marta", p)
		Expect(rev.State).To(Equal(pages.StateAllowed))

		// The same code in another page: the consent does not move with it.
		cp, _ := r.svc.GetShared(ctx, "marta", r.team, "copy")
		Expect(cp.Hash).To(Equal(p.Hash), "the specs need two pages with the same code")
		Expect(r.trust.Check(ctx, "marta", r.team, "copy", p.Hash, "")).To(MatchError(pages.ErrNoConsent))
		rev, _ = r.trust.State(ctx, "marta", cp)
		Expect(rev.State).To(Equal(pages.StateNone))
		// Nor to another viewer.
		Expect(r.trust.Check(ctx, "sara", r.team, "standup", p.Hash, "")).To(MatchError(pages.ErrNoConsent))

		// The author changes the page: the open frame keeps its version; a new
		// open is asked again.
		adminPut(r.f, team+"/pages/standup/index.html", "<p>standup v2</p>\n<script>2</script>\n")
		p2, _ := r.svc.GetShared(ctx, "marta", r.team, "standup")
		Expect(p2.Hash).NotTo(Equal(p.Hash))
		rev, _ = r.trust.State(ctx, "marta", p2)
		Expect(rev.State).To(Equal(pages.StateChanged))
		Expect(rev.Allowed.Size).To(Equal(p.Size))
		Expect(r.trust.Check(ctx, "marta", r.team, "standup", p.Hash, "")).To(Succeed(), "the version that runs keeps running")
		Expect(r.trust.Check(ctx, "marta", r.team, "standup", p2.Hash, "")).To(MatchError(pages.ErrNoConsent))
		// A data file the page writes does not change its hash.
		adminPut(r.f, team+"/pages/standup/data.json", `{"n":9}`)
		p3, _ := r.svc.GetShared(ctx, "marta", r.team, "standup")
		Expect(p3.Hash).To(Equal(p2.Hash))
		// page.json does.
		adminPut(r.f, team+"/pages/standup/page.json", `{"title":"Pay here"}`)
		p4, _ := r.svc.GetShared(ctx, "marta", r.team, "standup")
		Expect(p4.Hash).NotTo(Equal(p2.Hash))
	})

	It("a revoked consent stops an open frame at its next call, and every step is in the audit", func() {
		r := newSharedRig()
		p, _ := r.svc.GetShared(ctx, "marta", r.team, "standup")
		_, err := r.trust.Allow(ctx, "marta", r.team, "standup", p.Hash, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.trust.Check(ctx, "marta", r.team, "standup", p.Hash, "")).To(Succeed())
		Expect(r.trust.Revoke(ctx, "marta", r.team, "standup")).To(Succeed())
		Expect(r.trust.Check(ctx, "marta", r.team, "standup", p.Hash, "")).To(MatchError(pages.ErrNoConsent))
		evs, err := r.trust.Events(ctx, "marta", 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(evs).To(HaveLen(2))
		Expect(evs[0].Action).To(Equal("revoke"))
		Expect(evs[1].Action).To(Equal("allow"))
		Expect(evs[1].Hash).To(Equal(p.Hash))
		Expect(evs[1].Folder).To(Equal(team))
	})

	It("allow once runs one visit by its token, and ends", func() {
		r := newSharedRig()
		p, _ := r.svc.GetShared(ctx, "marta", r.team, "standup")
		c, err := r.trust.Allow(ctx, "marta", r.team, "standup", p.Hash, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.Token).To(HaveLen(32))
		Expect(r.trust.Check(ctx, "marta", r.team, "standup", p.Hash, c.Token)).To(Succeed())
		Expect(r.trust.Check(ctx, "marta", r.team, "standup", p.Hash, "")).To(MatchError(pages.ErrNoConsent), "a visit is not a lasting consent")
		rev, _ := r.trust.State(ctx, "marta", p)
		Expect(rev.State).To(Equal(pages.StateNone), "allow once leaves the page not allowed")
		Expect(r.trust.Check(ctx, "marta", r.team, "copy", p.Hash, c.Token)).To(MatchError(pages.ErrNoConsent), "the token of one page for another")
		Expect(r.trust.Check(ctx, "sara", r.team, "standup", p.Hash, c.Token)).To(MatchError(pages.ErrNoConsent), "another viewer's token")
		r.clock = r.clock.Add(pages.OnceTTL + time.Minute)
		Expect(r.trust.Check(ctx, "marta", r.team, "standup", p.Hash, c.Token)).To(MatchError(pages.ErrNoConsent), "an old visit")
		evs, _ := r.trust.Events(ctx, "marta", 10)
		Expect(evs[0].Action).To(Equal("allow_once"))
	})

	It("a revoked grant closes the page even with a consent", func() {
		r := newSharedRig()
		p, _ := r.svc.GetShared(ctx, "marta", r.team, "standup")
		_, err := r.trust.Allow(ctx, "marta", r.team, "standup", p.Hash, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.f.Store().Revoke(ctx, workspace.Actor{Principal: files.AdminPrincipal}, "marta", team)).To(Succeed())
		Expect(r.trust.Check(ctx, "marta", r.team, "standup", p.Hash, "")).To(MatchError(pages.ErrNotFound))
		_, err = r.svc.ReadShared(ctx, "marta", r.team, "standup", "data.json")
		Expect(err).To(MatchError(pages.ErrNotFound))
	})
})

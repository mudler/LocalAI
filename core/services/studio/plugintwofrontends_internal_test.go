package studio

import (
	"context"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/plugins"
	pstore "github.com/mudler/LocalAI/core/services/plugins/store"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

// Two frontends: two chats services and two Plugins services on one chat
// store, one workspace and one database. Nothing about plugins is kept in
// the memory of a frontend, so a change through one is in the next turn of
// the other.
var _ = Describe("plugins with two frontends", func() {
	It("shows an enable and a chat switch made through one frontend in the turn of the other", func() {
		ctx := context.Background()
		a, f := newSkillService(&skillBroker{})
		b, err := New(Deps{Store: a.d.Store, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Files: f})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = b.Close(ctx) })

		db, err := sqlitedb.Open("file:"+filepath.Join(GinkgoT().TempDir(), "plugins.db")+"?_busy_timeout=5000&_txlock=immediate", &gorm.Config{})
		Expect(err).NotTo(HaveOccurred())
		var services []*plugins.Service
		var st pstore.Store
		for range 2 {
			s, err := pstore.NewGormStore(db)
			Expect(err).NotTo(HaveOccurred())
			st = s
			ps, err := plugins.New(plugins.Options{Store: s, DB: db, Fetcher: noFetcher{}, Root: files.PluginsPath})
			Expect(err).NotTo(HaveOccurred())
			services = append(services, ps)
		}
		a.d.Plugins, b.d.Plugins = services[0], services[1]
		px := &pluginFixture{svc: services[0], st: st, f: f}
		id, _ := px.install("in-fmt", plugins.Model{Name: "fmt", Version: "1", Skills: []plugins.SkillRef{
			{Name: "notes", Description: "Take notes.", Dir: "skills/notes", UserInvocable: true},
		}}, "", map[string]string{"skills/notes/SKILL.md": "---\nname: notes\ndescription: Take notes.\n---\nBody.\n"})

		c, err := a.Create(ctx, "alice", CreateInput{Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		seen := func(svc *Service) bool {
			rec, err := svc.d.Store.GetChat(ctx, "alice", c.ID)
			Expect(err).NotTo(HaveOccurred())
			sc := svc.scopeOf(ctx, rec)
			list, _, _, err := svc.chatSkillSet(ctx, "alice", rec, svc.skillsOffOf(ctx, rec, sc), rec.AddonsOff, sc, Commands{})
			Expect(err).NotTo(HaveOccurred())
			_, ok := skills.Find(list, "fmt:notes")
			return ok
		}
		Expect(seen(b)).To(BeFalse())
		Expect(services[0].SetEnabled(ctx, plugins.Actor{ID: "alice"}, id, true)).To(Succeed())
		Expect(seen(b)).To(BeTrue(), "an enable through frontend A is in the turn of frontend B")
		Expect(a.SetPluginsOff(ctx, "alice", c.ID, []string{id})).To(Succeed())
		Expect(seen(b)).To(BeFalse(), "a chat switch through frontend A is in the turn of frontend B")
		Expect(b.SetPluginsOff(ctx, "alice", c.ID, nil)).To(Succeed())
		Expect(seen(a)).To(BeTrue())
		rc, _, err := f.Store().Open(ctx, f.Actor("alice"), files.PluginsPath+"/"+strings.TrimPrefix(mustDigest(st, id), "sha256:")+"/skills/notes/SKILL.md")
		Expect(err).NotTo(HaveOccurred(), "the package is in the shared workspace")
		_ = rc.Close()
	})
})

func mustDigest(st pstore.Store, id string) string {
	list, err := st.Installs(context.Background(), pstore.InstallFilter{ID: id})
	Expect(err).NotTo(HaveOccurred())
	Expect(list).To(HaveLen(1))
	return list[0].Digest
}

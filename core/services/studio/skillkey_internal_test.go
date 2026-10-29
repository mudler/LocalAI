package studio

import (
	"context"
	"path/filepath"
	"strings"

	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The refresh key tells the sandbox to refresh. A switch only takes a line out
// of the index, so it must leave the key alone; a skill that is new or changed
// must change it.
var _ = Describe("skillkey internal", func() {
	It("the refresh key moves only when a skill file does", func() {
		dsn := "file:" + filepath.Join(GinkgoT().TempDir(), "ws.db") + "?_busy_timeout=5000&_txlock=immediate"
		db, err := sqlitedb.Open(dsn, &gorm.Config{})
		Expect(err).NotTo(HaveOccurred())
		blobs, _ := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
		ws, err := gormstore.New(db, blobs, gormstore.Options{Admin: files.AdminPrincipal})
		Expect(err).NotTo(HaveOccurred())
		f := files.New(ws)
		st, _ := store.NewJSON("")
		svc, err := New(Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Files: f})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_ = svc.Close(context.Background())
			if raw, err := db.DB(); err == nil {
				_ = raw.Close()
			}
		})
		ctx := context.Background()
		Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
		put := func(name, desc string) {
			abs, _ := f.Abs("alice", "/.skills/"+name+"/SKILL.md")
			body := "---\nname: " + name + "\ndescription: " + desc + "\n---\nBody.\n"
			_, err := ws.Put(ctx, f.Actor("alice"), abs, strings.NewReader(body), workspace.PutOptions{Overwrite: true})
			Expect(err).NotTo(HaveOccurred())
		}
		put("review", "v1")
		put("notes", "v1")

		_, _, base := svc.skillTurn(ctx, "alice", nil, nil)
		Expect(base).NotTo(Equal(""), "no key for two skills")
		_, _, k := svc.skillTurn(ctx, "alice", []string{"review"}, nil)
		Expect(k).To(Equal(base), "switching a skill off changed the key: %q != %q", k, base)
		put("review", "v2")
		_, _, k = svc.skillTurn(ctx, "alice", nil, nil)
		Expect(k).NotTo(Equal(base), "a changed SKILL.md left the key as it was")
		put("extra", "new")
		_, _, again := svc.skillTurn(ctx, "alice", nil, nil)
		Expect(again).NotTo(Equal(base), "a new skill left the key as it was")
	})

	// A switch with a plain name turns off the skill that wins, so the one below it
	// shows through; "layer:name" turns off that layer's skill only.
	It("a skill switch turns off the winner or the named layer", func() {
		dsn := "file:" + filepath.Join(GinkgoT().TempDir(), "ws.db") + "?_busy_timeout=5000&_txlock=immediate"
		db, err := sqlitedb.Open(dsn, &gorm.Config{})
		Expect(err).NotTo(HaveOccurred())
		blobs, _ := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
		ws, err := gormstore.New(db, blobs, gormstore.Options{Admin: files.AdminPrincipal})
		Expect(err).NotTo(HaveOccurred())
		f := files.New(ws)
		st, _ := store.NewJSON("")
		svc, err := New(Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Files: f})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_ = svc.Close(context.Background())
			if raw, err := db.DB(); err == nil {
				_ = raw.Close()
			}
		})
		ctx := context.Background()
		Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
		Expect(f.EnsureShared(ctx)).To(Succeed())
		Expect(f.WriteSkill(ctx, "alice", skills.LayerPersonal, "report", "Personal report.", "Steps.")).To(Succeed())
		Expect(f.WriteSkill(ctx, files.AdminPrincipal, skills.LayerShared, "report", "Shared report.", "Steps.")).To(Succeed())
		index := func(off ...string) string {
			p, _, _ := svc.skillTurn(ctx, "alice", off, nil)
			return p
		}
		cases := []struct {
			off         []string
			personal    bool
			shared      bool
			explanation string
		}{
			{nil, true, false, "the nearest skill wins"},
			{[]string{"report"}, false, true, "a plain name turns off the winner and uncovers the one below"},
			{[]string{"personal:report"}, false, true, "the personal skill off"},
			{[]string{"shared:report"}, true, false, "the shared skill is covered anyway"},
			{[]string{"personal:report", "shared:report"}, false, false, "both layers off"},
			{[]string{"report", "report"}, false, true, "a name twice is still one switch"},
		}
		for _, c := range cases {
			got := index(c.off...)
			Expect(strings.Contains(got, "Personal report.")).To(Equal(c.personal))
			Expect(strings.Contains(got, "Shared report.")).To(Equal(c.shared))
		}
		for _, bad := range []string{"nolayer:report", "personal:", ":report", "personal:bad name", "a:b:c"} {
			Expect(svc.SetSkillsOff(ctx, "alice", "nope", []string{bad})).To(MatchError(ErrBadSkillName))
		}
	})
})

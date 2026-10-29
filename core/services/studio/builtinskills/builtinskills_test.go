package builtinskills_test

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/builtinskills"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

var names = []string{"docx", "xlsx", "pptx", "pdf", "writing-skills"}

var _ = Describe("the built-in skills", func() {
	It("parses every built-in skill", func() {
		fsys := builtinskills.FS()
		for _, n := range names {
			data, err := fs.ReadFile(fsys, n+"/SKILL.md")
			Expect(err).NotTo(HaveOccurred(), n)
			name, desc, err := skills.Parse(data)
			Expect(err).NotTo(HaveOccurred(), n)
			Expect(name).To(Equal(n))
			Expect(strings.TrimSpace(desc)).NotTo(BeEmpty(), n)
		}
	})

	It("keeps the source and the licence in the notice", func() {
		data, err := fs.ReadFile(builtinskills.FS(), "NOTICE")
		Expect(err).NotTo(HaveOccurred())
		for _, want := range []string{"paper-instruments/feather-train", "b7463092af862ebbdf4c5920c4bc9ab38ab17ad9", "MIT License", "Permission is hereby granted"} {
			Expect(string(data)).To(ContainSubstring(want), "the NOTICE lacks %q", want)
		}
	})

	It("lets no internal name leak into the tree", func() {
		err := fs.WalkDir(builtinskills.FS(), ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := fs.ReadFile(builtinskills.FS(), p)
			if err != nil {
				return err
			}
			for _, bad := range []string{"aioffice", "localstudio", "LocalStudio", "/home/mudler"} {
				Expect(string(data)).NotTo(ContainSubstring(bad), "%s mentions %q", p, bad)
			}
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
	})

	It("resolves the skills for a user once they are synced into the built-in layer", func() {
		ctx := context.Background()
		db, err := sqlitedb.Open("file:"+filepath.Join(GinkgoT().TempDir(), "ws.db")+"?_busy_timeout=5000&_txlock=immediate", &gorm.Config{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			if raw, err := db.DB(); err == nil {
				_ = raw.Close()
			}
		})
		blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
		Expect(err).NotTo(HaveOccurred())
		ws, err := gormstore.New(db, blobs, gormstore.Options{Admin: files.AdminPrincipal})
		Expect(err).NotTo(HaveOccurred())
		f := files.New(ws)
		Expect(f.SyncBuiltin(ctx, builtinskills.FS())).To(Succeed())
		Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
		got, _, err := skills.Resolve(ctx, ws, f.Actor("alice"), f.SkillRoots("alice"), skills.Options{})
		Expect(err).NotTo(HaveOccurred())
		have := map[string]bool{}
		for _, s := range got {
			if s.Layer == skills.LayerBuiltin {
				have[s.Name] = true
			}
		}
		for _, n := range names {
			Expect(have).To(HaveKey(n), "the built-in skill %s is not found by a user", n)
		}
	})
})

package builtinskills_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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

var names = []string{"docx", "xlsx", "pptx", "pdf", "writing-skills", "localai-workspace", "localai-apps"}

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

	It("marks the office skills as needing command execution, and only them", func() {
		for _, n := range names {
			data, err := fs.ReadFile(builtinskills.FS(), n+"/SKILL.md")
			Expect(err).NotTo(HaveOccurred(), n)
			switch n {
			case "docx", "xlsx", "pptx", "pdf":
				Expect(skills.Requires(data)).To(Equal([]string{skills.RequireExec}), n)
			default:
				Expect(skills.Requires(data)).To(BeEmpty(), n)
			}
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

// The calls that the Pages bridge offers to a page, from
// core/http/react-ui/src/utils/pagesBridge.js (the object api in BOOTSTRAP).
// The test below reads that file, so a change of the bridge fails here.
var bridgeCalls = []string{"files.read", "files.write", "files.list", "chat.send", "on", "page"}

var apiRE = regexp.MustCompile(`localai\.([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)?)`)

var _ = Describe("the localai-apps skill", func() {
	var skill string

	BeforeEach(func() {
		data, err := fs.ReadFile(builtinskills.FS(), "localai-apps/SKILL.md")
		Expect(err).NotTo(HaveOccurred())
		skill = string(data)
	})

	It("has a description that triggers on what a user calls an app", func() {
		_, desc, err := skills.Parse([]byte(skill))
		Expect(err).NotTo(HaveOccurred())
		for _, w := range []string{"app", "tool", "tracker", "dashboard", "form", "game", "calculator", "planner", "Page"} {
			Expect(desc).To(ContainSubstring(w))
		}
	})

	It("stays short and has no marketing", func() {
		Expect(strings.Count(skill, "\n")).To(BeNumerically("<", 250))
	})

	It("leaves the personalisation skill about personalisation and points to the apps skill", func() {
		data, err := fs.ReadFile(builtinskills.FS(), "localai-workspace/SKILL.md")
		Expect(err).NotTo(HaveOccurred())
		_, desc, err := skills.Parse(data)
		Expect(err).NotTo(HaveOccurred())
		Expect(desc).To(ContainSubstring("localai-apps"))
		Expect(desc).NotTo(ContainSubstring("pages,"))
	})

	It("uses only calls that the Pages bridge has", func() {
		src, err := os.ReadFile(filepath.Join("..", "..", "..", "http", "react-ui", "src", "utils", "pagesBridge.js"))
		Expect(err).NotTo(HaveOccurred())
		bridge := string(src)
		// Every call in the list is defined in the bridge source.
		for _, c := range bridgeCalls {
			switch c {
			case "on", "page":
				Expect(bridge).To(MatchRegexp(`\b`+c+`:`), "the bridge has no %q", c)
			default:
				parent, name, _ := strings.Cut(c, ".")
				Expect(bridge).To(MatchRegexp(`\b`+name+`: function`), "the bridge has no %q", c)
				Expect(bridge).To(ContainSubstring(parent+": Object.freeze"), "the bridge has no %q", c)
			}
		}
		used := apiRE.FindAllStringSubmatch(skill, -1)
		Expect(used).NotTo(BeEmpty())
		for _, m := range used {
			Expect(bridgeCalls).To(ContainElement(m[1]), "the skill uses localai.%s, which the bridge does not have", m[1])
		}
		for _, ev := range regexp.MustCompile(`localai\.on\('([^']+)'`).FindAllStringSubmatch(skill, -1) {
			Expect(bridge).To(ContainSubstring("'" + ev[1] + "': []"))
		}
	})

	It("keeps the example page inside the sandbox policy", func() {
		for _, bad := range []string{"fetch(", "XMLHttpRequest", "WebSocket(", "localStorage.", "<form", "https://", "http://", "innerHTML"} {
			// The rules section names them; the example must not use them.
			example := skill[strings.Index(skill, "## Example"):]
			example = example[:strings.Index(example, "## After you write it")]
			Expect(example).NotTo(ContainSubstring(bad), "the example uses %q", bad)
		}
	})
})

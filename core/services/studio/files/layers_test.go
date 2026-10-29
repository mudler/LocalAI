package files_test

import (
	"context"
	"errors"
	"strings"
	"testing/fstest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func skillMD(name, desc string) string {
	return "---\nname: " + name + "\ndescription: " + desc + "\n---\nBody of " + name + ".\n"
}

func adminPut(f *files.Service, p, content string) {
	_, err := f.Store().Put(context.Background(), workspace.Actor{Principal: files.AdminPrincipal}, p, strings.NewReader(content), workspace.PutOptions{Overwrite: true})
	Expect(err).NotTo(HaveOccurred(), "admin put %s", p)
}

func userPut(f *files.Service, user, rel, content string) {
	abs, _ := f.Abs(user, rel)
	_, err := f.Store().Put(context.Background(), f.Actor(user), abs, strings.NewReader(content), workspace.PutOptions{Overwrite: true})
	Expect(err).NotTo(HaveOccurred(), "%s put %s", user, rel)
}

func resolve(f *files.Service, user string) map[string]skills.Skill {
	list, _, err := skills.Resolve(context.Background(), f.Store(), f.Actor(user), f.SkillRoots(user), skills.Options{})
	Expect(err).NotTo(HaveOccurred())
	out := map[string]skills.Skill{}
	for _, s := range list {
		if s.Shadowed == "" {
			out[s.Name] = s
		}
	}
	return out
}

var _ = Describe("skill layers", func() {
	It("shows a user the skills of all four layers and lets the nearest win", func() {
		ctx := context.Background()
		f := newFiles()
		Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
		adminPut(f, "/builtin-skills/review/SKILL.md", skillMD("review", "built-in review"))
		adminPut(f, "/builtin-skills/pdfs/SKILL.md", skillMD("pdfs", "built-in pdfs"))
		adminPut(f, "/skills/review/SKILL.md", skillMD("review", "shared review"))
		adminPut(f, "/skills/house-style/SKILL.md", skillMD("house-style", "shared style"))
		userPut(f, "alice", "/.skills/notes/SKILL.md", skillMD("notes", "personal notes"))
		userPut(f, "alice", "/Projects/Atlas/.skills/atlas/SKILL.md", skillMD("atlas", "folder skill"))
		userPut(f, "alice", "/.skills/pdfs/SKILL.md", skillMD("pdfs", "personal pdfs"))

		got := resolve(f, "alice")
		want := map[string]skills.Layer{
			"review": skills.LayerShared, "house-style": skills.LayerShared, "notes": skills.LayerPersonal,
			"atlas": skills.LayerFolder, "pdfs": skills.LayerPersonal,
		}
		Expect(got).To(HaveLen(len(want)), "skills = %v", got)
		for name, layer := range want {
			Expect(got[name].Layer).To(Equal(layer), "skill %s comes from the wrong layer (nearest wins)", name)
		}
		Expect(got["notes"].GuestPath).To(HavePrefix(session.GuestRoot+"/"+session.GuestHome+"/.skills/notes"), "a personal skill's guest path")
		Expect(got["house-style"].GuestPath).To(HavePrefix(session.GuestRoot+"/shared-skills/"), "a shared skill's guest path")
	})

	It("keeps personal skills personal and shared ones read-only", func() {
		ctx := context.Background()
		f := newFiles()
		for _, u := range []string{"alice", "bob"} {
			Expect(f.EnsureHome(ctx, u)).To(Succeed())
		}
		userPut(f, "alice", "/.skills/secret/SKILL.md", skillMD("secret", "alice only"))
		adminPut(f, "/skills/team/SKILL.md", skillMD("team", "for everyone"))
		Expect(resolve(f, "bob")).NotTo(HaveKey("secret"), "bob sees alice's personal skill")
		Expect(resolve(f, "bob")).To(HaveKey("team"), "bob does not see the shared skill")
		for _, p := range []string{"/skills/evil/SKILL.md", "/builtin-skills/evil/SKILL.md", "/skills/team/SKILL.md"} {
			_, err := f.Store().Put(ctx, f.Actor("bob"), p, strings.NewReader("x"), workspace.PutOptions{Overwrite: true})
			Expect(err).To(HaveOccurred(), "bob wrote %s", p)
			Expect(errors.Is(err, workspace.ErrReadOnly) || errors.Is(err, workspace.ErrNotFound)).To(BeTrue(), "bob writing %s = %v, want a refusal", p, err)
		}
	})

	It("copies built-in skills once and updates them when they change", func() {
		ctx := context.Background()
		f := newFiles()
		tree := fstest.MapFS{"summarize/SKILL.md": {Data: []byte(skillMD("summarize", "v1"))}, "summarize/references/tips.md": {Data: []byte("tips")}}
		Expect(f.SyncBuiltin(ctx, tree)).To(Succeed())
		Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
		got := resolve(f, "alice")["summarize"]
		Expect(got.Description).To(Equal("v1"))
		Expect(got.Layer).To(Equal(skills.LayerBuiltin))
		before, _ := f.Store().History(ctx, workspace.Actor{Principal: files.AdminPrincipal}, workspace.HistoryQuery{Limit: 1000})
		Expect(f.SyncBuiltin(ctx, tree)).To(Succeed())
		after, _ := f.Store().History(ctx, workspace.Actor{Principal: files.AdminPrincipal}, workspace.HistoryQuery{Limit: 1000})
		Expect(after).To(HaveLen(len(before)), "a second sync of the same tree made changes")
		tree["summarize/SKILL.md"] = &fstest.MapFile{Data: []byte(skillMD("summarize", "v2"))}
		Expect(f.SyncBuiltin(ctx, tree)).To(Succeed())
		Expect(resolve(f, "alice")["summarize"].Description).To(Equal("v2"), "after the tree changed, built-in skill should be v2")
	})

	It("drops a built-in skill from the built-in layer when it leaves the tree", func() {
		ctx := context.Background()
		f := newFiles()
		tree := fstest.MapFS{
			"keep/SKILL.md":            {Data: []byte(skillMD("keep", "stays"))},
			"gone/SKILL.md":            {Data: []byte(skillMD("gone", "removed later"))},
			"gone/references/notes.md": {Data: []byte("notes")},
			"keep/references/a.md":     {Data: []byte("a")},
		}
		Expect(f.SyncBuiltin(ctx, tree)).To(Succeed())
		Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
		Expect(resolve(f, "alice")).To(HaveKey("gone"), "the skill is not there before it leaves the tree")
		delete(tree, "gone/SKILL.md")
		delete(tree, "gone/references/notes.md")
		delete(tree, "keep/references/a.md")
		Expect(f.SyncBuiltin(ctx, tree)).To(Succeed())
		got := resolve(f, "alice")
		Expect(got).NotTo(HaveKey("gone"), "a skill that left the tree is still in the built-in layer")
		Expect(got["keep"].Description).To(Equal("stays"))
		admin := workspace.Actor{Principal: files.AdminPrincipal}
		for _, p := range []string{files.BuiltinSkillsPath + "/gone", files.BuiltinSkillsPath + "/keep/references/a.md"} {
			_, err := f.Store().Stat(ctx, admin, p)
			Expect(err).To(HaveOccurred(), "%s is still there", p)
		}
		_, err := f.Store().Stat(ctx, admin, files.BuiltinSkillsPath+"/keep/SKILL.md")
		Expect(err).NotTo(HaveOccurred(), "keep/SKILL.md")
	})
})

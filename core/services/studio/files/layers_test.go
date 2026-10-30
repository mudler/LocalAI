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

var _ = Describe("the add-on skill layer", func() {
	admin := workspace.Actor{Principal: files.AdminPrincipal}
	resolveWith := func(f *files.Service, user string, roots ...skills.Root) map[string]skills.Skill {
		list, _, err := skills.Resolve(context.Background(), f.Store(), f.Actor(user), f.SkillRoots(user, roots...), skills.Options{})
		Expect(err).NotTo(HaveOccurred())
		out := map[string]skills.Skill{}
		for _, s := range list {
			if s.Shadowed == "" && s.Invalid == "" {
				out[s.Name] = s
			}
		}
		return out
	}

	It("copies an add-on's skills, offers them only with the add-on's root, and shows the add-on in the guest path", func() {
		ctx := context.Background()
		f := newFiles()
		Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
		tree := fstest.MapFS{
			"notes/SKILL.md":              {Data: []byte(skillMD("notes", "notes of the add-on"))},
			"notes/references/how.md":     {Data: []byte("how")},
			"notes-extra/SKILL.md":        {Data: []byte(skillMD("notes-extra", "more notes"))},
			"notes-extra/references/a.md": {Data: []byte("a")},
		}
		Expect(f.SyncAddonSkills(ctx, "notes", tree)).To(Succeed())
		Expect(resolveWith(f, "alice")).NotTo(HaveKey("notes"), "no root: an add-on that is not usable offers nothing")
		got := resolveWith(f, "alice", files.AddonSkillsRoot("notes", "Notes"))
		Expect(got).To(HaveKey("notes-extra"))
		sk := got["notes"]
		Expect(sk.Layer).To(Equal(skills.LayerAddon))
		Expect(sk.Addon).To(Equal("notes"))
		Expect(sk.AddonTitle).To(Equal("Notes"))
		Expect(sk.GuestPath).To(Equal(session.GuestRoot + "/shared-addon-skills/notes/notes/SKILL.md"))
		Expect(files.SharedGuestPath(sk.GuestPath)).To(Equal(files.AddonSkillsPath + "/notes/notes/SKILL.md"))
	})

	It("is idempotent, drops a file the package no longer has and leaves another add-on alone", func() {
		ctx := context.Background()
		f := newFiles()
		other := fstest.MapFS{"other/SKILL.md": {Data: []byte(skillMD("other", "other add-on"))}}
		Expect(f.SyncAddonSkills(ctx, "other", other)).To(Succeed())
		tree := fstest.MapFS{
			"notes/SKILL.md":          {Data: []byte(skillMD("notes", "v1"))},
			"notes/references/old.md": {Data: []byte("old")},
		}
		Expect(f.SyncAddonSkills(ctx, "notes", tree)).To(Succeed())
		before, _ := f.Store().History(ctx, admin, workspace.HistoryQuery{Limit: 1000})
		Expect(f.SyncAddonSkills(ctx, "notes", tree)).To(Succeed())
		after, _ := f.Store().History(ctx, admin, workspace.HistoryQuery{Limit: 1000})
		Expect(after).To(HaveLen(len(before)), "a second sync of the same tree made changes")

		delete(tree, "notes/references/old.md")
		Expect(f.SyncAddonSkills(ctx, "notes", tree)).To(Succeed())
		_, err := f.Store().Stat(ctx, admin, files.AddonSkillsPath+"/notes/notes/references/old.md")
		Expect(err).To(HaveOccurred(), "a dropped file is still there")
		_, err = f.Store().Stat(ctx, admin, files.AddonSkillsPath+"/other/other/SKILL.md")
		Expect(err).NotTo(HaveOccurred(), "another add-on's skill was touched")

		ids, err := f.AddonSkillIDs(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(ids).To(ConsistOf("notes", "other"))
		Expect(f.RemoveAddonSkills(ctx, "notes")).To(Succeed())
		ids, _ = f.AddonSkillIDs(ctx)
		Expect(ids).To(ConsistOf("other"), "uninstall removes the add-on's folder only")
		before, _ = f.Store().History(ctx, admin, workspace.HistoryQuery{Limit: 1000})
		Expect(f.RemoveAddonSkills(ctx, "notes")).To(Succeed(), "removing twice is not an error")
		after, _ = f.Store().History(ctx, admin, workspace.HistoryQuery{Limit: 1000})
		Expect(after).To(HaveLen(len(before)), "removing nothing made a change")
	})

	It("is read-only for users, and below every layer a person writes", func() {
		ctx := context.Background()
		f := newFiles()
		Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
		Expect(f.SyncAddonSkills(ctx, "notes", fstest.MapFS{
			"notes/SKILL.md":      {Data: []byte(skillMD("notes", "add-on"))},
			"notes-docs/SKILL.md": {Data: []byte(skillMD("notes-docs", "add-on docs"))},
		})).To(Succeed())
		adminPut(f, "/builtin-skills/notes-docs/SKILL.md", skillMD("notes-docs", "built-in"))
		userPut(f, "alice", "/.skills/notes/SKILL.md", skillMD("notes", "mine"))
		got := resolveWith(f, "alice", files.AddonSkillsRoot("notes", "Notes"))
		Expect(got["notes"].Layer).To(Equal(skills.LayerPersonal), "a person's own skill beats the add-on's")
		Expect(got["notes-docs"].Layer).To(Equal(skills.LayerBuiltin), "an add-on never takes over a built-in skill that is not replaceable")
		_, err := f.Store().Put(ctx, f.Actor("alice"), files.AddonSkillsPath+"/notes/notes/SKILL.md", strings.NewReader("x"), workspace.PutOptions{Overwrite: true})
		Expect(err).To(HaveOccurred(), "a user wrote the add-on layer")
		Expect(f.SyncAddonSkills(ctx, "Bad/Id", fstest.MapFS{})).NotTo(Succeed())
	})
})

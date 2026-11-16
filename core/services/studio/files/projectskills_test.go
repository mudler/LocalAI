package files_test

import (
	"context"
	"errors"
	"io"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/workfiles"
)

// resolveWith resolves the skills of user with the extra roots, through the
// guarded call the host uses, and returns the winners by name.
func resolveWith(f *files.Service, user string, extra ...skills.Root) map[string]skills.Skill {
	GinkgoHelper()
	list, _, err := f.ResolveSkills(context.Background(), user, f.SkillRoots(user, extra...), skills.Options{})
	Expect(err).NotTo(HaveOccurred())
	out := map[string]skills.Skill{}
	for _, s := range list {
		if s.Shadowed == "" && s.Invalid == "" {
			out[s.Name] = s
		}
	}
	return out
}

var _ = Describe("the project skills layer", func() {
	var (
		ctx   context.Context
		f     *files.Service
		admin = workspace.Actor{Principal: files.AdminPrincipal}
	)
	BeforeEach(func() {
		ctx = context.Background()
		f = newFiles()
		Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
		Expect(f.EnsureHome(ctx, "bob")).To(Succeed())
	})

	It("is a root only for a safe project folder name, and its path is rebuilt", func() {
		forged := skills.Root{Layer: skills.LayerProject, Project: "atlas", Path: "/users/bob/.skills", Guest: "/x"}
		roots := f.SkillRoots("alice", forged, files.ProjectSkillsRoot("alice", "../x"), skills.Root{Layer: skills.LayerProject, Project: "a/b"})
		var got []skills.Root
		for _, r := range roots {
			if r.Layer == skills.LayerProject {
				got = append(got, r)
			}
		}
		Expect(got).To(HaveLen(1))
		Expect(got[0].Path).To(Equal("/users/alice/Projects/atlas/skills"))
		Expect(got[0].Project).To(Equal("atlas"))
	})

	It("does not load Projects/<folder>/skills or .skills without the root, in any chat", func() {
		userPut(f, "alice", "/Projects/atlas/skills/plan/SKILL.md", skillMD("plan", "project plan"))
		userPut(f, "alice", "/Projects/atlas/.skills/old/SKILL.md", skillMD("old", "old place"))
		userPut(f, "alice", "/Projects/atlas/files/.skills/deep/SKILL.md", skillMD("deep", "deep folder skill"))
		userPut(f, "alice", "/Work/.skills/other/SKILL.md", skillMD("other", "a folder skill elsewhere"))
		got := resolveWith(f, "alice")
		Expect(got).To(HaveKey("other"))
		for _, n := range []string{"plan", "old", "deep"} {
			Expect(got).NotTo(HaveKey(n), "the folder walk entered Projects/")
		}
	})

	It("loads the project's skills with the root, above the personal and agent layers, below a folder skill", func() {
		userPut(f, "alice", "/Projects/atlas/skills/plan/SKILL.md", skillMD("plan", "project plan"))
		userPut(f, "alice", "/Projects/atlas/skills/style/SKILL.md", skillMD("style", "project style"))
		userPut(f, "alice", "/.skills/style/SKILL.md", skillMD("style", "personal style"))
		userPut(f, "alice", "/LocalAI/agents/sales/skills/style/SKILL.md", skillMD("style", "agent style"))
		userPut(f, "alice", "/LocalAI/agents/sales/skills/plan/SKILL.md", skillMD("plan", "agent plan"))
		userPut(f, "alice", "/Work/.skills/plan/SKILL.md", skillMD("plan", "folder plan"))
		got := resolveWith(f, "alice", files.ProjectSkillsRoot("alice", "atlas"), files.AgentSkillsRoot("alice", "sales", "Sales"))
		Expect(got["style"].Layer).To(Equal(skills.LayerProject))
		Expect(got["style"].Project).To(Equal("atlas"))
		Expect(got["style"].GuestPath).To(HaveSuffix("/Projects/atlas/skills/style/SKILL.md"))
		Expect(got["plan"].Layer).To(Equal(skills.LayerFolder), "the nearest folder skill still wins")
	})

	It("leaves the layer out when another person may write the project folder, and shows it again when not", func() {
		userPut(f, "alice", "/Projects/atlas/skills/plan/SKILL.md", skillMD("plan", "project plan"))
		root := files.ProjectSkillsRoot("alice", "atlas")
		Expect(resolveWith(f, "alice", root)).To(HaveKey("plan"))
		blocked, err := f.ProjectSkillsBlocked(ctx, "alice", "atlas")
		Expect(err).NotTo(HaveOccurred())
		Expect(blocked).To(BeFalse())

		Expect(f.Store().Grant(ctx, admin, "bob", "/users/alice/Projects/atlas", workspace.ModeRead)).To(Succeed())
		Expect(resolveWith(f, "alice", root)).To(HaveKey("plan"), "a read grant is not another writer")

		Expect(f.Store().Grant(ctx, admin, "bob", "/users/alice/Projects/atlas", workspace.ModeWrite)).To(Succeed())
		Expect(resolveWith(f, "alice", root)).NotTo(HaveKey("plan"))
		blocked, err = f.ProjectSkillsBlocked(ctx, "alice", "atlas")
		Expect(err).NotTo(HaveOccurred())
		Expect(blocked).To(BeTrue())

		Expect(f.Store().Revoke(ctx, admin, "bob", "/users/alice/Projects/atlas")).To(Succeed())
		Expect(resolveWith(f, "alice", root)).To(HaveKey("plan"))
	})

	It("is blocked by a write grant above the project, too", func() {
		userPut(f, "alice", "/Projects/atlas/skills/plan/SKILL.md", skillMD("plan", "project plan"))
		Expect(f.Store().Grant(ctx, admin, "bob", "/users/alice/Projects", workspace.ModeWrite)).To(Succeed())
		Expect(resolveWith(f, "alice", files.ProjectSkillsRoot("alice", "atlas"))).NotTo(HaveKey("plan"))
	})

	It("writes, reads and deletes a project skill, and refuses every one of them while the folder is blocked", func() {
		Expect(f.WriteProjectSkill(ctx, "alice", "atlas", "plan", "plan the work", "Step one.")).To(Succeed())
		desc, body, err := f.ReadProjectSkill(ctx, "alice", "atlas", "plan")
		Expect(err).NotTo(HaveOccurred())
		Expect(desc).To(Equal("plan the work"))
		Expect(body).To(Equal("Step one.\n"))
		Expect(resolveWith(f, "alice", files.ProjectSkillsRoot("alice", "atlas"))).To(HaveKey("plan"))

		Expect(f.Store().Grant(ctx, admin, "bob", "/users/alice/Projects/atlas", workspace.ModeWrite)).To(Succeed())
		Expect(f.WriteProjectSkill(ctx, "alice", "atlas", "plan", "x", "y")).To(MatchError(files.ErrProjectSkillsBlocked))
		_, _, err = f.ReadProjectSkill(ctx, "alice", "atlas", "plan")
		Expect(err).To(MatchError(files.ErrProjectSkillsBlocked))
		Expect(f.DeleteProjectSkill(ctx, "alice", "atlas", "plan")).To(MatchError(files.ErrProjectSkillsBlocked))

		Expect(f.Store().Revoke(ctx, admin, "bob", "/users/alice/Projects/atlas")).To(Succeed())
		Expect(f.DeleteProjectSkill(ctx, "alice", "atlas", "plan")).To(Succeed())
		_, _, err = f.ReadProjectSkill(ctx, "alice", "atlas", "plan")
		Expect(errors.Is(err, workspace.ErrNotFound)).To(BeTrue(), "%v", err)
	})

	It("refuses unsafe names and keeps one user out of another's project folder", func() {
		Expect(f.WriteProjectSkill(ctx, "alice", "../x", "plan", "d", "b")).To(MatchError(workspace.ErrBadName))
		Expect(f.WriteProjectSkill(ctx, "alice", "atlas", "Bad Name", "d", "b")).To(MatchError(files.ErrBadSkill))
		Expect(f.WriteProjectSkill(ctx, "alice", "atlas", "plan", "d", "b")).To(Succeed())
		_, _, err := f.ReadProjectSkill(ctx, "bob", "atlas", "plan")
		Expect(errors.Is(err, workspace.ErrNotFound)).To(BeTrue(), "bob read alice's project skill: %v", err)
	})

	It("is a managed location: a project's skills folder cannot be shared alone", func() {
		for _, p := range []string{"/Projects/atlas/skills", "/Projects/atlas/skills/plan", "/projects/Atlas/Skills/plan/SKILL.md"} {
			Expect(workfiles.Managed(p)).To(BeTrue(), p)
		}
		for _, p := range []string{"/Projects", "/Projects/atlas", "/Projects/atlas/files", "/Projects/atlas/memory", "/Notes/atlas/skills", "/skills"} {
			Expect(workfiles.Managed(p)).To(BeFalse(), p)
		}
		Expect(files.Managed("/Projects/atlas/skills")).To(Equal(files.ManagedProjectSkills))
		Expect(files.Managed("/Projects/atlas/skills/plan")).To(BeEmpty())
		Expect(files.Managed("/Memory/x/skills")).NotTo(Equal(files.ManagedProjectSkills))
	})
})

var _ = Describe("the copy of the old project .skills folders", func() {
	var (
		ctx   context.Context
		f     *files.Service
		admin = workspace.Actor{Principal: files.AdminPrincipal}
	)
	BeforeEach(func() {
		ctx = context.Background()
		f = newFiles()
		Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
		Expect(f.EnsureHome(ctx, "bob")).To(Succeed())
	})
	read := func(p string) string {
		GinkgoHelper()
		r, _, err := f.Store().Open(ctx, admin, p)
		Expect(err).NotTo(HaveOccurred(), p)
		defer func() { _ = r.Close() }()
		b, err := io.ReadAll(r)
		Expect(err).NotTo(HaveOccurred())
		return string(b)
	}

	It("copies each skill with its files, keeps the source, and does nothing the second time", func() {
		userPut(f, "alice", "/Projects/atlas/.skills/plan/SKILL.md", skillMD("plan", "old plan"))
		userPut(f, "alice", "/Projects/atlas/.skills/plan/references/notes.md", "reference")
		userPut(f, "bob", "/Projects/zed/.skills/zed/SKILL.md", skillMD("zed", "bob's"))
		userPut(f, "alice", "/Projects/atlas/files/readme.md", "not a skill")

		rep, err := f.MigrateProjectSkills(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(rep).To(Equal(files.ProjectSkillsMigration{Copied: 2}))
		Expect(read("/users/alice/Projects/atlas/skills/plan/SKILL.md")).To(ContainSubstring("old plan"))
		Expect(read("/users/alice/Projects/atlas/skills/plan/references/notes.md")).To(Equal("reference"))
		Expect(read("/users/bob/Projects/zed/skills/zed/SKILL.md")).To(ContainSubstring("bob's"))
		Expect(read("/users/alice/Projects/atlas/.skills/plan/SKILL.md")).To(ContainSubstring("old plan"), "the source stays")

		// The owner edits the copy; a second run neither copies nor overwrites.
		userPut(f, "alice", "/Projects/atlas/skills/plan/SKILL.md", skillMD("plan", "edited plan"))
		rep, err = f.MigrateProjectSkills(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(rep.Copied).To(BeZero())
		Expect(rep.Done).To(Equal(2), "a project whose skills folder exists is done")
		Expect(read("/users/alice/Projects/atlas/skills/plan/SKILL.md")).To(ContainSubstring("edited plan"))
		Expect(resolveWith(f, "alice", files.ProjectSkillsRoot("alice", "atlas"))["plan"].Layer).To(Equal(skills.LayerProject))
	})

	It("does not bring back a skill that the owner deleted from the new folder", func() {
		userPut(f, "alice", "/Projects/atlas/.skills/plan/SKILL.md", skillMD("plan", "old plan"))
		userPut(f, "alice", "/Projects/atlas/.skills/keep/SKILL.md", skillMD("keep", "old keep"))
		rep, err := f.MigrateProjectSkills(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(rep.Copied).To(Equal(2))
		Expect(f.DeleteProjectSkill(ctx, "alice", "atlas", "plan")).To(Succeed())
		rep, err = f.MigrateProjectSkills(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(rep.Copied).To(BeZero())
		_, err = f.Store().Stat(ctx, admin, "/users/alice/Projects/atlas/skills/plan")
		Expect(errors.Is(err, workspace.ErrNotFound)).To(BeTrue(), "the deleted skill stays deleted: %v", err)
		// The same when the owner deleted the last one.
		Expect(f.DeleteProjectSkill(ctx, "alice", "atlas", "keep")).To(Succeed())
		rep, err = f.MigrateProjectSkills(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(rep.Copied).To(BeZero())
		_, err = f.Store().Stat(ctx, admin, "/users/alice/Projects/atlas/skills/keep")
		Expect(errors.Is(err, workspace.ErrNotFound)).To(BeTrue(), "%v", err)
	})

	It("does not copy an old folder that another person may write", func() {
		userPut(f, "alice", "/Projects/atlas/.skills/planted/SKILL.md", skillMD("planted", "bob's text"))
		Expect(f.Store().Grant(ctx, admin, "bob", "/users/alice/Projects/atlas", workspace.ModeWrite)).To(Succeed())
		rep, err := f.MigrateProjectSkills(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(rep).To(Equal(files.ProjectSkillsMigration{Blocked: 1}))
		_, err = f.Store().Stat(ctx, admin, "/users/alice/Projects/atlas/skills/planted")
		Expect(errors.Is(err, workspace.ErrNotFound)).To(BeTrue(), "%v", err)
	})

	It("skips a folder without a SKILL.md and a name that is not a skill name", func() {
		userPut(f, "alice", "/Projects/atlas/.skills/empty/notes.txt", "x")
		userPut(f, "alice", "/Projects/atlas/.skills/Bad Name/SKILL.md", skillMD("bad", "d"))
		rep, err := f.MigrateProjectSkills(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(rep.Copied).To(BeZero())
	})
})

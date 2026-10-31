package files_test

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

var _ = Describe("skill files", func() {
	It("writes, reads and deletes a personal skill", func() {
		ctx := context.Background()
		f := newFiles()
		Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
		Expect(f.WriteSkill(ctx, "alice", skills.LayerPersonal, "review", "check a change", "Read the diff.\nThen say what you find.")).To(Succeed())
		desc, body, err := f.ReadSkill(ctx, "alice", skills.LayerPersonal, "review")
		Expect(err).NotTo(HaveOccurred())
		Expect(desc).To(Equal("check a change"))
		Expect(body).To(ContainSubstring("Then say what you find."))
		// What was written is found by the resolver, so the chat will use it.
		got := resolve(f, "alice")["review"]
		Expect(got.Description).To(Equal("check a change"))
		Expect(got.Layer).To(Equal(skills.LayerPersonal))
		// Writing again replaces it.
		Expect(f.WriteSkill(ctx, "alice", skills.LayerPersonal, "review", "check carefully", "New body.")).To(Succeed())
		d, _, _ := f.ReadSkill(ctx, "alice", skills.LayerPersonal, "review")
		Expect(d).To(Equal("check carefully"), "after the second write")
		Expect(f.DeleteSkill(ctx, "alice", skills.LayerPersonal, "review")).To(Succeed())
		_, _, err = f.ReadSkill(ctx, "alice", skills.LayerPersonal, "review")
		Expect(err).To(MatchError(workspace.ErrNotFound), "ReadSkill after the delete")
		trash, _ := f.Trash(ctx, "alice", 10)
		Expect(trash).To(HaveLen(1), "want the deleted skill in the trash")
	})

	It("lets only the admin write the shared skills", func() {
		ctx := context.Background()
		f := newFiles()
		_ = f.EnsureHome(ctx, "alice")
		Expect(f.WriteSkill(ctx, "alice", skills.LayerShared, "team", "for all", "Body.")).To(MatchError(files.ErrNotAdmin), "a user writing a shared skill")
		Expect(f.WriteSkill(ctx, files.AdminPrincipal, skills.LayerShared, "team", "for all", "Body.")).To(Succeed(), "the admin writing a shared skill")
		Expect(resolve(f, "alice")["team"].Layer).To(Equal(skills.LayerShared), "alice should resolve the shared skill")
		d, _, err := f.ReadSkill(ctx, "alice", skills.LayerShared, "team")
		Expect(err).NotTo(HaveOccurred(), "a user reading a shared skill")
		Expect(d).To(Equal("for all"))
		Expect(f.DeleteSkill(ctx, "alice", skills.LayerShared, "team")).To(MatchError(files.ErrNotAdmin), "a user deleting a shared skill")
		Expect(f.WriteSkill(ctx, files.AdminPrincipal, skills.LayerBuiltin, "x", "d", "b")).NotTo(Succeed(), "a built-in skill was written through the API")
	})

	It("needs a valid name and description for a skill", func() {
		ctx := context.Background()
		f := newFiles()
		_ = f.EnsureHome(ctx, "alice")
		for _, name := range []string{"", "Has Space", "../up", "a/b", strings.Repeat("x", 80), "UPPER"} {
			Expect(f.WriteSkill(ctx, "alice", skills.LayerPersonal, name, "d", "b")).To(MatchError(files.ErrBadSkill), "WriteSkill(%q)", name)
		}
		Expect(f.WriteSkill(ctx, "alice", skills.LayerPersonal, "ok", "", "b")).To(MatchError(files.ErrBadSkill), "an empty description")
		// A description cannot smuggle in frontmatter lines.
		Expect(f.WriteSkill(ctx, "alice", skills.LayerPersonal, "tricky", "fine\nname: other\n---\nextra", "b")).To(Succeed())
		got := resolve(f, "alice")
		Expect(got["tricky"].Name).To(Equal("tricky"), "a multi-line description changed the skill: %+v", got)
		Expect(got["other"].Name).To(BeEmpty(), "a multi-line description changed the skill: %+v", got)
	})
})

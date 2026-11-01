package studio

import (
	"context"
	"encoding/json"
	"strings"
	"testing/fstest"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// skillMD is a SKILL.md with a body that names the skill and its layer.
func skillMD(name, desc, body string, extra ...string) []byte {
	return []byte("---\nname: " + name + "\ndescription: " + desc + "\n" + strings.Join(extra, "\n") + "\n---\n" + body + "\n")
}

var _ = Describe("load_skill", func() {
	var (
		ctx  context.Context
		b    *skillBroker
		svc  *Service
		f    *files.Service
		conv string
	)
	put := func(a workspace.Actor, p string, data []byte) {
		_, err := f.Store().Put(ctx, a, p, strings.NewReader(string(data)), workspace.PutOptions{Overwrite: true})
		Expect(err).NotTo(HaveOccurred())
	}
	personal := func(user, name string, data []byte) {
		Expect(f.EnsureHome(ctx, user)).To(Succeed())
		p, err := f.Abs(user, "/.skills/"+name+"/SKILL.md")
		Expect(err).NotTo(HaveOccurred())
		put(f.Actor(user), p, data)
	}
	// load calls the tool as the model of the chat would, with the files and
	// the commands that o gives.
	load := func(name string, o loadOpts) (string, error) {
		args, _ := json.Marshal(map[string]string{"name": name})
		return svc.loadSkillRun(chatActor("alice", conv), o)(ctx, args)
	}
	full := loadOpts{files: true, exec: true}

	BeforeEach(func() {
		ctx = context.Background()
		b = &skillBroker{}
		svc, f = newSkillService(b)
		Expect(f.SyncBuiltin(ctx, fstest.MapFS{
			"pdf/SKILL.md":               {Data: skillMD("pdf", "Built-in PDF.", "Built-in PDF body. See references/qa.md.", "requires: [exec]")},
			"pdf/references/qa.md":       {Data: []byte("QA")},
			"pdf/scripts/check.py":       {Data: []byte("print(1)")},
			"xlsx/SKILL.md":              {Data: skillMD("xlsx", "Built-in sheets.", "Built-in xlsx body.")},
			"writing-skills/SKILL.md":    {Data: skillMD("writing-skills", "Built-in writing.", "Writing body.")},
			"localai-workspace/SKILL.md": {Data: skillMD("localai-workspace", "Workspace.", "Workspace body.")},
		})).To(Succeed())
		Expect(f.SyncAddonSkills(ctx, "notes", fstest.MapFS{
			"notes/SKILL.md": {Data: skillMD("notes", "Use for notes.", "Add-on notes body.")},
		})).To(Succeed())
		Expect(f.SyncAddonSkills(ctx, "office", fstest.MapFS{
			"xlsx/SKILL.md": {Data: skillMD("xlsx", "Office sheets.", "Office xlsx body.")},
		})).To(Succeed())
		b.set(
			AddonSkillSet{AddonID: "notes", Title: "Notes", Skills: []string{"notes"}, Running: true},
			AddonSkillSet{AddonID: "office", Title: "Office", Skills: []string{"xlsx"}, Shadows: []string{"xlsx"}, Running: true},
		)
		personal("alice", "review", skillMD("review", "Check a change.", "Personal review body."))
		Expect(f.EnsureShared(ctx)).To(Succeed())
		put(workspace.Actor{Principal: files.AdminPrincipal}, "/skills/house-style/SKILL.md", skillMD("house-style", "How we write.", "Shared style body."))
		rec, err := svc.Create(ctx, "alice", CreateInput{Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		conv = rec.ID
	})

	It("returns the instructions of a built-in, an add-on, a personal and a shared skill, without the frontmatter", func() {
		for name, want := range map[string]string{
			"writing-skills": "Writing body.",
			"notes":          "Add-on notes body.",
			"review":         "Personal review body.",
			"house-style":    "Shared style body.",
		} {
			got, err := load(name, full)
			Expect(err).NotTo(HaveOccurred(), name)
			Expect(got).To(Equal(want), name)
			Expect(got).NotTo(ContainSubstring("description:"), name)
		}
	})

	It("follows the precedence of the index: a personal skill over a built-in one, an add-on skill over the built-in it replaces", func() {
		got, err := load("xlsx", full)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("Office xlsx body."))

		personal("alice", "pdf", skillMD("pdf", "My PDF.", "Personal PDF body."))
		got, err = load("pdf", full)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("Personal PDF body."), "the personal skill has no other files, so no base directory either")
	})

	It("gives the built-in skill back when the chat switches the add-on's skill off", func() {
		Expect(svc.SetSkillsOff(ctx, "alice", conv, []string{"addon:xlsx"})).To(Succeed())
		got, err := load("xlsx", full)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("Built-in xlsx body."))
	})

	It("treats a skill switched off, or of a stopped add-on, as unknown and lists the skills there are", func() {
		Expect(svc.SetSkillsOff(ctx, "alice", conv, []string{"review"})).To(Succeed())
		_, err := load("review", full)
		Expect(err).To(MatchError(HavePrefix(`unknown skill "review".`)))
		Expect(err.Error()).To(ContainSubstring("The skills of this chat are: "))
		Expect(err.Error()).To(ContainSubstring("house-style"))
		Expect(err.Error()).NotTo(ContainSubstring("review,"))

		b.set(AddonSkillSet{AddonID: "notes", Title: "Notes", Skills: []string{"notes"}}) // stopped
		_, err = load("notes", full)
		Expect(err).To(MatchError(HavePrefix(`unknown skill "notes".`)))
		got, err := load("xlsx", full)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("Built-in xlsx body."), "the office add-on stopped too")
	})

	It("keeps an add-on the chat switched off out", func() {
		Expect(svc.SetAddonsOff(ctx, "alice", conv, []string{"notes"})).To(Succeed())
		_, err := load("notes", full)
		Expect(err).To(MatchError(HavePrefix(`unknown skill "notes".`)))
	})

	It("names the folder of a skill and its files when the chat can read them", func() {
		got, err := load("pdf", full)
		Expect(err).NotTo(HaveOccurred())
		lines := strings.Split(got, "\n")
		Expect(lines[0]).To(Equal("Base directory for this skill: /workspace/shared-builtin-skills/pdf"))
		Expect(lines[1]).To(HavePrefix("Files in it (read them with read_file"))
		Expect(lines[1]).To(ContainSubstring("references/qa.md"))
		Expect(lines[1]).To(ContainSubstring("scripts/check.py"))
		Expect(lines[1]).NotTo(ContainSubstring("SKILL.md"))
		Expect(got).To(HaveSuffix("\n\nBuilt-in PDF body. See references/qa.md."))
	})

	It("leaves the folder out without file tools, and refuses a skill that needs commands without them", func() {
		got, err := load("pdf", loadOpts{files: false, exec: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("Built-in PDF body. See references/qa.md."))

		got, err = load("pdf", loadOpts{files: false, exec: false})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("The skill pdf cannot be used in this chat: needs command execution. Do not start it; tell the user plainly what is missing."))
		Expect(got).NotTo(ContainSubstring("Built-in PDF body"))
	})

	It("learns from the final tool list what the chat can do", func() {
		built := svc.builtinsFor(ctx, chatActor("alice", conv), session.ChatRecord{ID: conv, Principal: "alice"}, false)
		var tool session.Tool
		for _, bt := range built {
			if bt.ID == loadSkillName {
				Expect(bt.OK).To(BeTrue(), bt.Reason)
				tool = bt.Tool
			}
		}
		Expect(tool.Name).To(Equal(loadSkillName))
		Expect(tool.Description).To(ContainSubstring("Skills: "))
		Expect(tool.Description).To(ContainSubstring("review"))
		Expect(tool.Description).NotTo(ContainSubstring("/workspace"))

		tools := svc.fitLoadSkill(chatActor("alice", conv), []session.Tool{tool})
		got, err := tools[0].Run(ctx, json.RawMessage(`{"name":"pdf"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HavePrefix("The skill pdf cannot be used in this chat"), "no bash in the list")

		tools = svc.fitLoadSkill(chatActor("alice", conv), []session.Tool{tool, {Name: "bash", Provider: BuiltinProvider}})
		got, err = tools[0].Run(ctx, json.RawMessage(`{"name":"pdf"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("Built-in PDF body. See references/qa.md."), "no read_file in the list")
	})

	It("suggests near names for an unknown one", func() {
		_, err := load("PDF", full)
		Expect(err).To(MatchError(ContainSubstring(`unknown skill "PDF". Did you mean pdf?`)))
		_, err = load("office-notes", full)
		Expect(err).To(MatchError(ContainSubstring("Did you mean notes?")))
		_, err = load("writng-skill", full)
		Expect(err).To(MatchError(ContainSubstring("Did you mean writing-skills?")))
		_, err = load("calendar", full)
		Expect(err).NotTo(MatchError(ContainSubstring("Did you mean")))
		Expect(err.Error()).To(ContainSubstring("The skills of this chat are: "))
	})

	It("never returns the personal skill of another user", func() {
		personal("bob", "secret", skillMD("secret", "Bob's.", "Bob's secret body."))
		_, err := load("secret", full)
		Expect(err).To(MatchError(HavePrefix(`unknown skill "secret".`)))
		Expect(err.Error()).NotTo(ContainSubstring("secret,"))
	})

	It("refuses a SKILL.md that is too large", func() {
		big := skillMD("huge", "Huge.", strings.Repeat("x", skills.MaxFileSize))
		personal("alice", "huge", big)
		_, err := load("huge", full)
		Expect(err).To(MatchError(HavePrefix(`unknown skill "huge".`)), "the resolver skips it as invalid")

		// A file that grows after the skills were resolved is refused too.
		list, err := svc.loadableSkills(ctx, chatActor("alice", conv))
		Expect(err).NotTo(HaveOccurred())
		personal("alice", "review", skillMD("review", "Check a change.", strings.Repeat("y", skills.MaxFileSize)))
		_, err = svc.loadSkill(ctx, "alice", list, "review", full)
		Expect(err).To(MatchError(ContainSubstring("too large to load")))
	})

	It("is not offered to a chat without skills", func() {
		empty, _ := newSkillService(&skillBroker{})
		rec, err := empty.Create(ctx, "alice", CreateInput{Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		found := false
		for _, bt := range empty.builtinsFor(ctx, chatActor("alice", rec.ID), rec, false) {
			if bt.ID == loadSkillName {
				found = true
				Expect(bt.OK).To(BeFalse())
				Expect(bt.Reason).To(Equal("the chat has no skills"))
			}
		}
		Expect(found).To(BeTrue(), "load_skill is registered")
	})
})

package studio

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/plugins"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

var _ = Describe("plugin commands", func() {
	var (
		ctx  context.Context
		svc  *Service
		f    *files.Service
		px   *pluginFixture
		conv string
		hex  string
	)
	model := plugins.Model{Name: "fmt", Version: "1",
		Commands: []plugins.CommandRef{
			{Name: "run", Path: "commands/run.md", ArgumentHint: "<path>"},
			{Name: "pair", Path: "commands/pair.md", Arguments: []string{"from", "to"}},
			{Name: "plain", Path: "commands/plain.md"},
			{Name: "db:migrate", Path: "commands/db/migrate.md"},
			{Name: "inject", Path: "commands/inject.md", ShellInjection: true},
			{Name: "inline"},
			{Name: "tiered", Path: "commands/tiered.md", Model: "haiku"},
		},
		Skills: []plugins.SkillRef{
			{Name: "hidden", Description: "Hidden.", Dir: "skills/hidden", UserInvocable: false},
			{Name: "greet", Description: "Greet.", Dir: "skills/greet", UserInvocable: true, Arguments: []string{"who"}},
		},
	}
	fileset := map[string]string{
		".claude-plugin/plugin.json": `{"name":"fmt","commands":{"inline":{"content":"Inline body for $ARGUMENTS."}}}`,
		"commands/run.md":            "---\ndescription: Run it\nargument-hint: <path>\n---\nRun all of $ARGUMENTS, first $ARGUMENTS[0], second $1, in ${CLAUDE_PLUGIN_ROOT}. Keep \\$1 and {{ .Literal }}.\n",
		"commands/pair.md":           "Copy $from to $to.\n",
		"commands/plain.md":          "No placeholders here.\n",
		"commands/db/migrate.md":     "Migrate.\n",
		"commands/inject.md":         "Status: !`git status`\n",
		"commands/tiered.md":         "---\nmodel: haiku\n---\nTiered body.\n",
		"skills/hidden/SKILL.md":     "---\nname: hidden\ndescription: Hidden.\nuser-invocable: false\n---\nHidden body.\n",
		"skills/greet/SKILL.md":      "---\nname: greet\ndescription: Greet.\narguments: [who]\n---\nSay hello to $who.\n",
	}
	run := func(skill, text string) (string, *session.Invocation, error) {
		rec, err := svc.d.Store.GetChat(ctx, "alice", conv)
		Expect(err).NotTo(HaveOccurred())
		return svc.commandMessage(ctx, "alice", rec, text, Command{Skill: skill})
	}

	BeforeEach(func() {
		ctx = context.Background()
		svc, f = newSkillService(&skillBroker{})
		px = newPluginFixture(svc, f)
		id, digest := px.install("in-fmt", model, "", fileset)
		hex = strings.TrimPrefix(digest, "sha256:")
		px.enable("alice", id, true)
		c, err := svc.Create(ctx, "alice", CreateInput{Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		conv = c.ID
	})

	It("renders the arguments and the variables into the note of the turn, and keeps a command chip", func() {
		directive, inv, err := run("fmt:run", `src "a b"`)
		Expect(err).NotTo(HaveOccurred())
		Expect(inv).To(Equal(&session.Invocation{Kind: session.InvokeCommand, Name: "fmt:run"}))
		Expect(directive).To(ContainSubstring(`Run all of src "a b", first src, second a b, in /workspace/shared-plugins/` + hex + "."))
		Expect(directive).To(ContainSubstring("Keep $1 and {{ .Literal }}."), "an escaped token and a template stay literal")
		Expect(directive).NotTo(ContainSubstring("description: Run it"), "the frontmatter is not the instruction")
		Expect(directive).To(HavePrefix("(System note: the user ran the command"))
	})

	It("fills named arguments and appends the arguments to a body with no placeholder", func() {
		directive, _, err := run("fmt:pair", "x.txt y.txt")
		Expect(err).NotTo(HaveOccurred())
		Expect(directive).To(ContainSubstring("Copy x.txt to y.txt."))
		directive, _, err = run("fmt:plain", "now please")
		Expect(err).NotTo(HaveOccurred())
		Expect(directive).To(ContainSubstring("No placeholders here.\n\nARGUMENTS: now please"))
	})

	It("finds a command in a subfolder and a command whose text is in the manifest", func() {
		directive, _, err := run("fmt:db:migrate", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(directive).To(ContainSubstring("Migrate."))
		directive, _, err = run("fmt:inline", "all")
		Expect(err).NotTo(HaveOccurred())
		Expect(directive).To(ContainSubstring("Inline body for all."))
	})

	It("answers that a command with a shell injection needs a later version", func() {
		_, _, err := run("fmt:inject", "")
		Expect(err).To(MatchError(ErrBadCommand))
		Expect(err.Error()).To(ContainSubstring("This command needs a later version of LocalAI"))
	})

	It("refuses an unknown command with the names the chat has", func() {
		_, _, err := run("fmt:nope", "")
		Expect(err).To(MatchError(ErrBadCommand))
		Expect(err.Error()).To(ContainSubstring("fmt:run"))
		Expect(err.Error()).To(ContainSubstring("fmt:db:migrate"))
		Expect(err.Error()).NotTo(ContainSubstring("fmt:hidden"), "a skill the user cannot start is not offered")
	})

	It("refuses a plugin skill that is not started with /, and renders the arguments of one that is", func() {
		_, _, err := run("fmt:hidden", "")
		Expect(err).To(MatchError(ErrBadCommand))
		Expect(err.Error()).To(ContainSubstring("not started with /"))
		directive, inv, err := run("fmt:greet", "Ada")
		Expect(err).NotTo(HaveOccurred())
		Expect(inv.Kind).To(Equal(session.InvokeSkill))
		Expect(directive).To(ContainSubstring("Say hello to Ada."))
	})

	It("gives no command of a plugin the chat switched off", func() {
		Expect(svc.SetPluginsOff(ctx, "alice", conv, []string{"in-fmt"})).To(Succeed())
		_, _, err := run("fmt:run", "x")
		Expect(err).To(MatchError(ErrBadCommand))
	})

	It("keeps the typed text as the message and runs the command again from it", func() {
		rec, err := svc.d.Store.GetChat(ctx, "alice", conv)
		Expect(err).NotTo(HaveOccurred())
		msg := session.Message{Text: "lib/", Invoked: &session.Invocation{Kind: session.InvokeCommand, Name: "fmt:run"}}
		svc.recommand(ctx, "alice", rec, &msg)
		Expect(msg.Text).To(Equal("lib/"))
		Expect(msg.Directive).To(ContainSubstring("Run all of lib/"))
		Expect(cleanInvocation(msg.Invoked)).To(Equal(msg.Invoked), "an exported command chip comes back on import")
	})
})

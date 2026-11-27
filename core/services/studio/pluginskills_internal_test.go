package studio

import (
	"context"
	"encoding/json"
	"strings"
	"testing/fstest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/plugins"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

var _ = Describe("the plugin skill layer in a chat", func() {
	var (
		ctx    context.Context
		svc    *Service
		f      *files.Service
		px     *pluginFixture
		conv   string
		fmtID  string
		fmtHex string
	)
	fmtModel := plugins.Model{Name: "fmt", Version: "1.0.0",
		Skills: []plugins.SkillRef{
			{Name: "lint", Description: "Lint the code.", Dir: "skills/lint", Path: "skills/lint/SKILL.md", UserInvocable: true, AllowedTools: []string{"Bash(go vet:*)", "Read"}},
			{Name: "deploy", Description: "Deploy it.", Dir: "skills/deploy", Path: "skills/deploy/SKILL.md", UserInvocable: true, DisableModelInvocation: true},
			{Name: "notes", Description: "Take notes.", Dir: "skills/notes", Path: "skills/notes/SKILL.md", UserInvocable: true},
		},
		UserConfig: []plugins.OptionRef{{Key: "token", Sensitive: true}},
	}
	fmtFiles := map[string]string{
		"skills/lint/SKILL.md":       "---\nname: lint\ndescription: Lint the code.\n---\nRun ${CLAUDE_PLUGIN_ROOT}/bin/lint in ${CLAUDE_SKILL_DIR}, chat ${CLAUDE_SESSION_ID}, project ${CLAUDE_PROJECT_DIR}, key ${user_config.token}, shell ${HOME}.\n",
		"skills/lint/rules.md":       "rules",
		"skills/deploy/SKILL.md":     "---\nname: deploy\ndescription: Deploy it.\ndisable-model-invocation: true\n---\nDeploy body.\n",
		"skills/notes/SKILL.md":      "---\nname: notes\ndescription: Take notes.\n---\nNotes body in ${CLAUDE_PLUGIN_ROOT}/notes.\n",
		".claude-plugin/plugin.json": `{"name":"fmt"}`,
	}
	chatRec := func() session.ChatRecord {
		rec, err := svc.d.Store.GetChat(ctx, "alice", conv)
		Expect(err).NotTo(HaveOccurred())
		return rec
	}
	setOf := func(rec session.ChatRecord) map[string]skills.Skill {
		sc := svc.scopeOf(ctx, rec)
		list, _, _, err := svc.chatSkillSet(ctx, "alice", rec, svc.skillsOffOf(ctx, rec, sc), rec.AddonsOff, sc, Commands{})
		Expect(err).NotTo(HaveOccurred())
		out := map[string]skills.Skill{}
		for _, sk := range skills.Usable(list) {
			out[sk.Name] = sk
		}
		return out
	}
	turn := func(rec session.ChatRecord) (prompt, key string) {
		sc := svc.scopeOf(ctx, rec)
		prompt, _, key, _, err := svc.skillTurn(ctx, "alice", rec, svc.skillsOffOf(ctx, rec, sc), rec.AddonsOff, sc, Commands{State: CommandsAvailable}, nil)
		Expect(err).NotTo(HaveOccurred())
		return prompt, key
	}
	load := func(name string) (string, error) {
		args, _ := json.Marshal(map[string]string{"name": name})
		return svc.loadSkillRun(userChatActor("alice", conv), loadOpts{files: true, exec: true})(ctx, args)
	}

	BeforeEach(func() {
		ctx = context.Background()
		svc, f = newSkillService(&skillBroker{})
		px = newPluginFixture(svc, f)
		Expect(f.SyncBuiltin(ctx, fstest.MapFS{
			"writing-skills/SKILL.md": {Data: skillMD("writing-skills", "Built-in writing.", "Writing body.")},
		})).To(Succeed())
		_, err := f.Store().Put(ctx, workspace.Actor{Principal: files.AdminPrincipal}, "/skills/house-style/SKILL.md",
			strings.NewReader(string(skillMD("house-style", "How we write.", "Shared style body."))), workspace.PutOptions{})
		Expect(err).NotTo(HaveOccurred())
		var digest string
		fmtID, digest = px.install("in-fmt", fmtModel, "", fmtFiles)
		fmtHex = strings.TrimPrefix(digest, "sha256:")
		px.enable("alice", fmtID, true)
		c, err := svc.Create(ctx, "alice", CreateInput{Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		conv = c.ID
	})

	It("offers the skills of an enabled plugin under qualified names, after the shared layer", func() {
		set := setOf(chatRec())
		Expect(set).To(HaveKey("fmt:lint"))
		lint := set["fmt:lint"]
		Expect(lint.Layer).To(Equal(skills.LayerPlugin))
		Expect(lint.PluginInstall).To(Equal(fmtID))
		Expect(lint.GuestPath).To(Equal("/workspace/shared-plugins/" + fmtHex + "/skills/lint/SKILL.md"))
		Expect(lint.Requires).To(Equal([]string{skills.RequireExec}), "allowed-tools names Bash")
		Expect(set["fmt:notes"].Requires).To(BeEmpty())
		var order []skills.Layer
		for _, sk := range skills.Usable(mapValues(set)) {
			if len(order) == 0 || order[len(order)-1] != sk.Layer {
				order = append(order, sk.Layer)
			}
		}
		Expect(order).To(Equal([]skills.Layer{skills.LayerShared, skills.LayerPlugin, skills.LayerBuiltin}))
	})

	It("gives no skill of a plugin the user did not enable, or one the chat switched off", func() {
		px.enable("alice", fmtID, false)
		Expect(setOf(chatRec())).NotTo(HaveKey("fmt:lint"))
		px.enable("alice", fmtID, true)
		rec := chatRec()
		rec.PluginsOff = []string{fmtID}
		set := setOf(rec)
		Expect(set).NotTo(HaveKey("fmt:lint"))
		Expect(set).NotTo(HaveKey("fmt:notes"))
		Expect(set).To(HaveKey("house-style"))
	})

	It("switches one plugin skill off by its ID plugin:<plugin>:<skill>", func() {
		Expect(svc.SetSkillsOff(ctx, "alice", conv, []string{"plugin:fmt:notes"})).To(Succeed())
		set := setOf(chatRec())
		Expect(set).NotTo(HaveKey("fmt:notes"))
		Expect(set).To(HaveKey("fmt:lint"))
		Expect(svc.SetSkillsOff(ctx, "alice", conv, []string{"shared:fmt:notes"})).To(MatchError(ErrBadSkillName))
	})

	It("keeps a skill the model may not start out of the index and refuses to load it", func() {
		prompt, _ := turn(chatRec())
		Expect(prompt).To(ContainSubstring("- fmt:lint: Lint the code."))
		Expect(prompt).NotTo(ContainSubstring("fmt:deploy"))
		out, err := load("fmt:deploy")
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("started only by the user"))
		Expect(out).NotTo(ContainSubstring("Deploy body."))
	})

	It("expands the plugin variables to guest paths and hides a sensitive option", func() {
		out, err := load("fmt:lint")
		Expect(err).NotTo(HaveOccurred())
		root := "/workspace/shared-plugins/" + fmtHex
		Expect(out).To(ContainSubstring("Base directory for this skill: " + root + "/skills/lint"))
		Expect(out).To(ContainSubstring("rules.md"))
		Expect(out).To(ContainSubstring("Run " + root + "/bin/lint in " + root + "/skills/lint, chat " + conv + ", project /workspace/home,"))
		Expect(out).To(ContainSubstring("key [sensitive option token]"))
		Expect(out).To(ContainSubstring("shell ${HOME}."), "a shell variable stays as written")
	})

	It("expands the variables of a plugin skill sent with /", func() {
		directive, inv, err := svc.skillDirective(ctx, "alice", chatRec(), "fmt:notes", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(inv.Name).To(Equal("fmt:notes"))
		Expect(directive).To(ContainSubstring("Notes body in /workspace/shared-plugins/" + fmtHex + "/notes."))
		_, _, err = svc.skillDirective(ctx, "alice", chatRec(), "fmt:lint", false)
		Expect(err).To(MatchError(ContainSubstring("needs to run commands")), "a plugin skill that runs Bash needs commands")
	})

	It("changes the refresh key when a plugin is enabled, and not when a skill is switched off", func() {
		_, before := turn(chatRec())
		Expect(svc.SetSkillsOff(ctx, "alice", conv, []string{"plugin:fmt:notes"})).To(Succeed())
		_, off := turn(chatRec())
		Expect(off).To(Equal(before), "a switch must not refresh the sandbox")

		other := plugins.Model{Name: "hooksonly", Version: "1"}
		id, _ := px.install("in-hooks", other, "", nil)
		px.enable("alice", id, true)
		_, after := turn(chatRec())
		Expect(after).NotTo(Equal(before), "a plugin with no skill still changes the key")
	})

	It("shows a chat with a saved agent only the plugins the agent grants", func() {
		st, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		svc.d.Agents = st
		ag, err := svc.SaveAgent(ctx, "alice", agents.Agent{Name: "Bot", Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		c, err := svc.Create(ctx, "alice", CreateInput{Model: "m", AgentID: ag.ID})
		Expect(err).NotTo(HaveOccurred())
		rec, err := svc.d.Store.GetChat(ctx, "alice", c.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(setOf(rec)).NotTo(HaveKey("fmt:lint"), "an empty grant list grants none")
	})

	It("lets the file tools read a plugin package at its guest path", func() {
		Expect(files.SharedGuestPath("/workspace/shared-plugins/" + fmtHex + "/skills/lint/rules.md")).To(Equal(files.PluginsPath + "/" + fmtHex + "/skills/lint/rules.md"))
		Expect(f.EnsureHome(ctx, "bob")).To(Succeed())
		rc, _, err := f.Store().Open(ctx, f.Actor("bob"), files.PluginsPath+"/"+fmtHex+"/skills/lint/rules.md")
		Expect(err).NotTo(HaveOccurred(), "every user reads the packages")
		_ = rc.Close()
		_, err = f.Store().Put(ctx, f.Actor("bob"), files.PluginsPath+"/"+fmtHex+"/x.md", strings.NewReader("x"), workspace.PutOptions{})
		Expect(err).To(HaveOccurred(), "no user writes them")
	})
})

func mapValues(m map[string]skills.Skill) []skills.Skill {
	out := make([]skills.Skill, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

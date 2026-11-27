package studio

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/plugins"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/skills"
)

var _ = Describe("plugins per user, per chat and per agent", func() {
	var (
		ctx context.Context
		svc *Service
		f   *files.Service
		px  *pluginFixture
		id  string
	)
	model := plugins.Model{Name: "fmt", Version: "1", Skills: []plugins.SkillRef{
		{Name: "notes", Description: "Take notes.", Dir: "skills/notes", UserInvocable: true},
	}}
	skillsOf := func(user, conv string) []string {
		rec, err := svc.d.Store.GetChat(ctx, user, conv)
		Expect(err).NotTo(HaveOccurred())
		sc := svc.scopeOf(ctx, rec)
		list, _, _, err := svc.chatSkillSet(ctx, user, rec, svc.skillsOffOf(ctx, rec, sc), rec.AddonsOff, sc, Commands{})
		Expect(err).NotTo(HaveOccurred())
		var out []string
		for _, sk := range skills.Usable(list) {
			if sk.Layer == skills.LayerPlugin {
				out = append(out, sk.Name)
			}
		}
		return out
	}
	newChat := func(in CreateInput) string {
		in.Model = "m"
		c, err := svc.Create(ctx, "alice", in)
		Expect(err).NotTo(HaveOccurred())
		return c.ID
	}

	BeforeEach(func() {
		ctx = context.Background()
		svc, f = newSkillService(&skillBroker{})
		px = newPluginFixture(svc, f)
		id, _ = px.install("in-fmt", model, "", map[string]string{"skills/notes/SKILL.md": "---\nname: notes\ndescription: Take notes.\n---\nBody.\n"})
		st, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		svc.d.Agents = st
	})

	It("gives a chat created before the plugin was enabled the plugin", func() {
		conv := newChat(CreateInput{})
		Expect(skillsOf("alice", conv)).To(BeEmpty())
		Expect(px.svc.SetEnabled(ctx, plugins.Actor{ID: "alice"}, id, true)).To(Succeed())
		Expect(skillsOf("alice", conv)).To(Equal([]string{"fmt:notes"}))
	})

	It("keeps the plugins a chat switched off, checked and capped", func() {
		px.enable("alice", id, true)
		conv := newChat(CreateInput{})
		Expect(svc.SetPluginsOff(ctx, "alice", conv, []string{id, id})).To(Succeed())
		rec, err := svc.d.Store.GetChat(ctx, "alice", conv)
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.PluginsOff).To(Equal([]string{id}), "an ID appears once")
		Expect(skillsOf("alice", conv)).To(BeEmpty())

		Expect(svc.SetPluginsOff(ctx, "alice", conv, []string{"../x"})).To(MatchError(ErrBadPluginSwitch))
		many := make([]string, maxPluginSwitches+1)
		for i := range many {
			many[i] = fmt.Sprintf("in-%d", i)
		}
		Expect(svc.SetPluginsOff(ctx, "alice", conv, many)).To(MatchError(ErrBadPluginSwitch))
		Expect(svc.SetPluginsOff(ctx, "bob", conv, nil)).To(MatchError(session.ErrNotFound))
		Expect(svc.SetPluginsOff(ctx, "alice", conv, nil)).To(Succeed())
		Expect(skillsOf("alice", conv)).To(Equal([]string{"fmt:notes"}))
	})

	It("starts a chat with the plugins its switches turn off", func() {
		px.enable("alice", id, true)
		conv := newChat(CreateInput{Switches: Switches{PluginsOff: []string{id}}})
		Expect(skillsOf("alice", conv)).To(BeEmpty())
	})

	It("gives a chat with a saved agent the plugins the agent grants, and none with an empty list", func() {
		px.enable("alice", id, true)
		none, err := svc.SaveAgent(ctx, "alice", agents.Agent{Name: "None", Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		granted, err := svc.SaveAgent(ctx, "alice", agents.Agent{Name: "Granted", Model: "m", Plugins: []string{id}})
		Expect(err).NotTo(HaveOccurred())
		Expect(granted.Plugins).To(Equal([]string{id}))
		Expect(skillsOf("alice", newChat(CreateInput{AgentID: none.ID}))).To(BeEmpty())
		withAgent := newChat(CreateInput{AgentID: granted.ID})
		Expect(skillsOf("alice", withAgent)).To(Equal([]string{"fmt:notes"}))
		Expect(svc.SetPluginsOff(ctx, "alice", withAgent, []string{id})).To(Succeed())
		Expect(skillsOf("alice", withAgent)).To(BeEmpty(), "a chat switch narrows the agent's grant")
	})

	It("gives a user's own install only to that user", func() {
		own, _ := px.install("in-own", plugins.Model{Name: "mine", Version: "1", Skills: []plugins.SkillRef{{Name: "x", Description: "X.", Dir: "skills/x", UserInvocable: true}}}, "bob", nil)
		px.enable("bob", own, true)
		px.enable("alice", own, true) // a stray state of another user grants nothing
		Expect(skillsOf("alice", newChat(CreateInput{}))).To(BeEmpty())
	})
})

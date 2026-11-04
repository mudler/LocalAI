package studio

import (
	"context"
	"encoding/json"
	"strings"
	"testing/fstest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

var _ = Describe("the agent skills layer", func() {
	var (
		ctx    context.Context
		svc    *Service
		f      *files.Service
		sales  agents.Agent
		other  agents.Agent
		chatID string
	)
	put := func(rel string, data []byte) {
		abs, err := f.Abs("alice", rel)
		Expect(err).NotTo(HaveOccurred())
		_, err = f.Store().Put(ctx, f.Actor("alice"), abs, strings.NewReader(string(data)), workspace.PutOptions{Overwrite: true})
		Expect(err).NotTo(HaveOccurred())
	}
	chatOf := func(a agents.Agent) session.ChatRecord {
		c, err := svc.Create(ctx, "alice", CreateInput{Model: "m", AgentID: a.ID})
		Expect(err).NotTo(HaveOccurred())
		rec, err := svc.d.Store.GetChat(ctx, "alice", c.ID)
		Expect(err).NotTo(HaveOccurred())
		return rec
	}
	resolveFor := func(rec session.ChatRecord) map[string]skills.Skill {
		sc := svc.scopeOf(ctx, rec)
		list, _, _, err := svc.chatSkillSet(ctx, "alice", rec.SkillsOff, rec.AddonsOff, sc, Commands{})
		Expect(err).NotTo(HaveOccurred())
		out := map[string]skills.Skill{}
		for _, sk := range list {
			if sk.Shadowed == "" && !sk.Off {
				out[sk.Name] = sk
			}
		}
		return out
	}

	BeforeEach(func() {
		ctx = context.Background()
		svc, f = newSkillService(&skillBroker{})
		st, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		svc.d.Agents = st
		Expect(f.SyncBuiltin(ctx, fstest.MapFS{
			"writing-skills/SKILL.md": {Data: skillMD("writing-skills", "Built-in writing.", "Writing body.")},
			"review/SKILL.md":         {Data: skillMD("review", "Built-in review.", "Built-in review body.")},
		})).To(Succeed())
		sales, err = svc.SaveAgent(ctx, "alice", agents.Agent{Name: "Sales", Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		other, err = svc.SaveAgent(ctx, "alice", agents.Agent{Name: "Other", Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		put("/.skills/review/SKILL.md", skillMD("review", "Personal review.", "Personal review body."))
		put("/.skills/notes/SKILL.md", skillMD("notes", "Personal notes.", "Personal notes body."))
		put("/"+files.AgentSkillsDir(sales.ID)+"/review/SKILL.md", skillMD("review", "Sales review.", "Sales review body."))
		put("/"+files.AgentSkillsDir(sales.ID)+"/pitch/SKILL.md", skillMD("pitch", "Sales pitch.", "Pitch body."))
		put("/"+files.AgentSkillsDir(other.ID)+"/secret/SKILL.md", skillMD("secret", "Other only.", "Secret body."))
	})

	It("is nearer than the personal layer for the agent that owns it", func() {
		rec := chatOf(sales)
		got := resolveFor(rec)
		Expect(got["review"].Layer).To(Equal(skills.LayerAgent))
		Expect(got["review"].Description).To(Equal("Sales review."))
		Expect(got["review"].Agent).To(Equal(sales.ID))
		Expect(got["review"].AgentName).To(Equal("Sales"))
		Expect(got["review"].GuestPath).To(HaveSuffix("/" + files.AgentSkillsDir(sales.ID) + "/review/SKILL.md"))
	})

	It("counts as granted without being listed in the agent's skills, and keeps ungranted ones out", func() {
		got := resolveFor(chatOf(sales))
		Expect(got).To(HaveKey("pitch"), "the agent layer is granted")
		Expect(got).NotTo(HaveKey("notes"), "a personal skill the agent was not granted stays out")
		Expect(got).NotTo(HaveKey("writing-skills"), "writing-skills needs skills.write")
	})

	It("shows an agent only its own skills", func() {
		got := resolveFor(chatOf(other))
		Expect(got).To(HaveKey("secret"))
		Expect(got).NotTo(HaveKey("pitch"))
		Expect(got).NotTo(HaveKey("review"), "nothing granted, and the other agent's review is not here")
	})

	It("is absent from a plain chat, which sees the personal skill", func() {
		c, err := svc.Create(ctx, "alice", CreateInput{Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		rec, err := svc.d.Store.GetChat(ctx, "alice", c.ID)
		Expect(err).NotTo(HaveOccurred())
		got := resolveFor(rec)
		Expect(got["review"].Layer).To(Equal(skills.LayerPersonal))
		Expect(got).NotTo(HaveKey("pitch"))
	})

	It("exposes writing-skills to an agent that holds skills.write, with the propose preamble", func() {
		a, err := svc.SaveAgent(ctx, "alice", agents.Agent{Name: "Writer", Model: "m", Builtins: []string{contract.CapSkillsWrite}})
		Expect(err).NotTo(HaveOccurred())
		rec := chatOf(a)
		Expect(resolveFor(rec)).To(HaveKey("writing-skills"))
		chatID = rec.ID
		args, _ := json.Marshal(map[string]string{"name": "writing-skills"})
		out, err := svc.loadSkillRun(userChatActor("alice", chatID), loadOpts{files: true})(ctx, args)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(HavePrefix("Do not write files. Call propose_change with kind skill."))
		Expect(out).To(ContainSubstring("Writing body."))
	})

	It("lists the skills of every agent for the Skills page, each with its own agent, and keeps them out of the plain list", func() {
		plain, _, err := svc.Skills(ctx, "alice", "")
		Expect(err).NotTo(HaveOccurred())
		for _, sk := range plain {
			Expect(sk.Layer).NotTo(Equal(skills.LayerAgent))
		}
		list, err := svc.AgentSkills(ctx, "alice")
		Expect(err).NotTo(HaveOccurred())
		by := map[string]string{}
		for _, sk := range list {
			if sk.Layer == skills.LayerAgent {
				by[sk.Name] = sk.Agent
			}
		}
		Expect(by).To(Equal(map[string]string{"review": sales.ID, "pitch": sales.ID, "secret": other.ID}))
	})

	It("is written only by a proposal that names a skill an agent may own", func() {
		path, err := skillKind.Path(proposals.Aim{Scope: contract.ScopeAgent, Agent: sales.ID, Target: "pitch"})
		Expect(err).NotTo(HaveOccurred())
		Expect(path).To(Equal(files.AgentSkillsDir(sales.ID) + "/pitch/SKILL.md"))
		check := func(name string) error {
			return skillKind.Check(proposals.CheckInput{Aim: proposals.Aim{Scope: contract.ScopeAgent, Agent: sales.ID, Target: name},
				Content: string(skillMD(name, "d", "body"))})
		}
		Expect(check("pitch")).To(Succeed())
		Expect(check("writing-skills")).To(MatchError(ContainSubstring("built-in skill")), "a core skill name")
		Expect(check("xlsx")).To(MatchError(ContainSubstring("document slot")), "a slot from slots.json")
	})

	It("is deleted to the Trash from the page", func() {
		Expect(f.DeleteAgentSkill(ctx, "alice", sales.ID, "pitch")).To(Succeed())
		Expect(resolveFor(chatOf(sales))).NotTo(HaveKey("pitch"))
		Expect(f.DeleteAgentSkill(ctx, "alice", "../x", "pitch")).NotTo(Succeed())
	})
})

package studio_test

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// scopedTrig is a trigger rig whose user has a saved agent limited to
// /Projects/Acme, next to a sibling /Projects/Other and a folder whose name
// starts the same, /Projects/AcmeX.
type scopedTrig struct {
	*trigRig
	ctx   context.Context
	act   workspace.Actor
	home  string
	agent agents.Agent
}

func newScopedTrig(script func(workspace.Actor, string) []studiotest.ToolStep) *scopedTrig {
	GinkgoHelper()
	sh := newShared()
	ctx := context.Background()
	Expect(sh.files.EnsureHome(ctx, alice)).To(Succeed())
	s := &scopedTrig{ctx: ctx, act: sh.files.Actor(alice), home: session.HomePath(alice)}
	for _, d := range []string{"/Projects", "/Projects/Acme", "/Projects/Acme/In", "/Projects/Other", "/Projects/AcmeX"} {
		s.mkdirIn(sh, d)
	}
	s.trigRig = sh.rig(script, studio.TriggerOptions{})
	ag, err := s.svc.SaveAgentWithFolder(ctx, alice, agents.Agent{Name: "Acme", Model: "m", Builtins: []string{"act.files"}},
		&studio.AgentFolderWrite{Path: "/Projects/Acme", Mode: agents.FolderWrite})
	Expect(err).NotTo(HaveOccurred())
	s.agent = ag
	return s
}

func (s *scopedTrig) mkdirIn(sh *shared, rel string) {
	GinkgoHelper()
	_, err := sh.files.Store().Mkdir(s.ctx, s.act, s.home+rel)
	Expect(err).NotTo(HaveOccurred())
}

func (s *scopedTrig) put(rel, content string) {
	GinkgoHelper()
	_, err := s.sh.files.Store().Put(s.ctx, s.act, s.home+rel, strings.NewReader(content), workspace.PutOptions{Overwrite: true})
	Expect(err).NotTo(HaveOccurred())
}

func (s *scopedTrig) remove(rel string) {
	GinkgoHelper()
	Expect(s.sh.files.Store().Delete(s.ctx, s.act, s.home+rel)).To(Succeed())
}

func (s *scopedTrig) fileTrigger(folder string) (triggers.Trigger, error) {
	return s.host.CreateTrigger(s.ctx, alice, studio.TriggerInput{Name: "Arrivals", Prompt: "Sort the new file.", Kind: triggers.KindFile,
		Folder: folder, Quiet: time.Second, AgentID: s.agent.ID, Enabled: true})
}

var _ = Describe("triggers of a scoped agent", func() {
	It("refuses a file trigger outside the agent's folder", func() {
		s := newScopedTrig(saySummary)
		for _, folder := range []string{"/Projects/Other", "/Projects", "/", "/Projects/AcmeX"} {
			_, err := s.fileTrigger(folder)
			Expect(err).To(MatchError(studio.ErrBadTrigger), "folder %s", folder)
			Expect(err.Error()).To(ContainSubstring("must be inside the agent's folder"), "folder %s", folder)
		}
		in, err := s.fileTrigger("/Projects/Acme/In")
		Expect(err).NotTo(HaveOccurred())
		Expect(in.Folder).To(Equal(s.home + "/Projects/Acme/In"))
		_, err = s.fileTrigger("/Projects/Acme")
		Expect(err).NotTo(HaveOccurred(), "the agent's folder itself")

		// At update too, and the stored trigger stays as it was.
		_, err = s.host.UpdateTrigger(s.ctx, alice, in.ID, studio.TriggerInput{Name: "Arrivals", Prompt: "Sort the new file.",
			Kind: triggers.KindFile, Folder: "/Projects/Other", AgentID: s.agent.ID, Enabled: true})
		Expect(err).To(MatchError(studio.ErrBadTrigger))
		Expect(err.Error()).To(ContainSubstring("must be inside the agent's folder"))
		got, err := s.host.GetTrigger(s.ctx, alice, in.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Folder).To(Equal(s.home + "/Projects/Acme/In"))

		// A trigger without an agent, or with an agent without a folder,
		// watches any folder of the home as before.
		_, err = s.host.CreateTrigger(s.ctx, alice, studio.TriggerInput{Name: "Home", Prompt: "p", Kind: triggers.KindFile,
			Folder: "/Projects/Other", Model: "m", Enabled: true})
		Expect(err).NotTo(HaveOccurred())
		plain, err := s.svc.SaveAgent(s.ctx, alice, agents.Agent{Name: "Plain", Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		_, err = s.host.CreateTrigger(s.ctx, alice, studio.TriggerInput{Name: "Plain", Prompt: "p", Kind: triggers.KindFile,
			Folder: "/Projects/Other", AgentID: plain.ID, Enabled: true})
		Expect(err).NotTo(HaveOccurred())
	})

	It("refuses a trigger whose agent's folder is missing", func() {
		s := newScopedTrig(saySummary)
		s.remove("/Projects/Acme")
		_, err := s.fileTrigger("/Projects/Acme/In")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("the agent's folder is missing"))
		_, err = s.host.CreateTrigger(s.ctx, alice, studio.TriggerInput{Name: "Daily", Prompt: "p", Kind: triggers.KindSchedule,
			Schedule: "daily 08:00", TZ: "UTC", AgentID: s.agent.ID, Enabled: true})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("the agent's folder is missing"))
	})

	It("re-checks the folder of a proposal when it is confirmed", func() {
		s := newScopedTrig(saySummary)
		p, err := s.host.ProposeTrigger(s.ctx, alice, studio.TriggerInput{Name: "Arrivals", Prompt: "p", Kind: triggers.KindFile,
			Folder: "/Projects/Acme/In", AgentID: s.agent.ID})
		Expect(err).NotTo(HaveOccurred())
		// The agent moves to another folder before the person confirms.
		_, err = s.svc.SaveAgentWithFolder(s.ctx, alice, s.agent, &studio.AgentFolderWrite{Path: "/Projects/Other", Mode: agents.FolderWrite})
		Expect(err).NotTo(HaveOccurred())
		_, err = s.host.ConfirmProposal(s.ctx, alice, p.ID, "UTC")
		Expect(err).To(MatchError(studio.ErrBadTrigger))
		Expect(err.Error()).To(ContainSubstring("must be inside the agent's folder"))
	})
})

// scopedToolScript runs one trigger tool call for each message: the message
// is "<tool> <json args>".
func scopedToolScript(_ workspace.Actor, text string) []studiotest.ToolStep {
	name, args, _ := strings.Cut(text, " ")
	return []studiotest.ToolStep{{Tool: name, Args: args}, {Say: "ok"}}
}

var _ = Describe("trigger tools of a scoped chat", func() {
	lastResult := func(s *scopedTrig, tool string) studiotest.ToolResult {
		GinkgoHelper()
		res := s.h.ResultsFor(tool)
		Expect(res).NotTo(BeEmpty())
		return res[len(res)-1]
	}
	call := func(s *scopedTrig, chat, tool, args string) studiotest.ToolResult {
		GinkgoHelper()
		sent(s.svc, alice, chat, tool+" "+args)
		idle(s.svc, alice, chat)
		return lastResult(s, tool)
	}

	It("cannot point a file trigger outside the agent's folder", func() {
		s := newScopedTrig(scopedToolScript)
		chat, err := s.svc.Create(s.ctx, alice, studio.CreateInput{AgentID: s.agent.ID})
		Expect(err).NotTo(HaveOccurred())

		// The chat's paths are paths of its place: / is the agent's folder.
		for _, folder := range []string{"/In", "/workspace/home/In", "In"} {
			res := call(s, chat.ID, "propose_trigger", `{"name":"Inbox","prompt":"Sort it.","when":{"file_arrives_in":"`+folder+`"}}`)
			Expect(res.Err).To(BeNil(), "folder %s: %+v", folder, res)
		}
		props, err := s.host.Proposals(s.ctx, alice)
		Expect(err).NotTo(HaveOccurred())
		Expect(props).To(HaveLen(1), "the same folder three times is one proposal: %+v", props)
		Expect(props[0].Folder).To(Equal(s.home + "/Projects/Acme/In"))
		Expect(props[0].AgentID).To(Equal(s.agent.ID))

		for _, folder := range []string{"/../Other", "/workspace/home/../Other", "/workspace/chat", "/workspace/shared-x/In", "/Projects/Other"} {
			res := call(s, chat.ID, "propose_trigger", `{"name":"Out","prompt":"Sort it.","when":{"file_arrives_in":"`+folder+`"}}`)
			Expect(res.Err).To(HaveOccurred(), "folder %s was accepted: %+v", folder, res)
		}
		props, _ = s.host.Proposals(s.ctx, alice)
		for _, p := range props {
			Expect(p.Folder == s.home+"/Projects/Acme" || strings.HasPrefix(p.Folder, s.home+"/Projects/Acme/")).To(BeTrue(), "proposal outside the folder: %+v", p)
		}

		// update_trigger: a trigger of the agent can be moved only inside.
		tr, err := s.fileTrigger("/Projects/Acme/In")
		Expect(err).NotTo(HaveOccurred())
		ref := studio.TriggerRef(tr.ID)
		res := call(s, chat.ID, "update_trigger", `{"ref":"`+ref+`","when":{"file_arrives_in":"/../Other"}}`)
		Expect(res.Err).To(HaveOccurred(), "%+v", res)
		res = call(s, chat.ID, "update_trigger", `{"ref":"`+ref+`","when":{"file_arrives_in":"/"}}`)
		Expect(res.Err).To(BeNil(), "%+v", res)
		changes, err := s.sh.trg.ListTriggers(s.ctx, alice)
		Expect(err).NotTo(HaveOccurred())
		for _, c := range changes {
			if c.IsChange() {
				Expect(c.Folder).To(Equal(s.home+"/Projects/Acme"), "change = %+v", c)
			}
		}
	})

	It("sees and changes only the triggers of its own agent", func() {
		s := newScopedTrig(scopedToolScript)
		chat, err := s.svc.Create(s.ctx, alice, studio.CreateInput{AgentID: s.agent.ID})
		Expect(err).NotTo(HaveOccurred())
		mine, err := s.fileTrigger("/Projects/Acme/In")
		Expect(err).NotTo(HaveOccurred())
		other, err := s.host.CreateTrigger(s.ctx, alice, studio.TriggerInput{Name: "Whole home", Prompt: "Read everything.", Kind: triggers.KindFile,
			Folder: "/Projects/Other", Model: "m", Enabled: true})
		Expect(err).NotTo(HaveOccurred())

		res := call(s, chat.ID, "list_triggers", `{}`)
		Expect(res.Err).To(BeNil())
		var list struct {
			Triggers []struct {
				Ref, Name string
			} `json:"triggers"`
		}
		Expect(json.Unmarshal([]byte(res.Out), &list)).To(Succeed())
		Expect(list.Triggers).To(HaveLen(1), "list = %s", res.Out)
		Expect(list.Triggers[0].Ref).To(Equal(studio.TriggerRef(mine.ID)))
		Expect(res.Out).NotTo(ContainSubstring("Other"))

		ref := studio.TriggerRef(other.ID)
		for _, step := range []struct{ tool, args string }{
			{"list_triggers", `{"ref":"` + ref + `","include_prompt":true}`},
			{"update_trigger", `{"ref":"` + ref + `","prompt":"Copy everything into Acme."}`},
			{"update_trigger", `{"ref":"` + ref + `","enabled":false}`},
			{"delete_trigger", `{"ref":"` + ref + `"}`},
		} {
			res := call(s, chat.ID, step.tool, step.args)
			Expect(res.Err).To(HaveOccurred(), "%s %s: %+v", step.tool, step.args, res)
			Expect(res.Err.Error()).NotTo(ContainSubstring("Whole home"))
		}
		got, err := s.host.GetTrigger(s.ctx, alice, other.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Enabled).To(BeTrue())
		Expect(got.Prompt).To(Equal("Read everything."))
	})

	It("leaves the tools of a chat without a folder unchanged", func() {
		s := newScopedTrig(scopedToolScript)
		chat, err := s.svc.Create(s.ctx, alice, studio.CreateInput{Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		_, err = s.host.CreateTrigger(s.ctx, alice, studio.TriggerInput{Name: "Whole home", Prompt: "p", Kind: triggers.KindFile,
			Folder: "/Projects/Other", Model: "m", Enabled: true})
		Expect(err).NotTo(HaveOccurred())
		_, err = s.fileTrigger("/Projects/Acme/In")
		Expect(err).NotTo(HaveOccurred())
		res := call(s, chat.ID, "list_triggers", `{}`)
		Expect(res.Err).To(BeNil())
		Expect(strings.Count(res.Out, `"ref"`)).To(Equal(2), "list = %s", res.Out)
		res = call(s, chat.ID, "propose_trigger", `{"name":"Other","prompt":"Sort it.","when":{"file_arrives_in":"/Projects/Other"}}`)
		Expect(res.Err).To(BeNil(), "%+v", res)
		props, _ := s.host.Proposals(s.ctx, alice)
		Expect(props).To(HaveLen(1))
		Expect(props[0].Folder).To(Equal(s.home + "/Projects/Other"))
	})
})

package studio

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"strings"
	"testing/fstest"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"

	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("document command eligibility", func() {
	It("does not offer installed Sandbox instructions without selected isolated commands", func() {
		ctx := context.Background()
		b := &skillBroker{}
		svc, f := newSkillService(b)
		Expect(f.SyncAddonSkills(ctx, "sandbox", fstest.MapFS{"docx/SKILL.md": {Data: md("docx", "Sandbox document.")}})).To(Succeed())
		b.set(AddonSkillSet{AddonID: "sandbox", Skills: []string{"docx"}, Shadows: []string{"docx"}, Isolated: []string{"docx"}, Running: true})
		list, _, err := svc.Skills(ctx, "alice", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(skills.Names(skills.Usable(list))).NotTo(ContainElement("docx"))
	})
	DescribeTable("tells the person why the Sandbox document skills are missing", func(command Commands, off []string, wantNote bool) {
		ctx := context.Background()
		b := &skillBroker{}
		svc, f := newSkillService(b)
		Expect(f.SyncAddonSkills(ctx, "sandbox", fstest.MapFS{
			"docx/SKILL.md": {Data: md("docx", "Sandbox document.")},
			"pdf/SKILL.md":  {Data: md("pdf", "Sandbox pdf.")},
			"plot/SKILL.md": {Data: md("plot", "Sandbox plots.")},
		})).To(Succeed())
		b.set(AddonSkillSet{AddonID: "sandbox", Skills: []string{"docx", "pdf", "plot"}, Shadows: []string{"docx", "pdf"}, Isolated: []string{"docx", "pdf"}, Running: true})
		_, notes, err := chatSkillNotes(svc, ctx, off, command)
		Expect(err).NotTo(HaveOccurred())
		if wantNote {
			Expect(notes).To(ContainElement(HavePrefix("the document skills docx, pdf need isolated command execution by Sandbox; ")))
		} else {
			Expect(strings.Join(notes, ";")).NotTo(ContainSubstring("isolated command execution"))
		}
	},
		Entry("commands run on the host", Commands{State: CommandsAvailable, Addon: "sandbox", Provider: "sandbox", Title: "Sandbox", Isolation: "host"}, nil, true),
		Entry("isolation unknown", Commands{State: CommandsAvailable, Addon: "sandbox", Provider: "sandbox", Title: "Sandbox"}, nil, true),
		Entry("isolated", Commands{State: CommandsAvailable, Addon: "sandbox", Provider: "sandbox", Isolation: "isolated"}, nil, false),
		Entry("another provider runs the commands", Commands{State: CommandsAvailable, Addon: "other", Provider: "other", Isolation: "host"}, nil, false),
		Entry("the Sandbox is off for the chat", Commands{State: CommandsAvailable, Addon: "sandbox", Provider: "sandbox", Isolation: "host"}, []string{"sandbox"}, false),
	)
	It("names the second provider by the title it declares, in the note and in the commands", func() {
		ctx := context.Background()
		b := &documentBroker{skillBroker: &skillBroker{}, isolation: "host", defs: []AddonTool{{AddonID: "runbox", Name: "addon__runbox__exec", Shadows: "bash",
			Execution: &contract.ExecutionDescriptor{Class: contract.ExecutionClassCommands, Title: "Runbox"}}}}
		svc, f := newSkillService(b.skillBroker)
		svc.d.AddonTools = b
		Expect(f.SyncAddonSkills(ctx, "runbox", fstest.MapFS{"docx/SKILL.md": {Data: md("docx", "Runbox document.")}})).To(Succeed())
		b.set(AddonSkillSet{AddonID: "runbox", Skills: []string{"docx"}, Shadows: []string{"docx"}, Isolated: []string{"docx"}, Running: true})
		rec, err := svc.Create(ctx, "alice", CreateInput{Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		command := documentCommands(svc, ctx, "alice", rec, nil)
		Expect(command.Title).To(Equal("Runbox"))
		Expect(isolatedProvider(command, "runbox")).To(BeFalse(), "the status says host")
		_, notes, err := chatSkillNotes(svc, ctx, nil, command)
		Expect(err).NotTo(HaveOccurred())
		Expect(notes).To(ContainElement(ContainSubstring("need isolated command execution by Runbox; its commands run on the server without isolation")))
		b.isolation = "isolated"
		command = documentCommands(svc, ctx, "alice", rec, nil)
		Expect(isolatedProvider(command, "runbox")).To(BeTrue())
		list, _, err := chatSkillNotes(svc, ctx, nil, command)
		Expect(err).NotTo(HaveOccurred())
		Expect(skills.Names(skills.Usable(list))).To(ContainElement("docx"))
	})
	It("offers the selected provider's document skills in a draft, and still gates a real chat on its status", func() {
		ctx := context.Background()
		b := &documentBroker{skillBroker: &skillBroker{}, isolation: "", defs: []AddonTool{{AddonID: "runbox", Name: "addon__runbox__exec", Shadows: "bash",
			Execution: &contract.ExecutionDescriptor{Class: contract.ExecutionClassCommands, Title: "Runbox"}}}}
		svc, f := newSkillService(b.skillBroker)
		svc.d.AddonTools = b
		Expect(f.SyncAddonSkills(ctx, "runbox", fstest.MapFS{"docx/SKILL.md": {Data: md("docx", "Runbox document.")}})).To(Succeed())
		b.set(AddonSkillSet{AddonID: "runbox", Skills: []string{"docx"}, Shadows: []string{"docx"}, Isolated: []string{"docx"}, Running: true})
		draft := documentCommands(svc, ctx, "alice", session.ChatRecord{}, nil)
		Expect(draft.Draft).To(BeTrue())
		list, _, err := svc.Skills(ctx, "alice", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(skills.Names(skills.Usable(list))).To(ContainElement("docx"), "a draft has no status to read, so it must not hide the skill")
		rec, err := svc.Create(ctx, "alice", CreateInput{Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		command := documentCommands(svc, ctx, "alice", rec, nil)
		Expect(command.Draft).To(BeFalse())
		list, _, err = chatSkillNotes(svc, ctx, nil, command)
		Expect(err).NotTo(HaveOccurred())
		Expect(skills.Names(skills.Usable(list))).NotTo(ContainElement("docx"), "a real chat with unknown isolation stays closed")
	})
	It("does not switch a missing pinned document provider to Office", func() {
		a := addonSkills{pins: map[string]string{"docx": "sandbox"}, usable: []AddonSkillSet{{AddonID: "office", Shadows: []string{"docx"}}}}
		id, chosen := a.provider("docx")
		Expect(chosen).To(BeTrue())
		Expect(id).To(BeEmpty())
	})
})

// sandboxExecution is how the Sandbox add-on declares its commands tool.
var sandboxExecution = &contract.ExecutionDescriptor{Class: contract.ExecutionClassCommands, Title: "Sandbox"}

// This broker lists metadata but fails immediately if descriptor construction
// invokes a command. Keeping it separate avoids changing older fixtures.
type documentBroker struct {
	*skillBroker
	defs      []AddonTool
	isolation string
	reason    string
	calls     int
}

func (b *documentBroker) Tools(context.Context, string) ([]AddonTool, error) {
	b.calls++
	return b.defs, nil
}
func (b *documentBroker) Unavailable(context.Context, string, string, bool) string { return b.reason }
func (b *documentBroker) Isolation(context.Context, string, string) string         { return b.isolation }
func (b *documentBroker) Call(context.Context, string, string, string, json.RawMessage) (string, error) {
	Fail("descriptor executed a command")
	return "", nil
}

var _ = Describe("document provider matrix", func() {
	DescribeTable("filters before arbitration", func(ids []string, command Commands, off []string, sc *scope, pin, want string) {
		ctx := context.Background()
		b := &skillBroker{}
		svc, f := newSkillService(b, map[string]string{"docx": pin})
		for _, id := range ids {
			Expect(f.SyncAddonSkills(ctx, id, fstest.MapFS{"docx/SKILL.md": {Data: md("docx", id+" document.")}})).To(Succeed())
			set := AddonSkillSet{AddonID: id, Skills: []string{"docx"}, Shadows: []string{"docx"}, Running: true}
			if id == "sandbox" || id == "runbox" { // office ships no commands tool, so its document skill needs no isolation
				set.Isolated = []string{"docx"}
			}
			b.sets = append(b.sets, set)
		}
		ctx, closeScope := svc.executionScope(ctx, "alice", "c1")
		defer closeScope()
		list, _, _, err := svc.chatSkillSet(ctx, "alice", nil, off, sc, command)
		Expect(err).NotTo(HaveOccurred())
		usable := skills.Usable(list)
		if want == "" {
			Expect(usable).To(BeEmpty())
		} else {
			Expect(usable).To(HaveLen(1))
			Expect(usable[0].Addon).To(Equal(want))
		}
	},
		Entry("neither", nil, Commands{}, nil, nil, "", ""),
		Entry("sandbox selected", []string{"sandbox"}, Commands{State: CommandsAvailable, Addon: "sandbox", Provider: "sandbox", Isolation: "isolated"}, nil, nil, "", "sandbox"),
		Entry("office without exec", []string{"office"}, Commands{}, nil, nil, "", "office"),
		Entry("both with host fallback", []string{"sandbox", "office"}, Commands{State: CommandsAvailable, Provider: BuiltinProvider, Isolation: "host"}, nil, nil, "", "office"),
		Entry("both with other isolated provider", []string{"sandbox", "office"}, Commands{State: CommandsAvailable, Addon: "other", Provider: "other", Isolation: "isolated"}, nil, nil, "", "office"),
		Entry("unknown isolation", []string{"sandbox"}, Commands{State: CommandsAvailable, Addon: "sandbox", Provider: "sandbox"}, nil, nil, "", ""),
		Entry("incomplete provider", []string{"sandbox"}, Commands{State: CommandsAvailable, Addon: "sandbox", Isolation: "isolated"}, nil, nil, "", ""),
		Entry("shell off", []string{"sandbox"}, Commands{State: CommandsSwitchedOff, Addon: "sandbox", Provider: "sandbox", Isolation: "isolated"}, nil, nil, "", ""),
		Entry("addon off", []string{"sandbox"}, Commands{State: CommandsAvailable, Addon: "sandbox", Provider: "sandbox", Isolation: "isolated"}, []string{"sandbox"}, nil, "", ""),
		Entry("ungranted", []string{"sandbox"}, Commands{State: CommandsAvailable, Addon: "sandbox", Provider: "sandbox", Isolation: "isolated"}, nil, &scope{skills: map[string]bool{"docx": true}}, "", ""),
		Entry("deleted scope", []string{"sandbox"}, Commands{}, nil, &scope{deleted: true}, "", ""),
		Entry("unavailable pin", []string{"sandbox", "office"}, Commands{}, nil, nil, "sandbox", ""),
		Entry("missing pin", []string{"office"}, Commands{}, nil, nil, "sandbox", ""),
		Entry("eligible pin", []string{"sandbox", "office"}, Commands{State: CommandsAvailable, Addon: "sandbox", Provider: "sandbox", Isolation: "isolated"}, nil, nil, "sandbox", "sandbox"),
		Entry("a second provider selected", []string{"runbox"}, Commands{State: CommandsAvailable, Addon: "runbox", Provider: "runbox", Isolation: "isolated"}, nil, nil, "", "runbox"),
		Entry("a second provider on the host", []string{"runbox"}, Commands{State: CommandsAvailable, Addon: "runbox", Provider: "runbox", Isolation: "host"}, nil, nil, "", ""),
		Entry("a second provider with unknown isolation", []string{"runbox"}, Commands{State: CommandsAvailable, Addon: "runbox", Provider: "runbox"}, nil, nil, "", ""),
		Entry("the other provider runs the commands", []string{"runbox"}, Commands{State: CommandsAvailable, Addon: "sandbox", Provider: "sandbox", Isolation: "isolated"}, nil, nil, "", ""),
		Entry("existing default tie", []string{"sandbox", "office"}, Commands{State: CommandsAvailable, Addon: "sandbox", Provider: "sandbox", Isolation: "isolated"}, nil, nil, "", "office"),
	)
	It("describes selected commands without recursive tool building or execution, including subagent final pruning", func() {
		ctx := context.Background()
		svc, _ := newSkillService(&skillBroker{})
		b := &documentBroker{skillBroker: &skillBroker{}, isolation: "isolated", defs: []AddonTool{{AddonID: "sandbox", Name: "addon__sandbox__exec", Shadows: "bash", Execution: sandboxExecution, Schema: json.RawMessage(`{"type":"object"}`)}}}
		svc.d.AddonTools = b
		rec, err := svc.Create(ctx, "alice", CreateInput{Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		c := documentCommands(svc, ctx, "alice", rec, nil)
		Expect(isolatedProvider(c, "sandbox")).To(BeTrue())
		Expect(b.calls).To(BeNumerically("<=", 3))
		rec.Kind = session.KindSubAgent
		Expect(isolatedProvider(documentCommands(svc, ctx, "alice", rec, nil), "sandbox")).To(BeFalse())
		svc.allowed[key("alice", rec.ID)] = []session.Tool{{Name: "bash", Provider: "sandbox"}}
		Expect(isolatedProvider(documentCommands(svc, ctx, "alice", rec, nil), "sandbox")).To(BeTrue())
		Expect(isolatedProvider(documentCommands(svc, ctx, "alice", rec, &scope{deleted: true}), "sandbox")).To(BeFalse())
	})
	DescribeTable("uses saved-agent subagent grants for the index and loader", func(granted bool) {
		ctx := context.Background()
		b := &documentBroker{skillBroker: &skillBroker{}, isolation: "isolated", defs: []AddonTool{{AddonID: "sandbox", Name: "addon__sandbox__exec", Shadows: "bash", Execution: sandboxExecution}}}
		svc, f := newSkillService(b.skillBroker)
		svc.d.AddonTools = b
		agentsStore, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		svc.d.Agents = agentsStore
		Expect(f.SyncAddonSkills(ctx, "sandbox", fstest.MapFS{"docx/SKILL.md": {Data: skillMD("docx", "Sandbox document.", "Sandbox body.", "requires: [exec]")}})).To(Succeed())
		b.set(AddonSkillSet{AddonID: "sandbox", Skills: []string{"docx"}, Shadows: []string{"docx"}, Isolated: []string{"docx"}, Running: true})
		grants := []string{}
		if granted {
			grants = []string{"sandbox"}
		}
		agent, err := svc.SaveAgent(ctx, "alice", agents.Agent{Name: "Document worker", Model: "m", Addons: grants, Skills: []string{"docx"}})
		Expect(err).NotTo(HaveOccurred())
		parent, err := svc.Create(ctx, "alice", CreateInput{Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		// Persist the same saved-agent child record as spawn, without a model
		// turn. Such children deliberately have no entry in s.allowed.
		rec := session.ChatRecord{ID: "document-child", Principal: "alice", Model: "m", Kind: session.KindSubAgent, ParentID: parent.ID, AgentID: agent.ID}
		Expect(svc.d.Store.CreateChat(ctx, rec)).To(Succeed())
		Expect(svc.allowed).NotTo(HaveKey(key("alice", rec.ID)))
		x, err := svc.ChatAccess(ctx, "alice", rec.ID)
		Expect(err).NotTo(HaveOccurred())
		turn, err := svc.chatTools(ctx, x)
		Expect(err).NotTo(HaveOccurred())
		Expect(hasTool(turn.Tools, "bash")).To(Equal(granted))
		Expect(strings.Contains(turn.Prompt, "- docx: Sandbox document.")).To(Equal(granted))
		picker, _, err := svc.Skills(ctx, "alice", rec.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(len(skills.Usable(picker)) > 0).To(Equal(granted))
		if !granted {
			Expect(hasTool(turn.Tools, "load_skill")).To(BeFalse())
			return
		}
		Expect(hasTool(turn.Tools, "load_skill")).To(BeTrue(), "saved-agent Sandbox commands and index require a real loader")
		var loader session.Tool
		for _, tool := range turn.Tools {
			if tool.Name == "load_skill" {
				loader = tool
			}
		}
		body, err := loader.Run(ctx, json.RawMessage(`{"name":"docx"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(body).To(ContainSubstring("Sandbox body."))
		// A previously constructed loader must not retain a revoked grant.
		agent.Addons = nil
		Expect(agentsStore.Put(ctx, agent)).To(Succeed())
		body, err = loader.Run(ctx, json.RawMessage(`{"name":"docx"}`))
		Expect(err).To(HaveOccurred())
		Expect(body).NotTo(ContainSubstring("Sandbox body."))
		turn, err = svc.chatTools(ctx, x)
		Expect(err).NotTo(HaveOccurred())
		Expect(hasTool(turn.Tools, "bash")).To(BeFalse())
		Expect(hasTool(turn.Tools, "load_skill")).To(BeFalse())
		Expect(turn.Prompt).NotTo(ContainSubstring("- docx: Sandbox document."))
		picker, _, err = svc.Skills(ctx, "alice", rec.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(skills.Usable(picker)).To(BeEmpty())
		saved, err := agentsStore.Get(ctx, "alice", agent.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(saved.Addons).To(BeEmpty())
		Expect(saved.Skills).To(Equal([]string{"docx"}))
	}, Entry("permitted then revoked Sandbox", true), Entry("absent Sandbox grant", false))

	It("says why a document skill is missing when the status cannot be read after the tool was offered", func() {
		ctx := context.Background()
		b := &documentBroker{skillBroker: &skillBroker{}, isolation: "isolated", defs: []AddonTool{{AddonID: "sandbox", Name: "addon__sandbox__exec", Shadows: "bash", Execution: sandboxExecution}}}
		svc, f := newSkillService(b.skillBroker)
		svc.d.AddonTools = b
		agentsStore, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		svc.d.Agents = agentsStore
		Expect(f.SyncAddonSkills(ctx, "sandbox", fstest.MapFS{"pptx/SKILL.md": {Data: skillMD("pptx", "Sandbox slides.", "Slides body.", "requires: [exec]")}})).To(Succeed())
		b.set(AddonSkillSet{AddonID: "sandbox", Skills: []string{"pptx"}, Shadows: []string{"pptx"}, Isolated: []string{"pptx"}, Running: true})
		agent, err := svc.SaveAgent(ctx, "alice", agents.Agent{Name: "Slides", Model: "m", Addons: []string{"sandbox"}, Skills: []string{"pptx"}})
		Expect(err).NotTo(HaveOccurred())
		parent, err := svc.Create(ctx, "alice", CreateInput{Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		rec := session.ChatRecord{ID: "slides-child", Principal: "alice", Model: "m", Kind: session.KindSubAgent, ParentID: parent.ID, AgentID: agent.ID}
		Expect(svc.d.Store.CreateChat(ctx, rec)).To(Succeed())
		x, err := svc.ChatAccess(ctx, "alice", rec.ID)
		Expect(err).NotTo(HaveOccurred())
		turn, err := svc.chatTools(ctx, x)
		Expect(err).NotTo(HaveOccurred())
		var loader session.Tool
		for _, tool := range turn.Tools {
			if tool.Name == "load_skill" {
				loader = tool
			}
		}
		Expect(loader.Run).NotTo(BeNil(), "the tool is offered while the status reads as isolated")
		// The status can no longer be read when the model calls the tool.
		b.isolation = ""
		_, err = loader.Run(ctx, json.RawMessage(`{"name":"pptx"}`))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).NotTo(ContainSubstring("has no skills now"))
		Expect(err.Error()).To(ContainSubstring("need isolated command execution by Sandbox; its status could not be read"))
	})
	It("opens a scoped chat with load_skill without reentering session access resolution", func() {
		ctx := context.Background()
		// A stub harness: the spec checks that Open builds the chat tools
		// without waiting on itself, not that nib can start a session. The
		// real harness takes about a second under -race, and a loaded CI
		// runner pushed it past the two second budget this spec once had.
		svc, f := newSkillServiceWith(&skillBroker{}, studiotest.NewGateHarness())
		store, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		svc.d.Agents = store
		_, err = f.Store().Mkdir(ctx, f.Actor("alice"), session.HomePath("alice")+"/Work")
		Expect(err).NotTo(HaveOccurred())
		agent, err := svc.SaveAgentWithFolder(ctx, "alice", agents.Agent{Name: "Scoped", Model: "m"}, &AgentFolderWrite{Path: "/Work", Mode: agents.FolderRead})
		Expect(err).NotTo(HaveOccurred())
		Expect(f.SyncAddonSkills(ctx, "office", fstest.MapFS{"docx/SKILL.md": {Data: md("docx", "Office document.")}})).To(Succeed())
		rec, err := svc.Create(ctx, "alice", CreateInput{Model: "m", AgentID: agent.ID})
		Expect(err).NotTo(HaveOccurred())
		x, err := svc.ChatAccess(ctx, "alice", rec.ID)
		Expect(err).NotTo(HaveOccurred())
		_, fenced := x.Actor().Fence()
		Expect(fenced).To(BeTrue())
		// Open primes tools under the manager's open lock. No turn or model
		// inference is needed to exercise load_skill construction.
		done := make(chan error, 1)
		go func() {
			_, err := svc.mgr.Open(ctx, x, rec.ID)
			done <- err
		}()
		select {
		case err := <-done:
			Expect(err).NotTo(HaveOccurred())
		case <-time.After(30 * time.Second):
			// A re-entrant resolution blocks on the open lock for good, so
			// any finite bound tells it from a slow runner; keep it far
			// above the cost of an Open.
			// Fail without cleanup attempting to reacquire the stuck lock.
			// The test process timeout also bounds teardown on a regression.
			Fail("scoped chat setup reentered session access resolution")
		}
	})

	It("refuses another user's chat and access errors rather than using the whole home", func() {
		ctx := context.Background()
		svc, _ := newSkillService(&skillBroker{})
		b := &documentBroker{skillBroker: &skillBroker{}, isolation: "isolated", defs: []AddonTool{{AddonID: "sandbox", Name: "addon__sandbox__exec", Shadows: "bash", Execution: sandboxExecution}}}
		svc.d.AddonTools = b
		rec, err := svc.Create(ctx, "alice", CreateInput{Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		c, err := svc.addonCommandDescriptor(ctx, "bob", rec, nil)
		Expect(err).To(HaveOccurred())
		Expect(isolatedProvider(c, "sandbox")).To(BeFalse())
		Expect(b.calls).To(BeZero())
		rec.ID = "missing"
		c, err = svc.addonCommandDescriptor(ctx, "alice", rec, nil)
		Expect(err).To(HaveOccurred())
		Expect(isolatedProvider(c, "sandbox")).To(BeFalse())
		Expect(b.calls).To(BeZero())
		// Only the genuinely chatless picker may describe person capabilities.
		Expect(isolatedProvider(documentCommands(svc, ctx, "alice", session.ChatRecord{}, nil), "sandbox")).To(BeTrue())
	})

	It("keeps picker, slash selection, index and fresh loads aligned across readiness and final pruning", func() {
		ctx := context.Background()
		b := &documentBroker{skillBroker: &skillBroker{}, isolation: "isolated", defs: []AddonTool{{AddonID: "sandbox", Name: "addon__sandbox__exec", Shadows: "bash", Execution: sandboxExecution}}}
		svc, f := newSkillService(b.skillBroker)
		svc.d.AddonTools = b
		Expect(f.SyncAddonSkills(ctx, "sandbox", fstest.MapFS{"docx/SKILL.md": {Data: skillMD("docx", "Sandbox document.", "Sandbox body.", "requires: [exec]")}})).To(Succeed())
		b.set(AddonSkillSet{AddonID: "sandbox", Skills: []string{"docx"}, Shadows: []string{"docx"}, Isolated: []string{"docx"}, Running: true})
		rec, err := svc.Create(ctx, "alice", CreateInput{Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		a := userChatActor("alice", rec.ID)
		tools := svc.fitLoadSkill(a, []session.Tool{{Name: "load_skill"}, {Name: "bash", Provider: "sandbox"}, {Name: "read_file"}})
		args := json.RawMessage(`{"name":"docx"}`)
		check := func(want bool) {
			picker, _, err := svc.Skills(ctx, "alice", rec.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(skills.Usable(picker)) > 0).To(Equal(want))
			slash, err := svc.chatSkills(ctx, "alice", rec)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(skills.Usable(slash)) > 0).To(Equal(want))
			command := documentCommands(svc, ctx, "alice", rec, nil)
			index, _, _, _, err := svc.skillTurn(ctx, "alice", nil, nil, nil, command, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.Contains(index, "docx")).To(Equal(want))
			body, err := tools[0].Run(ctx, args)
			if want {
				Expect(err).NotTo(HaveOccurred())
			} else {
				Expect(err).To(HaveOccurred())
			}
			Expect(strings.Contains(body, "Sandbox body.")).To(Equal(want))
		}
		check(true)
		b.reason = "guest not ready"
		check(false) // A previously built loader must recheck readiness.
		b.reason = ""
		check(true)
		pruned := svc.fitLoadSkill(a, []session.Tool{{Name: "load_skill"}, {Name: "read_file"}})
		body, err := pruned[0].Run(ctx, args)
		Expect(err).To(HaveOccurred())
		Expect(body).NotTo(ContainSubstring("Sandbox body."))

		// A cached loader and every descriptor/picker must fail closed when
		// the authoritative owner policy cannot be refreshed, not pick Office
		// or host execution as an alternative to the pinned Sandbox provider.
		unavailable := errors.New("owner policy unavailable")
		svc.d.ToolPins = func(context.Context) (map[string]string, error) { return nil, unavailable }
		command, err := svc.addonCommandDescriptor(ctx, "alice", rec, nil)
		Expect(errors.Is(err, unavailable)).To(BeTrue())
		Expect(isolatedProvider(command, "sandbox")).To(BeFalse())
		_, err = svc.CommandRunner(ctx, "alice", rec.ID)
		Expect(errors.Is(err, unavailable)).To(BeTrue())
		_, _, err = svc.Skills(ctx, "alice", rec.ID)
		Expect(errors.Is(err, unavailable)).To(BeTrue())
		_, err = svc.chatSkills(ctx, "alice", rec)
		Expect(errors.Is(err, unavailable)).To(BeTrue())
		_, err = svc.AdminBuiltinSkills(ctx, "alice")
		Expect(errors.Is(err, unavailable)).To(BeTrue())
		body, err = tools[0].Run(ctx, args)
		Expect(errors.Is(err, unavailable)).To(BeTrue())
		Expect(body).NotTo(ContainSubstring("Sandbox body."))
	})

})

func documentCommands(s *Service, ctx context.Context, p string, rec session.ChatRecord, sc *scope) Commands {
	c, err := s.addonCommandDescriptor(ctx, p, rec, sc)
	Expect(err).NotTo(HaveOccurred())
	return c
}

// chatSkillNotes resolves the skills of a chat as the turn does and returns
// the notes it gets.
func chatSkillNotes(svc *Service, ctx context.Context, off []string, command Commands) ([]skills.Skill, []string, error) {
	ctx, closeScope := svc.executionScope(ctx, "alice", "c1")
	defer closeScope()
	list, notes, _, err := svc.chatSkillSet(ctx, "alice", nil, off, nil, command)
	return list, notes, err
}

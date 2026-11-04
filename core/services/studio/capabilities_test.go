package studio_test

import (
	"context"
	"github.com/mudler/LocalAI/core/services/studio/executionrequest"
	model "github.com/mudler/LocalAI/pkg/addons/model/executionstatus"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// sandboxExecution is how the Sandbox add-on declares its commands tool.
var sandboxExecution = &contract.ExecutionDescriptor{Class: contract.ExecutionClassCommands, Title: "Sandbox", Note: sandbox.GuestToolsNote}

// runAddon is the Sandbox add-on as the broker lists it: bash runs commands.
func runAddon() []studio.AddonTool {
	bash := addonTool("sandbox", "bash")
	bash.Action, bash.Shadows, bash.Execution = true, "bash", sandboxExecution
	read := addonTool("sandbox", "read")
	read.Action = true
	return []studio.AddonTool{bash, read}
}

// isolatedAddons is a broker that says how its runner isolates commands.
type isolatedAddons struct {
	*fakeAddons
	mode string
}

func (i isolatedAddons) Isolation(context.Context, string, string) string { return i.mode }

// writeExecSkill writes a shared skill that needs a way to run commands.
func writeExecSkill(f *files.Service, name string) {
	Expect(f.EnsureShared(context.Background())).To(Succeed())
	body := "---\nname: " + name + "\ndescription: Work with " + name + " files.\nrequires: [exec]\n---\nRun python.\n"
	_, err := f.Store().Put(context.Background(), workspace.Actor{Principal: files.AdminPrincipal}, "/skills/"+name+"/SKILL.md",
		strings.NewReader(body), workspace.PutOptions{Overwrite: true})
	Expect(err).NotTo(HaveOccurred())
}

// newCapsChat is a chat service with files and the given add-on broker.
func newCapsChat(broker studio.AddonTools, script func(workspace.Actor, string) []studiotest.ToolStep) (*studio.Service, *studiotest.ToolHarness, *files.Service) {
	f := newFiles()
	if script == nil {
		script = func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: "ok"}} }
	}
	svc, h := newToolService(script, func(d *studio.Deps) {
		d.Files = f
		if broker != nil {
			d.AddonTools = broker
		}
	})
	return svc, h, f
}

func turnPrompt(svc *studio.Service, h *studiotest.ToolHarness, id string) string {
	sent(svc, alice, id, "hello")
	idle(svc, alice, id)
	return h.TurnPrompt(id)
}

var _ = Describe("capabilities", func() {
	It("tells a chat with no way to run commands what it can do instead", func() {
		svc, h, f := newCapsChat(nil, nil)
		writeExecSkill(f, "xlsx")
		id := create(svc, alice)
		prompt := turnPrompt(svc, h, id)

		Expect(prompt).To(ContainSubstring("Commands: nothing in this chat runs code or commands."))
		Expect(prompt).To(ContainSubstring("Never claim or imply that you ran anything"))
		Expect(prompt).To(ContainSubstring("do not ask a sub-agent to run it"))
		Expect(prompt).To(ContainSubstring("write the output directly with write_file"))
		Expect(prompt).To(ContainSubstring("give the user the script to run"))
		Expect(prompt).To(ContainSubstring("an administrator can enable running code by installing an add-on that runs commands"))
		Expect(prompt).To(ContainSubstring("A skill marked unavailable here needs this"))
		Expect(prompt).To(ContainSubstring("- xlsx: Work with xlsx files. (unavailable here: needs command execution)"))
		Expect(prompt).NotTo(ContainSubstring("/workspace/shared-skills/xlsx/SKILL.md"), "an unavailable skill must not give the path to start it")
		Expect(svc.CommandRunner(context.Background(), alice, id)).To(Equal(studio.Commands{State: studio.CommandsNone}))
	})

	It("keeps the block short and leaves out the skill line when no skill needs commands", func() {
		svc, h, _ := newCapsChat(nil, nil)
		id := create(svc, alice)
		prompt := turnPrompt(svc, h, id)
		var block string
		for part := range strings.SplitSeq(prompt, "\n\n") {
			if strings.HasPrefix(part, "Commands:") {
				block = part
			}
		}
		Expect(block).NotTo(BeEmpty(), "no Commands block in:\n%s", prompt)
		Expect(block).NotTo(ContainSubstring("skill"))
		// About 60 tokens: a word is more than one token on average, so count words.
		Expect(len(strings.Fields(block))).To(BeNumerically("<=", 70), "the block is too long: %q", block)
	})

	It("names the tool that runs commands and lists the skills as usable", func() {
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: runAddon()}}
		svc, h, f := newCapsChat(isolatedAddons{fakeAddons: fa, mode: "isolated"}, nil)
		writeExecSkill(f, "xlsx")
		id := create(svc, alice)
		prompt := turnPrompt(svc, h, id)

		Expect(prompt).To(ContainSubstring("Commands: bash runs commands and code in an isolated environment (Sandbox)."))
		Expect(prompt).To(ContainSubstring(sandbox.GuestToolsNote), "the isolated sandbox lists its programs")
		Expect(prompt).To(ContainSubstring("Skill folders are not visible in the isolated environment: load skills with load_skill."))
		Expect(prompt).NotTo(ContainSubstring("nothing in this chat runs code"))
		Expect(prompt).To(ContainSubstring("\n- xlsx: "))
		Expect(prompt).NotTo(ContainSubstring("/workspace/shared-skills/xlsx/SKILL.md"), "the index names no path")
		Expect(prompt).NotTo(ContainSubstring("unavailable here"))
		Expect(svc.CommandRunner(context.Background(), alice, id)).To(Equal(studio.Commands{State: studio.CommandsAvailable, Tool: "bash", Isolation: "isolated", Addon: "sandbox", Provider: "sandbox", Title: "Sandbox", Note: sandbox.GuestToolsNote}))
	})

	It("warns when commands run on the server with no isolation", func() {
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: runAddon()}}
		svc, h, _ := newCapsChat(isolatedAddons{fakeAddons: fa, mode: "host"}, nil)
		id := create(svc, alice)
		Expect(turnPrompt(svc, h, id)).To(ContainSubstring("Commands: bash runs commands directly on the server, with no isolation."))
		Expect(turnPrompt(svc, h, id)).NotTo(ContainSubstring(sandbox.GuestToolsNote), "the host has its own programs, not the guest's")
		Expect(turnPrompt(svc, h, id)).NotTo(ContainSubstring("Skill folders are not visible"), "only an isolated chat has the hint")
	})

	It("says the Sandbox is off when the chat switched it off", func() {
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: runAddon()}}
		svc, h, _ := newCapsChat(fa, nil)
		id := create(svc, alice)
		Expect(svc.SetAddonsOff(context.Background(), alice, id, []string{"sandbox"})).To(Succeed())
		prompt := turnPrompt(svc, h, id)
		Expect(prompt).To(ContainSubstring("nothing in this chat runs code or commands"))
		Expect(prompt).To(ContainSubstring("they can switch the Sandbox add-on on in the Tools menu of this chat"))
		Expect(svc.CommandRunner(context.Background(), alice, id)).To(Equal(studio.Commands{State: studio.CommandsOff, Addon: "sandbox", Title: "Sandbox"}))
	})

	It("says the Sandbox cannot run when no backend works", func() {
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: runAddon()}}
		gate := gatedAddons{fakeAddons: fa, reasons: map[string]string{"sandbox": "no sandbox backend works on this host"}}
		svc, h, _ := newCapsChat(gate, nil)
		id := create(svc, alice)
		prompt := turnPrompt(svc, h, id)
		Expect(prompt).To(ContainSubstring("the Sandbox add-on cannot run commands on this server"))
		Expect(svc.CommandRunner(context.Background(), alice, id)).To(Equal(studio.Commands{State: studio.CommandsUnavailable, Reason: "no sandbox backend works on this host", Addon: "sandbox", Title: "Sandbox"}))
	})

	It("does not call the add-on off for a user who cannot use it", func() {
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: runAddon()}}
		svc, h, _ := newCapsChat(fa, nil)
		a := create(svc, alice)
		_ = turnPrompt(svc, h, a) // the service has now seen the Sandbox
		b := create(svc, "bob")
		Expect(svc.SetAddonsOff(context.Background(), "bob", b, []string{"sandbox"})).To(Succeed())
		Expect(svc.CommandRunner(context.Background(), "bob", b)).To(Equal(studio.Commands{State: studio.CommandsNone}))
	})

	It("refuses a sub-agent task that asks to run something when nothing runs commands", func() {
		svc, h, _ := newCapsChat(nil, func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "go" {
				return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"Actually execute the script /workspace/home/make_deck.py and report the output","wait":true}`}, {Say: "done"}}
			}
			return []studiotest.ToolStep{{Say: "ran it"}}
		})
		id := create(svc, alice)
		sent(svc, alice, id, "go")
		idle(svc, alice, id)
		res := h.ResultsFor("spawn_agent")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).To(MatchError(studio.ErrSubAgentCannotRun))
		Expect(res[0].Err.Error()).To(ContainSubstring("the sub-agent has no way to run commands either"))
		Expect(childrenOf(svc, alice, id)).To(BeEmpty(), "no sub-agent may start for a task it cannot do")
	})

	It("starts a sub-agent that asks to run something when the chat can run commands", func() {
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: runAddon()}}
		svc, h, _ := newCapsChat(fa, func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "go" {
				return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"Run python3 make_deck.py","wait":true}`}, {Say: "done"}}
			}
			return []studiotest.ToolStep{{Say: "built"}}
		})
		id := create(svc, alice)
		sent(svc, alice, id, "go")
		idle(svc, alice, id)
		res := h.ResultsFor("spawn_agent")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).NotTo(HaveOccurred())
		Expect(res[0].Out).To(ContainSubstring("built"))
		kids := childrenOf(svc, alice, id)
		Expect(kids).To(HaveLen(1))
		// A sub-agent that can run commands needs no commands block; it is told
		// where its parent keeps working files.
		Expect(h.TurnPrompt(kids[0].ID)).NotTo(ContainSubstring("Commands:"), "a sub-agent that can run commands needs no block")
		Expect(h.TurnPrompt(kids[0].ID)).To(HavePrefix("Working files:"))
	})

	It("tells a sub-agent without a way to run commands not to pretend", func() {
		svc, h, _ := newCapsChat(nil, func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "go" {
				return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"summarize the notes","wait":true}`}, {Say: "done"}}
			}
			return []studiotest.ToolStep{{Say: "summary"}}
		})
		id := create(svc, alice)
		sent(svc, alice, id, "go")
		idle(svc, alice, id)
		kids := childrenOf(svc, alice, id)
		Expect(kids).To(HaveLen(1))
		Expect(h.TurnPrompt(kids[0].ID)).To(ContainSubstring("Commands: nothing here runs code or commands."))
	})

	It("refuses the same sub-agent task again while no file of the chat changed", func() {
		svc, h, _ := newCapsChat(nil, func(_ workspace.Actor, text string) []studiotest.ToolStep {
			switch text {
			case "ask":
				return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"Check  the numbers in q1.csv","wait":true}`}, {Say: "asked"}}
			case "ask again":
				return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"check the numbers in q1.csv","wait":true}`}, {Say: "asked"}}
			case "write":
				return []studiotest.ToolStep{{Tool: "write_file", Args: `{"path":"/q1.csv","content":"a,b\n1,2\n"}`}, {Say: "written"}}
			}
			return []studiotest.ToolStep{{Say: "the numbers add up"}}
		})
		id := create(svc, alice)
		for _, msg := range []string{"ask", "ask again", "write", "ask again"} {
			sent(svc, alice, id, msg)
			idle(svc, alice, id)
		}
		res := h.ResultsFor("spawn_agent")
		Expect(res).To(HaveLen(3))
		Expect(res[0].Err).NotTo(HaveOccurred())
		Expect(res[1].Err).To(MatchError(studio.ErrSubAgentRepeat), "the same task with no new file must be refused")
		Expect(res[2].Err).NotTo(HaveOccurred(), "a file changed since the first run, so the task may run again")
		Expect(childrenOf(svc, alice, id)).To(HaveLen(2))
	})
})

// presentationAddons supplies an admitted request just as the product broker does.
type presentationAddons struct {
	*fakeAddons
	bound bool
}

func (p presentationAddons) NewExecutionObservation(ctx context.Context, user, conv string) (*executionrequest.Request, string, func() studio.ExecutionDetail) {
	if !p.bound {
		return nil, "", nil
	}
	b := model.Binding{Actor: user, Addon: "sandbox", Generation: "27", Incarnation: "runtime-two", ExecutionTool: "bash", StatusTool: "status"}
	r := executionrequest.New(ctx, b, "q", model.Eligible, executionrequest.IO{
		Validate: func(context.Context, model.Binding) error { return nil },
		Read: func(context.Context, model.Binding, model.ObservationID, time.Time) (model.Payload, error) {
			return model.ValidPayload(model.Value{Readiness: model.Available, Isolation: model.Host}), nil
		},
	}, nil)
	Expect(r.Binding()).To(Equal(b))
	return r, b.Addon, func() studio.ExecutionDetail { return studio.ExecutionDetail{Explanation: "plain explanation"} }
}

var _ = Describe("execution presentation DTO", func() {
	It("returns admitted binding identity through CommandRunner and no identity without admission", func() {
		for _, bound := range []bool{true, false} {
			broker := presentationAddons{fakeAddons: &fakeAddons{tools: map[string][]studio.AddonTool{alice: runAddon()}}, bound: bound}
			svc, _, _ := newCapsChat(broker, nil)
			id := create(svc, alice)
			c, err := svc.CommandRunner(context.Background(), alice, id)
			Expect(err).NotTo(HaveOccurred())
			Expect(c.ExecutionStatus).NotTo(BeNil())
			if bound {
				Expect(c.ExecutionStatus.Generation).To(Equal("27"))
				Expect(c.ExecutionStatus.Incarnation).To(Equal("runtime-two"))
				Expect(c.ExecutionStatus.Readiness).To(Equal("available"))
			} else {
				Expect(c.ExecutionStatus.Generation).To(BeEmpty())
				Expect(c.ExecutionStatus.Readiness).To(Equal("unknown"))
			}
		}
	})
})

var _ = Describe("the execution status of a chat that does not exist yet", func() {
	It("says the chat is a draft, not that the add-on is not selected", func() {
		broker := presentationAddons{fakeAddons: &fakeAddons{tools: map[string][]studio.AddonTool{alice: runAddon()}}, bound: true}
		svc, _, _ := newCapsChat(broker, nil)
		_, c, err := svc.DraftAddons(context.Background(), alice, "m", "", studio.Switches{})
		Expect(err).NotTo(HaveOccurred())
		Expect(c.ExecutionStatus).NotTo(BeNil())
		Expect(c.ExecutionStatus.UnknownReason).To(Equal("draft"))
	})
})

// observingAddons reads the execution status in Unavailable, as the real
// gateway does, so a call outside an execution scope trips the guard.
type observingAddons struct{ presentationAddons }

func (b observingAddons) Unavailable(ctx context.Context, user, addon string, _ bool) string {
	studio.ExecutionObservation(ctx, user, addon)
	return ""
}

var _ = Describe("the commands state of a draft with switches", func() {
	broker := func() presentationAddons {
		return presentationAddons{fakeAddons: &fakeAddons{tools: map[string][]studio.AddonTool{alice: runAddon()}}, bound: true}
	}
	It("is not switched off without switches", func() {
		svc, _, _ := newCapsChat(broker(), nil)
		_, c, err := svc.DraftAddons(context.Background(), alice, "m", "", studio.Switches{})
		Expect(err).NotTo(HaveOccurred())
		Expect(c.State).NotTo(Equal(studio.CommandsSwitchedOff))
	})
	It("is switched off when the draft switched the shell group off", func() {
		svc, _, _ := newCapsChat(broker(), nil)
		_, c, err := svc.DraftAddons(context.Background(), alice, "m", "", studio.Switches{BuiltinsOff: []string{"shell"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(c.State).To(Equal(studio.CommandsSwitchedOff))
	})
	It("lists an add-on the draft switched off as off, and says the commands are off", func() {
		svc, _, _ := newCapsChat(broker(), nil)
		list, c, err := svc.DraftAddons(context.Background(), alice, "m", "", studio.Switches{AddonsOff: []string{"sandbox"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(list).NotTo(BeEmpty())
		for _, a := range list {
			Expect(a.Off).To(BeTrue(), a.ID)
		}
		Expect(c.State).To(Equal(studio.CommandsOff))
	})
	It("refuses a group that does not exist", func() {
		svc, _, _ := newCapsChat(broker(), nil)
		_, _, err := svc.DraftAddons(context.Background(), alice, "m", "", studio.Switches{BuiltinsOff: []string{"nope"}})
		Expect(err).To(MatchError(studio.ErrBadBuiltinGroup))
	})
})

var _ = Describe("the draft lists of a chat that does not exist yet", func() {
	// The scopeless guard of this suite fails the spec when a draft path reads
	// the execution status outside an execution scope.
	It("reads the add-on status inside a scope for the tools list", func() {
		broker := observingAddons{presentationAddons{fakeAddons: &fakeAddons{tools: map[string][]studio.AddonTool{alice: runAddon()}}, bound: true}}
		svc, _, _ := newCapsChat(broker, nil)
		_, err := svc.DraftTools(context.Background(), alice, "m", "")
		Expect(err).NotTo(HaveOccurred())
	})
	It("reads the add-on status inside a scope for the tools of a stored chat", func() {
		broker := observingAddons{presentationAddons{fakeAddons: &fakeAddons{tools: map[string][]studio.AddonTool{alice: runAddon()}}, bound: true}}
		svc, _, _ := newCapsChat(broker, nil)
		_, err := svc.Tools(context.Background(), alice, create(svc, alice))
		Expect(err).NotTo(HaveOccurred())
	})
	It("reads the add-on status inside a scope for the add-on list", func() {
		broker := observingAddons{presentationAddons{fakeAddons: &fakeAddons{tools: map[string][]studio.AddonTool{alice: runAddon()}}, bound: true}}
		svc, _, _ := newCapsChat(broker, nil)
		_, _, err := svc.DraftAddons(context.Background(), alice, "m", "", studio.Switches{})
		Expect(err).NotTo(HaveOccurred())
	})
})

var _ = Describe("a second provider of commands", func() {
	runbox := func() []studio.AddonTool {
		bash := addonTool("runbox", "bash")
		bash.Action, bash.Shadows = true, "bash"
		bash.Execution = &contract.ExecutionDescriptor{Class: contract.ExecutionClassCommands, Title: "Runbox", Note: "Runbox has python3 and jq."}
		return []studio.AddonTool{bash}
	}
	It("is described by the title and the note it declares, not by a name known to the host", func() {
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: runbox()}}
		svc, h, _ := newCapsChat(isolatedAddons{fakeAddons: fa, mode: "isolated"}, nil)
		id := create(svc, alice)
		prompt := turnPrompt(svc, h, id)
		Expect(prompt).To(ContainSubstring("Commands: bash runs commands and code in an isolated environment (Runbox)."))
		Expect(prompt).To(ContainSubstring("Runbox has python3 and jq."))
		Expect(prompt).NotTo(ContainSubstring("Sandbox"))
		Expect(svc.CommandRunner(context.Background(), alice, id)).To(Equal(studio.Commands{State: studio.CommandsAvailable, Tool: "bash", Isolation: "isolated", Addon: "runbox", Provider: "runbox", Title: "Runbox", Note: "Runbox has python3 and jq."}))
	})
	It("is named when the chat switched it off and when it cannot run", func() {
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: runbox()}}
		svc, h, _ := newCapsChat(fa, nil)
		id := create(svc, alice)
		Expect(svc.SetAddonsOff(context.Background(), alice, id, []string{"runbox"})).To(Succeed())
		Expect(turnPrompt(svc, h, id)).To(ContainSubstring("they can switch the Runbox add-on on in the Tools menu of this chat"))

		gate := gatedAddons{fakeAddons: fa, reasons: map[string]string{"runbox": "no backend works"}}
		svc, h, _ = newCapsChat(gate, nil)
		id = create(svc, alice)
		prompt := turnPrompt(svc, h, id)
		Expect(prompt).To(ContainSubstring("the Runbox add-on cannot run commands on this server"))
		Expect(prompt).NotTo(ContainSubstring("Sandbox"))
	})
	It("behaves as an unnamed provider when its manifest declares no descriptor", func() {
		bash := addonTool("runbox", "bash")
		bash.Action, bash.Shadows = true, "bash"
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: {bash}}}
		svc, h, _ := newCapsChat(isolatedAddons{fakeAddons: fa, mode: ""}, nil)
		id := create(svc, alice)
		Expect(turnPrompt(svc, h, id)).To(ContainSubstring("Commands: bash runs commands and code."))
	})
})

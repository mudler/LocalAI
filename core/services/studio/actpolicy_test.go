package studio_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/tests/studiobot"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// syncBuffer is a log sink that the service and the spec can share.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

var _ = Describe("the action policy of an unattended run", func() {
	ctx := context.Background()
	const refused = "I did not run write_file"
	var (
		r    *connRig
		b    connectors.Binding
		logs *syncBuffer
	)
	// The model of every run reads the folder, then writes a file, then
	// reports. The spec reads what each tool answered.
	script := func(_ workspace.Actor, text string) []studiotest.ToolStep {
		if strings.Contains(text, "WRITE") {
			return []studiotest.ToolStep{
				{Tool: "list_files", Args: `{"path":"/"}`},
				{Tool: "write_file", Args: `{"path":"/report.txt","content":"x"}`},
				{Say: "finished"},
			}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}
	// setup binds a connection with the approval under test to a saved agent
	// that holds the given grants.
	setup := func(ap connectors.Approval, grants ...string) {
		logs = &syncBuffer{}
		sh := newShared()
		sh.deps = func(d *studio.Deps) { d.Logger = slog.New(slog.NewTextHandler(logs, nil)) }
		tr := studiobot.NewTransport(studiobot.Addon{ID: "chat-addon", Kind: contract.ConnectorKind{ID: "chatty", Title: "Chatty", Delivery: contract.DeliveryPull}, Runtime: &postAddon{}})
		r = newConnRigOpts(sh, connStore(), tr, script, nil, nil)
		ag, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Worker", Model: "m", Builtins: grants})
		Expect(err).NotTo(HaveOccurred())
		r.agent = ag
		gate := &capSwitch{}
		gate.set(studio.CapTriggersRun, true)
		r.svc.SetCapabilityGate(gate)
		b = r.bind(studio.ConnectorInput{Kind: "chatty", Token: "tok", Allow: []string{"11"}, Approval: &ap})
	}
	// run makes a trigger of the binding, lets it run once and returns what
	// write_file answered.
	var ended triggers.Run
	run := func(in studio.TriggerInput) (out string, err error) {
		in.Prompt, in.AgentID = "WRITE the report", r.agent.ID
		tr := r.schedule(in)
		r.due()
		end := r.waitEnded(tr.ID)
		ended = end
		Expect(end.Status).To(BeElementOf(triggers.StatusDone, triggers.StatusBlocked), "run = %+v", end)
		rs := r.h.ResultsFor("write_file")
		Expect(rs).To(HaveLen(1), "results = %+v", rs)
		Expect(r.h.ResultsFor("list_files")).To(HaveLen(1))
		Expect(r.h.ResultsFor("list_files")[0].Out).NotTo(ContainSubstring("I did not run"), "a read always runs")
		return rs[0].Out, rs[0].Err
	}

	It("refuses every non-read tool of a trigger made without asking by an auto_readonly binding, whatever it holds", func() {
		setup(connectors.Approval{Mode: connectors.ApprovalAutoReadonly}, "act.files")
		out, err := run(studio.TriggerInput{Acts: []string{"act.files"}, Route: triggers.Route{Binding: b.ID, Auto: true}})
		Expect(err).NotTo(HaveOccurred(), "a refusal is an answer, not an error")
		Expect(out).To(ContainSubstring(refused))
		Expect(out).To(ContainSubstring("read and report only"))
		Expect(logs.String()).To(And(ContainSubstring("unattended action"), ContainSubstring("tool=write_file"), ContainSubstring("allowed=false"), ContainSubstring("reason=read_only")))
	})

	It("treats a binding it cannot read as auto_readonly", func() {
		setup(connectors.Approval{}, "act.files")
		out, _ := run(studio.TriggerInput{Acts: []string{"act.files"}, Route: triggers.Route{Binding: "gone", Auto: true}})
		Expect(out).To(ContainSubstring(refused))
	})

	It("refuses a trigger that declared its runs only read, even when it holds act.files", func() {
		setup(connectors.Approval{}, "act.files")
		out, _ := run(studio.TriggerInput{Acts: []string{"act.files"}, Route: triggers.Route{Binding: b.ID, ReadOnly: true}})
		Expect(out).To(ContainSubstring(refused))
	})

	It("starts read only: a trigger with no acts cannot write, and the refusal names the missing grant", func() {
		setup(connectors.Approval{}, "act.files")
		out, _ := run(studio.TriggerInput{Route: triggers.Route{Binding: b.ID}})
		Expect(out).To(ContainSubstring(refused))
		Expect(out).To(ContainSubstring("act.files"))
		Expect(logs.String()).To(And(ContainSubstring("allowed=false"), ContainSubstring("reason=not_granted"), ContainSubstring("missing=act.files")))
	})

	It("ends a run as blocked when an action was refused for a missing grant, and names the grant", func() {
		setup(connectors.Approval{}, "act.files")
		run(studio.TriggerInput{Route: triggers.Route{Binding: b.ID}})
		Expect(ended.Status).To(Equal(triggers.StatusBlocked))
		Expect(ended.Reason).To(ContainSubstring("act.files"))
	})

	It("ends a run as done when its actions were allowed", func() {
		setup(connectors.Approval{}, "act.files")
		run(studio.TriggerInput{Acts: []string{"act.files"}, Route: triggers.Route{Binding: b.ID}})
		Expect(ended.Status).To(Equal(triggers.StatusDone))
	})

	It("lets a trigger write when the owner gave it act.files and the agent allows it", func() {
		setup(connectors.Approval{}, "act.files")
		out, err := run(studio.TriggerInput{Acts: []string{"act.files"}, Route: triggers.Route{Binding: b.ID}})
		Expect(err).NotTo(HaveOccurred())
		Expect(out).NotTo(ContainSubstring("I did not run"))
		Expect(logs.String()).To(And(ContainSubstring("tool=write_file"), ContainSubstring("allowed=true"), ContainSubstring("reason=granted")))
	})

	It("keeps the agent's grants as the ceiling of the trigger's acts", func() {
		setup(connectors.Approval{} /* the agent holds no act.* grant */)
		out, _ := run(studio.TriggerInput{Acts: []string{"act.files"}, Route: triggers.Route{Binding: b.ID}})
		Expect(out).To(ContainSubstring(refused))
	})

	It("lets a trigger write the file its plan names, as the owner saw on the card", func() {
		setup(connectors.Approval{}, "act.files")
		out, err := run(studio.TriggerInput{Writes: "report.txt", Route: triggers.Route{Binding: b.ID}})
		Expect(err).NotTo(HaveOccurred())
		Expect(out).NotTo(ContainSubstring("I did not run"))
	})

	It("keeps acts on an edit that leaves them out, clears them on an empty list, and refuses an unknown one", func() {
		setup(connectors.Approval{}, "act.files")
		tr := r.schedule(studio.TriggerInput{Prompt: "WRITE", AgentID: r.agent.ID, Acts: []string{"act.files"}})
		Expect(tr.Acts).To(Equal([]string{"act.files"}))
		edit := func(acts []string) (triggers.Trigger, error) {
			return r.host.UpdateTrigger(ctx, alice, tr.ID, studio.TriggerInput{Name: tr.Name, Prompt: tr.Prompt, Kind: tr.Kind,
				Schedule: tr.Schedule, TZ: tr.TZ, Model: tr.Model, AgentID: tr.AgentID, Enabled: true, Acts: acts})
		}
		up, err := edit(nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(up.Acts).To(Equal([]string{"act.files"}))
		up, err = edit([]string{})
		Expect(err).NotTo(HaveOccurred())
		Expect(up.Acts).To(BeEmpty())
		_, err = edit([]string{"act.nothing"})
		Expect(err).To(HaveOccurred())
	})
})

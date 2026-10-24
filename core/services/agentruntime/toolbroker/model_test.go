// SPDX-License-Identifier: MIT
package toolbroker

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestToolBroker(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "ToolBroker pure model") }
func fixture(policy PolicyMode) ModelConfig {
	b, err := NewBinding(Scope{"alice", "chat", "turn", 1, 1}, "request", "read", []byte(`{"secret":"sentinel"}`), Revisions{Catalog: "1", Config: "1", Authorization: "1", Resource: "1", Proposal: "1", ParentGrant: "1", Taint: "1"}, "provider-key")
	Expect(err).NotTo(HaveOccurred())
	return ModelConfig{Binding: b, Policy: policy, ApprovalTTL: 10}
}
func input(c ModelConfig, n int64) Input { return Input{Binding: c.Binding, Now: n} }
func admitted(c ModelConfig, n int64) AdmissionPassed {
	return AdmissionPassed{Input: input(c, n), Evidence: Admission{Current: true, Registered: true, Selected: true, Resource: true, Helper: true}}
}
func step(s State, e Event) Transition {
	t, err := Reduce(s, e)
	Expect(err).NotTo(HaveOccurred())
	Expect(CheckInvariants(t.State)).To(Succeed())
	Expect(CheckTransition(s, e, t)).To(Succeed())
	return t
}
func start(c ModelConfig) State     { return step(NewState(c), Requested{input(c, 1)}).State }
func authorize(c ModelConfig) State { return step(start(c), admitted(c, 1)).State }
func claim(c ModelConfig) State {
	s := authorize(c)
	s = step(s, ClaimRequested{input(c, 2)}).State
	s = step(s, PreparedObserved{Input: input(c, 2), Status: RecordPrepared}).State
	return step(s, DispatchGranted{Input: input(c, 2), Fresh: true}).State
}
func run(c ModelConfig, events []Event, rules []string, final string) []TraceStep {
	s := NewState(c)
	trace := []TraceStep{}
	for _, e := range events {
		t := step(s, e)
		trace = append(trace, NewTraceStep(s, e, t))
		s = t.State
	}
	got := []string{}
	for _, v := range trace {
		got = append(got, v.RuleID)
	}
	Expect(got).To(Equal(rules))
	Expect(PhaseName(s.Phase())).To(Equal(final))
	return trace
}

var _ = Describe("pure broker contract", func() {
	It("waits, binds human approval, dispatches once and journals before completion", func() {
		c := fixture(RequireApproval)
		i := input(c, 2)
		trace := run(c, []Event{Requested{input(c, 1)}, admitted(c, 1), ApprovalGranted{Input: i, Approval: Approval{Binding: c.Binding, Actor: HumanActor, ActorID: "human", ExpiresAt: 9}}, ClaimRequested{i}, PreparedObserved{Input: i, Status: RecordPrepared}, DispatchGranted{Input: i, Fresh: true}, InvokeRequested{i}, admitted(c, 2), ExecutionStarted{i}, ExecutionCompleted{Input: i, Output: NewOutput([]byte(`{"ok":true}`))}, JournalCompleted{i}}, []string{"TB-R01", "TB-R04", "TB-R08", "TB-R12", "TB-R12", "TB-R13", "TB-R14", "TB-R14", "TB-R14", "TB-R17", "TB-R17"}, "Completed")
		if dir := os.Getenv("TOOLBROKER_EVIDENCE"); dir != "" {
			Expect(WriteTraceViewer(dir, trace)).To(Succeed())
		}
	})
	It("denial cannot be freshened by a late approval", func() {
		c := fixture(RequireApproval)
		i := input(c, 2)
		run(c, []Event{Requested{input(c, 1)}, admitted(c, 1), ApprovalDenied{i}, ApprovalGranted{Input: i}}, []string{"TB-R01", "TB-R04", "TB-R09", "TB-R22"}, "Denied")
	})
	It("expiry boundary invalidates waiting approval", func() {
		c := fixture(RequireApproval)
		run(c, []Event{Requested{input(c, 1)}, admitted(c, 1), Expired{input(c, 11)}, ApprovalGranted{Input: input(c, 12)}}, []string{"TB-R01", "TB-R04", "TB-R10", "TB-R22"}, "Denied")
	})
	It("out of order dispatch and repeated invocation never run twice", func() {
		c := fixture(AllowAll)
		i := input(c, 2)
		run(c, []Event{DispatchGranted{Input: i, Fresh: true}, Requested{i}, admitted(c, 2), ClaimRequested{i}, PreparedObserved{Input: i, Status: RecordPrepared}, DispatchGranted{Input: i, Fresh: true}, InvokeRequested{i}, InvokeRequested{i}, admitted(c, 2), InvokeRequested{i}}, []string{"TB-R00", "TB-R01", "TB-R07", "TB-R12", "TB-R12", "TB-R13", "TB-R14", "TB-R21", "TB-R14", "TB-R00"}, "Executing")
	})
	It("dispatched timeout holds, recovery never redispatches, reconciliation observes completion", func() {
		c := fixture(AllowAll)
		i := input(c, 2)
		run(c, []Event{Requested{i}, admitted(c, 2), ClaimRequested{i}, PreparedObserved{Input: i, Status: RecordPrepared}, DispatchGranted{Input: i, Fresh: true}, ExecutionUncertain{i}, RecoveryObserved{Input: i, Status: RecordUnknown}, ClaimRequested{i}, RecoveryObserved{Input: i, Status: RecordCompleted, Output: NewOutput([]byte(`{"ok":true}`))}}, []string{"TB-R01", "TB-R07", "TB-R12", "TB-R12", "TB-R13", "TB-R18", "TB-R20", "TB-R22", "TB-R20"}, "Completed")
	})
	It("DenyContinue returns a denial without invocation", func() {
		c := fixture(DenyContinue)
		t := step(start(c), admitted(c, 1))
		Expect(t.RuleID).To(Equal("TB-R05"))
		Expect(PhaseName(t.State.Phase())).To(Equal("Denied"))
	})
	It("Stop interrupts rather than silently continuing", func() { c := fixture(Stop); Expect(step(start(c), admitted(c, 1)).RuleID).To(Equal("TB-R06")) })
	It("tainted writes wait even under AllowAll", func() {
		c := fixture(AllowAll)
		c.ExternalTaintedWrite = true
		Expect(step(start(c), admitted(c, 1)).RuleID).To(Equal("TB-R11"))
	})
	It("tainted write cannot accept host automation approval", func() {
		c := fixture(AllowAll)
		c.ExternalTaintedWrite = true
		s := authorize(c)
		t := step(s, ApprovalGranted{Input: input(c, 2), Approval: Approval{Binding: c.Binding, Actor: HostActor, ActorID: "host", ExpiresAt: 9}})
		Expect(PhaseName(t.State.Phase())).To(Equal("WaitingApproval"))
	})
	It("every approval binding component is exact", func() {
		c := fixture(RequireApproval)
		s := authorize(c)
		for _, b := range BindingMismatchWitnesses(c.Binding) {
			t := step(s, ApprovalGranted{Input: input(c, 2), Approval: Approval{Binding: b, Actor: HumanActor, ActorID: "human", ExpiresAt: 9}})
			Expect(PhaseName(t.State.Phase())).To(Equal("WaitingApproval"))
		}
	})
	It("approval is invalid exactly at its expiry", func() {
		c := fixture(RequireApproval)
		t := step(authorize(c), ApprovalGranted{Input: input(c, 9), Approval: Approval{Binding: c.Binding, Actor: HumanActor, ActorID: "human", ExpiresAt: 9}})
		Expect(t.RuleID).To(Equal("TB-R10"))
	})
	It("fresh ACL admission is mandatory after local claim consumption", func() {
		c := fixture(AllowAll)
		s := step(claim(c), InvokeRequested{input(c, 3)}).State
		t := step(s, AdmissionRejected{input(c, 3)})
		Expect(PhaseName(t.State.Phase())).To(Equal("Unknown"))
		Expect(t.RuleID).To(Equal("TB-R02"))
	})
	It("a fabricated fresh Dispatch without prepared intent grants nothing", func() {
		c := fixture(AllowAll)
		s := step(authorize(c), ClaimRequested{input(c, 2)}).State
		t := step(s, DispatchGranted{Input: input(c, 2), Fresh: true})
		Expect(t.RuleID).To(Equal("TB-R00"))
	})
	It("lease loss after dispatch prohibits invocation", func() {
		c := fixture(AllowAll)
		s := step(claim(c), LeaseLost{input(c, 3)}).State
		Expect(step(s, InvokeRequested{input(c, 3)}).RuleID).To(Equal("TB-R22"))
	})
	It("started body with failed result persistence is Unknown", func() {
		c := fixture(AllowAll)
		s := step(claim(c), InvokeRequested{input(c, 3)}).State
		s = step(s, admitted(c, 3)).State
		s = step(s, ExecutionCompleted{Input: input(c, 3), Output: NewOutput([]byte("private"))}).State
		Expect(PhaseName(step(s, JournalFailed{input(c, 3)}).State.Phase())).To(Equal("Unknown"))
	})
	It("stale completion cannot change current request", func() {
		c := fixture(AllowAll)
		bad := fixture(AllowAll)
		bad.Binding = BindingMismatchWitnesses(c.Binding)[0]
		t := step(claim(c), ExecutionCompleted{Input: input(bad, 3)})
		Expect(t.RuleID).To(Equal("TB-R19"))
	})
	It("completed journal observation reuses without executor", func() {
		c := fixture(AllowAll)
		s := step(authorize(c), ClaimRequested{input(c, 2)}).State
		t := step(s, PreparedObserved{Input: input(c, 2), Status: RecordCompleted, Output: NewOutput([]byte("private"))})
		Expect(t.RuleID).To(Equal("TB-R15"))
		Expect(t.Effects).To(HaveLen(1))
		Expect(t.Effects[0]).To(BeAssignableToTypeOf(ReturnRecordedResult{}))
	})
	It("dispatched journal observation never retries", func() {
		c := fixture(AllowAll)
		s := step(authorize(c), ClaimRequested{input(c, 2)}).State
		Expect(step(s, PreparedObserved{Input: input(c, 2), Status: RecordDispatched}).RuleID).To(Equal("TB-R16"))
	})
	It("state and byte accessors own private input and output", func() {
		a := []byte("secret")
		b, err := NewBinding(Scope{"p", "c", "t", 1, 1}, "r", "x", a, Revisions{}, "key")
		Expect(err).NotTo(HaveOccurred())
		a[0] = 'X'
		out := b.Arguments()
		out[0] = 'Y'
		Expect(string(b.Arguments())).To(Equal("secret"))
		o := NewOutput(a)
		a[0] = 'Z'
		Expect(string(o.Bytes())).To(Equal("Xecret"))
	})
	It("default formatting JSON and nested effects do not disclose secrets", func() {
		c := fixture(AllowAll)
		s := claim(c)
		for _, v := range []any{c, c.Binding, s, Requested{input(c, 2)}, step(s, InvokeRequested{input(c, 2)}), NewOutput([]byte("sentinel"))} {
			for _, f := range []string{"%v", "%+v", "%#v"} {
				Expect(fmt.Sprintf(f, v)).NotTo(ContainSubstring("sentinel"))
			}
			b, e := json.Marshal(v)
			Expect(e).NotTo(HaveOccurred())
			Expect(string(b)).NotTo(ContainSubstring("sentinel"))
		}
	})
	It("checks bounded reachable invariants liveness and all phase/event witnesses", func() {
		report, err := CheckBoundedModel()
		Expect(err).NotTo(HaveOccurred())
		Expect(report.Phases).To(HaveLen(11))
		Expect(report.Events).To(HaveLen(21))
		Expect(report.Pairs).To(Equal(11 * 21))
		fmt.Fprintln(GinkgoWriter, report.String())
	})
})

var _ = Describe("catalog and finish contracts", func() {
	spec := func(id ToolID) ToolSpec {
		return ToolSpec{ID: id, Name: string(id), Class: ReadTool, Revision: "1", Group: "files"}
	}
	It("rejects unknown malformed and duplicate catalog entries", func() {
		_, e := NewCatalog([]ToolSpec{spec("a"), spec("a")})
		Expect(e).To(HaveOccurred())
		_, e = NewCatalog([]ToolSpec{{ID: "a"}})
		Expect(e).To(HaveOccurred())
	})
	It("materializes group IDs without automatic expansion", func() {
		a, _ := NewCatalog([]ToolSpec{spec("a")})
		ids := a.GroupIDs("files")
		b, _ := NewCatalog([]ToolSpec{spec("a"), spec("b")})
		Expect(b.Effective(Selection{Selected: ids, ACL: []ToolID{"a", "b"}, Resources: []ToolID{"a", "b"}})).To(Equal([]ToolID{"a"}))
	})
	It("empty allow means no restriction only within explicit selection", func() {
		a, _ := NewCatalog([]ToolSpec{spec("a"), spec("b")})
		Expect(a.Effective(Selection{Selected: []ToolID{"a"}, ACL: []ToolID{"a", "b"}, Resources: []ToolID{"a", "b"}})).To(Equal([]ToolID{"a"}))
	})
	It("exclusions override allow including registered host controls", func() {
		s := spec("stop")
		s.Class = HostControlTool
		a, _ := NewCatalog([]ToolSpec{s})
		Expect(a.Effective(Selection{Selected: []ToolID{"stop"}, Allow: []ToolID{"stop"}, Exclude: []ToolID{"stop"}, ACL: []ToolID{"stop"}, Resources: []ToolID{"stop"}})).To(BeEmpty())
	})
	It("helper grants require exact host scope and intersect parent", func() {
		a, _ := NewCatalog([]ToolSpec{spec("a"), spec("b")})
		sc := Scope{"p", "c", "t", 1, 1}
		sel := Selection{Selected: []ToolID{"a", "b"}, ACL: []ToolID{"a", "b"}, Resources: []ToolID{"a", "b"}, Helper: &HelperGrant{Scope: sc, IDs: []ToolID{"a"}}, Scope: sc}
		Expect(a.Effective(sel)).To(Equal([]ToolID{"a"}))
		sel.Scope.Epoch = 2
		Expect(a.Effective(sel)).To(BeEmpty())
	})
	It("resource and artifact grants are separate current selections", func() {
		a, _ := NewCatalog([]ToolSpec{spec("a")})
		sel := Selection{Selected: []ToolID{"a"}, ACL: []ToolID{"a"}}
		Expect(a.Effective(sel)).To(BeEmpty())
		Expect(CanSaveArtifact(sel, "a")).To(BeFalse())
		sel.Resources = []ToolID{"a"}
		Expect(CanSaveArtifact(sel, "a")).To(BeFalse())
		sel.Artifacts = []ToolID{"a"}
		Expect(CanSaveArtifact(sel, "a")).To(BeTrue())
	})
	It("required finish is inactive when filtered and reminders are bounded", func() {
		g := NewFinishGate("a", []ToolID{}, 2)
		Expect(g.Active()).To(BeFalse())
		g = NewFinishGate("a", []ToolID{"a"}, 2)
		Expect(g.CanFinish()).To(BeFalse())
		g = g.Remind()
		g = g.Remind()
		Expect(g.Reminders()).To(Equal(uint(2)))
		Expect(g.Remind().Reminders()).To(Equal(uint(2)))
	})
	It("finish accepts matching completed top-level success not nested denial or unknown", func() {
		g := NewFinishGate("a", []ToolID{"a"}, 2)
		Expect(g.Observe("b", Completed{}, NewOutput([]byte(`{"ok":true}`))).CanFinish()).To(BeFalse())
		Expect(g.Observe("a", Unknown{}, NewOutput([]byte(`{"ok":true}`))).CanFinish()).To(BeFalse())
		Expect(g.Observe("a", Completed{}, NewOutput([]byte(`{"nested":{"ok":true}}`))).CanFinish()).To(BeFalse())
		Expect(g.Observe("a", Completed{}, NewOutput([]byte(`{"ok":true}`))).CanFinish()).To(BeTrue())
	})
	It("finish accepts documented MCP text wrapping only", func() {
		Expect(OutputSuccess(NewOutput([]byte(`{"content":[{"type":"text","text":"{\"ok\":true}"}]}`)))).To(BeTrue())
		Expect(OutputSuccess(NewOutput([]byte(`{"isError":true,"content":[{"type":"text","text":"{\"ok\":true}"}]}`)))).To(BeFalse())
	})
})

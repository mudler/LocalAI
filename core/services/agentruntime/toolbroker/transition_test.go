// SPDX-License-Identifier: MIT
package toolbroker

import (
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("independent transition gate", func() {
	invocation := func(c ModelConfig) State { return step(claim(c), InvokeRequested{input(c, 3)}).State }
	execution := func(c ModelConfig) State { return step(invocation(c), admitted(c, 3)).State }
	rejected := func(s State, e Event, t Transition) { Expect(CheckTransition(s, e, t)).To(MatchError(ErrInvariant)) }

	DescribeTable("rejects mismatched bindings on every effect", func(fx Effect) {
		c := fixture(AllowAll)
		s := invocation(c)
		e := admitted(c, 3)
		t := step(s, e)
		// RunExecutor has the correct source phase/event; other effects are injected
		// alongside it so rejection cannot rely on their own reducer construction.
		v := reflect.New(reflect.TypeOf(fx)).Elem()
		v.Set(reflect.ValueOf(fx))
		v.FieldByName("Binding").Set(reflect.ValueOf(BindingMismatchWitnesses(c.Binding)[0]))
		if _, ok := fx.(RunExecutor); ok {
			t.Effects = []Effect{v.Interface().(Effect)}
		} else {
			t.Effects = append(t.Effects, v.Interface().(Effect))
		}
		rejected(s, e, t)
	},
		Entry("CheckAdmission", CheckAdmission{}), Entry("RequestApproval", RequestApproval{}),
		Entry("RecordDecision", RecordDecision{}), Entry("PrepareJournal", PrepareJournal{}),
		Entry("DispatchJournal", DispatchJournal{}), Entry("RecheckExecutionAdmission", RecheckExecutionAdmission{}),
		Entry("RunExecutor", RunExecutor{}), Entry("CompleteJournal", CompleteJournal{}),
		Entry("CancelExecutor", CancelExecutor{}), Entry("ReturnRecordedResult", ReturnRecordedResult{}),
		Entry("ReturnDenied", ReturnDenied{}), Entry("HoldForReconciliation", HoldForReconciliation{}),
		Entry("NotifyInterrupted", NotifyInterrupted{}))

	DescribeTable("rejects execution with missing admission evidence", func(flag string) {
		c := fixture(AllowAll)
		s := invocation(c)
		e := admitted(c, 3)
		t := step(s, e)
		reflect.ValueOf(&e.Evidence).Elem().FieldByName(flag).SetBool(false)
		rejected(s, e, t)
	}, Entry("Current", "Current"), Entry("Registered", "Registered"), Entry("Selected", "Selected"), Entry("Resource", "Resource"), Entry("Helper", "Helper"))

	It("rejects CompleteJournal in the wrong phase", func() {
		c := fixture(AllowAll)
		s := NewState(c)
		e := Requested{input(c, 1)}
		t := step(s, e)
		t.Effects = append(t.Effects, CompleteJournal{Binding: c.Binding})
		rejected(s, e, t)
	})
	It("rejects CompleteJournal for the wrong event", func() {
		c := fixture(AllowAll)
		s := execution(c)
		e := ExecutionStarted{input(c, 3)}
		t := step(s, e)
		t.Effects = []Effect{CompleteJournal{Binding: c.Binding}}
		rejected(s, e, t)
	})
	It("rejects repeated CompleteJournal while awaiting journal acceptance", func() {
		c := fixture(AllowAll)
		s := step(execution(c), ExecutionCompleted{Input: input(c, 3)}).State
		e := ExecutionCompleted{Input: input(c, 3)}
		t := step(s, e)
		t.Effects = []Effect{CompleteJournal{Binding: c.Binding}}
		rejected(s, e, t)
	})
	It("rejects extra RunExecutor", func() {
		c := fixture(AllowAll)
		s := invocation(c)
		e := admitted(c, 3)
		t := step(s, e)
		t.Effects = append(t.Effects, RunExecutor{Binding: c.Binding})
		rejected(s, e, t)
	})
	It("rejects omitted RunExecutor even with a forged counter", func() {
		c := fixture(AllowAll)
		s := invocation(c)
		e := admitted(c, 3)
		t := step(s, e)
		t.Effects = nil
		t.State = s
		rejected(s, e, t)
	})
	It("rejects unexpected RunExecutor", func() {
		c := fixture(AllowAll)
		s := NewState(c)
		e := Requested{input(c, 1)}
		t := step(s, e)
		t.Effects = append(t.Effects, RunExecutor{Binding: c.Binding})
		rejected(s, e, t)
	})
	DescribeTable("rejects immutable configuration tampering", func(field string) {
		c := fixture(AllowAll)
		s := NewState(c)
		e := Requested{input(c, 1)}
		t := step(s, e)
		switch field {
		case "binding":
			t.State.config.Binding = BindingMismatchWitnesses(c.Binding)[0]
		case "policy":
			t.State.config.Policy = Stop
		case "ttl":
			t.State.config.ApprovalTTL++
		case "taint":
			t.State.config.ExternalTaintedWrite = true
		}
		rejected(s, e, t)
	}, Entry("binding", "binding"), Entry("policy", "policy"), Entry("ttl", "ttl"), Entry("taint", "taint"))
	DescribeTable("rejects state and transition metadata tampering", func(field string) {
		c := fixture(AllowAll)
		s := NewState(c)
		e := Requested{input(c, 1)}
		t := step(s, e)
		switch field {
		case "phase":
			t.State.phase = Authorized{}
		case "result":
			t.State.result = NewOutput([]byte("forged"))
		case "time":
			t.State.lastTime++
		case "rule":
			t.RuleID = "TB-R99"
		case "guard":
			t.Guard = "forged guard"
		case "effect output":
			t.Effects = []Effect{CheckAdmission{Binding: c.Binding, Output: NewOutput([]byte("forged"))}}
		}
		rejected(s, e, t)
	}, Entry("phase", "phase"), Entry("result", "result"), Entry("time", "time"), Entry("rule", "rule"), Entry("guard", "guard"), Entry("effect output", "effect output"))
	It("rejects invalid source state and nil event without panic", func() {
		c := fixture(AllowAll)
		s := NewState(c)
		e := Requested{input(c, 1)}
		t := step(s, e)
		rejected(s, nil, t)
		s.config.Policy = PolicyMode(255)
		rejected(s, e, t)
	})
})

var _ = Describe("empty binding arguments", func() {
	It("normalizes nil and empty before hashing into valid stable bindings", func() {
		s := Scope{"p", "c", "t", 1, 1}
		a, err := NewBinding(s, "r", "x", nil, Revisions{}, "key")
		Expect(err).NotTo(HaveOccurred())
		b, err := NewBinding(s, "r", "x", []byte{}, Revisions{}, "key")
		Expect(err).NotTo(HaveOccurred())
		Expect(a.valid()).To(BeTrue())
		Expect(b.valid()).To(BeTrue())
		Expect(a == b).To(BeTrue())
		Expect(a.Digest()).To(Equal(b.Digest()))
		out := a.Arguments()
		Expect(out).NotTo(BeNil())
		out = append(out, 'x')
		Expect(a.Arguments()).To(BeEmpty())
		c := ModelConfig{Binding: a, Policy: AllowAll, ApprovalTTL: 10}
		step(NewState(c), Requested{input(c, 1)})
	})
	It("preserves exact nonempty bytes and owns returned arguments", func() {
		raw := []byte(" \x00\xff{}\n")
		want := append([]byte{}, raw...)
		b, err := NewBinding(Scope{"p", "c", "t", 1, 1}, "r", "x", raw, Revisions{}, "key")
		Expect(err).NotTo(HaveOccurred())
		raw[0] = 'X'
		out := b.Arguments()
		out[0] = 'Y'
		Expect(b.Arguments()).To(Equal(want))
		Expect(b.valid()).To(BeTrue())
	})
})

// SPDX-License-Identifier: MIT
package runner

import (
	"encoding/json"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"testing"
)

func TestRuntimeRunner(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Runtime runner pure model")
}
func fixture() data {
	return ownData(&dataFields{id: ownIdentity(&identityFields{principal: "private-principal", conversation: "private-conversation", turn: "private-turn", attempt: 1, epoch: 1}), input: []byte("private-input")})
}

// phaseFixture supplies a lawful representative, not an assertion that every
// cross-product of shared payload fields is valid. Invalid shapes are tested
// separately; the 190 pairs and 950 stale bindings must use legal pre-states.
func phaseFixture(phase string) data {
	d := fixture()
	if phase == "Cancelling" {
		d.fields().outcome = stopped
	}
	if phase == "Settling" {
		d.fields().ciphertext = []byte("cipher")
	}
	return d
}

func ev(n string, d data) event {
	return makeEvent(n, ownEventData(&eventFields{id: d.fields().id, now: 1, trusted: true, content: []byte("private-ciphertext")}))
}
func step(s runnerState, e event) transition {
	r := PureReduce(s, e)
	Expect(CheckTransition(s, e, r)).To(Succeed())
	return r
}
func effectNames(r transition) []string {
	a := []string{}
	for _, e := range r.effects {
		a = append(a, e.name())
	}
	return a
}

var _ = Describe("Runner model", func() {
	It("begins a server-owned attempt by restoring", func() {
		d := fixture()
		r := step(idle{d}, ev("Begin", d))
		Expect(r.state.phase()).To(Equal("Restoring"))
		Expect(effectNames(r)).To(Equal([]string{"RestoreSession"}))
	})
	It("attaches without starting execution", func() {
		d := fixture()
		r := step(idle{d}, ev("Attach", d))
		Expect(r.state.payload().fields().attached).To(BeTrue())
		Expect(r.effects).To(BeEmpty())
	})
	It("detaches only the attachment during running", func() {
		d := fixture()
		d.fields().attached = true
		r := step(running{d}, ev("Detach", d))
		Expect(r.state.phase()).To(Equal("Running"))
		Expect(r.effects).To(BeEmpty())
		Expect(r.state.payload().fields().attached).To(BeFalse())
	})
	for _, field := range []string{"attempt", "epoch", "principal", "conversation", "turn"} {
		field := field
		It("rejects stale callback "+field, func() {
			d := fixture()
			e := ev("TurnFinished", d).payload()
			switch field {
			case "attempt":
				e.fields().id.fields().attempt++
			case "epoch":
				e.fields().id.fields().epoch++
			case "principal":
				e.fields().id.fields().principal += "other"
			case "conversation":
				e.fields().id.fields().conversation += "other"
			case "turn":
				e.fields().id.fields().turn += "other"
			}
			r := step(running{d}, makeEvent("TurnFinished", e))
			Expect(r.rule).To(Equal("RR002"))
			Expect(r.state.phase()).To(Equal("Running"))
			Expect(r.effects).To(BeEmpty())
		})
	}
	It("stop requests cancellation and a join", func() {
		d := fixture()
		r := step(running{d}, ev("Stop", d))
		Expect(r.state.phase()).To(Equal("Cancelling"))
		Expect(effectNames(r)).To(Equal([]string{"CancelSession", "AwaitQuiescence"}))
	})
	It("lease loss requests cancellation without releasing input", func() {
		d := fixture()
		r := step(running{d}, ev("LeaseLost", d))
		Expect(r.state.payload().fields().outcome).To(Equal(leaseLost))
		Expect(r.state.payload().fields().input).To(Equal(d.fields().input))
		Expect(effectNames(r)).NotTo(ContainElement("StartNext"))
	})
	It("late finish during cancellation cannot export", func() {
		d := phaseFixture("Cancelling")
		r := step(cancelling{d}, ev("TurnFinished", d))
		Expect(r.state.phase()).To(Equal("Cancelling"))
		Expect(r.effects).To(BeEmpty())
	})
	It("cancel timeout never attests quiescence", func() {
		d := phaseFixture("Cancelling")
		r := step(cancelling{d}, ev("CancelTimedOut", d))
		Expect(r.state.phase()).To(Equal("Interrupted"))
		Expect(effectNames(r)).To(Equal([]string{"HoldInput"}))
	})
	It("restore failure holds the active input", func() {
		d := fixture()
		r := step(restoring{d}, ev("RestoreFailed", d))
		Expect(r.state.phase()).To(Equal("Interrupted"))
		Expect(effectNames(r)).To(Equal([]string{"CloseSession", "HoldInput"}))
		Expect(r.state.payload().fields().input).To(Equal(d.fields().input))
	})
	It("requires trusted quiescence before export", func() {
		d := phaseFixture("Cancelling")
		e := ev("QuiescenceConfirmed", d).payload()
		e.fields().trusted = false
		r := step(cancelling{d}, makeEvent("QuiescenceConfirmed", e))
		Expect(r.state.phase()).To(Equal("Cancelling"))
		Expect(r.effects).To(BeEmpty())
	})
	It("seal failure holds input without advancing FIFO", func() {
		d := fixture()
		r := step(sealing{d}, ev("SealFailed", d))
		Expect(r.state.phase()).To(Equal("Interrupted"))
		Expect(effectNames(r)).To(Equal([]string{"HoldInput"}))
	})
	It("settlement failure holds ciphertext and input", func() {
		d := fixture()
		d.fields().ciphertext = []byte("cipher")
		r := step(settling{d}, ev("SettleFailed", d))
		Expect(r.state.phase()).To(Equal("Interrupted"))
		Expect(r.state.payload().fields().ciphertext).To(Equal(d.fields().ciphertext))
		Expect(effectNames(r)).To(Equal([]string{"HoldInput"}))
	})
	It("only successful trusted settlement releases FIFO", func() {
		d := fixture()
		d.fields().ciphertext = []byte("cipher")
		r := step(settling{d}, ev("SettleSucceeded", d))
		Expect(r.state.phase()).To(Equal("Idle"))
		Expect(effectNames(r)).To(Equal([]string{"StartNext"}))
		Expect(r.state.payload().fields().input).To(BeEmpty())
		e := ev("SettleSucceeded", d).payload()
		e.fields().trusted = false
		r = step(settling{d}, makeEvent("SettleSucceeded", e))
		Expect(effectNames(r)).NotTo(ContainElement("StartNext"))
	})
	It("unknown journal blocks ordinary resume", func() {
		d := fixture()
		r := step(running{d}, ev("UnknownJournal", d))
		Expect(r.state.phase()).To(Equal("Reconciliation"))
		r = step(r.state, ev("Resume", d))
		Expect(r.state.phase()).To(Equal("Reconciliation"))
		Expect(effectNames(r)).NotTo(ContainElement("StartNext"))
	})
	It("reconciliation requires explicit trusted evidence and a fresh attempt", func() {
		d := fixture()
		e := ev("Resume", d).payload()
		e.fields().id.fields().attempt++
		e.fields().reconciled = true
		r := step(reconciliation{d}, makeEvent("Resume", e))
		Expect(r.state.phase()).To(Equal("Restoring"))
		Expect(r.state.payload().fields().input).To(Equal(d.fields().input))
	})
	It("deleted attempts cannot resurrect", func() {
		d := fixture()
		for _, n := range events {
			r := step(deleted{d}, ev(n, d))
			Expect(r.state.phase()).To(Equal("Deleted"))
			Expect(r.effects).To(BeEmpty())
		}
	})
	It("rejects zero identity and nil input variants", func() {
		Expect(CheckInvariants(idle{})).NotTo(Succeed())
		r := PureReduce(idle{}, beginEvent{})
		Expect(r.rule).To(Equal("RR001"))
		Expect(CheckTransition(nil, nil, r)).NotTo(Succeed())
	})
	It("owns event state and effect buffers", func() {
		d := fixture()
		b := []byte("cipher")
		e := makeEvent("SealSucceeded", ownEventData(&eventFields{id: d.fields().id, trusted: true, now: 1, content: b}))
		b[0] = 'X'
		r := step(sealing{d}, e)
		Expect(string(r.state.payload().fields().ciphertext)).To(Equal("cipher"))
		r.effects[0].payload().fields().content[0] = 'Y'
		Expect(string(r.state.payload().fields().ciphertext)).To(Equal("cipher"))
		d.fields().input[0] = 'X'
		Expect(string(r.state.payload().fields().input)).To(Equal("private-input"))
	})
	It("redacts formatting JSON and validation errors", func() {
		d := fixture()
		r := PureReduce(sealing{d}, ev("SealSucceeded", d))
		for _, v := range []any{d, d.fields().id, sealing{d}, ev("Stream", d), r, r.effects[0]} {
			for _, f := range []string{"%v", "%+v", "%#v"} {
				Expect(fmt.Sprintf(f, v)).NotTo(ContainSubstring("private-"))
			}
			b, err := json.Marshal(v)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(b)).NotTo(ContainSubstring("private-"))
		}
		Expect(fmt.Sprint(CheckInvariants(idle{}))).NotTo(ContainSubstring("private-"))
	})
	It("rejects forged effect sets even when the final phase is correct", func() {
		d := fixture()
		e := ev("Detach", d)
		r := PureReduce(running{d}, e)
		r.effects = []effect{startNextEffect{ownEffectData(&effectFields{id: d.fields().id})}}
		Expect(CheckTransition(running{d}, e, r)).NotTo(Succeed())
	})
	It("rejects forged state identity and guard labels", func() {
		d := fixture()
		e := ev("Stop", d)
		r := PureReduce(running{d}, e)
		r.guard = "forged"
		Expect(CheckTransition(running{d}, e, r)).NotTo(Succeed())
		r = PureReduce(running{d}, e)
		x := r.state.payload()
		x.fields().id.fields().epoch++
		r.state = cancelling{x}
		Expect(CheckTransition(running{d}, e, r)).NotTo(Succeed())
	})
	It("enumerates all 190 phase-event pairs with stable distinct pair rules", func() {
		seen := map[string]bool{}
		for _, p := range phases {
			for _, n := range events {
				d := phaseFixture(p)
				s := makeState(p, d)
				Expect(CheckInvariants(s)).To(Succeed())
				r := step(s, ev(n, d))
				Expect(r.rule).NotTo(BeEmpty())
				Expect(r.guard).NotTo(BeEmpty())
				seen[p+"/"+n] = true
			}
		}
		Expect(seen).To(HaveLen(190))
		GinkgoWriter.Printf("phase-event witnesses=%d\n", len(seen))
	})
	It("checks bounded reachable states and existential progress with paths", func() { boundedCheck() })
})

// State keys deliberately omit redacted formatters. The finite abstraction uses
// one principal/conversation/turn, two attempts, one epoch, input/cipher symbols,
// two attachment values and three completion reasons. Resume caps attempts at 2.
func stateKey(s runnerState) string {
	d := s.payload()
	return fmt.Sprintf("%s/%d/%d/%t/%d/%s/%s", s.phase(), d.fields().id.fields().attempt, d.fields().id.fields().epoch, d.fields().attached, d.fields().outcome, string(d.fields().input), string(d.fields().ciphertext))
}
func boundedEvents(s runnerState) []event {
	d := s.payload()
	out := []event{}
	for _, n := range events {
		e := ev(n, d).payload()
		e.fields().content = []byte("cipher")
		out = append(out, makeEvent(n, e))
		e.fields().trusted = false
		out = append(out, makeEvent(n, e))
		if n == "Resume" && d.fields().id.fields().attempt < 2 {
			e.fields().trusted = true
			e.fields().reconciled = true
			e.fields().id.fields().attempt++
			out = append(out, makeEvent(n, e))
		}
	}
	return out
}
func boundedCheck() {
	type node struct {
		s    runnerState
		path []string
	}
	q := []node{{s: idle{fixture()}}}
	seen := map[string]node{stateKey(q[0].s): q[0]}
	edges := map[string][]string{}
	witness := map[string]bool{}
	count := 0
	for i := 0; i < len(q); i++ {
		v := q[i]
		Expect(CheckInvariants(v.s)).To(Succeed(), "path %v", v.path)
		for _, e := range boundedEvents(v.s) {
			r := PureReduce(v.s, e)
			Expect(CheckTransition(v.s, e, r)).To(Succeed(), "path %v event %s", v.path, e.name())
			count++
			witness[v.s.phase()+"/"+e.name()] = true
			k := stateKey(r.state)
			edges[stateKey(v.s)] = append(edges[stateKey(v.s)], k)
			if _, ok := seen[k]; !ok {
				n := node{r.state, append(append([]string{}, v.path...), e.name()+":"+r.rule)}
				seen[k] = n
				q = append(q, n)
			}
		}
		Expect(len(q)).To(BeNumerically("<=", 2000))
	}
	// Existential reachability to terminal or input/proof waiting states; not fairness.
	good := map[string]bool{}
	for k, n := range seen {
		switch n.s.phase() {
		case "Idle", "Interrupted", "Reconciliation", "Deleted":
			good[k] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for k, es := range edges {
			if good[k] {
				continue
			}
			for _, to := range es {
				if good[to] {
					good[k] = true
					changed = true
					break
				}
			}
		}
	}
	for k, n := range seen {
		Expect(good[k]).To(BeTrue(), "no terminal/wait path from %v", n.path)
	}
	Expect(witness).To(HaveLen(190))
	GinkgoWriter.Printf("bounded attempts=2 epochs=1 identities=1 states=%d transitions=%d phase-event=%d terminal/wait-reachable=%d\n", len(seen), count, len(witness), len(good))
}

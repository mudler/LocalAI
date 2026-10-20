// SPDX-License-Identifier: MIT
package runner

import (
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"reflect"
)

var _ = Describe("Independent guard rejection", func() {
	It("rejects export forged from running and from a cancel timeout", func() {
		d := fixture()
		for _, n := range []string{"Stream", "CancelTimedOut", "TurnFinished"} {
			s := runnerState(running{d})
			if n == "CancelTimedOut" {
				s = cancelling{phaseFixture("Cancelling")}
			}
			e := ev(n, d)
			r := PureReduce(s, e)
			r.state = sealing{d}
			r.effects = []effect{exportAndSealEffect{ownEffectData(&effectFields{id: d.fields().id})}}
			Expect(CheckTransition(s, e, r)).NotTo(Succeed())
		}
	})
	It("rejects forged settlement release after untrusted success or failed settlement", func() {
		d := fixture()
		d.fields().ciphertext = []byte("cipher")
		for _, n := range []string{"SettleSucceeded", "SettleFailed"} {
			e := ev(n, d).payload()
			e.fields().trusted = false
			r := PureReduce(settling{d}, makeEvent(n, e))
			x := d.clone()
			x.fields().input = nil
			x.fields().ciphertext = nil
			r.state = idle{x}
			r.effects = []effect{startNextEffect{ownEffectData(&effectFields{id: d.fields().id})}}
			Expect(CheckTransition(settling{d}, makeEvent(n, e), r)).NotTo(Succeed())
		}
	})
	It("rejects empty ciphertext and nontrusted or zero-time host results", func() {
		d := fixture()
		for _, n := range []string{"RestoreSucceeded", "QuiescenceConfirmed", "SealSucceeded", "SettleSucceeded"} {
			p := map[string]string{"RestoreSucceeded": "Restoring", "QuiescenceConfirmed": "Cancelling", "SealSucceeded": "Sealing", "SettleSucceeded": "Settling"}[n]
			d = phaseFixture(p)
			s := makeState(p, d)
			Expect(CheckInvariants(s)).To(Succeed())
			e := ev(n, d).payload()
			e.fields().now = 0
			r := step(s, makeEvent(n, e))
			Expect(r.state.phase()).To(Equal(p))
			Expect(r.effects).To(BeEmpty())
		}
		d = phaseFixture("Sealing")
		e := ev("SealSucceeded", d).payload()
		e.fields().content = nil
		r := step(sealing{d}, makeEvent("SealSucceeded", e))
		Expect(r.state.phase()).To(Equal("Sealing"))
		Expect(r.effects).To(BeEmpty())
	})
	It("stop and lease loss invalidate pending seal and settlement", func() {
		d := fixture()
		for _, p := range []string{"Quiescent", "Sealing", "Settling"} {
			d = phaseFixture(p)
			for _, n := range []string{"Stop", "LeaseLost"} {
				r := step(makeState(p, d), ev(n, d))
				Expect(effectNames(r)).To(ContainElement("CancelSession"))
				Expect(r.state.phase()).NotTo(Equal(p))
				for _, cb := range []string{"SealSucceeded", "SettleSucceeded"} {
					r = step(r.state, ev(cb, d))
					Expect(effectNames(r)).NotTo(ContainElement("StartNext"))
					Expect(r.state.payload().fields().input).To(Equal(d.fields().input))
				}
			}
		}
	})
	It("does not alias ignored or stale state buffers", func() {
		for _, n := range []string{"LateCallback", "Stream"} {
			d := fixture()
			e := ev(n, d)
			if n == "Stream" {
				v := e.payload()
				v.fields().id.fields().epoch++
				e = makeEvent(n, v)
			}
			r := step(idle{d}, e)
			d.fields().input[0] = 'X'
			Expect(string(r.state.payload().fields().input)).To(Equal("private-input"))
		}
	})
	It("rejects cross-scope resume and same-attempt reconciliation", func() {
		d := fixture()
		for _, change := range []string{"principal", "conversation", "turn", "attempt", "epoch", "proof"} {
			v := ev("Resume", d).payload()
			v.fields().id.fields().attempt++
			v.fields().reconciled = true
			switch change {
			case "principal":
				v.fields().id.fields().principal = "other"
			case "conversation":
				v.fields().id.fields().conversation = "other"
			case "turn":
				v.fields().id.fields().turn = "other"
			case "attempt":
				v.fields().id.fields().attempt = d.fields().id.fields().attempt
			case "epoch":
				v.fields().id.fields().epoch = 0
			case "proof":
				v.fields().reconciled = false
			}
			r := PureReduce(reconciliation{d}, makeEvent("Resume", v))
			Expect(r.state.phase()).To(Equal("Reconciliation"))
			Expect(r.effects).To(BeEmpty())
		}
	})
	It("has exactly ten sealed states nineteen events and ten effects", func() {
		d := fixture()
		ss := map[reflect.Type]bool{}
		es := map[reflect.Type]bool{}
		fs := map[reflect.Type]bool{}
		ids := map[string]bool{}
		for _, p := range phases {
			ss[reflect.TypeOf(makeState(p, d))] = true
		}
		for _, n := range events {
			es[reflect.TypeOf(ev(n, d))] = true
		}
		for _, r := range pairRules {
			Expect(ids[r.rule]).To(BeFalse())
			ids[r.rule] = true
			for _, n := range []string{"RestoreSession", "StartTurn", "CancelSession", "AwaitQuiescence", "ExportAndSeal", "SettleCiphertext", "StartNext", "EmitSafe", "CloseSession", "HoldInput"} {
				fs[reflect.TypeOf(makeEffect(n, d))] = true
			}
		}
		Expect(ss).To(HaveLen(10))
		Expect(es).To(HaveLen(19))
		Expect(fs).To(HaveLen(10))
		Expect(ids).To(HaveLen(190))
		GinkgoWriter.Printf("sealed states=%d events=%d effects=%d unique pair rules=%d\n", len(ss), len(es), len(fs), len(ids))
	})
	It("enumerates stale bindings across all phases without mutation", func() {
		count := 0
		for _, p := range phases {
			for _, n := range events {
				for _, field := range []string{"principal", "conversation", "turn", "attempt", "epoch"} {
					d := phaseFixture(p)
					v := ev(n, d).payload()
					switch field {
					case "principal":
						v.fields().id.fields().principal = "other"
					case "conversation":
						v.fields().id.fields().conversation = "other"
					case "turn":
						v.fields().id.fields().turn = "other"
					case "attempt":
						v.fields().id.fields().attempt++
					case "epoch":
						v.fields().id.fields().epoch++
					}
					s := makeState(p, d)
					Expect(CheckInvariants(s)).To(Succeed())
					r := step(s, makeEvent(n, v))
					Expect(r.effects).To(BeEmpty(), fmt.Sprintf("%s %s %s", p, n, field))
					Expect(sameState(r.state, s)).To(BeTrue())
					count++
				}
			}
		}
		Expect(count).To(Equal(950))
		GinkgoWriter.Printf("stale phase-event-binding witnesses=%d\n", count)
	})
})

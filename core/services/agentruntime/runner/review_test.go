// SPDX-License-Identifier: MIT
package runner

import (
	"encoding/json"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Review regressions", func() {
	It("protects safe logging holders even through private reflective wrappers", func() {
		d := fixture()
		r := PureReduce(sealing{d}, ev("SealSucceeded", d))
		for _, v := range []any{d, d.id, running{d}, ev("Stream", d), r, r.effects, r.effects[0]} {
			h := safeLog(v)
			for _, container := range []any{h, []any{h}, map[string]any{"value": h}, struct{ Value any }{h}, struct{ value any }{h}, struct{ value logHolder }{h}} {
				for _, f := range []string{"%v", "%+v", "%#v"} {
					text := fmt.Sprintf(f, container)
					Expect(text).NotTo(ContainSubstring("private-"))
					Expect(text).NotTo(ContainSubstring("112 114 105 118 97 116 101"))
					Expect(text).NotTo(ContainSubstring("0x70, 0x72, 0x69, 0x76"))
				}
				b, err := json.Marshal(container)
				Expect(err).NotTo(HaveOccurred())
				Expect(string(b)).NotTo(ContainSubstring("private-"))
			}
		}
	})
	It("redacts raw private-field wrappers without relying on nested Formatter", func() {
		d := fixture()
		for _, raw := range []any{struct{ state runnerState }{running{d}}, struct{ value data }{d}} {
			for _, f := range []string{"%v", "%+v", "%#v"} {
				text := fmt.Sprintf(f, raw)
				Expect(text).NotTo(ContainSubstring("private-"))
				Expect(text).NotTo(ContainSubstring("112 114 105 118 97 116 101"))
				Expect(text).NotTo(ContainSubstring("0x70, 0x72, 0x69, 0x76"))
			}
		}
	})
	It("rejects invalid phase shapes before effects can release FIFO", func() {
		d := fixture()
		invalid := []runnerState{cancelling{d}}
		x := d.clone()
		x.outcome = stopped
		invalid = append(invalid, idle{x}, restoring{x}, running{x}, quiescent{x})
		x = d.clone()
		x.ciphertext = []byte("cipher")
		invalid = append(invalid, idle{x}, restoring{x}, running{x}, quiescent{x}, sealing{x}, cancelling{x})
		x = d.clone()
		x.input = nil
		invalid = append(invalid, restoring{x}, running{x}, cancelling{x}, quiescent{x}, sealing{x}, settling{x})
		for _, s := range invalid {
			Expect(CheckInvariants(s)).NotTo(Succeed(), "phase %s", s.phase())
			for _, n := range []string{"QuiescenceConfirmed", "SealSucceeded", "SettleSucceeded"} {
				e := ev(n, d)
				r := PureReduce(s, e)
				Expect(r.rule).To(Equal("RR001"))
				Expect(r.effects).To(BeEmpty())
				Expect(CheckTransition(s, e, r)).NotTo(Succeed())
			}
		}
	})
	It("holds an empty settled attempt under uncertainty without empty restore", func() {
		d := fixture()
		d.ciphertext = []byte("cipher")
		settled := step(settling{d}, ev("SettleSucceeded", d))
		Expect(effectNames(settled)).To(Equal([]string{"StartNext"}))
		s := step(settled.state, ev("UnknownJournal", d)).state
		Expect(s.phase()).To(Equal("Reconciliation"))
		v := ev("Resume", d).payload()
		v.id.attempt++
		v.reconciled = true
		r := step(s, makeEvent("Resume", v))
		Expect(r.state.phase()).To(Equal("Reconciliation"))
		Expect(r.effects).To(BeEmpty())
		Expect(r.state.payload().id).To(Equal(d.id))
	})
	It("retains failure and interruption ciphertext until a legitimate fresh resume", func() {
		for _, outcome := range []completion{finished, stopped, leaseLost} {
			d := fixture()
			d.outcome = outcome
			d.ciphertext = []byte("cipher")
			for _, n := range []string{"SettleFailed", "UnknownJournal"} {
				r := step(settling{d}, ev(n, d))
				Expect(r.state.payload().ciphertext).To(Equal(d.ciphertext))
				Expect(r.state.payload().input).To(Equal(d.input))
				Expect(effectNames(r)).NotTo(ContainElement("StartNext"))
				v := ev("Resume", d).payload()
				v.id.attempt++
				v.reconciled = true
				resumed := step(r.state, makeEvent("Resume", v))
				Expect(resumed.state.phase()).To(Equal("Restoring"))
				Expect(resumed.state.payload().input).To(Equal(d.input))
				Expect(resumed.state.payload().ciphertext).To(BeEmpty())
			}
		}
	})
})

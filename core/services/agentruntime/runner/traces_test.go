// SPDX-License-Identifier: MIT
package runner

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type traceStep struct {
	event, rule, phase string
	effects            []string
}

func traceRun(steps []traceStep) {
	var s runnerState = idle{fixture()}
	for i, w := range steps {
		e := ev(w.event, s.payload())
		if w.event == "Resume" {
			v := e.payload()
			v.id.attempt++
			v.reconciled = true
			e = makeEvent("Resume", v)
		}
		r := step(s, e)
		Expect(r.rule).To(Equal(w.rule), "step %d", i)
		Expect(r.state.phase()).To(Equal(w.phase), "step %d", i)
		Expect(effectNames(r)).To(Equal(w.effects), "step %d", i)
		GinkgoWriter.Printf("trace step=%d event=%s rule=%s guard=%s before=%s after=%s effects=%v\n", i, w.event, r.rule, r.guard, s.phase(), r.state.phase(), effectNames(r))
		s = r.state
	}
}

var _ = Describe("Ordered runner traces", func() {
	It("disconnect running finish join seal settle next", func() {
		traceRun([]traceStep{
			{"Begin", "RR102", "Restoring", []string{"RestoreSession"}},
			{"RestoreSucceeded", "RR122", "Running", []string{"StartTurn"}},
			{"Detach", "RR139", "Running", []string{}},
			{"TurnFinished", "RR144", "Quiescent", []string{"AwaitQuiescence"}},
			{"QuiescenceConfirmed", "RR186", "Sealing", []string{"ExportAndSeal"}},
			{"SealSucceeded", "RR206", "Settling", []string{"SettleCiphertext"}},
			{"SettleSucceeded", "RR227", "Idle", []string{"StartNext"}},
		})
	})
	It("stop late finish join seal settles interrupted without next", func() {
		traceRun([]traceStep{
			{"Begin", "RR102", "Restoring", []string{"RestoreSession"}},
			{"RestoreSucceeded", "RR122", "Running", []string{"StartTurn"}},
			{"Stop", "RR145", "Cancelling", []string{"CancelSession", "AwaitQuiescence"}},
			{"TurnFinished", "RR163", "Cancelling", []string{}},
			{"QuiescenceConfirmed", "RR167", "Sealing", []string{"ExportAndSeal"}},
			{"SealSucceeded", "RR206", "Settling", []string{"SettleCiphertext"}},
			{"SettleSucceeded", "RR227", "Interrupted", []string{"HoldInput"}},
		})
	})
	It("lease lost timeout holds input", func() {
		traceRun([]traceStep{
			{"Begin", "RR102", "Restoring", []string{"RestoreSession"}},
			{"RestoreSucceeded", "RR122", "Running", []string{"StartTurn"}},
			{"LeaseLost", "RR146", "Cancelling", []string{"CancelSession", "AwaitQuiescence"}},
			{"CancelTimedOut", "RR166", "Interrupted", []string{"HoldInput"}},
		})
	})
	It("restore failure holds input", func() {
		traceRun([]traceStep{
			{"Begin", "RR102", "Restoring", []string{"RestoreSession"}},
			{"RestoreFailed", "RR123", "Interrupted", []string{"CloseSession", "HoldInput"}},
		})
	})
	It("uncertain journal blocks resume until fresh reconciled attempt", func() {
		traceRun([]traceStep{
			{"Begin", "RR102", "Restoring", []string{"RestoreSession"}},
			{"RestoreSucceeded", "RR122", "Running", []string{"StartTurn"}},
			{"UnknownJournal", "RR153", "Reconciliation", []string{"CancelSession", "AwaitQuiescence", "HoldInput"}},
			{"Resume", "RR268", "Restoring", []string{"RestoreSession"}},
			{"RestoreSucceeded", "RR122", "Running", []string{"StartTurn"}},
		})
	})
	It("delete then old callback is ignored", func() {
		traceRun([]traceStep{
			{"Begin", "RR102", "Restoring", []string{"RestoreSession"}},
			{"Delete", "RR136", "Deleted", []string{"CancelSession", "AwaitQuiescence", "CloseSession", "HoldInput"}},
			{"LateCallback", "RR289", "Deleted", []string{}},
		})
	})
	It("out of order and duplicate finish cannot trigger export", func() {
		traceRun([]traceStep{
			{"TurnFinished", "RR106", "Idle", []string{}},
			{"Begin", "RR102", "Restoring", []string{"RestoreSession"}},
			{"RestoreSucceeded", "RR122", "Running", []string{"StartTurn"}},
			{"TurnFinished", "RR144", "Quiescent", []string{"AwaitQuiescence"}},
			{"TurnFinished", "RR182", "Quiescent", []string{}},
		})
	})
})

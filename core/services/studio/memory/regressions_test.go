// SPDX-License-Identifier: MIT
package memory

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func advance(s State, events ...func(Envelope) Event) State {
	for _, makeEvent := range events {
		s = Reduce(s, makeEvent(envelopeOfState(s))).State
	}
	return s
}
func grant(e Envelope) Event   { return ClaimGranted{e} }
func auth(e Envelope) Event    { return AuthorizationGranted{e} }
func persist(e Envelope) Event { return PayloadPersisted{e} }

var _ = Describe("Regression boundaries", func() {
	It("rejects a replayed pre-payload authorization in the same claim", func() {
		s := advance(Initial("source-a", ContentInput{"hello"}), grant)
		old := envelopeOfState(s)
		s = advance(s, auth, persist)
		tr := Reduce(s, AuthorizationGranted{old})
		Expect(tr.RuleID).To(Equal("R02-stale"))
		Expect(tr.Effects).To(BeEmpty())
	})
	It("freezes a summary across ambiguous payload persistence failure", func() {
		s := advance(Initial("source-a", SummaryInput{"input"}), grant, auth)
		s = Reduce(s, SummarySucceeded{envelopeOfState(s), "first summary"}).State
		s = Reduce(s, PayloadPersistFailed{envelopeOfState(s)}).State
		env := envelopeOfState(s)
		env.Now = s.(RetryWaiting).Due
		s = Reduce(s, RetryTick{env}).State
		s = advance(s, grant)
		tr := Reduce(s, AuthorizationGranted{envelopeOfState(s)})
		Expect(tr.State).To(BeAssignableToTypeOf(PersistingPayload{}))
		Expect(tr.Effects[0].(PersistPayload).Payload.Text).To(Equal("first summary"))
	})
	It("rejects blank summary input without calling a model", func() {
		s := advance(Initial("source-a", SummaryInput{" \n"}), grant)
		tr := Reduce(s, AuthorizationGranted{envelopeOfState(s)})
		Expect(tr.RuleID).To(Equal("R04-empty"))
		Expect(tr.State).To(BeAssignableToTypeOf(Failed{}))
	})
	It("expires a capture even while successive phase deadlines are renewed", func() {
		s := advance(Initial("source-a", ContentInput{"hello"}), grant)
		v := s.(Authorizing)
		v.Work.Job.Expires = 10
		s = v
		env := envelopeOfState(s)
		env.Now = 10
		tr := Reduce(s, AuthorizationGranted{env})
		Expect(tr.RuleID).To(Equal("R06-exhausted"))
		Expect(tr.State).To(BeAssignableToTypeOf(Failed{}))
	})
})

var _ = Describe("Frozen recovery and lifetime", func() {
	It("retains a produced summary across persistence timeout and expired-lease recovery", func() {
		for _, recover := range []bool{false, true} {
			s := advance(Initial("source-a", SummaryInput{"input"}), grant, auth)
			s = Reduce(s, SummarySucceeded{envelopeOfState(s), "frozen"}).State
			env := envelopeOfState(s)
			w, _ := workOf(s)
			env.Now = w.Deadline
			if recover {
				env.Generation++
				env.Owner = "frontend-b"
				s = Reduce(s, RestartRecovered{env}).State
			} else {
				s = Reduce(s, Timeout{env}).State
				env = envelopeOfState(s)
				env.Now = s.(RetryWaiting).Due
				s = Reduce(s, RetryTick{env}).State
				s = advance(s, grant)
			}
			tr := Reduce(s, AuthorizationGranted{envelopeOfState(s)})
			Expect(tr.RuleID).To(Equal("R04-resume"))
			Expect(tr.Effects[0].(PersistPayload).Payload.Text).To(Equal("frozen"))
			s = advance(tr.State, persist, auth)
			Expect(s).To(BeAssignableToTypeOf(Writing{}))
		}
	})
	It("never revives expired waiting work on a retry tick or early phase timeout", func() {
		s := advance(Initial("source-a", ContentInput{"input"}), grant)
		v := s.(Authorizing)
		v.Work.Job.Expires = 10
		s = v
		env := envelopeOfState(s)
		env.Now = 10
		Expect(Reduce(s, Timeout{env}).RuleID).To(Equal("R06-exhausted"))
		j := jobOf(s)
		s = RetryWaiting{j, 1}
		Expect(Reduce(s, RetryTick{env}).RuleID).To(Equal("R06-exhausted"))
	})
})

var _ = Describe("Capture expiry precedence", func() {
	It("expires every nonterminal phase on timed results, ticks and recovery before renewed deadlines", func() {
		for _, s := range SampleStates() {
			if terminal(s) {
				continue
			}
			j := jobOf(s)
			for _, e := range SampleEvents() {
				switch e.(type) {
				case DuplicateDelivered, Revoked:
					continue
				}
				env := envelopeOfState(s)
				env.Now = j.Expires
				tr := Reduce(s, withEnvelope(e, env))
				Expect(tr.RuleID).To(Equal("R06-exhausted"), "%T + %T", s, e)
				Expect(tr.State).To(BeAssignableToTypeOf(Failed{}))
				Expect(effectNames(tr.Effects)).To(Equal([]string{"PublishStatus"}))
			}
		}
	})
})

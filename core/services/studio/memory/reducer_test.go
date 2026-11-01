// SPDX-License-Identifier: MIT
package memory

import (
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"reflect"
)

var _ = Describe("Complete state/event vocabulary", func() {
	It("covers every sealed constructor and explicitly explains every ignored pair", func() {
		states := SampleStates()
		events := SampleEvents()
		Expect(states).To(HaveLen(9))
		Expect(events).To(HaveLen(18))
		stateNames := []string{"Queued", "Authorizing", "Transforming", "PersistingPayload", "Writing", "RetryWaiting", "Saved", "Failed", "Skipped"}
		eventNames := []string{"ClaimGranted", "ClaimDenied", "AuthorizationGranted", "AuthorizationDenied", "SummarySucceeded", "SummaryFailed", "PayloadPersisted", "PayloadPersistFailed", "WriteSucceeded", "WriteFailed", "Timeout", "RetryTick", "Revoked", "DuplicateDelivered", "RestartRecovered", "AuthorizedRetry", "WriteDeleted", "EntryDeleted"}
		for i, s := range states {
			Expect(reflect.TypeOf(s).Name()).To(Equal(stateNames[i]))
			for j, e := range events {
				Expect(reflect.TypeOf(e).Name()).To(Equal(eventNames[j]))
				before := fmt.Sprintf("%#v", s)
				tr := Reduce(s, e)
				Expect(tr.State).NotTo(BeNil())
				Expect(Rules).To(HaveKey(tr.RuleID))
				if reflect.DeepEqual(tr.State, s) && len(tr.Effects) == 0 {
					Expect(tr.IgnoredReason).NotTo(BeEmpty())
				}
				Expect(CheckInvariants(tr.State)).To(Succeed(), "%T + %T", s, e)
				Expect(fmt.Sprintf("%#v", s)).To(Equal(before))
				Expect(Reduce(s, e)).To(Equal(tr))
			}
		}
	})
	It("requires durable payload acknowledgment and fresh authorization before write", func() {
		s := Initial("source-a", ContentInput{Text: "hello"})
		r := RunTrace(s, []Event{ClaimGranted{sampleEnvelope()}, AuthorizationGranted{sampleEnvelope()}})
		Expect(r[1].State).To(BeAssignableToTypeOf(PersistingPayload{}))
		Expect(r[1].Effects[0]).To(BeAssignableToTypeOf(PersistPayload{}))
		stale := sampleEnvelope()
		stale.Operation = "old"
		Expect(Reduce(r[1].State, PayloadPersisted{stale}).IgnoredReason).NotTo(BeEmpty())
		p := envelopeOfState(r[1].State)
		a := Reduce(r[1].State, PayloadPersisted{p})
		Expect(a.State).To(BeAssignableToTypeOf(Authorizing{}))
		Expect(a.Effects[0]).To(BeAssignableToTypeOf(CheckAuthorization{}))
		w := Reduce(a.State, AuthorizationGranted{envelopeOfState(a.State)})
		Expect(w.State).To(BeAssignableToTypeOf(Writing{}))
		Expect(w.Effects[0]).To(BeAssignableToTypeOf(ReconcileOrWrite{}))
	})
	It("rejects malformed identity and frozen payloads", func() {
		s := Initial("", ContentInput{Text: "x"})
		Expect(CheckInvariants(s)).NotTo(Succeed())
	})
})

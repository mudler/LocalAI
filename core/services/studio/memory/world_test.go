// SPDX-License-Identifier: MIT
package memory

import (
	"fmt"
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// This is an explicit idealized shell contract, NOT evidence that any storage
// engine implements atomic claims, authorization, payload persistence or writes.
// In particular, checking the live fence and policy together with committing a
// unique receipt/tombstone is an assumption to verify in later adapter tasks.
type captureWorld struct {
	claims     map[string]Envelope
	receipts   map[string]string
	tombstones map[string]bool
	payloads   map[string]FrozenPayload
	policy     map[string]bool
}

func newCaptureWorld() captureWorld {
	return captureWorld{map[string]Envelope{}, map[string]string{}, map[string]bool{}, map[string]FrozenPayload{}, map[string]bool{}}
}
func copyMap[V any](m map[string]V) map[string]V {
	out := map[string]V{}
	for k, v := range m {
		out[k] = v
	}
	return out
}
func (w captureWorld) clone() captureWorld {
	return captureWorld{copyMap(w.claims), copyMap(w.receipts), copyMap(w.tombstones), copyMap(w.payloads), copyMap(w.policy)}
}
func (w captureWorld) mutate(work Work, now int64) bool {
	key := work.Job.Identity.Key()
	live := w.claims[key]
	p, durable := w.payloads[key]
	if !w.policy[key] || w.tombstones[key] || live.Owner != work.Fence.Owner || live.Generation != work.Fence.Generation || now >= work.Deadline || now >= work.Job.Expires || !durable || !reflect.DeepEqual(p, work.Job.Input) {
		return false
	}
	if _, exists := w.receipts[key]; !exists {
		w.receipts[key] = "entry-" + key
	}
	return true
}

var _ = Describe("Bounded reducer and explicit claim/backend world", func() {
	It("explores two owners and two keys with current policy, frozen writes, crash receipts and terminal absorption", func() {
		type node struct {
			s    State
			w    captureWorld
			path string
		}
		for _, source := range []string{"source-a", "source-b"} {
			initial := Initial(source, SummaryInput{"input"})
			key := jobOf(initial).Identity.Key()
			other := jobOf(Initial("other-source", ContentInput{"other"})).Identity.Key()
			world := newCaptureWorld()
			world.policy[key] = true
			world.receipts[other] = "other-entry"
			queue := []node{{initial, world, source}}
			seen := map[string]bool{}
			writes := 0
			crashRecoveries := 0
			for len(queue) > 0 {
				n := queue[0]
				queue = queue[1:]
				fingerprint := fmt.Sprintf("%#v/%v/%v/%v/%v/%v", n.s, n.w.claims, n.w.receipts, n.w.tombstones, n.w.payloads, n.w.policy)
				if seen[fingerprint] {
					continue
				}
				seen[fingerprint] = true
				Expect(len(seen)).To(BeNumerically("<", 20000), n.path)
				Expect(CheckInvariants(n.s)).To(Succeed(), n.path)
				Expect(n.w.receipts[other]).To(Equal("other-entry"), n.path)
				Expect(len(n.w.receipts)).To(BeNumerically("<=", 2), n.path)
				Expect(jobOf(n.s).Identity).To(Equal(jobOf(initial).Identity), n.path)
				if terminal(n.s) {
					for _, e := range SampleEvents() {
						if _, retry := e.(AuthorizedRetry); retry {
							continue
						}
						tr := Reduce(n.s, withEnvelope(e, envelopeOfState(n.s)))
						Expect(tr.State).To(Equal(n.s), n.path)
						Expect(tr.Effects).To(BeEmpty(), n.path)
					}
					continue
				}
				if work, active := workOf(n.s); active {
					Expect(n.w.claims[key].Owner).To(Equal(work.Fence.Owner), n.path)
					Expect(n.w.claims[key].Generation).To(Equal(work.Fence.Generation), n.path)
				}
				// A deletion after backend commit survives queue recovery and
				// reconciliation; retain the receipt as a deduplication tombstone.
				if _, writing := n.s.(Writing); writing && !n.w.tombstones[key] {
					deleted := n.w.clone()
					deleted.tombstones[key] = true
					queue = append(queue, node{n.s, deleted, n.path + " -> backend deletion"})
				}
				Expect(expires(n.s, 12)).To(BeTrue(), n.path)
				for _, e := range enabledEvents(n.s) {
					// Only a real modeled receipt may be reported as successful.
					if _, ok := e.(WriteSucceeded); ok && n.w.receipts[key] == "" {
						continue
					}
					if _, ok := e.(WriteDeleted); ok && !n.w.tombstones[key] {
						continue
					}
					if success, ok := e.(WriteSucceeded); ok {
						if n.w.tombstones[key] {
							continue
						}
						success.EntryID = n.w.receipts[key]
						e = success
					}
					w := n.w.clone()
					if _, ok := e.(Revoked); ok {
						w.policy[key] = false
					}
					if _, ok := e.(PayloadPersisted); ok {
						w.payloads[key] = n.s.(PersistingPayload).Payload
					}
					tr := Reduce(n.s, e)
					for _, effect := range tr.Effects {
						switch eff := effect.(type) {
						case CheckAuthorization:
							old := w.claims[key]
							Expect(eff.Work.Fence.Generation).To(BeNumerically(">=", old.Generation), n.path)
							w.claims[key] = eff.Work.Fence
						case ReconcileOrWrite:
							Expect(e).To(BeAssignableToTypeOf(AuthorizationGranted{}), n.path)
							Expect(n.s).To(BeAssignableToTypeOf(Authorizing{}), n.path)
							if w.tombstones[key] {
								Expect(w.mutate(eff.Work, eventEnvelope(e).Now)).To(BeFalse(), n.path)
								continue
							}
							Expect(w.mutate(eff.Work, eventEnvelope(e).Now)).To(BeTrue(), n.path)
							writes++
							// Two backend deliveries return the same exact receipt, never uploads.
							receipt := w.receipts[key]
							Expect(w.mutate(eff.Work, eventEnvelope(e).Now)).To(BeTrue(), n.path)
							Expect(w.receipts[key]).To(Equal(receipt), n.path)
							// Policy loss at the actual mutation boundary defeats a previously
							// issued effect. It cannot be a reusable authorization token.
							denied := w.clone()
							denied.policy[key] = false
							Expect(denied.mutate(eff.Work, eventEnvelope(e).Now)).To(BeFalse(), n.path)
							stale := eff.Work
							stale.Fence.Owner = "stale-owner"
							Expect(w.mutate(stale, eventEnvelope(e).Now)).To(BeFalse(), n.path)
							deleted := w.clone()
							deleted.tombstones[key] = true
							Expect(deleted.mutate(eff.Work, eventEnvelope(e).Now)).To(BeFalse(), n.path)
						}
					}
					if _, ok := e.(RestartRecovered); ok && w.receipts[key] != "" {
						crashRecoveries++
					}
					if !reflect.DeepEqual(tr.State, n.s) {
						queue = append(queue, node{tr.State, w, n.path + fmt.Sprintf(" -> %T/%s", e, tr.RuleID)})
					}
				}
			}
			Expect(writes).To(BeNumerically(">", 0))
			Expect(crashRecoveries).To(BeNumerically(">", 0))
			Expect(len(seen)).To(BeNumerically(">", 20))
		}
	})
	It("keeps capture acceptance independent of answer outcome and never supplies a destination", func() {
		// Answer success/failure is deliberately absent from the event vocabulary:
		// once input is accepted, either answer outcome runs the identical capture.
		for _, answer := range []string{"succeeded", "failed", "cancelled"} {
			s := advance(Initial("source-a", ContentInput{"accepted text"}), grant, auth, persist, auth)
			Expect(s).To(BeAssignableToTypeOf(Writing{}), answer)
			Expect(jobOf(s).Identity.Destination).To(Equal("collection"), answer)
		}
		j := jobOf(Initial("source-a", ContentInput{"text"}))
		j.Identity.Destination = ""
		Expect(CheckInvariants(Queued{j})).NotTo(Succeed())
	})
})

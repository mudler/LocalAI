// SPDX-License-Identifier: MIT
package studio

import (
	"encoding/json"
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/memory"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

var _ = Describe("memory status projection", func() {
	It("projects every state without leaking private fields", func() {
		for _, state := range memory.SampleStates() {
			tt := struct {
				s             memory.State
				status        string
				due           int64
				entry, reason string
			}{s: state}
			switch v := state.(type) {
			case memory.Queued:
				tt.status = "queued"
			case memory.RetryWaiting:
				tt.status = "queued"
				tt.due = v.Due
			case memory.Saved:
				tt.status = "saved"
				tt.entry = v.EntryID
			case memory.Failed:
				tt.status = "failed"
				tt.reason = "capture_failed"
			case memory.Skipped:
				tt.status = "skipped"
				tt.reason = "capture_skipped"
			default:
				tt.status = "processing"
			}

			raw, err := json.Marshal(tt.s)
			Expect(err).NotTo(HaveOccurred())
			r := store.CaptureRecord{ID: "capture", Identity: stateJobForStatusTest(state).Identity, State: reflect.TypeOf(tt.s).Name(), InputType: func() string {
				if _, ok := state.(memory.Writing); ok {
					return "FrozenPayload"
				}
				if _, ok := state.(memory.Saved); ok {
					return "FrozenPayload"
				}
				if _, ok := state.(memory.Failed); ok {
					return "FrozenPayload"
				}
				return "ContentInput"
			}(), StateJSON: raw, RawText: "secret-raw"}
			got, err := projectMemoryStatus(r)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Status).To(Equal(tt.status), "%T", tt.s)
			Expect(got.NextRetry).To(Equal(tt.due), "%T", tt.s)
			Expect(got.EntryID).To(Equal(tt.entry), "%T", tt.s)
			Expect(got.Reason).To(Equal(tt.reason), "%T", tt.s)
			Expect(got.RetryEligible).To(BeFalse(), "%T", tt.s)
			b, err := json.Marshal(got)
			Expect(err).NotTo(HaveOccurred())
			for _, secret := range []string{"secret-input", "secret-error", "secret-principal", "secret-raw"} {
				Expect(string(b)).NotTo(ContainSubstring(secret))
			}
		}
	})
})

func stateJobForStatusTest(s memory.State) memory.Job {
	switch v := s.(type) {
	case memory.Queued:
		return v.Job
	case memory.RetryWaiting:
		return v.Job
	case memory.Saved:
		return v.Job
	case memory.Failed:
		return v.Job
	case memory.Skipped:
		return v.Job
	case memory.Authorizing:
		return v.Work.Job
	case memory.Transforming:
		return v.Work.Job
	case memory.PersistingPayload:
		return v.Work.Job
	case memory.Writing:
		return v.Work.Job
	}
	panic("unknown state")
}

var _ = Describe("memory safe reasons", func() {
	It("allowlists safe codes and hides unknown backend details", func() {
		for _, tt := range []struct{ raw, code string }{
			{"attempts exhausted or expired", "exhausted_or_expired"},
			{"capture expired or exhausted", "exhausted_or_expired"},
			{"capture expired", "expired"}, {"recovery exhausted", "exhausted"},
			{"current policy revoked; no destination fallback", "policy_revoked"},
			{"current authorization denied", "authorization_denied"},
			{"backend tombstone; do not recreate", "deleted"},
			{"empty input", "empty_input"}, {"empty summary", "empty_summary"},
			{"summary failed; no content fallback", "summary_failed"},
			{"missing exact entry identifier", "missing_entry_id"},
			{"capture persistence unavailable; payload not retained", "payload_unavailable"},
			{"https://secret:token@backend/private", "capture_failed"},
		} {
			Expect(safeCaptureReason(tt.raw, "capture_failed")).To(Equal(tt.code), tt.raw)
		}
	})
})

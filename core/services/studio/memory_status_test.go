// SPDX-License-Identifier: MIT
package studio

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/memory"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

func TestMemoryStatusProjection(t *testing.T) {
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

		raw, _ := json.Marshal(tt.s)
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
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != tt.status || got.NextRetry != tt.due || got.EntryID != tt.entry || got.Reason != tt.reason || got.RetryEligible {
			t.Fatalf("%T: %+v", tt.s, got)
		}
		b, _ := json.Marshal(got)
		for _, secret := range []string{"secret-input", "secret-error", "secret-principal", "secret-raw"} {
			if strings.Contains(string(b), secret) {
				t.Fatalf("leak: %s", b)
			}
		}
	}
}

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

func TestMemorySafeReasons(t *testing.T) {
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
		if got := safeCaptureReason(tt.raw, "capture_failed"); got != tt.code {
			t.Fatalf("%q: %s", tt.raw, got)
		}
	}
}

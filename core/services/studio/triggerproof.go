package studio

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/triggers"
)

// The states a trigger tool reports for the trigger it touched.
const (
	stateProposed       = "proposed"
	stateActive         = "active"
	statePaused         = "paused"
	stateDeletedPending = "deleted_pending"
	stateExpired        = "expired"
	stateGone           = "gone"
	stateWithdrawn      = "withdrawn"
)

// triggerProof is what a trigger tool tells the model about the stored state of
// the trigger it touched, so the model says no more than the store holds.
type triggerProof struct {
	Ref    string `json:"ref"`
	State  string `json:"state"`
	Active bool   `json:"active"`
	// NextRun is the next run by the schedule and the zone, RFC 3339 in the
	// zone of the trigger. It is empty for a trigger that watches a folder, for
	// a paused one, and for a proposal whose zone is not known yet.
	NextRun string `json:"next_run,omitempty"`
	// Expires is when a proposal lapses, when it does.
	Expires string `json:"expires,omitempty"`
	// Now is the time of the read, in the zone of the trigger (UTC when it has
	// none): the model needs no clock of its own to judge next_run.
	Now string `json:"now"`
	// Change is "pending" while an update or a delete waits for the person.
	Change string `json:"change,omitempty"`
}

// proofMark starts the proof in the result of a tool.
const proofMark = " Stored state: "

// proofOf reads the record id back from the store and words its state. It reads
// the store, not the struct that the write came from, so a write that did not
// land cannot be reported as one that did. For a change record the proof is the
// one of the trigger it is about.
func (h *Triggers) proofOf(ctx context.Context, id string) string {
	p, ok := h.readProof(ctx, id)
	if !ok {
		return proofMark + `{"error":"the store has no such record after the write"} Do not say anything was set.`
	}
	out, _ := json.Marshal(p)
	return proofMark + string(out)
}

func (h *Triggers) readProof(ctx context.Context, id string) (triggerProof, bool) {
	rec, err := h.o.Store.GetTrigger(ctx, id)
	if err != nil {
		return triggerProof{}, false
	}
	now := h.o.Clock()
	if !rec.IsChange() {
		return h.proofFor(rec, now), true
	}
	target, err := h.o.Store.GetTrigger(ctx, rec.Change.Target)
	if err != nil {
		// The target is gone: a delete that was applied, or removed since.
		return triggerProof{Ref: TriggerRef(rec.Change.Target), State: stateGone}, true
	}
	p := h.proofFor(target, now)
	if rec.Outcome == "" {
		p.Change = "pending"
		if rec.Change.Op == triggers.ChangeDelete && !target.Proposed {
			p.State = stateDeletedPending
		}
	}
	return p, true
}

func (h *Triggers) proofFor(t triggers.Trigger, now time.Time) triggerProof {
	p := triggerProof{Ref: TriggerRef(t.ID), Active: t.Enabled && !t.Proposed}
	loc, err := t.Location()
	if err != nil {
		loc = time.UTC
	}
	p.Now = now.In(loc).Format(time.RFC3339)
	switch {
	case t.Proposed && t.Outcome == triggers.OutcomeExpired:
		p.State = stateExpired
	case t.Proposed && t.Outcome == triggers.OutcomeDeclined:
		p.State = stateWithdrawn
	case t.Proposed:
		p.State = stateProposed
	case t.Enabled:
		p.State = stateActive
	default:
		p.State = statePaused
	}
	if !t.Route.Expires.IsZero() && p.State == stateProposed {
		p.Expires = t.Route.Expires.UTC().Format(time.RFC3339)
	}
	if p.State == stateActive || p.State == stateProposed {
		if next, ok := h.nextRun(t, now); ok {
			p.NextRun = next.Format(time.RFC3339)
		}
	}
	return p
}

// nextRun is the next time the schedule of t runs after now, in its own zone.
// A proposal that names no zone has none yet: the person's zone is taken when
// they confirm, so no time is promised.
func (h *Triggers) nextRun(t triggers.Trigger, now time.Time) (time.Time, bool) {
	if t.Kind != triggers.KindSchedule || (t.Proposed && t.TZ == "") {
		return time.Time{}, false
	}
	sch, err := triggers.ParseSchedule(t.Schedule)
	if err != nil {
		return time.Time{}, false
	}
	loc, err := t.Location()
	if err != nil {
		return time.Time{}, false
	}
	next, ok := sch.Next(now, loc)
	if !ok {
		return time.Time{}, false
	}
	return next.In(loc), true
}

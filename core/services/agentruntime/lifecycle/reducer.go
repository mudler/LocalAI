// SPDX-License-Identifier: MIT
package lifecycle

import (
	"fmt"
	"slices"
)

// Reduce is the one-for-one Go adaptation of documentation rules R00–R50.
// Returned effects are descriptions, not permission to bypass durable CAS.
func Reduce(input State, e Event) (Transition, error) {
	if err := validate(input); err != nil {
		return Transition{}, err
	}
	if !knownEvent(e) {
		return Transition{}, fmt.Errorf("invalid lifecycle event %T", e)
	}
	s := input
	s.Queue = slices.Clone(input.Queue)
	s.Accepted = slices.Clone(input.Accepted)
	s.Settled = slices.Clone(input.Settled)
	s.Journal = slices.Clone(input.Journal)
	p := s.Phase
	i := e.identity()
	kind := e.tag()
	current := turn(p)
	out := func(rule, guard string, changed bool, effects ...Effect) (Transition, error) {
		if changed {
			s.Rev++
		}
		if err := validate(s); err != nil {
			return Transition{}, fmt.Errorf("%s: %w", rule, err)
		}
		return Transition{State: s, Effects: effects, Rule: rule, Guard: guard}, nil
	}
	emit := func(rule, guard string, effects ...Effect) (Transition, error) {
		return out(rule, guard, true, effects...)
	}
	ignore := func(rule, guard string) (Transition, error) { return out(rule, guard, false) }
	interrupt := func(reason string) {
		for n := range s.Journal {
			if s.Journal[n].Status == InFlight {
				s.Journal[n].Status = Unknown
			}
		}
		tool := NoTool
		switch v := p.(type) {
		case Running:
			if v.Tool == InFlight {
				tool = Unknown
			}
		case Interrupted:
			tool = v.Tool
		}
		s.Phase = Interrupted{current, reason, tool}
		s.Epoch++
	}
	start := func(id string) Effect { s.Attempt++; s.Phase = Running{id, NoTool}; return Run{id, s.Attempt, s.Epoch} }
	if i.Principal != s.Principal {
		return ignore("R01", "principal mismatch; no data returned by API")
	}
	if _, ok := p.(Deleted); ok {
		return ignore("R02", "tombstone absorbs every event")
	}
	if kind == "Disconnect" || kind == "Reconnect" {
		s.Connection = Detached
		if kind == "Reconnect" {
			s.Connection = Attached
			return emit("R03", "view lifecycle only", ReplaySanitizedEvents{})
		}
		return emit("R03", "view lifecycle only")
	}
	if i.Rev != s.Rev {
		return ignore("R05", "CAS revision mismatch; client retries same id after refresh")
	}
	if i.Epoch != s.Epoch {
		return ignore("R06", "stale fencing epoch")
	}
	if kind == "StorageFailed" || kind == "CheckpointFailed" {
		interrupt(kind)
		s.Lease = Lost
		return emit("R04", "storage failure closes admission", CancelTree{}, CloseAdmission{})
	}
	if kind == "AcquireLease" {
		if s.Lease != Lost {
			return ignore("R07", "lease already owned")
		}
		s.Lease = Owned
		s.Epoch++
		return emit("R08", "external lease CAS succeeded; no automatic resume")
	}
	if kind == "Restart" || kind == "LeaseLost" {
		if kind == "LeaseLost" {
			s.Lease = Lost
		}
		switch p.(type) {
		case Idle, Interrupted:
			s.Epoch++
		default:
			interrupt(kind)
		}
		if kind == "Restart" {
			s.Lease = Owned
		}
		return emit("R09", "fence prior worker; queue retained; no dispatch", CancelTree{})
	}
	if s.Lease != Owned {
		return ignore("R10", "no owner lease; admission closed")
	}
	if kind == "Delete" {
		s.Phase = Deleted{}
		s.Queue = nil
		s.Epoch++
		return emit("R11", "durable tombstone before cancellation", CancelTree{}, RevokeArtifactAccess{})
	}
	if kind == "Revoke" {
		s.Access = Revoked
		_, parked := p.(Parked)
		if active(p) || parked {
			interrupt("AccessRevoked")
		}
		return emit("R12", "revoke wins over prior approval", CancelTree{}, PurgeTaintedContext{})
	}
	if v, ok := e.(Submit); ok {
		if v.ID == "" {
			return ignore("R13", "turn id required")
		}
		if slices.Contains(s.Accepted, v.ID) {
			return ignore("R14", "turn id deduplication")
		}
		closed := false
		switch v := p.(type) {
		case Interrupted, Stopping:
			closed = true
		case Parked:
			closed = v.Reason != Checkpoint
		}
		if s.Access != Granted || closed {
			return ignore("R15", "admission closed until explicit recovery")
		}
		_, idle := p.(Idle)
		if !idle && len(s.Queue) == 2 {
			return ignore("R16", "bounded queue full; no accepted id")
		}
		s.Accepted = append(s.Accepted, v.ID)
		if idle {
			return emit("R17", "idle submit starts immediately", start(v.ID))
		}
		s.Queue = append(s.Queue, v.ID)
		return emit("R18", "busy submit queues FIFO; never injects")
	}
	if kind == "Resume" {
		available := false
		switch v := p.(type) {
		case Interrupted:
			available = true
		case Parked:
			available = v.Reason != Checkpoint
		}
		if !available || s.Access != Granted {
			return ignore("R19", "resume unavailable")
		}
		s.Epoch++
		if current == "" {
			s.Phase = Idle{}
			return emit("R20", "no interrupted input to restore")
		}
		s.Attempt++
		s.Phase = Restoring{current}
		return emit("R20", "same logical turn; FIFO held until settlement", LoadCheckpointAndToolJournal{current, s.Attempt})
	}
	if kind == "Stop" {
		if !active(p) {
			return ignore("R21", "nothing executing")
		}
		for n := range s.Journal {
			if s.Journal[n].Status == InFlight {
				s.Journal[n].Status = Unknown
			}
		}
		s.Phase = Stopping{current}
		s.Epoch++
		return emit("R22", "stop fences callbacks and holds queue", CancelTree{})
	}
	// All remaining variants are execution callbacks, including approval and answers.
	if i.Turn != current {
		return ignore("R23", "late or unrelated turn")
	}
	if i.Attempt != s.Attempt {
		return ignore("R41", "stale execution attempt")
	}
	return reduceCallback(&s, e, emit, ignore, interrupt)
}

func knownEvent(e Event) bool {
	switch e.(type) {
	case Submit, Complete, CheckpointOK, CheckpointFailed, StorageFailed, ToolRequest, ToolResult, Approve, Deny, Ask, Answer, Compact, CompactOK, CompactFailed, Stop, Stopped, Delete, Restart, Resume, Revoke, LeaseLost, AcquireLease, Disconnect, Reconnect, Timeout, RestoreOK, RestoreFailed, ReconcileOK:
		return true
	}
	return false
}

// SPDX-License-Identifier: MIT
package runner

import (
	"errors"
	"reflect"
	"slices"
	"strings"
)

var errInvalid = errors.New("runner: invalid model state or transition")

func knownState(s runnerState) bool {
	switch s.(type) {
	case idle, restoring, running, cancelling, quiescent, sealing, settling, interrupted, reconciliation, deleted:
		return true
	}
	return false
}
func knownEvent(e event) bool {
	switch e.(type) {
	case attachEvent, detachEvent, beginEvent, restoreSucceededEvent, restoreFailedEvent, streamEvent, turnFinishedEvent, stopEvent, leaseLostEvent, cancelTimedOutEvent, quiescenceConfirmedEvent, sealSucceededEvent, sealFailedEvent, settleSucceededEvent, settleFailedEvent, unknownJournalEvent, resumeEvent, deleteEvent, lateCallbackEvent:
		return true
	}
	return false
}
func CheckInvariants(s runnerState) error {
	if !knownState(s) {
		return errInvalid
	}
	d := s.payload()
	if !d.id.valid() || d.outcome > leaseLost {
		return errInvalid
	}
	// Empty input represents a settled attempt only, not an executable turn.
	// Late uncertainty may hold that empty attempt; it cannot restore or start it.
	if len(d.input) == 0 {
		if (s.phase() != "Idle" && s.phase() != "Reconciliation" && s.phase() != "Deleted") || d.outcome != finished || len(d.ciphertext) != 0 {
			return errInvalid
		}
	}
	switch s.phase() {
	case "Idle", "Restoring", "Running", "Quiescent":
		if d.outcome != finished || len(d.ciphertext) != 0 {
			return errInvalid
		}
	case "Cancelling":
		if d.outcome == finished || len(d.ciphertext) != 0 {
			return errInvalid
		}
	case "Sealing":
		if len(d.ciphertext) != 0 {
			return errInvalid
		}
	case "Settling":
		if len(d.ciphertext) == 0 {
			return errInvalid
		}
	case "Interrupted", "Reconciliation", "Deleted":
		// Restore/seal failures legitimately retain finished as the outcome.
		// Settlement failure/interruption/uncertainty may retain ciphertext.
	}
	return nil
}
func freshResume(s runnerState, e event) bool {
	a, b := s.payload().id, e.payload().id
	return e.name() == "Resume" && (s.phase() == "Interrupted" || s.phase() == "Reconciliation") && a.principal == b.principal && a.conversation == b.conversation && a.turn == b.turn && b.attempt > a.attempt && b.epoch >= a.epoch
}
func lookup(p, n string) pairRule {
	for _, r := range pairRules {
		if r.phase == p && r.event == n {
			return r
		}
	}
	panic("runner: unenumerated pair")
}

// PureReduce returns data only. Quiescent means Run returned, not joined; only
// QuiescenceConfirmed can authorize ExportAndSeal. No timeout substitutes for it.
func PureReduce(s runnerState, e event) transition {
	if CheckInvariants(s) != nil || !knownEvent(e) || !e.payload().id.valid() {
		return transition{state: s, rule: "RR001", guard: "invalid input fails closed"}
	}
	d := s.payload().clone()
	v := e.payload()
	same := d.id.equal(v.id)
	if !same && !freshResume(s, e) {
		return transition{state: makeState(s.phase(), d), rule: "RR002", guard: "stale host binding rejected"}
	}
	r := lookup(s.phase(), e.name())
	out := transition{state: makeState(s.phase(), d), rule: r.rule, guard: r.guard}
	if s.phase() == "Deleted" || r.next == "=" && r.effects == "" && e.name() != "Attach" && e.name() != "Detach" {
		return out
	}
	if !v.trusted || v.now == 0 {
		out.guard += "; missing trusted host fact"
		return out
	}
	if e.name() == "Resume" && (!freshResume(s, e) || !v.reconciled) {
		out.guard += "; fresh reconciled join proof required"
		return out
	}
	if e.name() == "SealSucceeded" && len(v.content) == 0 {
		out.guard += "; empty ciphertext rejected"
		return out
	}
	if (e.name() == "Begin" || e.name() == "Resume") && len(d.input) == 0 {
		out.guard += "; missing active input"
		return out
	}
	next := r.next
	if next == "=" {
		next = s.phase()
	}
	fx := r.effects
	switch e.name() {
	case "Attach":
		d.attached = true
	case "Detach":
		d.attached = false
	case "Stop":
		d.outcome = stopped
	case "LeaseLost":
		d.outcome = leaseLost
	case "Resume":
		d.id = v.id
		d.outcome = finished
		d.ciphertext = nil
	case "SealSucceeded":
		d.ciphertext = slices.Clone(v.content)
	case "SettleSucceeded":
		if d.outcome != finished {
			next = "Interrupted"
			fx = "HoldInput"
		} else {
			d.input = nil
			d.ciphertext = nil
		}
	}
	out.state = makeState(next, d)
	for _, n := range strings.Fields(fx) {
		out.effects = append(out.effects, makeEffect(n, d))
	}
	return out
}

// CheckTransition checks safety independently of the reducer's rule lookup,
// then matches the canonical transition, including private payloads and ordering.
// A future executor MUST call this before interpreting any effects.
func CheckTransition(before runnerState, e event, r transition) error {
	if CheckInvariants(before) != nil || !knownEvent(e) || CheckInvariants(r.state) != nil {
		return errInvalid
	}
	a, b, v := before.payload(), r.state.payload(), e.payload()
	p, n := before.phase(), e.name()
	if !a.id.equal(b.id) && !(n == "Resume" && v.trusted && v.now > 0 && v.reconciled && freshResume(before, e) && b.id.equal(v.id)) {
		return errInvalid
	}
	if p == "Deleted" && (r.state.phase() != "Deleted" || len(r.effects) > 0) {
		return errInvalid
	}
	if !reflect.DeepEqual(a.input, b.input) && !(p == "Settling" && n == "SettleSucceeded" && v.trusted && v.now > 0 && a.id.equal(v.id) && a.outcome == finished && len(b.input) == 0) {
		return errInvalid
	}
	if n == "Detach" && (r.state.phase() != p || len(r.effects) > 0) {
		return errInvalid
	}
	for _, fx := range r.effects {
		if fx == nil || !fx.payload().id.equal(b.id) {
			return errInvalid
		}
		switch fx.(type) {
		case exportAndSealEffect:
			if (p != "Cancelling" && p != "Quiescent") || n != "QuiescenceConfirmed" || !v.trusted || v.now == 0 || !a.id.equal(v.id) || r.state.phase() != "Sealing" {
				return errInvalid
			}
		case startNextEffect:
			if p != "Settling" || n != "SettleSucceeded" || !v.trusted || v.now == 0 || !a.id.equal(v.id) || a.outcome != finished || r.state.phase() != "Idle" {
				return errInvalid
			}
		case settleCiphertextEffect:
			if p != "Sealing" || n != "SealSucceeded" || !v.trusted || v.now == 0 || !a.id.equal(v.id) || len(v.content) == 0 || !slices.Equal(fx.payload().content, v.content) {
				return errInvalid
			}
		case startTurnEffect:
			if p != "Restoring" || n != "RestoreSucceeded" || !v.trusted || v.now == 0 || !a.id.equal(v.id) || len(a.input) == 0 || a.outcome != finished {
				return errInvalid
			}
		case restoreSessionEffect:
			if len(a.input) == 0 || !v.trusted || v.now == 0 || !((p == "Idle" && n == "Begin") || ((p == "Interrupted" || p == "Reconciliation") && n == "Resume" && v.reconciled && freshResume(before, e))) {
				return errInvalid
			}
		case cancelSessionEffect, awaitQuiescenceEffect, emitSafeEffect, closeSessionEffect, holdInputEffect:
		default:
			return errInvalid
		}
		if fx.name() != "SettleCiphertext" && len(fx.payload().content) != 0 {
			return errInvalid
		}
	}
	if n == "CancelTimedOut" && r.state.phase() != p && (r.state.phase() == "Quiescent" || r.state.phase() == "Sealing" || r.state.phase() == "Settling") {
		return errInvalid
	}
	expected := PureReduce(before, e)
	if !reflect.DeepEqual(expected, r) {
		return errInvalid
	}
	return nil
}

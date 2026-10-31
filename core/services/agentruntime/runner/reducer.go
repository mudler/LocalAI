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

// Accept value variants before invoking any effect method. Inherited value
// methods on typed-nil pointers panic; nonnil pointers are unsupported too.
func knownEffect(e effect) bool {
	switch e.(type) {
	case restoreSessionEffect, startTurnEffect, cancelSessionEffect, awaitQuiescenceEffect, exportAndSealEffect, settleCiphertextEffect, startNextEffect, emitSafeEffect, closeSessionEffect, holdInputEffect:
		return true
	}
	return false
}

// Closures are opaque to reflect.DeepEqual. Compare every semantic field,
// including nil versus empty slices, without relying on diagnostic projections.
func sameData(a, b data) bool {
	x, y := a.fields(), b.fields()
	return x.id.equal(y.id) && x.attached == y.attached && x.outcome == y.outcome && reflect.DeepEqual(x.input, y.input) && reflect.DeepEqual(x.ciphertext, y.ciphertext)
}
func sameState(a, b runnerState) bool {
	return knownState(a) && knownState(b) && a.phase() == b.phase() && sameData(a.payload(), b.payload())
}
func sameTransition(a, b transition) bool {
	if a.rule != b.rule || a.guard != b.guard || !sameState(a.state, b.state) || (a.effects == nil) != (b.effects == nil) || len(a.effects) != len(b.effects) {
		return false
	}
	for i, x := range a.effects {
		y := b.effects[i]
		if !knownEffect(x) || !knownEffect(y) || x.name() != y.name() {
			return false
		}
		u, v := x.payload().fields(), y.payload().fields()
		if !u.id.equal(v.id) || !reflect.DeepEqual(u.content, v.content) {
			return false
		}
	}
	return true
}

func CheckInvariants(s runnerState) error {
	if !knownState(s) {
		return errInvalid
	}
	d := s.payload()
	if !d.fields().id.valid() || d.fields().outcome > leaseLost {
		return errInvalid
	}
	// Empty input represents a settled attempt only, not an executable turn.
	// Late uncertainty may hold that empty attempt; it cannot restore or start it.
	if len(d.fields().input) == 0 {
		if (s.phase() != "Idle" && s.phase() != "Reconciliation" && s.phase() != "Deleted") || d.fields().outcome != finished || len(d.fields().ciphertext) != 0 {
			return errInvalid
		}
	}
	switch s.phase() {
	case "Idle", "Restoring", "Running", "Quiescent":
		if d.fields().outcome != finished || len(d.fields().ciphertext) != 0 {
			return errInvalid
		}
	case "Cancelling":
		if d.fields().outcome == finished || len(d.fields().ciphertext) != 0 {
			return errInvalid
		}
	case "Sealing":
		if len(d.fields().ciphertext) != 0 {
			return errInvalid
		}
	case "Settling":
		if len(d.fields().ciphertext) == 0 {
			return errInvalid
		}
	case "Interrupted", "Reconciliation", "Deleted":
		// Restore/seal failures legitimately retain finished as the outcome.
		// Settlement failure/interruption/uncertainty may retain ciphertext.
	}
	return nil
}
func freshResume(s runnerState, e event) bool {
	a, b := s.payload().fields().id, e.payload().fields().id
	return e.name() == "Resume" && (s.phase() == "Interrupted" || s.phase() == "Reconciliation") && a.fields().principal == b.fields().principal && a.fields().conversation == b.fields().conversation && a.fields().turn == b.fields().turn && b.fields().attempt > a.fields().attempt && b.fields().epoch >= a.fields().epoch
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
	if CheckInvariants(s) != nil || !knownEvent(e) || !e.payload().fields().id.valid() {
		return transition{state: s, rule: "RR001", guard: "invalid input fails closed"}
	}
	d := s.payload().clone()
	v := e.payload()
	same := d.fields().id.equal(v.fields().id)
	if !same && !freshResume(s, e) {
		return transition{state: makeState(s.phase(), d), rule: "RR002", guard: "stale host binding rejected"}
	}
	r := lookup(s.phase(), e.name())
	out := transition{state: makeState(s.phase(), d), rule: r.rule, guard: r.guard}
	if s.phase() == "Deleted" || r.next == "=" && r.effects == "" && e.name() != "Attach" && e.name() != "Detach" {
		return out
	}
	if !v.fields().trusted || v.fields().now == 0 {
		out.guard += "; missing trusted host fact"
		return out
	}
	if e.name() == "Resume" && (!freshResume(s, e) || !v.fields().reconciled) {
		out.guard += "; fresh reconciled join proof required"
		return out
	}
	if e.name() == "SealSucceeded" && len(v.fields().content) == 0 {
		out.guard += "; empty ciphertext rejected"
		return out
	}
	if (e.name() == "Begin" || e.name() == "Resume") && len(d.fields().input) == 0 {
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
		d.fields().attached = true
	case "Detach":
		d.fields().attached = false
	case "Stop":
		d.fields().outcome = stopped
	case "LeaseLost":
		d.fields().outcome = leaseLost
	case "Resume":
		d.fields().id = v.fields().id
		d.fields().outcome = finished
		d.fields().ciphertext = nil
	case "SealSucceeded":
		d.fields().ciphertext = slices.Clone(v.fields().content)
	case "SettleSucceeded":
		if d.fields().outcome != finished {
			next = "Interrupted"
			fx = "HoldInput"
		} else {
			d.fields().input = nil
			d.fields().ciphertext = nil
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
	if !a.fields().id.equal(b.fields().id) && !(n == "Resume" && v.fields().trusted && v.fields().now > 0 && v.fields().reconciled && freshResume(before, e) && b.fields().id.equal(v.fields().id)) {
		return errInvalid
	}
	if p == "Deleted" && (r.state.phase() != "Deleted" || len(r.effects) > 0) {
		return errInvalid
	}
	if !reflect.DeepEqual(a.fields().input, b.fields().input) && !(p == "Settling" && n == "SettleSucceeded" && v.fields().trusted && v.fields().now > 0 && a.fields().id.equal(v.fields().id) && a.fields().outcome == finished && len(b.fields().input) == 0) {
		return errInvalid
	}
	if n == "Detach" && (r.state.phase() != p || len(r.effects) > 0) {
		return errInvalid
	}
	for _, fx := range r.effects {
		if !knownEffect(fx) || !fx.payload().fields().id.equal(b.fields().id) {
			return errInvalid
		}
		switch fx.(type) {
		case exportAndSealEffect:
			if (p != "Cancelling" && p != "Quiescent") || n != "QuiescenceConfirmed" || !v.fields().trusted || v.fields().now == 0 || !a.fields().id.equal(v.fields().id) || r.state.phase() != "Sealing" {
				return errInvalid
			}
		case startNextEffect:
			if p != "Settling" || n != "SettleSucceeded" || !v.fields().trusted || v.fields().now == 0 || !a.fields().id.equal(v.fields().id) || a.fields().outcome != finished || r.state.phase() != "Idle" {
				return errInvalid
			}
		case settleCiphertextEffect:
			if p != "Sealing" || n != "SealSucceeded" || !v.fields().trusted || v.fields().now == 0 || !a.fields().id.equal(v.fields().id) || len(v.fields().content) == 0 || !slices.Equal(fx.payload().fields().content, v.fields().content) {
				return errInvalid
			}
		case startTurnEffect:
			if p != "Restoring" || n != "RestoreSucceeded" || !v.fields().trusted || v.fields().now == 0 || !a.fields().id.equal(v.fields().id) || len(a.fields().input) == 0 || a.fields().outcome != finished {
				return errInvalid
			}
		case restoreSessionEffect:
			if len(a.fields().input) == 0 || !v.fields().trusted || v.fields().now == 0 || !((p == "Idle" && n == "Begin") || ((p == "Interrupted" || p == "Reconciliation") && n == "Resume" && v.fields().reconciled && freshResume(before, e))) {
				return errInvalid
			}
		case cancelSessionEffect, awaitQuiescenceEffect, emitSafeEffect, closeSessionEffect, holdInputEffect:
		default:
			return errInvalid
		}
		if fx.name() != "SettleCiphertext" && len(fx.payload().fields().content) != 0 {
			return errInvalid
		}
	}
	if n == "CancelTimedOut" && r.state.phase() != p && (r.state.phase() == "Quiescent" || r.state.phase() == "Sealing" || r.state.phase() == "Settling") {
		return errInvalid
	}
	expected := PureReduce(before, e)
	if !sameTransition(expected, r) {
		return errInvalid
	}
	return nil
}

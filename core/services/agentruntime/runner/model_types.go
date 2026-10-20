// SPDX-License-Identifier: MIT
// Package runner contains only the tool-disabled runner model. No executor is
// connected. Host facts are not authorization: the eventual shell must supply
// synchronized ACL, lease, journal, key and durable settlement proofs. This model
// refines lifecycle cancellation/attachment/FIFO concepts, not its grant engine.
package runner

import (
	"fmt"
	"io"
	"slices"
)

// Direct formatting/JSON redact. Sensitive leaves live behind private pointers:
// fmt's recursive private-field formatting prints nested pointers as addresses,
// not their referents. This protects raw wrappers too, without a global registry.
// Pointer addresses (including explicit %p) are not content redaction failures,
// but are not stable diagnostics; use safeLog in future host logs. Constructors
// and payload accessors clone owned storage, identity fields and byte buffers.
// Explicit payload access or dereferencing private storage is trusted internal
// access, not a redaction boundary. No unsafe/reflection-introspection claim.
type redacted struct{}

func (redacted) Format(f fmt.State, _ rune)   { _, _ = io.WriteString(f, "[runner:redacted]") }
func (redacted) MarshalJSON() ([]byte, error) { return []byte(`"[runner:redacted]"`), nil }

// identity is a value supplied by the host, never inferred from callback order.
// Conversation is explicit in addition to lifecycle's principal/turn/attempt/epoch.
type identity struct {
	redacted
	*identityFields
}

type identityFields struct {
	principal, conversation, turn string
	attempt, epoch                uint64
}

func (i identity) valid() bool {
	return i.identityFields != nil && i.principal != "" && i.conversation != "" && i.turn != "" && i.attempt > 0 && i.epoch > 0
}

func (i identity) clone() identity {
	if i.identityFields == nil {
		return identity{}
	}
	fields := *i.identityFields
	return identity{identityFields: &fields}
}
func (i identity) equal(other identity) bool {
	if i.identityFields == nil || other.identityFields == nil {
		return i.identityFields == other.identityFields
	}
	return *i.identityFields == *other.identityFields
}

type completion uint8

const (
	finished completion = iota
	stopped
	leaseLost
)

type data struct {
	redacted
	*dataFields
}

type dataFields struct {
	id                identity
	attached          bool
	input, ciphertext []byte
	outcome           completion
}

func (d data) clone() data {
	if d.dataFields == nil {
		return data{dataFields: &dataFields{}}
	}
	fields := *d.dataFields
	d.dataFields = &fields
	d.id = d.id.clone()
	d.input = slices.Clone(d.input)
	d.ciphertext = slices.Clone(d.ciphertext)
	return d
}

type runnerState interface {
	phase() string
	payload() data
	sealedState()
}
type event interface {
	name() string
	payload() eventData
	sealedEvent()
}

// now is explicit trusted host time, not a clock read. Proof booleans below are
// modeled host facts only, and deliberately have no public construction API.
type eventData struct {
	redacted
	*eventFields
}

type eventFields struct {
	id                  identity
	now                 uint64
	content             []byte
	trusted, reconciled bool
}
type effect interface {
	name() string
	payload() effectData
	sealedEffect()
}
type effectData struct {
	redacted
	*effectFields
}

type effectFields struct {
	id      identity
	content []byte
}

func (e eventData) clone() eventData {
	if e.eventFields == nil {
		return eventData{eventFields: &eventFields{}}
	}
	fields := *e.eventFields
	fields.id = fields.id.clone()
	fields.content = slices.Clone(fields.content)
	return eventData{eventFields: &fields}
}
func (e effectData) clone() effectData {
	if e.effectFields == nil {
		return effectData{effectFields: &effectFields{}}
	}
	fields := *e.effectFields
	fields.id = fields.id.clone()
	fields.content = slices.Clone(fields.content)
	return effectData{effectFields: &fields}
}

type transition struct {
	redacted
	state       runnerState
	effects     []effect
	rule, guard string
}

type idle struct{ data }

func (idle) phase() string   { return "Idle" }
func (s idle) payload() data { return s.data.clone() }
func (idle) sealedState()    {}

type restoring struct{ data }

func (restoring) phase() string   { return "Restoring" }
func (s restoring) payload() data { return s.data.clone() }
func (restoring) sealedState()    {}

type running struct{ data }

func (running) phase() string   { return "Running" }
func (s running) payload() data { return s.data.clone() }
func (running) sealedState()    {}

type cancelling struct{ data }

func (cancelling) phase() string   { return "Cancelling" }
func (s cancelling) payload() data { return s.data.clone() }
func (cancelling) sealedState()    {}

type quiescent struct{ data }

func (quiescent) phase() string   { return "Quiescent" }
func (s quiescent) payload() data { return s.data.clone() }
func (quiescent) sealedState()    {}

type sealing struct{ data }

func (sealing) phase() string   { return "Sealing" }
func (s sealing) payload() data { return s.data.clone() }
func (sealing) sealedState()    {}

type settling struct{ data }

func (settling) phase() string   { return "Settling" }
func (s settling) payload() data { return s.data.clone() }
func (settling) sealedState()    {}

type interrupted struct{ data }

func (interrupted) phase() string   { return "Interrupted" }
func (s interrupted) payload() data { return s.data.clone() }
func (interrupted) sealedState()    {}

type reconciliation struct{ data }

func (reconciliation) phase() string   { return "Reconciliation" }
func (s reconciliation) payload() data { return s.data.clone() }
func (reconciliation) sealedState()    {}

type deleted struct{ data }

func (deleted) phase() string   { return "Deleted" }
func (s deleted) payload() data { return s.data.clone() }
func (deleted) sealedState()    {}

type attachEvent struct{ eventData }

func (attachEvent) name() string         { return "Attach" }
func (e attachEvent) payload() eventData { return e.eventData.clone() }
func (attachEvent) sealedEvent()         {}

type detachEvent struct{ eventData }

func (detachEvent) name() string         { return "Detach" }
func (e detachEvent) payload() eventData { return e.eventData.clone() }
func (detachEvent) sealedEvent()         {}

type beginEvent struct{ eventData }

func (beginEvent) name() string         { return "Begin" }
func (e beginEvent) payload() eventData { return e.eventData.clone() }
func (beginEvent) sealedEvent()         {}

type restoreSucceededEvent struct{ eventData }

func (restoreSucceededEvent) name() string         { return "RestoreSucceeded" }
func (e restoreSucceededEvent) payload() eventData { return e.eventData.clone() }
func (restoreSucceededEvent) sealedEvent()         {}

type restoreFailedEvent struct{ eventData }

func (restoreFailedEvent) name() string         { return "RestoreFailed" }
func (e restoreFailedEvent) payload() eventData { return e.eventData.clone() }
func (restoreFailedEvent) sealedEvent()         {}

type streamEvent struct{ eventData }

func (streamEvent) name() string         { return "Stream" }
func (e streamEvent) payload() eventData { return e.eventData.clone() }
func (streamEvent) sealedEvent()         {}

type turnFinishedEvent struct{ eventData }

func (turnFinishedEvent) name() string         { return "TurnFinished" }
func (e turnFinishedEvent) payload() eventData { return e.eventData.clone() }
func (turnFinishedEvent) sealedEvent()         {}

type stopEvent struct{ eventData }

func (stopEvent) name() string         { return "Stop" }
func (e stopEvent) payload() eventData { return e.eventData.clone() }
func (stopEvent) sealedEvent()         {}

type leaseLostEvent struct{ eventData }

func (leaseLostEvent) name() string         { return "LeaseLost" }
func (e leaseLostEvent) payload() eventData { return e.eventData.clone() }
func (leaseLostEvent) sealedEvent()         {}

type cancelTimedOutEvent struct{ eventData }

func (cancelTimedOutEvent) name() string         { return "CancelTimedOut" }
func (e cancelTimedOutEvent) payload() eventData { return e.eventData.clone() }
func (cancelTimedOutEvent) sealedEvent()         {}

type quiescenceConfirmedEvent struct{ eventData }

func (quiescenceConfirmedEvent) name() string         { return "QuiescenceConfirmed" }
func (e quiescenceConfirmedEvent) payload() eventData { return e.eventData.clone() }
func (quiescenceConfirmedEvent) sealedEvent()         {}

type sealSucceededEvent struct{ eventData }

func (sealSucceededEvent) name() string         { return "SealSucceeded" }
func (e sealSucceededEvent) payload() eventData { return e.eventData.clone() }
func (sealSucceededEvent) sealedEvent()         {}

type sealFailedEvent struct{ eventData }

func (sealFailedEvent) name() string         { return "SealFailed" }
func (e sealFailedEvent) payload() eventData { return e.eventData.clone() }
func (sealFailedEvent) sealedEvent()         {}

type settleSucceededEvent struct{ eventData }

func (settleSucceededEvent) name() string         { return "SettleSucceeded" }
func (e settleSucceededEvent) payload() eventData { return e.eventData.clone() }
func (settleSucceededEvent) sealedEvent()         {}

type settleFailedEvent struct{ eventData }

func (settleFailedEvent) name() string         { return "SettleFailed" }
func (e settleFailedEvent) payload() eventData { return e.eventData.clone() }
func (settleFailedEvent) sealedEvent()         {}

type unknownJournalEvent struct{ eventData }

func (unknownJournalEvent) name() string         { return "UnknownJournal" }
func (e unknownJournalEvent) payload() eventData { return e.eventData.clone() }
func (unknownJournalEvent) sealedEvent()         {}

type resumeEvent struct{ eventData }

func (resumeEvent) name() string         { return "Resume" }
func (e resumeEvent) payload() eventData { return e.eventData.clone() }
func (resumeEvent) sealedEvent()         {}

type deleteEvent struct{ eventData }

func (deleteEvent) name() string         { return "Delete" }
func (e deleteEvent) payload() eventData { return e.eventData.clone() }
func (deleteEvent) sealedEvent()         {}

type lateCallbackEvent struct{ eventData }

func (lateCallbackEvent) name() string         { return "LateCallback" }
func (e lateCallbackEvent) payload() eventData { return e.eventData.clone() }
func (lateCallbackEvent) sealedEvent()         {}

type restoreSessionEffect struct{ effectData }

func (restoreSessionEffect) name() string          { return "RestoreSession" }
func (e restoreSessionEffect) payload() effectData { return e.effectData.clone() }
func (restoreSessionEffect) sealedEffect()         {}

type startTurnEffect struct{ effectData }

func (startTurnEffect) name() string          { return "StartTurn" }
func (e startTurnEffect) payload() effectData { return e.effectData.clone() }
func (startTurnEffect) sealedEffect()         {}

type cancelSessionEffect struct{ effectData }

func (cancelSessionEffect) name() string          { return "CancelSession" }
func (e cancelSessionEffect) payload() effectData { return e.effectData.clone() }
func (cancelSessionEffect) sealedEffect()         {}

type awaitQuiescenceEffect struct{ effectData }

func (awaitQuiescenceEffect) name() string          { return "AwaitQuiescence" }
func (e awaitQuiescenceEffect) payload() effectData { return e.effectData.clone() }
func (awaitQuiescenceEffect) sealedEffect()         {}

type exportAndSealEffect struct{ effectData }

func (exportAndSealEffect) name() string          { return "ExportAndSeal" }
func (e exportAndSealEffect) payload() effectData { return e.effectData.clone() }
func (exportAndSealEffect) sealedEffect()         {}

type settleCiphertextEffect struct{ effectData }

func (settleCiphertextEffect) name() string          { return "SettleCiphertext" }
func (e settleCiphertextEffect) payload() effectData { return e.effectData.clone() }
func (settleCiphertextEffect) sealedEffect()         {}

// StartNext is permission to hand off to a future host coordinator, not a
// second turn executed by this reducer. This machine owns one immutable attempt
// (Resume explicitly creates a fresh attempt for the SAME held turn). Successful
// settlement leaves empty Idle with the old identity; Begin cannot reuse it.
// The host must admit the next FIFO item through a fresh initial constructor,
// with its own identity/input. No production constructor/coordinator exists here.
type startNextEffect struct{ effectData }

func (startNextEffect) name() string          { return "StartNext" }
func (e startNextEffect) payload() effectData { return e.effectData.clone() }
func (startNextEffect) sealedEffect()         {}

type emitSafeEffect struct{ effectData }

func (emitSafeEffect) name() string          { return "EmitSafe" }
func (e emitSafeEffect) payload() effectData { return e.effectData.clone() }
func (emitSafeEffect) sealedEffect()         {}

type closeSessionEffect struct{ effectData }

func (closeSessionEffect) name() string          { return "CloseSession" }
func (e closeSessionEffect) payload() effectData { return e.effectData.clone() }
func (closeSessionEffect) sealedEffect()         {}

type holdInputEffect struct{ effectData }

func (holdInputEffect) name() string          { return "HoldInput" }
func (e holdInputEffect) payload() effectData { return e.effectData.clone() }
func (holdInputEffect) sealedEffect()         {}

func makeState(phase string, d data) runnerState {
	switch phase {
	case "Idle":
		return idle{d.clone()}
	case "Restoring":
		return restoring{d.clone()}
	case "Running":
		return running{d.clone()}
	case "Cancelling":
		return cancelling{d.clone()}
	case "Quiescent":
		return quiescent{d.clone()}
	case "Sealing":
		return sealing{d.clone()}
	case "Settling":
		return settling{d.clone()}
	case "Interrupted":
		return interrupted{d.clone()}
	case "Reconciliation":
		return reconciliation{d.clone()}
	case "Deleted":
		return deleted{d.clone()}
	}
	panic("invalid runner phase")
}

func makeEffect(name string, d data) effect {
	e := effectData{effectFields: &effectFields{id: d.id.clone()}}
	if name == "SettleCiphertext" {
		e.content = slices.Clone(d.ciphertext)
	}
	switch name {
	case "RestoreSession":
		return restoreSessionEffect{e}
	case "StartTurn":
		return startTurnEffect{e}
	case "CancelSession":
		return cancelSessionEffect{e}
	case "AwaitQuiescence":
		return awaitQuiescenceEffect{e}
	case "ExportAndSeal":
		return exportAndSealEffect{e}
	case "SettleCiphertext":
		return settleCiphertextEffect{e}
	case "StartNext":
		return startNextEffect{e}
	case "EmitSafe":
		return emitSafeEffect{e}
	case "CloseSession":
		return closeSessionEffect{e}
	case "HoldInput":
		return holdInputEffect{e}
	}
	panic("invalid runner effect")
}

func makeEvent(name string, e eventData) event {
	e = e.clone()
	switch name {
	case "Attach":
		return attachEvent{e}
	case "Detach":
		return detachEvent{e}
	case "Begin":
		return beginEvent{e}
	case "RestoreSucceeded":
		return restoreSucceededEvent{e}
	case "RestoreFailed":
		return restoreFailedEvent{e}
	case "Stream":
		return streamEvent{e}
	case "TurnFinished":
		return turnFinishedEvent{e}
	case "Stop":
		return stopEvent{e}
	case "LeaseLost":
		return leaseLostEvent{e}
	case "CancelTimedOut":
		return cancelTimedOutEvent{e}
	case "QuiescenceConfirmed":
		return quiescenceConfirmedEvent{e}
	case "SealSucceeded":
		return sealSucceededEvent{e}
	case "SealFailed":
		return sealFailedEvent{e}
	case "SettleSucceeded":
		return settleSucceededEvent{e}
	case "SettleFailed":
		return settleFailedEvent{e}
	case "UnknownJournal":
		return unknownJournalEvent{e}
	case "Resume":
		return resumeEvent{e}
	case "Delete":
		return deleteEvent{e}
	case "LateCallback":
		return lateCallbackEvent{e}
	}
	panic("invalid runner event")
}

var phases = [...]string{"Idle", "Restoring", "Running", "Cancelling", "Quiescent", "Sealing", "Settling", "Interrupted", "Reconciliation", "Deleted"}
var events = [...]string{"Attach", "Detach", "Begin", "RestoreSucceeded", "RestoreFailed", "Stream", "TurnFinished", "Stop", "LeaseLost", "CancelTimedOut", "QuiescenceConfirmed", "SealSucceeded", "SealFailed", "SettleSucceeded", "SettleFailed", "UnknownJournal", "Resume", "Delete", "LateCallback"}

// logHolder is a lossy diagnostic projection, NOT a payload transport. It
// deliberately retains no identity, content, interface, pointer or closure over
// its argument. Thus even fmt's private-field reflection sees no private data.
// Adding a raw value here would recreate the Formatter bypass. Execution must
// use the private model values, never this holder. No global registry is used.
type logHolder struct{ redacted }

func safeLog(_ any) logHolder { return logHolder{} }

// SPDX-License-Identifier: MIT

package proposalflow

import "fmt"

// Step is one event of a trace with what the reducer must answer.
type Step struct {
	Event   Event
	Rule    string
	Effects []string
}

// Trace is a named scenario: a list of events with the expected rule ids and
// effects in order, and the expected final state. The tests assert all three.
// The viewer replays the same traces through the real reducer.
type Trace struct {
	Name  string
	About string
	Steps []Step
	Final State
}

// StepResult is what the reducer really did for one step.
type StepResult struct {
	Event   Event
	Before  State
	After   State
	Rule    string
	Effects []Effect
}

// Run feeds the events through Reduce from the start state.
func Run(start State, events []Event) []StepResult {
	out := make([]StepResult, 0, len(events))
	s := start
	for _, e := range events {
		next, fx, rule := Reduce(s, e)
		out = append(out, StepResult{Event: e, Before: s, After: next, Rule: rule, Effects: fx})
		s = next
	}
	return out
}

// Events lists the events of a trace.
func (t Trace) Events() []Event {
	ev := make([]Event, len(t.Steps))
	for i, s := range t.Steps {
		ev[i] = s.Event
	}
	return ev
}

// Verify replays the trace and reports every mismatch with the expectation.
func (t Trace) Verify() []string {
	var bad []string
	res := Run(Unproposed{}, t.Events())
	for i, r := range res {
		want := t.Steps[i]
		if r.Rule != want.Rule {
			bad = append(bad, fmt.Sprintf("%s step %d (%s): rule %s, want %s", t.Name, i+1, r.Event.Name(), r.Rule, want.Rule))
		}
		got := EffectNames(r.Effects)
		if len(got) != len(want.Effects) {
			bad = append(bad, fmt.Sprintf("%s step %d: effects %v, want %v", t.Name, i+1, got, want.Effects))
			continue
		}
		for j := range got {
			if got[j] != want.Effects[j] {
				bad = append(bad, fmt.Sprintf("%s step %d: effects %v, want %v", t.Name, i+1, got, want.Effects))
				break
			}
		}
	}
	if len(res) > 0 && Key(res[len(res)-1].After) != Key(t.Final) {
		bad = append(bad, fmt.Sprintf("%s: final %s, want %s", t.Name, Key(res[len(res)-1].After), Key(t.Final)))
	}
	return bad
}

// Trace building blocks. The proposer is alice; bob is another user.

func prop(k ToolKind, r Role) Prop {
	return Prop{ID: "op1", Proposer: "alice", ProposerRole: r, Kind: k, Gen: 1, ExpiresAt: 10}
}

func propose(r Role, k ToolKind) Propose {
	return Propose{ID: "op1", Actor: "alice", Role: r, Tool: k, Claimed: ActionWrite, Gen: 1, RuntimeGen: 1, ExpiresAt: 10, Now: 0}
}

func confirm() Confirm {
	return Confirm{Actor: "alice", Role: RoleOwner, Human: true, DigestOK: true, Now: 1}
}

func granted() AdmitGranted { return AdmitGranted{Now: 1, RuntimeGen: 1} }

func st(e Event, rule string, fx ...string) Step { return Step{Event: e, Rule: rule, Effects: fx} }

// ownerResolve is the resource owner at a human surface with a reason.
func ownerResolve() OwnerResolve { return OwnerResolve{Role: RoleOwner, Human: true, Reason: true} }

// toSent is the common prefix that ends in a request on the wire.
func toSent() []Step {
	return []Step{
		st(propose(RoleOwner, KindWrite), "R8", "persist:pending", "reply:created"),
		st(confirm(), "R15", "admit:write"),
		st(granted(), "R32", "persist:executing", "dispatch"),
		st(SendStarted{}, "R33"),
	}
}

func with(prefix []Step, more ...Step) []Step { return append(append([]Step{}, prefix...), more...) }

// Traces returns the scenario traces. They cover the three review findings:
// (a) a refusal before the send is retryable, (b) the action comes from the
// tool kind, (c) an unknown outcome always has a way out.
func Traces() []Trace {
	w := prop(KindWrite, RoleOwner)
	sch := prop(KindSchema, RoleOwner)
	return []Trace{
		{
			Name:  "happy-path",
			About: "propose, confirm, admit, send, committed: Applied",
			Steps: with(toSent(), st(ApplyResult{ApplyCommitted}, "R37", "persist:applied", "reply:applied")),
			Final: Applied{P: w},
		},
		{
			Name:  "retry-after-not-sent",
			About: "finding (a): the client refuses before the send (no free slot); the proposal returns to Pending and is retried, never OutcomeUnknown",
			Steps: []Step{
				st(propose(RoleOwner, KindWrite), "R8", "persist:pending", "reply:created"),
				st(confirm(), "R15", "admit:write"),
				st(granted(), "R32", "persist:executing", "dispatch"),
				st(NotSent{NotSentSlots}, "R34", "persist:pending", "reply:unavailable"),
				st(confirm(), "R15", "admit:write"),
				st(granted(), "R32", "persist:executing", "dispatch"),
				st(SendStarted{}, "R33"),
				st(ApplyResult{ApplyCommitted}, "R37", "persist:applied", "reply:applied"),
			},
			Final: Applied{P: w},
		},
		{
			Name:  "retry-after-admission-refused",
			About: "finding (a): the admission says held; nothing was persisted or sent, so the proposal stays retryable",
			Steps: []Step{
				st(propose(RoleOwner, KindWrite), "R8", "persist:pending", "reply:created"),
				st(confirm(), "R15", "admit:write"),
				st(AdmitRefused{CauseHeld}, "R29", "reply:unavailable"),
				st(HoldReleased{}, "I4"),
				st(confirm(), "R15", "admit:write"),
				st(AdmitRefused{CauseBlocked}, "R29", "reply:blocked"),
				st(confirm(), "R15", "admit:write"),
				st(granted(), "R32", "persist:executing", "dispatch"),
				st(SendStarted{}, "R33"),
				st(ApplyResult{ApplyCommitted}, "R37", "persist:applied", "reply:applied"),
			},
			Final: Applied{P: w},
		},
		{
			Name:  "withdrawn-then-generation-moved",
			About: "finding (a): the client is withdrawn before the send (add-on replaced); retryable, then the generation check makes it Stale instead of unknown",
			Steps: []Step{
				st(propose(RoleOwner, KindWrite), "R8", "persist:pending", "reply:created"),
				st(confirm(), "R15", "admit:write"),
				st(granted(), "R32", "persist:executing", "dispatch"),
				st(NotSent{NotSentWithdrawn}, "R34", "persist:pending", "reply:unavailable"),
				st(GenerationChanged{New: 2}, "R57", "persist:rejected"),
				st(confirm(), "R19", "reply:rejected"),
			},
			Final: Stale{P: w},
		},
		{
			Name:  "timeout-unknown-then-receipt-committed",
			About: "finding (c): the apply times out after the send; lookups run; the receipt says committed",
			Steps: with(toSent(),
				st(ApplyResult{ApplyTimeout}, "R39", "persist:outcome-unknown", "reply:outcome-unknown"),
				st(LookupDue{}, "R45", "lookup-receipt"),
				st(ReceiptResult{ReceiptUnsettled}, "R43", "persist:outcome-unknown"),
				st(LookupDue{}, "R45", "lookup-receipt"),
				st(ReceiptResult{ReceiptCommitted}, "R40", "persist:applied"),
			),
			Final: Applied{P: w},
		},
		{
			Name:  "unknown-bounded-then-manual-resolution",
			About: "finding (c): lookups stay unsettled up to the bound; UnknownManual; owner resolution abandons it; retention drops it. No state is stuck",
			Steps: with(toSent(),
				st(ApplyResult{ApplyTransportError}, "R39", "persist:outcome-unknown", "reply:outcome-unknown"),
				st(ReceiptResult{ReceiptUnsettled}, "R43", "persist:outcome-unknown"),
				st(ReceiptResult{ReceiptUnsettled}, "R44", "persist:outcome-unknown"),
				st(LookupDue{}, "I7"),
				st(confirm(), "R17", "lookup-receipt", "reply:outcome-unknown"),
				st(OwnerResolve{Role: RoleEditor, Human: true, Reason: true}, "R46", "reply:forbidden"),
				st(OwnerResolve{Role: RoleOwner, Human: true, Reason: true, Held: true}, "R47", "reply:unavailable"),
				st(ownerResolve(), "R48", "persist:abandoned-unknown", "lookup-receipt", "reply:resolved"),
				st(LookupDue{}, "R45", "lookup-receipt"),
				st(RetentionElapsed{}, "R61"),
			),
			Final: Dropped{P: w},
		},
		{
			Name:  "late-receipt-after-abandon",
			About: "an abandoned outcome that later turns out committed is recorded as Applied, not hidden",
			Steps: with(toSent(),
				st(ApplyResult{ApplyTimeout}, "R39", "persist:outcome-unknown", "reply:outcome-unknown"),
				st(ownerResolve(), "R48", "persist:abandoned-unknown", "lookup-receipt", "reply:resolved"),
				st(ReceiptResult{ReceiptCommitted}, "R40", "persist:applied"),
			),
			Final: Applied{P: w},
		},
		{
			Name:  "two-actors-racing-confirm",
			About: "another user's confirm is forbidden; a second confirm by the proposer while admission runs is busy; a repeat during execution looks the receipt up and never applies twice",
			Steps: []Step{
				st(propose(RoleOwner, KindWrite), "R8", "persist:pending", "reply:created"),
				st(Confirm{Actor: "bob", Role: RoleOwner, Human: true, DigestOK: true, Now: 1}, "R10", "reply:forbidden"),
				st(confirm(), "R15", "admit:write"),
				st(confirm(), "R16", "reply:busy"),
				st(Cancel{Actor: "bob"}, "R23", "reply:forbidden"),
				st(granted(), "R32", "persist:executing", "dispatch"),
				st(confirm(), "R17", "lookup-receipt", "reply:outcome-unknown"),
				st(Cancel{Actor: "alice"}, "R26", "reply:closed"),
				st(SendStarted{}, "R33"),
				st(ApplyResult{ApplyCommitted}, "R37", "persist:applied", "reply:applied"),
			},
			Final: Applied{P: w},
		},
		{
			Name:  "cancel-races-confirm",
			About: "cancel wins while the confirmation waits for admission; the late admission answer finds the proposal closed and nothing runs",
			Steps: []Step{
				st(propose(RoleOwner, KindWrite), "R8", "persist:pending", "reply:created"),
				st(confirm(), "R15", "admit:write"),
				st(Cancel{Actor: "alice"}, "R25", "persist:cancelled", "reply:cancelled"),
				st(granted(), "I3"),
				st(confirm(), "R20", "reply:closed"),
			},
			Final: Cancelled{P: w, Why: CancelByProposer},
		},
		{
			Name:  "hold-during-prepared-dispatch",
			About: "a hold arrives after executing is persisted but before the send: the dispatch is cancelled, nothing was sent, Cancelled",
			Steps: []Step{
				st(propose(RoleOwner, KindWrite), "R8", "persist:pending", "reply:created"),
				st(confirm(), "R15", "admit:write"),
				st(granted(), "R32", "persist:executing", "dispatch"),
				st(HoldAcquired{}, "R52", "cancel-dispatch:hold"),
				st(NotSent{NotSentHold}, "R35", "persist:cancelled", "reply:cancelled"),
				st(HoldReleased{}, "I2"),
			},
			Final: Cancelled{P: w, Why: CancelByHold},
		},
		{
			Name:  "hold-too-late-to-cancel",
			About: "the hold races the send and loses: the request is on the wire, so the proposal waits for the result instead of claiming it was cancelled",
			Steps: []Step{
				st(propose(RoleOwner, KindWrite), "R8", "persist:pending", "reply:created"),
				st(confirm(), "R15", "admit:write"),
				st(granted(), "R32", "persist:executing", "dispatch"),
				st(HoldAcquired{}, "R52", "cancel-dispatch:hold"),
				st(SendStarted{}, "R33"),
				st(HoldAcquired{}, "R53", "cancel-send"),
				st(HoldReleased{}, "I4"),
				st(ApplyResult{ApplyCommitted}, "R37", "persist:applied", "reply:applied"),
			},
			Final: Applied{P: w},
		},
		{
			Name:  "hold-during-sent-request",
			About: "a hold aborts the request in flight; the aborted call is OutcomeUnknown, and a not-committed answer settles it",
			Steps: with(toSent(),
				st(HoldAcquired{}, "R53", "cancel-send"),
				st(ApplyResult{ApplyTransportError}, "R39", "persist:outcome-unknown", "reply:outcome-unknown"),
				st(LookupDue{}, "R45", "lookup-receipt"),
				st(ReceiptResult{ReceiptNotCommitted}, "R42", "persist:rejected"),
			),
			Final: NotApplied{P: w},
		},
		{
			Name:  "hold-on-pending",
			About: "a hold invalidates a pending proposal; a later confirm gets closed",
			Steps: []Step{
				st(propose(RoleOwner, KindWrite), "R8", "persist:pending", "reply:created"),
				st(HoldAcquired{}, "R51", "persist:cancelled"),
				st(confirm(), "R20", "reply:closed"),
			},
			Final: Cancelled{P: w, Why: CancelByHold},
		},
		{
			Name:  "generation-change-while-pending",
			About: "the add-on moves to a new generation: the pending proposal is Stale and never executes",
			Steps: []Step{
				st(propose(RoleOwner, KindWrite), "R8", "persist:pending", "reply:created"),
				st(GenerationChanged{New: 2}, "R57", "persist:rejected"),
				st(confirm(), "R19", "reply:rejected"),
				st(Tick{Now: 20}, "I2"),
			},
			Final: Stale{P: w},
		},
		{
			Name:  "generation-seen-at-admission",
			About: "the generation moved before the change event arrived: the admission check finds a runtime of another generation and the proposal is Stale",
			Steps: []Step{
				st(propose(RoleOwner, KindWrite), "R8", "persist:pending", "reply:created"),
				st(confirm(), "R15", "admit:write"),
				st(AdmitGranted{Now: 1, RuntimeGen: 2}, "R31", "persist:rejected", "reply:stale"),
			},
			Final: Stale{P: w},
		},
		{
			Name:  "generation-change-while-prepared",
			About: "the generation moves after executing is persisted: the dispatch is cancelled and the not-sent answer makes it Stale",
			Steps: []Step{
				st(propose(RoleOwner, KindWrite), "R8", "persist:pending", "reply:created"),
				st(confirm(), "R15", "admit:write"),
				st(granted(), "R32", "persist:executing", "dispatch"),
				st(GenerationChanged{New: 2}, "R58", "cancel-dispatch:superseded"),
				st(NotSent{NotSentSuperseded}, "R36", "persist:rejected", "reply:stale"),
			},
			Final: Stale{P: w},
		},
		{
			Name:  "duplicate-confirm-after-applied",
			About: "a repeated confirm, propose or cancel after Applied returns the stored answer and runs nothing",
			Steps: with(toSent(),
				st(ApplyResult{ApplyCommitted}, "R37", "persist:applied", "reply:applied"),
				st(confirm(), "R18", "reply:applied"),
				st(propose(RoleOwner, KindWrite), "R9", "reply:existing"),
				st(Cancel{Actor: "alice"}, "R26", "reply:closed"),
			),
			Final: Applied{P: w},
		},
		{
			Name:  "duplicate-confirm-while-unknown",
			About: "a repeated confirm of an unknown outcome looks the receipt up and never applies again",
			Steps: with(toSent(),
				st(ApplyResult{ApplyTimeout}, "R39", "persist:outcome-unknown", "reply:outcome-unknown"),
				st(confirm(), "R17", "lookup-receipt", "reply:outcome-unknown"),
				st(confirm(), "R17", "lookup-receipt", "reply:outcome-unknown"),
			),
			Final: OutcomeUnknown{P: w},
		},
		{
			Name:  "editor-schema-propose-refused",
			About: "finding (b): an editor proposes a schema-kind tool while claiming the write action; the action comes from the tool kind, so it is Refused",
			Steps: []Step{
				st(propose(RoleEditor, KindSchema), "R4", "reply:forbidden"),
				st(Confirm{Actor: "alice", Role: RoleEditor, Human: true, DigestOK: true, Now: 1}, "R22", "reply:forbidden"),
			},
			Final: Refused{Why: RefuseInsufficientRole},
		},
		{
			Name:  "editor-confirm-of-schema-after-demotion",
			About: "finding (b): an owner proposes a schema change, is lowered to editor, and the confirm is forbidden; the proposal stays Pending",
			Steps: []Step{
				st(propose(RoleOwner, KindSchema), "R8", "persist:pending", "reply:created"),
				st(Confirm{Actor: "alice", Role: RoleEditor, Human: true, DigestOK: true, Now: 1}, "R14", "reply:forbidden"),
				st(confirm(), "R15", "admit:schema"),
			},
			Final: Admitting{P: sch, By: "alice"},
		},
		{
			Name:  "out-of-order-receipt",
			About: "a receipt reply for a proposal not yet sent is ignored; a committed receipt that beats the apply reply settles it, and the late apply error changes nothing",
			Steps: []Step{
				st(propose(RoleOwner, KindWrite), "R8", "persist:pending", "reply:created"),
				st(ReceiptResult{ReceiptCommitted}, "I3"),
				st(confirm(), "R15", "admit:write"),
				st(granted(), "R32", "persist:executing", "dispatch"),
				st(SendStarted{}, "R33"),
				st(ReceiptResult{ReceiptUnsettled}, "I5"),
				st(ReceiptResult{ReceiptCommitted}, "R40", "persist:applied"),
				st(ApplyResult{ApplyTransportError}, "I3"),
			},
			Final: Applied{P: w},
		},
		{
			Name:  "late-apply-reply-after-timeout",
			About: "the apply reply arrives after the timeout was recorded: the committed answer still settles it",
			Steps: with(toSent(),
				st(ApplyResult{ApplyTimeout}, "R39", "persist:outcome-unknown", "reply:outcome-unknown"),
				st(ApplyResult{ApplyCommitted}, "R37", "persist:applied", "reply:applied"),
				st(ApplyResult{ApplyTimeout}, "I3"),
			),
			Final: Applied{P: w},
		},
		{
			Name:  "restart-recovery",
			About: "a restart in admission returns to Pending; a restart with an executing proposal is OutcomeUnknown and a lookup settles it",
			Steps: []Step{
				st(propose(RoleOwner, KindWrite), "R8", "persist:pending", "reply:created"),
				st(confirm(), "R15", "admit:write"),
				st(Restart{}, "R59"),
				st(confirm(), "R15", "admit:write"),
				st(granted(), "R32", "persist:executing", "dispatch"),
				st(Restart{}, "R60", "persist:outcome-unknown"),
				st(LookupDue{}, "R45", "lookup-receipt"),
				st(ReceiptResult{ReceiptCommitted}, "R40", "persist:applied"),
			},
			Final: Applied{P: w},
		},
		{
			Name:  "addon-conflict-is-rejected",
			About: "a 409 answer is the add-on's definite answer, so Rejected, not OutcomeUnknown",
			Steps: with(toSent(), st(ApplyResult{ApplyConflict}, "R38", "persist:rejected", "reply:rejected")),
			Final: Rejected{P: w, Why: RejectConflict},
		},
		{
			Name:  "expiry",
			About: "a tick before expiry changes nothing; a tick at expiry expires it; a confirm then gets expired",
			Steps: []Step{
				st(propose(RoleOwner, KindWrite), "R8", "persist:pending", "reply:created"),
				st(Tick{Now: 5}, "I8"),
				st(Tick{Now: 10}, "R28", "persist:expired"),
				st(confirm(), "R21", "reply:expired"),
			},
			Final: Expired{P: w},
		},
		{
			Name:  "propose-while-held-then-retry",
			About: "a propose during a hold stores nothing and is retried after it",
			Steps: []Step{
				st(Propose{ID: "op1", Actor: "alice", Role: RoleOwner, Tool: KindWrite, Claimed: ActionWrite, Gen: 1, RuntimeGen: 1, ExpiresAt: 10, Held: true}, "R1", "reply:unavailable"),
				st(Propose{ID: "op1", Actor: "alice", Role: RoleOwner, Tool: KindWrite, Claimed: ActionWrite, Gen: 1, RuntimeGen: 1, ExpiresAt: 10, Blocked: true}, "R6", "reply:blocked"),
				st(Propose{ID: "op1", Actor: "alice", Role: RoleOwner, Tool: KindWrite, Claimed: ActionWrite, Gen: 1, RuntimeGen: 1, ExpiresAt: 0}, "R7", "reply:unavailable"),
				st(propose(RoleOwner, KindWrite), "R8", "persist:pending", "reply:created"),
			},
			Final: Pending{P: w},
		},
		{
			Name:  "propose-undeclared-tool",
			About: "a tool the add-on did not declare is refused and nothing is stored",
			Steps: []Step{st(propose(RoleOwner, KindUndeclared), "R2", "reply:refused")},
			Final: Refused{Why: RefuseUndeclaredTool},
		},
		{
			Name:  "propose-read-tool",
			About: "a read tool is never a proposal",
			Steps: []Step{st(propose(RoleOwner, KindRead), "R3", "reply:refused")},
			Final: Refused{Why: RefuseNotAMutation},
		},
		{
			Name:  "propose-stale-generation",
			About: "a preview made against another generation than the runtime's is refused",
			Steps: []Step{st(Propose{ID: "op1", Actor: "alice", Role: RoleOwner, Tool: KindWrite, Claimed: ActionWrite, Gen: 1, RuntimeGen: 2, ExpiresAt: 10}, "R5", "reply:stale")},
			Final: Refused{Why: RefuseStaleGeneration},
		},
		{
			Name:  "confirm-guards",
			About: "a confirm from a non-human credential or with a changed digest is refused and changes nothing; a confirm after expiry expires the proposal",
			Steps: []Step{
				st(propose(RoleOwner, KindWrite), "R8", "persist:pending", "reply:created"),
				st(Confirm{Actor: "alice", Role: RoleOwner, Human: false, DigestOK: true, Now: 1}, "R11", "reply:forbidden"),
				st(Confirm{Actor: "alice", Role: RoleOwner, Human: true, DigestOK: false, Now: 1}, "R12", "reply:changed"),
				st(Confirm{Actor: "alice", Role: RoleOwner, Human: true, DigestOK: true, Now: 10}, "R13", "persist:expired", "reply:expired"),
			},
			Final: Expired{P: w},
		},
		{
			Name:  "expiry-seen-at-admission",
			About: "the clock passes expiry while the confirmation waits for admission: Expired, nothing runs",
			Steps: []Step{
				st(propose(RoleOwner, KindWrite), "R8", "persist:pending", "reply:created"),
				st(confirm(), "R15", "admit:write"),
				st(AdmitGranted{Now: 10, RuntimeGen: 1}, "R30", "persist:expired", "reply:expired"),
			},
			Final: Expired{P: w},
		},
		{
			Name:  "cancel-pending",
			About: "the proposer cancels a pending proposal; an owner resolution and a confirm of it are closed",
			Steps: []Step{
				st(propose(RoleOwner, KindWrite), "R8", "persist:pending", "reply:created"),
				st(ownerResolve(), "R49", "reply:closed"),
				st(Cancel{Actor: "alice"}, "R24", "persist:cancelled", "reply:cancelled"),
				st(Cancel{Actor: "alice"}, "R27", "reply:cancelled"),
				st(confirm(), "R20", "reply:closed"),
			},
			Final: Cancelled{P: w, Why: CancelByProposer},
		},
		{
			Name:  "authority-lowered-while-pending",
			About: "the proposer loses authority: the pending proposal is cancelled",
			Steps: []Step{
				st(propose(RoleOwner, KindWrite), "R8", "persist:pending", "reply:created"),
				st(AuthorityLowered{}, "R54", "persist:cancelled"),
			},
			Final: Cancelled{P: w, Why: CancelByAuthority},
		},
		{
			Name:  "authority-lowered-before-send",
			About: "authority is lowered after executing is persisted but before the send: the dispatch is cancelled, nothing was sent",
			Steps: []Step{
				st(propose(RoleOwner, KindWrite), "R8", "persist:pending", "reply:created"),
				st(confirm(), "R15", "admit:write"),
				st(granted(), "R32", "persist:executing", "dispatch"),
				st(AuthorityLowered{}, "R55", "cancel-dispatch:authority-lowered"),
				st(NotSent{NotSentAuthority}, "R35", "persist:cancelled", "reply:cancelled"),
			},
			Final: Cancelled{P: w, Why: CancelByAuthority},
		},
		{
			Name:  "authority-lowered-after-send",
			About: "authority is lowered while the request is on the wire: the call is aborted, the outcome is unknown, and a rejected receipt settles it",
			Steps: with(toSent(),
				st(AuthorityLowered{}, "R56", "cancel-send"),
				st(ApplyResult{ApplyTransportError}, "R39", "persist:outcome-unknown", "reply:outcome-unknown"),
				st(ReceiptResult{ReceiptRejected}, "R41", "persist:rejected"),
				st(ownerResolve(), "R50", "reply:resolved"),
			),
			Final: Rejected{P: w, Why: RejectReceipt},
		},
	}
}

// SPDX-License-Identifier: MIT
package toolbroker

// Reduce trusts evidence only as model input. A Fresh dispatch is the successful
// DB Prepared->Dispatched observation, never merely a lease or Prepare success.
// Its single local claim is not an external-provider atomicity guarantee.
func Reduce(s State, e Event) (Transition, error) {
	if e == nil || s.phase == nil || !s.config.Binding.valid() || s.config.Policy > AllowAll || s.config.ApprovalTTL <= 0 {
		return Transition{}, ErrInvalid
	}
	if err := CheckInvariants(s); err != nil {
		return Transition{}, err
	}
	EventName(e) // panic on a package extension missing from the closed vocabulary
	before := s
	in := e.eventInput()
	b := s.config.Binding
	emit := func(p Phase, id, guard string, fx ...Effect) (Transition, error) {
		n := s
		n.phase = p
		if in.Now > n.lastTime {
			n.lastTime = in.Now
		}
		t := Transition{State: n, Effects: fx, RuleID: id, Guard: guard}
		if err := CheckTransition(before, e, t); err != nil {
			return Transition{}, err
		}
		return t, nil
	}
	unchanged := func(id, guard string) (Transition, error) { return Transition{State: s, RuleID: id, Guard: guard}, nil }
	deny := func(id, guard string) (Transition, error) {
		return emit(Denied{}, id, guard, ReturnDenied{Binding: b}, RecordDecision{Binding: b})
	}
	unresolved := func(id, guard string) (Transition, error) {
		return emit(Unknown{}, id, guard, HoldForReconciliation{Binding: b})
	}
	if in.Binding != b {
		return unchanged("TB-R19", "ignored: stale immutable request binding")
	}
	if in.Now <= 0 || in.Now < s.lastTime {
		return unchanged("TB-R19", "ignored: stale or invalid host time")
	}
	// Recovery can observe a reconciled completed result, but cannot restore a claim.
	if r, ok := e.(RecoveryObserved); ok {
		switch s.phase.(type) {
		case Unknown, Completed:
			if r.Status == RecordCompleted {
				s.result = r.Output
				return emit(Completed{}, "TB-R20", "host recovery observes completed journal only", ReturnRecordedResult{Binding: b, Output: r.Output})
			}
			return unchanged("TB-R20", "recovery holds unresolved record without dispatch")
		case New, Checking, WaitingApproval, Authorized, Claiming, Claimed, Executing, Denied, Interrupted:
			return unchanged("TB-R20", "recovery cannot establish execution authority")
		}
	}
	switch s.phase.(type) {
	case Completed, Denied, Interrupted, Unknown:
		return unchanged("TB-R22", "ignored: terminal state never revives authority")
	case New, Checking, WaitingApproval, Authorized, Claiming, Claimed, Executing:
	}
	switch e.(type) {
	case Cancelled, AccessRevoked, LeaseLost, AttemptReplaced:
		if s.dispatches > 0 {
			return unresolved("TB-R10", "invalidated dispatched claim remains unresolved")
		}
		return emit(Interrupted{}, "TB-R10", "cancel revoke lease or attempt invalidates authority", NotifyInterrupted{Binding: b})
	case Expired:
		switch p := s.phase.(type) {
		case WaitingApproval:
			if in.Now >= p.expires {
				return deny("TB-R10", "waiting approval expired")
			}
		case Authorized:
			if p.until > 0 && in.Now >= p.until {
				return deny("TB-R10", "authorization expired")
			}
		case Claiming:
			if p.until > 0 && in.Now >= p.until {
				return deny("TB-R10", "claim preparation approval expired")
			}
		case Claimed:
			if p.until > 0 && in.Now >= p.until {
				return unresolved("TB-R10", "dispatched approval expired")
			}
		case Executing:
			return unresolved("TB-R18", "execution timeout cannot retry")
		case New, Checking:
		}
	case ExecutionUncertain:
		if s.dispatches > 0 {
			return unresolved("TB-R18", "dispatched or started execution outcome uncertain")
		}
	case JournalFailed:
		if s.dispatches > 0 {
			return unresolved("TB-R18", "result not durably accepted")
		}
	}
	switch p := s.phase.(type) {
	case New:
		if _, ok := e.(Requested); ok {
			return emit(Checking{purpose: initialAdmission}, "TB-R01", "valid host request requires admission", CheckAdmission{Binding: b})
		}
	case Checking:
		switch v := e.(type) {
		case AdmissionRejected:
			if s.dispatches > 0 {
				return unresolved("TB-R02", "fresh execution admission rejected after dispatch")
			}
			return deny("TB-R02", "current host admission rejected")
		case AdmissionPassed:
			a := v.Evidence
			if !a.Registered {
				if s.dispatches > 0 {
					return unresolved("TB-R01", "tool unregistered after dispatch")
				}
				return deny("TB-R01", "tool unregistered")
			}
			if !a.Current {
				if s.dispatches > 0 {
					return unresolved("TB-R02", "current authority lost after dispatch")
				}
				return deny("TB-R02", "principal attempt epoch or lease not current")
			}
			if !a.Selected || !a.Resource || !a.Helper {
				if s.dispatches > 0 {
					return unresolved("TB-R03", "selection resource or helper access lost after dispatch")
				}
				return deny("TB-R03", "selection resource or parent helper intersection denied")
			}
			switch p.purpose {
			case invocationAdmission:
				if p.until > 0 && in.Now >= p.until {
					return unresolved("TB-R10", "approval expired during fresh execution checks")
				}
				if s.dispatches != 1 || s.runs != 0 {
					return Transition{}, ErrInvariant
				}
				s.runs++
				return emit(Executing{stage: runningBody}, "TB-R14", "consumed single claim and fresh execution admission", RunExecutor{Binding: b})

			case initialAdmission:
				switch s.config.Policy {
				case DenyContinue:
					return deny("TB-R05", "host policy denies and continues")
				case Stop:
					return emit(Interrupted{}, "TB-R06", "host policy stops attempt", NotifyInterrupted{Binding: b}, RecordDecision{Binding: b})
				case RequireApproval, AllowAll:
					if s.config.Policy == RequireApproval || s.config.ExternalTaintedWrite {
						if in.Now > int64(^uint64(0)>>1)-s.config.ApprovalTTL {
							return Transition{}, ErrInvalid
						}
						id := "TB-R04"
						if s.config.ExternalTaintedWrite {
							id = "TB-R11"
						}
						return emit(WaitingApproval{expires: in.Now + s.config.ApprovalTTL}, id, "exact bound expiring approval required", RequestApproval{Binding: b})
					}
					return emit(Authorized{}, "TB-R07", "host AllowAll audited after current checks", RecordDecision{Binding: b})
				}
			}
		}
	case WaitingApproval:
		switch v := e.(type) {
		case ApprovalDenied:
			return deny("TB-R09", "human approval denied terminally")
		case ApprovalGranted:
			a := v.Approval
			if in.Now >= p.expires || a.ExpiresAt <= in.Now {
				return deny("TB-R10", "approval or wait expired at current host time")
			}
			if a.Binding != b || a.ExpiresAt > p.expires || a.ActorID == "" || (a.Actor != HumanActor && a.Actor != HostActor) || (s.config.ExternalTaintedWrite && a.Actor != HumanActor) {
				return unchanged("TB-R08", "rejected: approval binding actor or expiry mismatch")
			}
			return emit(Authorized{until: a.ExpiresAt}, "TB-R08", "exact fresh bound approval accepted", RecordDecision{Binding: b})
		}
	case Authorized:
		switch v := e.(type) {
		case ClaimRequested:
			if p.until > 0 && in.Now >= p.until {
				return deny("TB-R10", "approval expired before preparation")
			}
			return emit(Claiming{stage: awaitingPrepare, until: p.until}, "TB-R12", "prepare intent is not execution authority", PrepareJournal{Binding: b})
		case ApprovalGranted:
			if v.Approval.Binding == b && v.Approval.ExpiresAt == p.until {
				return unchanged("TB-R21", "duplicate equivalent approval has no effects")
			}
			return unchanged("TB-R21", "rejected: conflicting decision cannot refresh approval")
		}
	case Claiming:
		switch v := e.(type) {
		case PreparedObserved:
			switch v.Status {
			case RecordCompleted:
				s.result = v.Output
				return emit(Completed{}, "TB-R15", "completed record reused without execution", ReturnRecordedResult{Binding: b, Output: v.Output})
			case RecordDispatched, RecordUnknown, RecordConflict:
				return unresolved("TB-R16", "dispatched unknown or conflicting intent cannot retry")
			case RecordPrepared:
				if p.until > 0 && in.Now >= p.until {
					return deny("TB-R10", "approval expired before dispatch")
				}
				return emit(Claiming{stage: awaitingDispatch, until: p.until}, "TB-R12", "prepared journal requires separately fenced dispatch", DispatchJournal{Binding: b})
			}
		case DispatchGranted:
			if p.stage != awaitingDispatch {
				return unchanged("TB-R00", "ignored: dispatch before prepared observation")
			}
			if !v.Fresh {
				return unresolved("TB-R16", "non-fresh dispatch cannot mint claim")
			}
			s.dispatches++
			if p.until > 0 && in.Now >= p.until {
				return unresolved("TB-R10", "dispatch succeeded but approval already expired")
			}
			return emit(Claimed{until: p.until}, "TB-R13", "fresh Prepared to Dispatched creates single local claim")
		case DispatchRejected:
			return unresolved("TB-R16", "dispatch rejected or raced: never execute")
		}
	case Claimed:
		if _, ok := e.(InvokeRequested); ok {
			if p.until > 0 && in.Now >= p.until {
				return unresolved("TB-R10", "approval expired before claim consumption")
			}
			return emit(Checking{purpose: invocationAdmission, until: p.until}, "TB-R14", "consume claim before fresh execution checks", RecheckExecutionAdmission{Binding: b})
		}
	case Executing:
		switch v := e.(type) {
		case ExecutionStarted:
			if p.stage == runningBody {
				return unchanged("TB-R14", "executor start acknowledged without a second run")
			}
		case ExecutionCompleted:
			if p.stage == runningBody {
				s.result = v.Output
				return emit(Executing{stage: awaitingJournal}, "TB-R17", "body result awaits accepted journal completion", CompleteJournal{Binding: b, Output: v.Output})
			}
			if s.result.equal(v.Output) {
				return unchanged("TB-R21", "duplicate equivalent body result")
			}
			return unchanged("TB-R21", "rejected: conflicting body result")
		case JournalCompleted:
			if p.stage == awaitingJournal {
				return emit(Completed{}, "TB-R17", "completion only after journal acceptance", ReturnRecordedResult{Binding: b, Output: s.result})
			}
		}
	case Completed, Denied, Interrupted, Unknown: // handled above, retained for full phase enumeration
	}
	if _, ok := e.(InvokeRequested); ok {
		if p, ok := s.phase.(Checking); ok && p.purpose == invocationAdmission {
			return unchanged("TB-R21", "duplicate invoke while consumed claim checks pending")
		}
	}
	return unchanged("TB-R00", "ignored: event not enabled in this explicit phase")
}

func CheckInvariants(s State) error {
	if s.phase == nil || !s.config.Binding.valid() || s.dispatches > 1 || s.runs > 1 || s.runs > s.dispatches {
		return ErrInvariant
	}
	switch p := s.phase.(type) {
	case New, WaitingApproval, Authorized, Denied, Interrupted:
		if s.dispatches != 0 || s.runs != 0 {
			return ErrInvariant
		}
	case Checking:
		switch p.purpose {
		case initialAdmission:
			if s.dispatches != 0 || s.runs != 0 {
				return ErrInvariant
			}
		case invocationAdmission:
			if s.dispatches != 1 || s.runs != 0 {
				return ErrInvariant
			}
		}
	case Claiming:
		if s.dispatches != 0 || s.runs != 0 || p.stage > awaitingDispatch {
			return ErrInvariant
		}
	case Claimed:
		if s.dispatches != 1 || s.runs != 0 {
			return ErrInvariant
		}
	case Executing:
		if s.dispatches != 1 || s.runs != 1 || p.stage > awaitingJournal {
			return ErrInvariant
		}
	case Completed, Unknown:
	}
	return nil
}
func CheckTransition(before State, e Event, t Transition) error {
	if err := CheckInvariants(t.State); err != nil {
		return err
	}
	if before.config != t.State.config || t.State.dispatches < before.dispatches || t.State.runs < before.runs || t.Guard == "" || len(t.RuleID) != 6 {
		return ErrInvariant
	}
	runs := 0
	for _, fx := range t.Effects {
		switch fx.(type) {
		case RunExecutor:
			runs++
			p, ok := before.phase.(Checking)
			if !ok || p.purpose != invocationAdmission || t.RuleID != "TB-R14" {
				return ErrInvariant
			}
			if _, ok := e.(AdmissionPassed); !ok {
				return ErrInvariant
			}
		case DispatchJournal:
			if _, ok := e.(PreparedObserved); !ok {
				return ErrInvariant
			}
		case PrepareJournal:
			if _, ok := before.phase.(Authorized); !ok {
				return ErrInvariant
			}
		case CheckAdmission, RequestApproval, RecordDecision, RecheckExecutionAdmission, CompleteJournal, CancelExecutor, ReturnRecordedResult, ReturnDenied, HoldForReconciliation, NotifyInterrupted:
		}
	}
	if int(t.State.runs-before.runs) != runs {
		return ErrInvariant
	}
	if t.State.dispatches > before.dispatches {
		v, ok := e.(DispatchGranted)
		if !ok || !v.Fresh {
			return ErrInvariant
		}
		p, ok := before.phase.(Claiming)
		if !ok || p.stage != awaitingDispatch {
			return ErrInvariant
		}
	}
	switch before.phase.(type) {
	case Denied, Interrupted:
		if stateKey(before) != stateKey(t.State) || len(t.Effects) > 0 {
			return ErrInvariant
		}
	case Completed, Unknown:
		if _, ok := e.(RecoveryObserved); !ok {
			if stateKey(before) != stateKey(t.State) || len(t.Effects) > 0 {
				return ErrInvariant
			}
		}
	case New, Checking, WaitingApproval, Authorized, Claiming, Claimed, Executing:
	}
	return nil
}

// SPDX-License-Identifier: MIT
package lifecycle

type emitTransition func(string, string, ...Effect) (Transition, error)
type ignoreTransition func(string, string) (Transition, error)

func reduceCallback(s *State, e Event, emit emitTransition, ignore ignoreTransition, interrupt func(string)) (Transition, error) {
	p := s.Phase
	current := turn(p)
	journal := func(request string) *ToolAttempt {
		for n := range s.Journal {
			j := &s.Journal[n]
			if j.Turn == current && j.Request == request {
				return j
			}
		}
		return nil
	}
	unknown := func() bool {
		for _, j := range s.Journal {
			if j.Turn == current && j.Status == Unknown {
				return true
			}
		}
		return false
	}
	restored := func() []Effect {
		s.Phase = Running{current, NoTool}
		results := []ToolAttempt{}
		for _, j := range s.Journal {
			if j.Turn == current && j.Status == Completed {
				results = append(results, j)
			}
		}
		return []Effect{ReuseRecordedResults{results}, ContinueRestoredTurn{current, s.Attempt}}
	}
	dispatch := func(request string) Effect {
		s.Journal = append(s.Journal, ToolAttempt{Turn: current, Request: request, Attempt: s.Attempt, Status: InFlight})
		s.Phase = Running{current, InFlight}
		return AuthorizedTool{current, request, s.Attempt, s.Epoch}
	}
	switch v := e.(type) {
	case RestoreFailed:
		if _, ok := p.(Restoring); ok {
			interrupt("RestoreFailed")
			return emit("R42", "restore failed; active input and FIFO retained", NotifyRestoreFailure{})
		}
	case RestoreOK:
		if _, ok := p.(Restoring); ok {
			if unknown() {
				s.Phase = WaitingReconciliation{current}
				return emit("R43", "unknown dispatch blocks continuation", ShowReconciliation{})
			}
			return emit("R44", "validated checkpoint and journal; same logical turn", restored()...)
		}
	case ReconcileOK:
		if _, ok := p.(WaitingReconciliation); ok {
			j := journal(v.Request)
			if j != nil && j.Status == Unknown && v.VerifiedResult != nil {
				j.Status = Completed
				j.Result = *v.VerifiedResult
				if unknown() {
					return emit("R45", "more unknown outcomes remain")
				}
				return emit("R46", "host verified durable reconciliation success; no redispatch", restored()...)
			}
		}
	case Complete:
		if p, ok := p.(Running); ok && p.Tool != InFlight {
			s.Phase = Parked{current, Checkpoint}
			return emit("R24", "turn result waits for atomic checkpoint", CommitCheckpoint{[]string{"modelContext", "artifacts", "pruning", "transcriptCursor", "turnResult"}})
		}
	case CheckpointOK:
		if p, ok := p.(Parked); ok && p.Reason == Checkpoint {
			s.Checkpoint++
			s.Settled = append(s.Settled, current)
			s.Phase = Idle{}
			if len(s.Queue) > 0 {
				id := s.Queue[0]
				s.Queue = s.Queue[1:]
				s.Attempt++
				s.Phase = Running{id, NoTool}
				return emit("R25", "durable completed-turn checkpoint releases one queued turn", Run{id, s.Attempt, s.Epoch})
			}
			return emit("R25", "durable completed-turn checkpoint releases one queued turn")
		}
	case ToolRequest:
		if p, ok := p.(Running); ok && p.Tool == NoTool {
			if v.Request == "" {
				return ignore("R47", "tool request identity required")
			}
			if j := journal(v.Request); j != nil {
				if j.Status == Completed {
					return emit("R48", "reuse known result; never redispatch", ReuseToolResult{v.Request, j.Result})
				}
				return ignore("R49", "request already dispatched or unresolved")
			}
			if s.Access != Granted {
				return ignore("R26", "ACL denies regardless of policy")
			}
			switch s.Policy {
			case Wait:
				s.Phase = WaitingApproval{current, v.Request}
				return emit("R27", "Wait policy gates tool", ShowApproval{})
			case DenyContinue:
				return emit("R28", "deny tool and continue reasoning", ToolDenied{})
			case StopPolicy:
				s.Phase = Stopping{current}
				s.Epoch++
				return emit("R29", "Stop policy ends turn", CancelTree{})
			case AllowAll:
				return emit("R30", "AllowAll; enabled tools and current ACL checked by broker", dispatch(v.Request))
			}
		}
	case Approve:
		if p, ok := p.(WaitingApproval); ok && p.Request == v.Request && s.Access == Granted {
			return emit("R31", "matching host approval plus current authorization", dispatch(v.Request))
		}
	case Deny:
		if p, ok := p.(WaitingApproval); ok && p.Request == v.Request {
			s.Phase = Running{current, NoTool}
			return emit("R32", "deny one request without granting anything", ToolDenied{})
		}
	case ToolResult:
		if p, ok := p.(Running); ok && p.Tool == InFlight {
			j := journal(v.Request)
			if j == nil || j.Status != InFlight || j.Attempt != v.Attempt {
				return ignore("R50", "result does not match current dispatched request")
			}
			j.Status = Completed
			j.Result = v.Result
			s.Phase = Running{current, NoTool}
			return emit("R33", "matching tool result")
		}
	case Ask:
		if p, ok := p.(Running); ok && p.Tool == NoTool && v.ID != "" {
			s.Phase = WaitingAnswer{current, v.ID}
			return emit("R34", "question suspends turn", ShowQuestion{})
		}
	case Answer:
		if p, ok := p.(WaitingAnswer); ok && p.Question == v.ID {
			s.Phase = Running{current, NoTool}
			return emit("R35", "matching answer is not queued input", Continue{})
		}
	case Compact:
		if p, ok := p.(Running); ok && p.Tool == NoTool {
			s.Phase = Compacting{current}
			return emit("R36", "safe context boundary; transcript unchanged", CompactSanitizedContext{})
		}
	case CompactOK:
		if _, ok := p.(Compacting); ok {
			s.Phase = Running{current, NoTool}
			return emit("R37", "compaction candidate remains inside pending checkpoint")
		}
	case CompactFailed:
		if _, ok := p.(Compacting); ok {
			s.Phase = Parked{current, CompactionFailed}
			return emit("R38", "retain prior checkpoint; no truncation or queue drain", NotifyCompactionFailure{})
		}
	case Stopped:
		if _, ok := p.(Stopping); ok {
			s.Phase = Parked{current, StoppedReason}
			return emit("R39", "cancel acknowledgement parks queue", RecordPartialTurn{})
		}
	case Timeout:
		if active(p) {
			interrupt("Timeout")
			return emit("R40", "bounded wait or cancellation timeout", CancelTree{})
		}
	}
	return ignore("R00", e.tag()+" not applicable in "+p.tag()+"; explicit ignored pair")
}

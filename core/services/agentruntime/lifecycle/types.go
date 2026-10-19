// SPDX-License-Identifier: MIT
// Package lifecycle adapts documentation rules R00–R50 one for one.
// It is a deterministic, bounded conversation model, not a storage implementation.
// Effects describe committed transactions; a host must commit CAS before running
// them. R04 is a local fail-closed latch, not a claim of durable storage success.
// AcquireLease and Restart are trusted host facts after external ownership CAS.
// ReconcileOK is a host-verified result, never a user acknowledgement of uncertainty.
// Queue capacity two is the documentation abstraction, not a product limit.
// Schedule and goal machines and their six assertions belong to Task 10.
package lifecycle

import (
	"fmt"
	"slices"
)

type phase interface{ tag() string }
type event interface {
	tag() string
	identity() Identity
}
type effect interface{ tag() string }

// Event is sealed by package-private methods; callers construct concrete payloads.
type Event = event
type Effect = effect
type Phase = phase

// Identity carries abstract admission CAS, lease fencing, and callback identity.
type Identity struct {
	Principal           string
	Rev, Epoch, Attempt uint64
	Turn                string
}

func (i Identity) identity() Identity { return i }

type Policy string

const (
	Wait         Policy = "Wait"
	DenyContinue Policy = "DenyContinue"
	StopPolicy   Policy = "Stop"
	AllowAll     Policy = "AllowAll"
)

type ToolStatus string

const (
	NoTool    ToolStatus = "None"
	InFlight  ToolStatus = "InFlight"
	Unknown   ToolStatus = "Unknown"
	Completed ToolStatus = "Completed"
)

type ParkReason string

const (
	Checkpoint       ParkReason = "Checkpoint"
	StoppedReason    ParkReason = "Stopped"
	CompactionFailed ParkReason = "CompactionFailed"
)

type Lease string

const (
	Owned Lease = "Owned"
	Lost  Lease = "Lost"
)

type Access string

const (
	Granted Access = "Granted"
	Revoked Access = "Revoked"
)

type Connection string

const (
	Attached Connection = "Attached"
	Detached Connection = "Detached"
)

type ToolAttempt struct {
	Turn, Request string
	Attempt       uint64
	Status        ToolStatus
	Result        string
}
type State struct {
	Principal                string
	Rev, Epoch               uint64
	Lease                    Lease
	Phase                    Phase
	Queue, Accepted, Settled []string
	Policy                   Policy
	Access                   Access
	Attempt                  uint64
	Journal                  []ToolAttempt
	Checkpoint               uint64
	Connection               Connection
}
type Transition struct {
	State       State
	Effects     []Effect
	Rule, Guard string
}

func NewState(principal string) State {
	return State{Principal: principal, Epoch: 1, Lease: Owned, Phase: Idle{}, Policy: Wait, Access: Granted, Connection: Attached}
}

type Idle struct{}

func (Idle) tag() string { return "Idle" }

type Running struct {
	Turn string
	Tool ToolStatus
}

func (Running) tag() string { return "Running" }

type WaitingApproval struct{ Turn, Request string }

func (WaitingApproval) tag() string { return "WaitingApproval" }

type WaitingAnswer struct{ Turn, Question string }

func (WaitingAnswer) tag() string { return "WaitingAnswer" }

type Compacting struct{ Turn string }

func (Compacting) tag() string { return "Compacting" }

type Parked struct {
	Turn   string
	Reason ParkReason
}

func (Parked) tag() string { return "Parked" }

type Stopping struct{ Turn string }

func (Stopping) tag() string { return "Stopping" }

type Interrupted struct {
	Turn, Reason string
	Tool         ToolStatus
}

func (Interrupted) tag() string { return "Interrupted" }

type Restoring struct{ Turn string }

func (Restoring) tag() string { return "Restoring" }

type WaitingReconciliation struct{ Turn string }

func (WaitingReconciliation) tag() string { return "WaitingReconciliation" }

type Deleted struct{}

func (Deleted) tag() string { return "Deleted" }

type Submit struct {
	Identity
	ID string
}

func (Submit) tag() string { return "Submit" }

type Complete struct{ Identity }

func (Complete) tag() string { return "Complete" }

type CheckpointOK struct{ Identity }

func (CheckpointOK) tag() string { return "CheckpointOK" }

type CheckpointFailed struct{ Identity }

func (CheckpointFailed) tag() string { return "CheckpointFailed" }

type StorageFailed struct{ Identity }

func (StorageFailed) tag() string { return "StorageFailed" }

type ToolRequest struct {
	Identity
	Request string
}

func (ToolRequest) tag() string { return "ToolRequest" }

type ToolResult struct {
	Identity
	Request string
	Result  string
}

func (ToolResult) tag() string { return "ToolResult" }

type Approve struct {
	Identity
	Request string
}

func (Approve) tag() string { return "Approve" }

type Deny struct {
	Identity
	Request string
}

func (Deny) tag() string { return "Deny" }

type Ask struct {
	Identity
	ID string
}

func (Ask) tag() string { return "Ask" }

type Answer struct {
	Identity
	ID string
}

func (Answer) tag() string { return "Answer" }

type Compact struct{ Identity }

func (Compact) tag() string { return "Compact" }

type CompactOK struct{ Identity }

func (CompactOK) tag() string { return "CompactOK" }

type CompactFailed struct{ Identity }

func (CompactFailed) tag() string { return "CompactFailed" }

type Stop struct{ Identity }

func (Stop) tag() string { return "Stop" }

type Stopped struct{ Identity }

func (Stopped) tag() string { return "Stopped" }

type Delete struct{ Identity }

func (Delete) tag() string { return "Delete" }

type Restart struct{ Identity }

func (Restart) tag() string { return "Restart" }

type Resume struct{ Identity }

func (Resume) tag() string { return "Resume" }

type Revoke struct{ Identity }

func (Revoke) tag() string { return "Revoke" }

type LeaseLost struct{ Identity }

func (LeaseLost) tag() string { return "LeaseLost" }

type AcquireLease struct{ Identity }

func (AcquireLease) tag() string { return "AcquireLease" }

type Disconnect struct{ Identity }

func (Disconnect) tag() string { return "Disconnect" }

type Reconnect struct{ Identity }

func (Reconnect) tag() string { return "Reconnect" }

type Timeout struct{ Identity }

func (Timeout) tag() string { return "Timeout" }

type RestoreOK struct{ Identity }

func (RestoreOK) tag() string { return "RestoreOK" }

type RestoreFailed struct{ Identity }

func (RestoreFailed) tag() string { return "RestoreFailed" }

type ReconcileOK struct {
	Identity
	Request        string
	VerifiedResult *string
}

func (ReconcileOK) tag() string { return "ReconcileOK" }

type Run struct {
	Turn           string
	Attempt, Epoch uint64
}

func (Run) tag() string { return "Run" }

type ReplaySanitizedEvents struct{}

func (ReplaySanitizedEvents) tag() string { return "ReplaySanitizedEvents" }

type CancelTree struct{}

func (CancelTree) tag() string { return "CancelTree" }

type CloseAdmission struct{}

func (CloseAdmission) tag() string { return "CloseAdmission" }

type RevokeArtifactAccess struct{}

func (RevokeArtifactAccess) tag() string { return "RevokeArtifactAccess" }

type PurgeTaintedContext struct{}

func (PurgeTaintedContext) tag() string { return "PurgeTaintedContext" }

type LoadCheckpointAndToolJournal struct {
	Turn    string
	Attempt uint64
}

func (LoadCheckpointAndToolJournal) tag() string { return "LoadCheckpointAndToolJournal" }

type NotifyRestoreFailure struct{}

func (NotifyRestoreFailure) tag() string { return "NotifyRestoreFailure" }

type ShowReconciliation struct{}

func (ShowReconciliation) tag() string { return "ShowReconciliation" }

type ReuseRecordedResults struct{ Results []ToolAttempt }

func (ReuseRecordedResults) tag() string { return "ReuseRecordedResults" }

type ContinueRestoredTurn struct {
	Turn    string
	Attempt uint64
}

func (ContinueRestoredTurn) tag() string { return "ContinueRestoredTurn" }

type CommitCheckpoint struct{ Fields []string }

func (CommitCheckpoint) tag() string { return "CommitCheckpoint" }

type ReuseToolResult struct{ Request, Result string }

func (ReuseToolResult) tag() string { return "ReuseToolResult" }

type ShowApproval struct{}

func (ShowApproval) tag() string { return "ShowApproval" }

type ToolDenied struct{}

func (ToolDenied) tag() string { return "ToolDenied" }

type AuthorizedTool struct {
	Turn, Request  string
	Attempt, Epoch uint64
}

func (AuthorizedTool) tag() string { return "AuthorizedTool" }

type ShowQuestion struct{}

func (ShowQuestion) tag() string { return "ShowQuestion" }

type Continue struct{}

func (Continue) tag() string { return "Continue" }

type CompactSanitizedContext struct{}

func (CompactSanitizedContext) tag() string { return "CompactSanitizedContext" }

type NotifyCompactionFailure struct{}

func (NotifyCompactionFailure) tag() string { return "NotifyCompactionFailure" }

type RecordPartialTurn struct{}

func (RecordPartialTurn) tag() string { return "RecordPartialTurn" }
func turn(p Phase) string {
	switch p := p.(type) {
	case Running:
		return p.Turn
	case WaitingApproval:
		return p.Turn
	case WaitingAnswer:
		return p.Turn
	case Compacting:
		return p.Turn
	case Parked:
		return p.Turn
	case Stopping:
		return p.Turn
	case Interrupted:
		return p.Turn
	case Restoring:
		return p.Turn
	case WaitingReconciliation:
		return p.Turn
	}
	return ""
}
func active(p Phase) bool {
	switch p.(type) {
	case Running, WaitingApproval, WaitingAnswer, Compacting, Stopping, Restoring, WaitingReconciliation:
		return true
	}
	return false
}
func validate(s State) error {
	bad := func() error { return fmt.Errorf("invalid lifecycle state") }
	if s.Phase == nil || s.Principal == "" {
		return bad()
	}
	switch p := s.Phase.(type) {
	case Idle:
		if len(s.Queue) > 0 {
			return bad()
		}
	case Deleted:
		if len(s.Queue) > 0 {
			return bad()
		}
	case Interrupted:
		if p.Tool != NoTool && p.Tool != Unknown {
			return bad()
		}
	case Running:
		if p.Tool != NoTool && p.Tool != InFlight {
			return bad()
		}
	case Parked:
		if p.Reason != Checkpoint && p.Reason != StoppedReason && p.Reason != CompactionFailed {
			return bad()
		}
	case WaitingApproval:
		if p.Request == "" {
			return bad()
		}
	case WaitingAnswer:
		if p.Question == "" {
			return bad()
		}
	case Compacting, Stopping, Restoring, WaitingReconciliation:
	default:
		return bad()
	}
	if s.Phase.tag() != "Idle" && s.Phase.tag() != "Deleted" && s.Phase.tag() != "Interrupted" && turn(s.Phase) == "" {
		return bad()
	}
	if s.Lease != Owned && s.Lease != Lost {
		return bad()
	}
	if s.Lease == Lost && active(s.Phase) {
		return bad()
	}
	if s.Policy != Wait && s.Policy != DenyContinue && s.Policy != StopPolicy && s.Policy != AllowAll {
		return bad()
	}
	if s.Access != Granted && s.Access != Revoked {
		return bad()
	}
	if s.Connection != Attached && s.Connection != Detached {
		return bad()
	}
	if len(s.Queue) > 2 {
		return bad()
	}
	for _, ids := range [][]string{s.Queue, s.Accepted, s.Settled} {
		seen := map[string]bool{}
		for _, id := range ids {
			if id == "" || seen[id] {
				return bad()
			}
			seen[id] = true
		}
	}
	for _, id := range s.Queue {
		if id == turn(s.Phase) || slices.Contains(s.Settled, id) || !slices.Contains(s.Accepted, id) {
			return bad()
		}
	}
	if turn(s.Phase) != "" && (slices.Contains(s.Settled, turn(s.Phase)) || !slices.Contains(s.Accepted, turn(s.Phase))) {
		return bad()
	}
	seen := map[[2]string]bool{}
	for _, j := range s.Journal {
		key := [2]string{j.Turn, j.Request}
		if j.Turn == "" || j.Request == "" || seen[key] || j.Attempt == 0 || j.Attempt > s.Attempt {
			return bad()
		}
		seen[key] = true
		if j.Status != InFlight && j.Status != Unknown && j.Status != Completed {
			return bad()
		}
		if _, ok := s.Phase.(Running); ok && j.Turn == turn(s.Phase) && j.Status == Unknown {
			return bad()
		}
	}
	return nil
}

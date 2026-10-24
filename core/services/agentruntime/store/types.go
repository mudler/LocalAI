// SPDX-License-Identifier: MIT

// Package store persists logical runtime records, independently of the lifecycle
// snapshot codec and UI transcript. It does not authorize checkpoint readers,
// sanitize events, deliver the outbox, acquire leases, or journal tool calls.
// Callers must supply an authenticated principal and already-sanitized events.
// State mode is stateless per request: database transactions are the authority,
// with no process-local cache or coordination mutex.
// Persisted unsigned counters and checkpoint versions have domain [0, MaxInt64],
// the common signed INTEGER/BIGINT range of SQLite and PostgreSQL. Go uint64
// fields do not imply unsigned database storage. Counter exhaustion rejects the
// mutation with ErrConflict (no wrapping); read-only dedup remains available.
// Commit compares expectedRevision in Go after scoped lookup, so values outside
// that domain conflict without being bound to SQL. Caller Event.Sequence is
// ignored. Events cursors >= MaxInt64 yield empty pages only after validation
// and scoped live-conversation lookup.
// ApplySettlement is a trusted transaction participant, not an authority API.
// Runtime hosts use ownership.Manager.Settle; legacy Commit stays unfenced and
// retains whole-transaction transient retries without revision rebasing. Hold
// persists a waiting outcome, not a new durable lifecycle or recovery phase.
// SQLite is tested; PostgreSQL/distributed acceptance remains a separate gate.
package store

import "errors"

var (
	ErrNotFound     = errors.New("runtime conversation not found")
	ErrDeleted      = errors.New("runtime conversation deleted")
	ErrConflict     = errors.New("runtime revision, turn or input payload conflict")
	ErrInvalidInput = errors.New("invalid runtime input")
)

// Identities are opaque, case-sensitive strings of at most 256 bytes. Whitespace
// only identities are rejected, never mapped to an anonymous owner.
const MaxIdentityBytes = 256
const MaxEventPage = 1000

type Admission struct {
	InputID     string
	RunningTurn string
	Queued      bool
	Revision    uint64
}

// Input IDs are conversation-local logical turn IDs, not execution attempts.
// Ordinal and PayloadHash are assigned at admission and retained after settlement.
type Input struct {
	InputID     string
	Payload     []byte
	PayloadHash []byte
	Ordinal     uint64
}

// Checkpoint fields are opaque and independent of the public UI transcript.
// Version describes the caller's context codec, not the conversation revision.
// Values above MaxInt64 are ErrInvalidInput before SQL; MaxInt64 is valid.
type Checkpoint struct {
	Version          uint64
	Context          []byte
	Artifacts        []byte
	Pruning          []byte
	TranscriptCursor []byte
}

type Snapshot struct {
	Revision   uint64
	Running    *Input
	Pending    []Input
	Checkpoint Checkpoint
	Deleted    bool
}

// Event.Sequence is server assigned. Commit binds TurnID to the settling turn;
// an empty TurnID is filled in, a mismatched TurnID is rejected. Payload must
// already have been sanitized by the authorized caller.
type Event struct {
	Sequence uint64
	TurnID   string
	Kind     string
	Payload  []byte
}

type Settlement struct {
	Revision uint64
	Next     *Input
}

// SettlementMode selects whether settlement completes or retains the active turn.
type SettlementMode uint8

const (
	CompleteTurn SettlementMode = iota + 1
	HoldTurn
)

// SettlementWrite is trusted host input. Events must already be sanitized and
// checkpoint bytes must already be sealed. Event.Sequence is ignored.
type SettlementWrite struct {
	ExpectedRevision uint64
	TurnID           string
	Checkpoint       Checkpoint
	Events           []Event
	Mode             SettlementMode
}

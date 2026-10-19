// SPDX-License-Identifier: MIT
// Package ownership provides database-backed leases and a tool intent journal.
// All timestamps must come from a trusted host UTC clock, never model/browser
// input. This package fences its own transactions, not the separate store APIs;
// the runner must unify their transaction boundary before integration. SQLite
// behavior is tested; PostgreSQL and multi-node clock authority are later gates.
// No external side-effect exactly-once guarantee is made: Unknown needs evidence,
// not replay. Reconcile must only be exposed to host-verified evidence admission.
package ownership

import (
	"errors"
	"time"
)

var (
	ErrNotFound     = errors.New("ownership: not found")
	ErrLeaseLost    = errors.New("ownership: lease lost")
	ErrConflict     = errors.New("ownership: conflict")
	ErrInvalidInput = errors.New("ownership: invalid input")
)

type Key struct{ Principal, Conversation string }
type Lease struct {
	Key
	Owner     string
	Epoch     uint64
	ExpiresAt time.Time
}
type ToolCall struct {
	TurnID, RequestID, ArgumentsHash string
	Attempt                          uint64
}
type ToolStatus string

const (
	Prepared   ToolStatus = "prepared"
	Dispatched ToolStatus = "dispatched"
	Completed  ToolStatus = "completed"
	Unknown    ToolStatus = "unknown"
)

type ToolRecord struct {
	Call         ToolCall
	Status       ToolStatus
	Result       []byte
	ResolutionID string
}

// Times must be UTC in the positive signed Unix-nanosecond domain (through
// 2262-04-11); grants that overflow that domain are rejected.
// MaxTTL limits individual grants. Epochs and attempts use the positive signed
// 64-bit database domain. Identifiers/hashes are opaque, nonblank, <=512 bytes.
const MaxTTL = 24 * time.Hour

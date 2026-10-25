// Package store keeps what the studio loop needs between requests: chat
// records and transcripts (the session.Store contract) plus the messages a user
// sent while a turn ran, and the claim that lets one turn run per chat.
//
// Two implementations exist: NewJSON (files under a data path, or memory when
// the path is empty) and NewGORM (SQLite or PostgreSQL). storetest.Run checks
// both.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/session"
)

// Errors. A chat that does not exist, or belongs to another principal, is
// session.ErrNotFound for every method.
var (
	// ErrDelivered means the pending message was already taken for delivery.
	ErrDelivered = errors.New("studio store: the message was already delivered")
	// ErrTurnRunning means another turn holds the chat.
	ErrTurnRunning = errors.New("studio store: a turn is already running")
	// ErrNotClaimed means the run ID does not hold the chat.
	ErrNotClaimed = errors.New("studio store: the run does not hold the chat")
)

// Pending is a message the user sent while a turn ran.
type Pending struct {
	ID        string
	Text      string
	Seq       int64 // send order within the chat
	Delivered bool
	At        time.Time
}

// TurnStatus is how a turn ended.
type TurnStatus string

// Turn outcomes.
const (
	TurnDone        TurnStatus = "done"
	TurnStopped     TurnStatus = "stopped"
	TurnFailed      TurnStatus = "failed"
	TurnInterrupted TurnStatus = "interrupted"
)

// TurnInfo is the turn state of a chat.
type TurnInfo struct {
	Running bool
	RunID   string
	Since   time.Time  // when the running turn was claimed
	Last    TurnStatus // outcome of the last finished turn; empty before the first
}

// Store is the studio loop's storage.
type Store interface {
	session.Store

	// AddPending queues text as the next message, after the pending ones.
	AddPending(ctx context.Context, principal, chatID, text string) (Pending, error)
	// Pending returns the messages not yet delivered, in send order.
	Pending(ctx context.Context, principal, chatID string) ([]Pending, error)
	// EditPending changes the text of an undelivered message.
	EditPending(ctx context.Context, principal, chatID, pendingID, text string) error
	// CancelPending removes an undelivered message.
	CancelPending(ctx context.Context, principal, chatID, pendingID string) error
	// TakePending marks every undelivered message delivered and returns them in
	// send order. A message is returned by one call only, however many callers
	// race.
	TakePending(ctx context.Context, principal, chatID string) ([]Pending, error)
	// RestorePending makes the named messages undelivered again, in their
	// original positions. The loop calls it when a model request failed.
	RestorePending(ctx context.Context, principal, chatID string, ids []string) error

	// ClaimTurn gives runID the right to run a turn on the chat.
	ClaimTurn(ctx context.Context, principal, chatID, runID string) error
	// ReleaseTurn ends the claim and records how the turn ended.
	ReleaseTurn(ctx context.Context, principal, chatID, runID string, status TurnStatus) error
	// Turn returns the turn state of the chat.
	Turn(ctx context.Context, principal, chatID string) (TurnInfo, error)
}

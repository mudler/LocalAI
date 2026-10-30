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
	// Unconfirmed is true from the moment a request took the message until the
	// host confirms the model has it recorded. A turn that is reaped gives such
	// a message back.
	Unconfirmed bool
	At          time.Time
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

// ReapedTurn is a turn that ReapTurns ended.
type ReapedTurn struct {
	Principal, ChatID, RunID string
}

// TurnInfo is the turn state of a chat.
type TurnInfo struct {
	Running bool
	RunID   string
	Since   time.Time  // when the running turn was claimed
	Beat    time.Time  // the last sign of life of the running turn
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
	// ConfirmPending says the named messages are recorded in the transcript, so
	// a reaped turn no longer gives them back.
	ConfirmPending(ctx context.Context, principal, chatID string, ids []string) error
	// RestorePending makes the named messages undelivered again, in their
	// original positions. The loop calls it when a model request failed.
	RestorePending(ctx context.Context, principal, chatID string, ids []string) error

	// SetSkillsOff sets the skills this chat has switched off. It is not part
	// of UpdateChat: the loop saves its own copy of the record, which has no
	// switches in it. ChatRecord.SkillsOff reads them back.
	SetSkillsOff(ctx context.Context, principal, chatID string, off []string) error

	// SetAddonsOff sets the add-ons this chat has switched off, the way
	// SetSkillsOff does for skills.
	SetAddonsOff(ctx context.Context, principal, chatID string, off []string) error

	// SetMcpOff sets the MCP servers and connectors this chat has switched off,
	// the way SetAddonsOff does for add-ons.
	SetMcpOff(ctx context.Context, principal, chatID string, off []string) error

	// SetBuiltinsOff sets the groups of built-in tools this chat has switched
	// off, the way SetAddonsOff does for add-ons.
	SetBuiltinsOff(ctx context.Context, principal, chatID string, off []string) error

	// ListChildren returns the sub-agent chats of a parent chat, oldest first.
	ListChildren(ctx context.Context, principal, parentID string) ([]session.ChatRecord, error)

	// SetChatMeta changes the title, the model and the settings of a chat. It
	// does not touch Updated or the transcript. The caller makes sure no turn
	// runs when it changes Title or Model, because the loop saves its own copy
	// of the record at the end of a turn.
	SetChatMeta(ctx context.Context, principal, chatID string, m session.ChatMeta) error

	// TruncateEntries removes the transcript entries after seq keep, so the
	// next entry gets Seq keep+1. It is for regenerate and edit-and-resend;
	// the caller holds the turn claim.
	TruncateEntries(ctx context.Context, principal, chatID string, keep int64) error

	// ClaimTurn gives runID the right to run a turn on the chat.
	ClaimTurn(ctx context.Context, principal, chatID, runID string) error
	// ReleaseTurn ends the claim and records how the turn ended.
	ReleaseTurn(ctx context.Context, principal, chatID, runID string, status TurnStatus) error
	// TouchTurn records that the run holding the chat is still alive. The
	// frontend that runs a turn calls it on a timer.
	TouchTurn(ctx context.Context, principal, chatID, runID string) error
	// ReapTurns ends every running turn whose last sign of life is before
	// olderThan: the chat is free again, its last outcome is interrupted, and
	// the messages a request took but nobody confirmed are undelivered again. A
	// frontend that died leaves such turns behind. It returns the turns it ended.
	ReapTurns(ctx context.Context, olderThan time.Time) ([]ReapedTurn, error)
	// Turn returns the turn state of the chat.
	Turn(ctx context.Context, principal, chatID string) (TurnInfo, error)
}

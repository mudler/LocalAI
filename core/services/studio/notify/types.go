// Package notify keeps the notifications of the chats: one row for each thing
// that ended while its owner was elsewhere, such as a trigger run that finished
// or failed. A notification belongs to one user.
//
// Two stores exist: NewJSON (a file under a data directory, or memory) and
// NewGORM (SQLite or PostgreSQL). notifytest.Run checks both. The Service adds
// live delivery on top: every frontend hears every new notification, so a
// client connected to one frontend sees what another frontend stored.
package notify

import (
	"context"
	"errors"
	"time"
)

// Kinds of notification.
const (
	KindRunEnded       = "run_ended"       // a run finished
	KindRunFailed      = "run_failed"      // a run failed or was skipped
	KindNeedsAttention = "needs_attention" // something needs the owner, such as a trigger switched off
)

// Sources of a notification: what sent it. The inbox draws a different icon
// for each one. A row stored before sources existed has none.
const (
	SourceTrigger   = "trigger"   // a trigger and its runs
	SourceConnector = "connector" // a connector, such as a mail or chat account
	SourceSystem    = "system"    // LocalAI itself, such as the conversion of old data
)

// Statuses of a notification.
const (
	StatusOK     = "ok"
	StatusFailed = "failed"
)

// Limits. A user keeps at most MaxPerUser notifications, none older than MaxAge
// when the newest one was added.
const (
	MaxPerUser = 500
	MaxAge     = 90 * 24 * time.Hour

	MaxTitleRunes = 140
	MaxBodyRunes  = 320
	MaxLinkBytes  = 300
)

// ErrNotFound means a push subscription does not exist.
var ErrNotFound = errors.New("notify: not found")

// Notification is one row of the inbox.
type Notification struct {
	ID        int64  `json:"id"`
	Principal string `json:"-"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	// Source is what sent it: SourceTrigger, SourceConnector or SourceSystem.
	Source string `json:"source,omitempty"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	// Link is the in-app path that opens the thing, for example /app/chats/<id>.
	Link    string    `json:"link"`
	Read    bool      `json:"read"`
	Created time.Time `json:"created"`
	// Op is set only on a live signal that the inbox changed (OpChanged), not
	// on a stored row. Clients that hear it read the list again.
	Op string `json:"op,omitempty"`
}

// OpChanged is the live signal that notifications were read or removed.
const OpChanged = "changed"

// Store is the storage of notifications.
type Store interface {
	// Add stores n (its ID is ignored), gives it an ID that grows with each
	// call, and trims the principal's rows to MaxPerUser and to MaxAge counted
	// back from n.Created, in the same step.
	Add(ctx context.Context, n Notification) (int64, error)
	// List returns the principal's notifications, newest first. A limit of zero
	// or less means no limit.
	List(ctx context.Context, principal string, unreadOnly bool, limit int) ([]Notification, error)
	// Count returns the number of unread notifications.
	Count(ctx context.Context, principal string) (int, error)
	// MarkRead marks the named notifications read. Another user's ids are ignored.
	MarkRead(ctx context.Context, principal string, ids []int64) error
	// MarkChatRead marks every notification with this link read.
	MarkChatRead(ctx context.Context, principal, link string) error
	MarkAllRead(ctx context.Context, principal string) error
	// Delete removes the named notifications. Another user's ids are ignored.
	Delete(ctx context.Context, principal string, ids []int64) error
	// DeleteAll removes every notification of the principal, or with readOnly
	// only the read ones. It returns how many it removed.
	DeleteAll(ctx context.Context, principal string, readOnly bool) (int, error)
}

func cutRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// Clean cuts a notification's text to its limits.
func Clean(n Notification) Notification {
	n.Title = cutRunes(n.Title, MaxTitleRunes)
	n.Body = cutRunes(n.Body, MaxBodyRunes)
	if len(n.Link) > MaxLinkBytes {
		n.Link = ""
	}
	return n
}

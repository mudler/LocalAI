package triggers

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned by a Store for a trigger or run that does not exist.
var ErrNotFound = errors.New("triggers: not found")

// Retention says which finished runs Store.PruneRuns removes. A run that is
// queued or running is never removed.
type Retention struct {
	// Keep is the number of newest finished runs (by claim order) that are
	// kept. Zero or less sets no count limit.
	Keep int
	// Protect keeps every finished run that is due at or after this time, even
	// beyond Keep. The daily limit counts the runs of the current day, so those
	// must stay. The zero time protects nothing.
	Protect time.Time
	// Expire removes every finished run that is due before this time, whatever
	// Keep and Protect say. The zero time sets no age limit.
	Expire time.Time
}

// protects tells whether a finished run due at due stays beyond Keep.
func (p Retention) protects(due time.Time) bool {
	return !p.Protect.IsZero() && !due.Before(p.Protect)
}

// expires tells whether a finished run due at due is removed for its age.
func (p Retention) expires(due time.Time) bool {
	return !p.Expire.IsZero() && due.Before(p.Expire)
}

// Store keeps triggers, runs and the state of a Runner. A Store is safe for
// concurrent use. triggerstest.RunStoreSuite checks every method.
type Store interface {
	// PutTrigger creates the trigger or replaces the one with the same ID.
	PutTrigger(ctx context.Context, t Trigger) error
	// SetOutcome sets Outcome and OutcomeAt of the trigger id to to and at,
	// only while its Outcome is from, and reports whether it did. It is the
	// one write that decides an answer to a change record: two frontends that
	// answer the same record at once get true once.
	SetOutcome(ctx context.Context, id, from, to string, at time.Time) (bool, error)
	// DeleteTrigger removes the trigger with its runs and its last due time.
	// It is not an error when the trigger does not exist.
	DeleteTrigger(ctx context.Context, id string) error
	// GetTrigger returns ErrNotFound for an unknown ID.
	GetTrigger(ctx context.Context, id string) (Trigger, error)
	// ListTriggers returns the triggers of one owner, sorted by name and then ID.
	ListTriggers(ctx context.Context, owner string) ([]Trigger, error)
	// EnabledTriggers returns every enabled trigger of every owner, sorted by ID.
	EnabledTriggers(ctx context.Context) ([]Trigger, error)
	// AllTriggers returns every trigger of every owner, sorted by owner, name
	// and ID. It is for the administrator's view and for migrations.
	AllTriggers(ctx context.Context) ([]Trigger, error)

	// Claim stores a new run. It returns false, and stores nothing, when a run
	// with the same TriggerID and DueKey exists, whatever its ID. It returns an
	// error, and stores nothing, when only the ID is taken.
	// Two concurrent calls with the same key return true once.
	Claim(ctx context.Context, r Run) (bool, error)
	// GetRun returns ErrNotFound for an unknown ID.
	GetRun(ctx context.Context, id string) (Run, error)
	// SetRunStatus replaces Status, Reason, ChatID, Started and Ended of the
	// run with the ID of r. It returns ErrNotFound for an unknown ID.
	SetRunStatus(ctx context.Context, r Run) error
	// ListRuns returns the runs of a trigger, newest claim first, at most limit.
	// A limit of zero or less returns every run.
	ListRuns(ctx context.Context, triggerID string, limit int) ([]Run, error)
	// PruneRuns removes finished runs of a trigger as p says and returns how
	// many it removed. A run that is queued or running stays. The unique claim
	// of a removed run is gone too, so retention bounds how far back a due
	// time or file version is remembered.
	PruneRuns(ctx context.Context, triggerID string, p Retention) (int, error)
	// RunningRuns returns every run with the status running, oldest claim first.
	RunningRuns(ctx context.Context) ([]Run, error)
	// QueuedRuns returns every run with the status queued, oldest claim first.
	QueuedRuns(ctx context.Context) ([]Run, error)
	// RunByChat returns the run that started the chat, or ErrNotFound.
	RunByChat(ctx context.Context, chatID string) (Run, error)
	// CountRunsSince counts the runs of a trigger whose Due is not before since
	// and whose status is not skipped.
	CountRunsSince(ctx context.Context, triggerID string, since time.Time) (int, error)

	// Cursor returns the revision of the workspace change feed that the Runner
	// has handled, and false when none was saved.
	Cursor(ctx context.Context) (rev int64, ok bool, err error)
	SetCursor(ctx context.Context, rev int64) error
	// LastDue returns the last due time that a schedule trigger has handled,
	// or the zero time.
	LastDue(ctx context.Context, triggerID string) (time.Time, error)
	SetLastDue(ctx context.Context, triggerID string, t time.Time) error
}

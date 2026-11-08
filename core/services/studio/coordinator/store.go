package coordinator

import (
	"context"
	"time"
)

// Store keeps the configs and counters of the coordinators. Every method is
// safe to call from several frontends: the counters and the claims are single
// conditional updates, never read then write.
type Store interface {
	// Put saves a config. An existing row keeps ChatID, Paused, PausedBy,
	// PausedAt, NextWake, LastWake and Created.
	Put(ctx context.Context, c Config) error
	Get(ctx context.Context, agentID string) (Config, error)
	List(ctx context.Context) ([]Config, error)
	// Delete removes the config and its counters. The audit log stays.
	Delete(ctx context.Context, agentID string) error
	// SetChat stores the chat of the coordinator if none is stored and
	// returns the stored one: the first call wins.
	SetChat(ctx context.Context, agentID, chatID string) (string, error)
	// ReplaceChat stores newID as the chat of the coordinator only if the
	// stored chat is oldID (a chat that was deleted), and returns the stored
	// chat: the first caller wins, the others get its chat.
	ReplaceChat(ctx context.Context, agentID, oldID, newID string) (string, error)
	// SetPaused sets the pause. changed is false when the state was already
	// the asked one. A resume clears PausedBy and PausedAt.
	SetPaused(ctx context.Context, agentID string, paused bool, by string, at time.Time) (cfg Config, changed bool, err error)
	// ClaimWake succeeds only if NextWake <= now, and then sets NextWake=next
	// and LastWake=now.
	ClaimWake(ctx context.Context, agentID string, now, next time.Time) (bool, error)
	// Reserve counts one use in bucket if the count is below limit. It is
	// atomic: with limit n, exactly n callers get true.
	Reserve(ctx context.Context, agentID, bucket string, limit int) (bool, error)
	Used(ctx context.Context, agentID, bucket string) (int, error)

	// AddAudit appends an entry. A zero At means now; the ID is assigned.
	AddAudit(ctx context.Context, e AuditEntry) error
	// Audit returns the entries that match, newest first.
	Audit(ctx context.Context, q AuditQuery) ([]AuditEntry, error)
	// Prune drops the audit entries older than before and the counters whose
	// period ended before it (day and minute buckets).
	Prune(ctx context.Context, before time.Time) (int, error)

	// AddReport records a helper whose end must be reported to the
	// coordinator. It is idempotent on ChildID: a second call changes nothing,
	// not even a report that is done.
	AddReport(ctx context.Context, r Report) error
	// DueReports returns the reports that are not done and have no lease
	// running at now, oldest first, at most limit.
	DueReports(ctx context.Context, now time.Time, limit int) ([]Report, error)
	// ClaimReport takes the lease of a report until until. It succeeds for
	// exactly one caller while the lease runs, and never for a done or
	// unknown report.
	ClaimReport(ctx context.Context, childID string, now, until time.Time) (bool, error)
	// FinishReport marks the report delivered. An unknown report is not an
	// error.
	FinishReport(ctx context.Context, childID string, at time.Time) error
	// ReleaseReport drops the lease so the report is due at once. An unknown
	// report is not an error.
	ReleaseReport(ctx context.Context, childID string) error
	// GetReport returns one report, done or not, or ErrNotFound.
	GetReport(ctx context.Context, childID string) (Report, error)
	// PendingReports counts the reports to deliver into the chat parentChat
	// that are not done, claimed or not. A helper that has such a report
	// coming is not finished yet: its own report waits.
	PendingReports(ctx context.Context, parentChat string) (int, error)
	// PruneReports drops the reports that are done and the ones created
	// before before, and returns how many.
	PruneReports(ctx context.Context, before time.Time) (int, error)
}

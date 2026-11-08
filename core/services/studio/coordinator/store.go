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
}

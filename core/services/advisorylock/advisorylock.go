package advisorylock

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"
	"sync"

	"gorm.io/gorm"
)

// localLocks holds one buffered channel (capacity 1) per lock key, used as an
// in-process mutex for non-PostgreSQL dialects (SQLite). A SQLite auth DB is
// effectively single-process, so serializing guarded sections within this
// process is sufficient - we cannot and need not coordinate across processes
// the way a PostgreSQL advisory lock does.
var (
	localLocksMu sync.Mutex
	localLocks   = map[int64]chan struct{}{}
)

// localLockChan returns the per-key buffered channel, creating it on first use.
func localLockChan(key int64) chan struct{} {
	localLocksMu.Lock()
	defer localLocksMu.Unlock()
	ch, ok := localLocks[key]
	if !ok {
		ch = make(chan struct{}, 1)
		localLocks[key] = ch
	}
	return ch
}

// isPostgres reports whether the gorm dialect is PostgreSQL. Anything else
// (SQLite and any non-postgres dialect) uses the in-process fallback, because
// the pg_* advisory lock functions only exist on PostgreSQL.
func isPostgres(db *gorm.DB) bool {
	return strings.Contains(db.Dialector.Name(), "postgres")
}

// TryWithLockCtx attempts to acquire a lock and run fn without blocking.
// Returns (true, nil) if the lock was acquired and fn executed, (false, nil) if
// the lock was already held, or (false, error) on failure.
//
// On PostgreSQL it uses pg_try_advisory_lock (cross-process). On other dialects
// (SQLite) it uses a non-blocking in-process lock keyed by key.
func TryWithLockCtx(ctx context.Context, db *gorm.DB, key int64, fn func() error) (bool, error) {
	if !isPostgres(db) {
		ch := localLockChan(key)
		select {
		case ch <- struct{}{}:
			defer func() { <-ch }()
			return true, fn()
		default:
			return false, nil
		}
	}

	sqlDB, err := db.DB()
	if err != nil {
		return false, fmt.Errorf("get sql.DB: %w", err)
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return false, fmt.Errorf("advisory lock conn: %w", err)
	}
	defer conn.Close()

	var acquired bool
	if err := conn.QueryRowContext(ctx,
		"SELECT pg_try_advisory_lock($1)", key).Scan(&acquired); err != nil {
		return false, fmt.Errorf("pg_try_advisory_lock: %w", err)
	}
	if !acquired {
		return false, nil
	}
	// Always unlock, even if context is cancelled (use Background for cleanup)
	defer conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", key)

	return true, fn()
}

// KeyFromUUID converts a UUID (as [16]byte) to a lock key via FNV-1a hashing.
func KeyFromUUID(b [16]byte) int64 {
	h := fnv.New64a()
	h.Write(b[:])
	return int64(h.Sum64()>>1) | 0x100000000
}

// KeyFromString converts an arbitrary string to an advisory lock key via FNV-1a hashing.
func KeyFromString(s string) int64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return int64(h.Sum64()>>1) | 0x100000000
}

// WithLockCtx acquires a lock for key, runs fn, then releases it, respecting
// context cancellation. If ctx is cancelled while waiting for the lock, the
// function returns ctx.Err().
//
// On PostgreSQL it uses pg_advisory_lock (cross-process). On other dialects
// (SQLite) it falls back to a blocking in-process lock keyed by key, which is
// sufficient because a SQLite auth DB is effectively single-process.
func WithLockCtx(ctx context.Context, db *gorm.DB, key int64, fn func() error) error {
	if !isPostgres(db) {
		// Honor an already-cancelled context before attempting acquisition:
		// select picks a ready case at random, so without this an already-free
		// lock could be taken despite a cancelled ctx.
		if err := ctx.Err(); err != nil {
			return err
		}
		ch := localLockChan(key)
		select {
		case ch <- struct{}{}:
			defer func() { <-ch }()
			return fn()
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("advisorylock: getting sql.DB: %w", err)
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("advisorylock: getting connection: %w", err)
	}
	defer conn.Close()

	// Neutralize any deployment-wide lock_timeout on this dedicated connection.
	// Operators commonly set a short global lock_timeout (on the role or
	// database) to bound ordinary row-lock waits. Applied to the blocking
	// pg_advisory_lock below, it aborts the wait with SQLSTATE 55P03 and turns
	// LocalAI's intentional cross-replica "wait your turn, then re-check"
	// coordination into a hard error for the caller (e.g. a chat request that
	// just wanted to reuse a model another replica is loading). Let the Go
	// context be the single source of truth for how long we wait instead.
	if _, err := conn.ExecContext(ctx, "SET lock_timeout = 0"); err != nil {
		return fmt.Errorf("advisorylock: disabling lock_timeout: %w", err)
	}
	// Restore the session default before this pooled connection is reused.
	defer func() { _, _ = conn.ExecContext(context.Background(), "RESET lock_timeout") }()

	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
		return fmt.Errorf("advisorylock: acquiring lock %d: %w", key, err)
	}
	// Always release the lock, even if ctx is cancelled
	defer conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", key)

	return fn()
}

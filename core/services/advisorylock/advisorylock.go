package advisorylock

import (
	"context"
	"fmt"
	"hash/fnv"

	"gorm.io/gorm"
)

// TryWithLockCtx attempts to acquire a PostgreSQL advisory lock using the provided context.
// Returns (true, nil) if the lock was acquired and fn executed, (false, nil) if the lock
// was already held, or (false, error) on failure.
func TryWithLockCtx(ctx context.Context, db *gorm.DB, key int64, fn func() error) (bool, error) {
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

// WithLockCtx is like WithLock but respects context cancellation.
// If ctx is cancelled while waiting for the lock, the function returns ctx.Err().
func WithLockCtx(ctx context.Context, db *gorm.DB, key int64, fn func() error) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("advisorylock: getting sql.DB: %w", err)
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("advisorylock: getting connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
		return fmt.Errorf("advisorylock: acquiring lock %d: %w", key, err)
	}
	// Always release the lock, even if ctx is cancelled
	defer conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", key)

	return fn()
}
